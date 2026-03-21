
# HTTP Proxy
- Any HTTP(S) listener that you open in `goto` is ready to act as an http proxy in addition to the other server duties it performs.
- To use an HTTP(S) listener as a proxy, all you need to do is add one or more upstream targets. The request processing path in `goto` checks for the presence of proxy targets for the listener where a request arrives. If any proxy target is defined on the listener, `goto` matches the request with those targets and forwards it to the matched targets. If no match found, the listener processes the request as a server locally.

## Proxy Targets
Upstream targets can be defined in one of the two ways:
- By posting a JSON spec to `/proxy/http/targets/add` API.
- Feeding the targets through a startup YAML config (See Goto Start Configs).
An upstream target is defined as a set of endpoints, along with trigger definitions that provide match criteria controlling when specific endpoints are invoked.

## Proxy Target Triggers
A target must be defined with one or more triggers. Each trigger defines one or more sets of match criteria as `matchAny` list.
- Each match criteria in the `matchAny` list must define a `uriPrefix` at the minimum. A trigger can be defined to accept all requests by setting URI prefix match to `/`.
- A trigger can define aditional match criteria based on HTTP headers. Each header match criteria defines an exact match on header name, and optionally a match on header value.
- URI match `/` has the special behavior of matching all traffic.
   
    Example: if a `MatchAny` criteria is defined as `{"uriPrefix": "/foo", "headers": {"Foo":""}}`, any inbound request that has uri prefix of `/foo/...` and header `Foo: ...` will cause the trigger to satisfy and the endpoints identified by the trigger to be included in the proxy invocation set.


- URI and header match support use of variables with syntax `{foo}` to indicate the variable portion of a URI/header value, and to capture the value that appears at that position at runtime. 
        
    For example, `/foo/{f}/bar/{b}` will match URIs like `/foo/123/bar/abc`, `/foo/something/bar/otherthing`, etc. The variables are captured under the given labels (`f` and `b` in the previous example).
    
- Additional match constraints can be defined for the values of variables, to precisely control a trigger's match criteria.
- The captured variables can be referenced in transformations to allow runtime capturing and applying of values from one part of inbound HTTP request to another part of outbound HTTP request.
      

## Target Transformation

- `Goto` HTTP proxy can perform the following HTTP transformations before forwarding a received request to the upstream target.
  - URI rewrite
  - Add/remove headers.
  - Add/remove query params.
  - Auto-assign a Request ID to the proxied request (logical ID or a UUID)
  - Convert between HTTP/HTTPS and HTTP11.1-H2 protocols.
  - Route a single request to multiple upstream targets and send the combined responses back to the downstream client.
- Outside of the specified transformations, downstream HTTP metadata (URI, headers, query params) get applied to the upstream request as-is except for the necessary adjustments of headers like Content-Length, Host/Authority etc.
- Note that if you use an HTTPS listener as a proxy, `goto` will perform TLS termination and will redo TLS for upstream HTTPS endpoints. If you need a passthrough proxy, you can use a TCP listener in goto to act as a TCP proxy for HTTP traffic.
- Transformations can be defined per target (applied to all endpoints of a target) or per trigger (applied to all endpoints selected by the trigger). This allows for the same downstream request to result in different upstream requests to different endpoints.



## Traffic Config
- Traffic config allows for optional control over delay and retries to be applied to the upstream requests.
- Additional flag `clean` allows control over whether a single upstream response should be sent to the downstream client as-is. (See additional details in the Response section below)

## Response
- The default proxy response behavior is to wrap all upstream responses (response headers, response code, and call completion summary info) into a single response payload keyed by the target and endpoint names. The response headers that the downstream client receives by default are those sent by the proxy `Goto` instance.
- Traffic config flag `clean: true` changes the default behavior such that proxy will pick the first response from upstream endpoint invocations and send the response headers and payload as-is to the downstream client, adding additional proxy response headers to indicate that the call was proxied. The `clean` mode ignores the responses from additional endpoints, and allows downstream client to operate on the response as if the client was directly connected to the proxied upstream endpoint.


## Chaos
In addition to request routing, the `goto` proxy also offers some chaos features:
- A `delay` can be configured per target that's applied to all communication in either direction for that target. For HTTP proxy, the delay is applied before sending the request and response.
    > Note that this delay feature is separate from the one offered by `Goto` as a Server, via [URIs](#-uris) and [Response Delay](#-response-delay). The URI delay feature in particular allows for more fine-grained delay configuration that can be used in conjunction with the proxy feature to apply delays to URIs that eventually get routed via proxy.
- The HTTP proxy transformations provide another way of introducing unexpected circumstances for the client and service, by intentionally dropping or adding headers and changing URI values. For example, configure the proxy to remove a required header, or change some header's value, and observe whether the two parties deal with the situation gracefully.
- Additional chaos can be applied via listener API, by closing/reopening a connection.


#### HTTP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /proxy/http/enable | Enables HTTP proxy on the current port on which API is called (or port referenced by `/port=<port>` prefix in the URI) |
| PUT, POST | /proxy/http/disable | Disables HTTP proxy on the current port on which API is called (or port referenced by `/port=<port>` prefix in the URI) |
| PUT, POST | /proxy/http/responses/set | Set proxy response mappings that translate upstream response status codes to a proxy response status code. Accepts a JSON array of `Proxy Response` objects. See `Proxy Response JSON Schema`. |
| GET | /proxy/http/responses | Get the currently configured proxy response mappings |
| GET | /proxy/http | Get proxy details for the current port (or port referenced by `/port=<port>` prefix in the URI) |
| GET | /proxy/http/all | Get proxy details for all ports |
| PUT, POST | /proxy/http/targets/add | Add a new target based on the JSON payload given in request body. See `HTTP Proxy Target JSON Schema` for payload details. |
| PUT, POST | /proxy/http/targets/clear | Clear all proxy targets for the current port |
| PUT, POST | /proxy/http/targets/`{target}`/remove | Remove the proxy target with the given name |
| PUT, POST | /proxy/http/targets/`{target}`/enable | Enable the proxy target with the given name |
| PUT, POST | /proxy/http/targets/`{target}`/disable | Disable the proxy target with the given name |
| GET | /proxy/http/targets | Get all proxy targets for the current port |
| GET | /proxy/http/targets/all | Get all proxy targets for all ports |
| GET | /proxy/http/targets/`{target}`/tracker | Get tracking data for the given target on the current port |
| GET | /proxy/http/trackers | Get all HTTP proxy tracking data for the current port |
| GET | /proxy/http/trackers/all | Get all HTTP proxy tracking data for all ports |
| POST | /proxy/trackers/clear | Clear HTTP proxy tracking data for the current port |
| POST | /proxy/trackers/all/clear | Clear HTTP proxy tracking data for all ports |

#### HTTP Proxy JSON Schema

|Field|Data Type|Description|
|---|---|---|
| port | `int` | Port of the proxy |
| enabled | `bool` | Whether or not the proxy is currently active |
| targets | `map[string]Target` | JSON object containing target definitions keyed by target name. See `HTTP Proxy Target JSON Schema` |
| proxyResponses | `[]ProxyResponse` | Array of proxy response mappings. See `Proxy Response JSON Schema` |
| tracker | `HTTPProxyTracker` | Tracking data for the proxy. See `HTTP Proxy Tracker JSON Schema` |

#### Proxy Response JSON Schema

|Field|Data Type|Description|
|---|---|---|
| upResponseRange | `[]int` | Two-element array `[min, max]` defining the upstream response status code range to match |
| proxyResponse | `int` | The HTTP status code the proxy should return when the upstream response falls within the `upResponseRange` |

#### HTTP Proxy Target JSON Schema

|Field|Data Type|Description|
|---|---|---|
| name | `string` | Name for this target (required) |
| enabled | `bool` | Whether or not the proxy target is currently active |
| endpoints | `map[string]TargetEndpoint` | JSON object containing endpoint definitions keyed by endpoint name. See `HTTP Proxy Target Endpoint JSON Schema` (required) |
| triggers | `map[string]TargetTrigger` | JSON object containing trigger definitions keyed by trigger name. At least one trigger is required. See `HTTP Proxy Target Trigger JSON Schema` (required) |
| transform | `TrafficTransform` | Optional transform configuration applied to all triggers unless overridden at trigger level. See `HTTP Proxy Target Transform JSON Schema` |
| trafficConfig | `TrafficConfig` | Optional traffic configuration applied to all triggers unless overridden at trigger level. See `HTTP Proxy Traffic Config JSON Schema` |

#### HTTP Proxy Target Endpoint JSON Schema

|Field|Data Type|Description|
|---|---|---|
| url | `string` | URL of the upstream endpoint to forward requests to (required) |
| method | `string` | HTTP method for the upstream request. Defaults to the downstream request method if not specified |
| protocol | `string` | Protocol to use for the upstream request (e.g. `HTTP/1.1`, `HTTP/2`) |
| authority | `string` | Authority (host header) to use for the upstream request |
| tls | `bool` | Whether TLS should be used for the upstream connection |
| requestCount | `int` | Number of requests to send per invocation |
| concurrent | `int` | Number of concurrent replicas for the upstream request |

#### HTTP Proxy Target Trigger JSON Schema

|Field|Data Type|Description|
|---|---|---|
| matchAny | `[]TargetMatch` | Array of match criteria. A trigger matches if any one of these criteria matches the incoming request. At least one match entry is required. See `HTTP Proxy Target Match JSON Schema` |
| endpoints | `[]string` | Array of endpoint names (referencing keys in the target's `endpoints` map) to invoke when this trigger matches. At least one endpoint is required |
| transform | `TrafficTransform` | Optional transform configuration specific to this trigger, overrides target-level transform. See `HTTP Proxy Target Transform JSON Schema` |
| trafficConfig | `TrafficConfig` | Optional traffic configuration specific to this trigger, overrides target-level traffic config. See `HTTP Proxy Traffic Config JSON Schema` |

#### HTTP Proxy Target Match JSON Schema

|Field|Data Type|Description|
|---|---|---|
| uriPrefix | `string` | URI prefix pattern to match against the incoming request URI. Supports embedded path variables using `{varName}` syntax |
| headers | `map[string]string` | Map of header name to header value patterns to match. Values can be literal strings or `{varName}` fillers to capture header values |
| vars | `map[string]Match` | Map of variable name to match criteria for validating captured path/header variables. See `Match JSON Schema` |

#### Match JSON Schema

|Field|Data Type|Description|
|---|---|---|
| exact | `string` | Exact string value to match against |
| in | `[]string` | Array of string values; matches if the captured value equals any one of them |
| regex | `string` | Regular expression pattern to match against the captured value |
| inRegex | `[]string` | Array of regular expression patterns; matches if the captured value matches any one of them |

#### HTTP Proxy Target Transform JSON Schema

|Field|Data Type|Description|
|---|---|---|
| uriMap | `map[string]string` | Map of matched downstream URI to upstream URI. When a matched URI is found in this map, the request is forwarded to the mapped upstream URI instead |
| headers | `Keys` | Header transformation rules for the upstream request. See `Keys JSON Schema` |
| queries | `Keys` | Query parameter transformation rules for the upstream request. See `Keys JSON Schema` |
| stripURI | `string` | Regular expression pattern for stripping a portion of the URI. The matched portion is removed, and the remainder is forwarded |
| requestId | `RequestId` | Configuration for sending a request ID with the upstream request. See `Request ID JSON Schema` |

#### Keys JSON Schema

|Field|Data Type|Description|
|---|---|---|
| add | `map[string]string` | Map of key-value pairs to add to the upstream request |
| remove | `[]string` | Array of keys to remove from the upstream request |

#### Request ID JSON Schema

|Field|Data Type|Description|
|---|---|---|
| send | `bool` | Whether to send a request ID with the upstream request |
| uuid | `bool` | Whether to generate a UUID as the request ID |
| header | `string` | Header name to use for sending the request ID |
| query | `string` | Query parameter name to use for sending the request ID |

#### HTTP Proxy Traffic Config JSON Schema

|Field|Data Type|Description|
|---|---|---|
| delay | `Delay` | Delay to apply before forwarding the request. See `Delay JSON Schema` |
| retries | `int` | Number of retries for failed upstream requests |
| retryDelay | `Delay` | Delay between retries. See `Delay JSON Schema` |
| retryOn | `[]int` | Array of HTTP status codes that should trigger a retry |
| payload | `bool` | Whether to collect and return the upstream response payload |
| clean | `bool` | When true, returns the first upstream response payload directly instead of wrapping all responses in a JSON envelope |

#### Delay JSON Schema

|Field|Data Type|Description|
|---|---|---|
| min | `duration` | Minimum delay duration (e.g. `100ms`, `1s`, `2s500ms`) |
| max | `duration` | Maximum delay duration. When both `min` and `max` are specified, the actual delay is a random value between them |
| count | `int` | Number of requests to apply this delay to |

#### HTTP Proxy Tracker JSON Schema

|Field|Data Type|Description|
|---|---|---|
| downstreamRequestCount | `int` | Number of downstream requests received |
| upstreamRequestCount | `int` | Number of upstream requests sent |
| requestDropCount | `int` | Number of requests dropped |
| responseDropCount | `int` | Number of responses dropped |
| downstreamRequestCountsByURI | `map[string]int` | Number of downstream requests received, grouped by URIs |
| upstreamRequestCountsByURI | `map[string]int` | Number of upstream requests sent, grouped by URIs |
| requestDropCountsByURI | `map[string]int` | Number of requests dropped, grouped by URIs |
| responseDropCountsByURI | `map[string]int` | Number of responses dropped, grouped by URIs |
| uriMatchCounts | `map[string]int` | Number of downstream requests that were forwarded due to URI match, grouped by matching URIs |
| headerMatchCounts | `map[string]int` | Number of downstream requests that were forwarded due to header match, grouped by matching headers |
| headerValueMatchCounts | `map[string]map[string]int` | Number of downstream requests that were forwarded due to header+value match, grouped by matching headers and values |
| queryMatchCounts | `map[string]int` | Number of downstream requests that were forwarded due to query param match, grouped by matching query params |
| queryValueMatchCounts | `map[string]map[string]int` | Number of downstream requests that were forwarded due to query param+value match, grouped by matching query params and values |
| targetTrackers | `map[string]HTTPTargetTracker` | Tracking info per target. Each `HTTP Target Tracker JSON Schema` has same fields as above. |

#### HTTP Target Tracker JSON Schema
Same fields as `HTTP Proxy Tracker JSON Schema` above

## HTTP Proxy Examples
See [HTTP Proxy Examples][proxy-examples]


[proxy-examples]: ProxyExamples.md