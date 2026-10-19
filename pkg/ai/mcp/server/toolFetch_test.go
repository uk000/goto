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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type seenRequest struct {
	method, host, userAgent, added, forwarded, path string
}

// newUpstream is an HTTP server that answers every request with the body, and tells what the requests were like.
func newUpstream(t *testing.T, body string) (*httptest.Server, chan seenRequest) {
	t.Helper()
	seen := make(chan seenRequest, 32)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- seenRequest{r.Method, r.Host, r.Header.Get("User-Agent"), r.Header.Get("X-Added"), r.Header.Get("X-Fwd"), r.URL.Path}:
		default:
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts, seen
}

func nextSeen(t *testing.T, seen chan seenRequest) seenRequest {
	t.Helper()
	select {
	case r := <-seen:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the upstream got no request")
		return seenRequest{}
	}
}

func fetchTool(url, extra string) string {
	return toolJSON("fetcher", `{"fetch":true}`, fmt.Sprintf(`{"remote":{"url":%q%s}}`, url, extra))
}

func TestFetchToolFetchesTheConfiguredURL(t *testing.T) {
	upstream, seen := newUpstream(t, "upstream body")
	_, cs := newToolServer(t, fetchTool(upstream.URL+"/data", `,"headers":{"request":{"add":{"X-Added":"yes"}}}`))
	res := invokeTool(t, cs, "fetcher", map[string]any{})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	expectContains(t, resultText(res), "upstream body")
	r := nextSeen(t, seen)
	if r.method != "GET" || r.path != "/data" || r.added != "yes" || !strings.Contains(r.userAgent, "/tools/fetcher") {
		t.Errorf("request = %+v", r)
	}
}

func TestFetchToolFetchesAsManyTimesAsCounted(t *testing.T) {
	upstream, seen := newUpstream(t, "again")
	_, cs := newToolServer(t, fetchTool(upstream.URL, ""))
	res := invokeTool(t, cs, "fetcher", map[string]any{"count": 3})
	if got := strings.Count(resultText(res), "again"); got != 3 {
		t.Errorf("got the body %d times, want 3: %q", got, resultText(res))
	}
	for i := 0; i < 3; i++ {
		nextSeen(t, seen)
	}
	select {
	case r := <-seen:
		t.Errorf("one request too many: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestFetchToolTakesTheURLAndTheAuthorityOfTheCall(t *testing.T) {
	configured, configuredSeen := newUpstream(t, "configured")
	other, otherSeen := newUpstream(t, "other")
	_, cs := newToolServer(t, fetchTool(configured.URL, ""))
	res := invokeTool(t, cs, "fetcher", map[string]any{"remoteArgs": map[string]any{"url": other.URL + "/elsewhere", "authority": "called.example.com"}})
	expectContains(t, resultText(res), "other")
	if r := nextSeen(t, otherSeen); r.path != "/elsewhere" || r.host != "called.example.com" {
		t.Errorf("request = %+v", r)
	}
	select {
	case r := <-configuredSeen:
		t.Errorf("the configured URL was fetched: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestFetchToolAddsTheSchemeToAURLThatHasNone(t *testing.T) {
	upstream, _ := newUpstream(t, "no scheme")
	_, cs := newToolServer(t, fetchTool(strings.TrimPrefix(upstream.URL, "http://"), ""))
	expectContains(t, callText(t, cs, "fetcher", map[string]any{}), "no scheme")
}

func TestFetchToolForwardsTheHeadersOfTheMCPRequest(t *testing.T) {
	upstream, seen := newUpstream(t, "forwarded")
	port := isolate(t)
	s := addServer(t, port, "fetch-fwd", true)
	addTools(t, port, "fetch-fwd", "["+fetchTool(upstream.URL, `,"headers":{"request":{"forward":["X-Fwd"]}}`)+"]")
	e := newEndpoint(t, port)
	resp := e.callTool(s.URI, "fetcher", `{}`, map[string]string{"X-Fwd": "from-client"})
	if resp.code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.code, resp.body)
	}
	expectContains(t, resp.body, "forwarded")
	if r := nextSeen(t, seen); r.forwarded != "from-client" {
		t.Errorf("forwarded header = %q, want the value that the client sent", r.forwarded)
	}
}

func TestFetchToolReportsAnUpstreamThatIsDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	_, cs := newToolServer(t, fetchTool(url, ""))
	res, err := callToolRaw(t, cs, "fetcher", map[string]any{})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "connect")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "Failed to invoke Remote URL")
	default:
		t.Errorf("a fetch from a server that is down is a success: %+v", res)
	}
}
