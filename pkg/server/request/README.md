# HTTP Request Tracking Features

## Request Headers Tracking

This feature allows tracking request counts by headers.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /server/request/headers<br/>/track/clear									| Remove all tracked headers |
|PUT, POST| /server/request/headers<br/>/track/add/`{headers}`					| Add headers to track |
|PUT, POST|	/server/request/headers<br/>/track/`{headers}`/remove				| Remove given headers from tracking |
|GET      | /server/request/headers<br/>/track/`{header}`/counts				| Get counts for a tracked header |
|PUT, POST| /server/request/headers<br/>/track/counts<br/>/clear/`{headers}`	| Clear counts for given tracked headers |
|POST     | /server/request/headers<br/>/track/counts/clear						| Clear counts for all tracked headers |
|GET      | /server/request/headers<br/>/track/counts									| Get counts for all tracked headers |
|GET      | /server/request/headers/track									      | Get list of tracked headers |


<br/>
<details>
<summary> Request Headers Tracking Events </summary>

- `Tracking Headers Added`
- `Tracking Headers Removed`
- `Tracking Headers Cleared`
- `Tracked Header Counts Cleared`

</details>

<details>
<summary>Request Headers Tracking API Examples</summary>

```
curl -X POST localhost:8080/server/request/headers/track/clear

curl -X PUT localhost:8080/server/request/headers/track/add/x,y

curl -X PUT localhost:8080/server/request/headers/track/remove/x

curl -X POST localhost:8080/server/request/headers/track/counts/clear/x

curl -X POST localhost:8080/server/request/headers/track/counts/clear

curl -X POST localhost:8080/server/request/headers/track/counts/clear

curl localhost:8080/server/request/headers/track
```

</details>

<details>
<summary>Request Header Tracking Results Example</summary>
<p>

```
$ curl localhost:8080/server/request/headers/track/counts

{
  "x": {
    "requestCountsByHeaderValue": {
      "x1": 20
    },
    "requestCountsByHeaderValueAndRequestedStatus": {
      "x1": {
        "418": 20
      }
    },
    "requestCountsByHeaderValueAndResponseStatus": {
      "x1": {
        "418": 20
      }
    }
  },
  "y": {
    "requestCountsByHeaderValue": {
      "y1": 20
    },
    "requestCountsByHeaderValueAndRequestedStatus": {
      "y1": {
        "418": 20
      }
    },
    "requestCountsByHeaderValueAndResponseStatus": {
      "y1": {
        "418": 20
      }
    }
  }
}
```

</p>
</details>

# <a name="request-timeout-tracking"></a>
## Request Timeout Tracking
This feature allows tracking request timeouts by headers.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /server/request/timeout<br/>/track/headers/`{headers}`  | Add one or more headers. Requests carrying these headers will be tracked for timeouts and reported |
|PUT, POST| /server/request<br/>/timeout/track/all                | Enable request timeout tracking for all requests |
|POST     |	/server/request<br/>/timeout/track/clear              | Clear timeout tracking configs |
|GET      |	/server/request<br/>/timeout/status                   | Get a report of tracked request timeouts so far |


<br/>
<details>
<summary> Request Timeout Tracking Events </summary>

- `Timeout Tracking Headers Added`
- `All Timeout Tracking Enabled`
- `Timeout Tracking Headers Cleared`
- `Timeout Tracked`

</details>

<details>
<summary>Request Timeout Tracking API Examples</summary>

```
curl -X POST localhost:8080/server/request/timeout/track/headers/x,y

curl -X POST localhost:8080/server/request/timeout/track/headers/all

curl -X POST localhost:8080/server/request/timeout/track/clear

curl localhost:8080/server/request/timeout/status
```

</details>

<details>
<summary>Request Timeout Status Result Example</summary>
<p>

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

</p>
</details>


## Request URI Tracking
This feature allows responding with custom status code and delays for specific URIs, and tracking request counts for calls made to specific URIs (ignoring query parameters). URIs can be specified with `*` suffix to match all request URIs carrying the given URI as a prefix.
Note: To configure a `goto` server to respond with custom/random response payloads for specific URIs, see [`Response Payload`](#server-response-payload) feature.

#### URI APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     |	/server/request/uri<br/>/set/status=`{status:count}`?uri=`{uri}` | Set forced response status to respond with for a URI, either for all subsequent calls until cleared, or for a specific number of subsequent calls. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. |
|POST     |	/server/request/uri<br/>/set/delay=`{delay:count}`?uri=`{uri}` | Set forced delay for a URI, either for all subsequent calls until cleared, or for a specific number of subsequent calls. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range each time. |
|GET      |	/server/request<br/>/uri/counts                     | Get request counts for all URIs |
|POST     |	/server/request<br/>/uri/counts/enable              | Enable tracking request counts for all URIs |
|POST     |	/server/request<br/>/uri/counts/disable             | Disable tracking request counts for all URIs |
|POST     |	/server/request<br/>/uri/counts/clear               | Clear request counts for all URIs |
|GET     |	/server/request/uri               | Get current configurations for all configured URIs |


<br/>
<details>
<summary> URIs Events </summary>

- `URI Status Configured`
- `URI Status Cleared`
- `URI Delay Configured`
- `URI Delay Cleared`
- `URI Call Counts Cleared`
- `URI Call Counts Enabled`
- `URI Call Counts Disabled`
- `URI Delay Applied`
- `URI Status Applied`

</details>

<details>
<summary>URI API Examples</summary>

```
curl -X POST localhost:8080/server/request/uri/set/status=418,401,404:2?uri=/foo

curl -X POST localhost:8080/server/request/uri/set/delay=1s-3s:2?uri=/foo

curl localhost:8080/server/request/uri/counts

curl -X POST localhost:8080/server/request/uri/counts/enable

curl -X POST localhost:8080/server/request/uri/counts/disable

curl -X POST localhost:8080/server/request/uri/counts/clear
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


## Requests Filtering

This feature allows bypassing or ignoring some requests based on URIs and Headers match. A status code can be configured to be sent for ignored/bypassed requests. While both `bypass` and `ignore` filtering results in requests skipping additional processing, `bypass` requests are still logged whereas `ignored` requests don't generate any logs. Request counts are tracked for both bypassed and ignored requests.

* Ignore and Bypass configurations are not port specific and apply to all ports.
* APIs for Bypass and Ignore are alike and listed in a single table below. The two feature APIs only differ in the prefix `/server/request/bypass` vs `/server/request/ignore`
* For URI matches, prefix `!` can be used for negative matches. Negative URI matches are treated with conjunction (`AND`) whereas positive URI matches are treated with disjunction (`OR`). A URI gets filtered if: 
    * It matches any positive URI filter
    * It doesn't match all negative URI filters
* When `/` is configured as URI match, base URL both with and without `/` are matched

#### Request Ignore/Bypass APIs

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/add?uri=`{uri}`       | Filter (ignore or bypass) requests based on uri match, where uri can be a regex. `!` prefix in the URI causes it to become a negative match. |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/add/header/`{header}`  | Filter (ignore or bypass) requests based on header name match |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/add/header/`{header}`=`{value}`  | Filter (ignore or bypass) requests where the given header name as well as the value matches, where value can be a regex. |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/remove?uri=`{uri}`    | Remove a URI filter config |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/remove/header<br/>/`{header}`    | Remove a header filter config |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/remove/header<br/>/`{header}`=`{value}`    | Remove a header+value filter config |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/set/status=`{status}` | Set status code to be returned for filtered URI requests |
|GET      |	/server/request<br/>/`[ignore\|bypass]`/status              | Get current ignore or bypass status code |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`/clear               | Remove all filter configs |
|GET      |	/server/request<br/>/`[ignore\|bypass]`/count               | Get ignored or bypassed request count |
|GET      |	/server/request<br/>/`[ignore\|bypass]`                     | Get current ignore or bypass configs |


<br/>
<details>
<summary>Request Filter (Ignore/Bypass) Events</summary>

- `Request Filter Added`
- `Request Filter Removed`
- `Request Filter Status Configured`
- `Request Filters Cleared`

</details>

<details>
<summary>Request Filter (Ignore/Bypass) API Examples</summary>

```
#all APIs can be used for both ignore and bypass

curl -X POST localhost:8080/server/request/ignore/clear
curl -X POST localhost:8080/server/request/bypass/clear

#ignore all requests where URI has /foo prefix
curl -X PUT localhost:8080/server/request/ignore/add?uri=/foo.*

#ignore all requests where URI has /foo prefix and contains bar somewhere
curl -X PUT localhost:8080/server/request/ignore/add?uri=/foo.*bar.*

#ignore all requests where URI does not have /foo prefix
curl -X POST localhost:8080/server/request/ignore/add?uri=!/foo.*

#ignore all requests that carry a header `foo` with value that has `bar` prefix
curl -X PUT localhost:8080/server/request/ignore/add/header/foo=bar.*

curl -X PUT localhost:8080/server/request/bypass/add/header/foo=bar.*

curl -X PUT localhost:8080/server/request/ignore/remove?uri=/bar
curl -X PUT localhost:8080/server/request/bypass/remove?uri=/bar

#set status code to use for ignore and bypass requests
curl -X PUT localhost:8080/server/request/ignore/set/status=418
curl -X PUT localhost:8080/server/request/bypass/set/status=418

curl localhost:8080/server/request/ignore
curl localhost:8080/server/request/bypass

```

</details>

<details>
<summary>Ignore Result Example</summary>
<p>

```
$ curl localhost:8080/server/request/ignore
{
  "uris": {
    "/foo": {}
  },
  "headers": {
    "foo": {
      "bar.*": {}
    }
  },
  "uriUpdates": {
    "/ignoreme": {}
  },
  "headerUpdates": {},
  "status": 200,
  "filteredCount": 1,
  "pendingUpdates": true
}
```

</p>
</details>
