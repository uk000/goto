#### GRPC Response Examples:

The `Goto` service (`Goto2` and `Goto3` are declared with the same methods, but they have no implementation and answer `UNIMPLEMENTED`) is defined in `pkg/rpc/grpc/protos/goto.proto`, see the [gRPC server APIs](../pkg/rpc/README.md) for the details. Its input message `Input` has a single field `text`, and its output message `Output` carries `id`, `text`, `at`, `gotoHost`, `gotoPort`, `viaGoto`, `gotoTLS`, `requestURI`, `requestHeaders` and `responseHeaders`.

Listeners are added with the `/server/listeners/add` API (the path `/listeners/add` is not a `goto` API: the request is just echoed back with status 200 and no listener gets created).

```
$ curl -X POST localhost:8080/server/listeners/add --data '{"label":"grpc-9091", "port":9091, "protocol":"grpc", "open":true}'

Listener 9091 added and opened.
```

The service can then be called with `grpcurl` (the server supports reflection, so no proto file is needed) or with `grpc_cli`.

```
$ grpcurl -v -plaintext -d '{"text":"hello"}' localhost:9091 Goto.echo

Resolved method descriptor:
rpc echo ( .Input ) returns ( .Output );

Request metadata to send:
(empty)

Response headers received:
content-type: application/grpc
goto-host: local@1.1.1.1:8080
goto-port: 9091
goto-protocol: gRPC
goto-remote-address: [::1]:54378
goto-rpc-method: /Goto/echo
request-authority: localhost:9091
request-content-type: application/grpc
request-grpc-accept-encoding: gzip
request-user-agent: grpcurl/1.9.4 grpc-go/1.83.2
via-goto: grpc-9091

Response contents:
{
  "id": "1",
  "text": "hello",
  "at": "2026-11-07T16:17:52.146926-08:00",
  "gotoHost": "local@1.1.1.1:8080",
  "gotoPort": 9091,
  "viaGoto": "grpc-9091",
  "gotoTLS": "false",
  "requestURI": "/Goto/echo",
  "requestHeaders": {
    ":authority": "localhost:9091",
    "content-type": "application/grpc",
    "grpc-accept-encoding": "gzip",
    "user-agent": "grpcurl/1.9.4 grpc-go/1.83.2"
  },
  "responseHeaders": {
    "Goto-Host": "local@1.1.1.1:8080",
    "Goto-Port": "9091",
    "Goto-Protocol": "gRPC",
    "Goto-RPC-Method": "/Goto/echo",
    "Goto-Remote-Address": "[::1]:54378",
    "Via-Goto": "grpc-9091",
    ...
  }
}

Response trailers received:
(empty)
Sent 1 request and received 1 response
```

The same call with `grpc_cli` (the output shows the `Output` message in text format):

```
$ grpc_cli call localhost:9091 Goto.echo "text: 'hello'"

connecting to localhost:9091
Received initial metadata from server:
goto-host : local@1.1.1.1:8080
goto-port : 9091
goto-protocol : gRPC
goto-remote-address : [::1]:54378
goto-rpc-method : /Goto/echo
via-goto : grpc-9091
id: "1"
text: "hello"
at: "2026-11-07T16:17:52.146926-08:00"
gotoHost: "local@1.1.1.1:8080"
gotoPort: 9091
viaGoto: "grpc-9091"
gotoTLS: "false"
requestURI: "/Goto/echo"
...
Rpc succeeded with OK status
```

A server stream call: the `text` of each response is the requested `text` (a random string of `chunkSize` characters when none is given), suffixed with the chunk number, `chunkCount` responses are sent, one per `interval`.

```
$ grpcurl -plaintext -d '{"chunkSize":10, "chunkCount":3, "interval":"1s"}' localhost:9091 Goto.streamOut

{
  "id": "2",
  "text": "2BtnfRxQos(1/3)",
  "at": "2026-11-07T16:17:52.167729-08:00",
  "gotoHost": "local@1.1.1.1:8080",
  "gotoPort": 9091,
  "viaGoto": "grpc-9091",
  "gotoTLS": "false",
  "requestURI": "/Goto/streamOut",
  "requestHeaders": {...},
  "responseHeaders": {...}
}
{
  "id": "3",
  "text": "2BtnfRxQos(2/3)",
  "at": "2026-11-07T16:17:53.168382-08:00",
  ...
}
{
  "id": "4",
  "text": "2BtnfRxQos(3/3)",
  "at": "2026-11-07T16:17:54.170442-08:00",
  ...
}
```

and with `grpc_cli`:

```
$ grpc_cli call localhost:9091 Goto.streamOut "chunkSize: 10, chunkCount: 3, interval: '1s'"

connecting to localhost:9091
Received initial metadata from server:
goto-host : local@1.1.1.1:8080
goto-port : 9091
goto-protocol : gRPC
goto-remote-address : [::1]:54347
goto-rpc-method : /Goto/streamOut
via-goto : grpc-9091
id: "2"
text: "2BtnfRxQos(1/3)"
at: "2021-02-07T12:32:11.690931-08:00"
gotoHost: "local@1.1.1.1:8080"
gotoPort: 9091
viaGoto: "grpc-9091"
...
id: "3"
text: "2BtnfRxQos(2/3)"
at: "2021-02-07T12:32:12.691058-08:00"
...
id: "4"
text: "2BtnfRxQos(3/3)"
at: "2021-02-07T12:32:13.691418-08:00"
...
Rpc succeeded with OK status
```

The listener-specific response payload, response delay, tracking and extauthz features are described in the [gRPC server APIs](../pkg/rpc/README.md); note that the payload and tracking APIs apply to the port of the request, so for a gRPC listener on `9091` they need to be called as `/port=9091/grpc/server/...`.
