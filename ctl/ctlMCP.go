package ctl

import (
	"bytes"
	"fmt"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/util"
	"log"
	"net/http"
)

type MCPCompletions struct {
	List  []string `yaml:"list,omitempty"`
	Delay string   `yaml:"delay,omitempty"`
}

type MCPToolPayload struct {
	Name    string         `yaml:"name,omitempty"`
	Payload map[string]any `yaml:"payload,omitempty"`
}

type StreamPayload struct {
	Data  []map[string]any `yaml:"data,omitempty"`
	Count int              `yaml:"count,omitempty"`
	Delay string           `yaml:"delay,omitempty"`
}

type MCPStreamPayload struct {
	Name    string         `yaml:"name,omitempty"`
	Payload *StreamPayload `yaml:"payload,omitempty"`
}

type MCPServerPayloads struct {
	Completions    *MCPCompletions     `yaml:"completions,omitempty"`
	ToolPayloads   []*MCPToolPayload   `yaml:"toolPayloads,omitempty"`
	StreamPayloads []*MCPStreamPayload `yaml:"streamPayloads,omitempty"`
}

type MCPClientPayloads struct {
	Sample map[string]any   `yaml:"sample,omitempty"`
	Elicit map[string]any   `yaml:"elicit,omitempty"`
	Roots  []map[string]any `yaml:"roots,omitempty"`
}

type MCPPayloads struct {
	Server *MCPServerPayloads `yaml:"server,omitempty"`
	Client *MCPClientPayloads `yaml:"client,omitempty"`
}

type MCPServer struct {
	name      string
	Server    *mcpserver.MCPServerPayload `yaml:"server"`
	Tools     []map[string]any            `yaml:"tools,omitempty"`
	Prompts   []map[string]any            `yaml:"prompts,omitempty"`
	Resources []map[string]any            `yaml:"resources,omitempty"`
	Templates []map[string]any            `yaml:"templates,omitempty"`
	Payloads  *MCPPayloads                `yaml:"payloads,omitempty"`
}

type MCP struct {
	ToolSchemas      []map[string]any `yaml:"toolSchemas,omitempty"`
	Servers          []*MCPServer     `yaml:"servers,omitempty"`
	toolInputSchemas map[string]map[string]any
}

var (
	defaultInputSchema = map[string]any{
		"type":     "object",
		"required": []any{},
	}
)

func processMCP(config *GotoConfig) {
	if config.MCP == nil || len(config.MCP.Servers) == 0 {
		log.Println("No MCP servers to configure")
		return
	}
	config.MCP.ProcessToolSchemas()
	servers := []any{}
	for _, s := range config.MCP.Servers {
		config.MCP.ProcessMCPServer(s)
		servers = append(servers, s.Server)
	}
	config.MCP.sendMCPServers(servers)
	for _, s := range config.MCP.Servers {
		config.MCP.sendMCPConfigs(s)
	}
}

func (m *MCP) ProcessToolSchemas() {
	m.toolInputSchemas = map[string]map[string]any{}
	for _, s := range m.ToolSchemas {
		name := s["name"].(string)
		m.toolInputSchemas[name] = s["inputSchema"].(map[string]any)
	}

}

func (m *MCP) ProcessMCPServer(server *MCPServer) {
	server.name = server.Server.Name
	if server.Tools != nil {
		for _, t := range server.Tools {
			tool := t["tool"].(map[string]any)
			if t["schema"] != nil {
				schemaName := t["schema"].(string)
				if m.toolInputSchemas[schemaName] == nil {
					log.Fatalf("Invalid tool schema reference: %s", schemaName)
				}
				tool["inputSchema"] = m.toolInputSchemas[schemaName]
			} else if tool["inputSchema"] == nil {
				tool["inputSchema"] = defaultInputSchema
			}
		}
	}
}

func (m *MCP) sendMCPServers(servers []any) {
	url := fmt.Sprintf("%s/mcpapi/servers/add", currentContext.RemoteGotoURL)
	json := util.ToJSONBytes(servers)
	if json == nil {
		log.Fatalf("error marshalling Server JSON: %+v", servers)
	}
	log.Printf("Sending MCP servers to [%s]\n", url)
	resp, err := http.Post(url, "application/json", bytes.NewReader(json))
	if err != nil {
		log.Printf("Failed to send MCP servers. Error [%s], Content:: %v\n", err, servers)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Server returned non-OK status: %s\n", resp.Status)
	} else {
		log.Printf("MCP Servers sent successfully. Response: [%s]\n", util.Read(resp.Body))
	}
}

func (m *MCP) sendMCPConfigs(server *MCPServer) {
	serverURL := func(serverName, uri string) string {
		return fmt.Sprintf("%s/mcpapi/server/%s/%s", currentContext.RemoteGotoURL, serverName, uri)
	}
	clientURL := func(serverName, uri string) string {
		return fmt.Sprintf("%s/mcpapi/client/%s/%s", currentContext.RemoteGotoURL, serverName, uri)
	}
	sendData := func(serverName, kind, url string, data any) {
		json := util.ToJSONBytes(data)
		if json == nil {
			log.Fatalf("JSON marshalling error. Server [%s] JSON: %+v", server.name, server.Tools)
		}
		log.Printf("Sending %s for MCP Server [%s] to URL [%s]\n", kind, serverName, url)
		resp, err := http.Post(url, "application/json", bytes.NewReader(json))
		if err != nil {
			log.Printf("Failed to send %s for MCP Server [%s]. Error [%s]n", kind, serverName, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("Non-OK status for %s for MCP Server [%s]: %s\n", kind, serverName, resp.Status)
		} else {
			log.Printf("%s sent successfully for MCP Server [%s]. Response: [%s]\n", kind, serverName, util.Read(resp.Body))
		}
	}
	sendData(server.name, "tools", serverURL(server.name, "tools/add"), server.Tools)
	sendData(server.name, "prompts", serverURL(server.name, "prompts/add"), server.Prompts)
	sendData(server.name, "resources", serverURL(server.name, "resources/add"), server.Resources)
	sendData(server.name, "templates", serverURL(server.name, "templates/add"), server.Templates)
	sendData(server.name, "completions", serverURL(server.name, fmt.Sprintf("payload/completion/delay=%s?type=ref/prompt", server.Payloads.Server.Completions.Delay)), server.Payloads.Server.Completions.List)
	for _, tp := range server.Payloads.Server.ToolPayloads {
		sendData(server.name, tp.Name+" payload", serverURL(server.name, fmt.Sprintf("payload/tools/%s", tp.Name)), tp.Payload)
	}
	for _, sp := range server.Payloads.Server.StreamPayloads {
		sendData(server.name, sp.Name+" payload", serverURL(server.name, fmt.Sprintf("payload/tools/%s/stream/count=%d/delay=%s", sp.Name, sp.Payload.Count, sp.Payload.Delay)), sp.Payload.Data)
	}
	sendData(server.name, "client sample payload", clientURL(server.name, "payload/sample"), server.Payloads.Client.Sample)
	sendData(server.name, "client elicit payload", clientURL(server.name, "payload/elicit"), server.Payloads.Client.Elicit)
	sendData(server.name, "client roots payload", clientURL(server.name, "payload/roots"), server.Payloads.Client.Roots)
}
