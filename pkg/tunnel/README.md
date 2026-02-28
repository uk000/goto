# Tunnel

`Tunnel` feature allows a `goto` instance to act as a L7 tunnel, receiving HTTP/HTTPS/H2 requests from clients and forwarding those to any arbitrary endpoints over the same or a different protocol. This feature can be useful in several scenarios: e.g. 
- A client wishes to reach an endpoint by IP address, but the endpoint is not accessible from the client's network space (e.g. K8S overlay network). In this case, a single `goto` instance deployed inside the overlay network (e.g. K8S cluster) but accessible to the client network space via an FQDN can receive requests from the client and transparently forward those to any overlay IP address that's visible to the `goto` instance.
- Route traffic from a client to a service through `goto` proxy in order to inspect traffic and capture details in both directions.
- Observe network behavior (latencies, packet drops, etc) between two endpoints
- Send a request from a client to two or more service endpoints and analyze results from those, while sending the response to client from whichever endpoint responds first.
- Send a request on a multi-hop journey, routing via multiple goto tunnels, in order to observe latency or other network behaviors.
- Test a client and/or service's behavior if the other party that's communicating with it changed its protocol from HTTP/1 to HTTP/2 without having to change the real applications' code.

There are four different ways in which requests can be tunneled via `Goto`:

#### 1. Configured Tunnels
<div style="padding-left:20px">
  Any listener can be converted into a tunnel by calling `/tunnels/add/{protocol:address:port}` API to add one or more endpoints as tunnel destinations. When a tunnel has more than one endpoint, the requests are forwarded to all the endpoints, and the earliest response gets sent to the client.
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
</div>

### Tunnel APIs

###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

All `Goto` APIs support tunnel prefix, allowing any `goto` API to be proxied from one instance to another. In addition, any arbitrary API can also be called using the tunnel prefix. See the tunnel feature description above for the specification of `{endpoint}` used in the tunnel APIs. To configure a tunnel on a port using another port, use `port={port}` prefix format.

|METHOD|URI|Description|
|---|---|---|
| ALL       |	/`tunnel={endpoint}`/`...` | URI prefix format to tunnel any request on the fly. |
| POST, PUT |	/tunnels/add<br/>/`{endpoint}`?`uri={uri}`| Adds a tunnel on the listener port to redirect traffic to the given `endpoint`. If the URI query param is specified, only traffic to the given URI is intercepted. |
| POST, PUT |	/tunnels/add/`{endpoint}`<br/>/header/`{key}={value}`<br/>?`uri={uri}` | Adds a tunnel that acts upon requests carrying the given header key and optional value. If value is omitted, just the presence of the header key triggers the tunnel. If a URI is specified as a query param, the header match is performed only on requests to the given URI. |
| POST, PUT |	/tunnels/add<br/>/`endpoint}`/transparent | Add a transparent tunnel that doesn't add goto request headers when forwarding a request to the upstream endpoints. However, goto response headers are still added to the response sent to the downstream client. |
| POST, PUT |	/tunnels/remove<br/>/`{endpoint}`?`uri={uri}` | Remove a configured endpoint tunnel on the listener port. If the URI query param is specified, only the tunnel for that URI is removed. |
| POST, PUT |	/tunnels/remove/`{endpoint}`<br/>/header/`{key}={value}`<br/>?`uri={uri}` | Remove a configured endpoint tunnel on the listener port for the given header match. If the URI query param is specified, tunnels for that URI are removed. |
| POST, PUT |	/tunnels/clear | Clear all tunnels on the listener port on which the API is called. |
| GET |	/tunnels | Get currently configured tunnels. |
| GET |	/tunnels/active | Get currently active tunnels. |
| POST, PUT |	/tunnels/track<br/>/header/`{headers}` | Track tunnel traffic on the listener port for the given headers. |
| POST, PUT |	/tunnels/track<br/>/query/`{params}` | Track tunnel traffic on the listener port for the given query params. |
| POST, PUT |	/tunnels/track/clear | Clear tunnel traffic tracking on the listener port. |
| GET |	/tunnels/track/ | Get tunnel traffic tracking report. |
| POST, PUT |	/tunnels/traffic<br/>/capture=`{yn}` | Enable/disable capture of tunnel traffic on the listener port. |
| GET |	/tunnels/traffic | Get tunnel traffic log if traffic capturing was enabled for the listener port. |


## Notes
- `{endpoint}` - The tunnel endpoint address
- `{header}` - HTTP header name for tunnel matching
- `{value}` - HTTP header value for tunnel matching
- `uri` query parameter - Optional URI pattern for tunnel matching
- `{yn}` - yes/no flag for enabling/disabling traffic capture
- Most endpoints support port-specific operations via query parameters or headers
