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
	"errors"
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
	"goto/pkg/server/listeners"
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
	configWatchers   []fswatcher.Watcher
	tlsConfigs       = map[string]ctl.PortTLS{}
	a2aConfigs       = map[string]ctl.A2A{}
	mcpConfigs       = map[string]*ctl.MCP{}
	httpProxyConfigs = map[string]*httpproxy.Proxy{}
	tcpProxyConfigs  = map[string]*tcpproxy.TCPProxy{}
	lock             = sync.Mutex{}
)

type ChangeLog map[string]any

func Start() {
	loadStartupConfigs()
	//runStartupScript()
}

func Stop() {
	for _, cw := range configWatchers {
		if cw.IsRunning() {
			cw.Close()
		}
	}
}

func loadStartupConfigs() {
	if len(global.ServerConfig.ConfigPaths) > 0 {
		loadConfigsFromPaths(nil)
		for _, configPath := range global.ServerConfig.ConfigPaths {
			configWatcher, err := fswatcher.New(fswatcher.WithPath(configPath, fswatcher.WithDepth(fswatcher.WatchTopLevel)))
			if err != nil {
				log.Printf("Failed to set watch for config [%s] with error: %s\n", configPath, err.Error())
				log.Printf("Will load config without watching\n")
			} else {
				watchStartupConfig(configWatcher)
			}
			configWatchers = append(configWatchers, configWatcher)
		}
	}
}

func watchStartupConfig(configWatcher fswatcher.Watcher) {
	debounceRemove := util.Debounce(5 * time.Second)
	debounceLoad := util.Debounce(5 * time.Second)
	changeChan := make(chan string, 10)
	go loadChanges(changeChan)
	go configWatcher.Watch(context.Background())
	go func() {
		for event := range configWatcher.Events() {
			removed := false
			for _, t := range event.Types {
				switch t {
				case fswatcher.EventRename:
					_, err := os.Stat(event.Path)
					removed = errors.Is(err, os.ErrNotExist)
				case fswatcher.EventRemove:
					removed = true
				}
			}
			all := false
			if strings.Contains(event.Path, "..") {
				all = true
			}
			if all {
				if removed {
					debounceRemove(func() {
						removeAllConfigs()
					})
				} else {
					debounceLoad(func() {
						loadConfigsFromPaths(nil)
					})
				}
			} else {
				if removed {
					log.Printf("Files removed in [%s]. Will cleanup configs.", event.Path)
					removeConfigs(event.Path)
				} else {
					log.Printf("Files changed in [%s]. Will reload with delay.", event.Path)
					changeChan <- event.Path
				}
			}
		}
	}()
}

func removeAllConfigs() {
	for filename := range tlsConfigs {
		log.Printf("Removing TLS configs for %s.\n", filename)
		clearTLS(tlsConfigs[filename])
		delete(tlsConfigs, filename)
	}
	for filename := range a2aConfigs {
		log.Printf("Removing A2A configs for %s.\n", filename)
		clearA2A(a2aConfigs[filename])
		delete(a2aConfigs, filename)
	}
	for filename := range mcpConfigs {
		log.Printf("Removing MCP configs for %s.\n", filename)
		clearMCP(mcpConfigs[filename])
		delete(mcpConfigs, filename)
	}
	for filename := range httpProxyConfigs {
		log.Printf("Removing HTTP Proxy configs for %s.\n", filename)
		removeHTTPProxy(httpProxyConfigs[filename])
		delete(httpProxyConfigs, filename)
	}
	for filename := range tcpProxyConfigs {
		log.Printf("Removing TCP Proxy configs for %s.\n", filename)
		removeTCPProxy(tcpProxyConfigs[filename])
		delete(tcpProxyConfigs, filename)
	}
}

func removeConfigs(filePath string) {
	parts := strings.Split(filePath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	lock.Lock()
	defer lock.Unlock()
	log.Printf("File [%s] removed.", filename)
	if tlsConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing TLS configs.\n", filename)
		clearTLS(tlsConfigs[filename])
	}
	if a2aConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing A2A configs.\n", filename)
		clearA2A(a2aConfigs[filename])
	}
	if mcpConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing MCP configs.\n", filename)
		clearMCP(mcpConfigs[filename])
	}
	if httpProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing HTTP Proxy configs.\n", filename)
		removeHTTPProxy(httpProxyConfigs[filename])
	}
	if tcpProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing TCP Proxy configs.\n", filename)
		removeTCPProxy(tcpProxyConfigs[filename])
	}
}

func (c ChangeLog) Contains(v string) bool {
	if _, ok := c[v]; ok {
		return true
	}
	for k := range c {
		if strings.Contains(k, v) || strings.Contains(v, k) {
			return true
		}
	}
	return false
}

func loadConfigsFromPaths(changeLog ChangeLog) {
	for _, configPath := range global.ServerConfig.ConfigPaths {
		log.Printf("Loading configs from path [%s]", configPath)
		files, err := os.ReadDir(configPath)
		if err != nil {
			log.Printf("Failed to read config path [%s] with error: %s\n", configPath, err.Error())
			continue
		}
		for _, file := range files {
			filePath := filepath.Join(configPath, file.Name())
			if len(changeLog) == 0 || changeLog.Contains(filePath) {
				loadConfigFromFile(filePath)
			} else {
				log.Printf("Skipping unchanged file [%s]", filePath)
			}
		}
	}
}

func loadChanges(changeChan chan string) {
	debounce := util.Debounce(5 * time.Second)
	lock := sync.Mutex{}
	changeLog := ChangeLog{}
	for path := range changeChan {
		lock.Lock()
		changeLog[path] = 0
		lock.Unlock()
		debounce(func() {
			lock.Lock()
			loadConfigsFromPaths(changeLog)
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
	parts := strings.Split(filePath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	if config == nil {
		log.Println("No config loaded")
		return
	}
	if config.TLS != nil {
		lock.Lock()
		tlsConfigs[filename] = config.TLS
		lock.Unlock()
		processTLS(config.TLS)

	}
	if config.MCP != nil {
		lock.Lock()
		mcpConfigs[filename] = config.MCP
		lock.Unlock()
		loadMCP(config.MCP)
	}
	if config.A2A != nil {
		lock.Lock()
		a2aConfigs[filename] = config.A2A
		lock.Unlock()
		loadA2A(config.A2A)
	}
	if config.Proxies != nil {
		for _, proxy := range config.Proxies {
			if proxy.HTTP != nil {
				lock.Lock()
				httpProxyConfigs[filename] = proxy.HTTP
				lock.Unlock()
				loadHTTPProxy(proxy.HTTP)
			}
			if proxy.TCP != nil {
				lock.Lock()
				tcpProxyConfigs[filename] = proxy.TCP
				lock.Unlock()
				loadTCPProxy(proxy.TCP)
			}
		}
	}
}

func clearTLS(portTLS ctl.PortTLS) {
	for _, tls := range portTLS {
		listeners.RemoveListenerCert(tls.Port)
	}
}

func processTLS(portTLS ctl.PortTLS) {
	if len(portTLS) == 0 {
		log.Println("No TLS configs to configure")
		return
	}
	for _, tls := range portTLS {
		l := listeners.GetListenerForPort(tls.Port)
		if l == nil {
			log.Printf("No Listener on Port [%d]", tls.Port)
			continue
		}
		log.Printf("Loading TLS Cert from path [%s]", tls.Cert)
		cert, key, err := tls.Load()
		if err != nil {
			log.Printf("Failed to read TLS cert file [%s] with error: %s\n", tls.Cert, err.Error())
			continue
		}
		if err = listeners.AddListenerCert(tls.Port, key, cert, true); err != nil {
			log.Printf("Failed to add listener cert for port [%d] cert [%s] key [%s] with error: %s\n", tls.Port, tls.Cert, tls.Key, err.Error())
		}
		log.Println("============================================================")
		log.Printf("Loaded TLS cert for port [%d]", tls.Port)
		log.Println("============================================================")

	}
}

func clearMCP(mcp *ctl.MCP) {
	for _, s := range mcp.Servers {
		if s.Server != nil {
			mcpserver.RemoveMCPServer(s.Server.Port, s.Server.Name)
		}
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
	log.Println("============================================================")
	log.Printf("Added MCP Servers: %+v\n", names)
	log.Println("============================================================")

	addComponents := func(kind string, server *mcpserver.MCPServer, data []byte) {
		if len(data) == 0 {
			return
		}
		names, err := server.AddComponents(kind, data)
		if err != nil {
			log.Printf("Failed to add %s to server [%s] on port [%d] with error [%s]\n", kind, server.Name, server.Port, err.Error())
		} else {
			log.Println("============================================================")
			log.Printf("Added %s to server [%s] on port [%d]: %+v\n", kind, server.Name, server.Port, names)
			log.Println("============================================================")
		}
	}
	for _, s := range mcp.Servers {
		if s == nil || s.Server == nil {
			continue
		}
		server := mcpserver.GetMCPServer(s.Server.Port, s.Server.Name)
		addComponents("tools", server, util.ToJSONBytes(s.Tools))
		addComponents("prompts", server, util.ToJSONBytes(s.Prompts))
		addComponents("resources", server, util.ToJSONBytes(s.Resources))
		addComponents("templates", server, util.ToJSONBytes(s.Templates))

		if s.Payloads != nil {
			sps := s.Payloads.Server
			if sps != nil {
				if sps.Completions != nil {
					delayMin, delayMax, delayCount, _ := types.ParseDurationRange(sps.Completions.Delay)
					count := server.AddCompletionPayload("ref/prompt", util.ToJSONBytes(sps.Completions.List), delayMin, delayMax, delayCount)
					log.Printf("Set completion payload (count [%d]) for server [%s] on port [%d]\n", count, server.Name, server.Port)
				}
				for _, tp := range sps.ToolPayloads {
					if err := server.AddPayload(tp.Name, "tools", util.ToJSONBytes(tp.Payload), true, false, 0, 0, 0, 0); err != nil {
						log.Printf("Failed to set payload for tool [%s] in MCP server [%s] on port [%d] with error [%s]\n", tp.Name, server.Name, server.Port, err.Error())
					} else {
						log.Printf("Set payload for tool [%s] in MCP server [%s] on port [%d]\n", tp.Name, server.Name, server.Port)
					}
				}
				for _, sp := range sps.StreamPayloads {
					if sp.Payload == nil || sp.Payload.Data == nil {
						continue
					}
					delayMin, delayMax, delayCount, _ := types.ParseDurationRange(sp.Payload.Delay)
					if err := server.AddPayload(sp.Name, "tools", util.ToJSONBytes(sp.Payload.Data), true, true, sp.Payload.Count, delayMin, delayMax, delayCount); err != nil {
						log.Printf("Failed to set stream payload for tool [%s] in MCP server [%s] on port [%d] with error [%s]\n", sp.Name, server.Name, server.Port, err.Error())
					} else {
						log.Printf("Set stream payload for tool [%s] in MCP server [%s] on port [%d]\n", sp.Name, server.Name, server.Port)
					}
				}
			}
			spc := s.Payloads.Client
			if spc != nil {
				if spc.Elicit != nil {
					if err := mcpclient.AddPayload(server.Name, "elicit", util.ToJSONBytes(spc.Elicit)); err != nil {
						log.Printf("Failed to set client elicit payload in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
					} else {
						log.Printf("Client elicit payload added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
					}
				}
				if spc.Sample != nil {
					if err := mcpclient.AddPayload(server.Name, "sample", util.ToJSONBytes(spc.Sample)); err != nil {
						log.Printf("Failed to set client sample payload in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
					} else {
						log.Printf("Client sample payload added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
					}
				}
				if spc.Roots != nil {
					if err := mcpclient.SetRoots(server.Name, util.ToJSONBytes(spc.Roots)); err != nil {
						log.Printf("Failed to set client roots in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
					} else {
						log.Printf("Client roots added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
					}
				}
			}
		}
	}
}

func clearA2A(a2a ctl.A2A) {
	for _, pa := range a2a {
		for _, a2aAgent := range pa.Agents {
			if a2aAgent != nil && a2aAgent.Card != nil {
				a2aserver.RemoveAgent(a2aAgent.Port, a2aAgent.Card.Name)
				registry.TheAgentRegistry.RemoveAgent(a2aAgent.Card.Name)
			}
		}
	}
}

func loadA2A(a2a ctl.A2A) {
	names := []string{}
	clearA2A(a2a)
	for _, pa := range a2a {
		for aname, agent := range pa.Agents {
			if agent == nil || agent.Card == nil || agent.Config == nil {
				log.Printf("Skipping agent [%s] due to missing Card/Config\n", aname)
				continue
			}
			agent.Port = pa.Port
			name := agent.Card.Name
			server := a2aserver.GetOrAddServer(agent.Port)
			if err := server.AddAgent(agent); err == nil {
				registry.TheAgentRegistry.AddAgent(agent, agent.Port)
				names = append(names, fmt.Sprintf("%s(%d)", name, agent.Port))
			} else {
				log.Printf("Failed to load agent [%s]: %s\n", aname, err.Error())
			}
		}
	}
	log.Println("============================================================")
	log.Printf("Added Agents: %+v\n", names)
	log.Println("============================================================")
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
			log.Println("============================================================")
			log.Printf("HTTP Proxy target [%s] loaded successfully", name)
			log.Println("============================================================")
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
	log.Println("============================================================")
	log.Printf("TCP Proxy [%d] loaded [%d] upstreams successfully", p.Port, len(p.Upstreams))
	log.Println("============================================================")
}

func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}
