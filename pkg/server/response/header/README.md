
# Response Headers
This feature allows adding custom response headers to all responses sent by the server.

###### <small>* All `/response/...` APIs below are also served under the `/server/response/...` prefix.</small>

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /response<br/>/headers/add<br/>/`{header}`=`{value}`  | Add a custom header to be sent with all responses. The value must be non-empty (`.../add/x=` is a `404`). Adding the same header again accumulates another value (both are sent). |
| PUT, POST | /response<br/>/headers/remove/`{header}`       | Mark a header to be removed from responses: the `Request-<header>` echo headers that goto copies from the request (see notes) are skipped, and the header is excluded from goto's logged/copied headers. |
| PUT, POST |	/response<br/>/headers/clear                 | Remove all configured custom response headers (added and removed lists) |
| GET       |	/response/headers                       | Get list of configured custom response headers, as plain text: `+[name:value]` for added headers and `-[name]` for removed ones (names of removed headers are lower-cased); empty if none. |

Replies: `200` with a plain-text message (e.g. `Port [8080] Will add header [X-Resp : 1] to responses`); `400` for an invalid header name.

Notes (current behaviour):
* The configured headers are applied by a middleware after the request has been processed, and only when the response headers have not already been sent and the request is not an admin, tunnel or gRPC request. In the current build the Goto headers are written before the middleware chain runs, so the **headers added here are not seen on normal traffic responses** (`/foo`, `/status/200`, ...) and a header marked for removal is not stripped from them (see bug report REQ-13); they are only applied to the response of `GET /response/headers` itself. This section documents the intended contract; verify with `curl -i` before relying on it.
* `remove` only affects the `Request-*` echo headers goto adds to responses and goto's own header copying; it cannot strip headers set by other handlers.

<details>
<summary>Response Headers API Examples</summary>

```
curl -X POST localhost:8080/response/headers/clear

curl -X POST localhost:8080/response/headers/add/x=x1

curl -X POST localhost:8080/response/headers/remove/x

curl localhost:8080/response/headers
```

Sample output after `add/X-Resp=1`, `add/X-Resp=2` and `remove/Request-User-Agent`:
```
$ curl localhost:8080/response/headers
+[X-Resp:1] +[X-Resp:2] -[request-user-agent] 
```

</details>
