### TCP API Examples:

```
curl localhost:8080/server/listeners/add --data '{"label":"tcp-9000", "port":9000, "protocol":"tcp", "open":true}'

curl localhost:8080/server/tcp/9000/configure --data '{"readTimeout":"1m","writeTimeout":"1m","connectTimeout":"15s","connIdleTimeout":"1m", "connectionLife":"2m", "echo":true, "echoResponseSize":10, "echoResponseDelay": "1s"}'

curl localhost:8080/server/tcp/9000/configure --data '{"stream": true, "streamDuration":"5s", "streamChunkDelay":"1s", "streamPayloadSize": "2K", "streamChunkSize":"250", "streamChunkCount":15}'

curl -X PUT localhost:8080/server/tcp/9000/mode/echo=n

curl -X PUT localhost:8080/server/tcp/9000/mode/stream=y

curl -X POST localhost:8080/server/tcp/9000/stream/payload=1K/duration=30s/delay=1s

curl -X PUT localhost:8080/server/tcp/9000/expect/payload/length=10

curl -X PUT localhost:8080/server/tcp/9000/expect/payload --data 'SomePayload'
```

### TCP Status APIs Output Example

```
curl -s localhost:8080/server/tcp/history | jq
{
  "9000": {
    "1": {
      "config": {
        "readTimeout": "",
        "writeTimeout": "",
        "connectTimeout": "",
        "connIdleTimeout": "",
        "connectionLife": "",
        "stream": false,
        "echo": false,
        "conversation": false,
        "silentLife": false,
        "closeAtFirstByte": false,
        "validatePayloadLength": true,
        "validatePayloadContent": false,
        "expectedPayloadLength": 10,
        "echoResponseSize": 100,
        "echoResponseDelay": "",
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
        "connStartTime": "2020-12-05T15:05:50.748382-08:00",
        "connCloseTime": "2020-12-05T15:06:20.754224-08:00",
        "firstByteInAt": "2020-12-05T15:05:56.078853-08:00",
        "lastByteInAt": "2020-12-05T15:05:56.078853-08:00",
        "firstByteOutAt": "2020-12-05T15:06:20.754152-08:00",
        "lastByteOutAt": "2020-12-05T15:06:20.754152-08:00",
        "totalBytesRead": 10,
        "totalBytesSent": 81,
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
        "readTimeout": "1m",
        "writeTimeout": "1m",
        "connectTimeout": "15s",
        "connIdleTimeout": "1m",
        "connectionLife": "1m",
        "stream": false,
        "echo": false,
        "conversation": true,
        "silentLife": false,
        "closeAtFirstByte": false,
        "validatePayloadLength": false,
        "validatePayloadContent": false,
        "expectedPayloadLength": 0,
        "echoResponseSize": 100,
        "echoResponseDelay": "",
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
        "connStartTime": "2020-12-05T15:06:14.669709-08:00",
        "connCloseTime": "2020-12-05T15:06:19.247841-08:00",
        "firstByteInAt": "2020-12-05T15:06:16.51267-08:00",
        "lastByteInAt": "2020-12-05T15:06:19.247753-08:00",
        "firstByteOutAt": "2020-12-05T15:06:16.512726-08:00",
        "lastByteOutAt": "2020-12-05T15:06:19.247801-08:00",
        "totalBytesRead": 12,
        "totalBytesSent": 12,
        "totalReads": 2,
        "totalWrites": 2,
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
