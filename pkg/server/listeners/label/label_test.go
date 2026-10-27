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

package label

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	setupOnce sync.Once
	// Labels are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

type env struct {
	port int
}

// newEnv sets up the routes, which are registered on a global router and so only once, and takes a port that nothing
// else uses. What the tests may change of the default listener is put back when they are done.
func newEnv(t *testing.T) *env {
	t.Helper()
	setupOnce.Do(func() {
		stubGlobalFuncs()
		Middleware.SetRoutes(nil)
	})
	e := &env{port: int(43000 + portSeq.Add(1))}
	label, defaultLabel, serverPort := listeners.DefaultListener.Label, listeners.DefaultLabel, global.Self.ServerPort
	t.Cleanup(func() {
		listeners.DefaultListener.Label, listeners.DefaultLabel, global.Self.ServerPort = label, defaultLabel, serverPort
	})
	return e
}

// withListener adds a listener, that is not opened, for the port of the env.
func (e *env) withListener(t *testing.T, label string) *listeners.Listener {
	t.Helper()
	l := &listeners.Listener{Port: e.port, Protocol: "http", Label: label}
	if err, msg := listeners.AddOrUpdateListener(l); err != nil {
		t.Fatalf("cannot add the listener: %v %s", err, msg)
	}
	t.Cleanup(func() { listeners.RemoveListener(l) })
	return l
}

// asServerPort makes the port of the env the one of the server, which has no listener of its own but the default one.
func (e *env) asServerPort() {
	global.Self.ServerPort = e.port
}

func (e *env) call(method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	middleware.RootRouters["/server"].ServeHTTP(rr, req)
	return rr
}

func (e *env) get(t *testing.T) string {
	t.Helper()
	rr := e.call("GET", "/server/label")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /server/label = %d %q, want 200", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func TestGetLabelOfAListenerGivesItsLabel(t *testing.T) {
	e := newEnv(t)
	e.withListener(t, "my-listener")
	if got, want := e.get(t), "Server Label: my-listener\n"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}

func TestGetLabelOfAListenerWithoutOneGivesTheDefaultLabelOfThePort(t *testing.T) {
	e := newEnv(t)
	if got, want := e.get(t), "Server Label: "+util.BuildListenerLabel(e.port)+"\n"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}

func TestGetLabelOfTheServerPortGivesTheLabelOfTheDefaultListener(t *testing.T) {
	e := newEnv(t)
	e.asServerPort()
	listeners.DefaultListener.Label = ""
	if got, want := e.get(t), "Server Label: "+global.Self.HostLabel+"\n"; got != want {
		t.Errorf("label = %q, want the label of the host when the default listener has none: %q", got, want)
	}
	listeners.DefaultListener.Label = "the-default"
	if got, want := e.get(t), "Server Label: the-default\n"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}

func TestSetLabelOfAListener(t *testing.T) {
	e := newEnv(t)
	l := e.withListener(t, "before")
	for _, method := range []string{"PUT", "POST"} {
		label := "after-" + strings.ToLower(method)
		rr := e.call(method, "/server/label/set/"+label)
		if want := fmt.Sprintf("Will use label %s for all responses on port %d\n", label, e.port); rr.Code != http.StatusOK || rr.Body.String() != want {
			t.Errorf("%s: response = %d %q, want 200 %q", method, rr.Code, rr.Body.String(), want)
		}
		if got := listeners.GetListenerLabelForPort(e.port); got != label || l.Label != label {
			t.Errorf("%s: label of the listener = %q (%q), want %q", method, got, l.Label, label)
		}
		if got, want := e.get(t), "Server Label: "+label+"\n"; got != want {
			t.Errorf("%s: label = %q, want %q", method, got, want)
		}
	}
}

func TestSetLabelDoesNotChangeTheLabelOfOtherListeners(t *testing.T) {
	e := newEnv(t)
	other := &env{port: e.port + 5000}
	e.withListener(t, "first")
	otherListener := &listeners.Listener{Port: other.port, Protocol: "http", Label: "second"}
	if err, msg := listeners.AddOrUpdateListener(otherListener); err != nil {
		t.Fatalf("cannot add the listener: %v %s", err, msg)
	}
	t.Cleanup(func() { listeners.RemoveListener(otherListener) })

	e.call("POST", "/server/label/set/changed")
	if got := listeners.GetListenerLabelForPort(other.port); got != "second" {
		t.Errorf("label of the other listener = %q, want it as it was", got)
	}
}

func TestSetLabelOfThePortOfTheServerSetsTheDefaultLabel(t *testing.T) {
	e := newEnv(t)
	e.asServerPort()
	rr := e.call("POST", "/server/label/set/server-label")
	if want := fmt.Sprintf("Will use label server-label for all responses on port %d\n", e.port); rr.Code != http.StatusOK || rr.Body.String() != want {
		t.Errorf("response = %d %q, want 200 %q", rr.Code, rr.Body.String(), want)
	}
	if listeners.DefaultListener.Label != "server-label" || listeners.DefaultLabel != "server-label" {
		t.Errorf("labels of the default listener = %q, %q, want the label that was set", listeners.DefaultListener.Label, listeners.DefaultLabel)
	}
	if got, want := e.get(t), "Server Label: server-label\n"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}

func TestSetLabelWithoutALabelIsNotARoute(t *testing.T) {
	e := newEnv(t)
	e.withListener(t, "kept")
	for _, target := range []string{"/server/label/set", "/server/label/set/"} {
		if rr := e.call("POST", target); rr.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d %q, want 404", target, rr.Code, rr.Body.String())
		}
	}
	if got := listeners.GetListenerLabelForPort(e.port); got != "kept" {
		t.Errorf("label = %q, want it as it was", got)
	}
}

func TestLabelRoutesOnlyAnswerTheirMethods(t *testing.T) {
	e := newEnv(t)
	e.withListener(t, "kept")
	for _, tt := range []struct{ method, target string }{
		{"GET", "/server/label/set/other"},
		{"DELETE", "/server/label/set/other"},
		{"GET", "/server/label/clear"},
		{"PUT", "/server/label/clear"},
	} {
		if rr := e.call(tt.method, tt.target); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d %q, want it refused", tt.method, tt.target, rr.Code, rr.Body.String())
		}
	}
	if got := listeners.GetListenerLabelForPort(e.port); got != "kept" {
		t.Errorf("label = %q, want it as it was", got)
	}
}

func TestClearLabelOfAListener(t *testing.T) {
	e := newEnv(t)
	l := e.withListener(t, "to-clear")
	rr := e.call("POST", "/server/label/clear")
	if rr.Code != http.StatusOK {
		t.Errorf("clear = %d %q, want 200", rr.Code, rr.Body.String())
	}
	if l.Label != "" {
		t.Errorf("label of the listener = %q, want it cleared", l.Label)
	}
}

func TestSetThenClearThenSetAgain(t *testing.T) {
	e := newEnv(t)
	e.withListener(t, "start")
	steps := []struct{ method, target, want string }{
		{"POST", "/server/label/set/one", "one"},
		{"POST", "/server/label/clear", ""},
		{"PUT", "/server/label/set/two", "two"},
	}
	for _, s := range steps {
		if rr := e.call(s.method, s.target); rr.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %q, want 200", s.method, s.target, rr.Code, rr.Body.String())
		}
		if got := listeners.GetListenerLabelForPort(e.port); got != s.want {
			t.Errorf("after %s %s: label = %q, want %q", s.method, s.target, got, s.want)
		}
	}
}

func TestLabelOfAPortWithoutAListenerIsNotTheLabelOfAnotherPort(t *testing.T) {
	e := newEnv(t)
	e.withListener(t, "of-the-port")
	// The request is made on a port that has no listener, while another port has one.
	other := &env{port: e.port + 7000}
	req := httptest.NewRequest("GET", "/server/label", nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, other.port))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	middleware.RootRouters["/server"].ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "of-the-port") {
		t.Errorf("label of another port = %d %q, want the label of that port", rr.Code, rr.Body.String())
	}
	if want := "Server Label: " + util.BuildListenerLabel(other.port) + "\n"; rr.Body.String() != want {
		t.Errorf("label of another port = %q, want %q", rr.Body.String(), want)
	}
}
