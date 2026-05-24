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

package grpcclient

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	Middleware = middleware.NewMiddleware("rpc", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	router := middleware.RootPath("/grpc")
	clientRouter := util.PathRouter(router, "/client")
	util.AddRoute(clientRouter, "/call/{service}/{method}/{endpoint}", callServiceMethod, "POST")
	util.AddRoute(clientRouter, "/call/{service}/{method}/{endpoint}/stream", callServiceMethod, "POST")
	util.AddRoute(clientRouter, "/call", call, "POST")
}

func call(w http.ResponseWriter, r *http.Request) {
	call := &GRPCCall{}
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, &call)
	if err != nil {
		util.SendBadRequest(fmt.Sprintf("Failed to parse payload with error [%s]", err.Error()), w, r)
		return
	}
	doCall(call, r, w)
}

func callServiceMethod(w http.ResponseWriter, r *http.Request) {
	endpoint := util.GetStringParamValue(r, "endpoint")
	serviceName := util.GetStringParamValue(r, "service")
	methodName := util.GetStringParamValue(r, "method")
	stream := strings.Contains(r.RequestURI, "stream")
	if endpoint == "" || serviceName == "" || methodName == "" {
		util.SendBadRequest("Missing endpoint/service/method", w, r)
		return
	}
	call := &GRPCCall{
		Service:  serviceName,
		Method:   methodName,
		Endpoint: endpoint,
		Payloads: &GRPCPayloads{Linear: []*GRPCPayload{{Payload: util.Read(r.Body)}}},
		Push:     stream,
	}
	doCall(call, r, w)
}

func doCall(call *GRPCCall, r *http.Request, w http.ResponseWriter) {
	msg := ""
	defer func() {
		if msg != "" {
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	}()
	call.RequestHeaders = r.Header
	client, err := CreateGRPCClient(nil, "", call.Endpoint, "", "", &GRPCOptions{IsTLS: false, VerifyTLS: false, KeepOpen: 1 * time.Second})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	err = client.LoadServiceMethodFromReflection(call.Service, call.Method)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	method := client.Service.Methods[call.Method]
	if method == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Invalid method"
		return
	}
	contentType := r.Header.Get(constants.HeaderContentType)
	if contentType == "" {
		contentType = "plain/text"
	}
	rs := util.GetRequestStore(r)
	headersReceived := false
	lock := sync.Mutex{}
	checkViaGotos := func(h metadata.MD) {
		viaGotos := h[constants.HeaderViaGoto]
		if len(viaGotos) == 0 {
			viaGotos = h[util.LowerViaGoto]
		}
		if len(viaGotos) == 0 {
			viaGotos = h[constants.HeaderViaGoto]
		}
		if len(viaGotos) == 0 {
			viaGotos = h[util.LowerViaGoto]
		}
		if len(viaGotos) > 0 {
			lock.Lock()
			for _, v := range viaGotos {
				rs.ViaGotos = append(rs.ViaGotos, fmt.Sprintf("%s(gRPC)", v))
			}
			util.SendGotoTrailers(w, r)
			rs.ViaGotos = nil
			headersReceived = true
			lock.Unlock()
		}
	}
	var callback func(m proto.Message, h metadata.MD)
	if call.Push {
		fw := intercept.NewFlushWriter(w)
		callback = func(m proto.Message, h metadata.MD) {
			if !headersReceived {
				checkViaGotos(h)
			}
			if b, err := protojson.Marshal(m); err == nil {
				lock.Lock()
				fmt.Fprintln(w, util.ToPrettyJSONText(util.JSONFromBytes(b)))
				fw.Flush()
				lock.Unlock()
			}
		}
	}
	result := client.Invoke(call, callback)
	if result == nil || len(result.Responses) == 0 || len(result.Errors) > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		msg = result.GetErrors()
		if msg == "" {
			msg = "Upstream Unavailable"
		}
		return
	}
	if call.Result {
		type responseType struct {
			Headers any
			Payload any
		}
		response := []responseType{}
		for _, resp := range result.Responses {
			jsons := []util.JSON{}
			if len(resp.ResponsePayload) > 0 {
				for _, r := range resp.ResponsePayload {
					j, ok := util.JSONFromJSONText(r)
					if ok && !j.IsEmpty() {
						jsons = append(jsons, j)
					}
				}
			}
			if !headersReceived {
				checkViaGotos(resp.ResponseHeaders)
			}
			response = append(response, responseType{
				Headers: metadata.Join(resp.ResponseHeaders, resp.ResponseTrailers),
				Payload: jsons,
			})
		}
		util.WriteJson(w, response)
	}
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage(fmt.Sprintf("Invoked Service [%s] Method [%s]", call.Service, call.Method), r)
}
