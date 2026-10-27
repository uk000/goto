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

package ctl

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/types"
)

// request is a request that the remote goto got.
type request struct {
	Method      string
	Path        string
	RawQuery    string
	ContentType string
	Body        string
}

// remote is a goto that the commands talk to, which keeps what it is sent, and answers with the status that it is given.
type remote struct {
	*httptest.Server
	mu       sync.Mutex
	requests []request
	status   int
	answer   string
}

func newRemote(t *testing.T) *remote {
	t.Helper()
	r := &remote{status: http.StatusOK, answer: "ok"}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, request{req.Method, req.URL.Path, req.URL.RawQuery, req.Header.Get("Content-Type"), string(body)})
		status, answer := r.status, r.answer
		r.mu.Unlock()
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(r.Server.Close)
	return r
}

// got returns what the remote was sent, in order.
func (r *remote) got() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request{}, r.requests...)
}

// setStatus makes the remote answer with the status from now on.
func (r *remote) setStatus(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

// paths returns the method and the path of every request that the remote got.
func (r *remote) paths() []string {
	var paths []string
	for _, req := range r.got() {
		paths = append(paths, req.Method+" "+req.Path)
	}
	return paths
}

// find returns the requests of a path.
func (r *remote) find(path string) []request {
	var found []request
	for _, req := range r.got() {
		if req.Path == path {
			found = append(found, req)
		}
	}
	return found
}

// newFlagSets makes the flag sets of the commands the way cmd does, except that a mistake in the arguments is an error
// that is returned, and not an exit.
func newFlagSets() (apply, ctx *flag.FlagSet) {
	apply = flag.NewFlagSet("apply", flag.ContinueOnError)
	apply.SetOutput(io.Discard)
	apply.StringVar(&global.CtlConfig.ConfigFile, "file", "", "")
	apply.StringVar(&global.CtlConfig.ConfigFile, "f", "", "")
	ctx = flag.NewFlagSet("ctx", flag.ContinueOnError)
	ctx.SetOutput(io.Discard)
	ctx.StringVar(&global.CtlConfig.Name, "name", "default", "")
	ctx.StringVar(&global.CtlConfig.Name, "n", "default", "")
	ctx.StringVar(&global.CtlConfig.RemoteURL, "remote", "http://localhost:8080", "")
	ctx.StringVar(&global.CtlConfig.RemoteURL, "r", "http://localhost:8080", "")
	return
}

// isolate gives the test a ctl that has none of what other tests, or the home of the user, have: no contexts, a context
// file in a directory of the test, and flag sets that don't exit. It returns the directory, and puts everything back
// when the test is done.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldCtxFile, oldContexts, oldCurrent := ctxFile, contexts, currentContext
	oldDefaultFile, oldDefaultPath := DefaultCtxFile, DefaultCtlContextPath
	oldConfig := global.CtlConfig
	oldApply, oldCtx := ApplyFlagSet, CtxFlagSet
	DefaultCtlContextPath = filepath.Join(dir, "default") + "/"
	DefaultCtxFile = DefaultCtlContextPath + "gotoContext.yaml"
	ctxFile, contexts, currentContext = "", nil, nil
	global.CtlConfig = types.CmdCtlConfig{Context: "default", Name: "default", RemoteURL: "http://localhost:8080"}
	t.Cleanup(func() {
		ctxFile, contexts, currentContext = oldCtxFile, oldContexts, oldCurrent
		DefaultCtxFile, DefaultCtlContextPath = oldDefaultFile, oldDefaultPath
		global.CtlConfig = oldConfig
		ApplyFlagSet, CtxFlagSet = oldApply, oldCtx
	})
	ApplyFlagSet, CtxFlagSet = newFlagSets()
	return dir
}

// useRemote makes the commands talk to the URL.
func useRemote(t *testing.T, url string) {
	t.Helper()
	old := currentContext
	currentContext = &types.Context{Name: "test", RemoteGotoURL: url}
	t.Cleanup(func() { currentContext = old })
}

// within runs fn, and fails the test when it panics or when it has not returned by the deadline. The commands call a
// server without a timeout of their own.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s panicked: %v", what, p)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not return in time", what)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseConfigReadsEveryPartOfAConfig(t *testing.T) {
	config := ParseConfig("some/path.yaml", []byte(`
name: my-config
order: 3
listeners:
  8443:
    label: l-8443
    protocol: https
    tls: true
  9000:
    protocol: tcp
tls:
  spiffeSocket: unix:///tmp/spire.sock
  caCerts:
  - name: ca1
    domain: goto.goto
    key: ca-key
    cert: ca-cert
  certs:
  - port: 8443
    domain: goto.goto
    san: [a.goto, b.goto]
    spiffeID: spiffe://goto/a
  - name: client
    autoCert: true
    domain: goto.client
scripts:
  config:
  - name: s1
    content: echo hi
  run:
  - name: s1
    args: [a, b]
grpc:
  protos:
  - name: p1
    path: /protos/p1.proto
    register: true
  services:
  - service: Svc
    port: 1234
    serve: true
    methods:
    - method: Call
registry:
  locker: lock-1
traffic:
  config:
    tracking:
      headers: [x-a]
      time:
        buckets: [1s, 2s]
    targets:
    - name: t1
      method: GET
      url: http://x
      replicas: 2
      requestCount: 5
      expectation:
        statusCode: 200
        headers:
          h: v
  invoke: [t1]
jobs:
  config:
  - id: j1
    task:
      cmd: echo
      args: [hi]
    count: 2
  run: [j1]
http:
  servers:
  - port: 8080
    response:
      payloads:
      - uri: /x
proxy:
- http:
    port: 7000
    targets:
      t1:
        enabled: true
`))
	if config == nil {
		t.Fatal("the config was not parsed")
	}
	if config.Filepath != "some/path.yaml" || config.Name != "my-config" || config.Order != 3 {
		t.Errorf("config = %q %q %d", config.Filepath, config.Name, config.Order)
	}
	if len(config.Listeners) != 2 || config.Listeners[8443] == nil || config.Listeners[8443].Label != "l-8443" || config.Listeners[9000] == nil {
		t.Errorf("listeners = %v", config.Listeners)
	}
	tls := config.TLS
	if tls == nil || tls.SpiffeSocket != "unix:///tmp/spire.sock" || len(tls.CACerts) != 1 || len(tls.Certs) != 2 {
		t.Fatalf("tls = %+v", tls)
	}
	if ca := tls.CACerts[0]; ca.Name != "ca1" || ca.Domain != "goto.goto" || ca.Key != "ca-key" || ca.Cert != "ca-cert" {
		t.Errorf("ca cert = %+v", ca)
	}
	if c := tls.Certs[0]; c.Port != 8443 || c.Domain != "goto.goto" || len(c.SAN) != 2 || c.SpiffeID != "spiffe://goto/a" {
		t.Errorf("cert 0 = %+v", c)
	}
	if c := tls.Certs[1]; c.Name != "client" || !c.AutoCert || c.Domain != "goto.client" {
		t.Errorf("cert 1 = %+v", c)
	}
	if s := config.Scripts; s == nil || len(s.Config) != 1 || s.Config[0].Content != "echo hi" || len(s.Run) != 1 || len(s.Run[0].Args) != 2 {
		t.Errorf("scripts = %+v", s)
	}
	if g := config.GRPC; g == nil || len(g.Protos) != 1 || !g.Protos[0].Register || len(g.Services) != 1 || g.Services[0].Port != 1234 || len(g.Services[0].Methods) != 1 {
		t.Errorf("grpc = %+v", g)
	}
	if config.Registry == nil || config.Registry.Locker != "lock-1" {
		t.Errorf("registry = %+v", config.Registry)
	}
	tr := config.Traffic
	if tr == nil || tr.Config == nil || len(tr.Config.Targets) != 1 || tr.Config.Targets[0].Replicas != 2 || tr.Config.Targets[0].Expectation.Headers["h"] != "v" ||
		len(tr.Config.Tracking.Time.Buckets) != 2 || len(tr.Invoke) != 1 {
		t.Errorf("traffic = %+v", tr)
	}
	if j := config.Jobs; j == nil || len(j.Config) != 1 || j.Config[0].Task.Cmd != "echo" || j.Config[0].Count != 2 || len(j.Run) != 1 {
		t.Errorf("jobs = %+v", j)
	}
	if h := config.HTTP; h == nil || len(h.Servers) != 1 || h.Servers[0].Port != 8080 || h.Servers[0].Response == nil || len(h.Servers[0].Response.Payloads) != 1 {
		t.Errorf("http = %+v", h)
	}
	if len(config.Proxies) != 1 || config.Proxies[0].HTTP == nil || config.Proxies[0].HTTP.Port != 7000 || config.Proxies[0].HTTP.Targets["t1"] == nil {
		t.Errorf("proxies = %+v", config.Proxies)
	}
}

func TestParseConfigOfAnEmptyDocument(t *testing.T) {
	config := ParseConfig("empty.yaml", []byte(""))
	if config == nil || config.Filepath != "empty.yaml" || config.Name != "" || config.TLS != nil || len(config.Listeners) != 0 {
		t.Errorf("config = %+v", config)
	}
}

func TestParseConfigThatIsNotValidIsNil(t *testing.T) {
	for name, data := range map[string]string{
		"not yaml":         "key: [unclosed",
		"tab indentation":  "name: x\n\tbad: y\n",
		"wrong type":       "order: not-a-number\n",
		"listener of text": "listeners:\n  notaport:\n    protocol: tcp\n",
		"list for a map":   "scripts: [1, 2]\n",
	} {
		if config := ParseConfig("bad.yaml", []byte(data)); config != nil {
			t.Errorf("%s: config = %+v, want none", name, config)
		}
	}
}

func TestParseConfigOfTheSampleConfig(t *testing.T) {
	data, err := os.ReadFile("../sample_goto_config.yaml")
	if err != nil {
		t.Skipf("no sample config: %v", err)
	}
	config := ParseConfig("sample", data)
	if config == nil {
		t.Skip("the sample config is not a valid config as it is")
	}
	if config.Name != "goto-test-config" || config.TLS == nil || len(config.Listeners) == 0 {
		t.Errorf("config = %+v", config)
	}
}

func TestReadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.yaml", "name: from-file\norder: 7\n")
	config := ReadConfigFromFile(path)
	if config == nil || config.Name != "from-file" || config.Order != 7 || config.Filepath != path {
		t.Errorf("config = %+v", config)
	}
	if got := ReadConfigFromFile(filepath.Join(dir, "missing.yaml")); got != nil {
		t.Errorf("config of a file that is not there = %+v", got)
	}
	if got := ReadConfigFromFile(dir); got != nil {
		t.Errorf("config of a directory = %+v", got)
	}
	bad := writeFile(t, dir, "bad.yaml", "key: [unclosed")
	if got := ReadConfigFromFile(bad); got != nil {
		t.Errorf("config of a file that is not valid = %+v", got)
	}
}

func TestCtlIgnoresAnUnknownCommand(t *testing.T) {
	dir := isolate(t)
	global.CtlConfig.ContextFile = filepath.Join(dir, "ctx", "ctx.yaml")
	within(t, "Ctl", func() { Ctl([]string{"nothing", "-x"}) })
}

func TestCtlApplyOfAConfigThatIsNotThereDoesNothing(t *testing.T) {
	dir := isolate(t)
	remote := newRemote(t)
	global.CtlConfig.ContextFile = filepath.Join(dir, "ctx", "ctx.yaml")
	global.CtlConfig.RemoteURL = remote.URL
	within(t, "Ctl apply", func() { Ctl([]string{"apply", "-f", filepath.Join(dir, "missing.yaml")}) })
	if got := remote.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestCtlApplySendsTheConfigToTheRemote(t *testing.T) {
	dir := isolate(t)
	remote := newRemote(t)
	global.CtlConfig.ContextFile = filepath.Join(dir, "ctx", "ctx.yaml")
	global.CtlConfig.RemoteURL = remote.URL
	config := writeFile(t, dir, "config.yaml", `
scripts:
  config:
  - name: applied
    content: echo applied
  run:
  - name: applied
    args: [x]
`)
	within(t, "Ctl apply", func() { Ctl([]string{"apply", "-file", config}) })
	if paths := remote.paths(); len(paths) != 2 || paths[0] != "POST /scripts/add/applied" || paths[1] != "POST /scripts/applied/run" {
		t.Fatalf("requests = %v", paths)
	}
	if got := remote.find("/scripts/add/applied")[0]; got.Body != "echo applied" || got.ContentType != "text/plain" {
		t.Errorf("script request = %+v", got)
	}
	if got := remote.find("/scripts/applied/run")[0]; got.RawQuery != "args=x" {
		t.Errorf("run request = %+v", got)
	}
	if currentContext == nil || currentContext.RemoteGotoURL != remote.URL {
		t.Errorf("current context = %+v, want the one that was made for the remote", currentContext)
	}
}

func TestCtlApplyUsesTheContextThatIsAskedFor(t *testing.T) {
	dir := isolate(t)
	a, b := newRemote(t), newRemote(t)
	ctxPath := filepath.Join(dir, "ctx", "ctx.yaml")
	global.CtlConfig.ContextFile = ctxPath
	writeFile(t, dir, "ctx/ctx.yaml", "default:\n  name: default\n  remoteGotoURL: "+a.URL+"\nother:\n  name: other\n  remoteGotoURL: "+b.URL+"\n")
	config := writeFile(t, dir, "config.yaml", "scripts:\n  config:\n  - name: s\n    content: echo\n")
	global.CtlConfig.Context = "other"
	within(t, "Ctl apply", func() { Ctl([]string{"apply", "-f", config}) })
	if len(a.got()) != 0 || len(b.got()) != 1 {
		t.Errorf("requests: default=%v other=%v, want the one for the context that was asked for", a.paths(), b.paths())
	}
	// A context that is not known is replaced by the default.
	global.CtlConfig.Context = "unknown"
	within(t, "Ctl apply", func() { Ctl([]string{"apply", "-f", config}) })
	if len(a.got()) != 1 || len(b.got()) != 1 {
		t.Errorf("requests: default=%v other=%v, want the default context for a context that is not known", a.paths(), b.paths())
	}
}

func TestCtlCtxCreatesAndUpdatesContexts(t *testing.T) {
	dir := isolate(t)
	ctxPath := filepath.Join(dir, "ctx", "ctx.yaml")
	global.CtlConfig.ContextFile = ctxPath
	within(t, "Ctl ctx", func() { Ctl([]string{"ctx", "-n", "one", "-r", "http://one:1"}) })
	if c := contexts["one"]; c == nil || c.RemoteGotoURL != "http://one:1" || c.Name != "one" {
		t.Fatalf("contexts = %v", contexts)
	}
	if contexts["default"] == nil {
		t.Errorf("the default context was not made with the file: %v", contexts)
	}
	within(t, "Ctl ctx", func() { Ctl([]string{"ctx", "-name", "one", "-remote", "http://changed:2"}) })
	if c := contexts["one"]; c == nil || c.RemoteGotoURL != "http://changed:2" {
		t.Errorf("context one = %+v, want its URL changed", c)
	}
	data, err := os.ReadFile(ctxPath)
	if err != nil || !strings.Contains(string(data), "http://changed:2") || !strings.Contains(string(data), "default") {
		t.Errorf("context file = %q, %v", data, err)
	}
}

func TestCtlApplyWithABadFlagDoesNotRun(t *testing.T) {
	// The flag set of apply reports a mistake with the exit of the process, which the flag sets of the tests do not do.
	dir := isolate(t)
	remote := newRemote(t)
	global.CtlConfig.ContextFile = filepath.Join(dir, "ctx", "ctx.yaml")
	global.CtlConfig.RemoteURL = remote.URL
	within(t, "Ctl apply", func() { Ctl([]string{"apply", "-no-such-flag"}) })
	if got := remote.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
