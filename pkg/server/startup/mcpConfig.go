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
	"fmt"
	"goto/ctl"
	mcpclient "goto/pkg/ai/mcp/client"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
)

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
