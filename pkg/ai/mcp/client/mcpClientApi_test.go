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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, rr.Body.String())
	}
}

func expectStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, want, rr.Body.String())
	}
}

func listURL(path, target string, extra ...string) string {
	q := url.Values{}
	q.Set("url", target)
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return "/mcpapi/client/list/" + path + "?" + q.Encode()
}

// ----------------------------------------------------------------------------------------------------------------
// Payload and roots registration APIs
// ----------------------------------------------------------------------------------------------------------------

func TestAddClientPayloadAPI(t *testing.T) {
	setup(t)
	cases := []struct {
		name, path, wantMsg, wantName, wantKind string
	}{
		{"default sample", "/mcpapi/client/payload/sample", "Client payload added for MCP [] kind [sample]\n", "", "sample"},
		{"default elicit", "/mcpapi/client/payload/elicit", "Client payload added for MCP [] kind [elicit]\n", "", "elicit"},
		{"named sample", "/mcpapi/client/api-named/payload/sample", "Client payload added for MCP [api-named] kind [sample]\n", "api-named", "sample"},
		{"named elicit", "/mcpapi/client/api-named/payload/elicit", "Client payload added for MCP [api-named] kind [elicit]\n", "api-named", "elicit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := apiCall(t, "POST", c.path, `{"contents":["c1","c2"],"roles":["user"],"models":["m1"],"actions":["approve"]}`)
			expectStatus(t, rr, http.StatusOK)
			if got := rr.Body.String(); got != c.wantMsg {
				t.Errorf("body = %q, want %q", got, c.wantMsg)
			}
			np := getNamedClientPayload(c.wantName)
			if np == nil {
				t.Fatalf("no payload registered for %q", c.wantName)
			}
			p := np.SamplePayload
			if c.wantKind == "elicit" {
				p = np.ElicitPayload
			}
			if p == nil || !reflect.DeepEqual(p.Contents, []string{"c1", "c2"}) || !reflect.DeepEqual(p.Models, []string{"m1"}) ||
				!reflect.DeepEqual(p.Roles, []string{"user"}) || !reflect.DeepEqual(p.Actions, []string{"approve"}) {
				t.Errorf("registered %s payload = %+v", c.wantKind, p)
			}
		})
	}
}

func TestAddClientPayloadAPIInvalidPayload(t *testing.T) {
	setup(t)
	rr := apiCall(t, "POST", "/mcpapi/client/bad-payload/payload/sample", `{"contents": [`)
	expectStatus(t, rr, http.StatusBadRequest)
	if got := rr.Body.String(); !strings.HasPrefix(got, "Failed to add client payload for MCP [bad-payload] kind [sample] with error [") {
		t.Errorf("body = %q", got)
	}
	if np := getNamedClientPayload("bad-payload"); np != nil {
		t.Errorf("a payload was registered despite the error: %+v", np)
	}
}

func TestAddClientPayloadAPIUnknownKind(t *testing.T) {
	setup(t)
	rr := apiCall(t, "POST", "/mcpapi/client/payload/bogus", `{"contents":["x"]}`)
	expectStatus(t, rr, http.StatusNotFound)
}

func TestAddClientPayloadAPIRejectsGET(t *testing.T) {
	setup(t)
	rr := apiCall(t, "GET", "/mcpapi/client/payload/sample", "")
	if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
		t.Errorf("GET status = %d, want 405 or 404", rr.Code)
	}
}

func TestAddRootsAPI(t *testing.T) {
	setup(t)
	body := `[{"uri":"file:///a","name":"A"},{"uri":"file:///b"}]`
	rr := apiCall(t, "POST", "/mcpapi/client/roots-named/payload/roots", body)
	expectStatus(t, rr, http.StatusOK)
	want := "Client roots added for MCP [roots-named]: [" + body + "]\n"
	if got := rr.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	roots := getNamedClientPayload("roots-named").Roots
	if len(roots) != 2 || roots[0].URI != "file:///a" || roots[0].Name != "A" || roots[1].URI != "file:///b" {
		t.Errorf("roots = %+v", roots)
	}
	// Roots without a name go to the default entry.
	rr = apiCall(t, "POST", "/mcpapi/client/payload/roots", `[{"uri":"file:///d"}]`)
	expectStatus(t, rr, http.StatusOK)
	if got := getNamedClientPayload("").Roots; len(got) != 1 || got[0].URI != "file:///d" {
		t.Errorf("default roots = %+v", got)
	}
}

func TestAddRootsAPIInvalidPayload(t *testing.T) {
	setup(t)
	rr := apiCall(t, "POST", "/mcpapi/client/bad-roots/payload/roots", `{"not":"an array"}`)
	expectStatus(t, rr, http.StatusBadRequest)
	if got := rr.Body.String(); !strings.HasPrefix(got, "Failed to set client roots for MCP [bad-roots] with error [") {
		t.Errorf("body = %q", got)
	}
}

func TestGetDetailsAPI(t *testing.T) {
	setup(t)
	if err := AddPayload("details-a", "sample", []byte(`{"contents":["sa"],"models":["ma"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := AddPayload("details-b", "elicit", []byte(`{"contents":["eb"],"actions":["deny"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := SetRoots("details-b", []byte(`[{"uri":"file:///r"}]`)); err != nil {
		t.Fatal(err)
	}
	t.Run("named", func(t *testing.T) {
		rr := apiCall(t, "GET", "/mcpapi/client/details-b/details", "")
		expectStatus(t, rr, http.StatusOK)
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
			t.Errorf("content type = %q, want JSON", ct)
		}
		got := &MCPNamedClientPayload{}
		decodeJSON(t, rr, got)
		if got.ElicitPayload == nil || !reflect.DeepEqual(got.ElicitPayload.Contents, []string{"eb"}) ||
			!reflect.DeepEqual(got.ElicitPayload.Actions, []string{"deny"}) {
			t.Errorf("elicit payload = %+v", got.ElicitPayload)
		}
		if got.SamplePayload != nil {
			t.Errorf("sample payload = %+v, want none", got.SamplePayload)
		}
		if len(got.Roots) != 1 || got.Roots[0].URI != "file:///r" {
			t.Errorf("roots = %+v", got.Roots)
		}
	})
	t.Run("all", func(t *testing.T) {
		rr := apiCall(t, "GET", "/mcpapi/client/details", "")
		expectStatus(t, rr, http.StatusOK)
		got := map[string]*MCPNamedClientPayload{}
		decodeJSON(t, rr, &got)
		names := []string{}
		for n := range got {
			names = append(names, n)
		}
		sort.Strings(names)
		if !reflect.DeepEqual(names, []string{"details-a", "details-b"}) {
			t.Errorf("clients = %v, want [details-a details-b]", names)
		}
		if got["details-a"].SamplePayload == nil || got["details-a"].SamplePayload.Contents[0] != "sa" {
			t.Errorf("details-a = %+v", got["details-a"])
		}
	})
	t.Run("unknown name falls back to the default payload", func(t *testing.T) {
		rr := apiCall(t, "GET", "/mcpapi/client/no-such-client/details", "")
		expectStatus(t, rr, http.StatusOK)
		got := &MCPNamedClientPayload{}
		decodeJSON(t, rr, got)
		// The default is the payload that was created last.
		if got.ElicitPayload == nil || got.ElicitPayload.Contents[0] != "eb" {
			t.Errorf("fallback payload = %+v, want the details-b payload", got)
		}
	})
	t.Run("yaml", func(t *testing.T) {
		rr := apiCall(t, "GET", "/mcpapi/client/details-b/details", "", "Accept", "application/yaml")
		expectStatus(t, rr, http.StatusOK)
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
			t.Errorf("content type = %q, want YAML", ct)
		}
		if body := rr.Body.String(); !strings.Contains(body, "eb") || json.Valid(rr.Body.Bytes()) {
			t.Errorf("body is not YAML with the payload: %q", body)
		}
	})
}

func TestGetDetailsAPINothingRegistered(t *testing.T) {
	setup(t)
	rr := apiCall(t, "GET", "/mcpapi/client/details", "")
	expectStatus(t, rr, http.StatusOK)
	if got := strings.TrimSpace(rr.Body.String()); got != "{}" {
		t.Errorf("body = %q, want {}", got)
	}
}

// ----------------------------------------------------------------------------------------------------------------
// Listing APIs
// ----------------------------------------------------------------------------------------------------------------

func TestListAPIs(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	toolNames := []string{"echo", "elicit", "fail", "plain", "roots", "sample", "slow"}
	for _, transport := range []struct {
		name, url, sse string
	}{
		{"streamable", srv.streamURL(), ""},
		{"sse", srv.sseURL(), "true"},
	} {
		extra := []string{}
		if transport.sse != "" {
			// The sse param only takes effect together with the other params of its group.
			extra = []string{"sse", "true", "tls", "false", "authority", ""}
		}
		t.Run(transport.name+"/all", func(t *testing.T) {
			rr := apiCall(t, "GET", listURL("all", transport.url, extra...), "")
			expectStatus(t, rr, http.StatusOK)
			got := struct {
				Tools     []struct{ Name string }
				Prompts   []struct{ Name string }
				Resources []struct{ Name, URI string }
			}{}
			decodeJSON(t, rr, &got)
			names := []string{}
			for _, tool := range got.Tools {
				names = append(names, tool.Name)
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, toolNames) {
				t.Errorf("tools = %v, want %v", names, toolNames)
			}
			if len(got.Prompts) != 1 || got.Prompts[0].Name != "greet" {
				t.Errorf("prompts = %+v", got.Prompts)
			}
			if len(got.Resources) != 1 || got.Resources[0].Name != "readme" || got.Resources[0].URI != "file:///readme.txt" {
				t.Errorf("resources = %+v", got.Resources)
			}
		})
		t.Run(transport.name+"/tools", func(t *testing.T) {
			rr := apiCall(t, "GET", listURL("tools", transport.url, extra...), "")
			expectStatus(t, rr, http.StatusOK)
			got := map[string]json.RawMessage{}
			decodeJSON(t, rr, &got)
			if _, ok := got["tools"]; !ok || len(got) != 1 {
				t.Errorf("response keys = %v, want only tools", keys(got))
			}
		})
		t.Run(transport.name+"/tool names", func(t *testing.T) {
			rr := apiCall(t, "POST", listURL("tools/names", transport.url, extra...), "")
			expectStatus(t, rr, http.StatusOK)
			got := map[string][]string{}
			decodeJSON(t, rr, &got)
			sort.Strings(got["tools"])
			if len(got) != 1 || !reflect.DeepEqual(got["tools"], toolNames) {
				t.Errorf("response = %v, want only the tool names %v", got, toolNames)
			}
		})
	}
}

func keys[V any](m map[string]V) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestListAPIAppliesAuthority(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	rr := apiCall(t, "GET", listURL("tools", srv.streamURL(), "sse", "false", "tls", "false", "authority", "mcp.example.test"), "")
	expectStatus(t, rr, http.StatusOK)
	hosts := srv.recordedHosts()
	if len(hosts) == 0 {
		t.Fatal("the server saw no request")
	}
	for _, h := range hosts {
		if h != "mcp.example.test" {
			t.Errorf("server saw Host %q, want the requested authority mcp.example.test (all hosts: %v)", h, hosts)
			break
		}
	}
}

func TestListAPIUnreachableServer(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	target := srv.streamURL()
	srv.srv.CloseClientConnections()
	srv.srv.Close()
	rr := apiCall(t, "GET", listURL("all", target), "")
	expectStatus(t, rr, http.StatusBadRequest)
	if got := rr.Body.String(); !strings.HasPrefix(got, fmt.Sprintf("Failed to list tools/prompts/resources on url %s with error [", target)) {
		t.Errorf("body = %q", got)
	}
}

func TestListAPIRequiresURL(t *testing.T) {
	setup(t)
	rr := apiCall(t, "GET", "/mcpapi/client/list/all", "")
	expectStatus(t, rr, http.StatusNotFound)
}

// ----------------------------------------------------------------------------------------------------------------
// Tool call API
// ----------------------------------------------------------------------------------------------------------------

func toolCallBody(t *testing.T, tc map[string]any) string {
	t.Helper()
	b, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCallToolAPI(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	for _, transport := range []struct {
		name, url string
		force     bool
	}{
		{"streamable", srv.streamURL(), false},
		{"sse", srv.sseURL(), true},
	} {
		t.Run(transport.name, func(t *testing.T) {
			before := len(srv.recordedCalls())
			body := toolCallBody(t, map[string]any{
				"tool": "echo", "url": transport.url, "forceSSE": transport.force, "noEvents": true,
				"args": map[string]any{"text": "hello-" + transport.name},
			})
			rr := apiCall(t, "POST", "/mcpapi/client/call/tool", body)
			expectStatus(t, rr, http.StatusOK)
			out := struct {
				ID          string
				Server      string
				Tool        string
				CallResults map[string]struct {
					RequestID string
					Content   []struct{ Type, Text string }
					Data      map[string]any
				}
			}{}
			decodeJSON(t, rr, &out)
			if out.Tool != "echo" || !strings.HasPrefix(out.ID, "[echo]@") {
				t.Errorf("result tool/id = %q/%q", out.Tool, out.ID)
			}
			if len(out.CallResults) != 1 {
				t.Fatalf("call results = %d, want 1: %s", len(out.CallResults), rr.Body.String())
			}
			for _, cr := range out.CallResults {
				if len(cr.Content) != 1 || cr.Content[0].Text != "echo:hello-"+transport.name {
					t.Errorf("content = %+v", cr.Content)
				}
				if cr.Data["echoed"] != "hello-"+transport.name {
					t.Errorf("structured data = %+v", cr.Data)
				}
			}
			calls := srv.recordedCalls()
			if len(calls) != before+1 {
				t.Fatalf("server saw %d calls, want %d", len(calls), before+1)
			}
			if got := calls[before].Args["text"]; got != "hello-"+transport.name {
				t.Errorf("server saw text %v", got)
			}
		})
	}
}

func TestCallToolAPIStreamsEvents(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	body := toolCallBody(t, map[string]any{"tool": "plain", "url": srv.streamURL()})
	rr := apiCall(t, "POST", "/mcpapi/client/call/tool", body)
	expectStatus(t, rr, http.StatusOK)
	out := rr.Body.String()
	for _, want := range []string{"Initiating Tool Call [plain]", "Calling Tool [plain]", "Tool [plain] called successfully"} {
		if !strings.Contains(out, want) {
			t.Errorf("streamed body does not report %q:\n%s", want, out)
		}
	}
}

func TestCallToolAPIInvalidPayloads(t *testing.T) {
	setup(t)
	cases := []struct{ name, body, wantPrefix string }{
		{"not json", `{`, "Failed to parse tool call payload with error ["},
		{"no tool", `{"url":"http://127.0.0.1:1/mcp"}`, "Failed to parse tool call payload with error [invalid tool call payload]"},
		{"no url", `{"tool":"echo"}`, "Failed to parse tool call payload with error [invalid tool call payload]"},
		{"empty", ``, "Failed to parse tool call payload with error ["},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := apiCall(t, "POST", "/mcpapi/client/call/tool", c.body)
			expectStatus(t, rr, http.StatusBadRequest)
			if got := rr.Body.String(); !strings.HasPrefix(got, c.wantPrefix) {
				t.Errorf("body = %q, want prefix %q", got, c.wantPrefix)
			}
		})
	}
}

func TestCallToolAPIAcceptsYAML(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	body := "tool: plain\nurl: " + srv.streamURL() + "\nnoEvents: true\n"
	rr := apiCall(t, "POST", "/mcpapi/client/call/tool", body)
	expectStatus(t, rr, http.StatusOK)
	if srv.toolHits.Load() != 1 {
		t.Errorf("server tool hits = %d, want 1", srv.toolHits.Load())
	}
}

func TestCallToolAPIUnreachableServer(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	target := srv.streamURL()
	srv.srv.CloseClientConnections()
	srv.srv.Close()
	rr := apiCall(t, "POST", "/mcpapi/client/call/tool", toolCallBody(t, map[string]any{"tool": "plain", "url": target, "noEvents": true}))
	expectStatus(t, rr, http.StatusBadRequest)
	if strings.TrimSpace(rr.Body.String()) == "" {
		t.Error("the error is not reported in the response body")
	}
}

func TestCallToolAPIRequiresPOST(t *testing.T) {
	setup(t)
	rr := apiCall(t, "GET", "/mcpapi/client/call/tool", "")
	if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
		t.Errorf("GET status = %d, want 405 or 404", rr.Code)
	}
}
