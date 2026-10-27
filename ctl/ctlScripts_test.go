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
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

func TestProcessScriptsWithNothingSendsNothing(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processScripts", func() {
		processScripts(nil)
		processScripts(&Scripts{})
		processScripts(&Scripts{Config: []*ScriptConfig{}, Run: []*ScriptRun{}})
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestProcessScriptsSendsTheContentOfAScript(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processScripts", func() {
		processScripts(&Scripts{Config: []*ScriptConfig{{Name: "inline", Content: "echo one\necho two"}}})
	})
	got := r.got()
	if len(got) != 1 {
		t.Fatalf("requests = %v, want 1", got)
	}
	if got[0].Method != "POST" || got[0].Path != "/scripts/add/inline" || got[0].ContentType != "text/plain" || got[0].Body != "echo one\necho two" {
		t.Errorf("request = %+v", got[0])
	}
}

func TestProcessScriptsStoresTheFileOfAScript(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	path := writeFile(t, t.TempDir(), "run.sh", "#!/bin/sh\necho from file\n")
	within(t, "processScripts", func() {
		// The file is what is sent, and not the content, when there are both.
		processScripts(&Scripts{Config: []*ScriptConfig{{Name: "stored", FilePath: path, Content: "ignored"}}})
	})
	got := r.got()
	if len(got) != 1 || got[0].Path != "/scripts/store/stored" || got[0].Body != "#!/bin/sh\necho from file\n" {
		t.Errorf("requests = %+v", got)
	}
}

func TestProcessScriptsSkipsScriptsThatAreNotValid(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	missing := filepath.Join(t.TempDir(), "missing.sh")
	within(t, "processScripts", func() {
		processScripts(&Scripts{Config: []*ScriptConfig{
			nil,
			{Name: "", Content: "echo no name"},
			{Name: "nocontent"},
			{Name: "nofile", FilePath: missing},
			{Name: "valid", Content: "echo valid"},
		}})
	})
	if paths := r.paths(); len(paths) != 1 || paths[0] != "POST /scripts/add/valid" {
		t.Errorf("requests = %v, want only the valid one", paths)
	}
}

func TestProcessScriptsRunsScriptsWithArgs(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processScripts", func() {
		processScripts(&Scripts{Run: []*ScriptRun{
			{Name: "plain"},
			{Name: "with-args", Args: []string{"one", "two three", "a,b&c=d"}},
			{Name: "  "},
			{Name: ""},
		}})
	})
	got := r.got()
	if len(got) != 2 {
		t.Fatalf("requests = %+v, want the 2 that have a name", got)
	}
	if got[0].Method != "POST" || got[0].Path != "/scripts/plain/run" || got[0].RawQuery != "" || got[0].ContentType != "text/plain" {
		t.Errorf("first request = %+v", got[0])
	}
	if got[1].Path != "/scripts/with-args/run" {
		t.Errorf("second request = %+v", got[1])
	}
	q, err := url.ParseQuery(got[1].RawQuery)
	if err != nil || len(q["args"]) != 3 || q["args"][0] != "one" || q["args"][1] != "two three" || q["args"][2] != "a,b&c=d" {
		t.Errorf("args = %v, %v, want the args as they are, one per param", q["args"], err)
	}
}

func TestProcessScriptsConfiguresBeforeItRuns(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processScripts", func() {
		processScripts(&Scripts{
			Run:    []*ScriptRun{{Name: "s"}},
			Config: []*ScriptConfig{{Name: "s", Content: "echo"}},
		})
	})
	if paths := r.paths(); len(paths) != 2 || paths[0] != "POST /scripts/add/s" || paths[1] != "POST /scripts/s/run" {
		t.Errorf("requests = %v, want the script to be added before it is run", paths)
	}
}

func TestProcessScriptsWhenTheRemoteFailsGoesOn(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusInternalServerError)
	useRemote(t, r.URL)
	within(t, "processScripts", func() {
		processScripts(&Scripts{
			Config: []*ScriptConfig{{Name: "a", Content: "echo a"}, {Name: "b", Content: "echo b"}},
			Run:    []*ScriptRun{{Name: "a"}},
		})
	})
	if paths := r.paths(); len(paths) != 3 {
		t.Errorf("requests = %v, want all 3 to be tried", paths)
	}
}

func TestProcessScriptsWhenTheRemoteIsGoneDoesNotPanic(t *testing.T) {
	r := newRemote(t)
	url := r.URL
	r.Close()
	useRemote(t, url)
	within(t, "processScripts", func() {
		processScripts(&Scripts{
			Config: []*ScriptConfig{{Name: "a", Content: "echo a"}},
			Run:    []*ScriptRun{{Name: "a"}},
		})
	})
}
