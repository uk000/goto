/**
 * Copyright 2026 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package startup

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"goto/ctl"
	a2aserver "goto/pkg/ai/a2a/server"
	mcpclient "goto/pkg/ai/mcp/client"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/ai/registry"
	"goto/pkg/global"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/scripts"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgtdi/fswatcher"
)

var (
	configWatcher    fswatcher.Watcher
	a2aConfigs       = map[string]*ctl.A2A{}
	mcpConfigs       = map[string]*ctl.MCP{}
	httpProxyConfigs = map[string]*httpproxy.Proxy{}
	tcpProxyConfigs  = map[string]*tcpproxy.TCPProxy{}
	lock             = sync.Mutex{}
)

func Start() {
	loadStartupConfigs()
	//runStartupScript()
}

func Stop() {
	if configWatcher != nil && configWatcher.IsRunning() {
		configWatcher.Close()
	}
}

func loadStartupConfigs() {
	if len(global.ServerConfig.ConfigPaths) > 0 {
		loadConfigsFromPaths("")
		var err error
		for _, configPath := range global.ServerConfig.ConfigPaths {
			if configWatcher == nil {
				configWatcher, err = fswatcher.New(fswatcher.WithPath(configPath, fswatcher.WithDepth(fswatcher.WatchTopLevel)))
				if err != nil {
					log.Printf("Failed to set watch for config [%s] with error: %s\n", configPath, err.Error())
					log.Printf("Will load config without watching\n")
				} else {
					watchStartupConfig()
				}
			} else {
				configWatcher.AddPath(configPath, fswatcher.WithDepth(fswatcher.WatchNested))
			}
		}
	}
}

func watchStartupConfig() {
	changeChan := make(chan string, 10)
	go loadChanges(changeChan)
	go configWatcher.Watch(context.Background())
	go func() {
		for event := range configWatcher.Events() {
			removed := false
			for _, t := range event.Types {
				if t == fswatcher.EventRemove || t == fswatcher.EventRename {
					removed = true
				}
			}
			if removed {
				removeConfigs(event.Path)
			} else {
				log.Printf("Files changed in [%s]. Will reload with delay.", event.Path)
				changeChan <- event.Path
			}
		}
	}()
}

func removeConfigs(filePath string) {
	lock.Lock()
	defer lock.Unlock()
	if a2aConfigs[filePath] != nil {
		log.Printf("File removed: %s. Removing A2A configs.\n", filePath)
		clearA2A(a2aConfigs[filePath])
	}
	if mcpConfigs[filePath] != nil {
		log.Printf("File removed: %s. Removing MCP configs.\n", filePath)
		clearMCP(mcpConfigs[filePath])
	}
	if httpProxyConfigs[filePath] != nil {
		log.Printf("File removed: %s. Removing HTTP Proxy configs.\n", filePath)
		removeHTTPProxy(httpProxyConfigs[filePath])
	}
	if tcpProxyConfigs[filePath] != nil {
		log.Printf("File removed: %s. Removing TCP Proxy configs.\n", filePath)
		removeTCPProxy(tcpProxyConfigs[filePath])
	}
}

func loadConfigsFromPaths(filter string) {
	for _, configPath := range global.ServerConfig.ConfigPaths {
		log.Printf("Loading configs from path [%s]", configPath)
		files, err := os.ReadDir(configPath)
		if err != nil {
			log.Printf("Failed to read config path [%s] with error: %s\n", configPath, err.Error())
			continue
		}
		for _, file := range files {
			filePath := filepath.Join(configPath, file.Name())
			if filter != "" && !strings.Contains(filePath, filter) && !strings.Contains(filter, filePath) {
				log.Printf("Skipping unchanged file [%s]", filePath)
				continue
			}
			loadConfigFromFile(filePath)
		}
	}
}

func loadChangeLog(changeLog map[string]any) {
	for path := range changeLog {
		loadConfigsFromPaths(path)
	}
}

func loadChanges(changeChan chan string) {
	debounce := util.Debounce(5 * time.Second)
	lock := sync.Mutex{}
	changeLog := map[string]any{}
	for path := range changeChan {
		lock.Lock()
		changeLog[path] = 0
		lock.Unlock()
		debounce(func() {
			lock.Lock()
			loadChangeLog(changeLog)
			lock.Unlock()
		})
	}
}

func loadConfigFromFile(filePath string) {
	if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		log.Printf("Loading config from file: %s\n", filePath)
		loadConfig(filePath, ctl.LoadConfig(filePath))
	} else if strings.HasSuffix(filePath, ".sh") {
		log.Printf("Loading config from script: %s\n", filePath)
		loadConfigFromScript(filePath)
	} else {
		log.Printf("Ignoring non-config file: %s\n", filePath)
	}
}

func loadConfigFromScript(filePath string) {
	script := &scripts.Script{
		Name:     "GotoConfig",
		FilePath: filePath,
	}
	var buff bytes.Buffer
	w := bufio.NewWriter(&buff)
	script.RunWithStdIn(w)
	w.Flush()
	loadConfig(filePath, ctl.ParseConfig(buff.Bytes()))
}

func loadConfig(filePath string, config *ctl.GotoConfig) {
	if config == nil {
		log.Println("No config loaded")
		return
	}
	if config.MCP != nil {
		lock.Lock()
		mcpConfigs[filePath] = config.MCP
		lock.Unlock()
		loadMCP(config.MCP)
	}
	if config.A2A != nil {
		lock.Lock()
		a2aConfigs[filePath] = config.A2A
		lock.Unlock()
		loadA2A(config.A2A)
	}
	if config.Proxies != nil {
		for _, proxy := range config.Proxies {
			if proxy.HTTP != nil {
				lock.Lock()
				httpProxyConfigs[filePath] = proxy.HTTP
				lock.Unlock()
				loadHTTPProxy(proxy.HTTP)
			}
			if proxy.TCP != nil {
				lock.Lock()
				tcpProxyConfigs[filePath] = proxy.TCP
				lock.Unlock()
				loadTCPProxy(proxy.TCP)
			}
		}
	}
}

func clearMCP(mcp *ctl.MCP) {
	for _, s := range mcp.Servers {
		mcpserver.RemoveMCPServer(s.Server.Port, s.Server.Name)
	}
}

func loadMCP(mcp *ctl.MCP) {
	mcp.ProcessToolSchemas()
	servers := []*mcpserver.MCPServerPayload{}
	clearMCP(mcp)
	names := []string{}
	for _, s := range mcp.Servers {
		mcp.ProcessMCPServer(s)
		servers = append(servers, s.Server)
		names = append(names, fmt.Sprintf("%s (port: %d)", s.Server.Name, s.Server.Port))
	}
	mcpserver.AddMCPServers(0, servers)
	log.Printf("Added MCP Servers: %+v\n", names)

	addComponents := func(kind string, server *mcpserver.MCPServer, data []byte) {
		names, err := server.AddComponents(kind, data)
		if err != nil {
			log.Printf("Failed to add %s to server [%s] on port [%d] with error [%s]\n", kind, server.Name, server.Port, err.Error())
		} else {
			log.Printf("Added %s to server [%s] on port [%d]: %+v\n", kind, server.Name, server.Port, names)
		}
	}
	for _, s := range mcp.Servers {
		server := mcpserver.GetMCPServer(s.Server.Port, s.Server.Name)
		addComponents("tools", server, util.ToJSONBytes(s.Tools))
		addComponents("prompts", server, util.ToJSONBytes(s.Prompts))
		addComponents("resources", server, util.ToJSONBytes(s.Resources))
		addComponents("templates", server, util.ToJSONBytes(s.Templates))

		delayMin, delayMax, delayCount, _ := types.ParseDurationRange(s.Payloads.Server.Completions.Delay)
		count := server.AddCompletionPayload("ref/prompt", util.ToJSONBytes(s.Payloads.Server.Completions.List), delayMin, delayMax, delayCount)
		log.Printf("Set completion payload (count [%d]) for server [%s] on port [%d]\n", count, server.Name, server.Port)

		for _, tp := range s.Payloads.Server.ToolPayloads {
			if err := server.AddPayload(tp.Name, "tools", util.ToJSONBytes(tp.Payload), true, false, 0, 0, 0, 0); err != nil {
				log.Printf("Failed to set payload for tool [%s] in MCP server [%s] on port [%d] with error [%s]\n", tp.Name, server.Name, server.Port, err.Error())
			} else {
				log.Printf("Set payload for tool [%s] in MCP server [%s] on port [%d]\n", tp.Name, server.Name, server.Port)
			}
		}
		for _, sp := range s.Payloads.Server.StreamPayloads {
			delayMin, delayMax, delayCount, _ := types.ParseDurationRange(sp.Payload.Delay)
			if err := server.AddPayload(sp.Name, "tools", util.ToJSONBytes(sp.Payload), false, true, sp.Payload.Count, delayMin, delayMax, delayCount); err != nil {
				log.Printf("Failed to set stream payload for tool [%s] in MCP server [%s] on port [%d] with error [%s]\n", sp.Name, server.Name, server.Port, err.Error())
			} else {
				log.Printf("Set stream payload for tool [%s] in MCP server [%s] on port [%d]\n", sp.Name, server.Name, server.Port)
			}
		}
		if s.Payloads.Client.Elicit != nil {
			if err := mcpclient.AddPayload(server.Name, "elicit", util.ToJSONBytes(s.Payloads.Client.Elicit)); err != nil {
				log.Printf("Failed to set client elicit payload in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
			} else {
				log.Printf("Client elicit payload added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
			}
		}
		if s.Payloads.Client.Sample != nil {
			if err := mcpclient.AddPayload(server.Name, "sample", util.ToJSONBytes(s.Payloads.Client.Sample)); err != nil {
				log.Printf("Failed to set client sample payload in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
			} else {
				log.Printf("Client sample payload added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
			}
		}
		if s.Payloads.Client.Roots != nil {
			if err := mcpclient.SetRoots(server.Name, util.ToJSONBytes(s.Payloads.Client.Roots)); err != nil {
				log.Printf("Failed to set client roots in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
			} else {
				log.Printf("Client roots added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
			}
		}
	}
}

func clearA2A(a2a *ctl.A2A) {
	for _, a2aAgent := range a2a.Agents {
		a2aserver.RemoveAgent(a2aAgent.Port, a2aAgent.Agent.Card.Name)
		registry.TheAgentRegistry.RemoveAgent(a2aAgent.Agent.Card.Name)
	}
}

func loadA2A(a2a *ctl.A2A) {
	names := []string{}
	clearA2A(a2a)
	for _, a2aAgent := range a2a.Agents {
		name := a2aAgent.Agent.Card.Name
		server := a2aserver.GetOrAddServer(a2aAgent.Port)
		server.AddAgent(a2aAgent.Agent)
		registry.TheAgentRegistry.AddAgent(a2aAgent.Agent, a2aAgent.Port)
		names = append(names, fmt.Sprintf("%s(%d)", name, a2aAgent.Port))

		// if a2aAgent.Response != nil {
		// 	agent := a2aserver.GetAgent(a2aAgent.Port, name)
		// 	if agent == nil {
		// 		fmt.Printf("Bad agent [%s]\n", name)
		// 	} else {
		// 		if err := agent.SetPayload(util.ToJSONBytes(a2aAgent.Response)); err != nil {
		// 			fmt.Printf("Failed to set payload for agent [%s] with error: %s\n", name, err.Error())
		// 		} else {
		// 			fmt.Printf("Payload set successfully for agent [%s]\n", name)
		// 		}
		// 	}
		// }
	}
	log.Printf("Added Agents: %+v\n", names)
}

func removeHTTPProxy(p *httpproxy.Proxy) {
	httpproxy.ClearPortProxy(p.Port)
}

func loadHTTPProxy(p *httpproxy.Proxy) {
	removeHTTPProxy(p)
	proxy := httpproxy.GetPortProxy(p.Port)
	proxy.Enabled = p.Enabled
	proxy.ProxyResponses = p.ProxyResponses
	log.Printf("Proxy [%d] will use responses: %+v", p.Port, util.ToJSONText(proxy.ProxyResponses))
	for name, target := range p.Targets {
		target.Name = name
		log.Printf("Loading HTTP Proxy Target [%s]\n", name)
		if err := proxy.AddTarget(target); err != nil {
			log.Printf("Failed to process HTTP Proxy target [%s] with error: %s", name, err.Error())
		} else {
			log.Printf("HTTP Proxy target [%s] loaded successfully", name)
		}
	}
}

func removeTCPProxy(p *tcpproxy.TCPProxy) {
	tcpproxy.ClearPortProxy(p.Port)
}

func loadTCPProxy(p *tcpproxy.TCPProxy) {
	removeTCPProxy(p)
	if err := tcpproxy.ValidateUpstreams(p.Upstreams); err != nil {
		log.Printf("TCP Proxy [%d] Upstreams failed validation: %s\n", p.Port, err.Error())
	}
	log.Printf("Loading TCP Proxy [%d]\n", p.Port)
	tcpproxy.GetPortProxy(p.Port).AddUpstreams(p.Upstreams)
	log.Printf("TCP Proxy [%d] loaded [%d] upstreams successfully", p.Port, len(p.Upstreams))
}

func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}
