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

package stream

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveRecovering(t *testing.T, h http.Handler, method, target, body string) (rr *httptest.ResponseRecorder, panicked any) {
	t.Helper()
	rr = httptest.NewRecorder()
	func() {
		defer func() { panicked = recover() }()
		h.ServeHTTP(rr, newRequest(method, target, body))
	}()
	return rr, panicked
}

// The newline that ends a chunk was appended to a slice of the payload, which has the rest of the payload in its
// capacity, so it overwrote the first character of the next chunk.
func TestStreamDoesNotOverwriteThePayloadItStreams(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "POST", "/stream?count=10&delay=1ms&chunksize=3", "abcdefghij")
	if want := "abc\ndef\nghi\nj\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
}

// With a duration, the count of chunks was found by dividing by the delay in whole milliseconds, which is zero for a
// delay under a millisecond.
func TestDurationWithADelayUnderAMillisecondDoesNotPanic(t *testing.T) {
	h := setup(t, "")
	rr, panicked := serveRecovering(t, h, "GET", "/stream?duration=5ms&delay=500us&chunksize=4", "")
	if panicked != nil {
		t.Fatalf("the stream panicked: %v", panicked)
	}
	if v := hdr(rr, "Goto-Chunk-Count"); v != "11" {
		t.Errorf("chunk count = %q, want 11 (5ms at one chunk per 500us, and the first one)", v)
	}
	if got := chunks(t, rr.Body.String()); len(got) != 11 {
		t.Errorf("body has %d chunks, want 11", len(got))
	}
}

// A payload that is shorter than the chunk size was padded with random characters up to it, which changed what the
// caller asked to be streamed. The last chunk of a longer payload is not padded either.
func TestShortPayloadIsStreamedAsItIs(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "POST", "/stream?count=3&delay=1ms&chunksize=10", "hello")
	if want := "hello\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
	rr = serve(h, "POST", "/stream?count=3&delay=1ms&chunksize=4", "abcdef")
	if want := "abcd\nef\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
}

// A chunk size below 1 made the payload's chunks be sliced with a negative end.
func TestNonPositiveChunkSizeUsesTheDefault(t *testing.T) {
	h := setup(t, "")
	for _, size := range []string{"-5", "0"} {
		rr, panicked := serveRecovering(t, h, "GET", "/stream?count=2&delay=1ms&chunksize="+size, "")
		if panicked != nil {
			t.Errorf("chunksize=%s: the stream panicked: %v", size, panicked)
			continue
		}
		got := chunks(t, rr.Body.String())
		if len(got) != 2 || len(got[0]) != 10 || len(got[1]) != 10 {
			t.Errorf("chunksize=%s: body = %q, want 2 chunks of the default 10", size, rr.Body.String())
		}
	}
}

// The label of a listener leads the generated payload, and is not cut to the chunk size by the generator, so the
// chunks that were made of a label longer than the chunk size were of different sizes.
func TestGeneratedChunksAreOfTheChunkSizeWhateverTheLabel(t *testing.T) {
	h := setup(t, "a-long-label")
	rr := serve(h, "GET", "/stream?count=4&delay=1ms&chunksize=5", "")
	got := chunks(t, rr.Body.String())
	if len(got) != 4 {
		t.Fatalf("body %q has %d chunks, want 4", rr.Body.String(), len(got))
	}
	for i, c := range got {
		if c != "a-lon" {
			t.Errorf("chunk %d = %q, want a-lon: the label cut to the chunk size", i, c)
		}
	}
	if strings.Contains(rr.Body.String(), "g-lab") {
		t.Errorf("body %q has the rest of the label, which is not part of a chunk", rr.Body.String())
	}
}
