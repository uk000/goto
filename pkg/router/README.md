# Routing API

This document describes the HTTP API provided by the router package. The router exposes a small admin API under the `/routing` path to manage runtime routes and inspect the port router state.

## APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another (for `clear` and `GET`; `add` takes the port from the route's `from.port`). There is no `/server/routing` alias.</small>

|METHOD|URI|Description|
|---|---|---|
| POST       | /routing/add    | Add a new `Route` (see JSON example below) to the router of its `from.port`. A route with the same `label` replaces the previous one. Replies `200` with the route as JSON (with the defaults filled in, e.g. `http://` added to `to.url`). **An invalid route or unparseable body also replies `200`, with an empty body**: check the body, or `GET /routing`. A `from.uriPrefix` that is not a valid regex-like pattern (e.g. `/b(`) currently resets the connection.  |
| POST       | /routing/clear    | Clear all routes (and recorded traffic) for the current port.  |
| POST       | /routing/clear/all    | Reset the routers of all ports and the correlation id counter.  |
| GET       | /routing    | Get routes and recorded traffic of the current port.  |
| GET       | /routing/all    | Get routes and recorded traffic of all ports (map of port to router).  |


### Route JSON Schema

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| label | string || Name for this route. Routes of a port are keyed by label |
| from | || |
| from.port | int || **Required (> 0).** Port on which inbound request is to be rerouted |
| from.uriPrefix | string | `/` | Prefix to match to filter inbound requests for rerouting. Matched case-insensitively against the full request URI; `{var}` segments and a trailing `*` are accepted. Empty or `/` matches every request on the port |
| to | || |
| to.url | string || **Required.** upstream host (`http://`, or `https://` if `to.tls`, is prepended when the URL does not start with `http`) |
| to.uriPrefix | string | default uses from.uriPrefix | upstream URI prefix: the matched `from.uriPrefix` is replaced with it in the forwarded URI (query string kept), e.g. `/api/v1/x` from `/api/v1` to `/` becomes `/x`. |
| to.authority | string || optional authority (sent as the `Host` of the upstream request) |
| to.requestHeaders | || |
| to.requestHeaders.add | map `string: string` || Headers to add to the upstream request (replacing a header of the same name) |
| to.requestHeaders.remove | map `string: string` || Headers to remove from the inbound request before forwarding to upstream. The map value is optional: if given, the header is removed only when its (first) value equals it; if empty, the header is always removed |
| to.responseHeaders |  || |
| to.responseHeaders.add | map `string: string` || Headers to add to the final response |
| to.responseHeaders.remove | map `string: string` || Headers to remove from the upstream response before sending it to downstream (same value rule as above) |
| to.http2 | boolean | false | whether the outbound request should use http/2 |
| to.tls | boolean | false | whether the outbound request should use TLS |
| to.noSNI | boolean | false | do not send SNI on the outbound TLS connection |
| logBody | boolean | false | if true, request/response bodies are logged and their sizes recorded in the traffic |
| logHops | boolean | false | if true, the response body is replaced by a JSON document describing the hop (`{"<listener>": {"self": {...echo of the inbound request...}, "upstream": {"headers": ..., "body": ...}}}`), and `Route-Request-At`, `Route-Response-At` and `Route-Took` headers are added |
| processReq | boolean | false | if true, the routed request also passes through goto's request middleware (request filters, header/URI tracking, timeouts, ...) |
| processResp | boolean | false | if true, the routed response also passes through goto's response middleware (payload, status, triggers, ...) |

## Route JSON Example

Minimal:
```
{"label": "r1", "from": {"port": 7000, "uriPrefix": "/rt"}, "to": {"url": "localhost:8080"}}
```

Rich:
```
{
  "label": "string",
  "from": {
    "port": 7000,
    "uriPrefix": "/api/v1"
  },
  "to": {
    "url": "upstream.example:8080",
    "uriPrefix": "/",
    "authority": "optional-host:port",
    "requestHeaders": {
      "add": { "X-Foo": "bar" },
      "remove": { "Authorization": "" }
    },
    "responseHeaders": {
      "add": { "X-Proxy": "true" },
      "remove": { "Set-Cookie": "" }
    },
    "http2": false,
    "tls": false
  },
  "logBody": false,
  "logHops": false,
  "processReq": false,
  "processResp": false
}
```

## Behavior notes

- Matching: routes are matched by the `From.URIPrefix` and the configured port. The router builds a regexp matcher when a route is added. If `From.URIPrefix` is `/` or empty, the route becomes a root match. When several routes of a port match a request, the one with the longest prefix wins, and among equal prefixes the one with the alphabetically first label.
- **Routing is evaluated before goto's own APIs.** A route is matched for every request on its port, including admin calls: with a root (`/`) route, calls such as `/routing`, `/routing/clear` and `/echo` on that port are forwarded upstream too, so the route cannot be removed through that port's own admin API. Calling `/port={port}/routing/clear` on another listener of the same goto does **not** help either, because that request is evaluated against the root route of `{port}` and is forwarded upstream as well. The way out is to restart goto, so avoid root routes on a port you still need to administer.
- **No loop protection.** Nothing marks a request as already routed and there is no hop limit. A route whose upstream is a listener of the same goto that matches the same route again (for example a root route to another local port combined with a `/port={port}/...` request) forwards the request to itself repeatedly until the connections are exhausted (bug report REQ-23).
- URL normalization: if `To.URL` does not start with `http`, the router will prefix `http://` or `https://` depending on the `To.IsTLS` flag.
- Header overrides: `requestHeaders` and `responseHeaders` support `add` and `remove` maps. Keys are treated case-insensitively when removing.
- Correlation: each routed request is assigned a correlation id (a process-wide counter starting at 1) in the `X-Correlation-ID` request header propagated upstream and used to map active routes for responses.
- Traffic: every routed request is recorded in the port router's `traffic` map (keyed by correlation id) with the route, listener, timings and headers. It is never pruned: it grows until `clear`.

## Examples

Add a route (curl):

```bash
curl -X POST http://localhost:7000/routing/add \
  -H "Content-Type: application/json" \
  -d '{
    "label": "example",
    "from": { "port": 7000, "uriPrefix": "/api/v1" },
    "to": { "url": "upstream.local:8080", "uriPrefix": "/" },
    "logBody": false
  }'
```

Get routes for a port:

```bash
curl http://localhost:7000/routing
```

Get routes of all ports:

```bash
curl http://localhost:7000/routing/all
```

Clear routes on the current port (or on another one):

```bash
curl -X POST http://localhost:7000/routing/clear
curl -X POST http://localhost:7000/port=7001/routing/clear
curl -X POST http://localhost:7000/routing/clear/all
```

Sample `GET /routing` output after one routed request:

```
{
  "id": "PortRouter-7000",
  "port": 7000,
  "routes": {
    "example": {
      "label": "example",
      "from": {"port": 7000, "uriPrefix": "/api/v1"},
      "to": {"url": "http://upstream.local:8080", "uriPrefix": "/", "authority": "", "requestHeaders": null,
             "responseHeaders": null, "http2": false, "tls": false, "noSNI": false},
      "logHops": false, "logBody": false, "processReq": false, "processResp": false
    }
  },
  "traffic": {
    "1": { "label": "example", "listener": "...", "requestURI": "/api/v1/x", "requestAt": "...", "took": 1234567, ... }
  }
}
```
