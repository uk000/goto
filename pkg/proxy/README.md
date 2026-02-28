# <a name="proxy"></a>
# Proxy


`Goto` can act as an HTTP, TCP, UDP or MCP proxy that sits between a downstream client and an upstream service, allowing you a point in the network where the communication between the downstream client and upstream service can be observed and affected.
The HTTP Proxy implementation is lot more comprehensive than other proxies in that it allows for = manipulation of requests and responses, and can perform multiplexing to multiple upstreams.
The TCP and UDP Proxy feature only supports forwarding to a single upstream, with some basic tracking.
The MCP proxy feature supports proxying all MCP traffic on a port, or forwarding requests for a specific MCP tool call.

### TCP Proxy
- Any TCP listener can act as a TCP proxy if a proxy target is defined for that listener.
- A TCP proxy only supports routing to a single upstream target, except for SNI based routing for TLS connections. This is unlike an HTTP proxy that can route a single request to multiple upstream targets.
- TCP proxy is a passthrough proxy as it transmits bytes opaquely between the downstream and upstream parties. The only time the TCP proxy inspects the bytes is if you configure it to perform SNI based routing.
- You can optionally add SNI match criteria to a TCP proxy target (see the relevant APIs further below in the doc). In this case, `goto` will inspect the client TLS handshake packets to read SNI information and match it against the defined server names. The connection will be routed to the first target for which the defined server name matches client's requested SNI.
  <details>
  <summary>More details about SNI matching</summary>
  <ul style='font-size:0.95em'>
    <li>
    At the time of a new downstream connection, `goto` checks whether TCP proxy targets on this port have been configured with SNI match. If so, `goto` reads SNI from the TLS client handshake data without actually doing the handshake, and uses the SNI DNS hostname to pick an upstream endpoint to route to. The client TLS handshake data is forwarded to the upstream endpoint so the actual handshake still happens between the client and the upstream service.
    </li>
    <li>
    While inspecting the client's TLS handshake, `goto` also logs the Cipher Suites and Signature Algorithms requested by the client. However, these can only be seen in the `goto` logs for now, not exposed via any API.
    </li>
  </ul>
  </details>

### HTTP Proxy
- Any HTTP(S) listener that you open in `goto` is ready to act as an http proxy in addition to the other server duties it performs.
- To use an HTTP(S) listener as a proxy, all you need to do is add one or more upstream targets. The request processing path in `goto` checks for the presence of proxy targets for the listener where a request arrives. If any proxy target is defined on the listener, `goto` matches the request with those targets and forwards it to the matched targets. If no match found, the listener processes the request as a server.
- As an HTTP proxy, `goto` can perform various kind of HTTP filtering and transformations before forwarding a downstream request to the upstream service.
  - URI rewrite
  - Add/remove headers.
  - Add/remove query params.
  - Convert between HTTP and HTTPS protocols.
  - Route a single request to multiple upstream targets and send the combined responses back to the downstream client.
- Note that if you use an HTTPS listener as a proxy, `goto` will perform TLS termination and will redo TLS for upstream HTTPS endpoints. If you want a passthrough proxy, you can use a TCP listener in goto to act as a TCP proxy for HTTP traffic.


### Upstream targets
Upstream targets can be defined in one of the two ways:
- By posting a JSON spec to `/proxy/targets/add` API.
- Build the upstream endpoint incrementally via multiple API calls. Benefit of this approach is that targets can be defined via just API calls without the need for a JSON payload, but it does require multiple API calls to define each target.
    <details>
      <summary>See the steps needed to define a proxy target using APIs</summary>
      
    - First add a target endpoint using API `/proxy/targets/add/{target}?url={url}`
    - Then define one or more URI routing for this endpoint using API `/proxy/targets/{target}/route?from={uri}&to={uri}`. A target should have at least one URI route defined for it to be triggered.
    - Optionally define any necessary header and query match criteria via APIs `/proxy/targets/{target}/match/[header|query]/...`
    - Optionally define any necessary header and query transformations via APIs `/proxy/targets/{target}/headers/[add|remove]/...`, and `/proxy/targets/{target}/query/[add|remove]/...`.
      
    </details>

### Proxy Target Matching
- HTTP proxy targets require URI based matching at the minimum, and additional match criteria can be defined based on HTTP headers and query params. A target can be defined to accept all requests by setting URI match to `/`.
    - URI route match is a pre-requisite for routing a call to upstream endpoints. It serves as a match criteria of its own, in `addition` to the `MatchAny` or `MatchAll` criteria of the target. 
    - So if there's a `MatchAny` criteria defined for the target as `Header=Foo` and a route defined as `/foo -> /bar`, then a request will get routed to the upstream only if it has the URI `/foo` AND the header `Foo:<any value>`.
    - The match criteria added via APIs get added with `MatchAny` semantics. If you need to use `MatchAll` semantics for a target's match criteria, you must define the target using JSON schema.
- TCP targets support SNI based matching if the communication is over TLS. Otherwise TCP proxy only supports a single upstream service.
    <details>
    <summary> &#x1F525; More detailed explanation of HTTP target matching &#x1F525; </summary>

    Proxy target match criteria are based on request URI match and optional headers and query parameters matching.
    An upstream target is defined with a URI routing table, and the upstream target gets triggered for all requests matching any of the URIs defined in the routing table. However, if you need additional filtering of requests before sending those over to the upstream endpoints, you can use the headers and query params match criteria. These criteria can be defined to either match ANY of them or ALL of them for the request to qualify.

    - URI Routing Table: this is defined as the mandatory `routes` field in the target definition. The source URI in the routing table can be specified using variables, e.g. `{foo}`, to indicate the variable portion of a URI. For example, `/foo/{f}/bar/{b}` will match URIs like `/foo/123/bar/abc`, `/foo/something/bar/otherthing`, etc. The variables are captured under the given labels (`f` and `b` in the previous example). The destination URI in the routing table can refer to those captured variables using the syntax described in this example:
      
      ```
      curl http://goto:8080/proxy/targets/add --data \
      '{"name": "target1", "endpoint":"http://somewhere", \
      "routes":{"/foo/{x}/bar/{y}": "/abc/{y:.*}/def/{x:.*}"}, \
      "enabled":true, "sendID": true}'
      ```

      This target will be triggered for requests with the pattern `/foo/<somex>/bar/<somey>` and the request will be forwarded to the target as `http://somewhere/abc/somey/def/somex`, where the values `somex` and `somey` are extracted from the original request and injected into the replacement URI.

      URI match `/` has the special behavior of matching all traffic.

    <br/>

    - Headers: specified in the `matchAll` or `matchAny` field as a list of key-value pairs, with the ability to capture values in named variables and reference those variables in the `addHeaders` list. A target is triggered if any of the headers in the match list are present in the request (headers are matched using OR instead of AND). The variable to capture header value is specified as `{foo}` and can be referenced in the `addHeaders` list again as `{foo}`. This example will make it clear:

      ```
      curl http://goto:8080/proxy/targets/add --data \
      '{"name": "target2", "endpoint":"http://somewhere", "routes":{"/": ""}, \
      "matchAll":{"headers":[["foo", "{x}"], ["bar", "{y}"]]}, \
      "addHeaders":[["abc","{x}"], ["def","{y}"]], "removeHeaders":["foo"], \
      "enabled":true, "sendID": true}'
      ```

      This target will be triggered for requests carrying headers `foo` or `bar`. On the proxied request, additional headers will be set: `abc` with value copied from `foo`, and `def` with value copied from `bar`. Also, header `foo` will be removed from the proxied request.

      So a downstream request `curl -v localhost:8080/bla/bla -H'foo:123' -H'bar:456'` gets sent to the upstream endpoint with the same URI (passthrough) but headers `'bar:456'`, `'abc:123'` and `'def:456'`.
    <br/>

    - Query: specified as a list of key-value pairs, with the ability to capture values in named variables and reference those variables in the `addQuery` list. A target is triggered if any of the query parameters in the match list are present in the request (matched using OR instead of AND). The variable to capture query parameter value is specified as `{foo}` and can be referenced in the `addQuery` list again as `{foo}`. Example:

      ```
      curl http://goto:8080/proxy/targets/add --data \
      '{"name": "target3", "endpoint":"http://somewhere", "routes":{"/": ""},\
      "matchAny":{"query":[["foo", "{x}"], ["bar", "{y}"]]}, \
      "addQuery":[["abc","{x}"], ["def","{y}"]], "removeQuery":["foo"], \
      "enabled":true, "sendID": true}'
      ```

      This target will be triggered for requests that carry either of the query params `foo` or `bar`. On the proxied request, query param `foo` will be removed, and additional query params will be set: `abc` with value copied from `foo`, and `def` with value copied from `bar`. The incoming request `http://goto:8080?foo=123&bar=456` gets proxied as `http://somewhere?abc=123&def=456&bar=456`.

    </details>

### Request Transformation
- Downstream request headers, query params, and payload gets passed to the upstream requests. Transformations can be defined per upstream endpoint, allowing for URI, headers and query params to be added/removed/replaced. This allows for the upstream requests to differ from the downstream request.

### Response
- If the request only matches a single upstream endpoint, the upstream response payload is sent as downstream response payload.
- If the request matches multiple upstream endpoints, the downstream response payload will be a wrapper containing a collection of all those upstream responses.
- Downstream response headers include all the upstream response headers combined with the `goto` headers from the proxy instance. As a result, downstream responses may contain duplicate headers or multiple values for the same header.

### Chaos
In addition to request routing, the `goto` proxy also offers some chaos features:
- A `delay` can be configured per target that's applied to all communication in either direction for that target. For HTTP proxy, the delay is applied before sending the request and response. For TCP proxy, the delay is applied to each packet write.
    > Note that this delay feature is separate from the one offered by `Goto` as a Server, via [URIs](#-uris) and [Response Delay](#-response-delay). The URI delay feature in particular allows for more fine-grained delay configuration that can be used in conjunction with the proxy feature to apply delays to URIs that eventually get routed via proxy.
- A `drop` percentage can be configured per target that specifies what percentage of writes should be skipped in either direction for that target. If configured, `goto` will skip every `100/{pct}` write to/from this target. For TCP targets, the drop pct gets applied across packets in both directions. For HTTP targets, when it's time to drop a write, it'll choose to drop either of the request or the response based on a random coin flip.
  > While technically this is not a true network TCP packet drop, it does allow for some interesting chaos testing where data is randomly lost between the two parties.
  > If you want to drop some TCP writes for HTTP traffic, you can configure the port in `goto` as a TCP port and use the TCP features described below while the client and upstream still communicate over HTTP.
  > For TLS traffic, the initial TLS handshake is also subject to the drop, which can create more chaos by randomly failing TLS handshake.
- The HTTP proxy allows you to add/remove headers and query params, which can be a good way to create unexpected circumstances for the client and service. For example, configure the proxy to remove a required header, or change some header's value, and observe whether the two parties deal with the situation gracefully.
- Additional chaos can be applied via listener API, by closing/reopening a connection.


## TCP Proxy

### TCP Proxy Operations
- **POST** `/proxy/tcp/{port}/{endpoint}?sni={sni}`
- **POST** `/proxy/tcp/{port}/{endpoint}/retries/{retries}?sni={sni}`

## UDP Proxy

### UDP Proxy Operations
- **POST** `/proxy/udp/{port}/{endpoint}`
- **POST** `/proxy/udp/{port}/{endpoint}/delay/{delay}`
- **POST** `/proxy/udp/{port}/delay/{delay}`


## Proxy Reports

### Get Reports
- **GET** `/proxy/report/grpc`


#### Common Proxy Admin APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST | /proxy/enable  | Enable proxy feature on the port |
| POST | /proxy/disable | Disable proxy feature on the port |
| GET | /proxy/report | Get a report of the activity so far for all targets (HTTP + TCP) |
| GET | /proxy/all/report | Get a combined report of all proxies (all ports) |
| POST | /proxy/report/clear | Clear all the accumulated report counts/info |
| POST | /proxy/all/report/clear | Clear reports of all proxies (all ports)  |


#### Common Proxy Targets Admin APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/proxy/targets/clear            | Remove all proxy targets |
| PUT, POST |	/proxy/targets/add  | Add target for proxying requests [see `Proxy Target JSON Schema`](#proxy-target-json-schema) |
| POST | /proxy/targets<br/>/`{target}`/remove  | Remove a proxy target |
| POST | /proxy/targets<br/>/`{target}`/enable  | Enable a proxy target |
| POST | /proxy/targets<br/>/`{target}`/disable | Disable a proxy target |
| PUT, POST | /proxy/targets<br/>/`{target}`/delay=`{delay}` | Configure a delay to be applied to the requests/responses or tcp packets going to/from the given target |
| POST | /proxy/targets<br/>/`{target}`/delay/clear | Clears any configured delay for the given target |
| PUT, POST | /proxy/targets<br/>/`{target}`/drop=`{pct}` | Configure a percentage of writes to be dropped to/from the given target. If configured, `goto` will skip every `100/{pct}` write to/from this target. For TCP, this results in dropping packets in both directions. For HTTP, it'll choose to drop either of the request or the response based on a random coin flip. |
| POST | /proxy/targets<br/>/`{target}`/drop/clear | Clears any configured drop for the given target so that the traffic can go back to normal |
| GET |	/proxy/targets                  | List all proxy targets |
| GET | /proxy/targets<br/>/`{target}`/report | Get a report of the activity so far for the given target |


#### TCP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST |	/proxy/tcp/targets<br/>/add/`{name}`<br/>?<br/>address=`{address}`<br/>&sni=`{sni}` | Add a new TCP upstream target with the given name and address, where the address is in the format `hostname:port`. The optional `sni` param can be a comma-separated list of host names to perform SNI based routing. The presence of `sni` param indicates that the TCP traffic for this proxy port is encrypted. |
| POST |	/proxy/tcp/{port}/{endpoint}?sni={sni}           | Setup TCP proxy on the given port, forwarding to the given endpoint. Optionally specify an SNI match for TLS traffic. |
| POST |	/proxy/tcp/{port}/{endpoint}/retries/{retries}?sni={sni}   | Setup TCP proxy on the given port, forwarding to the given endpoint, and retry failed connections as well as failed packet writes up to the given number of retries |
| GET | /proxy/report/tcp | Get a report of the activity so far for all TCP targets |


#### UDP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/proxy/udp/{port}/{endpoint}?sni={sni}           | Setup UDP proxy on the given port, forwarding to the given endpoint. Optionally specify an SNI match for TLS traffic. |
| POST |	/proxy/udp/{port}/{endpoint}/retries/{retries}?sni={sni}   | Setup UDP proxy on the given port, forwarding to the given endpoint, and retry failed connections as well as failed packet writes up to the given number of retries |

#### MCP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/mcpapi/proxy?endpoint={endpoint}&sni={sni}&headers={headers} | Setup MCP proxy on the request's port, forwarding to the given upstream endpoint. Optionally specify an SNI match for TLS traffic, and optional headers to be added to the upstream request. |
| POST |	/mcpapi/proxy/{tool}?to={tool}&endpoint={endpoint}&sni={sni}&headers={headers}   | Setup MCP proxy on the MCP server for the given MCP tool, forwarding to the given tool at the given upstream. Optionally specify an SNI match for TLS traffic, and optional headers to be added to the upstream request. |

#### GRPC Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| GET |	/grpc/proxy/status | Get GRPC Proxy Details. |
| POST |	/grpc/proxy/clear   | Clear GRPC Proxies. |
| POST | /grpc/proxy/{service}/{upstream}/tee/{teeport} | Setup GRPC Proxy for the given service to the given upstream endpoint (expecting same service to be available at the upstream endpoint).  |
| POST | /grpc/proxy/{service}/{upstream}/delay/{delay} | Same as above, with an additional delay to be added to all requests/responses |
| POST | /grpc/proxy/{service}/{upstream}/tee/{teeport} | Same as abuve, but also captures a copy of the requests/responses to be replayed for any service that connects to the `teeport` port  |
| POST | /grpc/proxy/{service}/{upstream}/{targetService} | Setup GRPC Proxy for the given service to the given upstream endpoint, to a different service as identified by `targetService`. The `targetService` should accept the same input/output payload spec. |
| POST | /grpc/proxy/{service}/{upstream}/{targetService}/delay/{delay} | Same as above, with an additional delay to be added to all requests/responses |
| POST | /grpc/proxy/{service}/{upstream}/{targetService}/tee/{teeport} | Same as above, but also captures a copy of the requests/responses to be replayed for any service that connects to the `teeport` port  |


#### HTTP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST |	/proxy/http/targets<br/>/add/`{name}`?<br/>url=`{url}`<br/>&proto=`{proto}`<br/>&from=`{uri}`&to=`{uri}` | Add a new target with the given name and URL. Optional param `proto` can be used to assign a protocol (`HTTP/1.1, HTTP/2`), defaults to HTTP/1.1. Params `from` and `to` are used to define a URI mapping for this target. Additional URI routing can be defined using the `/{target}/route` API given below. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/route?<br/>from=`{uri}`&to=`{uri}` | Add URI routing for the given target from the given downstream URI (`from`) to the given upstream URI (`to`). |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/match/header<br/>/`{key}`[=`{value}`] | Define a header match criteria to match just the header name, or both name and value. <small>Note: Header match criteria added via this API are treated as `MatchAny`. To use `MatchAll` semantics, use JSON payload to define the target.</small> |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/match/query<br/>/`{key}`[=`{value}`] | Define a query param match criteria to match just the param name, or both name and value. <small>Note: Query match criteria added via this API are treated as `MatchAny`. To use `MatchAll` semantics, use JSON payload to define the target.</small> |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/headers<br/>/add/`{key}`=`{value}` | Define a header key/value that should be added to the upstream request. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/headers<br/>/remove/`{key}` | Define a downstream header that should be removed from the upstream request. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/query/add<br/>/`{key}`=`{value}` | Define a query param key/value that should be added to the upstream request. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/query<br/>/remove/`{key}` | Define a downstream query param that should be removed from the upstream request. |
| GET | /proxy/report/http | Get a report of the activity so far for all HTTP targets |

###### <small> [Back to TOC](#goto-proxy) </small>

<br/>
<details>
<summary>HTTP Proxy Target JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| name        | string             | Name for this target |
| protocol    | string             | "tcp", "http", "http/2" (can also use "h2", "h2c", "http/2.0") |
| endpoint    | string             | Upstream address for the target. |
| routes        | map[string]string  | URI mapping from downstream source request URI to upstream request URI. Downstream request's URI is matched against this routing table and if a match is found, the corresponding destination URI is used. If the destination URI is set to "", the source request URI is used. |
| sendID        | bool           | Whether or not a unique ID be sent with each request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| addHeaders    | `[][]string`                            | Additional headers to add to the request before proxying |
| removeHeaders | `[]string `                             | Headers to remove from the original request before proxying |
| addQuery      | `[][]string`                            | Additional query parameters to add to the request before proxying |
| removeQuery   | `[]string`                              | Query parameters to remove from the original request before proxying |
| stripURI | string (regex) | Optional regex to capture any portions of the request URI that should be stripped. If given, it's applied on the request URI and the matching pieces get removed from the URI. |
| matchAny        | JSON     | Match criteria based on which runtime traffic gets proxied to this target. See [JSON Schema](#proxy-target-match-criteria-json-schema) and [detailed explanation](#proxy-target-match-criteria) below |
| matchAll        | JSON     | Match criteria based on which runtime traffic gets proxied to this target. See [JSON Schema](#proxy-target-match-criteria-json-schema) and [detailed explanation](#proxy-target-match-criteria) below |
| replicas     | int      | Number of parallel replicated calls to be made to this target for each matched request. This allows each request to result in multiple calls to be made to a target if needed for some test scenarios |
| delayMin      | duration     | Minimum delay to be applied to the requests/responses to/from this target |
| delayMax      | duration     | Max delay to be applied to the requests/responses to/from this target |
| delayCount    | int     | Number of requests for which the delay should be applied. After the count of requests have been affected by the delay, the subsequent calls revert to normal processing. |
| dropPct      | int     | Percent of TCP writes to be dropped in the proxy. This only applies to TCP targets. If given, every Nth write (calculated based on the given percentage) will be skipped in both directions. |
| enabled       | bool     | Whether or not the proxy target is currently active |


#### HTTP Proxy Target Match Criteria JSON Schema
|Field|Data Type|Description|
|---|---|---|
| headers | `[][]string`  | Headers names and optional values to match against request headers |
| query   | `[][]string`  | Query parameters with optional values to match against request query |
| sni     | `[]string`  | List of server names to match against client TLS handshake |

#### HTTP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| downstreamRequestCount | int  | Number of downstream requests received  |
| upstreamRequestCount | int  | Number of upstream requests sent |
| requestDropCount | int  | Number of requests dropped |
| responseDropCount | int  | Number of responses dropped |
| downstreamRequestCountsByURI | map[string]int  | Number of downstream requests received, grouped by URIs |
| upstreamRequestCountsByURI | map[string]int  | Number of upstream requests sent, grouped by URIs |
| requestDropCountsByURI | map[string]int  | Number of requests dropped, grouped by URIs |
| responseDropCountsByURI | map[string]int  | Number of responses dropped, grouped by URIs |
| uriMatchCounts | map[string]int  | Number of downstream requests that were forwarded due to URI match, grouped by matching URIs |
| headerMatchCounts | map[string]int  | Number of downstream requests that were forwarded due to header match, grouped by matching headers |
| headerValueMatchCounts | map[string]map[string]int  | Number of downstream requests that were forwarded due to header+value match, grouped by matching headers and values |
| queryMatchCounts | map[string]int  | Number of downstream requests that were forwarded due to query param match, grouped by matching query params |
| queryValueMatchCounts | map[string]map[string]int  | Number of downstream requests that were forwarded due to query param+value match, grouped by matching query params and values |
| targetTrackers | map[string]HTTPTargetTracker  | Tracking info per target. Each `HTTP Target Tracker JSON Schema` has same fields as above. |


#### HTTP Target Tracker JSON Schema
Same fields as `HTTP Proxy Tracker JSON Schema` above


#### TCP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountsBySNI | map[string]int  | Number of downstream connections received for SNI match, grouped by SNI server names |
| rejectCountsBySNI | map[string]int  | Number of downstream connections rejected due to SNI mismatch, grouped by SNI server names |
| targetTrackers |  map[string]TCPTargetTracker  | Tracking details per target. See `TCP Target Tracker JSON Schema` below.  |


#### TCP Target Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountsBySNI | map[string]int  | Number of downstream connections received for SNI match, grouped by SNI server names |
| tcpSessions |  map[string]TCPSessionTracker  | Tracking details per client session. See `TCP Session Tracker JSON Schema` below.  |


#### TCP Session Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| sni | string  | SNI server name if one was used to match target for this session |
| downstream |  map[string]ConnTracker  | Downstream connection tracking details for this session. See `Connection Tracker JSON Schema` below. |
| upstream |  map[string]ConnTracker  | Upstream connection tracking details for this session. See `Connection Tracker JSON Schema` below. |

#### UDP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| packetCount | int  | Number of packets received  |
| packetCountByUpstream | map[string]int  | Number of packets sent per upstream endpoint |
| packetCountByDomain | map[string]int  | Number of packets sent per DNS domain (for proxying to DNS servers) |
| packetCountByUpstreamDomain | map[string]int  | Number of packets sent per upstream endpoint per DNS domain (for proxying to DNS servers) |

#### MCP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountByUpstream | map[string]int  | Number of connections per upstream endpoint |
| requestCountByUpstream | map[string]int  | Number of requests per upstream endpoint |
| requestCountByServer | map[string]int  | Number of requests per requested MCP server |
| requestCountByServerTool | map[string]map[string]int  | Number of requests per MCP server per Tool |
| responseCountCountByUpstream | map[string]int  | Number of responses per upstream endpoint |
| responseCountCountByServer | map[string]int  | Number of responses per requested MCP server |
| responseCountsCountByServerTool | map[string]map[string]int  | Number of responses per MCP Tool for each server |
| messageCountByType | map[string]int  | Number of messages by message type |

#### GRPC Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountByUpstream | map[string]int  | Number of connections per upstream endpoint |
| requestCountByUpstream | map[string]int  | Number of requests per upstream endpoint |
| requestCountByService | map[string]int  | Number of requests per requested Servcice |
| requestCountByServiceMethod | map[string]map[string]int  | Number of requests per RPC methdMCP server per Tool |
| responseCountCountByUpstream | map[string]int  | Number of responses per upstream endpoint |
| responseCountCountByServer | map[string]int  | Number of responses per requested MCP server |
| responseCountsCountByServerTool | map[string]map[string]int  | Number of responses per MCP Tool for each server |
| messageCountByType | map[string]int  | Number of messages by message type |


#### TCP Connection Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| startTime | string  | Connection start time |
| endTime | string  | Connection end time |
| firstByteInAt | string  | Time of receipt of the first byte of data |
| lastByteInAt | string  | Time of receipt of the last byte of data |
| firstByteOutAt | string  | Time of dispatch of the first byte of data |
| lastByteOutAt | string  | Time of dispatch of the last byte of data |
| totalBytesRead | int  | Total number of bytes read from this connection |
| totalBytesWritten | int  | Total number of bytes written to this connection |
| totalReads | int  | Total number of read operations performed |
| totalWrites | int  | Total number of write operations performed |
| delayCount | int  | Total number of write operations where a delay was applied |
| dropCount | int  | Total number of skipped write operations due to being dropped |
| closed | bool  | Whether the connection is closed |
| remoteClosed | bool  | Whether the connection was closed by remote party |
| readError | bool  | Whether the connection was closed due to a read error |
| writeError | bool  | Whether the connection was closed due a write error |


</details>

<details>
<summary>Proxy Events</summary>

- `Proxy Target Rejected`
- `Proxy Target Added`
- `Proxy Target Removed`
- `Proxy Target Enabled`
- `Proxy Target Disabled`
- `Proxy Target Invoked`

</details>

See [Proxy Example](../../docs/proxy-example.md)


## Notes
- Most endpoints support port-specific operations via query parameters or headers
- Query parameters are optional unless marked as required
- All POST/PUT endpoints accept JSON payloads where applicable
