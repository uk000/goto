# HTTP Request Tracking Features

###### <small>* All `/request/...` APIs below are also served under the `/server/request/...` prefix (e.g. `/server/request/track/headers` is the same as `/request/track/headers`). Admin and goto-internal URIs (the `/request`, `/response`, `/server`, `/registry`, `/probes`, `/metrics`, `/health`, ... APIs, probes and the like) are never tracked, filtered, delayed or forced to a status by these features.</small>

## Request Headers Tracking

This feature allows tracking request counts by headers (and by URIs, see below). Header names are case-insensitive and are stored lower-cased. A header is counted once for every traffic request (any non-admin URI) that carries it, with a per-value breakdown. Requests to `/echo` and other goto-internal endpoints are not counted.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /request/track<br/>/headers/clear								| Remove all tracked headers and their counts |
|POST     | /request/track<br/>/headers/`{headers}`							| Add one or more (comma-separated) headers to track. Same as `/add/{headers}` |
|POST     | /request/track<br/>/headers/add/`{headers}`						| Add headers to track |
|POST     |	/request/track<br/>/headers/remove/`{headers}`					| Remove given headers from tracking |
|POST     | /request/track<br/>/headers/counts/clear/`{headers}`			| Clear counts for given tracked headers |
|POST     | /request/track<br/>/headers/counts/clear						| Clear counts for all tracked headers |
|GET      | /request/track<br/>/headers/counts								| Get all tracking data (headers and URIs, see the result example) |
|GET      | /request/track<br/>/counts											| Same as above |
|GET      | /request/track														| Same as above |
|GET      | /request/track/headers											| Get list of tracked headers (JSON array of names) |
|POST     | /request/track<br/>?uri=`{uri}`									| Track request counts of a URI |
|POST     | /request/track<br/>/headers/`{headers}`?uri=`{uri}`				| Track request counts of a URI, and the given headers for that URI |
|POST     | /request/track<br/>/`{header}`=`{value}`?uri=`{uri}`			| Track request counts of a URI, and the given header for that URI. The `{value}` must be non-empty but is otherwise ignored: header values are always counted per value |

Notes:
* Only `POST` is registered for the tracking APIs (the earlier doc listed `PUT` too, and a `/track/{header}/counts` API for a single header; neither exists).
* `GET /request/track/headers/counts`, `/request/track/counts` and `/request/track` all return the same document: the tracking data for all ports and keys. There is no per-header counts API.
* `track/headers/{headers}` with no valid header returns `400 Invalid Headers`; `add`/`remove` with no valid header return `400`. `?uri=` calls with a missing URI/header/value return `400 Invalid URI/...`.
* A tracked URI is matched as a prefix, and counted under the URI that was requested. Tracking `/trk` and `/trk2` therefore counts a request for `/trk2` twice (once per matching tracked prefix). Use distinct, non-overlapping URIs.
* The short form `POST /request/track/headers/{headers}` is the catch-all of the headers router, so a typo such as `POST /request/track/headers/counts` does not report an error: it starts tracking a header called `counts`.


<details>
<summary>Request Headers Tracking API Examples</summary>

```
curl -X POST localhost:8080/request/track/headers/clear

curl -X POST localhost:8080/request/track/headers/x,y
curl -X POST localhost:8080/request/track/headers/add/x,y

curl -X POST localhost:8080/request/track/headers/remove/x

curl -X POST localhost:8080/request/track/headers/counts/clear/x

curl -X POST localhost:8080/request/track/headers/counts/clear

curl localhost:8080/request/track/headers

# track calls to a URI
curl -X POST 'localhost:8080/request/track?uri=/foo'

# track calls to a URI along with the values of headers x and y sent to it
curl -X POST 'localhost:8080/request/track/headers/x,y?uri=/foo'
curl -X POST 'localhost:8080/request/track/x=1?uri=/foo'

# read the tracking results
curl localhost:8080/request/track
curl localhost:8080/request/track/headers/counts

# work on another port's tracking data
curl -X POST localhost:8080/port=9090/request/track/headers/x
```

</details>

<details>
<summary>Request Header Tracking Results Example</summary>
<p>

The result is keyed by port. The empty-string key (the second level) is the tracking "key" used by hooks that track under a name of their own; plain HTTP tracking always uses the `""` key. `byHeader` holds the counts of the headers tracked via `/request/track/headers/...` for all URIs; `byURI` and `byURIAndHeader` hold the counts of URIs tracked via `?uri=`.

```
$ curl localhost:8080/request/track/headers/counts

{
  "byPort": {
    "": {
      "8080": {
        "byHeader": {
          "x": {
            "requestCounts": 20,
            "requestCountsByHeaderValue": {
              "x1": 20
            }
          },
          "y": {
            "requestCounts": 20,
            "requestCountsByHeaderValue": {
              "y1": 20
            }
          }
        },
        "byURI": {
          "/foo": 20
        },
        "byURIAndHeader": {
          "/foo": {
            "x": {
              "requestCounts": 20,
              "requestCountsByHeaderValue": {
                "x1": 20
              }
            }
          }
        }
      }
    }
  }
}
```

</p>
</details>

# <a name="request-timeout-tracking"></a>
## Request Timeout Tracking
This feature allows tracking request timeouts (client connection closed before the request completed) by headers, or for all requests.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /request/timeout<br/>/track/headers/`{headers}`  | Add one or more (comma-separated) headers. Requests carrying these headers will be tracked for timeouts and reported. Replies `200`; `400` if no header is given |
|PUT, POST| /request/timeout<br/>/track/all                | Enable request timeout tracking for all requests |
|POST     |	/request/timeout<br/>/track/clear              | Clear timeout tracking configs (headers and the "all" flag) |
|GET      |	/request/timeout<br/>/status                   | Get a report of tracked request timeouts so far |
|GET      |	/request/timeout                               | Same as `/status` |

(`track/headers/all` is *not* a way of tracking all requests: it tracks a header called `all`. Use `track/all`.)

Known issue: once timeout tracking is enabled (for all requests or for a matching header), goto currently fails the tracked requests (the connection is reset, HTTP status `000`/curl error 52) because the tracking middleware requires the response writer to implement `http.CloseNotifier`. The tracking APIs themselves work.


<details>
<summary>Request Timeout Tracking API Examples</summary>

```
curl -X POST localhost:8080/request/timeout/track/headers/x,y

curl -X POST localhost:8080/request/timeout/track/all

curl -X POST localhost:8080/request/timeout/track/clear

curl localhost:8080/request/timeout/status
```

</details>

<details>
<summary>Request Timeout Status Result Example</summary>
<p>

`all` is `null` unless tracking of all requests is enabled. For each tracked header, the counts are kept per header value seen.

```
{
  "all": {
    "connectionClosed": 1,
    "requestCompleted": 0
  },
  "headers": {
    "x": {
      "x1": {
        "connectionClosed": 1,
        "requestCompleted": 5
      },
      "x2": {
        "connectionClosed": 1,
        "requestCompleted": 4
      }
    },
    "y": {
      "y1": {
        "connectionClosed": 0,
        "requestCompleted": 2
      },
      "y2": {
        "connectionClosed": 1,
        "requestCompleted": 4
      }
    }
  }
}
```

After `track/clear`: `{"all":null,"headers":{}}`. Right after `track/headers/x,y`: `{"all":{"connectionClosed":0,"requestCompleted":0},"headers":{"x":{},"y":{}}}` (if `track/all` was also called).

</p>
</details>


## Request URI Tracking
This feature allows responding with custom status code and delays for specific URIs, and tracking request counts for calls made to specific URIs (ignoring query parameters). URIs can be specified with `*` suffix to match all request URIs carrying the given URI as a prefix. URIs are stored lower-cased, and a request matches the exact URI first, otherwise the glob (`*`) URI with the longest matching prefix.
Note: To configure a `goto` server to respond with custom/random response payloads for specific URIs, see [`Response Payload`](../response/payload/README.md#response-payload) feature.

#### URI APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST, PUT|	/request/uri<br/>/set/status=`{status:count}`?uri=`{uri}` | Set forced response status to respond with for a URI, either for all subsequent calls until cleared, or for a specific number of subsequent calls. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. Codes must be in the range 100-999 (`400 Invalid status` otherwise); status `0` clears the config for the URI. The `uri` query parameter is required (`400 Cannot add. Invalid URI`) |
|POST, PUT|	/request/uri<br/>/set/delay=`{delay:count}`?uri=`{uri}` | Set forced delay for a URI, either for all subsequent calls until cleared, or for a specific number of subsequent calls. `delay` must be a single Go duration (e.g. `300ms`, `2s`); **ranges are not supported here** (a `low-high` range or any unparseable duration silently clears the URI's delay and still replies `200`). A zero/invalid delay clears the config for the URI. |
|GET      |	/request/uri<br/>/counts                     | Get request counts for all URIs (JSON object `{uri: count}`) |
|POST, PUT|	/request/uri<br/>/counts/enable              | Enable tracking request counts for all URIs (global, not per port) |
|POST, PUT|	/request/uri<br/>/counts/disable             | Disable tracking request counts for all URIs |
|POST     |	/request/uri<br/>/counts/clear               | Clear request counts for all URIs |
|GET      |	/request/uri               | Get current configurations (delays, statuses, counts) for all configured URIs of the port |

Behaviour notes:
* `status` and `delay` accept an optional `:count` suffix (`418:2`, `300ms:2`): the config applies to the next `count` calls and is then removed. Without it (or with a count <= 0) it applies until cleared.
* Responses carry the headers `Goto-URI-Status` (forced status), `Goto-URI-Status-Remaining` (calls left, when a count was given) and `Goto-Response-Delay` (applied delay).
* Matching uses the request path without the query string, lower-cased: the config of the exact URI wins, otherwise the config of the longest `*` (glob) URI that is a prefix of the path. A URI status takes precedence over the global response status ([`/response/status`](../response/status/README.md)).
* Replies: `200` with a plain-text confirmation, e.g. `Port [8080] URI [/foo] status set to [418] for next [2] calls`.


<details>
<summary>URI API Examples</summary>

```
curl -X POST 'localhost:8080/request/uri/set/status=418,401,404:2?uri=/foo'

curl -X POST 'localhost:8080/request/uri/set/status=503?uri=/api*'

curl -X POST 'localhost:8080/request/uri/set/status=0?uri=/foo'    # clear

curl -X POST 'localhost:8080/request/uri/set/delay=2s:2?uri=/foo'

curl -X POST 'localhost:8080/request/uri/set/delay=300ms?uri=/slow*'

curl localhost:8080/request/uri

curl localhost:8080/request/uri/counts

curl -X POST localhost:8080/request/uri/counts/enable

curl -X POST localhost:8080/request/uri/counts/disable

curl -X POST localhost:8080/request/uri/counts/clear
```

</details>

<details>
<summary>URI Counts Result Example</summary>
<p>

```
{
  "/debug": 18,
  "/echo": 5,
  "/foo": 4,
  "/foo/3/bar/4": 10,
  "/foo/4/bar/5": 10
}
```

</p>
</details>

<details>
<summary>URI Configs Result Example (<code>GET /request/uri</code>)</summary>
<p>

Delay is reported in nanoseconds; `Times` is `0` for "until cleared".

```
{
  "uriCountsByPort": {"/foo": 4},
  "uriDelayByPort": {
    "/slow": {"URI": "/slow", "Glob": true, "Delay": 300000000, "Times": 0}
  },
  "uriStatusByPort": {
    "/foo": {"URI": "/foo", "Glob": false, "Statuses": [418, 401, 404], "Times": 2}
  }
}
```

</p>
</details>


## Requests Filtering

This feature allows bypassing or ignoring some requests based on URIs and Headers match. A status code can be configured to be sent for ignored/bypassed requests. While both `bypass` and `ignore` filtering results in requests skipping additional processing, `bypass` requests are still logged whereas `ignored` requests don't generate any logs. Request counts are tracked for both bypassed and ignored requests.

* Ignore and Bypass configurations are not port specific and apply to all ports.
* APIs for Bypass and Ignore are alike and listed in a single table below. The two feature APIs only differ in the prefix `/request/bypass` vs `/request/ignore` (also served as `/server/request/bypass|ignore`).
* The default status for filtered requests is `200`, for both ignore and bypass. Filtered responses carry the header `Goto-Filtered-Request: true` and an empty body.
* For URI matches, prefix `!` can be used for negative matches. Negative URI matches are treated with conjunction (`AND`) whereas positive URI matches are treated with disjunction (`OR`). A URI gets filtered if: 
    * It matches any positive URI filter
    * It doesn't match all negative URI filters
* When `/` is configured as URI match, base URL both with and without `/` are matched
* URI matches are case-insensitive regexes anchored to the whole request URI **including the query string** (`^uri$`): `ignore/add?uri=/foo` filters `/foo` but not `/foo?x=1`; use `/foo.*` for that. An invalid regex currently resets the connection (the API call panics) instead of returning `400`.
* Header matches: the header name is case-insensitive; the optional value is matched case-insensitively against the whole header value, either as a regex (`bar.*`) or, if it ends in `*`, as a glob (`bar*`, stored as `^bar(.*)?$`). A header filter added without a value matches any value of the header.
* Changes are queued (`pendingUpdates: true` in the config) and applied on the next filtered-request check; `GET` shows both the active (`uris`, `notUris`, `headers`) and queued (`uriUpdates`, `notUriUpdates`, `headerUpdates`) sets.
* Order of evaluation per request: ignore URIs, bypass URIs, ignore headers, bypass headers.
* `remove` is by exact key: `remove/header/{header}` removes only the "any value" entry; an entry added as `X-Skip=a*` must be removed with `remove/header/X-Skip=a*`. The reply text for a header removal is `Will not ignore Header [name: value]` regardless of whether anything was removed.

#### Request Ignore/Bypass APIs

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/add?uri=`{uri}`       | Filter (ignore or bypass) requests based on uri match, where uri can be a regex. `!` prefix in the URI causes it to become a negative match. Replies `200 Will ignore URI [..]`; `400` if `uri` is missing. |
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/add/header/`{header}`  | Filter (ignore or bypass) requests based on header name match |
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/add/header/`{header}`=`{value}`  | Filter (ignore or bypass) requests where the given header name as well as the value matches, where value can be a regex (`bar.*`) or a glob (`bar*`). An invalid regex currently resets the connection. |
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/remove?uri=`{uri}`    | Remove a URI filter config (positive or negative, case-insensitive) |
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/remove/header<br/>/`{header}`    | Remove a header filter config |
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/remove/header<br/>/`{header}`=`{value}`    | Remove a header+value filter config |
|PUT, POST| /request<br/>/`[ignore\|bypass]`<br/>/set/status=`{status}` | Set status code to be returned for filtered URI requests (100-999, else `400 Invalid status`) |
|GET      |	/request<br/>/`[ignore\|bypass]`/status              | Get current ignore or bypass status code (plain text `Status for ignore: 200`) |
|PUT, POST| /request<br/>/`[ignore\|bypass]`/clear               | Remove all filter configs and reset the count |
|GET      |	/request<br/>/`[ignore\|bypass]`/count               | Get ignored or bypassed request count: `{"ignoredRequests": N}` (the key is `ignoredRequests` for bypass too) |
|GET      |	/request<br/>/`[ignore\|bypass]`                     | Get current ignore or bypass configs |


<details>
<summary>Request Filter (Ignore/Bypass) API Examples</summary>

```
#all APIs can be used for both ignore and bypass

curl -X POST localhost:8080/request/ignore/clear
curl -X POST localhost:8080/request/bypass/clear

#ignore all requests where URI has /foo prefix
curl -X PUT 'localhost:8080/request/ignore/add?uri=/foo.*'

#ignore all requests where URI has /foo prefix and contains bar somewhere
curl -X PUT 'localhost:8080/request/ignore/add?uri=/foo.*bar.*'

#ignore all requests where URI does not have /foo prefix
curl -X POST 'localhost:8080/request/ignore/add?uri=!/foo.*'

#ignore all requests that carry a header `foo` with value that has `bar` prefix
curl -X PUT 'localhost:8080/request/ignore/add/header/foo=bar*'

curl -X PUT 'localhost:8080/request/bypass/add/header/foo=bar*'

curl -X PUT 'localhost:8080/request/ignore/remove?uri=/bar'
curl -X PUT 'localhost:8080/request/bypass/remove?uri=/bar'
curl -X PUT 'localhost:8080/request/ignore/remove/header/foo=bar*'

#set status code to use for ignore and bypass requests
curl -X PUT localhost:8080/request/ignore/set/status=418
curl -X PUT localhost:8080/request/bypass/set/status=418
curl localhost:8080/request/ignore/status

curl localhost:8080/request/ignore/count

curl localhost:8080/request/ignore
curl localhost:8080/request/bypass

```

</details>

<details>
<summary>Ignore Result Example</summary>
<p>

Values of `uris`, `notUris` and `headers` are the compiled regexes; the `...Updates` fields hold the queued configuration that becomes active on the next check.

```
$ curl localhost:8080/request/ignore
{
  "uris": {
    "/foo.*": "(?i)^/foo.*$"
  },
  "notUris": {
    "!/keep.*": "(?i)^/keep.*$"
  },
  "headers": {
    "foo": {
      "bar*": "(?i)^bar(.*)?$"
    }
  },
  "uriUpdates": {
    "/foo.*": "(?i)^/foo.*$"
  },
  "notUriUpdates": {
    "!/keep.*": "(?i)^/keep.*$"
  },
  "headerUpdates": {
    "foo": {
      "bar*": "(?i)^bar(.*)?$"
    }
  },
  "status": 200,
  "filteredCount": 1,
  "pendingUpdates": false
}
```

</p>
</details>
