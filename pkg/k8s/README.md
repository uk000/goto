
# K8s Features
`Goto` exposes APIs through which info can be fetched from a k8s cluster that `goto` is connected to. `Goto` can connect to the local K8s cluster when running inside a cluster, or connect to a remote K8s cluster via locally available k8s config. When connected remotely, it relies on the authentication performed by local K8s context.

The `goto` K8s APIs support working with both native K8s resources (e.g. Namespaces, Pods, etc.) as well as custom K8s resources identified via GVK (e.g. Istio VirtualService). The kinds that the `/k8s/resources` and `/k8s/watch` APIs know are listed in [Supported resource kinds](#supported-resource-kinds); `/k8s/yaml` can apply any kind that the cluster serves.

`Goto` also has a [mock K8s API server](#mock-k8s-api-server), which lets K8s clients (including `goto` itself) read and write resources without a cluster.

# <a name="k8s-apis"></a>
###  K8s APIs

|METHOD|URI|Description|
|---|---|---|
| GET      | /k8s/resources/{kind}  | Get a list of native k8s resource instances cluster-wide (namespaced or non-namespaced) for the given resource kind
| GET      | /k8s/resources/{kind}?`[jq|jp]`={q}  | Same as above, with jq or jp query filtering applied (see [Filtering with jq and jp](#filtering-with-jq-and-jp))
| GET      | /k8s/resources/{kind}/{name}  | Get a resource of the given kind and name. Meant for the kinds that are not in a namespace (namespaces, nodes, persistent volumes, storage classes, volume attachments); for a namespaced kind use the next URI |
| GET      | /k8s/resources/{kind}/{namespace}/{name}  | Get a namespaced resource (native or custom) by name from the given namespace |
| GET      | /k8s/resources/{kind}/{namespace}/all  | Get all the resources of the given kind from the given namespace |
| GET      | /k8s/watch/{kind}  | Watch the resources of the given kind in all namespaces: responds with a stream of events |
| GET      | /k8s/watch/{kind}/{name}  | Watch a resource of the given kind and name (a non-namespaced resource, or the name in any namespace) |
| GET      | /k8s/watch/{kind}/{namespace}/all  | Watch all resources of the given kind in the given namespace |
| GET      | /k8s/watch/{kind}/{namespace}/{name}  | Watch the resource of the given kind, namespace and name |
| POST      | /k8s/context/{name}  | Switch the APIs to the cluster that was configured with the given name (`default` is the cluster of the local kube config or of the cluster that `goto` runs in). All reads, watches and applies then go to that cluster |
| POST      | /k8s/config/{name}/{url}/{cadata}  | Configure K8s client for the cluster with the given CA data. `{url}` can have a scheme (`https://host:6443`) or be `host:port` |
| POST      | /k8s/clear  | Clear K8s cache |

The APIs that store, read, apply and delete YAML are in [K8s YAML APIs](#k8s-yaml-apis).

## Responses
The resource APIs answer with the resource (or the list of them, as `<Kind>List`), as YAML by default, and as JSON when the request has an `Accept` header that mentions `json`. 

|Status|When|
|---|---|
| 200 | The resource, or the list (which is empty when there is nothing of the kind) was read. After `jq`/`jp` filtering, the response is the result of the filter |
| 400 | The kind is not one of the [supported kinds](#supported-resource-kinds), there is no K8s client, or the cluster failed the request (for example, a forbidden request). The body of the response is empty: the reason is in the `goto` log. `/k8s/watch` is different: its body has the reason |
| 404 | A named resource does not exist in the cluster |

```
curl -s -H "Accept: application/json" localhost:8080/k8s/resources/pods/default/mypod
curl -s localhost:8080/k8s/resources/namespaces/default
curl -s localhost:8080/k8s/resources/svc/default/all
```

Kinds can be given in any form of the [table below](#supported-resource-kinds): `pods`, `pod`, `po`, `deploy`, `vs`, etc. A namespace is read as `/k8s/resources/namespaces/{name}` (or `/k8s/resources/namespaces/{name}/all`).

## Supported resource kinds
`{kind}` of `/k8s/resources` and `/k8s/watch` is the plural, the short name, the other name or the kind (in lower case) of one of these resources. 

|Resource|Kind|Short / other names|Group/Version|Scope|
|---|---|---|---|---|
| authorizationpolicies | AuthorizationPolicy | ap | security.istio.io/v1 | namespaced |
| configmaps | ConfigMap | cm | v1 | namespaced |
| cronjobs | CronJob | cj, cron | batch/v1 | namespaced |
| daemonsets | DaemonSet | ds, daemon | apps/v1 | namespaced |
| deployments | Deployment | deploy, dep | apps/v1 | namespaced |
| destinationrules | DestinationRule | dr, dest | networking.istio.io/v1 | namespaced |
| endpoints | Endpoints | ep | v1 | namespaced |
| envoyfilters | EnvoyFilter | | networking.istio.io/v1alpha3 | namespaced |
| events | Event | ev | v1 | namespaced |
| gateways | Gateway | gw | gateway.networking.k8s.io/v1 | namespaced |
| grpcroutes | GRPCRoute | | gateway.networking.k8s.io/v1 | namespaced |
| horizontalpodautoscalers | HorizontalPodAutoscaler | hpa | autoscaling/v2 | namespaced |
| httproutes | HTTPRoute | | gateway.networking.k8s.io/v1 | namespaced |
| ingresses | Ingress | ing | networking.k8s.io/v1 | namespaced |
| jobs | Job | | batch/v1 | namespaced |
| namespaces | Namespace | ns | v1 | cluster |
| networkpolicies | NetworkPolicy | netpol | networking.k8s.io/v1 | namespaced |
| nodes | Node | no | v1 | cluster |
| peerauthentications | PeerAuthentication | pa | security.istio.io/v1 | namespaced |
| persistentvolumes | PersistentVolume | pv | v1 | cluster |
| pods | Pod | po | v1 | namespaced |
| poddisruptionbudgets | PodDisruptionBudget | pdb | policy/v1 | namespaced |
| replicasets | ReplicaSet | rs, replica | apps/v1 | namespaced |
| requestauthentications | RequestAuthentication | ra | security.istio.io/v1 | namespaced |
| rolebindings | RoleBinding | rb | rbac.authorization.k8s.io/v1 | namespaced |
| roles | Role | | rbac.authorization.k8s.io/v1 | namespaced |
| secrets | Secret | | v1 | namespaced |
| serviceaccounts | ServiceAccount | sa | v1 | namespaced |
| serviceentries | ServiceEntry | se | networking.istio.io/v1 | namespaced |
| services | Service | svc | v1 | namespaced |
| sidecars | Sidecar | | networking.istio.io/v1 | namespaced |
| statefulsets | StatefulSet | sts, stateful | apps/v1 | namespaced |
| storageclasses | StorageClass | sc | storage.k8s.io/v1 | cluster |
| telemetries | Telemetry | telemetry | telemetry.istio.io/v1 | namespaced |
| virtualservices | VirtualService | vs, virtual | networking.istio.io/v1 | namespaced |
| volumeattachments | VolumeAttachment | | storage.k8s.io/v1 | cluster |
| wasmplugins | WasmPlugin | wasm | extensions.istio.io/v1alpha1 | namespaced |
| workloadentries | WorkloadEntry | we | networking.istio.io/v1 | namespaced |
| workloadgroups | WorkloadGroup | wg | networking.istio.io/v1 | namespaced |

A kind of a custom resource (Istio, Gateway API) is served only when the cluster has its CRD; otherwise the API answers 400 (the log has the reason). Kinds that are not in the table (for example `clusterroles` or `persistentvolumeclaims`) are answered with 400 `Resource not found` by the watch API, and with an empty 400 by the resource API.

## Filtering with jq and jp
`/k8s/resources/{kind}...` takes a filter that is applied to the resource or the list before it is returned: `jq` (a [jq](https://jqlang.github.io/jq/manual/) expression) or `jp` (a Kubernetes-style [JSONPath](https://kubernetes.io/docs/reference/kubectl/jsonpath/) expression, with the expression in braces). The syntax of the parameter is `{key}={expression}`: the result of the expression is returned under `key`. 

|Rule|Example|
|---|---|
| `key=expression`. Without a key (no `=`), the key is `1` | `jq=.items[].metadata.name` returns `{"1":[...]}` |
| Many filters can be given, as a comma-separated list or as repeated params. The result has a key for each | `jq=n=.items[].metadata.name,k=.kind` or `jq=n=...&jq=k=.kind` |
| `jp` takes the expression in braces | `jp=names={.items[*].metadata.name}` |
| `jq` and `jp` cannot be mixed: if both are given, `jp` is used | |
| The result is JSON when the request accepts `json`. Otherwise it is YAML. The result of a `jp` filter is text (a JSON array in a string) | |
| The param is split at each `,` into filters, and each filter at `=`. A filter is `key=expression` only when it has exactly one `=`. With none, or with more than one (for example `select(.a=="x")`), the key is a number and the expression is the text before the first `=`, which is rarely valid. So an expression cannot have `=` or `,` (also `{a,b}`) | `jq=n=.items[]\|select(.a=="x")` is the expression `n` |
| A filter that is not valid is ignored without an error. When none is valid, the response is the resource without filtering | `jq=n=.items[` returns the whole list |

```
curl -s -H "Accept: application/json" "localhost:8080/k8s/resources/pods?jq=names=.items[].metadata.name"
{"names":["pod-a","pod-b"]}

curl -s -g "localhost:8080/k8s/resources/pods?jp=names={.items[*].metadata.name}"
names: |
  [
      "pod-a",
      "pod-b"
  ]
```
`jq` and `jp` are looked for as query parameters, so a resource that has `jq` or `jp` in its name is not affected. The filter params are accepted on all the `/k8s/resources` URIs (for example `/k8s/resources/pods/default/all?jq=n=.items[].metadata.name`), though only the first is in the table. (`curl` needs `-g` when the URL has braces.)

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
| `timeout` | How long the watch lasts, as a duration (`30s`, `2m`) or in seconds. Without it, the watch lasts until the client disconnects. A value that is neither is answered with 400 `timeout [x] is neither a duration nor a number of seconds` |
| `labelSelector` | Only the resources with these labels (`app=x,tier=web`) |
| `fieldSelector` | Only the resources that match (`metadata.namespace=default`). A name in the URI is added to it |
| `resourceVersion` | Watch the changes since this version, without reporting the resources that exist |

```
curl -N "localhost:8080/k8s/watch/configmaps/default/all?timeout=60s"
```

A watch that cannot be opened is answered with the status of the cluster (or 400), and the reason as the body: `k8s client is not configured`, `Resource not found` (a kind that is not known). An error during the watch is sent as an event of type `ERROR` (with the status of the failure as its object), after which the stream ends. The [mock API server](#mock-k8s-api-server) cannot watch, and so the response is one `ERROR` event.

## Caching
A resource that is read by name is cached for `store.CacheTTL` (60 seconds), per cluster. A list (no name) is not cached. The cache is cleared for a resource that is applied or deleted through `/k8s/yaml`. A change made in the cluster by somebody else is seen after the time is over, or after `/k8s/clear`; the watch APIs always report the changes as they happen.

## Cluster config and context
`Goto` has a client for each cluster that it was configured for, and the K8s APIs (and the [pipes](../pipe/README.md) with K8s sources) talk to the one that is current.

- At startup, `goto` makes the client `default`: the cluster that it runs in, else the cluster of `$HOME/.kube/config` (with the context that is current in it). When it finds neither, there is no default client: the resource APIs answer 400, the watch API answers 400 `k8s client is not configured`, `apply` and `delete` of `/k8s/yaml` answer 200 with an error text, and the rest of the APIs work. The `--kubeConfig` startup flag does not change this: the default client is made before the flags are read, and `KUBECONFIG` is not used. 
- `POST /k8s/config/{name}/{url}/{cadata}` makes a client named `name` for the API server at `url`. `cadata` is the base64 text of the PEM CA certificate that verifies the server, and cannot be empty. It must be a certificate: `Failed to create client for K8s config [...]` (400) is the answer to a value that is not base64 or not a certificate. The client has a request timeout of 5 seconds, and has no credentials (no token and no client certificate), so it is for an API server that does not ask for them, like the [mock server](#mock-k8s-api-server) (or an authenticating proxy in front of a cluster). A config with the name of an earlier one replaces it; a client that is current is replaced after the next `/k8s/context/{name}`.
- `POST /k8s/context/{name}` makes the client of that name the current one: `Current Context swtiched to [name]` (the spelling is of the code), or 400 `Context [name] not found.` when there is no client of that name. The switch is for the whole `goto` instance, and the cache is kept for each cluster, so a switch does not mix the resources of two clusters.
- There is no API that lists the clients or tells which one is current.

```
CA=$(base64 < ca.pem | tr -d '\n')
curl -X POST localhost:8080/k8s/config/dev/https://10.0.0.1:6443/$CA
curl -X POST localhost:8080/k8s/context/dev
curl -X POST localhost:8080/k8s/context/default
curl -X POST localhost:8080/k8s/clear
```

## <a name="k8s-yaml-apis"></a>K8s YAML APIs
YAML (one or many documents) can be stored in `goto` under a name (a `unit`), and then be applied to, or be deleted from, the current cluster, in whole or in part. 

|METHOD|URI|Description|
|---|---|---|
| POST | /k8s/yaml/store/`{yamlName}` | Store the resources of the YAML in the request body under the unit. The body can have many documents (`---`). The resources are kept by namespace, kind and name; a Namespace is kept under its own name as the namespace. A resource that is stored again replaces the earlier one of the same namespace, kind and name |
| POST | /k8s/yaml/apply/`{yamlName}` | Apply (server-side apply, as field manager `goto`) all the resources of the unit, each to its own namespace |
| POST | /k8s/yaml/apply/`{yamlName}`/to?`ns={ns}` | Apply all the resources of the unit to the namespace `ns` (a Namespace resource gets the name `ns`). The unit is not changed |
| POST | /k8s/yaml/apply/`{yamlName}`/`{namespace}` | Apply the resources of the unit that are of this namespace (as stored) |
| POST | /k8s/yaml/apply/`{yamlName}`/`{namespace}`/`{kind}` | Apply the resources of this kind (a plural or short name is fine) of the namespace |
| POST | /k8s/yaml/apply/`{yamlName}`/`{namespace}`/`{kind}`/`{name}` | Apply the resource of this namespace, kind and name |
| POST | /k8s/yaml/delete/`{yamlName}`... | The same five URIs as `apply`, with `delete`: delete the resources from the cluster (the unit keeps them) |
| GET  | /k8s/yaml/`{yamlName}` | Get the resources that are stored under the unit (404 if there are none) |
| GET  | /k8s/yaml/`{yamlName}`/`{namespace}` | Get the stored resources of the namespace |
| GET  | /k8s/yaml/`{yamlName}`/`{namespace}`/`{kind}` | Get the stored resources of the kind in the namespace |
| GET  | /k8s/yaml | Always answers 404 (it has no unit name): the units cannot be listed |
| POST | /k8s/yaml/clear | Clear all the units |
| POST | /k8s/yaml/clear/`{yamlName}` | Clear the unit |
| POST | /k8s/yaml/clear/`{yamlName}`/`{namespace}` | Clear the namespace of the unit |

- A stored resource has the form `{"resource": {the object}, "gvk": {"Group","Version","Kind"}, "namespace": "", "name": ""}`, in the tree `{namespace: {Kind: {name: resource}}}`. The `GET` APIs answer with YAML, or JSON when the `Accept` header mentions `json`.
- Without `metadata.namespace`, a resource is stored under the namespace `""`, and applies to no namespace: use `.../apply/{unit}/to?ns={ns}` for it. Without the `ns` param, `.../apply/{unit}/to` is the namespace `to` of the unit.
- The `{kind}` of the unit APIs is the kind as it is in the YAML (`ConfigMap`), or any of the names of the [supported kinds](#supported-resource-kinds) (`cm`, `configmaps`).
- The result of `apply`/`delete`, and of a failure, is the status 200 with a text body (the APIs do not use the status for the failures):

|Body|When|
|---|---|
| `Stored N resources from Yaml` followed by the unit as YAML | The YAML was stored. `N` is the number of namespaces in the unit, not the number of resources |
| `Failed to store resources from Yaml with errors: [...]` | A document is not valid YAML or has no `kind` (`Object 'Kind' is missing in '...'`), or the body has no document (`no documents found in body`). Nothing is stored |
| `Successfully applied N resources: [[ns/Kind/name ...]] to namespace: [ns]` | `N` resources were applied (the same for delete: `Successfully deleted ...`). The namespace is the one that is asked for ( `[]` when none) |
| `Failed to apply N resources. Error [...]` | N failed. It is printed after the success line, when some succeeded. The text for a delete says `Failed to apply` too. Errors are what the cluster said (`configmaps "x" not found`), `no resources found for unit [x]` (also for a namespace or a kind that is not in the unit), or `k8s client is not configured` |
| `Cleared resources` | Clear, even when there was nothing |
| 404 `No resources found for yamlName [x] and namespace [y]` | `GET` of a unit or a namespace that is not stored |

- The resources are applied in no fixed order: a resource may be applied before the namespace that it needs (see Notes).

```
curl -X POST --data-binary @app.yaml localhost:8080/k8s/yaml/store/app
curl -X POST localhost:8080/k8s/yaml/apply/app
curl -X POST "localhost:8080/k8s/yaml/apply/app/to?ns=test"
curl -X POST localhost:8080/k8s/yaml/apply/app/web/deploy/web
curl -H "Accept: application/json" localhost:8080/k8s/yaml/app/web/cm
curl -X POST localhost:8080/k8s/yaml/delete/app/web
curl -X POST localhost:8080/k8s/yaml/clear/app
```

Do not call `apply` or `delete` unless the current cluster is the one that is meant to change: they act on whichever cluster is current (`/k8s/context/{name}`).

## Mock K8s API server
`Goto` serves a small, in-memory mock of the K8s REST API, for tests of K8s clients (`kubectl`, client libraries, `goto` itself). It serves the routes under `/api` and `/apis`, only for requests whose **Host** has `k8s` as its first label (any case, the port is not counted): `k8s.localhost:8080`, `k8s.example.com`. For other hosts `/api` and `/apis` are what any other URI is (echoed as usual), and `/version`, `/healthz`, `/readyz` and `/livez` are never served by it. The keyword `k8s` is not configurable.

|Route|Description|
|---|---|
| GET /api, /api/v1 | Discovery: the API versions, and the resources of the core group |
| GET /apis, /apis/{group}, /apis/{group}/{version} | Discovery: the groups, and the resources of a group |
| GET, POST, DELETE `/api/v1/namespaces/{ns}/{resource}`<br/>`/apis/{group}/{version}/namespaces/{ns}/{resource}` | List, create, and delete the collection (DELETE) of a namespaced resource |
| GET, PUT, PATCH, DELETE `.../namespaces/{ns}/{resource}/{name}` | Get, update, patch and delete a resource. Every kind of patch (merge, strategic merge, JSON patch, server-side apply) is applied as a JSON merge patch |
| The same without `/namespaces/{ns}`: `/api/v1/{resource}[/{name}]`, `/apis/{group}/{version}/{resource}[/{name}]` | The resources that are not in a namespace, and the list of a namespaced kind across all the namespaces |

- The resources are kept in memory only (per `goto` instance); the first list or get of a kind that has a sample (pods, services, endpoints, configmaps, secrets, namespaces, nodes, persistent volumes and claims, service accounts, deployments, replicasets, daemonsets, statefulsets, jobs, cronjobs, ingresses, network policies, storage classes, HPAs, PDBs, roles, role bindings, cluster roles and bindings, volume attachments, Istio virtual services, destination rules and gateways) creates two samples (named like `sample-pods-1` and `sample-pods-2`) in the namespace. A get of a name that does not exist also answers with a sample of that name, which it then keeps.
- Watches are not served: a request with `watch=true` is answered with the list.
- Each request is logged (method, URI, headers, body).

To use it from `goto` itself, make a client for it (a CA certificate is needed by the call, but is not used for `http`) and switch to it:
```
CA=$(curl -s localhost:8080/tls/ca/cert | base64 | tr -d '\n')
curl -X POST localhost:8080/k8s/config/mock/http://k8s.localhost:8080/$CA
curl -X POST localhost:8080/k8s/context/mock
curl -s -H "Accept: application/json" "localhost:8080/k8s/resources/pods?jq=names=.items[].metadata.name"
curl -X POST --data-binary @app.yaml localhost:8080/k8s/yaml/store/app
curl -X POST localhost:8080/k8s/yaml/apply/app
curl -X POST localhost:8080/k8s/context/default
```
For `kubectl`, point it at `http://k8s.localhost:8080`, and give it a kube config that is empty, so that it does not send the credentials or the TLS settings of your current context to the mock: `touch /tmp/empty-kubeconfig; KUBECONFIG=/tmp/empty-kubeconfig kubectl --server http://k8s.localhost:8080 get pods -A` (`k8s.localhost` resolves to the loopback address).

## Pipes
K8s resources can be read, and watched, from a [pipeline](../pipe/README.md) with a `K8s` source, which is added with `POST /pipes/{pipe}/sources/add/k8s/{name}?spec={spec}` or in the pipe JSON. The `spec` is `group/version/kind/namespace/name` (`/v1/ns/goto`, `/v1/pod/goto/mypod`, `networking.istio.io/v1/virtualservice/foo/bar`); a spec with `"watch": true` is followed with an informer. The source uses the current cluster of `goto` (see [Cluster config and context](#cluster-config-and-context)). Commands can be run in pods with the pipe source `K8sPodExec`: there is no `/k8s` API for it.

## Testing against a real cluster
The tests of `pkg/k8s/api` can run against a local cluster (like the one of Rancher Desktop), which is the kube context that is named in the environment. They refuse a cluster that is not on the loopback interface, never use the current context of the kube config, and work in a namespace that they make (`goto-it-xxxxxxxx`) and delete:
```
GOTO_K8S_TEST_CONTEXT=rancher-desktop go test -count=1 -run Integration -v ./pkg/k8s/api/
```

## Notes
- `{name}` - Cluster name or resource name
- `{url}` - Kubernetes API server URL
- `{cadata}` - Base64-encoded CA certificate data. It is a segment of the URI, so it must not have a `/`: the base64 of a PEM certificate has none
- `{kind}` - Kubernetes resource kind (e.g., pods, services, deployments)
- `{namespace}` - Kubernetes namespace
- `{yamlName}` - The name of a unit of stored YAML
- `jq` query parameter - JQ filter expression for JSON processing
- `jp` query parameter - JSONPath expression for JSON processing
- The cluster that a `/k8s/yaml` apply or delete goes to is the current one; the APIs do not ask for a confirmation.
- Known issues: the watch of a `K8s` pipe source can block the notifier of the informer cache (so that no pipe is notified after that), the resources of a unit are applied in random order, and the `--kubeConfig` flag is ignored. 
