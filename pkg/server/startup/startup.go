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
	"time"

	"github.com/sgtdi/fswatcher"
)

var (
	configWatcher fswatcher.Watcher
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
				configWatcher.AddPath(configPath, fswatcher.WithDepth(fswatcher.WatchTopLevel))
			}
		}
	}
}

func watchStartupConfig() {
	debounce := util.Debounce(5 * time.Second)
	go configWatcher.Watch(context.Background())
	go func() {
		for event := range configWatcher.Events() {
			for _, t := range event.Types {
				if t == fswatcher.EventRemove {
					log.Printf("File removed: %s. Configs loaded from this file will NOT be removed automatically.\n", event.Path)
				} else {
					log.Printf("Files changed in [%s]. Will reload with delay.", event.Path)
					debounce(func() {
						loadConfigsFromPaths(event.Path)
					})
				}
			}
		}
	}()
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
func loadConfigFromFile(filePath string) {
	if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		log.Printf("Loading config from file: %s\n", filePath)
		loadConfig(ctl.LoadConfig(filePath))
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
	loadConfig(ctl.ParseConfig(buff.Bytes()))
}

func loadConfig(config *ctl.GotoConfig) {
	if config == nil {
		log.Println("No config loaded")
		return
	}
	if config.MCP != nil {
		loadMCP(config.MCP)
	}
	if config.A2A != nil {
		loadA2A(config.A2A)
	}
	if config.Proxies != nil {
		httpproxy.ClearAllProxies()
		tcpproxy.ClearAllProxies()
		for _, proxy := range config.Proxies {
			if proxy.HTTP != nil {
				loadHTTPProxy(proxy.HTTP)
			}
			if proxy.TCP != nil {
				loadTCPProxy(proxy.TCP)
			}
		}
	}
}

func loadMCP(mcp *ctl.MCP) {
	mcp.ProcessToolSchemas()
	servers := []*mcpserver.MCPServerPayload{}
	mcpserver.ClearAllMCPServers()
	for _, s := range mcp.Servers {
		mcp.ProcessMCPServer(s)
		servers = append(servers, s.Server)
	}
	mcpserver.AddMCPServers(0, servers)
	names := []string{}
	for _, s := range servers {
		names = append(names, fmt.Sprintf("%s (port: %d)", s.Name, s.Port))
	}
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
		server := mcpserver.GetMCPServer(s.Server.Name)
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

func loadA2A(a2a *ctl.A2A) {
	names := []string{}
	a2aserver.ClearAllServers()
	for _, a2aAgent := range a2a.Agents {
		name := a2aAgent.Agent.Card.Name
		server := a2aserver.GetOrAddServer(a2aAgent.Port)
		server.AddAgent(a2aAgent.Agent)
		registry.TheAgentRegistry.AddAgent(a2aAgent.Agent, a2aAgent.Port)
		names = append(names, name)

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

func loadHTTPProxy(proxy *httpproxy.Proxy) {
	for name, target := range proxy.Targets {
		target.Name = name
		log.Printf("Loading HTTP Proxy Target [%s]\n", name)
		proxy := httpproxy.GetPortProxy(proxy.Port)
		if err := proxy.AddTarget(target); err != nil {
			log.Printf("Failed to process HTTP Proxy target [%s] with error: %s", name, err.Error())
		} else {
			log.Printf("HTTP Proxy target [%s] loaded successfully", name)
		}
	}
}

func loadTCPProxy(proxy *tcpproxy.TCPProxy) {
	if err := tcpproxy.ValidateUpstreams(proxy.Upstreams); err != nil {
		log.Printf("TCP Proxy [%d] Upstreams failed validation: %s\n", proxy.Port, err.Error())
	}
	log.Printf("Loading TCP Proxy [%d]\n", proxy.Port)
	tcpproxy.GetPortProxy(proxy.Port).AddUpstreams(proxy.Upstreams)
	log.Printf("TCP Proxy [%d] loaded [%d] upstreams successfully", proxy.Port, len(proxy.Upstreams))
}

func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}
