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

- TCP targets support SNI based matching if the communication is over TLS. Otherwise TCP proxy only supports a single upstream service.

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
