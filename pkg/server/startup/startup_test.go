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

package startup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/ctl"
	"goto/pkg/global"
	grpcproxy "goto/pkg/proxy/grpc"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/server/listeners"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"github.com/sgtdi/fswatcher"
)

var (
	stubOnce sync.Once
	// The configs of a file are applied to the ports that they name, and the ports are global to the process: every test
	// uses ports of its own, which are only keys, as nothing in the tests is opened.
	portSeq atomic.Int32
)

// The startup logs everything that it loads. The log is only worth reading when a test fails, and then it is there with
// GOTO_TEST_LOGS set.
func TestMain(m *testing.M) {
	if os.Getenv("GOTO_TEST_LOGS") == "" {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return true }
	global.Funcs.IsListenerOpen = func(int) bool { return true }
	global.Funcs.GetListenerID = func(port int) string { return strconv.Itoa(port) + "-1" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	global.Funcs.CloseConnectionsForPort = func(int) {}
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func newPort() int {
	return int(47000 + portSeq.Add(1))
}

// lockedBuffer is a buffer that the log can be written to by any goroutine while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog gives what the startup logs while the test runs.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	b := &lockedBuffer{}
	log.SetOutput(b)
	t.Cleanup(func() {
		if os.Getenv("GOTO_TEST_LOGS") == "" {
			log.SetOutput(io.Discard)
		} else {
			log.SetOutput(os.Stderr)
		}
	})
	return b
}

// resetState empties what the startup remembers of the configs it loaded, as it was at the start of the process, and
// puts it back that way when the test ends.
func resetState(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	// The URIs of response payloads are registered on a router that the server makes when it starts. A router of its own
	// for each test has no route of the tests before it.
	util.CreateRouters(mux.NewRouter())
	reset := func() {
		lock.Lock()
		defer lock.Unlock()
		tlsConfigs = map[string]*ctl.TLSConfigs{}
		listenerConfigs = map[string]ctl.Listeners{}
		a2aConfigs = map[string]*ctl.A2A{}
		mcpConfigs = map[string]*ctl.MCP{}
		httpProxyConfigs = map[string]map[int]*httpproxy.Proxy{}
		tcpProxyConfigs = map[string]map[int]*tcpproxy.TCPProxy{}
		grpcProxyConfigs = map[string]map[int]*grpcproxy.GRPCProxy{}
		httpConfigs = map[string]*ctl.HTTP{}
		grpcConfigs = map[string]*ctl.GRPC{}
		loaded = false
	}
	reset()
	paths := global.ServerConfig.ConfigPaths
	t.Cleanup(func() {
		Stop()
		// what the test loaded is taken out of the process with the configs that kept it
		removeAllConfigs()
		reset()
		global.ServerConfig.ConfigPaths = paths
	})
}

// writeFile writes a file of a config, and gives its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// parse gives the config of a YAML text, the way it is read from a file.
func parse(t *testing.T, name, yamlText string) *ctl.GotoConfig {
	t.Helper()
	c := ctl.ParseConfig(filepath.Join("/configs", name), []byte(yamlText))
	if c == nil {
		t.Fatalf("config %s did not parse:\n%s", name, yamlText)
	}
	return c
}

// httpYAML is a config of a file that has a response payload for an HTTP port.
func httpYAML(port int, uriPrefix, body string) string {
	return fmt.Sprintf(`
http:
  servers:
  - port: %d
    response:
      payloads:
      - payload: %q
        contentType: text/plain
        matches:
        - uriPrefix: %s
`, port, body, uriPrefix)
}

// httpPayload tells what the response payload of an HTTP port is for a URI, and if there is one.
func httpPayload(port int, uri string) (string, bool) {
	_, rp, _, found := payload.PayloadManager.GetResponsePayload(port, false, uri, http.Header{}, nil, io.NopCloser(strings.NewReader("")))
	if !found || rp == nil {
		return "", false
	}
	return string(rp.Payload), true
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// mustListener gives the listener of a port, and fails the test if there is none.
func mustListener(t *testing.T, port int) *listeners.Listener {
	t.Helper()
	l := listeners.GetListenerForPort(port)
	if l == nil {
		t.Fatalf("no listener on port %d", port)
	}
	return l
}

func removeListenersAfter(t *testing.T, ports ...int) {
	t.Helper()
	t.Cleanup(func() {
		for _, p := range ports {
			if l := listeners.GetListenerForPort(p); l != nil {
				l.Close()
				listeners.RemoveListener(l)
			}
		}
	})
}

// ---- reading configs ----

func TestChangeLogContains(t *testing.T) {
	c := ChangeLog{"/configs/a.yaml": 0, "/configs/b.yaml": 0}
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"/configs/a.yaml", true},
		{"/configs/b.yaml", true},
		{"/configs/c.yaml", false},
		{"/configs", true},            // a part of a changed path
		{"/configs/a.yaml.bak", true}, // a changed path is a part of it
		{"b.yaml", true},
		{"", true}, // the empty text is a part of every path
		{"/other/c.yaml", false},
	} {
		if got := c.Contains(tc.v); got != tc.want {
			t.Errorf("Contains(%q) = %t, want %t", tc.v, got, tc.want)
		}
	}
	if (ChangeLog{}).Contains("/configs/a.yaml") {
		t.Error("an empty change log contains nothing")
	}
}

func TestGetFileConfigReadsYAMLFiles(t *testing.T) {
	dir := t.TempDir()
	body := "name: from file\norder: 3\n"
	for _, name := range []string{"c.yaml", "c.yml"} {
		p := writeFile(t, dir, name, body)
		c := getFileConfig(p)
		if c == nil || c.Name != "from file" || c.Order != 3 || c.Filepath != p {
			t.Errorf("%s config = %+v", name, c)
		}
	}
}

func TestGetFileConfigIgnoresOtherFilesAndBadOnes(t *testing.T) {
	dir := t.TempDir()
	if c := getFileConfig(writeFile(t, dir, "notes.txt", "name: x\n")); c != nil {
		t.Errorf("a .txt file was read as a config: %+v", c)
	}
	if c := getFileConfig(writeFile(t, dir, "config.yaml.bak", "name: x\n")); c != nil {
		t.Errorf("a .bak file was read as a config: %+v", c)
	}
	if c := getFileConfig(filepath.Join(dir, "missing.yaml")); c != nil {
		t.Errorf("a file that doesn't exist gave a config: %+v", c)
	}
	if c := getFileConfig(writeFile(t, dir, "bad.yaml", "name: [unclosed\n")); c != nil {
		t.Errorf("a file that is not YAML gave a config: %+v", c)
	}
	if c := getFileConfig(writeFile(t, dir, "wrongtype.yaml", "order: not-a-number\n")); c != nil {
		t.Errorf("a file with a value of the wrong type gave a config: %+v", c)
	}
}

func TestReadConfigFromScript(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "gen.sh", "#!/bin/sh\necho 'name: from script'\necho 'order: 7'\n")
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}
	c := getFileConfig(script)
	if c == nil || c.Name != "from script" || c.Order != 7 || c.Filepath != script {
		t.Errorf("config of the script = %+v", c)
	}
	if c := readConfigFromScript(script); c == nil || c.Name != "from script" {
		t.Errorf("readConfigFromScript = %+v", c)
	}
}

func TestReadConfigFromScriptThatFails(t *testing.T) {
	dir := t.TempDir()
	script := writeFile(t, dir, "bad.sh", "#!/bin/sh\necho 'name: [unclosed'\nexit 3\n")
	os.Chmod(script, 0o700)
	if c := getFileConfig(script); c != nil {
		t.Errorf("a script that prints no YAML gave a config: %+v", c)
	}
	// a script that cannot be run
	if c := getFileConfig(filepath.Join(dir, "missing.sh")); c != nil {
		t.Errorf("a script that is missing gave a config: %+v", c)
	}
}

// ---- loading from paths ----

// loadedOrder tells in which order the configs of the log were loaded.
func loadedOrder(logged string) []string {
	var order []string
	for _, line := range strings.Split(logged, "\n") {
		if i := strings.Index(line, "Loading Config: "); i >= 0 && !strings.Contains(line, "Finished") {
			rest := line[i+len("Loading Config: "):]
			rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "==================="))
			order = append(order, filepath.Base(rest))
		}
	}
	return order
}

func TestLoadConfigsFromPathsLoadsInOrder(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "name: a\norder: 3\n")
	writeFile(t, dir, "b.yml", "name: b\norder: 1\n")
	writeFile(t, dir, "c.yaml", "name: c\norder: 2\n")
	writeFile(t, dir, "d.txt", "name: d\norder: 0\n")
	global.ServerConfig.ConfigPaths = []string{dir}
	logged := captureLog(t)

	loadConfigsFromPaths(nil)
	if got := loadedOrder(logged.String()); strings.Join(got, ",") != "b.yml,c.yaml,a.yaml" {
		t.Errorf("configs were loaded in the order %v, want b.yml,c.yaml,a.yaml (by their order, text files left out)", got)
	}
}

func TestLoadConfigsFromPathsLoadsAllPaths(t *testing.T) {
	resetState(t)
	d1, d2 := t.TempDir(), t.TempDir()
	p1, p2 := newPort(), newPort()
	writeFile(t, d1, "one.yaml", httpYAML(p1, "/one", "first"))
	writeFile(t, d2, "two.yaml", httpYAML(p2, "/two", "second"))
	global.ServerConfig.ConfigPaths = []string{d1, filepath.Join(t.TempDir(), "missing"), d2}
	loadConfigsFromPaths(nil)
	if got := keys(httpConfigs); len(got) != 2 {
		t.Errorf("loaded configs = %v, want one.yaml and two.yaml, with the missing path skipped", got)
	}
	if body, ok := httpPayload(p1, "/one"); !ok || body != "first" {
		t.Errorf("payload of port %d = %q, %t", p1, body, ok)
	}
	if body, ok := httpPayload(p2, "/two"); !ok || body != "second" {
		t.Errorf("payload of port %d = %q, %t", p2, body, ok)
	}
}

func TestLoadConfigsFromPathsLoadsOnlyChangedFiles(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	pa, pb, pc := newPort(), newPort(), newPort()
	a := writeFile(t, dir, "a.yaml", httpYAML(pa, "/a", "a"))
	writeFile(t, dir, "b.yaml", httpYAML(pb, "/b", "b"))
	writeFile(t, dir, "c.yaml", httpYAML(pc, "/c", "c"))
	global.ServerConfig.ConfigPaths = []string{dir}

	changes := ChangeLog{a: 0}
	loadConfigsFromPaths(changes)
	if got := keys(httpConfigs); len(got) != 1 || got[0] != "a.yaml" {
		t.Errorf("loaded configs = %v, want only the changed a.yaml", got)
	}
	if len(changes) != 0 {
		t.Errorf("the change log still has %v after the change was loaded", changes)
	}
}

func TestLoadConfigsFromPathsKeepsChangesOfOtherPaths(t *testing.T) {
	resetState(t)
	d1, d2 := t.TempDir(), t.TempDir()
	p1, p2 := newPort(), newPort()
	one := writeFile(t, d1, "one.yaml", httpYAML(p1, "/one", "1"))
	writeFile(t, d1, "other.yaml", httpYAML(newPort(), "/other", "o"))
	two := writeFile(t, d2, "two.yaml", httpYAML(p2, "/two", "2"))
	writeFile(t, d2, "another.yaml", httpYAML(newPort(), "/another", "a"))
	global.ServerConfig.ConfigPaths = []string{d1, d2}

	loadConfigsFromPaths(ChangeLog{one: 0, two: 0})
	got := keys(httpConfigs)
	if len(got) != 2 || httpConfigs["one.yaml"] == nil || httpConfigs["two.yaml"] == nil {
		t.Errorf("loaded configs = %v, want the changed one.yaml and two.yaml only", got)
	}
}

func TestLoadConfigsFromPathsWithEmptyOrUnreadablePaths(t *testing.T) {
	resetState(t)
	global.ServerConfig.ConfigPaths = []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")}
	loadConfigsFromPaths(nil)
	if len(httpConfigs) != 0 || len(listenerConfigs) != 0 {
		t.Errorf("configs were loaded from nothing: %v %v", httpConfigs, listenerConfigs)
	}
	global.ServerConfig.ConfigPaths = nil
	loadConfigsFromPaths(nil)
}

// ---- applying a config ----

func TestLoadConfigNilConfigIsNothing(t *testing.T) {
	resetState(t)
	loadConfig(nil)
	if len(httpConfigs)+len(listenerConfigs)+len(tlsConfigs)+len(mcpConfigs)+len(a2aConfigs) != 0 {
		t.Error("a nil config loaded something")
	}
}

func TestLoadConfigKeepsConfigsByFileName(t *testing.T) {
	resetState(t)
	port := newPort()
	lport := newPort()
	removeListenersAfter(t, lport)
	c := parse(t, "all.yaml", httpYAML(port, "/x", "hello")+fmt.Sprintf(`
listeners:
  %d:
    protocol: tcp
    open: false
mcp:
  servers:
  - server:
      name: startup-mcp-%d
      port: %d
tls: {}
`, lport, port, port))
	loadConfig(c)
	if httpConfigs["all.yaml"] == nil || listenerConfigs["all.yaml"] == nil || mcpConfigs["all.yaml"] == nil || tlsConfigs["all.yaml"] == nil {
		t.Errorf("configs by file: http %v listeners %v mcp %v tls %v", httpConfigs, listenerConfigs, mcpConfigs, tlsConfigs)
	}
	if grpcConfigs["all.yaml"] != nil || a2aConfigs["all.yaml"] != nil {
		t.Error("configs of the sections that the file doesn't have were kept")
	}
	if body, ok := httpPayload(port, "/x"); !ok || body != "hello" {
		t.Errorf("response payload = %q, %t", body, ok)
	}
	if l := listeners.GetListenerForPort(lport); l == nil || !l.IsTCP || l.TCP == nil {
		t.Errorf("listener = %+v", l)
	}
}

func TestLoadConfigReloadReplacesTheConfigOfTheFile(t *testing.T) {
	resetState(t)
	port := newPort()
	loadConfig(parse(t, "f.yaml", httpYAML(port, "/old", "old")))
	if _, ok := httpPayload(port, "/old"); !ok {
		t.Fatal("the payload of the first load is not there")
	}
	loadConfig(parse(t, "f.yaml", httpYAML(port, "/new", "new")))
	if body, ok := httpPayload(port, "/new"); !ok || body != "new" {
		t.Errorf("payload of the reload = %q, %t", body, ok)
	}
	if _, ok := httpPayload(port, "/old"); ok {
		t.Error("the payload of the file as it was is still served after the file changed")
	}
	if len(httpConfigs) != 1 {
		t.Errorf("configs = %v, want the one of the file", keys(httpConfigs))
	}
}

func TestLoadConfigWithoutSectionsDoesNothing(t *testing.T) {
	resetState(t)
	loadConfig(parse(t, "empty.yaml", "name: nothing\n"))
	if len(httpConfigs)+len(listenerConfigs)+len(tlsConfigs)+len(mcpConfigs)+len(a2aConfigs)+len(grpcConfigs) != 0 {
		t.Error("a config without sections kept configs")
	}
}

func TestRemoveConfigsRemovesWhatTheFileLoaded(t *testing.T) {
	resetState(t)
	port, lport := newPort(), newPort()
	removeListenersAfter(t, lport)
	loadConfig(parse(t, "gone.yaml", httpYAML(port, "/gone", "x")+fmt.Sprintf(`
listeners:
  %d:
    protocol: tcp
mcp:
  servers:
  - server:
      name: gone-%d
      port: %d
proxy:
- tcp:
    port: %d
    upstreams:
      u:
        endpoints:
          e:
            address: 127.0.0.1:1
`, lport, port, port, port)))
	loadConfig(parse(t, "stays.yaml", httpYAML(newPort(), "/stays", "y")))
	if listeners.GetListenerForPort(lport) == nil || len(tcpProxyConfigs["gone.yaml"]) != 1 {
		t.Fatalf("the file didn't load what the test removes: listener %v, proxies %v", listeners.GetListenerForPort(lport), tcpProxyConfigs)
	}

	removeConfigs(filepath.Join("/configs", "gone.yaml"))
	if httpConfigs["gone.yaml"] != nil || listenerConfigs["gone.yaml"] != nil || mcpConfigs["gone.yaml"] != nil || tcpProxyConfigs["gone.yaml"] != nil {
		t.Errorf("configs of the removed file are still kept: %v %v %v %v", httpConfigs, listenerConfigs, mcpConfigs, tcpProxyConfigs)
	}
	if httpConfigs["stays.yaml"] == nil {
		t.Error("the config of the other file was removed")
	}
	if listeners.GetListenerForPort(lport) != nil {
		t.Error("the listener of the removed file is still there")
	}
	if _, ok := httpPayload(port, "/gone"); ok {
		t.Error("the response payload of the removed file is still served")
	}
	if tcpproxy.WillProxyTCP(port) {
		t.Error("the tcp proxy of the removed file still has upstreams")
	}
	// a file that was never loaded
	removeConfigs("/configs/never-loaded.yaml")
}

func TestRemoveAllConfigs(t *testing.T) {
	resetState(t)
	p1, p2, l1 := newPort(), newPort(), newPort()
	removeListenersAfter(t, l1)
	loadConfig(parse(t, "one.yaml", httpYAML(p1, "/one", "1")+fmt.Sprintf("listeners:\n  %d:\n    protocol: tcp\n", l1)))
	loadConfig(parse(t, "two.yaml", httpYAML(p2, "/two", "2")))
	removeAllConfigs()
	if len(httpConfigs)+len(listenerConfigs)+len(tlsConfigs)+len(mcpConfigs)+len(a2aConfigs)+len(grpcConfigs)+len(tcpProxyConfigs)+len(httpProxyConfigs)+len(grpcProxyConfigs) != 0 {
		t.Errorf("configs are still kept: %v %v", httpConfigs, listenerConfigs)
	}
	if _, ok := httpPayload(p1, "/one"); ok {
		t.Error("a response payload is still served")
	}
	if listeners.GetListenerForPort(l1) != nil {
		t.Error("a listener is still there")
	}
}

// ---- Start and Stop ----

func TestStartLoadsTheConfigsOnce(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	port := newPort()
	writeFile(t, dir, "once.yaml", httpYAML(port, "/once", "first"))
	global.ServerConfig.ConfigPaths = []string{dir}

	Start()
	if _, ok := httpPayload(port, "/once"); !ok {
		t.Fatal("Start didn't load the configs of the path")
	}
	if len(configWatchers) != 1 {
		t.Errorf("watchers = %d, want one for the path", len(configWatchers))
	}
	// a second Start is not another load: the file is not read again
	if err := os.Remove(filepath.Join(dir, "once.yaml")); err != nil {
		t.Fatal(err)
	}
	removeAllConfigs()
	Start()
	if _, ok := httpPayload(port, "/once"); ok {
		t.Error("the second Start loaded again")
	}
	Stop()
	if len(configWatchers) != 0 {
		t.Errorf("watchers = %d after Stop, want none", len(configWatchers))
	}
}

func TestStartWithoutConfigPaths(t *testing.T) {
	resetState(t)
	global.ServerConfig.ConfigPaths = nil
	Start()
	if len(configWatchers) != 0 {
		t.Errorf("watchers = %d, want none without paths", len(configWatchers))
	}
	if !loaded {
		t.Error("Start didn't take note that it started")
	}
	Stop()
	Stop() // stopping again is harmless
}

func TestStartConcurrentlyLoadsOnce(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	writeFile(t, dir, "c.yaml", "name: c\n")
	global.ServerConfig.ConfigPaths = []string{dir}
	logged := captureLog(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Start()
		}()
	}
	wg.Wait()
	if n := strings.Count(logged.String(), "Loading configs from path"); n != 1 {
		t.Errorf("the path was loaded %d times by concurrent starts, want 1", n)
	}
	Stop()
}

func TestStartWatchesTheConfigPathAndStopStopsWatching(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	global.ServerConfig.ConfigPaths = []string{dir}
	Start()
	watchersLock.Lock()
	ws := append([]fswatcher.Watcher(nil), configWatchers...)
	watchersLock.Unlock()
	if len(ws) != 1 {
		t.Fatalf("watchers = %d, want one for the path", len(ws))
	}
	w := ws[0]
	waitFor(t, "the watcher to run", w.IsRunning)
	Stop()
	waitFor(t, "the watcher to stop", func() bool { return !w.IsRunning() })
}

func TestStartKeepsWorkingWhenAPathCannotBeWatched(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	port := newPort()
	writeFile(t, dir, "a.yaml", httpYAML(port, "/a", "a"))
	global.ServerConfig.ConfigPaths = []string{filepath.Join(dir, "missing"), dir}
	Start()
	if body, ok := httpPayload(port, "/a"); !ok || body != "a" {
		t.Errorf("payload = %q, %t, want the config of the path that is there", body, ok)
	}
	if len(configWatchers) != 1 {
		t.Errorf("watchers = %d, want one for the path that can be watched", len(configWatchers))
	}
	Stop() // a path that couldn't be watched left no watcher to stop
}

// ---- reacting to the changes of the files ----

// fakeWatcher is a file watcher that delivers the events that the test gives it.
type fakeWatcher struct {
	events  chan fswatcher.WatchEvent
	done    chan struct{}
	once    sync.Once
	running atomic.Bool
	closed  atomic.Int32
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{events: make(chan fswatcher.WatchEvent, 16), done: make(chan struct{})}
}

func (f *fakeWatcher) Watch(ctx context.Context) error {
	f.running.Store(true)
	defer f.running.Store(false)
	select {
	case <-f.done:
	case <-ctx.Done():
	}
	return nil
}
func (f *fakeWatcher) AddPath(string, ...fswatcher.PathOption) error { return nil }
func (f *fakeWatcher) DropPath(string) error                         { return nil }
func (f *fakeWatcher) Events() <-chan fswatcher.WatchEvent           { return f.events }
func (f *fakeWatcher) Dropped() <-chan fswatcher.WatchEvent          { return nil }
func (f *fakeWatcher) IsRunning() bool                               { return f.running.Load() }
func (f *fakeWatcher) Stats() fswatcher.WatcherStats                 { return fswatcher.WatcherStats{} }
func (f *fakeWatcher) Paths() []string                               { return nil }
func (f *fakeWatcher) Close() {
	f.closed.Add(1)
	f.once.Do(func() {
		close(f.done)
		close(f.events)
	})
}
func (f *fakeWatcher) send(path string, types ...fswatcher.EventType) {
	f.events <- fswatcher.WatchEvent{Path: path, Types: types}
}

// shortDebounce makes the changes of the files wait for a moment rather than for the seconds that they do.
func shortDebounce(t *testing.T) {
	t.Helper()
	old := configDebounce
	configDebounce = 50 * time.Millisecond
	t.Cleanup(func() { configDebounce = old })
}

// waitFor polls for a condition that something else makes true, and fails the test if it doesn't happen in time.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hasHTTPConfig(name string) bool {
	lock.Lock()
	defer lock.Unlock()
	return httpConfigs[name] != nil
}

// goroutinesIn tells how many goroutines have a function of the given name on their stack.
func goroutinesIn(name string) int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, name) {
			n++
		}
	}
	return n
}

func TestWatchedChangeLoadsTheChangedFileOnly(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	dir := t.TempDir()
	changed := writeFile(t, dir, "changed.yaml", httpYAML(newPort(), "/changed", "c"))
	writeFile(t, dir, "other.yaml", httpYAML(newPort(), "/other", "o"))
	global.ServerConfig.ConfigPaths = []string{dir}
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	fw.send(changed, fswatcher.EventMod)
	fw.send(changed, fswatcher.EventMod) // a burst of events is one change
	waitFor(t, "the changed file to be loaded", func() bool { return hasHTTPConfig("changed.yaml") })
	time.Sleep(3 * configDebounce)
	if hasHTTPConfig("other.yaml") {
		t.Error("a file that didn't change was loaded")
	}
}

func TestWatchedChangesOfSeveralFilesLoadTogether(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	dir := t.TempDir()
	a := writeFile(t, dir, "a.yaml", httpYAML(newPort(), "/a", "a"))
	b := writeFile(t, dir, "b.yaml", httpYAML(newPort(), "/b", "b"))
	writeFile(t, dir, "c.yaml", httpYAML(newPort(), "/c", "c"))
	global.ServerConfig.ConfigPaths = []string{dir}
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	fw.send(a, fswatcher.EventCreate)
	fw.send(b, fswatcher.EventMod)
	waitFor(t, "both files to be loaded", func() bool { return hasHTTPConfig("a.yaml") && hasHTTPConfig("b.yaml") })
	time.Sleep(3 * configDebounce)
	if hasHTTPConfig("c.yaml") {
		t.Error("a file that didn't change was loaded")
	}
}

func TestWatchedRemoveAndRenameAwayRemoveTheConfigs(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	dir := t.TempDir()
	global.ServerConfig.ConfigPaths = []string{dir}
	portRemoved, portRenamed, portKept := newPort(), newPort(), newPort()
	for name, port := range map[string]int{"removed.yaml": portRemoved, "renamed.yaml": portRenamed, "kept.yaml": portKept} {
		loadConfig(parse(t, name, httpYAML(port, "/x", name)))
	}
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	// the files of the events are gone from the disk, as they are when the events are of their removal
	fw.send(filepath.Join(dir, "removed.yaml"), fswatcher.EventRemove)
	fw.send(filepath.Join(dir, "renamed.yaml"), fswatcher.EventRename)
	waitFor(t, "the configs to be removed", func() bool { return !hasHTTPConfig("removed.yaml") && !hasHTTPConfig("renamed.yaml") })
	if !hasHTTPConfig("kept.yaml") {
		t.Error("the config of a file that is still there was removed")
	}
	if _, ok := httpPayload(portRemoved, "/x"); ok {
		t.Error("the payload of the removed file is still served")
	}
	if _, ok := httpPayload(portKept, "/x"); !ok {
		t.Error("the payload of the file that is still there is not served any more")
	}
}

func TestWatchedRenameOfAFileThatIsStillThereIsAChange(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	dir := t.TempDir()
	global.ServerConfig.ConfigPaths = []string{dir}
	f := writeFile(t, dir, "moved-in.yaml", httpYAML(newPort(), "/in", "in"))
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)
	fw.send(f, fswatcher.EventRename) // the file that was renamed to this name is there
	waitFor(t, "the file to be loaded", func() bool { return hasHTTPConfig("moved-in.yaml") })
}

func TestWatchedEventOfTheWholePath(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	dir := t.TempDir()
	global.ServerConfig.ConfigPaths = []string{dir}
	writeFile(t, dir, "a.yaml", httpYAML(newPort(), "/a", "a"))
	writeFile(t, dir, "b.yaml", httpYAML(newPort(), "/b", "b"))
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	// an event for the parent of the path (it has ".." in it) is a change of everything
	fw.send(dir+"/../x", fswatcher.EventCreate)
	waitFor(t, "everything to be loaded", func() bool { return hasHTTPConfig("a.yaml") && hasHTTPConfig("b.yaml") })
	fw.send(dir+"/../x", fswatcher.EventRemove)
	waitFor(t, "everything to be removed", func() bool { return !hasHTTPConfig("a.yaml") && !hasHTTPConfig("b.yaml") })
}

func TestWatcherOfTheStartupIsClosedByStopWhetherOrNotItRuns(t *testing.T) {
	resetState(t)
	idle, running := newFakeWatcher(), newFakeWatcher()
	go running.Watch(context.Background())
	waitFor(t, "the watcher to run", running.IsRunning)
	watchersLock.Lock()
	configWatchers = []fswatcher.Watcher{idle, nil, running}
	watchersLock.Unlock()
	Stop()
	if idle.closed.Load() != 1 || running.closed.Load() != 1 {
		t.Errorf("closed: idle %d, running %d, want each once", idle.closed.Load(), running.closed.Load())
	}
	waitFor(t, "the watcher to stop", func() bool { return !running.IsRunning() })
	if len(configWatchers) != 0 {
		t.Errorf("watchers = %d after Stop, want none", len(configWatchers))
	}
}
