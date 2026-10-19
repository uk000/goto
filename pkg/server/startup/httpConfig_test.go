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

package startup

import (
	"fmt"
	"strings"
	"testing"

	"goto/ctl"
	"goto/pkg/server/response/payload"
)

func httpPayloadOf(body string, prefixes ...string) *payload.ResponsePayload {
	rp := &payload.ResponsePayload{Payload: []byte(body), ContentType: "text/plain"}
	for _, p := range prefixes {
		rp.RequestMatches = append(rp.RequestMatches, &payload.RequestMatch{URIPrefix: p})
	}
	return rp
}

func TestLoadHTTPNothingToLoad(t *testing.T) {
	resetState(t)
	loadHTTP(nil)
	clearHTTP(nil)
	loadHTTP(&ctl.HTTP{})
	loadHTTP(&ctl.HTTP{Servers: []*ctl.HTTPServerConfig{nil, {Port: newPort()}}}) // an entry without anything, and a server without a response
	clearHTTP(&ctl.HTTP{Servers: []*ctl.HTTPServerConfig{nil}})
}

func TestLoadHTTPServesThePayloadsOfEachServer(t *testing.T) {
	resetState(t)
	p1, p2 := newPort(), newPort()
	loadHTTP(&ctl.HTTP{Servers: []*ctl.HTTPServerConfig{
		{Port: p1, Response: &ctl.HTTPResponse{Payloads: []*payload.ResponsePayload{httpPayloadOf("one", "/one"), httpPayloadOf("two", "/two", "/second")}}},
		{Port: p2, Response: &ctl.HTTPResponse{Payloads: []*payload.ResponsePayload{httpPayloadOf("other", "/one")}}},
	}})
	for _, tc := range []struct {
		port      int
		uri, body string
	}{
		{p1, "/one", "one"}, {p1, "/two", "two"}, {p1, "/second", "two"}, {p1, "/ONE/more", "one"}, {p2, "/one", "other"},
	} {
		if body, ok := httpPayload(tc.port, tc.uri); !ok || body != tc.body {
			t.Errorf("port %d %s: payload = %q, %t, want %q", tc.port, tc.uri, body, ok, tc.body)
		}
	}
	if _, ok := httpPayload(p2, "/two"); ok {
		t.Error("a payload of one server is served by another")
	}
	if _, ok := httpPayload(p1, "/unknown"); ok {
		t.Error("a URI without a payload has one")
	}
}

func TestLoadHTTPSkipsPayloadsThatAreNotValid(t *testing.T) {
	resetState(t)
	port := newPort()
	logged := captureLog(t)
	loadHTTP(&ctl.HTTP{Servers: []*ctl.HTTPServerConfig{{Port: port, Response: &ctl.HTTPResponse{Payloads: []*payload.ResponsePayload{
		nil,
		httpPayloadOf("no uri", ""), // a match must have a URI
		httpPayloadOf("valid", "/valid"),
	}}}}})
	if body, ok := httpPayload(port, "/valid"); !ok || body != "valid" {
		t.Errorf("the payload after the ones that are not valid = %q, %t", body, ok)
	}
	if !strings.Contains(logged.String(), "Error processing HTTP response") {
		t.Errorf("the payload without a URI match was not reported: %s", logged.String())
	}
}

func TestClearHTTPRemovesThePayloadsOfItsServersOnly(t *testing.T) {
	resetState(t)
	p1, p2 := newPort(), newPort()
	h1 := &ctl.HTTP{Servers: []*ctl.HTTPServerConfig{{Port: p1, Response: &ctl.HTTPResponse{Payloads: []*payload.ResponsePayload{httpPayloadOf("a", "/a")}}}}}
	h2 := &ctl.HTTP{Servers: []*ctl.HTTPServerConfig{{Port: p2, Response: &ctl.HTTPResponse{Payloads: []*payload.ResponsePayload{httpPayloadOf("b", "/b")}}}}}
	loadHTTP(h1)
	loadHTTP(h2)
	clearHTTP(h1)
	if _, ok := httpPayload(p1, "/a"); ok {
		t.Error("the payload of the cleared server is still served")
	}
	if _, ok := httpPayload(p2, "/b"); !ok {
		t.Error("the payload of the other server was cleared")
	}
}

func TestLoadConfigHTTPFromYAML(t *testing.T) {
	resetState(t)
	port := newPort()
	loadConfig(parse(t, "yaml.yaml", fmt.Sprintf(`
http:
  servers:
  - port: %d
    response:
      payloads:
      - payload: "from yaml"
        contentType: text/plain
        matches:
        - uriPrefix: /yaml
      -
`, port)))
	if body, ok := httpPayload(port, "/yaml"); !ok || body != "from yaml" {
		t.Errorf("payload = %q, %t", body, ok)
	}
}
