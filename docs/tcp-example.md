### TCP API Examples:

```
curl localhost:8080/server/listeners/add --data '{"label":"tcp-9000", "port":9000, "protocol":"tcp", "open":true}'

#response payload mode config example
curl localhost:8080/server/tcp/9000/configure --data '{"payload": true, "responsePayloads": ["HTTP/1.1 200 OK\r\nServer: goto\r\nContent-Type: text/plain\r\nContent-Length:11\r\nConnection: Keep-Alive\r\nFoo: Bar\r\n\nHello Goto!"], "responseDelay":"2s", "respondAfterRead": true, "keepOpen": true, "connectionLife": "10s"}'

#echo mode config example
curl localhost:8080/server/tcp/9000/configure --data '{"readTimeout":"1m","writeTimeout":"1m","connectTimeout":"15s","connIdleTimeout":"1m", "connectionLife":"2m", "echo":true, "echoResponseSize":10, "echoResponseDelay": "1s"}'

#stream mode config example
curl localhost:8080/server/tcp/9000/configure --data '{"stream": true, "streamDuration":"5s", "streamChunkDelay":"1s", "streamPayloadSize": "2K", "streamChunkSize":"250", "streamChunkCount":15}'

curl -X PUT localhost:8080/server/tcp/9000/set/payload=n

curl -X PUT localhost:8080/server/tcp/9000/mode/echo=n

curl -X PUT localhost:8080/server/tcp/9000/mode/stream=y

curl -X POST localhost:8080/server/tcp/9000/stream/payload=1K/duration=30s/delay=1s

curl -X PUT localhost:8080/server/tcp/9000/expect/payload/length=10

curl -X PUT localhost:8080/server/tcp/9000/expect/payload --data 'SomePayload'

#validate the content that clients send against the payload stored by the expect/payload call above
curl -X PUT localhost:8080/server/tcp/9000/mode/validate=y

#timeouts and connection life
curl -X PUT localhost:8080/server/tcp/9000/timeout/set/read=45s
curl -X PUT localhost:8080/server/tcp/9000/connection/set/life=2m
curl -X PUT localhost:8080/server/tcp/9000/echo/response/set/delay=250ms

#look at the connections that were served
curl -s localhost:8080/server/tcp/9000/history
curl -s localhost:8080/server/tcp/active
curl -X POST localhost:8080/server/tcp/9000/history/clear
```

A TCP listener is configured through `/server/tcp/{port}/...`. The older `/tcp/{port}/...` form that this page used to show does not exist and answers `404`.

### Talking to the listener

```
#echo mode (the default when nothing else is configured): the listener sends back the first {echoResponseSize}
#bytes that it received (10 by default), as they are
$ printf 'hello world' | nc localhost 9000
hello worl

#payload validation ({"validatePayloadLength":true,"expectedPayloadLength":10}): the verdict is sent when the client
#half-closes the connection (nc does that at the end of its input), or when connectionLife ends
$ printf '0123456789' | nc localhost 9000
[host.local[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)][SUCCESS]: Received payload matches expected payload of length [10] on port [9000]

$ printf '01234' | nc localhost 9000
[host.local[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)][ERROR:TIMEOUT] - Timed out before receiving payload of expected length [10] on port [9000]
```

### TCP Status APIs Output Example

Connection 1 below was served by a listener configured with `{"validatePayloadLength":true,"expectedPayloadLength":10,"connectionLife":"3s"}` and sent 10 bytes, so its verdict was written when the 3s life ended (`lifeTimeout` is true). Connection 2 was served in `conversation` mode. Each entry has the `config` that was in force when the connection was accepted and the `status` of the connection. The `connectionLife`, `*Timeout` and `echoResponseSize` values are the effective values after the defaults were applied.

```
curl -s localhost:8080/server/tcp/9000/history | jq
{
  "9000": {
    "1": {
      "config": {
        "tls": false,
        "mtls": false,
        "readTimeout": "1m",
        "writeTimeout": "1m",
        "connectTimeout": "15s",
        "connIdleTimeout": "1m",
        "connectionLife": "3s",
        "keepOpen": false,
        "payload": false,
        "stream": false,
        "echo": false,
        "conversation": false,
        "silentLife": false,
        "closeAtFirstByte": false,
        "validatePayloadLength": true,
        "validatePayloadContent": false,
        "expectedPayloadLength": 10,
        "echoResponseSize": 10,
        "echoResponseDelay": "",
        "responsePayloads": null,
        "responseDelay": "",
        "respondAfterRead": false,
        "streamPayloadSize": "",
        "streamChunkSize": "0",
        "streamChunkCount": 0,
        "streamChunkDelay": "0s",
        "streamDuration": "0s"
      },
      "status": {
        "port": 9000,
        "listenerID": "9000-1",
        "requestID": 1,
        "connStartTime": "2026-11-07T15:58:54.759754-08:00",
        "connCloseTime": "2026-11-07 23:58:57.761113 +0000 UTC",
        "firstByteInAt": "2026-11-07 23:58:54.759827 +0000 UTC",
        "lastByteInAt": "2026-11-07 23:58:54.759827 +0000 UTC",
        "firstByteOutAt": "2026-11-07 23:58:57.761058 +0000 UTC",
        "lastByteOutAt": "2026-11-07 23:58:57.761058 +0000 UTC",
        "totalBytesRead": 10,
        "totalBytesSent": 151,
        "totalReads": 2,
        "totalWrites": 1,
        "closed": true,
        "clientClosed": false,
        "serverClosed": true,
        "errorClosed": false,
        "readTimeout": false,
        "idleTimeout": false,
        "lifeTimeout": true,
        "writeErrors": 0
      }
    },
    "2": {
      "config": {
        "tls": false,
        "mtls": false,
        "readTimeout": "1m",
        "writeTimeout": "1m",
        "connectTimeout": "15s",
        "connIdleTimeout": "1m",
        "connectionLife": "1m",
        "keepOpen": false,
        "payload": false,
        "stream": false,
        "echo": false,
        "conversation": true,
        "silentLife": false,
        "closeAtFirstByte": false,
        "validatePayloadLength": false,
        "validatePayloadContent": false,
        "expectedPayloadLength": 0,
        "echoResponseSize": 10,
        "echoResponseDelay": "",
        "responsePayloads": null,
        "responseDelay": "",
        "respondAfterRead": false,
        "streamPayloadSize": "",
        "streamChunkSize": "0",
        "streamChunkCount": 0,
        "streamChunkDelay": "0s",
        "streamDuration": "0s"
      },
      "status": {
        "port": 9000,
        "listenerID": "9000-1",
        "requestID": 2,
        "connStartTime": "2026-11-07T15:58:57.846128-08:00",
        "connCloseTime": "2026-11-07 23:58:57.846641 +0000 UTC",
        "firstByteInAt": "2026-11-07 23:58:57.846205 +0000 UTC",
        "lastByteInAt": "2026-11-07 23:58:57.846503 +0000 UTC",
        "firstByteOutAt": "2026-11-07 23:58:57.846238 +0000 UTC",
        "lastByteOutAt": "2026-11-07 23:58:57.84653 +0000 UTC",
        "totalBytesRead": 24,
        "totalBytesSent": 226,
        "totalReads": 3,
        "totalWrites": 3,
        "closed": true,
        "clientClosed": false,
        "serverClosed": false,
        "errorClosed": false,
        "readTimeout": false,
        "idleTimeout": false,
        "lifeTimeout": false,
        "writeErrors": 0
      }
    }
  }
}
```
