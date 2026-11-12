# <a name="proxy"></a>
# Proxy


`Goto` can act as an HTTP, TCP, UDP or MCP proxy that sits between a downstream client and an upstream service, allowing you a point in the network where the communication between the downstream client and upstream service can be observed and affected.
The HTTP Proxy implementation is lot more comprehensive than other proxies in that it allows for manipulation of requests and responses, and can perform multiplexing to multiple upstreams.
The TCP and UDP Proxy feature forward each connection (TCP) or datagram (UDP) to one upstream, with some basic tracking (TCP routes by TLS SNI if asked to, and can also act as an HTTP CONNECT proxy). The gRPC proxy forwards the calls of a service to an upstream gRPC endpoint, optionally teeing the traffic to another port.
The MCP proxy feature supports proxying all MCP traffic on a port, or forwarding requests for a specific MCP tool call.

See the following docs for corresponding protocol proxies:
### * [HTTP Proxy](http/README.md)
### * [TCP Proxy](tcp/README.md)
### * [UDP Proxy](udp/README.md)
### * [gRPC Proxy](grpc/README.md)
### * [MCP Proxy](mcp/README.md)

### Chaos
In addition to request routing, the `goto` proxy also offers some chaos features:
- A `delay` can be configured that's applied to all communication in either direction. For the HTTP proxy it is the `delay` of the `trafficConfig` of a target or trigger, applied before each upstream request (and reported in the `Goto-Proxy-Delay` response header). For the TCP proxy it is the `delay` of an upstream, applied to each packet write (and the CONNECT mode has its own delay). For the UDP proxy it is applied before sending to the upstream and before sending the answer back. For the gRPC proxy it is applied to each proxied call. The HTTP `trafficConfig` also has `retries`, `retryDelay` and `retryOn` to retry upstream calls on given response statuses.
    > Note that this delay feature is separate from the one offered by `Goto` as a Server, via [URIs](../server/request/README.md#request-uri-tracking) and [Response Delay](../server/response/delay/README.md#response-delay). The URI delay feature in particular allows for more fine-grained delay configuration that can be used in conjunction with the proxy feature to apply delays to URIs that eventually get routed via proxy.
- A `dropPct` percentage can be configured on a TCP upstream that specifies what percentage of writes should be skipped in either direction. If configured, `goto` will skip every `100/{pct}` write to/from the upstream, applied across packets in both directions. (The HTTP proxy has no drop setting.)
  > While technically this is not a true network TCP packet drop, it does allow for some interesting chaos testing where data is randomly lost between the two parties.
  > If you want to drop some TCP writes for HTTP traffic, you can configure the port in `goto` as a TCP port and use the TCP features described below while the client and upstream still communicate over HTTP.
  > For TLS traffic, the initial TLS handshake is also subject to the drop, which can create more chaos by randomly failing TLS handshake.
- The HTTP proxy allows you to add/remove headers and query params, which can be a good way to create unexpected circumstances for the client and service. For example, configure the proxy to remove a required header, or change some header's value, and observe whether the two parties deal with the situation gracefully.
- Additional chaos can be applied via listener API, by closing/reopening a connection.



#### Proxy Admin APIs
###### <small>* Unless the URI names a port, these APIs apply to the port that the request arrives on, and can be invoked with prefix `/port={port}/...` to configure/read data of one port via another (the port must already be a listener of this `goto`; otherwise the request is answered `200` with `Port [{port}] not configured` and nothing happens).</small>

There is no common `/proxy/targets/...`, `/proxy/enable` or `/proxy/report` API: each proxy type has its own APIs, documented in its own README.

|Proxy|APIs|Details|
|---|---|---|
| HTTP | `/proxy/http/...` (targets, headers, enable/disable, proxy responses, trackers) | [HTTP Proxy APIs](http/README.md) |
| TCP and HTTP CONNECT | `/proxy/tcp/...` (`/proxy/tcp/{port}?address=`, upstreams, report, `http/connect/...`) | [TCP Proxy APIs](tcp/README.md) |
| UDP | `/proxy/udp/{port}/...` (and the equivalent `/server/udp/{port}/...`) | [UDP Proxy APIs](udp/README.md) |
| gRPC | `/proxy/grpc/...` | [gRPC Proxy APIs](grpc/README.md) |
| MCP | `/proxy/mcp/...` | [MCP Proxy](mcp/README.md) |

Proxies can also be configured at startup with the `proxy` section of a config file (list of `http`, `tcp` and `grpc` entries, see [sample-proxy-config.yaml](sample-proxy-config.yaml)). The startup loads only `http`, `tcp` and `grpc` entries; there is no `udp` entry.

#### Tracker APIs shared by the proxies
|METHOD|URI|Description|
|---|---|---|
| GET | /proxy/http/trackers<br/>/proxy/http/trackers/all | HTTP proxy counters of the port (`{"port":N,"enabled":true,"http":{...}}`) or of all the ports |
| POST | /proxy/trackers/clear | Reset the HTTP proxy tracker of the port. Answers `200` `Proxy[8080]: HTTP Proxy Tracking Info Cleared`. |
| POST | /proxy/trackers/all/clear | Reset the HTTP proxy trackers of all ports |
| GET | /proxy/tcp<br/>/proxy/tcp/all | TCP proxy report including its tracker, of the port or of all ports |
| GET | /proxy/grpc/status | gRPC proxy report including its tracker, of the port |

There is no tracker API for the UDP proxy in this version, and the TCP and gRPC trackers are cleared only by clearing/reconfiguring the proxy (`POST /proxy/grpc/clear`, the startup config).

The per-target, per-protocol knobs are fields of the targets: HTTP `trafficConfig` (`delay`, `retries`, `retryDelay`, `retryOn`, `transparent`, ...), TCP upstream `delay`, `retries`, `retryDelay` and `dropPct`, gRPC and UDP `delay`.


See [Proxy Example](../../docs/proxy-example.md)


## Notes
- Most endpoints support port-specific operations via query parameters or headers
- Query parameters are optional unless marked as required
- All POST/PUT endpoints accept JSON payloads where applicable
