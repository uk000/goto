
# Goto Client: Targets and Traffic

As a client tool, `goto` offers the feature to configure multiple targets and send http/https/tcp/grpc traffic:

- Allows targets to be configured and invoked via REST APIs
- Configure targets to be invoked ahead of time before invocation, as well as auto-invoke targets upon configuration
- Invoke selective targets or all configured targets in batches
- Control various parameters for a target: number of concurrent, total number of requests, minimum wait time after each replica set invocation per target, various timeouts, etc
- Headers can be set to track results for target invocations, and APIs make those results available for consumption as JSON output.
- Retry requests for specific response codes, and option to use a fallback URL for retries
- Make simultaneous calls to two URLs to perform an A-B comparison of responses. In AB mode, the same request ID (enabled via sendID flag) are used for both A and B calls, but with a suffix `-B` used for B calls. This allows tracking the A and B calls in logs.
- Have client invoke a random URL for each request from a set of URLs
- Generate hybrid traffic that includes HTTP/S, H2, TCP and GRPC requests.

The invocation results get accumulated across multiple invocations until cleared explicitly. Various results APIs can be used to read the accumulated results. Clearing of all results resets the invocation counter too, causing the next invocation to start at counter 1 again. When a peer is connected to a registry instance, it stores all its invocation results in a registry locker. The peer publishes its invocation results to the registry at an interval of 3-5 seconds depending on the flow of results. See Registry APIs for detail on how to query results accumulated from multiple peers.

In addition to keeping the results in the `goto` client instance, those are also stored in a locker on the registry instance if enabled. (See `--locker` command arg). Various events are added to the peer timeline related to target invocations it performs, which are also reported to the registry. These events can be seen in the event timeline on the peer instance as well as its event timeline from the registry.

Client sends header `From-Goto-Host` to pass its identity to the server.


# <a name="client-apis"></a>
### Client APIs
|METHOD|URI|Description|
|---|---|---|
| GET, POST, PUT, OPTIONS | /client/http/invoke | Invoke a single HTTP call using a `CallSpec` definition (see schema example below). |
| POST      | /client/targets/add                   | Add a target for invocation. [See `Client Target JSON Schema` for Payload](#client-target-json-schema) |
| POST      |	/client/targets/`{targets}`/remove      | Remove given targets |
| POST      | /client/targets/`{targets}`/invoke      | Invoke given targets |
| POST      |	/client/targets/invoke/all            | Invoke all targets |
| POST      | /client/targets/`{targets}`/stop        | Stops a running target |
| POST      | /client/targets/stop/all              | Stops all running targets |
| GET       |	/client/targets                       | Get list of currently configured targets |
| GET       |	/client/targets/{target}           | Get details of given target |
| POST      |	/client/targets/clear                 | Remove all targets |
| GET       |	/client/targets/active                | Get list of currently active (running) targets |
| POST      |	/client/targets/cacert/add            | Store CA cert to use for all target invocations |
| POST      |	/client/targets/cacert/remove         | Remove stored CA cert |
| PUT, POST |	/client/track/headers/`{headers}`   | Add headers for tracking response counts per target |
| POST      | /client/track/headers/clear           | Remove all tracked headers |
| GET       |	/client/track/headers                 | Get list of tracked headers |
| PUT, POST |	/client/track/time/`{buckets}`   | Add time buckets for tracking response counts per bucket. Buckets are added as a comma-separated list of `low-high` values in millis, e.g. `0-100,101-300,301-1000` |
| POST      | /client/track/time/clear           | Remove all tracked time buckets |
| GET       |	/client/track/time                 | Get list of tracked time buckets |
| GET       |	/client/results                       | Get combined results for all invocations since last time results were cleared. |
| GET       |	/client/results/invocations           | Get invocation results broken down for each invocation that was triggered since last time results were cleared |
| POST      | /client/results/clear                 | Clear previously accumulated invocation results |
| POST      | /client/results/clear                 | Clear previously accumulated invocation results |
| POST      | /client/results<br/>/all/`{enable}`          | Enable/disable collection of cumulative results across all targets. This gives a high level overview of all traffic, but at a performance overhead. Disabled by default. |
| POST      | /client/results<br/>/invocations/`{enable}`          | Enable/disable collection of results by invocations. This gives more detailed visibility into results per invocation but has performance overhead. Disabled by default. |

### One-off Call JSON schema
```json
curl -X POST localhost:8080/client/http/invoke \
  -H 'Content-Type: application/json' \
  -d '{
    {
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
      "tlsVersion": 1.3,
      "clientCert": "SomePreLoadedClientCert",
      "alpn": ["istio", "istio-peer-exchange", "h2"],
      "payload": ["hello", "world"],
      "streamDelay": "100ms",
      "headers": {
        "add": {
          "x-test": "value"
        },
        "forward": ["x-forwarded-for"]
      }
    }'
```

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



#### Client Events
<details>
<summary>Client Events List</summary>

- `Target Added`: an invocation target was added
- `Targets Removed`: one or more invocation targets were removed
- `Targets Cleared`: all invocation targets were removed
- `Tracking Headers Added`: headers added for tracking against invocation responses
- `Tracking Headers Cleared`: all tracking headers were removed
- `Tracking Time Buckets Added`: time buckets added for tracking against invocation responses
- `Tracking Time Buckets Cleared`: all tracking time buckets were removed
- `Client CA Cert Stored`: CA cert was added for validating TLS cert presented by target
- `Client CA Cert Removed`: CA cert was removed
- `Results Cleared`: all collected invocation results were cleared
- `Target Invoked`: one or more invocation targets were invoked
- `Targets Stopped`: one or more invocation targets were stopped
- `Invocation Started`: invocation started for a target
- `Invocation Finished`: invocation finished for a target
- `Invocation Response Status`: Either first invocation response received, or the HTTP response status was different from previous response from a target during an invocation
- `Invocation Repeated Response Status`: All HTTP responses after the first response from a target where the response status code was the same as the previous, are accumulated and reported in summary. This event is sent out when the next response is found to carry a different response status code, or if all requests to a target completed for an invocation.
- `Invocation Failure`: Event reported upon first failed request, or if a request fails after previous successful request.
- `Invocation Repeated Failure`: All request failures after a failed request are accumulated and reported in summary, either when the next request succeeds or when the invocation completes.
</details>
<br/>
