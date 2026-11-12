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
	"fmt"

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

// NewMCPResource creates a resource with the given details and text.
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

// componentText reads the optional text of a resource or a template from its raw definition, and rejects a definition
// that is not an object or has text that is not a string, instead of panicking on the type assertions.
func componentText(definition any, what string) (string, error) {
	fields, ok := definition.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s must be an object, got %T", what, definition)
	}
	text := "<No Text>"
	if v := fields["text"]; v != nil {
		t, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("text of %s must be a string, got %T", what, v)
		}
		text = t
	}
	return text, nil
}

// ParseResource parses a JSON resource, and returns nil if the payload has no resource in it.
func ParseResource(payload []byte) (*MCPResource, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	resourceData := data["resource"]
	if resourceData == nil {
		return nil, nil
	}
	resourceText, err := componentText(resourceData, "resource")
	if err != nil {
		return nil, err
	}
	resource := &MCPResource{}
	if err := json.Unmarshal(payload, resource); err != nil {
		return nil, err
	}
	resource.Kind = KindResources
	resource.Text = resourceText
	return resource, nil
}

// Handle answers a read of the resource with its response payload if it has one, or else its text.
func (r *MCPResource) Handle(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	result := &gomcp.ReadResourceResult{}
	if r.Response != nil && r.Response.JSON != nil {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: r.Response.JSON.ToJSONText()})
	} else {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{Text: r.Text})
	}
	return result, nil
}

// NewMCPResourceTemplate creates a resource template with the given details and text.
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

// ParseResourceTemplate parses a JSON resource template, and returns nil if the payload has no template in it.
func ParseResourceTemplate(payload []byte) (*MCPResourceTemplate, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	templateData := data["template"]
	if templateData == nil {
		return nil, nil
	}
	templateText, err := componentText(templateData, "template")
	if err != nil {
		return nil, err
	}
	template := &MCPResourceTemplate{}
	if err := json.Unmarshal(payload, template); err != nil {
		return nil, err
	}
	template.Text = templateText
	template.Kind = KindTemplates
	return template, nil
}

// Handle answers a read of the resource template with its response payload if it has one, or else its text.
func (r *MCPResourceTemplate) Handle(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	result := &gomcp.ReadResourceResult{}
	if r.Response != nil && r.Response.JSON != nil {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{URI: r.URI, Text: r.Response.JSON.ToJSONText()})
	} else {
		result.Contents = append(result.Contents, &gomcp.ResourceContents{URI: r.URI, Text: r.Text})
	}
	return result, nil
}
