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

package util

import (
	"fmt"
	"goto/pkg/global"
	"net/http"
)

var (
	ExcludedHeaders = map[string]bool{}
	HTTPHandler     http.Handler

	WillTunnel    func(*http.Request, *RequestStore) bool
	WillProxyGRPC func(int, any) bool
	WillProxyMCP  func(*http.Request, *RequestStore) bool
)

func GotoProtocol(isH2, isTLS, isGRPC bool) string {
	protocol := "HTTP"
	if isGRPC {
		protocol = "GRPC"
	} else if isTLS {
		if isH2 {
			protocol = "HTTP/2"
		} else {
			protocol = "HTTPS"
		}
	} else if isH2 {
		protocol = "H2C"
	} else {
		protocol = "HTTP/1.1"
	}
	return protocol
}

func SendBadRequest(msg string, w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintln(w, msg)
	AddLogMessage(msg, r)
}

func BuildGotoClientInfo(container map[string]any, port int, name, label, target, url, server string, inArgs, outArgs any, inHeaders, outHeaders http.Header, more map[string]any) map[string]any {
	if container == nil {
		container = map[string]any{}
	}
	clientInfo := map[string]any{
		"Goto-Client":           name,
		"Goto-Host":             global.Self.HostLabel,
		"Goto-Listener":         global.Funcs.GetListenerLabelForPort(port),
		"Goto-Label":            label,
		"Goto-Remote-Target":    target,
		"Goto-Remote-URL":       url,
		"Goto-Remote-Server":    server,
		"Goto-Inbound-Args":     inArgs,
		"Goto-Inbound-Headers":  inHeaders,
		"Goto-Outbound-Args":    outArgs,
		"Goto-Outbound-Headers": outHeaders,
	}
	for k, v := range more {
		clientInfo[k] = v
	}
	container["Goto-Client-Info"] = clientInfo
	return container
}
