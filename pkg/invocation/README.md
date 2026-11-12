# Invocation Package

This package provides internal invocation tracking and management functionality: it is the engine behind [client targets](../client/README.md), [jobs](../job/README.md) with an HTTP task, and the one-off client calls.

## REST APIs

This package does not expose direct REST APIs. It provides internal functionality for tracking and managing invocations across other packages (client, registry, jobs, etc.). Its configuration and results are exposed through the client APIs: the target spec is described in the [Client Target JSON Schema](../../docs/client-api-json-schemas.md), and the `InvocationStatus`/`InvocationResult` objects reported by `/client/targets/active`, `/client/results/invocations` and the job results are described there too.

## How an invocation runs

1. An invocation is registered for a target spec (`RegisterInvocation`) and gets the next invocation counter (the key of `/client/results/invocations`). The counter is reset to 0 by `POST /client/targets/stop/all`.
2. When started, the invocation waits for the target's `initialDelay`, sends `warmupCount` warmup requests (not reported in the results), and then starts `replicas` runners. Each runner takes one request at a time, waits the target's `delay` before it, and a new request is handed out as soon as a runner is done, until `replicas * requestCount` requests were assigned. A stop request ends the invocation after the in-flight requests.
3. Every request is sent with these headers, in addition to the target's own `headers` (headers with empty values are not sent): `Goto-Target-ID` and `Goto-Target-URL` (always), the request ID in header `Goto-Request-Id` and in query param `x-request-id` when `sendID` is set (or as configured by `requestId`), and `Goto-Retry-Count` on retries. The `targetID` of a request, `<target>[<runner>][<request number>]`, is also what is reported in the result.
4. A request is retried up to `retries` times (waiting `retryDelay` between attempts) when it fails with a connection error or returns one of the `retriableStatusCodes`. With `fallback`, retry attempts go to the target's `burls` in order. With `ab`, each request is additionally sent to all `burls` (counted as `abCount`). With `random`, each request goes to a random URL among the URL and the `burls`.
5. The response is checked against the target's `assertions` (the first assertion that matches sets `validAssertionIndex`; if none matches the failures are recorded in the result's `errors`), recorded in the invocation status (`successCount` counts every request that got a response, `failureCount` counts requests that failed on the client side) and published to the result sinks (the results aggregated by the client results APIs, and the registry locker if enabled).
6. When all requests are done the invocation is marked `closed` and removed from the active invocations, and its client transport is closed (HTTP clients are cached per target name and re-created by the next invocation).

The package also keeps the state of the active invocations that `/client/targets/active` reports, and a background monitor of the cached HTTP clients.

Current limitations to be aware of: a target with protocol `tcp` is accepted but cannot be invoked; the request ID that is sent with `sendID` is the target name (the unique per-request ID is the `targetID` of the result); `collectResponse` payloads are collected but not shown in the results (only `payloadSize` is); `keepOpen` is used for gRPC streaming only.
