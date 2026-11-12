# Watch Package

This package holds the websocket broadcast machinery that streams the requests received by a `goto` server, along with their responses, to connected websocket clients. It is used by the UI (`GET /ui/ws`, see the UI middleware in `pkg/server/ui`): a client that connects to `/ui/ws` gets a message for every request that `goto` serves while the socket is open.

## REST APIs

The package declares two routes of its own, `POST /watch/add/{name}?url={url}` (register a webhook watcher) and `GET /watch/ws` (open a websocket), but its middleware is **not registered** with the `goto` server, so neither route is served: a request to `/watch/...` is handled like any other request (by the catch-all echo handler) and nothing is registered. The webhook watchers are also never called. Use the UI websocket instead:

|METHOD|URI|Description|
|---|---|---|
| GET | /ui/ws | Upgrades the connection to a websocket and registers it (keyed by the client's remote address) to receive a message for each request served by `goto`. A new connection from the same remote address replaces the earlier one. |

## Websocket message format

While at least one socket is open, `goto` captures the response of every request it serves and sends a text message to each open socket after the response is generated. Each message is a YAML document (not JSON) with this structure:

```yaml
request:
  uri: /foo?x=y            # the request URI including the query string
  headers:                 # request headers
    User-Agent: [curl/8.0]
response:
  statusCode: 200
  headers:                 # response headers
    Content-Type: [text/plain]
  body: "response payload as text"
```

Notes:
- Each socket has a queue of 100 messages; messages are dropped for a socket whose queue is full.
- The server sends a websocket ping after 5 seconds of idleness, and drops a socket when a write or ping fails.
- While no socket is open, responses are not captured, so there is no overhead.
