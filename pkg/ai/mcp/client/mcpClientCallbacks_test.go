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

package mcpclient

import (
	"context"
	"strings"
	"testing"
	"time"

	"goto/pkg/global"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newCallbackSession creates a session with events enabled in its timeline, and the given client payload.
func newCallbackSession(t *testing.T, np *MCPNamedClientPayload) *MCPSession {
	t.Helper()
	setup(t)
	tc := &ToolCall{Tool: "echo", URL: "http://127.0.0.1:1/mcp"}
	tc.Prepare()
	s := newTestClient(false).CreateSession(context.Background(), "", "callbacks", tc, nil)
	s.clientPayload = np
	return s
}

func eventTexts(s *MCPSession) string {
	texts := []string{}
	for _, e := range s.Timeline.Events {
		texts = append(texts, e.Label+": "+e.Text)
	}
	return strings.Join(texts, "\n")
}

func delayPayload(t *testing.T, contents ...string) *MCPClientPayload {
	t.Helper()
	p := newPayload(contents...)
	p.Actions = []string{"approve"}
	p.Delay = mustDelay(t, 40*time.Millisecond, 50*time.Millisecond)
	return p
}

func TestElicitationHandlerWithoutPayload(t *testing.T) {
	s := newCallbackSession(t, nil)
	res, err := s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{Params: &gomcp.ElicitParams{Message: "need input"}})
	if err != nil || res == nil {
		t.Fatalf("result, error = %+v, %v", res, err)
	}
	if res.Action != "approve" {
		t.Errorf("action = %q, want approve", res.Action)
	}
	if res.Content["requestParams"] == nil || res.Content["Timeline"] == nil {
		t.Errorf("content = %+v, want the request params and a timeline", res.Content)
	}
	if got := eventTexts(s); !strings.Contains(got, "No Elicitation Content") {
		t.Errorf("events = %q", got)
	}
	// A request without params is fine too.
	res, err = s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{})
	if err != nil || res == nil || res.Content["requestParams"] != nil {
		t.Errorf("result for a request without params = %+v, %v", res, err)
	}
}

func TestElicitationHandlerWithPayload(t *testing.T) {
	p := newPayload("only-content")
	p.Actions = []string{"decline"}
	s := newCallbackSession(t, &MCPNamedClientPayload{ElicitPayload: p})
	res, err := s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{Params: &gomcp.ElicitParams{Message: "m"}})
	if err != nil || res == nil || res.Action != "decline" {
		t.Fatalf("result, error = %+v, %v, want the action of the payload", res, err)
	}
	if got := eventTexts(s); !strings.Contains(got, "only-content") {
		t.Errorf("events = %q, want the content of the payload", got)
	}
	// The action is picked from the actions of the payload.
	p.Actions = []string{"a", "b", "c"}
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		res, _ := s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{})
		seen[res.Action] = true
	}
	for a := range seen {
		if a != "a" && a != "b" && a != "c" {
			t.Errorf("action %q is not one of the payload actions", a)
		}
	}
	if len(seen) < 2 {
		t.Errorf("actions seen over 60 requests = %v, want a random pick", seen)
	}
}

func TestElicitationHandlerDelay(t *testing.T) {
	s := newCallbackSession(t, &MCPNamedClientPayload{ElicitPayload: delayPayload(t, "slow")})
	start := time.Now()
	res, err := s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{})
	if err != nil || res == nil {
		t.Fatalf("result, error = %+v, %v", res, err)
	}
	if took := time.Since(start); took < 40*time.Millisecond {
		t.Errorf("handler took %s, want it to delay by at least 40ms", took)
	}
	if got := eventTexts(s); !strings.Contains(got, "Delaying for") {
		t.Errorf("events = %q, want the delay to be reported", got)
	}
}

func TestElicitationHandlerAfterStop(t *testing.T) {
	s := newCallbackSession(t, nil)
	s.Stop = true
	res, err := s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{})
	if res != nil || err != nil {
		t.Errorf("result, error = %+v, %v, want nothing after the client stopped", res, err)
	}
	if got := eventTexts(s); strings.Contains(got, "Elicitation") {
		t.Errorf("events = %q, want no events after the client stopped", got)
	}
}

func createMessageRequest(systemPrompt string) *gomcp.CreateMessageRequest {
	return &gomcp.CreateMessageRequest{Params: &gomcp.CreateMessageParams{SystemPrompt: systemPrompt, MaxTokens: 5}}
}

func TestCreateMessageHandlerWithPayload(t *testing.T) {
	sample := newPayload("sample-content")
	sample.Models = []string{"model-x"}
	sample.Roles = []string{"assistant"}
	s := newCallbackSession(t, &MCPNamedClientPayload{SamplePayload: sample})
	res, err := s.CreateMessageHandler(context.Background(), createMessageRequest("be helpful"))
	if err != nil || res == nil {
		t.Fatalf("result, error = %+v, %v", res, err)
	}
	if res.Model != "model-x" || res.Role != "assistant" || res.StopReason != "be helpful" {
		t.Errorf("model/role/stop reason = %q/%q/%q", res.Model, res.Role, res.StopReason)
	}
	text, ok := res.Content.(*gomcp.TextContent)
	if !ok || !strings.Contains(text.Text, "sample-content") || !strings.Contains(text.Text, "requestParams") {
		t.Errorf("content = %+v, want the payload content and the request params", res.Content)
	}
	if got := eventTexts(s); !strings.Contains(got, "[Sampling/Message]") {
		t.Errorf("events = %q, want the sampling to be reported", got)
	}
}

func TestCreateMessageHandlerPayloadWithoutDetails(t *testing.T) {
	s := newCallbackSession(t, &MCPNamedClientPayload{SamplePayload: &MCPClientPayload{}})
	res, err := s.CreateMessageHandler(context.Background(), createMessageRequest("sys"))
	if err != nil || res == nil {
		t.Fatalf("result, error = %+v, %v", res, err)
	}
	if res.Model != "GotoModel" || res.Role != "none" {
		t.Errorf("model/role = %q/%q, want the defaults", res.Model, res.Role)
	}
	if got := eventTexts(s); !strings.Contains(got, "no defined payload") {
		t.Errorf("events = %q", got)
	}
}

func TestCreateMessageHandlerUsesTheElicitPayloadForElicitations(t *testing.T) {
	elicit := newPayload("elicit-content")
	elicit.Models = []string{"elicit-model"}
	s := newCallbackSession(t, &MCPNamedClientPayload{SamplePayload: newPayload("sample-content"), ElicitPayload: elicit})
	res, err := s.CreateMessageHandler(context.Background(), createMessageRequest("please elicit something"))
	if err != nil || res == nil {
		t.Fatalf("result, error = %+v, %v", res, err)
	}
	if res.Model != "elicit-model" {
		t.Errorf("model = %q, want the one of the elicit payload", res.Model)
	}
	if text, ok := res.Content.(*gomcp.TextContent); !ok || !strings.Contains(text.Text, "elicit-content") || strings.Contains(text.Text, "sample-content") {
		t.Errorf("content = %+v, want the elicit payload content", res.Content)
	}
	if got := eventTexts(s); !strings.Contains(got, "[Elicitation]") {
		t.Errorf("events = %q, want the request to be reported as an elicitation", got)
	}
}

func TestCreateMessageHandlerDelay(t *testing.T) {
	s := newCallbackSession(t, &MCPNamedClientPayload{SamplePayload: delayPayload(t, "slow")})
	start := time.Now()
	if res, err := s.CreateMessageHandler(context.Background(), createMessageRequest("sys")); err != nil || res == nil {
		t.Fatalf("result, error = %+v, %v", res, err)
	}
	if took := time.Since(start); took < 40*time.Millisecond {
		t.Errorf("handler took %s, want it to delay by at least 40ms", took)
	}
}

func TestCreateMessageHandlerAfterStop(t *testing.T) {
	s := newCallbackSession(t, nil)
	s.Stop = true
	res, err := s.CreateMessageHandler(context.Background(), createMessageRequest("sys"))
	if res != nil || err != nil {
		t.Errorf("result, error = %+v, %v, want nothing after the client stopped", res, err)
	}
}

func progressRequest(progress, total float64, message string, meta gomcp.Meta) *gomcp.ProgressNotificationClientRequest {
	return &gomcp.ProgressNotificationClientRequest{Params: &gomcp.ProgressNotificationParams{
		Meta: meta, Message: message, Progress: progress, Total: total,
	}}
}

func TestProgressNotificationHandler(t *testing.T) {
	t.Run("plain message", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.ProgressNotificationHandler(context.Background(), progressRequest(0, 0, "just text", nil))
		if len(s.Timeline.Events) != 1 {
			t.Fatalf("events = %d, want 1", len(s.Timeline.Events))
		}
		e := s.Timeline.Events[0]
		if e.RemoteText != "just text" || !strings.Contains(e.Text, "callbacks: [ProgressNotification]") || strings.Contains(e.Text, "Progress:") {
			t.Errorf("event = text %q remote text %q", e.Text, e.RemoteText)
		}
	})
	t.Run("progress", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.ProgressNotificationHandler(context.Background(), progressRequest(2, 10, "step", nil))
		if got := eventTexts(s); !strings.Contains(got, "[Total: 10.000000][Progress: 2.000000]") {
			t.Errorf("events = %q, want the progress to be reported", got)
		}
	})
	t.Run("json client info", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.ProgressNotificationHandler(context.Background(), progressRequest(0, 0, `{"ClientInfo":{"Label":"c1"}}`, gomcp.Meta{"json": true}))
		e := s.Timeline.Events[0]
		if e.RemoteText != "ClientInfo" || e.RemoteClient == nil {
			t.Errorf("event = remote text %q remote client %+v", e.RemoteText, e.RemoteClient)
		}
	})
	t.Run("json timeline", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.ProgressNotificationHandler(context.Background(), progressRequest(0, 0, `{"TYPE":"TIMELINE","Label":"remote","Port":1}`, gomcp.Meta{"json": true}))
		e := s.Timeline.Events[0]
		if e.RemoteTimeline == nil || e.RemoteTimeline.Label != "remote" {
			t.Errorf("remote timeline = %+v, want the timeline of the message", e.RemoteTimeline)
		}
	})
	t.Run("json data", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.ProgressNotificationHandler(context.Background(), progressRequest(0, 0, `{"some":"data"}`, gomcp.Meta{"json": true}))
		if len(s.Timeline.Events) != 1 {
			t.Fatalf("events = %d, want 1", len(s.Timeline.Events))
		}
	})
	t.Run("invalid json is kept as text", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.ProgressNotificationHandler(context.Background(), progressRequest(0, 0, `{broken`, gomcp.Meta{"json": true}))
		if e := s.Timeline.Events[0]; e.RemoteText != "{broken" {
			t.Errorf("remote text = %q, want the message as it came", e.RemoteText)
		}
	})
	t.Run("after stop", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		s.Stop = true
		s.ProgressNotificationHandler(context.Background(), progressRequest(1, 2, "late", nil))
		if len(s.Timeline.Events) != 0 {
			t.Errorf("events = %q, want none after the client stopped", eventTexts(s))
		}
	})
	t.Run("verbose", func(t *testing.T) {
		s := newCallbackSession(t, nil)
		global.Flags.VerboseMCP = true
		s.ProgressNotificationHandler(context.Background(), progressRequest(0, 0, "loud", nil))
		if len(s.Timeline.Events) != 1 {
			t.Errorf("events = %d, want 1", len(s.Timeline.Events))
		}
	})
}

func TestNotificationHandlersDoNothing(t *testing.T) {
	s := newCallbackSession(t, nil)
	ctx := context.Background()
	s.ToolListChangedHandler(ctx, &gomcp.ToolListChangedRequest{})
	s.PromptListChangedHandler(ctx, &gomcp.PromptListChangedRequest{})
	s.ResourceListChangedHandler(ctx, &gomcp.ResourceListChangedRequest{})
	s.ResourceUpdatedHandler(ctx, &gomcp.ResourceUpdatedNotificationRequest{})
	s.LoggingMessageHandler(ctx, &gomcp.LoggingMessageRequest{})
	if len(s.Timeline.Events) != 0 {
		t.Errorf("events = %q, want none", eventTexts(s))
	}
}
