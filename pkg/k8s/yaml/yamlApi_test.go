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

package ctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/k8s/k8stest"
	"goto/pkg/k8s/store"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	stubOnce   sync.Once
	routesOnce sync.Once
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// router is the router that serves the APIs of the yaml of k8s, which the middleware package keeps for the path.
func router() http.Handler {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/k8s"]
}

// setup makes the default client of k8s talk to an API server of the test, and leaves the stored yaml and the cache
// empty before and after the test.
func setup(t *testing.T) *k8stest.API {
	t.Helper()
	api := k8stest.Install(t)
	store.Kstore.Clear("", "")
	store.Cache.Clear()
	t.Cleanup(func() {
		store.Kstore.Clear("", "")
		store.Cache.Clear()
	})
	return api
}

func call(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	h := router()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		h.ServeHTTP(rr, req)
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s %s panicked: %v", method, target, p)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("%s %s did not return in time", method, target)
	}
	return rr
}

func wantStatus(t *testing.T, rr *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, status, rr.Body.String())
	}
}

func wantBodyHas(t *testing.T, rr *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(rr.Body.String(), w) {
			t.Errorf("body has no %q:\n%s", w, rr.Body.String())
		}
	}
}

// twoNamespaces is a yaml of a namespace, and config maps and a service in it and in another namespace.
const twoNamespaces = `apiVersion: v1
kind: Namespace
metadata:
  name: yns-a
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-one
  namespace: yns-a
data:
  key: one
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-two
  namespace: yns-a
data:
  key: two
---
apiVersion: v1
kind: Service
metadata:
  name: svc-one
  namespace: yns-b
spec:
  ports:
  - port: 80
`

func store1(t *testing.T, name, body string) {
	t.Helper()
	rr := call(t, "POST", "/k8s/yaml/store/"+name, body)
	wantStatus(t, rr, 200)
	if !strings.HasPrefix(rr.Body.String(), "Stored ") {
		t.Fatalf("the yaml was not stored: %s", rr.Body.String())
	}
}

func jsonOf(t *testing.T, rr *httptest.ResponseRecorder) map[string]map[string]map[string]map[string]interface{} {
	t.Helper()
	var got map[string]map[string]map[string]map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not the JSON of stored resources: %v\n%s", err, rr.Body.String())
	}
	return got
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "k8s" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler != nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}

func TestStoreYamlKeepsTheResourcesByNamespaceKindAndName(t *testing.T) {
	setup(t)
	rr := call(t, "POST", "/k8s/yaml/store/stored", twoNamespaces)
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Stored ", "yns-a", "yns-b", "cm-one", "cm-two", "svc-one")
	rr = call(t, "GET", "/k8s/yaml/stored", "", "Accept", "application/json")
	wantStatus(t, rr, 200)
	got := jsonOf(t, rr)
	// A namespace is kept in the namespace that it is.
	if got["yns-a"]["Namespace"]["yns-a"] == nil || got["yns-a"]["ConfigMap"]["cm-one"] == nil || got["yns-a"]["ConfigMap"]["cm-two"] == nil {
		t.Errorf("namespace yns-a = %v", got["yns-a"])
	}
	if got["yns-b"]["Service"]["svc-one"] == nil {
		t.Errorf("namespace yns-b = %v", got["yns-b"])
	}
	if len(got) != 2 {
		t.Errorf("namespaces = %v, want 2", got)
	}
	// A resource has its object, kind, namespace and name.
	cm := got["yns-a"]["ConfigMap"]["cm-one"]
	if cm["name"] != "cm-one" || cm["namespace"] != "yns-a" || cm["resource"] == nil || cm["gvk"] == nil {
		t.Errorf("resource = %v", cm)
	}
}

func TestStoreYamlOfTheSameNameReplacesTheResources(t *testing.T) {
	setup(t)
	store1(t, "again", twoNamespaces)
	store1(t, "again", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm-one\n  namespace: yns-a\ndata:\n  key: changed\n")
	rr := call(t, "GET", "/k8s/yaml/again/yns-a/ConfigMap", "", "Accept", "application/json")
	wantStatus(t, rr, 200)
	var got map[string]map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body = %s: %v", rr.Body.String(), err)
	}
	if len(got) != 2 {
		t.Errorf("resources = %v, want the first two config maps, as a yaml adds to what is stored", got)
	}
	if !strings.Contains(rr.Body.String(), "changed") {
		t.Errorf("body = %s, want the config map to have been replaced", rr.Body.String())
	}
}

func TestStoreYamlThatIsNotValidIsReported(t *testing.T) {
	setup(t)
	for name, body := range map[string]string{
		"empty":   "",
		"scalar":  "just text",
		"nokind":  "metadata:\n  name: x\n",
		"badyaml": "kind: [unclosed",
	} {
		rr := call(t, "POST", "/k8s/yaml/store/"+name, body)
		wantBodyHas(t, rr, "Failed to store resources from Yaml")
		if strings.HasPrefix(rr.Body.String(), "Stored") {
			t.Errorf("%s: the yaml was reported as stored: %s", name, rr.Body.String())
		}
		if got := store.Kstore.Get(name, "", ""); got != nil {
			t.Errorf("%s: resources were stored: %v", name, got)
		}
	}
}

func TestGetResourcesNarrowsDownByNamespaceAndKind(t *testing.T) {
	setup(t)
	store1(t, "narrow", twoNamespaces)
	rr := call(t, "GET", "/k8s/yaml/narrow/yns-a", "", "Accept", "application/json")
	wantStatus(t, rr, 200)
	var kinds map[string]map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &kinds); err != nil {
		t.Fatalf("body = %s: %v", rr.Body.String(), err)
	}
	if kinds["ConfigMap"] == nil || kinds["Namespace"] == nil || len(kinds) != 2 {
		t.Errorf("kinds = %v, want the kinds of the namespace", kinds)
	}
	for _, kind := range []string{"ConfigMap", "cm", "configmaps"} {
		rr = call(t, "GET", "/k8s/yaml/narrow/yns-a/"+kind, "", "Accept", "application/json")
		wantStatus(t, rr, 200)
		var names map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &names); err != nil {
			t.Fatalf("%s: body = %s: %v", kind, rr.Body.String(), err)
		}
		if names["cm-one"] == nil || names["cm-two"] == nil || len(names) != 2 {
			t.Errorf("%s: resources = %v, want the 2 config maps", kind, names)
		}
	}
}

func TestGetResourcesIsYAMLByDefault(t *testing.T) {
	setup(t)
	store1(t, "asyaml", twoNamespaces)
	rr := call(t, "GET", "/k8s/yaml/asyaml/yns-b/Service", "")
	wantStatus(t, rr, 200)
	if strings.HasPrefix(strings.TrimSpace(rr.Body.String()), "{") {
		t.Errorf("body is JSON, want YAML:\n%s", rr.Body.String())
	}
	wantBodyHas(t, rr, "svc-one", "name:")
}

func TestGetResourcesNotFound(t *testing.T) {
	setup(t)
	store1(t, "known", twoNamespaces)
	for _, target := range []string{"/k8s/yaml", "/k8s/yaml/unknown", "/k8s/yaml/known/no-namespace", "/k8s/yaml/unknown/yns-a"} {
		rr := call(t, "GET", target, "")
		if !strings.Contains(rr.Body.String(), "No resources found for yamlName") {
			t.Errorf("GET %s = %d %q, want the report that there are no resources", target, rr.Code, rr.Body.String())
		}
		// The status was written after the body, which makes it the 200 of the body.
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, rr.Code)
		}
	}
	rr := call(t, "GET", "/k8s/yaml/known/yns-a", "")
	wantStatus(t, rr, 200)
}

func TestClearResources(t *testing.T) {
	setup(t)
	store1(t, "clear-a", twoNamespaces)
	store1(t, "clear-b", twoNamespaces)
	// A namespace of a yaml.
	rr := call(t, "POST", "/k8s/yaml/clear/clear-a/yns-a", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "Cleared resources\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
	if store.Kstore.Get("clear-a", "yns-a", "") != nil || store.Kstore.Get("clear-a", "yns-b", "") == nil || store.Kstore.Get("clear-b", "yns-a", "") == nil {
		t.Error("clearing a namespace of a yaml took out something else, or not that")
	}
	// A yaml.
	wantStatus(t, call(t, "POST", "/k8s/yaml/clear/clear-a", ""), 200)
	if store.Kstore.Get("clear-a", "", "") != nil || store.Kstore.Get("clear-b", "", "") == nil {
		t.Error("clearing a yaml took out something else, or not that")
	}
	// Everything.
	wantStatus(t, call(t, "POST", "/k8s/yaml/clear", ""), 200)
	if store.Kstore.Get("clear-b", "", "") != nil {
		t.Error("clearing all left a yaml")
	}
	// What is not there is not an error.
	wantStatus(t, call(t, "POST", "/k8s/yaml/clear/never/never", ""), 200)
}

func TestApplyYamlSendsTheResourcesToTheCluster(t *testing.T) {
	api := setup(t)
	store1(t, "apply", twoNamespaces)
	rr := call(t, "POST", "/k8s/yaml/apply/apply", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully applied 4 resources")
	if strings.Contains(rr.Body.String(), "Failed") {
		t.Errorf("body = %s", rr.Body.String())
	}
	for _, want := range []struct{ apiVersion, kind, namespace, name string }{
		{"v1", "Namespace", "", "yns-a"},
		{"v1", "ConfigMap", "yns-a", "cm-one"},
		{"v1", "ConfigMap", "yns-a", "cm-two"},
		{"v1", "Service", "yns-b", "svc-one"},
	} {
		if api.Get(want.apiVersion, want.kind, want.namespace, want.name) == nil {
			t.Errorf("the cluster has no %s %s/%s", want.kind, want.namespace, want.name)
		}
	}
	// Resources are applied with the content type of an apply, and the manager of the fields is goto.
	patches := 0
	for _, r := range api.Requests() {
		if r.Method != "PATCH" {
			continue
		}
		patches++
		if r.ContentType != "application/apply-patch+yaml" || !strings.Contains(r.RawQuery, "fieldManager=goto") {
			t.Errorf("request %s %s?%s has content type %q", r.Method, r.Path, r.RawQuery, r.ContentType)
		}
	}
	if patches != 4 {
		t.Errorf("patches = %d, want 4", patches)
	}
}

func TestApplyYamlOfANamespaceKindAndName(t *testing.T) {
	api := setup(t)
	store1(t, "narrow-apply", twoNamespaces)
	rr := call(t, "POST", "/k8s/yaml/apply/narrow-apply/yns-a/cm/cm-two", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully applied 1 resources")
	if api.Get("v1", "ConfigMap", "yns-a", "cm-two") == nil || api.Get("v1", "ConfigMap", "yns-a", "cm-one") != nil {
		t.Error("only the config map that was asked for must be applied")
	}
	api.ClearRequests()
	rr = call(t, "POST", "/k8s/yaml/apply/narrow-apply/yns-a/ConfigMap", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully applied 2 resources")
	rr = call(t, "POST", "/k8s/yaml/apply/narrow-apply/yns-b", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully applied 1 resources")
	if api.Get("v1", "Service", "yns-b", "svc-one") == nil {
		t.Error("the service of the namespace was not applied")
	}
}

func TestApplyYamlToAnotherNamespace(t *testing.T) {
	api := setup(t)
	store1(t, "move", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: moved\n  namespace: yns-from\ndata:\n  k: v\n")
	rr := call(t, "POST", "/k8s/yaml/apply/move/to?ns=yns-to", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully applied 1 resources", "yns-to")
	obj := api.Get("v1", "ConfigMap", "yns-to", "moved")
	if obj == nil {
		t.Fatal("the config map is not in the namespace that it was moved to")
	}
	if ns, _ := obj["metadata"].(map[string]interface{})["namespace"].(string); ns != "yns-to" {
		t.Errorf("namespace of the applied object = %q, want yns-to", ns)
	}
	if api.Get("v1", "ConfigMap", "yns-from", "moved") != nil {
		t.Error("the config map was applied to the namespace that it was stored for")
	}
	// What is stored is not changed by moving it.
	rr = call(t, "GET", "/k8s/yaml/move/yns-from/ConfigMap", "", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"namespace":"yns-from"`) && !strings.Contains(rr.Body.String(), `"namespace": "yns-from"`) {
		t.Errorf("stored resource = %s, want it to be still in its namespace", rr.Body.String())
	}
}

func TestDeleteYamlRemovesTheResourcesFromTheCluster(t *testing.T) {
	api := setup(t)
	store1(t, "del", twoNamespaces)
	wantStatus(t, call(t, "POST", "/k8s/yaml/apply/del", ""), 200)
	rr := call(t, "POST", "/k8s/yaml/delete/del/yns-a/ConfigMap/cm-one", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully deleted 1 resources")
	if api.Get("v1", "ConfigMap", "yns-a", "cm-one") != nil || api.Get("v1", "ConfigMap", "yns-a", "cm-two") == nil {
		t.Error("only the config map that was asked for must be deleted")
	}
	rr = call(t, "POST", "/k8s/yaml/delete/del/yns-b", "")
	wantStatus(t, rr, 200)
	wantBodyHas(t, rr, "Successfully deleted 1 resources")
	if api.Get("v1", "Service", "yns-b", "svc-one") != nil {
		t.Error("the service of the namespace was not deleted")
	}
	// What is gone from the cluster fails to delete again.
	rr = call(t, "POST", "/k8s/yaml/delete/del/yns-b", "")
	wantBodyHas(t, rr, "Failed to apply 1 resources")
	if strings.Contains(rr.Body.String(), "Successfully") {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestApplyYamlThatIsNotStoredIsReported(t *testing.T) {
	api := setup(t)
	store1(t, "stored-for-apply", twoNamespaces)
	for _, target := range []string{
		"/k8s/yaml/apply/not-stored",
		"/k8s/yaml/apply/stored-for-apply/no-such-namespace",
		"/k8s/yaml/apply/stored-for-apply/yns-a/Service",
		"/k8s/yaml/delete/not-stored/yns-a",
	} {
		rr := call(t, "POST", target, "")
		wantBodyHas(t, rr, "Failed to apply 1 resources", "no resources found")
		if strings.Contains(rr.Body.String(), "Successfully") {
			t.Errorf("POST %s = %s", target, rr.Body.String())
		}
	}
	if got := api.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none for resources that are not there", got)
	}
}

func TestApplyYamlWhenTheClusterFailsReportsEveryFailure(t *testing.T) {
	api := setup(t)
	store1(t, "failing", twoNamespaces)
	api.FailResources(500)
	rr := call(t, "POST", "/k8s/yaml/apply/failing", "")
	wantBodyHas(t, rr, "Failed to apply 4 resources")
	if strings.Contains(rr.Body.String(), "Successfully") {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestMethodsOfTheRoutesAreChecked(t *testing.T) {
	setup(t)
	for _, tt := range []struct{ method, target string }{
		{"GET", "/k8s/yaml/store/x"},
		{"GET", "/k8s/yaml/apply/x"},
		{"GET", "/k8s/yaml/clear"},
		{"POST", "/k8s/yaml/x/ns/kind"},
		{"DELETE", "/k8s/yaml/x"},
	} {
		if rr := call(t, tt.method, tt.target, ""); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want it to be refused", tt.method, tt.target, rr.Code)
		}
	}
}
