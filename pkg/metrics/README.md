### Prometheus Metrics

`Goto` exposes custom server metrics as well as golang VM metrics in prometheus format. All the counters are kept in memory, for the life of the process, and they are counted asynchronously (a counter may trail the request that it counts by a few milliseconds).
<details>
<summary>List of Prometheus metrics exposed by goto</summary>

- `goto_requests_by_type` (vector): Number of requests by type (dimension: requestType). The types are `all` (every request), `admin` (admin API calls), `metrics` (calls to `/metrics` and `/stats`), `catchAll` (requests served by the [catch-all](../server/README.md#catch-all)), `echo`, `wsecho`, `status`, `delay`, `timeout`, `tunnel`, `readinessProbe` and `livenessProbe`.
- `goto_requests_by_headers` (vector): Number of requests by request headers (dimension: requestHeader). Only the headers that are tracked with the [header tracking](../server/request/README.md) APIs are counted.
- `goto_requests_by_header_values` (vector): Number of requests by request headers values (dimensions: requestHeader, headerValue)
- `goto_requests_by_uris` (vector): Number of requests by URIs (dimension: uri). The URI is the request URI as received, with its query string.
- `goto_requests_by_headers_and_uris` (vector): Number of requests by request headers and URIs (dimensions: requestHeader, uri)
- `goto_requests_by_port` (vector): Number of requests by ports (dimension: port)
- `goto_requests_by_port_and_uris` (vector): Number of requests by ports and uris (dimensions: port, uri)
- `goto_requests_by_port_and_headers` (vector): Number of requests by ports and request headers (dimensions: port, requestHeader)
- `goto_requests_by_port_and_header_values` (vector): Number of requests by ports and request header values (dimensions: port, requestHeader, headerValue)
- `goto_requests_by_protocol` (vector): Number of requests by protocol (dimension: protocol). The protocol is one of `HTTP/1.1`, `H2C`, `HTTPS`, `HTTP/2` or `GRPC`. Calls to the admin root paths (`/server`, `/probes`, `/log`, `/metrics`, `/request`, `/response`, `/registry`, `/client`, `/proxy`, `/tcp`, `/grpc`, `/job`, `/scripts`, `/tls`, `/routing`, ...) are not counted; `/stats`, `/goto`, `/hooks` and `/ui` are not among those root paths, so calls to them are counted like traffic (checked for `/stats` and `/goto/version`).
- `goto_requests_by_protocol_and_uris` (vector): Number of requests by protocol and URIs (dimensions: protocol, uri)
- `goto_invocations_by_targets` (vector): Number of client invocations by target (dimension: target)
- `goto_failed_invocations_by_targets` (vector): Number of failed invocations by target (dimension: target)
- `goto_requests_by_client` (vector): Number of server requests by client (dimension: client). The client is the IP address of the caller.
- `goto_proxied_requests` (vector): Number of proxied requests (dimension: proxyTarget)
- `goto_triggers` (vector): Number of triggered requests (dimension: triggerTarget)
- `goto_conn_counts` (vector): Number of connections by type (dimension: connType). The server counts the type `tcp`.
- `goto_tcp_conn_counts` (vector): Number of TCP connections by type (dimension: tcpType). The type is the mode that served the connection: `Response`, `Echo`, `Stream`, `Conversation`, `Payload` (payload validation), `SilentLife` or `CloseAtFirstByte` (see the [TCP Server](../server/tcp/README.md)).
- `goto_total_conns_by_targets` (vector): Total client connections by targets (dimension: target)
- `goto_active_conn_counts_by_targets` (gauge): Number of active client connections by targets (dimension: target)

A metric has no sample until the first event that it counts, so a fresh instance shows only the ones that have been hit.

> &#x1F4DD; <small><i>Known issues: the per-request counters of the `metrics` middleware (the `all`, `admin` and `metrics` values of `goto_requests_by_type`) are not recorded for HTTP requests on this version, because the middleware is not invoked for them; the other types, and every other metric, are. Every distinct URI (with its query string) adds a label value that is never removed until `/metrics/clear`, so traffic with many distinct URIs grows the memory use and the size of the `/metrics` response.</i></small>

</details>
<br/>

`Goto` tracks request counts by various dimensions for validation usage, exposed via API `/stats`:

<details>
<summary>List of server stats tracked by goto</summary>

- `requestCountsByHeaders`
- `requestCountsByURIs`
- `requestCountsByURIsAndHeaders`
- `requestCountsByURIsAndHeaderValues`
- `requestCountsByPortAndURIs`
- `requestCountsByPortAndHeaders`
- `requestCountsByPortAndHeaderValues`
- `requestCountsByURIsAndProtocol`

The response of `/stats` is one JSON object with these fields, all of them present (empty objects when nothing was counted):

|Field|Data Type|Description|
|---|---|---|
| requestCountsByHeaders | map[header]count | Requests that carried each tracked header |
| requestCountsByURIs | map[uri]count | Requests by request URI, with the query string, admin calls included |
| requestCountsByURIsAndHeaders | map[uri]map[header]count | Requests by URI and tracked header |
| requestCountsByURIsAndHeaderValues | map[uri]map[header]map[value]count | Requests by URI, tracked header and header value |
| requestCountsByPortAndURIs | map[port]map[uri]count | Requests by listener port and URI |
| requestCountsByPortAndHeaders | map[port]map[header]count | Requests by port and tracked header |
| requestCountsByPortAndHeaderValues | map[port]map[header]map[value]count | Requests by port, tracked header and header value |
| requestCountsByURIsAndProtocol | map[uri]map[protocol]count | Requests by URI and protocol (`HTTP/1.1`, `H2C`, `HTTPS`, `HTTP/2`, `GRPC`); calls to the admin root paths are not counted (see `goto_requests_by_protocol`) |

Header counts come from the [request header tracking](../server/request/README.md) APIs: only the headers that you asked to track are counted, per request, with the URI that the request was made to.

```
$ curl localhost:8080/stats
{
  "requestCountsByHeaders": {"x": 1},
  "requestCountsByURIs": {"/foo": 2, "/probes": 1},
  "requestCountsByURIsAndHeaders": {"/foo": {"x": 1}},
  "requestCountsByURIsAndHeaderValues": {"/foo": {"x": {"y": 1}}},
  "requestCountsByPortAndURIs": {"8080": {"/foo": 2, "/probes": 1}},
  "requestCountsByPortAndHeaders": {"8080": {"x": 1}},
  "requestCountsByPortAndHeaderValues": {"8080": {"x": {"y": 1}}},
  "requestCountsByURIsAndProtocol": {"/foo": {"HTTP/1.1": 2}}
}
```

</details>

#### Metrics APIs
|METHOD|URI|Description|
|---|---|---|
| GET   | /metrics       | Custom metrics in prometheus format |
| GET   | /metrics/go    | Go VM metrics in prometheus format |
| POST  | /metrics/clear | Clear custom metrics. Answers `200 Metrics cleared`. The Go VM metrics are not affected. |
| GET   | /stats         | Server counts (JSON, see above) |
| POST  | /stats/clear   | Clear server counts. Answers `200 Stats cleared`. The prometheus metrics are not affected. |

- `GET` is the only method of `/metrics`, `/metrics/go` and `/stats`, and `POST` the only one of the `clear` URIs (other methods answer `404`).
- `/metrics` and `/metrics/go` answer the prometheus text exposition format (`text/plain; version=0.0.4`), which a prometheus server can scrape. `/stats` answers JSON.
- Calls to these APIs are themselves counted (by URI and port), including the `clear` calls: a `/stats/clear` is followed by a count of 1 for `/stats/clear`.
- Logging of calls to these URIs is controlled by `--metricsLogs` and `POST /log/metrics/{enable}` (see [Log](../log/README.md)).

<details>
<summary>Metrics API Examples</summary>

```
curl localhost:8080/metrics
curl localhost:8080/metrics/go
curl -X POST localhost:8080/metrics/clear
# Metrics cleared

curl localhost:8080/stats
curl -X POST localhost:8080/stats/clear
# Stats cleared

# Count requests by header: track the header x, then send traffic that carries it
curl -X POST localhost:8080/request/track/headers/add/x
curl -H 'x: y' localhost:8080/foo
curl localhost:8080/stats
```

</details>

<br/>

See [Metrics Example](../../docs/metrics-example.md)
