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

// setRoutes registers the gRPC client call routes under both /grpc/client and /client/grpc.
func setRoutes(r *mux.Router) {
	grpcRouter := middleware.RootPath("/grpc")
	clientRouter := middleware.RootPath("/client")
	grpcClientRouter := util.PathRouter(grpcRouter, "/client")
	clientGRPCRouter := util.PathRouter(clientRouter, "/grpc")
	setRoutesWithRouter(grpcClientRouter)
	setRoutesWithRouter(clientGRPCRouter)
}

// setRoutesWithRouter adds the call, call-with-stream and generic call routes to the given router.
func setRoutesWithRouter(r *mux.Router) {
	util.AddRoute(r, "/call/{service}/{method}/{endpoint}", callServiceMethod, "POST")
	util.AddRoute(r, "/call/{service}/{method}/{endpoint}/stream", callServiceMethodStream, "POST")
	util.AddRoute(r, "/call", call, "POST")
}

// call invokes a gRPC method as described by the JSON or YAML GRPCCall in the request body.
func call(w http.ResponseWriter, r *http.Request) {
	call := &GRPCCall{}
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, &call)
	if err != nil {
		util.SendBadRequest(w, r, "Failed to parse payload with error [%s]", err.Error())
		return
	}
	if call.Service == "" || call.Method == "" || call.Endpoint == "" || call.Payloads == nil {
		util.SendBadRequest(w, r, "Invalid payload: %+v", call)
		return
	}
	doCall(call, r, w)
}

// callServiceMethod invokes a gRPC method named in the URI, sending the request body as its payload.
func callServiceMethod(w http.ResponseWriter, r *http.Request) {
	callServiceMethodWithPush(w, r, false)
}

// callServiceMethodStream invokes a gRPC method named in the URI, pushing the response messages as they arrive.
func callServiceMethodStream(w http.ResponseWriter, r *http.Request) {
	callServiceMethodWithPush(w, r, true)
}

// callServiceMethodWithPush is for the routes of the call with the service, method and endpoint in the URI. Whether
// the messages of the call are pushed as they arrive is up to the route, and not to what the URI happens to contain.
func callServiceMethodWithPush(w http.ResponseWriter, r *http.Request, stream bool) {
	endpoint := util.GetStringParamValue(r, "endpoint")
	serviceName := util.GetStringParamValue(r, "service")
	methodName := util.GetStringParamValue(r, "method")
	if endpoint == "" || serviceName == "" || methodName == "" {
		util.SendBadRequest(w, r, "Missing endpoint/service/method")
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

// doCall connects to the endpoint, loads the method via reflection, invokes it, and writes the response headers and payloads.
func doCall(call *GRPCCall, r *http.Request, w http.ResponseWriter) {
	msg := ""
	defer func() {
		if msg != "" {
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	}()
	port := util.GetRequestOrListenerPortNum(r)
	call.RequestHeaders = r.Header
	client, err := CreateGRPCClient("GRPCClient", port, nil, "", call.Endpoint, "", "", &GRPCOptions{IsTLS: false, VerifyTLS: false, KeepOpen: 1 * time.Second})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	defer client.Close()
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
			return
		}
		lock.Lock()
		defer lock.Unlock()
		if headersReceived {
			return
		}
		for _, v := range viaGotos {
			rs.ViaGotos = append(rs.ViaGotos, fmt.Sprintf("%s(gRPC)", v))
		}
		util.SendGotoTrailers(w, r)
		rs.ViaGotos = nil
		headersReceived = true
	}
	var callback func(m proto.Message, h metadata.MD)
	if call.Push {
		fw := intercept.NewFlushWriter(w)
		callback = func(m proto.Message, h metadata.MD) {
			checkViaGotos(h)
			if b, err := protojson.Marshal(m); err == nil {
				lock.Lock()
				fmt.Fprintln(w, util.ToPrettyJSONText(util.JSONFromBytes(b)))
				if fw != nil {
					fw.Flush()
				}
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
	type CallResult struct {
		Headers any
		Payload any
	}
	output := []*CallResult{}
	for _, resp := range result.Responses {
		checkViaGotos(resp.ResponseHeaders)
		callResult := &CallResult{
			Headers: metadata.Join(resp.ResponseHeaders, resp.ResponseTrailers),
		}
		output = append(output, callResult)
		if call.Result {
			jsons := []util.JSON{}
			if len(resp.ResponsePayload) > 0 {
				for _, r := range resp.ResponsePayload {
					j, ok := util.JSONFromJSONText(r)
					if ok && !j.IsEmpty() {
						jsons = append(jsons, j)
					}
				}
			}
			callResult.Payload = jsons
		}
	}
	w.WriteHeader(http.StatusOK)
	util.WriteJson(w, output)
	util.AddLogMessage(fmt.Sprintf("Invoked Service [%s] Method [%s]", call.Service, call.Method), r)
}
