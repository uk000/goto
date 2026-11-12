### Listener API Examples:

All the listener APIs are served under `/server/listeners` (the singular `/server/listener` works too). The `Listener JSON Schema` is in the [Server Listeners](../pkg/server/README.md#listener-json-schema) doc.

```
#HTTP listener that is opened as soon as it is added
curl localhost:8080/server/listeners/add --data '{"port":8081, "protocol":"http", "label":"Server-8081", "open":true}'

#HTTP listener that is only added ("open" is false when it is left out, so open it with the /open call below)
curl localhost:8080/server/listeners/add --data '{"port":8082, "protocol":"http", "label":"Server-8082"}'

#HTTPS listener with auto-generated cert using common name `foo.com`
curl -s localhost:8080/server/listeners/add --data '{"label":"https-8443", "port":8443, "protocol":"https", "open":true, "autoCert":true, "commonName":"foo.com"}'

#TCP Listener. The TCP behaviour is configured with the TCP APIs once the listener exists.
curl -s localhost:8080/server/listeners/add --data '{"label":"tcp-9000", "port":9000, "protocol":"tcp", "open":true}'
curl -s localhost:8080/server/tcp/9000/configure --data '{"readTimeout":"15s","writeTimeout":"15s","connectTimeout":"15s","connIdleTimeout":"20s","echo":true,"echoResponseDelay":"1s","connectionLife":"20s"}'

#gRPC listener
curl localhost:8080/server/listeners/add --data '{"port":9091, "protocol":"grpc", "label":"GRPC-9091"}'

#TCP listener that forwards its connections to the listener on port 8443
curl localhost:8080/server/listeners/add --data '{"port":8000, "protocol":"tcp", "open":true, "forward":8443}'

#the listener on port 8000 now relays whatever it receives to the listener on port 8443

curl -X POST localhost:8080/server/listeners/8081/remove

curl -X PUT localhost:8080/server/listeners/9000/open

curl -X PUT localhost:8080/server/listeners/9000/close

curl -X PUT localhost:8080/server/listeners/9000/reopen

curl localhost:8080/server/listeners

curl localhost:8080/server/listeners/9000

curl localhost:8080/server/listeners/ports
```

The calls answer with a one-line text message. The messages that you will see:

```
Listener 8082 added.
Listener 8081 added and opened.
Listener 8082 already present, restarted.        (add or update on a port that has a listener)
Listener 8082 already present, failed to restart.
Listener 8082 added but failed to open.          (still status 200)
Listener on port 8082 closed
Listener opened on port 8082
Listener reopened on port 8082
Listener on port 8082 removed
Port 8082 not open                               (close of a listener that is not open, status 400)
Port 8099: No listener on the port, or listener not closeable   (status 400, any call on a port that has no listener)
[Invalid port number: 70000]                     (status 400)
Failed to parse json with error: unexpected end of JSON input   (status 400)
```

Note that a `protocol` that `goto` does not know (for example a typo like `htp`) is not rejected: the listener is created as a `tcp` listener.

### Listener Output Example

The output of `GET /server/listeners` has one entry for each listener, by port. A listener that has been added but not opened reports `open: false`, an empty `listenerID` and `generation` 0 (port 8082 and 9091 below). The `tcp` field is there for TCP listeners only. The `serverCert` field is there for TLS listeners, and describes the certificate that the listener serves.

```
$ curl -s localhost:8080/server/listeners

{
  "8081": {
    "listenerID": "8081-1",
    "label": "Server-8081",
    "hostLabel": "pod.ns[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)",
    "port": 8081,
    "forward": 0,
    "protocol": "http",
    "l8Proto": "h2c",
    "alpn": null,
    "open": true,
    "autoCert": false,
    "autoSNI": false,
    "commonName": "",
    "spiffeID": "",
    "altNames": null,
    "tls": false,
    "mTLS": false,
    "verifyClientCert": false,
    "isHTTP": true,
    "isH2": true,
    "isGRPC": false,
    "isJSONRPC": false,
    "isTCP": false,
    "isUDP": false,
    "isXDS": false,
    "generation": 1,
    "serverCert": null
  },
  "8082": {
    "listenerID": "",
    "label": "Server-8082",
    "hostLabel": "pod.ns[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)",
    "port": 8082,
    "forward": 0,
    "protocol": "http",
    "l8Proto": "h2c",
    "alpn": null,
    "open": false,
    "autoCert": false,
    "autoSNI": false,
    "commonName": "",
    "spiffeID": "",
    "altNames": null,
    "tls": false,
    "mTLS": false,
    "verifyClientCert": false,
    "isHTTP": true,
    "isH2": true,
    "isGRPC": false,
    "isJSONRPC": false,
    "isTCP": false,
    "isUDP": false,
    "isXDS": false,
    "generation": 0,
    "serverCert": null
  },
  "8443": {
    "listenerID": "8443-1",
    "label": "https-8443",
    "hostLabel": "pod.ns[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)",
    "port": 8443,
    "forward": 0,
    "protocol": "https",
    "l8Proto": "h2",
    "alpn": null,
    "open": true,
    "autoCert": true,
    "autoSNI": false,
    "commonName": "foo.com",
    "spiffeID": "",
    "altNames": null,
    "tls": true,
    "mTLS": false,
    "verifyClientCert": false,
    "isHTTP": true,
    "isH2": true,
    "isGRPC": false,
    "isJSONRPC": false,
    "isTCP": false,
    "isUDP": false,
    "isXDS": false,
    "generation": 1,
    "serverCert": {
      "startAt": "2026-11-07T16:05:58.785843-08:00",
      "endAt": "0001-01-01T00:00:00Z",
      "finished": false,
      "remoteAddr": "",
      "subject": "org=[foo.com],co=[],cn=[foo.com]",
      "dnsNames": [
        "foo.com"
      ],
      "issuer": "org=[foo.com],co=[],sn=[]"
    }
  },
  "9000": {
    "listenerID": "9000-1",
    "label": "tcp-9000",
    "hostLabel": "pod.ns[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)",
    "port": 9000,
    "forward": 0,
    "protocol": "tcp",
    "l8Proto": "tcp",
    "alpn": null,
    "open": true,
    "autoCert": false,
    "autoSNI": false,
    "commonName": "",
    "spiffeID": "",
    "altNames": null,
    "tls": false,
    "mTLS": false,
    "verifyClientCert": false,
    "tcp": {
      "tls": false,
      "mtls": false,
      "readTimeout": "15s",
      "writeTimeout": "15s",
      "connectTimeout": "15s",
      "connIdleTimeout": "20s",
      "connectionLife": "20s",
      "keepOpen": false,
      "payload": false,
      "stream": false,
      "echo": true,
      "conversation": false,
      "silentLife": false,
      "closeAtFirstByte": false,
      "validatePayloadLength": false,
      "validatePayloadContent": false,
      "expectedPayloadLength": 0,
      "echoResponseSize": 10,
      "echoResponseDelay": "1s",
      "responsePayloads": null,
      "responseDelay": "",
      "respondAfterRead": false,
      "streamPayloadSize": "",
      "streamChunkSize": "0",
      "streamChunkCount": 0,
      "streamChunkDelay": "0s",
      "streamDuration": "0s"
    },
    "isHTTP": false,
    "isH2": false,
    "isGRPC": false,
    "isJSONRPC": false,
    "isTCP": true,
    "isUDP": false,
    "isXDS": false,
    "generation": 1,
    "serverCert": null
  },
  "9091": {
    "listenerID": "",
    "label": "GRPC-9091",
    "hostLabel": "pod.ns[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)",
    "port": 9091,
    "forward": 0,
    "protocol": "grpc",
    "l8Proto": "grpc",
    "alpn": null,
    "open": false,
    "autoCert": false,
    "autoSNI": false,
    "commonName": "",
    "spiffeID": "",
    "altNames": null,
    "tls": false,
    "mTLS": false,
    "verifyClientCert": false,
    "isHTTP": false,
    "isH2": false,
    "isGRPC": true,
    "isJSONRPC": false,
    "isTCP": false,
    "isUDP": false,
    "isXDS": false,
    "generation": 0,
    "serverCert": null
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

`GET /server/listeners/ports` is a smaller view of the same thing, with the `l8Proto` of each port: `{"8081":"h2c","8082":"h2c","8443":"h2","9000":"tcp","9091":"grpc"}`. (A real instance also lists its bootstrap, JSON-RPC and gRPC ports.)
