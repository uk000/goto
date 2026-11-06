
# Response Delay
This feature allows adding a delay to all requests except bypass URIs and proxy requests. Delay is specified as duration, e.g. 1s. 

Delay is not applied to the following requests:
- `Goto` admin calls (`/request/...`, `/response/...`, `/server/...`, `/registry/...`, etc.) and known non-traffic URIs (probes, `/health`, `/metrics`, `/version`, ...)
- Any request whose URI contains `/delay` (this includes the Delay API `/delay/{delay}` below, and also any other URI that happens to contain that text, e.g. `/foo/delay/bar`)

When a delay is applied to a request, the response carries a header `Goto-Response-Delay` with the value of the applied delay.

###### <small>* All `/response/...` APIs below are also served under the `/server/response/...` prefix.</small>

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /response<br/>/delay/set/`{delay}` | Set a delay for non-management requests (i.e. runtime traffic) of the port. `delay` is `<delay>[:<count>]` where `<delay>` is either a single duration (`500ms`, `2s`, ...) or a `low-high` range (`100ms-2s`), in which case a random duration will be picked from the given range each time. The optional `:count` limits the delay to the next `count` requests, after which the delay is removed; without a count (or with a count <= 0) it applies until cleared. A `low` greater than `high` is swapped. A delay of `0` (e.g. `0s`) clears the delay. Replies `200` with a message, or `400 Invalid delay param` (e.g. `abc`, `5` without a unit, `1s-`) leaving the existing delay unchanged. |
| PUT, POST | /response<br/>/delay/clear       | Remove currently set delay |
| GET       |	/response/delay             | Get currently set delay and remaining request count: `{"delay": [min, max], "count": N}` with durations in nanoseconds; `{"delay": null, "count": 0}` if none is set. `count` is `0` when no limit was given. |

Notes:
* The delay is per port and is applied before the request is processed by the rest of the chain, so it also delays forced status, payload, stream (on top of the stream's own chunk delays) and `/status/{status}` responses.
* URI specific delays can be configured with [`/request/uri/set/delay`](../../request/README.md#request-uri-tracking), which takes a single duration only (no range).
* The delay `:count` is consumed by every request that would be delayed, including concurrent ones, atomically.
* Note: a default/header/query payload that was set without a `?uri=` (see the [payload README](../payload/README.md)) currently makes goto serve requests through a route that bypasses the delay middleware (see bug report REQ-03); URI payloads are likewise served without the response delay.


<details>
<summary>Response Delay API Examples</summary>

```
curl -X POST localhost:8080/response/delay/clear

curl -X PUT localhost:8080/response/delay/set/1s-3s

# delay the next 5 requests by 200ms
curl -X POST localhost:8080/response/delay/set/200ms:5

curl localhost:8080/response/delay
```

Sample output:
```
$ curl -X POST localhost:8080/response/delay/set/100ms-150ms:2
Port [8080] will delay next [2] requests with delay [100ms 150ms]
$ curl localhost:8080/response/delay
{"count":2,"delay":[100000000,150000000]}
$ curl -s -D - -o /dev/null localhost:8080/foo | grep -i delay
Goto-Response-Delay: 149.361318ms
```

</details>

# Delay
The URI `/delay/{delay}` allows clients to ask for a specific delay to be applied to the current request. The delay API is not subject to the response delay that may be configured for all responses. Calling the URI as `/delay` responds with no delay, and so does the call as `/delay/0`, `/delay/0s`, etc.
When a delay is passed to this API, the response carries a header `Goto-Response-Delay` with the value of the applied delay.

> Note: For requesting a delay along with a specific status, check the `/status` API documentation above.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/delay/`{delay}` | Responds `200` after the given delay (empty body). `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. An unparseable delay (e.g. `abc`) responds `400`; `0`/`0s` responds immediately without the header. To apply delay with a specific response status code, see `/status` API above. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/sleep/`{delay}` | Alias of `/delay/{delay}` |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/response/delay/`{delay}` | Same, under the `/response` (and `/server/response`) prefix. Note that this shares the path with `/response/delay/clear` and `/response/delay/set/...`, which take precedence. |

The `delay/{delay}` form takes no `:count` (a count is ignored).

<br/>
<details>
<summary>Delay API Example</summary>

```
curl -I  localhost:8080/delay/2s
curl -v  localhost:8080/delay/100ms-2s
curl -I  localhost:8080/sleep/50ms
```
</details>
