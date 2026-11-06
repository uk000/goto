
# UDP Proxy
- A UDP listener of `goto` can act as a UDP proxy: every datagram that arrives at the port is forwarded to an upstream UDP endpoint, and the upstream's answer (if any) is sent back to the client that sent the datagram.
- The proxy supports any number of upstreams for a port. Datagrams are handed to the running upstreams in turn (round-robin, in the order of the upstream addresses). The address of an upstream is resolved when it is added.
- Each datagram is forwarded from a socket of its own to the upstream, and the proxy waits up to 2 seconds for one answer (`upstreamTimeout`). No answer within the timeout means nothing is sent back to the client. At most 1024 datagrams of a port are handled at a time; the ones that arrive while that many are being handled are dropped (and logged).
- A configured delay (`delay` below) is applied twice for a round trip: before the datagram is sent to the upstream, and before the answer is sent back to the client. A range (`100ms-200ms`) picks a random delay each time.
- If a datagram is a DNS query, the proxy extracts the queried domain and counts it in the tracker (this is useful to watch DNS traffic when `goto` fronts a DNS server).
- Clients are told apart by their address, up to 10000 at a time; beyond that the proxy forgets them and counts them as new ones when they show up again.

### UDP Proxy Operations
- **POST** `/proxy/udp/{port}/{endpoint}`
- **POST** `/proxy/udp/{port}/{endpoint}/delay/{delay}`
- **POST** `/proxy/udp/{port}/delay/{delay}`

#### UDP Proxy APIs
###### <small>* The `{port}` of these APIs is an explicit port number; they are served by any port of `goto`, so there is no need for the `/port={port}/...` prefix.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/proxy/udp/`{port}`/`{endpoint}` | Open a UDP listener on `{port}` (if one is not open yet) and proxy it to `{endpoint}` (format `host:port`). Calling it again with another endpoint adds another upstream to the same port. Responses: `200` `Proxying UDP on port [25089] to upstream [127.0.0.1:25099]`; `400` `Invalid port [0] or upstream address [...]`; `400` `invalid upstream address [nohost.invalid:25097]: lookup nohost.invalid: no such host` (the host must resolve and the port must be given); `400` `Failed to open listener on port [N] with error: ...`. |
| POST |	/proxy/udp/`{port}`/`{endpoint}`<br/>/delay/`{delay}` | Same as above, with a delay. `{delay}` is a duration (`300ms`) or a range (`100ms-200ms`). The response text is the same as without the delay; `400` `Invalid delay value [bogus]` for an invalid delay. |
| POST |	/proxy/udp/`{port}`/delay/`{delay}` | Set the delay of all the upstreams of the proxy of the port (a UDP listener must exist on the port). Responses: `200` `Delay configured for UDP port [25089]`; `400` `Invalid delay value [bogus]`; `400` `No listener for port 25098`; `400` `No upstream to configure the delay of on UDP port [N]` if the port has no proxy upstream. |

#### UDP Proxy APIs under `/server/udp`
A second set of APIs sets up, tunes and stops a UDP proxy per upstream. They are served by any HTTP port of `goto` (`{port}` is the UDP port, so no `/port={port}/` prefix is needed). `{upstream}` is `host:port`, and `{delay}` is a duration (`100ms`) or a range (`100ms-200ms`). They are also listed in the [server README](../../server/README.md#tcp-and-udp-server-apis).

|METHOD|URI|Description|
|---|---|---|
| POST | /server/udp/`{port}`/proxy/`{upstream}` | Open a UDP listener on `{port}` if there is none and proxy it to `{upstream}`. `200` `Proxying UDP on port [25090] to upstream [127.0.0.1:25091] with delay [0s-0s]`; `400` `Invalid port [N] or upstream address [...]` or `Failed to open UDP listener on port [N] with error: ...` |
| POST | /server/udp/`{port}`/proxy/`{upstream}`<br/>/delay/`{delay}` | Same, with a delay. The response ends in the delay as `[100ms-100ms]`. A delay that cannot be parsed is taken as no delay (this route does not answer `400` for it, unlike `/proxy/udp/.../delay/{delay}`). |
| POST | /server/udp/`{port}`/delay/`{upstream}`/`{delay}` | Set the delay of one upstream of the port. The port must have a UDP listener. `200` `Delay configured for UDP port [25090]`; `400` `Invalid delay value [bogus]`; `400` `No listener for port 25098` |
| POST | /server/udp/`{port}`/stop/`{upstream}` | Stop proxying the port to `{upstream}`. `200` `Stopped Proxying UDP on port [25090] to upstream [127.0.0.1:25091]`; `400` `No listener for port 25098` if the port has no UDP listener |

```
curl -X POST localhost:8080/server/udp/9053/proxy/localhost:5353/delay/100ms
curl -X POST localhost:8080/server/udp/9053/delay/localhost:5353/200ms-300ms
curl -X POST localhost:8080/server/udp/9053/stop/localhost:5353
```

There is no API to read the UDP proxy configuration or tracker (see the tracker schema below), and the only way to remove an upstream is `/server/udp/{port}/stop/{upstream}`.

```
# Open UDP port 9053 on goto and proxy it to a UDP service on localhost:5353
curl -X POST localhost:8080/proxy/udp/9053/localhost:5353
Proxying UDP on port [9053] to upstream [localhost:5353]

# Same, with a random delay of 100ms to 200ms (applied before sending to the upstream and before replying)
curl -X POST localhost:8080/proxy/udp/9053/localhost:5353/delay/100ms-200ms

# Change the delay of the proxy of port 9053 later
curl -X POST localhost:8080/proxy/udp/9053/delay/300ms

# Send a datagram through the proxy (with a UDP echo service as the upstream, the reply comes back after about twice the delay: ~0.6s for 300ms)
printf 'hello' | nc -u -w3 localhost 9053
```

#### UDP Proxy Tracker JSON Schema
The tracker is kept for each proxied port. In this version no API returns it.

|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of distinct downstream clients seen (UDP has no connections, so a client address is counted once as a connection) |
| packetCount | int  | Number of packets received  |
| packetCountByUpstream | map[string]int  | Number of packets sent per upstream endpoint |
| packetCountByDomain | map[string]int  | Number of packets sent per DNS domain (for proxying to DNS servers) |
| packetCountByUpstreamDomain | map[string]map[string]int  | Number of packets sent per upstream endpoint per DNS domain (for proxying to DNS servers) |
