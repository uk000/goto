# MCP Server Features
The `pkg/ai/mcp/server` sub-package implements a generic MCP server with admin APIs to let users configure `Goto` as one or more MCP servers exposed over the same or different ports.

The `MCP` admin APIs are exposed with path-prefix `/mcpapi/...` and allow users to add/start/stop MCP servers, add tools/prompts/resources/resource templates to an MCP server, configure response payloads, force HTTP statuses, invoke tools via REST, and get MCP server details.

Once an MCP server is configured, a client can call `/mcp/{server-name}` to interact with the MCP server over MCP protocol (Streamable HTTP), or `/mcp/sse/{server-name}` to use the SSE transport.

## Feature Overview
- **Multiple servers per port**: each server is stateful or stateless, and is reachable by name (`/mcp/{server}`), by a custom `uri`, or by a `uriRegex`. A tool is also reachable at `/mcp/{server}/{tool}`.
- **Default servers**: `default-stateless` (`/mcp/default/stateless`) and `default-stateful` (`/mcp/default/stateful`) are created at startup on the JSON-RPC port, and serve MCP requests that match no other server.
- **Tools with built-in behaviors**: echo, ping, time, status (forced HTTP status), stream and resumable stream, elicitation / sampling / list-roots callbacks to the client, HTTP fetch from a remote URL, remote MCP tool call, remote A2A agent call, server introspection, and adding tools on the fly. See [Tool Behavior Details](#tool-behavior-details).
- **Prompts, resources, resource templates and completions**, with configurable static or streaming response payloads.
- **Reusable tool input schemas** that are registered once and referenced by name.
- **Forced HTTP status** for tool calls, per server or per tool, optionally matched by header or HTTP method, and limited to a number of calls.
- **Delays** on tool execution and between streamed chunks.
- **Call timelines**: a tool result carries a timeline of events and data in its `structuredContent`, including the details of the remote calls that the tool made. Events are also sent to the client as progress notifications.
- **Direct tool invocation via REST**, without an MCP client.

## Contents
- [Request Routing and Response Headers](#mcp-request-routing)
- [Quick Start](#mcp-quick-start)
- [MCP Admin APIs](#mcp-admin-apis)
  - [Tool Schema API](#tool-schema-api)
  - [Servers APIs](#servers-apis) and [Server APIs](#server-apis)
  - [Component Management](#component-management), [Component Payload Configuration](#component-payload-configuration), [Tool Call](#tool-call-server-side)
  - [MCP Status Management](#mcp-status-management)
- [MCP JSON Schemas](#mcp-json-schemas): [MCPServerPayload](#mcpserverpayload), [MCPServer](#mcpserver), [ToolSchema](#toolschema), [MCPTool](#mcptool), [Tool](#tool), [ToolAnnotations](#toolannotations), [ToolBehavior](#toolbehavior), [ToolConfig](#toolconfig), [ToolCall](#toolcall), [AgentCall](#agentcall), [ToolCallArgs](#toolcallargs), [Tool Behavior Details](#tool-behavior-details), [MCPPrompt](#mcpprompt), [MCPResource](#mcpresource), [MCPResourceTemplate](#mcpresourcetemplate), [StatusConfig](#statusconfig), [Payload](#payload), [Headers](#headers), [Delay](#delay), [Status Path Parameter Format](#status-path-parameter-format)

## <a name="mcp-request-routing"></a>Request Routing and Response Headers
- A request is treated as an MCP request if its URI contains `/mcp/` (or ends with `/mcp`) or `/sse`, or if it arrives on a JSON-RPC listener.
- `/mcp/{server}` (Streamable HTTP) and `/mcp/sse/{server}` (SSE) reach the server by name. A URI with `/sse` in it is served over SSE, anything else over Streamable HTTP.
- `/mcp/{server}/{tool}` reaches a tool's URI. A request sent to a tool's URI can only call that tool.
- A server added with `uri` (or routed with `/mcpapi/server/{server}/route?uri=`) is also reachable at that URI, its tools at `{uri}/{tool}` (tool names lowercased), and its SSE URI is derived by inserting `/sse` after `/mcp`, e.g. `/mcp/my-server` becomes `/mcp/sse/my-server`. A server with `uriRegex` serves all URIs that match the regex.
- If no server matches an MCP request, it is served by the port's most recently added server, or by `default-stateless` if the port has none. A disabled server is replaced by `default-stateless` too.
- Responses carry the headers `Goto-MCP-Server` (the server ID, `[name:port][namespace][cluster]`) and, for requests to a tool's URI, `Goto-MCP-Tool`. When a status is forced on a tool call, the response also has the headers `Goto-Forced-Status` and `Goto-Forced-Status-Remaining`.

## <a name="mcp-quick-start"></a>Quick Start
```
# Add an enabled server that is routed at /mcp/demo
curl -X POST localhost:8080/mcpapi/servers/add -d '[{"name":"demo","version":"1.0","uri":"/mcp/demo","hasTools":true,"enabled":true}]'

# Add an echo tool to it
curl -X POST localhost:8080/mcpapi/server/demo/tools/add -d '[{"tool":{"name":"echo","description":"Echo back","inputSchema":{"type":"object"}},"behavior":{"echo":true}}]'

# Call the tool through the admin API (no MCP client needed)
curl -X POST localhost:8080/mcpapi/server/demo/tool/echo/call -d '{"text":"hello"}'
```
An MCP client can now connect to `http://localhost:8080/mcp/demo` (or `http://localhost:8080/mcp/sse/demo` over SSE).

# MCP Admin APIs
These APIs are used to add/remove/configure MCP servers and their components (tools, prompts, resources, resource templates) to be exposed by `Goto`.

# <a name="mcp-server-apis"></a>

## Server Management

### <a name="tool-schema-api"></a>Tool Schema API
Tool input schemas can be registered once, and then referenced by name from the `schema` field of any tool. See [ToolSchema](#toolschema).

|METHOD|URI|Description|
|---|---|---|
| POST | /mcpapi/schemas/add | Register one or more tool input schemas from the request's JSON array payload: `[ToolSchema, ...]`. Registering an existing name replaces it. Returns `400` if the payload can't be parsed. |

## <a name="servers-apis"></a>Servers APIs (apply to all MCP servers configured for a port)

|METHOD|URI|Description|
|---|---|---|
| GET | /mcpapi/servers | Get details of the configured MCP servers on the current port. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET | /mcpapi/servers/all | Get details of all configured MCP servers across all ports. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET | /mcpapi/servers/names | Get a summary map of MCP server names to their tool names, grouped by port. |
| POST | /mcpapi/servers/add | Add one or more MCP servers from the request's JSON array payload. [See MCPServerPayload Schema](#mcpserverpayload). Returns `400` if the payload is invalid, a server has no name, or a `uriRegex` doesn't compile. |
| POST | /mcpapi/servers/start | Enable all MCP servers on the current port. A server added through the API is disabled unless its payload has `enabled: true`. |
| POST | /mcpapi/servers/stop | Disable all MCP servers on the current port. Requests for a disabled server are served by the `default-stateless` server instead. |
| POST | /mcpapi/servers/clear | Clear all component state (tools, prompts, resources, templates, payloads, sessions) from all servers on the current port. |
| POST | /mcpapi/servers/clear/all | Remove all MCP servers across all ports, resetting global state. |
| GET | /mcpapi/servers/{kind} | Get all components of the given `{kind}` from all MCP servers across all ports, as a map of component name to server name to component. `{kind}` must be one of `tools`, `prompts`, `resources`, `templates`. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET | /mcpapi/servers/status | Get all configured MCP status rules across all ports. |
| POST | /mcpapi/servers/status/clear | Clear all configured MCP status rules on the current port. |


## <a name="server-apis"></a>Server APIs (apply to a specific MCP server on a port)
|METHOD|URI|Description|
|---|---|---|
| GET | /mcpapi/server/{server} | Get details of a specific MCP server by name. See [MCPServer](#mcpserver). Returns `400` if the server is not found on any port. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| POST | /mcpapi/server/{server}/route?uri={uri} | Configure the given MCP server to be served over the specified URI (the `uri` query param is required, and is lowercased). All tools that the server has at this point are also registered on their corresponding subpaths under the given URI. An SSE URI is auto-generated by inserting `/sse` into the path after `/mcp`. Tools that are added later are routed under the server's own `uri`, so prefer setting `uri` in the server's payload. |
| POST | /mcpapi/server/{server}/start | Mark a specific MCP server as enabled. Returns `400` if the server is not found. |
| POST | /mcpapi/server/{server}/stop | Mark a specific MCP server as disabled. Requests for it are served by the `default-stateless` server instead. Returns `400` if the server is not found. |
| POST | /mcpapi/server/{server}/clear | Clear all component state from a specific MCP server. Returns `400` if the server is not found. |


#### <a name="component-management"></a>A Server's Component Management (Tools, Prompts, Resources, Templates)

|METHOD|URI|Description|
|---|---|---|
| POST | /mcpapi/server/{server}/{kind}/add | Add one or more components of the given `{kind}` to the specified server. `{kind}` must be one of `tools`, `prompts`, `resources`, `templates`. Accepts a JSON array of component objects. See [MCPTool](#mcptool), [MCPPrompt](#mcpprompt), [MCPResource](#mcpresource), [MCPResourceTemplate](#mcpresourcetemplate) schemas. Returns `400` if the server is not found or a component is invalid, e.g. a tool whose `behavior` needs a missing `config`, or has no input schema. |
| GET | /mcpapi/server/{server}/{kind} | Get all components of the given `{kind}` from a specific MCP server. Returns `400` if the server is not found. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET | /mcpapi/server/{server}/{kind}/{name} | Get a specific named component of the given `{kind}` from a specific MCP server. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |

#### <a name="component-payload-configuration"></a>Component Payload Configuration

A component's response [Payload](#payload) can be given in the component's `response` field when it is added, or set afterwards with these APIs. Which part of the component uses it:
- Tools: the `stream` behavior streams it back; `elicit` and `sample` use its JSON (see [Tool Behavior Details](#tool-behavior-details)). Other behaviors ignore it.
- Prompts: it is the text of the `assistant` message of the prompt's result (`<No payload>` if not set).
- Resources and resource templates: a JSON payload is what the resource returns when read. Without one, the `text` of the resource/template is returned.

|METHOD|URI|Description|
|---|---|---|
| POST | /mcpapi/server/{server}/payload/{kind}/{name} | Set a static response payload for a component. `{kind}` must be one of `tools`, `prompts`, `resources`, `templates`. `{name}` is the component name. The body is treated as JSON unless the request header `ResponseContent-Type` is set to something other than `application/json` (e.g. `text/plain`), in which case it is text. A body that is meant to be JSON but isn't valid JSON is stored as the JSON `{"data": "<body>"}`. Returns `400` if the server or the component is not found. |
| POST | /mcpapi/server/{server}/payload/{kind}/{name}/stream/count={count} | Set a streaming response payload for a component. The body must be a JSON array (of JSON objects, or of strings for a text payload), and each element is one chunk. A text body that is not a JSON array of strings is a single chunk. `{count}` is the number of chunks sent per invocation: the chunks are replayed from the start if `{count}` is more than the number of elements. |
| POST | /mcpapi/server/{server}/payload/{kind}/{name}/stream/count={count}/delay={delay} | Set a streaming response payload with inter-chunk delay. `{delay}` format: `<min>-<max>` or `<min>-<max>:<delayCount>`, e.g. `100ms-500ms` or `1s-2s:5`. See [Delay format](#delay). |
| POST | /mcpapi/server/{server}/payload/completion?type={type} | Set the completion suggestions of the server for a completion reference type. `type` is the reference type of the client's completion request, e.g. `ref/prompt` or `ref/resource`. The body is a JSON array of strings, one per suggestion; any other body is a single suggestion. Returns the number of suggestions. Without a payload for the type, completion requests get the single suggestion `<No Completion>`. |
| POST | /mcpapi/server/{server}/payload/completion/delay={delay}?type={type} | Set completion suggestions with an inter-suggestion delay. `{delay}` format: `<min>-<max>` or `<min>-<max>:<delayCount>`. See [Delay format](#delay). |

#### <a name="tool-call-server-side"></a>Tool Call (server-side)

|METHOD|URI|Description|
|---|---|---|
| POST | /mcpapi/server/{server}/tool/{tool}/call | Directly invoke a tool on the specified MCP server via REST. Accepts a JSON object as the tool arguments (see [ToolCallArgs](#toolcallargs)) in the request body. Returns the tool's result as JSON. Returns `400` if the server or tool is not found, or the body is not valid JSON. There is no MCP client session for such a call, so the behaviors that need the client (`ping`, `elicit`, `sample`, `listRoots`) return an error message. |


#### <a name="mcp-status-management"></a>MCP Status Management

These APIs force an HTTP status code on the responses of a server. A status applies to `tools/call` requests: the tool call is still processed, but its HTTP response is sent with the status, along with the `Goto-Forced-Status` and `Goto-Forced-Status-Remaining` headers. If a status is set both for a server and for one of its tools, the server's status is used, and the tool's status is used only while the server has none. A tool can also force the status of its own response: the `status` behavior (with the `status` arg), and the `remote` behavior, which forces the HTTP status of the last response it got from the remote tool.

|METHOD|URI|Description|
|---|---|---|
| GET | /mcpapi/server/{server}/status | Report the status currently configured for the server, as a text line with the status and the number of times left. Reporting it doesn't count as using it. Returns `400` if the server is not found. |
| POST | /mcpapi/server/{server}/status/clear | Clear the status rules of the server's URI and of its tools' URIs on the current port. Rules that match by header are not cleared by this API, use `/mcpapi/servers/status/clear` for those. Returns `400` if the server is not found. |
| POST | /mcpapi/server/{server}/status/configure | Configure status rules from a JSON payload. [See StatusConfig Schema](#statusconfig). The URI match of the rule is always set to the server's URI. Returns the applied config. |
| POST | /mcpapi/server/{server}/status/set/{status} | Set an HTTP status code to return for MCP requests. `{status}` format: `<code>` or `<code>,<code>` or `<code>:<times>`, e.g. `500`, `404,503`, `500:10`. See [Status Path Parameter Format](#status-path-parameter-format). |
| POST | /mcpapi/server/{server}/tool/{tool}/status/set/{status} | Set a status code for calls to a specific tool. Returns `400` if the tool is not found. |
| POST | /mcpapi/server/{server}/status/set/{status}/header/{header}={value} | Set a status code for calls to this MCP server with the specified header present with the given value. |
| POST | /mcpapi/server/{server}/status/set/{status}/header/{header} | Set a status code for calls to this MCP server with the specified header present. |
| POST | /mcpapi/server/{server}/status/set/{status}/header/not/{header} | Set a status code for calls to this MCP server when the specified header is NOT present. |

Every `/mcpapi/server/{server}/.../status/set/...` URI above takes an optional query param `methods`, the HTTP methods that the status is for: a comma
separated list such as `methods=GET`, `methods=GET,POST`, or `methods=*` for all methods. Without it the status is for `POST`, which is how tools are called.
A status for `POST` applies to tool calls. A status for `GET` applies to the requests that open the stream of events of the server, which are answered with the
status instead of opening it. The statuses of different methods for the same server are separate: each has its own `times` count, and setting one again
changes it and leaves the others as they are.
---

## MCP JSON Schemas
# <a name="mcp-json-schemas"></a>

#### MCPServerPayload
<a name="mcpserverpayload"></a>

The `/mcpapi/servers/add` API accepts a JSON array of `MCPServerPayload` objects: `[MCPServerPayload, ...]`

|Field|Data Type|Required|Description|
|---|---|---|---|
| port | int | No | The port over which the MCP server will be exposed. Defaults to the port that the admin request was sent to. |
| name | string | Yes | MCP server name. Used as the name in the server's URI (`/mcp/{name}`), and referenced by other APIs for further configuration (e.g. adding tools). Names are case-insensitive. |
| version | string | Yes | Server version string, e.g. `"1.0"`. |
| description | string | No | Human-readable description of the MCP server. |
| instructions | string | No | Instructions text provided to MCP clients during initialization. |
| keepAlive | duration | No | Keep-alive interval for server sessions. A duration is a JSON number of nanoseconds, e.g. `0` for none. |
| pageSize | int | No | Page size for paginated list responses (tools, prompts, resources). |
| hasPrompts | bool | No | If `true`, advertises prompt capabilities to MCP clients. |
| hasResources | bool | No | If `true`, advertises resource capabilities to MCP clients. |
| hasTools | bool | No | If `true`, advertises tool capabilities to MCP clients. |
| stateless | bool | No | If `true`, creates a stateless MCP server (no session tracking). Default `false` (stateful). |
| enabled | bool | No | If `true`, the server is enabled and ready to serve requests. Default `false`: a server that is not enabled is replaced by the `default-stateless` server until it is started. |
| uri | string | No | Custom URI path for the MCP server, e.g. `"/mcp/my-server"`. The server is routed on it, and its tools on `{uri}/{tool-name}`. |
| uriRegex | string | No | Custom regex pattern for matching request URIs to this server, used for flexible routing. Must be a valid regex, or the request is rejected. |

#### MCPServer
<a name="mcpserver"></a>

The details of a server, as reported by the `GET` server APIs.

|Field|Data Type|Description|
|---|---|---|
| id | string | Server ID, `[name:port][namespace][cluster]`. |
| name, title, version | string | The name (as given in the payload), title (same as name) and version of the server. |
| description | string | Description from the payload. |
| host | string | Pod and listener label of the port that the server is on. |
| port | int | The port of the server. |
| tools | map[string][MCPTool](#mcptool) | Tools by name, including the built-in tools. |
| prompts | map[string][MCPPrompt](#mcpprompt) | Prompts by name. |
| resources | map[string][MCPResource](#mcpresource) | Resources by name. |
| templates | map[string][MCPResourceTemplate](#mcpresourcetemplate) | Resource templates by name. |
| completionPayload | map[string][Payload](#payload) | Completion suggestions by reference type. |
| stateless | bool | Whether the server is stateless. |
| enabled | bool | Whether the server is enabled. |
| uri | string | The server's URI. |
| sseURI | string | The server's SSE URI, if it has been routed. |
| toolsByURI | map[string][MCPTool](#mcptool) | Tools by their URI subpath. |
| uriRegex | string | The server's URI regex, if any. |

#### ToolSchema
<a name="toolschema"></a>

The `/mcpapi/schemas/add` API accepts a JSON array of `ToolSchema` objects: `[ToolSchema, ...]`. A tool uses a registered schema as its input schema by naming it in its `schema` field. The schemas `default` (an empty object schema) and `basic` (an object with `text`, `delay`, `count` and `size` properties) are always available.

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | Yes | Name of the schema, to be used as `MCPTool.schema`. |
| inputSchema | object | Yes | JSON Schema of the tool's input. It must have `type: object` to be used by a tool. |

#### MCPTool
<a name="mcptool"></a>

The `/mcpapi/server/{server}/tools/add` API accepts a JSON array of `MCPTool` objects: `[MCPTool, ...]`

|Field|Data Type|Required|Description|
|---|---|---|---|
| tool | [Tool](#tool) | Yes | The MCP tool definition including name, description, and input/output schemas. |
| schema | string | No | Name of a registered [tool schema](#toolschema) to use as the tool's input schema. Required if `tool.inputSchema` is not given. Adding the tool fails if no schema is registered under the name. |
| behavior | [ToolBehavior](#toolbehavior) | No | Configures the tool's built-in behavior. Set one behavior flag; a tool without one streams (see [ToolBehavior](#toolbehavior)). |
| config | [ToolConfig](#toolconfig) | No | Configuration for the tool's behavior, e.g. remote call targets, delays, and stream settings. Adding the tool fails with `Incomplete remote configs` if the behavior needs a remote config that is missing. |
| uri | string | No | Custom URI subpath for this tool. Defaults to `"/<tool-name>"` (lowercased). |
| serverURI | string | No | Output only: the full URI of the tool, which is the server's URI followed by `uri`. |
| response | [Payload](#payload) | No | Pre-configured static or streaming response payload for the tool. |
| proxy | bool | No | If `true`, the tool is a proxy that forwards calls to a remote MCP server, through the MCP proxy of the port. Requires `config.remote.url`. |

#### Tool
<a name="tool"></a>

The MCP protocol Tool definition (from `go-sdk/mcp`).

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | Yes | Programmatic name of the tool. Used for identification in tool calls. |
| title | string | No | Human-readable display title for the tool. |
| description | string | No | Human-readable description of what the tool does. Helps LLMs understand tool purpose. |
| inputSchema | object | Yes, unless `MCPTool.schema` is given | JSON Schema object defining the expected input parameters for the tool. It must have `type: object`. |
| outputSchema | object | No | JSON Schema object defining the structure of the tool's output. |
| annotations | [ToolAnnotations](#toolannotations) | No | Optional annotations describing tool behavior characteristics. |

The SDK's `_meta` and `icons` fields are accepted too.

#### ToolAnnotations
<a name="toolannotations"></a>

|Field|Data Type|Required|Description|
|---|---|---|---|
| destructiveHint | bool | No | If `true`, the tool may perform destructive updates to its environment. Default `true`. |
| idempotentHint | bool | No | If `true`, calling the tool repeatedly with the same arguments has no additional effect. Default `false`. |
| openWorldHint | bool | No | If `true`, the tool may interact with external entities. Default `true`. |
| readOnlyHint | bool | No | If `true`, the tool does not modify its environment. Default `false`. |
| title | string | No | Human-readable title for the tool annotation. |

#### ToolBehavior
<a name="toolbehavior"></a>

Configures what the tool does when invoked. Set one flag to `true`. If several are set, the first of these that is set is the behavior: `echo`, `status`, `ping`, `time`, `listRoots`, `sample`, `elicit`, `fetch`, `remote`, `agents`, `serverDetails`, `serverPaths`, `allServers`, `allComponents`, `addTool`. A tool for which none of them is set runs the stream behavior, which is also what `stream` and `resumable` select. [Tool Behavior Details](#tool-behavior-details) has more on each.

|Field|Data Type|Required|Description|
|---|---|---|---|
| ping | bool | No | Pings the MCP client. Needs a client session. |
| echo | bool | No | Echoes back the tool call's `text` and `metadata` arguments. |
| time | bool | No | Returns the current server time and the tool call's arguments. |
| status | bool | No | Reports a status: `200`, or the status given in the `status` argument, which is also forced as the HTTP status of the tool call's response. |
| stream | bool | No | Streams the response payload back in chunks, with a progress event per chunk. Uses `config.streamCount` and `config.delay`. This is also what a tool runs when no behavior is set, but without the progress events. |
| elicit | bool | No | Triggers an elicitation reverse invocation to the MCP client during tool execution. |
| sample | bool | No | Triggers a createMessage (sampling) reverse invocation to the MCP client during tool execution. |
| listRoots | bool | No | Requests the MCP client's root list during tool execution. |
| fetch | bool | No | Fetches data from a remote HTTP endpoint (non-MCP). Requires `config.remote` with a URL. |
| remote | bool | No | Forwards the tool call to a remote MCP server's tool. Requires `config.remote` with a URL. |
| multiRemote | bool | No | Requires a non-empty `config.multiRemote`, but is not implemented as a behavior: a tool with only this flag runs as the default stream behavior. |
| resumable | bool | No | Makes the stream behavior resumable: each call sends at most 2 chunks, and the next call of the session resumes where the previous one stopped. |
| agents | bool | No | Calls an A2A (Agent-to-Agent) agent. Requires `config.agent` with an `agentURL`. |
| serverDetails | bool | No | Returns the current MCP server's details. Auto-added to every server as tool `ServerDetails`. |
| serverPaths | bool | No | Returns all registered server URI paths. Auto-added to every server as tool `ServerPaths`. |
| allServers | bool | No | Returns details of all MCP servers on the port. Auto-added to every server as tool `ListServers`. |
| allComponents | bool | No | Returns all registered components from all servers. Auto-added to every server as tool `ListComponents`. |
| addTool | bool | No | Adds a tool to the server on the fly, from the call's arguments. Auto-added to every server as tool `AddTool`. |

#### ToolConfig
<a name="toolconfig"></a>

|Field|Data Type|Required|Description|
|---|---|---|---|
| remote | [ToolCall](#toolcall) | No | Remote MCP tool call configuration. Required (with a `url`) when `behavior.remote`, `behavior.fetch` or `proxy` is set. For `fetch`, only `url`, `authority` and `headers` are used. |
| multiRemote | \[\]\[[ToolCall](#toolcall)\] | No | Array of arrays of remote MCP tool calls, required to be non-empty by `behavior.multiRemote`. Not used by any behavior at present. |
| agent | [AgentCall](#agentcall) | No | A2A agent call configuration. Required (with an `agentURL`) when `behavior.agents` is `true`. |
| delay | [Delay](#delay) | No | Delay to apply during tool execution, and between the chunks of a stream. A `delay` argument in the tool call overrides it. |
| streamCount | int | No | Number of chunks to send when streaming, unless the call has a `count` argument. The larger of this and the response payload's `streamCount` is used. |

#### ToolCall
<a name="toolcall"></a>

Configuration for calling a remote MCP tool (used in `ToolConfig.remote` and `ToolConfig.multiRemote`).

|Field|Data Type|Required|Description|
|---|---|---|---|
| tool | string | Yes | MCP tool name to invoke on the remote server. |
| url | string | Yes | MCP server URL to connect to. |
| sseURL | string | No | URL to use instead of `url` when the call is made over SSE: the inbound call came over SSE, or `forceSSE` or the `remoteArgs.sse` argument is set. |
| server | string | No | Server identifier. Defaults to `authority` if not set. |
| authority | string | No | Authority (Host header value) to use when connecting to the MCP server. |
| h2 | bool | No | If `true`, connects over HTTP/2. |
| tls | bool | No | If `true`, connects over TLS. |
| forceSSE | bool | No | If `true`, forces SSE transport instead of Streamable HTTP. Default `false`. |
| neat | bool | No | If `true`, the client doesn't add the `Host` (from `authority`) and `User-Agent` headers to its requests. Default `false`. |
| args | [ToolCallArgs](#toolcallargs) | No | Arguments to pass to the remote MCP tool. The call's `remoteArgs` are merged over them. |
| headers | [Headers](#headers) | No | Request/response header manipulation configuration. |
| delay | string | No | Delay range, e.g. `"100ms-500ms"` or `"1s-5s:3"`. See [Delay format](#delay). It is not applied to the call: use `initialDelay`/`requestDelay` to delay on the client side, or the `delay` arg to have the remote tool delay its response. |
| requestCount | int | No | Number of times to call the remote tool. Default `1`. The `count` argument of the tool call overrides it. |
| concurrent | int | No | Number of concurrent calls to make in a round. Default `1`. |
| initialDelay | string | No | Delay before the first call, e.g. `"1s"` or `"1s-3s"`. |
| requestDelay | string | No | Delay between rounds of concurrent calls, when there is more than one round. |
| requestTimeout | string | No | Timeout of each call, e.g. `"30s"`. Defaults to `10h`, which is also used if the value is not a valid positive duration. |
| resultOnly | bool | No | Reduces the verbosity of the result: the remote tool's text content and the events of its timeline are left out. Also set when the tool call has the `resultOnly` argument. |
| noEvents | bool | No | Leaves the timeline events out of the result. Also set when the tool call has the `noEvents` argument. |
| noCallDetails | bool | No | Leaves the per-call details (remote call data and content) out of the result. |
| retryDelay, retriableStatusCodes, requestId, forcedStatus, stream | various | No | Accepted, but not used by calls made from a tool of this server. |

#### AgentCall
<a name="agentcall"></a>

Configuration for calling an A2A (Agent-to-Agent) agent (used in `ToolConfig.agent`).

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | No | Name identifier for the agent. |
| agentURL | string | Yes | URL of the A2A agent endpoint. |
| cardURL | string | No | URL of the agent's card (metadata endpoint). |
| authority | string | No | Authority (Host header value) to use when connecting to the agent. |
| h2 | bool | No | If `true`, connects over HTTP/2. |
| tls | bool | No | If `true`, connects over TLS. |
| delay | string | No | Delay range. See [Delay format](#delay). It is not applied to the call: use `initialDelay`/`requestDelay`. |
| message | string | No | Message text to send to the agent, unless the tool call's `remoteArgs.agentMessage` is given. |
| args | map[string]any | No | Additional data to pass to the agent, unless the tool call's `remoteArgs.agentArgs` is given. |
| headers | [Headers](#headers) | No | Request/response header manipulation configuration. |
| requestCount | int | No | Number of times to call the agent. Default `1`. |
| concurrent | int | No | Number of concurrent calls to make in a round. Default `1`. |
| initialDelay | string | No | Delay before the first call. |
| requestDelay | string | No | Delay between rounds of concurrent calls. |
| requestTimeout | string | No | Timeout of each call, e.g. `"30s"`. Defaults to `10h`. |
| resultOnly | bool | No | Reduces the verbosity of the agent's result. Also set when the tool call has the `resultOnly` argument. |
| noEvents | bool | No | Leaves the timeline events out of the result. Also set when the tool call has the `noEvents` argument. |
| noCallDetails | bool | No | Leaves the per-call details out of the result. |
| retryDelay, retriableStatusCodes, requestId, forcedStatus, stream | various | No | Accepted, but not used by calls made from a tool of this server. |

#### ToolCallArgs
<a name="toolcallargs"></a>

The arguments of a tool call: the JSON object that an MCP client sends as the tool's `arguments`, or that is sent in the body of `/mcpapi/server/{server}/tool/{tool}/call`. A tool's input schema should declare the ones that its behavior uses. Fields that a behavior doesn't use are ignored.

|Field|Data Type|Description|
|---|---|---|
| delay | string | Delay range for this call, e.g. `"100ms-500ms"` or `"1s-5s:3"`, overriding `config.delay`. See [Delay format](#delay). |
| count | int | Number of chunks to stream (`stream`), number of HTTP requests to make (`fetch`), or number of requests to make to the remote tool (`remote`). |
| size | int | Length of the random text generated for each chunk by a `stream` tool that has neither a `text` argument nor a response payload. Default `10`. |
| text | string | The text to echo (`echo`), or the text to send in every chunk (`stream`). |
| status | int | The HTTP status to force on the response (`status`). |
| metadata | map[string]string | Metadata, which `echo` reports back. |
| resultOnly | bool | Reduces the verbosity of the results of the remote calls that the tool makes: the remote content and the events of the remote timeline are left out. |
| noEvents | bool | Leaves the timeline events out of the result (and out of the progress notifications). |
| remoteArgs | ToolCallArgs | The arguments for the remote call of `fetch`, `remote` and `agents` tools. They override the tool's `config.remote`/`config.agent`, as described below. |
| toolDef | object | Set by the server to the whole arguments object of the call, which is what `addTool` reads. A value that is sent is replaced. |
| tool, agent, url, authority, sse, headers, forwardHeaders, agentMessage, agentArgs | various | Used inside `remoteArgs`, where they are `tool` (remote tool), `agent` (agent name), `url`, `authority`, `sse` (use SSE), `headers` ([Headers](#headers)), `forwardHeaders` (names of the inbound request headers to forward), `agentMessage` and `agentArgs` (the agent's message and data). |

How `remoteArgs` is used by each behavior:
- `fetch`: `remoteArgs.url`, `remoteArgs.authority` and `remoteArgs.headers` override `config.remote`; headers are merged.
- `remote`: `remoteArgs.tool`, `url`, `authority` and `headers` override `config.remote`. The other fields of `remoteArgs` (e.g. `text`, `count`, `status`, `delay`, `metadata`, and a nested `remoteArgs` for chained calls) are merged over `config.remote.args` to make the arguments sent to the remote tool.
- `agents`: `remoteArgs.agent`, `url`, `authority`, `agentMessage`, `agentArgs` and `headers` override the `name`, `agentURL`, `authority`, `message`, `args` and `headers` of `config.agent`.

#### Tool Behavior Details
<a name="tool-behavior-details"></a>

- **echo**: returns the `text` and `metadata` arguments back, along with the server, listener and time (`<No input to echo>` if there are none). Applies the delay.
- **ping**: pings the client over its session. **time**: returns the current time and the call's arguments.
- **status**: returns `Status: Success [200]`, or `Status: Requested [<status>]` if the `status` argument is given, in which case the HTTP status of the response is forced to it. Applies the delay.
- **stream**: sends the tool's response payload as chunks (text `[<n>] <chunk>`), with a delay between chunks. The number of chunks is the `count` argument, or else the larger of the payload's `streamCount` and `config.streamCount`. Without a response payload, every chunk is the `text` argument or a random string of `size` characters, and the delay defaults to a random `0-1s`. The delay is the larger of the call's (or `config.delay`) and the payload's. With the `stream` flag the tool also reports a progress event per chunk, and its response is sent as it is produced instead of at the end of the call.
- **resumable**: the stream behavior that sends at most 2 chunks per call. It saves its progress (and the call's arguments and delay) in Goto's registry locker, per MCP session and tool, and the next call from the session continues after the chunks that were sent.
- **elicit**: sends an elicitation request to the client, with the `message` and the `requestedSchema` (its `type` and the `type` of each property) read from the tool's response payload JSON. Set it with the tool's `response` (`{"json": {"message": "...", "requestedSchema": {"type": "object", "properties": {"name": {"type": "string"}}}}}`) or with the payload API. Returns the client's answer.
- **sample**: sends a `createMessage` (sampling) request to the client: the user message is the tool's response payload as JSON, the system prompt is the tool's `description`, with `maxTokens` 10 and `includeContext` set to `allServers`. Returns the model, role, stop reason and content of the client's reply.
- **listRoots**: asks the client for its roots, and returns each as `<name> [uri: <uri>]`, or `no roots`.
- **fetch**: makes an HTTP `GET` to `config.remote.url` (`http://` is assumed if there is no scheme; an `https:` URL is called over TLS), `count` times (default once), with the `User-Agent` set to the tool's label, the `Host` set to `authority` if there is one, and the `request.add` and `request.forward` headers of the configs. Applies the delay after every request. Returns the response bodies as text content, and as an array in `structuredContent`. Failed requests mark the result as an error.
- **remote**: calls the remote MCP tool as described in [ToolCall](#toolcall) and [ToolCallArgs](#toolcallargs). The result is the timeline of the remote call, which includes the remote call details, the upstream headers and statuses. The HTTP status of the last response from the remote tool is forced on the response of this tool. Applies the delay after the call. A failure is reported as an error result.
- **agents**: loads the agent card, and calls the agent (streaming, if its card says so), reporting the agent's progress to the client. Returns the agent's result with the upstream data in `structuredContent`.
- **serverDetails**, **serverPaths**, **allServers**, **allComponents**: return the JSON of the server (same as `GET /mcpapi/server/{server}`), of the routes of all servers, of all the servers of the port, and of all the components of all servers, respectively.
- **addTool**: adds a tool to the server. It reads `name`, `description`, `schema` (a registered [tool schema](#toolschema)) and `behavior` ([ToolBehavior](#toolbehavior)) from the call's arguments. The new tool has no `config`, so the behaviors that need one (`fetch`, `remote`, `agents`) can't be added this way. The result is marked as an error if the tool can't be added.

Results: a tool's result has text `content`, and `structuredContent` which, unless the behavior returns its own, is the call's timeline: its `Events` (left out with `noEvents`), `Data`, `Status`, `UpstreamHeaders` and `UpstreamStatuses`. The events are also sent to the client as progress notifications.

#### MCPPrompt
<a name="mcpprompt"></a>

The `/mcpapi/server/{server}/prompts/add` API accepts a JSON array of `MCPPrompt` objects: `[MCPPrompt, ...]`

When a prompt is requested, its result has two messages: a `user` message with the requested arguments as JSON, and an `assistant` message with the text of the prompt's `response` payload (`<No payload>` if it has none).

|Field|Data Type|Required|Description|
|---|---|---|---|
| prompt | [Prompt](#prompt) | Yes | The MCP prompt definition including name, description, and arguments. |
| uri | string | No | Accepted, but not used for prompts. |
| response | [Payload](#payload) | No | Pre-configured response payload for the prompt. |
| proxy | bool | No | Accepted, but not used for prompts. |

#### Prompt
<a name="prompt"></a>

The MCP protocol Prompt definition (from `go-sdk/mcp`).

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | Yes | Programmatic name of the prompt. |
| title | string | No | Human-readable display title for the prompt. |
| description | string | No | Description of what this prompt provides. |
| arguments | \[[PromptArgument](#promptargument)\] | No | List of arguments for templating the prompt. |

#### PromptArgument
<a name="promptargument"></a>

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | Yes | Programmatic name of the argument. |
| title | string | No | Human-readable display title for the argument. |
| description | string | No | Human-readable description of the argument. |
| required | bool | No | If `true`, this argument must be provided. Default `false`. |

#### MCPResource
<a name="mcpresource"></a>

The `/mcpapi/server/{server}/resources/add` API accepts a JSON array of `MCPResource` objects: `[MCPResource, ...]`

When a resource is read, it returns the JSON of its `response` payload if that is a JSON payload, or else its text.

|Field|Data Type|Required|Description|
|---|---|---|---|
| resource | [Resource](#resource) | Yes | The MCP resource definition including name, URI, and optional metadata. Its `uri` must be a valid URI. The resource's text is given as `resource.text`. |
| resource.text | string | No | Text content of the resource, given inside the `resource` object. Defaults to `"<No Text>"` if not provided. A `text` outside of `resource` is ignored. |
| uri | string | No | Accepted, but not used for resources. |
| response | [Payload](#payload) | No | Pre-configured response payload for the resource. |
| proxy | bool | No | Accepted, but not used for resources. |

#### Resource
<a name="resource"></a>

The MCP protocol Resource definition (from `go-sdk/mcp`).

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | Yes | Programmatic name of the resource. |
| title | string | No | Human-readable display title for the resource. |
| description | string | No | Description of what this resource represents. |
| uri | string | Yes | The URI of this resource. |
| mimeType | string | No | MIME type of the resource, e.g. `"text/plain"`, `"application/json"`. |
| size | int | No | Size of the raw resource content in bytes. |

#### MCPResourceTemplate
<a name="mcpresourcetemplate"></a>

The `/mcpapi/server/{server}/templates/add` API accepts a JSON array of `MCPResourceTemplate` objects: `[MCPResourceTemplate, ...]`

|Field|Data Type|Required|Description|
|---|---|---|---|
| template | [ResourceTemplate](#resourcetemplate) | Yes | The MCP resource template definition including name, URI template, and metadata. |
| template.text | string | No | Text content of the template, given inside the `template` object. Defaults to `"<No Text>"` if not provided. A `text` outside of `template` is ignored. |
| uri | string | No | Accepted, but not used for templates. |
| response | [Payload](#payload) | No | Pre-configured response payload for the template. |
| proxy | bool | No | Accepted, but not used for templates. |

#### ResourceTemplate
<a name="resourcetemplate"></a>

The MCP protocol Resource Template definition (from `go-sdk/mcp`).

|Field|Data Type|Required|Description|
|---|---|---|---|
| name | string | Yes | Programmatic name of the resource template. |
| title | string | No | Human-readable display title for the template. |
| description | string | No | Description of what this template is for. |
| uriTemplate | string | Yes | URI template (RFC 6570) for constructing resource URIs, e.g. `"file:///{path}"`. |
| mimeType | string | No | MIME type for all resources matching this template. |

#### StatusConfig
<a name="statusconfig"></a>

The `/mcpapi/server/{server}/status/configure` API accepts a `StatusConfig` JSON object.

|Field|Data Type|Required|Description|
|---|---|---|---|
| port | int | No | The port to apply this status config to. Defaults to the current request's port. |
| statuses | []int | Yes | Array of HTTP status codes to return. Codes are cycled through on successive requests. A `0` is treated as `200`. |
| times | int | No | Number of times to apply the status before reverting. `-1` for infinite. Defaults to `-1`. |
| match | [StatusMatch](#statusmatch) | No | Optional matching criteria to selectively apply the status. |

The applied config is returned, with the read-only fields `lastStatusIndex`, `lastStatus` and `statusCount`.

#### StatusMatch
<a name="statusmatch"></a>

|Field|Data Type|Required|Description|
|---|---|---|---|
| uri | [StatusURIMatch](#statusurimatch) | No | URI matching criteria. For `status/configure`, `exact` is always set to the server's URI. |
| headers | \[[StatusHeaderMatch](#statusheadermatch)\] | No | Array of header matching criteria. |
| methods | []string | No | HTTP methods that the status applies to, e.g. `["POST"]`. If empty, it applies to all methods (the `set` APIs use `POST` unless `methods` is given). |

#### StatusURIMatch
<a name="statusurimatch"></a>

|Field|Data Type|Required|Description|
|---|---|---|---|
| prefix | string | No | Match URIs starting with this prefix. |
| exact | string | No | Match URIs exactly equal to this value. |
| regex | string | No | Match URIs against this regex pattern. |
| not | bool | No | If `true`, inverts the match (apply status when URI does NOT match). |

#### StatusHeaderMatch
<a name="statusheadermatch"></a>

|Field|Data Type|Required|Description|
|---|---|---|---|
| header | string | Yes | Header name to match. |
| value | string | No | Header value to match. If empty, matches presence of the header regardless of value. |
| present | bool | No | If `true`, match when header is present. If `false`, match when header is absent. Default `true`. |

#### Payload
<a name="payload"></a>

Pre-configured response payload for a component. Set through the `response` field of the component, or with the `/mcpapi/server/{server}/payload/{kind}/{name}` APIs.

When read from JSON, the payload is a stream if it has a `streamCount` above 0, or has any of `jsonStream`, `textStream`, `rawStream`; else it is `json` if that is given, else `text`, else `raw`.

|Field|Data Type|Required|Description|
|---|---|---|---|
| isStream | bool | No | Set automatically for a streaming payload (see above), not read from the input. |
| streamCount | int | No | Number of chunks for streaming. Defaults to the number of chunks in the stream array. |
| delay | [Delay](#delay) | No | Inter-chunk delay for streaming payloads. |
| json | object | No | JSON response payload. |
| text | string | No | Text response payload. |
| raw | any | No | Raw response payload, returned as it is. |
| jsonStream | []object | No | Array of JSON objects streamed as individual chunks. |
| textStream | []string | No | Array of text strings streamed as individual chunks. |
| rawStream | []any | No | Array of raw values streamed as individual chunks. |

#### Headers
<a name="headers"></a>

|Field|Data Type|Description|
|---|---|---|
| request | [HeadersConfig](#headersconfig) | Headers to manipulate on outgoing requests. |
| response | [HeadersConfig](#headersconfig) | Headers to manipulate on incoming responses. |

#### HeadersConfig
<a name="headersconfig"></a>

|Field|Data Type|Description|
|---|---|---|
| add | map[string]string | Headers to add as key-value pairs. |
| remove | []string | Header names to remove. |
| forward | []string | Header names to forward from the original inbound request to the outgoing request. |

#### Delay
<a name="delay"></a>

|Field|Data Type|Description|
|---|---|---|
| min | duration | Minimum delay duration (e.g. `"100ms"`, `"1s"`). |
| max | duration | Maximum delay duration. A random value between `min` and `max` is used. |
| count | int | Number of times to apply the delay. `-1` (the default) for unlimited. |

When used as a path parameter or tool argument string (e.g. in `/delay={delay}`), the format is `"<min>-<max>"` or `"<min>-<max>:<count>"`, e.g. `"100ms-500ms"` or `"1s-2s:5"`. A single duration (e.g. `"1s"`) is a fixed delay.

#### Status Path Parameter Format
<a name="status-path-parameter-format"></a>

The `{status}` path parameter in `/mcpapi/server/{server}/status/set/{status}` accepts the following formats:
- Single status code: `500`
- Multiple comma-separated codes: `404,503` (cycled through on successive requests)
- With repeat count: `500:10` (apply 10 times, then revert)
- Combined: `404,503:5` (cycle through 404 and 503, apply 5 times total)
