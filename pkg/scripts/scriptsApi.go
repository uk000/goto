/**
 * Copyright 2025 uk
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
	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("script", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	scriptRouter := util.PathRouter(r, "/scripts")
	util.AddRoute(scriptRouter, "/add/{name}", addScript, "POST", "PUT")
	util.AddRoute(scriptRouter, "/store/{name}", addScript, "POST", "PUT")
	util.AddRoute(scriptRouter, "/remove/all", removeScript, "POST")
	util.AddRoute(scriptRouter, "/remove/{name}", removeScript, "POST")
	util.AddRoute(scriptRouter, "/{name}/remove", removeScript, "POST")
	util.AddRoute(scriptRouter, "/run/{name}", runScript, "POST")
	util.AddRouteQO(scriptRouter, "/{name}/run", runScript, "args", "POST")
	util.AddRoute(scriptRouter, "", getScripts, "GET")
}

func addScript(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	store := strings.Contains(r.RequestURI, "/store/")
	Scripts.AddScript(name, util.Read(r.Body), store)
	msg := fmt.Sprintf("Script [%s] added successfully", name)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func removeScript(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	msg := ""
	if name == "all" || strings.Contains(r.RequestURI, "/remove/all") {
		Scripts.RemoveAll()
		msg = "All scripts removed successfully"
	} else {
		Scripts.RemoveScript(name)
		msg = fmt.Sprintf("Script [%s] removed successfully", name)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func runScript(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	args, _ := util.GetListParam(r, "args")
	w.Header().Set(constants.HeaderContentType, "application/octet-stream")
	Scripts.RunScript(name, args, r.Body, w)
	msg := fmt.Sprintf("Script [%s] run successfully", name)
	util.AddLogMessage(msg, r)
}

func getScripts(w http.ResponseWriter, r *http.Request) {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	Scripts.DumpScripts(w)
}
