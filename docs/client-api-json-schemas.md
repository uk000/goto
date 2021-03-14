#### Client Target JSON Schema

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| name         | string         || Name for this target |
| method       | string         || HTTP method to use for this target |
| url          | string         || URL for this target   |
| burls        | []string       || Secondary URLs to use for `fallback` or `AB Mode` (see below)   |
| verifyTLS    | bool           |false| Whether the TLS certificate presented by the target is verified. (Also see `--certs` command arg) |
| headers      | [][]string     || Headers to be sent to this target |
| body         | string         || Request body to use for this target|
| autoPayload  | string         || Auto-generate payload of this size when making calls to this target. This field supports numeric sizes (e.g. `1000`) as well as byte size suffixes `K`, `KB`, `M` and `MB` (e.g. `1K`). If auto payload is specified, `body` field is ignored. |
| protocol     | string         |`HTTP/1.1`| Request Protocol to use. Supports `HTTP/1.1` (default) and `HTTP/2.0`.|
| autoUpgrade  | bool           |false| Whether client should negotiate auto-upgrade from http/1.1 to http/2. |
| replicas     | int            |1| Number of parallel invocations to be done for this target. |
| requestCount | int            |1| Number of requests to be made per replicas for this target. The final request count becomes replicas * requestCount   |
| initialDelay | duration       || Minimum delay to wait before starting traffic to a target. Actual delay will be the max of all the targets being invoked in a given round of invocation. |
| delay        | duration       |10ms| Minimum delay to be added per request. The actual added delay will be the max of all the targets being invoked in a given round of invocation, but guaranteed to be greater than this delay |
| retries      | int            |0| Number of retries to perform for requests to this target for connection errors or for `retriableStatusCodes`.|
| retryDelay   | duration       |1s| Time to wait between retries.|
| retriableStatusCodes| []int|| HTTP response status codes for which requests should be retried |
| sendID       | bool           |false| Whether or not a unique ID be sent with each client request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| connTimeout  | duration       |10s| Timeout for opening target connection |
| connIdleTimeout | duration    |5m| Idle Timeout for target connection |
| requestTimeout | duration     |30s| Timeout for HTTP requests to the target |
| autoInvoke   | bool           |false| Whether this target should be invoked as soon as it's added |
| fallback     | bool           |false| If enabled, retry attempts will use secondary urls (`burls`) instead of the primary url. The query param `x-request-id` will carry suffixes of `-<counter>` for each retry attempt. |
| ab      | bool           |false| If enabled, each request will simultaneously be sent to all secondary urls (`burls`) in addition to the primary url. The query param `x-request-id` will carry suffixes of `-B-<index>` for each secondary URL. |
| random      | bool           |false| If enabled, each request will pick a random URL from either the primary URL or the B-URLs. |



#### Client Results Schema (output of API /client/results)

The results are keyed by targets, with an empty key "" used to capture all results (across all targets) if "capturing of all results" is enabled (via API `/client/results/all/`{enable}``).
The schema below describes fields per target.

|Field|Data Type|Description|
|---|---|---|
| target            | string | Target for which these results are captured |
| invocationCounts      | int                 | Total requests sent to this target |
| firstResponse        | time                | Time of first response received from the target |
| lastResponse         | time                | Time of last response received from the target |
| retriedInvocationCounts | int | Total requests to this target that were retried at least once |
| countsByStatus       | string->int   | Response counts by HTTP Status |
| countsByStatusCodes  | string->int   | Response counts by HTTP Status Code |
| countsByHeaders      | string->HeaderCounts   | Response counts by header, with detailed info captured in `HeaderCounts` object described below |
| countsByURIs         | string->int   | Response counts by URIs |
| countsByTimeBuckets         | string->int   | Response counts by URIs |

#### HeaderCounts schema

The schema below describes fields of HeaderCounts json (used in `countsByHeaders` result field).

|Field|Data Type|Description|
|---|---|---|
| header            | string | Header for which these results are captured |
| count       | int | number of responses for this header  |
| retries     | int | number of requests that were retried for this header |
| firstResponse | time | Time of first response for this header  |
| lastResponse  | time | Time of last response for this header |
| countsByValues | string->CountInfo   | request counts info per header value for this header |
| countsByStatusCodes | int->CountInfo   | request counts info per status code for this header |
| countsByValuesStatusCodes | string->int->CountInfo   | request counts info per status code per header value for this header |
| crossHeaders | string->HeaderCounts   | HeaderCounts for each cross-header for this header |
| crossHeadersByValues | string->string->HeaderCounts   | HeaderCounts for each cross-header per header value for this header |
| firstResponse        | time | Time of first response received for this header |
| lastResponse         | time | Time of last response received for this header |


#### URICounts schema

The schema below describes fields of json object used in `countsByURIs` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this uri  |
| retries     | int | number of requests that were retried for this uri |
| firstResponse | time | Time of first response for this uri  |
| lastResponse  | time | Time of last response for this uri |
| countsByStatusCodes | string->StatusCodeCounts   | counts for this uri broken down by status codes |
| countsByTimeBuckets | string->TimeBucketsCounts   | counts for this uri broken down by response time buckets |

#### StatusCodeCounts schema

The schema below describes fields of json object used in `countsByStatusCodes` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this status code  |
| retries     | int | number of requests that were retried for this status code |
| firstResponse | time | Time of first response for this status code  |
| lastResponse  | time | Time of last response for this status code |
| countsByTimeBuckets | string->TimeBucketsCounts   | counts for this status code broken down by response time buckets (except when the status code counts is already a sub-result of time bucket counts) |


#### TimeBucketsCounts schema

The schema below describes fields of json object used in `countsByTimeBuckets` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this time bucket  |
| retries     | int | number of requests that were retried for this time bucket |
| firstResponse | time | Time of first response for this time bucket  |
| lastResponse  | time | Time of last response for this time bucket |
| countsByStatusCodes | string->StatusCodeCounts   | counts for this time bucket broken down by status codes (except when the time bucket counts is already a sub-result of status code counts) |


#### CountInfo schema

The schema below describes fields per target.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses in this set  |
| retries     | int | number of requests that were retried in this set |
| firstResponse | time | Time of first response in this set  |
| lastResponse  | time | Time of last response received in this set |

#### Invocation Results Schema (output of API /client/results/invocations)

- Reports results for all invocations since last clearing of results, as an Object with invocation counter as key and invocation's results as value. The results for each invocation have same schema as `Client Results Schema`, with an additional bool flag `finished` to indicate whether the invocation is still running or has finished. See example below.

#### Active Targets Schema (output of API /client/targets/active)

- Reports set of targets for which traffic is running at the time of API invocation. Result is an object with invocation counter as key, and value as object that has status for all active targets in that invocation. For each active target, the following data is reported. Also see example below.

|Field|Data Type|Description|
|---|---|---|
| target                | Client Target       | Target details as described in `Client Target JSON Schema`  |
| completedRequestCount | int                 | Number of requests completed for this target in this invocation |
| stopRequested         | bool                | Whether `stop` has been requested for this target |
| stopped               | bool                | Whether the target has already stopped. Quite likely this will not show up as true, because the target gets removed from active set soon after it's stopped |
