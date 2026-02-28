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

package grpcclient

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc/metadata"
)

var (
	Middleware = middleware.NewMiddleware("rpc", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	router := util.PathRouter(r, "/grpc")

	util.AddRoute(router, "/call/{endpoint}/{service}/{method}", callServiceMethod, "POST")

}

func callServiceMethod(w http.ResponseWriter, r *http.Request) {
	msg := ""
	defer func() {
		if msg != "" {
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	}()
	endpoint := util.GetStringParamValue(r, "endpoint")
	serviceName := util.GetStringParamValue(r, "service")
	methodName := util.GetStringParamValue(r, "method")
	if endpoint == "" || serviceName == "" || methodName == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing endpoint/service/method"
		return
	}
	client, err := CreateGRPCClient(nil, "", endpoint, "", "", &GRPCOptions{IsTLS: false, VerifyTLS: false, KeepOpen: 1 * time.Minute})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	err = client.LoadServiceMethodFromReflection(serviceName, methodName)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	method := client.Service.Methods[methodName]
	if method == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Invalid method"
		return
	}
	var content [][]byte
	if method.IsClientStream {
		content = util.ReadArrayOfArrays(r.Body)
	} else {
		content = [][]byte{util.ReadBytes(r.Body)}
	}
	contentType := r.Header.Get(constants.HeaderContentType)
	if contentType == "" {
		contentType = "plain/text"
	}
	resp, err := client.Invoke(methodName, nil, content)
	if err != nil || resp == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	w.WriteHeader(http.StatusOK)
	response := map[string]any{}
	response["headers"] = metadata.Join(resp.ResponseHeaders, resp.ResponseTrailers)
	if len(resp.ResponsePayload) > 0 {
		jsons := []util.JSON{}
		for _, r := range resp.ResponsePayload {
			jsons = append(jsons, util.JSONFromJSONText(r))
		}
		response["payload"] = jsons
	} else {
		response["payload"] = "<No Payload>"

	}
	util.WriteJson(w, response)
	util.AddLogMessage(fmt.Sprintf("Invoked Service [%s] Method [%s] with Response [%+v]", serviceName, methodName, response), r)
}
