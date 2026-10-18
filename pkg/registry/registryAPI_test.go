package registry

// Unit tests for the HTTP APIs exposed by the registry (routes registered in registryAPI.go).
//
// Branch portability: everything that differs between branches (how routes are registered, the request
// store, the "self" address, registry internals) lives in registryAPI_compat_test.go. To replay these
// tests on another branch, port that file; this one only talks HTTP through the router.
//
// Design notes:
//  - Tests go through the mux router (the public surface), not the handler funcs, so the same
//    tests can be replayed against another branch to detect API drift.
//  - Peers are simulated with httptest servers that record every call the registry fans out.
//  - The registry is a package level singleton, so every test starts/ends with resetRegistryState()
//    (see the compat file). Tests must not use t.Parallel().
//  - A handler panic fails the test that triggered it (see harness.do) instead of aborting the test binary.
//    Tests that document a panicking request use harness.assertPanics.
//  - The code under test is go1.16 (go.mod), so avoid `any`, generics and other newer features here.
//  - Assertions describe CURRENT behavior. Quirks are called out with "QUIRK" comments rather than
//    being silently "fixed", so that a diff against main shows up as a test failure.

import (
  "encoding/json"
  "fmt"
  "io/ioutil"
  "net/http"
  "net/http/httptest"
  "reflect"
  "runtime"
  "sort"
  "strings"
  "sync"
  "testing"

  "github.com/gorilla/mux"
)

const (
  peersPath   = "/registry/peers"
  lockersPath = "/registry/lockers"
)

// ---------------------------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------------------------

type response struct {
  Code   int
  Body   string
  Header http.Header
}

func (r response) decode(t *testing.T, v interface{}) {
  t.Helper()
  if err := json.Unmarshal([]byte(r.Body), v); err != nil {
    t.Fatalf("response body is not valid JSON for %T: %v\nbody: %q", v, err, r.Body)
  }
}

func (r response) asMap(t *testing.T) map[string]interface{} {
  t.Helper()
  m := map[string]interface{}{}
  r.decode(t, &m)
  return m
}

type harness struct {
  t      *testing.T
  router *mux.Router
}

// newHarness builds a router with the registry routes. The request store middleware stands in for the one the real
// server injects, since several handlers call util.AddLogMessage unconditionally.
func newHarness(t *testing.T) *harness {
  t.Helper()
  resetRegistryState()
  restoreAddress := setSelfAddress("registry.test:8080")
  t.Cleanup(func() {
    restoreAddress()
    resetRegistryState()
  })
  return &harness{t: t, router: newRouter(requestStoreMiddleware)}
}

// serve runs a request through the router and reports whether the handler panicked.
func (h *harness) serve(method, target, body string, headers ...string) (resp response, panicked interface{}) {
  req := httptest.NewRequest(method, target, strings.NewReader(body))
  for i := 0; i+1 < len(headers); i += 2 {
    req.Header.Set(headers[i], headers[i+1])
  }
  req = prepareRequest(req)
  rec := httptest.NewRecorder()
  func() {
    defer func() { panicked = recover() }()
    h.router.ServeHTTP(rec, req)
  }()
  return response{Code: rec.Code, Body: rec.Body.String(), Header: rec.Header()}, panicked
}

// do fails the test if the handler panics, so that one unexpected panic doesn't abort the whole test binary.
func (h *harness) do(method, target, body string, headers ...string) response {
  h.t.Helper()
  resp, panicked := h.serve(method, target, body, headers...)
  if panicked != nil {
    h.t.Fatalf("%s %s: handler panicked: %v", method, target, panicked)
  }
  return resp
}

// assertPanics asserts that the request makes the handler panic (documenting a known defect).
func (h *harness) assertPanics(method, target, body string) {
  h.t.Helper()
  if _, panicked := h.serve(method, target, body); panicked == nil {
    h.t.Errorf("%s %s: expected the handler to panic", method, target)
  }
}

func (h *harness) get(target string) response            { h.t.Helper(); return h.do("GET", target, "") }
func (h *harness) post(target, body string) response     { h.t.Helper(); return h.do("POST", target, body) }
func (h *harness) put(target, body string) response      { h.t.Helper(); return h.do("PUT", target, body) }

func (h *harness) peers() map[string]*Peers {
  h.t.Helper()
  resp := h.get(peersPath)
  assertStatus(h.t, resp, http.StatusOK)
  peers := map[string]*Peers{}
  resp.decode(h.t, &peers)
  return peers
}

// podsOf returns the pods of a peer keyed by address (nil if peer is unknown).
func (h *harness) podsOf(peer string) map[string]*Pod {
  h.t.Helper()
  if p := h.peers()[peer]; p != nil {
    return p.Pods
  }
  return nil
}

func peerJSON(name, address, pod string) string {
  return fmt.Sprintf(`{"name":%q,"address":%q,"pod":%q,"namespace":"ns","node":"node-1","cluster":"cluster-1"}`,
    name, address, pod)
}

func (h *harness) addPeer(name, address, pod string) response {
  h.t.Helper()
  resp := h.post(peersPath+"/add", peerJSON(name, address, pod))
  assertStatus(h.t, resp, http.StatusOK)
  return resp
}

const targetJSON = `{"name":"t1","method":"GET","url":"http://target.test/foo"}`
const jobJSON = `{"name":"j1","task":{"cmd":"echo","args":["hi"]}}`

func assertStatus(t *testing.T, resp response, want int) {
  t.Helper()
  if resp.Code != want {
    t.Fatalf("status = %d, want %d. body: %q", resp.Code, want, resp.Body)
  }
}

func assertContains(t *testing.T, s string, subs ...string) {
  t.Helper()
  for _, sub := range subs {
    if !strings.Contains(s, sub) {
      t.Errorf("expected %q to contain %q", s, sub)
    }
  }
}

func assertNotContains(t *testing.T, s string, subs ...string) {
  t.Helper()
  for _, sub := range subs {
    if strings.Contains(s, sub) {
      t.Errorf("expected %q NOT to contain %q", s, sub)
    }
  }
}

// ---------------------------------------------------------------------------------------------
// Fake peer: records the calls the registry fans out to registered peer pods.
// ---------------------------------------------------------------------------------------------

type peerCall struct {
  Method string
  Path   string
  Query  string
  Body   string
  Header http.Header
}

func (c peerCall) String() string { return c.Method + " " + c.Path }

type fakePeer struct {
  server *httptest.Server
  lock   sync.Mutex
  calls  []peerCall
  status int
}

func newFakePeer(t *testing.T, status int) *fakePeer {
  t.Helper()
  p := &fakePeer{status: status}
  p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    body, _ := ioutil.ReadAll(r.Body)
    p.lock.Lock()
    p.calls = append(p.calls, peerCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body), Header: r.Header.Clone()})
    code := p.status
    p.lock.Unlock()
    w.WriteHeader(code)
    fmt.Fprint(w, "ok")
  }))
  t.Cleanup(p.server.Close)
  return p
}

func (p *fakePeer) address() string { return strings.TrimPrefix(p.server.URL, "http://") }

// kill shuts the peer down so that calls to it fail with "connection refused".
func (p *fakePeer) kill() { p.server.Close() }

func (p *fakePeer) setStatus(status int) {
  p.lock.Lock()
  p.status = status
  p.lock.Unlock()
}

func (p *fakePeer) recorded() []peerCall {
  p.lock.Lock()
  defer p.lock.Unlock()
  return append([]peerCall{}, p.calls...)
}

func (p *fakePeer) reset() {
  p.lock.Lock()
  p.calls = nil
  p.lock.Unlock()
}

func (p *fakePeer) summary() []string {
  summary := []string{}
  for _, c := range p.recorded() {
    summary = append(summary, c.String())
  }
  sort.Strings(summary)
  return summary
}

// find returns the first recorded call matching method and path.
func (p *fakePeer) find(method, path string) (peerCall, bool) {
  for _, c := range p.recorded() {
    if c.Method == method && c.Path == path {
      return c, true
    }
  }
  return peerCall{}, false
}

func (p *fakePeer) assertCalled(t *testing.T, method, path string) peerCall {
  t.Helper()
  c, found := p.find(method, path)
  if !found {
    t.Fatalf("peer %s was not called with %s %s. calls: %v", p.address(), method, path, p.summary())
  }
  return c
}

func (p *fakePeer) assertNotCalled(t *testing.T, method, path string) {
  t.Helper()
  if _, found := p.find(method, path); found {
    t.Fatalf("peer %s should not have been called with %s %s. calls: %v", p.address(), method, path, p.summary())
  }
}

func (p *fakePeer) assertNoCalls(t *testing.T) {
  t.Helper()
  if calls := p.recorded(); len(calls) > 0 {
    t.Fatalf("peer %s should not have been called. calls: %v", p.address(), p.summary())
  }
}

// addFakePeer starts a fake peer with the given status and registers it as pod "<name>-pod" of peer <name>.
func (h *harness) addFakePeer(name string, status int) *fakePeer {
  h.t.Helper()
  p := newFakePeer(h.t, status)
  h.addPeer(name, p.address(), name+"-pod")
  return p
}

// ---------------------------------------------------------------------------------------------
// Route table
// ---------------------------------------------------------------------------------------------

func handlerName(h http.Handler) string {
  if h == nil {
    return ""
  }
  name := runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
  return name[strings.LastIndex(name, ".")+1:]
}

type routeCase struct {
  methods []string
  target  string
  handler string
}

func rc(handler string, target string, methods ...string) routeCase {
  return routeCase{methods: methods, target: target, handler: handler}
}

// Every route registered by the registry (including query-string variants) mapped to its expected handler.
// Registration order matters in gorilla/mux (first match wins), so this also guards against
// accidental route shadowing.
func routeCases() []routeCase {
  const (
    P    = peersPath
    L    = lockersPath
    addr = "10.0.0.1:8080"
  )
  PP := []string{"PUT", "POST"}
  cases := []routeCase{
    // --- peers
    rc("addPeer", P+"/add", "POST"),
    rc("addPeer", P+"/p1/remember", "POST"),
    rc("removePeer", P+"/p1/remove/"+addr, PP...),
    rc("checkPeerHealth", P+"/p1/health/"+addr, "GET"),
    rc("checkPeerHealth", P+"/p1/health", "GET"),
    rc("checkPeerHealth", P+"/health", "GET"),
    rc("cleanupUnhealthyPeers", P+"/p1/health/cleanup", "POST"),
    rc("cleanupUnhealthyPeers", P+"/health/cleanup", "POST"),
    rc("clearPeerEpochs", P+"/clear/epochs", "POST"),
    rc("clearPeers", P+"/clear", "POST"),
    rc("copyPeersToLocker", P+"/copyToLocker", "POST"),
    rc("getPeers", P, "GET"),
    rc("getPeers", P+"/", "GET"),

    // --- labeled lockers
    rc("openLabeledLocker", L+"/open/L1", "POST"),
    rc("closeOrClearLabeledLocker", L+"/close/L1", "POST"),
    rc("closeOrClearLabeledLocker", L+"/L1/close", "POST"),
    rc("closeOrClearLabeledLocker", L+"/close", "POST"),
    rc("closeOrClearLabeledLocker", L+"/L1/clear", "POST"),
    rc("closeOrClearLabeledLocker", L+"/clear", "POST"),
    rc("getLockerLabels", L+"/labels", "GET"),
    rc("storeInLabeledLocker", L+"/L1/store/a,b", "POST"),
    rc("removeFromLabeledLocker", L+"/L1/remove/a,b", "POST"),
    rc("getFromDataLocker", L+"/L1/get/a,b", "GET"),
    rc("getFromDataLocker", L+"/L1/get/a,b?data=y&level=1", "GET"),
    rc("getDataLockerPaths", L+"/L1/data/keys", "GET"),
    rc("getDataLockerPaths", L+"/L1/data/paths", "GET"),
    rc("getDataLockerPaths", L+"/data/keys", "GET"),
    rc("getDataLockerPaths", L+"/data/paths", "GET"),
    rc("searchInDataLockers", L+"/L1/search/txt", "GET"),
    rc("searchInDataLockers", L+"/search/txt", "GET"),
    rc("getDataLockers", L+"/data", "GET"),
    rc("getDataLockers", L+"/data?data=y&level=1", "GET"),
    rc("getDataLockers", L+"/L1/data", "GET"),
    rc("getDataLockers", L+"/L1/data?data=y", "GET"),
    rc("getLabeledLocker", L+"/L1", "GET"),
    rc("getLabeledLocker", L+"/L1?data=y&events=y&peers=y&level=2", "GET"),
    rc("getAllLockers", L, "GET"),
    rc("getAllLockers", L+"?data=y&events=y&peers=y&level=2", "GET"),

    // --- peer lockers and events
    rc("storeInPeerLocker", P+"/p1/"+addr+"/locker/store/k1", "POST"),
    rc("removeFromPeerLocker", P+"/p1/"+addr+"/locker/remove/k1", "POST"),
    rc("storeInPeerLocker", P+"/p1/locker/store/k1", "POST"),
    rc("removeFromPeerLocker", P+"/p1/locker/remove/k1", "POST"),
    rc("storePeerEvent", P+"/p1/"+addr+"/events/store", "POST"),
    rc("clearLocker", P+"/p1/"+addr+"/lockers/clear", "POST"),
    rc("clearLocker", P+"/p1/lockers/clear", "POST"),
    rc("clearLocker", P+"/lockers/clear", "POST"),
    // backward compatible v0.8.x path
    rc("clearLocker", P+"/p1/"+addr+"/locker/clear", "POST"),
    rc("clearLocker", P+"/p1/locker/clear", "POST"),
    rc("flushPeerEvents", P+"/events/flush", "POST"),
    rc("clearPeerEvents", P+"/events/clear", "POST"),

    rc("getPeerEvents", L+"/L1/peers/p1,p2/events", "GET"),
    rc("getPeerEvents", L+"/L1/peers/p1,p2/events?data=y&reverse=y", "GET"),
    rc("getPeerEvents", L+"/L1/peers/events", "GET"),
    rc("getPeerEvents", L+"/L1/peers/events?data=y&unified=y&reverse=y", "GET"),
    rc("getPeerEvents", P+"/p1/events", "GET"),
    rc("getPeerEvents", P+"/p1/events?data=y&reverse=y", "GET"),
    rc("getPeerEvents", P+"/events", "GET"),
    rc("getPeerEvents", P+"/events?data=y&unified=y&reverse=y", "GET"),

    rc("searchInPeerEvents", L+"/L1/peers/p1,p2/events/search/txt", "GET"),
    rc("searchInPeerEvents", L+"/L1/peers/p1,p2/events/search/txt?data=y&reverse=y", "GET"),
    rc("searchInPeerEvents", L+"/L1/peers/events/search/txt", "GET"),
    rc("searchInPeerEvents", L+"/L1/peers/events/search/txt?data=y&unified=y&reverse=y", "GET"),
    rc("searchInPeerEvents", P+"/p1/events/search/txt", "GET"),
    rc("searchInPeerEvents", P+"/p1/events/search/txt?data=y&reverse=y", "GET"),
    rc("searchInPeerEvents", P+"/events/search/txt", "GET"),
    rc("searchInPeerEvents", P+"/events/search/txt?data=y&unified=y&reverse=y", "GET"),

    rc("getTargetsResultsCompat", L+"/L1/peers/p1/targets/results", "GET"),
    rc("getTargetsResultsCompat", L+"/L1/peers/p1/targets/results?detailed=y", "GET"),
    rc("getTargetsResultsCompat", L+"/L1/peers/targets/results", "GET"),
    rc("getTargetsResultsCompat", L+"/L1/peers/targets/results?detailed=y", "GET"),
    rc("getTargetsResultsCompat", P+"/lockers/targets/results", "GET"),
    rc("getTargetsResultsCompat", P+"/lockers/targets/results?detailed=y", "GET"),
    rc("getTargetsResultsCompat", P+"/p1/targets/results", "GET"),
    rc("getTargetsResultsCompat", P+"/p1/targets/results?detailed=y", "GET"),

    rc("getFromPeerLocker", L+"/L1/peers/p1/get/k1", "GET"),
    rc("getFromPeerLocker", L+"/L1/peers/p1/get/k1?data=y&level=1", "GET"),
    rc("getFromPeerLocker", L+"/L1/peers/p1/locker/get/k1", "GET"),
    rc("getFromPeerLocker", L+"/L1/peers/p1/"+addr+"/get/k1", "GET"),
    rc("getFromPeerLocker", L+"/L1/peers/p1/"+addr+"/locker/get/k1", "GET"),
    rc("getPeerLocker", L+"/L1/peers/p1/"+addr, "GET"),
    rc("getPeerLocker", L+"/L1/peers/p1/"+addr+"?data=y&events=y&level=2", "GET"),
    rc("getPeerLocker", L+"/L1/peers/p1", "GET"),
    rc("getPeerLocker", L+"/L1/peers/p1?data=y", "GET"),
    rc("getPeerLocker", L+"/L1/peers", "GET"),
    rc("getPeerLocker", L+"/L1/peers?data=y&events=y&level=2", "GET"),
    rc("getFromPeerLocker", P+"/p1/locker/get/k1", "GET"),
    rc("getFromPeerLocker", P+"/p1/locker/get/k1?data=y&level=1", "GET"),
    rc("getFromPeerLocker", P+"/p1/"+addr+"/locker/get/k1", "GET"),
    rc("getPeerLocker", P+"/p1/"+addr+"/lockers", "GET"),
    rc("getPeerLocker", P+"/p1/"+addr+"/lockers?data=y&events=y&level=2", "GET"),
    rc("getPeerLocker", P+"/p1/lockers", "GET"),
    rc("getPeerLocker", P+"/lockers", "GET"),
    rc("getPeerLocker", P+"/lockers?data=y&events=y&level=2", "GET"),

    // --- targets
    rc("addPeerTarget", P+"/p1/targets/add", "POST"),
    rc("addPeerTarget", P+"/targets/add", "POST"),
    rc("removePeerTargets", P+"/p1/targets/t1,t2/remove", PP...),
    rc("removePeerTargets", P+"/p1/targets/remove/all", PP...),
    rc("invokePeerTargets", P+"/p1/targets/t1,t2/invoke", PP...),
    rc("invokePeerTargets", P+"/p1/targets/invoke/all", PP...),
    rc("invokePeerTargets", P+"/targets/invoke/all", PP...),
    rc("stopPeerTargets", P+"/p1/targets/t1,t2/stop", PP...),
    rc("stopPeerTargets", P+"/p1/targets/stop/all", PP...),
    rc("stopPeerTargets", P+"/targets/stop/all", PP...),
    rc("clearPeerTargets", P+"/p1/targets/clear", "POST"),
    rc("getPeerTargets", P+"/p1/targets", "GET"),
    rc("enableAllClientResultsCollection", P+"/client/results/all/y", PP...),
    rc("enableInvocationResultsCollection", P+"/client/results/invocations/y", PP...),
    // backward compatible v0.8.x paths
    rc("enableAllClientResultsCollection", P+"/targets/results/all/y", PP...),
    rc("enableInvocationResultsCollection", P+"/targets/results/invocations/y", PP...),
    rc("clearPeerTargets", P+"/targets/clear", "POST"),
    rc("getPeerTargets", P+"/targets", "GET"),

    // --- jobs
    rc("addPeerJob", P+"/p1/jobs/add", "POST"),
    rc("addPeerJob", P+"/jobs/add", "POST"),
    rc("removePeerJobs", P+"/p1/jobs/j1,j2/remove", "POST"),
    rc("runPeerJobs", P+"/p1/jobs/j1,j2/run", "POST"),
    rc("runPeerJobs", P+"/p1/jobs/run/all", "POST"),
    rc("stopPeerJobs", P+"/p1/jobs/j1,j2/stop", "POST"),
    rc("stopPeerJobs", P+"/p1/jobs/stop/all", "POST"),
    rc("clearPeerJobs", P+"/p1/jobs/clear", "POST"),
    rc("getPeerJobs", P+"/p1/jobs", "GET"),
    rc("clearPeerJobs", P+"/jobs/clear", "POST"),
    rc("getPeerJobs", P+"/jobs", "GET"),

    // --- tracking headers, probes
    rc("addPeersTrackingHeaders", P+"/track/headers/h1,h2", PP...),
    rc("getPeersTrackingHeaders", P+"/track/headers", "GET"),
    rc("setPeersProbe", P+"/probes/readiness/set?uri=/ready", PP...),
    rc("setPeersProbeStatus", P+"/probes/readiness/set/status=200", PP...),
    rc("getPeersProbes", P+"/probes", "GET"),

    // --- calling peers
    rc("callPeer", P+"/p1/call?uri=/x", "GET", "POST", "PUT"),
    rc("callPeer", P+"/call?uri=/x", "GET", "POST", "PUT"),
    rc("getPeersAPIs", P+"/p1/apis", "GET"),
    rc("getPeersAPIs", P+"/apis", "GET"),

    // --- clone, dump, load
    rc("cloneFromRegistry", "/registry/cloneFrom?url=http://other.test", "POST"),
    rc("dumpRegistry", "/registry/dump", "GET"),
    rc("loadRegistryDump", "/registry/load", "POST"),
  }
  return cases
}

func TestRoutes_MapToExpectedHandlers(t *testing.T) {
  // Route.GetHandler() returns the registered handler itself, without any middleware wrapping.
  router := newRouter()
  for _, tc := range routeCases() {
    for _, method := range tc.methods {
      tc, method := tc, method
      t.Run(method+" "+tc.target, func(t *testing.T) {
        req := httptest.NewRequest(method, tc.target, nil)
        var match mux.RouteMatch
        if !router.Match(req, &match) {
          t.Fatalf("no route matched (err: %v), want %s", match.MatchErr, tc.handler)
        }
        if got := handlerName(match.Route.GetHandler()); got != tc.handler {
          t.Errorf("handler = %s, want %s", got, tc.handler)
        }
      })
    }
  }
}

// Each route is also registered with a trailing slash (util.AddRoute and friends).
func TestRoutes_TrailingSlashAccepted(t *testing.T) {
  router := newRouter()
  for _, tc := range routeCases() {
    if strings.Contains(tc.target, "?") || strings.HasSuffix(tc.target, "/") {
      continue
    }
    tc := tc
    t.Run(tc.target, func(t *testing.T) {
      req := httptest.NewRequest(tc.methods[0], tc.target+"/", nil)
      var match mux.RouteMatch
      if !router.Match(req, &match) {
        t.Fatalf("no route matched %s %s/ (err: %v)", tc.methods[0], tc.target, match.MatchErr)
      }
      if got := handlerName(match.Route.GetHandler()); got != tc.handler {
        t.Errorf("handler = %s, want %s", got, tc.handler)
      }
    })
  }
}

func TestRoutes_UnsupportedMethodAndUnknownPaths(t *testing.T) {
  h := newHarness(t)
  for _, tc := range []struct {
    method, target string
    want           int
  }{
    {"GET", peersPath + "/add", http.StatusMethodNotAllowed},
    {"GET", peersPath + "/clear", http.StatusMethodNotAllowed},
    {"DELETE", peersPath, http.StatusMethodNotAllowed},
    {"POST", peersPath, http.StatusMethodNotAllowed},
    {"GET", lockersPath + "/open/L1", http.StatusMethodNotAllowed},
    {"DELETE", lockersPath, http.StatusMethodNotAllowed},
    {"POST", "/registry/dump", http.StatusMethodNotAllowed},
    {"GET", "/registry/load", http.StatusMethodNotAllowed},
    {"GET", "/registry/cloneFrom?url=http://other.test", http.StatusMethodNotAllowed},
    {"GET", "/registry", http.StatusNotFound},
    {"GET", "/registry/unknown", http.StatusNotFound},
    {"GET", peersPath + "/p1/unknown/path/here/x/y", http.StatusNotFound},
    // Routes registered with AddRouteQ need their query param to be present.
    {"POST", peersPath + "/probes/readiness/set", http.StatusNotFound},
    {"GET", peersPath + "/call", http.StatusNotFound},
    {"POST", "/registry/cloneFrom", http.StatusNotFound},
  } {
    t.Run(tc.method+" "+tc.target, func(t *testing.T) {
      resp := h.do(tc.method, tc.target, "")
      // gorilla/mux 1.8.1 (v0.8.x had 1.8.0) reports a route that matches the path but not the method as not found
      // rather than not allowed, because the following routes (the trailing-slash variants, etc.) reset the
      // method mismatch. The request is rejected either way, so accept both until mux is fixed or pinned back.
      if tc.want == http.StatusMethodNotAllowed && resp.Code == http.StatusNotFound {
        return
      }
      if resp.Code != tc.want {
        t.Errorf("status = %d, want %d", resp.Code, tc.want)
      }
    })
  }
}

// ---------------------------------------------------------------------------------------------
// More helpers
// ---------------------------------------------------------------------------------------------

func (r response) peerResults(t *testing.T) PeerResults {
  t.Helper()
  results := PeerResults{}
  r.decode(t, &results)
  return results
}

// dig walks nested JSON objects (decoded into interface{}) along keys, failing the test if a key is missing.
func dig(t *testing.T, v interface{}, keys ...string) interface{} {
  t.Helper()
  for _, k := range keys {
    m, ok := v.(map[string]interface{})
    if !ok {
      t.Fatalf("cannot read key %q: %v (%T) is not a JSON object", k, v, v)
    }
    if v, ok = m[k]; !ok {
      t.Fatalf("key %q not found. available: %v", k, m)
    }
  }
  return v
}

func hasKey(v interface{}, keys ...string) bool {
  for _, k := range keys {
    m, ok := v.(map[string]interface{})
    if !ok {
      return false
    }
    if v, ok = m[k]; !ok {
      return false
    }
  }
  return true
}

func assertEqual(t *testing.T, what string, got, want interface{}) {
  t.Helper()
  if !reflect.DeepEqual(got, want) {
    t.Errorf("%s = %#v, want %#v", what, got, want)
  }
}

// ---------------------------------------------------------------------------------------------
// Peers: add/remember/remove/health/cleanup/clear/epochs/copy
// ---------------------------------------------------------------------------------------------

func TestGetPeers_Empty(t *testing.T) {
  h := newHarness(t)
  resp := h.get(peersPath)
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "body", resp.Body, "{}\n")
  assertContains(t, resp.Header.Get("Content-Type"), "application/json")
}

func TestAddPeer(t *testing.T) {
  h := newHarness(t)
  resp := h.post(peersPath+"/add", peerJSON("p1", "10.0.0.1:8080", "p1-pod"))
  assertStatus(t, resp, http.StatusOK)
  // PeerData is returned to the peer; Message is only populated for /remember and for errors.
  assertEqual(t, "PeerData.Message", dig(t, resp.asMap(t), "Message"), "")

  peers := h.peers()
  if peers["p1"] == nil || peers["p1"].Pods["10.0.0.1:8080"] == nil {
    t.Fatalf("peer p1 pod not registered: %s", h.get(peersPath).Body)
  }
  assertEqual(t, "namespace", peers["p1"].Namespace, "ns")
  pod := peers["p1"].Pods["10.0.0.1:8080"]
  assertEqual(t, "pod name", pod.Name, "p1-pod")
  assertEqual(t, "pod url", pod.URL, "http://10.0.0.1:8080")
  assertEqual(t, "pod node", pod.Node, "node-1")
  assertEqual(t, "pod cluster", pod.Cluster, "cluster-1")
  assertEqual(t, "pod healthy", pod.Healthy, true)
  assertEqual(t, "pod offline", pod.Offline, false)
  assertEqual(t, "pod epoch", pod.CurrentEpoch.Epoch, 0)
  assertEqual(t, "podEpochs", len(peers["p1"].PodEpochs["10.0.0.1:8080"]), 1)

  // Adding a peer also creates an (empty) peer locker in the current locker.
  lockerResp := h.get(lockersPath + "/default/peers")
  assertStatus(t, lockerResp, http.StatusOK)
  assertEqual(t, "peer locker present", hasKey(lockerResp.asMap(t), "p1"), true)
}

func TestAddPeer_ReturnsStartupDataForPeer(t *testing.T) {
  h := newHarness(t)
  // Targets, jobs, tracking headers and probes can be configured before the peer shows up.
  assertStatus(t, h.post(peersPath+"/p1/targets/add", targetJSON), http.StatusOK)
  assertStatus(t, h.post(peersPath+"/p1/jobs/add", jobJSON), http.StatusOK)
  assertStatus(t, h.post(peersPath+"/track/headers/h1,h2", ""), http.StatusOK)
  assertStatus(t, h.post(peersPath+"/probes/readiness/set?uri=/ready", ""), http.StatusOK)

  resp := h.post(peersPath+"/add", peerJSON("p1", "10.0.0.1:8080", "p1-pod"))
  assertStatus(t, resp, http.StatusOK)
  data := resp.asMap(t)
  assertEqual(t, "target t1 present", hasKey(data, "Targets", "t1"), true)
  assertEqual(t, "target t1 url", dig(t, data, "Targets", "t1", "url"), "http://target.test/foo")
  assertEqual(t, "job j1 present", hasKey(data, "Jobs", "j1"), true)
  assertEqual(t, "tracking headers", dig(t, data, "TrackingHeaders"), "h1,h2")
  assertEqual(t, "readiness probe", dig(t, data, "Probes", "ReadinessProbe"), "/ready")

  // A different peer doesn't get p1's targets and jobs (it gets empty collections, since adding a peer
  // initializes them), but does get the global tracking headers.
  other := h.post(peersPath+"/add", peerJSON("p2", "10.0.0.2:8080", "p2-pod")).asMap(t)
  assertEqual(t, "p2 targets", dig(t, other, "Targets"), map[string]interface{}{})
  assertEqual(t, "p2 jobs", dig(t, other, "Jobs"), map[string]interface{}{})
  assertEqual(t, "p2 tracking headers", dig(t, other, "TrackingHeaders"), "h1,h2")
}

func TestAddAndRememberPeer_InvalidPayload(t *testing.T) {
  h := newHarness(t)
  for _, target := range []string{peersPath + "/add", peersPath + "/p1/remember"} {
    resp := h.post(target, "{not json")
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, dig(t, resp.asMap(t), "Message").(string), "failed to parse json with error")
  }
  assertEqual(t, "registered peers", len(h.peers()), 0)
}

func TestRememberPeer(t *testing.T) {
  h := newHarness(t)
  resp := h.post(peersPath+"/p2/remember", peerJSON("p2", "10.0.0.2:8080", "p2-pod"))
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, dig(t, resp.asMap(t), "Message").(string), "Remembered Peer", "Name:p2", "Address:10.0.0.2:8080")
  pods := h.podsOf("p2")
  if pods["10.0.0.2:8080"] == nil || !pods["10.0.0.2:8080"].Healthy {
    t.Fatalf("remembered peer p2 not registered as healthy: %v", pods)
  }
}

func TestRememberPeer_RevivesUnhealthyPod(t *testing.T) {
  h := newHarness(t)
  p := newFakePeer(t, http.StatusInternalServerError)
  h.addPeer("p1", p.address(), "p1-pod")

  h.get(peersPath + "/p1/health") // 500 from peer => marked unhealthy
  assertEqual(t, "healthy after failed check", h.podsOf("p1")[p.address()].Healthy, false)

  assertStatus(t, h.post(peersPath+"/p1/remember", peerJSON("p1", p.address(), "p1-pod")), http.StatusOK)
  pods := h.podsOf("p1")
  assertEqual(t, "number of pods", len(pods), 1)
  assertEqual(t, "healthy after remember", pods[p.address()].Healthy, true)
  assertEqual(t, "offline after remember", pods[p.address()].Offline, false)
}

func TestPeerEpochs_AddRepeatedlyAndClear(t *testing.T) {
  h := newHarness(t)
  for i := 0; i < 3; i++ {
    h.addPeer("p3", "10.0.0.3:8080", "p3-pod")
  }
  peer := h.peers()["p3"]
  pod := peer.Pods["10.0.0.3:8080"]
  assertEqual(t, "current epoch", pod.CurrentEpoch.Epoch, 2)
  assertEqual(t, "past epochs", len(pod.PastEpochs), 2)
  assertEqual(t, "podEpochs", len(peer.PodEpochs["10.0.0.3:8080"]), 3)

  resp := h.post(peersPath+"/clear/epochs", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peers Epochs Cleared")
  peer = h.peers()["p3"]
  pod = peer.Pods["10.0.0.3:8080"]
  assertEqual(t, "current epoch number is retained", pod.CurrentEpoch.Epoch, 2)
  assertEqual(t, "past epochs after clear", len(pod.PastEpochs), 0)
  assertEqual(t, "podEpochs after clear", len(peer.PodEpochs["10.0.0.3:8080"]), 1)
}

func TestClearPeerEpochs_DropsPeersWithoutPods(t *testing.T) {
  h := newHarness(t)
  h.addPeer("gone", "10.0.0.4:8080", "gone-pod")
  h.addPeer("stay", "10.0.0.5:8080", "stay-pod")
  assertStatus(t, h.post(peersPath+"/gone/remove/10.0.0.4:8080", ""), http.StatusOK)
  // After removal the peer is still listed (with no pods) and its epoch history is retained.
  assertEqual(t, "pods of removed peer", len(h.peers()["gone"].Pods), 0)
  assertEqual(t, "epochs of removed peer", len(h.peers()["gone"].PodEpochs["10.0.0.4:8080"]), 1)

  assertStatus(t, h.post(peersPath+"/clear/epochs", ""), http.StatusOK)
  peers := h.peers()
  if _, found := peers["gone"]; found {
    t.Errorf("peer without pods should be dropped by clear/epochs: %v", peers)
  }
  if peers["stay"] == nil {
    t.Errorf("peer with pods should be retained by clear/epochs")
  }
}

func TestRemovePeer(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "pod-a")
  h.addPeer("p1", "10.0.0.2:8080", "pod-b")

  resp := h.post(peersPath+"/p1/remove/10.0.0.1:8080", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peer Removed: p1")
  pods := h.podsOf("p1")
  assertEqual(t, "pods left", len(pods), 1)
  if pods["10.0.0.2:8080"] == nil {
    t.Errorf("wrong pod removed: %v", pods)
  }

  // PUT is also accepted.
  resp = h.put(peersPath+"/p1/remove/10.0.0.2:8080", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "pods left", len(h.podsOf("p1")), 0)
}

func TestRemovePeer_UnknownPeer(t *testing.T) {
  h := newHarness(t)
  resp := h.post(peersPath+"/nope/remove/10.0.0.1:8080", "")
  assertStatus(t, resp, http.StatusNotAcceptable)
  assertContains(t, resp.Body, "Peer not found: nope")
}

func TestRemovePeer_KnownPeerUnknownAddressIsAcceptedQuirk(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "pod-a")
  // QUIRK: success is decided by the peer name alone; an unknown address still reports "Peer Removed".
  resp := h.post(peersPath+"/p1/remove/9.9.9.9:1", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peer Removed: p1")
  assertEqual(t, "pods left", len(h.podsOf("p1")), 1)
}

func TestCheckPeerHealth(t *testing.T) {
  h := newHarness(t)
  good := h.addFakePeer("p1", http.StatusOK)
  bad := newFakePeer(t, http.StatusInternalServerError)
  h.addPeer("p1", bad.address(), "p1-pod-bad")
  other := h.addFakePeer("p2", http.StatusOK)
  reset := func() { good.reset(); bad.reset(); other.reset() }

  t.Run("all peers", func(t *testing.T) {
    reset()
    resp := h.get(peersPath + "/health")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{
      "p1": {good.address(): true, bad.address(): false},
      "p2": {other.address(): true},
    })
    good.assertCalled(t, "GET", "/health")
    bad.assertCalled(t, "GET", "/health")
    other.assertCalled(t, "GET", "/health")
  })

  t.Run("pod health is recorded", func(t *testing.T) {
    pods := h.podsOf("p1")
    assertEqual(t, "good pod healthy", pods[good.address()].Healthy, true)
    assertEqual(t, "bad pod healthy", pods[bad.address()].Healthy, false)
  })

  t.Run("one peer", func(t *testing.T) {
    reset()
    resp := h.get(peersPath + "/p1/health")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {good.address(): true, bad.address(): false}})
    other.assertNoCalls(t)
  })

  t.Run("one pod of a peer", func(t *testing.T) {
    reset()
    resp := h.get(peersPath + "/p1/health/" + good.address())
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {good.address(): true}})
    bad.assertNoCalls(t)
    other.assertNoCalls(t)
  })

  t.Run("unknown peer and unknown address give empty results", func(t *testing.T) {
    // (v0.8.x listed the peer with an empty result, e.g. {"p9":{}})
    assertEqual(t, "unknown peer", h.get(peersPath+"/p9/health").peerResults(t), PeerResults{})
    assertEqual(t, "unknown address", h.get(peersPath+"/p1/health/1.2.3.4:5").peerResults(t), PeerResults{})
  })

  t.Run("unhealthy pod is re-checked and recovers", func(t *testing.T) {
    bad.setStatus(http.StatusOK)
    resp := h.get(peersPath + "/p1/health/" + bad.address())
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {bad.address(): true}})
    assertEqual(t, "bad pod healthy", h.podsOf("p1")[bad.address()].Healthy, true)
  })

  t.Run("unreachable pod is reported unhealthy", func(t *testing.T) {
    good.kill()
    resp := h.get(peersPath + "/p1/health/" + good.address())
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {good.address(): false}})
    assertEqual(t, "good pod healthy", h.podsOf("p1")[good.address()].Healthy, false)
  })
}

func TestCleanupUnhealthyPeers(t *testing.T) {
  h := newHarness(t)
  live := h.addFakePeer("p1", http.StatusOK)
  dead := newFakePeer(t, http.StatusOK)
  h.addPeer("p1", dead.address(), "p1-dead")
  onlyDead := newFakePeer(t, http.StatusOK)
  h.addPeer("p2", onlyDead.address(), "p2-dead")
  other := h.addFakePeer("p3", http.StatusOK)
  dead.kill()
  onlyDead.kill()

  // Cleanup a single peer: only its dead pod is removed.
  resp := h.post(peersPath+"/p1/health/cleanup", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {live.address(): true, dead.address(): false}})
  pods := h.podsOf("p1")
  assertEqual(t, "p1 pods", len(pods), 1)
  if pods[live.address()] == nil {
    t.Errorf("live pod should be retained: %v", pods)
  }
  assertEqual(t, "p2 pods untouched", len(h.podsOf("p2")), 1)
  other.assertNoCalls(t)

  // Cleanup all peers.
  resp = h.post(peersPath+"/health/cleanup", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "results", resp.peerResults(t), PeerResults{
    "p1": {live.address(): true}, "p2": {onlyDead.address(): false}, "p3": {other.address(): true}})
  // The peer entry itself is retained even when all its pods are removed.
  peers := h.peers()
  if peers["p2"] == nil {
    t.Fatalf("peer p2 should still be listed: %v", peers)
  }
  assertEqual(t, "p2 pods", len(peers["p2"].Pods), 0)
  assertEqual(t, "p3 pods", len(peers["p3"].Pods), 1)
}

func TestClearPeers(t *testing.T) {
  h := newHarness(t)
  // A live fake peer is required: targets/jobs are pushed to registered pods, and an unroutable
  // address would block until the registry's client timeout.
  h.addFakePeer("p1", http.StatusOK)
  h.post(peersPath+"/p1/targets/add", targetJSON)
  h.post(peersPath+"/p1/jobs/add", jobJSON)
  h.post(lockersPath+"/L1/store/a", "x")

  resp := h.post(peersPath+"/clear", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peers Cleared")
  assertEqual(t, "peers", h.get(peersPath).Body, "{}\n")
  assertEqual(t, "targets", h.get(peersPath+"/targets").Body, "{}\n")
  assertEqual(t, "jobs", h.get(peersPath+"/jobs").Body, "{}\n")
  // QUIRK: clearing peers also resets all labeled lockers.
  labels := h.get(lockersPath + "/labels").asMap(t)
  assertEqual(t, "locker labels", hasKey(labels, "L1"), false)
  assertEqual(t, "default locker", hasKey(labels, "default"), true)
}

func TestCopyPeersToLocker(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
  resp := h.post(peersPath+"/copyToLocker", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peers [len: 1] stored in labeled locker default under path 'peers'")

  stored := h.get(lockersPath + "/default/get/peers")
  assertStatus(t, stored, http.StatusOK)
  assertEqual(t, "stored peer name", dig(t, stored.asMap(t), "p1", "name"), "p1")

  // Copies go to the *current* locker.
  h.post(lockersPath+"/open/L1", "")
  resp = h.post(peersPath+"/copyToLocker", "")
  assertContains(t, resp.Body, "stored in labeled locker L1")
  assertStatus(t, h.get(lockersPath+"/L1/get/peers"), http.StatusOK)
  assertEqual(t, "peers in L1", hasKey(h.get(lockersPath+"/L1/get/peers").asMap(t), "p1"), true)
}

func TestGetPeersExportedHelper(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "pod-a")
  h.addPeer("p1", "10.0.0.2:8080", "pod-b")
  assertEqual(t, "pod name -> address", GetPeers("p1", nil), map[string]string{"pod-a": "10.0.0.1:8080", "pod-b": "10.0.0.2:8080"})
}

// ---------------------------------------------------------------------------------------------
// Labeled lockers
// ---------------------------------------------------------------------------------------------

func TestOpenLabeledLocker(t *testing.T) {
  h := newHarness(t)
  resp := h.post(lockersPath+"/open/L1", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Locker L1 is open and active")

  labels := h.get(lockersPath + "/labels")
  assertStatus(t, labels, http.StatusOK)
  assertEqual(t, "L1 is current", dig(t, labels.asMap(t), "L1", "current"), true)
  assertEqual(t, "default is not current", dig(t, labels.asMap(t), "default", "current"), false)

  // "current" is an alias for the locker that was opened last.
  current := h.get(lockersPath + "/current")
  assertStatus(t, current, http.StatusOK)
  assertEqual(t, "current label", dig(t, current.asMap(t), "label"), "L1")

  // Opening another locker moves "current".
  h.post(lockersPath+"/open/L2", "")
  assertEqual(t, "current label", dig(t, h.get(lockersPath+"/current").asMap(t), "label"), "L2")
  assertEqual(t, "L1 current flag", dig(t, h.get(lockersPath+"/labels").asMap(t), "L1", "current"), false)
}

func TestOpenLabeledLocker_CommaSeparatedLabelCreatesSubLocker(t *testing.T) {
  h := newHarness(t)
  assertStatus(t, h.post(lockersPath+"/open/A,B", ""), http.StatusOK)
  labels := h.get(lockersPath + "/labels").asMap(t)
  assertEqual(t, "A present", hasKey(labels, "A"), true)
  assertContains(t, h.get(lockersPath+"/labels").Body, `"A,B"`)
  assertEqual(t, "A,B locker reachable", h.get(lockersPath+"/A,B").Code, http.StatusOK)
  assertEqual(t, "current locker", dig(t, h.get(lockersPath+"/current").asMap(t), "label"), "B")
}

func TestStoreInLabeledLocker_CreatesLockerAndStoresData(t *testing.T) {
  h := newHarness(t)
  resp := h.post(lockersPath+"/L1/store/a,b", "hello")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Data stored in [Context: ] labeled locker L1 for path [a b]")
  assertEqual(t, "L1 created", hasKey(h.get(lockersPath+"/labels").asMap(t), "L1"), true)
  // Storing does not change which locker is current.
  assertEqual(t, "current label", dig(t, h.get(lockersPath+"/current").asMap(t), "label"), "default")

  // "current" can be used as a label to store in the current locker.
  assertStatus(t, h.post(lockersPath+"/current/store/c", "in-current"), http.StatusOK)
  assertEqual(t, "stored in default", h.get(lockersPath+"/default/get/c").Body, "in-current")
}

func TestGetFromDataLocker(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/L1/store/a,c", "world")

  t.Run("leaf key returns raw data", func(t *testing.T) {
    // Exact key => raw value, with or without ?data=y.
    assertEqual(t, "body", h.get(lockersPath+"/L1/get/a,b").Body, "hello")
    assertEqual(t, "body with data=y", h.get(lockersPath+"/L1/get/a,b?data=y").Body, "hello")
  })

  t.Run("intermediate key returns a masked subtree unless data=y", func(t *testing.T) {
    resp := h.get(lockersPath + "/L1/get/a")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "a.b masked", dig(t, resp.asMap(t), "a", "b", "data"), "...")
    assertEqual(t, "a.c masked", dig(t, resp.asMap(t), "a", "c", "data"), "...")

    resp = h.get(lockersPath + "/L1/get/a?data=y&level=0")
    assertEqual(t, "a.b", dig(t, resp.asMap(t), "a", "b", "data"), "hello")
    assertEqual(t, "a.c", dig(t, resp.asMap(t), "a", "c", "data"), "world")
    assertEqual(t, "firstReported present", hasKey(resp.asMap(t), "a", "firstReported"), true)

    assertStatus(t, h.get(lockersPath+"/L1/get/a?data=y&level=1"), http.StatusOK)
  })

  t.Run("missing path or locker returns empty object", func(t *testing.T) {
    for _, target := range []string{lockersPath + "/L1/get/zzz", lockersPath + "/NOPE/get/a,b"} {
      resp := h.get(target)
      assertStatus(t, resp, http.StatusOK)
      assertEqual(t, target, resp.Body, "{}\n")
    }
  })

  t.Run("query-less and query routes agree", func(t *testing.T) {
    assertEqual(t, "level=0", h.get(lockersPath+"/L1/get/a,b?level=0").Body, "hello")
  })
}

func TestRemoveFromLabeledLocker(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/L1/store/a,c", "world")

  resp := h.post(lockersPath+"/L1/remove/a,b", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Data removed from labeled locker L1 for path [a b]")
  assertEqual(t, "removed key", h.get(lockersPath+"/L1/get/a,b").Body, "{}\n")
  assertEqual(t, "sibling retained", h.get(lockersPath+"/L1/get/a,c").Body, "world")
}

func TestGetLabeledLocker(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/open/L1", "")

  resp := h.get(lockersPath + "/L1")
  assertStatus(t, resp, http.StatusOK)
  locker := resp.asMap(t)
  assertEqual(t, "label", dig(t, locker, "label"), "L1")
  assertEqual(t, "current", dig(t, locker, "current"), true)
  assertEqual(t, "masked data", dig(t, locker, "dataLocker", "a", "b", "data"), "...")

  resp = h.get(lockersPath + "/L1?data=y&events=n&peers=n&level=2")
  assertEqual(t, "data", dig(t, resp.asMap(t), "dataLocker", "a", "b", "data"), "hello")

  resp = h.get(lockersPath + "/current")
  assertEqual(t, "current alias", dig(t, resp.asMap(t), "label"), "L1")

  resp = h.get(lockersPath + "/NOPE")
  assertStatus(t, resp, http.StatusNotFound)
  assertEqual(t, "body", resp.Body, "Locker not found")
}

func TestGetLabeledLocker_PeersAndEventsAreOptIn(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
  h.post(peersPath+"/p1/locker/store/k1", "v1")
  h.post(peersPath+"/p1/10.0.0.1:8080/events/store", `{"title":"Ev","summary":"s","data":"d","at":"2026-10-12T10:00:00Z","peer":"p1"}`)

  plain := h.get(lockersPath + "/default?data=y&events=n&peers=n&level=2").asMap(t)
  assertEqual(t, "peer lockers hidden by default", len(dig(t, plain, "peerLockers").(map[string]interface{})), 0)

  withPeers := h.get(lockersPath + "/default?data=y&events=n&peers=y&level=2").asMap(t)
  assertEqual(t, "peer data included", dig(t, withPeers, "peerLockers", "p1", "k1", "data"), "v1")
  assertEqual(t, "events hidden by default", hasKey(withPeers, "peerLockers", "p1", "events"), false)

  // Events need level=0 (unlimited) or a deeper level to be reported alongside peer data.
  withEvents := h.get(lockersPath + "/default?data=y&peers=y&events=y&level=0").asMap(t)
  // (events reported by a peer instance are kept in the locker of that instance)
  assertEqual(t, "events included", hasKey(withEvents, "peerLockers", "p1", "instanceLockers", "10.0.0.1:8080", "events", "1"), true)
  assertEqual(t, "peer data still included", dig(t, withEvents, "peerLockers", "p1", "k1", "data"), "v1")

  // Unlike v0.8.x, peer lockers are not trimmed away at the default level (2) when events are requested.
  trimmed := h.get(lockersPath + "/default?data=y&events=y&peers=y&level=2").asMap(t)
  assertEqual(t, "peer data included at level 2", dig(t, trimmed, "peerLockers", "p1", "k1", "data"), "v1")
}

func TestGetAllLockers(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")

  resp := h.get(lockersPath)
  assertStatus(t, resp, http.StatusOK)
  all := resp.asMap(t)
  assertEqual(t, "default present", hasKey(all, "default"), true)
  assertEqual(t, "L1 masked", dig(t, all, "L1", "dataLocker", "a", "b", "data"), "...")

  resp = h.get(lockersPath + "?data=y&peers=y&events=y&level=3")
  assertEqual(t, "L1 data", dig(t, resp.asMap(t), "L1", "dataLocker", "a", "b", "data"), "hello")
}

func TestGetDataLockers(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")

  resp := h.get(lockersPath + "/data")
  assertStatus(t, resp, http.StatusOK)
  all := resp.asMap(t)
  assertEqual(t, "L1 masked", dig(t, all, "L1", "a", "b", "data"), "...")
  assertEqual(t, "default is empty", len(dig(t, all, "default").(map[string]interface{})), 0)

  resp = h.get(lockersPath + "/data?data=y&level=2")
  assertEqual(t, "L1 data", dig(t, resp.asMap(t), "L1", "a", "b", "data"), "hello")

  resp = h.get(lockersPath + "/L1/data")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "only L1", hasKey(resp.asMap(t), "default"), false)
  assertEqual(t, "L1 masked", dig(t, resp.asMap(t), "L1", "a", "b", "data"), "...")

  assertEqual(t, "L1 data", dig(t, h.get(lockersPath+"/L1/data?data=y&level=2").asMap(t), "L1", "a", "b", "data"), "hello")
}

// The "level" query param limits how deep keys are reported (default 2 for locker views, 5 for /lockers).
// Keys below the limit are replaced with a "..." marker; level=0 means unlimited.
func TestGetDataLockers_LevelTrimsDepth(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/L1/store/a,b,c,d", "deep")

  resp := h.get(lockersPath + "/data?data=y&level=1").asMap(t)
  assertEqual(t, "level=1 shows a", hasKey(resp, "L1", "a"), true)
  assertEqual(t, "level=1 truncates a's children", hasKey(resp, "L1", "a", "..."), true)
  assertEqual(t, "level=1 hides a.b", hasKey(resp, "L1", "a", "b"), false)

  resp = h.get(lockersPath + "/data?data=y&level=2").asMap(t)
  assertEqual(t, "level=2 shows a.b", dig(t, resp, "L1", "a", "b", "data"), "hello")
  assertEqual(t, "level=2 truncates b's children", hasKey(resp, "L1", "a", "b", "..."), true)
  assertEqual(t, "level=2 hides a.b.c", hasKey(resp, "L1", "a", "b", "c"), false)

  resp = h.get(lockersPath + "/data?data=y&level=0").asMap(t)
  assertEqual(t, "level=0 is unlimited", dig(t, resp, "L1", "a", "b", "c", "d", "data"), "deep")

  // The per-locker view and /lockers apply the same trimming.
  assertEqual(t, "locker view level=1", hasKey(h.get(lockersPath+"/L1?data=y&events=n&peers=n&level=1").asMap(t), "dataLocker", "a", "..."), true)
  assertEqual(t, "all lockers level=1", hasKey(h.get(lockersPath+"?data=y&events=n&peers=n&level=1").asMap(t), "L1", "dataLocker", "a", "..."), true)
  assertEqual(t, "all lockers level=0", dig(t, h.get(lockersPath+"?data=y&events=n&peers=n&level=0").asMap(t), "L1", "dataLocker", "a", "b", "c", "d", "data"), "deep")
}

// For /get/{path}, level is relative to the requested path.
func TestGetFromDataLocker_LevelIsRelativeToPath(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/L1/store/a,b,c,d", "deep")

  resp := h.get(lockersPath + "/L1/get/a?data=y&level=1").asMap(t)
  assertEqual(t, "a.b data", dig(t, resp, "a", "b", "data"), "hello")
  assertEqual(t, "b children truncated", hasKey(resp, "a", "b", "..."), true)
  assertEqual(t, "a.b.c hidden", hasKey(resp, "a", "b", "c"), false)

  resp = h.get(lockersPath + "/L1/get/a?data=y&level=2").asMap(t)
  assertEqual(t, "a.b.c truncated", hasKey(resp, "a", "b", "c", "..."), true)

  resp = h.get(lockersPath + "/L1/get/a?data=y&level=0").asMap(t)
  assertEqual(t, "no level => full depth", dig(t, resp, "a", "b", "c", "d", "data"), "deep")
}

func TestGetDataLockerKeysAndPaths(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/L1/store/a,c", "world")

  keysFor := func(target string) []string {
    resp := h.get(target)
    assertStatus(t, resp, http.StatusOK)
    keys := []string{}
    for _, k := range dig(t, resp.asMap(t), "L1", "data").([]interface{}) {
      keys = append(keys, k.(string))
    }
    sort.Strings(keys)
    return keys
  }
  assertEqual(t, "keys of L1", keysFor(lockersPath+"/L1/data/keys"), []string{"a,b", "a,c"})
  assertEqual(t, "keys of all lockers", keysFor(lockersPath+"/data/keys"), []string{"a,b", "a,c"})

  for _, target := range []string{lockersPath + "/L1/data/paths", lockersPath + "/data/paths"} {
    resp := h.get(target)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, target+" a,b", dig(t, resp.asMap(t), "L1", "data", "a", "b"), "/registry/lockers/L1/get/a,b")
    assertEqual(t, target+" a,c", dig(t, resp.asMap(t), "L1", "data", "a", "c"), "/registry/lockers/L1/get/a,c")
  }

  // Keys/paths of a locker that doesn't exist: nothing to report.
  assertEqual(t, "unknown locker", h.get(lockersPath+"/NOPE/data/keys").Body, "{}\n")
}

func TestSearchInDataLockers(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/greeting", "hello")
  h.post(lockersPath+"/L1/store/ab,abc", "x")

  t.Run("matches keys case-insensitively and returns URIs", func(t *testing.T) {
    for _, text := range []string{"greet", "GREET"} {
      resp := h.get(lockersPath + "/L1/search/" + text)
      assertStatus(t, resp, http.StatusOK)
      assertEqual(t, text, dig(t, resp.asMap(t), "L1", "data", "greeting"), "/registry/lockers/L1/get/greeting")
    }
  })

  t.Run("search without label covers all lockers", func(t *testing.T) {
    resp := h.get(lockersPath + "/search/ab")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "nested match", dig(t, resp.asMap(t), "L1", "data", "ab", "abc"), "/registry/lockers/L1/get/ab,abc")
    assertEqual(t, "default locker is searched too", hasKey(resp.asMap(t), "default"), true)
  })

  t.Run("no match", func(t *testing.T) {
    resp := h.get(lockersPath + "/L1/search/qqq")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "L1 data", dig(t, resp.asMap(t), "L1", "data"), nil)
  })

  t.Run("keys must match at every level of the path", func(t *testing.T) {
    // QUIRK: the pattern is applied to each key on the way down, so searching for a leaf key ("abc")
    // doesn't find it unless its parent key ("ab") matches as well.
    resp := h.get(lockersPath + "/L1/search/abc")
    assertEqual(t, "leaf-only match", dig(t, resp.asMap(t), "L1", "data"), nil)
  })
}

func TestSearchInDataLockers_InvalidRegexp(t *testing.T) {
  h := newHarness(t)
  // The search text is a regular expression; an invalid one is a bad request (it used to panic the handler).
  for _, target := range []string{"/search/%28", "/L1/search/%28"} {
    resp := h.get(lockersPath + target)
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "Invalid search expression")
  }
}

func TestCloseOrClearLabeledLocker_Close(t *testing.T) {
  for _, closeURI := range []string{"/close/L1", "/L1/close"} {
    t.Run(closeURI, func(t *testing.T) {
      h := newHarness(t)
      h.post(lockersPath+"/open/L1", "")
      h.post(lockersPath+"/L1/store/a", "x")

      resp := h.post(lockersPath+closeURI, "")
      assertStatus(t, resp, http.StatusOK)
      assertContains(t, resp.Body, "Locker L1 is closed")
      labels := h.get(lockersPath + "/labels").asMap(t)
      assertEqual(t, "L1 closed", hasKey(labels, "L1"), false)
      // Closing the current locker makes the default locker current again.
      assertEqual(t, "default is current", dig(t, labels, "default", "current"), true)
      assertEqual(t, "L1 lookup", h.get(lockersPath+"/L1").Code, http.StatusNotFound)
    })
  }
}

func TestCloseOrClearLabeledLocker_DefaultLockerCannotBeClosed(t *testing.T) {
  h := newHarness(t)
  for _, target := range []string{"/close/default", "/default/close", "/close/DEFAULT"} {
    resp := h.post(lockersPath+target, "")
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "Default locker cannot be closed")
  }
  assertEqual(t, "default still present", hasKey(h.get(lockersPath+"/labels").asMap(t), "default"), true)
}

func TestCloseOrClearLabeledLocker_Clear(t *testing.T) {
  h := newHarness(t)
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/default/store/keep", "me")

  resp := h.post(lockersPath+"/L1/clear", "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Locker L1 is cleared")
  assertEqual(t, "data cleared", h.get(lockersPath+"/L1/get/a,b").Body, "{}\n")
  assertEqual(t, "locker still exists", h.get(lockersPath+"/L1").Code, http.StatusOK)
  assertEqual(t, "other locker untouched", h.get(lockersPath+"/default/get/keep").Body, "me")

  // The default locker can be cleared (but not closed).
  assertStatus(t, h.post(lockersPath+"/default/clear", ""), http.StatusOK)
  assertEqual(t, "default cleared", h.get(lockersPath+"/default/get/keep").Body, "{}\n")
}

func TestCloseOrClearLabeledLocker_UnknownLabelIsNotFound(t *testing.T) {
  h := newHarness(t)
  // v0.8.x answered 200 "Locker NOPE is cleared/closed" for a locker that doesn't exist.
  for _, target := range []string{"/NOPE/clear", "/NOPE/close", "/clear/NOPE", "/close/NOPE"} {
    resp := h.post(lockersPath+target, "")
    assertStatus(t, resp, http.StatusNotFound)
    assertContains(t, resp.Body, "Locker not found")
  }
}

func TestCloseOrClearLabeledLocker_WithoutLabelResetsAllLockersAndClearsPeers(t *testing.T) {
  for _, target := range []string{"/clear", "/close"} {
    t.Run(target, func(t *testing.T) {
      h := newHarness(t)
      p := h.addFakePeer("p1", http.StatusOK)
      h.post(lockersPath+"/L1/store/a", "x")
      h.post(lockersPath+"/L2/store/b", "y")

      resp := h.post(lockersPath+target, "")
      assertStatus(t, resp, http.StatusOK)
      // Body is the per-pod result of clearing events and results on peers.
      assertEqual(t, "peer results", PeerResults{"p1": {p.address(): true}}, func() PeerResults {
        r := PeerResults{}
        if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Body)), &r); err != nil {
          t.Fatalf("unexpected body %q: %v", resp.Body, err)
        }
        return r
      }())
      p.assertCalled(t, "POST", "/events/clear")
      p.assertCalled(t, "POST", "/client/results/clear")

      labels := h.get(lockersPath + "/labels").asMap(t)
      assertEqual(t, "L1 gone", hasKey(labels, "L1"), false)
      assertEqual(t, "L2 gone", hasKey(labels, "L2"), false)
      assertEqual(t, "default remains", hasKey(labels, "default"), true)
    })
  }
}

func TestGetLockerLabels_Default(t *testing.T) {
  h := newHarness(t)
  resp := h.get(lockersPath + "/labels")
  assertStatus(t, resp, http.StatusOK)
  labels := resp.asMap(t)
  assertEqual(t, "labels", len(labels), 1)
  assertEqual(t, "default is current", dig(t, labels, "default", "current"), true)
  assertEqual(t, "firstOpened present", hasKey(labels, "default", "firstOpened"), true)
}

// ---------------------------------------------------------------------------------------------
// Peer lockers, peer events, targets results
// ---------------------------------------------------------------------------------------------

const eventOne = `{"title":"Ev One","summary":"first event","data":"payload","at":"2026-10-12T10:00:00Z","peer":"p1","peerHost":"h"}`
const eventTwo = `{"title":"Ev Two","summary":"second event","data":"payload2","at":"2026-10-12T10:00:05Z","peer":"p1","peerHost":"h"}`

// v0.8.x panicked here (nil passed to util.WriteJsonPayload): with the current locker label, or without a label
// (the peer routes under /registry/peers read the current locker), or for a key that doesn't exist.
func TestGetFromPeerLocker_CurrentLockerAndMissingData(t *testing.T) {
  t.Run("no label and only the default locker exists", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
    h.post(peersPath+"/p1/locker/store/k1", "v1")
    assertEqual(t, "body", h.get(peersPath+"/p1/locker/get/k1").Body, "v1")
  })
  t.Run("current label", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
    h.post(peersPath+"/p1/locker/store/k1", "v1")
    assertEqual(t, "body", h.get(lockersPath+"/current/peers/p1/get/k1").Body, "v1")
  })
  t.Run("key or peer that does not exist", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
    h.post(peersPath+"/p1/locker/store/k1", "v1")
    assertEqual(t, "existing key", h.get(lockersPath+"/default/peers/p1/get/k1").Body, "v1")
    for _, target := range []string{
      lockersPath + "/default/peers/p1/get/nope", lockersPath + "/default/peers/p9/get/k1",
      lockersPath + "/current/peers/p1/get/nope", peersPath + "/p1/locker/get/nope", peersPath + "/p9/locker/get/k1",
    } {
      resp := h.get(target)
      assertStatus(t, resp, http.StatusOK)
      assertEqual(t, target, resp.Body, "{}\n")
    }
  })
}

func TestPeerLocker_StoreGetAndRemove(t *testing.T) {
  h := newHarness(t)
  addr := "10.0.0.1:8080"
  h.addPeer("p1", addr, "p1-pod")

  resp := h.post(peersPath+"/p1/locker/store/k1", "v1")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peer p1 data stored for path [k1]")
  resp = h.post(peersPath+"/p1/"+addr+"/locker/store/k2,x", "v2")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Peer p1 data stored for path [k2 x]")

  t.Run("get without locker label reads the current locker", func(t *testing.T) {
    // v0.8.x reported the value per locker, e.g. {"default":"v1"}. The README documents the current locker.
    resp := h.get(peersPath + "/p1/locker/get/k1")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "k1", resp.Body, "v1")
    // ?data=y makes no difference for a leaf key.
    assertEqual(t, "k1 data=y", h.get(peersPath+"/p1/locker/get/k1?data=y&level=1").Body, "v1")
  })

  t.Run("get with locker label returns the raw value", func(t *testing.T) {
    assertEqual(t, "body", h.get(lockersPath+"/default/peers/p1/get/k1").Body, "v1")
    assertEqual(t, "body with data", h.get(lockersPath+"/default/peers/p1/get/k1?data=y&level=1").Body, "v1")
  })

  t.Run("get for a specific pod address", func(t *testing.T) {
    resp := h.get(peersPath + "/p1/" + addr + "/locker/get/k2,x")
    assertEqual(t, "k2,x", resp.Body, "v2")
    assertEqual(t, "via locker label", h.get(lockersPath+"/default/peers/p1/"+addr+"/get/k2,x").Body, "v2")
    assertEqual(t, "via locker label and /locker/", h.get(lockersPath+"/default/peers/p1/"+addr+"/locker/get/k2,x").Body, "v2")
    assertEqual(t, "via locker label without address", h.get(lockersPath+"/default/peers/p1/locker/get/k1").Body, "v1")
  })

  t.Run("missing key, peer or locker give an empty object", func(t *testing.T) {
    assertEqual(t, "missing key", h.get(peersPath+"/p1/locker/get/nope").Body, "{}\n")
    assertEqual(t, "unknown peer", h.get(peersPath+"/p9/locker/get/k1").Body, "{}\n")
  })

  t.Run("remove", func(t *testing.T) {
    resp := h.post(peersPath+"/p1/locker/remove/k1", "")
    assertStatus(t, resp, http.StatusOK)
    assertContains(t, resp.Body, "Peer p1 data removed for path [k1]")
    assertEqual(t, "after remove", h.get(peersPath+"/p1/locker/get/k1").Body, "{}\n")
    // pod-level data is not affected by removing peer-level data
    assertEqual(t, "pod data retained", h.get(lockersPath+"/default/peers/p1/"+addr+"/get/k2,x").Body, "v2")

    resp = h.post(peersPath+"/p1/"+addr+"/locker/remove/k2,x", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "pod data removed", h.get(peersPath+"/p1/"+addr+"/locker/get/k2,x").Body, "{}\n")
  })
}

func TestGetPeerLocker(t *testing.T) {
  h := newHarness(t)
  addr := "10.0.0.1:8080"
  h.addPeer("p1", addr, "p1-pod")
  h.addPeer("p2", "10.0.0.2:8080", "p2-pod")
  h.post(peersPath+"/p1/locker/store/k1", "v1")
  h.post(peersPath+"/p1/"+addr+"/locker/store/k2,x", "v2")

  t.Run("all peers of the current locker", func(t *testing.T) {
    resp := h.get(peersPath + "/lockers")
    assertStatus(t, resp, http.StatusOK)
    all := resp.asMap(t)
    assertEqual(t, "p1 masked", dig(t, all, "p1", "default", "k1", "data"), "...")
    assertEqual(t, "p1 pod data", hasKey(all, "p1", "default", "instanceLockers", addr, "k2"), true)
    assertEqual(t, "p2 present", hasKey(all, "p2"), true)

    resp = h.get(peersPath + "/lockers?data=y&events=n&level=3")
    assertEqual(t, "p1 data", dig(t, resp.asMap(t), "p1", "default", "k1", "data"), "v1")
    assertEqual(t, "p1 pod data", dig(t, resp.asMap(t), "p1", "default", "instanceLockers", addr, "k2", "x", "data"), "v2")
  })

  t.Run("one peer", func(t *testing.T) {
    resp := h.get(peersPath + "/p1/lockers?data=y&events=n&level=3")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "p2 excluded", hasKey(resp.asMap(t), "p2"), false)
    assertEqual(t, "p1 data", dig(t, resp.asMap(t), "p1", "default", "k1", "data"), "v1")
  })

  t.Run("one pod of a peer", func(t *testing.T) {
    resp := h.get(peersPath + "/p1/" + addr + "/lockers?data=y&events=n&level=2")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "pod data", dig(t, resp.asMap(t), "p1", "default", "k2", "x", "data"), "v2")
  })

  t.Run("via locker label", func(t *testing.T) {
    for _, target := range []string{"/default/peers", "/default/peers/p1", "/default/peers/p1/" + addr, "/current/peers/p1"} {
      assertStatus(t, h.get(lockersPath+target), http.StatusOK)
    }
    assertEqual(t, "p1 via label", hasKey(h.get(lockersPath+"/default/peers/p1?data=y&events=n&level=3").asMap(t), "p1"), true)
    assertEqual(t, "p2 filtered out", hasKey(h.get(lockersPath+"/default/peers/p1?data=y&events=n&level=3").asMap(t), "p2"), false)
  })

  t.Run("unknown locker", func(t *testing.T) {
    for _, target := range []string{"/NOPE/peers", "/NOPE/peers/p1", "/NOPE/peers/p1/" + addr} {
      resp := h.get(lockersPath + target)
      assertStatus(t, resp, http.StatusNotFound)
      assertEqual(t, target, resp.Body, "Locker not found")
    }
  })

  t.Run("locker without peer data", func(t *testing.T) {
    h.post(lockersPath+"/open/L1", "")
    // Peers added before L1 was opened live in the default locker, not L1.
    resp := h.get(lockersPath + "/L1/peers")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "no peers in L1", strings.TrimSpace(resp.Body), "")
  })
}

func TestStorePeerEvent_AndGetPeerEvents(t *testing.T) {
  h := newHarness(t)
  addr := "10.0.0.1:8080"
  h.addPeer("p1", addr, "p1-pod")

  t.Run("unknown peer is rejected", func(t *testing.T) {
    resp := h.post(peersPath+"/p9/"+addr+"/events/store", eventOne)
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "No Peer")
  })

  t.Run("events are numbered per peer", func(t *testing.T) {
    resp := h.post(peersPath+"/p1/"+addr+"/events/store", eventOne)
    assertStatus(t, resp, http.StatusOK)
    assertContains(t, resp.Body, "Peer [p1] Instance Label ["+addr+"] event [1] stored")
    resp = h.post(peersPath+"/p1/"+addr+"/events/store", eventTwo)
    assertStatus(t, resp, http.StatusOK)
    assertContains(t, resp.Body, "Peer [p1] Instance Label ["+addr+"] event [2] stored")
  })

  titles := func(resp response, peer string) []string {
    assertStatus(t, resp, http.StatusOK)
    events := dig(t, resp.asMap(t), peer).([]interface{})
    titles := []string{}
    for _, e := range events {
      titles = append(titles, e.(map[string]interface{})["title"].(string))
    }
    return titles
  }
  firstEventData := func(resp response, peer string) interface{} {
    return dig(t, resp.asMap(t), peer).([]interface{})[0].(map[string]interface{})["data"]
  }

  t.Run("by peer within a locker", func(t *testing.T) {
    resp := h.get(lockersPath + "/default/peers/p1/events")
    assertEqual(t, "titles", titles(resp, "p1"), []string{"Ev One", "Ev Two"})
    assertEqual(t, "data is masked by default", firstEventData(resp, "p1"), "...")

    resp = h.get(lockersPath + "/default/peers/p1/events?data=y&reverse=n")
    assertEqual(t, "data", firstEventData(resp, "p1"), "payload")

    resp = h.get(lockersPath + "/default/peers/p1/events?data=y&reverse=y")
    assertEqual(t, "reversed titles", titles(resp, "p1"), []string{"Ev Two", "Ev One"})
  })

  t.Run("multiple peers by comma separated list and unknown peers", func(t *testing.T) {
    h.addPeer("p2", "10.0.0.2:8080", "p2-pod")
    assertEqual(t, "p1,p2", titles(h.get(lockersPath+"/default/peers/p1,p2/events"), "p1"), []string{"Ev One", "Ev Two"})
    resp := h.get(lockersPath + "/default/peers/p9/events")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "unknown peer", resp.Body, "{}\n")
  })

  t.Run("all peers, unified", func(t *testing.T) {
    resp := h.get(peersPath + "/events?data=y&reverse=n&unified=y")
    assertEqual(t, "unified titles", titles(resp, "all"), []string{"Ev One", "Ev Two"})
    // With an explicit locker, unified results are keyed by the locker label instead of "all".
    resp = h.get(lockersPath + "/default/peers/events?data=n&reverse=n&unified=y")
    assertEqual(t, "unified titles via locker", titles(resp, "default"), []string{"Ev One", "Ev Two"})
  })

  t.Run("locker labels current and all", func(t *testing.T) {
    for _, label := range []string{"current", "all", "default"} {
      assertEqual(t, label, titles(h.get(lockersPath+"/"+label+"/peers/events"), "p1"), []string{"Ev One", "Ev Two"})
    }
  })

  t.Run("unknown locker", func(t *testing.T) {
    resp := h.get(lockersPath + "/NOPE/peers/events")
    assertStatus(t, resp, http.StatusNotFound)
    assertEqual(t, "body", resp.Body, "Locker not found")
  })

  t.Run("peer name in /registry/peers/{peer}/events filters the events", func(t *testing.T) {
    // (v0.8.x and the first main ignored {peer} here and returned the events of all peers)
    assertEqual(t, "p1", titles(h.get(peersPath+"/p1/events"), "p1"), []string{"Ev One", "Ev Two"})
    resp := h.get(peersPath + "/p9/events")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "unknown peer", resp.Body, "{}\n")
    resp = h.get(peersPath + "/p2/events")
    assertEqual(t, "peer without events", resp.Body, "{}\n")
  })
}

func TestSearchInPeerEvents(t *testing.T) {
  h := newHarness(t)
  addr := "10.0.0.1:8080"
  h.addPeer("p1", addr, "p1-pod")
  h.post(peersPath+"/p1/"+addr+"/events/store", eventOne)
  h.post(peersPath+"/p1/"+addr+"/events/store", eventTwo)

  titles := func(resp response, peer string) []string {
    assertStatus(t, resp, http.StatusOK)
    titles := []string{}
    for _, e := range dig(t, resp.asMap(t), peer).([]interface{}) {
      titles = append(titles, e.(map[string]interface{})["title"].(string))
    }
    return titles
  }

  assertEqual(t, "match on title", titles(h.get(peersPath+"/events/search/Two"), "p1"), []string{"Ev Two"})
  assertEqual(t, "match on data", titles(h.get(lockersPath+"/default/peers/p1/events/search/payload2"), "p1"), []string{"Ev Two"})
  assertEqual(t, "regexp", titles(h.get(lockersPath+"/default/peers/events/search/Ev%20(One|Two)"), "p1"), []string{"Ev One", "Ev Two"})
  assertEqual(t, "reverse", titles(h.get(lockersPath+"/default/peers/p1/events/search/Ev?data=n&reverse=y"), "p1"), []string{"Ev Two", "Ev One"})
  assertEqual(t, "unified", titles(h.get(peersPath+"/events/search/Ev?data=n&reverse=n&unified=y"), "all"), []string{"Ev One", "Ev Two"})

  resp := h.get(peersPath + "/events/search/payload?data=y&reverse=n")
  assertEqual(t, "data revealed", dig(t, resp.asMap(t), "p1").([]interface{})[0].(map[string]interface{})["data"], "payload")
  resp = h.get(peersPath + "/events/search/payload")
  assertEqual(t, "data masked", dig(t, resp.asMap(t), "p1").([]interface{})[0].(map[string]interface{})["data"], "...")

  resp = h.get(peersPath + "/events/search/zzz")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "no match", resp.Body, "{}\n")

  resp = h.get(lockersPath + "/NOPE/peers/events/search/Ev")
  assertStatus(t, resp, http.StatusNotFound)
  assertEqual(t, "unknown locker", resp.Body, "Locker not found")

  // {peer} filters on /registry/peers/{peer}/events/search/{text}, as it does on /registry/peers/{peer}/events.
  assertEqual(t, "peer filter", titles(h.get(peersPath+"/p1/events/search/Two"), "p1"), []string{"Ev Two"})
  assertEqual(t, "unknown peer", h.get(peersPath+"/p9/events/search/Two").Body, "{}\n")

  // The text is a regular expression, an invalid one is a bad request (it used to panic the handler).
  resp = h.get(peersPath + "/events/search/%28")
  assertStatus(t, resp, http.StatusBadRequest)
  assertContains(t, resp.Body, "Invalid search expression")
}

// v0.8.x paths to report client results of peers. They are kept as aliases of main's /client/results APIs, so the
// output has main's format: a summary per peer, or {"summary", "details"} with ?detailed=y.
func TestGetTargetsSummaryResults_V08xCompatPaths(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "p1-pod")

  t.Run("summary", func(t *testing.T) {
    for _, target := range []string{
      peersPath + "/p1/targets/results",
      peersPath + "/lockers/targets/results",
      lockersPath + "/default/peers/p1/targets/results",
      lockersPath + "/current/peers/targets/results",
    } {
      resp := h.get(target)
      assertStatus(t, resp, http.StatusOK)
      assertEqual(t, target, resp.asMap(t), map[string]interface{}{"p1": map[string]interface{}{}})
      assertEqual(t, target+" same as the main API", resp.asMap(t), h.get(peersPath+"/p1/client/results/summary").asMap(t))
    }
  })

  t.Run("detailed", func(t *testing.T) {
    want := map[string]interface{}{
      "summary": map[string]interface{}{"p1": map[string]interface{}{}},
      "details": map[string]interface{}{"p1": map[string]interface{}{}},
    }
    for _, target := range []string{peersPath + "/p1/targets/results?detailed=y", lockersPath + "/default/peers/p1/targets/results?detailed=y"} {
      resp := h.get(target)
      assertStatus(t, resp, http.StatusOK)
      assertEqual(t, target, resp.asMap(t), want)
    }
  })

  t.Run("unknown locker", func(t *testing.T) {
    resp := h.get(lockersPath + "/NOPE/peers/targets/results")
    assertStatus(t, resp, http.StatusNotFound)
    assertEqual(t, "body", resp.Body, "Locker not found")
  })
}

func TestClearLocker(t *testing.T) {
  h := newHarness(t)
  a := h.addFakePeer("p1", http.StatusOK)
  b := newFakePeer(t, http.StatusOK)
  h.addPeer("p1", b.address(), "p1-pod-b")
  c := h.addFakePeer("p2", http.StatusOK)
  resetCalls := func() { a.reset(); b.reset(); c.reset() }
  store := func() {
    h.post(peersPath+"/p1/locker/store/k1", "v1")
    h.post(peersPath+"/p1/"+a.address()+"/locker/store/ka,x", "va")
    h.post(peersPath+"/p1/"+b.address()+"/locker/store/kb,x", "vb")
    h.post(peersPath+"/p2/locker/store/k2", "v2")
  }
  assertCleared := func(t *testing.T, calls ...*fakePeer) {
    t.Helper()
    for _, p := range calls {
      p.assertCalled(t, "POST", "/events/clear")
      p.assertCalled(t, "POST", "/client/results/clear")
    }
  }

  t.Run("one pod", func(t *testing.T) {
    store()
    resetCalls()
    resp := h.post(peersPath+"/p1/"+a.address()+"/locker/clear", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {a.address(): true}})
    assertCleared(t, a)
    b.assertNoCalls(t)
    c.assertNoCalls(t)
    assertEqual(t, "pod a data cleared", h.get(peersPath+"/p1/"+a.address()+"/locker/get/ka,x").Body, "{}\n")
    assertEqual(t, "pod b data retained", h.get(lockersPath+"/default/peers/p1/"+b.address()+"/get/kb,x").Body, "vb")
    assertEqual(t, "peer data retained", h.get(lockersPath+"/default/peers/p1/get/k1").Body, "v1")
  })

  t.Run("all pods of a peer", func(t *testing.T) {
    store()
    resetCalls()
    resp := h.post(peersPath+"/p1/locker/clear", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {a.address(): true, b.address(): true}})
    assertCleared(t, a, b)
    c.assertNoCalls(t)
    assertEqual(t, "peer data cleared", h.get(peersPath+"/p1/locker/get/k1").Body, "{}\n")
    assertEqual(t, "pod b data cleared", h.get(peersPath+"/p1/"+b.address()+"/locker/get/kb,x").Body, "{}\n")
    assertEqual(t, "other peer retained", h.get(lockersPath+"/default/peers/p2/get/k2").Body, "v2")
  })

  t.Run("all peers", func(t *testing.T) {
    store()
    resetCalls()
    resp := h.post(peersPath+"/lockers/clear", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{
      "p1": {a.address(): true, b.address(): true}, "p2": {c.address(): true}})
    assertCleared(t, a, b, c)
    assertEqual(t, "p2 data cleared", h.get(peersPath+"/p2/locker/get/k2").Body, "{}\n")
  })

  t.Run("unknown peer", func(t *testing.T) {
    resetCalls()
    resp := h.post(peersPath+"/p9/locker/clear", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{})
    a.assertNoCalls(t)
    c.assertNoCalls(t)
  })

  t.Run("unknown address of a known peer", func(t *testing.T) {
    resetCalls()
    resp := h.post(peersPath+"/p1/1.2.3.4:5/locker/clear", "")
    assertStatus(t, resp, http.StatusOK)
    a.assertNoCalls(t)
    b.assertNoCalls(t)
  })
}

func TestFlushAndClearPeerEvents(t *testing.T) {
  h := newHarness(t)
  a := h.addFakePeer("p1", http.StatusOK)
  b := h.addFakePeer("p2", http.StatusOK)
  h.post(peersPath+"/p1/"+a.address()+"/events/store", eventOne)
  assertEqual(t, "stored events", len(dig(t, h.get(peersPath+"/events").asMap(t), "p1").([]interface{})), 1)

  resp := h.post(peersPath+"/events/flush", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "flush results", resp.peerResults(t), PeerResults{"p1": {a.address(): true}, "p2": {b.address(): true}})
  a.assertCalled(t, "POST", "/events/flush")
  b.assertCalled(t, "POST", "/events/flush")
  assertEqual(t, "flush keeps stored events", len(dig(t, h.get(peersPath+"/events").asMap(t), "p1").([]interface{})), 1)

  a.reset()
  b.reset()
  resp = h.post(peersPath+"/events/clear", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "clear results", resp.peerResults(t), PeerResults{"p1": {a.address(): true}, "p2": {b.address(): true}})
  a.assertCalled(t, "POST", "/events/clear")
  b.assertCalled(t, "POST", "/events/clear")
  // Stored events are cleared in the registry too.
  assertEqual(t, "events after clear", h.get(peersPath+"/events").Body, "{}\n")
}

// ---------------------------------------------------------------------------------------------
// Targets
// ---------------------------------------------------------------------------------------------

// twoPeers registers two single-pod peers (p1, p2) backed by live fake peers.
func twoPeers(t *testing.T) (*harness, *fakePeer, *fakePeer) {
  t.Helper()
  h := newHarness(t)
  return h, h.addFakePeer("p1", http.StatusOK), h.addFakePeer("p2", http.StatusOK)
}

func TestAddPeerTarget(t *testing.T) {
  t.Run("to a single peer", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    resp := h.post(peersPath+"/p1/targets/add", targetJSON)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}})
    call := p1.assertCalled(t, "POST", "/client/targets/add")
    assertContains(t, call.Body, `"name":"t1"`, `"url":"http://target.test/foo"`)
    p2.assertNoCalls(t)

    stored := h.get(peersPath + "/p1/targets")
    assertStatus(t, stored, http.StatusOK)
    assertEqual(t, "stored url", dig(t, stored.asMap(t), "t1", "url"), "http://target.test/foo")
    // defaults are applied when the spec is validated
    assertEqual(t, "protocol default", dig(t, stored.asMap(t), "t1", "protocol"), "HTTP/1.1")
    assertEqual(t, "replicas default", dig(t, stored.asMap(t), "t1", "replicas"), float64(1))
    assertEqual(t, "requestCount default", dig(t, stored.asMap(t), "t1", "requestCount"), float64(1))
    // The other peer is registered, so it has an (empty) collection rather than being "not found".
    other := h.get(peersPath + "/p2/targets")
    assertStatus(t, other, http.StatusOK)
    assertEqual(t, "other peer targets", other.asMap(t), map[string]interface{}{})
  })

  t.Run("to all peers", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    resp := h.post(peersPath+"/targets/add", `{"name":"t2","method":"POST","url":"http://x.test","body":"b"}`)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}, "p2": {p2.address(): true}})
    p1.assertCalled(t, "POST", "/client/targets/add")
    p2.assertCalled(t, "POST", "/client/targets/add")
    all := h.get(peersPath + "/targets").asMap(t)
    assertEqual(t, "p1 has t2", hasKey(all, "p1", "t2"), true)
    assertEqual(t, "p2 has t2", hasKey(all, "p2", "t2"), true)
  })

  t.Run("replaces a target with the same name", func(t *testing.T) {
    h, _, _ := twoPeers(t)
    h.post(peersPath+"/p1/targets/add", targetJSON)
    h.post(peersPath+"/p1/targets/add", `{"name":"t1","method":"POST","url":"http://other.test"}`)
    stored := h.get(peersPath + "/p1/targets").asMap(t)
    assertEqual(t, "number of targets", len(stored), 1)
    assertEqual(t, "url", dig(t, stored, "t1", "url"), "http://other.test")
  })

  t.Run("invalid payloads are rejected", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    for _, tc := range []struct{ name, body, want string }{
      {"malformed json", "{nope", "failed to parse json"},
      {"missing name", `{"method":"GET","url":"http://x"}`, "Invalid target spec: name is required"},
      {"missing url", `{"name":"t","method":"GET"}`, "Invalid target spec: url is required"},
      {"ab without burls", `{"name":"t","method":"GET","url":"http://x","ab":true}`, "at least one B-URL is required"},
      {"fallback without burls", `{"name":"t","method":"GET","url":"http://x","fallback":true}`, "at least one B-URL is required"},
      {"random without burls", `{"name":"t","method":"GET","url":"http://x","random":true}`, "at least one B-URL is required"},
      {"negative replicas", `{"name":"t","method":"GET","url":"http://x","replicas":-1}`, "invalid replicas"},
    } {
      resp := h.post(peersPath+"/p1/targets/add", tc.body)
      if resp.Code != http.StatusBadRequest {
        t.Errorf("%s: status = %d, want 400. body: %q", tc.name, resp.Code, resp.Body)
      }
      assertContains(t, resp.Body, tc.want)
    }
    p1.assertNoCalls(t)
    assertEqual(t, "nothing stored", h.get(peersPath+"/targets").asMap(t),
      map[string]interface{}{"p1": map[string]interface{}{}, "p2": map[string]interface{}{}})
  })

  t.Run("method defaults to GET", func(t *testing.T) {
    // (v0.8.x rejected a target without a method)
    h, p1, _ := twoPeers(t)
    resp := h.post(peersPath+"/p1/targets/add", `{"name":"tm","url":"http://x.test"}`)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "stored method", dig(t, h.get(peersPath+"/p1/targets").asMap(t), "tm", "method"), "GET")
    p1.assertCalled(t, "POST", "/client/targets/add")
  })

  t.Run("target for a peer without pods is stored for later", func(t *testing.T) {
    h := newHarness(t)
    resp := h.post(peersPath+"/p9/targets/add", targetJSON)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "body", resp.Body, "{}\n")
    assertEqual(t, "stored", h.get(peersPath+"/p9/targets").Code, http.StatusOK)
  })

  t.Run("failing pod gives 424", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    p1.setStatus(http.StatusInternalServerError)
    resp := h.post(peersPath+"/p1/targets/add", targetJSON)
    assertStatus(t, resp, http.StatusFailedDependency)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): false}})
    // The target is still recorded in the registry.
    assertEqual(t, "stored", hasKey(h.get(peersPath+"/p1/targets").asMap(t), "t1"), true)
  })

  t.Run("one failing pod among several still succeeds", func(t *testing.T) {
    // A peer is successful as soon as one of its pods succeeded.
    h, p1, _ := twoPeers(t)
    bad := newFakePeer(t, http.StatusInternalServerError)
    h.addPeer("p1", bad.address(), "p1-pod-bad")
    resp := h.post(peersPath+"/p1/targets/add", targetJSON)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true, bad.address(): false}})
  })

  t.Run("all pods failing gives 424", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    p1.setStatus(http.StatusInternalServerError)
    bad := newFakePeer(t, http.StatusInternalServerError)
    h.addPeer("p1", bad.address(), "p1-pod-bad")
    resp := h.post(peersPath+"/p1/targets/add", targetJSON)
    assertStatus(t, resp, http.StatusFailedDependency)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): false, bad.address(): false}})
  })

  t.Run("a peer failing on all pods gives 424 even if another peer succeeded", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    p1.setStatus(http.StatusInternalServerError)
    resp := h.post(peersPath+"/targets/add", targetJSON)
    assertStatus(t, resp, http.StatusFailedDependency)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): false}, "p2": {p2.address(): true}})
  })
}

func TestGetPeerTargets(t *testing.T) {
  h, _, _ := twoPeers(t)
  h.post(peersPath+"/p1/targets/add", targetJSON)

  resp := h.get(peersPath + "/p1/targets")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "p1 targets", hasKey(resp.asMap(t), "t1"), true)

  resp = h.get(peersPath + "/p9/targets")
  assertStatus(t, resp, http.StatusNoContent)
  assertEqual(t, "unknown peer body", resp.Body, "[]\n")

  all := h.get(peersPath + "/targets").asMap(t)
  assertEqual(t, "all: p1.t1", hasKey(all, "p1", "t1"), true)
  assertEqual(t, "all: p2 present but empty", dig(t, all, "p2"), map[string]interface{}{})
}

func TestRemovePeerTargets(t *testing.T) {
  setup := func(t *testing.T) (*harness, *fakePeer, *fakePeer) {
    h, p1, p2 := twoPeers(t)
    h.post(peersPath+"/p1/targets/add", targetJSON)
    h.post(peersPath+"/p1/targets/add", `{"name":"t2","method":"GET","url":"http://t2.test"}`)
    p1.reset()
    return h, p1, p2
  }

  t.Run("specific targets", func(t *testing.T) {
    h, p1, p2 := setup(t)
    resp := h.post(peersPath+"/p1/targets/t1/remove", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}})
    p1.assertCalled(t, "POST", "/client/targets/t1/remove")
    p2.assertNoCalls(t)
    stored := h.get(peersPath + "/p1/targets").asMap(t)
    assertEqual(t, "t1 removed", hasKey(stored, "t1"), false)
    assertEqual(t, "t2 retained", hasKey(stored, "t2"), true)

    // comma separated list, PUT
    p1.reset()
    resp = h.put(peersPath+"/p1/targets/t2,t3/remove", "")
    assertStatus(t, resp, http.StatusOK)
    p1.assertCalled(t, "POST", "/client/targets/t2,t3/remove")
    assertEqual(t, "all removed", h.get(peersPath+"/p1/targets").asMap(t), map[string]interface{}{})
  })

  t.Run("remove/all", func(t *testing.T) {
    h, p1, _ := setup(t)
    resp := h.post(peersPath+"/p1/targets/remove/all", "")
    assertStatus(t, resp, http.StatusOK)
    // Removing all targets clears them on the peer (the client has no remove-all, and "/client/targets//remove"
    // used to be called, which isn't a valid route).
    p1.assertCalled(t, "POST", "/client/targets/clear")
    // The peer's entry is dropped from the registry altogether.
    assertEqual(t, "targets after remove all", h.get(peersPath+"/p1/targets").Code, http.StatusNoContent)
  })

  t.Run("failing peer keeps targets in registry", func(t *testing.T) {
    h, p1, _ := setup(t)
    p1.setStatus(http.StatusInternalServerError)
    resp := h.post(peersPath+"/p1/targets/t1/remove", "")
    assertStatus(t, resp, http.StatusFailedDependency)
    assertEqual(t, "t1 retained", hasKey(h.get(peersPath+"/p1/targets").asMap(t), "t1"), true)
  })

  t.Run("unknown peer", func(t *testing.T) {
    h, p1, p2 := setup(t)
    resp := h.post(peersPath+"/p9/targets/t1/remove", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "body", resp.Body, "{}\n")
    p1.assertNoCalls(t)
    p2.assertNoCalls(t)
  })
}

func TestClearPeerTargets(t *testing.T) {
  t.Run("one peer", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    h.post(peersPath+"/targets/add", targetJSON)
    p1.reset()
    p2.reset()
    resp := h.post(peersPath+"/p1/targets/clear", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}})
    p1.assertCalled(t, "POST", "/client/targets/clear")
    p2.assertNoCalls(t)
    assertEqual(t, "p1 targets", h.get(peersPath+"/p1/targets").Code, http.StatusNoContent)
    assertEqual(t, "p2 targets retained", hasKey(h.get(peersPath+"/p2/targets").asMap(t), "t1"), true)
  })

  t.Run("all peers", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    h.post(peersPath+"/targets/add", targetJSON)
    p1.reset()
    p2.reset()
    resp := h.post(peersPath+"/targets/clear", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}, "p2": {p2.address(): true}})
    assertEqual(t, "all targets", h.get(peersPath+"/targets").Body, "{}\n")
  })
}

// ---------------------------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------------------------

func TestAddPeerJob(t *testing.T) {
  t.Run("to a single peer", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    resp := h.post(peersPath+"/p1/jobs/add", jobJSON)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}})
    call := p1.assertCalled(t, "POST", "/jobs/add")
    assertContains(t, call.Body, `"name":"j1"`, `"cmd":"echo"`)
    p2.assertNoCalls(t)

    stored := h.get(peersPath + "/p1/jobs")
    assertStatus(t, stored, http.StatusOK)
    assertEqual(t, "stored name", dig(t, stored.asMap(t), "j1", "name"), "j1")
    // defaults applied when the job is parsed
    assertEqual(t, "count default", dig(t, stored.asMap(t), "j1", "count"), float64(1))
    assertEqual(t, "maxResults default", dig(t, stored.asMap(t), "j1", "maxResults"), float64(20))
    assertEqual(t, "keepResults default", dig(t, stored.asMap(t), "j1", "keepResults"), float64(5))
    assertEqual(t, "timeout default (10m in ns)", dig(t, stored.asMap(t), "j1", "timeout"), float64(600000000000))
  })

  t.Run("to all peers", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    resp := h.post(peersPath+"/jobs/add", `{"name":"j2","task":{"cmd":"ls"}}`)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}, "p2": {p2.address(): true}})
    p1.assertCalled(t, "POST", "/jobs/add")
    p2.assertCalled(t, "POST", "/jobs/add")
    all := h.get(peersPath + "/jobs").asMap(t)
    assertEqual(t, "p1 has j2", hasKey(all, "p1", "j2"), true)
    assertEqual(t, "p2 has j2", hasKey(all, "p2", "j2"), true)
  })

  t.Run("http task", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    resp := h.post(peersPath+"/p1/jobs/add", `{"name":"jh","task":{"name":"t","method":"GET","url":"http://x.test"}}`)
    assertStatus(t, resp, http.StatusOK)
    p1.assertCalled(t, "POST", "/jobs/add")
  })

  t.Run("invalid payloads are rejected", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    for _, tc := range []struct{ name, body string }{
      {"malformed json", "{nope"},
      {"task is neither http nor command", `{"name":"j9","task":{"foo":1}}`},
      {"command task without command", `{"name":"j9","task":{"args":["x"]}}`},
    } {
      resp := h.post(peersPath+"/p1/jobs/add", tc.body)
      if resp.Code != http.StatusBadRequest {
        t.Errorf("%s: status = %d, want 400. body: %q", tc.name, resp.Code, resp.Body)
      }
      assertContains(t, resp.Body, "failed to read job")
    }
    p1.assertNoCalls(t)
  })

  t.Run("a v0.8.x payload identifies the job by id", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    resp := h.post(peersPath+"/p1/jobs/add", `{"id":"jid","task":{"cmd":"echo"}}`)
    assertStatus(t, resp, http.StatusOK)
    call := p1.assertCalled(t, "POST", "/jobs/add")
    assertContains(t, call.Body, `"name":"jid"`)
    assertEqual(t, "stored under the id", dig(t, h.get(peersPath+"/p1/jobs").asMap(t), "jid", "name"), "jid")
  })

  t.Run("a job without a name is rejected", func(t *testing.T) {
    // It used to be accepted and stored under an empty key, where it could not be removed or run by name.
    h, p1, _ := twoPeers(t)
    for _, body := range []string{`{"task":{"cmd":"echo"}}`, `{"name":"","task":{"cmd":"echo"}}`} {
      resp := h.post(peersPath+"/p1/jobs/add", body)
      assertStatus(t, resp, http.StatusBadRequest)
      assertContains(t, resp.Body, "job name is required")
    }
    p1.assertNoCalls(t)
    assertEqual(t, "nothing stored", h.get(peersPath+"/p1/jobs").asMap(t), map[string]interface{}{})
  })

  t.Run("job for a peer without pods is stored for later", func(t *testing.T) {
    h := newHarness(t)
    resp := h.post(peersPath+"/p9/jobs/add", jobJSON)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "stored", h.get(peersPath+"/p9/jobs").Code, http.StatusOK)
  })

  t.Run("failing pod gives 424", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    p1.setStatus(http.StatusInternalServerError)
    resp := h.post(peersPath+"/p1/jobs/add", jobJSON)
    assertStatus(t, resp, http.StatusFailedDependency)
    assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): false}})
  })
}

func TestGetPeerJobs(t *testing.T) {
  h, _, _ := twoPeers(t)
  h.post(peersPath+"/p1/jobs/add", jobJSON)

  assertEqual(t, "p1 has j1", hasKey(h.get(peersPath+"/p1/jobs").asMap(t), "j1"), true)
  resp := h.get(peersPath + "/p9/jobs")
  assertStatus(t, resp, http.StatusNoContent)
  assertEqual(t, "unknown peer body", resp.Body, "[]\n")
  all := h.get(peersPath + "/jobs").asMap(t)
  assertEqual(t, "all: p1.j1", hasKey(all, "p1", "j1"), true)
  assertEqual(t, "all: p2 present but empty", dig(t, all, "p2"), map[string]interface{}{})
}

func TestRemovePeerJobs(t *testing.T) {
  h, p1, p2 := twoPeers(t)
  h.post(peersPath+"/p1/jobs/add", jobJSON)
  h.post(peersPath+"/p1/jobs/add", `{"name":"j2","task":{"cmd":"ls"}}`)
  p1.reset()

  resp := h.post(peersPath+"/p1/jobs/j1/remove", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}})
  p1.assertCalled(t, "POST", "/jobs/j1/remove")
  p2.assertNoCalls(t)
  stored := h.get(peersPath + "/p1/jobs").asMap(t)
  assertEqual(t, "j1 removed", hasKey(stored, "j1"), false)
  assertEqual(t, "j2 retained", hasKey(stored, "j2"), true)

  // A failing peer keeps the job in the registry.
  p1.setStatus(http.StatusInternalServerError)
  resp = h.post(peersPath+"/p1/jobs/j2/remove", "")
  assertStatus(t, resp, http.StatusFailedDependency)
  assertEqual(t, "j2 retained after failure", hasKey(h.get(peersPath+"/p1/jobs").asMap(t), "j2"), true)
}

func TestClearPeerJobs(t *testing.T) {
  h, p1, p2 := twoPeers(t)
  h.post(peersPath+"/jobs/add", jobJSON)
  p1.reset()
  p2.reset()

  resp := h.post(peersPath+"/p1/jobs/clear", "")
  assertStatus(t, resp, http.StatusOK)
  p1.assertCalled(t, "POST", "/jobs/clear")
  p2.assertNoCalls(t)
  assertEqual(t, "p1 jobs", h.get(peersPath+"/p1/jobs").Code, http.StatusNoContent)
  assertEqual(t, "p2 jobs retained", hasKey(h.get(peersPath+"/p2/jobs").asMap(t), "j1"), true)

  resp = h.post(peersPath+"/jobs/clear", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): true}, "p2": {p2.address(): true}})
  assertEqual(t, "all jobs", h.get(peersPath+"/jobs").Body, "{}\n")
}

// ---------------------------------------------------------------------------------------------
// Operations that fan out to peers: invoke/stop/run/clear/results collection/tracking/probes
// ---------------------------------------------------------------------------------------------

type fanOutCase struct {
  name    string
  method  string
  target  string
  peers   []string // peers expected to be called, others must not be
  uri     string   // path expected on each called peer
  query   string   // raw query expected on each called peer
}

func fanOutCases() []fanOutCase {
  P := peersPath
  all := []string{"p1", "p2"}
  one := []string{"p1"}
  return []fanOutCase{
    {"invoke targets", "POST", P + "/p1/targets/t1,t2/invoke", one, "/client/targets/t1,t2/invoke", ""},
    {"invoke all targets of a peer (PUT)", "PUT", P + "/p1/targets/invoke/all", one, "/client/targets/invoke/all", ""},
    {"invoke all targets of all peers", "POST", P + "/targets/invoke/all", all, "/client/targets/invoke/all", ""},
    {"stop targets", "POST", P + "/p1/targets/t1/stop", one, "/client/targets/t1/stop", ""},
    {"stop all targets of a peer (PUT)", "PUT", P + "/p1/targets/stop/all", one, "/client/targets/stop/all", ""},
    {"stop all targets of all peers", "POST", P + "/targets/stop/all", all, "/client/targets/stop/all", ""},
    {"enable all client results", "POST", P + "/client/results/all/y", all, "/client/results/all/y", ""},
    {"disable invocation results (PUT)", "PUT", P + "/client/results/invocations/n", all, "/client/results/invocations/n", ""},
    {"enable all client results (v0.8.x path)", "POST", P + "/targets/results/all/y", all, "/client/results/all/y", ""},
    {"disable invocation results (v0.8.x path, PUT)", "PUT", P + "/targets/results/invocations/n", all, "/client/results/invocations/n", ""},
    {"run jobs", "POST", P + "/p1/jobs/j1,j2/run", one, "/jobs/j1,j2/run", ""},
    {"run all jobs of a peer", "POST", P + "/p1/jobs/run/all", one, "/jobs/run/all", ""},
    {"stop jobs", "POST", P + "/p1/jobs/j1/stop", one, "/jobs/j1/stop", ""},
    {"stop all jobs of a peer", "POST", P + "/p1/jobs/stop/all", one, "/jobs/stop/all", ""},
    {"track headers", "POST", P + "/track/headers/h1,h2|h3", all, "/client/track/headers/h1,h2|h3", ""},
    {"track headers (PUT)", "PUT", P + "/track/headers/h1", all, "/client/track/headers/h1", ""},
    {"set readiness probe uri", "POST", P + "/probes/readiness/set?uri=/ready", all, "/probes/readiness/set", "uri=/ready"},
    {"set liveness probe uri (PUT)", "PUT", P + "/probes/liveness/set?uri=/live", all, "/probes/liveness/set", "uri=/live"},
    {"set liveness probe status", "PUT", P + "/probes/liveness/set/status=503", all, "/probes/liveness/set/status=503", ""},
    {"set readiness probe status", "POST", P + "/probes/readiness/set/status=200", all, "/probes/readiness/set/status=200", ""},
  }
}

func TestFanOutOperations_CallPeersAndReportResults(t *testing.T) {
  for _, tc := range fanOutCases() {
    tc := tc
    t.Run(tc.name, func(t *testing.T) {
      h, p1, p2 := twoPeers(t)
      peers := map[string]*fakePeer{"p1": p1, "p2": p2}
      p1.reset()
      p2.reset()

      resp := h.do(tc.method, tc.target, "")
      assertStatus(t, resp, http.StatusOK)

      expected := PeerResults{}
      for _, name := range tc.peers {
        expected[name] = PodResults{peers[name].address(): true}
        call := peers[name].assertCalled(t, "POST", tc.uri)
        assertEqual(t, "query on "+name, call.Query, tc.query)
        assertEqual(t, "number of calls to "+name, len(peers[name].recorded()), 1)
      }
      for name, p := range peers {
        if _, called := expected[name]; !called {
          p.assertNoCalls(t)
        }
      }
      assertEqual(t, "results", resp.peerResults(t), expected)
    })
  }
}

func TestFanOutOperations_FailingPodGives424(t *testing.T) {
  for _, tc := range fanOutCases() {
    tc := tc
    t.Run(tc.name, func(t *testing.T) {
      h, p1, _ := twoPeers(t)
      p1.setStatus(http.StatusInternalServerError)
      resp := h.do(tc.method, tc.target, "")
      assertStatus(t, resp, http.StatusFailedDependency)
      results := resp.peerResults(t)
      assertEqual(t, "p1 result", results["p1"], map[string]bool{p1.address(): false})
    })
  }
}

func TestFanOutOperations_SkipUnhealthyPods(t *testing.T) {
  h, p1, p2 := twoPeers(t)
  p1.setStatus(http.StatusInternalServerError)
  h.get(peersPath + "/health") // marks p1's pod unhealthy
  assertEqual(t, "p1 healthy", h.podsOf("p1")[p1.address()].Healthy, false)
  p1.setStatus(http.StatusOK)
  p1.reset()
  p2.reset()

  resp := h.post(peersPath+"/targets/invoke/all", "")
  assertStatus(t, resp, http.StatusFailedDependency)
  assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {p1.address(): false}, "p2": {p2.address(): true}})
  p1.assertNoCalls(t)
  p2.assertCalled(t, "POST", "/client/targets/invoke/all")
}

func TestFanOutOperations_ClearTargetsAndJobsOnPeers(t *testing.T) {
  // Covered by fan-out specific tests above for registry state; this verifies the peer URIs.
  h, p1, p2 := twoPeers(t)
  h.post(peersPath+"/targets/add", targetJSON)
  h.post(peersPath+"/jobs/add", jobJSON)
  for _, p := range []*fakePeer{p1, p2} {
    p.reset()
  }
  assertStatus(t, h.post(peersPath+"/targets/clear", ""), http.StatusOK)
  assertStatus(t, h.post(peersPath+"/jobs/clear", ""), http.StatusOK)
  for _, p := range []*fakePeer{p1, p2} {
    assertEqual(t, "peer calls", p.summary(), []string{"POST /client/targets/clear", "POST /jobs/clear"})
  }
}

// ---------------------------------------------------------------------------------------------
// Tracking headers and probes
// ---------------------------------------------------------------------------------------------

func TestPeersTrackingHeaders(t *testing.T) {
  h, _, _ := twoPeers(t)
  resp := h.get(peersPath + "/track/headers")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "initially empty", resp.Body, "\"\"\n")

  assertStatus(t, h.post(peersPath+"/track/headers/h1,h2|h3", ""), http.StatusOK)
  assertEqual(t, "headers", h.get(peersPath+"/track/headers").Body, "\"h1,h2|h3\"\n")

  // A later call replaces the headers.
  assertStatus(t, h.put(peersPath+"/track/headers/x1", ""), http.StatusOK)
  assertEqual(t, "replaced headers", h.get(peersPath+"/track/headers").Body, "\"x1\"\n")
}

func TestPeersProbes(t *testing.T) {
  type probes struct {
    ReadinessProbe  string
    LivenessProbe   string
    ReadinessStatus int
    LivenessStatus  int
  }
  getProbes := func(h *harness) probes {
    resp := h.get(peersPath + "/probes")
    assertStatus(t, resp, http.StatusOK)
    p := probes{}
    resp.decode(t, &p)
    return p
  }

  t.Run("not configured", func(t *testing.T) {
    h := newHarness(t)
    resp := h.get(peersPath + "/probes")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "body", resp.Body, "null\n")
  })

  t.Run("set uri and status", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    assertStatus(t, h.post(peersPath+"/probes/readiness/set?uri=/ready", ""), http.StatusOK)
    // Setting a URI defaults the status to 200 when none was configured.
    assertEqual(t, "after readiness uri", getProbes(h), probes{ReadinessProbe: "/ready", ReadinessStatus: 200})

    assertStatus(t, h.put(peersPath+"/probes/liveness/set/status=503", ""), http.StatusOK)
    assertEqual(t, "after liveness status", getProbes(h), probes{ReadinessProbe: "/ready", ReadinessStatus: 200, LivenessStatus: 503})

    assertStatus(t, h.post(peersPath+"/probes/liveness/set?uri=/live", ""), http.StatusOK)
    assertEqual(t, "after liveness uri", getProbes(h), probes{ReadinessProbe: "/ready", ReadinessStatus: 200, LivenessProbe: "/live", LivenessStatus: 503})
    p1.assertCalled(t, "POST", "/probes/liveness/set")
  })

  t.Run("probe type is case insensitive", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    assertStatus(t, h.post(peersPath+"/probes/Readiness/set?uri=/ready", ""), http.StatusOK)
    assertEqual(t, "probes", getProbes(h).ReadinessProbe, "/ready")
    p1.assertCalled(t, "POST", "/probes/Readiness/set")
  })

  t.Run("invalid status falls back to 200", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    for _, status := range []string{"abc", "0", "-5"} {
      p1.reset()
      assertStatus(t, h.post(peersPath+"/probes/readiness/set/status="+status, ""), http.StatusOK)
      p1.assertCalled(t, "POST", "/probes/readiness/set/status=200")
      assertEqual(t, "status for "+status, getProbes(h).ReadinessStatus, 200)
    }
  })

  t.Run("invalid probe type", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    for _, target := range []string{peersPath + "/probes/foo/set?uri=/x", peersPath + "/probes/foo/set/status=200"} {
      resp := h.post(target, "")
      assertStatus(t, resp, http.StatusBadRequest)
      assertContains(t, resp.Body, "Cannot add. Invalid probe type")
    }
    p1.assertNoCalls(t)
    assertEqual(t, "probes untouched", h.get(peersPath+"/probes").Body, "null\n")
  })

  t.Run("empty uri", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    resp := h.post(peersPath+"/probes/readiness/set?uri=", "")
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "Cannot add. Invalid URI")
    p1.assertNoCalls(t)
  })
}

// ---------------------------------------------------------------------------------------------
// Calling peers and listing their APIs
// ---------------------------------------------------------------------------------------------

func TestCallPeer(t *testing.T) {
  t.Run("single peer, GET", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    p1.reset()
    p2.reset()
    resp := h.get(peersPath + "/p1/call?uri=/hello")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "response", resp.asMap(t), map[string]interface{}{"p1": map[string]interface{}{p1.address(): "ok"}})
    p1.assertCalled(t, "GET", "/hello")
    p2.assertNoCalls(t)
  })

  t.Run("method, headers and body are forwarded", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    resp := h.do("POST", peersPath+"/p1/call?uri=/echo", "BODY", "X-Test", "abc")
    assertStatus(t, resp, http.StatusOK)
    call := p1.assertCalled(t, "POST", "/echo")
    assertEqual(t, "body", call.Body, "BODY")
    assertEqual(t, "header", call.Header.Get("X-Test"), "abc")

    p1.reset()
    h.do("PUT", peersPath+"/p1/call?uri=/put", "")
    p1.assertCalled(t, "PUT", "/put")
  })

  t.Run("all peers", func(t *testing.T) {
    h, p1, p2 := twoPeers(t)
    p1.reset()
    p2.reset()
    resp := h.do("PUT", peersPath+"/call?uri=/all", "")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "response", resp.asMap(t), map[string]interface{}{
      "p1": map[string]interface{}{p1.address(): "ok"}, "p2": map[string]interface{}{p2.address(): "ok"}})
    p1.assertCalled(t, "PUT", "/all")
    p2.assertCalled(t, "PUT", "/all")
  })

  t.Run("non 200 responses are still reported", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    p1.setStatus(http.StatusTeapot)
    resp := h.get(peersPath + "/p1/call?uri=/x")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "response", dig(t, resp.asMap(t), "p1", p1.address()), "ok")
  })

  t.Run("empty uri", func(t *testing.T) {
    h, p1, _ := twoPeers(t)
    p1.reset()
    resp := h.get(peersPath + "/p1/call?uri=")
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "Cannot call peer. Invalid URI")
    p1.assertNoCalls(t)
  })

  t.Run("unknown peer", func(t *testing.T) {
    h, _, _ := twoPeers(t)
    resp := h.get(peersPath + "/p9/call?uri=/x")
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "body", resp.Body, "{}\n")
  })
}

func TestGetPeersAPIs(t *testing.T) {
  h := newHarness(t)
  h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
  h.addPeer("p2", "10.0.0.2:8080", "p2-pod")

  decode := func(resp response) map[string]map[string][]string {
    assertStatus(t, resp, http.StatusOK)
    apis := map[string]map[string][]string{}
    resp.decode(t, &apis)
    return apis
  }

  apis := decode(h.get(peersPath + "/apis"))
  assertEqual(t, "peers", len(apis), 2)
  all := apis["p1"]["all"]
  assertEqual(t, "number of generic apis", len(all), 15)
  assertEqual(t, "first generic api", all[0], "http://registry.test:8080/registry/peers/p1/call?uri=/version")
  assertContains(t, strings.Join(all, " "), "/registry/peers/p1/call?uri=/client/targets", "/registry/peers/p1/call?uri=/jobs")

  pod := apis["p1"]["p1-pod"]
  assertContains(t, strings.Join(pod, " "),
    "http://10.0.0.1:8080/version", "http://10.0.0.1:8080/client/results", "http://10.0.0.1:8080/jobs",
    "http://registry.test:8080/registry/peers/p1/lockers?data=y&events=n&level=3",
    "http://registry.test:8080/registry/lockers/default/peers/p1?data=y&events=n&level=3")
  assertNotContains(t, strings.Join(pod, " "), "10.0.0.2")

  single := decode(h.get(peersPath + "/p2/apis"))
  assertEqual(t, "single peer", len(single), 1)
  assertEqual(t, "p2 pod apis present", len(single["p2"]["p2-pod"]) > 15, true)

  // An unknown peer is not reported (v0.8.x reported it with just the generic "all" entry).
  unknown := decode(h.get(peersPath + "/p9/apis"))
  assertEqual(t, "unknown peer", len(unknown), 0)
}

// ---------------------------------------------------------------------------------------------
// Clone, dump, load
// ---------------------------------------------------------------------------------------------

func TestDumpRegistry(t *testing.T) {
  h := newHarness(t)
  h.addFakePeer("p1", http.StatusOK)
  h.post(peersPath+"/p1/targets/add", targetJSON)
  h.post(peersPath+"/p1/jobs/add", jobJSON)
  h.post(peersPath+"/track/headers/h1", "")
  h.post(peersPath+"/probes/readiness/set?uri=/r", "")
  h.post(lockersPath+"/L1/store/a,b", "hello")

  resp := h.get("/registry/dump")
  assertStatus(t, resp, http.StatusOK)
  dump := resp.asMap(t)
  for _, key := range []string{"lockers", "peers", "peerTargets", "peerJobs", "peerTrackingHeaders", "peerProbes"} {
    assertEqual(t, "dump has "+key, hasKey(dump, key), true)
  }
  assertEqual(t, "peer in dump", hasKey(dump, "peers", "p1", "pods"), true)
  assertEqual(t, "target in dump", hasKey(dump, "peerTargets", "p1", "t1"), true)
  assertEqual(t, "job in dump", hasKey(dump, "peerJobs", "p1", "j1"), true)
  assertEqual(t, "headers in dump", dig(t, dump, "peerTrackingHeaders"), "h1")
  assertEqual(t, "probes in dump", dig(t, dump, "peerProbes", "ReadinessProbe"), "/r")
  // Locker data is dumped unmasked and untrimmed.
  assertEqual(t, "locker data in dump", dig(t, dump, "lockers", "L1", "dataLocker", "a", "b", "data"), "hello")
}

func TestLoadRegistryDump_RestoresDumpedState(t *testing.T) {
  h := newHarness(t)
  p := h.addFakePeer("p1", http.StatusOK)
  h.post(peersPath+"/p1/targets/add", targetJSON)
  h.post(peersPath+"/p1/jobs/add", jobJSON)
  h.post(peersPath+"/track/headers/h1", "")
  h.post(peersPath+"/probes/readiness/set?uri=/r", "")
  h.post(lockersPath+"/L1/store/a,b", "hello")
  dump := h.get("/registry/dump")
  assertStatus(t, dump, http.StatusOK)

  assertStatus(t, h.post(peersPath+"/clear", ""), http.StatusOK)
  assertEqual(t, "peers cleared", h.get(peersPath).Body, "{}\n")

  resp := h.post("/registry/load", dump.Body)
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Registry data loaded")

  peers := h.peers()
  if peers["p1"] == nil || peers["p1"].Pods[p.address()] == nil {
    t.Fatalf("peer not restored: %v", h.get(peersPath).Body)
  }
  assertEqual(t, "target restored", hasKey(h.get(peersPath+"/targets").asMap(t), "p1", "t1"), true)
  assertEqual(t, "job restored", hasKey(h.get(peersPath+"/jobs").asMap(t), "p1", "j1"), true)
  assertEqual(t, "headers restored", h.get(peersPath+"/track/headers").Body, "\"h1\"\n")
  assertEqual(t, "probes restored", dig(t, h.get(peersPath+"/probes").asMap(t), "ReadinessProbe"), "/r")
  assertEqual(t, "pod restored", peers["p1"].Pods[p.address()].Name, "p1-pod")

  // Loaded pods have no http client, so they are not called until they register again.
  p.reset()
  resp = h.post(peersPath+"/targets/invoke/all", "")
  assertStatus(t, resp, http.StatusOK)
  assertEqual(t, "results", resp.peerResults(t), PeerResults{"p1": {}})
  p.assertNoCalls(t)
}

// Lockers survive a dump/load (and cloneFrom, which uses the same code): data, nested lockers, peer lockers, events,
// the current locker, and the labels used to look them up.
func TestLoadRegistryDump_RestoresLockers(t *testing.T) {
  h := newHarness(t)
  addr := "10.0.0.1:8080"
  h.post(lockersPath+"/L1/store/a,b", "hello")
  h.post(lockersPath+"/open/L1,sub", "") // flags both L1 and L1,sub as current. L1,sub is the one that was opened
  h.addPeer("p1", addr, "p1-pod")
  h.post(lockersPath+"/L1,sub/store/x", "nested")
  h.post(peersPath+"/p1/locker/store/k1", "v1")
  h.post(peersPath+"/p1/"+addr+"/locker/store/k2,x", "v2")
  assertStatus(t, h.post(peersPath+"/p1/"+addr+"/events/store", eventOne), http.StatusOK)
  dump := h.get("/registry/dump")
  assertStatus(t, dump, http.StatusOK)

  // Drop the lockers, and load them back.
  assertStatus(t, h.post(lockersPath+"/L1/close", ""), http.StatusOK)
  assertEqual(t, "L1 closed", h.get(lockersPath+"/L1").Code, http.StatusNotFound)
  assertEqual(t, "L1,sub closed", h.get(lockersPath+"/L1,sub").Code, http.StatusNotFound)
  resp := h.post("/registry/load", dump.Body)
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Registry data loaded")

  labels := h.get(lockersPath + "/labels").asMap(t)
  assertEqual(t, "default restored", hasKey(labels, "default"), true)
  assertEqual(t, "L1 restored", hasKey(labels, "L1"), true)
  assertEqual(t, "no empty label", hasKey(labels, ""), false)
  assertEqual(t, "L1 data", h.get(lockersPath+"/L1/get/a,b").Body, "hello")
  assertEqual(t, "nested locker data", h.get(lockersPath+"/L1,sub/get/x").Body, "nested")
  assertEqual(t, "current locker is the deepest one", dig(t, h.get(lockersPath+"/current").asMap(t), "label"), "sub")
  assertEqual(t, "peer data", h.get(lockersPath+"/L1,sub/peers/p1/get/k1").Body, "v1")
  assertEqual(t, "peer instance data", h.get(lockersPath+"/L1,sub/peers/p1/"+addr+"/get/k2,x").Body, "v2")
  assertEqual(t, "peer data via the current locker", h.get(peersPath+"/p1/locker/get/k1").Body, "v1")
  events := dig(t, h.get(lockersPath+"/L1,sub/peers/p1/events?data=y&reverse=n").asMap(t), "p1").([]interface{})
  assertEqual(t, "number of events", len(events), 1)
  assertEqual(t, "event title", events[0].(map[string]interface{})["title"], "Ev One")
  assertEqual(t, "event data", events[0].(map[string]interface{})["data"], "payload")

  // The loaded lockers are fully usable: they can be written to, and are linked to their parents.
  assertStatus(t, h.post(lockersPath+"/L1/store/c", "new"), http.StatusOK)
  assertEqual(t, "store after load", h.get(lockersPath+"/L1/get/c").Body, "new")
  assertStatus(t, h.post(peersPath+"/p1/locker/store/k3", "v3"), http.StatusOK)
  assertEqual(t, "peer store after load", h.get(peersPath+"/p1/locker/get/k3").Body, "v3")
  assertStatus(t, h.post(lockersPath+"/L1,sub/close", ""), http.StatusOK)
  assertEqual(t, "sub closed", h.get(lockersPath+"/L1,sub").Code, http.StatusNotFound)
  assertEqual(t, "parent retained", h.get(lockersPath+"/L1/get/a,b").Body, "hello")
  assertEqual(t, "default is current again", dig(t, h.get(lockersPath+"/current").asMap(t), "label"), "default")
}

func TestLoadRegistryDump_LockersWithoutDefaultOrCurrent(t *testing.T) {
  h := newHarness(t)
  resp := h.post("/registry/load", `{"lockers":{"X":{"label":"X","current":false}}}`)
  assertStatus(t, resp, http.StatusOK)
  labels := h.get(lockersPath + "/labels").asMap(t)
  assertEqual(t, "X loaded", hasKey(labels, "X"), true)
  assertEqual(t, "default added", hasKey(labels, "default"), true)
  assertEqual(t, "default is current", dig(t, h.get(lockersPath+"/current").asMap(t), "label"), "default")
  assertStatus(t, h.post(lockersPath+"/X/store/k", "v"), http.StatusOK)
  assertStatus(t, h.post(lockersPath+"/default/store/k", "d"), http.StatusOK)
  assertEqual(t, "X data", h.get(lockersPath+"/X/get/k").Body, "v")
}

func TestLoadRegistryDump_PartialAndInvalidInput(t *testing.T) {
  t.Run("empty object changes nothing", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
    resp := h.post("/registry/load", "{}")
    assertStatus(t, resp, http.StatusOK)
    assertContains(t, resp.Body, "Registry data loaded")
    assertEqual(t, "peer retained", len(h.podsOf("p1")), 1)
  })

  t.Run("only the sections present are loaded", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("p1", "10.0.0.1:8080", "p1-pod")
    resp := h.post("/registry/load", `{"peerTrackingHeaders":"zz"}`)
    assertStatus(t, resp, http.StatusOK)
    assertEqual(t, "headers", h.get(peersPath+"/track/headers").Body, "\"zz\"\n")
    assertEqual(t, "peer retained", len(h.podsOf("p1")), 1)
  })

  t.Run("malformed json", func(t *testing.T) {
    h := newHarness(t)
    resp := h.post("/registry/load", "{nope")
    // (the status used to stay 200, since the error was written before the status)
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "failed to load data with error")
  })

  t.Run("invalid sections are rejected and leave the registry as it was", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("keep", "10.0.0.9:8080", "keep-pod")
    resp := h.post("/registry/load", `{"lockers":"notamap"}`)
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "Registry data not loaded", "failed to load lockers with error")

    resp = h.post("/registry/load", `{"peers":"notamap","peerTargets":"x","peerJobs":"x","peerProbes":"x"}`)
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body,
      "failed to load peers with error", "failed to load peerTargets with error",
      "failed to load peerJobs with error", "failed to load peerProbes with error")
    // A failed load used to reset the existing collection before parsing, wiping what was there.
    assertEqual(t, "peer retained", len(h.podsOf("keep")), 1)
  })

  t.Run("a bad section stops the good ones from being loaded", func(t *testing.T) {
    h := newHarness(t)
    h.addPeer("keep", "10.0.0.9:8080", "keep-pod")
    before := h.get(peersPath + "/track/headers").Body
    resp := h.post("/registry/load", `{"peerTrackingHeaders":"zz","peers":"bad"}`)
    assertStatus(t, resp, http.StatusBadRequest)
    assertEqual(t, "headers unchanged", h.get(peersPath+"/track/headers").Body, before)
    assertEqual(t, "peer retained", len(h.podsOf("keep")), 1)
  })

  t.Run("tracking headers of the wrong type", func(t *testing.T) {
    h := newHarness(t)
    // This used to panic on an unchecked type assertion.
    resp := h.post("/registry/load", `{"peerTrackingHeaders":5}`)
    assertStatus(t, resp, http.StatusBadRequest)
    assertContains(t, resp.Body, "failed to load peerTrackingHeaders with error")
    assertStatus(t, h.get(peersPath), http.StatusOK)
  })
}

// sourceRegistry is a canned registry that serves what cloneFromRegistry reads.
func sourceRegistry(t *testing.T) (*httptest.Server, func() []string) {
  t.Helper()
  bodies := map[string]string{
    "/registry/peers": `{"src":{"name":"src","namespace":"ns","pods":{"1.1.1.1:80":{"name":"src-pod","address":"1.1.1.1:80",` +
      `"node":"n","cluster":"c","healthy":true,"currentEpoch":{"epoch":0,"name":"src-pod","address":"1.1.1.1:80"},` +
      `"pastEpochs":[],"url":"http://1.1.1.1:80","offline":false}},"podEpochs":{}}}`,
    "/registry/lockers":             `{}`,
    "/registry/peers/targets":       `{"src":{"t9":{"name":"t9","method":"GET","url":"http://t9.test"}}}`,
    // j8 is in the v0.8.x format, which has no name in the payload
    "/registry/peers/jobs":          `{"src":{"j9":{"name":"j9","task":{"cmd":"echo"}},"j8":{"id":"j8","task":{"cmd":"echo"}}}}`,
    "/registry/peers/track/headers": `"hx"`,
    "/registry/peers/probes":        `{"ReadinessProbe":"/rr","LivenessProbe":"","ReadinessStatus":200,"LivenessStatus":0}`,
  }
  var lock sync.Mutex
  var requested []string
  server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    lock.Lock()
    requested = append(requested, r.URL.RequestURI())
    lock.Unlock()
    body, found := bodies[r.URL.Path]
    if !found {
      w.WriteHeader(http.StatusNotFound)
      return
    }
    w.Header().Set("Content-Type", "application/json")
    fmt.Fprint(w, body)
  }))
  t.Cleanup(server.Close)
  return server, func() []string {
    lock.Lock()
    defer lock.Unlock()
    sorted := append([]string{}, requested...)
    sort.Strings(sorted)
    return sorted
  }
}

func TestCloneFromRegistry(t *testing.T) {
  h := newHarness(t)
  h.addPeer("old", "10.0.0.9:8080", "old-pod")
  server, requested := sourceRegistry(t)

  resp := h.post("/registry/cloneFrom?url="+server.URL, "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Cloned data from registry ["+server.URL+"]")
  assertEqual(t, "source requests", requested(), []string{
    "/registry/lockers?data=y&events=y&peers=y&level=0", "/registry/peers", "/registry/peers/jobs", "/registry/peers/probes",
    "/registry/peers/targets", "/registry/peers/track/headers"})

  // Peers are replaced (not merged), and cloned pods are marked offline.
  peers := h.peers()
  assertEqual(t, "old peer replaced", peers["old"] == nil, true)
  if peers["src"] == nil || peers["src"].Pods["1.1.1.1:80"] == nil {
    t.Fatalf("source peer not cloned: %s", h.get(peersPath).Body)
  }
  assertEqual(t, "pod offline", peers["src"].Pods["1.1.1.1:80"].Offline, true)
  assertEqual(t, "pod name", peers["src"].Pods["1.1.1.1:80"].Name, "src-pod")
  assertEqual(t, "targets", dig(t, h.get(peersPath+"/targets").asMap(t), "src", "t9", "url"), "http://t9.test")
  assertEqual(t, "jobs", dig(t, h.get(peersPath+"/jobs").asMap(t), "src", "j9", "name"), "j9")
  assertEqual(t, "job from a v0.8.x registry is named after its key", dig(t, h.get(peersPath+"/jobs").asMap(t), "src", "j8", "name"), "j8")
  assertEqual(t, "headers", h.get(peersPath+"/track/headers").Body, "\"hx\"\n")
  assertEqual(t, "probes", dig(t, h.get(peersPath+"/probes").asMap(t), "ReadinessProbe"), "/rr")
}

func TestCloneFromRegistry_FromLiveRegistryRoundTrip(t *testing.T) {
  h := newHarness(t)
  h.addFakePeer("p1", http.StatusOK)
  h.post(peersPath+"/p1/targets/add", targetJSON)
  h.post(peersPath+"/track/headers/h1", "")
  h.post(lockersPath+"/L1/store/a,b", "hello")
  // /registry/lockers also reports the lockers of contexts, which must not be mistaken for lockers when cloning.
  assertStatus(t, h.post("/registry/context/ctx1/lockers/C1/store/k", "v"), http.StatusOK)
  // Serve this very registry over HTTP and clone from it: exercises the real JSON of every endpoint
  // that cloneFromRegistry reads.
  server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    h.router.ServeHTTP(w, prepareRequest(r))
  }))
  defer server.Close()

  resp := h.post("/registry/cloneFrom?url="+server.URL, "")
  assertStatus(t, resp, http.StatusOK)
  assertContains(t, resp.Body, "Cloned data from registry")
  for _, pod := range h.podsOf("p1") {
    assertEqual(t, "cloned pod offline", pod.Offline, true)
  }
  assertEqual(t, "target kept", hasKey(h.get(peersPath+"/targets").asMap(t), "p1", "t1"), true)
  assertEqual(t, "headers kept", h.get(peersPath+"/track/headers").Body, "\"h1\"\n")
  // Probes aren't set, so the source reports null, which has to be a valid response to clone.
  assertEqual(t, "probes", dig(t, h.get(peersPath+"/probes").asMap(t), "ReadinessProbe"), "")
  // Lockers are cloned too, along with their data.
  assertEqual(t, "locker data", h.get(lockersPath+"/L1/get/a,b").Body, "hello")
  labels := h.get(lockersPath + "/labels").asMap(t)
  assertEqual(t, "default locker", hasKey(labels, "default"), true)
  assertEqual(t, "context is not a locker", hasKey(labels, "ctx1"), false)
  assertEqual(t, "number of lockers", len(labels), 2)
}

func TestCloneFromRegistry_InvalidAndUnreachable(t *testing.T) {
  h := newHarness(t)

  resp := h.post("/registry/cloneFrom?url=", "")
  assertStatus(t, resp, http.StatusBadRequest)
  assertContains(t, resp.Body, "Cannot clone. Invalid URI")

  dead := newFakePeer(t, http.StatusOK)
  url := dead.server.URL
  dead.kill()
  resp = h.post("/registry/cloneFrom?url="+url, "")
  assertStatus(t, resp, http.StatusInternalServerError)
  // (the failure message used to be overwritten, so the body claimed success even though the status was 500)
  assertContains(t, resp.Body, "failed to clone peers data from registry ["+url+"]")
  assertNotContains(t, resp.Body, "Cloned data from registry")
}
