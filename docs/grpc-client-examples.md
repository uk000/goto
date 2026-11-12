# GRPC Client Example
This example shows usage of `goto` as a client for sending GRPC traffic.

1. Launch a `goto` instance to be used as a client.
    ```
    $ goto --ports 8080
    ```

2. The purpose of the `goto` client is to test some upstream service. However, for now we'll use another `goto` instance as the upstream service to test. We're launching this instance with an insecure `GRPC` port `9000` and a secure `GRPC` port `9001`.
    ```
    $ goto --ports 8081,9000/grpc,9001/grpcs
    ```

`Goto` exposes a GRPC service named, well, `Goto`. The service proto (`pkg/rpc/grpc/protos/goto.proto`) is given below for reference.

      ```
      syntax = "proto3";
      option go_package=".;pb";

      message Input {
        string text = 1;
      }

      message StreamConfig {
        int32  chunkSize = 1;
        int32  chunkCount = 2;
        string interval = 3;
        string text = 4;
      }

      message Output {
        string id = 1;
        string text = 2;
        string at = 3;
        string gotoHost = 4;
        int32  gotoPort = 5;
        string viaGoto = 6;
        string gotoTLS = 7;
        string requestURI = 8;
        map<string, string> requestHeaders = 9;
        map<string, string> responseHeaders = 10;
      }

      service Goto {
        rpc echo(Input) returns (Output) {}
        rpc streamIn(stream Input) returns (Output) {}
        rpc streamOut(StreamConfig) returns (stream Output) {}
        rpc streamInOut(stream StreamConfig) returns (stream Output) {}
      }
      ```

The same four methods are also declared by two more services, `Goto2` and `Goto3`, but those two have no implementation: calls to them are answered with `UNIMPLEMENTED` unless they are proxied to a service that is implemented (see the [gRPC proxy](../pkg/proxy/grpc/README.md)). Use `Goto` for the examples below.

3. The proto definition above is what connects the dots: the client needs to know the service methods and their in/out parameters. The `goto` client learns them through the gRPC server reflection of the upstream (every `goto` GRPC listener serves reflection), so there is nothing to upload for this example. (Uploading a proto file with `curl -X POST localhost:8080/grpc/protos/add/goto --data-binary @./goto.proto`, which answers `Proto [goto] stored`, is for making `goto` *serve* a service from the proto; see the [gRPC server APIs](../pkg/rpc/README.md#generic-grpc-server).)

4. We can use the service name and its method names in an invocation spec. So let's add a GRPC target to the `goto` client that'll invoke `echo` method on the plain text GRPC port `9000`. Note the `body` field in the invocation spec below, it must provide a valid JSON to match the service method input (the field of `Input` is `text`). We're also adding two headers, `foo` and `bar`, to the call. The `headers` of a target are a map of header name to value. 
    > You can check the client invocation schema docs for additional configurations at [Client JSON Schemas](client-api-json-schemas.md).

    ```
    $ curl -s localhost:8080/client/targets/add --data '{"name": "grpc-target-echo", "protocol": "grpc", "service": "Goto", "method": "echo", "url": "dns:localhost:9000", "headers": {"foo":"1","bar":"2"}, "body": "{\"text\": \"hello\"}", "replicas": 1, "requestCount": 1, "connTimeout":"10s", "requestTimeout":"10s", "autoInvoke":false}'

    Added target: {"name":"grpc-target-echo" ....
    ```

5. We could have set the `autoInvoke` field in the invocation spec above to `true`, in which case `goto` client would have started the client traffic automatically. For now, let's trigger the client traffic.
    ```
    $ curl -X POST localhost:8080/client/targets/grpc-target-echo/invoke
    ```

If you observe the client and service logs, you should see that the client made a GRPC call and got a response from the service, while the service logs should show that it served a GRPC echo call. 

6. Let's check the invocation results on the client.
    ```
    $ curl -s localhost:8080/client/results | jq
    {
      ...
      "grpc-target-echo": {
        "target": "grpc-target-echo",
        "invocationCount": 1,
        "firstResultAt": "...",
        "lastResultAt": "...",
        "clientStreamCount": 0,
        "serverStreamCount": 0,        
        "retriedInvocationCounts": 0,
        "countsByStatus": {
          "200": 1
        },
        "countsByStatusCodes": {
          "200": {
            "count": 1,
            "clientStreamCount": 0,
            "serverStreamCount": 0,        
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "..."
          }
        },
        "countsByURIs": {
          "echo": {
            "count": 1,
            "clientStreamCount": 0,
            "serverStreamCount": 0,        
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "...",
            "byStatusCodes": {
              "200": {
                "count": 1,
                "clientStreamCount": 0,
                "serverStreamCount": 0,        
                "retries": 0,
                "firstResultAt": "...",
                "lastResultAt": "..."
              }
            }
          }
        },
        "countsByResponsePayloadSizes": {
          ...
        }
      }
    }
    ```
> The `goto` client uses the GRPC method name as URI since it tries to fit the GRPC results into the same schema used for HTTP calls. The client is capable of tracking results broken down by URIs, headers, payload sizes, response status codes, etc. Check the client docs for more details.

<br/><br/>

# GRPC Client Example with Stream
This is an example of using `goto` client against a streaming GRPC service (bi-directional).

1. Same as previous example, we'll launch two `goto` instances, one as a client and another as a GRPC service.
    ```
    $ goto --ports 8080

    $ goto --ports 8081,9000/grpc,9001/grpcs
    ```

2. As in the previous example, there is nothing to upload to the client: it reads the service definition from the reflection of the upstream.

3. We want the `goto` client to work in stream mode this time, which means it should stream request payload to the service and receive response payload as a stream. `Goto` GRPC service exposes the method `streamInOut` that provides bi-directional streaming for testing. Before we proceed with the client config, let's see this service method in action so that we know what the client will be getting back from the service. 
    > You can use grpcli instead of grpcurl too
    ```
    $  grpcurl -v -plaintext -d '{"chunkSize": 10, "chunkCount": 3, "interval": "1s", "text": "hello"}' localhost:9000 Goto.streamInOut

      Resolved method descriptor:
      rpc streamInOut ( stream .StreamConfig ) returns ( stream .Output );
      ...
      Response headers received:
      content-type: application/grpc
      goto-host: ...
      goto-port: 9000
      goto-protocol: GRPC
      goto-remote-address: ...
      request-authority: localhost:9000
      request-content-type: application/grpc
      request-user-agent: grpcurl/v1.8.6 grpc-go/1.44.1-dev
      via-goto: ...

      Estimated response size: 104 bytes

      Response contents:
      {
        "id": "...",
        "text": "hello(1/3)",
        "at": "...",
        "gotoHost": "...",
        "gotoPort": 9000,
        "viaGoto": "...",
        "requestURI": "/Goto/streamInOut",
        ...
      }

      Estimated response size: 104 bytes

      Response contents:
      {
        "id": "...",
        "text": "hello(2/3)",
        "at": "...",
        "gotoHost": "...",
        "gotoPort": 9000,
        "viaGoto": "...",
        "requestURI": "/Goto/streamInOut",
        ...
      }

      Estimated response size: 104 bytes

      Response contents:
      {
        "id": "...",
        "text": "hello(3/3)",
        "at": "...",
        "gotoHost": "...",
        "gotoPort": 9000,
        "viaGoto": "...",
        "requestURI": "/Goto/streamInOut",
        ...
      }
    ```

    We see 3 responses coming back from the service because our input to the service asked for the response to be streamed back in 3 chunks (`chunkCount`: 3). The text of each chunk is the given `text` (a random string of `chunkSize` characters if no text is given) followed by the chunk number, e.g. `hello(1/3)`.

4. Let's add a GRPC target to the `goto` client that'll invoke the `streamInOut` method on the plain text GRPC port `9000`. 
    - The protocol is `grpc`. (A secure call would use the protocol `grpcs` against a `grpcs` listener, but a `grpcs` listener of `goto` doesn't negotiate the HTTP/2 ALPN protocol at present, which the `goto` client requires, so such a call fails with a TLS handshake error such as `missing selected ALPN property`.)
    - Note the use of `streamPayload` instead of `body`. The `streamPayload` field captures an array of text payloads that the client will stream to the upstream service, one record at a time. The service method we're invoking is a `bidi` stream. 
    - Each item in the `streamPayload` array should be a valid input JSON matching the service method input schema.
    - Across the 3 inputs we send, we're asking for `chunkCount` of `3+2+3 = 8` response stream chunks from the GRPC service.
      > The `streamInOut` method of `goto` GRPC service accepts one or more config objects as input stream, and streams data back based on each of the input configs in succession.

      > At present the `goto` client sends each item of `streamPayload` as a stream call of its own (the service logs show three `streamInOut` calls, each with one config), so the chunks of the three configs come back as three separate responses streams, and only the first of them is counted in the client results below. To send several messages in one client stream, use the `/grpc/client/call` API with a `stream` payload, see the [gRPC client APIs](../pkg/rpc/README.md#generic-grpc-client).

      > You can check the client invocation schema docs for additional configurations at [Client JSON Schemas](client-api-json-schemas.md).

    ```
    $ curl -s localhost:8080/client/targets/add --data '{"name": "grpc-target-stream", "protocol": "grpc", "service": "Goto", "method": "streamInOut", "url": "dns:localhost:9000", "headers": {"foo":"123","bar":"456"}, "streamPayload": ["{\"chunkSize\": 10, \"chunkCount\": 3, \"interval\": \"1s\", \"text\": \"hello\"}", "{\"chunkSize\": 10, \"chunkCount\": 2, \"interval\": \"1s\", \"text\": \"hello\"}", "{\"chunkSize\": 10, \"chunkCount\": 3, \"interval\": \"2s\", \"text\": \"hello\"}"], "replicas": 1, "requestCount": 1, "autoInvoke":true}'

    Added target: {"name":"grpc-target-echo" ....
    ```

5. The target was marked with `autoInvoke:true`, so `goto` client will start the traffic immediately. 
    
    On the client `goto`, we should see invocation logs like these:
    ```
    Invocation[1]: Started target [grpc-target-stream] with total requests [1]
    Invocation[1]: Request[grpc-target-stream[1][1]]: Invoking targetID [grpc-target-stream], url [dns:localhost:9000], method [streamInOut], headers [...foo:123 bar:456...]
    Invocation[1]: Target [grpc-target-stream], url [dns:localhost:9000], burls [], Response Status [200] Repeated x[1]
    Invocation[1]: finished for target [grpc-target-stream] with remaining requests [0]
    ```

    On the `goto` GRPC service instance, we should 3 such log entries (because we sent 3 inputs in our client stream):
    ```
     GRPC[9000]: Serving StreamInOut with config [chunkSize: 10, chunkCount: 3, interval: 1s, payload size: [5]]
    ```
    And finally the server should show an access log entry for each call, like:
    ```
    gRPC Access Log: [...]: Protocol: [GRPC] Service [Goto] Method [streamInOut] RemoteAddr [...], Request{ Host: [localhost:9000], Count [1], Content Length [0], Request Headers: [{":authority":"localhost:9000","bar":"456","content-type":"application/grpc","foo":"123", ...}] } --> Served StreamInOut with total chunks [3] and total payload length [...] --> Response Status [200], Response Body Length [...]
    ```

6. Just for fun, let's add update the target to invoke the `streamOut` method on the upstream `goto` GRPC service. This method provides a way to test GRPC calls that send regular payload from client (not a stream) but receive stream response payload back.
    - Note the use of `body` now instead of `streamPayload` since we're not going to send a stream from client. 
    - The contents of the request body in this call is equivalent of a single item from the stream array in the previous target spec.
    - Also this time we're asking for `replicas=2`, each to send `requestCount=3`, so total 6 requests to be sent to the service. Each request asks for `chunkCount=3`, so total `18` response stream chunks should come back from the GRPC service.
    - We could have created another target for this second GRPC invocation, and quite likely that's what you'd do in real life. However, by changing URI of the same target, we will get combined results of the two calls as you'll see later.
    ```
    $ curl -s localhost:8080/client/targets/add --data '{"name": "grpc-target-stream", "protocol": "grpc", "service": "Goto", "method": "streamOut", "url": "dns:localhost:9000", "headers": {"foo":"123","bar":"456"}, "body": "{\"chunkSize\": 10, \"chunkCount\": 3, \"interval\": \"1s\"}", "replicas": 2, "requestCount": 3, "autoInvoke":true}'

    Added target: {"name":"grpc-target-echo" ....
    ```


7. You can check the invocation results on the client.
    - Note the total invocation count `invocationCount=7`. The `streamInOut` call was a single client thread, but for the `streamOut` call we had asked for 6 client threads.
    - `clientStreamCount=7` counts the messages sent by the client: `1` for the `streamInOut` call (the first item of the stream array, see the note above) and one for each of the `6` `streamOut` calls.
    - `serverStreamCount=21` includes `3` responses for `streamInOut` (the chunks of its first config) and `18` responses for `streamOut`.
    - `countsByURIs` section breaks it down by URIs, which in this case are GRPC methods.
    - `Goto` GRPC client reports successful executions under status code `200`.
      > Failures would get reported under status code `0` in most cases.
    - `countsByResponsePayloadSizes` is omitted in the sample results below. This is where `goto` client tracks results broken down by response payload size.
    ```
    $ curl -s localhost:8080/client/results | jq
    {
      "grpc-target-stream": {
        "target": "grpc-target-stream",
        "invocationCount": 7,
        "firstResultAt": "...",
        "lastResultAt": "...",
        "clientStreamCount": 7,
        "serverStreamCount": 21,
        "retriedInvocationCounts": 0,
        "countsByStatus": {
          "200": 7
        },
        "countsByStatusCodes": {
          "200": {
            "count": 7,
            "clientStreamCount": 7,
            "serverStreamCount": 21,
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "..."
          }
        },
        "countsByURIs": {
          "streaminout": {
            "count": 1,
            "clientStreamCount": 1,
            "serverStreamCount": 3,
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "...",
            "byStatusCodes": {
              "200": {
                "count": 1,
                "clientStreamCount": 1,
                "serverStreamCount": 3,
                "retries": 0,
                "firstResultAt": "...",
                "lastResultAt": "..."
              }
            }
          },
          "streamout": {
            "count": 6,
            "clientStreamCount": 6,
            "serverStreamCount": 18,
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "...",
            "byStatusCodes": {
              "200": {
                "count": 6,
                "clientStreamCount": 6,
                "serverStreamCount": 18,
                "retries": 0,
                "firstResultAt": "...",
                "lastResultAt": "..."
              }
            }
          }
        },
        "countsByResponsePayloadSizes": {
          ...
        }
      }
    }
    ```

This example shows how `goto` client can be used to test an arbitrary GRPC service and track the results broken down by service, method, success/failures, headers, payload size, etc. See [client docs](../pkg/client/README.md) for examples of HTTP target results tracking by headers. The same approach will work for GRPC calls too for tracking results by headers.