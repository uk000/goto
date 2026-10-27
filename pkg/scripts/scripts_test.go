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

package scripts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newManager is a script manager that the test owns alone.
func newManager() *ScriptManager {
	return &ScriptManager{scripts: map[string]*Script{}}
}

// useScriptsDir makes the scripts that are stored go to a directory of the test, which does not exist yet.
func useScriptsDir(t *testing.T) string {
	t.Helper()
	old := ScriptsDir
	dir := filepath.Join(t.TempDir(), "scripts")
	ScriptsDir = dir
	t.Cleanup(func() { ScriptsDir = old })
	return dir
}

// has tells if the manager has a script of the name.
func has(sm *ScriptManager, name string) bool {
	sm.scriptsLock.RLock()
	defer sm.scriptsLock.RUnlock()
	return sm.scripts[name] != nil
}

// within runs fn, and fails the test when it panics or when it has not returned by the deadline. A script that is run
// could otherwise stop the test run.
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
	case <-time.After(20 * time.Second):
		t.Fatalf("%s did not return in time", what)
	}
}

// run runs a script of the manager, and returns what it wrote.
func run(t *testing.T, sm *ScriptManager, name string, args []string, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	within(t, "running script "+name, func() {
		sm.RunScript(name, args, strings.NewReader(stdin), &out)
	})
	return out.String()
}

func TestAddScriptFromTextRuns(t *testing.T) {
	dir := useScriptsDir(t)
	sm := newManager()
	sm.AddScript("hello", "echo hello", false)
	if got := run(t, sm, "hello", nil, ""); got != "hello\n" {
		t.Errorf("output = %q, want %q", got, "hello\n")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a script that is not stored created %s (%v)", dir, err)
	}
}

func TestAddScriptStoreWritesAnExecutableFile(t *testing.T) {
	dir := useScriptsDir(t)
	sm := newManager()
	content := "#!/bin/sh\necho \"stored:$1:$2\"\n"
	sm.AddScript("st", content, true)
	path := filepath.Join(dir, "st.sh")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatalf("file %s = %q, %v, want the content of the script", path, data, err)
	}
	if info, _ := os.Stat(path); info == nil || info.Mode()&0100 == 0 {
		t.Errorf("file %s is not executable: %v", path, info)
	}
	if got := run(t, sm, "st", []string{"a", "b"}, ""); got != "stored:a:b\n" {
		t.Errorf("output = %q, want the args to be $1 and $2", got)
	}
	// A script of the same name is replaced.
	sm.AddScript("st", "#!/bin/sh\necho second\n", true)
	if got := run(t, sm, "st", nil, ""); got != "second\n" {
		t.Errorf("output after replacing = %q, want %q", got, "second\n")
	}
}

func TestAddScriptStoreFailsWithoutRegistering(t *testing.T) {
	sm := newManager()
	// The directory of the scripts can't be made when a file is where it is meant to be.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	old := ScriptsDir
	ScriptsDir = filepath.Join(blocker, "scripts")
	t.Cleanup(func() { ScriptsDir = old })
	sm.AddScript("nodir", "echo no", true)
	// The file can't be written when the name leads to a directory that is not there.
	ScriptsDir = filepath.Join(t.TempDir(), "scripts")
	sm.AddScript("sub/missing", "echo no", true)
	var dump bytes.Buffer
	sm.DumpScripts(&dump)
	if strings.TrimSpace(dump.String()) != "{}" {
		t.Errorf("scripts = %s, want none to be registered when they can't be stored", dump.String())
	}
}

func TestAddMultiCommandScriptRunsTheCommandsInOrder(t *testing.T) {
	sm := newManager()
	sm.AddMultiCommandScript("multi", []string{"echo a", "echo b", "echo c"})
	if got := run(t, sm, "multi", nil, ""); got != "a\nb\nc\n" {
		t.Errorf("output = %q, want the commands in order", got)
	}
	sm.AddMultiCommandScript("multi", []string{"echo only"})
	if got := run(t, sm, "multi", nil, ""); got != "only\n" {
		t.Errorf("output after replacing = %q", got)
	}
}

func TestRunScriptHasStdin(t *testing.T) {
	sm := newManager()
	sm.AddScript("cat", "cat", false)
	if got := run(t, sm, "cat", nil, "piped input"); got != "piped input" {
		t.Errorf("output = %q, want the input", got)
	}
}

func TestRunScriptWritesStderrToTheOutput(t *testing.T) {
	sm := newManager()
	sm.AddScript("err", "echo out; echo err 1>&2", false)
	if got := run(t, sm, "err", nil, ""); got != "out\nerr\n" {
		t.Errorf("output = %q, want stdout and stderr", got)
	}
}

func TestRunFailingScriptReportsTheErrorInTheOutput(t *testing.T) {
	sm := newManager()
	sm.AddScript("fail", "echo before; exit 3", false)
	got := run(t, sm, "fail", nil, "")
	if !strings.HasPrefix(got, "before\n") || !strings.Contains(got, "Failed to run script") || !strings.Contains(got, "exit status 3") {
		t.Errorf("output = %q, want the output of the script and its failure", got)
	}
}

func TestScriptWithoutContentFails(t *testing.T) {
	sm := newManager()
	sm.AddScript("empty", "", false)
	if got := run(t, sm, "empty", nil, ""); !strings.Contains(got, "Failed to run script") {
		t.Errorf("output = %q, want a failure for a script that has nothing to run", got)
	}
}

func TestRunScriptWithNilArgs(t *testing.T) {
	sm := newManager()
	sm.AddScript("plain", "echo ok", false)
	if got := run(t, sm, "plain", nil, ""); got != "ok\n" {
		t.Errorf("output = %q", got)
	}
}

func TestRemoveScriptDeletesItsFile(t *testing.T) {
	dir := useScriptsDir(t)
	sm := newManager()
	sm.AddScript("gone", "#!/bin/sh\necho x\n", true)
	sm.AddScript("kept", "#!/bin/sh\necho y\n", true)
	sm.AddScript("text", "echo z", false)
	sm.RemoveScript("gone")
	if _, err := os.Stat(filepath.Join(dir, "gone.sh")); !os.IsNotExist(err) {
		t.Errorf("file of the removed script is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kept.sh")); err != nil {
		t.Errorf("file of the other script is gone: %v", err)
	}
	sm.RemoveScript("text")
	sm.RemoveScript("never-added")
	if has(sm, "gone") || has(sm, "text") || !has(sm, "kept") {
		t.Errorf("scripts that are left: gone=%v text=%v kept=%v", has(sm, "gone"), has(sm, "text"), has(sm, "kept"))
	}
}

func TestRemoveAllRemovesScriptsAndFiles(t *testing.T) {
	dir := useScriptsDir(t)
	sm := newManager()
	sm.AddScript("a", "#!/bin/sh\necho a\n", true)
	sm.AddScript("b", "echo b", false)
	sm.AddMultiCommandScript("c", []string{"echo c"})
	sm.RemoveAll()
	for _, name := range []string{"a", "b", "c"} {
		if has(sm, name) {
			t.Errorf("script %s is still there", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.sh")); !os.IsNotExist(err) {
		t.Errorf("file of a stored script is still there: %v", err)
	}
	sm.RemoveAll()
}

func TestDumpScriptsShape(t *testing.T) {
	dir := useScriptsDir(t)
	sm := newManager()
	sm.AddScript("stored", "#!/bin/sh\necho x\n", true)
	sm.AddScript("text", "echo secret-text", false)
	var out bytes.Buffer
	sm.DumpScripts(&out)
	var got map[string]map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the dump is not JSON: %v\n%s", err, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("dump = %s, want 2 scripts", out.String())
	}
	if got["stored"]["name"] != "stored" || got["stored"]["filePath"] != filepath.Join(dir, "stored.sh") {
		t.Errorf("stored script = %v", got["stored"])
	}
	if got["text"]["name"] != "text" || got["text"]["filePath"] != "" {
		t.Errorf("text script = %v", got["text"])
	}
	if strings.Contains(out.String(), "secret-text") {
		t.Errorf("the text of a script is in the dump: %s", out.String())
	}
}

func TestRunCommandsRunsThem(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out")
	sm := newManager()
	sm.RunCommands("m", []string{"echo one > " + file, "echo two >> " + file})
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "one\ntwo\n" {
		t.Errorf("file = %q, %v, want the output of the commands", data, err)
	}
	file2 := filepath.Join(t.TempDir(), "out")
	within(t, "RunCommands", func() { RunCommands("startup", []string{"echo " + "pkg > " + file2}) })
	data, err = os.ReadFile(file2)
	if err != nil || string(data) != "pkg\n" {
		t.Errorf("file = %q, %v, want the output of the command", data, err)
	}
}

func TestScriptRunWithStdIn(t *testing.T) {
	var out bytes.Buffer
	within(t, "RunWithStdIn", func() {
		newScriptFromText("s", "echo from-script").RunWithStdIn(&out)
	})
	if out.String() != "from-script\n" {
		t.Errorf("output = %q", out.String())
	}
	file := filepath.Join(t.TempDir(), "out")
	within(t, "RunToStdOut", func() {
		newScriptFromCommands("s", []string{"echo a > " + file, "echo b >> " + file}).RunToStdOut()
	})
	if data, _ := os.ReadFile(file); string(data) != "a\nb\n" {
		t.Errorf("file = %q", data)
	}
}

func TestScriptFromFileOfMissingPathFails(t *testing.T) {
	var out bytes.Buffer
	s := &Script{Name: "missing", FilePath: filepath.Join(t.TempDir(), "nothing.sh")}
	within(t, "Run", func() { s.Run(nil, strings.NewReader(""), &out) })
	if !strings.Contains(out.String(), "Failed to run script") {
		t.Errorf("output = %q, want the failure", out.String())
	}
}

func TestNewScriptConstructors(t *testing.T) {
	if s := newScriptFromText("n", "echo a"); s.Name != "n" || s.Text != "echo a" || s.FilePath != "" {
		t.Errorf("script = %+v", s)
	}
	if s := newScriptFromCommands("n", []string{"a", "b"}); s.Text != "a; b" {
		t.Errorf("text = %q, want the commands joined", s.Text)
	}
	if s := newScriptFromCommands("n", nil); s.Text != "" {
		t.Errorf("text = %q, want none", s.Text)
	}
}

func TestScriptsDirHasADefault(t *testing.T) {
	if ScriptsDir == "" {
		t.Error("ScriptsDir is empty")
	}
	if Scripts == nil {
		t.Error("the package-wide manager is missing")
	}
}

func TestConcurrentUseOfAManager(t *testing.T) {
	useScriptsDir(t)
	sm := newManager()
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers*4)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("w%d", i)
			sm.AddMultiCommandScript(name, []string{fmt.Sprintf("echo %d", i)})
			var out bytes.Buffer
			sm.RunScript(name, nil, strings.NewReader(""), &out)
			if want := fmt.Sprintf("%d\n", i); out.String() != want {
				errs <- fmt.Errorf("%s output = %q, want %q", name, out.String(), want)
			}
			sm.DumpScripts(io.Discard)
			sm.RemoveScript(name)
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the workers did not finish")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var dump bytes.Buffer
	sm.DumpScripts(&dump)
	if strings.TrimSpace(dump.String()) != "{}" {
		t.Errorf("scripts left = %s, want none", dump.String())
	}
}
