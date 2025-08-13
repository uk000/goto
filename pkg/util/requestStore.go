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
	"goto/pkg/global"
	"net/http"
	"strings"
	"sync"
)

type ContextKey struct{ Key string }

type RequestStore struct {
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
	GotoProtocol            string
	StatusCode              int
	IsH2                    bool
	IsH2C                   bool
	IsTLS                   bool
	IsGRPC                  bool
	IsJSONRPC               bool
	ServerName              string
	TLSVersion              string
	TLSVersionNum           uint16
	RequestPayload          string
	RequestPayloadSize      int
	RequestPort             string
	RequestPortNum          int
	RequestPortChecked      bool
	TrafficDetails          []string
	LogMessages             []string
	InterceptResponseWriter interface{}
	TunnelCount             int
	RequestedTunnels        []string
	TunnelEndpoints         interface{}
	TunnelLock              sync.RWMutex
	WillProxy               bool
	ProxyTargets            interface{}
}

var (
	RequestStoreKey   = &ContextKey{Key: "requestStore"}
	CurrentPortKey    = &ContextKey{Key: "currentPort"}
	RequestPortKey    = &ContextKey{Key: "requestPort"}
	IgnoredRequestKey = &ContextKey{Key: "ignoredRequest"}
	ConnectionKey     = &ContextKey{Key: "connection"}
)

func GetRequestStore(r *http.Request) *RequestStore {
	if val := r.Context().Value(RequestStoreKey); val != nil {
		return val.(*RequestStore)
	}
	_, rs := WithRequestStore(r)
	return rs
}

func GetRequestStoreForContext(ctx context.Context) (context.Context, *RequestStore) {
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

func WithRequestStore(r *http.Request) (context.Context, *RequestStore) {
	rs := &RequestStore{}
	ctx := context.WithValue(r.Context(), RequestStoreKey, rs)
	isAdminRequest := CheckAdminRequest(r)
	rs.IsGRPC = r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc")
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
	rs.WillProxy = !isAdminRequest && WillProxyHTTP(r, rs)
	rs.IsH2C = r.ProtoMajor == 2
	return ctx, rs
}

func WithRequestStoreForContext(ctx context.Context) (context.Context, *RequestStore) {
	rs := &RequestStore{}
	ctx = context.WithValue(ctx, RequestStoreKey, rs)
	return ctx, rs
}
