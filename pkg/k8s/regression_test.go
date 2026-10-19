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
	"strings"
	"testing"

	k8sClient "goto/pkg/k8s/client"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// There is no default client when the server starts where no kube config or cluster is found, and the APIs that need a
// client have to answer with an error then, and not take the process down with a nil pointer.
func TestMappingWithoutDefaultClientIsAnErrorNotAPanic(t *testing.T) {
	old := k8sClient.Client
	k8sClient.Client = nil
	defer func() { k8sClient.Client = old }()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("mapping without a client panicked: %v", r)
		}
	}()
	if _, err := GetGVRMapping(&schema.GroupVersionKind{Version: "v1", Kind: "Pod"}); err == nil {
		t.Error("no error without a client")
	}
}

// Namespaces are not in a namespace.
func TestNamespacesAreClusterScoped(t *testing.T) {
	for _, name := range []string{"namespaces", "namespace", "ns"} {
		if r := K8sApiResourcesMap[name]; r == nil || r.Namespaced {
			t.Errorf("%q = %+v, want a resource that is not namespaced", name, r)
		}
	}
}

// A name that two resources have would take to either of them, whichever of them the map was filled with last (and that
// changes from one start to the next).
func TestNamesOfResourcesAreUnique(t *testing.T) {
	owners := map[string]string{}
	seen := map[*K8sApiResource]bool{}
	for _, r := range K8sApiResourcesMap {
		if seen[r] {
			continue
		}
		seen[r] = true
		for _, name := range []string{r.ShortName, r.OtherName, r.Kind} {
			if name == "" {
				continue
			}
			name = strings.ToLower(name)
			if other, ok := owners[name]; ok && other != r.Kind {
				t.Errorf("name %q is of both %s and %s", name, other, r.Kind)
			}
			owners[name] = r.Kind
		}
	}
	if r := K8sApiResourcesMap["sc"]; r == nil || r.Kind != "StorageClass" {
		t.Errorf("sc = %+v, want the storage class", r)
	}
}

// The GVK of a resource is its version and kind.
func TestGVKOfResourcesIsVersionAndKind(t *testing.T) {
	for key, r := range K8sApiResourcesMap {
		if want := r.Version + "/" + r.Kind; r.GVK != want {
			t.Errorf("%q: GVK = %q, want %q", key, r.GVK, want)
		}
	}
}
