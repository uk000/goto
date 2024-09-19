/**
 * Copyright 2024 uk
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
  "bytes"
  "context"
  "fmt"
  "goto/pkg/global"
  "goto/pkg/util"
  "log"
  "net/http"
  "path/filepath"
  "runtime"
  "strings"
  "sync"
  "time"

  corev1 "k8s.io/api/core/v1"
  "k8s.io/apimachinery/pkg/api/meta"
  "k8s.io/apimachinery/pkg/apis/meta/v1"
  "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
  "k8s.io/apimachinery/pkg/fields"
  k8sruntime "k8s.io/apimachinery/pkg/runtime"
  "k8s.io/apimachinery/pkg/runtime/schema"
  "k8s.io/apimachinery/pkg/runtime/serializer"
  "k8s.io/apimachinery/pkg/watch"
  "k8s.io/client-go/discovery"
  "k8s.io/client-go/discovery/cached/memory"
  "k8s.io/client-go/dynamic"
  "k8s.io/client-go/kubernetes/scheme"
  typedclient "k8s.io/client-go/kubernetes/typed/core/v1"
  _ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
  "k8s.io/client-go/rest"
  "k8s.io/client-go/restmapper"
  "k8s.io/client-go/tools/cache"
  "k8s.io/client-go/tools/clientcmd"
  "k8s.io/client-go/tools/remotecommand"
  "k8s.io/client-go/util/homedir"
)

type K8sClient struct {
  config      *rest.Config
  client      dynamic.Interface
  typedClient *typedclient.CoreV1Client
  dClient     *discovery.DiscoveryClient
  restMapper  *restmapper.DeferredDiscoveryRESTMapper
}

type K8sCacheEntry struct {
  key   string
  store cache.Store
}

type K8sInterceptWatch struct {
  watch.ProxyWatcher
  eventChannel chan watch.Event
  stopChannel  chan bool
}

type K8sResourceWatchCallback func(string)

type K8sResourceWatch struct {
  Watches map[string]K8sResourceWatchCallback
  lock    sync.RWMutex
}

const (
  CacheRefreshPeriod = 60 * time.Second
)

var (
  k8sClient           = CreateK8sClient()
  k8sCache            = map[string]*K8sCacheEntry{}
  k8sResourceWatch    = &K8sResourceWatch{Watches: map[string]K8sResourceWatchCallback{}}
  cacheLock           sync.RWMutex
  k8sCacheStopChannel = make(chan struct{}, 1)
)

func StopCache() {
  k8sCacheStopChannel <- struct{}{}
}

func ClearCache() {
  cacheLock.Lock()
  k8sCache = map[string]*K8sCacheEntry{}
  k8sClient = CreateK8sClient()
  cacheLock.Unlock()
  runtime.GC()
}

func CreateK8sClient() *K8sClient {
  k8sClient := &K8sClient{}
  var err error
  if global.KubeConfig != "" {
    if k8sClient.config, err = clientcmd.BuildConfigFromFlags("", global.KubeConfig); err != nil {
      log.Printf("K8s: Failed to load kube config [%s] with error: %s\n", global.KubeConfig, err.Error())
      return nil
    }
  } else {
    if k8sClient.config, err = rest.InClusterConfig(); err != nil {
      kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
      if k8sClient.config, err = clientcmd.BuildConfigFromFlags("", kubeconfig); err != nil {
        log.Printf("K8s: Failed to load kube config [%s] with error: %s\n", global.KubeConfig, err.Error())
        return nil
      }
    }
  }
  k8sClient.config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
  if k8sClient.client, err = dynamic.NewForConfig(k8sClient.config); err != nil {
    log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
    return nil
  }
  if k8sClient.typedClient, err = typedclient.NewForConfig(k8sClient.config); err != nil {
    log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
    return nil
  }
  k8sClient.dClient, err = discovery.NewDiscoveryClientForConfig(k8sClient.config)
  if err != nil {
    log.Printf("K8s: Failed to load discovery client with error: %s\n", err.Error())
    return nil
  }
  k8sClient.restMapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(k8sClient.dClient))
  return k8sClient
}

func EncodeResourceID(group, version, kind, namespace, name string) string {
  return strings.ToLower(fmt.Sprintf("%s/%s/%s/%s/%s", group, version, kind, namespace, name))
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

func reinterpretResource(kind, namespace, name string) (string, string, string) {
  isNS := kind == "ns" || kind == "namespace" || kind == "namespaces"
  isPod := kind == "pod" || kind == "pods"
  isSvc := kind == "svc" || kind == "service" || kind == "services"
  if isNS {
    kind = "namespace"
    name = namespace
    namespace = ""
  } else if isPod {
    kind = "pod"
  } else if isSvc {
    kind = "service"
  }
  return kind, namespace, name
}

func GetResourceByID(id string) util.JSON {
  group, version, kind, namespace, name := DecodeResourceID(id)
  return GetResource(group, version, kind, namespace, name, nil, nil, nil)
}

func GetResource(group, version, kind, namespace, name string, jp *util.JSONPath, jq *util.JQ, r *http.Request) util.JSON {
  kind, namespace, name = reinterpretResource(kind, namespace, name)
  id := EncodeResourceID(group, version, kind, namespace, name)
  cacheLock.RLock()
  k8sCacheEntry := k8sCache[id]
  cacheLock.RUnlock()
  var resource interface{}
  var exists bool
  var json util.JSON
  if k8sCacheEntry != nil {
    if resource, exists, _ = k8sCacheEntry.store.GetByKey(k8sCacheEntry.key); exists {
      msg := fmt.Sprintf("K8s: Serving Resource [%s] from cache.", id)
      if r != nil {
        util.AddLogMessage(msg, r)
      } else {
        log.Println(msg)
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
  mapping, err := k8sClient.restMapper.RESTMapping(schema.GroupKind{Group: group, Kind: kind}, version)
  if err != nil {
    log.Printf("K8s: Failed to load REST mapping with error: %s\n", err.Error())
    return nil
  }
  var list *unstructured.UnstructuredList
  var obj interface{}
  var exists bool
  if name != "" {
    o, key, store := getK8sCachedResource(namespace, name, mapping, k8sClient)
    if o != nil {
      cacheLock.Lock()
      k8sCache[id] = &K8sCacheEntry{store: store, key: key}
      cacheLock.Unlock()
      obj = o
      exists = true
    }
  } else if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
    list, err = k8sClient.client.Resource(mapping.Resource).Namespace(namespace).List(context.Background(), v1.ListOptions{})
    if err == nil {
      exists = true
    } else {
      log.Printf("K8s: Failed to load list of resources for request [%+v] with error: %s\n", id, err.Error())
    }
  } else {
    list, err = k8sClient.client.Resource(mapping.Resource).List(context.Background(), v1.ListOptions{})
    if err == nil {
      exists = true
    } else {
      log.Printf("K8s: Failed to load list of resources for request [%+v] with error: %s\n", id, err.Error())
    }
  }

  if exists {
    var result interface{}
    if obj != nil {
      result = obj
    } else {
      result = list
    }
    jsonResult = util.FromJSON(result)
  } else {
    log.Println("Resource [%s] not found.", id)
  }
  return jsonResult
}

func getK8sResource(namespace, name string, mapping *meta.RESTMapping, k8sClient *K8sClient) (obj *unstructured.Unstructured, err error) {
  if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
    obj, err = k8sClient.client.Resource(mapping.Resource).Namespace(namespace).Get(context.Background(), name, v1.GetOptions{})
  } else {
    obj, err = k8sClient.client.Resource(mapping.Resource).Get(context.Background(), name, v1.GetOptions{})
  }
  if err != nil || obj == nil {
    log.Printf("K8s: Failed to load k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
  }
  return obj, err
}

func getK8sCachedResource(namespace, name string, mapping *meta.RESTMapping, k8sClient *K8sClient) (interface{}, string, cache.Store) {
  obj, err := getK8sResource(namespace, name, mapping, k8sClient)
  if err != nil {
    log.Printf("K8s: Failed to get k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
    return nil, "", nil
  }
  key, err := cache.MetaNamespaceKeyFunc(obj)
  if err != nil {
    log.Printf("K8s: Failed to get key for k8s object for resource [%s/%s] with error: %s\n", namespace, name, err)
    return nil, "", nil
  }
  store, controller := cache.NewInformer(
    &cache.ListWatch{
      ListFunc: func(lo v1.ListOptions) (result k8sruntime.Object, err error) {
        lo.FieldSelector = fields.OneTermEqualSelector(v1.ObjectNameField, name).String()
        if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
          return k8sClient.client.Resource(mapping.Resource).Namespace(namespace).List(context.Background(), lo)
        } else {
          return k8sClient.client.Resource(mapping.Resource).List(context.Background(), lo)
        }
      },
      WatchFunc: func(lo v1.ListOptions) (watch.Interface, error) {
        interceptWatcher := &K8sInterceptWatch{eventChannel: make(chan watch.Event), stopChannel: make(chan bool)}
        lo.FieldSelector = fields.OneTermEqualSelector(v1.ObjectNameField, name).String()
        if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
          if w, err := k8sClient.client.Resource(mapping.Resource).Namespace(namespace).Watch(context.Background(), lo); err == nil {
            return interceptWatcher.Watch(w), nil
            // return w, nil
          } else {
            return nil, err
          }
        } else {
          if w, err := k8sClient.client.Resource(mapping.Resource).Watch(context.Background(), lo); err == nil {
            return interceptWatcher.Watch(w), nil
            // return w, nil
          } else {
            return nil, err
          }
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

func (w *K8sInterceptWatch) Stop() {
  w.stopChannel <- true
}

func (w *K8sInterceptWatch) ResultChan() <-chan watch.Event {
  return w.eventChannel
}

func (w *K8sInterceptWatch) Watch(i watch.Interface) watch.Interface {
  resultChan := i.ResultChan()
WatchLoop:
  for {
    select {
    case e := <-resultChan:
      if e.Object != nil {
        gvk := e.Object.GetObjectKind().GroupVersionKind()
        json := util.FromObject(e.Object)
        metadata := json.Get(".metadata")
        namespace := metadata.GetText(".namespace")
        name := metadata.GetText(".name")
        id := EncodeResourceID(gvk.Group, gvk.Version, gvk.Kind, namespace, name)
        if k8sResourceWatch.trigger(id) {
          log.Printf("Watch triggered for resource [%s]\n", id)
        }
      }
      w.eventChannel <- e
    case <-w.stopChannel:
      i.Stop()
      break WatchLoop
    }
  }
  return w
}

func (k *K8sResourceWatch) addWatch(id string, callback K8sResourceWatchCallback) {
  k.lock.Lock()
  k.Watches[id] = callback
  k.lock.Unlock()
}

func (k *K8sResourceWatch) removeWatch(id string) {
  k.lock.Lock()
  delete(k.Watches, id)
  k.lock.Unlock()
}

func (k *K8sResourceWatch) trigger(id string) bool {
  k.lock.RLock()
  defer k.lock.RUnlock()
  if callback := k.Watches[id]; callback != nil {
    callback(id)
    return true
  }
  return false
}

func WatchResource(id string, callback K8sResourceWatchCallback) {
  group, version, kind, namespace, name := DecodeResourceID(id)
  kind, namespace, name = reinterpretResource(kind, namespace, name)
  id = EncodeResourceID(group, version, kind, namespace, name)
  k8sResourceWatch.addWatch(id, callback)
}

func UnwatchResource(id string) {
  group, version, kind, namespace, name := DecodeResourceID(id)
  kind, namespace, name = reinterpretResource(kind, namespace, name)
  id = EncodeResourceID(group, version, kind, namespace, name)
  k8sResourceWatch.removeWatch(id)
}

func PodExec(namespace, label, container string, command string) (string, error) {
  var podName string
  if pods, err := k8sClient.typedClient.Pods(namespace).List(context.Background(),
    v1.ListOptions{LabelSelector: label}); err == nil {
    if len(pods.Items) == 0 {
      log.Printf("K8s: No pods in namespace [%s] for label [%s]\n", namespace, label)
      return "", err
    } else {
      podName = pods.Items[0].Name
    }
  } else {
    log.Printf("K8s: Failed to load pods list with error: %s\n", err.Error())
    return "", err
  }
  req := k8sClient.typedClient.RESTClient().Post().Namespace(namespace).
    Resource("pods").Name(podName).SubResource("exec").
    VersionedParams(&corev1.PodExecOptions{
      Container: container,
      Command:   []string{"sh", "-c", command},
      Stdin:     false,
      Stdout:    true,
      Stderr:    true,
      TTY:       false,
    }, scheme.ParameterCodec)
  exec, err := remotecommand.NewSPDYExecutor(k8sClient.config, "POST", req.URL())
  if err != nil {
    log.Printf("K8s: Failed to execute pod command with error: %s\n", err.Error())
    return "", err
  }
  var outBuf bytes.Buffer
  var errBuf bytes.Buffer
  err = exec.Stream(remotecommand.StreamOptions{
    Stdin:  nil,
    Stdout: &outBuf,
    Stderr: &errBuf,
    Tty:    false,
  })
  if err != nil {
    log.Printf("K8s: Failed to read pod output with error: %s\n", err.Error())
    return "", err
  }
  output := outBuf.String()
  if output == "" {
    output = errBuf.String()
  }
  return output, nil
}
