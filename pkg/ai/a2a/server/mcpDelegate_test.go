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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/ai/a2a/model"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPDelegateServer serves an MCP server of the tools over a loopback listener, and returns the URL of it. Each tool
// answers with its name, and counts the calls it got.
func newMCPDelegateServer(t *testing.T, calls map[string]*atomic.Int64, tools ...string) string {
	t.Helper()
	server := gomcp.NewServer(&gomcp.Implementation{Name: "delegate-mcp", Version: "1.0"}, nil)
	for _, name := range tools {
		name := name
		calls[name] = &atomic.Int64{}
		server.AddTool(&gomcp.Tool{Name: name, Description: "answers with its name", InputSchema: map[string]any{"type": "object"}},
			func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
				calls[name].Add(1)
				return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: "answer-of-" + name}}}, nil
			})
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server },
		&gomcp.StreamableHTTPOptions{DisableLocalhostProtection: true}))
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return srv.URL + "/mcp"
}

func countOf(n *atomic.Int64) int { return int(n.Load()) }

func mcpToolDelegates(url string, tools ...string) map[string]*model.DelegateToolCall {
	delegates := map[string]*model.DelegateToolCall{}
	for i, tool := range tools {
		delegates[fmt.Sprintf("t%d", i+1)] = &model.DelegateToolCall{
			ToolCall: &mcpToolCall{Tool: tool, URL: url, RequestTimeout: "10s"},
			Triggers: []string{"go"},
		}
	}
	return delegates
}

// The agent calls the tools of an MCP server that is there.
func TestFederateCallsToolDelegatesOfALiveMCPServer(t *testing.T) {
	ts := newTestServer(t)
	calls := map[string]*atomic.Int64{}
	url := newMCPDelegateServer(t, calls, "tool-a", "tool-b")
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{MaxCalls: 2, Tools: mcpToolDelegates(url, "tool-a", "tool-b")}}
	fed, agent := ts.add("fed-live-tools", false, &model.AgentBehavior{Federate: true}, cfg)

	resp := ts.postMessage("/agent/"+fed+"/", "go")
	if resp.Code != http.StatusOK {
		t.Fatalf("call = %d: %s", resp.Code, resp.Body)
	}
	// What the tools answered is not checked: the agent reports the results that come in the format of the tools of this
	// server (with the timeline of the remote call), and says that no tool produced any for a plain MCP server like this one.
	// What is checked is that the tools were called, once each, and that the trackers know of it.
	for _, tool := range []string{"tool-a", "tool-b"} {
		if !strings.Contains(resp.Body, tool+" (MCP)") {
			t.Errorf("response = %s, want it to have the headers exchanged with %s", resp.Body, tool)
		}
	}
	for name, seen := range calls {
		if n := countOf(seen); n != 1 {
			t.Errorf("tool %s was called %d times, want once", name, n)
		}
	}
	for name, d := range agent.Config.Delegates.Tools {
		if d.Tracker.CallCount.Load() != 1 || !d.Tracker.IsDone() {
			t.Errorf("tracker of %s = %d calls, done %v, want one call that is done", name, d.Tracker.CallCount.Load(), d.Tracker.IsDone())
		}
	}
}

// Calls to an agent that has tool delegates go on while the delegates of the agent are read through the admin API, which
// is how the trackers of the delegates are looked at while they are changing.
func TestFederateToolDelegatesUnderConcurrentCallsAndReads(t *testing.T) {
	ts := newTestServer(t)
	calls := map[string]*atomic.Int64{}
	url := newMCPDelegateServer(t, calls, "tool-a", "tool-b")
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{MaxCalls: 2, Tools: mcpToolDelegates(url, "tool-a", "tool-b")}}
	fed, agent := ts.add("fed-concurrent-tools", false, &model.AgentBehavior{Federate: true}, cfg)

	const callers, rounds = 6, 3
	type outcome struct {
		resp *rawResponse
		err  error
	}
	outcomes := make(chan outcome, callers*rounds)
	stop := make(chan struct{})
	var readers, callersWG sync.WaitGroup
	for _, path := range []string{
		"/a2a/agents/" + fed + "/delegates",
		"/a2a/agents/" + fed + "/delegates/tools",
		"/a2a/agents/" + fed,
	} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					ts.try("GET", path, "")
				}
			}
		}()
	}
	for c := 0; c < callers; c++ {
		callersWG.Add(1)
		go func() {
			defer callersWG.Done()
			for r := 0; r < rounds; r++ {
				resp, err := ts.try("POST", "/agent/"+fed+"/", sendMessageBody("go"))
				outcomes <- outcome{resp, err}
			}
		}()
	}
	callersWG.Wait()
	close(stop)
	readers.Wait()
	close(outcomes)
	for o := range outcomes {
		if o.err != nil || o.resp.Code != http.StatusOK || !strings.Contains(o.resp.Body, "tool-a (MCP)") || !strings.Contains(o.resp.Body, "tool-b (MCP)") {
			t.Errorf("call = %+v, %v, want 200 with the headers exchanged with both tools", o.resp, o.err)
		}
	}
	for name, seen := range calls {
		if n := countOf(seen); n != callers*rounds {
			t.Errorf("tool %s was called %d times, want %d", name, n, callers*rounds)
		}
	}
	for name, d := range agent.Config.Delegates.Tools {
		if n := d.Tracker.CallCount.Load(); n != callers*rounds {
			t.Errorf("calls of %s = %d, want %d", name, n, callers*rounds)
		}
	}
}
