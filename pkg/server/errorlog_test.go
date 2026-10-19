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

package server

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// lockedBuffer is a buffer that can be written (by the server, which logs the panic of a handler from the goroutine of
// the connection) while it is read by the test. The race that the race detector found there is of the test, not of the
// logging: a bytes.Buffer is not safe for that, where the log package serializes its writes to the output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// A panic in a handler is recovered by net/http, which reports it only through the server's ErrorLog. It must show up
// in the log, while the other output of the server's ErrorLog stays suppressed.
func TestServerErrorLogReportsHandlerPanics(t *testing.T) {
	var logged lockedBuffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		m["x"] = "y" // panics: assignment to entry in nil map
	}))
	server.Config.ErrorLog = newServerErrorLog()
	server.Start()
	defer server.Close()

	if resp, err := http.Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatalf("expected the request to fail as the handler panicked, got status %d", resp.StatusCode)
	}
	output := logged.String()
	if !strings.Contains(output, "panic serving") || !strings.Contains(output, "assignment to entry in nil map") {
		t.Errorf("expected the handler panic to be logged, got: %q", output)
	}

	logged.Reset()
	server.Config.ErrorLog.Printf("http: TLS handshake error from 1.1.1.1:1: EOF")
	if logged.Len() != 0 {
		t.Errorf("expected other server errors to stay suppressed, got: %q", logged.String())
	}
}

func TestServerErrorLogWriter(t *testing.T) {
	var logged lockedBuffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	w := serverErrorLogWriter{}
	noise := []byte("http: TLS handshake error from 10.0.0.1:5000: EOF\n")
	if n, err := w.Write(noise); n != len(noise) || err != nil {
		t.Errorf("Write(noise) = %d, %v, want %d, nil: the writer takes everything it is given", n, err, len(noise))
	}
	if logged.Len() != 0 {
		t.Errorf("Write(noise) logged %q, want nothing", logged.String())
	}

	panicText := []byte("http: panic serving 10.0.0.1:5000: boom\ngoroutine 7 [running]:\n")
	if n, err := w.Write(panicText); n != len(panicText) || err != nil {
		t.Errorf("Write(panic) = %d, %v, want %d, nil", n, err, len(panicText))
	}
	output := logged.String()
	if !strings.Contains(output, "[ERROR] http: panic serving 10.0.0.1:5000: boom\ngoroutine 7 [running]:") {
		t.Errorf("Write(panic) logged %q, want the whole text of the panic with the ERROR mark", output)
	}
	if strings.HasSuffix(output, "\n\n") {
		t.Errorf("Write(panic) logged %q, want the newline at the end of the text to be trimmed, not doubled", output)
	}
}
