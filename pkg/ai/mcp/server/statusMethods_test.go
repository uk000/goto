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
	"net/http"
	"strconv"
	"testing"

	"goto/pkg/constants"
)

func (f *forcedServer) openStream() rawResponse {
	f.t.Helper()
	return f.do("GET", f.server.URI, "", map[string]string{"Accept": "text/event-stream"})
}

func requireForcedStream(t *testing.T, resp rawResponse, code int) {
	t.Helper()
	if resp.code != code || resp.header.Get(constants.HeaderGotoForcedStatus) != strconv.Itoa(code) {
		t.Errorf("stream = %d %v, want the forced %d", resp.code, resp.header, code)
	}
}

func requireStreamNotForced(t *testing.T, resp rawResponse) {
	t.Helper()
	if resp.header.Get(constants.HeaderGotoForcedStatus) != "" || resp.code == http.StatusServiceUnavailable {
		t.Errorf("stream = %d %v, want it not forced", resp.code, resp.header)
	}
}

func TestForcedStatusOfGETStreams(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503?methods=GET")
	for i := 0; i < 3; i++ {
		requireForcedStream(t, f.openStream(), 503)
	}
	// The tool calls are POST, which the status is not for.
	if resp := f.call(nil); resp.code != http.StatusOK {
		t.Errorf("tool call = %d %q, want it served", resp.code, resp.body)
	}
}

func TestForcedStatusOfGETStreamsIsCounted(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:2?methods=get")
	requireForcedStream(t, f.openStream(), 503)
	requireForcedStream(t, f.openStream(), 503)
	requireStreamNotForced(t, f.openStream())
}

func TestForcedStatusOfAllMethods(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503?methods=*")
	requireForcedStream(t, f.openStream(), 503)
	if resp := f.call(nil); resp.code != http.StatusServiceUnavailable {
		t.Errorf("tool call = %d %q, want the forced 503", resp.code, resp.body)
	}
}

// The status for GET and the status for POST of a server are two, which don't use each other's counts.
func TestForcedStatusOfGETAndOfPOSTAreSeparate(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/502:1")
	f.admin("POST", "/status/set/503:2?methods=GET")
	if resp := f.call(nil); resp.code != http.StatusBadGateway {
		t.Errorf("tool call = %d %q, want the forced 502", resp.code, resp.body)
	}
	requireForcedStream(t, f.openStream(), 503)
	requireForcedStream(t, f.openStream(), 503)
	requireStreamNotForced(t, f.openStream())
	if resp := f.call(nil); resp.code != http.StatusOK {
		t.Errorf("tool call = %d %q, want it served once its status is used up", resp.code, resp.body)
	}
}

// A status of POST, which is what a status is set for by default, never keeps a stream from opening, or uses its count.
func TestForcedStatusOfPOSTDoesNotApplyToGETStreams(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:1")
	requireStreamNotForced(t, f.openStream())
	if resp := f.call(nil); resp.code != http.StatusServiceUnavailable {
		t.Errorf("tool call = %d %q, want the forced 503, as the stream didn't use it", resp.code, resp.body)
	}
}

func TestForcedStatusWithInvalidMethodsIsRejected(t *testing.T) {
	f := newForcedServer(t)
	for _, query := range []string{"?methods=GE%20T", "?methods=P0ST", "?methods=,"} {
		if rr := admin(f.port, "POST", "/mcpapi/server/"+f.name+"/status/set/503"+query, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("set with %s = %d %q, want 400", query, rr.Code, rr.Body.String())
		}
	}
	if resp := f.call(nil); resp.code != http.StatusOK {
		t.Errorf("tool call = %d %q, want it served: nothing was set", resp.code, resp.body)
	}
}
