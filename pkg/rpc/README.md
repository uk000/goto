# RPC Package

This package contains implementation for gRPC and JSON-RPC protocol.


## > gRPC
All HTTP ports that a `goto` instance listens on (including bootstrap port) support both `HTTP/2` and `gRPC` protocol. Any listener that's created with protocol `grpc` works exclusively in `grpc` mode, not supporting HTTP requests and only responding to the gRPC operations described below.

gRPC support in `Goto` falls into 3 categories:
1. Goto's own gRPC Service
2. Generic gRPC Server that can expose any custom gRPC service from a given proto file
3. Generic gRPC client that can call any remote gRPC service using reflection

All of these 3 gRPC features are described below.

### Goto's gRPC server
Goto exposes a gRPC server with the following RPC methods:

1. `Goto.echo`: Unary grpc method that echoes back the given payload along with headers. The `echo` input message is given below. It responds with a single instance of `Output` message described later.
   ```
   message Input {
     string payload = 1;
   }
   ```
2. `Goto.streamIn`: This is a client-streaming method that accepts a stream of `Input` messages (described above under unary `echo` method). Once client ends the stream, the server responds with a single message of `Output` type (described further down).

3. `Goto.streamOut`: This is a server streaming service method that accepts a `StreamConfig` input message allowing the client to configure the parameters of stream response. It responds with `chunkCount` number of `Output` messages, each output carrying a payload of size `chunkSize`, and there is `interval` delay between two output messages.
   ```
   message StreamConfig {
     int32  chunkSize = 1;
     int32  chunkCount = 2;
     string interval = 3;
     string payload = 4;
   }
   ```
4. `Goto.streamInOut`: This is a bi-directional streaming service method that accepts a stream of `StreamConfig` input messages as described in `streamOut` operation above. Each input `StreamConfig` message requests the server to send a stream response based on the given stream config. For each input message, the service responds with `chunkCount` number of `Output` messages, each output carrying a payload of size `chunkSize`, and there is `interval` delay between two output messages.

All `grpc` operations exposed by `goto` produce the following proto message as output:

```
message Output {
  string id = 1;
  string payload = 2;
  string at = 3;
  string gotoHost = 4;
  int32  gotoPort = 5;
  string viaGoto = 6;
}
```
The gRPC response from `goto` carries the following headers:

- `goto-host`
- `goto-port`
- `goto-protocol`
- `goto-remote-address`
- `goto-rpc`
- `via-goto`
- *request headers echoed back with `request-` prefix
- `request-authority`
- `request-content-type`
- `request-host`

<br/>
<details>
<summary> GRPC Tracking Events </summary>

- `GRPC Server Started`
- `GRPC Server Stopped`
- `GRPC Listener Started`
- `GRPC.echo`
- `GRPC.streamOut.start`
- `GRPC.streamOut.end`
- `GRPC.streamInOut.start`
- `GRPC.streamInOut.end`

</details>


See [Goto GRPC Server Example](../../docs/grpc-server-example.md)

### Generic gRPC Server
Goto's gRPC feature becomes more interesting when it's used as a generic gRPC server that can act as any gRPC service based on a given proto file. This feature also includes a generic Proto registry with REST APIs to let users upload any number of proto files.


#### Proto APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /grpc/protos/add<br/>/`{name}`?path=`{path}` | Adds an uploaded proto file into the registry and on the local filesystem. The `path` param allows users to specify a subdir where the proto file should be saved under goto's CWD. This allows users to upload files that would later be referenced from another proto file and hence must be at a specific path location. |
|POST     | /grpc/protos/store<br/>/`{name}`?path=`{path}` | Only stores an uploaded proto file to the filesystem without adding it to the registry. This is useful if the proto file is only meant to be used as a dependency for another proto, and will not be used to instantiate a gRPC service. |
|POST     | /grpc/protos/remove/`{name}` | Removes a previously uploaded proto file from the registry and the local filesystem. |
|POST     | /grpc/protos/clear | Removes all proto files from the registry and the local filesystem. |
|GET     | /grpc/protos/`{proto}`<br/>/list/services | Get a list of all services parsed from a specific proto file |
|GET     | /grpc/protos/`{proto}`<br/>/list/`{service}`/methods | Get a list of service methods for a specific service from a specific proto file |
|GET     | /grpc/protos | Get a listing of all services parsed from the uploaded protos |


#### Server APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /port=`{port}`/grpc/open | Open the referenced port as gRPC port. |
|POST     | /grpc/services<br/>/reflect/`{upstream}` | Load gRPC services through reflection call made to the given upstream endpoint. The reflected service specs are added to the Goto gRPC registry so that those can later be served by the generic gRPC server. |
|POST     | /grpc/services/`{service}`/serve | Serve the referenced service on the current port. The service name must be a valid gRPC service previously uploaded either through a proto file or via upstream reflection.  |
|POST     | /grpc/services/`{service}`/stop | Stop serving the given service. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload | Upload response payload to be used for responses generated by a service method. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload<br/>/header/`{header}` | Upload response payload to be used for requests matching the given header (any value). |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload<br/>/header/`{header}`=`{value}` | Upload response payload to be used for requests matching the given header/value. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload<br/>/body~`{regexes}` | Upload response payload to be used for requests where the body content matches any of the given set of regex expressions. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload<br/>/body/paths/`{paths}` | Upload response payload to be used for requests where the body content matches any of the given JSONPath expressions. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload/transform | Upload response payload to be used for requests where the body content matches the defined transform config. The response would be generated by applying the transform expressions to the request body and the uploaded payload. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload/clear | Clear uploaded payload for a service method. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload/stream<br/>/count=`{count}`/delay=`{delay}` | Set streaming response payload. The uploaded payload must be a JSON array, items from which will be sent as response stream up to the given count, with delay applied before sending each item. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload/stream<br/>/count=`{count}`/delay=`{delay}`<br/>/header/`{header}` | Set streaming response payload for requests matching the given header. |
|POST     | /grpc/services/`{service}`<br/>/`{method}`/payload/stream<br/>/count=`{count}`/delay=`{delay}`<br/>/header/`{header}`=`{value}` | Set streaming response payload for requests matching the given header=value. |
|POST     | /grpc/services/`{service}`/track | Track various counts for calls to a service. |
|POST     | /grpc/services/`{service}`<br/>/track/headers/`{headers}` | Track request counts for calls to a service for the given headers. |
|POST     | /grpc/services/`{service}`<br/>/track/`{header}`=`{value}` | Track request counts for calls to a service for the given header=value combination. |
|GET     | /grpc/services/active | Get a list of services currently being served by the generic GRgRPCPC server. |
|GET     | /grpc/services | Get a list of all services in the gRPC registry. |
|GET     | /grpc/{service} | Get details of a service. |
|GET     | /grpc/{service}/tracking | Get tracking details for a service. |


### Generic gRPC Client
Goto can act as a generic gRPC client that can invoke any upstream gRPC service using reflection. The client can be used via the sole gRPC client API listed here as well as via Client Traffic feature.


#### gRPC Client APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /grpc/call/`{endpoint}`<br/>/`{service}`/`{method}` | Call an upstream service based on the given endpoint, service name, method name, and payload from request body. |


## Notes
- Replace `grpc` with `jsonrpc` in paths for JSON-RPC services
- `{service}` - Service name
- `{method}` - Method name
- `{headers}` - Comma-separated list of header names
- `{count}` - Number of streaming responses
- `{delay}` - Delay between streaming responses (milliseconds)
