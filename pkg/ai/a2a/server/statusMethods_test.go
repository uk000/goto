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

package a2aserver

import (
	"net/http"
	"strings"
	"testing"

	"goto/pkg/constants"
)

// setStatusFor sets a forced status for the agent with the methods that it is for, given in the query of the call.
func (ts *testServer) setStatusFor(name, spec, query string) *rawResponse {
	ts.t.Helper()
	return ts.request("POST", ts.agentPath(name, "/status/set/"+spec)+query, "")
}

func (ts *testServer) callAgent(method, name string) *rawResponse {
	ts.t.Helper()
	if method == "GET" {
		return ts.getCard(name)
	}
	return ts.request(method, "/agent/"+name+"/", "")
}

func requireNotForced(t *testing.T, resp *rawResponse) {
	t.Helper()
	if resp.Header.Get(constants.HeaderGotoForcedStatus) != "" || resp.Code == http.StatusServiceUnavailable {
		t.Errorf("response = %d %v, want it not forced", resp.Code, resp.Header)
	}
}

func TestForcedStatusOfGETRequests(t *testing.T) {
	ts := newTestServer(t)
	name, agent := ts.add("getstatus", false, nil, nil)
	calls := countCalls(agent)
	if resp := ts.setStatusFor(name, "503", "?methods=GET"); resp.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", resp.Code, resp.Body)
	}
	// The card is fetched with GET, and is given the status. The agent is not called for it.
	requireForced(t, ts.getCard(name), 503, "-1")
	requireForced(t, ts.getCard(name), 503, "-1")
	if n := calls.Load(); n != 0 {
		t.Errorf("agent served %d requests, want none while the card has a forced status", n)
	}
	// Calls of the agent are POST, which the status is not for.
	requireServed(t, ts.post(name))
}

func TestForcedStatusOfSeveralMethods(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("severalmethods", false, nil, nil)
	if resp := ts.setStatusFor(name, "503:3", "?methods=get,Post"); resp.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", resp.Code, resp.Body)
	}
	requireForced(t, ts.getCard(name), 503, "2")
	requireForced(t, ts.post(name), 503, "1")
	requireForced(t, ts.getCard(name), 503, "0")
	requireServed(t, ts.post(name))
	requireNotForced(t, ts.getCard(name))
}

func TestForcedStatusOfAllMethods(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("allmethods", false, nil, nil)
	if resp := ts.setStatusFor(name, "502", "?methods=*"); resp.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", resp.Code, resp.Body)
	}
	requireForced(t, ts.getCard(name), 502, "-1")
	requireForced(t, ts.post(name), 502, "-1")
	if resp := ts.callAgent("DELETE", name); resp.Header.Get(constants.HeaderGotoForcedStatus) != "502" {
		t.Errorf("DELETE = %d %v, want the forced 502", resp.Code, resp.Header)
	}
}

// The statuses that are set for GET and for POST of an agent are two, each with its own count, and the one for POST is
// still what a status is set for without the methods.
func TestForcedStatusOfGETAndOfPOSTAreSeparate(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("getandpost", false, nil, nil)
	ts.setStatus(name, "502:1")
	if resp := ts.setStatusFor(name, "503:2", "?methods=GET"); resp.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", resp.Code, resp.Body)
	}
	requireForced(t, ts.getCard(name), 503, "1")
	requireForced(t, ts.post(name), 502, "0")
	requireForced(t, ts.getCard(name), 503, "0")
	requireServed(t, ts.post(name))
	requireNotForced(t, ts.getCard(name))

	// Setting the status for GET again changes the one that there was, it doesn't add one.
	ts.setStatusFor(name, "504:1", "?methods=get")
	ts.setStatusFor(name, "429:1", "?methods=GET")
	requireForced(t, ts.getCard(name), 429, "0")
	requireNotForced(t, ts.getCard(name))
}

func TestForcedStatusWithInvalidMethodsIsRejected(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("badmethods", false, nil, nil)
	for _, query := range []string{"?methods=GE%20T", "?methods=P0ST", "?methods=GET%3BPOST", "?methods=,"} {
		resp := ts.setStatusFor(name, "503", query)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "Invalid methods") {
			t.Errorf("set with %s = %d: %s, want a 400 for the methods", query, resp.Code, resp.Body)
		}
	}
	requireServed(t, ts.post(name))
	requireNotForced(t, ts.getCard(name))
}
