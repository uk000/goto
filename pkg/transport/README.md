# Transport Package

This package provides HTTP transport and connection management functionality.

## REST APIs

This package does not expose REST APIs. It provides internal HTTP client and transport functionality used by other packages for making outbound requests: the [client](../client/README.md) targets and its one-off `/client/http/invoke` call, [jobs](../job/README.md) HTTP tasks, the registry and peer clients, and the proxies.

## What it provides

- `CreateHTTPClient` / `CreateDefaultHTTPClient`: build the client transport (HTTP/1.1, or HTTP/2 over TLS or as h2c) for a target or a service call, with intercepts that count the open connections, record the TLS peer certificate and the negotiated ALPN of each connection, and support sending requests through an HTTP proxy (`CONNECT`, also for plain `http` targets; see [Calling via a Proxy](../client/README.md#calling-via-a-proxy)). The target spec fields that configure it (connection, idle and request timeouts, `tls`, `verifyTLS`, `noSNI`, `tlsVersion`, `clientCert`, `alpn`, `autoUpgrade`) are described in the [Client Target JSON Schema](../../docs/client-api-json-schemas.md). `CreateDefaultHTTPClient` is the variant for goto's own service calls: 10 minute request timeout, 3 minute connect and idle timeouts, and HTTP/2 auto-upgrade.
- Connection tracking: dialed connections are wrapped so that each client knows how many connections are open (`GetOpenConnectionCount`) and can announce new connections. Note that the connections of HTTP/2 clients are currently not counted.
- Peer certificate info: for TLS calls the subject, DNS names, URIs and issuer of the server certificate and the negotiated SNI/ALPN are kept per remote address and surface as `serverCert`/`peerCertInfo` in the one-off call response and in the client invocation results.
- Call timings (`calltimings.go`): times the phases of a one-off client call (DNS, TCP connect, TLS handshake with the proxy, CONNECT exchange, TLS handshake with the target, response head, body, total) and reports them as `Goto-Client-*-Took` headers, see [Timings of a One-off Call](../client/README.md#timings-of-a-one-off-call).
- gRPC clients (client targets with protocol `grpc`/`grpcs`) use the same client transport type, holding a gRPC connection instead of an HTTP client.
