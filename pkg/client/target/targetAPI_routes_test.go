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

package target

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func handlerNameOf(h http.Handler) string {
	name := runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	return name[strings.LastIndex(name, ".")+1:]
}

// The registry pushes tracking headers to its peers at /client/track/headers/{headers}. Registries of v0.8.x
// used /client/track/headers/add/{headers}, which a client of this version still has to accept.
func TestTrackingHeadersRoutes(t *testing.T) {
	root := mux.NewRouter()
	SetRoutes(root.PathPrefix("/client").Subrouter())
	for _, tc := range []struct {
		method, target, handler string
	}{
		{"POST", "/client/track/headers/h1,h2", "addTrackingHeaders"},
		{"PUT", "/client/track/headers/h1", "addTrackingHeaders"},
		{"POST", "/client/track/headers/add/h1,h2", "addTrackingHeaders"},
		{"PUT", "/client/track/headers/add/h1", "addTrackingHeaders"},
		{"POST", "/client/track/headers/clear", "clearTrackingHeaders"},
		{"GET", "/client/track/headers", "getTrackingHeaders"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			var match mux.RouteMatch
			if !root.Match(httptest.NewRequest(tc.method, tc.target, nil), &match) {
				t.Fatalf("no route matched (err: %v), want %s", match.MatchErr, tc.handler)
			}
			if got := handlerNameOf(match.Route.GetHandler()); got != tc.handler {
				t.Errorf("handler = %s, want %s", got, tc.handler)
			}
			if tc.handler == "addTrackingHeaders" {
				want := tc.target[strings.LastIndex(tc.target, "/")+1:]
				if got := match.Vars["headers"]; got != want {
					t.Errorf("headers = %q, want %q", got, want)
				}
			}
		})
	}
}
