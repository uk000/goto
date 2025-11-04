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
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ContextKey struct{ Key string }

type RequestStore struct {
	startTime               time.Time
	endTime                 time.Time
	IsKnownNonTraffic       bool
	IsVersionRequest        bool
	IsFilteredRequest       bool
	IsLockerRequest         bool
	IsPeerEventsRequest     bool
	IsAdminRequest          bool
	IsMetricsRequest        bool
	IsReminderRequest       bool
	IsProbeRequest          bool
	IsHealthRequest         bool
	IsStatusRequest         bool
	IsDelayRequest          bool
	IsPayloadRequest        bool
	IsTunnelConnectRequest  bool
	IsTunnelRequest         bool
	IsTunnelConfigRequest   bool
	IsTrafficEventReported  bool
	IsHeadersSent           bool
	IsTunnelResponseSent    bool
	IsH2                    bool
	IsH2C                   bool
	IsTLS                   bool
	IsGRPC                  bool
	IsJSONRPC               bool
	IsMCP                   bool
	IsAI                    bool
	IsSSE                   bool
	RequestPortChecked      bool
	RequestServed           bool
	StatusCode              int
	BodyLength              int
	RequestPayloadSize      int
	RequestPortNum          int
	HostLabel               string
	ListenerLabel           string
	RequestPort             string
	GotoProtocol            string
	RequestHost             string
	RequestURI              string
	RequestQuery            string
	RequestMethod           string
	RequestProtcol          string
	RequestedMCPTool        string
	DownstreamAddr          string
	UpstreamAddr            string
	ServerName              string
	RequestPayload          string
	TLSVersion              string
	TLSVersionNum           uint16
	RequestHeaders          map[string][]string
	TrafficDetails          []string
	LogMessages             []string
	InterceptResponseWriter interface{}
	HeadersInterceptRW      interface{}
	TunnelCount             int
	RequestedTunnels        []string
	TunnelEndpoints         interface{}
	TunnelLock              sync.RWMutex
	WillProxy               bool
	ProxyTargets            interface{}
	Request                 *http.Request
	ResponseWriter          http.ResponseWriter
}

var (
	RequestStoreKey     = &ContextKey{Key: "requestStore"}
	CurrentPortKey      = &ContextKey{Key: "currentPort"}
	RequestPortKey      = &ContextKey{Key: "requestPort"}
	IgnoredRequestKey   = &ContextKey{Key: "ignoredRequest"}
	ConnectionKey       = &ContextKey{Key: "connection"}
	ProtocolKey         = &ContextKey{Key: "protocol"}
	HeadersKey          = &ContextKey{Key: "headers"}
	AgentContextKey     = &ContextKey{Key: "agentContext"}
	DefaultRequestStore = &RequestStore{}
)

func GetRequestStore(r *http.Request) *RequestStore {
	if r == nil {
		return DefaultRequestStore
	}
	if val := r.Context().Value(RequestStoreKey); val != nil {
		return val.(*RequestStore)
	}
	_, _, rs := WithRequestStore(r)
	return rs
}

func GetRequestStoreFromContext(ctx context.Context) (context.Context, *RequestStore) {
	if val := ctx.Value(RequestStoreKey); val != nil {
		return ctx, val.(*RequestStore)
	}
	return WithRequestStoreForContext(ctx)
}

func GetRequestStoreIfPresent(r *http.Request) *RequestStore {
	if val := r.Context().Value(RequestStoreKey); val != nil {
		return val.(*RequestStore)
	}
	return nil
}

func WithRequestStore(r *http.Request) (context.Context, *http.Request, *RequestStore) {
	rs := &RequestStore{}
	ctx := context.WithValue(r.Context(), RequestStoreKey, rs)
	r = r.WithContext(ctx)
	rs.IsTLS = r.TLS != nil
	rs.Request = r
	populateRequestStore(r)
	return ctx, r, rs
}

func populateRequestStore(r *http.Request) (context.Context, *RequestStore) {
	ctx := r.Context()
	var rs *RequestStore
	if val := ctx.Value(RequestStoreKey); val != nil {
		rs = val.(*RequestStore)
	} else {
		return nil, nil
	}
	isAdminRequest := CheckAdminRequest(r)
	rs.IsGRPC = r.ProtoMajor == 2 && (strings.HasPrefix(r.Header.Get(constants.HeaderContentType), "application/grpc") || r.Method == "PRI")
	rs.IsAdminRequest = isAdminRequest
	rs.IsVersionRequest = strings.HasPrefix(r.RequestURI, "/version")
	rs.IsLockerRequest = strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker")
	rs.IsPeerEventsRequest = strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/events")
	rs.IsMetricsRequest = strings.HasPrefix(r.RequestURI, "/metrics") || strings.HasPrefix(r.RequestURI, "/stats")
	rs.IsReminderRequest = strings.Contains(r.RequestURI, "/remember")
	rs.IsProbeRequest = global.Funcs.IsReadinessProbe(r) || global.Funcs.IsLivenessProbe(r)
	rs.IsHealthRequest = !isAdminRequest && strings.HasPrefix(r.RequestURI, "/health")
	rs.IsStatusRequest = !isAdminRequest && strings.HasPrefix(r.RequestURI, "/status")
	rs.IsDelayRequest = !isAdminRequest && strings.Contains(r.RequestURI, "/delay")
	rs.IsPayloadRequest = !isAdminRequest && !rs.IsGRPC && (strings.Contains(r.RequestURI, "/stream") || strings.Contains(r.RequestURI, "/payload"))
	rs.IsTunnelRequest = strings.HasPrefix(r.RequestURI, "/tunnel=") || !isAdminRequest && WillTunnel(r, rs)
	rs.IsTunnelConfigRequest = strings.HasPrefix(r.RequestURI, "/tunnels")
	rs.IsKnownNonTraffic = rs.IsProbeRequest || rs.IsReminderRequest || rs.IsHealthRequest ||
		rs.IsMetricsRequest || rs.IsVersionRequest || rs.IsLockerRequest ||
		rs.IsAdminRequest || rs.IsTunnelConfigRequest
	rs.WillProxy = !isAdminRequest && WillProxyHTTP(r, rs)
	rs.IsH2C = r.ProtoMajor == 2
	rs.DownstreamAddr = r.RemoteAddr
	rs.RequestHost = r.Host
	rs.RequestURI = r.RequestURI
	rs.RequestQuery = r.URL.Query().Encode()
	rs.RequestMethod = r.Method
	rs.RequestProtcol = r.Proto
	rs.RequestHeaders = r.Header
	rs.IsMCP = strings.Contains(r.RequestURI, "/mcp/") || strings.HasSuffix(r.RequestURI, "/mcp") || strings.Contains(r.RequestURI, "/sse")
	rs.IsAI = strings.Contains(r.RequestURI, "/agent/")
	return ctx, rs
}

func WithRequestStoreForContext(ctx context.Context) (context.Context, *RequestStore) {
	rs := &RequestStore{}
	ctx = context.WithValue(ctx, RequestStoreKey, rs)
	return ctx, rs
}

func (rs *RequestStore) Start() {
	if rs.startTime.IsZero() {
		rs.startTime = time.Now()
	}
}

func (rs *RequestStore) ReportTime(w http.ResponseWriter) {
	if !rs.endTime.IsZero() {
		return
	}
	rs.endTime = time.Now()
	startTime := rs.startTime.UTC().String()
	endTime := rs.endTime.UTC().String()
	took := rs.endTime.Sub(rs.startTime).String()
	if rs.IsTunnelRequest {
		w.Header().Add(fmt.Sprintf("%s-%d", constants.HeaderGotoInAt, rs.TunnelCount), startTime)
		w.Header().Add(fmt.Sprintf("%s-%d", constants.HeaderGotoOutAt, rs.TunnelCount), endTime)
		w.Header().Add(fmt.Sprintf("%s-%d", constants.HeaderGotoTook, rs.TunnelCount), took)
	} else if rs.WillProxy {
		w.Header().Add(fmt.Sprintf("Proxy-%s", constants.HeaderGotoInAt), startTime)
		w.Header().Add(fmt.Sprintf("Proxy-%s", constants.HeaderGotoOutAt), endTime)
		w.Header().Add(fmt.Sprintf("Proxy-%s", constants.HeaderGotoTook), took)
	} else {
		w.Header().Add(constants.HeaderGotoInAt, startTime)
		w.Header().Add(constants.HeaderGotoOutAt, endTime)
		w.Header().Add(constants.HeaderGotoTook, took)
	}
}
