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
	"fmt"
	"goto/pkg/global"
	"goto/pkg/k8s"
	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/util"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/dynamicinformer"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/tools/cache"
)

type K8sResourceWatchCallback struct {
	Name     string
	OnAdd    func(namespace, name string, obj interface{})
	OnUpdate func(namespace, name string, obj interface{})
	OnDelete func(namespace, name string, obj interface{})
}

type K8sResourceWatch struct {
	watches    map[string]map[string]map[string]*K8sResourceWatchCallback
	allAdds    chan interface{}
	allUpdates chan interface{}
	allDeletes chan interface{}
	lock       sync.RWMutex
}

type K8sCache struct {
	Cache map[string]map[string]interface{}
	// stored is when each object was stored. An object that has no time (one that was put in Cache directly) is kept
	// until the cache is cleared.
	stored map[string]map[string]time.Time
	lock   sync.RWMutex
}

const (
	CacheRefreshPeriod = 60 * time.Second
)

var (
	// CacheTTL is how long an object that was read from the cluster is served from the cache. Nothing refreshes the cache
	// but the end of this time and what goes through goto (see InvalidateResource), as a change made in the cluster by
	// others is not reported to it.
	CacheTTL = CacheRefreshPeriod

	Cache = &K8sCache{
		Cache: map[string]map[string]interface{}{},
	}
	k8sResourceWatch = &K8sResourceWatch{
		watches:    map[string]map[string]map[string]*K8sResourceWatchCallback{},
		allAdds:    make(chan interface{}, 100),
		allUpdates: make(chan interface{}, 100),
		allDeletes: make(chan interface{}, 100),
	}
	k8sWatchStopChannel = make(chan bool, 1)
	_                   = global.OnShutdown(StopWatch)
)

func init() {
	go runWatchNotifier()
}

// runWatchNotifier hands the changes of the watched resources, which the informers report, to the watchers of those, until
// StopWatch.
func runWatchNotifier() {
	for {
		var obj interface{}
		var isAdd, isUpdate, isDelete bool
		select {
		case <-k8sWatchStopChannel:
			return
		case obj = <-k8sResourceWatch.allAdds:
			isAdd = true
		case obj = <-k8sResourceWatch.allUpdates:
			isUpdate = true
		case obj = <-k8sResourceWatch.allDeletes:
			isDelete = true
		}
		namespace, name, ok := objectNamespaceAndName(obj)
		if !ok {
			log.Printf("K8s: Ignoring a change of an object that has no name: %T\n", obj)
			continue
		}
		log.Printf("K8s: Notifying watchers of [%s/%s]: add [%t] update [%t] delete [%t]\n", namespace, name, isAdd, isUpdate, isDelete)
		k8sResourceWatch.notifyWatchers(namespace, name, obj, isAdd, isUpdate, isDelete)
	}
}

// objectNamespaceAndName reads the namespace and the name of what an informer reports. The namespace is empty for the
// resources that are not in a namespace. What is reported for a delete that the informer saw late is not the resource.
func objectNamespaceAndName(obj interface{}) (namespace, name string, ok bool) {
	if tombstone, isTombstone := obj.(cache.DeletedFinalStateUnknown); isTombstone {
		obj = tombstone.Obj
	}
	u, isUnstructured := obj.(*unstructured.Unstructured)
	if !isUnstructured || u == nil {
		return "", "", false
	}
	return u.GetNamespace(), u.GetName(), u.GetName() != ""
}

// StopWatch stops the notifier of the changes of the watched resources, and the informers that report the changes.
func StopWatch() {
	stopInformers(func(informerKey) bool { return true })
	k8sWatchStopChannel <- true
}

func (k *K8sCache) Clear() {
	k.lock.Lock()
	k.Cache = map[string]map[string]interface{}{}
	k.stored = nil
	k.lock.Unlock()
	runtime.GC()
}

func (k *K8sCache) Store(namespace, name string, obj interface{}) {
	k.lock.Lock()
	defer k.lock.Unlock()
	if k.Cache[namespace] == nil {
		k.Cache[namespace] = map[string]interface{}{}
	}
	k.Cache[namespace][name] = obj
	if k.stored == nil {
		k.stored = map[string]map[string]time.Time{}
	}
	if k.stored[namespace] == nil {
		k.stored[namespace] = map[string]time.Time{}
	}
	k.stored[namespace][name] = time.Now()
}

// Get returns the object that is cached, or nil when there is none or it has been cached for longer than CacheTTL.
func (k *K8sCache) Get(namespace, name string) interface{} {
	k.lock.Lock()
	defer k.lock.Unlock()
	obj := k.Cache[namespace][name]
	if obj == nil {
		return nil
	}
	if at, ok := k.stored[namespace][name]; ok && time.Since(at) > CacheTTL {
		k.remove(namespace, name)
		return nil
	}
	return obj
}

// Delete forgets the object that is cached, if any.
func (k *K8sCache) Delete(namespace, name string) {
	k.lock.Lock()
	defer k.lock.Unlock()
	k.remove(namespace, name)
}

func (k *K8sCache) remove(namespace, name string) {
	delete(k.Cache[namespace], name)
	if len(k.Cache[namespace]) == 0 {
		delete(k.Cache, namespace)
	}
	delete(k.stored[namespace], name)
	if len(k.stored[namespace]) == 0 {
		delete(k.stored, namespace)
	}
}

func (kw *K8sResourceWatch) addWatch(namespace, name string, watcher *K8sResourceWatchCallback) {
	kw.lock.Lock()
	defer kw.lock.Unlock()
	if kw.watches[namespace] == nil {
		kw.watches[namespace] = map[string]map[string]*K8sResourceWatchCallback{}
	}
	if kw.watches[namespace][name] == nil {
		kw.watches[namespace][name] = map[string]*K8sResourceWatchCallback{}
	}
	kw.watches[namespace][name][watcher.Name] = watcher
}

func (kw *K8sResourceWatch) removeWatch(namespace, name string, watcher *K8sResourceWatchCallback) {
	kw.lock.Lock()
	defer kw.lock.Unlock()
	if kw.watches[namespace] != nil && kw.watches[namespace][name] != nil {
		delete(kw.watches[namespace][name], watcher.Name)
		if len(kw.watches[namespace][name]) == 0 {
			delete(kw.watches[namespace], name)
		}
		if len(kw.watches[namespace]) == 0 {
			delete(kw.watches, namespace)
		}
	}
}

func (kw *K8sResourceWatch) notifyWatchers(namespace, name string, obj interface{}, isAdd, isUpdate, isDelete bool) {
	kw.lock.RLock()
	defer kw.lock.RUnlock()
	var watchers map[string]*K8sResourceWatchCallback
	if kw.watches[namespace] != nil && kw.watches[namespace][name] != nil {
		watchers = kw.watches[namespace][name]
	} else if kw.watches[""] != nil && kw.watches[""][name] != nil {
		watchers = kw.watches[""][name]
	}
	for _, watcher := range watchers {
		if isAdd && watcher.OnAdd != nil {
			watcher.OnAdd(namespace, name, obj)
		} else if isUpdate && watcher.OnUpdate != nil {
			watcher.OnUpdate(namespace, name, obj)
		} else if isDelete && watcher.OnDelete != nil {
			watcher.OnDelete(namespace, name, obj)
		}
	}
}

func WatchResourceById(id string, watcher *K8sResourceWatchCallback) {
	group, version, kind, namespace, name, ok := decodeResourceID(id)
	if !ok {
		log.Printf("K8s: Cannot watch resource: invalid resource id [%s]\n", id)
		return
	}
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	WatcheResource(group, version, kind, namespace, name, watcher)
}

// WatcheResource has the watcher told of the changes of the resource of the namespace and name (an empty namespace is the
// name in any namespace): its add when the watch starts if it exists, its updates and its delete. The resource type is
// given by the name of it, the plural of it, the singular of it or the kind of it. The watch lasts until the watcher is
// removed with UnwatchResource.
//
// The watchers of a namespace and name are told of the changes of any kind of resource that has them. A watch has to have
// a name: the watchers are told of the changes by the names of the resources, and none is told for an empty one.
func WatcheResource(group, version, resource, namespace, name string, watcher *K8sResourceWatchCallback) {
	resource, namespace, name = reinterpretResource(resource, namespace, name)
	// The watcher is added first, so that it is told of what the informer reports as soon as it does.
	k8sResourceWatch.addWatch(namespace, name, watcher)
	log.Printf("Added watcher %s for resource [%s/%s]\n", watcher.Name, namespace, name)
	if name == "" {
		log.Printf("K8s: Not watching [%s/%s]: a watch needs the name of a resource\n", namespace, name)
		return
	}
	gvr, err := k8s.ResolveResource(group, version, resource)
	if err != nil {
		log.Printf("K8s: Cannot watch resource [%s/%s] of [%s/%s/%s]: %s\n", namespace, name, group, version, resource, err.Error())
		return
	}
	startInformer(gvr, namespace, name)
}

func UnwatchResourceById(id string, watcher *K8sResourceWatchCallback) {
	group, version, kind, namespace, name, ok := decodeResourceID(id)
	if !ok {
		log.Printf("K8s: Cannot unwatch resource: invalid resource id [%s]\n", id)
		return
	}
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	UnwatchResource(group, version, kind, namespace, name, watcher)
}

// UnwatchResource removes the watcher. The informers of the namespace and name stop with the last watcher of them.
func UnwatchResource(group, version, resource, namespace, name string, watcher *K8sResourceWatchCallback) {
	_, namespace, name = reinterpretResource(resource, namespace, name)
	k8sResourceWatch.removeWatch(namespace, name, watcher)
	log.Printf("Removed watcher %s for resource [%s/%s]\n", watcher.Name, namespace, name)
	if !k8sResourceWatch.isWatched(namespace, name) {
		stopInformers(func(key informerKey) bool { return key.namespace == namespace && key.name == name })
	}
}

func (kw *K8sResourceWatch) isWatched(namespace, name string) bool {
	kw.lock.RLock()
	defer kw.lock.RUnlock()
	return len(kw.watches[namespace][name]) > 0
}

// informerKey is what an informer is of: one is run for each, however many watchers there are of it.
type informerKey struct {
	cluster         string
	gvr             schema.GroupVersionResource
	namespace, name string
}

var (
	informersLock sync.Mutex
	// informers has the channel that stops each informer that is running.
	informers = map[informerKey]chan struct{}{}
)

// startInformer runs an informer of the resource of the name, unless there is one of it running, which reports what
// changes to the watchers. An empty namespace is the name in all the namespaces.
func startInformer(gvr schema.GroupVersionResource, namespace, name string) {
	c := k8sClient.Active()
	if c == nil || c.Config == nil {
		log.Printf("K8s: Cannot watch resource [%s/%s]: no k8s client is configured\n", namespace, name)
		return
	}
	key := informerKey{cluster: c.ID(), gvr: gvr, namespace: namespace, name: name}
	informersLock.Lock()
	defer informersLock.Unlock()
	if informers[key] != nil {
		return
	}
	// The informer lasts, so it does not get the timeout that the client has for requests that are answered at once.
	client, err := c.StreamDynamic()
	if err != nil {
		log.Printf("K8s: Cannot watch resource [%s/%s]: %s\n", namespace, name, err.Error())
		return
	}
	stop := make(chan struct{})
	// There is no resync, which would tell the watchers of an update that is none, at each period.
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, namespace, func(opts *metav1.ListOptions) {
		opts.FieldSelector = fields.OneTermEqualSelector(metav1.ObjectNameField, name).String()
	})
	informer := factory.ForResource(gvr).Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { report(k8sResourceWatch.allAdds, obj, stop) },
		UpdateFunc: func(_, newObj interface{}) { report(k8sResourceWatch.allUpdates, newObj, stop) },
		DeleteFunc: func(obj interface{}) { report(k8sResourceWatch.allDeletes, obj, stop) },
	})
	factory.Start(stop)
	informers[key] = stop
	log.Printf("K8s: Started informer for [%s] resources [%s/%s] of cluster [%s]\n", gvr.String(), namespace, name, key.cluster)
}

// report hands a change to the notifier, unless the informer that reports it has been stopped while it waits.
func report(changes chan interface{}, obj interface{}, stop <-chan struct{}) {
	select {
	case changes <- obj:
	case <-stop:
	}
}

// stopInformers stops the informers that are matched.
func stopInformers(match func(informerKey) bool) {
	informersLock.Lock()
	defer informersLock.Unlock()
	for key, stop := range informers {
		if match(key) {
			close(stop)
			delete(informers, key)
			log.Printf("K8s: Stopped informer for [%s] resources [%s/%s] of cluster [%s]\n", key.gvr.String(), key.namespace, key.name, key.cluster)
		}
	}
}

func GetResourceByID(id string) (util.JSON, error) {
	group, version, resource, namespace, name, ok := decodeResourceID(id)
	if !ok {
		return nil, fmt.Errorf("invalid resource id [%s]: want group/version/resource[/namespace[/name]]", id)
	}
	kind, namespace, name := reinterpretResource(resource, namespace, name)
	gvk := &schema.GroupVersionKind{
		Group:   group,
		Version: version,
		Kind:    kind,
	}
	return getResource(gvk, namespace, name, nil, nil)
}

func GetResource(kind, namespace, name string, jp *util.JSONPath, jq *util.JQ, r *http.Request) (*schema.GroupVersionKind, util.JSON, error) {
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	k8sApi := k8s.K8sApiResourcesMap[strings.ToLower(kind)]
	if k8sApi == nil {
		log.Printf("K8s: Resource [%s] not found in K8sApiResourcesMap.\n", kind)
		return nil, nil, errors.NewBadRequest("Resource not found")
	}
	gvk := schema.FromAPIVersionAndKind(k8sApi.Version, k8sApi.Kind)
	json, err := getResource(&gvk, namespace, name, jp, jq)
	return &gvk, json, err
}

func getResource(gvk *schema.GroupVersionKind, namespace, name string, jp *util.JSONPath, jq *util.JQ) (util.JSON, error) {
	json := getResourceFromCache(gvk, namespace, name)
	var err error
	if json == nil {
		if json, err = fetchResource(gvk, namespace, name); err != nil {
			log.Printf("K8s: Failed to get resource [%s/%s] with error: %s\n", namespace, name, err)
			return nil, err
		}
	}
	if json != nil {
		if jp != nil && !jp.IsEmpty() {
			json = jp.Apply(json)
		} else if jq != nil && !jq.IsEmpty() {
			json = jq.Apply(json)
		}
	}
	return json, nil
}

// cacheName is the name that a resource is cached by. Names are only unique among the resources of a kind, so the kind is
// a part of it: a config map and a pod of the same name in a namespace are two resources. So is the cluster, as the
// same resource of two clusters is two.
func cacheName(cluster string, gvk *schema.GroupVersionKind, name string) string {
	return cluster + "|" + gvk.String() + "/" + name
}

// InvalidateResource forgets the resource that is cached, for the active client. It is for what changes a resource through
// goto, after which the resource is to be read again.
func InvalidateResource(gvk *schema.GroupVersionKind, namespace, name string) {
	Cache.Delete(namespace, cacheName(k8sClient.Active().ID(), gvk, name))
}

func getResourceFromCache(gvk *schema.GroupVersionKind, namespace, name string) util.JSON {
	obj := Cache.Get(namespace, cacheName(k8sClient.Active().ID(), gvk, name))
	if obj != nil {
		log.Printf("K8s: Serving Resource [%s/%s] from cache.\n", namespace, name)
		return util.JSONFromObject(obj)
	}
	return nil
}

func fetchResource(gvk *schema.GroupVersionKind, namespace, name string) (util.JSON, error) {
	cluster := k8sClient.Active().ID()
	if obj, err := getResourceFromK8s(gvk, namespace, name); err == nil && obj != nil {
		// What was read is not kept when the active client was switched while it was read, as it is not known which of
		// the clusters it is of.
		if name != "" && k8sClient.Active().ID() == cluster {
			Cache.Store(namespace, cacheName(cluster, gvk, name), obj)
		}
		return util.JSONFromJSON(obj), nil
	} else {
		if se, ok := err.(*errors.StatusError); ok {
			if se.Status().Code == http.StatusNotFound {
				log.Printf("K8s: Resource [%s/%s] not found in k8s.\n", namespace, name)
				return nil, nil
			}
		}
		log.Printf("K8s: Failed to get k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
		return nil, err
	}
}

func getResourceFromK8s(gvk *schema.GroupVersionKind, namespace, name string) (result interface{}, err error) {
	ri, _, err := k8s.GetResourceInterface(gvk, namespace)
	if err != nil {
		return nil, err
	}
	if name != "" {
		result, err = ri.Get(context.Background(), name, metav1.GetOptions{})
	} else {
		result, err = ri.List(context.Background(), metav1.ListOptions{})
	}
	if err != nil || result == nil {
		log.Printf("K8s: Failed to load k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
	}
	return
}

// WatchOptions select what a watch is of, besides the kind, namespace and name.
type WatchOptions struct {
	LabelSelector   string
	FieldSelector   string
	ResourceVersion string
	// Timeout is how long the watch lasts. It lasts until it is stopped when it is zero.
	Timeout time.Duration
}

// OpenWatch starts a watch of the resources of a kind on the cluster of the active client. The watch reports the
// resources that exist as adds, unless it is from a resource version, and then what changes. An empty name is all the
// resources of the namespace, and an empty namespace is all the namespaces. The watch ends when the context is done, and
// has to be stopped by the caller when it is not used any more.
func OpenWatch(ctx context.Context, kind, namespace, name string, opts WatchOptions) (*schema.GroupVersionKind, watch.Interface, error) {
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	k8sApi := k8s.K8sApiResourcesMap[strings.ToLower(kind)]
	if k8sApi == nil {
		log.Printf("K8s: Resource [%s] not found in K8sApiResourcesMap.\n", kind)
		return nil, nil, errors.NewBadRequest("Resource not found")
	}
	gvk := schema.FromAPIVersionAndKind(k8sApi.Version, k8sApi.Kind)
	ri, _, err := k8s.GetWatchInterface(&gvk, namespace)
	if err != nil {
		return &gvk, nil, err
	}
	listOpts := metav1.ListOptions{
		LabelSelector:   opts.LabelSelector,
		FieldSelector:   opts.FieldSelector,
		ResourceVersion: opts.ResourceVersion,
	}
	if name != "" {
		byName := fields.OneTermEqualSelector(metav1.ObjectNameField, name).String()
		if listOpts.FieldSelector != "" {
			listOpts.FieldSelector += "," + byName
		} else {
			listOpts.FieldSelector = byName
		}
	}
	if opts.Timeout > 0 {
		seconds := int64(opts.Timeout.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		listOpts.TimeoutSeconds = &seconds
	}
	w, err := ri.Watch(ctx, listOpts)
	return &gvk, w, err
}
