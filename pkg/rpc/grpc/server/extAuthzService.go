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
	"context"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	gg "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/extauthz"
	"goto/pkg/util"
	"log"
)

var IsExtAuthzServiceRunning = false

func init() {
	global.AddGRPCIntercept(RegisterExtAuthzServer)
	global.AddGRPCStartWatcher(OnExtAuthzStart)
	global.AddGRPCStopWatcher(OnExtAuthzStop)
}

func OnExtAuthzStart() {
}

func OnExtAuthzStop() {
	IsExtAuthzServiceRunning = false
}

func RegisterExtAuthzServer(server global.IGRPCManager) {
	if !IsExtAuthzServiceRunning {
		server.InterceptWithMiddleware(&extauthz.Authorization_ServiceDesc, &ExtAuthzGRPCService{})
		IsExtAuthzServiceRunning = true
	}
	log.Println("Registered Envoy External Authorization GRPC Service")
}

// ExtAuthzGRPCService serves envoy.service.auth.v3.Authorization requests coming from
// Envoy's external authorization filter
type ExtAuthzGRPCService struct {
	extauthz.UnimplementedAuthorizationServer
}

func (es *ExtAuthzGRPCService) Check(ctx context.Context, request *extauthz.CheckRequest) (*extauthz.CheckResponse, error) {
	port := util.GetGRPCPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.extauthz.check", 200)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, nil)
	requestMiniBody := ""
	if request != nil && request.Attributes != nil && request.Attributes.Request != nil && request.Attributes.Request.Http != nil {
		httpRequest := request.Attributes.Request.Http
		requestMiniBody = httpRequest.Method + " " + httpRequest.Host + httpRequest.Path
		if len(requestMiniBody) > 50 {
			requestMiniBody = requestMiniBody[:50]
		}
	}
	gg.LogRequest(ctx, port, "envoy.service.auth.v3.Authorization", "Check", requestHeaders[constants.HeaderAuthority],
		requestHeaders, 1, 0, requestMiniBody)
	// Check request headers against configured rules; first match wins,
	// default is allow
	envoyHeaders := map[string]string{}
	if request != nil && request.Attributes != nil && request.Attributes.Request != nil &&
		request.Attributes.Request.Http != nil {
		for k, v := range request.Attributes.Request.Http.Headers {
			envoyHeaders[k] = v
		}
	}
	var response *extauthz.CheckResponse
	if matchExtAuthzRule(envoyHeaders) == "deny" {
		response = &extauthz.CheckResponse{
			Status: &extauthz.Status{Code: 7, Message: "Permission denied"},
			HttpResponse: &extauthz.CheckResponse_DeniedResponse{
				DeniedResponse: &extauthz.DeniedHttpResponse{
					Status: &extauthz.HttpStatus{Code: extauthz.StatusCode_Forbidden},
				},
			},
		}
	} else {
		response = &extauthz.CheckResponse{
			Status: &extauthz.Status{Message: "OK"},
			HttpResponse: &extauthz.CheckResponse_OkResponse{
				OkResponse: &extauthz.OkHttpResponse{},
			},
		}
	}
	responseLength := -1
	if global.Flags.LogResponseBody || global.Flags.LogResponseMiniBody {
		responseBodyText := util.ToJSONText(response)
		responseLength = len(responseBodyText)
	}
	responseStatus := 200
	responseMessage := "Authorized request"
	if response.GetDeniedResponse() != nil {
		responseStatus = 403
		responseMessage = "Denied request"
	}
	gg.LogResponse(ctx, responseHeaders, responseStatus, 1, responseLength, responseMessage)
	return response, nil
}
