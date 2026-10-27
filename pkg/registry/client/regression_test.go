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

package registryclient

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A body that is not JSON object was turned into an empty object, and reported as if it was what the locker had.
func TestRegressionGetJSONReportsBodyThatIsNotJSON(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	fr.respond("/registry/lockers/l1/get/text", http.StatusOK, "plain text")
	fr.respond("/registry/lockers/l1/get/array", http.StatusOK, `[1,2]`)
	fr.respond("/registry/peers/peer1/10.1.2.3:8080/locker/get/text", http.StatusOK, "plain text")
	if j, err := l.GetJSON([]string{"text"}); err == nil {
		t.Errorf("GetJSON of text = %v, nil, want an error", j)
	}
	if j, err := l.GetJSON([]string{"array"}); err == nil {
		t.Errorf("GetJSON of an array = %v, nil, want an error", j)
	}
	if j, err := l.GetPeerJSON([]string{"text"}); err == nil {
		t.Errorf("GetPeerJSON of text = %v, nil, want an error", j)
	}
}

// The responses with an error status were not closed, so every one of them used a new connection.
func TestRegressionFailedGetsReuseTheConnection(t *testing.T) {
	var newConns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/open") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, strings.Repeat("not found ", 20))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	configure(t, srv.URL, true)
	l, err := NewRegistryClient().OpenLocker("", "l1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := l.GetData([]string{"missing"}); err == nil {
			t.Fatal("expected an error for the missing key")
		}
	}
	if n := newConns.Load(); n != 1 {
		t.Errorf("%d connections were made by 9 requests, want 1", n)
	}
}
