# <a name="proxy"></a>
# Proxy


`Goto` can act as an HTTP, TCP, UDP or MCP proxy that sits between a downstream client and an upstream service, allowing you a point in the network where the communication between the downstream client and upstream service can be observed and affected.
The HTTP Proxy implementation is lot more comprehensive than other proxies in that it allows for manipulation of requests and responses, and can perform multiplexing to multiple upstreams.
The TCP and UDP Proxy feature only supports forwarding to a single upstream, with some basic tracking.
The MCP proxy feature supports proxying all MCP traffic on a port, or forwarding requests for a specific MCP tool call.

See the following docs for corresponding protocol proxies:
### * [HTTP Proxy](http/README.md)
### * [TCP Proxy](tcp/README.md)
### * [UDP Proxy](udp/README.md)
### * [gRPC Proxy](grpc/README.md)
### * [MCP Proxy](mcp/README.md)

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
