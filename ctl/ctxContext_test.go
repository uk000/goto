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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goto/pkg/global"
	"goto/pkg/types"

	"gopkg.in/yaml.v3"
)

// readContexts reads the contexts that were saved to a file.
func readContexts(t *testing.T, path string) Contexts {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := Contexts{}
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatalf("context file is not YAML: %v\n%s", err, data)
	}
	return saved
}

func TestDefaultContextPathsAreInTheHomeDirectory(t *testing.T) {
	if !strings.HasSuffix(DefaultCtlContextPath, "/.goto_context/") {
		t.Errorf("default context path = %q", DefaultCtlContextPath)
	}
	if DefaultCtxFile != DefaultCtlContextPath+"gotoContext.yaml" {
		t.Errorf("default context file = %q", DefaultCtxFile)
	}
	if DefaultContextName != "default" {
		t.Errorf("default context name = %q", DefaultContextName)
	}
	if CtxFlagSet == nil || ApplyFlagSet == nil {
		t.Errorf("flag sets = %v, %v", ApplyFlagSet, CtxFlagSet)
	}
}

func TestLoadOrCreateContextFileCreatesTheFileWithTheDefaultContext(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "new", "dirs", "ctx.yaml")
	global.CtlConfig.ContextFile = path
	global.CtlConfig.RemoteURL = "http://remote:9090"
	loadOrCreateContextFile()
	if ctxFile != path {
		t.Errorf("context file = %q, want %q", ctxFile, path)
	}
	saved := readContexts(t, path)
	if len(saved) != 1 || saved["default"] == nil || saved["default"].RemoteGotoURL != "http://remote:9090" || saved["default"].Name != "default" {
		t.Errorf("saved contexts = %v", saved)
	}
	if contexts["default"] == nil || contexts["default"].RemoteGotoURL != "http://remote:9090" {
		t.Errorf("contexts = %v", contexts)
	}
}

func TestLoadOrCreateContextFileWithoutAPathUsesTheDefaultFile(t *testing.T) {
	isolate(t)
	global.CtlConfig.ContextFile = ""
	loadOrCreateContextFile()
	if ctxFile != DefaultCtxFile {
		t.Errorf("context file = %q, want the default %q", ctxFile, DefaultCtxFile)
	}
	if saved := readContexts(t, DefaultCtxFile); saved["default"] == nil {
		t.Errorf("saved contexts = %v", saved)
	}
}

func TestLoadOrCreateContextFileReadsAFileThatIsThere(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, dir, "ctx.yaml", "a:\n  name: a\n  remoteGotoURL: http://a\nb:\n  name: b\n  remoteGotoURL: http://b\n")
	global.CtlConfig.ContextFile = path
	loadOrCreateContextFile()
	if len(contexts) != 2 || contexts["a"].RemoteGotoURL != "http://a" || contexts["b"].RemoteGotoURL != "http://b" {
		t.Errorf("contexts = %v", contexts)
	}
	// What is there is not touched.
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "http://a") || strings.Contains(string(data), "default") {
		t.Errorf("context file = %q", data)
	}
}

func TestLoadOrCreateContextFileWithAFileThatIsNotValid(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, dir, "ctx.yaml", "a: [unclosed")
	global.CtlConfig.ContextFile = path
	within(t, "loadOrCreateContextFile", loadOrCreateContextFile)
	if len(contexts) != 0 {
		t.Errorf("contexts = %v, want none from a file that is not valid", contexts)
	}
	if data, _ := os.ReadFile(path); string(data) != "a: [unclosed" {
		t.Errorf("the file was changed: %q", data)
	}
}

func TestLoadOrCreateContextFileWhenThePathIsADirectory(t *testing.T) {
	dir := isolate(t)
	global.CtlConfig.ContextFile = dir
	within(t, "loadOrCreateContextFile", loadOrCreateContextFile)
	if len(contexts) != 0 {
		t.Errorf("contexts = %v, want none", contexts)
	}
}

func TestAddContext(t *testing.T) {
	dir := isolate(t)
	ctxFile = filepath.Join(dir, "ctx.yaml")
	addContext("first", "http://first")
	addContext("first", "http://ignored")
	addContext("", "")
	if c := contexts["first"]; c == nil || c.RemoteGotoURL != "http://first" {
		t.Errorf("context first = %+v, want the one that was added first", c)
	}
	if c := contexts["default"]; c == nil || c.RemoteGotoURL != "http://localhost:8080" {
		t.Errorf("default context = %+v, want the remote URL of the config", c)
	}
	saved := readContexts(t, ctxFile)
	if len(saved) != 2 || saved["first"] == nil || saved["default"] == nil {
		t.Errorf("saved contexts = %v", saved)
	}
}

func TestSaveContextsWithoutAFileUsesTheDefault(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(DefaultCtlContextPath, 0755); err != nil {
		t.Fatal(err)
	}
	contexts = Contexts{"x": &types.Context{Name: "x", RemoteGotoURL: "http://x"}}
	ctxFile = ""
	saveContexts()
	if ctxFile != DefaultCtxFile {
		t.Errorf("context file = %q, want the default", ctxFile)
	}
	if saved := readContexts(t, DefaultCtxFile); saved["x"] == nil || saved["x"].RemoteGotoURL != "http://x" {
		t.Errorf("saved contexts = %v", saved)
	}
}

func TestSaveContextsToAPathThatCannotBeWrittenDoesNotPanic(t *testing.T) {
	dir := isolate(t)
	ctxFile = filepath.Join(dir, "no-such-dir", "ctx.yaml")
	contexts = Contexts{"x": &types.Context{Name: "x"}}
	within(t, "saveContexts", saveContexts)
	if _, err := os.Stat(ctxFile); !os.IsNotExist(err) {
		t.Errorf("a file was made: %v", err)
	}
}

func TestUpdateContext(t *testing.T) {
	dir := isolate(t)
	ctxFile = filepath.Join(dir, "ctx.yaml")
	contexts = Contexts{"known": &types.Context{Name: "known", RemoteGotoURL: "http://old"}}

	// A context that is not known is added.
	global.CtlConfig.Name, global.CtlConfig.RemoteURL = "fresh", "http://fresh"
	updateContext()
	if contexts["fresh"] == nil || contexts["fresh"].RemoteGotoURL != "http://fresh" {
		t.Errorf("contexts = %v", contexts)
	}
	// A context that is known gets the new URL, and the file has it.
	global.CtlConfig.Name, global.CtlConfig.RemoteURL = "known", "http://new"
	updateContext()
	if currentContext == nil || currentContext != contexts["known"] || currentContext.RemoteGotoURL != "http://new" {
		t.Errorf("current context = %+v", currentContext)
	}
	if saved := readContexts(t, ctxFile); saved["known"].RemoteGotoURL != "http://new" {
		t.Errorf("saved contexts = %v", saved)
	}
	// Without a URL there is nothing to change.
	global.CtlConfig.RemoteURL = ""
	updateContext()
	if contexts["known"].RemoteGotoURL != "http://new" {
		t.Errorf("URL = %q, want it to be kept when no URL is given", contexts["known"].RemoteGotoURL)
	}
	// Without a name, it is the default context.
	global.CtlConfig.Name, global.CtlConfig.RemoteURL = "", "http://default"
	updateContext()
	if contexts["default"] == nil || contexts["default"].RemoteGotoURL != "http://default" {
		t.Errorf("default context = %+v", contexts["default"])
	}
}

func TestLoadContext(t *testing.T) {
	isolate(t)
	contexts = Contexts{
		"default": &types.Context{Name: "default", RemoteGotoURL: "http://d"},
		"named":   &types.Context{Name: "named", RemoteGotoURL: "http://n"},
	}
	global.CtlConfig.Context = "named"
	loadContext()
	if currentContext != contexts["named"] {
		t.Errorf("current context = %+v, want named", currentContext)
	}
	global.CtlConfig.Context = "unknown"
	loadContext()
	if currentContext != contexts["default"] {
		t.Errorf("current context = %+v, want the default for a name that is not known", currentContext)
	}
	contexts = Contexts{}
	loadContext()
	if currentContext != nil {
		t.Errorf("current context = %+v, want none when there are no contexts", currentContext)
	}
}

func TestContextsAreYAMLOfTheNameAndTheRemoteURL(t *testing.T) {
	out, err := yaml.Marshal(Contexts{"c": &types.Context{Name: "c", RemoteGotoURL: "http://c"}})
	if err != nil || string(out) != "c:\n    name: c\n    remoteGotoURL: http://c\n" {
		t.Errorf("yaml = %q, %v", out, err)
	}
}
