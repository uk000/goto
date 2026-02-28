package startup

import (
	"context"
	"fmt"
	"goto/ctl"
	a2aserver "goto/pkg/ai/a2a/server"
	mcpclient "goto/pkg/ai/mcp/client"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/ai/registry"
	"goto/pkg/global"
	"goto/pkg/scripts"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"os"
	"path/filepath"
	"strings"

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
		for _, configPath := range global.ServerConfig.ConfigPaths {
			files, err := os.ReadDir(configPath)
			if err != nil {
				log.Fatal(err)
			}
			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".yaml") || strings.HasSuffix(file.Name(), ".yml") {
					loadConfig(filepath.Join(configPath, file.Name()))
				}
			}
			if configWatcher == nil {
				configWatcher, err = fswatcher.New(fswatcher.WithPath(configPath, fswatcher.WithDepth(fswatcher.WatchTopLevel)))
				if err != nil {
					fmt.Printf("Failed to set watch for config [%s] with error: %s\n", configPath, err.Error())
					fmt.Printf("Will load config without watching\n")
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
	go configWatcher.Watch(context.Background())
	go func() {
		for event := range configWatcher.Events() {
			var types, flags []string
			for _, t := range event.Types {
				types = append(types, t.String())
			}
			for _, f := range event.Flags {
				flags = append(flags, f)
			}
			fmt.Printf("File changed: %s %v %v\n", event.Path, types, flags)
			loadConfig(event.Path)
		}
	}()
}

func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}

func loadConfig(configPath string) {
	config := ctl.LoadConfig(configPath)
	if config.MCP != nil {
		loadMCP(config.MCP)
	}
	if config.A2A != nil {
		loadA2A(config.A2A)
	}
}

func loadMCP(mcp *ctl.MCP) {
	mcp.ProcessToolSchemas()
	servers := []*mcpserver.MCPServerPayload{}
	for _, s := range mcp.Servers {
		mcp.ProcessMCPServer(s)
		servers = append(servers, s.Server)
	}
	mcpserver.AddMCPServers(0, servers)
	names := []string{}
	for _, s := range servers {
		names = append(names, fmt.Sprintf("%s (port: %d)", s.Name, s.Port))
	}
	fmt.Printf("Added MCP Servers: %+v\n", names)

	addComponents := func(kind string, server *mcpserver.MCPServer, data []byte) {
		names, err := server.AddComponents(kind, data)
		if err != nil {
			fmt.Printf("Failed to add %s to server [%s] on port [%d] with error [%s]\n", kind, server.Name, server.Port, err.Error())
		} else {
			fmt.Printf("Added %s to server [%s] on port [%d]: %+v\n", kind, server.Name, server.Port, names)
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
		fmt.Printf("Set completion payload (count [%d]) for server [%s] on port [%d]\n", count, server.Name, server.Port)

		for _, tp := range s.Payloads.Server.ToolPayloads {
			if err := server.AddPayload(tp.Name, "tools", util.ToJSONBytes(tp.Payload), true, false, 0, 0, 0, 0); err != nil {
				fmt.Printf("Failed to set payload for tool [%s] in MCP server [%s] on port [%d] with error [%s]\n", tp.Name, server.Name, server.Port, err.Error())
			} else {
				fmt.Printf("Set payload for tool [%s] in MCP server [%s] on port [%d]\n", tp.Name, server.Name, server.Port)
			}
		}
		for _, sp := range s.Payloads.Server.StreamPayloads {
			delayMin, delayMax, delayCount, _ := types.ParseDurationRange(sp.Payload.Delay)
			if err := server.AddPayload(sp.Name, "tools", util.ToJSONBytes(sp.Payload), false, true, sp.Payload.Count, delayMin, delayMax, delayCount); err != nil {
				fmt.Printf("Failed to set stream payload for tool [%s] in MCP server [%s] on port [%d] with error [%s]\n", sp.Name, server.Name, server.Port, err.Error())
			} else {
				fmt.Printf("Set stream payload for tool [%s] in MCP server [%s] on port [%d]\n", sp.Name, server.Name, server.Port)
			}
		}
		if s.Payloads.Client.Elicit != nil {
			if err := mcpclient.AddPayload(server.Name, "elicit", util.ToJSONBytes(s.Payloads.Client.Elicit)); err != nil {
				fmt.Printf("Failed to set client elicit payload in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
			} else {
				fmt.Printf("Client elicit payload added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
			}
		}
		if s.Payloads.Client.Sample != nil {
			if err := mcpclient.AddPayload(server.Name, "sample", util.ToJSONBytes(s.Payloads.Client.Sample)); err != nil {
				fmt.Printf("Failed to set client sample payload in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
			} else {
				fmt.Printf("Client sample payload added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
			}
		}
		if s.Payloads.Client.Roots != nil {
			if err := mcpclient.SetRoots(server.Name, util.ToJSONBytes(s.Payloads.Client.Roots)); err != nil {
				fmt.Printf("Failed to set client roots in MCP server [%s] on port [%d] with error [%s]\n", server.Name, server.Port, err.Error())
			} else {
				fmt.Printf("Client roots added for MCP server [%s] on port [%d]\n", server.Name, server.Port)
			}
		}
	}
}

func loadA2A(a2a *ctl.A2A) {
	names := []string{}
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
	fmt.Printf("Added Agents: %+v\n", names)
}
