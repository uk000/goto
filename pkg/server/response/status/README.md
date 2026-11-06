# Response Status
This feature allows setting a forced response status for all requests except bypass URIs. Server also tracks the number of status requests received (via /status URI) and the number of responses sent per status code.

###### <small>* All `/response/...` APIs below are also served under the `/server/response/...` prefix (e.g. `/server/response/status/counts`).</small>

#### Response Status APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST | /response<br/>/status/set/`{status}`     | Set a forced response status that all non-proxied and non-management requests will be responded with. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. Syntax of status param is `<status1,status2,...>:<times>`, where `times` is the number of times the given forced statuses will be used, after which the requests will be served the normal response status. `status` `0` removes the forced status. A value that is not a status list (e.g. `abc`) gets `400 Invalid Status`. **Codes are not range-checked**: a code outside 100-999 (e.g. `1000`) is accepted with `200`, and from then on every affected response fails with a connection reset (see bug report); only use real status codes. |
| POST | /response<br/>/status/set/`{status}`?uri=`{uri}`     | Set a forced response status for a specific URL (exact match, case-insensitive, compared against the full request URI including the query string). All non-proxied requests for the URI will be responded with the given `status`. Status syntax is `<status1,status2,...>:<times>`. |
| POST | /response<br/>/status/set/`{status}`?uriPrefix=`{uriPrefix}`     | Same as above, for all URIs that start with the given prefix. |
| POST |	/response<br/>/status/clear            | Remove all currently configured forced response statuses (and the flipflop state) of the port, so that all subsequent calls will receive their original deemed response |
| POST |	/response<br/>/status/clear?uri=`{uri}` | Remove the forced statuses that were set for the given URI (as exact URI or as prefix) |
| POST | /response<br/>/status/configure     | Add a forced status with a rich match (URI exact/prefix/regex, headers, methods), see [Configure API](#status-configure-api) |
| POST | /response<br/>/status/counts/clear     | Clear counts tracked for response statuses |
| GET       |	/response<br/>/status/counts/`{status}`  | Get request and response counts for a given status |
| GET       |	/response<br/>/status/counts           | Get request counts for all response statuses so far |
| GET       |	/response<br/>/status/flipflop         | Get the current flipflop state (per request id) of the port |
| GET       |	/response/status                  | Get the currently configured forced response statuses for all ports, and the flipflop state of all ports |

Notes:
* `set` and `clear` are `POST` only (the earlier doc also listed `PUT`).
* A forced status applies to every response of a non-admin request (a URI/`uriPrefix` status applies to matching requests only), including requests for `/status/{status}`: the forced status wins over the requested one.
* When several statuses are configured, the ones with a URI/header/method match are tried before the ones that match all requests (regardless of the order they were set in); among those in a group, the first one that was set and still has remaining count is used. A used-up status is skipped. A status set to `0` for specific requests shields them from the status that is set for all requests.
* Responses carry `Goto-Forced-Status` and (when a count was given) `Goto-Forced-Status-Remaining`.
* `GET .../counts` and `.../counts/{status}` return `204` with `{}` if the port has not seen any status request yet.

<details>
<summary>Response Status API Examples</summary>

```
curl -X POST localhost:8080/response/status/counts/clear

curl -X POST localhost:8080/response/status/clear

curl -X POST localhost:8080/response/status/set/502

# 502 or 503 (random) for the next 5 requests
curl -X POST localhost:8080/response/status/set/502,503:5

# only for one URI, or for a URI prefix
curl -X POST 'localhost:8080/response/status/set/418?uri=/foo'
curl -X POST 'localhost:8080/response/status/set/503?uriPrefix=/api'
curl -X POST 'localhost:8080/response/status/clear?uri=/foo'

# remove the forced status
curl -X POST localhost:8080/response/status/set/0

curl localhost:8080/response/status

curl localhost:8080/response/status/counts

curl localhost:8080/response/status/counts/502

curl localhost:8080/response/status/flipflop
```

</details>

<details>
<summary>Response Status Tracking Result Example</summary>

<p>

`GET /response/status/counts`: `countsByRequestedStatus` counts the statuses asked for via `/status/{status}`; `countsByResponseStatus` counts the statuses that were actually sent for non-admin requests. (Earlier docs named the second field `countsByReportedStatus`; the code uses `countsByResponseStatus`.)

```
{
  "port": "8080",
  "countsByRequestedStatus": {
    "418": 20
  },
  "countsByResponseStatus": {
    "200": 15,
    "202": 4,
    "208": 5,
    "418": 20
  }
}
```

`GET /response/status/counts/418` returns `{"port":"8080","status":418,"requestCount":20,"responseCount":20}`.

</p>
</details>

<details>
<summary>Forced Status State Result Example (<code>GET /response/status</code>)</summary>

<p>

```
{
  "PortStatuses": {
    "8080": [
      {
        "port": 8080,
        "statuses": [502, 503],
        "times": 5,
        "lastStatusIndex": 0,
        "lastStatus": 0,
        "statusCount": 0,
        "match": {
          "uri": {"prefix": "", "exact": "/foo", "regex": "", "not": false},
          "headers": [],
          "methods": null
        }
      }
    ]
  },
  "PortFlipFlopConfigs": {}
}
```

</p>
</details>

#### <a name="status-configure-api"></a>Status Configure API

`POST /response/status/configure` adds a forced-status config for a port using a JSON body. Unlike `set`, it can match on URI regex, negative URI match, request headers (value or presence/absence) and HTTP methods.

|Field|Type|Default|Meaning|
|---|---|---|---|
| port | int | the port the request is sent to | Port the config applies to |
| statuses | []int | none, **required** | One or more status codes; a random one is picked per request. `0` is treated as `200`. Empty list gets `400 no status`; negative gets `400 invalid status` (codes > 999 are not rejected, see above) |
| times | int | `-1` (forever) | Number of requests the config applies to. `0` is treated as `-1`; values below `-1` get `400 invalid status times` |
| match.uri.exact | string | "" | Full request URI (path + query, lower-cased) must equal this |
| match.uri.prefix | string | "" | Request URI must start with this (lower-cased) |
| match.uri.regex | string | "" | Case-insensitive regex that must match the request URI (invalid regex gets `400`) |
| match.uri.not | bool | false | Negates the exact/prefix/regex conditions |
| match.headers[].header | string | | Header name. **Must be given in lower case**: names are compared against lower-cased request header names, so `X-Foo` never matches |
| match.headers[].value | string | "" | Header value, compared case-insensitively. Empty means any value |
| match.headers[].present | bool | true | `false` means the header must be absent |
| match.methods | []string | all | HTTP methods the config applies to (case-insensitive) |

All of `uri`, `headers` and `methods` that are given must match (AND). The response is `200` with the stored config as JSON (including `lastStatusIndex`, `lastStatus`, `statusCount` counters), or `400` with `Failed to parse status config with error: ...`.

Minimal:
```
curl -X POST localhost:8080/response/status/configure -d '{"statuses":[503]}'
```

Rich:
```
curl -X POST localhost:8080/response/status/configure -d '
{
  "port": 8080,
  "statuses": [403, 429],
  "times": 10,
  "match": {
    "uri": {"prefix": "/api", "not": false},
    "headers": [
      {"header": "x-foo", "present": false},
      {"header": "x-bar", "value": "foo"}
    ],
    "methods": ["GET", "POST"]
  }
}'
```


# Status
The URI `/status/`{status}`` allows clients to ask for a specific status as response code. The given status is reported back, except when forced status is configured in which case the forced status is sent as response.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status/`{status}` | This call either receives the given status, or the forced response status if one is set. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. The response carries `Goto-Requested-Status`. A non-numeric value responds `200`; a code outside 100-999 resets the connection. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status/`{status:count}` | When the status param is passed in the format `<code>:<count>`, the requested response code is returned for `count` number of subsequent calls (starting from the current one), after which `200` is returned (with a `Goto-Status-Flip: <code>` header) until the state is cleared via `POST /response/status/clear`. The state is kept per port and is shared by all clients (the request id is always empty, see below). |
| GET |	/response/status/flipflop | Reports the current flipflop state: `{"": {"times": 2, "statusCount": -1, "lastStatusIndex": 0, "lastStatus": 503}}`. |
| POST |	/response/status/clear | Clears the current state of all stateful statuses recorded so far (and the forced statuses). |

**Not working in this version** (documented earlier, but the routes can never match, see bug report): `/status={status}`, `/status={status}/delay={delay}`, `/status={status:count}/flipflop`, `/status={status:count}/delay={delay}/flipflop` and the per-client `?x-request-id=` state. Requests to these URLs fall through to the catch-all (`200` echo). Also `/status/{status}/delay={delay}` is `404`, and `/status/flipflop` and `/status/clear` (without `/response`) are treated as status `{status}` values that don't parse, so they just return `200`. Use the delay APIs ([`/response/delay`](../delay/README.md) or [`/request/uri/set/delay`](../../request/README.md)) and `/response/status/flipflop|clear` instead.

Known issue: with a list of statuses and a count, e.g. `/status/200,201,202:5`, a later request that uses a *shorter* list under the same (shared) request id panics while holding the port's status lock, which blocks every later non-admin request on the port until restart (see bug report REQ-01). Do not mix list lengths.

<br/>
<details>
<summary>Status API Example</summary>

```
curl -I  localhost:8080/status/418

curl -I  localhost:8080/status/501,502,503

curl -I localhost:8080/status/503:2

curl localhost:8080/response/status/flipflop

curl -XPOST localhost:8080/response/status/clear
```
</details>
