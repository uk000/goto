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

package tls

import (
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tls", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	tlsRouter := r.PathPrefix("/tls").Subrouter()
	util.AddRoute(tlsRouter, "/cacert/add", addCACert, "PUT", "POST")
	util.AddRoute(tlsRouter, "/cacert/remove", removeCACert, "PUT", "POST")
	util.AddRouteQ(tlsRouter, "/workdir/set", setWorkDir, "dir", "POST", "PUT")
}

func addCACert(w http.ResponseWriter, r *http.Request) {
	msg := ""
	data := util.ReadBytes(r.Body)
	if len(data) > 0 {
		StoreCACert(data)
		msg = "CA Cert Stored"
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No Cert Payload"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeCACert(w http.ResponseWriter, r *http.Request) {
	RemoveCACert()
	msg := "CA Cert Removed"
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setWorkDir(w http.ResponseWriter, r *http.Request) {
	msg := ""
	dir := util.GetStringParamValue(r, "dir")
	if dir != "" {
		global.ServerConfig.WorkDir = dir
		msg = fmt.Sprintf("Working directory set to [%s]", dir)
	} else {
		msg = "Missing directory path"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
