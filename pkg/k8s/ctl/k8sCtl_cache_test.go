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
	"io"
	"strings"
	"testing"

	"goto/pkg/k8s/store"
)

const cachedYaml = `apiVersion: v1
kind: ConfigMap
metadata:
  name: cached
  namespace: ns1
data:
  k: %s
`

func storeValue(t *testing.T, value string) {
	t.Helper()
	text := strings.Replace(cachedYaml, "%s", value, 1)
	if _, errs := store.Kstore.StoreYaml("cached-unit", io.NopCloser(strings.NewReader(text))); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func readCached(t *testing.T) string {
	t.Helper()
	_, j, err := store.GetResource("configmaps", "ns1", "cached", nil, nil, nil)
	if err != nil || j == nil {
		t.Fatalf("read = %v, %v", j, err)
	}
	return j.ToJSONText()
}

// What goto applies or deletes is what it reads next, and not what it read before and keeps for a while.
func TestApplyAndDeleteForgetWhatIsCachedOfTheResource(t *testing.T) {
	newEnv(t)
	store.Cache.Clear()
	t.Cleanup(store.Cache.Clear)

	storeValue(t, "first")
	if _, errs := ApplyYaml("cached-unit", "ns1", "ConfigMap", "cached", "ns1", false); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := readCached(t); !strings.Contains(got, "first") {
		t.Fatalf("read after the first apply = %s", got)
	}

	storeValue(t, "second")
	if _, errs := ApplyYaml("cached-unit", "ns1", "ConfigMap", "cached", "ns1", false); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := readCached(t); !strings.Contains(got, "second") {
		t.Errorf("read after the second apply = %s, want the applied one and not the cached", got)
	}

	if _, errs := ApplyYaml("cached-unit", "ns1", "ConfigMap", "cached", "ns1", true); len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, j, err := store.GetResource("configmaps", "ns1", "cached", nil, nil, nil); j != nil || err != nil {
		t.Errorf("read after the delete = %v, %v, want the resource not to be found", j, err)
	}
}
