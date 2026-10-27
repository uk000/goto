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
	"net/http/httptest"
	"testing"
)

func TestCheckAdminRequest(t *testing.T) {
	tests := map[string]bool{
		// The root paths of the goto APIs are admin requests, also when they are for a port.
		"/server/response/status/set/503": true,
		"/request/headers/track/add/x":    true,
		"/response/delay/set/1s":          true,
		"/k8s/resources":                  true,
		"/port=8080/k8s/x":                true,
		"/port=8080/server/listeners":     true,
		"/registry/peers":                 true,
		// The k8s API that goto mocks is traffic, so forced status, delay and the other rules of goto apply to it,
		// as they do to any other URI that is not an API of goto.
		"/api":                                   false,
		"/api/v1":                                false,
		"/api/v1/namespaces/x/configmaps":        false,
		"/apis":                                  false,
		"/apis/apps":                             false,
		"/apis/apps/v1":                          false,
		"/apis/apps/v1/namespaces/x/deployments": false,
		"/port=8080/apis/apps/v1":                false,
		"/port=8080/api/v1/pods":                 false,
		"/":                                      false,
		"/anything/else":                         false,
	}
	for uri, want := range tests {
		r := httptest.NewRequest("GET", uri, nil)
		if got := CheckAdminRequest(r); got != want {
			t.Errorf("CheckAdminRequest(%q) = %v, want %v", uri, got, want)
		}
	}
}
