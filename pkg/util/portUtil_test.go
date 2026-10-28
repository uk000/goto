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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"goto/pkg/global"
)

func TestGetConnectionPortNum(t *testing.T) {
	local := func(port int) context.Context {
		return context.WithValue(context.Background(), http.LocalAddrContextKey, net.Addr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}))
	}
	tests := []struct {
		name string
		ctx  context.Context
		uri  string
		want int
	}{
		{"local address", local(7070), "/x", 7070},
		{"port prefix doesn't change the connection port", local(7070), "/port=7000/x", 7070},
		{"current port set by the prefix doesn't change it either", context.WithValue(local(7070), CurrentPortKey, 7000), "/port=7000/x", 7070},
		{"no local address falls back to the current port", context.WithValue(context.Background(), CurrentPortKey, 7071), "/x", 7071},
		{"nothing known", context.Background(), "/x", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.uri, nil).WithContext(tc.ctx)
			if got := GetConnectionPortNum(r); got != tc.want {
				t.Errorf("GetConnectionPortNum = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGetConnectionPortNumWithTheRequestStore(t *testing.T) {
	// the request store makes the port that the prefix names the current one, and the connection keeps its own
	prevReady, prevLive, prevTunnel := global.Funcs.IsReadinessProbe, global.Funcs.IsLivenessProbe, WillTunnel
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	WillTunnel = func(*http.Request, *RequestStore) bool { return false }
	t.Cleanup(func() {
		global.Funcs.IsReadinessProbe, global.Funcs.IsLivenessProbe, WillTunnel = prevReady, prevLive, prevTunnel
	})
	r := httptest.NewRequest("POST", "/proxy/tcp/http/connect/enable", nil)
	r.RequestURI = "/port=7000/proxy/tcp/http/connect/enable"
	ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7070}))
	r = r.WithContext(context.WithValue(ctx, CurrentPortKey, 7070))
	_, r, _ = WithRequestStore(r, nil)
	if got := GetListenerPortNum(r); got != 7000 {
		t.Fatalf("GetListenerPortNum = %d, want 7000: the request store is expected to make the named port current", got)
	}
	if got := GetRequestOrListenerPortNum(r); got != 7000 {
		t.Errorf("GetRequestOrListenerPortNum = %d, want 7000", got)
	}
	if got := GetConnectionPortNum(r); got != 7070 {
		t.Errorf("GetConnectionPortNum = %d, want 7070", got)
	}
}
