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

package filter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var stubOnce sync.Once

// stubGlobalFuncs fills the global.Funcs hooks that the server package normally wires up at startup.
// util.WithRequestStore dereferences some of them.
func stubGlobalFuncs() {
	no := func(*http.Request) bool { return false }
	if global.Funcs.IsReadinessProbe == nil {
		global.Funcs.IsReadinessProbe = no
	}
	if global.Funcs.IsLivenessProbe == nil {
		global.Funcs.IsLivenessProbe = no
	}
	if global.Funcs.IsListenerPresent == nil {
		global.Funcs.IsListenerPresent = func(int) bool { return false }
	}
	if global.Funcs.IsListenerOpen == nil {
		global.Funcs.IsListenerOpen = func(int) bool { return false }
	}
	if global.Funcs.GetListenerID == nil {
		global.Funcs.GetListenerID = func(int) string { return "" }
	}
	if global.Funcs.GetListenerLabel == nil {
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	}
	if global.Funcs.GetListenerLabelForPort == nil {
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	}
	// Set by the tunnel package at startup. Required for requests to non admin URIs.
	if util.WillTunnel == nil {
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	}
}

// setup resets the package level ignore and bypass filters, and returns a new router with the filter routes
// registered. The routes are bound to the filter instances that exist at the time of registration, so the router
// has to be created after the reset.
func setup(t *testing.T) *mux.Router {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	reset := func() {
		ignoreFilter = newRequestFilter()
		bypassFilter = newRequestFilter()
	}
	reset()
	t.Cleanup(reset)
	router := mux.NewRouter()
	setRoutes(router)
	return router
}

func filterFor(filterType string) *RequestFilter {
	if filterType == "ignore" {
		return ignoreFilter
	}
	return bypassFilter
}

// newRequest creates a request carrying a RequestStore, as the server does before routing.
func newRequest(method, target string, headers ...string) (*http.Request, *util.RequestStore) {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	_, req, rs := util.WithRequestStore(req, nil)
	return req, rs
}

func call(router http.Handler, method, target string) *httptest.ResponseRecorder {
	req, _ := newRequest(method, target)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func expectStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d (body: %q)", rr.Code, want, rr.Body.String())
	}
}

// expectNotRouted verifies that no route accepts the request. Depending on how the routers are nested, gorilla/mux
// reports a method mismatch as either 404 or 405.
func expectNotRouted(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404 or 405 (body: %q)", rr.Code, rr.Body.String())
	}
}

func expectBodyContains(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	if !strings.Contains(rr.Body.String(), want) {
		t.Errorf("body = %q, want it to contain %q", rr.Body.String(), want)
	}
}

var filterTypes = []string{"ignore", "bypass"}

func TestMiddlewareRegistration(t *testing.T) {
	if Middleware == nil {
		t.Fatal("Middleware is nil")
	}
	if Middleware.Name != "filter" {
		t.Errorf("Middleware.Name = %q, want %q", Middleware.Name, "filter")
	}
	if Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Error("Middleware must provide both routes and a handler")
	}
}

func TestNewRequestFilterDefaults(t *testing.T) {
	rf := newRequestFilter()
	if rf.Status != http.StatusOK {
		t.Errorf("default Status = %d, want %d", rf.Status, http.StatusOK)
	}
	if rf.Uris == nil || rf.NotUris == nil || rf.Headers == nil ||
		rf.UriUpdates == nil || rf.NotUriUpdates == nil || rf.HeaderUpdates == nil {
		t.Error("all filter maps must be initialized")
	}
	if rf.FilteredCount != 0 || rf.PendingUpdates {
		t.Errorf("new filter must have no count and no pending updates, got %d/%v", rf.FilteredCount, rf.PendingUpdates)
	}
}

func TestResetCount(t *testing.T) {
	rf := newRequestFilter()
	rf.FilteredCount = 7
	rf.ResetCount()
	if rf.FilteredCount != 0 {
		t.Errorf("FilteredCount = %d, want 0", rf.FilteredCount)
	}
}

func TestAddURIFilter(t *testing.T) {
	for _, ft := range filterTypes {
		for _, method := range []string{"PUT", "POST"} {
			t.Run(ft+"/"+method, func(t *testing.T) {
				router := setup(t)
				rr := call(router, method, "/"+ft+"/add?uri=/foo.*")
				expectStatus(t, rr, http.StatusOK)
				expectBodyContains(t, rr, "Will "+ft+" URI [/foo.*]")

				rf := filterFor(ft)
				re := rf.UriUpdates["/foo.*"]
				if re == nil {
					t.Fatalf("UriUpdates = %v, want an entry for /foo.*", rf.UriUpdates)
				}
				if !rf.PendingUpdates || !rf.hasURIs {
					t.Errorf("PendingUpdates/hasURIs = %v/%v, want true/true", rf.PendingUpdates, rf.hasURIs)
				}
				if len(rf.Uris) != 0 {
					t.Errorf("Uris = %v, updates must not be active until applied", rf.Uris)
				}
				for uri, want := range map[string]bool{"/foo": true, "/foo/bar": true, "/FOO/bar": true, "/bar": false, "/x/foo": false} {
					if got := re.MatchString(uri); got != want {
						t.Errorf("regex %q MatchString(%q) = %v, want %v", re, uri, got, want)
					}
				}
			})
		}
	}
}

func TestAddURIFilterNegative(t *testing.T) {
	router := setup(t)
	rr := call(router, "POST", "/ignore/add?uri=!/foo.*")
	expectStatus(t, rr, http.StatusOK)

	if len(ignoreFilter.UriUpdates) != 0 {
		t.Errorf("UriUpdates = %v, want none for a negative match", ignoreFilter.UriUpdates)
	}
	re := ignoreFilter.NotUriUpdates["!/foo.*"]
	if re == nil {
		t.Fatalf("NotUriUpdates = %v, want an entry for !/foo.*", ignoreFilter.NotUriUpdates)
	}
	if !re.MatchString("/foo/bar") || re.MatchString("/bar") {
		t.Errorf("negative filter regex %q must match the URI without the ! prefix", re)
	}
}

func TestAddURIFilterRoot(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/"), http.StatusOK)

	re := ignoreFilter.UriUpdates["/"]
	if re == nil {
		t.Fatalf("UriUpdates = %v, want an entry for /", ignoreFilter.UriUpdates)
	}
	for uri, want := range map[string]bool{"": true, "/": true, "/foo": false, "//": false} {
		if got := re.MatchString(uri); got != want {
			t.Errorf("root regex MatchString(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestAddFilterRejectsOtherMethods(t *testing.T) {
	router := setup(t)
	for _, target := range []string{"/ignore/add?uri=/foo", "/ignore/add/header/foo", "/ignore/clear"} {
		expectNotRouted(t, call(router, "GET", target))
	}
	if len(ignoreFilter.UriUpdates) != 0 || len(ignoreFilter.HeaderUpdates) != 0 {
		t.Error("rejected requests must not change the filter")
	}
}

func TestAddHeaderFilter(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		header    string
		key       string
		message   string
		matches   []string
		noMatches []string
	}{
		{
			name: "header name only", target: "/ignore/add/header/foo", header: "foo", key: "",
			message: "Will ignore header [foo : ]", matches: []string{""},
		},
		{
			name: "exact value", target: "/ignore/add/header/foo=bar", header: "foo", key: "bar",
			message: "Will ignore header [foo : bar]", matches: []string{"bar", "BAR"}, noMatches: []string{"barx", "xbar", ""},
		},
		{
			name: "glob value", target: "/ignore/add/header/foo=bar*", header: "foo", key: "bar*",
			message: "Will ignore header [foo : bar*]", matches: []string{"bar", "barbaz"}, noMatches: []string{"xbar"},
		},
		{
			name: "header name is lowercased", target: "/ignore/add/header/Foo=bar", header: "foo", key: "bar",
			message: "Will ignore header [foo : bar]", matches: []string{"bar"}, noMatches: []string{"baz"},
		},
	}
	for _, tt := range tests {
		for _, method := range []string{"PUT", "POST"} {
			t.Run(tt.name+"/"+method, func(t *testing.T) {
				router := setup(t)
				rr := call(router, method, tt.target)
				expectStatus(t, rr, http.StatusOK)
				expectBodyContains(t, rr, tt.message)

				re := ignoreFilter.HeaderUpdates[tt.header][tt.key]
				if re == nil {
					t.Fatalf("HeaderUpdates = %v, want entry [%s][%q]", ignoreFilter.HeaderUpdates, tt.header, tt.key)
				}
				if !ignoreFilter.PendingUpdates || !ignoreFilter.hasHeaders {
					t.Errorf("PendingUpdates/hasHeaders = %v/%v, want true/true", ignoreFilter.PendingUpdates, ignoreFilter.hasHeaders)
				}
				for _, v := range tt.matches {
					if !re.MatchString(v) {
						t.Errorf("regex %q must match %q", re, v)
					}
				}
				for _, v := range tt.noMatches {
					if re.MatchString(v) {
						t.Errorf("regex %q must not match %q", re, v)
					}
				}
			})
		}
	}
}

func TestAddAndRemoveWithoutURIOrHeader(t *testing.T) {
	setup(t)
	req, _ := newRequest("POST", "/ignore/add")

	rr := httptest.NewRecorder()
	ignoreFilter.addFilterHeaderOrURI(rr, req)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Cannot add request filter. No URI or Header")

	rr = httptest.NewRecorder()
	ignoreFilter.removeIgnoreHeaderOrURI(rr, req)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Cannot remove request filter. No URI or Header")

	if ignoreFilter.PendingUpdates {
		t.Error("rejected requests must not mark updates as pending")
	}
}

func TestRemoveURIFilter(t *testing.T) {
	for _, ft := range filterTypes {
		t.Run(ft, func(t *testing.T) {
			router := setup(t)
			expectStatus(t, call(router, "POST", "/"+ft+"/add?uri=/foo"), http.StatusOK)
			expectStatus(t, call(router, "POST", "/"+ft+"/add?uri=!/bar"), http.StatusOK)
			rf := filterFor(ft)
			rf.PendingUpdates = false

			rr := call(router, "POST", "/"+ft+"/remove?uri=/foo")
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Will not "+ft+" URI[/foo]")
			if len(rf.UriUpdates) != 0 || len(rf.NotUriUpdates) != 1 {
				t.Errorf("after removing /foo: UriUpdates=%v NotUriUpdates=%v", rf.UriUpdates, rf.NotUriUpdates)
			}
			if !rf.PendingUpdates {
				t.Error("removal must mark updates as pending")
			}

			expectStatus(t, call(router, "PUT", "/"+ft+"/remove?uri=!/bar"), http.StatusOK)
			if len(rf.NotUriUpdates) != 0 {
				t.Errorf("NotUriUpdates = %v, want it empty after removing !/bar", rf.NotUriUpdates)
			}

			// Removing something that was never added is not an error.
			expectStatus(t, call(router, "POST", "/"+ft+"/remove?uri=/unknown"), http.StatusOK)
		})
	}
}

func TestRemoveHeaderFilter(t *testing.T) {
	router := setup(t)
	for _, target := range []string{"/ignore/add/header/foo=bar", "/ignore/add/header/foo=baz", "/ignore/add/header/qux"} {
		expectStatus(t, call(router, "POST", target), http.StatusOK)
	}

	rr := call(router, "POST", "/ignore/remove/header/foo=bar")
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Will not ignore Header [foo: bar]")
	if _, present := ignoreFilter.HeaderUpdates["foo"]["bar"]; present || len(ignoreFilter.HeaderUpdates["foo"]) != 1 {
		t.Errorf("HeaderUpdates[foo] = %v, want only baz left", ignoreFilter.HeaderUpdates["foo"])
	}

	expectStatus(t, call(router, "PUT", "/ignore/remove/header/foo=baz"), http.StatusOK)
	if _, present := ignoreFilter.HeaderUpdates["foo"]; present {
		t.Errorf("HeaderUpdates = %v, header foo must be dropped once it has no values", ignoreFilter.HeaderUpdates)
	}
	if !ignoreFilter.hasHeaders {
		t.Error("hasHeaders must stay true while qux is configured")
	}

	expectStatus(t, call(router, "POST", "/ignore/remove/header/qux"), http.StatusOK)
	if len(ignoreFilter.HeaderUpdates) != 0 || ignoreFilter.hasHeaders {
		t.Errorf("HeaderUpdates=%v hasHeaders=%v, want empty/false", ignoreFilter.HeaderUpdates, ignoreFilter.hasHeaders)
	}

	// Removing an unknown header is not an error.
	expectStatus(t, call(router, "POST", "/ignore/remove/header/unknown=x"), http.StatusOK)
}

func TestSetAndGetStatus(t *testing.T) {
	for _, ft := range filterTypes {
		t.Run(ft, func(t *testing.T) {
			router := setup(t)
			rf := filterFor(ft)

			rr := call(router, "GET", "/"+ft+"/status")
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Status for "+ft+": 200")

			rr = call(router, "POST", "/"+ft+"/set/status=418")
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Status for "+ft+" set to [418]")
			if rf.Status != 418 {
				t.Errorf("Status = %d, want 418", rf.Status)
			}

			expectBodyContains(t, call(router, "GET", "/"+ft+"/status"), "Status for "+ft+": 418")

			rr = call(router, "PUT", "/"+ft+"/set/status=202")
			expectStatus(t, rr, http.StatusOK)
			if rf.Status != 202 {
				t.Errorf("Status = %d, want 202", rf.Status)
			}

			other := ignoreFilter
			if ft == "ignore" {
				other = bypassFilter
			}
			if other.Status != http.StatusOK {
				t.Errorf("status of the other filter = %d, filters must be independent", other.Status)
			}
		})
	}
}

func TestClear(t *testing.T) {
	for _, ft := range filterTypes {
		t.Run(ft, func(t *testing.T) {
			router := setup(t)
			rf := filterFor(ft)
			expectStatus(t, call(router, "POST", "/"+ft+"/add?uri=/foo.*"), http.StatusOK)
			expectStatus(t, call(router, "POST", "/"+ft+"/add?uri=!/bar.*"), http.StatusOK)
			expectStatus(t, call(router, "POST", "/"+ft+"/add/header/foo=bar"), http.StatusOK)
			rf.applyUpdates()
			rf.FilteredCount = 5

			rr := call(router, "POST", "/"+ft+"/clear")
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Request filter "+ft+" Cleared")
			if len(rf.UriUpdates) != 0 || len(rf.NotUriUpdates) != 0 || len(rf.HeaderUpdates) != 0 {
				t.Errorf("updates not cleared: %v %v %v", rf.UriUpdates, rf.NotUriUpdates, rf.HeaderUpdates)
			}
			if rf.FilteredCount != 0 || !rf.PendingUpdates {
				t.Errorf("FilteredCount/PendingUpdates = %d/%v, want 0/true", rf.FilteredCount, rf.PendingUpdates)
			}

			// Once the cleared state is applied, nothing is filtered any more.
			foo, _ := newRequest("GET", "/foo/x", "Foo", "bar")
			if rf.matchURI(foo) || rf.matchHeaders(foo) {
				t.Error("cleared filter must not match any request")
			}
			if len(rf.Uris) != 0 || len(rf.NotUris) != 0 || len(rf.Headers) != 0 {
				t.Errorf("active filters not cleared: %v %v %v", rf.Uris, rf.NotUris, rf.Headers)
			}
		})
	}
}

func TestGetFilteredRequestCount(t *testing.T) {
	for _, ft := range filterTypes {
		t.Run(ft, func(t *testing.T) {
			router := setup(t)
			filterFor(ft).FilteredCount = 3

			rr := call(router, "GET", "/"+ft+"/count")
			expectStatus(t, rr, http.StatusOK)
			if ct := rr.Header().Get(constants.HeaderContentType); ct != constants.ContentTypeJSON {
				t.Errorf("Content-Type = %q, want %q", ct, constants.ContentTypeJSON)
			}
			result := map[string]int64{}
			if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
				t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
			}
			if result["ignoredRequests"] != 3 {
				t.Errorf("result = %v, want ignoredRequests=3", result)
			}

			expectNotRouted(t, call(router, "POST", "/"+ft+"/count"))
		})
	}
}

func TestGetRequestFilterConfig(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/foo.*"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=!/bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add/header/foo=bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/set/status=418"), http.StatusOK)

	rr := call(router, "GET", "/ignore")
	expectStatus(t, rr, http.StatusOK)
	config := map[string]interface{}{}
	if err := json.Unmarshal(rr.Body.Bytes(), &config); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if config["status"] != float64(418) || config["filteredCount"] != float64(0) || config["pendingUpdates"] != true {
		t.Errorf("status/filteredCount/pendingUpdates = %v/%v/%v, want 418/0/true",
			config["status"], config["filteredCount"], config["pendingUpdates"])
	}
	if uris, _ := config["uriUpdates"].(map[string]interface{}); uris["/foo.*"] != "(?i)^/foo.*$" {
		t.Errorf("uriUpdates = %v, want the regex for /foo.*", config["uriUpdates"])
	}
	if uris, _ := config["notUriUpdates"].(map[string]interface{}); len(uris) != 1 {
		t.Errorf("notUriUpdates = %v, want one entry", config["notUriUpdates"])
	}
	headers, _ := config["headerUpdates"].(map[string]interface{})
	if values, _ := headers["foo"].(map[string]interface{}); values["bar"] != "(?i)^bar$" {
		t.Errorf("headerUpdates = %v, want the regex for foo=bar", config["headerUpdates"])
	}
	// Updates are not active until the next request is checked.
	if uris, _ := config["uris"].(map[string]interface{}); len(uris) != 0 {
		t.Errorf("uris = %v, want none before updates are applied", config["uris"])
	}

	// The bypass filter has its own config.
	bypass := map[string]interface{}{}
	if err := json.Unmarshal(call(router, "GET", "/bypass").Body.Bytes(), &bypass); err != nil {
		t.Fatalf("bypass config is not valid JSON: %v", err)
	}
	if bypass["status"] != float64(200) {
		t.Errorf("bypass status = %v, want 200", bypass["status"])
	}
}

func TestApplyUpdates(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/foo"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=!/bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add/header/foo=bar"), http.StatusOK)

	ignoreFilter.applyUpdates()
	if len(ignoreFilter.Uris) != 1 || len(ignoreFilter.NotUris) != 1 || len(ignoreFilter.Headers["foo"]) != 1 {
		t.Errorf("applied: Uris=%v NotUris=%v Headers=%v", ignoreFilter.Uris, ignoreFilter.NotUris, ignoreFilter.Headers)
	}
	if ignoreFilter.PendingUpdates || !ignoreFilter.hasURIs {
		t.Errorf("PendingUpdates/hasURIs = %v/%v, want false/true", ignoreFilter.PendingUpdates, ignoreFilter.hasURIs)
	}

	// Active filters are copies: changing the pending updates must not change them until applied.
	expectStatus(t, call(router, "POST", "/ignore/remove?uri=/foo"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/remove/header/foo=bar"), http.StatusOK)
	if len(ignoreFilter.Uris) != 1 || len(ignoreFilter.Headers["foo"]) != 1 {
		t.Errorf("active filters changed before apply: Uris=%v Headers=%v", ignoreFilter.Uris, ignoreFilter.Headers)
	}
	ignoreFilter.applyUpdates()
	if len(ignoreFilter.Uris) != 0 || len(ignoreFilter.Headers) != 0 {
		t.Errorf("after apply: Uris=%v Headers=%v, want empty", ignoreFilter.Uris, ignoreFilter.Headers)
	}
	if len(ignoreFilter.NotUris) != 1 {
		t.Errorf("NotUris = %v, the negative filter must be kept", ignoreFilter.NotUris)
	}
}

func TestMatchURI(t *testing.T) {
	tests := []struct {
		name    string
		filters []string // values of the uri query param
		uri     string
		want    bool
	}{
		{"no filters", nil, "/foo", false},
		{"positive match", []string{"/foo.*"}, "/foo/bar", true},
		{"positive match with query", []string{"/foo.*"}, "/foo?x=1", true},
		{"positive match is case insensitive", []string{"/foo.*"}, "/FOO/Bar", true},
		{"positive mismatch", []string{"/foo.*"}, "/bar", false},
		{"positive must match whole uri", []string{"/foo"}, "/foo/bar", false},
		{"any positive filter is enough", []string{"/foo.*", "/bar.*"}, "/bar/1", true},
		{"root matches slash", []string{"/"}, "/", true},
		{"root does not match other uris", []string{"/"}, "/foo", false},
		{"negative filter spares matching uri", []string{"!/foo.*"}, "/foo/bar", false},
		{"negative filter filters other uri", []string{"!/foo.*"}, "/bar", true},
		{"uri matching any negative filter is spared", []string{"!/foo.*", "!/bar.*"}, "/bar", false},
		{"uri matching no negative filter is filtered", []string{"!/foo.*", "!/bar.*"}, "/baz", true},
		{"positive filter wins over negative", []string{"/a", "!/a"}, "/a", true},
		{"negative applies when positive does not match", []string{"/a", "!/b"}, "/c", true},
		{"negative spares when positive does not match", []string{"/a", "!/b"}, "/b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := setup(t)
			for _, f := range tt.filters {
				expectStatus(t, call(router, "POST", "/ignore/add?uri="+f), http.StatusOK)
			}
			req, _ := newRequest("GET", tt.uri)
			if got := ignoreFilter.matchURI(req); got != tt.want {
				t.Errorf("matchURI(%q) with filters %v = %v, want %v", tt.uri, tt.filters, got, tt.want)
			}
			wantCount := int64(0)
			if tt.want {
				wantCount = 1
			}
			if ignoreFilter.FilteredCount != wantCount {
				t.Errorf("FilteredCount = %d, want %d", ignoreFilter.FilteredCount, wantCount)
			}
			if ignoreFilter.PendingUpdates && len(tt.filters) > 0 {
				t.Error("matching must apply the pending updates")
			}
		})
	}
}

func TestMatchURIRootWithEmptyRequestURI(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/"), http.StatusOK)
	req, _ := newRequest("GET", "/")
	req.RequestURI = ""
	if !ignoreFilter.matchURI(req) {
		t.Error("root filter must match a request with an empty URI")
	}
}

func TestMatchHeaders(t *testing.T) {
	tests := []struct {
		name    string
		filters []string // header filters, in the form of the path after /add/header/
		headers []string // name, value pairs
		want    bool
	}{
		{"no filters", nil, []string{"Foo", "bar"}, false},
		{"header name only matches any value", []string{"foo"}, []string{"Foo", "anything"}, true},
		{"header name only needs the header", []string{"foo"}, []string{"Other", "x"}, false},
		{"exact value", []string{"foo=bar"}, []string{"Foo", "bar"}, true},
		{"exact value is case insensitive", []string{"foo=bar"}, []string{"Foo", "BAR"}, true},
		{"exact value mismatch", []string{"foo=bar"}, []string{"Foo", "barx"}, false},
		{"glob value", []string{"foo=bar*"}, []string{"Foo", "barbaz"}, true},
		{"glob value mismatch", []string{"foo=bar*"}, []string{"Foo", "xbar"}, false},
		{"header name is case insensitive", []string{"FOO=bar"}, []string{"fOO", "bar"}, true},
		{"any value of a multi value header", []string{"foo=bar"}, []string{"Foo", "x", "Foo", "bar"}, true},
		{"any configured value", []string{"foo=a", "foo=b"}, []string{"Foo", "b"}, true},
		{"any configured header", []string{"foo=a", "qux=b"}, []string{"Qux", "b"}, true},
		{"no headers on request", []string{"foo=bar"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := setup(t)
			for _, f := range tt.filters {
				expectStatus(t, call(router, "POST", "/ignore/add/header/"+f), http.StatusOK)
			}
			req, _ := newRequest("GET", "/foo", tt.headers...)
			if got := ignoreFilter.matchHeaders(req); got != tt.want {
				t.Errorf("matchHeaders(%v) with filters %v = %v, want %v", tt.headers, tt.filters, got, tt.want)
			}
			wantCount := int64(0)
			if tt.want {
				wantCount = 1
			}
			if ignoreFilter.FilteredCount != wantCount {
				t.Errorf("FilteredCount = %d, want %d", ignoreFilter.FilteredCount, wantCount)
			}
		})
	}
}

func TestMatchHeadersAfterRemoval(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add/header/foo=bar"), http.StatusOK)
	req, _ := newRequest("GET", "/x", "Foo", "bar")
	if !ignoreFilter.matchHeaders(req) {
		t.Fatal("header filter must match before removal")
	}
	expectStatus(t, call(router, "POST", "/ignore/remove/header/foo=bar"), http.StatusOK)
	if ignoreFilter.matchHeaders(req) {
		t.Error("header filter must not match after removal")
	}
}

// serve runs the filter middleware in front of a handler that records whether it was reached.
func serve(next http.Handler, method, target string, headers ...string) (rr *httptest.ResponseRecorder, rs *util.RequestStore) {
	var req *http.Request
	req, rs = newRequest(method, target, headers...)
	rr = httptest.NewRecorder()
	Middleware.MiddlewareHandler(next).ServeHTTP(rr, req)
	return rr, rs
}

func recordingHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusAccepted)
	})
}

func TestMiddlewareFiltersMatchingRequest(t *testing.T) {
	for _, ft := range filterTypes {
		t.Run(ft+"/uri", func(t *testing.T) {
			router := setup(t)
			expectStatus(t, call(router, "POST", "/"+ft+"/add?uri=/foo.*"), http.StatusOK)
			expectStatus(t, call(router, "POST", "/"+ft+"/set/status=418"), http.StatusOK)

			reached := false
			rr, rs := serve(recordingHandler(&reached), "GET", "/foo/bar")
			if reached {
				t.Error("filtered request must not reach the next handler")
			}
			expectStatus(t, rr, 418)
			if got := rr.Header().Get(constants.HeaderGotoFilteredRequest); got != "true" {
				t.Errorf("%s = %q, want true", constants.HeaderGotoFilteredRequest, got)
			}
			if !rs.IsFilteredRequest {
				t.Error("request store must be marked as filtered")
			}
			if filterFor(ft).FilteredCount != 1 {
				t.Errorf("FilteredCount = %d, want 1", filterFor(ft).FilteredCount)
			}
		})
		t.Run(ft+"/header", func(t *testing.T) {
			router := setup(t)
			expectStatus(t, call(router, "POST", "/"+ft+"/add/header/foo=bar*"), http.StatusOK)

			reached := false
			rr, rs := serve(recordingHandler(&reached), "GET", "/anything", "Foo", "barbaz")
			if reached {
				t.Error("filtered request must not reach the next handler")
			}
			expectStatus(t, rr, http.StatusOK) // the default filter status
			if rr.Header().Get(constants.HeaderGotoFilteredRequest) != "true" || !rs.IsFilteredRequest {
				t.Error("request must be reported as filtered")
			}
		})
	}
}

func TestMiddlewarePassesThroughUnmatchedRequest(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/foo.*"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/bypass/add/header/foo=bar"), http.StatusOK)

	reached := false
	rr, rs := serve(recordingHandler(&reached), "GET", "/other", "Foo", "nomatch")
	if !reached {
		t.Fatal("unmatched request must reach the next handler")
	}
	expectStatus(t, rr, http.StatusAccepted)
	if rr.Header().Get(constants.HeaderGotoFilteredRequest) != "" || rs.IsFilteredRequest {
		t.Error("unmatched request must not be reported as filtered")
	}
}

func TestMiddlewareWithoutFilters(t *testing.T) {
	setup(t)
	reached := false
	rr, _ := serve(recordingHandler(&reached), "GET", "/foo")
	if !reached {
		t.Error("request must reach the next handler when nothing is configured")
	}
	expectStatus(t, rr, http.StatusAccepted)
}

func TestMiddlewareFilterPrecedence(t *testing.T) {
	// Order of evaluation: ignore URI, bypass URI, ignore headers, bypass headers.
	tests := []struct {
		name       string
		ignoreURI  string
		bypassURI  string
		ignoreHdr  string
		bypassHdr  string
		wantStatus int
	}{
		{"ignore uri over bypass uri", "/foo.*", "/foo.*", "", "", 418},
		{"bypass uri over ignore header", "", "/foo.*", "foo=bar", "", 202},
		{"ignore header over bypass header", "", "", "foo=bar", "foo=bar", 418},
		{"bypass header when only it matches", "", "", "foo=nomatch", "foo=bar", 202},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := setup(t)
			add := func(ft, uri, header string) {
				if uri != "" {
					expectStatus(t, call(router, "POST", "/"+ft+"/add?uri="+uri), http.StatusOK)
				}
				if header != "" {
					expectStatus(t, call(router, "POST", "/"+ft+"/add/header/"+header), http.StatusOK)
				}
			}
			add("ignore", tt.ignoreURI, tt.ignoreHdr)
			add("bypass", tt.bypassURI, tt.bypassHdr)
			expectStatus(t, call(router, "POST", "/ignore/set/status=418"), http.StatusOK)
			expectStatus(t, call(router, "POST", "/bypass/set/status=202"), http.StatusOK)

			reached := false
			rr, _ := serve(recordingHandler(&reached), "GET", "/foo/x", "Foo", "bar")
			if reached {
				t.Error("request must be filtered")
			}
			expectStatus(t, rr, tt.wantStatus)
		})
	}
}

func TestMiddlewareSkipsKnownNonTraffic(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=.*"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/set/status=418"), http.StatusOK)

	reached := false
	rr, rs := serve(recordingHandler(&reached), "GET", "/server/request/ignore")
	if !rs.IsKnownNonTraffic {
		t.Fatal("admin uri must be treated as non traffic")
	}
	if !reached {
		t.Error("non traffic requests must never be filtered")
	}
	expectStatus(t, rr, http.StatusAccepted)
	if ignoreFilter.FilteredCount != 0 {
		t.Errorf("FilteredCount = %d, want 0", ignoreFilter.FilteredCount)
	}
}

func TestMiddlewareWithNilNext(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/foo"), http.StatusOK)

	// Neither a filtered nor an unfiltered request may panic when there is no next handler.
	rr, _ := serve(nil, "GET", "/foo")
	expectStatus(t, rr, http.StatusOK)
	rr, _ = serve(nil, "GET", "/bar")
	expectStatus(t, rr, http.StatusOK)
}

func TestFilterRequest(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/bypass/add?uri=/foo"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/bypass/set/status=503"), http.StatusOK)

	req, rs := newRequest("GET", "/foo")
	rr := httptest.NewRecorder()
	if !filterRequest(rr, req) {
		t.Fatal("filterRequest = false, want true for a matching request")
	}
	expectStatus(t, rr, 503)
	if !rs.IsFilteredRequest {
		t.Error("request store must be marked as filtered")
	}

	req, rs = newRequest("GET", "/bar")
	rr = httptest.NewRecorder()
	if filterRequest(rr, req) {
		t.Error("filterRequest = true, want false for a non matching request")
	}
	if rs.IsFilteredRequest || rr.Header().Get(constants.HeaderGotoFilteredRequest) != "" {
		t.Error("non matching request must not be marked as filtered")
	}
}
