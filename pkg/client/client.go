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

package client

import (
	"goto/pkg/client/target"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Handler        util.ServerHandler   = util.ServerHandler{Name: "client", SetRoutes: SetRoutes}
	clientHandlers []util.ServerHandler = []util.ServerHandler{target.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	clientRouter := r.PathPrefix("/client").Subrouter()
	util.AddRoutes(clientRouter, r, root, clientHandlers...)
}
