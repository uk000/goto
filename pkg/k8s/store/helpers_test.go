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
	"io"
	"strings"
	"testing"
	"time"

	"goto/pkg/k8s/k8stest"
	"goto/pkg/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
spec:
  replicas: 2
`

// newEnv gives a test the API server that the client talks to, and a cache and a store of YAML that are empty, and that
// are emptied again when the test is done.
func newEnv(t *testing.T) *k8stest.API {
	t.Helper()
	api := k8stest.Install(t)
	reset := func() {
		Cache.Clear()
		Kstore.lock.Lock()
		Kstore.YamlResources = YamlNamespaceMap{}
		Kstore.lock.Unlock()
	}
	reset()
	t.Cleanup(reset)
	return api
}

func storeYaml(t *testing.T, unit, text string) NamespaceKindMap {
	t.Helper()
	stored, errs := Kstore.StoreYaml(unit, io.NopCloser(strings.NewReader(text)))
	if len(errs) > 0 {
		t.Fatalf("storing the YAML failed: %v", errs)
	}
	return stored
}

func object(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	meta := map[string]interface{}{"name": name}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": apiVersion, "kind": kind, "metadata": meta}}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// jsonOf returns a JSON of what a resource lookup found, as a text that tests look into.
func jsonOf(j util.JSON) string {
	if j == nil {
		return ""
	}
	return j.ToJSONText()
}

// after is the time that a test waits for what a goroutine of the code does.
func after() <-chan time.Time { return time.After(5 * time.Second) }
