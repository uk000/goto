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
	"errors"
	"goto/pkg/util"
	"net/http"
	"strings"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseArgsReadsTheArgsOfACall(t *testing.T) {
	args, err := parseArgs(json.RawMessage(`{"text":"hi","count":3,"status":418,"delay":"10ms","metadata":{"k":"v"},"remoteArgs":{"url":"http://x","tool":"t"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.Text != "hi" || args.Count != 3 || args.Status != 418 || args.DelayText != "10ms" || args.Metadata["k"] != "v" {
		t.Errorf("args = %+v", args)
	}
	if args.RemoteArgs == nil || args.RemoteArgs.URL != "http://x" || args.RemoteArgs.ToolName != "t" {
		t.Errorf("remote args = %+v", args.RemoteArgs)
	}
	// All the args are kept as they came too, which is what the tool that adds tools reads its definition from.
	if args.ToolDef["text"] != "hi" || args.ToolDef["count"] != float64(3) {
		t.Errorf("tool def = %v", args.ToolDef)
	}
}

func TestParseArgsRejectsArgsThatAreNotAnObject(t *testing.T) {
	for _, raw := range []string{`[1,2]`, `"text"`, `5`, `{"count":"many"}`, `{"text":`} {
		mustNotPanic(t, "parsing "+raw, func() {
			if args, err := parseArgs(json.RawMessage(raw)); err == nil {
				t.Errorf("parseArgs(%s) = %+v, want an error", raw, args)
			}
		})
	}
}

func TestToolWithArgsThatAreNotAnObjectIsAnErrorOfTheCall(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	mustNotPanic(t, "calling with args that are not an object", func() {
		if res, err := callToolRaw(t, cs, "echo", map[string]any{"count": "many"}); err == nil && (res == nil || !res.IsError) {
			t.Errorf("call with a count that is not a number succeeded: %+v", res)
		}
	})
}

func TestToolContextLogsAndFlushes(t *testing.T) {
	tctx := newToolContextOfEcho(t)
	if got := tctx.Log("first"); got != "first" {
		t.Errorf("Log = %q", got)
	}
	tctx.Log("second %d", 2)
	if got := tctx.Flush(false, false); got != "first --> second 2" {
		t.Errorf("Flush = %q", got)
	}
	if got := tctx.Flush(false, true); got != "first --> second 2" {
		t.Errorf("Flush that clears = %q", got)
	}
	if got := tctx.Flush(false, false); got != "" {
		t.Errorf("Flush after clearing = %q, want nothing", got)
	}
}

func TestToolContextAddsEventsAndDataToTheTimeline(t *testing.T) {
	tctx := newToolContextOfEcho(t)
	before := len(tctx.timeline.Events)
	tctx.AddEvent("")
	tctx.AddRemoteEvent("", "", nil, false)
	tctx.AddData(nil, false)
	if len(tctx.timeline.Events) != before {
		t.Errorf("events = %d, want %d: nothing was added", len(tctx.timeline.Events), before)
	}
	tctx.AddEvent("an event")
	tctx.AddRemoteEvent("a remote event", "remote text", map[string]any{"r": 1}, false)
	tctx.AddData(map[string]any{"d": 1}, true)
	var texts []string
	for _, e := range tctx.timeline.Events {
		texts = append(texts, e.Text)
	}
	expectContains(t, strings.Join(texts, "|"), "an event", "a remote event")
	if len(tctx.timeline.Events) < before+2 {
		t.Errorf("events = %d, want at least %d", len(tctx.timeline.Events), before+2)
	}
}

func TestToolContextWithoutASessionHasNoClient(t *testing.T) {
	tctx := newToolContextOfEcho(t)
	if _, err := tctx.session(); !errors.Is(err, errNoSession) {
		t.Errorf("err = %v, want %v", err, errNoSession)
	}
	tctx.req = nil
	if _, err := tctx.session(); !errors.Is(err, errNoSession) {
		t.Errorf("err without a request = %v, want %v", err, errNoSession)
	}
	// Telling the client of a call that has none is not a failure of the call.
	tctx2 := newToolContextOfEcho(t)
	mustNotPanic(t, "notifying a client that is not there", func() {
		tctx2.notifyClient("progress", map[string]any{"a": 1}, false)
		tctx2.notifyClient("progress", map[string]any{"a": 1}, true)
		if err := tctx2.notifyClientWithError("progress", nil, false); err != nil {
			t.Errorf("err = %v", err)
		}
		notifyClient(context.Background(), nil, "no session", nil, nil)
	})
}

func TestToolContextDelaysOnlyAsMuchAsAsked(t *testing.T) {
	tctx := newToolContextOfEcho(t)
	start := time.Now()
	tctx.applyDelay()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("a call with no delay took %s", elapsed)
	}
	tctx.args.Delay = nil
	mustNotPanic(t, "a call that has no delay", tctx.applyDelay)
}

func TestGetRequestHeadersOfACall(t *testing.T) {
	rs := &util.RequestStore{RequestURI: "/uri", RequestHost: "host"}
	// From the headers of the request that the call is in.
	req := &gomcp.CallToolRequest{Extra: &gomcp.RequestExtra{Header: http.Header{"X-A": {"1"}, headerRequestKey: {"req-1"}}}}
	got := getRequestHeaders(context.Background(), req, rs)
	if got.Get("X-A") != "1" || got[headerRequestKey] != nil || got["RequestURI"][0] != "/uri" {
		t.Errorf("headers = %v", got)
	}
	if http.Header(rs.RequestHeaders).Get("X-A") != "1" {
		t.Error("the headers of the request store were not set")
	}
	// From the request store, when the call has none of its own.
	got = getRequestHeaders(context.Background(), &gomcp.CallToolRequest{}, rs)
	if got.Get("X-A") != "1" {
		t.Errorf("headers from the request store = %v", got)
	}
	// And none at all when there is nothing.
	mustNotPanic(t, "headers of a call that has none", func() {
		if got := getRequestHeaders(context.Background(), &gomcp.CallToolRequest{}, nil); got != nil {
			t.Errorf("headers = %v, want none", got)
		}
	})
}
