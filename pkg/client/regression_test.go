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

package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegressionRequestIDKeepsTheQueryOfTheURL(t *testing.T) {
	// the request id used to replace the query that the URL had
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: "http://127.0.0.1:9/p?a=1&b=two%20words", RequestID: true}
	spec.PrepareAuthority(r)
	if err := spec.PrepareRequest(r); err != nil {
		t.Fatal(err)
	}
	q := spec.request.URL.Query()
	id := spec.request.Header.Get("x-request-id")
	if id == "" || q.Get("x-request-id") != id || q.Get("a") != "1" || q.Get("b") != "two words" || len(q) != 3 {
		t.Errorf("query = %v, header id = %q", q, id)
	}
}

func TestRegressionRequestIDReplacesAnIDOfTheURL(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: "http://127.0.0.1:9/p?x-request-id=old&a=1", RequestID: true}
	spec.PrepareAuthority(r)
	if err := spec.PrepareRequest(r); err != nil {
		t.Fatal(err)
	}
	q := spec.request.URL.Query()
	if ids := q["x-request-id"]; len(ids) != 1 || ids[0] == "old" || ids[0] != spec.request.Header.Get("x-request-id") || q.Get("a") != "1" {
		t.Errorf("query = %v", q)
	}
}

func TestRegressionURLOfAHostThatStartsLikeAScheme(t *testing.T) {
	// a URL got no scheme when the text of it started with http, as the names of some hosts do
	r := httptest.NewRequest("GET", "/", nil)
	for _, tc := range []struct {
		url, wantURL, wantAuthority string
		wantTLS                     bool
	}{
		{"httpbin.test:8080/get", "http://httpbin.test:8080/get", "httpbin.test:8080", false},
		{"http-echo.test/x", "http://http-echo.test/x", "http-echo.test", false},
		{"httpsbin.test", "http://httpsbin.test", "httpsbin.test", false},
		{"HTTPS-secure.test", "http://HTTPS-secure.test", "HTTPS-secure.test", false},
	} {
		spec := &CallSpec{URL: tc.url}
		spec.PrepareAuthority(r)
		if spec.URL != tc.wantURL || spec.Authority != tc.wantAuthority || spec.TLS != tc.wantTLS {
			t.Errorf("%q: url = %q, authority = %q, tls = %v; want %q, %q, %v", tc.url, spec.URL, spec.Authority, spec.TLS, tc.wantURL, tc.wantAuthority, tc.wantTLS)
		}
	}
	spec := &CallSpec{URL: "httpsbin.test", TLS: true}
	spec.PrepareAuthority(r)
	if spec.URL != "https://httpsbin.test" || !spec.TLS {
		t.Errorf("tls call to a host that starts like a scheme: url = %q, tls = %v", spec.URL, spec.TLS)
	}
}

func TestRegressionStreamedPayloadsStayWithTheirOwnRequest(t *testing.T) {
	// the goroutine that streams the chunks of a request used to take the writer of the spec each time, which is the
	// writer of a later request when the server answers before the chunks are all sent
	rc := &recorder{}
	var requests atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			// the first request is answered before its body is read
			w.WriteHeader(http.StatusOK)
			return
		}
		b, _ := io.ReadAll(r.Body)
		rc.add(r, string(b))
		w.Write(b)
	}))
	s.Config.Protocols = new(http.Protocols)
	s.Config.Protocols.SetHTTP1(true)
	s.Config.Protocols.SetUnencryptedHTTP2(true)
	s.Start()
	t.Cleanup(s.Close)

	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL, IsH2: true, Method: "POST", Count: 2, Payload: []string{"a", "b", "c"}, StreamDelay: "40ms"}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Statuses) != 2 {
		t.Fatalf("statuses = %v", results.Statuses)
	}
	got := rc.all()
	if len(got) != 1 || got[0].Body != "abc" {
		t.Errorf("the second request had %+v, want its own chunks only", got)
	}
	// the goroutines of the calls are done soon after the chunks are, which is what the delays add up to
	time.Sleep(200 * time.Millisecond)
}
