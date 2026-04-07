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
