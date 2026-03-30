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

package mcpserver

import (
	"context"
	"encoding/json"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPResource struct {
	MCPComponent
	Resource *gomcp.Resource `json:"resource"`
	Text     string          `json:"text"`
}

type MCPResourceTemplate struct {
	MCPComponent
	ResourceTemplate *gomcp.ResourceTemplate `json:"template"`
	Text             string                  `json:"text"`
}

func NewMCPResource(name, desc, mimeType, uri string, size int, text string) *MCPResource {
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
		Text: text,
	}
}

func ParseResource(payload []byte) (*MCPResource, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	resourceData := data["resource"]
	if resourceData == nil {
		return nil, nil
	}
	resourceText := "<No Text>"
	if resourceData.(map[string]any)["text"] != nil {
		resourceText = resourceData.(map[string]any)["text"].(string)
	}
	resource := &MCPResource{}
	if err := json.Unmarshal(payload, resource); err != nil {
		return nil, err
	}
	resource.Kind = KindResources
	resource.Text = resourceText
	return resource, nil
}

func (r *MCPResource) Handle(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	result := &gomcp.ReadResourceResult{}
	if r.Response != nil && r.Response.JSON != nil {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: r.Response.JSON.ToJSONText()})
	} else {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: r.Text})
	}
	return result, nil
}

func NewMCPResourceTemplate(name, desc, mimeType, uri string, size int, text string) *MCPResourceTemplate {
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
		Text: text,
	}
}

func ParseResourceTemplate(payload []byte) (*MCPResourceTemplate, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	templateData := data["template"]
	if templateData == nil {
		return nil, nil
	}
	templateText := "<No Text>"
	if templateData.(map[string]any)["text"] != nil {
		templateText = templateData.(map[string]any)["text"].(string)
	}
	template := &MCPResourceTemplate{}
	if err := json.Unmarshal(payload, template); err != nil {
		return nil, err
	}
	template.Text = templateText
	template.Kind = KindTemplates
	return template, nil
}

func (r *MCPResourceTemplate) Handle(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	result := &gomcp.ReadResourceResult{}
	if r.Response != nil && r.Response.JSON != nil {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{URI: r.URI, Text: r.Response.JSON.ToJSONText()})
	} else {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{URI: r.URI, Text: r.Text})
	}
	return result, nil
}
