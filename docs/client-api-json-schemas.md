#### Client Target JSON Schema

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| name         | string         || Name for this target |
| protocol     | string         |`HTTP/1.1`| Request Protocol to use. Supports `HTTP/1.1` (default) and `HTTP/2.0`.|
| method       | string         || HTTP method to use for this target |
| url          | string         || URL for this target   |
| burls        | []string       || Secondary URLs to use for `fallback` or `AB Mode` (see below)   |
| headers      | [][]string     || Headers to be sent to this target |
| body         | string         || Request body to use for this target|
| autoPayload  | string         || Auto-generate request payload of the given size, specified as a numeric value (e.g. `1000`) or a byte size using suffixes `K`, `KB`, `M` and `MB` (e.g. `1K`). Use of auto payload causes the `body` field to be ignored. For response payload, use `/echo/body` API as target if the destination is also a `goto` server, or see response payload feature.  |
| replicas     | int            |1| Number of parallel invocations to be done for this target. |
| requestCount | int            |1| Number of requests to be made per replica for this target. The final request count becomes replicas * requestCount   |
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
| fallback     | bool  |false| If enabled, retry attempts will use secondary urls (`burls`) instead of the primary url. The query param `x-request-id` will carry suffixes of `-<counter>` for each retry attempt. |
| ab      | bool |false| If enabled, each request will simultaneously be sent to all secondary urls (`burls`) in addition to the primary url. The query param `x-request-id` will carry suffixes of `-B-<index>` for each secondary URL. |
| random      | bool |false| If enabled, each request will pick a random URL from either the primary URL or the B-URLs. |
| streamPayload  | []string  || If specified, elements of this array are sent to the target server as a stream with delay between each chunk applied based on `streamDelay`. |
| streamDelay | duration |"10ms"| For streaming request payload (`streamPayload` field), this configures the delay to be applied between payload chunks. |
| binary      | bool |false| Indicates whether request and response payload should be treated as binary data for this target |
| collectResponse | bool  |false| Indicates whether response payload should be kept in the results for this target. By default, response payload is discarded. |
| assertions | []Asssert  || List of assertions to be validated against each invocation response. Multiple assertions are applied with logical `OR` (disjunction) so that any one of them passing causes the result to be treated as passed. If all assertions fail, `errors` array in the result will contain the failure details.  |
| autoUpgrade  | bool           |false| Whether client should negotiate auto-upgrade from http/1.1 to http/2. |
| verifyTLS    | bool           |false| Whether the TLS certificate presented by the target is verified. (Also see `--certs` command arg) |


#### Assertion JSON Schema

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| statusCode | int   || Validate response status code to match this value. Status code must be specified if expectation is given for a target. |
| payloadSize | int   || Validate response payload size to match this value |
| payload | string   || Validate response payload to match this value. For binary paylods, the expected payload should be specified as base64 encoded string |
| headers | map[string]string   || Validate response headers to contain these headers and optionally header values |
| retries | int   || If specified, the response must have the same number of retry attempts. |
| failedURL | string   || If specified, the response must have a failure recorded for this URL (from the A/B URLs) |
| successURL | string   || If specified, the response must have a success recorded for this URL (from the A/B URLs) |


#### Client Results Schema (output of API /client/results)

The results are keyed by targets, with an empty key "" used to capture all results (across all targets) if "capturing of all results" is enabled (via API `/client/results/all/`{enable}``).
The schema below describes fields per target.

|Field|Data Type|Description|
|---|---|---|
| target            | string | Target for which these results are captured |
| invocationCounts      | int                 | Total requests sent to this target |
| firstResultAt        | time                | Time of first result received from the target |
| lastResultAt         | time                | Time of last result received from the target |
| retriedInvocationCounts | int | Total requests to this target that were retried at least once |
| countsByHeaders      | string->HeaderCounts   | Response counts by header, with detailed info captured in `HeaderCounts` object described below |
| countsByStatus       | string->int   | Response counts by HTTP Status |
| countsByStatusCodes  | string->TimeBucketsCounts   | Response counts by HTTP Status Code |
| countsByURIs         | string->KeyResultCounts   | Response counts by URI |
| countsByRequestPayloadSizes | string->KeyResultCounts   | Response counts by request payload sizes |
| countsByResponsePayloadSizes | string->KeyResultCounts   | Response counts by response payload sizes |
| countsByRetries | string->KeyResultCounts   | Response counts by number of retries |
| countsByRetryReasons | string->KeyResultCounts   | Response counts by retry reasons |
| countsByErrors | string->KeyResultCounts   | Response counts by error type (relevant for response validations) |
| countsByTimeBuckets | string->StatusCodeCounts   | Response counts by time buckets if defined |

#### HeaderCounts schema

The schema below describes fields of HeaderCounts json (used in `countsByHeaders` result field).

|Field|Data Type|Description|
|---|---|---|
| header            | string | Header for which these results are captured |
| count       | int | number of responses for this header  |
| retries     | int | number of requests that were retried for this header |
| firstResultAt | time | Time of first result for this header  |
| lastResultAt  | time | Time of last result for this header |
| countsByValues | string->CountInfo   | request counts info per header value for this header |
| countsByStatusCodes | int->CountInfo   | request counts info per status code for this header |
| countsByValuesStatusCodes | string->int->CountInfo   | request counts info per status code per header value for this header |
| crossHeaders | string->HeaderCounts   | HeaderCounts for each cross-header for this header |
| crossHeadersByValues | string->string->HeaderCounts   | HeaderCounts for each cross-header per header value for this header |


#### KeyResultCounts schema

The schema below describes fields of json object used to report data related to invocation counts for various fields, e.g. `countsByURIs`.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this key  |
| retries     | int | number of requests that were retried for this key |
| firstResultAt | time | Time of first result for this key  |
| lastResultAt  | time | Time of last result for this key |
| byStatusCodes | string->StatusCodeCounts   | counts for this key broken down by status codes |
| byTimeBuckets | string->TimeBucketsCounts   | counts for this key broken down by response time buckets |

#### StatusCodeCounts schema

The schema below describes fields of json object used in `countsByStatusCodes` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this status code  |
| retries     | int | number of requests that were retried for this status code |
| firstResultAt | time | Time of first result for this status code  |
| lastResultAt  | time | Time of last result for this status code |
| byTimeBuckets | string->TimeBucketsCounts   | counts for this status code broken down by response time buckets (except when the status code counts is already a sub-result of time bucket counts) |


#### TimeBucketsCounts schema

The schema below describes fields of json object used in `countsByTimeBuckets` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this time bucket  |
| retries     | int | number of requests that were retried for this time bucket |
| firstResultAt | time | Time of first result for this time bucket  |
| lastResultAt  | time | Time of last result for this time bucket |
| countsByStatusCodes | string->StatusCodeCounts   | counts for this time bucket broken down by status codes (except when the time bucket counts is already a sub-result of status code counts) |


#### CountInfo schema

The schema below describes fields per target.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses in this set  |
| retries     | int | number of requests that were retried in this set |
| firstResultAt | time | Time of first result in this set  |
| lastResultAt  | time | Time of last result in this set |

#### Invocation Results Schema (output of API /client/results/invocations)

- Reports results for all invocations since last clearing of results, as an Object with invocation counter as key and following JSON as result. Invocation results are not retained unless explicitly enabled via API `/results/invocations/{enable}`.

|Field|Data Type|Description|
|---|---|---|
| invocationIndex | int | invocation counter  |
| target     | Target | target of this invocation. See `Client Target JSON Schema` |
| status | InvocationStatus | See `InvocationStatus` schema below. |
| results  | []InvocationResult | One result per request sent for this invocation. See `InvocationResult` schema below. |



#### InvocationStatus Schema

- Used to report status of an invocation within `Invocation Results Schema`.

|Field|Data Type|Description|
|---|---|---|
| totalRequests | int | total requests made to the target for this invocation, including retries  |
| completedRequests | int | number of completed requests (totalRequests - retriesCount)  |
| successCount | int | number of successful requests  |
| failureCount | int | number of failed requests  |
| retriesCount | int | number of retries made across all requests  |
| abCount | int | number of requests made for A/B comparison if configured  |
| firstRequestAt | time | time when first request completed  |
| lastRequestAt | time | time when last request completed  |
| stopRequested | bool | whether a request was made to stop this invocation while it's running  |
| stopped | bool | whether the invocation has been stopped, either due to stop request or it finished all requests  |
| closed | bool | whether the invocation has been marked as `finished` after being stopped.  |


#### InvocationResult Schema

- Used to report result of each invocation request within `Invocation Results Schema`.

|Field|Data Type|Description|
|---|---|---|
| targetName | string | name of the target this invocation  |
| targetID | string | target id of the target this invocation  |
| status | string | response status text  |
| statusCode | int | response status code  |
| requestPayloadSize | int | request payload size (if request had a payload)  |
| responsePayloadSize | int | response payload size (if response had a payload)  |
| firstByteOutAt | string | time when first request payload byte was sent  |
| lastByteOutAt | string | time when last request payload byte was sent  |
| firstByteInAt | string | time when first reponse payload byte was read  |
| lastByteInAt | string | time when last reponse payload byte was read  |
| firstRequestAt | time | time when first request completed  |
| lastRequestAt | time | time when last request completed (if retries were made)  |
| retries | int | number of retries done for this request  |
| url | string | url of this request  |
| uri | string | uri of this request  |
| requestID | string | id of this request  |
| headers | map[string][]string | response headers received  |
| failedURLs | map[string]int | A/B URLs that got a failure response. The `url` field would contain the URL that succeeded if any. |
| retryURL | string | if request was retried due to an error or response code, the last retry url  |
| lastRetryReason | string | if request was retried due to an error or response code, the reason for last retry  |
| validAssertionIndex | int | index of the assertion that passed validation  |
| errors | map[string]any | validation or other errors if any  |
| tookNanos | int | total time taken by this request as observed by the clinet  |



#### Active Targets Schema (output of API /client/targets/active)

- Reports set of targets for which traffic is running at the time of API invocation. Result is an object with results grouped by target name, then grouped by invocation counter for the target (if same target has multiple ongoing invocations), and `InvocationStatus` as the value object. See `InvocationStatus` schema above.
