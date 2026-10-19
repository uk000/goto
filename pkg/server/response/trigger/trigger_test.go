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

package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	// api serves the admin routes, mounted where the server mounts them: under /server/response.
	api *mux.Router
	// Triggers are kept per port, and targets are known by their names, so every test uses a port and names of its own.
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

// Routes are registered on global routers, so they are registered only once, and tests don't run in parallel. The
// routers of the server exist when the targets are added, so that is how the tests are set up too.
func setupRoutes() {
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() {
		util.CreateRouters(mux.NewRouter())
		api = mux.NewRouter()
		Middleware.SetRoutes(api.PathPrefix("/server/response").Subrouter())
	})
}

type testEnv struct {
	port int
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	setupRoutes()
	e := &testEnv{port: int(43000 + portSeq.Add(1))}
	t.Cleanup(func() {
		triggerLock.Lock()
		trigger := portTriggers[strconv.Itoa(e.port)]
		delete(portTriggers, strconv.Itoa(e.port))
		triggerLock.Unlock()
		if trigger != nil {
			trigger.lock.RLock()
			targets := map[string]*TriggerTarget{}
			for name, tt := range trigger.Targets {
				targets[name] = tt
			}
			trigger.lock.RUnlock()
			triggerLock.Lock()
			for name, tt := range targets {
				if allTriggerTargets[name] == tt {
					delete(allTriggerTargets, name)
				}
			}
			triggerLock.Unlock()
		}
	})
	return e
}

// name makes a target name that is not used by any other test.
func (e *testEnv) name(s string) string { return fmt.Sprintf("p%d-%s", e.port, s) }

func (e *testEnv) newRequest(method, target, body string, headers ...string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func (e *testEnv) call(method, target, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, e.newRequest(method, target, body))
	return rr
}

// mustCall expects the admin API to answer with the given status, and returns the body.
func (e *testEnv) mustCall(t *testing.T, method, target, body string, status int) string {
	t.Helper()
	rr := e.call(method, target, body)
	if rr.Code != status {
		t.Fatalf("%s %s = %d %q, want %d", method, target, rr.Code, rr.Body.String(), status)
	}
	return rr.Body.String()
}

func (e *testEnv) add(t *testing.T, spec map[string]any) {
	t.Helper()
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	e.mustCall(t, "POST", "/server/response/triggers/add", string(body), http.StatusOK)
}

// targetSpec is the JSON of a target that calls the given URL, with the settings given to override or add to it.
func targetSpec(name, url string, settings map[string]any) map[string]any {
	spec := map[string]any{
		"name":       name,
		"httpTarget": map[string]any{"method": "POST", "url": url},
		"enabled":    true,
	}
	for k, v := range settings {
		spec[k] = v
	}
	return spec
}

// pipeSpec is the JSON of a target that has no HTTP target and is piped to the callbacks that register for it.
func pipeSpec(name string, settings map[string]any) map[string]any {
	spec := map[string]any{"name": name, "pipe": true, "enabled": true}
	for k, v := range settings {
		spec[k] = v
	}
	return spec
}

type targetView struct {
	Name            string            `json:"name"`
	Enabled         bool              `json:"enabled"`
	Pipe            bool              `json:"pipe"`
	TriggerURIs     []string          `json:"triggerURIs"`
	TriggerHeaders  map[string]string `json:"triggerHeaders"`
	TriggerStatuses []int             `json:"triggerStatuses"`
	StartFrom       int               `json:"startFrom"`
	StopAt          int               `json:"stopAt"`
	MatchCount      int               `json:"matchCount"`
	TriggerCount    int               `json:"triggerCount"`
}

type triggersView struct {
	Targets          map[string]targetView          `json:"targets"`
	TargetsByURIs    map[string][]string            `json:"targetsByURIs"`
	TargetsByHeaders map[string]map[string][]string `json:"targetsByHeaders"`
	TargetsByStatus  map[string][]string            `json:"targetsByStatus"`
	TriggerResults   map[string]map[string]int      `json:"triggerResults"`
}

// view reads the triggers of the port through the API.
func (e *testEnv) view(t *testing.T) triggersView {
	t.Helper()
	body := e.mustCall(t, "GET", "/server/response/triggers", "", http.StatusOK)
	var v triggersView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("GET /triggers returned %q, which is not the JSON of triggers: %v", body, err)
	}
	return v
}

func (e *testEnv) counts(t *testing.T) map[string]map[string]int {
	t.Helper()
	body := e.mustCall(t, "GET", "/server/response/triggers/counts", "", http.StatusOK)
	counts := map[string]map[string]int{}
	if err := json.Unmarshal([]byte(body), &counts); err != nil {
		t.Fatalf("GET /triggers/counts returned %q, which is not JSON: %v", body, err)
	}
	return counts
}

// fired returns the names of the targets that a response makes the port invoke, and the ones that it makes the port
// pipe, without invoking anything.
func (e *testEnv) fired(uri string, status int, headers ...string) (httpTargets, pipeSources []string) {
	req := e.newRequest("GET", uri, "", headers...)
	h, p := getPortTrigger(req).findMatchingTriggers(req, status)
	for name := range h {
		httpTargets = append(httpTargets, name)
	}
	for name := range p {
		pipeSources = append(pipeSources, name)
	}
	sort.Strings(httpTargets)
	sort.Strings(pipeSources)
	return
}

func expectNames(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	got = append([]string{}, got...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// eventually waits for something that happens in the background.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type hit struct {
	Method string
	Path   string
	Query  string
	Body   string
	Header http.Header
}

// targetServer is a server on a port of its own that the triggers call. It records the requests that it gets.
type targetServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []hit
	status atomic.Int32
}

func newTargetServer(t *testing.T) *targetServer {
	t.Helper()
	ts := &targetServer{}
	ts.status.Store(http.StatusOK)
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts.mu.Lock()
		ts.hits = append(ts.hits, hit{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body), Header: r.Header.Clone()})
		ts.mu.Unlock()
		w.WriteHeader(int(ts.status.Load()))
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *targetServer) url(path string) string { return ts.srv.URL + path }

func (ts *targetServer) count() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.hits)
}

func (ts *targetServer) waitFor(t *testing.T, n int) []hit {
	t.Helper()
	eventually(t, fmt.Sprintf("%d calls of the target server, which has %d", n, ts.count()), func() bool { return ts.count() >= n })
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]hit{}, ts.hits...)
}

// Adding targets

func TestAddTriggerListsTargetAndItsIndexes(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	byStatus, byHeader, byURI := e.name("status"), e.name("header"), e.name("uri")
	e.add(t, targetSpec(byStatus, ts.url("/s"), map[string]any{"triggerStatuses": []int{500, 503}, "startFrom": 2, "stopAt": 5}))
	e.add(t, targetSpec(byHeader, ts.url("/h"), map[string]any{"triggerHeaders": map[string]string{"X-Env": "prod"}}))
	e.add(t, targetSpec(byURI, ts.url("/u"), map[string]any{"triggerURIs": []string{"/API/Orders"}, "enabled": false}))
	v := e.view(t)
	if len(v.Targets) != 3 {
		t.Fatalf("targets = %v, want the 3 that were added", v.Targets)
	}
	if s := v.Targets[byStatus]; s.Name != byStatus || !s.Enabled || s.StartFrom != 2 || s.StopAt != 5 || !reflect.DeepEqual(s.TriggerStatuses, []int{500, 503}) {
		t.Errorf("status target = %+v, want the settings that it was added with", s)
	}
	if v.Targets[byURI].Enabled {
		t.Errorf("target that was added as disabled is enabled")
	}
	expectNames(t, "targets of status 500", v.TargetsByStatus["500"], byStatus)
	expectNames(t, "targets of status 503", v.TargetsByStatus["503"], byStatus)
	expectNames(t, "targets of header X-Env=prod", v.TargetsByHeaders["X-Env"]["prod"], byHeader)
	// The URIs are listed in lower case, since requests are matched in lower case.
	expectNames(t, "targets of the URI", v.TargetsByURIs["/api/orders"], byURI)
	if len(v.TargetsByURIs) != 1 || len(v.TargetsByStatus) != 2 || len(v.TargetsByHeaders) != 1 {
		t.Errorf("indexes = %v %v %v, want one entry for each of the targets that were added", v.TargetsByURIs, v.TargetsByStatus, v.TargetsByHeaders)
	}
}

func TestAddTriggerMessage(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("msg")
	msg := e.mustCall(t, "POST", "/server/response/triggers/add", fmt.Sprintf(`{"name":%q,"pipe":true}`, name), http.StatusOK)
	if !strings.Contains(msg, "Added trigger: "+name) {
		t.Errorf("message = %q, want it to name the trigger", msg)
	}
}

func TestAddTriggerListsTargetByURIOnlyWhenItHasURIs(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("uri")
	e.add(t, pipeSpec(name, map[string]any{
		"triggerURIs": []string{"/a", "/b/{id}"}, "triggerStatuses": []int{500}, "triggerHeaders": map[string]string{"X-A": "a"},
	}))
	v := e.view(t)
	if len(v.TargetsByURIs) != 2 || len(v.TargetsByStatus) != 0 || len(v.TargetsByHeaders) != 0 {
		t.Errorf("indexes = %v %v %v, want the target only listed by its URIs, which are matched along with its statuses and headers",
			v.TargetsByURIs, v.TargetsByStatus, v.TargetsByHeaders)
	}
}

func TestAddTriggerRejectsInvalidTriggers(t *testing.T) {
	e := newTestEnv(t)
	for name, body := range map[string]string{
		"not json":         `not json`,
		"wrong field type": `{"name": 5}`,
		"no name":          `{"pipe": true}`,
		"no url":           `{"name":"x","httpTarget":{"method":"GET"}}`,
		"invalid URI":      `{"name":"x","pipe":true,"triggerURIs":["/a/{b"]}`,
	} {
		rr := e.call("POST", "/server/response/triggers/add", body)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid trigger") {
			t.Errorf("%s: POST /triggers/add = %d %q, want 400 Invalid trigger", name, rr.Code, rr.Body.String())
		}
	}
	if v := e.view(t); len(v.Targets) != 0 {
		t.Errorf("targets = %v after invalid triggers, want none", v.Targets)
	}
}

func TestTriggerRoutesRefuseOtherMethods(t *testing.T) {
	e := newTestEnv(t)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/server/response/triggers/add"},
		{"PUT", "/server/response/triggers/add"},
		{"GET", "/server/response/triggers/x/remove"},
		{"GET", "/server/response/triggers/x/enable"},
		{"GET", "/server/response/triggers/x/disable"},
		{"GET", "/server/response/triggers/x/invoke"},
		{"GET", "/server/response/triggers/clear"},
	} {
		if rr := e.call(tc.method, tc.target, ""); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want it to be refused", tc.method, tc.target, rr.Code)
		}
	}
}

func TestAddTriggerWithSameNameReplacesTarget(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("replaced")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500}}))
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/x"}, "enabled": false}))
	v := e.view(t)
	if len(v.Targets) != 1 || v.Targets[name].Enabled || len(v.TargetsByStatus) != 0 {
		t.Errorf("triggers = %+v, want only the second target, with nothing left of the first", v)
	}
	expectNames(t, "targets of the URI", v.TargetsByURIs["/x"], name)
}

func TestTriggersAreKeptPerPort(t *testing.T) {
	a, b := newTestEnv(t), newTestEnv(t)
	nameA, nameB := a.name("t"), b.name("t")
	a.add(t, pipeSpec(nameA, nil))
	b.add(t, pipeSpec(nameB, nil))
	if v := a.view(t); len(v.Targets) != 1 || v.Targets[nameA].Name == "" {
		t.Errorf("targets of A = %v, want only its own", v.Targets)
	}
	b.mustCall(t, "POST", "/server/response/triggers/clear", "", http.StatusOK)
	if v := a.view(t); len(v.Targets) != 1 {
		t.Errorf("targets of A = %v after B cleared its own, want them to stay", v.Targets)
	}
}

// Removing, enabling and disabling

func TestRemoveTrigger(t *testing.T) {
	e := newTestEnv(t)
	keep, remove := e.name("keep"), e.name("remove")
	e.add(t, pipeSpec(keep, map[string]any{"triggerStatuses": []int{500}}))
	e.add(t, pipeSpec(remove, map[string]any{"triggerStatuses": []int{500, 502}}))
	msg := e.mustCall(t, "POST", "/server/response/triggers/"+remove+"/remove", "", http.StatusOK)
	if !strings.Contains(msg, "Trigger Removed: "+remove) {
		t.Errorf("message = %q, want it to name the trigger", msg)
	}
	v := e.view(t)
	if _, found := v.Targets[remove]; found || len(v.Targets) != 1 {
		t.Errorf("targets = %v, want only %s", v.Targets, keep)
	}
	expectNames(t, "targets of status 500", v.TargetsByStatus["500"], keep)
	if len(v.TargetsByStatus["502"]) != 0 {
		t.Errorf("targets of status 502 = %v, want none", v.TargetsByStatus["502"])
	}
	// The target is not known to pipes anymore either.
	triggerLock.RLock()
	_, known := allTriggerTargets[remove]
	triggerLock.RUnlock()
	if known {
		t.Errorf("target %s is still known after it was removed", remove)
	}
}

func TestRemoveTriggerAcceptsPut(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("put")
	e.add(t, pipeSpec(name, nil))
	e.mustCall(t, "PUT", "/server/response/triggers/"+name+"/remove", "", http.StatusOK)
	if v := e.view(t); len(v.Targets) != 0 {
		t.Errorf("targets = %v, want none", v.Targets)
	}
}

func TestRemoveUnknownTrigger(t *testing.T) {
	e := newTestEnv(t)
	rr := e.call("POST", "/server/response/triggers/"+e.name("unknown")+"/remove", "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No triggers") {
		t.Errorf("removing an unknown trigger = %d %q, want 400 No triggers", rr.Code, rr.Body.String())
	}
}

func TestEnableAndDisableTrigger(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("toggle")
	e.add(t, pipeSpec(name, map[string]any{"enabled": false}))
	msg := e.mustCall(t, "POST", "/server/response/triggers/"+name+"/enable", "", http.StatusOK)
	if !strings.Contains(msg, "enabled") || !e.view(t).Targets[name].Enabled {
		t.Errorf("after enabling: message %q, enabled %v, want it enabled", msg, e.view(t).Targets[name].Enabled)
	}
	msg = e.mustCall(t, "PUT", "/server/response/triggers/"+name+"/disable", "", http.StatusOK)
	if !strings.Contains(msg, "disabled") || e.view(t).Targets[name].Enabled {
		t.Errorf("after disabling: message %q, enabled %v, want it disabled", msg, e.view(t).Targets[name].Enabled)
	}
	e.mustCall(t, "PUT", "/server/response/triggers/"+name+"/enable/", "", http.StatusOK)
	if !e.view(t).Targets[name].Enabled {
		t.Errorf("target is not enabled by the call with a trailing slash")
	}
}

func TestEnableAndDisableUnknownTrigger(t *testing.T) {
	e := newTestEnv(t)
	for _, action := range []string{"enable", "disable"} {
		rr := e.call("POST", "/server/response/triggers/"+e.name("unknown")+"/"+action, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "not found") {
			t.Errorf("%s of an unknown trigger = %d %q, want 400 not found", action, rr.Code, rr.Body.String())
		}
	}
}

func TestClearTriggers(t *testing.T) {
	e := newTestEnv(t)
	first, second := e.name("first"), e.name("second")
	e.add(t, pipeSpec(first, map[string]any{"triggerStatuses": []int{500}}))
	e.add(t, pipeSpec(second, map[string]any{"triggerURIs": []string{"/x"}}))
	msg := e.mustCall(t, "POST", "/server/response/triggers/clear", "", http.StatusOK)
	if !strings.Contains(msg, "Cleared") {
		t.Errorf("message = %q, want it to say that triggers are cleared", msg)
	}
	v := e.view(t)
	if len(v.Targets) != 0 || len(v.TargetsByStatus) != 0 || len(v.TargetsByURIs) != 0 {
		t.Errorf("triggers = %+v after clear, want none", v)
	}
	triggerLock.RLock()
	_, known := allTriggerTargets[first]
	triggerLock.RUnlock()
	if known {
		t.Errorf("target %s is still known after clear", first)
	}
	// Clearing again works, and so does adding after clearing.
	e.mustCall(t, "POST", "/server/response/triggers/clear", "", http.StatusOK)
	e.add(t, pipeSpec(first, nil))
	if v := e.view(t); len(v.Targets) != 1 {
		t.Errorf("targets = %v, want the one added after clear", v.Targets)
	}
}

func TestClearTriggersOfPortWithoutTriggers(t *testing.T) {
	e := newTestEnv(t)
	e.mustCall(t, "POST", "/server/response/triggers/clear", "", http.StatusOK)
	if v := e.view(t); len(v.Targets) != 0 {
		t.Errorf("targets = %v, want none", v.Targets)
	}
}

// Invoking targets

func TestInvokeTriggerCallsTarget(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("invoke")
	e.add(t, map[string]any{
		"name": name,
		"httpTarget": map[string]any{
			"method": "POST", "url": ts.url("/invoked?a=1"), "body": "payload",
			"headers": map[string]string{"X-Static": "s", "X-Uri": "{request.uri}", "X-Req-Headers": "{request.headers}", "X-Unknown": "{no-such-header}"},
		},
	})
	req := e.newRequest("POST", "/server/response/triggers/"+name+"/invoke", "", "X-Probe", "probe")
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("invoke = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var results []struct {
		TargetName string `json:"targetName"`
		Response   struct {
			StatusCode int `json:"statusCode"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("invoke returned %q, which is not JSON: %v", rr.Body.String(), err)
	}
	if len(results) != 1 || results[0].TargetName != name || results[0].Response.StatusCode != http.StatusOK {
		t.Errorf("results = %+v, want one result of 200 for the target", results)
	}
	hits := ts.waitFor(t, 1)
	if len(hits) != 1 {
		t.Fatalf("target server calls = %d, want 1", len(hits))
	}
	h := hits[0]
	if h.Method != "POST" || h.Path != "/invoked" || h.Query != "a=1" || h.Body != "payload" {
		t.Errorf("call = %+v, want POST /invoked?a=1 with the body of the target", h)
	}
	if h.Header.Get("X-Static") != "s" || h.Header.Get("X-Uri") != "/server/response/triggers/"+name+"/invoke" {
		t.Errorf("headers = %v, want the static header and the URI of the request that invoked the target", h.Header)
	}
	if !strings.Contains(h.Header.Get("X-Req-Headers"), "X-Probe") {
		t.Errorf("X-Req-Headers = %q, want the headers of the request", h.Header.Get("X-Req-Headers"))
	}
	if got := h.Header.Get("X-Unknown"); got != "{no-such-header}" {
		t.Errorf("X-Unknown = %q, want a header that names no response header to be sent as it is", got)
	}
	if got := e.counts(t)[name]; !reflect.DeepEqual(got, map[string]int{"200": 1}) {
		t.Errorf("counts = %v, want one response of 200", got)
	}
	if got := e.view(t).Targets[name].TriggerCount; got != 1 {
		t.Errorf("trigger count = %d, want 1", got)
	}
}

func TestInvokeTriggerIgnoresEnabledFlag(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("disabled")
	e.add(t, targetSpec(name, ts.url("/x"), map[string]any{"enabled": false}))
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	ts.waitFor(t, 1)
}

func TestInvokeSeveralTriggers(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	a, b, c := e.name("a"), e.name("b"), e.name("c")
	for _, name := range []string{a, b, c} {
		e.add(t, targetSpec(name, ts.url("/"+name), nil))
	}
	e.mustCall(t, "POST", "/server/response/triggers/"+a+","+c+",missing/invoke", "", http.StatusOK)
	paths := map[string]bool{}
	for _, h := range ts.waitFor(t, 2) {
		paths[h.Path] = true
	}
	if len(paths) != 2 || !paths["/"+a] || !paths["/"+c] || paths["/"+b] {
		t.Errorf("paths called = %v, want the targets that were asked for and exist", paths)
	}
	if ts.count() != 2 {
		t.Errorf("target server calls = %d, want 2", ts.count())
	}
}

func TestInvokeUnknownTriggers(t *testing.T) {
	e := newTestEnv(t)
	rr := e.call("POST", "/server/response/triggers/"+e.name("x")+","+e.name("y")+"/invoke", "")
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "not found") {
		t.Errorf("invoke of unknown triggers = %d %q, want 404 not found", rr.Code, rr.Body.String())
	}
}

func TestInvokeTriggerOfUnreachableTargetCountsAsUnavailable(t *testing.T) {
	e := newTestEnv(t)
	// The server is closed at once, so its port refuses connections.
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	name := e.name("unreachable")
	e.add(t, targetSpec(name, url+"/x", nil))
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	if got := e.counts(t)[name]; !reflect.DeepEqual(got, map[string]int{"503": 1}) {
		t.Errorf("counts = %v, want the failed call to count as 503", got)
	}
}

func TestInvokeTriggerCountsTargetStatuses(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("statuses")
	e.add(t, targetSpec(name, ts.url("/x"), nil))
	ts.status.Store(http.StatusOK)
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	ts.status.Store(http.StatusTeapot)
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	if got := e.counts(t)[name]; !reflect.DeepEqual(got, map[string]int{"200": 2, "418": 1}) {
		t.Errorf("counts = %v, want 2 responses of 200 and 1 of 418", got)
	}
	if got := e.view(t).TriggerResults[name]; !reflect.DeepEqual(got, map[string]int{"200": 2, "418": 1}) {
		t.Errorf("results in the list of triggers = %v, want the same counts", got)
	}
}

func TestCountsWithoutTriggers(t *testing.T) {
	e := newTestEnv(t)
	if got := e.counts(t); len(got) != 0 {
		t.Errorf("counts = %v, want none", got)
	}
}

// Pipes

type pipeCall struct {
	Target, Source string
	Port           int
	Status         int
	Headers        http.Header
}

func recordPipe(calls chan<- pipeCall) PipeCallback {
	return func(target, source string, port int, r *http.Request, statusCode int, responseHeaders http.Header) {
		calls <- pipeCall{Target: target, Source: source, Port: port, Status: statusCode, Headers: responseHeaders}
	}
}

func TestInvokeTriggerCallsPipeCallbacks(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("pipe")
	e.add(t, pipeSpec(name, nil))
	calls := make(chan pipeCall, 4)
	RegisterPipeCallback(name, "source-a", recordPipe(calls))
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	select {
	case c := <-calls:
		if c.Target != name || c.Source != "source-a" || c.Port != e.port || c.Status != http.StatusOK {
			t.Errorf("callback = %+v, want the target, the source, the port %d and status 200", c, e.port)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("callback was not called")
	}
	if got := e.view(t).Targets[name].TriggerCount; got != 1 {
		t.Errorf("trigger count = %d, want 1", got)
	}
}

func TestPipeCallbackOfSameSourceIsReplaced(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("replaced")
	e.add(t, pipeSpec(name, nil))
	first, second := make(chan pipeCall, 4), make(chan pipeCall, 4)
	RegisterPipeCallback(name, "source", recordPipe(first))
	RegisterPipeCallback(name, "source", recordPipe(second))
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/invoke", "", http.StatusOK)
	if len(first) != 0 || len(second) != 1 {
		t.Errorf("callbacks called: first %d, second %d, want only the one registered last", len(first), len(second))
	}
}

func TestRegisterPipeCallbackForUnknownTarget(t *testing.T) {
	e := newTestEnv(t)
	calls := make(chan pipeCall, 1)
	RegisterPipeCallback(e.name("unknown"), "source", recordPipe(calls))
	body := e.mustCall(t, "GET", "/server/response/triggers/pipes", "", http.StatusOK)
	if strings.Contains(body, e.name("unknown")) {
		t.Errorf("pipes = %q, want no pipe for a target that doesn't exist", body)
	}
}

func TestGetTriggerPipes(t *testing.T) {
	e := newTestEnv(t)
	a, b, none := e.name("a"), e.name("b"), e.name("none")
	e.add(t, pipeSpec(a, nil))
	e.add(t, pipeSpec(b, nil))
	e.add(t, pipeSpec(none, nil))
	calls := make(chan pipeCall, 1)
	RegisterPipeCallback(a, "z-source", recordPipe(calls))
	RegisterPipeCallback(a, "a-source", recordPipe(calls))
	RegisterPipeCallback(b, "only", recordPipe(calls))
	body := e.mustCall(t, "GET", "/server/response/triggers/pipes", "", http.StatusOK)
	pipes := map[string][]string{}
	if err := json.Unmarshal([]byte(body), &pipes); err != nil {
		t.Fatalf("pipes = %q, which is not JSON: %v", body, err)
	}
	if !reflect.DeepEqual(pipes[a], []string{"a-source", "z-source"}) || !reflect.DeepEqual(pipes[b], []string{"only"}) {
		t.Errorf("pipes = %v, want the sources of each target", pipes)
	}
	if _, found := pipes[none]; found {
		t.Errorf("pipes = %v, want no entry for a target without callbacks", pipes)
	}
}

// Matching responses

func TestTriggersMatchByStatus(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("status")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500, 503}}))
	for _, status := range []int{500, 503} {
		_, pipes := e.fired("/anything", status)
		expectNames(t, fmt.Sprintf("fired by %d", status), pipes, name)
	}
	for _, status := range []int{200, 404, 502} {
		_, pipes := e.fired("/anything", status)
		expectNames(t, fmt.Sprintf("fired by %d", status), pipes)
	}
}

func TestTriggersMatchByURI(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("uri")
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/Orders"}}))
	for _, uri := range []string{"/orders", "/ORDERS/17", "/orders?x=1"} {
		_, pipes := e.fired(uri, 200)
		expectNames(t, "fired by "+uri, pipes, name)
	}
	for _, uri := range []string{"/other", "/", "/order"} {
		_, pipes := e.fired(uri, 200)
		expectNames(t, "fired by "+uri, pipes)
	}
}

func TestTriggersMatchByURIWithVariables(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("uri")
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/orders/{id}/items"}}))
	if _, pipes := e.fired("/orders/17/items", 200); !reflect.DeepEqual(pipes, []string{name}) {
		t.Errorf("fired by /orders/17/items = %v, want %s", pipes, name)
	}
	if _, pipes := e.fired("/orders/17", 200); len(pipes) != 0 {
		t.Errorf("fired by /orders/17 = %v, want none", pipes)
	}
}

func TestTriggersMatchByURIAndStatus(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("both")
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/orders"}, "triggerStatuses": []int{500}}))
	if _, pipes := e.fired("/orders", 500); !reflect.DeepEqual(pipes, []string{name}) {
		t.Errorf("fired by /orders with 500 = %v, want %s", pipes, name)
	}
	if _, pipes := e.fired("/orders", 200); len(pipes) != 0 {
		t.Errorf("fired by /orders with 200 = %v, want none: the status must match too", pipes)
	}
	if _, pipes := e.fired("/other", 500); len(pipes) != 0 {
		t.Errorf("fired by /other with 500 = %v, want none: the URI must match too", pipes)
	}
}

func TestTriggersMatchByURIAndHeaders(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("both")
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/orders"}, "triggerHeaders": map[string]string{"X-Env": "prod"}}))
	if _, pipes := e.fired("/orders", 200, "X-Env", "PROD"); !reflect.DeepEqual(pipes, []string{name}) {
		t.Errorf("fired by /orders with the header = %v, want %s: header values are matched without case", pipes, name)
	}
	if _, pipes := e.fired("/orders", 200, "X-Env", "dev"); len(pipes) != 0 {
		t.Errorf("fired by /orders with another header value = %v, want none", pipes)
	}
	if _, pipes := e.fired("/orders", 200); len(pipes) != 0 {
		t.Errorf("fired by /orders without the header = %v, want none", pipes)
	}
}

func TestTriggersMatchByHeader(t *testing.T) {
	e := newTestEnv(t)
	withValue, anyValue := e.name("value"), e.name("any")
	e.add(t, pipeSpec(withValue, map[string]any{"triggerHeaders": map[string]string{"X-Env": "prod"}}))
	e.add(t, pipeSpec(anyValue, map[string]any{"triggerHeaders": map[string]string{"X-Debug": ""}}))
	_, pipes := e.fired("/anything", 200, "X-Env", "prod")
	expectNames(t, "fired by X-Env: prod", pipes, withValue)
	_, pipes = e.fired("/anything", 404, "X-Env", "dev")
	expectNames(t, "fired by X-Env: dev", pipes)
	_, pipes = e.fired("/anything", 200, "X-Debug", "whatever")
	expectNames(t, "fired by X-Debug", pipes, anyValue)
	_, pipes = e.fired("/anything", 200)
	expectNames(t, "fired by no header", pipes)
}

func TestTriggersMatchByStatusAndHeader(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("both")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500}, "triggerHeaders": map[string]string{"X-Env": "prod"}}))
	if _, pipes := e.fired("/anything", 500, "X-Env", "prod"); !reflect.DeepEqual(pipes, []string{name}) {
		t.Errorf("fired by 500 with the header = %v, want %s", pipes, name)
	}
	if _, pipes := e.fired("/anything", 500); len(pipes) != 0 {
		t.Errorf("fired by 500 without the header = %v, want none", pipes)
	}
	if _, pipes := e.fired("/anything", 200, "X-Env", "prod"); len(pipes) != 0 {
		t.Errorf("fired by 200 with the header = %v, want none: the status must match too", pipes)
	}
}

func TestMatchingSeparatesHTTPTargetsFromPipeSources(t *testing.T) {
	e := newTestEnv(t)
	httpOnly, pipeOnly, both := e.name("http"), e.name("pipe"), e.name("both")
	status := map[string]any{"triggerStatuses": []int{500}}
	e.add(t, targetSpec(httpOnly, "http://127.0.0.1:1/x", status))
	e.add(t, pipeSpec(pipeOnly, status))
	e.add(t, targetSpec(both, "http://127.0.0.1:1/x", map[string]any{"triggerStatuses": []int{500}, "pipe": true}))
	httpTargets, pipeSources := e.fired("/anything", 500)
	expectNames(t, "http targets", httpTargets, httpOnly, both)
	expectNames(t, "pipe sources", pipeSources, pipeOnly, both)
}

func TestDisabledTargetsAreMatchedButNotFired(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("disabled")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500}, "enabled": false}))
	if _, pipes := e.fired("/anything", 500); len(pipes) != 0 {
		t.Errorf("fired = %v, want a disabled target not to fire", pipes)
	}
	if got := e.view(t).Targets[name].MatchCount; got != 1 {
		t.Errorf("match count = %d, want 1: a disabled target still counts the responses that match", got)
	}
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/enable", "", http.StatusOK)
	if _, pipes := e.fired("/anything", 500); !reflect.DeepEqual(pipes, []string{name}) {
		t.Errorf("fired = %v, want the target to fire once it is enabled", pipes)
	}
}

func TestTargetsFireBetweenStartFromAndStopAt(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("window")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500}, "startFrom": 2, "stopAt": 4}))
	var fired []bool
	for i := 0; i < 6; i++ {
		_, pipes := e.fired("/anything", 500)
		fired = append(fired, len(pipes) == 1)
	}
	if want := []bool{false, true, true, true, false, false}; !reflect.DeepEqual(fired, want) {
		t.Errorf("fired for 6 matching responses = %v, want %v", fired, want)
	}
	if got := e.view(t).Targets[name].MatchCount; got != 6 {
		t.Errorf("match count = %d, want 6", got)
	}
}

func TestTargetsWithoutStopAtFireForever(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("forever")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500}, "startFrom": 3}))
	fired := 0
	for i := 0; i < 8; i++ {
		if _, pipes := e.fired("/anything", 500); len(pipes) == 1 {
			fired++
		}
	}
	if fired != 6 {
		t.Errorf("fired %d times for 8 matching responses starting from the 3rd, want 6", fired)
	}
}

func TestOneResponseCountsOnceForTargetWithSeveralMatches(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("many")
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/a", "/a/b", "/{x}"}}))
	if _, pipes := e.fired("/a/b", 200); !reflect.DeepEqual(pipes, []string{name}) {
		t.Fatalf("fired = %v, want the target once", pipes)
	}
	if got := e.view(t).Targets[name].MatchCount; got != 1 {
		t.Errorf("match count = %d, want 1 for one response that several URIs of the target match", got)
	}
}

// Running triggers for responses

func TestRunTriggersInvokesHTTPTargetsOfMatchingResponse(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("run")
	e.add(t, map[string]any{
		"name": name, "enabled": true, "triggerStatuses": []int{500},
		"httpTarget": map[string]any{
			"method": "PUT", "url": ts.url("/triggered"), "body": "b",
			"headers": map[string]string{"X-From-Response": "{X-Upstream}", "X-Uri": "{request.uri}"},
		},
	})
	req := e.newRequest("GET", "/orders/17", "")
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Upstream", "u1")
	RunTriggers(req, rr, 500)
	hits := ts.waitFor(t, 1)
	h := hits[0]
	if h.Method != "PUT" || h.Path != "/triggered" || h.Body != "b" {
		t.Errorf("call = %+v, want PUT /triggered with the body of the target", h)
	}
	if h.Header.Get("X-From-Response") != "u1" || h.Header.Get("X-Uri") != "/orders/17" {
		t.Errorf("headers = %v, want the value of the response header and the URI of the request", h.Header)
	}
	eventually(t, "the result of the call to be counted", func() bool { return e.counts(t)[name]["200"] == 1 })
	v := e.view(t).Targets[name]
	if v.MatchCount != 1 || v.TriggerCount != 1 {
		t.Errorf("target = %+v, want it matched and triggered once", v)
	}
}

func TestRunTriggersDoesNotInvokeTargetsOfOtherResponses(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("run")
	e.add(t, targetSpec(name, ts.url("/x"), map[string]any{"triggerStatuses": []int{500}}))
	for _, status := range []int{200, 404, 503} {
		RunTriggers(e.newRequest("GET", "/orders", ""), httptest.NewRecorder(), status)
	}
	// A response that matches comes after, and everything before it was handled by the time that it is.
	RunTriggers(e.newRequest("GET", "/orders", ""), httptest.NewRecorder(), 500)
	ts.waitFor(t, 1)
	time.Sleep(100 * time.Millisecond)
	if got := ts.count(); got != 1 {
		t.Errorf("target server calls = %d, want only the one for the response that matches", got)
	}
}

func TestRunTriggersCallsPipeCallbacksWithResponse(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("pipe")
	e.add(t, pipeSpec(name, map[string]any{"triggerURIs": []string{"/teapots"}}))
	calls := make(chan pipeCall, 2)
	RegisterPipeCallback(name, "source", recordPipe(calls))
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Upstream", "u1")
	RunTriggers(e.newRequest("GET", "/teapots/1", ""), rr, 418)
	select {
	case c := <-calls:
		if c.Target != name || c.Source != "source" || c.Port != e.port || c.Status != 418 || c.Headers.Get("X-Upstream") != "u1" {
			t.Errorf("callback = %+v, want the target, the source, the port %d, status 418 and the response headers", c, e.port)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("callback was not called")
	}
}

func TestRunTriggersSkipsAdminAndMetricsRequests(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("skipped")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{200}}))
	calls := make(chan pipeCall, 4)
	RegisterPipeCallback(name, "source", recordPipe(calls))
	for _, uri := range []string{"/server/anything", "/request/anything", "/metrics", "/stats"} {
		RunTriggers(e.newRequest("GET", uri, ""), httptest.NewRecorder(), 200)
	}
	time.Sleep(50 * time.Millisecond)
	if len(calls) != 0 {
		t.Errorf("callbacks called %d times for admin and metrics requests, want none", len(calls))
	}
	if got := e.view(t).Targets[name].MatchCount; got != 0 {
		t.Errorf("match count = %d, want admin and metrics requests not to be matched", got)
	}
	RunTriggers(e.newRequest("GET", "/traffic", ""), httptest.NewRecorder(), 200)
	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatalf("callback was not called for a request of traffic")
	}
}

func TestRunTriggersOnlyRunsTriggersOfThePortOfTheRequest(t *testing.T) {
	a, b := newTestEnv(t), newTestEnv(t)
	name := a.name("t")
	a.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{200}}))
	calls := make(chan pipeCall, 2)
	RegisterPipeCallback(name, "source", recordPipe(calls))
	RunTriggers(b.newRequest("GET", "/traffic", ""), httptest.NewRecorder(), 200)
	time.Sleep(50 * time.Millisecond)
	if len(calls) != 0 {
		t.Fatalf("callback of port %d was called for a request of port %d", a.port, b.port)
	}
	RunTriggers(a.newRequest("GET", "/traffic", ""), httptest.NewRecorder(), 200)
	select {
	case c := <-calls:
		if c.Port != a.port {
			t.Errorf("callback port = %d, want %d", c.Port, a.port)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("callback was not called")
	}
}

// Concurrency

func TestConcurrentAddsOnFreshPortKeepAllTargets(t *testing.T) {
	e := newTestEnv(t)
	const adds = 40
	reqs := make([]*http.Request, adds)
	for i := range reqs {
		body := fmt.Sprintf(`{"name":%q,"pipe":true,"enabled":true,"triggerStatuses":[500]}`, e.name(fmt.Sprintf("t%d", i)))
		reqs[i] = e.newRequest("POST", "/server/response/triggers/add", body)
	}
	codes := make([]int, adds)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rr := httptest.NewRecorder()
			api.ServeHTTP(rr, reqs[i])
			codes[i] = rr.Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("add %d = %d, want 200", i, code)
		}
	}
	v := e.view(t)
	if len(v.Targets) != adds || len(v.TargetsByStatus["500"]) != adds {
		t.Errorf("targets = %d, targets of status 500 = %d, want %d of each", len(v.Targets), len(v.TargetsByStatus["500"]), adds)
	}
}

func TestConcurrentRemovalsKeepIndexesConsistent(t *testing.T) {
	e := newTestEnv(t)
	const total = 40
	var names []string
	for i := 0; i < total; i++ {
		name := e.name(fmt.Sprintf("t%d", i))
		names = append(names, name)
		e.add(t, pipeSpec(name, map[string]any{
			"triggerStatuses": []int{500},
		}))
	}
	var removes, runs, reads []*http.Request
	for i, name := range names {
		if i%2 == 0 {
			removes = append(removes, e.newRequest("POST", "/server/response/triggers/"+name+"/remove", ""))
		}
	}
	for i := 0; i < 20; i++ {
		runs = append(runs, e.newRequest("GET", "/traffic", ""))
		reads = append(reads, e.newRequest("GET", "/server/response/triggers", ""))
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, req := range removes {
		wg.Add(1)
		go func(req *http.Request) {
			defer wg.Done()
			<-start
			api.ServeHTTP(httptest.NewRecorder(), req)
		}(req)
	}
	for i := range runs {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			<-start
			RunTriggers(runs[i], httptest.NewRecorder(), 500)
		}(i)
		go func(i int) {
			defer wg.Done()
			<-start
			api.ServeHTTP(httptest.NewRecorder(), reads[i])
		}(i)
	}
	close(start)
	wg.Wait()
	v := e.view(t)
	var remaining []string
	for i, name := range names {
		if i%2 == 1 {
			remaining = append(remaining, name)
		}
	}
	var gotTargets []string
	for name := range v.Targets {
		gotTargets = append(gotTargets, name)
	}
	sort.Strings(gotTargets)
	expectNames(t, "targets", gotTargets, remaining...)
	expectNames(t, "targets of status 500", v.TargetsByStatus["500"], remaining...)
}

// Calls of one target made together are not tested: they share an HTTP client, which updates its TLS configuration
// for every call without synchronization (pkg/transport), so they are reported as a race that is not in triggers.
func TestConcurrentInvocationsOfDifferentTargets(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	const invocations = 12
	names := make([]string, invocations)
	reqs := make([]*http.Request, invocations)
	for i := range reqs {
		names[i] = e.name(fmt.Sprintf("busy%d", i))
		e.add(t, targetSpec(names[i], ts.url("/"+names[i]), nil))
		reqs[i] = e.newRequest("POST", "/server/response/triggers/"+names[i]+"/invoke", "")
	}
	codes := make([]int, invocations)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rr := httptest.NewRecorder()
			api.ServeHTTP(rr, reqs[i])
			codes[i] = rr.Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("invocation %d = %d, want 200", i, code)
		}
	}
	if got := ts.count(); got != invocations {
		t.Errorf("target server calls = %d, want %d", got, invocations)
	}
	counts, v := e.counts(t), e.view(t)
	for _, name := range names {
		if counts[name]["200"] != 1 || v.Targets[name].TriggerCount != 1 {
			t.Errorf("target %s: responses of 200 = %d, trigger count = %d, want 1 of each", name, counts[name]["200"], v.Targets[name].TriggerCount)
		}
	}
}

func TestMiddlewareIsRegistered(t *testing.T) {
	if Middleware == nil || Middleware.Name != "trigger" || Middleware.SetRoutes == nil {
		t.Errorf("Middleware = %+v, want the trigger middleware with routes", Middleware)
	}
	if Middleware.MiddlewareHandler != nil {
		t.Errorf("trigger has a middleware handler, want it to only add routes: triggers are run by the code that sends responses")
	}
}
