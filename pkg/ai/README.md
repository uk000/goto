
# AI
The AI package provides APIs and implementation to enable `Goto` to act as an A2A Agent, an A2A client, an MCP server, and an MCP client, all at once.

Contents:
- [A2A Server](#a2a-server), [A2A Client](#a2a-client), [MCP Server](#mcp-server), [MCP Client](#mcp-client), [MCP Proxy](#mcp-proxy)
- [AI Registry APIs](#ai-registry-apis) (`/ai/agents...`)
- [JSON-RPC Services APIs](#jsonrpc-services-apis) (`/jsonrpc/services...`)
- [JSON-RPC Server APIs](#jsonrpc-server-apis) (`/jsonrpc/server...`)
- [MCP Logging API](#mcp-logging-api) (`/log/mcp/verbose=...`)


## A2A Server
See [A2A Server][a2a-server]

## A2A Client
See [A2A Client][a2a-client]

## MCP Server
See [MCP Server][mcp-server]

## MCP Client
See [MCP Client][mcp-client]

## MCP Proxy
See [MCP Proxy][mcp-proxy]


# <a name="ai-registry-apis"></a>
## AI Registry APIs
The agent registry is a process-wide record of the agents by name and port. An agent is recorded when it is added with the [A2A server APIs](a2a/server/README.md#a2a-agent-apis) (`POST /a2a/agents/add`) or loaded from a startup config. The registry is also what `GET /a2a/agents` lists.

|METHOD|URI|Description|
|---|---|---|
| GET  | /ai/agents       | Lists the registered agents as a map of agent name to port to agent (JSON, or YAML with `Accept: application/yaml`). `{}` if there are none. |
| POST | /ai/agents/add   | Adds the agents of the request's JSON array payload (the [Agent](a2a/server/README.md#agent-json-schema) objects of the A2A server) to the registry, under the request's port, without serving them: the agent isn't callable on `/agent/{agent}/` (use `/a2a/agents/add` for that). Each agent needs a `card` with a `name`. |
| POST | /ai/agents/clear | Clears the registry. The agents that are served by the A2A servers of the ports aren't affected (use `POST /a2a/agents/clear` to clear both). |

Responses: `/ai/agents/add` returns `200` with `Added Agents: [name1 name2]`, or `400` with `Failed to parse payload with error [...]` (the payload must be JSON; YAML isn't accepted), `Failed to add agents, no agent cards in the payload`, or `Failed to add agents, agent #N in the payload has no card with a name` (nothing is added then). `/ai/agents/clear` returns `200` with `N Agents removed from registry`, where `N` is the number of agent names that were registered.

```
# minimal
curl -X POST localhost:8080/ai/agents/add -d '[{"card":{"name":"agent-1","description":"d","url":"http://localhost:8080/agent/agent-1/","version":"1.0","capabilities":{},"skills":[{"id":"e","name":"E"}]}}]'
curl localhost:8080/ai/agents
curl -X POST localhost:8080/ai/agents/clear
```


# <a name="jsonrpc-services-apis"></a>
## JSON-RPC Services APIs
A JSON-RPC service is a named set of methods whose responses are configured with the [RPC service APIs](../rpc/README.md) (`/jsonrpc/server/...`: list services, set the response payload and stream settings of a method, track calls). The APIs below, which are the JSON-RPC counterparts of the gRPC service APIs, add and remove the services. They are per port: the services are those of the port of the request (use the `/port={port}/...` prefix to configure another port). Once a service and its methods exist, a `POST` of a JSON-RPC request to `/{service}/{method}` (the lower-cased names, e.g. `/calc/add`) on the port is answered with the response payload that was set for the method.

|METHOD|URI|Description|
|---|---|---|
| POST | /jsonrpc/services/add/{service}                  | Adds a service from the JSON body ([Service schema](#jsonrpc-service-schema)), replacing a service of the same name. The `{service}` path param is only checked to be present: the name of the service comes from the body. |
| POST | /jsonrpc/services/add/{service}?fromGRPC=true    | Adds a service that mirrors the gRPC service `{service}` (its name, methods, streaming kinds and stream settings), without a body. For example the built-in `Goto` gRPC service has methods `echo`, `streamIn`, `streamOut` and `streamInOut`. |
| POST | /jsonrpc/services/remove/{service}               | Removes the service. |
| POST | /jsonrpc/services/{service}/remove               | Same as above. |
| POST | /jsonrpc/services/clear                          | Removes all the services of the port. |

Responses: `200` with `Registered JSONRPCService: calc with 3 methods`, `JSONRPCService [calc] removed` (also for a service that doesn't exist), `All services removed`. `/add` returns `500` with `Failed to register service [x] with error: ...` when the body can't be parsed (`unexpected end of JSON input`), has no `name` (`No name`), has a method whose key differs from its `name` (`Method [a] is named [b]`), or, with `fromGRPC`, when there is no such gRPC service (`No GRPCService found for name: x`).

#### <a name="jsonrpc-service-schema"></a>Service schema (body of `/add`)
|Field|Data Type|Default|Description|
|---|---|---|---|
| name | string | none, required | Name of the service. |
| methods | map[string]Method | `{}` | The methods, by name. The key must equal the method's `name` (a missing `name` takes the key; a `null` method is an empty method). |
| uri | string | `/{name}` in lower case | Set by `Goto`, whatever is given. The URI of a method is `/{service}/{method}` in lower case. |

|Method field|Data Type|Default|Description|
|---|---|---|---|
| name | string | the key | Name of the method. |
| isBatch | bool | `false` | The method takes a batch (client stream) of requests. |
| isStreaming | bool | `false` | The method streams its response (server stream). |
| responsePayload | string | `""` | Response payload of the method. Normally set with `/jsonrpc/server/{service}/{method}/payload`. |
| streamCount | int | `0` | Number of responses to stream. |
| streamDelayMin, streamDelayMax | int (nanoseconds) | `0` | Delay between the streamed responses. |
| streamDelayCount | int | `0` | Number of times to apply the delay. |

```
# minimal
curl -X POST localhost:8080/jsonrpc/services/add/calc -d '{"name":"calc"}'
# rich: two methods, one of them streaming, then set a response payload and call it
curl -X POST localhost:8080/jsonrpc/services/add/calc -d '{"name":"calc","methods":{"add":{},"watch":{"isStreaming":true}}}'
curl -X POST localhost:8080/jsonrpc/server/calc/add/payload -d '{"jsonrpc":"2.0","id":1,"result":{"v":42}}'
curl -X POST localhost:8080/calc/add -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"add","params":{"a":1}}'
# mirror a gRPC service, remove one service, clear all
curl -X POST "localhost:8080/jsonrpc/services/add/Goto?fromGRPC=true"
curl -X POST localhost:8080/jsonrpc/services/calc/remove
curl -X POST localhost:8080/jsonrpc/services/clear
```


# <a name="jsonrpc-server-apis"></a>
## JSON-RPC Server APIs
These APIs list the JSON-RPC services of the port, track calls to them, and set the responses of their methods. They are the same handlers as the gRPC ones under `/grpc/server/...` (see the [RPC README](../rpc/README.md)), and like those are per port (use the `/port={port}/...` prefix to configure another port). `{service}` and `{method}` are the names given when the service was added with the [services APIs](#jsonrpc-services-apis), and are case sensitive here (`/jsonrpc/server/MyCalc` finds a service named `MyCalc`, `/jsonrpc/server/mycalc` is a `400`), though the calls to the methods are always on the lower-cased URI.

|METHOD|URI|Description|
|---|---|---|
| GET  | /jsonrpc/server | Lists the services of the port and their methods, as YAML. |
| GET  | /jsonrpc/server/`{service}` | Details of the service, as YAML. `400` with `Service proto not defined [x]` if there is no such service. |
| POST | /jsonrpc/server/`{service}`/track | Starts tracking the calls to the service's methods. |
| POST | /jsonrpc/server/`{service}`/track/headers/`{headers}` | Same, also counting the calls by the comma-separated request headers. |
| POST | /jsonrpc/server/`{service}`/track/`{header}`=`{value}` | Same, also counting the calls that have the header with the value. |
| GET  | /jsonrpc/server/`{service}`/tracking | The call counts as JSON: `serviceCallCounts`, `serviceMethodCallCounts` (by `/{service}/{method}`) and `portTracking` (by URI and header). `404` with `jsonrpc Service [x] is not tracked` before tracking was started. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload | Sets the request body as the response of the method (the complete JSON-RPC response text, e.g. `{"jsonrpc":"2.0","id":1,"result":{"v":42}}`). |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/header/`{header}` | Sets the response for calls that have the request header. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/header/`{header}`=`{value}` | Sets the response for calls that have the request header with the value. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/body~`{regexes}` | Sets the response for calls whose body matches the comma-separated regexes, in the given order. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/body/paths/`{paths}` | Sets the response for calls whose body has all the comma-separated JSON paths (`.path={var}` captures a value for the response). See the [response payload README](../server/response/payload/README.md). |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/stream/count=`{count}`/delay=`{delay}` | Sets the response for a streaming method: it is sent `count` times, with `delay` (such as `10ms`, or a range) between them. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/stream/count=`{count}`/delay=`{delay}`/header/`{header}` | Same, for calls that have the request header. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/stream/count=`{count}`/delay=`{delay}`/header/`{header}`=`{value}` | Same, for calls that have the request header with the value. |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/transform | Sets the response as a transformation of the request body: the body of this request is a JSON array of transform sets, as described under [Payload transformations](../server/response/payload/README.md#payload-transformations). |
| POST | /jsonrpc/server/`{service}`/`{method}`/payload/clear | Clears the payloads. Note: this clears all the HTTP and RPC response payloads of the port, not only those of the method (the same as `POST /response/payload/clear`). |

The response payload content type is `plain/text` unless the request that sets it has the `ResponseContent-Type` header. The two `GET` lists reply with YAML, and `/tracking` with JSON; the `POST` APIs reply with a one-line text message and `200`. For all of them an unknown service or method is `400` (`Service proto not defined [x]`, `Method [m] not defined for service [calc]`). Failures to set the payload are `200` with a message that says `Failed to set ... with error [...]`, for example `No payload` for an empty body, and a transform body that isn't valid JSON is `200` with `Failed to read transformations: ...`.

Calls to a method are `POST /{service}/{method}` in lower case on the port (for example `/calc/add`) with a JSON-RPC request in the body. A call that doesn't have the header of a header payload gets the default payload of the method. A streaming payload is sent as `count` copies of the payload, one after the other with the delay, in a single response. A call to a method that doesn't exist on the service isn't answered as JSON-RPC: it gets the regular `Goto` request echo.

```
# add the service, then set the default response, one for calls with a header, and one that streams
curl -X POST localhost:8080/jsonrpc/services/add/calc -d '{"name":"calc","methods":{"add":{},"watch":{"isStreaming":true}}}'
curl -X POST localhost:8080/jsonrpc/server/calc/add/payload -d '{"jsonrpc":"2.0","id":1,"result":"default"}'
curl -X POST localhost:8080/jsonrpc/server/calc/add/payload/header/x-k=v1 -d '{"jsonrpc":"2.0","id":1,"result":"for v1"}'
curl -X POST localhost:8080/jsonrpc/server/calc/watch/payload/stream/count=3/delay=10ms -d '{"jsonrpc":"2.0","id":1,"result":"tick"}'

# track the calls, make some, read the tracking, and look at the service
curl -X POST localhost:8080/jsonrpc/server/calc/track/headers/x-k
curl -X POST localhost:8080/calc/add -H 'x-k: v1' -d '{"jsonrpc":"2.0","id":1,"method":"add"}'
curl localhost:8080/jsonrpc/server/calc/tracking
curl localhost:8080/jsonrpc/server/calc
curl localhost:8080/jsonrpc/server
```


# <a name="mcp-logging-api"></a>
## MCP Logging API
|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /log/mcp/verbose={verbose} | Turns verbose MCP logging on or off, process wide (`verbose` is a boolean such as `true` or `false`; the default is off). When on, the MCP servers and clients log how each request is routed to a server and tool, and the calls and responses that the MCP client makes. The response is `200` with an empty body. |

```
curl -X POST localhost:8080/log/mcp/verbose=true
curl -X PUT  localhost:8080/log/mcp/verbose=false
```


[a2a-server]: a2a/server/README.md
[a2a-client]: a2a/client/README.md
[mcp-server]: mcp/server/README.md
[mcp-client]: mcp/client/README.md
[mcp-proxy]: ../proxy/mcp/README.md
