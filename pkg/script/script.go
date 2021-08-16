/**
 * Copyright 2021 uk
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

package script

import (
  "fmt"
  "goto/pkg/constants"
  "goto/pkg/util"
  "io"
  "log"
  "net/http"
  "os"
  "os/exec"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type Script struct {
  Name     string   `json:"name"`
  Content  []string `json:"content"`
  Text     string   `json:"-"`
  commands string
}

type ScriptManager struct {
  scripts     map[string]*Script
  scriptsLock sync.RWMutex
}

var (
  Handler = util.ServerHandler{Name: "script", SetRoutes: SetRoutes}
  Scripts = &ScriptManager{scripts: map[string]*Script{}}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  scriptRouter := util.PathRouter(r, "/scripts")
  util.AddRoute(scriptRouter, "/add/{name}", addScript, "POST", "PUT")
  util.AddRoute(scriptRouter, "/remove/{name}", removeScript, "POST", "PUT")
  util.AddRoute(scriptRouter, "/{name}/remove", removeScript, "POST", "PUT")
  util.AddRoute(scriptRouter, "/run/{name}", runScript, "POST", "PUT")
  util.AddRoute(scriptRouter, "/{name}/run", runScript, "POST", "PUT")
  util.AddRoute(scriptRouter, "", getScripts, "GET")
}

func RunCommands(label string, lines []string) {
  Scripts.RunCommands(label, lines)
}

func addScript(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "name")
  Scripts.AddScript(name, util.Read(r.Body))
  msg := fmt.Sprintf("Script [%s] added successfully", name)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removeScript(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "name")
  Scripts.RemoveScript(name)
  msg := fmt.Sprintf("Script [%s] removed successfully", name)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func runScript(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "name")
  Scripts.RunScript(name, w)
  msg := fmt.Sprintf("Script [%s] run successfully", name)
  util.AddLogMessage(msg, r)
}

func getScripts(w http.ResponseWriter, r *http.Request) {
  w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
  Scripts.DumpScripts(w)
}

func (sm *ScriptManager) RunCommands(label string, commands []string) {
  newScriptFromCommands(label, commands).runScriptToStdOut()
}

func (sm *ScriptManager) AddScript(name, content string) {
  script := newScriptFromText(name, content)
  sm.scriptsLock.Lock()
  defer sm.scriptsLock.Unlock()
  sm.scripts[name] = script
}

func (sm *ScriptManager) AddMultiCommandScript(name string, lines []string) {
  script := newScriptFromCommands(name, lines)
  sm.scriptsLock.Lock()
  defer sm.scriptsLock.Unlock()
  sm.scripts[name] = script
}

func (sm *ScriptManager) RemoveScript(name string) {
  sm.scriptsLock.Lock()
  defer sm.scriptsLock.Unlock()
  delete(sm.scripts, name)
}

func (sm *ScriptManager) RunScript(name string, out io.Writer) {
  sm.scriptsLock.RLock()
  script := sm.scripts[name]
  sm.scriptsLock.RUnlock()
  script.runScript(out)
}

func (sm *ScriptManager) DumpScripts(out io.Writer) {
  sm.scriptsLock.RLock()
  defer sm.scriptsLock.RUnlock()
  util.WriteJson(out, sm.scripts)
}

func newScriptFromText(name, text string) *Script {
  lines := strings.Split(text, "\n")
  script := &Script{Name: name, Text: text}
  script.loadCommands(lines)
  return script
}

func newScriptFromCommands(name string, lines []string) *Script {
  script := &Script{Name: name, Text: strings.Join(lines, "; ")}
  script.loadCommands(lines)
  return script
}

func (s *Script) loadCommands(lines []string) {
  var commands []string
  for _, line := range lines {
    line := strings.Trim(line, " \t\n")
    if len(line) > 0 {
      commands = append(commands, line)
    }
  }
  s.Content = commands
  s.commands = strings.Join(commands, "; ")
}

func (s *Script) runScript(out io.Writer) {
  command := "sh"
  args := []string{"-c", s.commands}
  cmd := exec.Command(command, args...)
  cmd.Stdout = out
  cmd.Stderr = out
  if err := cmd.Run(); err != nil {
    log.Printf("Failed to run script [%s]. Error: [%s]\n", s.Text, err.Error())
  } else {
    log.Printf("Script [%s] ran successfully.\n", s.Text)
  }
}

func (s *Script) runScriptToStdOut() {
  s.runScript(os.Stdout)
}
