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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// A route whose query is optional is served without the query by the routers that are paired with the router that it
// is added to, as well as by that router. Before, the co-routers only had the route with the query.
func TestOptionalQueryRouteIsServedWithoutTheQueryByCoRouters(t *testing.T) {
	r, co := mux.NewRouter(), mux.NewRouter()
	coRoutersMap[r] = append(coRoutersMap[r], co)
	t.Cleanup(func() { delete(coRoutersMap, r) })
	AddRouteQO(r, "/opt", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "q=%q", GetStringParamValue(req, "q"))
	}, "q", "POST")

	for name, router := range map[string]*mux.Router{"router": r, "co-router": co} {
		for target, want := range map[string]string{"/opt": `q=""`, "/opt?q=1": `q="1"`} {
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest("POST", target, nil))
			if rr.Code != http.StatusOK || rr.Body.String() != want {
				t.Errorf("%s POST %s = %d %q, want 200 %q", name, target, rr.Code, rr.Body.String(), want)
			}
		}
	}
}

func TestRequiredQueryRouteIsNotServedWithoutTheQueryByCoRouters(t *testing.T) {
	r, co := mux.NewRouter(), mux.NewRouter()
	coRoutersMap[r] = append(coRoutersMap[r], co)
	t.Cleanup(func() { delete(coRoutersMap, r) })
	AddRouteQ(r, "/req", func(w http.ResponseWriter, req *http.Request) {}, "q", "POST")
	for name, router := range map[string]*mux.Router{"router": r, "co-router": co} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest("POST", "/req", nil))
		if rr.Code == http.StatusOK {
			t.Errorf("%s served a route that needs its query without it", name)
		}
	}
}
