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

```
