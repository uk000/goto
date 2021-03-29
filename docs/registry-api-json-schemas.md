
### Peer JSON Schema 
(to register a peer via /registry/peers/add)

|Field|Data Type|Description|
|---|---|---|
| name      | string | Name/Label of a peer |
| namespace | string | Namespace of the peer instance (if available, else `local`) |
| pod       | string | Pod/Hostname of the peer instance |
| address   | string | IP address of the peer instance |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |

### Peers JSON Schema 
Map of peer labels to peer details, where each peer details include the following info
(output of /registry/peers)

|Field|Data Type|Description|
|---|---|---|
| name      | string | Name/Label of a peer |
| namespace | string | Namespace of the peer instance (if available, else `local`) |
| pods      | map string->PodDetails | Map of Pod Addresses to Pod Details. See [Pod Details JSON Schema] below(#pod-details-json-schema) |
| podEpochs | map string->[]PodEpoch   | Past lives of this pod since last cleanup. |


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
** Same as [Client Target JSON Schema](../README.md#client-target-json-schema)

### Peer Job JSON Schema
** Same as [Jobs JSON Schema](../README.md#job-json-schema)


#### Peers Client Summary Results Schema (output of API /registry/peers/client/results/summary)

Results are keyed by peer name, with each result carrying summary results aggregated from all instances of this peer.

|Field|Data Type|Description|
|---|---|---|
| invocationCounts      | int  | Total requests sent |
| firstResultAt        | time  | Time of first result |
| lastResultAt         | time  | Time of last result |
| countsByStatusCodes  | string->int   | Response counts by HTTP Status Codes, broken down |
| countsByHeaders      | string->int   | Response counts by header|
| countsByHeaderValues | string->string->int   | Response counts by header values |
| countsByURIs         | string->int   | Response counts by URI |
| countsByRequestPayloadSizes | string->int   | Response counts by request payload sizes |
| countsByResponsePayloadSizes | string->int   | Response counts by response payload sizes |
| countsByRetries | string->int   | Response counts by number of retries |
| countsByRetryReasons | string->int   | Response counts by retry reasons |
| countsByErrors | string->int   | Response counts by error type (relevant for response validations) |
| countsByTimeBuckets | string->int   | Response counts by time buckets if defined |
| byTargets | string->SummaryResults | All the above summary counts broken down per target |


#### Peers Client Detailed Results Schema (output of API /registry/peers/client/results/details)

Results contains `summary` and `details` keys, each carrying results keyed by peer name. The summary results are same as `Peers Client Summary Results` described above, broken down by peers. The detailed results have the following schema per peer instance.

|Field|Data Type|Description|
|---|---|---|
| invocationCounts      | int  | Total requests sent |
| firstResultAt        | time  | Time of first result |
| lastResultAt         | time  | Time of last result |
| countsByStatusCodes  | string->SummaryCounts   | Response counts by HTTP Status Codes, broken down |
| countsByHeaders      | string->int   | Response counts by header|
| countsByHeaderValues | string->string->int   | Response counts by header values |
| countsByURIs         | string->SummaryCounts   | Response counts by URI |
| countsByRequestPayloadSizes | string->SummaryCounts   | Response counts by request payload sizes |
| countsByResponsePayloadSizes | string->SummaryCounts   | Response counts by response payload sizes |
| countsByRetries | string->SummaryCounts   | Response counts by number of retries |
| countsByRetryReasons | string->SummaryCounts   | Response counts by retry reasons |
| countsByErrors | string->SummaryCounts   | Response counts by error type (relevant for response validations) |
| countsByTimeBuckets | string->SummaryCounts   | Response counts by time buckets if defined |
| byTargets | string->DetailedResults | All the above aggregate counts broken down per target |



#### Peers Instances Client Results Schema (output of API /registry/peers/instances/client/results/details)

Results contains `summary` and `details` keys, each carrying results keyed by peer name and peer instance addresses. The summary results are same as `Peers Client Summary Results` described above, but broken down by peer instances. The detailed results have the following schema per peer instance.

|Field|Data Type|Description|
|---|---|---|
| invocationCounts      | int  | Total requests sent |
| firstResultAt        | time  | Time of first result |
| lastResultAt         | time  | Time of last result |
| countsByStatusCodes  | string->SummaryCounts   | Response counts by HTTP Status Codes, broken down |
| countsByHeaders      | string->int   | Response counts by header|
| countsByHeaderValues | string->string->int   | Response counts by header values |
| countsByURIs         | string->SummaryCounts   | Response counts by URI |
| countsByRequestPayloadSizes | string->SummaryCounts   | Response counts by request payload sizes |
| countsByResponsePayloadSizes | string->SummaryCounts   | Response counts by response payload sizes |
| countsByRetries | string->SummaryCounts   | Response counts by number of retries |
| countsByRetryReasons | string->SummaryCounts   | Response counts by retry reasons |
| countsByErrors | string->SummaryCounts   | Response counts by error type (relevant for response validations) |
| countsByTimeBuckets | string->SummaryCounts   | Response counts by time buckets if defined |
| byTargets | string->DetailedResults | All the above aggregate counts broken down per target |



#### SummaryCounts (used by Peers Results schema)

|Field|Data Type|Description|
|---|---|---|
| Count      | int  | Total requests sent for this set |
| byStatusCodes | string->int  | Total requests of this set broken down by status codes (except when under a status code) |
| ByTimeBuckets | string->int  | Total requests of this set broken down by time buckets (except when under a time bucket) |
