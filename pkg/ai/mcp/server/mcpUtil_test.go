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
	"context"
	"goto/pkg/util"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestGetMCPSessionIDPrefersTheExtendedHeader(t *testing.T) {
	headerWith := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	for name, tt := range map[string]struct {
		headers http.Header
		want    string
	}{
		"none":     {http.Header{}, ""},
		"nil":      {nil, ""},
		"standard": {headerWith(HeaderMCPSessionID, "std"), "std"},
		"extended": {headerWith(HeaderXMCPSessionID, "ext"), "ext"},
		"both":     {headerWith(HeaderXMCPSessionID, "ext", HeaderMCPSessionID, "std"), "ext"},
	} {
		if got := GetMCPSessionID(tt.headers); got != tt.want {
			t.Errorf("%s: GetMCPSessionID = %q, want %q", name, got, tt.want)
		}
	}
}

func TestStoreKeyOfPrefersTheKeyOfTheRequestToTheSession(t *testing.T) {
	h := http.Header{HeaderMCPSessionID: {"session-x"}}
	if got := storeKeyOf(h); got != "session-x" {
		t.Errorf("key = %q, want the session", got)
	}
	h.Set(headerRequestKey, "req-7")
	if got := storeKeyOf(h); got != "req-7" {
		t.Errorf("key = %q, want the key of the request", got)
	}
	if got := storeKeyOf(nil); got != "" {
		t.Errorf("key of no headers = %q", got)
	}
	if a, b := newRequestKey(), newRequestKey(); a == "" || a == b {
		t.Errorf("keys of requests = %q, %q, want them to differ", a, b)
	}
}

func TestSessionStoreIsKeptForTheSessionOfTheRequest(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "store-a", false)
	req := newRequest("POST", s.URI, port, "")
	req.Header.Set(HeaderMCPSessionID, "store-session")
	ms, rs := s.SetMCPSessionStore(req)
	if ms == nil || rs != util.GetRequestStore(req) || ms.RS != rs || ms.Ctx != req.Context() {
		t.Fatalf("store = %+v", ms)
	}
	if got := s.GetMCPSessionStore("store-session"); got != ms {
		t.Errorf("store of the session = %v, want the one that was made", got)
	}
	// The next request of the session updates the store that is there.
	req2 := newRequest("POST", s.URI, port, "")
	req2.Header.Set(HeaderMCPSessionID, "store-session")
	ms2, rs2 := s.SetMCPSessionStore(req2)
	if ms2 != ms || ms.RS != rs2 {
		t.Errorf("the store of the session was replaced instead of updated")
	}
	s.DeleteMCPSessionStore("store-session")
	if s.GetMCPSessionStore("store-session") != nil {
		t.Error("the store of the session was not deleted")
	}
	s.DeleteMCPSessionStore("store-session") // twice is fine
}

func TestCallStoreOfGivesACopyThatIsNotTheStoreOfTheSession(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "store-b", false)
	req := newRequest("POST", s.URI, port, "")
	req.Header.Set(HeaderMCPSessionID, "call-session")
	shared, _ := s.SetMCPSessionStore(req)
	setForcedStatus(shared, 418)

	gotShared, call := s.callStoreOf("call-session", context.Background())
	if gotShared != shared {
		t.Error("the shared store is not the one of the session")
	}
	if call == shared || call.RS != shared.RS || call.Ctx != shared.Ctx {
		t.Errorf("call store = %+v", call)
	}
	// The call doesn't inherit what a request of the session forced.
	if call.ForcedStatus != 0 {
		t.Errorf("forced status of the call = %d, want none", call.ForcedStatus)
	}
}

func TestCallStoreOfWithoutASessionIsMadeFromTheContext(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "store-c", false)
	req := newRequest("POST", s.URI, port, "")
	shared, call := s.callStoreOf("no-such-session", req.Context())
	if shared != nil {
		t.Errorf("shared = %v, want none", shared)
	}
	if call == nil || call.Ctx != req.Context() || call.RS != util.GetRequestStore(req) {
		t.Errorf("call store = %+v", call)
	}
	// And from a context that has nothing, which is a call that has no request behind it.
	mustNotPanic(t, "a store from a context that has nothing", func() {
		if _, call := s.callStoreOf("no-such-session", context.Background()); call == nil {
			t.Error("no call store")
		}
	})
}

func TestForcedStatusIsSafeForConcurrentUse(t *testing.T) {
	ms := &util.MCPRequestStore{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				setForcedStatus(ms, 400+w)
				if got := getForcedStatus(ms); got < 400 || got > 407 {
					t.Errorf("status = %d, want one that was set", got)
					return
				}
			}
		}(w)
	}
	waitFor(t, &wg, 30*time.Second)
}

func TestSessionContextsAreKeptByTheServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "session-a", false)
	if s.getOrSetSessionContext("") != nil {
		t.Error("a request without a session has a session context")
	}
	a := s.getOrSetSessionContext("s1")
	if a == nil || a.SessionID != "s1" || a.Server != s {
		t.Fatalf("context = %+v", a)
	}
	if s.getOrSetSessionContext("s1") != a || s.getSessionContext("s1") != a {
		t.Error("the context of the session was not kept")
	}
	custom := &SessionContext{SessionID: "s2", finished: make(chan bool, 1)}
	s.setSessionContext("s2", custom)
	if s.getSessionContext("s2") != custom {
		t.Error("the context that was set is not the one that is kept")
	}
	if got := s.getOrSetSessionContext("s2"); got != custom || got.Server != s {
		t.Errorf("the context that was set was replaced, or not given its server: %+v", got)
	}
	s.removeSessionContext("s1")
	if s.getSessionContext("s1") != nil {
		t.Error("the context was not removed")
	}
}

// The key of a request is the one that the server gives it, not one that a client sends to get at the store of another
// request or of a session.
func TestServeIgnoresTheRequestKeyThatTheClientSends(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "spoof-a", false)
	victim := s.GetMCPSessionStore("victim-key")
	if victim != nil {
		t.Fatal("the store of the victim is there before it was made")
	}
	seen := make(chan string, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Get(headerRequestKey) })
	for _, method := range []string{"GET", "DELETE", "POST"} {
		req := newRequest(method, s.URI, port, "")
		req.Header.Set(headerRequestKey, "victim-key")
		req.Header.Set(HeaderMCPSessionID, "spoof-session")
		Serve(s, httptest.NewRecorder(), req, handler)
		select {
		case got := <-seen:
			if got == "victim-key" {
				t.Errorf("%s: the handler got the key that the client sent", method)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the handler was not called", method)
		}
		if s.GetMCPSessionStore("victim-key") != nil {
			t.Errorf("%s: a store was made for the key that the client sent", method)
		}
	}
}
