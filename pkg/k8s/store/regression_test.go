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
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	k8sClient "goto/pkg/k8s/client"
)

func obj(kind, namespace, name string, data map[string]interface{}) map[string]interface{} {
	meta := map[string]interface{}{"name": name}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	o := map[string]interface{}{"apiVersion": "v1", "kind": kind, "metadata": meta}
	if data != nil {
		o["data"] = data
	}
	return o
}

// A cache of resources is keyed by the namespace and the name, which two resources of different kinds can have the same.
func TestResourcesOfDifferentKindsWithTheSameNameAreNotMixedUp(t *testing.T) {
	api := newEnv(t)
	api.Put(obj("ConfigMap", "ns1", "app", map[string]interface{}{"k": "v"}))
	api.Put(obj("Pod", "ns1", "app", nil))
	_, cm, err := GetResource("configmap", "ns1", "app", nil, nil, nil)
	if err != nil || cm == nil || cm.GetText("kind") != "ConfigMap" {
		t.Fatalf("config map = %q, %v", jsonOf(cm), err)
	}
	_, pod, err := GetResource("pod", "ns1", "app", nil, nil, nil)
	if err != nil || pod == nil || pod.GetText("kind") != "Pod" {
		t.Errorf("pod = %q, %v, want the pod and not the config map that has its name", jsonOf(pod), err)
	}
	_, cm, _ = GetResource("configmap", "ns1", "app", nil, nil, nil)
	if cm == nil || cm.GetText("kind") != "ConfigMap" {
		t.Errorf("config map from the cache = %q", jsonOf(cm))
	}
}

// Watchers are told the namespace and the name of what changed. They were given the name of the watcher where the
// namespace goes, and their own namespace where the name goes.
func TestWatchersAreToldWhichResourceChanged(t *testing.T) {
	kw := newWatches()
	var got []string
	record := func(kind string) func(namespace, name string, obj interface{}) {
		return func(namespace, name string, obj interface{}) { got = append(got, kind+":"+namespace+"/"+name) }
	}
	kw.addWatch("ns1", "p1", &K8sResourceWatchCallback{Name: "the-watcher", OnAdd: record("add"), OnUpdate: record("update"), OnDelete: record("delete")})
	kw.notifyWatchers("ns1", "p1", nil, true, false, false)
	kw.notifyWatchers("ns1", "p1", nil, false, true, false)
	kw.notifyWatchers("ns1", "p1", nil, false, false, true)
	want := "[add:ns1/p1 update:ns1/p1 delete:ns1/p1]"
	if fmt.Sprint(got) != want {
		t.Errorf("watcher was told %v, want %s", got, want)
	}
}

// Callbacks of a watcher are all optional, and a change that one is not set for was a nil function call.
func TestWatcherWithoutACallbackIsNotCalledThrough(t *testing.T) {
	kw := newWatches()
	kw.addWatch("ns1", "p1", &K8sResourceWatchCallback{Name: "none"})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("telling a watcher that has no callbacks panicked: %v", r)
		}
	}()
	kw.notifyWatchers("ns1", "p1", nil, true, false, false)
	kw.notifyWatchers("ns1", "p1", nil, false, true, false)
	kw.notifyWatchers("ns1", "p1", nil, false, false, true)
}

// What an informer reports is read for its name by the notifier, which is a goroutine that is there for as long as the
// server is. It took the metadata of an object to have a namespace, which a resource that is not in one does not.
func TestNotifierDoesNotCrashOnAResourceThatIsNotInANamespace(t *testing.T) {
	told := make(chan string, 1)
	w := &K8sResourceWatchCallback{Name: "cluster-scoped", OnAdd: func(namespace, name string, obj interface{}) { told <- namespace + "/" + name }}
	k8sResourceWatch.addWatch("", "cluster-thing", w)
	t.Cleanup(func() { k8sResourceWatch.removeWatch("", "cluster-thing", w) })
	k8sResourceWatch.allAdds <- object("v1", "Namespace", "", "cluster-thing")
	select {
	case got := <-told:
		if got != "/cluster-thing" {
			t.Errorf("told %q, want an empty namespace and the name", got)
		}
	case <-after():
		t.Error("a resource that is not in a namespace is not told to its watcher")
	}
}

func TestNotifierGoesOnAfterAnObjectThatItCannotRead(t *testing.T) {
	told := make(chan string, 1)
	w := &K8sResourceWatchCallback{Name: "after-junk", OnAdd: func(namespace, name string, obj interface{}) { told <- name }}
	k8sResourceWatch.addWatch("ns1", "good", w)
	t.Cleanup(func() { k8sResourceWatch.removeWatch("ns1", "good", w) })
	k8sResourceWatch.allAdds <- "this is not a resource"
	k8sResourceWatch.allAdds <- object("v1", "ConfigMap", "ns1", "")
	k8sResourceWatch.allAdds <- object("v1", "ConfigMap", "ns1", "good")
	select {
	case got := <-told:
		if got != "good" {
			t.Errorf("told %q, want good", got)
		}
	case <-after():
		t.Error("the notifier is not telling after it was given what it cannot read")
	}
}

// Ids of resources come in from requests, and are cut at the slashes without checking how many there are.
func TestResourceIdsThatAreNotCompleteAreRejectedAndNotPanicked(t *testing.T) {
	newEnv(t)
	for _, id := range []string{"", "v1", "/v1", "apps/v1"} {
		t.Run("id "+id, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked: %v", r)
				}
			}()
			if j, err := GetResourceByID(id); err == nil || j != nil {
				t.Errorf("got %v, %v, want an error", j, err)
			}
			w := &K8sResourceWatchCallback{Name: "bad"}
			WatchResourceById(id, w)
			UnwatchResourceById(id, w)
		})
	}
}

func TestResourceIdsAreOfThePluralsAndShortNamesThatNamesAreOf(t *testing.T) {
	api := newEnv(t)
	api.Put(obj("ConfigMap", "ns1", "cm1", nil))
	api.Put(obj("Pod", "ns1", "p1", nil))
	for _, id := range []string{"/v1/configmaps/ns1/cm1", "/v1/cm/ns1/cm1", "/v1/pods/ns1/p1"} {
		if j, err := GetResourceByID(id); err != nil || j == nil {
			t.Errorf("%s: got %v, %v, want the resource", id, jsonOf(j), err)
		}
	}
}

// A namespace is not in a namespace, so asking for the one of a name gives the name as the namespace to the lookup.
func TestNamespaceOfANameIsGotAndNotAllNamespaces(t *testing.T) {
	api := newEnv(t)
	api.Put(obj("Namespace", "", "ns1", nil))
	api.Put(obj("Namespace", "", "ns2", nil))
	_, j, err := GetResource("namespaces", "", "ns1", nil, nil, nil)
	if err != nil || j == nil {
		t.Fatalf("got %v, %v", jsonOf(j), err)
	}
	if text := jsonOf(j); !strings.Contains(text, "ns1") || strings.Contains(text, "ns2") {
		t.Errorf("got %s, want the namespace of the name and not a list of all of them", text)
	}
	if kind, namespace, name := reinterpretResource("namespace", "", "ns1"); kind != "Namespace" || namespace != "" || name != "ns1" {
		t.Errorf("reinterpreted as %q %q %q, want the name to be kept", kind, namespace, name)
	}
}

func TestWatchingWithoutAClientIsNotAPanic(t *testing.T) {
	old := k8sClient.Client
	k8sClient.Client = nil
	t.Cleanup(func() { k8sClient.Client = old })
	w := &K8sResourceWatchCallback{Name: "no-client"}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("watching without a client panicked: %v", r)
		}
	}()
	WatcheResource("", "v1", "configmaps", "ns1", "cm1", w)
	UnwatchResource("", "v1", "configmaps", "ns1", "cm1", w)
}

// What the store gives back was its own maps, that it goes on to change when more YAML is stored.
func TestStoringYamlWhileTheStoreIsReadIsSafe(t *testing.T) {
	newEnv(t)
	storeYaml(t, "unit", sampleYaml)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			y := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm-%d\n  namespace: ns1\n", i%20)
			if _, errs := Kstore.StoreYaml("unit", io.NopCloser(strings.NewReader(y))); len(errs) > 0 {
				t.Error(errs)
				return
			}
		}
	}()
	count := 0
	for i := 0; i < 300; i++ {
		if all, ok := Kstore.Get("unit", "", "").(NamespaceKindMap); ok {
			for _, kinds := range all {
				for _, resources := range kinds {
					count += len(resources)
				}
			}
		}
		if byName, ok := Kstore.Get("unit", "ns1", "ConfigMap").(ResourcesByName); ok {
			count += len(byName)
		}
		if targets, err := Kstore.GetTargetResources("unit", "ns1", "ConfigMap", ""); err == nil {
			count += len(targets["ns1"]["ConfigMap"])
		}
	}
	close(stop)
	wg.Wait()
	if count == 0 {
		t.Error("nothing was read")
	}
}

// A namespace that was cleared was left in the store as an entry without resources, which was listed.
func TestClearedNamespaceAndUnitAreNotListed(t *testing.T) {
	newEnv(t)
	storeYaml(t, "unit", sampleYaml)
	Kstore.Clear("unit", "ns1")
	all, ok := Kstore.Get("unit", "", "").(NamespaceKindMap)
	if !ok {
		t.Fatalf("get of the unit = %v", Kstore.Get("unit", "", ""))
	}
	if _, listed := all["ns1"]; listed {
		t.Errorf("namespaces = %v, want the cleared one not to be listed", names(all))
	}
	Kstore.Clear("unit", "")
	Kstore.lock.Lock()
	_, kept := Kstore.YamlResources["unit"]
	Kstore.lock.Unlock()
	if kept {
		t.Error("a unit that was cleared is kept in the store")
	}
}
