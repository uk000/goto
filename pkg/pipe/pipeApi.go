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
	"goto/pkg/global"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)


var (
  Handler = util.ServerHandler{Name: "pipe", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  pipeRouter := util.PathRouter(r, "/pipe")
  
	util.AddRoute(pipeRouter, "", getPipelines, "GET")
	util.AddRoute(pipeRouter, "/create/{name}", createPipeline, "POST", "PUT")

  util.AddRoute(pipeRouter, "/{pipe}/sources/add", addSource, "POST", "PUT")
  util.AddRouteQ(pipeRouter, "/{pipe}/sources/k8s/add/{name}", addK8sSource, "id", "POST", "PUT")
  util.AddRoute(pipeRouter, "/{pipe}/templates/store", storeTemplates, "POST", "PUT")

	util.AddRoute(pipeRouter, "/{name}/run", runPipeline, "POST", "PUT")

  util.AddRouteQ(pipeRouter, "/dir/set", setWorkDir, "dir", "{dir}", "POST", "PUT")
}

func getPipelines(w http.ResponseWriter, r *http.Request) {
	util.WriteStringJsonPayload(w, Manager.DumpPipes())
}

func createPipeline(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "name")
  Manager.CreatePipe(name)
  msg := fmt.Sprintf("Pipe [%s] created", name)
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

func addK8sSource(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pipe := util.GetStringParamValue(r, "pipe")
  sourceName := util.GetStringParamValue(r, "name")
  resourceID := util.GetStringParamValue(r, "id")
  if pipe == "" || sourceName == "" || resourceID == "" {
    msg = "Missing pipe and/or source name"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    if err := Manager.AddK8sSource(pipe, sourceName, resourceID); err == nil {
    	msg = fmt.Sprintf("Added K8s Source [%s] with Resource Spec [%s] to Pipe [%s]", sourceName, resourceID, pipe)
		} else {
			msg = fmt.Sprintf("Failed to add K8s source with error: %s", err.Error())
		}
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func storeTemplates(w http.ResponseWriter, r *http.Request) {
  pipe := util.GetStringParamValue(r, "pipe")
  result := Manager.StoreTemplates(pipe, util.ReadBytes(r.Body))
  util.AddLogMessage(util.WriteJsonPayload(w, result), r)
}

func setWorkDir(w http.ResponseWriter, r *http.Request) {
  msg := ""
  dir := util.GetStringParamValue(r, "dir")
  if dir != "" {
    global.WorkDir = dir
    msg = fmt.Sprintf("Working directory set to [%s]", dir)
  } else {
    msg = "Missing directory path"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func runPipeline(w http.ResponseWriter, r *http.Request) {
  msg := ""
  name := util.GetStringParamValue(r, "name")
  if name == "" {
    msg = "Missing pipe name"
    w.WriteHeader(http.StatusBadRequest)
  } else {
		if err := Manager.RunPipe(name, w); err == nil {
			msg = fmt.Sprintf("Pipe [%s] Ran Successfully", name)
		} else {
			msg = fmt.Sprintf("Failed to run pipeline [%s] with error: %s", name, err.Error())
			fmt.Fprintln(w, msg)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
  util.AddLogMessage(msg, r)
}
