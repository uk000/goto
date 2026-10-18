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
	"strings"
)

var panicMarker = []byte("panic serving")

// serverErrorLogWriter receives what net/http and http2 servers write to their ErrorLog. Most of it is noise (TLS
// handshake errors, client disconnects, etc.) and is dropped, but a panic in a request handler is never dropped:
// net/http recovers it, closes the connection (the client just sees an empty reply or EOF), and reports it only here.
type serverErrorLogWriter struct{}

func (serverErrorLogWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, panicMarker) {
		log.Printf("[ERROR] %s", strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

// newServerErrorLog returns the ErrorLog for the HTTP servers, which logs only the panics of request handlers.
func newServerErrorLog() *log.Logger {
	return log.New(serverErrorLogWriter{}, "", 0)
}
