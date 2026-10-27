
# K8s Features
`Goto` exposes APIs through which info can be fetched from a k8s cluster that `goto` is connected to. `Goto` can connect to the local K8s cluster when running inside a cluster, or connect to a remote K8s cluster via locally available k8s config. When connected remotely, it relies on the authentication performed by local K8s context.

The `goto` K8s APIs support working with both native K8s resources (e.g. Namespaces, Pods, etc.) as well as custom K8s resources identified via GVK (e.g. Istio VirtualService).

# <a name="k8s-apis"></a>
###  K8s APIs

|METHOD|URI|Description|
|---|---|---|
| GET      | /k8s/resources/{kind}  | Get a list of native k8s resource instances cluster-wide (namespaced or non-namespaced) for the given resource kind
| GET      | /k8s/resources/{kind}?`[jq|jp]`={q}  | Same as above, with jq or jp query filtering applied
| GET      | /k8s/resources/{kind}/{name}  | Get a native k8s resource of the given resource kind and name |
| GET      | /k8s/resources/{kind}/{namespace}/{name}  | Get a custom k8s namespaced resource by name from the given namespace |
| GET      | /k8s/resources/{kind}/{namespace}/all  | Get all custom k8s namespaced resource by name from the given namespace |
| GET      | /k8s/watch/{kind}  | Watch the resources of the given kind in all namespaces: responds with a stream of events |
| GET      | /k8s/watch/{kind}/{name}  | Watch a resource of the given kind and name (a non-namespaced resource, or the name in any namespace) |
| GET      | /k8s/watch/{kind}/{namespace}/all  | Watch all resources of the given kind in the given namespace |
| GET      | /k8s/watch/{kind}/{namespace}/{name}  | Watch the resource of the given kind, namespace and name |
| POST      | /k8s/context/{name}  | Switch the APIs to the cluster that was configured with the given name. All reads, watches and applies then go to that cluster |
| POST      | /k8s/config/{name}/{url}/{cadata}  | Configure K8s client for the cluster with the given CA data. `{url}` can have a scheme (`https://host:6443`) or be `host:port` |
| POST      | /k8s/clear  | Clear K8s cache |


## Watch
A watch responds with a stream of events, one JSON object per line, as the K8s API server sends them:
```
{"type":"ADDED","object":{...}}
{"type":"MODIFIED","object":{...}}
{"type":"DELETED","object":{...}}
```
The resources that exist when the watch starts are reported first, as `ADDED` (unless `resourceVersion` is given). The stream lasts until the client disconnects or the `timeout` is over, or until the cluster closes it. Each event is sent as it happens.

|Query param|Description|
|---|---|
| `timeout` | How long the watch lasts, as a duration (`30s`, `2m`) or in seconds. Without it, the watch lasts until the client disconnects |
| `labelSelector` | Only the resources with these labels (`app=x,tier=web`) |
| `fieldSelector` | Only the resources that match (`metadata.namespace=default`). A name in the URI is added to it |
| `resourceVersion` | Watch the changes since this version, without reporting the resources that exist |

```
curl -N "localhost:8080/k8s/watch/configmaps/default/all?timeout=60s"
```

## Caching
A resource that is read by name is cached for `store.CacheTTL` (60 seconds), per cluster. The cache is cleared for a resource that is applied or deleted through `/k8s/yaml`. A change made in the cluster by somebody else is seen after the time is over, or after `/k8s/clear`; the watch APIs always report the changes as they happen.

## Testing against a real cluster
The tests of `pkg/k8s/api` can run against a local cluster (like the one of Rancher Desktop), which is the kube context that is named in the environment. They refuse a cluster that is not on the loopback interface, never use the current context of the kube config, and work in a namespace that they make (`goto-it-xxxxxxxx`) and delete:
```
GOTO_K8S_TEST_CONTEXT=rancher-desktop go test -count=1 -run Integration -v ./pkg/k8s/api/
```

## Notes
- `{name}` - Cluster name or resource name
- `{url}` - Kubernetes API server URL
- `{cadata}` - Base64-encoded CA certificate data
- `{kind}` - Kubernetes resource kind (e.g., pods, services, deployments)
- `{namespace}` - Kubernetes namespace
- `jq` query parameter - JQ filter expression for JSON processing
- `jp` query parameter - JSONPath expression for JSON processing
