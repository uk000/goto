package mcpserver

import (
	"context"
	"goto/pkg/ai/mcp"
	"goto/pkg/util"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPResource struct {
	mcp.MCPComponent
	Resource *gomcp.Resource `json:"resource"`
}

type MCPResourceTemplate struct {
	mcp.MCPComponent
	ResourceTemplate *gomcp.ResourceTemplate `json:"template"`
}

func NewMCPResource(name, desc, mimeType, uri string, size int) *MCPResource {
	return &MCPResource{
		Resource: &gomcp.Resource{
			Meta:        map[string]any{},
			Annotations: &gomcp.Annotations{},
			Title:       name,
			Name:        name,
			Description: desc,
			MIMEType:    mimeType,
			Size:        int64(size),
			URI:         uri,
		},
	}
}

func ParseResource(payload []byte) (*MCPResource, error) {
	resource := &MCPResource{}
	if err := util.ReadJsonFromBytes(payload, resource); err != nil {
		return nil, err
	}
	resource.Kind = KindResources
	return resource, nil
}

func (r *MCPResource) Handle(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	result := &gomcp.ReadResourceResult{}
	if r.Payload != nil && r.Payload.JSON != nil {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: r.Payload.JSON.ToJSONText()})
	} else {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: "<No payload>"})
	}
	return result, nil
}

func NewMCPResourceTemplate(name, desc, mimeType, uri string, size int) *MCPResourceTemplate {
	return &MCPResourceTemplate{
		ResourceTemplate: &gomcp.ResourceTemplate{
			Meta:        map[string]any{},
			Annotations: &gomcp.Annotations{},
			Title:       name,
			Name:        name,
			Description: desc,
			MIMEType:    mimeType,
			URITemplate: uri,
		},
	}
}

func ParseResourceTemplate(payload []byte) (*MCPResourceTemplate, error) {
	template := &MCPResourceTemplate{}
	if err := util.ReadJsonFromBytes(payload, template); err != nil {
		return nil, err
	}
	template.Kind = KindTemplates
	return template, nil
}

func (r *MCPResourceTemplate) Handle(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	result := &gomcp.ReadResourceResult{}
	if r.Payload != nil && r.Payload.JSON != nil {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: r.Payload.JSON.ToJSONText()})
	} else {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: "<No payload>"})
	}
	return result, nil
}
