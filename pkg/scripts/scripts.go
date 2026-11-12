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
	"fmt"
	"goto/pkg/util"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Script struct {
	Name     string   `json:"name"`
	FilePath string   `json:"filePath"`
	Content  []string `json:"content"`
	Text     string   `json:"-"`
}

type ScriptManager struct {
	scripts     map[string]*Script
	scriptsLock sync.RWMutex
}

var (
	Scripts    = &ScriptManager{scripts: map[string]*Script{}}
	ScriptsDir = func() string {
		if dir := os.Getenv("SCRIPTS_DIR"); dir != "" {
			return dir
		}
		if dir := os.TempDir(); dir != "" {
			return dir + "/scripts"
		}
		if dir, err := os.Getwd(); err == nil {
			return dir + "/scripts"
		}
		return "./scripts"
	}()
)

// RunCommands runs the commands as one script via the global script manager, with output to stdout.
func RunCommands(label string, lines []string) {
	Scripts.RunCommands(label, lines)
}

// RunCommands joins the commands into a single script and runs it with output to stdout.
func (sm *ScriptManager) RunCommands(label string, commands []string) {
	newScriptFromCommands(label, commands).RunToStdOut()
}

// AddScript registers a script by name, either written as an executable .sh file under ScriptsDir (store) or kept in memory as text.
func (sm *ScriptManager) AddScript(name, content string, store bool) {
	var script *Script
	if store {
		filePath := fmt.Sprintf("%s/%s.sh", ScriptsDir, name)
		if err := os.MkdirAll(ScriptsDir, 0777); err != nil {
			log.Printf("Script: Failed to create scripts directory [%s]. Error: [%s]\n", ScriptsDir, err.Error())
			return
		}
		if err := os.WriteFile(filePath, []byte(content), 0777); err != nil {
			log.Printf("Script: Failed to write script file [%s]. Error: [%s]\n", filePath, err.Error())
			return
		}
		log.Printf("Script: Script file [%s] created successfully.\n", filePath)
		script = &Script{
			Name:     name,
			FilePath: filePath,
		}
	} else {
		script = newScriptFromText(name, content)
	}
	sm.scriptsLock.Lock()
	defer sm.scriptsLock.Unlock()
	sm.scripts[name] = script
}

// AddMultiCommandScript registers an in-memory script that runs the given lines in sequence.
func (sm *ScriptManager) AddMultiCommandScript(name string, lines []string) {
	script := newScriptFromCommands(name, lines)
	sm.scriptsLock.Lock()
	defer sm.scriptsLock.Unlock()
	sm.scripts[name] = script
}

// RemoveScript unregisters the named script and deletes its stored file, if any.
func (sm *ScriptManager) RemoveScript(name string) {
	sm.scriptsLock.Lock()
	defer sm.scriptsLock.Unlock()
	if sm.scripts[name] != nil {
		if sm.scripts[name].FilePath != "" {
			if err := os.Remove(sm.scripts[name].FilePath); err != nil {
				log.Printf("Script: Failed to remove script file [%s]. Error: [%s]\n", sm.scripts[name].FilePath, err.Error())
			} else {
				log.Printf("Script: Script file [%s] removed successfully.\n", sm.scripts[name].FilePath)
			}
		}
		log.Printf("Script: Script [%s] removed successfully.\n", name)
		delete(sm.scripts, name)
	}
}

// RemoveAll unregisters all scripts and deletes their stored files.
func (sm *ScriptManager) RemoveAll() {
	sm.scriptsLock.Lock()
	defer sm.scriptsLock.Unlock()
	for name, script := range sm.scripts {
		if script.FilePath != "" {
			if err := os.Remove(script.FilePath); err != nil {
				log.Printf("Script: Failed to remove script file [%s]. Error: [%s]\n", script.FilePath, err.Error())
			} else {
				log.Printf("Script: Script file [%s] removed successfully.\n", script.FilePath)
			}
		}
		log.Printf("Script: Script [%s] removed successfully.\n", name)
		delete(sm.scripts, name)
	}
}

// RunScript runs the named script with the args and the given input and output, writing a not-found message to out if it doesn't exist.
func (sm *ScriptManager) RunScript(name string, args []string, in io.Reader, out io.Writer) {
	sm.scriptsLock.RLock()
	script := sm.scripts[name]
	sm.scriptsLock.RUnlock()
	if script == nil {
		// There is no script to run when the name was never added, was removed, or could not be stored.
		msg := fmt.Sprintf("Script: Script [%s] not found", name)
		log.Println(msg)
		fmt.Fprintln(out, msg)
		return
	}
	if args == nil {
		args = []string{}
	}
	script.Run(args, in, out)
}

// DumpScripts writes the registered scripts to out as JSON.
func (sm *ScriptManager) DumpScripts(out io.Writer) {
	sm.scriptsLock.RLock()
	defer sm.scriptsLock.RUnlock()
	util.WriteJson(out, sm.scripts)
}

// newScriptFromText creates an in-memory script from the text.
func newScriptFromText(name, text string) *Script {
	script := &Script{Name: name, Text: text}
	return script
}

// newScriptFromCommands creates an in-memory script that runs the lines in sequence, joined with "; ".
func newScriptFromCommands(name string, lines []string) *Script {
	script := &Script{Name: name, Text: strings.Join(lines, "; ")}
	return script
}

// Run executes the script (its file, or its text via sh -c) with the args, wiring stdin to in and stdout/stderr to out.
func (s *Script) Run(args []string, in io.Reader, out io.Writer) {
	var command string
	if s.FilePath != "" {
		log.Printf("Script [%s]: Running script from file [%s] with args [%+v].\n", s.Name, s.FilePath, args)
		command = s.FilePath
	} else if s.Text != "" {
		log.Printf("Script [%s]: Running script from content with args [%+v].\n", s.Name, args)
		command = "sh"
		args = append([]string{"-c", s.Text}, args...)
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "Script: Failed to run script [%s]. Error: [%s]\n", s.Text, err.Error())
		log.Printf("Script: Failed to run script [%s]. Error: [%s]\n", s.Text, err.Error())
	} else {
		log.Printf("Script: Finished script successfully [%s].\n", s.Text)
	}
}

// RunToStdOut runs the script with os.Stdin as input and os.Stdout as output.
func (s *Script) RunToStdOut() {
	s.Run([]string{}, os.Stdin, os.Stdout)
}

// RunWithStdIn runs the script with os.Stdin as input and the given writer as output.
func (s *Script) RunWithStdIn(out io.Writer) {
	s.Run([]string{}, os.Stdin, out)
}
