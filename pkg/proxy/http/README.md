# HTTP Proxy
- Any HTTP(S) listener that you open in `goto` is ready to act as an http proxy in addition to the other server duties it performs.
- To use an HTTP(S) listener as a proxy, all you need to do is add one or more upstream targets. The request processing path in `goto` checks for the presence of proxy targets for the listener where a request arrives. If any proxy target is defined on the listener, `goto` matches the request with those targets and forwards it to the matched targets. If no match found, the listener processes the request as a server locally.

## Proxy Targets
Upstream targets can be defined in one of the two ways:
- By posting a JSON or YAML spec to the `/proxy/http/targets/add` API (see [HTTP Proxy API Details](#http-proxy-api-details)).
- Feeding the targets through a startup YAML config (See Goto Start Configs, and [sample-proxy-config.yaml](../sample-proxy-config.yaml)). In the startup config the proxy has to have `enabled: true` too, otherwise the proxy of the port is disabled.
A new target is **disabled unless the spec has `enabled: true`** (or it is enabled later with the API), and a disabled target is not matched.
An upstream target is defined as a set of endpoints, along with trigger definitions that provide match criteria controlling when specific endpoints are invoked.

See [HTTP Proxy Examples][proxy-examples] for startup config YAML examples

## Proxy Target Triggers
A target must be defined with one or more triggers. Each trigger defines one or more sets of match criteria as `matchAny` list.
- Each match criteria in the `matchAny` list must define a `uri` (exact path) or a `uriPrefix`. A trigger can be defined to accept all requests by setting URI prefix match to `/`. Exact `uri` matches are tried before prefix matches, and the matching is done per target: every enabled target that has a matching trigger is invoked for the request, so one request can go to several targets.
- A trigger can define aditional match criteria based on HTTP headers. Each header match criteria defines an exact match on header name, and optionally a match on header value.
- URI match `/` has the special behavior of matching all traffic.
   
    Example: if a `MatchAny` criteria is defined as `{"uriPrefix": "/foo", "headers": {"Foo":""}}`, any inbound request that has uri prefix of `/foo/...` and header `Foo: ...` will cause the trigger to satisfy and the endpoints identified by the trigger to be included in the proxy invocation set.


- URI and header match support use of variables with syntax `{foo}` to indicate the variable portion of a URI/header value, and to capture the value that appears at that position at runtime. 
        
    For example, `/foo/{f}/bar/{b}` will match URIs like `/foo/123/bar/abc`, `/foo/something/bar/otherthing`, etc. The variables are captured under the given labels (`f` and `b` in the previous example).
    
- Additional match constraints (`vars`) can be defined for the values of variables, to precisely control a trigger's match criteria. (Constraints on variables captured from the URI path are currently never satisfied, see the limitations under [HTTP Proxy API Details](#http-proxy-api-details); constraints on header variables work.)
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
- Traffic config allows for optional control over delay and retries to be applied to the upstream requests, and over what the proxy returns to the downstream client.
- `delay` is applied before each upstream call. Note that a delay range (`min`-`max`) is drawn once, when the target is added, and the same value is used for every request of the target; its `count` is not applied by the HTTP proxy.
- `retries`, `retryDelay` and `retryOn` retry the upstream call when it gets one of the `retryOn` statuses.
- The flags `payload`, `jsonPayload`, `yamlPayload`, `transparent` and `stream` (also available per endpoint) control the response. (See additional details in the Response section below.) There is no `clean` flag.
- A trigger's `trafficConfig` replaces (does not merge with) the target's `trafficConfig` for the endpoints that the trigger invokes.

## Response
- The default proxy response behavior is to wrap all upstream responses (response headers, response code, and call completion summary info) into a single response payload keyed by the target and endpoint names. The response headers that the downstream client receives by default are those sent by the proxy `Goto` instance.
- Traffic config flag `transparent: true` changes the default behavior such that proxy will pick the first response from upstream endpoint invocations and send the response headers and payload as-is to the downstream client, adding additional proxy response headers to indicate that the call was proxied. The `transparent` mode ignores the responses from additional endpoints, and allows downstream client to operate on the response as if the client was directly connected to the proxied upstream endpoint.
- `jsonPayload: true` / `yamlPayload: true` return the first upstream response payload parsed as JSON / YAML (and re-written as such) instead of the envelope, while the proxy's own response headers are kept (use `transparent` to get the upstream's headers). `payload: true` only makes the proxy collect the upstream payload text into the envelope (the envelope is still returned; the payload is part of the `payload` field of each response).
- `stream: true` (traffic config or endpoint) streams the upstream response body to the downstream client as it arrives.
- The status code of the proxy response is the one of the upstream response, or a mapped one if a [proxy response](#proxy-response-json-schema) range matches. If there are several upstream responses (several endpoints, targets, `requestCount`, `concurrent`) the status is the one of the last response that was collected, which varies from run to run when the upstreams answer differently. When an upstream could not be called at all (connection refused, timeout) its `statusCode` is `0`, the envelope shows the error text in `status`, and the proxy answers `200` unless a proxy response maps range `[0, 0]` to another status.
- The status of every upstream call is reported in the `Goto-Proxy-Upstream-Status` response header, e.g. `{"target1":{"ep1":200}}`, and the `Via-Goto` headers of `goto` upstreams are added to the response's `Via-Goto` header.


- The proxy tells about the time it took and the delay it added in headers of the response: `Goto-Proxy-Took` is the time from the first byte of the request to the last byte of the response written by the proxy, and is sent as a trailer (it's only known after the response is written), so clients must read the body to the end to get it, and it's not sent when the response has a `Content-Length`; `Goto-Proxy-Delay` is the artificial delay of the traffic config (`delay`), once per endpoint that has one, as a duration. `Goto-Proxy-In-At`, `Goto-Proxy-Out-At` and `Goto-Proxy-Head-Took` are when the request was received, when the response head was sent, and the time between them, so they are about the head of the response and not all of it. (A CONNECT port has headers of its own, `Goto-Connect-Proxy-*`: see the [CONNECT proxy](../tcp/README.md#http-connect-proxy).)

## Chaos
In addition to request routing, the `goto` proxy also offers some chaos features:
- A `delay` can be configured in the `trafficConfig` of a target or trigger, and it is applied before each upstream request (see the note under Traffic Config on how the range is drawn).
    > Note that this delay feature is separate from the one offered by `Goto` as a Server, via [URIs](../../server/request/README.md#request-uri-tracking) and [Response Delay](../../server/response/delay/README.md#response-delay). The URI delay feature in particular allows for more fine-grained delay configuration that can be used in conjunction with the proxy feature to apply delays to URIs that eventually get routed via proxy.
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
| PUT, POST | /proxy/http/targets/`{target}`/headers/add/`{header}={value}` | Set header/value to be added for calls made to the given upstream target. |
| PUT, POST | /proxy/http/targets/`{target}`/`{trigger}`/headers/add/`{header}={value}` | Set header/value to be added for calls made to the given upstream target when invoked through the given trigger match. |
| PUT, POST | /proxy/http/targets/`{target}`/headers/remove/`{header}` | Set header to be removed for calls made to the given upstream target. If same header is configured to be added and removed, removal occurs before adding the header back. |
| PUT, POST | /proxy/http/targets/`{target}`/`{trigger}`/headers/remove/`{header}` | Set header to be removed for calls made to the given upstream target when invoked through the given trigger match. If same header is configured to be added and removed, removal occurs before adding the header back. |
| PUT, POST | /proxy/http/targets/`{target}`/headers/clear/`{header}` | Remove the given header from the set of headers configured to be added to the upstream call. Clear operation is different than Remove: clear should be used to remove a header previously configured for addition to the call, whereas remove adds a header to the remove list for removal from calls at runtime. |
| PUT, POST | /proxy/http/targets/`{target}`/headers/clear | Remove all headers from the set of headers configured to be added to the upstream call. |
| PUT, POST | /proxy/http/targets/`{target}`/`{trigger}`/headers/clear/`{header}` | Remove the given header from the set of headers configured to be added to the upstream call for the given trigger. |
| PUT, POST | /proxy/http/targets/`{target}`/`{trigger}`/headers/clear | Remove all headers from the set of headers configured to be added to the upstream call for the given trigger. |
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

#### HTTP Proxy API Details
All APIs below answer `text/plain` unless noted, and apply to the port that the request arrives on, or to the port in a `/port={port}/` prefix. The prefix needs the port to be a listener of this `goto` already; for an unknown port the request is answered `200` with `Port [{port}] not configured` and nothing is changed. A proxy is created empty for a port the first time it is configured or read.

|API|Success|Errors|
|---|---|---|
| `POST /proxy/http/targets/add` | `200`, body: the stored target as YAML (defaults filled in: `requestCount: 1`, `concurrent: 1`, `callCount: 0`) | `400` `Failed to parse proxy target with error: target name missing` / `target endpoints missing` / `target endpoint [ep] missing url` / `At least one trigger is required` / `target trigger [t] must specify matchAny` / `target trigger [t] must specify one endpoint`; `400` `Failed to process target with error: Target [t] Trigger [x] refers to Endpoint [e] but endpoint not defined under target` (or an invalid URI pattern/spec). The body is JSON or YAML. A target with the same name replaces the previous one. |
| `POST /proxy/http/targets/{target}/enable`, `.../disable` | `200` `Proxy [8080] target [t1] enabled` / `disabled` | `200` `Proxy [8080] target [t1] doesn't exist` |
| `POST /proxy/http/targets/{target}/remove` | `200` `Proxy [8080] target [t1] removed` | `200` `Proxy [8080] target [t1] doesn't exist` |
| `POST /proxy/http/targets/clear` | `200` `Proxy [8080] targets cleared` | |
| `POST /proxy/http/enable`, `.../disable` | `200` `Proxy [8080] enabled` / `disabled`. A disabled proxy does not match any target, so the port serves requests locally. | |
| `POST /proxy/http/targets/{target}/headers/add/{header}={value}` and `.../{trigger}/headers/add/...` | `200` `Header [x-chaos] Value [1] will be added to the upstream call for Target [t1] Trigger []` | `200` `Failed to configure headers. Target [nope] or Trigger [] doesn't exist.` |
| `.../headers/remove/{header}` | `200` `Header [user-agent] will be removed from the upstream call for Target [t1] Trigger [trig1]` | same `Failed to configure headers...` text, with status 200 |
| `.../headers/clear[/{header}]` | `200` `All headers cleared for upstream call to Target [t1] Trigger []` or `Header [h] cleared for upstream call to Target [t1] Trigger [x]` | same `Failed to configure headers...` text, with status 200 |
| `POST /proxy/http/responses/set` | `200` `Proxy [8080] will use responses [[{"upResponseRange":[500,599],"proxyResponse":502}]]` | `400` `Proxy [8080] failed to parse responses: ...` |
| `GET /proxy/http/responses` | `200` JSON array of proxy responses (`[]` when none) | |
| `GET /proxy/http`, `/proxy/http/all` | `200` JSON `{"port":N,"http":{port, targets, enabled, proxyResponses, tracker}}`; `/all` returns an object keyed by port. | |
| `GET /proxy/http/targets`, `/proxy/http/targets/all` | `200` JSON `{"port":N,"http":{name: target}}`; `/all` returns an object keyed by port | |
| `GET /proxy/http/targets/{target}/tracker` | `200` JSON `{"port":N,"total":{...counts},"{target}":{...counts}}` | `400` `Invalid target: nope` |
| `GET /proxy/http/trackers`, `/proxy/http/trackers/all` | `200` JSON `{"port":N,"enabled":true,"http":{tracker}}`; `/all` returns an object keyed by port with `enabled` and `http` | |
| `POST /proxy/trackers/clear`, `/proxy/trackers/all/clear` | `200` `Proxy[8080]: HTTP Proxy Tracking Info Cleared` | |

Header APIs: `{trigger}` is optional. Without it the header is changed in the transform of the target and in all its triggers; with it, only in that trigger's transform (created if the trigger has none). The `{header}={value}` value can contain `{var}` placeholders of captured variables (URL-encode `{}`). Query strings containing the word `all` change what the `GET`/`clear` APIs above report (`/all` is detected by a substring of the URI).

Add a minimal target (all requests starting with `/foo` go to one upstream; the upstream URL's host replaces the proxy's, and the URI, headers and queries are kept):
```
curl -X POST localhost:8080/proxy/http/targets/add --data '{
  "name": "t1", "enabled": true,
  "endpoints": {"ep1": {"url": "http://localhost:8081"}},
  "triggers": {"trig1": {"matchAny": [{"uriPrefix": "/foo"}], "endpoints": ["ep1"]}}
}'

curl -v localhost:8080/foo/bar
```

A richer target (URI variable, header match with a validated header variable, URI rewrite, header/query transforms, request id, delay, retries, two endpoints for fan-out, and a status mapping for the proxy):
```
curl -X POST localhost:8080/proxy/http/responses/set --data '[{"upResponseRange":[500,599],"proxyResponse":502},{"upResponseRange":[0,0],"proxyResponse":504}]'

curl -X POST localhost:8080/proxy/http/targets/add --data '{
  "name": "orders", "enabled": true,
  "endpoints": {
    "primary": {"url": "http://localhost:8081", "method": "GET", "protocol": "HTTP/1.1", "requestCount": 1, "concurrent": 1},
    "mirror":  {"url": "http://localhost:8082"}
  },
  "triggers": {
    "v2": {
      "matchAny": [{"uriPrefix": "/api/{version}/orders", "headers": {"x-tenant": "{tenant}"},
                    "vars": {"tenant": {"regex": "^t-[a-z]+$"}}}],
      "endpoints": ["primary", "mirror"],
      "transform": {
        "uriMap": {"/api/{version}/orders": "/internal/orders"},
        "headers": {"add": {"x-version": "{version}", "x-tenant-id": "{tenant}"}, "remove": ["x-debug"]},
        "queries": {"add": {"via": "goto"}, "remove": ["debug"]},
        "requestId": {"send": true, "uuid": true, "header": "x-request-id"}
      },
      "trafficConfig": {"delay": {"min": "100ms", "max": "300ms"}, "retries": 2, "retryDelay": {"min": "50ms", "max": "100ms"}, "retryOn": [502, 503]}
    }
  }
}'

curl -s -H 'x-tenant: t-acme' localhost:8080/api/v2/orders/42 | jq
```
The call above fans out to `primary` and `mirror` and returns the envelope keyed by target then endpoint (`{"orders":{"primary":[{status, statusCode, headers, payloadSize, payload, ...}],"mirror":[...]}}`); `Goto-Proxy-Upstream-Status` has the status of each. Add `"trafficConfig":{"transparent":true}` to return just the first upstream response instead.

Current limitations of the matching (verified on the binary): a `vars` constraint (`exact`, `in`, `regex`, `inRegex`) on a variable that is part of the URI path (`/api/{version}/orders`) is never satisfied, so a trigger that needs it never fires; leave URI variables unconstrained and validate header variables instead, as above. A variable captured from a header can be validated, but its value is not available to `{var}` placeholders of the transforms (the placeholder is sent as it is); only URI variables can be referenced there (`"x-version": "{version}"` above is filled, `"x-tenant-id": "{tenant}"` is not). Names of URI variables must be in lower case (`{tenantid}`, not `{tenantId}`): the URI of a trigger is lower-cased when it is registered, so a variable with capital letters is never filled in.

Two more things to know when defining triggers. A trigger that matches everything (`uriPrefix: /`) also matches the admin API calls to the port (including those sent with the `/port={port}/` prefix), so such calls are proxied instead of served and the target can no longer be disabled, removed or cleared through the APIs (restart `goto` to recover); use specific prefixes on a port that you still want to configure. And when several triggers of one target match a request by prefix, the one that is used is chosen at random; an exact `uri` trigger always wins over prefix triggers.

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
| name | `string` | Name for this target (required in the JSON/YAML of the add API; in the startup config it is the key) |
| port | `int` | Optional. Port whose proxy gets the target; defaults to the port of the request (or the `/port=` prefix) |
| enabled | `bool` | Whether or not the proxy target is currently active. **Defaults to `false`**: a target that does not set it to `true` is added disabled |
| callCount | `int` | (read-only) Number of times the target was invoked |
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
| alpn | `[]string` | A list of ALPN protocols to present to the target for negotiation |
| clientCert | `string` | A pre-uploaded cert/key pair to use as client cert |
| requestCount | `int` | Number of requests to send per invocation (default 1) |
| concurrent | `int` | Number of concurrent replicas for the upstream request (default 1). The total number of upstream requests per downstream request is `requestCount` x `concurrent` |
| payload | `bool` | Collect the upstream response payload into the response envelope |
| jsonPayload | `bool` | Return this endpoint's response payload as JSON instead of the envelope |
| yamlPayload | `bool` | Return this endpoint's response payload as YAML instead of the envelope |
| transparent | `bool` | Return this endpoint's response as it is (see traffic config) |
| stream | `bool` | Whether proxy should stream the results back |
| callCount | `int` | (read-only) Number of times the endpoint has been called |


#### HTTP Proxy Target Trigger JSON Schema

|Field|Data Type|Description|
|---|---|---|
| matchAny | `[]TargetMatch` | Array of match criteria. A trigger matches if any one of these criteria matches the incoming request. At least one match entry is required. See `HTTP Proxy Target Match JSON Schema` |
| endpoints | `[]string` | Array of endpoint names (referencing keys in the target's `endpoints` map) to invoke when this trigger matches (all of them, concurrently). At least one endpoint is required |
| callCount | `int` | (read-only) Number of times the trigger fired |
| transform | `TrafficTransform` | Optional transform configuration specific to this trigger, overrides target-level transform. See `HTTP Proxy Target Transform JSON Schema` |
| trafficConfig | `TrafficConfig` | Optional traffic configuration specific to this trigger, overrides target-level traffic config. See `HTTP Proxy Traffic Config JSON Schema` |


#### HTTP Proxy Target Match JSON Schema

|Field|Data Type|Description|
|---|---|---|
| uri | `string` | Exact URI match against the incoming request path (the query is not part of it). Supports embedded path variables using `{varName}` syntax. If both `uri` and `uriPrefix` are given, `uri` wins |
| uriPrefix | `string` | URI prefix pattern to match against the incoming request URI. Supports embedded path variables using `{varName}` syntax |
| headers | `map[string]string` | Map of header name to header value patterns to match. Values can be literal strings or `{varName}` fillers to capture header values; an empty value matches on the presence of the header |
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
| uriMap | `map[string]string` | Map of downstream URI to upstream URI. The lookup key is, in this order: the full request URI (with query), the `uri`/`uriPrefix` pattern that matched (e.g. `/api/v1/users`), then that pattern followed by `*` (e.g. `/api/v1/users*`; not `/api/v1/users/*`). The mapped URI **replaces the whole request path** (a longer request path is not appended). Ignored if `stripURI` is set. |
| headers | `Keys` | Header transformation rules for the upstream request. See `Keys JSON Schema` |
| queries | `Keys` | Query parameter transformation rules for the upstream request. See `Keys JSON Schema` |
| payload | `string` | Replaces the request body sent upstream. `{var}` placeholders are filled from the captured variables; `Content-Length` is recomputed |
| stripURI | `string` | Regular expression pattern for stripping a portion of the URI. It is compiled as `^(.*)(<stripURI>)(/.+).*$` and the request path becomes `$1$3`: the matched segment is removed and the remainder is forwarded. Needs something after the stripped segment (`/gateway` strips from `/gateway/api/x`, not from `/gateway`) |
| requestId | `RequestId` | Configuration for sending a request ID with the upstream request. See `Request ID JSON Schema` |

#### Keys JSON Schema

|Field|Data Type|Description|
|---|---|---|
| add | `map[string]string` | Map of key-value pairs to add to the upstream request. Values can contain `{var}` placeholders filled with captured URI/header variables |
| remove | `[]string` | Array of keys to remove from the upstream request. Removal happens before the additions |
| map | `map[string]string` | Map of existing key to new key: the value of the key is sent under the new name |
| lower | `bool` | (headers only) Send the header names in lower case |

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
| payload | `bool` | Collect the upstream response payload into the response envelope (applied to all the endpoints that the target/trigger uses) |
| jsonPayload | `bool` | Return the first upstream payload, parsed as JSON, instead of the envelope |
| yamlPayload | `bool` | Return the first upstream payload, parsed as YAML, instead of the envelope |
| transparent | `bool` | Return the headers and payload of the first upstream response as they are (with the proxy's response headers added), instead of the envelope |
| stream | `bool` | Stream the upstream response to the downstream client as it arrives |

#### Delay JSON Schema

|Field|Data Type|Description|
|---|---|---|
| min | `duration` | Minimum delay duration (e.g. `100ms`, `1s`, `2s500ms`) |
| max | `duration` | Maximum delay duration. When both `min` and `max` are specified, the actual delay is a random value between them |
| count | `int` | Number of calls to apply this delay to (not applied by the HTTP proxy `trafficConfig`, which draws one value when the target is added; used by the TCP, UDP, gRPC and CONNECT proxies) |

#### HTTP Proxy Tracker JSON Schema

|Field|Data Type|Description|
|---|---|---|
| downstreamRequestCount | `int` | Number of downstream requests received |
| upstreamRequestCount | `int` | Number of upstream requests sent |
| requestDropCount | `int` | Number of requests dropped |
| responseDropCount | `int` | Number of responses dropped |
| downstreamRequestCountsByURI | `map[string]int` | Number of downstream requests received, grouped by URIs |
| upstreamRequestCountsByURI | `map[string]int` | Number of upstream requests sent, grouped by URIs |
| upstreamRequestCountsByEndpoint | `map[string]int` | Number of upstream requests sent, grouped by target and endpoint |
| upstreamRequestCountByStatus | `map[string]int` | Number of upstream responses, grouped by status code |
| upstreamRequestCountsByURIStatus | `map[string]map[string]int` | Number of upstream responses, grouped by URI and status code |
| upstreamRequestCountsByEndpointStatus | `map[string]map[string]int` | Number of upstream responses, grouped by endpoint and status code |
| requestDropCountsByURI | `map[string]int` | Number of requests dropped, grouped by URIs |
| responseDropCountsByURI | `map[string]int` | Number of responses dropped, grouped by URIs |
| uriMatchCounts | `map[string]int` | Number of downstream requests that were forwarded due to URI match, grouped by matching URIs |
| headerMatchCounts | `map[string]int` | Number of downstream requests that were forwarded due to header match, grouped by matching headers |
| headerValueMatchCounts | `map[string]map[string]int` | Number of downstream requests that were forwarded due to header+value match, grouped by matching headers and values |
| queryMatchCounts | `map[string]int` | Number of downstream requests that were forwarded due to query param match, grouped by matching query params |
| queryValueMatchCounts | `map[string]map[string]int` | Number of downstream requests that were forwarded due to query param+value match, grouped by matching query params and values |
| targetTrackers | `map[string]HTTPTargetTracker` | Tracking info per target (only on the proxy-level tracker). Each `HTTP Target Tracker JSON Schema` has same fields as above. |

#### HTTP Target Tracker JSON Schema
Same fields as `HTTP Proxy Tracker JSON Schema` above



### HTTP Proxy Startup Config Example
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        stream:
          enabled: true
          endpoints:
            ep1:
              url: http://localhost:7070
              stream: true
          triggers:
            t1:
              matchAny:
                - uriPrefix: /stream
              endpoints:
                - ep1
        agent:
          enabled: false
          endpoints:
            ep1:
              url: http://localhost:8080
              method: POST
          triggers:
            t1:
              matchAny:
                - uriPrefix: /callagent
              endpoints:
                - ep1
              transform:
                uriMap:
                  /callagent: /a2a/client/call/result
                headers:
                  add:
                    foo: "123"
                payload: |
                  name: goto-agent
                  agentURL: http://localhost:3000/agent/goto-agent
                  cardURL: http://localhost:3000/agent/goto-agent
                  message: echo
                  headers:
                    request:
                      forward:
                        - foo
                    response:
                      add:
                        y: x
                  delay: 1s                
        a:
          enabled: false
          endpoints:
            ep1:
              url: http://localhost:7071
            ep2:
              url: http://localhost:7072
          transform:
            uriMap:
              /abcd: /api/abcd
              /abcx: /api/abcx
              /abc: /api/foo
              /ab*: /api/foobar
          triggers:
            t1:
              matchAny:
                - uriPrefix: /api/entry
                - uriPrefix: /api/consprov
                - uriPrefix: /api/with                
              endpoints:
                - ep1
              transform:
                uriMap:
                  /api/with-no-valid-group: /api/foo
                  /api/entry: /api/consprov
                  /api/consprov: /api/bar
                  /api/with-different-outbound-token-header-name: /api/consprov
            t2:
              matchAny:
                - uriPrefix: /ab
              endpoints:
                - ep1
            t3:
              matchAny:
                - uri: /abc
              endpoints:
                - ep2
```



## HTTP Proxy Examples
See [HTTP Proxy Examples][proxy-examples]


[proxy-examples]: ProxyExamples.md