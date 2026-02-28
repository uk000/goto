
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
| POST      | /k8s/context/{name}  | Set current K8s context for the APIs |
| POST      | /k8s/config/{name}/{url}/{cadata}  | Configure K8s client for the cluster with the given CA data |
| POST      | /k8s/clear  | Clear K8s cache |


## Notes
- `{name}` - Cluster name or resource name
- `{url}` - Kubernetes API server URL
- `{cadata}` - Base64-encoded CA certificate data
- `{kind}` - Kubernetes resource kind (e.g., pods, services, deployments)
- `{namespace}` - Kubernetes namespace
- `jq` query parameter - JQ filter expression for JSON processing
- `jp` query parameter - JSONPath expression for JSON processing
