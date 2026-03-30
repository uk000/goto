# Response Delay
This feature allows adding a delay to all requests except bypass URIs and proxy requests. Delay is specified as duration, e.g. 1s. 

Delay is not applied to the following requests:
- `Goto` admin calls and
- Delay API `/delay`

When a delay is applied to a request, the response carries a header `Response-Delay` with the value of the applied delay.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/delay/set/{delay} | Set a delay for non-management requests (i.e. runtime traffic). `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range each time. |
| PUT, POST | /server/response<br/>/delay/clear       | Remove currently set delay |
| GET       |	/server/response/delay             | Get currently set delay |


<br/>
<details>
<summary>Response Delay Events</summary>

- `Delay Configured`
- `Delay Cleared`
- `Response Delay Applied`: generated when a configured response delay is applied to requests not explicitly asking for a delay, i.e. not generated for `/delay` API call.

</details>

<details>
<summary>Response Delay API Examples</summary>

```
curl -X POST localhost:8080/server/response/delay/clear

curl -X PUT localhost:8080/server/response/delay/set/1s-3s

curl localhost:8080/server/response/delay
```

</details>



# Response Headers
This feature allows adding custom response headers to all responses sent by the server.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/headers/add<br/>/`{header}`=`{value}`  | Add a custom header to be sent with all responses |
| PUT, POST | /server/response<br/>/headers/remove/`{header}`       | Remove a previously added custom response header |
| POST      |	/server/response<br/>/headers/clear                 | Remove all configured custom response headers |
| GET       |	/server/response/headers                       | Get list of configured custom response headers |

<br/>
<details>
<summary>Response Headers Events</summary>

- `Response Header Added`
- `Response Header Removed`
- `Response Header Cleared`

</details>

<details>
<summary>Response Headers API Examples</summary>

```
curl -X POST localhost:8080/server/response/headers/clear

curl -X POST localhost:8080/server/response/headers/add/x=x1

curl -X POST localhost:8080/server/response/headers/remove/x

curl localhost:8080/server/response/headers
```

</details>

# <a name="-stream-chunked-payload"></a>

## > Stream (Chunked) Payload

This URI responds with either pre-configured or random-generated payload where response behavior is controlled by the parameters passed to the API. The feature allows requesting a custom payload size, custom response duration over which to stream the payload, custom chunk size to be used for splitting the payload into chunks, and custom delay to be used in-between chunked responses. Combination of these parameters define the total payload size and the total duration of the response.

Stream responses carry following headers:

- `Goto-Stream-Length: <total payload size>`
- `Goto-Stream-Duration: <total response duration>`
- `Goto-Chunk-Count: <total number of chunks>`
- `Goto-Chunk-Length: <per-chunk size>`
- `Goto-Chunk-Delay: <per-chunk delay>`
- `X-Content-Type-Options: nosniff`
- `Transfer-Encoding: chunked`

#### Stream APIs
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/stream/payload=`{size}`<br/>/duration={duration}<br/>/delay={delay} | Respond with a payload of the given size delivered over the given duration with the given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/duration={duration}<br/>/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered over the given duration with given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/count={count}/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered the given count of times with given delay per chunk. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/duration={duration}<br/>/delay={delay} | Respond with pre-configured default payload split into enough chunks to spread out over the given duration with given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/count={count}/delay={delay} | Respond with pre-configured default payload split into the given count of chunks with the given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |

<br/>
<details>
<summary>Stream Response API Example</summary>

```
curl -v --no-buffer localhost:8080/stream/payload=10K/duration=5s-15s/delay=100ms-1s

curl -v --no-buffer localhost:8080/stream/chunksize=100/duration=5s/delay=500ms-2s

curl -v --no-buffer localhost:8080/stream/chunksize=100/count=5/delay=200ms

curl -v --no-buffer localhost:8080/stream/duration=5s/delay=100ms-300ms

curl -v --no-buffer localhost:8080/stream/count=10/delay=300ms
```

</details>


# Response Status
This feature allows setting a forced response status for all requests except bypass URIs. Server also tracks the number of status requests received (via /status URI) and the number of responses sent per status code.

#### Response Status APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/status/set/`{status}`     | Set a forced response status that all non-proxied and non-management requests will be responded with. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. Syntax of status param is `<status1,status2,...>:<times>`, where `times` is the number of times the given forced statuses will be used, after which the requests will be served the normal response status. |
| PUT, POST | /server/response<br/>/status/set/`{status}`?uri=`{uri}`     | Set a forced response status for a specific URL. All non-proxied requests for the URI will be responded with the given `status`. Status syntax is `<status1,status2,...>:<times>`. |
| PUT, POST |	/server/response<br/>/status/clear            | Remove currently configured forced response status, so that all subsequent calls will receive their original deemed response |
| PUT, POST | /server/response<br/>/status/counts/clear     | Clear counts tracked for response statuses |
| GET       |	/server/response<br/>/status/counts/`{status}`  | Get request counts for a given status |
| GET       |	/server/response<br/>/status/counts           | Get request counts for all response statuses so far |
| GET       |	/server/response/status                  | Get the currently configured forced response statuses for all ports |

<br/>
<details>
<summary>Response Status Events</summary>

- `Response Status Configured`
- `Response Status Cleared`
- `Response Status Counts Cleared`

</details>

<details>
<summary>Response Status API Examples</summary>

```
curl -X POST localhost:8080/server/response/status/counts/clear

curl -X POST localhost:8080/server/response/status/clear

curl -X PUT localhost:8080/server/response/status/set/502

curl -X PUT localhost:8080/server/response/status/set/0

curl -X POST localhost:8080/server/response/status/counts/clear

curl localhost:8080/server/response/status/counts

curl localhost:8080/server/response/status/counts/502

curl -X POST localhost:8080/server/response/status/configure --data <<EOF
{
	"port": 8080,
	"statuses": [403],
	"times": -1,
	"match": {
		"uri": "/foo",
		"headers": [
			{
				"header": "foo",
				"present": false
			},
			{
				"header": "bar",
        "value": "foo"
			}
		]
	}
}
EOF
```

</details>

<details>
<summary>Response Status Tracking Result Example</summary>

<p>

```
{
  "countsByRequestedStatus": {
    "418": 20
  },
  "countsByReportedStatus": {
    "200": 15,
    "202": 4,
    "208": 5,
    "418": 20
  }
}
```

</p>
</details>


# Response Triggers

`Goto` allows targets to be configured that are triggered based on response status. The triggers can be invoked manually for testing, but their real value is when they get triggered based on response status. Even more valuable when the request was proxied to another upstream service, in which case the trigger is based on the response status of the upstream service.

#### Triggers APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     |	/server/response<br/>/triggers/add              | Add a trigger target. See [Trigger Target JSON Schema](#trigger-target-json-schema) |
|PUT, POST| /server/response<br/>/triggers/{target}/remove  | Remove a trigger target |
|PUT, POST| /server/response<br/>/triggers/{target}/enable  | Enable a trigger target |
|PUT, POST| /server/response<br/>/triggers/{target}/disable | Disable a trigger target |
|POST     |	/server/response<br/>/triggers/`{targets}`/invoke | Invoke trigger targets by name for manual testing |
|POST     |	/server/response<br/>/triggers/clear            | Remove all trigger targets |
|GET 	    |	/server/response<br/>/triggers/counts             | Report invocation counts for all trigger targets |
|GET 	    |	/server/response/triggers             | List all trigger targets |

<br/>
<details>
<summary>Trigger Target JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| name        | string      | Name for this target |
| method      | string      | HTTP method to use for this target |
| url         | string      | URL for the target. |
| headers     | `[][]string`| request headers to send with this trigger request |
| body        | `string`    | request body to send with this trigger request |
| sendID      | bool        | Whether or not a unique ID be sent with each request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| enabled     | bool        | Whether or not the trigger is currently active |
| triggerOn   | []int       | List of response statuses for which this target will be triggered |
| startFrom   | int         | Trigger the target after these many occurrences of the trigger status codes |
| stopAt      | int         | Stop triggering the target after these many occurrences of the trigger status codes |
| statusCount | int         | (readonly) Number of occurrences of the status codes that this trigger listens on |
| triggerCount | int         | (readonly) Number of times this target has been triggered  |

</details>

<details>
<summary>Triggers Events</summary>

- `Trigger Target Added`
- `Trigger Target Removed`
- `Trigger Target Enabled`
- `Trigger Target Disabled`
- `Trigger Target Invoked`

</details>

See [Triggers Example](docs/triggers-example.md)



# Status
The URI `/status/`{status}`` allows clients to ask for a specific status as response code. The given status is reported back, except when forced status is configured in which case the forced status is sent as response.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status/`{status}` or /status=`{status}` | This call either receives the given status, or the forced response status if one is set. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE  |	/status=`{status}`<br/>/delay=`{delay}` | In addition to requesting a status as above, this API also allows a delay to be applied. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`?x-request-id=`{requestId}` | When the status param is passed in the format `<code>:<count>`, the requested response code is returned for `count` number of subsequent calls (starting from the current one) before reverting back to 200. The optional query param `x-request-id` can be used to ask for stateful status for each unique request id, allowing multiple concurrent clients to each receive its own independent stateful response. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`/delay=`{delay}`?x-request-id=`{requestId}` | Same as above, the requested response code is returned for `count` number of subsequent calls but with the given delay applied before a response. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. After `count` responses, the response status reverts to 200.  |
| GET, PUT, POST, OPTIONS, HEAD, DELETE  |	/status=`{status:count}`/flipflop?x-request-id=`{requestId}` | This call responds with the given status for the given count times when called successively with the same count value. Once the status is served `count` times, the next status served is `200`, and subsequent calls start the cycle again. Optional query param `x-request-id` can be used to perform status flip for each unique request, preventing requests from affecting one another. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`/delay=`{delay}`/flipflop?x-request-id=`{requestId}` | Same behavior as above except that the given delay duration param gets applied, allowing you to add artificial delay before responding with the given status. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET  |	/status/flipflop | Reports the current flipflop counter value, i.e. number of times the most recent status has been served. |
| POST |	/status/clear | Clears the current state of all stateful statuses recorded so far. |

<br/>
<details>
<summary>Status API Example</summary>

```
curl -I  localhost:8080/status/418

curl -I  localhost:8080/status/501,502,503

curl -v  localhost:8080/status=501,502,503/delay=100ms-1s

curl -v localhost:8080/status=503:2?x-request-id=1

curl -v localhost:8080/status=503:2/delay=1s?x-request-id=1

curl -I localhost:8080/status=503:3/flipflop?x-request-id=1

curl -v localhost:8080/status=503:3/delay=1s/flipflop?x-request-id=1

curl localhost:8080/status/flipflop

curl -XPOST localhost:8080/status/clear
```
</details>


# Delay
The URI `/delay/{delay}` allows clients to ask for a specific delay to be applied to the current request. The delay API is not subject to the response delay that may be configured for all responses. Calling the URI as `/delay` responds with no delay, and so does the call as `/delay/0`, `/delay/0s`, etc.
When a delay is passed to this API, the response carries a header `Response-Delay` with the value of the applied delay.

> Note: For requesting a delay along with a specific status, check the `/status` API documentation above.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/delay/`{delay}` | Responds after the given delay. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. To apply delay with a specific response status code, see `/status` API above. |

<br/>
<details>
<summary>Delay API Example</summary>

```
curl -I  localhost:8080/delay/2s
curl -v  localhost:8080/delay/100ms-2s
```
</details>
