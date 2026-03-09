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

func GetPortNumFromGRPCAuthority(ctx context.Context) int {
	if headers, ok := metadata.FromIncomingContext(ctx); ok && len(headers[":authority"]) > 0 {
		if pieces := strings.Split(headers[":authority"][0], ":"); len(pieces) > 1 {
			if portNum, err := strconv.Atoi(pieces[1]); err == nil {
				return portNum
			}
		}
	}
	return global.Self.GRPCPort
}

func GetPortFromAddress(addr string) int {
	if pieces := strings.Split(addr, ":"); len(pieces) > 1 {
		if port, err := strconv.Atoi(pieces[len(pieces)-1]); err == nil {
			return port
		}
	}
	return 0
}

func GetPortValueFromLocalAddressContext(ctx context.Context) string {
	if val := ctx.Value(http.LocalAddrContextKey); val != nil {
		srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
		if pieces := strings.Split(srvAddr.String(), ":"); len(pieces) > 1 {
			return pieces[len(pieces)-1]
		}
	}
	return ""
}

func GetContextPort(ctx context.Context) int {
	if val := ctx.Value(CurrentPortKey); val != nil {
		return val.(int)
	}
	if val := GetPortValueFromLocalAddressContext(ctx); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			return port
		}
	}
	return GetPortNumFromGRPCAuthority(ctx)
}

func GetListenerPortNum(r *http.Request) int {
	return GetContextPort(r.Context())
}

func checkRequestPort(r *http.Request, rs *RequestStore) (port string, portNum int) {
	uri := r.RequestURI
	if strings.HasPrefix(uri, "/port=") {
		rs.RequestPort = strings.Split(strings.Split(uri, "/port=")[1], "/")[0]
		rs.RequestPortNum, _ = strconv.Atoi(rs.RequestPort)
	} else {
		rs.RequestPortNum = GetListenerPortNum(r)
		rs.RequestPort = strconv.Itoa(rs.RequestPortNum)
	}
	rs.RequestPortChecked = true
	return rs.RequestPort, rs.RequestPortNum
}

func getRequestOrListenerPort(r *http.Request) (port string, portNum int) {
	rs := GetRequestStoreIfPresent(r)
	if rs != nil && rs.RequestPortChecked {
		return rs.RequestPort, rs.RequestPortNum
	}
	return checkRequestPort(r, rs)
}

func GetRequestOrListenerPort(r *http.Request) string {
	port, _ := getRequestOrListenerPort(r)
	return port
}
func GetRequestOrListenerPortNum(r *http.Request) int {
	_, port := getRequestOrListenerPort(r)
	return port
}

func GetCurrentListenerLabel(r *http.Request) string {
	return global.Funcs.GetListenerLabelForPort(GetRequestStore(r).RequestPortNum)
}

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
