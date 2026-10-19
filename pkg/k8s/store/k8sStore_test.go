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

package store

import (
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func names(m interface{}) []string {
	list := []string{}
	switch v := m.(type) {
	case NamespaceKindMap:
		for k := range v {
			list = append(list, k)
		}
	case KindResourcesMap:
		for k := range v {
			list = append(list, k)
		}
	case ResourcesByName:
		for k := range v {
			list = append(list, k)
		}
	}
	sort.Strings(list)
	return list
}

func TestDecodeResourceID(t *testing.T) {
	tests := []struct {
		id                                 string
		group, version, resource, ns, name string
		ok                                 bool
	}{
		{"/v1/configmaps/ns1/cm1", "", "v1", "configmaps", "ns1", "cm1", true},
		{"apps/v1/deployments/ns1/web", "apps", "v1", "deployments", "ns1", "web", true},
		{"apps/v1/deployments/ns1", "apps", "v1", "deployments", "ns1", "", true},
		{"apps/v1/deployments", "apps", "v1", "deployments", "", "", true},
		{"/v1/pods/ns1/p1", "", "v1", "pod", "ns1", "p1", true},
		{"/v1/ns/ns1", "", "v1", "namespace", "ns1", "", true},
		{"/v1/namespaces/ns1", "", "v1", "namespace", "ns1", "", true},
		{"/v1/svc/ns1/s1", "", "v1", "service", "ns1", "s1", true},
		{"/v1/services/ns1/s1", "", "v1", "service", "ns1", "s1", true},
		{"/v1/secrets/ns1/s1/extra", "", "v1", "secrets", "ns1", "s1", true},
		{"", "", "", "", "", "", false},
		{"v1", "", "", "", "", "", false},
		{"/v1", "", "", "", "", "", false},
		{"apps/v1", "", "", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			g, v, r, ns, n, ok := decodeResourceID(tt.id)
			if ok != tt.ok || g != tt.group || v != tt.version || r != tt.resource || ns != tt.ns || n != tt.name {
				t.Errorf("decoded = %q %q %q %q %q %t, want %q %q %q %q %q %t", g, v, r, ns, n, ok,
					tt.group, tt.version, tt.resource, tt.ns, tt.name, tt.ok)
			}
		})
	}
}

func TestReinterpretResource(t *testing.T) {
	tests := []struct {
		name                       string
		resource, namespace, rname string
		wantKind, wantNS, wantName string
	}{
		{"plural", "pods", "ns1", "p1", "Pod", "ns1", "p1"},
		{"short name", "cm", "ns1", "c1", "ConfigMap", "ns1", "c1"},
		{"singular lower case", "deployment", "ns1", "d1", "Deployment", "ns1", "d1"},
		{"kind is not changed", "ConfigMap", "ns1", "c1", "ConfigMap", "ns1", "c1"},
		{"unknown", "widgets", "ns1", "w1", "widgets", "ns1", "w1"},
		{"namespace takes its name from the namespace", "namespaces", "ns1", "", "Namespace", "", "ns1"},
		{"namespace short name", "ns", "ns1", "", "Namespace", "", "ns1"},
		{"namespace that is only given a name", "namespace", "", "ns1", "Namespace", "", "ns1"},
		{"namespace with namespace and name", "namespace", "ns1", "other", "Namespace", "", "ns1"},
		{"empty", "", "", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, ns, n := reinterpretResource(tt.resource, tt.namespace, tt.rname)
			if k != tt.wantKind || ns != tt.wantNS || n != tt.wantName {
				t.Errorf("got %q %q %q, want %q %q %q", k, ns, n, tt.wantKind, tt.wantNS, tt.wantName)
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestReadDocuments(t *testing.T) {
	docs, err := readDocuments(io.NopCloser(strings.NewReader("a: 1\n---\nb: 2\n---\nc: 3\n")))
	if err != nil || len(docs) != 3 {
		t.Fatalf("docs = %d, err = %v, want 3 documents", len(docs), err)
	}
	if !strings.Contains(string(docs[0]), "a: 1") || !strings.Contains(string(docs[2]), "c: 3") {
		t.Errorf("docs = %q", docs)
	}
	if docs, err := readDocuments(io.NopCloser(strings.NewReader(""))); err != nil || len(docs) != 0 {
		t.Errorf("an empty body: %d documents, %v, want none", len(docs), err)
	}
	if _, err := readDocuments(io.NopCloser(errReader{})); err == nil {
		t.Error("an error of reading was not reported")
	}
}

func TestReadResourceList(t *testing.T) {
	list, errs := readResourceList(io.NopCloser(strings.NewReader(sampleYaml)))
	if len(errs) != 0 || len(list) != 4 {
		t.Fatalf("list = %d, errs = %v, want 4 resources", len(list), errs)
	}
	if list[0].GetKind() != "Namespace" || list[1].GetName() != "cm1" || list[3].GetNamespace() != "ns2" {
		t.Errorf("resources are not as they were written: %v", list)
	}
	if _, errs := readResourceList(io.NopCloser(strings.NewReader(""))); len(errs) != 1 || !strings.Contains(errs[0].Error(), "no documents") {
		t.Errorf("errors for an empty body = %v", errs)
	}
	if _, errs := readResourceList(io.NopCloser(errReader{})); len(errs) != 1 {
		t.Errorf("errors for a body that fails = %v, want the one of the reading", errs)
	}
	list, errs = readResourceList(io.NopCloser(strings.NewReader("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n---\nnot: a resource\n")))
	if len(errs) != 1 || len(list) != 1 {
		t.Errorf("list = %d, errs = %v, want the resource and the error of the document that is not one", len(list), errs)
	}
}

func TestStoreYaml(t *testing.T) {
	newEnv(t)
	stored := storeYaml(t, "unit", sampleYaml)
	if got := names(stored); !reflect.DeepEqual(got, []string{"ns1", "ns2"}) {
		t.Fatalf("namespaces = %v, want ns1 (with the namespace itself) and ns2", got)
	}
	if got := names(stored["ns1"]); !reflect.DeepEqual(got, []string{"ConfigMap", "Namespace"}) {
		t.Errorf("kinds in ns1 = %v", got)
	}
	if got := names(stored["ns1"]["ConfigMap"]); !reflect.DeepEqual(got, []string{"cm1", "cm2"}) {
		t.Errorf("config maps in ns1 = %v", got)
	}
	ns := stored["ns1"]["Namespace"]["ns1"]
	if ns == nil || ns.Name != "ns1" || ns.GVK.Kind != "Namespace" || ns.Resource.GetName() != "ns1" {
		t.Errorf("the namespace is stored as %+v, want it under its own name", ns)
	}
	web := stored["ns2"]["Deployment"]["web"]
	if web == nil || web.Namespace != "ns2" || web.GVK.Group != "apps" || web.GVK.Version != "v1" {
		t.Errorf("deployment = %+v", web)
	}
	// Another unit is kept apart, and storing the same unit again replaces what has the same names.
	other := storeYaml(t, "other", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p1\n  namespace: nsx\n")
	if got := names(other); !reflect.DeepEqual(got, []string{"nsx"}) {
		t.Errorf("namespaces of the other unit = %v", got)
	}
	again := storeYaml(t, "unit", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\n  namespace: ns1\ndata:\n  k: changed\n")
	if again["ns1"]["ConfigMap"]["cm1"].Resource.Object["data"].(map[string]interface{})["k"] != "changed" {
		t.Error("the config map was not replaced")
	}
	if got := names(again["ns1"]["ConfigMap"]); !reflect.DeepEqual(got, []string{"cm1", "cm2"}) {
		t.Errorf("config maps after storing again = %v, want the old ones kept", got)
	}
}

func TestStoreYamlWithErrorsStoresNothing(t *testing.T) {
	newEnv(t)
	stored, errs := Kstore.StoreYaml("bad", io.NopCloser(strings.NewReader("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n---\nnot: valid\n")))
	if len(errs) == 0 || stored != nil {
		t.Errorf("stored = %v, errs = %v, want errors", stored, errs)
	}
	if Kstore.Get("bad", "", "") != nil {
		t.Error("something was stored from YAML with errors")
	}
	if _, errs := Kstore.StoreYaml("empty", io.NopCloser(strings.NewReader("  \n"))); len(errs) == 0 {
		t.Error("an empty YAML was stored without an error")
	}
}

func TestStoreClusterScopedResourceWithoutNamespace(t *testing.T) {
	newEnv(t)
	stored := storeYaml(t, "cluster", "apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata:\n  name: fast\n")
	if stored[""]["StorageClass"]["fast"] == nil {
		t.Errorf("stored = %v, want the resource under the empty namespace", stored)
	}
}

func TestKstoreGet(t *testing.T) {
	newEnv(t)
	storeYaml(t, "unit", sampleYaml)
	if got, ok := Kstore.Get("unit", "", "").(NamespaceKindMap); !ok || !reflect.DeepEqual(names(got), []string{"ns1", "ns2"}) {
		t.Errorf("get of the unit = %v", Kstore.Get("unit", "", ""))
	}
	if got, ok := Kstore.Get("unit", "ns1", "").(KindResourcesMap); !ok || !reflect.DeepEqual(names(got), []string{"ConfigMap", "Namespace"}) {
		t.Errorf("get of a namespace = %v", Kstore.Get("unit", "ns1", ""))
	}
	for _, kind := range []string{"ConfigMap", "configmaps", "cm", "configmap"} {
		got, ok := Kstore.Get("unit", "ns1", kind).(ResourcesByName)
		if !ok || !reflect.DeepEqual(names(got), []string{"cm1", "cm2"}) {
			t.Errorf("get of kind %q = %v", kind, Kstore.Get("unit", "ns1", kind))
		}
	}
	// Kind is looked up without the namespace, for any of its spellings.
	if got, ok := Kstore.Get("unit", "ns2", "deploy").(ResourcesByName); !ok || got["web"] == nil {
		t.Errorf("get by short name = %v", Kstore.Get("unit", "ns2", "deploy"))
	}
	for _, c := range []struct{ unit, ns, kind string }{{"", "ns1", "cm"}, {"missing", "", ""}, {"unit", "missing", ""}, {"unit", "missing", "cm"}} {
		if got := Kstore.Get(c.unit, c.ns, c.kind); got != nil {
			t.Errorf("get %+v = %v, want nothing", c, got)
		}
	}
}

func TestKstoreGetTargetResources(t *testing.T) {
	newEnv(t)
	storeYaml(t, "unit", sampleYaml)
	t.Run("whole unit", func(t *testing.T) {
		got, err := Kstore.GetTargetResources("unit", "", "", "")
		if err != nil || !reflect.DeepEqual(names(got), []string{"ns1", "ns2"}) {
			t.Errorf("got %v, %v", names(got), err)
		}
	})
	t.Run("namespace", func(t *testing.T) {
		got, err := Kstore.GetTargetResources("unit", "ns1", "", "")
		if err != nil || !reflect.DeepEqual(names(got), []string{"ns1"}) || !reflect.DeepEqual(names(got["ns1"]), []string{"ConfigMap", "Namespace"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("kind", func(t *testing.T) {
		got, err := Kstore.GetTargetResources("unit", "ns1", "configmaps", "")
		if err != nil || !reflect.DeepEqual(names(got["ns1"]), []string{"ConfigMap"}) || !reflect.DeepEqual(names(got["ns1"]["ConfigMap"]), []string{"cm1", "cm2"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("name", func(t *testing.T) {
		got, err := Kstore.GetTargetResources("unit", "ns1", "cm", "cm2")
		if err != nil || !reflect.DeepEqual(names(got["ns1"]["ConfigMap"]), []string{"cm2"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("namespace as a kind", func(t *testing.T) {
		got, err := Kstore.GetTargetResources("unit", "ns1", "namespaces", "")
		if err != nil || !reflect.DeepEqual(names(got["ns1"]["Namespace"]), []string{"ns1"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	for name, c := range map[string]struct{ unit, ns, kind, resource string }{
		"no unit":           {"", "", "", ""},
		"unknown unit":      {"missing", "", "", ""},
		"unknown namespace": {"unit", "missing", "", ""},
		"unknown kind":      {"unit", "ns1", "Deployment", ""},
		"unknown name":      {"unit", "ns1", "cm", "cm9"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := Kstore.GetTargetResources(c.unit, c.ns, c.kind, c.resource); err == nil || got != nil {
				t.Errorf("got %v, %v, want an error", got, err)
			}
		})
	}
}

func TestKstoreClear(t *testing.T) {
	newEnv(t)
	storeYaml(t, "unit", sampleYaml)
	storeYaml(t, "other", sampleYaml)
	Kstore.Clear("unit", "ns1")
	if Kstore.Get("unit", "ns1", "") != nil {
		t.Error("the namespace is still there after it was cleared")
	}
	if Kstore.Get("unit", "ns2", "") == nil || Kstore.Get("other", "ns1", "") == nil {
		t.Error("clearing a namespace of a unit cleared more")
	}
	Kstore.Clear("unit", "missing")
	Kstore.Clear("missing", "ns1")
	Kstore.Clear("unit", "")
	if Kstore.Get("unit", "ns2", "") != nil || Kstore.Get("unit", "", "") != nil {
		t.Error("the unit is still there after it was cleared")
	}
	if Kstore.Get("other", "ns1", "") == nil {
		t.Error("clearing a unit cleared another")
	}
	Kstore.Clear("", "")
	if Kstore.Get("other", "ns1", "") != nil {
		t.Error("everything was not cleared")
	}
	storeYaml(t, "unit", sampleYaml)
	if Kstore.Get("unit", "ns2", "") == nil {
		t.Error("the store cannot be used after clearing it")
	}
}

func TestKstoreGetReturnsWhatCallersMayChange(t *testing.T) {
	newEnv(t)
	storeYaml(t, "unit", sampleYaml)
	got := Kstore.Get("unit", "ns1", "ConfigMap").(ResourcesByName)
	delete(got, "cm1")
	got["intruder"] = nil
	again := Kstore.Get("unit", "ns1", "ConfigMap").(ResourcesByName)
	if !reflect.DeepEqual(names(again), []string{"cm1", "cm2"}) {
		t.Errorf("config maps = %v, want the store not to change with what a caller does to its result", names(again))
	}
	all, _ := Kstore.GetTargetResources("unit", "", "", "")
	delete(all, "ns1")
	if again, _ := Kstore.GetTargetResources("unit", "", "", ""); len(again) != 2 {
		t.Errorf("namespaces = %v, want the store not to change with what a caller does to its result", names(again))
	}
}
