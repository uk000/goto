### Listener API Examples:

```
curl localhost:8080/listeners/add --data '{"port":8081, "protocol":"http", "label":"Server-8081"}'

curl -s localhost:8080/listeners/add --data '{"label":"tcp-9000", "port":9000, "protocol":"tcp", "open":true, "tcp": {"readTimeout":"15s","writeTimeout":"15s","connectTimeout":"15s","connIdleTimeout":"20s","responseDelay":"1s", "connectionLife":"20s"}}'

curl localhost:8080/listeners/add --data '{"port":9091, "protocol":"grpc", "label":"GRPC-9091"}'

curl -X POST localhost:8080/listeners/8081/remove

curl -X PUT localhost:8080/listeners/9000/open

curl -X PUT localhost:8080/listeners/9000/close

curl -X PUT localhost:8080/listeners/9000/reopen

curl localhost:8080/listeners

```

### Listener Output Example

```
$ curl -s localhost:8080/listeners

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
