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

package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/global"
)

// Execute starts a server, or sends traffic that is saved under /tmp, unless it is given a command of ctl. So these tests
// only run the commands of ctl, which are ctx and apply.

type seenRequest struct {
	Method, URI, Body string
}

// remoteGoto is a goto that records what it is sent.
type remoteGoto struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

func newRemoteGoto(t *testing.T) *remoteGoto {
	t.Helper()
	r := &remoteGoto{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.seen = append(r.seen, seenRequest{req.Method, req.URL.RequestURI(), string(body)})
		r.mu.Unlock()
		w.Write([]byte("ok"))
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *remoteGoto) got() []seenRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seenRequest(nil), r.seen...)
}

// execute runs the command with the given arguments, as if it was started from the shell with them.
func execute(t *testing.T, args ...string) {
	t.Helper()
	freshFlags()
	os.Args = append([]string{"goto"}, args...)
	global.Version = ""
	within(t, "Execute", Execute)
}

func TestExecuteRunsTheCtlCommands(t *testing.T) {
	logged := isolate(t)
	remote := newRemoteGoto(t)
	dir := t.TempDir()
	ctxPath := filepath.Join(dir, "contexts", "ctx.yaml")
	cfgPath := filepath.Join(dir, "config.yaml")
	config := `
name: from-cmd-test
scripts:
  config:
    - name: s1
      content: echo hi
  run:
    - name: s1
      args: [a, b]
`
	if err := os.WriteFile(cfgPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	// ctx saves a context in the given file, and creates the folder of the file.
	execute(t, "-contextFile", ctxPath, "ctx", "-n", "mine", "-r", remote.URL)
	if global.Version != global.DevTag {
		t.Errorf("version = %q, want %q when there is none", global.Version, global.DevTag)
	}
	hasAll(t, logged, "Version: "+global.DevTag)
	if !global.CmdConfig.CmdCtlMode || global.CmdConfig.CmdClientMode {
		t.Errorf("ctl mode = %v, client mode = %v", global.CmdConfig.CmdCtlMode, global.CmdConfig.CmdClientMode)
	}
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("the context file was not written: %v", err)
	}
	for _, want := range []string{"mine:", "name: mine", "remoteGotoURL: " + remote.URL, "default:"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("context file has no %q:\n%s", want, data)
		}
	}
	if got := remote.got(); len(got) != 0 {
		t.Errorf("ctx sent %v to the remote, want nothing", got)
	}

	// apply sends the config to the remote of the context that is named.
	execute(t, "-contextFile", ctxPath, "-context", "mine", "apply", "-f", cfgPath)
	want := []seenRequest{
		{"POST", "/scripts/add/s1", "echo hi"},
		{"POST", "/scripts/s1/run?args=a&args=b", ""},
	}
	if got := remote.got(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v, want %+v", got, want)
	}
}

func TestExecuteApplyWithAMissingConfigFileSendsNothing(t *testing.T) {
	logged := isolate(t)
	remote := newRemoteGoto(t)
	dir := t.TempDir()
	ctxPath := filepath.Join(dir, "ctx.yaml")
	execute(t, "-contextFile", ctxPath, "ctx", "-n", "mine", "-r", remote.URL)
	execute(t, "-contextFile", ctxPath, "-context", "mine", "apply", "-f", filepath.Join(dir, "missing.yaml"))
	hasAll(t, logged, "Failed to read config path")
	if got := remote.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestExecuteKeepsTheVersionItIsGiven(t *testing.T) {
	logged := isolate(t)
	ctxPath := filepath.Join(t.TempDir(), "ctx.yaml")
	freshFlags()
	os.Args = []string{"goto", "-contextFile", ctxPath, "ctx", "-n", "x", "-r", "http://x:1"}
	global.Version, global.Commit = "1.2.3", "abc123"
	within(t, "Execute", Execute)
	if global.Version != "1.2.3" {
		t.Errorf("version = %q, want the one that was given", global.Version)
	}
	hasAll(t, logged, "Version: 1.2.3, Commit: abc123")
}
