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

package ctl

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestProcessHTTPSendsThePayloadsOfEveryServer(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("http.yaml", []byte(`
http:
  servers:
  - port: 8080
    response:
      payloads:
      - uriMatch: /one
        contentType: text/plain
      - uriMatch: /two
  - port: 9090
    response:
      payloads:
      - uriMatch: /three
  - port: 7070
`))
	if config == nil {
		t.Fatal("config not parsed")
	}
	within(t, "processHTTP", func() { processHTTP(config) })
	got := r.got()
	if len(got) != 3 {
		t.Fatalf("requests = %+v, want one per payload", got)
	}
	for i, want := range []struct{ path, uri string }{
		{"/port=8080/server/response/payload/set/matches", "/one"},
		{"/port=8080/server/response/payload/set/matches", "/two"},
		{"/port=9090/server/response/payload/set/matches", "/three"},
	} {
		if got[i].Method != "POST" || got[i].Path != want.path || got[i].ContentType != "application/json" {
			t.Errorf("request %d = %+v, want a POST of JSON to %s", i, got[i], want.path)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(got[i].Body), &payload); err != nil || payload["uriMatch"] != want.uri {
			t.Errorf("request %d body = %s, %v, want the payload of %s", i, got[i].Body, err, want.uri)
		}
	}
	if got[0].Body == "" || !json.Valid([]byte(got[0].Body)) {
		t.Errorf("body = %q", got[0].Body)
	}
}

func TestProcessHTTPWithNothingSendsNothing(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processHTTP", func() {
		processHTTP(&GotoConfig{})
		processHTTP(&GotoConfig{HTTP: &HTTP{}})
		processHTTP(&GotoConfig{HTTP: &HTTP{Servers: []*HTTPServerConfig{{Port: 1}, {Port: 2, Response: &HTTPResponse{}}}}})
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestProcessHTTPResponseWhenTheRemoteFails(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusBadRequest)
	useRemote(t, r.URL)
	config := ParseConfig("http.yaml", []byte("http:\n  servers:\n  - port: 80\n    response:\n      payloads:\n      - uriMatch: /a\n      - uriMatch: /b\n"))
	within(t, "processHTTP", func() { processHTTP(config) })
	if got := r.got(); len(got) != 2 {
		t.Errorf("requests = %d, want both payloads to be tried", len(got))
	}
}

func TestProcessHTTPResponseWhenTheRemoteIsGone(t *testing.T) {
	r := newRemote(t)
	url := r.URL
	r.Close()
	useRemote(t, url)
	config := ParseConfig("http.yaml", []byte("http:\n  servers:\n  - port: 80\n    response:\n      payloads:\n      - uriMatch: /a\n"))
	within(t, "processHTTP", func() { processHTTP(config) })
}
