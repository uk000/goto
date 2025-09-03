package mcpserver

import (
	"context"
	"encoding/json"
	"goto/pkg/util"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPPrompt struct {
	MCPComponent
	Prompt *gomcp.Prompt `json:"prompt"`
}

func NewMCPPrompt(name, desc string) *MCPPrompt {
	return &MCPPrompt{
		Prompt: &gomcp.Prompt{
			Meta:        map[string]any{},
			Title:       name,
			Name:        name,
			Description: desc,
			Arguments:   []*gomcp.PromptArgument{},
		},
	}
}

func ParsePrompt(payload []byte) (*MCPPrompt, error) {
	prompt := &MCPPrompt{}
	if err := json.Unmarshal(payload, prompt); err != nil {
		return nil, err
	}
	prompt.Kind = KindPrompts
	return prompt, nil
}

func (p *MCPPrompt) Handle(ctx context.Context, req *gomcp.GetPromptRequest) (*gomcp.GetPromptResult, error) {
	result := &gomcp.GetPromptResult{}
	result.Messages = append(result.Messages, &gomcp.PromptMessage{Content: &gomcp.TextContent{Text: util.ToJSONText(req.Params.Arguments)}, Role: gomcp.Role("user")})
	if p.Response != nil {
		result.Messages = append(result.Messages, &gomcp.PromptMessage{Content: &gomcp.TextContent{Text: p.Response.ToText()}, Role: gomcp.Role("assistant")})
	} else {
		result.Messages = append(result.Messages, &gomcp.PromptMessage{Content: &gomcp.TextContent{Text: "<No payload>"}, Role: gomcp.Role("assistant")})
	}
	return result, nil
}
