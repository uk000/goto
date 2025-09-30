### Prometheus Metrics

`Goto` exposes custom server metrics as well as golang VM metrics in prometheus format.
<details>
<summary>List of Prometheus metrics exposed by goto</summary>

- `goto_requests_by_type` (vector): Number of requests by type (dimension: requestType)
- `goto_requests_by_headers` (vector): Number of requests by request headers (dimension: requestHeader)
- `goto_requests_by_header_values` (vector): Number of requests by request headers values (dimensions: requestHeader, headerValue)
- `goto_requests_by_uris` (vector): Number of requests by URIs (dimension: uri)
- `goto_requests_by_uris_and_status` (vector): Number of requests by URIs and status code (dimensions: uri, statusCode)
- `goto_requests_by_headers_and_uris` (vector): Number of requests by request headers and URIs (dimensions: requestHeader, uri)
- `goto_requests_by_headers_and_status` (vector): Number of requests by request headers and status codes (dimensions: requestHeader, statusCode)
- `goto_requests_by_port` (vector): Number of requests by ports (dimension: port)
- `goto_requests_by_port_and_uris` (vector): Number of requests by ports and uris (dimensions: port, uri)
- `goto_requests_by_port_and_headers` (vector): Number of requests by ports and request headers (dimensions: port, requestHeader)
- `goto_requests_by_port_and_header_values` (vector): Number of requests by ports and request header values (dimensions: port, requestHeader, headerValue)
- `goto_requests_by_protocol` (vector): Number of requests by protocol (dimension: protocol)
- `goto_requests_by_protocol_and_uris` (vector): Number of requests by protocol and URIs (dimensions: protocol, uri)
- `goto_invocations_by_targets` (vector): Number of client invocations by target (dimension: target)
- `goto_failed_invocations_by_targets` (vector): Number of failed invocations by target (dimension: target)
- `goto_requests_by_client`: Number of server requests by client (dimension: client)
- `goto_proxied_requests` (vector): Number of proxied requests (dimension: proxyTarget)
- `goto_triggers` (vector): Number of triggered requests (dimension: triggerTarget)
- `goto_conn_counts` (vector): Number of connections by type (dimension: connType)
- `goto_tcp_conn_counts` (vector): Number of TCP connections by type (dimension: tcpType)
- `goto_total_conns_by_targets` (vector): Total client connections by targets (dimension: target)
- `goto_active_conn_counts_by_targets` (gauge): Number of active client connections by targets (dimension: target)

</details>
<br/>

`Goto` tracks request counts by various dimensions for validation usage, exposed via API `/stats`:

<details>
<summary>List of server stats tracked by goto</summary>

- `requestCountsByHeaders`
- `requestCountsByURIs`
- `requestCountsByURIsAndStatus`
- `requestCountsByURIsAndHeaders`
- `requestCountsByURIsAndHeaderValues`
- `requestCountsByHeadersAndStatus`
- `requestCountsByHeaderValuesAndStatus`
- `requestCountsByPortAndURIs`
- `requestCountsByPortAndHeaders`
- `requestCountsByPortAndHeaderValues`
- `requestCountsByURIsAndProtocol`

</details>

#### Metrics APIs
|METHOD|URI|Description|
|---|---|---|
| GET   | /metrics       | Custom metrics in prometheus format |
| GET   | /metrics/go    | Go VM metrics in prometheus format |
| POST  | /metrics/clear | Clear custom metrics |
| GET   | /stats         | Server counts |
| POST  | /stats/clear   | Clear server counts |


<br/>

See [Metrics Example](../../docs/metrics-example.md)
