
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
| PUT, POST | /proxy/tcp/http/connect<br/>/`{enable\|disable}` | Enable or disable the HTTP CONNECT proxy mode on the port. See [HTTP CONNECT Proxy](#http-connect-proxy) below. |
| PUT, POST | /proxy/tcp/http/connect<br/>/delay/`{delay}` | Set a delay for the HTTP CONNECT proxy of the port, for its connections and for every transfer of its tunnels. `{delay}` is a duration (`1s`), or a range (`100ms-1s`), and may have a count (`1s:5`). `0s` or `clear` takes it away. See [HTTP CONNECT Proxy](#http-connect-proxy) below. |

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

### HTTP CONNECT Proxy
- A TCP port can also act as an HTTP CONNECT proxy: clients send `CONNECT host:port HTTP/1.1`, `goto` connects to the destination, answers `200 Connection Established`, and then relays bytes opaquely in both directions until either side is done. This is how HTTPS (or any TCP protocol) is tunneled through an HTTP proxy.
- Since a tunnel is a stream of bytes, the port is served as a TCP port and its connections never reach the HTTP server. Enabling the mode makes the port a TCP listener (closing the listener of another kind that the port had). To avoid taking down the admin API, the mode can't be enabled for the port that the API request arrives on, nor for the main port of the server: call the API on one port with the `/port={port}/...` prefix to configure another.
- A port that is already a TCP listener keeps it. To have clients talk TLS to the proxy, add the port as a `tls` listener first (e.g. `--ports 9443/tls`, or the listeners API with `protocol: tls`) and then enable the mode on it. The tunnel is opaque in both cases, so a TLS handshake with the destination (`https://` through the proxy) works inside the tunnel, on plain as well as TLS proxy ports.
- A port in this mode only serves CONNECT. A client that sends a request for a proxy (`GET http://example.com/ HTTP/1.1`, which is what `curl -x` does for `http://` URLs unless it's given `-p`) is answered `405`.
- In this mode all connections of the port are treated as CONNECT clients, and any upstream targets of the port are not used. A request that isn't `CONNECT` is answered `405` (with `Allow: CONNECT`), a malformed request or destination `400`, a destination that can't be reached `502` (`504` on connect timeout).
- Data that the client sends right behind the CONNECT request is forwarded to the destination. When one side stops sending (half close), the other side is told so and left to finish. A tunnel that is silent in both directions for 5 minutes is closed.
- The answers that the proxy gives on its own (`200 Connection Established`, and the `400`, `405`, `502` and `504` rejections) carry its identity in a `Via-Goto` header, whatever the destination is: the value is the listener label of the port followed by `(Proxy)(HTTP)`, as for the HTTP proxy. A client that shows the answer to its CONNECT request (e.g. `curl -v`) can see it. The answers to a CONNECT request that was accepted (`200`, `502`, `504`) also have `Goto-Connect-Proxy-Took`, the time that the proxy took from the first byte of the request to the answer, and `Goto-Connect-Proxy-Delay`, the delay that the proxy added to the connection, if it added one. They also tell how that time went, for the part of it that the proxy got to: `Goto-Connect-Proxy-DNS-Took`, the time that it took to resolve the name of the destination (none for an IP address), and `Goto-Connect-Proxy-Dial-Took`, the time that it took to connect to the destination once the name was resolved, with the attempts that failed (a `502` has both for a destination that refused, and only the first for a name that didn't resolve). They are part of `Goto-Connect-Proxy-Took`, and are not in the responses that go through the tunnel. A `goto` client that goes through the proxy reports them in its response (see the client README).
- The headers of the CONNECT proxy are named `Goto-Connect-Proxy-*`, so that they are not mixed up with the `Goto-Proxy-*` ones of the HTTP proxy, which it doesn't share any with. They are sent to any client, `curl -x` included, and are not something that the `goto` client adds.
- The responses of an HTTP tunnel (see below) get these headers from the proxy, each as a header line of its own, so what the destination sent is kept: `Via-Goto`; `Goto-Connect-Proxy-Host`, `Goto-Connect-Proxy-Port` and `Goto-Connect-Proxy-Remote-Address` (the host and port of the proxy, and the address of the client), which are prefixed so as not to be mixed up with the `Goto-Host` and `Goto-Port` of a `goto` destination; `Goto-Connect-Proxy-Upstream-Status`, the status that the destination answered with, as the HTTP proxy tells it, by target and endpoint (the destination is both): `{"host:port":{"host:port":200}}`, after the values that the destination sent (a `Goto-Upstream-Status` of the destination is kept as it is); `Goto-Connect-Proxy-Took`, the time that the proxy took from the first byte of the request to the head of the response (the head is sent before the body, so it's not the time of the last byte, which a header can't tell), including the delays; and `Goto-Connect-Proxy-Delay`, the delays that the proxy added to the transfers of the exchange, when it added any. A response whose request the proxy couldn't follow gets no `Goto-Connect-Proxy-Took` and `Goto-Connect-Proxy-Delay`. The time from the first byte of a tunnel to the last byte transferred is in the log line that tells that the tunnel is finished, and the first and last byte times are in the trackers.
- If a tunnel carries plain HTTP/1.x (e.g. `CONNECT host:80`), `goto` detects it from the traffic and adds its identity to the `Via-Goto` header of the responses, the way the HTTP proxy does: the value is the listener label of the port followed by `(Proxy)(HTTP)`, added as a header line of its own, so a `Via-Goto` that the destination sent (e.g. a `goto` server) is kept and comes with the proxy's value. Informational responses (`100 Continue`) are left alone. The messages are otherwise passed on byte for byte. HTTPS and any other protocol are never looked into, and neither is HTTP/2 (h2c). If the proxy can't follow what the traffic carries (malformed or oversized heads, conflicting lengths), it passes on the rest of it as it is, and the responses that it couldn't follow get no `Via-Goto`. After a protocol switch (`101`, e.g. WebSocket) or a `CONNECT` through the tunnel, the rest of the connection is passed on as it is.
- Tunnels are tracked like proxied TCP connections, per destination and client address, and are reported by the TCP proxy tracker APIs. Requests that get rejected are counted as rejects.
- Don't configure a custom ALPN handshake preamble on a CONNECT proxy port: it is read from the client before the CONNECT request is.
- A delay can be set for the proxy of a port (`/proxy/tcp/http/connect/delay/{delay}`). It is applied to the connection of a client, before the proxy connects to the destination (requests that are rejected are not delayed), and to every transfer of the tunnels of the port, in both directions: what is read from the client or from the destination in one go is held back for the delay before it is passed on. A range (`100ms-1s`) picks a delay at random for each event. A count (`1s:5`) makes the delay apply to the next 5 of these events of the port, connections and transfers altogether, and it is used up by whichever of them come first, on any tunnel; without a count the delay is unlimited. The delay is also applied to the tunnels that are already open, and a tunnel is not idle (see the idle timeout above) while it waits. Delayed events are counted in the `delayCount` of the trackers. The delay is part of the report of the port as `http.connectDelay`, with the count that is left.
- Anyone who can reach the port can tunnel to any destination that `goto` can reach.
- To see where the time of a call through the proxy goes (DNS, connecting, the CONNECT exchange, TLS, the response), as a client that uses the resolver and dialer of Go gets it, run the [connectprobe](../../../tools/connectprobe/README.md) tool where the client runs (`make connect-probe`).
- The `goto` client can send its calls through the port, to `http` and `https` targets: set `proxy` in the call of the [client API](../../client/README.md#calling-via-a-proxy) to the URL of the port. The responses to calls to an `http` target have the `Via-Goto` of the proxy.

#### CONNECT Proxy in the YAML Config
A CONNECT proxy port can be set up in the `proxy` section of a startup config file (the files that `goto` loads and watches at startup), or sent to a running `goto` with `goto ctl apply`. The CONNECT proxy is configured under the `http` field of a `tcp` proxy:

```yaml
proxy:
- tcp:
    port: 9000
    enabled: true
    http:
      connect: true
      connectDelay:
        min: 1s
        max: 2s
        count: 5
```

| Field | Description |
|---|---|
| `http.connect` | `true` makes the port a CONNECT proxy port, the way the `connect/enable` API does. |
| `http.connectDelay` | Optional delay, with `min`, `max` and `count`, the way the `connect/delay` API sets one (`1s-2s:5`). A delay needs a `max` (a missing `min` is 0), and without a `count` it is unlimited. It is set for the port even if `connect` is not `true`, and applies when the mode is turned on. |

- The port can have `upstreams` as well, which are loaded as before; a port that only has `http` needs none. A port in CONNECT mode doesn't use them, though (see above).
- The port is opened as a TCP listener if it isn't one already. A TCP or TLS listener of the port in the `listeners` section of the config is kept, which is how the clients of the proxy talk TLS to it (`protocol: tls`). Listeners are loaded before proxies, so they can be in the same file.
- The ports of the server that serve the APIs (HTTP, gRPC and JSON-RPC), and invalid ports, can't be CONNECT proxy ports, and are skipped with an error in the log.
- `enabled` is not applied by the startup for CONNECT, as it isn't for upstreams.
- When the file changes the proxy of the port is set up again from the new content, and when the file is removed the CONNECT mode and the delay of its ports are taken away. The TCP listener that was opened for the mode stays, as it does when the mode is disabled with the API.
- `goto ctl apply` sends the delay, and then the mode, of a port to the remote `goto` with the `/port={port}/proxy/tcp/http/connect/...` APIs, so the admin port of the remote is never the one that becomes the proxy. It sends the `upstreams` of a TCP proxy as well, to the port of the proxy.

```
# Enable CONNECT mode on port 9000 (from the admin port), and use it
curl -X POST localhost:8080/port=9000/proxy/tcp/http/connect/enable
curl -x http://localhost:9000 https://example.com

# Plain HTTP is tunneled too, which curl does only when it's asked to (-p), and its responses get the Via-Goto of the proxy
curl -p -x http://localhost:9000 -i http://example.com

# Delay the connections and the transfers by 200ms-500ms, for the next 10 of them only, and then take the delay away
curl -X POST localhost:8080/port=9000/proxy/tcp/http/connect/delay/200ms-500ms:10
curl -X POST localhost:8080/port=9000/proxy/tcp/http/connect/delay/clear

# Disable it
curl -X POST localhost:8080/port=9000/proxy/tcp/http/connect/disable
```

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