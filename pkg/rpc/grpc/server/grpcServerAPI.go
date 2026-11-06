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

package grpcserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	gotogrpc "goto/pkg/rpc/grpc"
	grpcclient "goto/pkg/rpc/grpc/client"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

var (
	Middleware     = middleware.NewMiddleware("grpc", setRoutes, nil)
	ActiveServices = map[int]map[string]*grpc.GRPCService{}
	GRPCFactory    = GRPCManager
	lock           = sync.RWMutex{}
)

// setRoutes adds the routes of the gRPC server and ext authz admin APIs.
func setRoutes(r *mux.Router) {
	grpcRouter := middleware.RootPath("/grpc")
	serverRouter := util.PathRouter(grpcRouter, "/server")
	util.AddRoute(serverRouter, "/open", openGRPCPort, "POST")
	util.AddRoute(serverRouter, "/services/reflect/{upstream}", loadReflectedServices, "POST")
	util.AddRoute(serverRouter, "/serve/{service}", serveService, "POST")
	util.AddRoute(serverRouter, "/stop/{service}", stopService, "POST")
	util.AddRoute(serverRouter, "/services/{service}/serve", serveService, "POST")
	util.AddRoute(serverRouter, "/services/{service}/stop", stopService, "POST")
	util.AddRoute(serverRouter, "/services/active", getActiveServices, "GET")
	util.AddRoute(serverRouter, "/services", getServices, "GET")
	extAuthzRouter := util.PathRouter(grpcRouter, "/extauthz")
	util.AddRoute(extAuthzRouter, "/rules", getExtAuthzRulesAPI, "GET")
	util.AddRoute(extAuthzRouter, "/rules", addExtAuthzRulesAPI, "POST")
	util.AddRoute(extAuthzRouter, "/rules", removeExtAuthzRulesAPI, "DELETE")
}

// addExtAuthzRulesAPI stores the ext authz rules of the request body, which is a rule or a list of them, and fails if none of them is valid.
func addExtAuthzRulesAPI(w http.ResponseWriter, r *http.Request) {
	rule := ExtAuthzRule{}
	rules := []ExtAuthzRule{}
	msg := ""
	status := http.StatusOK
	body, err := io.ReadAll(r.Body)
	if err == nil {
		err = util.ReadJsonPayloadFromBody(bytes.NewReader(body), &rule)
	}
	if err == nil && rule.Header != "" {
		rules = append(rules, rule)
	} else {
		if err := util.ReadJsonPayloadFromBody(bytes.NewReader(body), &rules); err != nil {
			status = http.StatusBadRequest
			msg = "Invalid ext authz rules payload"
		} else if len(rules) == 0 {
			status = http.StatusBadRequest
			msg = "No ext authz rules given"
		}
	}
	if status == http.StatusOK {
		stored := 0
		for i := range rules {
			if StoreExtAuthzRule(&rules[i]) {
				stored++
			}
		}
		if stored == 0 {
			status = http.StatusBadRequest
			msg = "No valid ext authz rules stored; each rule needs a header and an action of allow or deny"
		} else {
			msg = fmt.Sprintf("Stored [%d] ext authz rules", stored)
		}
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// getExtAuthzRulesAPI reports the stored ext authz rules.
func getExtAuthzRulesAPI(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, GetExtAuthzRules())
}

// removeExtAuthzRulesAPI removes the ext authz rule of the header and value in the query, or all of the rules if no header is given.
func removeExtAuthzRulesAPI(w http.ResponseWriter, r *http.Request) {
	header := strings.ToLower(r.URL.Query().Get("header"))
	value := r.URL.Query().Get("value")
	msg := ""
	if header == "" {
		ClearExtAuthzRules()
		msg = "Cleared all ext authz rules"
	} else if RemoveExtAuthzRule(header, value) {
		msg = fmt.Sprintf("Removed ext authz rule for header [%s]", header)
	} else {
		msg = fmt.Sprintf("No ext authz rule found for header [%s]", header)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// requestedPort gives the port that a request names, in the port param of its path or in the one of its query. The
// routes here have no port in their paths, and the port of the path is only a param of the routes that have it.
func requestedPort(r *http.Request) int {
	if port := util.GetIntParamValue(r, "port"); port > 0 {
		return port
	}
	port, _ := strconv.Atoi(r.URL.Query().Get("port"))
	return port
}

// openGRPCPort opens a gRPC listener on the requested port and serves on it.
func openGRPCPort(w http.ResponseWriter, r *http.Request) {
	port := requestedPort(r)
	msg := ""
	status := http.StatusOK
	if port <= 0 || port > 65535 {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d]", port)
	} else if l, err := listeners.AddGRPCListener(port, true); err == nil {
		GRPCFactory.ServeListener(l)
		msg = fmt.Sprintf("Opened GRPC listener on port [%d]", port)
	} else {
		status = http.StatusInternalServerError
		msg = fmt.Sprintf("Failed to open GRPC listener on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// serveService serves the requested service on the port, which restarts the gRPC server, and records it in the active services of the port.
func serveService(w http.ResponseWriter, r *http.Request) {
	rs, _, _, msg, ok := rpc.CheckService(w, r, grpc.ServiceRegistry)
	if ok {
		service := rs.(*grpc.GRPCService)
		port := util.GetRequestOrListenerPortNum(r)
		GRPCFactory.Serve(port, service)
		lock.Lock()
		if ActiveServices[port] == nil {
			ActiveServices[port] = map[string]*grpc.GRPCService{}
		}
		ActiveServices[port][service.Name] = service
		lock.Unlock()
		msg = fmt.Sprintf("Service [%s] registered for serving on port [%d]", service.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// stopService stops serving the requested service, and removes it from the active services of the port.
func stopService(w http.ResponseWriter, r *http.Request) {
	rs, _, _, msg, ok := rpc.CheckService(w, r, grpc.ServiceRegistry)
	if ok {
		service := rs.(*grpc.GRPCService)
		port := util.GetRequestOrListenerPortNum(r)
		GRPCFactory.StopService(service)
		lock.Lock()
		delete(ActiveServices[port], service.Name)
		if len(ActiveServices[port]) == 0 {
			delete(ActiveServices, port)
		}
		lock.Unlock()
		msg = fmt.Sprintf("Service [%s] stopped on port [%d]", service.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// getActiveServices reports the services that are served, of all the ports or of the port that is asked for.
func getActiveServices(w http.ResponseWriter, r *http.Request) {
	port := requestedPort(r)
	lock.RLock()
	defer lock.RUnlock()
	if port > 0 {
		util.WriteJsonPayload(w, ActiveServices[port])
	} else {
		util.WriteJsonPayload(w, ActiveServices)
	}
}

// knownServices gives the services of the registry as they are now. Services are added to the registry while the
// APIs report them, and the registry encodes itself with its lock held, which the map of services can't be read with
// from here.
func knownServices() any {
	services := struct{ Services json.RawMessage }{}
	if b, err := json.Marshal(gotogrpc.ServiceRegistry); err != nil || json.Unmarshal(b, &services) != nil || len(services.Services) == 0 {
		return map[string]any{}
	}
	return services.Services
}

// getServices reports the services that are known, whether they are served or not.
func getServices(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, knownServices())
}

// loadReflectedServices loads the services of an upstream gRPC server through reflection, and reports the known services.
func loadReflectedServices(w http.ResponseWriter, r *http.Request) {
	msg := ""
	defer func() {
		if msg != "" {
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	}()
	upstream := util.GetStringParamValue(r, "upstream")
	if upstream == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing upstream"
		return
	}
	err := grpcclient.LoadRemoteReflectedServices(upstream)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	util.WriteJsonPayload(w, knownServices())
	util.AddLogMessage("Remote services loaded", r)
}
