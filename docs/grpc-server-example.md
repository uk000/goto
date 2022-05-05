#### GRPC Response Examples:

```
$ curl localhost:8080/listeners/add --data '{"label":"grpc-9091", "port":9091, "protocol":"grpc", "open":true}'

$ grpc_cli call localhost:9091 Goto.echo "payload: 'hello'"

connecting to localhost:9091
Received initial metadata from server:
goto-host : local@1.1.1.1:8080
goto-port : 9091
goto-protocol : GRPC
goto-remote-address : [::1]:54378
via-goto : grpc-9091
payload: "hello"
at: "2021-02-07T12:32:33.832499-08:00"
gotoHost: "local@1.1.1.1:8080"
gotoPort: 9091
viaGoto: "grpc-9091"
Rpc succeeded with OK status

$ grpc_cli call localhost:9091 Goto.streamOut "chunkSize: 10, chunkCount: 3, interval: '1s'"

connecting to localhost:9091
Received initial metadata from server:
goto-host : local@1.1.1.1:8080
goto-port : 9091
goto-protocol : GRPC
goto-remote-address : [::1]:54347
via-goto : grpc-9091
payload: "f4GE!G?Epr"
at: "2021-02-07T12:32:11.690931-08:00"
gotoHost: "local@1.1.1.1:8080"
gotoPort: 9091
viaGoto: "grpc-9091"
payload: "f4GE!G?Epr"
at: "2021-02-07T12:32:12.691058-08:00"
gotoHost: "local@1.1.1.1:8080"
gotoPort: 9091
viaGoto: "grpc-9091"
payload: "f4GE!G?Epr"
at: "2021-02-07T12:32:13.691418-08:00"
gotoHost: "local@1.1.1.1:8080"
gotoPort: 9091
viaGoto: "grpc-9091"
Rpc succeeded with OK status

$ curl -XPOST localhost:8080/events/flush

$ curl localhost:8080/events
[
  {
    "title": "Listener Added",
    "data": {
      "listener": {
        "listenerID": "9091-1",
        "label": "grpc-9091",
        "port": 9091,
        "protocol": "grpc",
        "open": true,
        "tls": false
      },
      "status": "Listener 9091 added and opened."
    },
    ...
  },
  {
    "title": "GRPC Listener Started",
    "data": {
      "details": "Starting GRPC Listener 9091-1"
    },
    ...
  },
  {
    "title": "Flushed Traffic Report",
    "data": [
      {
        "port": 9091,
        "uri": "GRPC.streamOut.start",
        "statusCode": 200,
        "statusRepeatCount": 2,
        "firstEventAt": "2021-02-07T12:31:29.81144-08:00",
        "lastEventAt": "2021-02-07T12:32:11.690928-08:00"
      },
      {
        "port": 9091,
        "uri": "GRPC.streamOut.end",
        "statusCode": 200,
        "statusRepeatCount": 2,
        "firstEventAt": "2021-02-07T12:31:32.817072-08:00",
        "lastEventAt": "2021-02-07T12:32:14.692153-08:00"
      },
      {
        "port": 9091,
        "uri": "GRPC.echo",
        "statusCode": 200,
        "statusRepeatCount": 2,
        "firstEventAt": "2021-02-07T12:32:33.832506-08:00",
        "lastEventAt": "2021-02-07T12:34:59.386956-08:00"
      }
    ],
    ...
  },
  {
    "title": "Events Flushed",
    ...
  }
]

```
