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

package echo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// plainWriter is a response writer that cannot be flushed, as what some of the wrappers around the connection are.
type plainWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (p *plainWriter) Header() http.Header         { return p.header }
func (p *plainWriter) WriteHeader(status int)      { p.status = status }
func (p *plainWriter) Write(b []byte) (int, error) { return p.body.Write(b) }

// A response writer that cannot be flushed made the stream echo call Flush on a nil writer and panic.
func TestEchoStreamToAWriterThatCannotFlushIsAnErrorNotAPanic(t *testing.T) {
	r := newRouter()
	w := &plainWriter{header: http.Header{}}
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("streaming to a writer that cannot flush panicked: %v", p)
			}
		}()
		r.ServeHTTP(w, newRequest("POST", "/echo/stream", "one\ntwo\n"))
	}()
	if w.status != http.StatusInternalServerError || !strings.Contains(w.body.String(), "Cannot stream") {
		t.Errorf("response = %d %q, want a 500 that says it cannot stream", w.status, w.body.String())
	}
}

// The other echo calls must not need a writer that can be flushed.
func TestEchoCallsOtherThanTheStreamWorkWithAWriterThatCannotFlush(t *testing.T) {
	r := newRouter()
	for _, target := range []string{"/echo", "/echo/headers", "/echo/body"} {
		w := &plainWriter{header: http.Header{}}
		r.ServeHTTP(w, newRequest("POST", target, "payload"))
		if w.status >= 400 || w.body.Len() == 0 {
			t.Errorf("%s: response = %d %q, want it served", target, w.status, w.body.String())
		}
	}
	// And a recorder, that can be flushed, still streams.
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, newRequest("POST", "/echo/stream", "x\n"))
	if rr.Code != http.StatusOK || rr.Body.String() != "x\n" {
		t.Errorf("stream = %d %q, want it streamed", rr.Code, rr.Body.String())
	}
}
