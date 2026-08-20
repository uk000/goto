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
	"fmt"
	"io"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	gotogrpc "goto/pkg/rpc/grpc"
	grpcclient "goto/pkg/rpc/grpc/client"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
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
	util.AddRoute(serverRouter, "/services", getActiveServices, "GET")
	extAuthzRouter := util.PathRouter(grpcRouter, "/extauthz")
	util.AddRoute(extAuthzRouter, "/rules", getExtAuthzRulesAPI, "GET")
	util.AddRoute(extAuthzRouter, "/rules", addExtAuthzRulesAPI, "POST")
	util.AddRoute(extAuthzRouter, "/rules", removeExtAuthzRulesAPI, "DELETE")
}

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

func getExtAuthzRulesAPI(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, GetExtAuthzRules())
}

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

func openGRPCPort(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
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
		msg = fmt.Sprintf("Service [%s] registered for serving on port [%d]", service.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getActiveServices(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	active := strings.Contains(r.RequestURI, "active")
	if active {
		if port > 0 {
			util.WriteJsonPayload(w, ActiveServices[port])
		} else {
			util.WriteJsonPayload(w, ActiveServices)
		}
	} else {
		util.WriteJsonPayload(w, gotogrpc.ServiceRegistry.Services)
	}
}

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
	util.WriteJsonPayload(w, gotogrpc.ServiceRegistry.Services)
	util.AddLogMessage("Remote services loaded", r)
}
