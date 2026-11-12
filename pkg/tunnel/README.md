# Tunnel

`Tunnel` feature allows a `goto` instance to act as a L7 tunnel, receiving HTTP/HTTPS/H2 requests from clients and forwarding those to any arbitrary endpoints over the same or a different protocol. This feature can be useful in several scenarios: e.g. 
- A client wishes to reach an endpoint by IP address, but the endpoint is not accessible from the client's network space (e.g. K8S overlay network). In this case, a single `goto` instance deployed inside the overlay network (e.g. K8S cluster) but accessible to the client network space via an FQDN can receive requests from the client and transparently forward those to any overlay IP address that's visible to the `goto` instance.
- Route traffic from a client to a service through `goto` proxy in order to inspect traffic and capture details in both directions.
- Observe network behavior (latencies, packet drops, etc) between two endpoints
- Send a request from a client to two or more service endpoints and analyze results from those, while sending the response to client from whichever endpoint responds first.
- Send a request on a multi-hop journey, routing via multiple goto tunnels, in order to observe latency or other network behaviors.
- Test a client and/or service's behavior if the other party that's communicating with it changed its protocol from HTTP/1 to HTTP/2 without having to change the real applications' code.

> **Status note.** On the current code the tunnel middleware is not invoked for HTTP requests (it is on the `Unintercepted` middleware list of `pkg/server/server.go`, whose handlers never run for requests that reach the catch-all; bug INF-01 in the scan report). The `/tunnels/...` configuration and report APIs below work, but a request is not tunneled by any of the four ways until that wiring is fixed. The tunnel behavior described below, and the outputs shown, were run on a build with the wiring fixed.

There are four different ways in which requests can be tunneled via `Goto`:

#### 1. Configured Tunnels
<div style="padding-left:20px">
  Any listener can be converted into a tunnel by calling `/tunnels/add/{protocol:address:port}` API to add one or more endpoints as tunnel destinations. When a tunnel has more than one endpoint, the requests are forwarded to all the endpoints, and the earliest response gets sent to the client.

  A tunnel can be added for all the requests of the port, for the requests to a URI, for the requests that carry a header (with a given value or any value), or for both a URI and a header. See [Which requests are tunneled](#which-requests-are-tunneled).
</div>

#### 2. On-the-fly Tunneling via URI prefix
<div style="padding-left:20px">
  Any request can be tunneled via `Goto` using URI path format `http://goto.goto/tunnel={endpoints}/some/uri`. `{endpoints}` is a list of endpoints where each endpoint is specified using the format `{protocol:address:port}`. The goto instance receiving the above formatted request will multicast the request to all the given endpoints, to URI `/some/uri` along with the same HTTP parameters that the client used (Method, Headers, Body, TLS). 

  In order to multi-tunnel a request via multiple `goto` instances, multiple tunnel path prefixes can be added, e.g. `http://goto-1:8080/tunnel={goto-2:8080}/tunnel={goto-3:8081}/tunnel={real-service:80}/some/uri`. <p/>
  As you can imagine by analyzing this request, it's processed by the first goto instance (`goto-1:8080`) using the previous logic, which ends up forwarding it to instance `goto-2:8080` with the remaining URI `/tunnel={goto-3:8081}/tunnel={real-service:80}/some/uri`. The second goto instance (`goto-2:8080`) again treats the incoming request as a tunnel request, and forwards it to instance `goto-3:8081` with the remaining URI `/tunnel={real-service:80}/some/uri`. The third goto instance finally tunnels it to the endpoint `real-service:80` with URI `/some/uri`.
</div>

#### 3. On-the-fly Tunneling using special header `Goto-Tunnel`
<div style="padding-left:20px">
  Goto can be asked to tunnel a request by sending it to the `goto` instance with an additional header `Goto-Tunnel:{endpoints}`. Endpoints can be a comma-separated list where each endpoint is of format `{protocol:address:port}`. This approach allows for rerouting some existing traffic via goto, which then sends it to the original intended upstream service without having to modify the URI. The `Goto-Tunnel` header allows for multicasting as well as multi-tunneling. <br/><br/>

  In order to multicast a request to several endpoints, add the `Goto-Tunnel` header multiple times (i.e. with list of HTTP header values). For example:
  ```
  curl -vk https://goto-1:8081/foo -H'Goto-Tunnel:goto-2:8082' -H'Goto-Tunnel:goto-3:8083'
  ```

  In order to send a request through multiple `goto` tunnels, add multiple `goto` endpoint addresses as a comma-separated value to a single `Goto-Tunnel` header. For example:
  ```
  curl -vk https://goto-1:8081/foo -H'Goto-Tunnel:goto-2:8082,goto-3:8083'
  ```

  The goto instance takes the first address of the header value as the endpoint to tunnel to, and passes the rest on, in the `Goto-Tunnel` header, to that endpoint. The `Goto-Tunnel` header is not sent to the last endpoint.

  `Endpoints` (in path prefix or header) can omit the protocol, or specify the protocol from one of: `http` (HTTP/1.1), `https` (HTTP/1.1 with TLS), `h2` (HTTP/2 with TLS) or `h2c` (HTTP/2 over cleartext).

  When the endpoints in a tunnel have different protocols, `Goto` performs protocol conversions between all possible translations (`http` to/from `https` and `HTTP/1.1` to/from `HTTP/2`). The request `Host` and `SNI Authority` are rewritten to match the endpoint host.

  When an endpoint in a tunnel omits protocol in its spec, the protocol used by the original/preceding client request is carried forward.
</div>


#### 4. HTTP(S) Proxy and CONNECT protocol
<div style="padding-left:20px">
  If `Goto` receives a header that indicates that a connected client has been routed via an HTTP(S) Proxy, or if `goto` receives an HTTP CONNECT request, `goto` establishes an on-the-fly tunnel to the destination endpoint.

      Note: The proxy auto-detection support is experimental and not confirmed to work under all circumstances.

  For example, the below curl request gets routed via HTTPS proxy `goto-2.goto`. The `goto` instance running on `goto-2.goto` intercepts the request and tunnels it to the original destination `goto-1.goto`, but gives you a chance to track its details.

  ```
  curl -vk goto-1.goto:8081/foo/bar -H'foo:bar' --proxy https://goto-2.goto:8082 --proxy-cacert goto-2-cert.pem
  ```

  The CONNECT connection is kept open and piped to the port itself until either side closes it, and is listed by `/tunnels/active` while it lasts. When one side ends its half of the connection, both directions are closed, so a client that half-closes after sending its request does not receive the response (bug INF-06).
</div>

### Tunnel APIs

###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

All `Goto` APIs support tunnel prefix, allowing any `goto` API to be proxied from one instance to another. In addition, any arbitrary API can also be called using the tunnel prefix. See the tunnel feature description above for the specification of `{endpoint}` used in the tunnel APIs. To configure a tunnel on a port using another port, use `port={port}` prefix format.

|METHOD|URI|Description|
|---|---|---|
| ALL       |	/`tunnel={endpoint}`/`...` | URI prefix format to tunnel any request on the fly. |
| POST, PUT |	/tunnels/add<br/>/`{endpoint}`?`uri={uri}`| Adds a tunnel on the listener port to redirect traffic to the given `endpoint`. If the URI query param is specified, only traffic to the given URI is intercepted. The URI must be the exact path of the request, without a query string (see Notes). |
| POST, PUT |	/tunnels/add/`{endpoint}`<br/>/header/`{key}={value}`<br/>?`uri={uri}` | Adds a tunnel that acts upon requests carrying the given header key and optional value. If value is omitted, just the presence of the header key triggers the tunnel. If a URI is specified as a query param, the header match is performed only on requests to the given URI. |
| POST, PUT |	/tunnels/add<br/>/`{endpoint}`/transparent | Add a transparent tunnel that doesn't add goto request headers when forwarding a request to the upstream endpoints. However, goto response headers are still added to the response sent to the downstream client. |
| POST, PUT |	/tunnels/remove<br/>/`{endpoint}`?`uri={uri}` | Remove a configured endpoint tunnel on the listener port. If the URI query param is specified, only the tunnel for that URI is removed. `{endpoint}` has to be the tunnel ID (`http:host:port`, see below), not the address that was given to `add`. |
| POST, PUT |	/tunnels/remove/`{endpoint}`<br/>/header/`{key}={value}`<br/>?`uri={uri}` | Remove a configured endpoint tunnel on the listener port for the given header match. If the URI query param is specified, tunnels for that URI are removed. `{endpoint}` is the tunnel ID. |
| POST, PUT |	/tunnels/clear | Clears the tunnels that apply to all the requests of the listener port on which the API is called. The URI and header tunnels are not cleared (see Notes). |
| GET |	/tunnels | Get currently configured tunnels, of all ports. |
| GET |	/tunnels/active | Get currently active proxy (CONNECT) tunnels on the listener port. |
| POST, PUT |	/tunnels/track<br/>/header/`{headers}` | Track tunnel traffic on the listener port for the given headers (comma-separated). The headers are counted in the requests and in the responses. |
| POST, PUT |	/tunnels/track<br/>/query/`{params}` | Track tunnel traffic on the listener port for the given query params (comma-separated). |
| POST, PUT |	/tunnels/track/clear | Clear tunnel traffic tracking on the listener port: the tracked headers and query params, the counts and the captured traffic log. |
| GET |	/tunnels/track | Get tunnel traffic tracking report, of all ports. |
| POST, PUT |	/tunnels/traffic<br/>/capture=`{yn}` | Enable/disable capture of tunnel traffic on the listener port. |
| GET |	/tunnels/traffic | Get tunnel traffic log if traffic capturing was enabled for the listener port. |

All the configuration APIs answer with a plain text message and status 200:

|API|Messages|
|---|---|
| `add` | `Tunnel added on port [23070] to endpoint [localhost:23073] for URI [/uri1], Header [X-Tun:v1]` |
| `remove` | `Tunnel removed on port [..] to endpoint [..] for URI [..], Header [k:v]` or `No tunnel to remove on port [..] ...` (also status 200) |
| `clear` | `Tunnel removed on port [23070] to endpoint [] for URI [], Header [:]` |
| `track/header`, `track/query` | `Tracking headers [X-Req X-Other] and query params [] added for tunnels on port [23070]` |
| `track/clear` | `Tracking cleared for tunnels on port [23070]` |
| `traffic/capture=y` | `Traffic capture enabled for tunnels on port [23070]` (`n`: `... disabled ...`) |

### Endpoint format and tunnel ID
An endpoint is `[protocol:]host:port`, with `protocol` one of `http`, `https`, `h2`, `h2c`. Without a protocol, the protocol of the request is used, and the endpoint is registered with the ID `http:host:port`, or `https:host:port` when the API call that added it came over TLS. The ID is what the reports show and what the `remove` APIs match:

|Address given to `add`|Tunnel ID|Notes|
|---|---|---|
| `localhost:23073` | `http:localhost:23073` | Uses the protocol of each request (`useRequestProto: true`) |
| `https:localhost:23073` | `https:localhost:23073` | HTTP/1.1 over TLS |
| `h2:localhost:23073` | `h2:localhost:23073` | HTTP/2 over TLS |
| `h2c:localhost:23073` | `h2c:localhost:23073` | HTTP/2 over cleartext |

A port defaults to `80` when none is given.

### Which requests are tunneled
For a request, `goto` looks at the following in order, and tunnels the request to the endpoints of the first one that matches (all the endpoints of that one):
1. A request with a URI that starts with `/tunnel=`, or that carries the `Goto-Tunnel` header or a `Proxy-Connection` header, or that comes through a CONNECT connection: always tunneled, to the endpoints that it names.
2. Tunnels that were added for a URI and a header (the header is present, and has the given value, or the tunnel was added without a value).
3. Tunnels that were added for the URI.
4. Tunnels that were added for a header.
5. Tunnels that were added without a URI or header (broad tunnels).

The URI match is an exact, case-sensitive match of the request URI. Requests for the `goto` API URIs (e.g. `/tunnels/...`) are not tunneled unless they use the `/tunnel=` prefix or the `Goto-Tunnel` header. The request is sent to all the endpoints at the same time, with a timeout of 30 seconds, and the first response is sent back to the client with its status, headers and body; the other responses are read and discarded (but counted in the tracking). TLS to an endpoint doesn't verify its certificate. The `Host` of the forwarded request is the endpoint host (without port).

### Headers
|Header|On|Description|
|---|---|---|
| `Goto-Via-Tunnel-Count` | request to the endpoint | The number of tunnels the request has gone through (1 on the first one). |
| `Goto-Tunnel-Host` | request to the endpoint | Added by configured (non-transparent) tunnels: the label of this goto host and the tunnel count, `host-label\|1`. A goto instance that receives a request to be tunneled, and finds its own host label in this header, doesn't tunnel it: it answers with `Tunnel loop detected in header [Goto-Tunnel-Host] : [...]` (status 200). |
| `Via-Goto-Tunnel` | request to the endpoint | Added by configured tunnels: the label of this listener and the tunnel count. |
| `Goto-Tunnel` | request | The endpoints to tunnel to (see above). Removed when it was the last one. |
| `Goto-Port_0`, `Goto-Protocol_0`, `Goto-Sni_0`, `Goto-Tunnel-Host_0`, `Via-Goto-Tunnel_0`, ... | response | The `Goto-*` headers of the tunneling instance are added to the response with a hop suffix `_0` (`_1`, ... when there are more), next to the ones of the endpoint. |

The tunnels that are made by the `/tunnel=` prefix and the `Goto-Tunnel` header are transparent: the request headers that have `tunnel` in their name (other than `Goto-Tunnel` itself) are not forwarded, and `Goto-Tunnel-Host` and `Via-Goto-Tunnel` are not added, so the loop detection works for configured tunnels only. Transparent configured tunnels (`/transparent`) behave the same.

### Reports
`GET /tunnels` returns the tunnels of all ports, by port:

|Field|Description|
|---|---|
| `port` | The port |
| `tunnels` | The endpoints by tunnel ID: `id`, `url`, `address`, `host`, `port`, `transparent`, `isTLS`, `isH2`, `useRequestProto` |
| `broadTunnels` | List of tunnel IDs that apply to all requests |
| `uriTunnels` | URI to list of tunnel IDs |
| `headerTunnels` | Header name to header value to list of tunnel IDs (the value is `""` for "any value") |
| `uriHeaderTunnels` | URI to header name to header value to list of tunnel IDs |
| `proxyTunnels` | The CONNECT tunnels that are open |
| `tunnelTrackingHeaders`, `tunnelTrackingQueries` | What the `track` APIs asked to track |
| `captureTrafficLog` | Whether `traffic/capture` is on |
| `traffic` | The counts, same as `GET /tunnels/traffic` below |

```
curl -s localhost:8080/tunnels
{
  "8080": {
    "port": 8080,
    "tunnels": {
      "http:localhost:8081": {
        "id": "http:localhost:8081",
        "url": "http://localhost:8081",
        "address": "localhost:8081",
        "host": "localhost",
        "port": 8081,
        "transparent": false,
        "isTLS": false,
        "isH2": false,
        "useRequestProto": true
      }
    },
    "proxyTunnels": {},
    "broadTunnels": [],
    "uriTunnels": { "/uri1": [ "http:localhost:8081" ] },
    "headerTunnels": {},
    "uriHeaderTunnels": {},
    ...
  }
}
```
(Abbreviated; the real output also has the tracking and traffic fields.) A tunnel stays in `tunnels` after it is removed from the lists, and the endpoints that were registered for the other tunnel kinds are listed there too (e.g. `https:localhost:8081` when it was added with the `https:` protocol).

`GET /tunnels/active` returns the open CONNECT tunnels of the port, by the address of the connection to the port itself:
```
curl -s localhost:8080/tunnels/active
{
  "172.19.217.97:56872": {
    "FromAddress": "[::1]:56871",
    "ToAddress": "localhost:23073",
    "IsH2": false,
    "IsH2C": false,
    "IsTLS": false
  }
}
```
It is `{}` when there is none.

`GET /tunnels/track` (all ports, by port) and `GET /tunnels/traffic` (the port of the request) report the traffic of the requests that were tunneled:

|Field|Description|
|---|---|
| `byEndpoints` | Tunnel ID to the number of requests sent to it |
| `byURIs` | Request URI (lower-case) to count |
| `byURIEndpoints` | URI to tunnel ID to count |
| `byRequestHeaders`, `byRequestHeaderValues`, `byRequestHeaderEndpoints`, `byURIRequestHeaders` | For the headers that `track/header` was given: header to count; header to value to count; header to tunnel ID to count; URI to header to count |
| `byRequestQueries`, `byRequestQueryValues`, `byRequestQueryEndpoints`, `byURIRequestQueries` | The same for the query params that `track/query` was given |
| `byResponseHeaders`, `byResponseHeaderValues`, `byResponseHeaderEndpoints`, `byURIResponseHeaders` | The same for the tracked headers in the responses of the endpoints |
| `byResponseStatus`, `byURIResponseStatus` | Status code to count; URI to status code to count |
| `logs` | When capture is on, one entry for each tunneled request: |

A traffic log entry:

|Field|Description|
|---|---|
| `requestURI` | The URI of the request, with the query |
| `requestHeaders` | Headers of the request |
| `requestProtocol` | Protocol of the request, e.g. `HTTP/1.1` |
| `requestPayloadLength` | Length of the request body |
| `responseHeaders` | List with the headers of the response of each endpoint |
| `responsePayloadLength` | List with the length of the body of the response of each endpoint |
| `responseProtocol` | Protocol of the response |

## Examples
Tunnel everything that arrives on `8080` to `8081`, track a header and a query param, and capture the traffic:
```
curl -X POST localhost:8080/tunnels/add/localhost:8081
Tunnel added on port [8080] to endpoint [localhost:8081] for URI [], Header [:]

curl -X POST localhost:8080/tunnels/track/header/X-Req,X-Other
curl -X POST localhost:8080/tunnels/track/query/q1
curl -X POST localhost:8080/tunnels/traffic/capture=y

curl -s 'localhost:8080/foo?q1=a' -H 'X-Req: r1'
curl -s localhost:8080/tunnels/track
```

Tunnel only some requests (a URI, a header with a value, a header with any value over h2c, and a URI plus header):
```
curl -X POST 'localhost:8080/tunnels/add/localhost:8081?uri=/uri1'
curl -X POST 'localhost:8080/tunnels/add/https:localhost:8081/header/X-Tun=v1'
curl -X POST 'localhost:8080/tunnels/add/h2c:localhost:8081/header/X-Any'
curl -X POST 'localhost:8080/tunnels/add/localhost:8081/header/X-Tun=v2?uri=/uri2'
```

Remove tunnels (use the tunnel ID):
```
curl -X POST 'localhost:8080/tunnels/remove/http:localhost:8081?uri=/uri1'
curl -X POST 'localhost:8080/tunnels/remove/https:localhost:8081/header/X-Tun=v1'
```

On-the-fly tunnels:
```
curl -s localhost:8080/tunnel=localhost:8081/foo5
curl -s localhost:8080/foo4 -H 'Goto-Tunnel: localhost:8081'
curl -s -x localhost:8080 http://localhost:8081/foo6
```

Configure the tunnels of another port:
```
curl -X POST localhost:8080/port=8081/tunnels/add/localhost:8082
```

## Notes
- `{endpoint}` - The tunnel endpoint address, `[protocol:]host:port`. `remove` takes the tunnel ID (`http:host:port`).
- `{header}` - HTTP header name for tunnel matching
- `{value}` - HTTP header value for tunnel matching
- `uri` query parameter - The exact request URI that the tunnel is for. It has to be the path of the request: a request with a query string (`/uri1?a=1`) does not match a tunnel for `/uri1`, and there is no prefix or pattern matching (bug INF-09).
- `{yn}` - yes/no flag for enabling/disabling traffic capture
- Most endpoints support port-specific operations via the `/port={port}/` prefix.
- `POST /tunnels/clear` only clears the tunnels that apply to all requests: the URI, header and URI+header tunnels stay active, and so do the endpoints in `GET /tunnels` (bug INF-08). Remove those with the `remove` APIs.
- The API that adds the tunnel decides that it's transparent by looking at whether the request URI ends with `transparent`, so a `uri` query param like `?uri=/transparent` also makes the tunnel transparent (bug INF-11).
- The tunnels made on the fly (`Goto-Tunnel` header and `/tunnel=` prefix) create a new connection pool for each request. Every such request leaves a connection to the endpoint open for up to 3 minutes (bug INF-05); a configured tunnel reuses one.
- Tunnel loop detection is by the `Goto-Tunnel-Host` header, which only the non-transparent configured tunnels add. There is no other guard (no maximum for `Goto-Via-Tunnel-Count`), so transparent tunnels that point to each other are not detected as a loop (from the code, not run). Checked with configured tunnels from `8080` to `8081` and from `8081` to `8080` of one instance (same host label): the request is answered with the loop message.
