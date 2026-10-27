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

package util

import (
	"context"
	"fmt"
	"goto/pkg/types"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestContextHeadersRoundTrip(t *testing.T) {
	headers := &types.Headers{}
	ctx := WithContextHeaders(context.Background(), headers)
	if got := GetContextHeaders(ctx); got != headers {
		t.Errorf("GetContextHeaders = %p, want the headers that the context was given, %p", got, headers)
	}
	if got := GetContextHeaders(context.Background()); got != nil {
		t.Errorf("GetContextHeaders of a context without headers = %v, want nil", got)
	}
}

// What a context carries under another key, or as another type, is not the headers of the context.
func TestGetContextHeadersIgnoresWhatIsNotHeaders(t *testing.T) {
	requestHeaders := WithRequestHeaders(context.Background(), map[string][]string{"A": {"1"}})
	if got := GetContextHeaders(requestHeaders); got != nil {
		t.Errorf("GetContextHeaders of a context that has request headers = %v, want nil", got)
	}
	wrongType := context.WithValue(context.Background(), HeadersKey, "not headers")
	if got := GetContextHeaders(wrongType); got != nil {
		t.Errorf("GetContextHeaders of a context that has a string as headers = %v, want nil", got)
	}
	wrongRequestType := context.WithValue(context.Background(), RequestHeadersKey, &types.Headers{})
	if got := GetContextHeaders(wrongRequestType); got != nil {
		t.Errorf("GetContextHeaders of a context that has headers where request headers go = %v, want nil", got)
	}
	headers := &types.Headers{}
	both := WithContextHeaders(requestHeaders, headers)
	if got := GetContextHeaders(both); got != headers {
		t.Errorf("GetContextHeaders of a context that has both kinds = %v, want the context headers", got)
	}
	if got := GetRequestHeaders(both); !reflect.DeepEqual(got, map[string][]string{"A": {"1"}}) {
		t.Errorf("GetRequestHeaders of a context that has both kinds = %v, want the request headers", got)
	}
}

func TestGetRequestHeaders(t *testing.T) {
	headers := map[string][]string{"A": {"1"}}
	if got := GetRequestHeaders(WithRequestHeaders(context.Background(), headers)); !reflect.DeepEqual(got, headers) {
		t.Errorf("GetRequestHeaders = %v, want %v", got, headers)
	}
	if got := GetRequestHeaders(WithRequestHeaders(context.Background(), http.Header{"B": {"2"}})); !reflect.DeepEqual(got, map[string][]string{"B": {"2"}}) {
		t.Errorf("GetRequestHeaders of http.Header = %v, want it", got)
	}
	if got := GetRequestHeaders(context.Background()); got != nil {
		t.Errorf("GetRequestHeaders of a context without headers = %v, want nil", got)
	}
	if got := GetRequestHeaders(context.WithValue(context.Background(), RequestHeadersKey, 5)); got != nil {
		t.Errorf("GetRequestHeaders of a context that has a number as headers = %v, want nil", got)
	}
}

// newRequestWithHeaders returns a request that has a request store already, which is not set up from the server here.
func newRequestWithHeaders(headers http.Header) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), RequestStoreKey, &RequestStore{}))
	for h, v := range headers {
		r.Header[h] = v
	}
	return r
}

func excludeForTest(t *testing.T, headers ...string) {
	t.Helper()
	for _, h := range headers {
		ExcludeHeader(h)
		t.Cleanup(func() { IncludeHeader(h) })
	}
}

func TestExcludedHeaders(t *testing.T) {
	excludeForTest(t, "X-Test-Secret")
	if !IsHeaderExcluded("x-test-secret") || !IsHeaderExcluded("X-TEST-SECRET") || IsHeaderExcluded("x-test-public") {
		t.Errorf("excluded headers = %v, want x-test-secret in any case, and not x-test-public", ExcludedHeaders)
	}
	if !ExcludedHeaders["x-test-secret"] {
		t.Errorf("ExcludedHeaders = %v, want the exported map to have x-test-secret", ExcludedHeaders)
	}
	header := http.Header{"X-Test-Secret": {"s"}, "X-Test-Public": {"p"}}
	log := GetHeadersLog(header)
	if strings.Contains(log, "X-Test-Secret") || !strings.Contains(log, "X-Test-Public") {
		t.Errorf("headers log = %s, want only the header that is not excluded", log)
	}
	r := newRequestWithHeaders(header)
	w := httptest.NewRecorder()
	CopyHeaders("", r, w, r.Header, false, false, true)
	if got := w.Header(); !reflect.DeepEqual(got, http.Header{"X-Test-Public": {"p"}}) {
		t.Errorf("headers copied = %v, want only the one that is not excluded", got)
	}
	IncludeHeader("x-test-secret")
	if IsHeaderExcluded("x-test-secret") || ExcludedHeaders["x-test-secret"] {
		t.Errorf("x-test-secret is still excluded after it was included again")
	}
}

// Headers get excluded while requests are served, by the configuration of the log and of response headers.
func TestExcludedHeadersChangeWhileRequestsAreServed(t *testing.T) {
	const writers, readers, rounds = 4, 4, 60
	var names []string
	for w := 0; w < writers; w++ {
		for i := 0; i < rounds; i++ {
			names = append(names, fmt.Sprintf("x-race-%d-%d", w, i))
		}
	}
	t.Cleanup(func() {
		for _, n := range names {
			IncludeHeader(n)
		}
	})
	header := http.Header{"X-Race-Probe": {"p"}, "X-Race-0-0": {"z"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				ExcludeHeader(fmt.Sprintf("x-race-%d-%d", w, i))
			}
		}(w)
	}
	failures := make([]string, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				if log := GetHeadersLog(header); !strings.Contains(log, "X-Race-Probe") {
					failures[r] = "headers log without the header that is never excluded: " + log
					return
				}
				req := newRequestWithHeaders(http.Header{"X-Race-Probe": {"p"}})
				w := httptest.NewRecorder()
				CopyHeaders("", req, w, req.Header, false, false, true)
				if got := w.Header()["X-Race-Probe"]; len(got) != 1 || got[0] != "p" {
					failures[r] = fmt.Sprintf("copied header = %v, want [p]", got)
					return
				}
			}
		}(r)
	}
	close(start)
	wg.Wait()
	for r, f := range failures {
		if f != "" {
			t.Errorf("reader %d: %s", r, f)
		}
	}
	for _, n := range names {
		if !IsHeaderExcluded(n) {
			t.Fatalf("%s was not excluded after all the writers were done", n)
		}
	}
}
