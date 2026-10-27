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

package k8s

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/k8stest"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func configMap(namespace, name string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace},
		"data":     map[string]interface{}{"k": "v"},
	}
}

// Every entry of the map of the API resources is found under its name, and also under its short name, its other name
// and the lower case of its kind.
func TestApiResourcesMapIsConsistent(t *testing.T) {
	if len(K8sApiResourcesMap) < 60 {
		t.Fatalf("map has %d names, want the resources and their aliases", len(K8sApiResourcesMap))
	}
	known := map[string]bool{"create": true, "delete": true, "deletecollection": true, "get": true, "list": true,
		"patch": true, "update": true, "watch": true}
	resources := map[*K8sApiResource]bool{}
	for key, r := range K8sApiResourcesMap {
		if r == nil {
			t.Errorf("%q has no resource", key)
			continue
		}
		resources[r] = true
		if key != strings.ToLower(key) {
			t.Errorf("name %q is not lower case: names are looked up in lower case", key)
		}
	}
	for r := range resources {
		if r.Kind == "" || r.Version == "" {
			t.Errorf("resource %+v has no kind or version", r)
			continue
		}
		if want := r.Version + "/" + r.Kind; r.GVK != want {
			t.Errorf("%s: GVK = %q, want %q", r.Kind, r.GVK, want)
		}
		if len(r.Verbs) == 0 {
			t.Errorf("%s has no verbs", r.Kind)
		}
		for _, v := range r.Verbs {
			if !known[v] {
				t.Errorf("%s has unknown verb %q", r.Kind, v)
			}
		}
		if K8sApiResourcesMap[strings.ToLower(r.Kind)] != r {
			t.Errorf("%s is not found by its kind", r.Kind)
		}
		if r.ShortName != "" && K8sApiResourcesMap[strings.ToLower(r.ShortName)] != r {
			t.Errorf("%s is not found by its short name %q, which is %v", r.Kind, r.ShortName, K8sApiResourcesMap[strings.ToLower(r.ShortName)])
		}
		if r.OtherName != "" && K8sApiResourcesMap[strings.ToLower(r.OtherName)] != r {
			t.Errorf("%s is not found by its other name %q", r.Kind, r.OtherName)
		}
	}
}

func TestApiResourcesMapKnownResources(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		version    string
		namespaced bool
	}{
		{"pods", "Pod", "v1", true},
		{"po", "Pod", "v1", true},
		{"pod", "Pod", "v1", true},
		{"configmaps", "ConfigMap", "v1", true},
		{"cm", "ConfigMap", "v1", true},
		{"namespaces", "Namespace", "v1", false},
		{"ns", "Namespace", "v1", false},
		{"deployments", "Deployment", "apps/v1", true},
		{"deploy", "Deployment", "apps/v1", true},
		{"services", "Service", "v1", true},
		{"svc", "Service", "v1", true},
		{"authorizationpolicies", "AuthorizationPolicy", "security.istio.io/v1", true},
	}
	for _, tt := range tests {
		r := K8sApiResourcesMap[tt.name]
		if r == nil {
			t.Errorf("%q is not in the map", tt.name)
			continue
		}
		if r.Kind != tt.kind || r.Version != tt.version || r.Namespaced != tt.namespaced {
			t.Errorf("%q = %s %s namespaced %t, want %s %s namespaced %t", tt.name, r.Kind, r.Version, r.Namespaced, tt.kind, tt.version, tt.namespaced)
		}
	}
	if K8sApiResourcesMap["nosuchkind"] != nil {
		t.Error("a name that is not a resource is in the map")
	}
	if KindNamespace != "Namespace" || K8sApiResourcesMap["namespaces"].Kind != KindNamespace {
		t.Error("the kind of namespaces is not the one that the code compares to")
	}
}

func TestResourceJSONShape(t *testing.T) {
	obj := &unstructured.Unstructured{Object: configMap("ns", "cm")}
	gvk := obj.GroupVersionKind()
	b, err := json.Marshal(&K8sResource{Resource: obj, GVK: &gvk, Namespace: "ns", Name: "cm"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"gvk", "name", "namespace", "resource"}) {
		t.Errorf("keys = %v", keys)
	}
	if got["name"] != "cm" || got["namespace"] != "ns" {
		t.Errorf("resource JSON = %s", b)
	}
	b, _ = json.Marshal(K8sApiResourcesMap["pods"])
	for _, key := range []string{`"shortName"`, `"version"`, `"kind"`, `"namespaced"`, `"verbs"`, `"group"`, `"otherName"`, `"gvk"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("API resource JSON %s has no %s", b, key)
		}
	}
}

func TestGetGVRMapping(t *testing.T) {
	k8stest.Install(t)
	tests := []struct {
		gvk        schema.GroupVersionKind
		resource   string
		group      string
		namespaced bool
	}{
		{schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, "configmaps", "", true},
		{schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}, "namespaces", "", false},
		{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, "deployments", "apps", true},
	}
	for _, tt := range tests {
		gvk := tt.gvk
		m, err := GetGVRMapping(&gvk)
		if err != nil {
			t.Errorf("%v: %v", tt.gvk, err)
			continue
		}
		if m.Resource.Resource != tt.resource || m.Resource.Group != tt.group || m.Resource.Version != "v1" {
			t.Errorf("%v: resource = %v, want %s in group %q", tt.gvk, m.Resource, tt.resource, tt.group)
		}
		if (m.Scope.Name() == meta.RESTScopeNameNamespace) != tt.namespaced {
			t.Errorf("%v: scope = %s, want namespaced %t", tt.gvk, m.Scope.Name(), tt.namespaced)
		}
	}
	if _, err := GetGVRMapping(&schema.GroupVersionKind{Version: "v1", Kind: "NoSuchKind"}); err == nil {
		t.Error("a kind that the server does not have was mapped")
	}
}

func TestGetResourceInterfaceReachesTheResources(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(configMap("ns1", "cm1"))
	api.Put(configMap("ns2", "cm1"))
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": "ns1"}})
	cmGVK := &schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	ri, mapping, err := GetResourceInterface(cmGVK, "ns1")
	if err != nil || mapping == nil {
		t.Fatalf("interface: %v, %v", mapping, err)
	}
	obj, err := ri.Get(context.Background(), "cm1", metav1.GetOptions{})
	if err != nil || obj.GetNamespace() != "ns1" || obj.GetName() != "cm1" {
		t.Fatalf("get = %v, %v, want the config map of the namespace", obj, err)
	}
	list, err := ri.List(context.Background(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 1 {
		t.Errorf("list of the namespace = %v, %v, want 1 item", list, err)
	}
	// Cluster scoped resources do not have the namespace.
	nsGVK := &schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}
	ri, _, err = GetResourceInterface(nsGVK, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if obj, err := ri.Get(context.Background(), "ns1", metav1.GetOptions{}); err != nil || obj.GetName() != "ns1" {
		t.Errorf("namespace = %v, %v", obj, err)
	}
	for _, r := range api.Requests() {
		if strings.Contains(r.Path, "ignored") {
			t.Errorf("request %s %s has the namespace of a cluster scoped resource", r.Method, r.Path)
		}
	}
	if _, _, err := GetResourceInterface(&schema.GroupVersionKind{Version: "v1", Kind: "NoSuchKind"}, "ns"); err == nil {
		t.Error("an interface was made for a kind that the server does not have")
	}
}

func TestGetGVRMappingWithoutClientFails(t *testing.T) {
	old := k8sClient.Client
	k8sClient.Client = nil
	defer func() { k8sClient.Client = old }()
	if m, err := GetGVRMapping(&schema.GroupVersionKind{Version: "v1", Kind: "Pod"}); err == nil || m != nil {
		t.Errorf("mapping = %v, %v, want an error without a client", m, err)
	}
	if ri, m, err := GetResourceInterface(&schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, "ns"); err == nil || ri != nil || m != nil {
		t.Errorf("interface = %v, %v, %v, want an error without a client", ri, m, err)
	}
}
