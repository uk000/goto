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
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

func TestK8sCache(t *testing.T) {
	c := &K8sCache{Cache: map[string]map[string]interface{}{}}
	if c.Get("ns", "a") != nil {
		t.Error("an empty cache has something")
	}
	c.Store("ns", "a", 1)
	c.Store("ns", "b", 2)
	c.Store("other", "a", 3)
	if c.Get("ns", "a") != 1 || c.Get("ns", "b") != 2 || c.Get("other", "a") != 3 {
		t.Error("what was stored is not what is read")
	}
	c.Store("ns", "a", 10)
	if c.Get("ns", "a") != 10 {
		t.Error("a stored value is not replaced")
	}
	if c.Get("ns", "missing") != nil || c.Get("missing", "a") != nil {
		t.Error("something was found that was not stored")
	}
	c.Clear()
	if c.Get("ns", "a") != nil || c.Get("other", "a") != nil {
		t.Error("the cache has values after it was cleared")
	}
	c.Store("ns", "a", 4)
	if c.Get("ns", "a") != 4 {
		t.Error("the cache cannot be used after it was cleared")
	}
}

func TestK8sCacheConcurrentUse(t *testing.T) {
	c := &K8sCache{Cache: map[string]map[string]interface{}{}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Store("ns", "a", j)
				c.Get("ns", "a")
				if j%50 == 0 {
					c.Clear()
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestObjectNamespaceAndName(t *testing.T) {
	pod := object("v1", "Pod", "ns1", "p1")
	node := object("v1", "Node", "", "n1")
	var nilObject *unstructured.Unstructured
	tests := []struct {
		name  string
		obj   interface{}
		ns, n string
		ok    bool
	}{
		{"resource", pod, "ns1", "p1", true},
		{"resource that is not in a namespace", node, "", "n1", true},
		{"tombstone of a resource", cache.DeletedFinalStateUnknown{Key: "ns1/p1", Obj: pod}, "ns1", "p1", true},
		{"tombstone of what is not a resource", cache.DeletedFinalStateUnknown{Key: "ns1/p1", Obj: "text"}, "", "", false},
		{"resource without a name", object("v1", "Pod", "ns1", ""), "", "", false},
		{"nil resource", nilObject, "", "", false},
		{"nil", nil, "", "", false},
		{"text", "text", "", "", false},
		{"other type", 42, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, n, ok := objectNamespaceAndName(tt.obj)
			if ok != tt.ok || (ok && (ns != tt.ns || n != tt.n)) {
				t.Errorf("got %q %q %t, want %q %q %t", ns, n, ok, tt.ns, tt.n, tt.ok)
			}
		})
	}
}

// call is what a watcher is told of a change.
type call struct {
	kind, namespace, name string
	obj                   interface{}
}

type recorder struct {
	mu    sync.Mutex
	calls []call
}

func (r *recorder) record(kind string) func(namespace, name string, obj interface{}) {
	return func(namespace, name string, obj interface{}) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, call{kind, namespace, name, obj})
	}
}

func (r *recorder) get() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]call{}, r.calls...)
}

func (r *recorder) callback(name string) *K8sResourceWatchCallback {
	return &K8sResourceWatchCallback{Name: name, OnAdd: r.record("add"), OnUpdate: r.record("update"), OnDelete: r.record("delete")}
}

func newWatches() *K8sResourceWatch {
	return &K8sResourceWatch{watches: map[string]map[string]map[string]*K8sResourceWatchCallback{}}
}

func TestNotifyWatchers(t *testing.T) {
	obj := object("v1", "Pod", "ns1", "p1")
	t.Run("each kind of change goes to the callback of it", func(t *testing.T) {
		kw, rec := newWatches(), &recorder{}
		kw.addWatch("ns1", "p1", rec.callback("w"))
		kw.notifyWatchers("ns1", "p1", obj, true, false, false)
		kw.notifyWatchers("ns1", "p1", obj, false, true, false)
		kw.notifyWatchers("ns1", "p1", obj, false, false, true)
		calls := rec.get()
		if len(calls) != 3 {
			t.Fatalf("calls = %v, want 3", calls)
		}
		for i, kind := range []string{"add", "update", "delete"} {
			if c := calls[i]; c.kind != kind || c.namespace != "ns1" || c.name != "p1" || c.obj != interface{}(obj) {
				t.Errorf("call %d = %+v, want %s of ns1/p1 with the object", i, c, kind)
			}
		}
	})
	t.Run("every watcher of the resource is told", func(t *testing.T) {
		kw, rec1, rec2 := newWatches(), &recorder{}, &recorder{}
		kw.addWatch("ns1", "p1", rec1.callback("w1"))
		kw.addWatch("ns1", "p1", rec2.callback("w2"))
		kw.notifyWatchers("ns1", "p1", obj, true, false, false)
		if len(rec1.get()) != 1 || len(rec2.get()) != 1 {
			t.Errorf("watchers were told %d and %d times, want once each", len(rec1.get()), len(rec2.get()))
		}
	})
	t.Run("a watcher that is added again by its name replaces the earlier", func(t *testing.T) {
		kw, rec1, rec2 := newWatches(), &recorder{}, &recorder{}
		kw.addWatch("ns1", "p1", rec1.callback("w"))
		kw.addWatch("ns1", "p1", rec2.callback("w"))
		kw.notifyWatchers("ns1", "p1", obj, true, false, false)
		if len(rec1.get()) != 0 || len(rec2.get()) != 1 {
			t.Errorf("watchers were told %d and %d times, want the later only", len(rec1.get()), len(rec2.get()))
		}
	})
	t.Run("a watcher of another resource is not told", func(t *testing.T) {
		kw, rec := newWatches(), &recorder{}
		kw.addWatch("ns1", "p2", rec.callback("w"))
		kw.addWatch("ns2", "p1", rec.callback("w"))
		kw.notifyWatchers("ns1", "p1", obj, true, false, false)
		if len(rec.get()) != 0 {
			t.Errorf("calls = %v, want none", rec.get())
		}
	})
	t.Run("a watch without a namespace is of the name in any namespace", func(t *testing.T) {
		kw, rec := newWatches(), &recorder{}
		kw.addWatch("", "p1", rec.callback("w"))
		kw.notifyWatchers("ns7", "p1", obj, true, false, false)
		calls := rec.get()
		if len(calls) != 1 || calls[0].namespace != "ns7" || calls[0].name != "p1" {
			t.Errorf("calls = %+v, want one that tells the namespace and the name of the object", calls)
		}
	})
	t.Run("a watch of the namespace wins over a watch without one", func(t *testing.T) {
		kw, specific, general := newWatches(), &recorder{}, &recorder{}
		kw.addWatch("ns1", "p1", specific.callback("s"))
		kw.addWatch("", "p1", general.callback("g"))
		kw.notifyWatchers("ns1", "p1", obj, true, false, false)
		if len(specific.get()) != 1 || len(general.get()) != 0 {
			t.Errorf("told %d and %d times, want the watch of the namespace only", len(specific.get()), len(general.get()))
		}
	})
	t.Run("callbacks that are not set are skipped", func(t *testing.T) {
		kw, rec := newWatches(), &recorder{}
		kw.addWatch("ns1", "p1", &K8sResourceWatchCallback{Name: "none"})
		kw.addWatch("ns1", "p1", &K8sResourceWatchCallback{Name: "add only", OnAdd: rec.record("add")})
		kw.notifyWatchers("ns1", "p1", obj, true, false, false)
		kw.notifyWatchers("ns1", "p1", obj, false, true, false)
		kw.notifyWatchers("ns1", "p1", obj, false, false, true)
		if calls := rec.get(); len(calls) != 1 || calls[0].kind != "add" {
			t.Errorf("calls = %+v, want the add only", calls)
		}
	})
	t.Run("nobody is watching", func(t *testing.T) {
		newWatches().notifyWatchers("ns1", "p1", obj, true, true, true)
	})
}

func TestAddAndRemoveWatch(t *testing.T) {
	kw, rec := newWatches(), &recorder{}
	w1, w2 := rec.callback("w1"), rec.callback("w2")
	kw.addWatch("ns1", "p1", w1)
	kw.addWatch("ns1", "p1", w2)
	kw.addWatch("ns1", "p2", w1)
	kw.removeWatch("ns1", "p1", w1)
	if kw.watches["ns1"]["p1"]["w2"] == nil || kw.watches["ns1"]["p1"]["w1"] != nil || kw.watches["ns1"]["p2"]["w1"] == nil {
		t.Errorf("watches = %v, want only w1 of p1 removed", kw.watches)
	}
	kw.removeWatch("ns1", "p1", w1)
	kw.removeWatch("nothing", "p1", w1)
	kw.removeWatch("ns1", "nothing", w1)
	kw.removeWatch("ns1", "p1", w2)
	if kw.watches["ns1"]["p1"] != nil {
		t.Error("the resource is still watched after the last watcher was removed")
	}
	kw.removeWatch("ns1", "p2", w1)
	if len(kw.watches) != 0 {
		t.Errorf("watches = %v, want none: the namespace is not kept without watches", kw.watches)
	}
}

func TestAddAndRemoveWatchConcurrently(t *testing.T) {
	kw, rec := newWatches(), &recorder{}
	obj := object("v1", "Pod", "ns1", "p1")
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := rec.callback("w")
			for j := 0; j < 200; j++ {
				kw.addWatch("ns1", "p1", w)
				kw.notifyWatchers("ns1", "p1", obj, true, false, false)
				kw.removeWatch("ns1", "p1", w)
			}
		}()
	}
	wg.Wait()
}

// watchNotified registers a watcher for the name, and waits for the notifier to tell it of a change that is put on the
// channel of the changes.
func TestNotifierTellsWatchersOfWhatInformersReport(t *testing.T) {
	rec := &recorder{}
	w := rec.callback("notifier-test")
	k8sResourceWatch.addWatch("ns1", "watched", w)
	t.Cleanup(func() { k8sResourceWatch.removeWatch("ns1", "watched", w) })
	obj := object("v1", "ConfigMap", "ns1", "watched")
	k8sResourceWatch.allAdds <- obj
	k8sResourceWatch.allUpdates <- obj
	k8sResourceWatch.allDeletes <- cache.DeletedFinalStateUnknown{Key: "ns1/watched", Obj: obj}
	waitFor(t, "the three changes to be told", func() bool { return len(rec.get()) == 3 })
	got := map[string]call{}
	for _, c := range rec.get() {
		got[c.kind] = c
	}
	for _, kind := range []string{"add", "update", "delete"} {
		if c, ok := got[kind]; !ok || c.namespace != "ns1" || c.name != "watched" {
			t.Errorf("%s = %+v, want it to be told for ns1/watched", kind, c)
		}
	}
}

func TestStopWatchStopsTheNotifier(t *testing.T) {
	rec := &recorder{}
	w := rec.callback("stop-test")
	k8sResourceWatch.addWatch("ns1", "stopped", w)
	t.Cleanup(func() { k8sResourceWatch.removeWatch("ns1", "stopped", w) })
	StopWatch()
	// The notifier takes the signal as soon as it is not busy. After that nobody is reading the changes.
	waitFor(t, "the notifier to take the signal", func() bool { return len(k8sWatchStopChannel) == 0 })
	t.Cleanup(func() { go runWatchNotifier() })
	time.Sleep(20 * time.Millisecond)
	k8sResourceWatch.allAdds <- object("v1", "ConfigMap", "ns1", "stopped")
	time.Sleep(50 * time.Millisecond)
	if calls := rec.get(); len(calls) != 0 {
		t.Errorf("calls = %+v, want a stopped notifier to tell nobody", calls)
	}
	// A notifier that is started again takes what was left.
	go runWatchNotifier()
	waitFor(t, "the notifier that was started again to tell", func() bool { return len(rec.get()) == 1 })
	// That one is the one to stop with the cleanup of the test, as the cleanup above starts another.
	StopWatch()
	waitFor(t, "the notifier to take the signal", func() bool { return len(k8sWatchStopChannel) == 0 })
}

func TestWatchResourceById(t *testing.T) {
	newEnv(t)
	rec := &recorder{}
	w := rec.callback("by-id")
	reset := func() {
		k8sResourceWatch.lock.Lock()
		k8sResourceWatch.watches = map[string]map[string]map[string]*K8sResourceWatchCallback{}
		k8sResourceWatch.lock.Unlock()
	}
	reset()
	t.Cleanup(reset)
	watching := func(ns, name string) bool {
		k8sResourceWatch.lock.RLock()
		defer k8sResourceWatch.lock.RUnlock()
		return k8sResourceWatch.watches[ns][name]["by-id"] != nil
	}
	WatchResourceById("/v1/configmaps/ns1/cm1", w)
	if !watching("ns1", "cm1") {
		t.Error("the config map is not watched")
	}
	UnwatchResourceById("/v1/configmaps/ns1/cm1", w)
	if watching("ns1", "cm1") {
		t.Error("the config map is still watched")
	}
	// A namespace is watched by its name, and the id may tell the name for the namespace.
	WatchResourceById("/v1/namespaces/ns1", w)
	if !watching("", "ns1") {
		t.Errorf("the namespace is not watched: %v", k8sResourceWatch.watches)
	}
	UnwatchResourceById("/v1/namespaces/ns1", w)
	if watching("", "ns1") {
		t.Error("the namespace is still watched")
	}
	WatcheResource("apps", "v1", "deployments", "ns2", "web", w)
	if !watching("ns2", "web") {
		t.Error("the deployment is not watched")
	}
	UnwatchResource("apps", "v1", "deployments", "ns2", "web", w)
	if watching("ns2", "web") {
		t.Error("the deployment is still watched")
	}
}

func TestWatchWithAnIdThatIsNotOne(t *testing.T) {
	newEnv(t)
	w := (&recorder{}).callback("bad-id")
	for _, id := range []string{"", "v1", "/v1", "apps/v1"} {
		WatchResourceById(id, w)
		UnwatchResourceById(id, w)
	}
	k8sResourceWatch.lock.RLock()
	defer k8sResourceWatch.lock.RUnlock()
	for ns, byName := range k8sResourceWatch.watches {
		for name, ws := range byName {
			if ws["bad-id"] != nil {
				t.Errorf("a watcher was added for [%s/%s] by an id that is not one", ns, name)
			}
		}
	}
}

func TestResourceByID(t *testing.T) {
	api := newEnv(t)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm1", "namespace": "ns1"}, "data": map[string]interface{}{"k": "v"}})
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]interface{}{"name": "p1", "namespace": "ns1"}})
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": "ns1"}})
	api.Put(map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]interface{}{"name": "web", "namespace": "ns2"}})
	for _, c := range []struct{ id, want string }{
		{"/v1/configmaps/ns1/cm1", `"cm1"`},
		{"/v1/configmap/ns1/cm1", `"cm1"`},
		{"/v1/cm/ns1/cm1", `"cm1"`},
		{"/v1/pods/ns1/p1", `"p1"`},
		{"/v1/pod/ns1/p1", `"p1"`},
		{"/v1/namespaces/ns1", `"ns1"`},
		{"/v1/ns/ns1", `"ns1"`},
		{"apps/v1/deployments/ns2/web", `"web"`},
	} {
		t.Run(c.id, func(t *testing.T) {
			j, err := GetResourceByID(c.id)
			if err != nil || j == nil || !strings.Contains(jsonOf(j), c.want) {
				t.Errorf("got %q, %v, want the resource with %s", jsonOf(j), err, c.want)
			}
		})
	}
	t.Run("not there", func(t *testing.T) {
		if j, err := GetResourceByID("/v1/configmaps/ns1/missing"); j != nil || err != nil {
			t.Errorf("got %v, %v, want nothing and no error", j, err)
		}
	})
	t.Run("list", func(t *testing.T) {
		j, err := GetResourceByID("/v1/configmaps/ns1")
		if err != nil || j == nil || !strings.Contains(jsonOf(j), "cm1") {
			t.Errorf("got %q, %v", jsonOf(j), err)
		}
	})
	for _, id := range []string{"", "v1", "/v1", "apps/v1"} {
		t.Run("invalid "+id, func(t *testing.T) {
			if j, err := GetResourceByID(id); err == nil || j != nil {
				t.Errorf("got %v, %v, want an error", j, err)
			}
		})
	}
}

func TestGetResource(t *testing.T) {
	api := newEnv(t)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm1", "namespace": "ns1", "labels": map[string]interface{}{"app": "a"}}, "data": map[string]interface{}{"k": "v"}})
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm2", "namespace": "ns1"}})
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm3", "namespace": "ns2"}})

	t.Run("one resource", func(t *testing.T) {
		gvk, j, err := GetResource("configmaps", "ns1", "cm1", nil, nil, nil)
		if err != nil || j == nil || gvk == nil {
			t.Fatalf("got %v, %v, %v", gvk, j, err)
		}
		if gvk.Group != "" || gvk.Version != "v1" || gvk.Kind != "ConfigMap" {
			t.Errorf("gvk = %v", gvk)
		}
		if got := j.GetText("metadata.name"); got != "cm1" {
			t.Errorf("name = %q in %s", got, jsonOf(j))
		}
		if got := j.GetText("data.k"); got != "v" {
			t.Errorf("data = %q in %s", got, jsonOf(j))
		}
	})
	t.Run("kind in any of its names", func(t *testing.T) {
		for _, kind := range []string{"configmaps", "configmap", "cm"} {
			if _, j, err := GetResource(kind, "ns1", "cm2", nil, nil, nil); err != nil || j == nil {
				t.Errorf("kind %q: got %v, %v", kind, j, err)
			}
		}
	})
	t.Run("list of a namespace", func(t *testing.T) {
		_, j, err := GetResource("cm", "ns1", "", nil, nil, nil)
		if err != nil || j == nil {
			t.Fatalf("got %v, %v", j, err)
		}
		text := jsonOf(j)
		if !strings.Contains(text, "cm1") || !strings.Contains(text, "cm2") || strings.Contains(text, "cm3") {
			t.Errorf("list = %s, want the config maps of ns1", text)
		}
	})
	t.Run("not found is not an error", func(t *testing.T) {
		gvk, j, err := GetResource("cm", "ns1", "nope", nil, nil, nil)
		if err != nil || j != nil || gvk == nil {
			t.Errorf("got %v, %v, %v, want the gvk, no resource and no error", gvk, j, err)
		}
	})
	t.Run("kind that is not known", func(t *testing.T) {
		gvk, j, err := GetResource("notakind", "ns1", "x", nil, nil, nil)
		if gvk != nil || j != nil || err == nil || !apierrors.IsBadRequest(err) {
			t.Errorf("got %v, %v, %v, want a bad request", gvk, j, err)
		}
	})
	t.Run("json path", func(t *testing.T) {
		jp := util.NewJSONPath().Parse([]string{"name={.metadata.name}"})
		_, j, err := GetResource("cm", "ns1", "cm1", jp, nil, nil)
		if err != nil || j == nil || !strings.Contains(jsonOf(j), "cm1") || strings.Contains(jsonOf(j), "apiVersion") {
			t.Errorf("got %q, %v, want what the path selects", jsonOf(j), err)
		}
	})
	t.Run("jq", func(t *testing.T) {
		jq := util.NewJQ().Parse([]string{"name=.metadata.name"})
		_, j, err := GetResource("cm", "ns1", "cm1", nil, jq, nil)
		if err != nil || j == nil || j.GetText("name") != "cm1" {
			t.Errorf("got %q, %v, want what the query selects", jsonOf(j), err)
		}
	})
	t.Run("json path comes before jq", func(t *testing.T) {
		jp := util.NewJSONPath().Parse([]string{"p={.metadata.name}"})
		jq := util.NewJQ().Parse([]string{"q=.metadata.name"})
		_, j, err := GetResource("cm", "ns1", "cm1", jp, jq, nil)
		if err != nil || j == nil || !strings.Contains(j.GetText("p"), "cm1") || j.GetText("q") != "" {
			t.Errorf("got %q, %v, want the json path only", jsonOf(j), err)
		}
	})
	t.Run("empty json path and jq are not applied", func(t *testing.T) {
		_, j, err := GetResource("cm", "ns1", "cm1", util.NewJSONPath(), util.NewJQ(), nil)
		if err != nil || j == nil || j.GetText("metadata.name") != "cm1" {
			t.Errorf("got %q, %v, want the whole resource", jsonOf(j), err)
		}
	})
}

func TestGetResourceIsServedFromCache(t *testing.T) {
	api := newEnv(t)
	cm := map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm1", "namespace": "ns1"}, "data": map[string]interface{}{"k": "old"}}
	api.Put(cm)
	if _, j, err := GetResource("cm", "ns1", "cm1", nil, nil, nil); err != nil || j.GetText("data.k") != "old" {
		t.Fatalf("got %q, %v", jsonOf(j), err)
	}
	api.ClearRequests()
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm1", "namespace": "ns1"}, "data": map[string]interface{}{"k": "new"}})
	_, j, err := GetResource("cm", "ns1", "cm1", nil, nil, nil)
	if err != nil || j.GetText("data.k") != "old" {
		t.Errorf("got %q, %v, want the cached resource", jsonOf(j), err)
	}
	if reqs := api.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %v, want none for a resource that is cached", reqs)
	}
	// Of a cached resource a path is applied to what was cached, without changing it.
	jp := util.NewJSONPath().Parse([]string{"k={.data.k}"})
	if _, j, _ := GetResource("cm", "ns1", "cm1", jp, nil, nil); j == nil || !strings.Contains(jsonOf(j), "old") {
		t.Errorf("got %q", jsonOf(j))
	}
	if _, j, _ := GetResource("cm", "ns1", "cm1", nil, nil, nil); j == nil || j.GetText("data.k") != "old" {
		t.Errorf("got %q, want the cached resource to be left as it was", jsonOf(j))
	}
	Cache.Clear()
	if _, j, err := GetResource("cm", "ns1", "cm1", nil, nil, nil); err != nil || j.GetText("data.k") != "new" {
		t.Errorf("got %q, %v, want the resource that is fetched again after the cache was cleared", jsonOf(j), err)
	}
	if len(api.Requests()) != 1 {
		t.Errorf("requests = %v, want one", api.Requests())
	}
}

func TestGetResourceListIsNotCached(t *testing.T) {
	api := newEnv(t)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm1", "namespace": "ns1"}})
	GetResource("cm", "ns1", "", nil, nil, nil)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm2", "namespace": "ns1"}})
	_, j, err := GetResource("cm", "ns1", "", nil, nil, nil)
	if err != nil || !strings.Contains(jsonOf(j), "cm2") {
		t.Errorf("got %q, %v, want a list that has what was added", jsonOf(j), err)
	}
}

func TestGetResourceWhenTheServerFails(t *testing.T) {
	api := newEnv(t)
	api.FailResources(http.StatusInternalServerError)
	gvk, j, err := GetResource("cm", "ns1", "cm1", nil, nil, nil)
	if err == nil || j != nil || gvk == nil {
		t.Errorf("got %v, %v, %v, want an error", gvk, j, err)
	}
	if _, j, err := GetResource("cm", "ns1", "", nil, nil, nil); err == nil || j != nil {
		t.Errorf("list: got %v, %v, want an error", j, err)
	}
	api.FailResources(0)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "cm1", "namespace": "ns1"}})
	if _, j, err := GetResource("cm", "ns1", "cm1", nil, nil, nil); err != nil || j == nil {
		t.Errorf("got %v, %v, want a failure not to be remembered", j, err)
	}
}

func TestGetResourceWithoutAClient(t *testing.T) {
	old := k8sClient.Client
	k8sClient.Client = nil
	t.Cleanup(func() { k8sClient.Client = old })
	Cache.Clear()
	if _, j, err := GetResource("cm", "ns1", "cm1", nil, nil, nil); err == nil || j != nil {
		t.Errorf("got %v, %v, want an error", j, err)
	}
	if j, err := GetResourceByID("/v1/configmaps/ns1/cm1"); err == nil || j != nil {
		t.Errorf("by id: got %v, %v, want an error", j, err)
	}
	// Watching needs a client too, and a watcher is still added for when there is one.
	w := (&recorder{}).callback("no-client")
	WatcheResource("", "v1", "configmaps", "ns1", "cm1", w)
	UnwatchResource("", "v1", "configmaps", "ns1", "cm1", w)
}
