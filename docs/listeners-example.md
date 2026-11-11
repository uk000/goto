### Listener API Examples:

```
#HTTP listener
curl localhost:8080/server/listeners/add --data '{"port":8081, "protocol":"http", "label":"Server-8081"}'

#HTTPS listener with auto-generated cert using common name `foo.com`
curl -s localhost:8080/listeners/add --data '{"label":"https-8443", "port":8443, "protocol":"https", "open":true, "autoCert":true, "commonName":"foo.com"}'

#TCP Listener
curl -s localhost:8080/server/listeners/add --data '{"label":"tcp-9000", "port":9000, "protocol":"tcp", "open":true, "tcp": {"readTimeout":"15s","writeTimeout":"15s","connectTimeout":"15s","connIdleTimeout":"20s","responseDelay":"1s", "connectionLife":"20s"}}'

curl localhost:8080/server/listeners/add --data '{"port":9091, "protocol":"grpc", "label":"GRPC-9091"}'

curl -X POST localhost:8080/server/listeners/8081/remove

curl -X PUT localhost:8080/server/listeners/9000/open

curl -X PUT localhost:8080/server/listeners/9000/close

curl -X PUT localhost:8080/server/listeners/9000/reopen

curl localhost:8080/server/listeners

```

### Listener Output Example

```
$ curl -s localhost:8080/server/listeners

{
  "8081": {
    "listenerID": "8081-1",
    "label": "http-8081",
    "port": 8081,
    "protocol": "http",
    "open": true,
    "tls": false
  },
  "8082": {
    "listenerID": "",
    "label": "http-8082",
    "port": 8082,
    "protocol": "http",
    "open": false,
    "tls": true
  },
  "9000": {
    "listenerID": "9000-1",
    "label": "tcp-9000",
    "port": 9000,
    "protocol": "tcp",
    "open": true,
    "tls": false,
    "tcp": {
      "readTimeout": "1m",
      "writeTimeout": "1m",
      "connectTimeout": "15s",
      "connIdleTimeout": "1m",
      "connectionLife": "2m",
      "stream": false,
      "echo": false,
      "conversation": false,
      "silentLife": false,
      "closeAtFirstByte": false,
      "validatePayloadLength": true,
      "validatePayloadContent": true,
      "expectedPayloadLength": 13,
      "echoResponseSize": 10,
      "echoResponseDelay": "1s",
      "streamPayloadSize": "",
      "streamChunkSize": "0",
      "streamChunkCount": 0,
      "streamChunkDelay": "0s",
      "streamDuration": "0s"
    }
  }
}
```

## Unix Domain Sockets (UDS)

`goto` can serve over a Unix domain socket in two ways.

#### At startup
`--uds {path}` serves the primary HTTP port's handler on a socket as well, in addition to the TCP ports:
```
goto --ports 8080 --uds /tmp/goto.sock

curl --unix-socket /tmp/goto.sock http://localhost/version
```
Requests on the socket are handled as requests to the primary port (8080 above).

#### Dynamically, via the listeners API
Add a `uds` path to the listener payload. The listener binds the socket instead of the TCP port:
```
curl -X POST localhost:8080/server/listeners/add \
  --data '{"port":9092, "protocol":"http", "uds":"/tmp/goto-9092.sock", "open":true}'
Listener 9092 added and opened on UDS /tmp/goto-9092.sock.

curl --unix-socket /tmp/goto-9092.sock http://localhost/version

# gRPC and TCP listeners work the same way
curl -X POST localhost:8080/server/listeners/add \
  --data '{"port":9091, "protocol":"grpc", "uds":"/tmp/goto-9091.sock", "open":true}'
```

#### Notes
- `port` is still required: it identifies the listener (for `/server/listeners/{port}/close`, `/open`, `/remove`, listener labels, metrics and logs), but nothing binds that TCP port.
- Supported for `http`, `https`, `grpc`, `grpcs` and `tcp` listeners, including TLS. Not supported for `udp`.
- A socket path can only be used by one listener, and can't be the path given to `--uds`. Such requests are rejected.
- The socket file is created with permissions `0660`. A stale socket file left by a previous run is replaced, but a path that exists and is not a socket is rejected and left untouched.
- Closing or removing the listener, or stopping the server, removes the socket file. Closing a listener also drops its open connections. The close is logged as `Closing http listener: 9092 (UDS: /tmp/goto-9092.sock)`.
- Clients on a socket have no IP address, so their remote address shows up empty or as `@` in logs and in TCP proxy/session tracking.
