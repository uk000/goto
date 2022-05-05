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

`Goto` exposes a GRPC service named, well, `Goto`. The service proto is given below for reference.

      ```
      syntax = "proto3";
      option go_package=".;pb";

      message Input {
        string payload = 1;
      }

      message StreamConfig {
        int32  chunkSize = 1;
        int32  chunkCount = 2;
        string interval = 3;
        string payload = 4;
      }

      message Output {
        string payload = 1;
        string at = 2;
        string gotoHost = 3;
        int32  gotoPort = 4;
        string viaGoto = 5;
      }

      service Goto {
        rpc echo(Input) returns (Output) {}
        rpc streamOut(StreamConfig) returns (stream Output) {}
        rpc streamInOut(stream StreamConfig) returns (stream Output) {}
      }
      ```

3. The proto definition above is crucial to connect the dots. `Goto` client needs to be fed the proto file so that it knows about the service methods and its in/out parameters.<br/>
Let's upload the proto file to the client, assuming the file is saved in the current working folder under the name `goto.proto`.
    ```
    curl -X POST localhost:8080/grpc/protos/add/goto --data-binary @./goto.proto

    Proto [goto] stored
    ```

4. Once the `goto` client has been given the proto file, we can use the service name and its method names in an invocation spec. So let's add a GRPC target to the `goto` client that'll invoke `echo` method on the plain text GRPC port `9000`. Note the `body` field in the invocation spec below, it must provide a valid JSON to match the service method input. We're also adding two headers, `foo` and `bar`, to the call. 
    > You can check the client invocation schema docs for additional configurations at [Client JSON Schemas](client-api-json-schemas.md).

    ```
    $ curl -s localhost:8080/client/targets/add --data '{"name": "grpc-target-echo", "protocol": "grpc", "service": "Goto", "method": "echo", "url": "dns:localhost:9000", "headers": [["foo","1"],["bar", "2"]], "body": "{\"payload\": \"hello\"}", "replicas": 1, "requestCount": 1, "connTimeout":"10s", "requestTimeout":"10s", "autoInvoke":false}'

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
          "160": {
            "count": 1,
            "clientStreamCount": 0,
            "serverStreamCount": 0,        
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "...",
            "byStatusCodes": {
              "200": {
                "count": 1,
                "retries": 0,
                "clientStreamCount": 0,
                "serverStreamCount": 0,        
                "firstResultAt": "...",
                "lastResultAt": "..."
              }
            }
          }
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

2. We need to upload the proto file from the previous example to the client.
    ```
    curl -X POST localhost:8080/grpc/protos/add/goto --data-binary @./goto.proto

    Proto [goto] stored
    ```

3. We want the `goto` client to work in stream mode this time, which means it should stream request payload to the service and receive response payload as a stream. `Goto` GRPC service exposes the method `streamInOut` that provides bi-directional streaming for testing. Before we proceed with the client config, let's see this service method in action so that we know what the client will be getting back from the service. 
    > You can use grpcli instead of grpcurl too
    ```
    $  grpcurl -v -insecure -d '{"chunkSize": 10, "chunkCount": 3, "interval": "1s", "payload": "hello"}' localhost:9001 Goto.streamInOut

      Resolved method descriptor:
      rpc streamInOut ( stream .StreamConfig ) returns ( stream .Output );
      ...
      Response headers received:
      content-type: application/grpc
      goto-host: ...
      goto-port: 9001
      goto-protocol: GRPC
      goto-remote-address: ...
      request-authority: localhost:9001
      request-content-type: application/grpc
      request-user-agent: grpcurl/v1.8.6 grpc-go/1.44.1-dev
      via-goto: ...

      Estimated response size: 104 bytes

      Response contents:
      {
        "payload": "hello",
        "at": "...",
        "gotoHost": "...",
        "gotoPort": 9001,
        "viaGoto": "..."
      }

      Estimated response size: 104 bytes

      Response contents:
      {
        "payload": "hello",
        "at": "...",
        "gotoHost": "...",
        "gotoPort": 9001,
        "viaGoto": "..."
      }

      Estimated response size: 104 bytes

      Response contents:
      {
        "payload": "hello",
        "at": "...",
        "gotoHost": "...",
        "gotoPort": 9001,
        "viaGoto": "..."
      }
    ```

    We see 3 responses coming back from the service because our input to the service asked for the response to be streamed back in 3 chunks (`chunkCount`: 3).

4. Let's add a GRPC target to the `goto` client that'll invoke the `streamInOut` method on the TLS GRPC port `9001`. 
    - Since we need this to be a secure call, we specify the protocol as `grpcs`.
    - Note the use of `streamPayload` instead of `body`. The `streamPayload` field captures an array of text payloads that the client will stream to the upstream service, one record at a time. The service method we're invoking is a `bidi` stream. 
    - Each item in the `streamPayload` array should be a valid input JSON matching the service method input schema.
    - Across the 3 inputs we send, we're asking for `chunkCount` of `3+2+3 = 8`, so a total of `8` response stream chunks should come back from the GRPC service.
      > The `streamInOut` method of `goto` GRPC service accepts one or more config objects as input stream, and streams data back based on each of the input configs in succession.

      > You can check the client invocation schema docs for additional configurations at [Client JSON Schemas](client-api-json-schemas.md).

    ```
    $ curl -s localhost:8080/client/targets/add --data '{"name": "grpc-target-stream", "protocol": "grpcs", "service": "Goto", "method": "streamInOut", "url": "dns:localhost:9001", "headers": [["foo","123"],["bar", "456"]], "streamPayload": ["{\"chunkSize\": 10, \"chunkCount\": 3, \"interval\": \"1s\", \"payload\": \"hello\"}", "{\"chunkSize\": 10, \"chunkCount\": 2, \"interval\": \"1s\", \"payload\": \"hello\"}", "{\"chunkSize\": 10, \"chunkCount\": 3, \"interval\": \"2s\", \"payload\": \"hello\"}"], "replicas": 1, "requestCount": 1, "autoInvoke":true}'

    Added target: {"name":"grpc-target-echo" ....
    ```

5. The target was marked with `autoInvoke:true`, so `goto` client will start the traffic immediately. 
    
    On the client `goto`, we should see invocation logs like these:
    ```
    Invocation[1]: Started target [grpc-target-stream] with total requests [1]
    Invocation[1]: Request[grpc-target-stream[1][1]]: Invoking targetID [grpc-target-stream], url [dns:localhost:9001], method [streamInOut], headers [[[foo 123] [bar 456] ...
    Invocation[1]: Target [grpc-target-stream], url [dns:localhost:9001], burls [], Response Status [200] Repeated x[1]
    Invocation[1]: finished for target [grpc-target-stream] with remaining requests [0]
    ```

    On the `goto` GRPC service instance, we should 3 such log entries (because we sent 3 inputs in our client stream):
    ```
     GRPC[9001]: Serving StreamInOut with config [chunkSize: 10, chunkCount: 3, interval: 1s, payload size: [5]]
    ```
    And finally the server should show the conclusive log entry:
    ```
    Goto GRPC: [...] RemoteAddr: [...], RequestHost: [localhost:9001], URI: [/Goto/streamInOut], Protocol: [GRPC], Request Headers: [{":authority":"localhost:9001","bar":"456","content-type":"application/grpc","foo":"123","from-goto": ...] --> Request Body Length [0] --> Served StreamInOut with total chunks [8] and total payload length [40] --> Response Status [200], Response Body Length [40]
    ```

6. Just for fun, let's add update the target to invoke the `streamOut` method on the upstream `goto` GRPC service. This method provides a way to test GRPC calls that send regular payload from client (not a stream) but receive stream response payload back.
    - Note the use of `body` now instead of `streamPayload` since we're not going to send a stream from client. 
    - The contents of the request body in this call is equivalent of a single item from the stream array in the previous target spec.
    - Also this time we're asking for `replicas=2`, each to send `requestCount=3`, so total 6 requests to be sent to the service. Each request asks for `chunkCount=3`, so total `18` response stream chunks should come back from the GRPC service.
    - We could have created another target for this second GRPC invocation, and quite likely that's what you'd do in real life. However, by changing URI of the same target, we will get combined results of the two calls as you'll see later.
    ```
    $ curl -s localhost:8080/client/targets/add --data '{"name": "grpc-target-stream", "protocol": "grpcs", "service": "Goto", "method": "streamOut", "url": "dns:localhost:9001", "headers": [["foo","123"],["bar", "456"]], "body": "{\"chunkSize\": 10, \"chunkCount\": 3, \"interval\": \"1s\"}", "replicas": 2, "requestCount": 3, "autoInvoke":true}'

    Added target: {"name":"grpc-target-echo" ....
    ```


7. You can check the invocation results on the client.
    - Note the total invocation count `invocationCount=7`. The `streamInOut` call was a single client thread, but for the `streamOut` call we had asked for 6 client threads.
    - `clientStreamCount=3` is for the `streamInOut` call where out stream input array had 3 items.
    - `serverStreamCount=26` includes `8` responses for `streamInOut` and `18` responses for `streamOut`.
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
        "clientStreamCount": 3,
        "serverStreamCount": 26,
        "retriedInvocationCounts": 0,
        "countsByStatus": {
          "200": 7
        },
        "countsByStatusCodes": {
          "200": {
            "count": 7,
            "clientStreamCount": 3,
            "serverStreamCount": 26,
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "..."
          }
        },
        "countsByURIs": {
          "streaminout": {
            "count": 1,
            "clientStreamCount": 3,
            "serverStreamCount": 8,
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "...",
            "byStatusCodes": {
              "200": {
                "count": 1,
                "clientStreamCount": 3,
                "serverStreamCount": 8,
                "retries": 0,
                "firstResultAt": "...",
                "lastResultAt": "..."
              }
            }
          },
          "streamout": {
            "count": 6,
            "clientStreamCount": 0,
            "serverStreamCount": 18,
            "retries": 0,
            "firstResultAt": "...",
            "lastResultAt": "...",
            "byStatusCodes": {
              "200": {
                "count": 6,
                "clientStreamCount": 0,
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

This example shows how `goto` client can be used to test an arbitrary GRPC service and track the results broken down by service, method, success/failures, headers, payload size, etc. See [client docs](../README.md#goto-client-targets-and-traffic) for examples of HTTP target results tracking by headers. The same approach will work for GRPC calls too for tracking results by headers.