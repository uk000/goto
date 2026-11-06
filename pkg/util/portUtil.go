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

package util

import (
	"context"
	"fmt"
	"goto/pkg/global"
	"net"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/metadata"
)

// GetGRPCPort finds the port that a gRPC call came in on: from the context, then the :authority header, then the default gRPC port.
func GetGRPCPort(ctx context.Context) int {
	port := GetContextPort(ctx)
	if port > 0 {
		return port
	}
	if headers, ok := metadata.FromIncomingContext(ctx); ok && len(headers[":authority"]) > 0 {
		if pieces := strings.Split(headers[":authority"][0], ":"); len(pieces) > 1 {
			if portNum, err := strconv.Atoi(pieces[1]); err == nil {
				return portNum
			}
		}
	}
	return global.Self.GRPCPort
}

// GetPortFromAddress returns the port after the last colon of the address, or 0 if there is none.
func GetPortFromAddress(addr string) int {
	if pieces := strings.Split(addr, ":"); len(pieces) > 1 {
		if port, err := strconv.Atoi(pieces[len(pieces)-1]); err == nil {
			return port
		}
	}
	return 0
}

// GetPortValueFromLocalAddressContext returns the port of the local address of the server that the context carries, or empty if there is none.
func GetPortValueFromLocalAddressContext(ctx context.Context) string {
	if val := ctx.Value(http.LocalAddrContextKey); val != nil {
		srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
		if pieces := strings.Split(srvAddr.String(), ":"); len(pieces) > 1 {
			return pieces[len(pieces)-1]
		}
	}
	return ""
}

// GetContextPort returns the current port set in the context, falling back to the port of the local address, or 0.
func GetContextPort(ctx context.Context) int {
	if val := ctx.Value(CurrentPortKey); val != nil {
		return val.(int)
	}
	if val := GetPortValueFromLocalAddressContext(ctx); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			return port
		}
	}
	return 0
}

// GetListenerPortNum returns the current port of the context of the request.
func GetListenerPortNum(r *http.Request) int {
	return GetContextPort(r.Context())
}

// GetConnectionPortNum is the port of the listener that the request came in over. Unlike GetListenerPortNum, it is not
// the port that the /port={port} prefix of the request names: the server makes that the current port of the request.
func GetConnectionPortNum(r *http.Request) int {
	if val := GetPortValueFromLocalAddressContext(r.Context()); val != "" {
		if port, err := strconv.Atoi(val); err == nil && port > 0 {
			return port
		}
	}
	if conn := GetConn(r); conn != nil {
		if addr := conn.LocalAddr(); addr != nil {
			if port := GetPortFromAddress(addr.String()); port > 0 {
				return port
			}
		}
	}
	return GetListenerPortNum(r)
}

// checkRequestPort works out the request port from the /port= URI prefix, the port var, or else the listener port, and remembers it in the request store.
func checkRequestPort(r *http.Request, rs *RequestStore) (port string, portNum int) {
	uri := r.RequestURI
	if strings.HasPrefix(uri, "/port=") {
		rs.RequestPort = strings.Split(strings.Split(uri, "/port=")[1], "/")[0]
		rs.RequestPortNum, _ = strconv.Atoi(rs.RequestPort)
	} else if port := GetStringParamValue(r, "port"); port != "" {
		rs.RequestPortNum, _ = strconv.Atoi(port)
		rs.RequestPort = port
	} else {
		rs.RequestPortNum = GetListenerPortNum(r)
		rs.RequestPort = strconv.Itoa(rs.RequestPortNum)
	}
	rs.RequestPortChecked = true
	return rs.RequestPort, rs.RequestPortNum
}

// getRequestOrListenerPort returns the request port from the request store, working it out first if it was not checked yet.
func getRequestOrListenerPort(r *http.Request) (port string, portNum int) {
	rs := GetRequestStoreIfPresent(r)
	if rs != nil && rs.RequestPortChecked {
		return rs.RequestPort, rs.RequestPortNum
	}
	return checkRequestPort(r, rs)
}

// GetRequestOrListenerPort returns the port that the request is for (from its /port= prefix or port var), or else the listener port, as a string.
func GetRequestOrListenerPort(r *http.Request) string {
	port, _ := getRequestOrListenerPort(r)
	return port
}

// GetRequestOrListenerPortNum returns the port that the request is for (from its /port= prefix or port var), or else the listener port, as a number.
func GetRequestOrListenerPortNum(r *http.Request) int {
	_, port := getRequestOrListenerPort(r)
	return port
}

// GetCurrentListenerLabel returns the label of the listener for the port that the request is for.
func GetCurrentListenerLabel(r *http.Request) string {
	return global.Funcs.GetListenerLabelForPort(GetRequestStore(r).RequestPortNum)
}

// ValidateListener checks that a listener exists for the port var of the request, and responds with 400 and a message if not.
func ValidateListener(w http.ResponseWriter, r *http.Request) (bool, string) {
	port := GetIntParamValue(r, "port")
	if !global.Funcs.IsListenerPresent(port) {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("No listener for port %d", port)
		fmt.Fprintln(w, msg)
		AddLogMessage(msg, r)
		return false, msg
	}
	return true, ""
}
