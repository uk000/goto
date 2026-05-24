
# TCP Proxy
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
  
### TCP Proxy Operations
- **POST** `/proxy/tcp/{port}/{endpoint}?sni={sni}`
- **POST** `/proxy/tcp/{port}/{endpoint}/retries/{retries}?sni={sni}`

#### TCP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST |	/proxy/tcp/targets<br/>/add/`{name}`<br/>?<br/>address=`{address}`<br/>&sni=`{sni}` | Add a new TCP upstream target with the given name and address, where the address is in the format `hostname:port`. The optional `sni` param can be a comma-separated list of host names to perform SNI based routing. The presence of `sni` param indicates that the TCP traffic for this proxy port is encrypted. |
| POST |	/proxy/tcp/{port}/{endpoint}?sni={sni}           | Setup TCP proxy on the given port, forwarding to the given endpoint. Optionally specify an SNI match for TLS traffic. |
| POST |	/proxy/tcp/{port}/{endpoint}/retries/{retries}?sni={sni}   | Setup TCP proxy on the given port, forwarding to the given endpoint, and retry failed connections as well as failed packet writes up to the given number of retries |
| GET | /proxy/report/tcp | Get a report of the activity so far for all TCP targets |

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

#### TCP Proxy Startup Config Example

```
proxy:
  - tcp:
      port: 10000
      enabled: true
      upstreams:
        http-8081-8082:
          delay:
            min: 0s
            max: 0s
          retries: 1
          retryDelay:
            min: 0s
            max: 0s
          dropPct: 0
          endpoints:
            ep1:
              address: "localhost:8081"
            ep2:
              address: "localhost:8082"
```