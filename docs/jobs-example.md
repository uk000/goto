#### Job APIs Examples:

```
curl -X POST http://localhost:8080/jobs/clear

# An HTTP job: the task is a client target spec (name and url are required, headers is an object)
curl localhost:8080/jobs/add --data '
{
"name": "job1",
"task": {
	"name": "job1",
	"method":	"POST",
	"url": "http://localhost:8081/echo",
	"headers":{"x": "x1", "y": "y1"},
	"body": "{\"test\":\"this\"}",
	"replicas": 1, "requestCount": 1,
	"delay": "200ms"
	},
"auto": false,
"count": 10,
"keepFirst": true,
"maxResults": 5,
"delay": "1s"
}'

# A command job whose output lines trigger job3: the 1st and 3rd words of each output line are named `date` and `msg`
# (outputMarkers is required for the outputTrigger to fire)
curl -s localhost:8080/jobs/add --data '
{
"name": "job2",
"task": {
	"cmd": "sh",
	"args": ["-c", "printf `date +%s`; echo \" Say Hello\"; sleep 1; printf `date +%s`; echo \" Say Hi\""],
	"outputMarkers": {"1":"date","3":"msg"}
},
"auto": false,
"count": 1,
"keepFirst": true,
"maxResults": 5,
"initialDelay": "1s",
"delay": "1s",
"outputTrigger": {"name": "job3"}
}'


# job3 references the markers of job2 in its args
curl -s localhost:8080/jobs/add --data '
{
"name": "job3",
"task": {
	"cmd": "sh",
	"args": ["-c", "printf `date +%s`; printf \" Output {date} {msg} Processed\"; sleep 1;"]
},
"auto": false,
"count": 1,
"keepFirst": true,
"maxResults": 10,
"delay": "1s"
}'

# A cron job (a duration or a cron expression), started right away; `timeout` is in nanoseconds
curl -s localhost:8080/jobs/add --data '
{
"name": "ticker",
"task": {"cmd": "date"},
"cron": "15s",
"keepResults": 10,
"timeout": 30000000000
}'

# An HTTP job that triggers another HTTP job upon completion, forwarding its response payload
curl -s localhost:8080/jobs/add --data '
{
"name": "job4",
"task": {"name": "job4", "method": "POST", "url": "http://localhost:8081/echo/body", "body": "hello"},
"finishTrigger": {"name": "job5", "forwardPayload": true}
}'
curl -s localhost:8080/jobs/add --data '
{
"name": "job5",
"task": {"name": "job5", "method": "POST", "url": "http://localhost:8081/echo/body"}
}'

# Update a job (same as add; responds `Updated Job: ...` for an existing job)
curl -X PUT localhost:8080/jobs/update --data '{"name": "job3", "task": {"cmd": "echo", "args": ["hi {msg}"]}}'

# Store a file at a given path, and a script. The script is registered as `ascriptjob` and a job with that name is created.
curl -X POST http://localhost:8080/jobs/store/file/some.conf?path=/tmp/foo/conf --data-binary @somepath/some-file.txt

curl -X POST http://localhost:8080/jobs/add/script/ascriptjob.sh --data-binary @somepath/somescript.sh

curl -X POST http://localhost:8080/jobs/ascriptjob/run

curl http://localhost:8080/jobs/scripts

curl -X POST http://localhost:8080/jobs/job1,job2/remove

curl http://localhost:8080/jobs

curl -X POST http://localhost:8080/jobs/job1,job2/run

curl -X POST http://localhost:8080/jobs/run/all

curl -X POST http://localhost:8080/jobs/job1,job2/stop

curl -X POST http://localhost:8080/jobs/stop/all

curl http://localhost:8080/jobs/job1/results

curl http://localhost:8080/jobs/results

curl -X POST http://localhost:8080/jobs/results/clear
```

#### Responses

```
$ curl -X POST localhost:8080/jobs/add --data '{"name":"job3","task":{"cmd":"echo","args":["got {x} {y}"]}}'
Added Job: {"name":"job3","task":{"args":["got {x} {y}"],"cmd":"echo"},"auto":false,"delay":"","initialDelay":"","count":1,"cron":"","maxResults":20,"keepResults":5,"keepFirst":false,"timeout":600000000000,"outputTrigger":null,"finishTrigger":null}

$ curl -X POST localhost:8080/jobs/job3/run
Jobs [job3] started

$ curl -X POST localhost:8080/jobs/nonexist/run        # HTTP 406
No jobs to run

$ curl -X POST localhost:8080/jobs/add --data '{"name":"bad"}'    # HTTP 400
Failed to add job with error: Jobs: Invalid Task: no task given

$ curl -X POST localhost:8080/jobs/add --data '{"name":"bad","task":{"foo":"bar"}}'    # HTTP 400
Failed to add job with error: Jobs: HTTP Task Error: [name is required] Jobs: Command Task Error: [Jobs: A command task must specify a command or reference a script]

$ curl -X POST localhost:8080/jobs/add/script/hello.sh --data-binary @s1.sh
Stored New Job Script [hello] at path [/work/dir/hello.sh], and Job created with name [hello]

$ curl localhost:8080/jobs/scripts
{
  "hello": {
    "name": "hello",
    "path": "/work/dir/hello.sh",
    "shell": true
  }
}
```

#### Job Result Example (command job)

Each run of a job has a run number (the key below). A command job ends its last iteration with a result that has empty `data` and `finished: true`.

```
$ curl http://localhost:8080/jobs/job2/results
{
  "1": [
    {
      "id": "1.1.1",
      "finished": false,
      "stopped": false,
      "last": true,
      "time": "2020-06-13T22:04:28.995178-07:00",
      "data": "1592111068 Say Hello"
    },
    {
      "id": "1.1.2",
      "finished": false,
      "stopped": false,
      "last": true,
      "time": "2020-06-13T22:04:30.006885-07:00",
      "data": "1592111070 Say Hi"
    },
    {
      "id": "1.1.3",
      "finished": true,
      "stopped": false,
      "last": true,
      "time": "2020-06-13T22:04:30.007281-07:00",
      "data": ""
    }
  ],
  "2": [
    {
      "id": "2.1.1",
      "finished": false,
      "stopped": false,
      "last": true,
      "time": "2020-06-13T22:04:35.600331-07:00",
      "data": "1592111075 Say Hello"
    },
    {
      "id": "2.1.2",
      "finished": false,
      "stopped": false,
      "last": true,
      "time": "2020-06-13T22:04:36.610472-07:00",
      "data": "1592111076 Say Hi"
    },
    {
      "id": "2.1.3",
      "finished": true,
      "stopped": false,
      "last": true,
      "time": "2020-06-13T22:04:36.610759-07:00",
      "data": ""
    }
  ]
}
```

#### Job Result Example (HTTP job)

For an HTTP job, `data` is the invocation result of the call (see `InvocationResult` in the [Client JSON Schemas](client-api-json-schemas.md)). The sample below is trimmed (response headers shortened) and comes from a job with `count: 2` and `keepResults: 3` that POSTs `hello` to `/echo`; the ids are `<run>.<iteration>.<result number>`.

```
$ curl http://localhost:8080/jobs/h1/results
{
  "1": [
    {
      "id": "1.1.1",
      "finished": false,
      "stopped": false,
      "last": false,
      "time": "2026-11-08T11:16:02.8451-08:00",
      "data": {
        "targetName": "h1",
        "targetID": "h1[1][1]",
        "request": {
          "id": "h1",
          "url": "http://localhost:8080/echo",
          "uri": "/echo",
          "headers": {
            "Goto-Target-ID": "h1[1][1]",
            "Goto-Target-URL": "http://localhost:8080/echo",
            "x": "x1"
          },
          "payloadSize": 5,
          "firstByteOutAt": "",
          "lastByteOutAt": "",
          "firstRequestAt": "2026-11-08T11:16:02.84502-08:00",
          "lastRequestAt": "2026-11-08T11:16:02.84502-08:00"
        },
        "response": {
          "status": "200 OK",
          "statusCode": 200,
          "headers": {
            "Content-Type": ["application/json"],
            "Goto-Response-Status": ["200"]
          },
          "payloadSize": 852,
          "clientStreamCount": 0,
          "serverStreamCount": 0,
          "payload": "",
          "firstByteInAt": "2026-11-08 19:16:02.845034 +0000 UTC",
          "lastByteInAt": "2026-11-08 19:16:02.845034 +0000 UTC",
          "peerCertInfo": null
        },
        "failedURLs": {},
        "retries": 0,
        "lastRetryReason": "",
        "validAssertionIndex": 0,
        "errors": null,
        "tookNanos": 3658125
      }
    }
  ]
}
```
