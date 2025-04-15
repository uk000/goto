/**
 * Copyright 2025 uk
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
	"goto/pkg/util"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	cache map[string]map[string]interface{}
	lock  sync.RWMutex
}

const (
	CacheRefreshPeriod = 60 * time.Second
)

var (
	k8sCache = K8sCache{
		cache: map[string]map[string]interface{}{},
	}
	k8sResourceWatch = &K8sResourceWatch{
		watches:    map[string]map[string]map[string]*K8sResourceWatchCallback{},
		allAdds:    make(chan interface{}, 100),
		allUpdates: make(chan interface{}, 100),
		allDeletes: make(chan interface{}, 100),
	}
	k8sWatchStopChannel = make(chan bool, 1)
)

func StartWatch() {
	notifyWatchers()
}

func StopWatch() {
	k8sWatchStopChannel <- true
}

func (k *K8sCache) clear() {
	k.lock.Lock()
	k.cache = map[string]map[string]interface{}{}
	k.lock.Unlock()
	runtime.GC()
}

func (k *K8sCache) store(namespace, name string, obj interface{}) {
	k.lock.Lock()
	defer k.lock.Unlock()
	if k.cache[namespace] == nil {
		k.cache[namespace] = map[string]interface{}{}
	}
	k.cache[namespace][name] = obj
}

func (k *K8sCache) get(namespace, name string) interface{} {
	k.lock.RLock()
	defer k.lock.RUnlock()
	if k.cache[namespace] != nil && k.cache[namespace][name] != nil {
		return k.cache[namespace][name]
	}
	return nil
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
	for name, watcher := range watchers {
		if isAdd {
			watcher.OnAdd(name, namespace, obj)
		} else if isUpdate {
			watcher.OnUpdate(name, namespace, obj)
		} else if isDelete {
			watcher.OnDelete(name, namespace, obj)
		}
	}
}

func WatchResourceById(id string, watcher *K8sResourceWatchCallback) {
	group, version, kind, namespace, name := decodeResourceID(id)
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	WatcheResource(group, version, kind, namespace, name, watcher)
}

func WatcheResource(group, version, resource, namespace, name string, watcher *K8sResourceWatchCallback) {
	resource, namespace, name = reinterpretResource(resource, namespace, name)
	setupWatchForResource(&schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}, name, namespace)
	k8sResourceWatch.addWatch(namespace, name, watcher)
	log.Printf("Added watcher %s for resource [%s/%s]\n", watcher.Name, namespace, name)
}

func UnwatchResourceById(id string, watcher *K8sResourceWatchCallback) {
	group, version, kind, namespace, name := decodeResourceID(id)
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	UnwatchResource(group, version, kind, namespace, name, watcher)
}

func UnwatchResource(group, version, resource, namespace, name string, watcher *K8sResourceWatchCallback) {
	_, namespace, name = reinterpretResource(resource, namespace, name)
	k8sResourceWatch.removeWatch(namespace, name, watcher)
	log.Printf("Removed watcher %s for resource [%s/%s]\n", watcher.Name, namespace, name)
}

func setupWatchForResource(gvr *schema.GroupVersionResource, namespace, name string) {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(k8sClient.client, CacheRefreshPeriod, namespace,
		func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector(metav1.ObjectNameField, name).String()
		})
	informer := factory.ForResource(*gvr).Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Printf("Added %s\n", obj)
			k8sResourceWatch.allAdds <- obj
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			log.Printf("Updated %s\n", newObj)
			k8sResourceWatch.allUpdates <- newObj
		},
		DeleteFunc: func(obj interface{}) {
			log.Printf("Deleted %s\n", obj)
			k8sResourceWatch.allDeletes <- obj
		},
	})
}

func notifyWatchers() {
	go func() {
		for {
			var obj interface{}
			var isAdd, isUpdate, isDelete bool
			select {
			case <-k8sWatchStopChannel:
				return
			case obj = <-k8sResourceWatch.allAdds:
				isAdd = true
				log.Printf("Notifying Add: %s\n", obj)
			case obj = <-k8sResourceWatch.allUpdates:
				isUpdate = true
				log.Printf("Notifying Update: %s\n", obj)
			case obj = <-k8sResourceWatch.allDeletes:
				isDelete = true
				log.Printf("Notifying Delete: %s\n", obj)
			}
			metadata := obj.(*unstructured.Unstructured).Object["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			namespace := metadata["namespace"].(string)
			k8sResourceWatch.notifyWatchers(namespace, name, obj, isAdd, isUpdate, isDelete)
		}
	}()
}

func GetResourceByID(id string) (util.JSON, error) {
	group, version, resource, namespace, name := decodeResourceID(id)
	gvk := &schema.GroupVersionKind{
		Group:   group,
		Version: version,
		Kind:    resource,
	}
	return getResource(gvk, namespace, name, nil, nil)
}

func GetResource(kind, namespace, name string, jp *util.JSONPath, jq *util.JQ, r *http.Request) (*schema.GroupVersionKind, util.JSON, error) {
	kind, namespace, name = reinterpretResource(kind, namespace, name)
	k8sApi := K8sApiResourcesMap[kind]
	if k8sApi == nil {
		log.Printf("K8s: Resource [%s] not found in K8sApiResourcesMap.\n", kind)
		return nil, nil, errors.NewBadRequest("Resource not found")
	}
	gvk := schema.FromAPIVersionAndKind(k8sApi.Version, k8sApi.Kind)
	json, err := getResource(&gvk, namespace, name, jp, jq)
	return &gvk, json, err
}

func getResource(gvk *schema.GroupVersionKind, namespace, name string, jp *util.JSONPath, jq *util.JQ) (util.JSON, error) {
	json := getResourceFromCache(namespace, name)
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

func getResourceFromCache(namespace, name string) util.JSON {
	obj := k8sCache.get(namespace, name)
	if obj != nil {
		log.Printf("K8s: Serving Resource [%s/%s] from cache.\n", namespace, name)
		return util.FromObject(obj)
	}
	return nil
}

func fetchResource(gvk *schema.GroupVersionKind, namespace, name string) (util.JSON, error) {
	if obj, err := getResourceFromK8s(gvk, namespace, name); err == nil && obj != nil {
		if name != "" {
			k8sCache.store(namespace, name, obj)
		}
		return util.FromJSON(obj), nil
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
	ri, _, err := getResourceInterface(gvk, namespace)
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
