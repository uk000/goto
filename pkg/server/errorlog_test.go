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
	"testing"
)

// A panic in a handler is recovered by net/http, which reports it only through the server's ErrorLog. It must show up
// in the log, while the other output of the server's ErrorLog stays suppressed.
func TestServerErrorLogReportsHandlerPanics(t *testing.T) {
	var logged bytes.Buffer
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
