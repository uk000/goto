# Registry Package REST APIs

This package provides REST APIs for managing registry and lockers.
See [Registry Overview](Overview.md) doc for an overview and examples of Goto's Registry feature.
JSON payload and response schemas are in [Registry JSON Schemas](../../docs/registry-api-json-schemas.md), and runnable `curl` examples are in [Registry APIs Examples](../../docs/registry-api-examples.md).

Every `goto` instance serves the `/registry/...` APIs, so any instance can act as a registry. Other instances point to it using `--registry <url>` (and optionally `--label <peer label>` and `--locker` to report their client results to the registry's locker). All examples below use `localhost:8080` as the registry.

### <a name="registry-api-conventions"></a>Conventions used by the Registry APIs

- **Optional `{peer}`**: where a URI is shown with `/registry/peers/{peer}/...` and also as `/registry/peers/...`, leaving out `{peer}` applies the operation to all peers known to the registry at that moment. A few APIs need a peer (e.g. `/registry/peers/{peer}/targets/{targets}/remove`, `.../{targets}/invoke`, `.../{targets}/stop`, `/registry/peers/{peer}/remove/{address}`).
- **Peer names**: peers are registered under the label they were given (case preserved, e.g. `peerA`). Peer lockers and all client-results APIs key peers by the lower-cased label (e.g. `peera`), and lookups by `{peer}` in locker/results APIs are case-insensitive. A peer named like one of the fixed path segments (`targets`, `jobs`, `lockers`, `health`, `track`, `probes`, `call`, `apis`, `client`, `instances`, `clear`, `add`) cannot be addressed by name.
- **Push results**: APIs that push configuration to peer pods or ask pods to act (targets, jobs, scripts/files, tracking, probes, results toggles, health, invoke/stop) return a JSON map `{"<peer>": {"<pod address>": true|false}}` (`true` = the pod accepted the request). The HTTP status is `200` if every targeted peer has at least one pod that succeeded (peers without callable pods are not failures), else `424 Failed Dependency`; the body is still the map. A peer that is unknown, or that has nothing saved at the registry for the operation, produces `200` with `{}`. Pods that are unhealthy are skipped and reported as `false`; pods loaded from a dump or cloned from another registry (`offline: true`) are skipped silently and don't appear in the map.
- **Calls to peers**: the registry calls each peer pod over HTTP using a pooled client with a 10 second timeout; most operations retry once or thrice after a timeout (2 seconds between tries) and mark the pod unhealthy if the timeouts persist. The client honors the `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` environment variables of the registry process, so peer addresses must be covered by `NO_PROXY` where a proxy is configured, otherwise every pod shows up unhealthy.
- **Path-style filters**: `targets={targets}` in the client results URIs is a path segment, not a query string, e.g. `/registry/peers/client/results/targets=t1,t2`.
- **Query parameter groups**: where a URI is shown with several query parameters (e.g. `?data={data}&peers={peers}&level={level}`), the parameters are honored only when ALL of the parameters of the group are present in the request. A request with just `?data=y` is served as if no query was given. Boolean parameters accept `y`, `yes`, `true` or `1` (case-insensitive); anything else is false.
- **Comma-separated paths**: locker key paths and nested locker labels are comma-separated lists (`A,B,C`). Keys cannot contain `/` or `,`.
- **Registry logs**: logging of these APIs can be controlled using `--registryLogs` (default `true`), `--lockerLogs` (default `false`), `--reminderLogs` (default `false`) and `--peerHealthLogs` (default `true`), or at runtime via `POST /log/registry/{enable}`, `POST /log/registry/locker/{enable}` and `POST /log/registry/reminder/{enable}` (see [Log APIs](../log/README.md)).

### Registry Clone, Dump and Load APIs

#### <a name="registry-clone-dump-and-load-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST | /registry/cloneFrom?url={url} | Clone data from another registry instance at the given URL (e.g. `http://other-registry:8080`). The current goto instance will download `peers`, `lockers`, `targets`, `jobs`, `tracking headers` and `probes` and **replace** its own copy of each. Tracking time buckets, job scripts and job files are not cloned. The peer pods downloaded from the other registry are marked `offline` and are not used for any invocation by this registry, they just become available locally for informational purposes. Any new pods connecting to this registry using the same peer labels will use the downloaded targets, jobs, etc. Returns `200 Cloned data from registry [url]`, `500` with the failed step in the message (the steps run in order peers, lockers, targets, jobs, headers, probes, and the ones that already completed stay applied), or `404` if the `url` query param is missing. |
| GET | /registry/dump | Dump current registry configs and locker data in json format: `{"lockers": {...}, "peers": {...}, "peerTargets": {...}, "peerJobs": {...}, "peerTrackingHeaders": "...", "peerProbes": {...}}`. Context lockers, tracking time buckets, job scripts and job files are not part of the dump. See [Registry Dump Schema](../../docs/registry-api-json-schemas.md#registry-dump-json-schema). |
| POST | /registry/load | Load registry configs and locker data from json dump produced via `/dump` API. Only the sections present in the payload (`lockers`, `peers`, `peerTargets`, `peerJobs`, `peerTrackingHeaders`, `peerProbes`) are replaced. All sections are parsed first: if any section fails to parse, nothing is loaded and the response is `400` with the reasons (`Registry data not loaded. [failed to load peerTargets with error: ...]`). Loaded pods are marked offline until they re-register. On success returns `200 Registry data loaded`. |

```
curl -s localhost:8080/registry/dump > registry.dump
curl -X POST localhost:8080/registry/load --data-binary @registry.dump
curl -X POST "localhost:8081/registry/cloneFrom?url=http://localhost:8080"
```


### Peer Management APIs

#### <a name="registry-peer-mgmt-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/peers/add     | Register a worker instance (referred to as peer). See [Peer JSON Schema](../../docs/registry-api-json-schemas.md#peer-json-schema). Responds `200` with the startup data for the peer (targets, jobs, job scripts, files, tracking headers/time buckets and probes saved for the peer's label, see [Peer Startup Data Schema](../../docs/registry-api-json-schemas.md#peer-startup-data-json-schema)), or `400` with `{"Message": "failed to parse json with error: ..."}` for an invalid payload. `name` and `address` are not validated, but both must be given: a peer is stored under `name`, its pod under `address`. Registering an `address` that is already known starts a new epoch of the pod (the earlier epochs move to `pastEpochs`) and empties that instance's locker in the current locker. |
| POST      | /registry/peers<br/>/`{peer}`/remember | Re-register a peer. Accepts the same request payload as /peers/add API but doesn't respond back with targets and jobs (the response carries only a `Message`). A known pod just gets its `lastContact` refreshed and is marked healthy and online again; an unknown peer is added. Peers call this API every 5 seconds, so that a registry that was restarted learns about its peers again. |
| POST, PUT | /registry/peers/`{peer}`<br/>/remove/`{address}` | Deregister a peer pod by its label and IP address (`address` is the `ip:port` the pod registered with). Responds `200 Peer Removed: {peer}`, or `406` when the peer label is unknown (a known peer with an unknown address also gets `200`). The peer entry and its epochs stay in the peers list even after its last pod is removed (see `clear/epochs`). |
| GET       | /registry/peers/`{peer}`<br/>/health/`{address}` | Check and report health of a specific peer instance based on label and IP address. Calls `GET /health` on the pod (expects `200`) and updates the pod's `healthy` flag. Response is the [push result map](#registry-api-conventions) `{"peer": {"address": true\|false}}`. |
| GET       | /registry/peers<br/>/`{peer}`/health | Check and report health of all instances of a peer |
| GET       | /registry/peers/health | Check and report health of all instances of all peers |
| GET       | /registry/peers<br/>/health/`{address}` | Same as `/registry/peers/health`: without a peer label the `address` is ignored and all instances of all peers are checked. |
| POST      | /registry/peers/`{peer}`<br/>/health/cleanup | Check health of all instances of the given peer label and remove IP addresses that are unresponsive. Healthy pods get their past epochs reset. Response is the same result map as for the health check (`false` entries are the pods that were removed). |
| POST      | /registry/peers<br/>/health/cleanup | Check health of all instances of all peers and remove IP addresses that are unresponsive |
| POST      | /registry/peers<br/>/clear/epochs   | Remove epochs for disconnected peers. Drops the `pastEpochs` of connected pods, forgets the epochs of pods that were removed, and deletes peers that have no pods left (along with their peer locker in the current locker). |
| POST      | /registry/peers/clear   | Remove all registered peers. Also clears every peer's saved targets, jobs, job scripts and job files, and **replaces all labeled lockers** (data lockers and peer lockers) with a fresh `default` locker. Context lockers, tracking headers/time buckets and probes are left as is. |
| POST      | /registry/peers<br/>/copyToLocker   | Copy current set of `Peers JSON` data (output of `/registry/peers` API) to current locker under a pre-defined key named `peers`. Responds `200` with `Peers [len: N] stored in labeled locker {label} under path 'peers'`. |
| GET       | /registry/peers         | Get all registered peers. See [Peers JSON Schema](../../docs/registry-api-json-schemas.md#peers-json-schema). An empty registry returns `{}`. |

```
curl localhost:8080/registry/peers/add --data '{"name":"peer1","namespace":"test","pod":"podXYZ","address":"1.1.1.1:8081","node":"n1","cluster":"c1"}'
curl localhost:8080/registry/peers
curl localhost:8080/registry/peers/health
curl localhost:8080/registry/peers/peer1/health/1.1.1.1:8081
curl -X POST localhost:8080/registry/peers/health/cleanup
curl -X POST localhost:8080/registry/peers/peer1/remove/1.1.1.1:8081
curl -X POST localhost:8080/registry/peers/clear/epochs
curl -X POST localhost:8080/registry/peers/copyToLocker
```


### Peer Targets APIs

These APIs manage client invocation targets on peers, allowing to add, remove, start and stop specific or all targets, and read client invocation results in a processed JSON format.

Targets are saved at the registry per peer label and pushed to the pods of that peer (`POST /client/targets/add` on each pod); a pod that registers later gets them in its startup data. A target added without a peer is saved for, and pushed to, each peer that is registered at that moment (peers that register later under new labels don't get it).

#### <a name="registry-peer-targets-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET       | /registry/peers/targets | Get all registered targets for all peers, as `{"<peer>": {"<target name>": {...target...}}}`. |
| POST      | /registry/peers<br/>/`{peer}`/targets/add | Add a target to be sent to a peer. See [Peer Target JSON Schema](../../docs/registry-api-json-schemas.md#peer-target-json-schema). Pushed immediately as well as upon start of a new peer instance. The target is validated first: a target without `name` or `url` (or otherwise invalid) gets `400 Invalid target spec: name is required` / `url is required` / ..., and an unparsable payload `400 failed to parse json`. A target with the same `name` replaces the saved one. Response is the [push result map](#registry-api-conventions). |
| POST      | /registry/peers<br/>/targets/add | Add a target to all peers (each peer registered at the time of the call). Same payload and responses as above. |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/remove | Remove given targets (comma-separated names) for a peer. The target is removed from the registry only if every pod of the peer removed it. |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/remove/all | Remove all targets of the peer; same as `targets/clear`. |
| POST      | /registry/peers<br/>/`{peer}`/targets/clear   | Remove all targets for a peer. Targets are dropped from the registry only if all pods of the peer cleared them. |
| GET       | /registry/peers/`{peer}`/targets   | Get all targets of a peer. Responds `204 No Content` if the registry holds no target entry for the peer label (an unknown peer, or targets that were cleared), else `200` with the name-to-target map (`{}` if the peer has none). |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/invoke | Invoke given targets on the given peer. A pod that doesn't have a named target reports `false`, and the response status is `424` when no pod of a peer succeeded. |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/invoke/all | Invoke all targets on the given peer |
| POST, PUT | /registry/peers<br/>/targets/invoke/all | Invoke all targets on all peers |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/stop | Stop given targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/stop/all | Stop all targets on the given peer |
| POST, PUT | /registry/peers<br/>/targets/stop/all | Stop all targets on all peers |
| POST      | /registry/peers/targets/clear   | Remove all targets from all peers |
| POST, PUT | /registry/peers<br/>/client/results<br/>/all/`{enable}`  | Controls whether results should be summarized across all targets, in the registry and on all peers (`{enable}` is `y`/`n`). Disabling this when not needed can improve performance. Disabled by default. Also served at `/registry/peers/targets/results/all/{enable}` (older path). |
| POST, PUT | /registry/peers<br/>/client/results<br/>/invocations/`{enable}`  | Controls whether results should be captured for individual invocations, in the registry and on all peers (`{enable}` is `y`/`n`). Disabling this when not needed can reduce memory usage. Disabled by default. Also served at `/registry/peers/targets/results/invocations/{enable}` (older path). |

With a peer named, `remove`, `invoke`, `stop` and `clear` are skipped (`200` with `{}`) when the registry holds no targets entry for that peer, i.e. the peer is unknown or its targets were cleared; without a peer, all pods of all peers are called.

```
curl localhost:8080/registry/peers/peer1/targets/add --data '{"name":"t1","method":"GET","url":"http://somewhere/foo","headers":{"x":"x1"},"replicas":2,"requestCount":10,"delay":"200ms","sendID":true,"autoInvoke":true}'
curl localhost:8080/registry/peers/targets/add --data '{"name":"t2","url":"http://somewhere/bar","requestCount":3}'
curl localhost:8080/registry/peers/peer1/targets
curl -X POST localhost:8080/registry/peers/peer1/targets/t1,t2/invoke
curl -X POST localhost:8080/registry/peers/targets/invoke/all
curl -X POST localhost:8080/registry/peers/peer1/targets/t1/stop
curl -X POST localhost:8080/registry/peers/peer1/targets/t1,t2/remove
curl -X POST localhost:8080/registry/peers/targets/clear
curl -X POST localhost:8080/registry/peers/client/results/all/y
```

### Peer Jobs APIs

Jobs, job scripts and job files are saved at the registry per peer label and pushed to the pods of that peer (`POST /jobs/add`, `/jobs/add/script/{name}`, `/jobs/store/file/{name}` on each pod). A pod that registers later gets them in its startup data (files first, then scripts, then jobs, then targets). As with targets, an `add` without a peer applies to each peer that is registered at that moment.

#### <a name="registry-peer-jobs-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET       | /registry/peers/jobs | Get all registered jobs for all peers, as `{"<peer>": {"<job name>": {...job...}}}` |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/add | Add a job to be sent to a peer. See [Peer Job JSON Schema](../../docs/registry-api-json-schemas.md#peer-job-json-schema). Pushed immediately as well as upon start of a new peer instance. The job is identified by its `name` (the older `id` field is accepted as the name when `name` is absent). An HTTP task is validated like a client target, so it needs its own `name` and `url`; a command task needs `cmd` or `script`. Responses: push result map, `400 failed to read job` for an unparsable job or task, `400 job name is required`. |
| POST, PUT | /registry/peers/jobs/add | Add a job to be sent to all peers. See [Peer Job JSON Schema](../../docs/registry-api-json-schemas.md#peer-job-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/add/script/`{name}` | Add a job script with the given name and request body as content, to be sent to instances of a peer. Pushed immediately as well as upon start of a new peer instance. The script is saved by the pod under `{name}` in its working directory and can be referenced by command jobs. An empty body gets `400 Invalid job script`. |
| POST, PUT | /registry/peers<br/>/jobs/add/script/`{name}` | Add a job script with the given name and request body as content, to be sent to all peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/store/file/`{name}` | Add a file with the given name and request body as content, to be sent to instances of a peer, to be saved at the current working directory of the peer `goto` process. Pushed immediately as well as upon start of a new peer instance. An empty body gets `400 Invalid job script`. |
| POST, PUT | /registry/peers<br/>/jobs/store/file/`{name}` | Add a file with the given name and request body as content, to be sent to all peers, to be saved at the current working directory of the peer `goto` process. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/store/file/`{name}`?path=`{path}` | Add a file with the given name and request body as content, to be sent to instances of a peer, to be saved at the given path in the peer `goto` process' host. Pushed immediately as well as upon start of a new peer instance. Note: if the request URI (including the query string, or the peer label) contains the text `script`, the request is handled as a job script upload instead of a file upload. |
| POST, PUT | /registry/peers<br/>/jobs/store/file/`{name}`?path=`{path}` | Add a file with the given name and request body as content, to be sent to all peers, to be saved at the given path in the peer `goto` process' host. Pushed immediately as well as upon start of a new peer instance. |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/remove | Remove given jobs (comma-separated names) for a peer. Jobs are dropped from the registry only if all pods of the peer removed them. |
| POST | /registry/peers<br/>/jobs/`{jobs}`/remove | Remove given jobs from all peers. |
| POST      | /registry/peers/`{peer}`<br/>/jobs/clear   | Remove all jobs for a peer.|
| POST      | /registry/peers<br/>/jobs/clear   | Remove all jobs from all peers.|
| GET       | /registry/peers/`{peer}`/jobs   | Get all jobs of a peer. Responds `204 No Content` if the registry holds no job entry for the peer label (unknown peer, or jobs cleared), else `200` with the name-to-job map. |
| GET       | /registry/peers/jobs   | Get all jobs of all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/run | Run given jobs on the given peer. A pod that doesn't have a named job reports `false` (the status is `424` if no pod of the peer succeeded). |
| POST | /registry/peers<br/>/jobs/`{jobs}`/run | Run given jobs on all peers |
| POST| /registry/peers/`{peer}`<br/>/jobs/run/all | Run all jobs on the given peer |
| POST| /registry/peers<br/>/jobs/run/all | Run all jobs on all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/stop | Stop given jobs on the given peer |
| POST | /registry/peers<br/>/jobs/`{jobs}`/stop | Stop given jobs on all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/stop/all | Stop all jobs on the given peer |
| POST | /registry/peers<br/>/jobs/stop/all | Stop all jobs on all peers |

There are no `invoke` variants for jobs: jobs are started with `.../run` (`/run/all`) and stopped with `.../stop` (`/stop/all`).

```
curl localhost:8080/registry/peers/peer1/jobs/add --data '{"name":"job1","task":{"cmd":"sh","args":["-c","date +%s; echo Hello"]},"auto":true,"count":3,"delay":"1s","maxResults":5,"keepFirst":true}'
curl localhost:8080/registry/peers/jobs/add --data '{"name":"httpjob","task":{"name":"httpjob","method":"GET","url":"http://somewhere/echo","headers":{"x":"x1"}},"count":2}'
curl -X POST localhost:8080/registry/peers/peer1/jobs/add/script/myscript --data-binary @somescript.sh
curl -X POST "localhost:8080/registry/peers/jobs/store/file/some.conf?path=/tmp/foo/conf" --data-binary @some-file.txt
curl localhost:8080/registry/peers/peer1/jobs
curl -X POST localhost:8080/registry/peers/peer1/jobs/job1,httpjob/run
curl -X POST localhost:8080/registry/peers/jobs/stop/all
curl -X POST localhost:8080/registry/peers/peer1/jobs/job1/remove
curl -X POST localhost:8080/registry/peers/jobs/clear
```

### Peer Tracking APIs

Tracking settings are saved at the registry and pushed to all peer pods; new pods get them in their startup data. They are also used by the registry when it summarizes client results from the lockers.

#### <a name="registry-peer-tracking-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST/PUT         | /registry/peers/track/headers/clear            | Clear tracking headers |
| POST/PUT         | /registry/peers/track/time/clear               | Clear tracking time buckets |
| POST, PUT | /registry/peers<br/>/track/headers/`{headers}` | Configure headers to be tracked by client invocations on peers. Pushed immediately as well as upon start of a new peer instance. `{headers}` is a comma-separated list of header names; a name written as `h1\|h2` tracks `h2` as a cross-header of `h1`. Responds with the push result map. |
| GET | /registry/peers/track/headers | Get the headers configured for tracking by the above `POST` API, as a JSON string (e.g. `"x-foo,y-bar"`, or `""` when not set). |
| POST, PUT | /registry/peers<br/>/track/time/`{buckets}` | Configure time buckets to be tracked by client invocations on peers. Pushed immediately as well as upon start of a new peer instance. Buckets are added as a comma-separated list of low-high values in millis, e.g. `0-100,101-300,301-1000`. Invalid buckets get `400 {"error":"Invalid time buckets"}`. |
| GET | /registry/peers/track/time | Get the time buckets configured for tracking by the above `POST` API, as plain text (e.g. `0-100,101-300`; the text is empty when not set). |

```
curl -X POST localhost:8080/registry/peers/track/headers/x-request-id,x-foo
curl localhost:8080/registry/peers/track/headers
curl -X POST localhost:8080/registry/peers/track/time/0-100,101-300,301-1000
curl localhost:8080/registry/peers/track/time
curl -X POST localhost:8080/registry/peers/track/headers/clear
curl -X POST localhost:8080/registry/peers/track/time/clear
```


### Peer Probes APIs

Probe settings are saved at the registry and pushed to all peer pods; new pods get them in their startup data. `{type}` is `readiness` or `liveness` (case-insensitive), any other type gets `400 Cannot add. Invalid probe type`.

#### <a name="registry-peer-probes-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST, PUT | /registry/peers/probes<br/>/liveness/set?uri=`{uri}` | Configure liveness probe URI for peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/probes<br/>/readiness/set/status=`{status}` | Configure readiness probe status for peers. Pushed immediately as well as upon start of a new peer instance. A non-numeric or non-positive status is stored as `200`. |
| POST, PUT | /registry/peers/probes<br/>/liveness/set/status=`{status}` | Configure liveness probe status for peers. Pushed immediately as well as upon start of a new peer instance. |
| GET | /registry/peers/probes | Get probe configuration given to registry via any of the above 4 probe APIs, as `{"ReadinessProbe": "", "LivenessProbe": "", "ReadinessStatus": 200, "LivenessStatus": 200}` (see [Peer Probes Schema](../../docs/registry-api-json-schemas.md#peer-probes-json-schema)), or `null` if nothing was set. |
| POST, PUT | /registry/peers/probes<br/>/readiness/set?uri=`{uri}` | Configure readiness probe URI for peers. Pushed immediately as well as upon start of a new peer instance. |

```
curl -X POST "localhost:8080/registry/peers/probes/readiness/set?uri=/ready"
curl -X POST localhost:8080/registry/peers/probes/liveness/set/status=503
curl localhost:8080/registry/peers/probes
```

### Peer Remote API Invocation APIs

#### <a name="registry-peer-call-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET, POST, PUT | /registry/peers/`{peer}`<br/>/call?uri=`{uri}` | Invoke the given `URI` on the given `peer`, using the HTTP method, headers and payload from this request. The `uri` is appended to the pod's base URL, so it must start with `/` and can carry its own query string (URL-encode `&` and `?` as needed). Returns `{"<peer>": {"<pod address>": <response>}}` where each response is the parsed JSON body when the pod answered with a JSON content type, else the body text; a pod that answers with a status other than `200` still contributes its response body, and a pod that could not be reached contributes an empty string. Unhealthy and offline pods are left out. A request without the `uri` query param is not routed (`404`). |
| GET, POST, PUT | /registry/peers/call?uri=`{uri}` | Invoke the given `URI` on all `peers`, using the HTTP method and payload from this request |
| GET | /registry/peers/`{peer}`/apis | Get a list of useful APIs ready for invocation on or related to the given peer. See [Peer APIs Schema](../../docs/registry-api-json-schemas.md#peer-apis-json-schema). |
| GET | /registry/peers/apis | Get a list of useful APIs ready for invocation on or related to all peers |

```
curl "localhost:8080/registry/peers/peer1/call?uri=/version"
curl -X POST "localhost:8080/registry/peers/peer1/call?uri=/request/headers/track/add/x"
curl -s "localhost:8080/registry/peers/call?uri=/probes"
curl localhost:8080/registry/peers/peer1/apis
```


## Locker Management APIs

A registry holds `labeled lockers`. It starts with a locker labeled `default`, which is the current locker. Opening a locker makes it the current locker, and the current locker is where results reported by peers (peer lockers) get stored. Each locker has a `data locker` for arbitrary data (stored under comma-separated key paths), `peer lockers` for the data of peers, and optionally child lockers (opened using comma-separated labels such as `A,B`, where `B` is a child of `A`). `current` can be used as the `{label}` in most APIs to refer to the current locker. Lockers in a `context` (the `/registry/context/{context}/...` APIs) are a separate set of lockers, with their own `default` and current locker, which don't receive peer results. See [Goto Lockers](../../docs/goto-lockers.md).

#### Context Lockers APIs

#### <a name="context-lockers-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST             | /registry/context/{context}/lockers/{label}/open | Open a locker in a context. The context is created if needed. |
| POST             | /registry/context/{context}/lockers/{label}/close | Close a locker in a context |
| POST             | /registry/context/{context}/lockers/{label}/clear | Clear a locker in a context |
| POST             | /registry/context/{context}/lockers/{label}/store/{path} | Store data in a locker in a context |
| POST             | /registry/context/{context}/lockers/{label}/{path}/store | Same as above |
| POST             | /registry/context/{context}/lockers/{label}/{path}/add | Append data to the list of values at the path in a locker in a context (see the `add` note below) |
| GET              | /registry/context/{context}/lockers/{label}/get/{path} | Get data from a locker in a context |
| GET              | /registry/context/{context}/lockers/{label}/{path}/get | Same as above |

The responses and rules are the same as for the corresponding non-context APIs below. Context lockers are included in the output of `GET /registry/lockers` (keyed by context name). There are no APIs to close or clear all lockers of a context, and context lockers are not part of the registry dump.

```
curl -X POST localhost:8080/registry/context/c1/lockers/Z/open
curl -X POST localhost:8080/registry/context/c1/lockers/Z/store/x,y --data 'some data'
curl localhost:8080/registry/context/c1/lockers/Z/get/x,y
```


#### Lockers Maintenance APIs

#### <a name="lockers-maintenance-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/lockers<br/>/open/`{label}` | Setup a locker with the given label and make it the current locker where peer results get stored. Comma-separated labels can be used to open nested lockers, where each non-leaf item in the CSV list is used as a parent locker. The leaf locker label becomes the currently active locker. Opening an existing locker makes it current again without clearing it. Responds `200 [Context: ] Locker {label} is open and active`. Empty segments in a CSV label (e.g. `,x`) are not supported, don't use them. Avoid the labels `current` and `all`, which have a special meaning in other APIs. |
| POST      | /registry/lockers<br/>/`{label}`/open | Same as above. |
| POST      | /registry/lockers<br/>/close/`{label}` | Remove the locker for the given label (and its child lockers). Responds `200 [Context: ] Locker {label} is closed`, `404 Locker not found` or `400 Default locker cannot be closed`. If the closed locker was itself the current locker, `default` becomes current (closing a parent of the current locker doesn't change the current locker, so re-open a locker afterwards). |
| POST      | /registry/lockers<br/>/`{label}`/close | Remove the locker for the given label. |
| POST      | /registry/lockers/close | Remove all labeled lockers and empty the default locker. Also asks every peer pod to clear its client results (`POST /client/results/clear`), and responds with the push result map. |
| POST      | /registry/lockers<br/>/`{label}`/clear | Clear the contents of the locker for the given label but keep the locker: its data, its peer lockers and its child lockers are removed. Responds `200 [Context: ] Locker {label} is cleared` or `404 Locker not found`. |
| POST      | /registry/lockers<br/>/clear/`{label}` | Same as above. |
| POST      | /registry/lockers/clear | Remove all labeled lockers and empty the default locker. Also asks every peer pod to clear its client results, and responds with the push result map. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/lockers/clear | Clear the locker for the peer instance under the currently active labeled locker. Also asks that pod to clear its client results; responds with the push result map. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/clear | Same as above. |
| POST      | /registry/peers/`{peer}`<br/>/lockers/clear | Clear the locker for all instances of the given peer under the currently active labeled locker. Also asks the pods of the peer to clear their client results. |
| POST      | /registry/peers/`{peer}`<br/>/locker/clear | Same as above. |
| POST      | /registry/peers<br/>/lockers/clear | Clear all peer lockers under the currently active labeled locker. Also asks all pods to clear their client results. |

```
curl -X POST localhost:8080/registry/lockers/open/A,B
curl -X POST localhost:8080/registry/lockers/A/clear
curl -X POST localhost:8080/registry/lockers/A/close
curl -X POST localhost:8080/registry/peers/peer1/lockers/clear
curl -X POST localhost:8080/registry/lockers/clear
```


#### Lockers Data Store APIs

Data is stored as text: the request body is stored as is (not parsed), at the leaf of the key path, and returned as text. A locker, and the keys on the path, are created on demand (also by `remove`). `store` replaces the value at the path; `add` appends it to a list of values at that path (an existing single value becomes the first item of the list). `store`/`add`/`remove` all respond `200` (`Data stored in [Context: ] labeled locker {label} for path [a b]` / `Data removed from labeled locker ...`) or `400 Not enough parameters to access locker`. The handlers look for `/add` (and, for `close`/`clear`, the word `close`) anywhere in the request URI, so a locker label, context name or leading key that starts with `add` (e.g. `address`) turns a `store` into an `add` in either URI form, and a label containing `close` turns a `clear` into a `close`. Avoid such names (a key that starts with `add` further down the path, as in `x,address`, is fine).

`remove` deletes the key at the end of the path only if it has no child keys, and then also deletes parent keys that are left without child keys (including the data held by those parents). Removing a key that has child keys has no effect.

#### <a name="locker-data-store-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/lockers<br/>/`{label}`/store/`{path}` | Store payload (body) as data in the given labeled locker at the leaf of the given key path. `path` can be a single key or a comma-separated list of subkeys, in which case data gets stored in the tree under the given path. |
| POST      | /registry/lockers<br/>/`{label}`/`{path}`/store | Same as above. |
| POST      | /registry/lockers<br/>/`{label}`/`{path}`/add | Append the payload to the list of values stored at the leaf of the given key path. |
| POST      | /registry/lockers<br/>/`{label}`/remove/`{path}` | Remove stored data, if any, from the given key path in the given labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data gets removed from the leaf of the given path. |
| POST      | /registry/lockers<br/>/`{label}`/`{path}`/remove | Same as above. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/store/`{path}` | Store any arbitrary value for the given `path` in the locker of the peer instance under the currently active labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data is read from the leaf of the given path. The peer and instance lockers are created if needed (the peer label is lower-cased); this is the API peers use to report their results. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/remove/`{path}` | Remove stored data for the given `path` from the locker of the peer instance under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case the leaf key in the path gets removed. |
| POST      | /registry/peers/`{peer}`<br/>/locker/store/`{path}` | Store any arbitrary value for the given key in the peer locker without associating data to a peer instance under the currently active labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data gets stored in the tree under the given complete path. |
| POST      | /registry/peers/`{peer}`<br/>/locker/remove/`{path}` | Remove stored data for the given key from the peer locker under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case the leaf key in the path gets removed. |

```
curl -X POST localhost:8080/registry/lockers/current/store/A,B,C --data '{"some":"data"}'
curl -X POST localhost:8080/registry/lockers/lockerA/A,B/add --data 'one'
curl -X POST localhost:8080/registry/lockers/lockerA/remove/A,B,C
curl -X POST localhost:8080/registry/peers/peer1/10.0.0.1:8080/locker/store/notes,1 --data 'peer data'
```


#### Lockers Data Read APIs

These APIs read either all contents of one or more lockers, or read data stored at specific paths/keys. Where applicable, query param `data` controls whether locker is returned with or without stored data (default value is `n` and only locker metadata is fetched; stored values show as `"..."`). Query param `peers` controls whether the returned locker(s) should include peer sub-lockers (containing data published by various peers). Query param `level` controls how many levels of subkeys are returned (levels below that are shown as `"...": ""`). Label `current` can be used with APIs that take a locker label param to get data from the currently active locker. Comma-separated labels can be used to read from a nested locker. Remember that the query params are honored only when all the params shown for the URI are given together (and `md` only along with the others), and that `level=0` means no trimming. The default `level` depends on the API:

|API|default `level` when not given|
|---|---|
| `/registry/lockers`, `/registry/lockers/all` | 5 (when the query params are not given the lockers are reported without data and without peer lockers, except for `/all`, which reports data and peers) |
| `/registry/lockers/{label}` | 2 (without data and without peers unless asked) |
| `/registry/lockers/data`, `/registry/lockers/{label}/data` | 2 (when `data=n` or not given: top-level lockers only) |
| `/registry/lockers/{label}/get/{path}` | no trimming; a `level` of `N` means `N` levels below `path` |
| peer lockers (`.../peers`, `.../{peer}/lockers` and similar) | 2 |
| peer locker `.../get/{path}` | `path` plus `level` levels below it (default: the node at the path, deeper keys shown as `...`) |

`GET /registry/lockers/{label}/get/{path}` returns the text stored at the path if there is any (a list built with `add` is printed as `[v1 v2]`); else the sub-tree under the path as JSON (with values hidden unless `data=y`); and `{}` if there is nothing at the path or if the locker doesn't exist. Passing `all` as the label reads the path from all lockers, keyed by locker label. A peer locker `get` returns the text of the peer's own locker, or, when the peer also has instance lockers with data at that path, a JSON map of instance address (and the lower-cased peer label for the peer's own data) to value.

#### <a name="locker-data-read-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET              | /registry/lockers/{label}/get/{path}?data={data}&level={level} | Get data from a locker |
| GET       | /registry/lockers/labels | Get a list of all existing locker labels, regardless of whether or not it has data. Returns `{"<label>": {"current": bool, "firstOpened": time, "lastOpened": time}}`; for a locker with child lockers the value is a map of path label (e.g. `A,B`) to the same info, with `"."` for the locker itself. |
| GET              | /registry/lockers?data={data}&peers={peers}&level={level} | Get all lockers data with peers. Returns a map of locker label to locker, plus an entry per context holding that context's lockers. |
| GET              | /registry/lockers/all | Get all lockers with data and peers (level 5). |
| GET              | /registry/lockers/`{label}`?data={data}&peers={peers}&level={level}[&md=y] | Get the given labeled locker (see [Locker JSON Schema](../../docs/registry-api-json-schemas.md#locker-json-schema)). `404 Locker not found` (plain text) if the label is unknown. With `md=y` the response is markdown (`text/markdown`) instead of JSON. |
| GET      | /registry/lockers<br/>/`{label}`/data/keys| Get a list of keys where some data is stored, from the given locker. |
| GET      | /registry/lockers<br/>/`{label}`/data/paths| Get a list of key paths (URIs) where some data is stored, from the given locker. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/data/keys| Get a list of keys where some data is stored, from all lockers.  |
| GET      | /registry/lockers<br/>/data/paths| Get a list of key paths (URIs) where some data is stored, from all lockers. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/search/`{text}` | Get a list of all valid URI paths (containing the locker label and keys) where the given text exists, across all lockers. `text` is a case-insensitive regular expression that is matched against the key names (not the stored values) at every level of the key path, and a sub-key is only reported if the keys above it match too (e.g. `k2` doesn't find the path `k1,k2` but `k` does); an invalid expression gets `400`. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/`{label}`/search/`{text}` | Get a list of all valid URI paths where the given text exists in the given locker. The returned URIs are valid for invocation against the base URL of the registry. |
| GET       | /registry/lockers<br/>/data?data=`[y/n]`<br/>&level=`{level}`[&md=y] | Get data sub-lockers from all labeled lockers |
| GET       | /registry/lockers/`{label}`<br/>/data?data=`[y/n]`<br/>&level=`{level}`[&md=y] | Get data sub-lockers from the given labeled locker.  |
| GET      | /registry/lockers<br/>/`{label}`/get/`{path}`?<br/>data=`[y/n]`&level=`{level}`[&md=y] | Read stored data, if any, at the given key path in the given labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data is read from the leaf of the given path. |
| GET      | /registry/context/`{context}`/lockers<br/>/`{label}`/get/`{path}` | See [Context Lockers APIs](#context-lockers-apis). |

```
curl localhost:8080/registry/lockers/labels
curl "localhost:8080/registry/lockers?data=y&peers=y&level=3"
curl "localhost:8080/registry/lockers/current?data=y&peers=n&level=2"
curl localhost:8080/registry/lockers/lockerA/get/A,B,C
curl "localhost:8080/registry/lockers/lockerA/get/A?data=y&level=1"
curl localhost:8080/registry/lockers/data/paths
curl localhost:8080/registry/lockers/search/foo
```


#### Lockers Peers Data Read APIs

These APIs read the data that peers stored in the peer lockers of a labeled locker (the current one for `/registry/peers/...` URIs, the given one for `/registry/lockers/{label}/peers/...` URIs; `{label}` can be `current`), including the client results that peers report when they run with `--locker`. A peer instance is identified by `{address}`, the `ip:port` the pod registered with. Peer labels are case-insensitive, and the outputs are keyed by the lower-cased label.

Client results are aggregated by the registry from the raw results found in the peer instances' lockers (under the `client` key), optionally restricted to given targets using the path segment `targets={targets}` (comma-separated target names). `.../client/results` and `.../client/results/summary` report summary results per peer (instances of a peer are added together), `.../client/results/details` reports both `summary` and `details` per peer, and `.../instances/client/results` reports summary results per peer instance. An unknown peer gives `{}`, a target filter that matches nothing gives an empty object for each peer (e.g. `{"peera": {}}`), and an unknown `{label}` gives `404 Locker not found`. Results are returned as documented in [Peers Client Results Schemas](../../docs/registry-api-json-schemas.md#peers-client-summary-results-schema). The registry's own tracking headers and time buckets (see [Peer Tracking APIs](#registry-peer-tracking-apis)) decide which header and time bucket counts show up.

#### <a name="locker-peers-data-read-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`<br/>/get/`{path}` | Get the data stored at the given path under the peer instance's locker under the given labeled locker.  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/get/`{path}` | Get the data stored at the given path under the peer locker under the given labeled locker.  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/locker/get/`{path}` | Same as above. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`<br/>/locker/get/`{path}` | Same as above, for the peer instance's locker. |
| GET       | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/get/`{path}`?data=`[y/n]`&level=`{level}` | Get the data stored at the given path under the peer instance's locker under the current labeled locker |
| GET       | /registry/peers/`{peer}`<br/>/locker/get/`{path}`?data=`[y/n]`&level=`{level}` | Get the data stored at the given path under the peer locker under the current labeled locker |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>/results`[/targets={targets}]` | Get invocation summary results for the given peer (results of all instances grouped together) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/instances<br/>/client<br/>/results`[/targets={targets}]` | Get invocation summary results for the given peer's instances (reported per instance) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer (summary and per-target details) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for all peers (results of all instances grouped together) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/instances<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peer instances (reported per instance) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer (summary and per-target details) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/instances/client/results<br/>`[/targets={targets}]` | Get invocation summary results for the given peer's instances (reported per instance) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for all peers (summary and per-target details) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/instances/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peer instances (reported per instance) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/targets/results[?detailed=`y`] | Older (v0.8.x) form of the client results APIs: summary results for all peers from the current locker, or the detailed results with `detailed=y`. Also available as `/registry/peers/{peer}/targets/results`, `/registry/lockers/{label}/peers/targets/results` and `/registry/lockers/{label}/peers/{peer}/targets/results`. |
| GET       | /registry/peers<br/>/`{peer}`/`{address}`/lockers?<br/>data=`[y/n]`<br/>&level=`{level}` | Get the peer instance's locker from the current active labeled locker. |
| GET       | /registry/peers/`{peer}`<br/>/lockers?data=`[y/n]`<br/>&level=`{level}` | Get locker's data for all instances of the peer from the currently active labeled locker |
| GET       | /registry/peers<br/>/lockers?data=`[y/n]`<br/>&level=`{level}` | Get the lockers of all peers from the currently active labeled locker. Responds with an empty body if there are no peer lockers. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`?<br/>data=`[y/n]`<br/>&level=`{level}` | Get the peer instance's locker from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`?data=`[y/n]`<br/>&level=`{level}` | Get the lockers of all instances of the given peer from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers?data=`[y/n]`<br/>&level=`{level}` | Get the lockers of all peers from the given labeled locker. |
| GET       | /registry/lockers/`{label}`?<br/>data=`[y/n]`<br/>&peers=`[y/n]`&level=`{level}` | Get the given labeled locker.  |
| GET       | /registry/lockers?<br/>data=`[y/n]`<br/>&peers=`[y/n]`&level=`{level}` | Get all lockers. |

```
curl localhost:8080/registry/peers/client/results
curl localhost:8080/registry/peers/client/results/targets=t1,t2
curl localhost:8080/registry/peers/peer1/client/results/details
curl localhost:8080/registry/peers/instances/client/results
curl localhost:8080/registry/lockers/lockerA/peers/client/results/summary
curl "localhost:8080/registry/peers/lockers?data=y&level=3"
curl localhost:8080/registry/peers/peer1/locker/get/notes,1
```


See [Registry Schema JSONs and APIs Examples](../../docs/registry-api-examples.md)
