# RPC Package

This package contains implementation for gRPC and JSON-RPC protocol.


## > gRPC
All HTTP ports that a `goto` instance listens on (including bootstrap port) support both `HTTP/2` and `gRPC` protocol. Any listener that's created with protocol `grpc` works exclusively in `grpc` mode, not supporting HTTP requests and only responding to the gRPC operations described below.

gRPC support in `Goto` falls into 3 categories:
1. Goto's own gRPC Service
2. Generic gRPC Server that can expose any custom gRPC service from a given proto file
3. Generic gRPC client that can call any remote gRPC service using reflection

All of these 3 gRPC features are described below.

#### Which port does an admin API configure?
The admin APIs on this page act on the port that the request arrives on: the payloads, tracking, and the served services are all kept per port. A listener in `grpc` mode cannot serve the admin APIs, so to configure one, send the request to an HTTP port with the `/port={grpcPort}/` prefix, e.g. `POST /port=8888/grpc/server/Goto/echo/payload`. The referenced port must already be a listener of the instance, otherwise the response is `200` with the message `Port [8888] not configured`. A request without the prefix configures the HTTP port it was sent to, which a gRPC client calling the gRPC port never sees.

###### <small>* All the `/grpc/server/...` APIs below (except `/grpc/server/open`, which takes the port as a query param) can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

### Goto's gRPC server
Goto exposes a gRPC server with the following RPC methods. The same four methods are declared by three services, `Goto`, `Goto2` and `Goto3`, all defined in `pkg/rpc/grpc/protos/goto.proto`, but only `Goto` is implemented: a call to a method of `Goto2` or `Goto3` is answered with gRPC status `UNIMPLEMENTED` (`method Echo not implemented`), also when a response payload was configured for it. `Goto2` and `Goto3` are there as service names for the [gRPC proxy](../proxy/grpc/README.md) and for the service registry; a proxy for `Goto2` to the `Goto` of an upstream (`/proxy/grpc/Goto2/{upstream}/Goto`) does answer calls (verified):

1. `Goto.echo`: Unary grpc method that echoes back the given payload along with headers. The `echo` input message is given below. It responds with a single instance of `Output` message described later. If a response payload was configured for `Goto/echo` (see the payload APIs below) on the port, the configured payload text is returned in the `text` field instead of the input.
   ```
   message Input {
     string text = 1;
   }
   ```
2. `Goto.streamIn`: This is a client-streaming method that accepts a stream of `Input` messages (described above under unary `echo` method). Once client ends the stream, the server responds with a single message of `Output` type (described further down) whose `text` is the text of each input message in brackets, e.g. `[hello 1][hello 2]` (or the configured payload in brackets, once per input message).

3. `Goto.streamOut`: This is a server streaming service method that accepts a `StreamConfig` input message allowing the client to configure the parameters of stream response. It responds with `chunkCount` number of `Output` messages, each output carrying a payload of size `chunkSize` (or the given `text`, or the configured payload) with the suffix `(i/chunkCount)` as its `text`, and there is `interval` delay between two output messages (100ms if the interval is empty or invalid).
   ```
   message StreamConfig {
    int32  chunkSize = 1;
    int32  chunkCount = 2;
    string interval = 3;
    string text = 4;
   }
   ```
4. `Goto.streamInOut`: This is a bi-directional streaming service method that accepts a stream of `StreamConfig` input messages as described in `streamOut` operation above. Each input `StreamConfig` message requests the server to send a stream response based on the given stream config. For each input message, the service responds with `chunkCount` number of `Output` messages, each output carrying a payload of size `chunkSize`, and there is `interval` delay between two output messages. The text of the messages received so far builds up from one config to the next (separated by ` | `), and the last one is marked `(FINAL)`.

All `grpc` operations exposed by `goto` produce the following proto message as output:

```
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
```
The gRPC response from `goto` carries the following headers:

- `goto-host`
- `goto-port`
- `goto-protocol`
- `goto-remote-address`
- `goto-rpc-method` (e.g. `/Goto/echo`)
- `via-goto`
- *request headers echoed back with `request-` prefix
- `request-authority`
- `request-content-type`
- `request-user-agent`
- `request-...` (all custom headers)

See [Goto GRPC Server Example](../../docs/grpc-server-example.md)

### Generic gRPC Server
Goto's gRPC feature becomes more interesting when it's used as a generic gRPC server that can act as any gRPC service based on a given proto file. This feature also includes a generic Proto registry with REST APIs to let users upload any number of proto files.


#### Proto APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another. The proto registry itself is not per port.</small>

|METHOD|URI|Description|
|---|---|---|
|POST, PUT | /grpc/protos/add<br/>/`{name}`?path=`{path}` | Compiles the request body as a proto file, saves it as `protos/{name}.proto` under goto's CWD, and adds the services it defines to the registry. Give the `{name}` without the `.proto` extension (the extension is appended). The `path` param is optional and saves the file under `protos/{path}/` instead, so that files uploaded later can import it. Imports are resolved against `protos/` and the given path. |
|POST, PUT | /grpc/protos/store<br/>/`{name}`?path=`{path}` | Only compiles and stores an uploaded proto file on the filesystem without adding its services to the registry. This is useful if the proto file is only meant to be used as a dependency for another proto, and will not be used to instantiate a gRPC service. |
|POST, PUT | /grpc/protos/remove/`{name}` | Removes a previously added proto from the registry (its services are removed too) and deletes its file. Always answers `Proto [name] removed`, also when the name is unknown. |
|POST     | /grpc/protos/clear | Removes all added protos and resets the whole gRPC service registry. See the notes below. |
|GET     | /grpc/protos | Get a JSON listing of the services that each uploaded proto defined, keyed by proto name. `{}` when nothing was added. |
|GET     | /grpc/protos/`{service}`<br/>/list/services | Get the details of one service as JSON. The path segment is looked up as a service name in the registry (not as the name of a proto file), e.g. `/grpc/protos/SampleService/list/services`. Answers `200` with `No services in proto [x]` if there is no such service. |
|GET     | /grpc/protos/`{proto}`<br/>/list/`{service}`/methods | Get the methods of a service as JSON, keyed by method name (stream flags, uri, payload and stream settings). The `{proto}` segment is not used for the lookup, only the service name is. Answers `200` with an empty body if the service isn't known. |

Responses of the proto APIs:

|API|Status|Body|
|---|---|---|
|add, store|200|`Proto [name] stored`|
|add, store (compile error, or file can't be written)|500|`Failed to store proto [name] with error: ...` |
|remove|200|`Proto [name] removed`|
|clear|200|`Protos cleared`|

Notes:
- Proto files are written under `protos/` in goto's working directory. Files stored with `/grpc/protos/store`, and files that failed to compile, are not deleted by `remove` or `clear`.
- `POST /grpc/protos/clear` also drops the built-in `Goto`, `Goto2` and `Goto3` services from the registry (the registry is reset to empty), after which APIs such as the payload and serve APIs answer `400 Service proto not defined [Goto]` until goto is restarted. Services that the gRPC server already registered keep serving until it restarts.

```
# upload a proto as protos/sample.proto and register its services
curl -X POST localhost:8080/grpc/protos/add/sample --data-binary @sample.proto
Proto [sample] stored

# upload a dependency under protos/deps/ without registering any service
curl -X POST 'localhost:8080/grpc/protos/store/common?path=deps' --data-binary @common.proto

curl localhost:8080/grpc/protos
curl localhost:8080/grpc/protos/SampleService/list/services
curl localhost:8080/grpc/protos/sample/list/SampleService/methods
curl -X POST localhost:8080/grpc/protos/remove/sample
```


#### Server APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another (see [Which port does an admin API configure?](#which-port-does-an-admin-api-configure)). `grpc` can be replaced with `jsonrpc` in the common `/server/...` routes marked (common) to work with JSON-RPC services.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /grpc/server/open?port=`{port}` | Open a new gRPC listener on the given port and start serving on it. `400` with `Invalid port [N]` if the port is missing or isn't in 1-65535, `500` if the port can't be opened. (Give the port as a query param: the `/port=N/` prefix is only for ports that already are listeners, and otherwise answers `200 Port [N] not configured`.) |
|POST     | /grpc/server/services<br/>/reflect/`{upstream}` | Load gRPC services through reflection call made to the given upstream endpoint (`host:port`, plaintext). The reflected service specs are added to the Goto gRPC registry, replacing the entries of the same names, and the response is the JSON of all services in the registry. `400` with the connection or reflection error if the upstream can't be reached. |
|POST     | /grpc/server/serve/`{service}`<br/>/grpc/server/services/`{service}`/serve | Serve the referenced service on the port of the request. The service name must be a valid gRPC service previously uploaded either through a proto file or via upstream reflection, or one of the built-in services. Restarts the gRPC server of the instance. `Service [X] registered for serving on port [N]`, or `400` with `Service proto not defined [X]`. |
|POST     | /grpc/server/stop/`{service}`<br/>/grpc/server/services/`{service}`/stop | Stop serving the given service. `Service [X] stopped on port [N]`. |
|GET      | /grpc/server/services/active | Get the services currently being served by the generic gRPC server, as JSON keyed by port. With `?port=N` the services of that port only. The built-in `Goto`, `Goto2` and `Goto3` services of a gRPC port are not listed: on a fresh instance the answer is `{}`. |
|GET      | /grpc/server/services | Get all services in the gRPC registry as JSON, keyed by service name. |
|GET (common) | /grpc/server | Get the registry as YAML. |
|GET (common) | /grpc/server/`{service}` | Get details of a service as YAML. `400` with `Service proto not defined [X]` if it isn't in the registry. |
|POST (common) | /grpc/server/`{service}`/track | Track call counts for the service. |
|POST (common) | /grpc/server/`{service}`<br/>/track/headers/`{headers}` | Track request counts for calls to a service for the given comma-separated headers. |
|POST (common) | /grpc/server/`{service}`<br/>/track/`{header}`=`{value}` | Track request counts for calls to a service for the given header=value combination. |
|GET (common) | /grpc/server/`{service}`/tracking | Get tracking details for a service (`serviceCallCounts`, `serviceMethodCallCounts`, `portTracking`). `404` with `grpc Service [X] is not tracked` if tracking wasn't enabled. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload | Upload response payload to be used for responses generated by a service method. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload<br/>/header/`{header}` | Upload response payload to be used for requests matching the given header (any value). |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload<br/>/header/`{header}`=`{value}` | Upload response payload to be used for requests matching the given header/value. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload<br/>/body~`{regexes}` | Upload response payload to be used for requests where the body content matches any of the given set of regex expressions. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload<br/>/body/paths/`{paths}` | Upload response payload to be used for requests where the body content matches any of the given JSONPath expressions. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload/transform | Upload response payload to be used for requests where the body content matches the defined transform config. The response would be generated by applying the transform expressions to the request body and the uploaded payload. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload/clear | Clear the uploaded payloads. This clears all response payloads of the port, not only those of the method (see the notes below). |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload/stream<br/>/count=`{count}`/delay=`{delay}` | Set streaming response payload. The uploaded payload must be a JSON array, items from which will be sent as response stream up to the given count (cycling through the items), with delay applied before sending each item. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload/stream<br/>/count=`{count}`/delay=`{delay}`<br/>/header/`{header}` | Set streaming response payload for requests matching the given header. |
|POST (common) | /grpc/server/`{service}`<br/>/`{method}`/payload/stream<br/>/count=`{count}`/delay=`{delay}`<br/>/header/`{header}`=`{value}` | Set streaming response payload for requests matching the given header=value. |

Responses of the payload APIs (all answer the message as plain text):

|Case|Status|Body|
|---|---|---|
|Stored|200|`Port [N]: Set GRPC payload for service [S] method [M], header [h:v], regexes [..], paths [..], content-type [..], length [L], count [C], delay [min-max]`|
|Transform stored|200|`Port [N]: Configured Transform paths for GRPC service [S] method [M], content-type [..], transforms: [..]`|
|Cleared|200|`Port [N]: Cleared GRPC payloads for service [S] method [M]`|
|Unknown service|400|`Service proto not defined [S]`|
|Unknown method|400|`Method [M] not defined for service [S]`|
|Empty body (the payload can't be empty), invalid transforms|200|`Port [N]: Failed to set GRPC payload ... with error [No payload]`, or `Failed to read transformations: ...`, `Missing transforms.` |
|Tracking set|200|`Tracking grpc Service [S] with headers [...]`|

Notes on the payload and tracking APIs:
- Payloads are stored per port, so to change what a gRPC listener answers, use the `/port={grpcPort}/` prefix. A payload is returned only for calls received on that port.
- For the built-in `Goto` services the configured payload replaces the `text` of the `Output` as is. For a generic service the payload is the JSON of the response message (e.g. `{"id":7,"content":"fixed"}`), and the stream payload is a JSON array of response messages.
- The `count` and `delay` of the stream payload APIs: `delay` is a duration (`100ms`) or a range (`10ms-50ms`, optionally with a `:count` suffix); the generic server sends `count` messages with that delay between them.
- Payloads configured with header, body-regex or body-path criteria are accepted by the APIs but are currently not matched at call time, so only the payload set without criteria is returned.
- `payload/clear` resets every payload of the port (HTTP response payloads too), not just the method's.
- `{service}` and `{method}` are the names as in the proto file, e.g. `Goto` and `echo`.

```
# serve a service from an uploaded proto on gRPC port 8888 and give it a canned response
curl -X POST localhost:8080/grpc/protos/add/sample --data-binary @sample.proto
curl -X POST localhost:8080/port=8888/grpc/server/serve/SampleService
Service [SampleService] registered for serving on port [8888]

curl -X POST localhost:8080/port=8888/grpc/server/SampleService/oneOne/payload -d '{"id":7,"content":"fixed"}'
curl -X POST localhost:8080/port=8888/grpc/server/SampleService/oneMany/payload/stream/count=3/delay=50ms \
     -d '[{"id":1,"content":"a"},{"id":2,"content":"b"}]'

# open a new gRPC listener, check what is served where, and stop a service
curl -X POST 'localhost:8080/grpc/server/open?port=8889'
curl localhost:8080/grpc/server/services/active
curl -X POST localhost:8080/port=8888/grpc/server/stop/SampleService

# load the services of another gRPC server through reflection, and track calls on one of them
curl -X POST localhost:8080/grpc/server/services/reflect/other-host:9000
curl -X POST localhost:8080/port=8888/grpc/server/Goto/track/headers/x-mode
curl localhost:8080/port=8888/grpc/server/Goto/tracking
```


### Generic gRPC Client
Goto can act as a generic gRPC client that can invoke any upstream gRPC service using reflection (plaintext connections only). The client can be used via the gRPC client API listed here as well as via Client Traffic feature.


#### gRPC Client APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another. Every API is available under both the `/grpc/client/...` and the `/client/grpc/...` prefixes.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /grpc/client/call | Call an upstream service based on the given call payload (JSON or YAML, see below). |
|POST     | /grpc/client/call/<br/>`{service}`/`{method}`<br/>/`{endpoint}` | Call an upstream service based on the given endpoint, service name, method name, and gRPC payload from request body (sent as one message). The response carries the response headers only: the payload is not returned (there is no way to pass `result`). |
|POST     | /grpc/client/call/<br/>`{service}`/`{method}`<br/>/`{endpoint}`/stream | Same as above, with `push` set: each response message is written to the HTTP response as soon as it arrives, before the final summary. |

Call payload fields (`POST /grpc/client/call`):

|Field|Type|Description|
|---|---|---|
|service|string, required|Service name, e.g. `Goto`. Loaded from the endpoint's server reflection.|
|method|string, required|Method name, e.g. `echo`.|
|endpoint|string, required|`host:port` of the upstream gRPC server.|
|payloads|object, required|`linear` (list, sent in order) or `concurrent` (list, run in parallel). Each item has `payload` (JSON text of one request message), or `stream` (list of JSON texts sent as one client stream), or a nested `linear` list, and optional `headers`.|
|headers|object|`request` (and `response`) with `add` (map of name to value), `remove` (list), `forward` (list of the names of the headers of the incoming HTTP request to send on). `payloads` items can carry their own `headers` with the same fields.|
|push|bool|Write the messages as they arrive.|
|result|bool|Include the response messages in the result. Default `false`, in which case only the response headers are returned.|

Responses:

|Case|Status|Body|
|---|---|---|
|Success|200|JSON list, one entry per call: `[{"Headers": {...response headers and trailers...}, "Payload": [ {...response message...}, ...]}]`. `Payload` is present only with `"result": true` (`null` otherwise).|
|Invalid payload (missing `service`, `method`, `endpoint` or `payloads`)|400|`Invalid payload: ...`|
|Body isn't JSON or YAML|400|`Failed to parse payload with error [...]`|
|Endpoint can't be reached, or the service/method isn't found by reflection|400|the error, e.g. `symbol not found: Nope.echo`, `rpc error: code = Unavailable ...`|
|The call failed, or no response was received|503|the call errors, or `Upstream Unavailable`|

```
# minimal
curl -XPOST localhost:8080/grpc/client/call -d '{
  "service": "Goto", "method": "echo", "endpoint": "localhost:8888",
  "payloads": {"linear": [{"payload": "{\"text\": \"hello\"}"}]},
  "result": true
}'

# same call, with the URI form (returns only the headers)
curl -XPOST localhost:8080/grpc/client/call/Goto/echo/localhost:8888 -d '{"text": "hello"}'
```

#### gRPC Client Call Payload Examples
```
curl -XPOST localhost:8080/grpc/client/call -H'Foo:123' -d '
{
  "service": "Goto",
  "method": "echo",
  "endpoint": "localhost:8888",
  "headers": {
    "request": {
      "forward":["foo"]
    }
  },
  "payloads": {
    "concurrent": [
      {
        "linear": [
          {"payload": "{\"text\": \"hello 1.1\"}"},
          {"payload": "{\"text\": \"hello 1.2\"}"}
        ],
        "headers": {"request":{"add":{"stream": "1"}}}
      },
      {
        "linear": [
          {"payload": "{\"text\": \"hello 2.1\"}"},
          {"payload": "{\"text\": \"hello 2.2\"}"}
        ],
        "headers": {"request":{"add":{"stream": "2"}}}
      }
    ]
  },
  "push": false,
  "result": true
}
'
```
```
{
  "service": "Goto",
  "method": "streamIn",
  "endpoint": "localhost:8888",
  "headers": {"request":{"forward":["foo"]}},
  "payloads": {
    "concurrent": [
      {
        "stream": [
          "{\"text\": \"hello 1.1\"}",
          "{\"text\": \"hello 1.2\"}"
        ],
        "headers": {"request":{"add":{"stream": "1"}}}
      },
      {
        "stream": [
          "{\"text\": \"hello 2.1\"}",
          "{\"text\": \"hello 2.2\"}"
        ],
        "headers": {"request":{"add":{"stream": "2"}}}
      }
    ]
  },
  "push": false,
  "result": true
}
```
```
{
  "service": "Goto",
  "method": "streamOut",
  "endpoint": "localhost:8888",
  "headers": {"request":{"forward":["foo"]}},
  "payloads": {
    "concurrent": [
      {
        "linear": [
          {
            "payload": "{\"chunkCount\":2, \"interval\":\"1s\", \"text\": \"hello 1.1\"}",
            "headers": {"request":{"add":{"stream": "1"}}}
          },
          {
            "payload": "{\"chunkCount\":3, \"interval\":\"1s\", \"text\": \"hello 1.2\"}",
            "headers": {"request":{"add":{"stream": "2"}}}
          }
        ]
      },
      {
        "linear": [
          {
            "payload": "{\"chunkCount\":3, \"interval\":\"1s\", \"text\": \"hello 2.1\"}",
            "headers": {"request":{"add":{"stream": "3"}}}
          },
          {
            "payload": "{\"chunkCount\":2, \"interval\":\"1s\", \"text\": \"hello 2.2\"}",
            "headers": {"request":{"add":{"stream": "4"}}}
          }
        ]
      }
    ]
  },
  "push": false,
  "result": true
}
```
```
{
  "service": "Goto",
  "method": "streamInOut",
  "endpoint": "localhost:8888",
  "headers": {"request":{"forward":["foo"]}},
  "payloads": {
    "concurrent": [
      {
        "stream": [
          "{\"chunkCount\":1, \"interval\":\"1s\", \"text\": \"hello 1.1\"}",
          "{\"chunkCount\":3, \"interval\":\"1s\", \"text\": \"hello 1.2\"}",
          "{\"chunkCount\":1, \"interval\":\"1s\", \"text\": \"hello 1.3\"}"
        ],
        "headers": {"request":{"add":{"stream": "1"}}}
      },
      {
        "stream": [
          "{\"chunkCount\":2, \"interval\":\"1s\", \"text\": \"hello 2.1\"}",
          "{\"chunkCount\":1, \"interval\":\"1s\", \"text\": \"hello 2.2\"}",
          "{\"chunkCount\":2, \"interval\":\"1s\", \"text\": \"hello 2.3\"}"
        ],
        "headers": {"request":{"add":{"stream": "2"}}}
      }
    ]
  },
  "push": false,
  "result": true
}
```

### gRPC Ext Authz Service
Every gRPC listener of goto also serves Envoy's external authorization service (`envoy.service.auth.v3.Authorization`, method `Check`), so goto can stand in for an ext authz server of an Envoy `ext_authz` filter. The decision of the service is driven by header rules:
- The rules are kept for the whole instance (not per port) and are evaluated in the order they were added; the first rule that matches the headers of the checked request decides. If no rule matches, the request is allowed.
- A rule matches a request if the request has the rule's `header` (and, if the rule has a `value`, the header's value is exactly equal to it). The header name `*` matches every request.
- `deny` answers with gRPC status code `7` (`Permission denied`) and a denied response with HTTP status `403 Forbidden`. `allow` answers with an `OK` response that changes nothing.
- Header names are stored in lower case, and are compared with the header names that Envoy sends in the check request, which are lower case too.
- Adding a rule with the same header and value as an existing rule replaces it.

#### Ext Authz APIs

|METHOD|URI|Description|
|---|---|---|
|POST     | /grpc/extauthz/rules | Add the rule or the list of rules in the request body. |
|GET      | /grpc/extauthz/rules | Get the rules, in evaluation order, as a JSON list. `[]` if there are none. |
|DELETE   | /grpc/extauthz/rules<br/>?header=`{header}`&value=`{value}` | Remove the rule of the given header and value (omit `value` to remove the rule that has no value). Without a `header` all the rules are removed. |

Rule fields:

|Field|Type|Description|
|---|---|---|
|header|string, required|Name of the request header to match, case insensitive. `*` matches every request.|
|value|string, optional|Exact value that the header must have. Without it the rule matches on the presence of the header.|
|action|string, required|`allow` or `deny`.|

Responses:

|Case|Status|Body|
|---|---|---|
|POST, rules stored|200|`Stored [N] ext authz rules` (N counts the valid rules; invalid rules in a list are skipped)|
|POST, body is neither a rule nor a list of rules|400|`Invalid ext authz rules payload`|
|POST, empty list|400|`No ext authz rules given`|
|POST, none of the rules has a header and an action of `allow` or `deny`|400|`No valid ext authz rules stored; each rule needs a header and an action of allow or deny`|
|DELETE, rule removed|200|`Removed ext authz rule for header [h]`|
|DELETE, no such rule|200|`No ext authz rule found for header [h]`|
|DELETE, no header|200|`Cleared all ext authz rules`|

```
# deny every request that has the header x-block, whatever its value
curl -X POST localhost:8080/grpc/extauthz/rules -d '{"header": "x-block", "action": "deny"}'
Stored [1] ext authz rules

# allow user alice, deny user mallory (a list of rules)
curl -X POST localhost:8080/grpc/extauthz/rules -d '[
  {"header": "x-user", "value": "alice", "action": "allow"},
  {"header": "x-user", "value": "mallory", "action": "deny"}
]'

# deny everything that no earlier rule allowed
curl -X POST localhost:8080/grpc/extauthz/rules -d '{"header": "*", "action": "deny"}'

curl localhost:8080/grpc/extauthz/rules
curl -X DELETE 'localhost:8080/grpc/extauthz/rules?header=x-user&value=mallory'
curl -X DELETE localhost:8080/grpc/extauthz/rules
```

## Notes
- Replace `grpc` with `jsonrpc` in paths for JSON-RPC services (the common `/server/...` routes)
- `{service}` - Service name
- `{method}` - Method name
- `{headers}` - Comma-separated list of header names
- `{count}` - Number of streaming responses
- `{delay}` - Delay between streaming responses: a duration such as `100ms`, or a range `10ms-50ms`, optionally followed by `:count`. A value that isn't a duration is ignored (no delay).
- Loading services by reflection (`/grpc/server/services/reflect/{upstream}`), or calling a service with the gRPC client API, registers what the remote server reports in the same service registry, replacing the entries of the same name. When the name is one of the built-in services (`Goto`, `Goto2`, `Goto3`), the built-in implementation is no longer used once the gRPC server restarts (which serving or stopping a service, or setting up a gRPC proxy, causes), and the service answers with empty messages until goto is restarted.
