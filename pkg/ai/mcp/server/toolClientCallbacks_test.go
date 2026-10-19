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
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const elicitPayload = `{"message":"Who are you?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}`

func TestSampleToolAsksTheClientForASample(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "sample-a", true)
	addTools(t, port, "sample-a", "["+toolJSON("sampler", `{"sample":true}`, "")+"]")
	var mu sync.Mutex
	var seen *gomcp.CreateMessageParams
	cs := connectInMemory(t, s, &gomcp.ClientOptions{
		CreateMessageHandler: func(ctx context.Context, req *gomcp.CreateMessageRequest) (*gomcp.CreateMessageResult, error) {
			mu.Lock()
			seen = req.Params
			mu.Unlock()
			return &gomcp.CreateMessageResult{
				Content:    &gomcp.TextContent{Text: `{"answer":42}`},
				Model:      "test-model",
				Role:       "assistant",
				StopReason: "endTurn",
			}, nil
		},
	})
	res := invokeTool(t, cs, "sampler", map[string]any{})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	expectContains(t, resultText(res), "Sampling successful", "Model: test-model", "Role: assistant", "StopReason: endTurn", `{"answer":42}`)
	mu.Lock()
	defer mu.Unlock()
	if seen == nil {
		t.Fatal("the client wasn't asked for a sample")
	}
	if seen.MaxTokens != 10 || seen.SystemPrompt != "sampler tool" || len(seen.Messages) != 1 {
		t.Errorf("request of the sample = %+v", seen)
	}
}

func TestSampleToolFailsWhenTheClientCannotSample(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "sample-b", true)
	addTools(t, port, "sample-b", "["+toolJSON("sampler", `{"sample":true}`, "")+"]")
	cs := connectInMemory(t, s, &gomcp.ClientOptions{
		CreateMessageHandler: func(ctx context.Context, req *gomcp.CreateMessageRequest) (*gomcp.CreateMessageResult, error) {
			return nil, errors.New("no model today")
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: "sampler", Arguments: map[string]any{}})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "sampling failed")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "sampling failed")
	default:
		t.Errorf("a failed sample is a success: %+v", res)
	}
}

func elicitServer(t *testing.T, name string, handler func(context.Context, *gomcp.ElicitRequest) (*gomcp.ElicitResult, error)) *gomcp.ClientSession {
	t.Helper()
	port := isolate(t)
	s := addServer(t, port, name, true)
	addTools(t, port, name, "["+toolJSON("elicitor", `{"elicit":true}`, "")+"]")
	mustAdmin(t, port, "POST", "/mcpapi/server/"+name+"/payload/tools/elicitor", elicitPayload)
	return connectInMemory(t, s, &gomcp.ClientOptions{ElicitationHandler: handler})
}

func TestElicitToolSendsTheRequestedSchemaAndReportsTheAnswer(t *testing.T) {
	var mu sync.Mutex
	var seen *gomcp.ElicitParams
	cs := elicitServer(t, "elicit-a", func(ctx context.Context, req *gomcp.ElicitRequest) (*gomcp.ElicitResult, error) {
		mu.Lock()
		seen = req.Params
		mu.Unlock()
		return &gomcp.ElicitResult{Action: "accept", Content: map[string]any{"name": "bob"}}, nil
	})
	res := invokeTool(t, cs, "elicitor", map[string]any{})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	expectContains(t, resultText(res), "Received elicit response from client")
	expectContains(t, compactJSON(t, toJSON(t, res.StructuredContent)), `"bob"`)
	mu.Lock()
	defer mu.Unlock()
	if seen == nil || seen.Message != "Who are you?" {
		t.Fatalf("elicit params = %+v", seen)
	}
	schema := toJSON(t, seen.RequestedSchema)
	expectContains(t, schema, `"type":"object"`, `"name":{"type":"string"}`)
}

func TestElicitToolReportsAnEmptyAnswer(t *testing.T) {
	cs := elicitServer(t, "elicit-b", func(ctx context.Context, req *gomcp.ElicitRequest) (*gomcp.ElicitResult, error) {
		return &gomcp.ElicitResult{Action: "decline"}, nil
	})
	expectContains(t, callText(t, cs, "elicitor", map[string]any{}), "Empty elicit response from client")
}

func TestElicitToolFailsWhenTheClientFails(t *testing.T) {
	cs := elicitServer(t, "elicit-c", func(ctx context.Context, req *gomcp.ElicitRequest) (*gomcp.ElicitResult, error) {
		return nil, errors.New("user left")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: "elicitor", Arguments: map[string]any{}})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "failed to get elicit response")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "failed to get elicit response")
	default:
		t.Errorf("a failed elicitation is a success: %+v", res)
	}
}

func TestElicitToolWithoutAPayloadStillAsks(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "elicit-d", true)
	addTools(t, port, "elicit-d", "["+toolJSON("elicitor", `{"elicit":true}`, "")+"]")
	asked := make(chan string, 1)
	cs := connectInMemory(t, s, &gomcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, req *gomcp.ElicitRequest) (*gomcp.ElicitResult, error) {
			asked <- fmt.Sprint(req.Params.Message)
			return &gomcp.ElicitResult{Action: "cancel"}, nil
		},
	})
	mustNotPanic(t, "eliciting without a payload", func() {
		callText(t, cs, "elicitor", map[string]any{})
	})
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Error("the client was not asked")
	}
}

func TestExtractClientData(t *testing.T) {
	for name, tt := range map[string]struct {
		content any
		want    map[string]any
	}{
		"map":                {map[string]any{"a": 1}, map[string]any{"a": 1}},
		"text that is JSON":  {&gomcp.TextContent{Text: `{"a":"b"}`}, map[string]any{"a": "b"}},
		"text value":         {gomcp.TextContent{Text: `{"a":"b"}`}, map[string]any{"a": "b"}},
		"text that is not":   {&gomcp.TextContent{Text: "plain words"}, map[string]any{"content": "plain words"}},
		"text value, plain":  {gomcp.TextContent{Text: "plain words"}, map[string]any{"content": "plain words"}},
		"nil":                {nil, nil},
		"something else":     {42, nil},
		"content of another": {&gomcp.ImageContent{Data: []byte("x"), MIMEType: "image/png"}, nil},
	} {
		got := extractClientData(tt.content)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: extractClientData = %v, want %v", name, got, tt.want)
		}
	}
}
