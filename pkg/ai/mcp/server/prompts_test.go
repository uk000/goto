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
	"strings"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewMCPPromptHasTheDefinitionOfAPrompt(t *testing.T) {
	p := NewMCPPrompt("p1", "the first prompt")
	if p.Prompt == nil || p.Prompt.Name != "p1" || p.Prompt.Title != "p1" || p.Prompt.Description != "the first prompt" {
		t.Errorf("prompt = %+v", p.Prompt)
	}
	if p.Prompt.Arguments == nil || len(p.Prompt.Arguments) != 0 {
		t.Errorf("arguments = %v, want none that are not nil", p.Prompt.Arguments)
	}
}

func TestParsePromptReadsAPrompt(t *testing.T) {
	p, err := ParsePrompt([]byte(`{"prompt":{"name":"greet","description":"says hi","arguments":[{"name":"who","required":true}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindPrompts || p.Prompt.Name != "greet" || len(p.Prompt.Arguments) != 1 || p.Prompt.Arguments[0].Name != "who" || !p.Prompt.Arguments[0].Required {
		t.Errorf("prompt = %+v", p.Prompt)
	}
	if _, err := ParsePrompt([]byte(`{{`)); err == nil {
		t.Error("a payload that is not JSON was parsed")
	}
}

func TestPromptIsListedAndGivenByTheServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "prompt-a", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/prompt-a/prompts/add",
		`[{"prompt":{"name":"greet","description":"says hi","arguments":[{"name":"who"}]}}]`)
	cs := connectInMemory(t, s, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	listed, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Prompts) != 1 || listed.Prompts[0].Name != "greet" {
		t.Fatalf("prompts = %+v", listed.Prompts)
	}

	// Without a payload the prompt answers that it has none, after echoing the arguments that it was given.
	got, err := cs.GetPrompt(ctx, &gomcp.GetPromptParams{Name: "greet", Arguments: map[string]string{"who": "world"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	if got.Messages[0].Role != "user" || !contentContains(got.Messages[0].Content, `{"who":"world"}`) {
		t.Errorf("first message = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || !contentContains(got.Messages[1].Content, "<No payload>") {
		t.Errorf("second message = %+v", got.Messages[1])
	}

	// With one, it answers with it.
	mustAdmin(t, port, "POST", "/mcpapi/server/prompt-a/payload/prompts/greet", `{"reply":"hello there"}`)
	got, err = cs.GetPrompt(ctx, &gomcp.GetPromptParams{Name: "greet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || !contentContains(got.Messages[1].Content, "hello there") {
		t.Errorf("messages with a payload = %+v", got.Messages)
	}
}

func contentContains(c gomcp.Content, want string) bool {
	text, ok := c.(*gomcp.TextContent)
	return ok && strings.Contains(text.Text, want)
}

func TestPromptThatTheServerDoesNotHaveIsAnError(t *testing.T) {
	_, cs := newToolServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if got, err := cs.GetPrompt(ctx, &gomcp.GetPromptParams{Name: "missing"}); err == nil {
		t.Errorf("got %+v for a prompt that doesn't exist", got)
	}
}
