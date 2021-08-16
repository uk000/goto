/**
 * Copyright 2021 uk
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
  "fmt"
  "goto/pkg/global"
  "goto/pkg/util"
  "log"
  "net/http"
  "path/filepath"
  "strings"
  "sync"
  "time"

  "k8s.io/apimachinery/pkg/api/meta"
  "k8s.io/apimachinery/pkg/apis/meta/v1"
  "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
  "k8s.io/apimachinery/pkg/fields"
  "k8s.io/apimachinery/pkg/runtime"
  "k8s.io/apimachinery/pkg/runtime/schema"
  "k8s.io/apimachinery/pkg/runtime/serializer"
  "k8s.io/apimachinery/pkg/watch"
  "k8s.io/client-go/discovery"
  "k8s.io/client-go/discovery/cached/memory"
  "k8s.io/client-go/dynamic"
  "k8s.io/client-go/kubernetes/scheme"
  _ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
  "k8s.io/client-go/rest"
  "k8s.io/client-go/restmapper"
  "k8s.io/client-go/tools/cache"
  "k8s.io/client-go/tools/clientcmd"
  "k8s.io/client-go/util/homedir"
)

type K8sClient struct {
  config     *rest.Config
  client     dynamic.Interface
  dClient    *discovery.DiscoveryClient
  restMapper *restmapper.DeferredDiscoveryRESTMapper
}

type K8sCacheEntry struct {
  key   string
  store cache.Store
}

const (
  CacheRefreshPeriod = 60 * time.Second
)

var (
  k8sClient           = CreateK8sClient()
  k8sCache            = map[string]*K8sCacheEntry{}
  cacheLock           sync.RWMutex
  k8sCacheStopChannel = make(chan struct{}, 1)
)

func StopCache() {
  k8sCacheStopChannel <- struct{}{}
}

func CreateK8sClient() *K8sClient {
  k8sClient := &K8sClient{}
  var err error
  if global.KubeConfig != "" {
    if k8sClient.config, err = clientcmd.BuildConfigFromFlags("", global.KubeConfig); err != nil {
      log.Printf("Failed to load kube config [%s] with error: %s\n", global.KubeConfig, err.Error())
      return nil
    }
  } else {
    if k8sClient.config, err = rest.InClusterConfig(); err != nil {
      kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
      if k8sClient.config, err = clientcmd.BuildConfigFromFlags("", kubeconfig); err != nil {
        log.Printf("Failed to load kube config [%s] with error: %s\n", global.KubeConfig, err.Error())
        return nil
      }
    }
  }
  k8sClient.config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
  if k8sClient.client, err = dynamic.NewForConfig(k8sClient.config); err != nil {
    log.Printf("Failed to load kube client with error: %s\n", err.Error())
    return nil
  }
  k8sClient.dClient, err = discovery.NewDiscoveryClientForConfig(k8sClient.config)
  if err != nil {
    log.Printf("Failed to load discovery client with error: %s\n", err.Error())
    return nil
  }
  k8sClient.restMapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(k8sClient.dClient))
  return k8sClient
}

func EncodeResourceID(group, version, kind, namespace, name string) string {
  return fmt.Sprintf("%s/%s/%s/%s/%s", group, version, kind, namespace, name)
}

func DecodeResourceID(id string) (group, version, kind, namespace, name string) {
  pieces := strings.Split(id, "/")
  group = pieces[0]
  version = pieces[1]
  kind = pieces[2]
  if len(pieces) > 3 {
    namespace = pieces[3]
  }
  if len(pieces) > 4 {
    name = pieces[4]
  }
  if kind == "ns" || kind == "namespaces" {
    kind = "namespace"
  } else if kind == "pods" {
    kind = "pod"
  } else if kind == "svc" || kind == "services" {
    kind = "service"
  }
  return
}

func GetResourceByID(id string) util.JSON {
  group, version, kind, namespace, name := DecodeResourceID(id)
  return GetResource(group, version, kind, namespace, name, nil, nil, nil)
}

func GetResource(group, version, kind, namespace, name string, jp *util.JSONPath, jq *util.JQ, r *http.Request) util.JSON {
  id := EncodeResourceID(group, version, kind, namespace, name)
  cacheLock.RLock()
  k8sCacheEntry := k8sCache[id]
  cacheLock.RUnlock()
  var resource interface{}
  var exists bool
  var json util.JSON
  if k8sCacheEntry != nil {
    if resource, exists, _ = k8sCacheEntry.store.GetByKey(k8sCacheEntry.key); exists {
      if r != nil {
        util.AddLogMessage(fmt.Sprintf("Serving Resource [%s] from cache.", id), r)
      } else {
        fmt.Printf("Serving Resource [%s] from cache.\n", id)
      }
      json = util.FromObject(resource)
    }
  }
  if !exists {
    json = fetchResources(id, group, version, kind, namespace, name, k8sClient)
  }
  if json != nil {
    if jp != nil && !jp.IsEmpty() {
      json = jp.Apply(json)
    } else if jq != nil && !jq.IsEmpty() {
      json = jq.Apply(json)
    }
  }
  return json
}

func fetchResources(id, group, version, kind, namespace, name string, k8sClient *K8sClient) util.JSON {
  var jsonResult util.JSON
  mapping, err := k8sClient.restMapper.RESTMapping(schema.GroupKind{group, kind}, version)
  if err != nil {
    log.Printf("Failed to load REST mapping with error: %s\n", err.Error())
    return nil
  }
  var list *unstructured.UnstructuredList
  var obj interface{}
  var exists bool
  if name != "" {
    o, key, store := GetK8sCachedResource(namespace, name, mapping, k8sClient)
    cacheLock.Lock()
    k8sCache[id] = &K8sCacheEntry{store: store, key: key}
    cacheLock.Unlock()
    obj = o
  } else if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
    list, err = k8sClient.client.Resource(mapping.Resource).Namespace(namespace).List(context.Background(), v1.ListOptions{})
    if err == nil {
      exists = true
    } else {
      log.Printf("Failed to load list of resources for request [%+v] with error: %s\n", err.Error())
    }
  } else {
    list, err = k8sClient.client.Resource(mapping.Resource).List(context.Background(), v1.ListOptions{})
    if err == nil {
      exists = true
    } else {
      log.Printf("Failed to load list of resources for request [%+v] with error: %s\n", err.Error())
    }
  }

  if err == nil && exists {
    var result interface{}
    if obj != nil {
      result = obj
    } else {
      result = list
    }
    jsonResult = util.FromJSON(result)
  } else {
    fmt.Println(err)
  }
  return jsonResult
}

func GetK8sResource(namespace, name string, mapping *meta.RESTMapping, k8sClient *K8sClient) (obj *unstructured.Unstructured, err error) {
  if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
    obj, err = k8sClient.client.Resource(mapping.Resource).Namespace(namespace).Get(context.Background(), name, v1.GetOptions{})
  } else {
    obj, err = k8sClient.client.Resource(mapping.Resource).Get(context.Background(), name, v1.GetOptions{})
  }
  if err != nil || obj == nil {
    fmt.Printf("Failed to load k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
  }
  return obj, err
}

func GetK8sCachedResource(namespace, name string, mapping *meta.RESTMapping, k8sClient *K8sClient) (interface{}, string, cache.Store) {
  obj, err := GetK8sResource(namespace, name, mapping, k8sClient)
  if err != nil {
    fmt.Printf("Failed to get k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
    return nil, "", nil
  }
  key, err := cache.MetaNamespaceKeyFunc(obj)
  if err != nil {
    fmt.Printf("Failed to get key for k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
    return nil, "", nil
  }
  store, controller := cache.NewInformer(
    &cache.ListWatch{
      ListFunc: func(lo v1.ListOptions) (result runtime.Object, err error) {
        lo.FieldSelector = fields.OneTermEqualSelector(v1.ObjectNameField, name).String()
        if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
          return k8sClient.client.Resource(mapping.Resource).Namespace(namespace).List(context.Background(), lo)
        } else {
          return k8sClient.client.Resource(mapping.Resource).List(context.Background(), lo)
        }
      },
      WatchFunc: func(lo v1.ListOptions) (watch.Interface, error) {
        lo.FieldSelector = fields.OneTermEqualSelector(v1.ObjectNameField, name).String()
        if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
          return k8sClient.client.Resource(mapping.Resource).Namespace(namespace).Watch(context.Background(), lo)
        } else {
          return k8sClient.client.Resource(mapping.Resource).Watch(context.Background(), lo)
        }
      },
    },
    obj,
    CacheRefreshPeriod,
    cache.ResourceEventHandlerFuncs{},
  )
  go controller.Run(k8sCacheStopChannel)
  return obj, key, store
}
