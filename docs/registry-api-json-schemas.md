
### Peer JSON Schema 
(to register a peer via /registry/peers/add, and to refresh its registration via /registry/peers/{peer}/remember)

All fields are strings. `name` and `address` identify the peer and the pod, and should both be given: the peer label is the key under which the registry stores the peer, its targets/jobs and its lockers (lockers use the lower-cased label). Peers send this payload themselves at startup (see [Peer Management APIs](../pkg/registry/README.md#registry-peer-mgmt-apis)). The response to `/registry/peers/add` is the [Peer Startup Data](#peer-startup-data-json-schema).

|Field|Data Type|Description|
|---|---|---|
| name      | string | Name/Label of a peer |
| namespace | string | Namespace of the peer instance (if available, else `local`) |
| pod       | string | Pod/Hostname of the peer instance |
| address   | string | Address (`ip:port`) of the peer instance. Used as the key of the pod in the registry, and as the instance label of the peer's instance locker |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |

### Peers JSON Schema 
Map of peer labels to peer details, where each peer details include the following info
(output of /registry/peers)

|Field|Data Type|Description|
|---|---|---|
| name      | string | Name/Label of a peer |
| namespace | string | Namespace of the peer instance (if available, else `local`) |
| pods      | map string->PodDetails | Map of Pod Addresses to Pod Details. See [Pod Details JSON Schema](#pod-details-json-schema) below |
| podEpochs | map string->[]PodEpoch   | Map of pod addresses to past lives of the pod since last cleanup. |


### Pod Details JSON Schema 

|Field|Data Type|Description|
|---|---|---|
| name      | string | Pod/Host Name |
| address   | string | Pod Address |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |
| url       | string | URL where this peer is reachable |
| healthy   | bool   | Whether the pod was found to be healthy at last interaction |
| offline   | bool   | Whether the pod is determined to be offline. Cloned and dump-loaded pods are marked as offline until they reconnect to the registry |
| currentEpoch | PodEpoch   | Current lifetime details of this pod |
| pastEpochs | []PodEpoch   | Past lives of this pod since last cleanup. |


### Pod Epoch JSON Schema 

|Field|Data Type|Description|
|---|---|---|
| epoch      | int | Epoch count of this pod |
| name      | string | Pod/Host Name |
| address   | string | Pod Address |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |
| firstContact   | time | First time this pod connected (at registration) |
| lastContact   | time | Last time this pod sent its reminder |


### Peer Target JSON Schema
** Same as [Client Target JSON Schema](client-api-json-schemas.md#client-target-json-schema)

Targets are kept per peer, keyed by target name (`name` and `url` are required; `headers` must be a JSON map of header name to a single string value, e.g. `{"x": "x1"}`, not an array of pairs). The registry only validates the target when it is added (`400 Invalid target spec: ...`), and each pod validates it again when it receives it. `GET /registry/peers/{peer}/targets` returns a map of target name to target.

### Peer Job JSON Schema
** Same as [Jobs JSON Schema](../pkg/job/README.md) (see the `Job JSON Schema` section)

Jobs are kept per peer, keyed by job `name` (older payloads that use `id` instead of `name` are still accepted for `jobs/add`). A job whose task is an HTTP task needs the task `name` and `url`, and one whose task is a command needs `cmd` or `script`. `GET /registry/peers/{peer}/jobs` returns a map of job name to job.


#### Peers Client Summary Results Schema 
(output of APIs /registry/peers/client/results, /registry/peers/client/results/summary and the same APIs under /registry/lockers/{label}/peers/...)

Results are keyed by the lower-cased peer name, with each result carrying summary results aggregated from all instances of this peer. The summary counts of a peer are not kept for the peer as a whole (there is no top-level `invocationCount`): the number of invocations and the first/last result times are reported per target under `byTargets`. Fields without data are omitted.

|Field|Data Type|Description|
|---|---|---|
| countsByStatusCodes  | string->int   | Response counts by HTTP Status Codes, broken down |
| countsByHeaders      | string->int   | Response counts by header (only for the headers the registry is tracking, see `/registry/peers/track/headers`) |
| countsByHeaderValues | string->string->int   | Response counts by header values |
| countsByURIs         | string->int   | Response counts by URI |
| countsByRequestPayloadSizes | string->int   | Response counts by request payload sizes |
| countsByResponsePayloadSizes | string->int   | Response counts by response payload sizes |
| countsByRetries | string->int   | Response counts by number of retries |
| countsByRetryReasons | string->int   | Response counts by retry reasons |
| countsByErrors | string->int   | Response counts by error type (relevant for response validations) |
| countsByTimeBuckets | string->int   | Response counts by time buckets if defined (see `/registry/peers/track/time`) |
| byTargets | string->TargetSummary | All the above summary counts broken down per target. In addition to the above fields, each `TargetSummary` has `invocationCount` (int), `firstResultAt` (time) and `lastResultAt` (time) |

Example (shortened):

```json
{
  "peera": {
    "countsByStatusCodes": {"200": 3},
    "countsByHeaders": {"goto-host": 3},
    "countsByURIs": {"/version": 3},
    "countsByTimeBuckets": {"[0 100]": 3},
    "byTargets": {
      "tx": {
        "countsByStatusCodes": {"200": 3},
        "countsByHeaders": {"goto-host": 3},
        "countsByURIs": {"/version": 3},
        "countsByTimeBuckets": {"[0 100]": 3},
        "invocationCount": 3,
        "firstResultAt": "2026-11-07T15:53:59.752753-08:00",
        "lastResultAt": "2026-11-07T15:53:59.781376-08:00"
      }
    }
  }
}
```


#### Peers Client Detailed Results Schema 
(output of APIs /registry/peers/client/results/details and the same API under /registry/lockers/{label}/peers/...)

Results contain two keys, `summary` and `details`. `summary` is keyed by peer name, and carries the same fields as the `Peers Client Summary Results` described above, except that the `countsBy...` fields (other than the header counts) hold `SummaryCounts` objects (see below) instead of plain numbers. `details` is keyed by peer name, and then by target name, carrying the results of all the instances of the peer for this target as the following `TargetResults`.

|Field|Data Type|Description|
|---|---|---|
| target | string | Target name |
| invocationCount      | int  | Total requests sent |
| firstResultAt        | time  | Time of first result |
| lastResultAt         | time  | Time of last result |
| clientStreamCount | int | Total messages streamed by the client (gRPC) |
| serverStreamCount | int | Total messages streamed by the server (gRPC) |
| retriedInvocationCounts | int | Number of invocations that were retried |
| countsByHeaders      | string->HeaderCounts | Counts per tracked header, with `header`, `countsByValues`, `countsByStatusCodes`, `countsByValuesStatusCodes`, `crossHeaders` and `crossHeadersByValues` breakdowns |
| countsByStatus  | string->int | Counts by response status text (e.g. `200 OK`) |
| countsByStatusCodes  | string->KeyResultCounts | Response counts by HTTP Status Codes, with a time bucket breakdown under each status code |
| countsByURIs         | string->KeyResultCounts | Response counts by URI, with status code and time bucket breakdowns under each URI |
| countsByRequestPayloadSizes | string->KeyResultCounts | Response counts by request payload sizes |
| countsByResponsePayloadSizes | string->KeyResultCounts | Response counts by response payload sizes |
| countsByRetries | string->KeyResultCounts | Response counts by number of retries |
| countsByRetryReasons | string->KeyResultCounts | Response counts by retry reasons |
| countsByErrors | string->KeyResultCounts | Response counts by error type (relevant for response validations) |
| countsByTimeBuckets | string->KeyResultCounts | Response counts by time buckets if defined, with a status code breakdown under each bucket |

`KeyResultCounts` has `count`, `clientStreamCount`, `serverStreamCount`, `retries`, `firstResultAt`, `lastResultAt`, and optionally `byStatusCodes` and `byTimeBuckets` (each a map to the same `count`...`lastResultAt` fields).


#### Peers Instances Client Results Schema 
(output of APIs /registry/peers/instances/client/results, /registry/peers/{peer}/instances/client/results and the same APIs under /registry/lockers/{label}/peers/...)

Results are keyed by the lower-cased peer name and then by the peer instance address (`ip:port`). Each instance carries the same summary results as the `Peers Client Summary Results` described above (the same fields, `byTargets` included), but for that instance alone. The instance results are summaries only: there is no detailed per-instance output in the registry APIs.

Example (shortened):

```json
{
  "peera": {
    "10.0.0.1:8081": {
      "countsByStatusCodes": {"200": 3},
      "countsByURIs": {"/version": 3},
      "byTargets": {"tx": {"countsByStatusCodes": {"200": 3}, "invocationCount": 3}}
    }
  }
}
```


#### SummaryCounts (used by Peers Results schema)

A `SummaryCounts` is reported as a plain number (the count) when it has no status code or time bucket breakdown, and as an object with the following fields otherwise.

|Field|Data Type|Description|
|---|---|---|
| count      | int  | Total requests sent for this set |
| clientStreamCount | int | Total messages streamed by the client for this set |
| serverStreamCount | int | Total messages streamed by the server for this set |
| byStatusCodes | string->int  | Total requests of this set broken down by status codes (except when under a status code) |
| byTimeBuckets | string->int  | Total requests of this set broken down by time buckets (except when under a time bucket) |


### Peer Startup Data JSON Schema 
(response of /registry/peers/add, `/registry/peers/{peer}/remember` echoes only `Message`)

The configuration the registry hands to a peer when it registers, so that the peer can apply it at startup. The keys are capitalized.

|Field|Data Type|Description|
|---|---|---|
| Targets | map string->[Peer Target](#peer-target-json-schema) | Targets to add on the peer (from `/registry/peers/{peer}/targets/add` and `/registry/peers/targets/add`) |
| Jobs | map string->[Peer Job](#peer-job-json-schema) | Jobs to add on the peer |
| JobScripts | map string->bytes | Job script files by file name (base64 text in JSON) |
| Files | map string->bytes | Job files by file name (base64 text in JSON) |
| TrackingHeaders | string | Comma-separated list of headers to track for client results |
| TrackingTimeBuckets | string | Time buckets for client results as `0-100,101-300` |
| Probes | [Peer Probes](#peer-probes-json-schema) | Readiness/liveness probe configuration |
| Message | string | Informational message, or the parse error (HTTP 400) when the payload was not a valid peer |

Example:

```json
{"Targets":{},"Jobs":{},"JobScripts":{},"Files":{},"TrackingHeaders":"","TrackingTimeBuckets":"0-100,101-300","Probes":{"ReadinessProbe":"/ready","LivenessProbe":"/live","ReadinessStatus":500,"LivenessStatus":503},"Message":""}
```


### Peer Probes JSON Schema 
(output of GET /registry/peers/probes; also the `peerProbes` section of the [registry dump](#registry-dump-json-schema))

The keys are capitalized. `GET /registry/peers/probes` returns `null` when no probe was ever configured on the registry.

|Field|Data Type|Description|
|---|---|---|
| ReadinessProbe | string | URI of the readiness probe |
| LivenessProbe | string | URI of the liveness probe |
| ReadinessStatus | int | HTTP status that the readiness probe responds with |
| LivenessStatus | int | HTTP status that the liveness probe responds with |


### Peer Push Results JSON Schema 
(output of the APIs that push configuration to peer pods or clear/check them: targets, jobs, track, probes, health, cleanup, locker clear, results clear, ...)

A map of peer name to a map of pod address to a boolean, `true` when the pod accepted the request (or was healthy), `false` when it failed it or couldn't be reached, or when the pod was skipped because it was already marked unhealthy. Pods that were loaded from a dump or cloned from another registry and have not reconnected (`offline`) are not called and don't appear in the map. A peer without callable pods (or one that has nothing saved for the operation) shows up as `{}`. The HTTP status is `200` unless some peer that has pods got no successful pod, in which case it is `424`.

```json
{
  "peerA": {"10.0.0.1:8080": true, "10.0.0.2:8080": false},
  "peerB": {}
}
```


### Peer Call JSON Schema 
(output of /registry/peers/{peer}/call and /registry/peers/call)

A map of peer name to a map of pod address to the response of the pod: the parsed JSON when the pod answered with a JSON content type, else the body text (an empty string if the pod couldn't be reached). Pods that are offline or unhealthy are skipped.

```json
{"peerA": {"10.0.0.1:8080": {"version": "..."}, "10.0.0.2:8080": ""}}
```


### Peer APIs JSON Schema 
(output of /registry/peers/{peer}/apis and /registry/peers/apis)

A map of peer name to a map with an `all` list holding the registry URLs (`.../registry/peers/{peer}/call?uri=...`) that invoke each API on all pods of the peer, plus one list per pod name holding the direct URLs of the same APIs on that pod.

```json
{"peerA": {"all": ["http://registry:8080/registry/peers/peerA/call?uri=/version", "..."],
           "pod1": ["http://10.0.0.1:8080/version", "..."]}}
```


### Registry Dump JSON Schema 
(output of GET /registry/dump, input of POST /registry/load, and what /registry/cloneFrom reads)

|Field|Data Type|Description|
|---|---|---|
| lockers | map string->[Locker](#locker-json-schema) | All the labeled lockers with their data and peer lockers (sub-lockers nested under `subLockers`). Context lockers are not included |
| peers | map string->[Peers](#peers-json-schema) | Registered peers |
| peerTargets | map string->(map string->[Peer Target](#peer-target-json-schema)) | Targets by peer and target name |
| peerJobs | map string->(map string->[Peer Job](#peer-job-json-schema)) | Jobs by peer and job name |
| peerTrackingHeaders | string | Comma-separated tracking headers |
| peerProbes | [Peer Probes](#peer-probes-json-schema) | Probe configuration |

The tracking time buckets, job scripts and job files are not part of the dump. When loading, only the sections present in the payload are applied, and they are applied only if all of the sections can be parsed (else `400` and nothing changes).


### Locker JSON Schema 
(output of /registry/lockers, /registry/lockers/{label} and the other locker APIs)

Lockers are organized as `labeled lockers`. A `CombiLocker` (the labeled locker) holds a data locker for arbitrary data, a locker per peer, and child lockers.

|Field|Data Type|Description|
|---|---|---|
| label | string | Label of this locker (for a child locker, only its own segment of the CSV path) |
| current | bool | Whether this is the active locker where peers' results are stored. Opening a nested locker (`A,B`) marks `A` as well as `B` as current |
| firstOpened | time | When the locker was first opened |
| lastOpened | time | When the locker was last opened |
| dataLocker | LockerKeys | Data stored via the `store` APIs, see below |
| peerLockers | map string->PeerLocker | Data of peers, keyed by the lower-cased peer label |
| subLockers | map string->Locker | Child lockers opened using comma-separated labels |

`LockerKeys` is a map from key to a value object where the keys of the next level are inlined next to the data of the key: 

|Field|Data Type|Description|
|---|---|---|
| data | string or list | Data stored at this key path. A string for `store`; a list of strings after `add`. Shown as `"..."` unless the request asked for the data (`data=y`). In `GET /registry/lockers/{label}?data=y` a text that is a JSON object is shown as an object, while `/get/{path}` returns it as the stored text |
| firstReported | time | Only on top-level keys: when the key was first stored |
| lastReported | time | Only on top-level keys: when the key was last stored |
| {key} | LockerKeys value | Each sub-key (a path `a,b` stores `b` under `a`). Levels beyond the requested `level` are shown as `"...": ""` |

`PeerLocker` carries `instanceLockers` (a map of pod address to `LockerKeys`, holding the data stored for each instance of the peer, including the client results that the instance reports under the key `client`), and then the data stored for the peer as a whole inlined as `LockerKeys`, e.g.:

```json
{
  "label": "Z", "current": true,
  "firstOpened": "2026-11-07T16:03:22.583404-08:00", "lastOpened": "2026-11-07T16:03:22.583404-08:00",
  "dataLocker": {
    "k1": {"data": ["v2"], "firstReported": "...", "lastReported": "...", "k2": {"data": "v1"}}
  },
  "peerLockers": {
    "peerb": {
      "instanceLockers": {"1.2.3.4:5": {"notes": {"1": {"data": "hello"}, "firstReported": "...", "lastReported": "..."}}},
      "own": {"k": {"data": "ownval"}, "firstReported": "...", "lastReported": "..."}
    }
  },
  "subLockers": {"Y": {"label": "Y", "current": true, "dataLocker": {}, "peerLockers": {}, "subLockers": {}, "firstOpened": "...", "lastOpened": "..."}}
}
```

Other locker outputs:

|API|Output|
|---|---|
| /registry/lockers/labels | map of locker label to `{current, firstOpened, lastOpened}`; for a locker that has child lockers, a map of path label to the same info, with the locker itself under `"."` and each child under its path label (`Z,Y`) |
| /registry/lockers/{label}/data/keys | map of locker label to `{"data": ["k1", "k1,k2"], "peerLockers": {...}, "subLockers": {...}}`, where `data` lists the key paths that hold data |
| /registry/lockers/{label}/data/paths | same shape as keys, but `data` is a nested map of key to the URI that reads it (a key that has both data and sub-keys has its own URI under `"."`) |
| /registry/lockers/search/{text} | same shape as paths, limited to the keys matching `text` |
| /registry/lockers/{label}/get/{path} | the text stored at the path (a list prints as `[a b]`), else the sub-tree as `LockerKeys`, else `{}` |
| /registry/lockers/{label}/data | map of locker label to `LockerKeys` (the data lockers alone) |
| /registry/peers/lockers, /registry/peers/{peer}/lockers, /registry/lockers/{label}/peers, /registry/lockers/{label}/peers/{peer} | map of lower-cased peer label to a map of the locker path label (e.g. `default`, or `Z,Y`) to the `PeerLocker` of that peer in that locker. A peer without any data shows up with `{}` |
| /registry/peers/{peer}/{address}/lockers, /registry/lockers/{label}/peers/{peer}/{address} | same, but the innermost value is the `LockerKeys` of just that instance |
