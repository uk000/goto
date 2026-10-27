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
	"context"
	"strings"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/k8stest"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

func configMap(namespace, name, value string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace},
		"data":     map[string]interface{}{"k": value},
	}
}

var configMapGVK = &schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}

func informerCount() int {
	informersLock.Lock()
	defer informersLock.Unlock()
	return len(informers)
}

// watching starts a watch of the config map with a watcher that records what it is told, which ends with the test.
func watching(t *testing.T, name, namespace, cm string) *recorder {
	t.Helper()
	rec := &recorder{}
	w := rec.callback(name)
	WatcheResource("", "v1", "configmaps", namespace, cm, w)
	t.Cleanup(func() { UnwatchResource("", "v1", "configmaps", namespace, cm, w) })
	return rec
}

func told(rec *recorder, kind string) bool {
	for _, c := range rec.get() {
		if c.kind == kind {
			return true
		}
	}
	return false
}

func TestWatchTellsWhatTheClusterHasAndWhatChanges(t *testing.T) {
	api := newEnv(t)
	api.Put(configMap("ns1", "cm1", "one"))
	rec := watching(t, "informer", "ns1", "cm1")

	waitFor(t, "the add of the config map that exists", func() bool { return told(rec, "add") })
	api.Put(configMap("ns1", "cm1", "two"))
	waitFor(t, "the update of the config map", func() bool { return told(rec, "update") })
	api.Delete("v1", "ConfigMap", "ns1", "cm1")
	waitFor(t, "the delete of the config map", func() bool { return told(rec, "delete") })

	for _, c := range rec.get() {
		if c.namespace != "ns1" || c.name != "cm1" {
			t.Errorf("told of %+v, want only ns1/cm1", c)
		}
	}
}

// The informer was of the name as the namespace and of the namespace as the name, and of the Kind that is not the name that
// the API has for the resources, which no server has.
func TestWatchAsksTheServerForTheResourceOfTheNamespaceAndName(t *testing.T) {
	api := newEnv(t)
	watching(t, "informer", "ns1", "cm1")
	waitFor(t, "the watch request", func() bool { return len(api.WatchRequests()) > 0 })
	for _, req := range api.WatchRequests() {
		if req.Path != "/api/v1/namespaces/ns1/configmaps" {
			t.Errorf("watched %s, want the config maps of ns1", req.Path)
		}
		if !strings.Contains(req.RawQuery, "fieldSelector=metadata.name%3Dcm1") {
			t.Errorf("query = %q, want the name of the config map as the field selector", req.RawQuery)
		}
	}
}

func TestWatchByTheNamesThatResourcesHave(t *testing.T) {
	api := newEnv(t)
	api.Put(configMap("ns1", "cm1", "one"))
	for _, resource := range []string{"configmaps", "configmap", "ConfigMap"} {
		rec := &recorder{}
		w := rec.callback("by-" + resource)
		WatcheResource("", "v1", resource, "ns1", "cm1", w)
		waitFor(t, "the add through "+resource, func() bool { return told(rec, "add") })
		UnwatchResource("", "v1", resource, "ns1", "cm1", w)
	}
}

func TestUnwatchStopsTheInformerWithTheLastWatcher(t *testing.T) {
	api := newEnv(t)
	api.Put(configMap("ns1", "cm1", "one"))
	first, second := &recorder{}, &recorder{}
	w1, w2 := first.callback("w1"), second.callback("w2")
	WatcheResource("", "v1", "configmaps", "ns1", "cm1", w1)
	WatcheResource("", "v1", "configmaps", "ns1", "cm1", w2)
	t.Cleanup(func() {
		UnwatchResource("", "v1", "configmaps", "ns1", "cm1", w1)
		UnwatchResource("", "v1", "configmaps", "ns1", "cm1", w2)
	})
	waitFor(t, "both watchers to be told", func() bool { return told(first, "add") && told(second, "add") })
	waitFor(t, "the watch on the server", func() bool { return api.Watchers() == 1 })
	if n := informerCount(); n != 1 {
		t.Errorf("informers = %d, want one for the two watchers of the same resource", n)
	}

	UnwatchResource("", "v1", "configmaps", "ns1", "cm1", w1)
	time.Sleep(50 * time.Millisecond)
	if api.Watchers() != 1 || informerCount() != 1 {
		t.Errorf("watches on the server = %d, informers = %d, want the informer to run while there is a watcher", api.Watchers(), informerCount())
	}
	UnwatchResource("", "v1", "configmaps", "ns1", "cm1", w2)
	waitFor(t, "the informer to stop", func() bool { return informerCount() == 0 })
	waitFor(t, "the watch on the server to end", func() bool { return api.Watchers() == 0 })
}

func TestWatchersOfOtherResourcesDoNotShareAnInformer(t *testing.T) {
	newEnv(t)
	watching(t, "a", "ns1", "cm1")
	watching(t, "b", "ns1", "cm2")
	watching(t, "c", "ns2", "cm1")
	waitFor(t, "the informers", func() bool { return informerCount() == 3 })
}

func TestWatchWithoutANameStartsNothing(t *testing.T) {
	api := newEnv(t)
	rec := &recorder{}
	w := rec.callback("nameless")
	WatcheResource("", "v1", "configmaps", "ns1", "", w)
	t.Cleanup(func() { UnwatchResource("", "v1", "configmaps", "ns1", "", w) })
	time.Sleep(50 * time.Millisecond)
	if informerCount() != 0 || len(api.WatchRequests()) != 0 {
		t.Errorf("informers = %d, watch requests = %d, want none for a watch that no change is told to", informerCount(), len(api.WatchRequests()))
	}
}

func TestWatchOfAKindTheClusterDoesNotHaveStartsNothing(t *testing.T) {
	newEnv(t)
	rec := &recorder{}
	w := rec.callback("unknown")
	WatcheResource("nothing.example.com", "v1", "widgets", "ns1", "w1", w)
	t.Cleanup(func() { UnwatchResource("nothing.example.com", "v1", "widgets", "ns1", "w1", w) })
	if informerCount() != 0 {
		t.Errorf("informers = %d, want none", informerCount())
	}
}

func TestWatchLastsLongerThanTheTimeoutOfTheClient(t *testing.T) {
	api := k8stest.New(t)
	cfg, err := k8sClient.NewK8sClientForConfig(api.URL(), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Config.Timeout = 200 * time.Millisecond
	c, err := k8sClient.NewK8sClientForRestConfig(cfg.Config)
	if err != nil {
		t.Fatal(err)
	}
	old := k8sClient.Client
	k8sClient.Client = c
	t.Cleanup(func() { k8sClient.Client = old })
	Cache.Clear()

	rec := watching(t, "slow", "ns1", "cm1")
	waitFor(t, "the watch on the server", func() bool { return api.Watchers() == 1 })
	time.Sleep(700 * time.Millisecond)
	api.Put(configMap("ns1", "cm1", "late"))
	waitFor(t, "the change that is after the timeout of the client", func() bool { return told(rec, "add") || told(rec, "update") })
	if api.Watchers() != 1 {
		t.Errorf("watches on the server = %d, want the one that the timeout of the client did not cut", api.Watchers())
	}
}

func TestStopWatchStopsTheInformers(t *testing.T) {
	api := newEnv(t)
	api.Put(configMap("ns1", "cm1", "one"))
	rec := watching(t, "informer", "ns1", "cm1")
	waitFor(t, "the add", func() bool { return told(rec, "add") })

	StopWatch()
	t.Cleanup(func() { go runWatchNotifier() })
	waitFor(t, "the notifier to take the signal", func() bool { return len(k8sWatchStopChannel) == 0 })
	waitFor(t, "the informers to stop", func() bool { return informerCount() == 0 })
	waitFor(t, "the watch on the server to end", func() bool { return api.Watchers() == 0 })
}

func TestCacheExpires(t *testing.T) {
	old := CacheTTL
	t.Cleanup(func() { CacheTTL = old; Cache.Clear() })
	Cache.Clear()
	CacheTTL = 30 * time.Millisecond
	Cache.Store("ns1", "kept", "v")
	// An object that is put in the map without being stored has no time, and is there until the cache is cleared.
	Cache.lock.Lock()
	Cache.Cache["ns1"]["forever"] = "v"
	Cache.lock.Unlock()
	if Cache.Get("ns1", "kept") == nil {
		t.Fatal("the object is not there when it is stored")
	}
	time.Sleep(60 * time.Millisecond)
	if Cache.Get("ns1", "kept") != nil {
		t.Error("the object is still served after its time")
	}
	if Cache.Get("ns1", "forever") == nil {
		t.Error("the object that has no time was dropped")
	}
	Cache.Cache["ns1"] = nil
	Cache.Delete("ns1", "forever")
	Cache.Delete("nothing", "nothing")
}

func TestCacheDeleteForgetsOneObject(t *testing.T) {
	Cache.Clear()
	t.Cleanup(Cache.Clear)
	Cache.Store("ns1", "a", 1)
	Cache.Store("ns1", "b", 2)
	Cache.Delete("ns1", "a")
	if Cache.Get("ns1", "a") != nil || Cache.Get("ns1", "b") == nil {
		t.Error("delete took out the wrong object, or none")
	}
	Cache.Delete("ns1", "b")
	if len(Cache.Cache) != 0 || len(Cache.stored) != 0 {
		t.Errorf("cache = %v, times = %v, want nothing left for the namespace", Cache.Cache, Cache.stored)
	}
}

func TestInvalidateResourceMakesTheNextReadFresh(t *testing.T) {
	api := newEnv(t)
	api.Put(configMap("ns1", "cm1", "old"))
	_, first, err := GetResource("configmaps", "ns1", "cm1", nil, nil, nil)
	if err != nil || !strings.Contains(jsonOf(first), `"old"`) {
		t.Fatalf("first read = %q, %v", jsonOf(first), err)
	}
	api.Put(configMap("ns1", "cm1", "new"))
	if _, again, _ := GetResource("configmaps", "ns1", "cm1", nil, nil, nil); !strings.Contains(jsonOf(again), `"old"`) {
		t.Fatalf("second read = %q, want the cached one", jsonOf(again))
	}
	InvalidateResource(configMapGVK, "ns1", "cm1")
	if _, fresh, _ := GetResource("configmaps", "ns1", "cm1", nil, nil, nil); !strings.Contains(jsonOf(fresh), `"new"`) {
		t.Errorf("read after the invalidation = %q, want the new one", jsonOf(fresh))
	}
}

func TestCachedResourcesAreOfTheClusterThatTheyWereReadFrom(t *testing.T) {
	first := newEnv(t)
	first.Put(configMap("ns1", "cm1", "from-first"))
	second := k8stest.New(t)
	second.Put(configMap("ns1", "cm1", "from-second"))
	secondClient, err := k8sClient.NewK8sClientForConfig(second.URL(), "")
	if err != nil {
		t.Fatal(err)
	}
	read := func() string {
		_, j, err := GetResource("configmaps", "ns1", "cm1", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return jsonOf(j)
	}
	if got := read(); !strings.Contains(got, "from-first") {
		t.Fatalf("read = %s, want the first cluster", got)
	}
	k8sClient.ReplaceCurrentClient(secondClient)
	t.Cleanup(func() { k8sClient.ReplaceCurrentClient(nil) })
	if got := read(); !strings.Contains(got, "from-second") {
		t.Errorf("read after the switch = %s, want the second cluster, and not what is cached of the first", got)
	}
	k8sClient.ReplaceCurrentClient(nil)
	if got := read(); !strings.Contains(got, "from-first") {
		t.Errorf("read after switching back = %s, want the first cluster", got)
	}
}

func TestOpenWatchStreamsTheChangesOfAResource(t *testing.T) {
	api := newEnv(t)
	api.Put(configMap("ns1", "cm1", "one"))
	api.Put(configMap("ns1", "cm2", "other"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gvk, w, err := OpenWatch(ctx, "configmaps", "ns1", "cm1", WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	if gvk.Kind != "ConfigMap" || gvk.Version != "v1" {
		t.Errorf("gvk = %v", gvk)
	}
	next := func() watch.Event {
		t.Helper()
		select {
		case e, ok := <-w.ResultChan():
			if !ok {
				t.Fatal("the watch ended")
			}
			return e
		case <-after():
			t.Fatal("no event")
		}
		return watch.Event{}
	}
	if e := next(); e.Type != watch.Added {
		t.Errorf("first event = %s, want the add of what exists", e.Type)
	}
	api.Put(configMap("ns1", "cm2", "changed"))
	api.Put(configMap("ns1", "cm1", "two"))
	if e := next(); e.Type != watch.Modified {
		t.Errorf("second event = %s, want the change of cm1 and not the one of cm2, which is not watched", e.Type)
	}
	api.Delete("v1", "ConfigMap", "ns1", "cm1")
	if e := next(); e.Type != watch.Deleted {
		t.Errorf("third event = %s, want the delete", e.Type)
	}
}

func TestOpenWatchHasWhatTheServerIsAskedFor(t *testing.T) {
	api := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, w, err := OpenWatch(ctx, "configmaps", "ns1", "cm1", WatchOptions{LabelSelector: "app=x", FieldSelector: "metadata.namespace=ns1", Timeout: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	waitFor(t, "the request", func() bool { return len(api.WatchRequests()) == 1 })
	query := api.WatchRequests()[0].RawQuery
	for _, want := range []string{"labelSelector=app%3Dx", "timeoutSeconds=90", "metadata.namespace%3Dns1%2Cmetadata.name%3Dcm1"} {
		if !strings.Contains(query, want) {
			t.Errorf("query = %q, want it to have %q", query, want)
		}
	}
}

func TestOpenWatchEndsWithItsTimeout(t *testing.T) {
	newEnv(t)
	_, w, err := OpenWatch(context.Background(), "configmaps", "ns1", "", WatchOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-w.ResultChan():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the watch did not end with its timeout")
		}
	}
}

func TestOpenWatchFailures(t *testing.T) {
	api := newEnv(t)
	ctx := context.Background()
	if _, w, err := OpenWatch(ctx, "nothings", "ns1", "", WatchOptions{}); err == nil || w != nil {
		t.Errorf("watch of a kind that is not known = %v, %v, want an error", w, err)
	}
	api.FailResources(500)
	if _, w, err := OpenWatch(ctx, "configmaps", "ns1", "", WatchOptions{}); err == nil || w != nil {
		t.Errorf("watch that the server fails = %v, %v, want an error", w, err)
	}
	api.FailResources(0)
	old := k8sClient.Client
	k8sClient.Client = nil
	t.Cleanup(func() { k8sClient.Client = old })
	if _, w, err := OpenWatch(ctx, "configmaps", "ns1", "", WatchOptions{}); err == nil || w != nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("watch without a client = %v, %v, want an error that there is none", w, err)
	}
}
