# Routing API

This document describes the HTTP API provided by the router package. The router exposes a small admin API under the `/routing` path to manage runtime routes and inspect the port router state.

## APIs
|METHOD|URI|Description|
|---|---|---|
| POST       | /routing/add    | Add a new `Route` (see JSON example below).  |
| POST       | /routing/clear    | Clear all routes for the current port.  |
| GET       | /routing    | Get routes configured for the current port.  |


### Route JSON Schema

|Field|Data Type|Default Value|Description|
|---|---|---|---|
| label | string || Name for this route |
| from | || |
| from.port | int || Port on which inbound request is to be rerouted |
| from.uriPrefix | string || Prefix to match to filter inbound requests for rerouting |
| to | || |
| to.url | string || upstream host (http/https will be prepended) |
| to.uriPrefix | string | default uses from.uriPrefix | upstream URI prefix |
| to.authority | string || optional authority |
| to.requestHeaders | || |
| to.requestHeaders.add | map `string: string` || Headers to add to the upstream request |
| to.requestHeaders.remove | map `string: string` || Headers to remove from the inbound request before forwarding to upstream |
| to.responseHeaders |  || |
| to.responseHeaders.add | map `string: string` || Headers to add to the final response |
| to.responseHeaders.remove | map `string: string` || Headers to remove from the upstream response before sending it to downstream |
| to.http2 | boolean | false | whether the outbound request should use http/2 |
| to.tls | boolean | false | whether the outbound request should use TLS |
| logBody | boolean | false | if true, request/response bodies are logged |

## Route JSON Example

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
  "logBody": false
}
```

## Behavior notes

- Matching: routes are matched by the `From.URIPrefix` and the configured port. The router builds a regexp matcher when a route is added. If `From.URIPrefix` is `/` or empty, the route becomes a root match.
- URL normalization: if `To.URL` does not start with `http`, the router will prefix `http://` or `https://` depending on the `To.IsTLS` flag.
- Header overrides: `requestHeaders` and `responseHeaders` support `add` and `remove` maps. Keys are treated case-insensitively when removing.
- Correlation: each routed request is assigned a correlation id in `X-Correlation-ID` propagated downstream and used to map active routes for responses.

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

Clear routes on the current port:

```bash
curl -X POST http://localhost:7000/routing/clear
```
