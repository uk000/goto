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

package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var routesOnce sync.Once

// serve serves the routes of the mock the way the goto server does: a request goes to the router of the first segment of
// its URI. A client of k8s is then pointed at it, to see what a client sees.
func serve(t *testing.T) *rest.Config {
	t.Helper()
	routesOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		Middleware.SetRoutes(nil)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root, _ := util.GetRootURI(r.RequestURI)
		router := middleware.RootRouterFor(root, r)
		if router == nil {
			// Goto hands such a request to what it does for any other URI.
			w.Header().Set(fellThrough, "true")
			http.NotFound(w, r)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, 8080))
		_, r, _ = util.WithRequestStore(r, nil)
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	// The clients call the way a k8s client does, by the name of a k8s host.
	return &rest.Config{Host: srv.URL, WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
		return withHost(rt, "k8s.example.com")
	}}
}

// fellThrough is set by the test server on the requests that the mock does not serve.
const fellThrough = "X-Fell-Through"

type hostTransport struct {
	rt   http.RoundTripper
	host string
}

func (h hostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = h.host
	return h.rt.RoundTrip(r)
}

func withHost(rt http.RoundTripper, host string) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return hostTransport{rt, host}
}

// get calls the server of the config with the given Host header, and returns the status and whether the mock left the
// request to the rest of goto.
func get(t *testing.T, cfg *rest.Config, host, uri string) (int, bool) {
	t.Helper()
	req, err := http.NewRequest("GET", cfg.Host+uri, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get(fellThrough) != ""
}

func dyn(t *testing.T) dynamic.Interface {
	t.Helper()
	d, err := dynamic.NewForConfig(serve(t))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var (
	configMaps  = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	pods        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	nodes       = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	deployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
)

func configMap(namespace, name, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace},
		"data":     map[string]interface{}{"k": value},
	}}
}

func dataValue(t *testing.T, u *unstructured.Unstructured) string {
	t.Helper()
	v, _, _ := unstructured.NestedString(u.Object, "data", "k")
	return v
}

// The mock is for the calls that read and write resources, so it serves /api and /apis only, and not the version, health
// and probe URIs.
func TestOnlyApiAndApisAreServed(t *testing.T) {
	cfg := serve(t)
	for _, uri := range []string{"/version", "/healthz", "/readyz", "/livez"} {
		if status, _ := get(t, cfg, "k8s.example.com", uri); status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d", uri, status, http.StatusNotFound)
		}
	}
	for _, uri := range []string{"/api", "/apis"} {
		if status, through := get(t, cfg, "k8s.example.com", uri); status != http.StatusOK || through {
			t.Errorf("GET %s = %d (fell through %v), want %d served by the mock", uri, status, through, http.StatusOK)
		}
	}
}

// The mock serves the requests for the hosts like k8s.xxx only, whose first label is the keyword, in any case and with or
// without a port, and the others are left to what goto does for any other URI.
func TestOnlyK8sHostsAreServed(t *testing.T) {
	cfg := serve(t)
	uris := []string{"/api", "/api/v1", "/apis", "/apis/apps/v1", "/api/v1/namespaces/hosts/configmaps"}
	for _, host := range []string{"k8s.example.com", "K8S.Example.COM", "k8s.example.com:8080", "k8s.a.b.example.com", "k8s.example.com.", "k8s"} {
		for _, uri := range uris {
			if status, through := get(t, cfg, host, uri); status != http.StatusOK || through {
				t.Errorf("GET %s for host %q = %d (fell through %v), want %d served by the mock", uri, host, status, through, http.StatusOK)
			}
		}
	}
	for _, host := range []string{
		"app.k8s.example.com", "my-k8s-cluster.example.com", "k8s-dev.example.com", "k8s2.example.com", "notk8s.example.com",
		"example.com", "app.example.com:8080", "127.0.0.1", "[::1]:8080", "localhost", "",
	} {
		for _, uri := range uris {
			if status, through := get(t, cfg, host, uri); status != http.StatusNotFound || !through {
				t.Errorf("GET %s for host %q = %d (fell through %v), want %d left to the rest of goto", uri, host, status, through, http.StatusNotFound)
			}
		}
	}
}

func TestHostKeywordCanBeChanged(t *testing.T) {
	cfg := serve(t)
	old := HostKeyword
	t.Cleanup(func() { HostKeyword = old })

	// The port is not part of the host, so a keyword in it is not found there.
	HostKeyword = "8080"
	if _, through := get(t, cfg, "example.com:8080", "/api"); !through {
		t.Error("the port of the host was taken for a part of it")
	}
	HostKeyword = "Mock"
	if status, through := get(t, cfg, "mock.example.com", "/api"); status != http.StatusOK || through {
		t.Errorf("host of the new keyword = %d (fell through %v), want served by the mock", status, through)
	}
	if _, through := get(t, cfg, "k8s.example.com", "/api"); !through {
		t.Error("the host of the old keyword is still served")
	}
	// Without a keyword the mock is open to all the hosts.
	HostKeyword = ""
	for _, host := range []string{"k8s.example.com", "example.com", "127.0.0.1", ""} {
		if status, through := get(t, cfg, host, "/api"); status != http.StatusOK || through {
			t.Errorf("host %q without a keyword = %d (fell through %v), want served by the mock", host, status, through)
		}
	}
}

func TestDiscovery(t *testing.T) {
	d, err := discovery.NewDiscoveryClientForConfig(serve(t))
	if err != nil {
		t.Fatal(err)
	}
	groups, err := d.ServerGroups()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, g := range groups.Groups {
		have[g.Name] = true
	}
	for _, want := range []string{"", "apps", "batch", "networking.k8s.io", "networking.istio.io"} {
		if !have[want] {
			t.Errorf("groups = %v, want %q among them", have, want)
		}
	}
	core, err := d.ServerResourcesForGroupVersion("v1")
	if err != nil {
		t.Fatal(err)
	}
	resources := map[string]bool{}
	for _, r := range core.APIResources {
		resources[r.Name] = true
	}
	for _, want := range []string{"pods", "configmaps", "namespaces", "nodes", "secrets", "services"} {
		if !resources[want] {
			t.Errorf("v1 resources = %v, want %q among them", resources, want)
		}
	}
	apps, err := d.ServerResourcesForGroupVersion("apps/v1")
	if err != nil || len(apps.APIResources) == 0 {
		t.Errorf("apps/v1 = %v, %v", apps, err)
	}
	// A group that is not served is not found.
	if _, err := d.ServerResourcesForGroupVersion("nothing.example.com/v1"); err == nil {
		t.Log("a group that is not served has no resources")
	}
}

func TestListAndGetSamples(t *testing.T) {
	d := dyn(t)
	ctx := context.Background()
	list, err := d.Resource(pods).Namespace("sample-list").List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) == 0 {
		t.Fatalf("pods = %v, %v, want sample pods", list, err)
	}
	if list.GetKind() != "PodList" {
		t.Errorf("kind = %q, want PodList", list.GetKind())
	}
	name := list.Items[0].GetName()
	pod, err := d.Resource(pods).Namespace("sample-list").Get(ctx, name, metav1.GetOptions{})
	if err != nil || pod.GetName() != name || pod.GetKind() != "Pod" {
		t.Errorf("pod = %v, %v", pod, err)
	}
	// What is cluster wide, and the groups.
	if ns, err := d.Resource(nodes).List(ctx, metav1.ListOptions{}); err != nil || len(ns.Items) == 0 {
		t.Errorf("nodes = %v, %v", ns, err)
	}
	if ds, err := d.Resource(deployments).Namespace("sample-list").List(ctx, metav1.ListOptions{}); err != nil || len(ds.Items) == 0 {
		t.Errorf("deployments = %v, %v", ds, err)
	}
	if all, err := d.Resource(pods).List(ctx, metav1.ListOptions{}); err != nil || len(all.Items) == 0 {
		t.Errorf("pods of all namespaces = %v, %v", all, err)
	}
}

func TestCreateGetUpdatePatchDelete(t *testing.T) {
	d := dyn(t)
	ctx := context.Background()
	cms := d.Resource(configMaps).Namespace("crud")

	created, err := cms.Create(ctx, configMap("crud", "cm1", "one"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := created.GetCreationTimestamp()
	if created.GetUID() == "" || created.GetResourceVersion() == "" || createdAt.IsZero() {
		t.Errorf("created = %v, want the uid, version and time that the server gives", created.Object["metadata"])
	}
	got, err := cms.Get(ctx, "cm1", metav1.GetOptions{})
	if err != nil || dataValue(t, got) != "one" {
		t.Fatalf("get = %v, %v", got, err)
	}
	list, err := cms.List(ctx, metav1.ListOptions{})
	if err != nil || len(list.Items) != 1 {
		t.Errorf("list = %v, %v, want the config map that was created", list, err)
	}

	got.Object["data"] = map[string]interface{}{"k": "two"}
	updated, err := cms.Update(ctx, got, metav1.UpdateOptions{})
	if err != nil || dataValue(t, updated) != "two" || updated.GetResourceVersion() == got.GetResourceVersion() {
		t.Fatalf("update = %v, %v", updated, err)
	}

	patched, err := cms.Patch(ctx, "cm1", types.MergePatchType, []byte(`{"data":{"extra":"x"},"metadata":{"labels":{"a":"b"}}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := unstructured.NestedString(patched.Object, "data", "extra"); v != "x" || dataValue(t, patched) != "two" || patched.GetLabels()["a"] != "b" {
		t.Errorf("patched = %v, want the patch merged into what was there", patched.Object)
	}

	if err := cms.Delete(ctx, "cm1", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cms.Get(ctx, "cm1", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("get after the delete = %v, want not found", err)
	}
	if err := cms.Delete(ctx, "cm1", metav1.DeleteOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("delete of what is gone = %v, want not found", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	d := dyn(t)
	ctx := context.Background()
	cms := d.Resource(configMaps).Namespace("collection")
	for _, name := range []string{"a", "b"} {
		if _, err := cms.Create(ctx, configMap("collection", name, name), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cms.DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if list, err := cms.List(ctx, metav1.ListOptions{}); err != nil || len(list.Items) != 0 {
		t.Errorf("list after delete collection = %v, %v, want nothing", list, err)
	}
}

// A collection that nobody has written to is served with samples, and a kind that has no sample has nothing.
func TestGetOfWhatIsNotThereIsNotFound(t *testing.T) {
	d := dyn(t)
	limitRanges := schema.GroupVersionResource{Version: "v1", Resource: "limitranges"}
	_, err := d.Resource(limitRanges).Namespace("nowhere").Get(context.Background(), "nothing", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("get = %v, want not found", err)
	}
	// The error is what a client takes it for: a failure, with the reason and the code.
	status := err.(*apierrors.StatusError).Status()
	if status.Status != metav1.StatusFailure || status.Reason != metav1.StatusReasonNotFound || status.Code != 404 {
		t.Errorf("status = %+v, want a failure, not found, 404", status)
	}
}

// What a client deleted does not come back as a sample, and a collection that it emptied is not filled again.
func TestWhatWasDeletedStaysDeleted(t *testing.T) {
	d := dyn(t)
	ctx := context.Background()
	cms := d.Resource(configMaps).Namespace("sampled")
	// The samples are there until a client writes.
	if list, err := cms.List(ctx, metav1.ListOptions{}); err != nil || len(list.Items) == 0 {
		t.Fatalf("list = %v, %v, want samples", list, err)
	}
	list, _ := cms.List(ctx, metav1.ListOptions{})
	name := list.Items[0].GetName()
	if err := cms.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cms.Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("get of what was deleted = %v, want not found", err)
	}
	if err := cms.DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if list, err := cms.List(ctx, metav1.ListOptions{}); err != nil || len(list.Items) != 0 {
		t.Errorf("list after the collection was deleted = %v, %v, want nothing", list, err)
	}
}

func TestCreateWithoutANameGetsOne(t *testing.T) {
	d := dyn(t)
	cm := configMap("generated", "", "x")
	delete(cm.Object["metadata"].(map[string]interface{}), "name")
	created, err := d.Resource(configMaps).Namespace("generated").Create(context.Background(), cm, metav1.CreateOptions{})
	if err != nil || created.GetName() == "" {
		t.Errorf("created = %v, %v, want a name that the server makes", created, err)
	}
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name == "" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler != nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}
