# Constants Package

This package defines constants used throughout the application, including HTTP headers and configuration keys.

## REST APIs

This package does not expose REST APIs. It provides constant definitions for:
- HTTP headers (Goto-specific headers for tunneling, proxying, tracking, etc.)
- Locker keys
- Default configuration values

## HTTP headers

The header names that `goto` sends or reads (`headers.go`). Many are described, with the conditions under which they are sent, in [HTTP Headers](../server/README.md#http-headers).

|Area|Headers|
|---|---|
| Identity of the server | `Via-Goto` (label of the listener), `Goto-Host`, `Goto-Listener` (in the echo response body), `Goto-Port`, `Goto-Protocol`, `Goto-Remote-Address` |
| TLS of the server connection | `Goto-TLS`, `Goto-mTLS`, `Goto-SNI`, `Goto-Client-Cert`, `Goto-Server-Cert` |
| Timing and status of a response | `Goto-In-At`, `Goto-Out-At`, `Goto-Took`, `Goto-Response-Status`, `Goto-Response-Delay`, `Goto-Upstream-Status` (statuses of the upstream calls of a proxy, as a list) |
| Sent by the `goto` client | `From-Goto`, `From-Goto-Host`, `Goto-Request-Id` (sent when the target is configured to send an id; the same id then also goes as the query parameter `x-request-id`), `Goto-Target-ID`, `Goto-Target-URL`, `Goto-Retry-Count`, `Goto-Client-TLS`, `Goto-Client-mTLS`, and the phase timings of the response `Goto-Client-DNS-Took`, `Goto-Client-TCP-Took`, `Goto-Client-Proxy-TLS-Took`, `Goto-Client-Tunnel-Took`, `Goto-Client-TLS-Took`, `Goto-Client-Response-Head-Took`, `Goto-Client-Body-Took`, `Goto-Client-Total-Took` (each is a JSON array with one value per call, `-` for a phase that the call did not go through) |
| Tunnels | `Goto-Tunnel` (request a tunnel), `Goto-Requested-Tunnel`, `Goto-Via-Tunnel-Count`, `Goto-Tunnel-Status`, and the per-hop response headers `Goto-Tunnel-Host-{seq}`, `Via-Goto-Tunnel-{seq}` |
| Proxy (HTTP, gRPC, MCP) | `Goto-Proxy-Host`, `Goto-Proxy-Port`, `Goto-Proxy-Upstream-Status`, `Goto-Proxy-Upstream-Took`, `Goto-Proxy-Delay`, `Goto-Proxy-Took` (trailer), `Goto-Proxy-In-At`, `Goto-Proxy-Out-At`, `Goto-Proxy-Head-Took`, `Goto-Proxy-Response-Status`, `Goto-Proxy-Request-Dropped`, `Goto-Proxy-Response-Dropped` |
| CONNECT proxy | `Goto-Connect-Proxy-Host`, `-Port`, `-Remote-Address`, `-Upstream-Status`, `-Delay`, `-Took`, `-DNS-Took`, `-Dial-Took` (each with the prefix `Goto-Connect-Proxy`) |
| Forced and URI status | `Goto-Requested-Status`, `Goto-Forced-Status`, `Goto-Forced-Status-Remaining`, `Goto-URI-Status`, `Goto-URI-Status-Remaining`, `Goto-Status-Flip` |
| Payload and streaming | `Goto-Payload-Content-Type`, `Goto-Payload-Length`, `Goto-Payload-Count`, `Goto-Chunk-Count`, `Goto-Chunk-Length`, `Goto-Chunk-Delay`, `Goto-Stream-Length`, `Goto-Stream-Duration`. The request header `ResponseContent-Type` (sic) selects the content type of a stored payload. |
| Filters | `Goto-Filtered-Request` |
| Probes | `Readiness-Request-Count`, `Readiness-Overflow-Count`, `Liveness-Request-Count`, `Liveness-Overflow-Count`, `Stopping-Readiness-Request` (prefix of the echoed request headers sent while the server is stopping) |
| AI (MCP and A2A) | `Goto-MCP-Server`, `Goto-MCP-Tool`, `Goto-MCP-Session-ID`, `Goto-MCP-Request-ID`, `Goto-MCP-Caller-ID`, `Goto-A2A-Server`, `Goto-A2A-Agent`, `Goto-A2A-Request-ID`, `Goto-Server-Timeline`, `Goto-RPC-Method` |
| Echo of the request | The request headers are echoed back with the prefix `Request-`, plus `Request-Method` and `Request-PayloadSize` |
| Standard names used by goto | `Content-Type`, `Content-Length`, `Cache-Control`, `Transfer-Encoding`, `X-Content-Type-Options`, `Proxy-Connection`, `:authority`; content types `application/json`, `application/yaml`, `text/markdown; charset=utf-8` |

`Goto-Client-Info` and `Goto-Server-Info` are defined but the server code does not use them.

## Locker keys

Keys of the registry locker (see the [registry lockers](../../docs/goto-lockers.md)): `client`, `results`, `invocations`, `default` (the default locker label), `current`, `peerLockers` and `all`.

See the source files (`headers.go`, `constants.go`) for the exact constant names.
