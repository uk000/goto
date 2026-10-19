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
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewMCPResourceHasTheDefinitionOfAResource(t *testing.T) {
	r := NewMCPResource("r1", "the first", "text/plain", "file:///r1.txt", 12, "contents")
	if r.Resource == nil || r.Resource.Name != "r1" || r.Resource.Description != "the first" || r.Resource.MIMEType != "text/plain" ||
		r.Resource.URI != "file:///r1.txt" || r.Resource.Size != 12 || r.Text != "contents" {
		t.Errorf("resource = %+v, text = %q", r.Resource, r.Text)
	}
	tpl := NewMCPResourceTemplate("t1", "the first", "text/plain", "file:///{name}.txt", 0, "template contents")
	if tpl.ResourceTemplate == nil || tpl.ResourceTemplate.Name != "t1" || tpl.ResourceTemplate.URITemplate != "file:///{name}.txt" || tpl.Text != "template contents" {
		t.Errorf("template = %+v, text = %q", tpl.ResourceTemplate, tpl.Text)
	}
}

func TestParseResourceReadsAResource(t *testing.T) {
	r, err := ParseResource([]byte(`{"resource":{"name":"r","uri":"file:///r.txt","text":"hello"}}`))
	if err != nil || r == nil {
		t.Fatalf("resource = %v, err = %v", r, err)
	}
	if r.Kind != KindResources || r.Resource.Name != "r" || r.Text != "hello" {
		t.Errorf("resource = %+v, text = %q", r.Resource, r.Text)
	}
	// Without text, there is a text that says so.
	r, err = ParseResource([]byte(`{"resource":{"name":"r","uri":"file:///r.txt"}}`))
	if err != nil || r == nil || r.Text != "<No Text>" {
		t.Errorf("resource without text = %v, err = %v", r, err)
	}
	// Without a resource, there is none, and it is not an error.
	if r, err := ParseResource([]byte(`{"other":1}`)); r != nil || err != nil {
		t.Errorf("payload without a resource = %v, err = %v", r, err)
	}
	if _, err := ParseResource([]byte(`{{`)); err == nil {
		t.Error("a payload that is not JSON was parsed")
	}
}

func TestParseResourceTemplateReadsATemplate(t *testing.T) {
	tpl, err := ParseResourceTemplate([]byte(`{"template":{"name":"t","uriTemplate":"file:///{n}.txt","text":"hello"}}`))
	if err != nil || tpl == nil {
		t.Fatalf("template = %v, err = %v", tpl, err)
	}
	if tpl.Kind != KindTemplates || tpl.ResourceTemplate.Name != "t" || tpl.Text != "hello" {
		t.Errorf("template = %+v, text = %q", tpl.ResourceTemplate, tpl.Text)
	}
	tpl, err = ParseResourceTemplate([]byte(`{"template":{"name":"t","uriTemplate":"file:///{n}.txt"}}`))
	if err != nil || tpl == nil || tpl.Text != "<No Text>" {
		t.Errorf("template without text = %v, err = %v", tpl, err)
	}
	if tpl, err := ParseResourceTemplate([]byte(`{"other":1}`)); tpl != nil || err != nil {
		t.Errorf("payload without a template = %v, err = %v", tpl, err)
	}
	if _, err := ParseResourceTemplate([]byte(`{{`)); err == nil {
		t.Error("a payload that is not JSON was parsed")
	}
}

func TestResourcesAndTemplatesAreListedAndReadFromTheServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "resource-a", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/resource-a/resources/add",
		`[{"resource":{"name":"doc","uri":"file:///doc.txt","mimeType":"text/plain","text":"the document"}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/resource-a/templates/add",
		`[{"template":{"name":"user","uriTemplate":"user:///{name}","text":"a user"}}]`)
	cs := connectInMemory(t, s, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resources, err := cs.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].Name != "doc" {
		t.Fatalf("resources = %+v, err = %v", resources, err)
	}
	templates, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(templates.ResourceTemplates) != 1 || templates.ResourceTemplates[0].Name != "user" {
		t.Fatalf("templates = %+v, err = %v", templates, err)
	}

	read, err := cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "file:///doc.txt"})
	if err != nil || len(read.Contents) != 1 || read.Contents[0].Text != "the document" {
		t.Fatalf("read = %+v, err = %v", read, err)
	}
	read, err = cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "user:///alice"})
	if err != nil || len(read.Contents) != 1 || read.Contents[0].Text != "a user" {
		t.Fatalf("read of a template = %+v, err = %v", read, err)
	}
	// The URI that the template was read with is the one of the contents.
	if read.Contents[0].URI != "" && read.Contents[0].URI != "user:///alice" {
		t.Errorf("URI of the contents = %q", read.Contents[0].URI)
	}

	// With a payload they answer with it instead.
	mustAdmin(t, port, "POST", "/mcpapi/server/resource-a/payload/resources/doc", `{"replaced":true}`)
	read, err = cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "file:///doc.txt"})
	if err != nil || len(read.Contents) != 1 {
		t.Fatalf("read with a payload = %+v, err = %v", read, err)
	}
	expectContains(t, compactJSON(t, read.Contents[0].Text), `"replaced":true`)

	if got, err := cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "file:///missing.txt"}); err == nil {
		t.Errorf("read of a resource that doesn't exist = %+v", got)
	}
}
