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

package pipe

import (
  "fmt"
  "goto/pkg/util"
  "net/http"
  "strings"

  "github.com/gorilla/mux"
)

var (
  Handler = util.ServerHandler{Name: "pipe", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  pipeRouter := util.PathRouter(r, "/pipes")

  util.AddRoute(pipeRouter, "", getPipelines, "GET")
  util.AddRoute(pipeRouter, "/clear", clearPipeline, "POST")

  util.AddRoute(pipeRouter, "/create/{name}", createPipeline, "POST", "PUT")
  util.AddRoute(pipeRouter, "/add", createPipeline, "POST", "PUT")
  util.AddRoute(pipeRouter, "/{name}/clear", clearPipeline, "POST", "PUT")
  util.AddRoute(pipeRouter, "/clear/{name}", clearPipeline, "POST", "PUT")
  util.AddRoute(pipeRouter, "/remove/{name}", removePipeline, "POST", "PUT")
  util.AddRoute(pipeRouter, "/{name}/remove", removePipeline, "POST", "PUT")

  util.AddRoute(pipeRouter, "/{pipe}/sources/add", addSource, "POST", "PUT")
  util.AddRoute(pipeRouter, "/{pipe}/sources/remove/{name}", removeSource, "POST", "PUT")

  util.AddRouteQ(pipeRouter, "/{pipe}/sources/add/k8s/{name}", addK8sSource, "spec", "POST", "PUT")
  util.AddRoute(pipeRouter, "/{pipe}/sources/add/script/{name}", addScriptSource, "POST", "PUT")

  util.AddRoute(pipeRouter, "/{name}/run", runPipeline, "POST")
  util.AddRoute(pipeRouter, "/{name}", getPipelineRuns, "GET")
}

func getPipelines(w http.ResponseWriter, r *http.Request) {
  util.WriteStringJsonPayload(w, Manager.DumpPipes())
}

func createPipeline(w http.ResponseWriter, r *http.Request) {
  msg := ""
  name := util.GetStringParamValue(r, "name")
  if name != "" {
    Manager.CreatePipe(name)
    msg = fmt.Sprintf("Pipe [%s] created", name)
  } else {
    pipe := &Pipe{}
    content := util.Read(r.Body)
    if err := util.ReadJson(content, &pipe); err == nil {
      Manager.AddPipe(pipe)
      msg = fmt.Sprintf("Pipe [%s] added", pipe.Name)
    } else {
      msg = fmt.Sprintf("Failed to parse pipe with error: %s", err.Error())
      fmt.Println(content)
    }
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearPipeline(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "name")
  Manager.ClearPipe(name)
  msg := ""
  if name != "" {
    msg = fmt.Sprintf("Pipe [%s] cleared", name)
  } else {
    msg = "All pipes cleared"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removePipeline(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "name")
  Manager.RemovePipe(name)
  msg := fmt.Sprintf("Pipe [%s] removed", name)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}
func addSource(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pipe := util.GetStringParamValue(r, "pipe")
  source := &PipelineSource{}
  if err := util.ReadJsonPayload(r, &source); err == nil {
    if err := Manager.AddSource(pipe, source); err == nil {
      msg = fmt.Sprintf("Added Source [%s] with Resource Spec [%s] to Pipe [%s]", source.Name, source.Spec, pipe)
    } else {
      msg = fmt.Sprintf("Failed to add source with error: %s", err.Error())
    }
  } else {
    msg = fmt.Sprintf("Failed to parse source with error: %s", err.Error())
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removeSource(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pipe := util.GetStringParamValue(r, "pipe")
  sourceName := util.GetStringParamValue(r, "name")
  if pipe == "" || sourceName == "" {
    msg = "Missing pipe and/or source name"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    if err := Manager.RemoveSource(pipe, sourceName); err == nil {
      msg = fmt.Sprintf("Removed Source [%s] from Pipe [%s]", sourceName, pipe)
    } else {
      msg = fmt.Sprintf("Failed to remove source [%s] from pipe [%s] with error: %s", sourceName, pipe, err.Error())
    }
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func addK8sSource(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pipe := util.GetStringParamValue(r, "pipe")
  sourceName := util.GetStringParamValue(r, "name")
  resourceID := util.GetStringParamValue(r, "spec")
  if pipe == "" || sourceName == "" || resourceID == "" {
    msg = "Missing pipe and/or source name"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    if err := Manager.AddK8sSource(pipe, sourceName, resourceID); err == nil {
      msg = fmt.Sprintf("Added K8s Source [%s] with Resource Spec [%s] to Pipe [%s]", sourceName, resourceID, pipe)
    } else {
      msg = fmt.Sprintf("Failed to add K8s source [%s] to pipe [%s] with error: %s", sourceName, pipe, err.Error())
    }
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func addScriptSource(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pipe := util.GetStringParamValue(r, "pipe")
  sourceName := util.GetStringParamValue(r, "name")
  if pipe == "" || sourceName == "" {
    msg = "Missing pipe and/or source name"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    Manager.AddScriptSource(pipe, sourceName, util.Read(r.Body))
    msg = fmt.Sprintf("Added Script Source [%s] to Pipe [%s]", sourceName, pipe)
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func runPipeline(w http.ResponseWriter, r *http.Request) {
  msg := ""
  name := util.GetStringParamValue(r, "name")
  yaml := strings.Contains(r.Header.Get("Accept"), "yaml")
  if name == "" {
    msg = "Missing pipe name"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    if err := Manager.RunPipe(name, w, yaml); err == nil {
      msg = fmt.Sprintf("Pipe [%s] Ran Successfully", name)
    } else {
      msg = fmt.Sprintf("Failed to run pipeline [%s] with error: %s", name, err.Error())
      fmt.Fprintln(w, msg)
      w.WriteHeader(http.StatusInternalServerError)
    }
  }
  util.AddLogMessage(msg, r)
}

func getPipelineRuns(w http.ResponseWriter, r *http.Request) {
  msg := ""
  name := util.GetStringParamValue(r, "name")
  yaml := strings.Contains(r.Header.Get("Accept"), "yaml")
  if name == "" {
    msg = "Missing pipe name"
    fmt.Fprintln(w, msg)
    w.WriteHeader(http.StatusBadRequest)
  } else {
    msg = fmt.Sprintf("Pipe [%s] Runs Reported", name)
    if yaml {

    } else {
      fmt.Fprintln(w, util.ToJSONText(Manager.GetRuns(name)))
    }
  }
  util.AddLogMessage(msg, r)
}