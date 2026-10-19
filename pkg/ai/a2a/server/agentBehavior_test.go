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

package a2aserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"goto/pkg/ai/a2a/model"
	"goto/pkg/types"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

func TestCreateMessages(t *testing.T) {
	text := createTextMessage("some text")
	if text.Role != a2aproto.MessageRoleAgent || len(text.Parts) != 1 {
		t.Fatalf("text message = %+v", text)
	}
	if p, ok := text.Parts[0].(a2aproto.TextPart); !ok || p.Text != "some text" {
		t.Errorf("text part = %#v", text.Parts[0])
	}
	data := createDataMessage(map[string]any{"k": "v"})
	if data.Role != a2aproto.MessageRoleAgent || len(data.Parts) != 1 {
		t.Fatalf("data message = %+v", data)
	}
	if p, ok := data.Parts[0].(a2aproto.DataPart); !ok || p.Data.(map[string]any)["k"] != "v" {
		t.Errorf("data part = %#v", data.Parts[0])
	}
}

func TestCreateAnyPart(t *testing.T) {
	if p, ok := createAnyPart("label", "text").(a2aproto.TextPart); !ok || !strings.Contains(p.Text, "label: text") {
		t.Errorf("part of a string = %#v", createAnyPart("label", "text"))
	}
	for name, value := range map[string]any{"slice": []any{"a", "b"}, "map": map[string]any{"a": 1}, "other": 42} {
		p, ok := createAnyPart("label", value).(a2aproto.DataPart)
		if !ok {
			t.Errorf("part of %s = %#v, want data", name, createAnyPart("label", value))
			continue
		}
		if _, has := p.Data.(map[string]any)["label"]; !has {
			t.Errorf("data of %s = %v, want it keyed by the label", name, p.Data)
		}
	}
	given := a2aproto.NewTextPart("as it is")
	if p, ok := createAnyPart("label", given).(a2aproto.TextPart); !ok || p.Text != "as it is" {
		t.Errorf("part of a part = %#v", createAnyPart("label", given))
	}
}

func TestCreatePartsFromMap(t *testing.T) {
	var parts []a2aproto.Part
	builder := &strings.Builder{}
	createPartsFromMap("Result", map[string]any{
		"one":    "first",
		"many":   []any{"second", "third"},
		"strs":   []string{"fourth"},
		"number": 7,
	}, &parts, builder, false)
	text := builder.String()
	for _, want := range []string{"Result: [one]: first", "Result: [many]: second", "Result: [many]: third", "Result: [strs]: fourth"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want it to have [%s]", text, want)
		}
	}
	if strings.Contains(text, "7") {
		t.Errorf("text = %q, want no text for a number", text)
	}
	if len(parts) != 4 {
		t.Errorf("parts = %d, want a text part for each of the 4 strings", len(parts))
	}
}

func TestCreateHybridMessage(t *testing.T) {
	msg := createHybridMessage("agent-1",
		map[string]any{"tool": "tool text"},
		map[string]any{"remote": "agent text"},
		map[string]any{"UpstreamHeaders": "headers"})
	if msg.Role != a2aproto.MessageRoleAgent {
		t.Errorf("role = %v", msg.Role)
	}
	first, ok := msg.Parts[0].(a2aproto.TextPart)
	if !ok || !strings.Contains(first.Text, "[agent-1] Reporting Result:: [tool]: tool text") || !strings.Contains(first.Text, "[remote]: agent text") {
		t.Errorf("first part = %#v, want the text of the results", msg.Parts[0])
	}
	dataParts := 0
	for _, p := range msg.Parts {
		if _, ok := p.(a2aproto.DataPart); ok {
			dataParts++
		}
	}
	if dataParts != 3 {
		t.Errorf("data parts = %d, want the 3 results", dataParts)
	}
}

func TestProcessMessageWithoutAgentContext(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("noctx", false, nil, nil)
	_, err := agent.Behavior.Impl.ProcessMessage(context.Background(), a2aproto.NewMessage(a2aproto.MessageRoleUser, nil), taskmanager.ProcessOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "without context") {
		t.Errorf("ProcessMessage = %v, want an error about the missing context", err)
	}
}

func TestBehaviorsThatDoNotDoUnaryOrStream(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("streamonly", true, &model.AgentBehavior{Stream: true}, nil)
	resp := ts.postMessage("/agent/"+name+"/", "hello")
	if !strings.Contains(resp.Body, "doesn't support Unary behavior") {
		t.Errorf("unary call of a stream agent = %d: %s, want the error that it is not supported", resp.Code, resp.Body)
	}
}

func TestPrepareDelay(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delay: types.NewDelay(time.Millisecond, 2*time.Millisecond, 0)}
	_, withDelay := ts.add("delay", false, nil, cfg)
	if echo := withDelay.Behavior.Impl.(*AgentBehaviorEcho); echo.delay == nil || echo.delay.Max.Duration != 2*time.Millisecond {
		t.Errorf("delay = %+v, want the one of the config", echo.delay)
	}
	_, without := ts.add("nodelay", false, nil, nil)
	if echo := without.Behavior.Impl.(*AgentBehaviorEcho); echo.delay != nil {
		t.Errorf("delay = %+v, want none", echo.delay)
	}
}

func TestBehaviorSelectionKeepsFirstOfSeveral(t *testing.T) {
	ts := newTestServer(t)
	tests := []struct {
		name     string
		behavior *model.AgentBehavior
		want     string
	}{
		{"echo and stream", &model.AgentBehavior{Echo: true, Stream: true}, "echo"},
		{"stream and federate", &model.AgentBehavior{Stream: true, Federate: true}, "stream"},
		{"federate and proxy", &model.AgentBehavior{Federate: true, HTTPProxy: true}, "federate"},
	}
	for _, tt := range tests {
		_, agent := ts.add("select", true, tt.behavior, nil)
		got := ""
		switch agent.Behavior.Impl.(type) {
		case *AgentBehaviorEcho:
			got = "echo"
		case *AgentBehaviorStream:
			got = "stream"
		case *AgentBehaviorFederate:
			got = "federate"
		}
		if got != tt.want {
			t.Errorf("%s: behavior = %s, want %s", tt.name, got, tt.want)
		}
	}
}
