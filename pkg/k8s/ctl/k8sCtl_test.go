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
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/k8stest"
	"goto/pkg/k8s/store"
)

const sampleYaml = `apiVersion: v1
kind: Namespace
metadata:
  name: ns1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: ns1
data:
  k: v
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm2
  namespace: ns1
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: ns2
`

// newEnv makes the API server that the client talks to, and a store that has the sample YAML as the unit "unit".
func newEnv(t *testing.T) *k8stest.API {
	t.Helper()
	api := k8stest.Install(t)
	store.Kstore.Clear("", "")
	t.Cleanup(func() { store.Kstore.Clear("", "") })
	if _, errs := store.Kstore.StoreYaml("unit", io.NopCloser(strings.NewReader(sampleYaml))); len(errs) > 0 {
		t.Fatal(errs)
	}
	return api
}

func objectMap(kind, namespace, name string) map[string]interface{} {
	apiVersion := "v1"
	if kind == "Deployment" {
		apiVersion = "apps/v1"
	}
	meta := map[string]interface{}{"name": name}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	return map[string]interface{}{"apiVersion": apiVersion, "kind": kind, "metadata": meta}
}

// requestLines is what the API server was asked, as "METHOD path", sorted.
func requestLines(api *k8stest.API) []string {
	var lines []string
	for _, r := range api.Requests() {
		lines = append(lines, r.Method+" "+r.Path)
	}
	sort.Strings(lines)
	return lines
}

func contains(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}

func TestApplyYamlToTheNamespacesOfTheResources(t *testing.T) {
	api := newEnv(t)
	list, errs := ApplyYaml("unit", "", "", "", "", false)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	sort.Strings(list)
	if want := "[ns1/ConfigMap/cm1 ns1/ConfigMap/cm2 ns1/Namespace/ns1 ns2/Deployment/web]"; strings.Join(list, " ") != strings.Trim(want, "[]") {
		t.Errorf("applied = %v, want %s", list, want)
	}
	lines := requestLines(api)
	for _, want := range []string{
		"PATCH /api/v1/namespaces/ns1",
		"PATCH /api/v1/namespaces/ns1/configmaps/cm1",
		"PATCH /api/v1/namespaces/ns1/configmaps/cm2",
		"PATCH /apis/apps/v1/namespaces/ns2/deployments/web",
	} {
		if !contains(lines, want) {
			t.Errorf("requests = %v, want %q", lines, want)
		}
	}
	if len(lines) != 4 {
		t.Errorf("requests = %v, want four", lines)
	}
}

func TestApplyYamlSendsTheResourceAsAServerSideApply(t *testing.T) {
	api := newEnv(t)
	if _, errs := ApplyYaml("unit", "ns1", "cm", "cm1", "ns1", false); len(errs) != 0 {
		t.Fatal(errs)
	}
	reqs := api.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %v, want one", reqs)
	}
	r := reqs[0]
	if r.Method != http.MethodPatch || r.Path != "/api/v1/namespaces/ns1/configmaps/cm1" {
		t.Errorf("request = %s %s", r.Method, r.Path)
	}
	if r.ContentType != "application/apply-patch+yaml" || !strings.Contains(r.RawQuery, "fieldManager=goto") {
		t.Errorf("content type = %q, query = %q, want an apply patch of the manager goto", r.ContentType, r.RawQuery)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["kind"] != "ConfigMap" || body["data"].(map[string]interface{})["k"] != "v" || body["metadata"].(map[string]interface{})["name"] != "cm1" {
		t.Errorf("body = %s", r.Body)
	}
	if got := api.Get("v1", "ConfigMap", "ns1", "cm1"); got == nil {
		t.Error("the config map was not applied")
	}
}

func TestApplyYamlSelectsWhatIsApplied(t *testing.T) {
	for name, c := range map[string]struct {
		namespace, kind, resource string
		want                      []string
	}{
		"namespace": {"ns1", "", "", []string{"PATCH /api/v1/namespaces/ns1", "PATCH /api/v1/namespaces/ns1/configmaps/cm1", "PATCH /api/v1/namespaces/ns1/configmaps/cm2"}},
		"kind":      {"ns1", "configmaps", "", []string{"PATCH /api/v1/namespaces/ns1/configmaps/cm1", "PATCH /api/v1/namespaces/ns1/configmaps/cm2"}},
		"name":      {"ns1", "cm", "cm2", []string{"PATCH /api/v1/namespaces/ns1/configmaps/cm2"}},
		"other":     {"ns2", "deploy", "", []string{"PATCH /apis/apps/v1/namespaces/ns2/deployments/web"}},
	} {
		t.Run(name, func(t *testing.T) {
			api := newEnv(t)
			if _, errs := ApplyYaml("unit", c.namespace, c.kind, c.resource, c.namespace, false); len(errs) != 0 {
				t.Fatal(errs)
			}
			if got := requestLines(api); strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("requests = %v, want %v", got, c.want)
			}
		})
	}
}

func TestApplyYamlMovesTheResourcesToAnotherNamespace(t *testing.T) {
	api := newEnv(t)
	list, errs := ApplyYaml("unit", "ns1", "cm", "", "ns9", false)
	if len(errs) != 0 || len(list) != 2 {
		t.Fatalf("applied = %v, errors = %v", list, errs)
	}
	lines := requestLines(api)
	if !contains(lines, "PATCH /api/v1/namespaces/ns9/configmaps/cm1") || !contains(lines, "PATCH /api/v1/namespaces/ns9/configmaps/cm2") || len(lines) != 2 {
		t.Errorf("requests = %v, want the config maps to be applied in ns9", lines)
	}
	for _, r := range api.Requests() {
		var body struct{ Metadata struct{ Namespace string } }
		if err := json.Unmarshal(r.Body, &body); err != nil || body.Metadata.Namespace != "ns9" {
			t.Errorf("body = %s, want the namespace of the resource to be ns9", r.Body)
		}
	}
}

// The unit of YAML is what is stored, and applying it to another namespace is not changing it.
func TestApplyYamlToAnotherNamespaceLeavesTheStoredResourcesAsTheyWere(t *testing.T) {
	newEnv(t)
	if _, errs := ApplyYaml("unit", "ns1", "", "", "ns9", false); len(errs) != 0 {
		t.Fatal(errs)
	}
	kinds, err := store.Kstore.GetTargetResources("unit", "ns1", "cm", "cm1")
	if err != nil {
		t.Fatal(err)
	}
	cm := kinds["ns1"]["ConfigMap"]["cm1"]
	if cm == nil || cm.Namespace != "ns1" || cm.Resource.GetNamespace() != "ns1" {
		t.Errorf("stored config map = %+v, want it to be of ns1 still", cm)
	}
	ns := store.Kstore.Get("unit", "ns1", "Namespace").(store.ResourcesByName)["ns1"]
	if ns == nil || ns.Name != "ns1" || ns.Resource.GetName() != "ns1" {
		t.Errorf("stored namespace = %+v, want it to be named ns1 still", ns)
	}
}

// A namespace that is moved is a namespace of another name, and that is the one that its request is for.
func TestApplyYamlOfANamespaceToAnotherOneIsForTheNameOfIt(t *testing.T) {
	api := newEnv(t)
	if _, errs := ApplyYaml("unit", "ns1", "namespace", "", "ns9", false); len(errs) != 0 {
		t.Fatal(errs)
	}
	lines := requestLines(api)
	if len(lines) != 1 || lines[0] != "PATCH /api/v1/namespaces/ns9" {
		t.Errorf("requests = %v, want the namespace ns9 to be applied", lines)
	}
	if api.Get("v1", "Namespace", "", "ns9") == nil {
		t.Error("the namespace ns9 was not applied")
	}
}

func TestDeleteYaml(t *testing.T) {
	api := newEnv(t)
	api.Put(objectMap("ConfigMap", "ns1", "cm1"))
	api.Put(objectMap("ConfigMap", "ns1", "cm2"))
	list, errs := ApplyYaml("unit", "ns1", "cm", "", "ns1", true)
	if len(errs) != 0 || len(list) != 2 {
		t.Fatalf("deleted = %v, errors = %v", list, errs)
	}
	if api.Get("v1", "ConfigMap", "ns1", "cm1") != nil || api.Get("v1", "ConfigMap", "ns1", "cm2") != nil {
		t.Error("the config maps are still there")
	}
	for _, r := range api.Requests() {
		if r.Method != http.MethodDelete {
			t.Errorf("request = %s %s, want deletes only", r.Method, r.Path)
		}
	}
}

func TestDeleteYamlWithoutANamespaceDeletesFromTheNamespacesOfTheResources(t *testing.T) {
	api := newEnv(t)
	api.Put(objectMap("ConfigMap", "ns1", "cm1"))
	api.Put(objectMap("Deployment", "ns2", "web"))
	list, errs := ApplyYaml("unit", "", "cm", "cm1", "", true)
	if len(errs) != 0 || len(list) != 1 || list[0] != "ns1/ConfigMap/cm1" {
		t.Fatalf("deleted = %v, errors = %v", list, errs)
	}
	if api.Get("v1", "ConfigMap", "ns1", "cm1") != nil {
		t.Error("the config map is still there")
	}
}

func TestDeleteOfWhatIsNotThereIsAnError(t *testing.T) {
	newEnv(t)
	list, errs := ApplyYaml("unit", "ns1", "cm", "cm1", "ns1", true)
	if len(list) != 0 || len(errs) != 1 {
		t.Errorf("deleted = %v, errors = %v, want the one error of the one that is not there", list, errs)
	}
}

func TestApplyYamlReportsWhatFailedAndWhatDidNot(t *testing.T) {
	api := newEnv(t)
	api.FailResources(http.StatusInternalServerError)
	list, errs := ApplyYaml("unit", "ns1", "", "", "ns1", false)
	if len(list) != 0 || len(errs) != 3 {
		t.Errorf("applied = %v, errors = %d, want every one of the three to fail", list, len(errs))
	}
	api.FailResources(0)
	list, errs = ApplyYaml("unit", "ns1", "", "", "ns1", false)
	if len(list) != 3 || len(errs) != 0 {
		t.Errorf("applied = %v, errors = %v, want a failure not to be remembered", list, errs)
	}
}

func TestApplyYamlOfWhatIsNotStored(t *testing.T) {
	newEnv(t)
	for name, c := range map[string][4]string{
		"no unit":           {"", "", "", ""},
		"unknown unit":      {"missing", "", "", ""},
		"unknown namespace": {"unit", "missing", "", ""},
		"unknown kind":      {"unit", "ns1", "Deployment", ""},
		"unknown name":      {"unit", "ns1", "cm", "cm9"},
	} {
		t.Run(name, func(t *testing.T) {
			list, errs := ApplyYaml(c[0], c[1], c[2], c[3], c[1], false)
			if len(list) != 0 || len(errs) != 1 {
				t.Errorf("applied = %v, errors = %v, want one error", list, errs)
			}
		})
	}
}

func TestApplyYamlWithoutAClient(t *testing.T) {
	newEnv(t)
	old := k8sClient.Client
	k8sClient.Client = nil
	t.Cleanup(func() { k8sClient.Client = old })
	list, errs := ApplyYaml("unit", "ns1", "cm", "", "ns1", false)
	if len(list) != 0 || len(errs) != 2 {
		t.Errorf("applied = %v, errors = %v, want an error for each resource and no panic", list, errs)
	}
}

func TestApplyYamlOfAResourceThatTheServerDoesNotServe(t *testing.T) {
	newEnv(t)
	if _, errs := store.Kstore.StoreYaml("other", io.NopCloser(strings.NewReader("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n  namespace: ns1\n"))); len(errs) != 0 {
		t.Fatal(errs)
	}
	list, errs := ApplyYaml("other", "", "", "", "", false)
	if len(list) != 0 || len(errs) != 1 {
		t.Errorf("applied = %v, errors = %v, want the error that the kind is not known", list, errs)
	}
}
