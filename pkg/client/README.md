<a name="toc"></a>

# Goto Client: Targets and Traffic

As a client tool, `goto` offers the feature to configure multiple targets and send http/https/h2/grpc traffic:

- Allows targets to be configured and invoked via REST APIs
- Configure targets to be invoked ahead of time before invocation, as well as auto-invoke targets upon configuration
- Invoke selective targets or all configured targets in batches
- Control various parameters for a target: number of concurrent, total number of requests, minimum wait time after each replica set invocation per target, various timeouts, etc
- Headers can be set to track results for target invocations, and APIs make those results available for consumption as JSON output.
- Retry requests for specific response codes, and option to use a fallback URL for retries
- Make simultaneous calls to two URLs to perform an A-B comparison of responses. In AB mode, the same request ID (enabled via sendID flag) are used for both A and B calls, but with a suffix `-B` used for B calls. This allows tracking the A and B calls in logs.
- Have client invoke a random URL for each request from a set of URLs
- Generate hybrid traffic that includes HTTP/S, H2 and GRPC requests. (A target with protocol `tcp` is accepted by the API, but a TCP target cannot currently be invoked.)

The invocation results get accumulated across multiple invocations until cleared explicitly. Various results APIs can be used to read the accumulated results. Each invocation gets an invocation counter (used as the key in the `/client/results/invocations` output). Note that the counter is not reset by `/client/results/clear`, but it is reset to 0 by `/client/targets/stop/all`, so invocations started after a `stop/all` start at counter 1 again and would share their invocation results entry with earlier invocations that had the same counter. When a peer is connected to a registry instance, it stores all its invocation results in a registry locker. The peer publishes its invocation results to the registry at an interval of 3-5 seconds depending on the flow of results. See Registry APIs for detail on how to query results accumulated from multiple peers.

In addition to keeping the results in the `goto` client instance, those are also stored in a locker on the registry instance if enabled. (See `--locker` command arg).

Targets are defined via the APIs below or pushed by the registry (see [Registry Peer Targets APIs](../registry/README.md)); a `traffic` (or `client`) section in a goto config file (`--config`, `goto ctl apply`) is currently not applied.

Client sends headers `From-Goto` and `From-Goto-Host` to pass its identity to the server (these are added to the headers of every target that's added via the API, and are visible in the target's `headers`).


# <a name="client-apis"></a>
### Client APIs
|METHOD|URI|Description|
|---|---|---|
| GET, POST, PUT, OPTIONS | /client/http/`{invoke\|call}` | Invoke a single HTTP call using a `CallSpec` definition given as a JSON or YAML request body (see schema example below). `/client/http/call` is an alias of `/client/http/invoke`. The response is YAML by default, or JSON if the request has header `Accept: application/json`. It has a `Results` map keyed by `<url>[<n>]` (one entry per call: `headers`, `payload`, `serverCert`, `Status`), `serverCerts`, `clientCert` and `Statuses`. An invalid spec or a call that fails returns `400` with the error message. |
| POST      | /client/targets/add                   | Add a target for invocation. [See `Client Target JSON Schema` for Payload](../../docs/client-api-json-schemas.md#client-target-json-schema). Responds `200` with `Added target: {json}` (the JSON has the defaults filled in), or `400` if the payload can't be parsed or the target is invalid (e.g. `name` or `url` missing). A target with `autoInvoke` is invoked right away. |
| POST      |	/client/targets/`{targets}`/remove      | Remove given targets (comma-separated names). Responds `200` with `Targets Removed: [...]` (names that don't exist are ignored), or `406` if any of the targets is running. |
| POST      | /client/targets/`{targets}`/invoke      | Invoke given targets (comma-separated names) asynchronously: the API returns `200` with an empty body as soon as the invocations are started, and the progress is visible via the `active` and `results` APIs. Returns `406` with `No targets to invoke` if none of the given targets exist. |
| POST      |	/client/targets/invoke/all            | Invoke all targets. Returns `406` with `No targets to invoke` if no targets are configured. |
| POST      | /client/targets/`{targets}`/stop        | Stops running invocations of the given targets (comma-separated names). Responds `200` with `Targets [...] stopped` or `No targets to stop`. The call waits up to 10 seconds for the targets to stop. |
| POST      | /client/targets/stop/all              | Stops all running targets. Note that this also resets the invocation counter. |
| GET       |	/client/targets                       | Get list of currently configured targets |
| GET       |	/client/targets/{target}           | Get details of given target. If the target doesn't exist, responds `200` with `{"error": "Target not found: <name>"}`. |
| POST      |	/client/targets/clear                 | Remove all targets. Responds `406` if any target is running. |
| GET       |	/client/targets/active                | Get list of currently active (running) targets, as `{"activeCount": n, "activeInvocations": {target: {invocationCounter: InvocationStatus}}}`. See [Active Targets Schema](../../docs/client-api-json-schemas.md). |
| PUT, POST |	/client/track/headers/`{headers}`   | Set the response headers to track for response counts per target. `{headers}` is a comma-separated list of header names (case-insensitive); a header can be followed by cross-headers using `\|`, e.g. `x-foo\|x-bar,x-baz` tracks `x-foo`, `x-bar` and `x-baz`, and also tracks `x-bar` as a cross-header of `x-foo`. This replaces any previously tracked headers, and applies to invocations started after the call. |
| PUT, POST |	/client/track/headers/add/`{headers}`   | Same as `/client/track/headers/{headers}`. (Despite the name, this too replaces the previously tracked headers rather than adding to them.) |
| POST      | /client/track/headers/clear           | Remove all tracked headers |
| GET       |	/client/track/headers                 | Get list of tracked headers |
| PUT, POST |	/client/track/time/`{buckets}`   | Set time buckets for tracking response counts per bucket. Buckets are given as a comma-separated list of `low-high` values in millis, e.g. `0-100,101-300,301-1000`. A `high` value of 0 means no upper limit (e.g. `1001-0`). Responds `400` with `Invalid time bucket` for an invalid bucket (e.g. `0-0`). |
| POST      | /client/track/time/clear           | Remove all tracked time buckets |
| GET       |	/client/track/time                 | Get list of tracked time buckets, as an array of `[low, high]` pairs |
| GET       |	/client/results                       | Get combined results for all invocations since last time results were cleared. The results are keyed by target name, with the key `""` holding the results across all targets (populated only if enabled via `/client/results/all/{enable}`). |
| GET       |	/client/results/invocations           | Get invocation results broken down for each invocation that was triggered since last time results were cleared. The invocation results are collected only if enabled via `/client/results/invocations/{enable}`. |
| POST      | /client/results/clear                 | Clear previously accumulated invocation results |
| POST      | /client/results<br/>/all/`{enable}`          | Enable/disable collection of cumulative results across all targets. This gives a high level overview of all traffic, but at a performance overhead. Disabled by default. `{enable}` is `y`, `yes`, `true` or `1` to enable; any other value disables. |
| POST      | /client/results<br/>/invocations/`{enable}`          | Enable/disable collection of results by invocations. This gives more detailed visibility into results per invocation but has performance overhead. Disabled by default. `{enable}` is `y`, `yes`, `true` or `1` to enable; any other value disables. |
| POST      | /client/grpc/call<br/>/grpc/client/call | Invoke a gRPC method on a given endpoint, using a JSON or YAML `GRPCCall` body: `{"service": "...", "method": "...", "endpoint": "host:port", "headers": {...}, "payloads": {"linear": [{"payload": "{json}"}]}, "push": false, "result": true}`. The service and method are discovered via reflection on the endpoint. All of `service`, `method`, `endpoint` and `payloads` are required (`400` otherwise). The response is a JSON array with the response `Headers` of each call, and `Payload` (included only if `result` is true). A failed call returns `400` (cannot connect or unknown method) or `503` (call failed). |
| POST      | /client/grpc/call<br/>/`{service}`/`{method}`/`{endpoint}` | Invoke a gRPC method with the request body as the payload (a JSON representation of the method's input message), e.g. `POST /client/grpc/call/Goto/echo/localhost:8080 -d '{"text":"hi"}'`. Also available as `/grpc/client/call/{service}/{method}/{endpoint}`. |
| POST      | /client/grpc/call<br/>/`{service}`/`{method}`/`{endpoint}`/stream | Same as above, but the response messages are written to the response as they arrive (useful for server-streaming methods). Also available as `/grpc/client/call/{service}/{method}/{endpoint}/stream`. |
| POST      | /log/client/`{enable}`                | Enable/disable client logging completely. See [Log APIs](../log/README.md). |

### One-off Call JSON schema
```json
curl -X POST localhost:8080/client/http/invoke \
  -H 'Content-Type: application/json' \
  -d '{
      "url": "example.com/path",
      "proxy": "http://localhost:9000",
      "authority": "example.com",
      "method": "GET",
      "count": 2,
      "requestID": true,
      "h2": false,
      "tls": true,
      "noSNI": false,
      "verifyTLS": false,
      "tlsVersion": 772,
      "clientCert": "SomePreLoadedClientCert",
      "alpn": {"protos": ["istio", "istio-peer-exchange", "h2"]},
      "payload": ["hello", "world"],
      "streamDelay": "100ms",
      "headers": {
        "add": {
          "x-test": "value"
        },
        "remove": ["x-unwanted"],
        "forward": ["x-forwarded-for"]
      }
    }'
```

|Field|Data Type|Description|
|---|---|---|
| url | string | URL to call (required). `http://` is assumed if the URL has no scheme. |
| proxy | string | Optional HTTP proxy to go through. See [Calling via a Proxy](#calling-via-a-proxy). |
| authority | string | Optional `Host`/authority to use for the request instead of the URL's host. |
| method | string | HTTP method. |
| count | int | Number of times to call the URL (default 1). |
| requestID | bool | Send a new UUID with each call, as both the `x-request-id` header and the `x-request-id` query param. |
| h2 | bool | Use HTTP/2. |
| tls | bool | Use TLS for the call. |
| noSNI | bool | Don't send SNI in the TLS handshake. |
| verifyTLS | bool | Verify the server's TLS certificate. |
| tlsVersion | int (uint16) | TLS version code: `769` (1.0), `770` (1.1), `771` (1.2), `772` (1.3). |
| clientCert | string | Name of a pre-uploaded client cert/key pair to present to the server. |
| alpn | object | ALPN config: `{"protos": [...], "keepDefault": bool, "handshake": {...}}`. See `alpn` in [Client JSON Schemas](../../docs/client-api-json-schemas.md). |
| payload | []string | Request payload. A single element is sent as the body; multiple elements are streamed as chunks with `streamDelay` between them. |
| streamDelay | string | Delay between payload chunks when streaming (e.g. `100ms`). |
| headers | object | Headers to `add` (map of name to value), `remove` (list of names), and `forward` (list of request header names to forward from the incoming API request to the call). |

#### Calling via a Proxy
The `proxy` field of a one-off call is the URL of an HTTP proxy (`http://host:port`, or `https://host:port` for a proxy that is reached over TLS; a bare `host:port` is taken as `http`) that the call goes through. The call asks the proxy for a tunnel to its target (`CONNECT host:port`), and is sent through the tunnel, for an `http` target as well as an `https` one. This is what a port of `goto` in [CONNECT mode](../proxy/tcp/README.md#http-connect-proxy), plain or TLS, serves.
- For an `https` target (`"tls": true`) the TLS handshake with the target happens over the tunnel. The TLS settings of the call (SNI, TLS version, certificate verification, client cert, ALPN protocols) apply to the connection with the target, and the server cert is reported as for any TLS call.
- For an `http` target the request goes through the tunnel as it would go to the target directly. If the proxy is a `goto` port, its identity is added to the `Via-Goto` header of the responses (see the proxy README), which shows in the headers of the results as well as in `Via-Goto` of the response of the API. The `Via-Goto` that the proxy adds to its answer to the CONNECT request is not part of the results.
- The tunnel is shared by the calls of a request (`count`), and a proxy that doesn't give a tunnel (any answer to the CONNECT request that is not a success, e.g. a proxy that can only forward requests, or one that doesn't tunnel to the port of the target) fails the call with an error that says so.
- Without `proxy`, the proxy of the environment (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) applies as before, which is never used for localhost targets and gets `http` requests as proxy requests. With `proxy`, the proxy applies to all targets, localhost included.
- A call that has a `proxy` is rejected (`400`) if it also has `h2` (the HTTP/2 transport has no proxy support), or if it is a TLS call that has `noSNI` or an ALPN `handshake` (the handshake over the tunnel is done by `net/http`, always with SNI and without a preamble), or if the proxy URL is not an `http`/`https` URL with a host.

```
# Go through a goto port 9000 that is in CONNECT mode, to an http target and to an https one
curl -X POST localhost:8080/port=9000/proxy/tcp/http/connect/enable
curl -X POST localhost:8080/client/http/invoke -d '{"url": "http://localhost:8081", "proxy": "http://localhost:9000"}'
curl -X POST localhost:8080/client/http/invoke -d '{"url": "https://example.com", "proxy": "http://localhost:9000"}'
```

#### Timings of a One-off Call
The response of a one-off call tells what each of its calls went through, in headers of its own. Each header is a JSON array that has a time for each call (`count`), in the order that the calls were made, like `Goto-Upstream-Status` has the statuses. A phase that a call didn't go through (an IP address has no name to resolve, a call that reuses the connection of an earlier one has nothing to connect to) is `"-"` in the array, and a header that no call has a time for is left out.
(Header names are case-insensitive, and are sent in the canonical form of Go: `Goto-Client-Dns-Took` for `Goto-Client-DNS-Took`.)

| Header | The time of |
|---|---|
| `Goto-Client-DNS-Took` | Resolving the name of the first hop: the proxy if the call goes through one, the target if not. |
| `Goto-Client-TCP-Took` | Connecting to the first hop once its name is resolved. It has the wait for the other address family in it (the dialer tries the second one 300ms after the first). |
| `Goto-Client-Proxy-TLS-Took` | The TLS handshake with the proxy, if it is an `https` one. |
| `Goto-Client-Tunnel-Took` | The exchange that asks the proxy for the tunnel, from the connection to the proxy being ready to its answer. The proxy connects to the target before it answers, so this has the lookup and the connecting of the proxy in it. |
| `Goto-Client-TLS-Took` | The TLS handshake with the target. |
| `Goto-Client-Response-Head-Took` | From the request being written to the head of the response being received. This has the time that the target (and a proxy that tunnels to it) took. |
| `Goto-Client-Body-Took` | Reading the body of the response. |
| `Goto-Client-Total-Took` | From the start of the call to the end of the body. |

The CONNECT proxy tells the time that it spent in its answer to the CONNECT request in headers of its own (`Goto-Connect-Proxy-*`, see the [proxy README](../proxy/tcp/README.md#http-connect-proxy)), which it sends to any client that opens a tunnel, `curl -x` included. The client passes them on in its response, under the same names and in the same form as the headers above: `Goto-Connect-Proxy-Took` (all of it), `Goto-Connect-Proxy-DNS-Took` (resolving the name of the target), `Goto-Connect-Proxy-Dial-Took` (connecting to it), and `Goto-Connect-Proxy-Delay` (the delay that the proxy added, if it did). The lookup and the connecting add up to a part of `Goto-Connect-Proxy-Took`, which is a part of `Goto-Client-Tunnel-Took`, and the phases add up to a part of `Goto-Client-Total-Took`: what is left is the time that the client spent on its own.

So a call through a proxy shows the time of each of the three: the client in the `Goto-Client-*` headers, the proxy in the `Goto-Connect-Proxy-*` ones (and, for the requests that went through the tunnel, in the `Goto-Connect-Proxy-*` headers of the results, which are the ones of each request), and a `goto` target in the `Goto-Took` header of the results. `connectprobe` (see `tools/connectprobe`) measures the same phases from outside of the client.

```
Goto-Client-DNS-Took: ["104.2ms","-"]
Goto-Client-TCP-Took: ["0.6ms","-"]
Goto-Client-Tunnel-Took: ["88.1ms","-"]
Goto-Client-Response-Head-Took: ["6.9ms","2.1ms"]
Goto-Client-Body-Took: ["0.1ms","0.1ms"]
Goto-Client-Total-Took: ["200ms","2.3ms"]
Goto-Connect-Proxy-Took: ["84.3ms","-"]
Goto-Connect-Proxy-DNS-Took: ["83.9ms","-"]
Goto-Connect-Proxy-Dial-Took: ["0.3ms","-"]
```

Only calls that got a response are reported: a call that failed fails the request, and the error is its report.

The phases are the ones of the calls, so the time that the client spends before its first call (reading the call, setting up its transport and TLS settings) is in none of them: it is the `Goto-Took` of the response of the API, less the `Goto-Client-Total-Took` of the calls. Calls over plain HTTP/2 (`h2` without `tls`) have no `dns` and `tcp` times, as the HTTP/2 transport dials without the context of the call.

###### <small> [Back to TOC](#toc) </small>

See [Client JSON Schemas](../../docs/client-api-json-schemas.md)

See [Client APIs and Results Examples](../../docs/client-api-examples.md)

See [Client GRPC Examples](../../docs/grpc-client-examples.md)
