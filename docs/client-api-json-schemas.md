#### Client Target JSON Schema

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| name         | string         || Name for this target (required) |
| protocol     | string         |`HTTP/1.1`| Request Protocol to use. Supports `HTTP/1.1` (default), `HTTP/1.0`, `HTTP/2`/`HTTP/2.0`/`H2`/`H2C`, `grpc` and `grpcs` (gRPC over TLS, sets `tls` to true). The value `tcp` is accepted by the API but a `tcp` target cannot be invoked. |
| host       | string         || HTTP Host/Authority. If not set, the `Host` header from `headers` is used, else the host of the URL. |
| method       | string         |`GET`| HTTP method to use for this target |
| service      | string         || Name of the GRPC Service. A proto must already be uploaded to the goto instance for this service. See `grpc` APIs for details. |
| url          | string         || URL for this target (required). For HTTP targets, `http://` is prefixed if the URL has no scheme. For gRPC targets, this is the `host:port` of the gRPC server. |
| burls        | []string       || Secondary URLs to use for `fallback` or `AB Mode` (see below)   |
| headers      | map[string]string     || Headers to be sent to this target, as a JSON object of header name to value (e.g. `{"x-foo": "bar"}`). An array value is rejected with a 400. Goto adds headers `From-Goto` and `From-Goto-Host` to every target it receives via the API. |
| lowerHeaders | bool           |false| Whether request header names should be sent in lower case. |
| body         | string         || Request body to use for this target|
| autoPayload  | string         || Auto-generate request payload of the given size, specified as a numeric value (e.g. `1000`) or a byte size using suffixes `K`, `KB`, `M` and `MB` (e.g. `1K`). Use of auto payload causes the `body` field to be ignored. For response payload, use `/echo/body` API as target if the destination is also a `goto` server, or see response payload feature.  |
| replicas     | int            |1| Number of parallel invocations to be done for this target. Negative values are invalid. |
| requestCount | int            |1| Number of requests to be made per replica for this target. The final request count becomes replicas * requestCount. It must not be less than `replicas`, and negative values are invalid. |
| warmupCount | int            |0| Number of requests to be sent as warmup before starting the real traffic.   |
| initialDelay | duration       || Minimum delay to wait before starting traffic to a target. Actual delay will be the max of all the targets being invoked in a given round of invocation. |
| delay        | duration       || Minimum delay to be added per request. The actual added delay will be the max of all the targets being invoked in a given round of invocation, but guaranteed to be greater than this delay |
| retries      | int            |0| Number of retries to perform for requests to this target for connection errors or for `retriableStatusCodes`.|
| retryDelay   | duration       |1s| Time to wait between retries.|
| retriableStatusCodes| []int|| HTTP response status codes for which requests should be retried |
| keepOpen   | duration (string)  || Applies to `grpc`/`grpcs` targets that use client or bidirectional streaming: if set (e.g. `"30s"`), the client keeps the stream open for this long after it has sent its messages. It has no effect on HTTP targets. |
| sendID       | bool           |false| Whether or not an ID be sent with each client request. If this flag is set, a query param `x-request-id` and a header `Goto-Request-Id` will be added to each request, which can help with tracing requests on the target servers. Note: in the current implementation the value sent is the target name (the unique per-request ID is reported in the result's `targetID` instead), so it's the same for all requests of a target. |
| requestId    | RequestId      || Finer control over request IDs, used only when `sendID` is false: `{"send": bool, "uuid": bool, "header": string, "query": string}`. `send` turns on sending of request IDs, `uuid` generates a new UUID per request instead of using the goto-generated ID, and `header` and `query` give the name of the header and/or query param that should carry the ID. |
| connTimeout  | duration       |5s| Timeout for opening target connection |
| connIdleTimeout | duration    |5m| Idle Timeout for target connection |
| requestTimeout | duration     |5m| Timeout for HTTP requests to the target |
| autoInvoke   | bool           |false| Whether this target should be invoked as soon as it's added |
| fallback     | bool  |false| If enabled, retry attempts will use secondary urls (`burls`) instead of the primary url. The query param `x-request-id` will carry suffixes of `-<counter>` for each retry attempt. |
| ab      | bool |false| If enabled, each request will simultaneously be sent to all secondary urls (`burls`) in addition to the primary url. The query param `x-request-id` will carry suffixes of `-B-<index>` for each secondary URL. |
| random      | bool |false| If enabled, each request will pick a random URL from either the primary URL or the B-URLs. |
| streamPayload  | []string  || If specified, elements of this array are sent to the target server as a stream with delay between each chunk applied based on `streamDelay`. |
| streamDelay | duration || For streaming request payload (`streamPayload` field), this configures the delay to be applied between payload chunks. |
| binary      | bool |false| Indicates whether request and response payload should be treated as binary data for this target |
| collectResponse | bool  |false| Indicates whether response payload should be kept in the results for this target. By default, response payload is discarded. (Currently the collected payload is not included in the `response.payload` field of the results, which is always empty; only `payloadSize` is reported.) |
| trackPayload   | bool   |false| Whether the response payload should be tracked (time of receipt of first and last bytes, total size, content, etc). Tracking payload is expensive, hence it's off by default. |
| assertions | []Assertion  || List of assertions to be validated against each invocation response. Multiple assertions are applied with logical `OR` (disjunction) so that any one of them passing causes the result to be treated as passed. If all assertions fail, `errors` array in the result will contain the failure details.  |
| autoUpgrade  | bool           |false| Whether client should negotiate auto-upgrade from http/1.1 to http/2. |
| verifyTLS    | bool           |false| Whether the TLS certificate presented by the target is verified. (Also see `--certs` command arg) |
| tls  | bool           |false| Whether client should send TLS traffic |
| noSNI  | bool           |false| Whether client should remove SNI header in TLS traffic |
| tlsVersion  | int (uint16)           |772| TLS version to use, as the numeric TLS version code: `769` (TLS 1.0), `770` (TLS 1.1), `771` (TLS 1.2), `772` (TLS 1.3). |
| clientCert  | string           || A pre-uploaded TLS cert/key pair to use as client cert |
| alpn  | ALPN           || ALPN configuration for the client, as an object `{"protos": ["h2","http/1.1"], "keepDefault": bool, "handshake": {...}}`. `protos` is the list of ALPNs that the client should present to the server for negotiation; `keepDefault` retains the default ALPNs in addition to `protos`; `handshake` optionally describes a TCP handshake to perform after ALPN negotiation: `{"protos": [...], "seq": [{"write": {"preamble": "text"}}, {"read": {"len": 10, "nextSize": false, "collect": true}}]}`. |


#### Assertion JSON Schema

Assertions are evaluated in order, and the first one that passes sets `validAssertionIndex` in the result to its 1-based position. If none passes, the result's `errors` array has one entry per failed assertion, with keys named after the failed fields (`statusCode`, `payloadLength`, `payload`, `headers`, `retries`, `successURL`, `failedURL`) each holding `{expected, actual}`, plus `assertionIndex` (1-based).

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| statusCode | int   || Validate response status code to match this value. Status code is always compared, so it must be specified in every assertion. |
| payloadSize | int   || Validate response payload size to match this value. Setting `payloadSize` or `payload` automatically turns on `collectResponse` and `trackPayload` for the target. |
| payload | string   || Validate response payload to match this value. For binary paylods, the expected payload should be specified as base64 encoded string |
| headers | map[string]string   || Validate response headers to contain these headers and optionally header values (header names are matched case-insensitively and values are treated as case-insensitive regular expressions) |
| retries | int   || If specified, the response must have the same number of retry attempts. |
| failedURL | string   || If specified, the response must have a failure recorded for this URL (from the A/B URLs) |
| successURL | string   || If specified, the response must have a success recorded for this URL (from the A/B URLs) |


#### Client Results Schema (output of API /client/results)

The results are keyed by targets, with an empty key `""` used to capture all results (across all targets). The `""` entry is always present in the output, but it's only populated if "capturing of all results" is enabled (via API `/client/results/all/{enable}`, where `{enable}` is one of `y`, `yes`, `true` or `1` to enable and anything else to disable). Per-target results are collected by default.
The schema below describes fields per target. Fields that have no data (e.g. `countsByHeaders` when no headers are tracked via `/client/track/headers`) are omitted from the output.

|Field|Data Type|Description|
|---|---|---|
| target            | string | Target for which these results are captured |
| invocationCount      | int                 | Total requests sent to this target |
| clientStreamCount      | int                 | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount      | int                 | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| firstResultAt        | time                | Time of first result received from the target |
| lastResultAt         | time                | Time of last result received from the target |
| retriedInvocationCounts | int | Total requests to this target that were retried at least once |
| countsByHeaders      | string->HeaderCounts   | Response counts by header, with detailed info captured in `HeaderCounts` object described below |
| countsByStatus       | string->int   | Response counts by HTTP Status |
| countsByStatusCodes  | string->StatusCodeCounts   | Response counts by HTTP Status Code |
| countsByURIs         | string->KeyResultCounts   | Response counts by URI (URIs are lower-cased) |
| countsByRequestPayloadSizes | string->KeyResultCounts   | Response counts by request payload sizes (only for requests that carried a payload) |
| countsByResponsePayloadSizes | string->KeyResultCounts   | Response counts by response payload sizes (only for responses that had a payload; response payload size is only tracked if `trackPayload` is enabled for the target) |
| countsByRetries | string->KeyResultCounts   | Response counts by number of retries |
| countsByRetryReasons | string->KeyResultCounts   | Response counts by retry reasons |
| countsByErrors | string->KeyResultCounts   | Response counts for requests that had validation `errors` (relevant for response assertions), keyed by the position (`0`, `1`, ...) of the error in the result's `errors` array |
| countsByTimeBuckets | string->TimeBucketsCounts   | Response counts by time buckets, if time buckets are configured via `/client/track/time`. The keys are of the form `[<low> <high>]` (in milliseconds, e.g. `[0 50]`), and a response that doesn't fit in any of the buckets is counted under the key `[]`. A bucket whose high value is 0 is open-ended. |

#### HeaderCounts schema

The schema below describes fields of HeaderCounts json (used in `countsByHeaders` result field).

|Field|Data Type|Description|
|---|---|---|
| header            | string | Header for which these results are captured |
| count       | int | number of responses for this header  |
| clientStreamCount | int | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount | int | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| retries     | int | number of requests that were retried for this header |
| firstResultAt | time | Time of first result for this header  |
| lastResultAt  | time | Time of last result for this header |
| countsByValues | string->CountInfo   | request counts info per header value for this header |
| countsByStatusCodes | string->CountInfo   | request counts info per status code for this header |
| countsByValuesStatusCodes | string->int->CountInfo   | request counts info per status code per header value for this header |
| crossHeaders | string->HeaderCounts   | HeaderCounts for each cross-header for this header |
| crossHeadersByValues | string->string->HeaderCounts   | HeaderCounts for each cross-header per header value for this header |


#### KeyResultCounts schema

The schema below describes fields of json object used to report data related to invocation counts for various fields, e.g. `countsByURIs`.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this key  |
| clientStreamCount | int | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount | int | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| retries     | int | number of requests that were retried for this key |
| firstResultAt | time | Time of first result for this key  |
| lastResultAt  | time | Time of last result for this key |
| byStatusCodes | string->CountInfo   | counts for this key broken down by status codes |
| byTimeBuckets | string->TimeBucketsCounts   | counts for this key broken down by response time buckets (only when time buckets are configured) |

#### StatusCodeCounts schema

The schema below describes fields of json object used in `countsByStatusCodes` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this status code  |
| clientStreamCount | int | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount | int | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| retries     | int | number of requests that were retried for this status code |
| firstResultAt | time | Time of first result for this status code  |
| lastResultAt  | time | Time of last result for this status code |
| byTimeBuckets | string->CountInfo   | counts for this status code broken down by response time buckets (except when the status code counts is already a sub-result of time bucket counts) |


#### TimeBucketsCounts schema

The schema below describes fields of json object used in `countsByTimeBuckets` result field.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses for this time bucket  |
| clientStreamCount | int | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount | int | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| retries     | int | number of requests that were retried for this time bucket |
| firstResultAt | time | Time of first result for this time bucket  |
| lastResultAt  | time | Time of last result for this time bucket |
| byStatusCodes | string->CountInfo   | counts for this time bucket broken down by status codes (except when the time bucket counts is already a sub-result of status code counts) |


#### CountInfo schema

The schema below describes fields per target.

|Field|Data Type|Description|
|---|---|---|
| count       | int | number of responses in this set  |
| clientStreamCount | int | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount | int | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| retries     | int | number of requests that were retried in this set |
| firstResultAt | time | Time of first result in this set  |
| lastResultAt  | time | Time of last result in this set |

#### Invocation Results Schema (output of API /client/results/invocations)

- Reports results for all invocations since last clearing of results, as an Object with invocation counter as key and following JSON as result. Invocation results are not retained unless explicitly enabled via API `/client/results/invocations/{enable}` (where `{enable}` is `y`, `yes`, `true` or `1`; any other value disables collection). While collection is disabled, `results` is empty.

|Field|Data Type|Description|
|---|---|---|
| invocationIndex | int | invocation counter  |
| target     | Target | target of this invocation. See `Client Target JSON Schema` |
| status | InvocationStatus | See `InvocationStatus` schema below. |
| results  | []InvocationResult | One result per request sent for this invocation. See `InvocationResult` schema below. |
| finished | bool | whether the invocation has finished and all its results have been collected |



#### InvocationStatus Schema

- Used to report status of an invocation within `Invocation Results Schema`, and for each running invocation in the output of `/client/targets/active`.

|Field|Data Type|Description|
|---|---|---|
| totalRequests | int | total requests made to the target for this invocation, including retries  |
| assignedRequests | int | number of requests that have been handed to the replicas so far (reaches `replicas * requestCount`)  |
| completedRequests | int | number of completed requests (totalRequests - retriesCount)  |
| successCount | int | number of requests that got a response (irrespective of the HTTP status code or assertion results)  |
| failureCount | int | number of requests that failed with a client-side error, e.g. connection failure or timeout  |
| retriesCount | int | number of retries made across all requests  |
| clientStreamCount | int | Total GRPC stream chunks sent by the client to this target. Only applicable to GRPC targets. |
| serverStreamCount | int | Total GRPC stream chunks received from the GRPC service. Only applicable to GRPC targets. |
| abCount | int | number of requests made for A/B comparison if configured  |
| firstRequestAt | string | time when first request completed  |
| lastRequestAt | string | time when last request completed  |
| stopRequested | bool | whether a request was made to stop this invocation while it's running  |
| stopped | bool | whether the invocation has been stopped by a stop request  |
| closed | bool | whether the invocation has finished (it ran to completion or was stopped) and its resources have been released.  |


#### InvocationResult Schema

- Used to report result of each invocation request within `Invocation Results Schema`. Request and response details are nested in the `request` and `response` objects.

|Field|Data Type|Description|
|---|---|---|
| targetName | string | name of the target this invocation  |
| targetID | string | unique id of this request, of the form `<target>[<runner>][<request number>]` (and `<target>-<customID>...` for invocations that carry a custom id). Also sent to the target as header `Goto-Target-ID`.  |
| request | InvocationResultRequest | details of the request, see below  |
| response | InvocationResultResponse | details of the response, see below  |
| failedURLs | map[string]int | A/B URLs that got a failure response, with failure counts. The `request.url` field would contain the URL that succeeded if any. |
| retries | int | number of retries done for this request  |
| lastRetryReason | string | if request was retried due to an error or response code, the reason for last retry  |
| validAssertionIndex | int | 1-based index of the assertion that passed validation (0 if no assertions are configured or none passed)  |
| errors | []map[string]any | assertion failures if any: one entry per failed assertion, see `Assertion JSON Schema` |
| tookNanos | int | total time taken by this request in nanoseconds, as observed by the client  |

##### InvocationResultRequest

|Field|Data Type|Description|
|---|---|---|
| id | string | id sent with this request when `sendID` or `requestId` is configured (currently this is the target name, see the `sendID` field)  |
| url | string | url of this request  |
| uri | string | uri of this request  |
| headers | map[string]string | request headers sent  |
| payloadSize | int | request payload size (if request had a payload)  |
| firstByteOutAt | string | time when first request payload byte was sent (if `trackPayload` is on) |
| lastByteOutAt | string | time when last request payload byte was sent (if `trackPayload` is on) |
| firstRequestAt | time | time when first request attempt completed  |
| lastRequestAt | time | time when last request attempt completed (differs from `firstRequestAt` if retries were made)  |

##### InvocationResultResponse

|Field|Data Type|Description|
|---|---|---|
| status | string | response status text, e.g. `200 OK` |
| statusCode | int | response status code  |
| headers | map[string][]string | response headers received, with header names in lower case  |
| payloadSize | int | response payload size in bytes (if response had a payload). For gRPC targets this is currently the number of response messages, not bytes.  |
| clientStreamCount | int | number of gRPC stream chunks sent by the client (gRPC targets only) |
| serverStreamCount | int | number of gRPC stream chunks received from the server (gRPC targets only) |
| payload | string | response payload text. Currently always empty for HTTP/gRPC client results (only `payloadSize` is reported). |
| firstByteInAt | string | time when first response payload byte was read (if `trackPayload` is on) |
| lastByteInAt | string | time when last response payload byte was read (if `trackPayload` is on) |
| peerCertInfo | object | details of the certificate presented by the server, for TLS requests |


#### Active Targets Schema (output of API /client/targets/active)

- Reports set of targets for which traffic is running at the time of API invocation. Result is an object with results grouped by target name, then grouped by invocation counter for the target (if same target has multiple ongoing invocations), and `InvocationStatus` as the value object. See `InvocationStatus` schema above.
