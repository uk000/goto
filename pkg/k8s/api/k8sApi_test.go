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

package k8sApi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	k8sClient "goto/pkg/k8s/client"
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

// router is the router that serves the APIs of k8s, which the middleware package keeps for the /k8s path.
func router() http.Handler {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/k8s"]
}

// setup makes the default client of k8s talk to an API server of the test, and leaves the cache, the clients and the
// current client as they were when the test is done.
func setup(t *testing.T) *k8stest.API {
	t.Helper()
	api := k8stest.Install(t)
	store.Cache.Clear()
	oldCurrent := k8sClient.CurrentClient
	oldClients := map[string]*k8sClient.K8sClient{}
	for name, c := range k8sClient.Clients {
		oldClients[name] = c
	}
	t.Cleanup(func() {
		store.Cache.Clear()
		k8sClient.CurrentClient = oldCurrent
		for name := range k8sClient.Clients {
			if oldClients[name] == nil {
				delete(k8sClient.Clients, name)
			}
		}
		for name, c := range oldClients {
			k8sClient.Clients[name] = c
		}
	})
	return api
}

func call(t *testing.T, method, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	h := router()
	req := httptest.NewRequest(method, target, nil)
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

func configMap(namespace, name string, data map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace},
		"data":     data,
	}
}

// caData is a CA certificate as the API takes it, which is the PEM text encoded in base 64. It has no characters that
// can't be in a segment of a path.
func caData(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "k8s-api-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var got map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rr.Body.String())
	}
	return got
}

func metadataName(obj map[string]interface{}) string {
	meta, _ := obj["metadata"].(map[string]interface{})
	name, _ := meta["name"].(string)
	return name
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "k8s" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler != nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}

func TestGetResourceIsYAMLByDefault(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "yaml-cm", map[string]interface{}{"key": "value"}))
	rr := call(t, "GET", "/k8s/resources/configmaps/api-ns/yaml-cm")
	wantStatus(t, rr, 200)
	for _, want := range []string{"kind: ConfigMap", "name: yaml-cm", "namespace: api-ns", "key: value"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body has no %q:\n%s", want, rr.Body.String())
		}
	}
}

func TestGetResourceIsJSONWhenAccepted(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "json-cm", map[string]interface{}{"key": "value"}))
	rr := call(t, "GET", "/k8s/resources/configmaps/api-ns/json-cm", "Accept", "application/json")
	wantStatus(t, rr, 200)
	got := decode(t, rr)
	if got["kind"] != "ConfigMap" || metadataName(got) != "json-cm" {
		t.Errorf("resource = %v", got)
	}
}

func TestGetResourceByShortNameAndKind(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "short-cm", nil))
	for _, kind := range []string{"configmaps", "cm", "ConfigMap", "configmap"} {
		store.Cache.Clear()
		rr := call(t, "GET", "/k8s/resources/"+kind+"/api-ns/short-cm", "Accept", "application/json")
		wantStatus(t, rr, 200)
		if got := metadataName(decode(t, rr)); got != "short-cm" {
			t.Errorf("%s: name = %q", kind, got)
		}
	}
}

func TestGetClusterScopedResourceByName(t *testing.T) {
	api := setup(t)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": "api-namespace"}})
	rr := call(t, "GET", "/k8s/resources/namespaces/api-namespace", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if got := decode(t, rr); got["kind"] != "Namespace" || metadataName(got) != "api-namespace" {
		t.Errorf("resource = %v", got)
	}
}

func TestListResourcesOfANamespace(t *testing.T) {
	api := setup(t)
	api.Put(configMap("list-ns-a", "one", nil))
	api.Put(configMap("list-ns-a", "two", nil))
	api.Put(configMap("list-ns-b", "other", nil))
	rr := call(t, "GET", "/k8s/resources/configmaps/list-ns-a/all", "Accept", "application/json")
	wantStatus(t, rr, 200)
	items, _ := decode(t, rr)["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("items = %v, want the 2 config maps of the namespace", items)
	}
	names := map[string]bool{}
	for _, item := range items {
		names[metadataName(item.(map[string]interface{}))] = true
	}
	if !names["one"] || !names["two"] {
		t.Errorf("names = %v", names)
	}
}

func TestListResourcesOfAllNamespaces(t *testing.T) {
	api := setup(t)
	api.Put(configMap("all-ns-a", "one", nil))
	api.Put(configMap("all-ns-b", "two", nil))
	rr := call(t, "GET", "/k8s/resources/configmaps", "Accept", "application/json")
	wantStatus(t, rr, 200)
	items, _ := decode(t, rr)["items"].([]interface{})
	if len(items) != 2 {
		t.Errorf("items = %v, want the config maps of both namespaces", items)
	}
}

func TestGetResourceWithQueries(t *testing.T) {
	api := setup(t)
	api.Put(configMap("query-ns", "query-cm", map[string]interface{}{"key": "value"}))
	rr := call(t, "GET", "/k8s/resources/configmaps/query-ns/query-cm?jq=picked=.data.key", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if got := decode(t, rr); len(got) != 1 || got["picked"] != "value" {
		t.Errorf("jq result = %v", got)
	}
	store.Cache.Clear()
	rr = call(t, "GET", "/k8s/resources/configmaps/query-ns/query-cm?jp=picked={.data.key}", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if got := decode(t, rr); len(got) != 1 || !strings.Contains(fmt.Sprint(got["picked"]), "value") {
		t.Errorf("jp result = %v", got)
	}
}

func TestGetResourceNotFound(t *testing.T) {
	api := setup(t)
	api.Put(configMap("missing-ns", "exists", nil))
	wantStatus(t, call(t, "GET", "/k8s/resources/configmaps/missing-ns/nothing"), 404)
	// A name without a namespace is not a name of a namespaced resource.
	wantStatus(t, call(t, "GET", "/k8s/resources/configmaps/exists"), 404)
}

func TestGetResourceOfUnknownKindIsBadRequest(t *testing.T) {
	setup(t)
	rr := call(t, "GET", "/k8s/resources/nosuchkind/ns/name")
	wantStatus(t, rr, 400)
	rr = call(t, "GET", "/k8s/resources/nosuchkind")
	wantStatus(t, rr, 400)
}

func TestGetResourceWhenTheServerFailsIsBadRequest(t *testing.T) {
	api := setup(t)
	api.Put(configMap("fail-ns", "fail-cm", nil))
	api.FailResources(500)
	wantStatus(t, call(t, "GET", "/k8s/resources/configmaps/fail-ns/fail-cm"), 400)
	api.FailResources(0)
	wantStatus(t, call(t, "GET", "/k8s/resources/configmaps/fail-ns/fail-cm"), 200)
}

func TestResourcesAreServedFromTheCacheUntilItIsCleared(t *testing.T) {
	api := setup(t)
	api.Put(configMap("cache-ns", "cache-cm", map[string]interface{}{"v": "1"}))
	wantStatus(t, call(t, "GET", "/k8s/resources/configmaps/cache-ns/cache-cm"), 200)
	first := len(api.Requests())
	if first == 0 {
		t.Fatal("the first request did not reach the API server")
	}
	// What the server has changes, which the cache does not know of.
	api.Put(configMap("cache-ns", "cache-cm", map[string]interface{}{"v": "2"}))
	rr := call(t, "GET", "/k8s/resources/configmaps/cache-ns/cache-cm", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if got := len(api.Requests()); got != first {
		t.Errorf("requests = %d, want %d: the second one is served from the cache", got, first)
	}
	if data, _ := decode(t, rr)["data"].(map[string]interface{}); data["v"] != "1" {
		t.Errorf("data = %v, want the cached one", data)
	}
	rr = call(t, "POST", "/k8s/clear")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "K8s Cache Cleared\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
	rr = call(t, "GET", "/k8s/resources/configmaps/cache-ns/cache-cm", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if data, _ := decode(t, rr)["data"].(map[string]interface{}); data["v"] != "2" {
		t.Errorf("data = %v, want the one of the server after the cache was cleared", data)
	}
}

func TestConfigClusterAddsAClient(t *testing.T) {
	setup(t)
	ca := caData(t)
	rr := call(t, "POST", "/k8s/config/api-cluster-1/127.0.0.1:1/"+ca)
	wantStatus(t, rr, 200)
	if want := fmt.Sprintf("Client configured successfully for K8s config [api-cluster-1, 127.0.0.1:1, %s]\n", ca); rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
	c := k8sClient.GetClient("api-cluster-1")
	if c == nil || c.Config == nil || c.Config.Host != "127.0.0.1:1" {
		t.Fatalf("client = %+v", c)
	}
	if !strings.Contains(string(c.Config.TLSClientConfig.CAData), "BEGIN CERTIFICATE") {
		t.Errorf("CA data = %q, want the decoded PEM", c.Config.TLSClientConfig.CAData)
	}
}

func TestConfigClusterWithACertThatIsNotOneIsBadRequest(t *testing.T) {
	setup(t)
	rr := call(t, "POST", "/k8s/config/api-cluster-bad/127.0.0.1:1/Y2E=")
	wantStatus(t, rr, 400)
	if want := "Failed to create client for K8s config [api-cluster-bad, 127.0.0.1:1, Y2E=]\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
	if k8sClient.GetClient("api-cluster-bad") != nil {
		t.Error("a client was added for a config that failed")
	}
}

func TestConfigClusterRoutesNeedAllTheParts(t *testing.T) {
	setup(t)
	for _, target := range []string{"/k8s/config/only-name", "/k8s/config/name/127.0.0.1:1"} {
		if rr := call(t, "POST", target); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want it to be refused", target, rr.Code)
		}
	}
	if rr := call(t, "GET", "/k8s/config/a/127.0.0.1:1/Y2E="); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want it to be refused", rr.Code)
	}
}

func TestSetContextSwitchesTheCurrentClient(t *testing.T) {
	setup(t)
	wantStatus(t, call(t, "POST", "/k8s/config/api-cluster-2/127.0.0.1:2/"+caData(t)), 200)
	rr := call(t, "POST", "/k8s/context/api-cluster-2")
	wantStatus(t, rr, 200)
	if want := "Current Context swtiched to [api-cluster-2]\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
	if k8sClient.CurrentClient == nil || k8sClient.CurrentClient != k8sClient.GetClient("api-cluster-2") {
		t.Errorf("current client = %+v", k8sClient.CurrentClient)
	}
}

func TestSetContextOfUnknownNameIsBadRequest(t *testing.T) {
	setup(t)
	before := k8sClient.CurrentClient
	rr := call(t, "POST", "/k8s/context/no-such-context")
	wantStatus(t, rr, 400)
	if rr.Body.String() != "Context [no-such-context] not found.\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
	if k8sClient.CurrentClient != before {
		t.Error("the current client was changed for a context that is not there")
	}
}

func TestMethodsOfTheRoutesAreChecked(t *testing.T) {
	setup(t)
	for _, tt := range []struct{ method, target string }{
		{"GET", "/k8s/clear"},
		{"GET", "/k8s/context/x"},
		{"POST", "/k8s/resources/configmaps/ns/name"},
		{"DELETE", "/k8s/resources/configmaps"},
	} {
		if rr := call(t, tt.method, tt.target); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want it to be refused", tt.method, tt.target, rr.Code)
		}
	}
}
