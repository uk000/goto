#### Triggers API Examples:

(All `/response/...` URIs also work as `/server/response/...`. See the [Response Triggers](../pkg/server/response/trigger/README.md) README for the target schema and matching rules.)

```
curl -X POST localhost:8080/response/triggers/clear

# add a target on port 8081, via port 8080. Note: "enabled" defaults to false
curl -s localhost:8080/port=8081/response/triggers/add --data '{
	"name": "t1",
	"enabled": true,
	"httpTarget": {
		"method": "POST",
		"url": "http://localhost:8082/response/status/clear",
		"headers": {"X-Orig-URI": "{request.uri}"},
		"body": "",
		"sendID": false
	},
	"triggerStatuses": [502, 503],
	"startFrom": 2,
	"stopAt": 3
}'

# a target triggered by a URI pattern, any status
curl -s localhost:8080/response/triggers/add --data '{
	"name": "t2",
	"enabled": true,
	"httpTarget": {"url": "localhost:8082/echo"},
	"triggerURIs": ["/orders/{id}*"]
}'

curl -X POST localhost:8080/response/triggers/t1/remove

curl -X POST localhost:8080/response/triggers/t1/enable

curl -X POST localhost:8080/response/triggers/t1/disable

# invoke one or more targets (comma separated) manually
curl -X POST localhost:8080/response/triggers/t1/invoke
curl -X POST localhost:8080/response/triggers/t1,t2/invoke

curl localhost:8080/response/triggers/counts

curl localhost:8080/response/triggers/pipes

curl localhost:8080/response/triggers

```

Typical replies:

```
$ curl -X POST localhost:8080/response/triggers/add -d '{"name":"t2","httpTarget":{"method":"GET","url":"localhost:8080/echo"},"triggerURIs":["/trig2"]}'
Port [8080] Added trigger: t2

$ curl -X POST localhost:8080/response/triggers/add -d '{"name":"bad","triggerURIs":["/x("]}'     # HTTP 400
Invalid trigger: invalid trigger URI [/x(]: error parsing regexp: missing closing ): `^(?i)x($`

$ curl -X POST localhost:8080/response/triggers/add -d '{"name":"bad2","httpTarget":{"method":"GET","url":""}}'   # HTTP 400
Invalid trigger: url is required

$ curl -X POST localhost:8080/response/triggers/t2/enable
Port [8080] Trigger [t2] enabled

$ curl -X POST localhost:8080/response/triggers/t2/remove
Port [8080] Trigger Removed: t2

$ curl -X POST localhost:8080/response/triggers/t2/remove    # HTTP 400
No triggers

$ curl -X POST localhost:8080/response/triggers/nope/invoke  # HTTP 404
Triggers not found

$ curl -X POST localhost:8080/response/triggers/clear
Port [8080] Triggers Cleared
```

#### Triggers Details and Results Example

```
$ curl localhost:8080/response/triggers
{
  "targets": {
    "t1": {
      "name": "t1",
      "httpTarget": {
        "method": "POST",
        "url": "http://localhost:8082/response/status/clear",
        "headers": {
          "X-Orig-URI": "{request.uri}"
        },
        "body": "",
        "sendID": false
      },
      "pipe": false,
      "enabled": true,
      "triggerURIs": null,
      "triggerHeaders": null,
      "triggerStatuses": [
        502,
        503
      ],
      "startFrom": 2,
      "stopAt": 3,
      "matchCount": 5,
      "triggerCount": 2
    }
  },
  "targetsByURIs": {},
  "targetsByHeaders": {},
  "targetsByStatus": {
    "502": ["t1"],
    "503": ["t1"]
  },
  "triggerResults": {
    "t1": {
      "200": 2
    }
  }
}

$ curl -s localhost:8080/response/triggers/counts
{
  "t1": {
    "200": 2
  },
  "t3": {
    "200": 3
  }
}
```

* `targetsByURIs`, `targetsByStatus` and `targetsByHeaders` are the indexes used to find the targets for a response: a target is listed under its URI patterns if it has any, else under its statuses, else under its headers.
* `matchCount` is the number of times the target's conditions matched; `triggerCount` the number of times it fired (matches within `startFrom`..`stopAt`, while enabled, plus manual invocations). In the example above 5 responses matched, and the target fired for the 2nd and 3rd.
* `counts` / `triggerResults` count the HTTP status that the trigger call itself got from its target (`503` if the call could not be made).

Result of a manual invocation (an array with one entry per invoked HTTP target; abbreviated):

```
$ curl -X POST localhost:8080/response/triggers/t1/invoke
[{"targetName":"t1","targetID":"t1[4][1]",
  "request":{"id":"t1","url":"http://localhost:8082/response/status/clear?x-request-id=t1","uri":"/response/status/clear",
             "headers":{"Goto-Request-Id":"t1","X-Orig-URI":"/response/triggers/t1/invoke"}, ...},
  "response":{"status":"200 OK","statusCode":200,"headers":{...}, ...},
  "failedURLs":{},"retries":0,"errors":null,"tookNanos":955458}]
```

(For a manual invoke, `{request.uri}` resolves to the URI of the `invoke` call itself.)
