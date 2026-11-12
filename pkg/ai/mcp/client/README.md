
## <a name="mcp-client-features"></a>MCP Client Features
The `ai/mcp/client` sub-package implements a generic MCP client with admin APIs to let users invoke a target MCP server via MCP protocol. The client APIs are meant to test a remote MCP server's presence and availability from test scripts using REST APIs without having to implement MCP protocol client in the test script.

Features:
- **REST to MCP conversion**: `POST` a `ToolCall` payload to `/mcpapi/client/call/tool`. `Goto` connects to the target MCP server, calls the tool via MCP protocol, and returns the result (plus request/response headers, statuses, `Via-Goto` hops and the remote `Timeline`, if any) as the REST response.
- **Tool listing**: list the tools (or tool names) of an MCP server, or all of its tools, prompts and resources, using a simple `GET` or `POST` with query params.
- **Transports**: Streamable HTTP (default) or SSE, over plain HTTP, TLS or HTTP/2, with a custom `authority` (Host header), custom request headers, and a configurable request timeout.
- **Load generation**: a single tool call can be repeated `requestCount` times, `concurrent` requests at a time, with initial and per-round delays.
- **Client-side callbacks**: configure how the client answers the reverse invocations that an MCP server makes during a tool call: `sample` (`sampling/createMessage`), `elicit` (elicitation) and `roots` (roots list). Callback behavior can be configured via APIs or via the `payloads.client` section of the MCP config at startup.

Contents:
- [MCP Client APIs](#mcp-client-apis)
- [Schemas](#mcp-client-schemas)
- [Response Formats](#mcp-client-responses)
- [Client Behavior](#mcp-client-behavior)
- [Sample Commands](#mcp-client-samples)

### MCP Client APIs
The MCP client APIs allow using a REST API to list an MCP server's tools, call an MCP tool with REST<->MCP conversion, and set the client side behavior for `elicit` and `sample` reverse invocations from MCP server to the client, as well as the client's MCP roots.



# <a name="mcp-client-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET | /mcpapi/client/details      | Get client-side payloads (sample, elicit, roots) of all named MCP clients, as a map of name to payload (`{}` if none). Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET | /mcpapi/client/{name}/details      | Get client-side payloads configured for a named MCP client. If no payload exists for the name, the fallback payload is returned (see [Client Behavior](#mcp-client-behavior)). Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET, POST | /mcpapi/client/list/all      | List all tools, prompts, and resources from the MCP server pointed to by the `url` query param. Optionally accepts `sse`, `tls` and `authority` query params. |
| GET, POST | /mcpapi/client/list/tools      | List only tools from the MCP server pointed to by the `url` query param. Optionally accepts `sse`, `tls` and `authority` query params. |
| GET, POST | /mcpapi/client/list/tools/names      | List only the names of tools from the MCP server pointed to by the `url` query param. Optionally accepts `sse`, `tls` and `authority` query params. |
| POST | /mcpapi/client/call/tool      | Call an MCP tool on a remote MCP server. Accepts a `ToolCall` payload (JSON) in the request body. Connects to the specified MCP server, invokes the tool (once, or `requestCount` times), and returns the result. See [/mcpapi/client/call/tool Response](#mcp-client-call-response). |
| POST | /mcpapi/client/payload/{kind}      | Set the client-side payload for `elicit` or `sample` reverse invocations, without a client name (stored under the empty name `""`). `{kind}` must be `sample` or `elicit`. Accepts an `MCPClientPayload` JSON body. |
| POST | /mcpapi/client/{name}/payload/{kind}      | Set a named client-side payload for `elicit` or `sample` reverse invocations. `{kind}` must be `sample` or `elicit`. Accepts an `MCPClientPayload` JSON body. |
| POST | /mcpapi/client/payload/roots      | Set the client-side MCP roots, without a client name (stored under the empty name `""`). Accepts a JSON array of `Root` objects in the request body. |
| POST | /mcpapi/client/{name}/payload/roots      | Set MCP roots for a named client. Accepts a JSON array of `Root` objects in the request body. |

---

### <a name="mcp-client-schemas"></a>Schemas

#### ToolCall
The request body of `/mcpapi/client/call/tool`. Accepts a JSON or a YAML body, using the field names below. Both `tool` and `url` are required, otherwise the call is rejected with `400`.

|Field|Data Type|Required|Description|
|---|---|---|---|
| tool | string | Yes | MCP tool name to invoke. |
| url | string | Yes | MCP server URL to connect to. If it has no `http://` or `https://` prefix, `http://` is assumed, or `https://` when `tls` is `true`. |
| server | string | No | Server identifier, sent to the MCP server in the `Goto-MCP-Server` request header. Defaults to `authority` if not set. |
| authority | string | No | Authority (Host header value) to use when connecting to the MCP server. When `neat` is `true`, the `Host` request header is not set from it. |
| h2 | bool | No | If `true`, use HTTP/2 for the connection. Default `false`. |
| tls | bool | No | If `true`, use TLS for the connection. Default `false`. |
| forceSSE | bool | No | If `true`, uses the SSE transport (`url` must then be the server's SSE endpoint) instead of Streamable HTTP transport. Default `false`. |
| neat | bool | No | If `true`, the client doesn't set its default `Host` (from `authority`) and `User-Agent` (client's caller ID) request headers. Default `false`. |
| args | ToolCallArgs | No | Arguments passed to the MCP tool. See `ToolCallArgs` schema. |
| headers | Headers | No | Request/response header manipulation configuration. See `Headers` schema. |
| delay | string | No | Delay text, e.g. `"100ms-500ms"` or `"1s-5s:3"`, that is passed to the remote tool as `args.delay` (unless `args.delay` is already set), for the tool to apply on the server side. It is not a client-side delay; see `initialDelay` and `requestDelay`. |
| requestCount | int | No | Total number of tool call requests to make. Default `1`. |
| concurrent | int | No | Number of concurrent requests per round. Default `1`, and never more than `requestCount`. Number of rounds = `requestCount / concurrent`, rounded up. |
| initialDelay | string | No | Client-side delay to apply once before the first round of requests. Same format as `delay`. |
| requestDelay | string | No | Client-side delay to apply between rounds of requests (not after the last round). Same format as `delay`. |
| requestTimeout | string | No | Timeout for each request, as a duration string, e.g. `"30s"`, `"2m"`. Defaults to `10h` if not set, not parsable, or not positive. |
| resultOnly | bool | No | If `true`, the MCP content of the responses is left out of `CallResults` (structured data and remote results are still reported). Also passed to the remote tool in `args`. |
| noEvents | bool | No | If `true`, timeline events are not streamed in the HTTP response, and the returned `Timeline` has no `Events`. Also passed to the remote tool in `args`. |
| noCallDetails | bool | No | If `true`, `CallResults` is cleared from the returned result. |
| sseURL, retryDelay, retriableStatusCodes, requestId, forcedStatus, stream | various | No | Accepted in the payload, but not used by `/mcpapi/client/call/tool`. |

#### ToolCallArgs
The `args` of a `ToolCall` are sent to the MCP tool as its call arguments, so their meaning is defined by the target tool (see the [MCP server README](../server/README.md) for the tools of a `Goto` MCP server). The client adds its own `metadata` entries to every request (`goto-client`, and the request, session and caller IDs). Only the fields listed here are sent; other keys in `args` are dropped.

|Field|Data Type|Description|
|---|---|---|
| text | string | Text input for the tool (e.g. the text for `echo` to echo back). |
| count | int | Count input for the tool (e.g. the number of responses for a streaming tool). |
| size | int | Size input for the tool (e.g. the length of generated text for a streaming tool when no `text` is given). |
| delay | string | Delay text for the tool to apply on the server side, e.g. `"1s"` or `"100ms-500ms:3"`. Defaults to `ToolCall.delay`. |
| status | int | HTTP status for the tool to report, e.g. the `status` tool of a `Goto` MCP server forces its MCP response to this status (see [/mcpapi/client/call/tool Response](#mcp-client-call-response)). |
| resultOnly | bool | Asks the tool to report only its result. Set to `true` if `ToolCall.resultOnly` is `true`. |
| noEvents | bool | Asks the tool to leave timeline events out of its response. Set to `true` if `ToolCall.noEvents` is `true`. |
| metadata | map[string]string | Free-form key-value metadata. |
| toolDef | map[string]any | Tool definition input, for tools that accept one. |
| tool | string | For tools that call another tool: name of the tool to call. |
| url | string | For tools that call another server/URL: the URL. |
| authority | string | For tools that call another server/URL: authority to use. |
| sse | bool | For tools that call another MCP server: use SSE transport. |
| headers | Headers | Headers configuration for the tool's own outbound calls. |
| forwardHeaders | []string | Names of headers for the tool to forward on its own outbound calls. |
| agent | string | For tools that call an agent: agent name. |
| agentMessage | string | For tools that call an agent: message to send. |
| agentArgs | map[string]any | For tools that call an agent: additional arguments. |
| remoteArgs | ToolCallArgs | Arguments for the remote tool's own downstream call. |

#### MCPClientPayload
Configures the client-side behavior when the MCP server sends `elicit` or `sample` (createMessage) reverse invocations back to the client. A random item is selected from each array for each invocation. The payload is read when a call's session is created.

|Field|Data Type|Required|Description|
|---|---|---|---|
| contents | []string | No | Array of content strings. A random entry is selected for each reverse invocation response. For `sample`, it is returned as the `Content` of the response text. For `elicit`, it is reported in the call's timeline events. |
| roles | []string | No | Array of role strings (e.g. `"user"`, `"assistant"`). A random entry is selected for sampling responses. Defaults to `"none"` if not set. |
| models | []string | No | Array of model name strings. A random entry is selected for sampling responses. Defaults to `"GotoModel"` if not set. |
| actions | []string | No | Array of action strings (e.g. `"approve"`, `"deny"`). A random entry is selected for elicitation responses. Defaults to `"approve"` if not set. |
| delay | Delay | No | Optional delay to apply before responding to the reverse invocation. See `Delay` schema. |

#### Root
The request body is a JSON array of `Root` objects: `[Root, ...]`

|Field|Data Type|Required|Description|
|---|---|---|---|
| uri | string | Yes | URI identifying the root. The MCP spec requires it to start with `file://` (not validated by `Goto`). |
| name | string | No | Optional human-readable name for the root, useful for display or referencing purposes. |

#### Headers
|Field|Data Type|Description|
|---|---|---|
| request | HeadersConfig | Headers to manipulate on outgoing MCP client requests. |
| response | HeadersConfig | Headers to manipulate on incoming MCP server responses. |

#### HeadersConfig
|Field|Data Type|Description|
|---|---|---|
| add | map[string]string | Headers to add as key-value pairs. |
| remove | []string | Header names to remove. |
| forward | []string | Header names to forward from the original inbound request to the outgoing MCP request. |

#### Delay
|Field|Data Type|Description|
|---|---|---|
| min | duration | Minimum delay duration (e.g. `"100ms"`, `"1s"`). |
| max | duration | Maximum delay duration. A random value between `min` and `max` is used. |
| count | int | Number of times to apply the delay. `0`, not set or any negative value means unlimited. |

When used as a string (e.g. in `ToolCall.delay`, `initialDelay`, `requestDelay`), the format is `"<duration>"`, `"<min>-<max>"` or `"<min>-<max>:<count>"`, e.g. `"500ms"`, `"100ms-500ms"` or `"1s-5s:3"`. A count that is not a positive number means unlimited.

#### Query Parameters for /mcpapi/client/list/* APIs
|Parameter|Data Type|Required|Description|
|---|---|---|---|
| url | string | Yes | MCP server URL to connect to for listing. Without it the request is not matched (`404`). If it has no `http://` or `https://` prefix, `http://` is assumed, or `https://` when `tls` is `true`. |
| sse | bool | No | If `true` (also `yes`, `y`, `1`), use SSE transport. Default `false` (uses Streamable HTTP). |
| tls | bool | No | If `true` (also `yes`, `y`, `1`), use TLS for the connection. Default `false`. |
| authority | string | No | Authority (Host header) to use when connecting to the MCP server. |

Each of the optional params can be used on its own.

### <a name="mcp-client-responses"></a>Response Formats

#### /mcpapi/client/list/all Response
```json
{
  "tools": [ { "name": "...", "description": "...", "inputSchema": {...} }, ... ],
  "prompts": [ { "name": "...", "description": "...", "arguments": [...] }, ... ],
  "resources": [ { "name": "...", "uri": "...", "description": "...", "mimeType": "..." }, ... ]
}
```

#### /mcpapi/client/list/tools Response
```json
{
  "tools": [ { "name": "...", "description": "...", "inputSchema": {...} }, ... ]
}
```

#### /mcpapi/client/list/tools/names Response
```json
{
  "tools": [ "toolName1", "toolName2", ... ]
}
```

If the MCP server can't be reached or a list request fails, the list APIs respond with status `400` and a plain-text message `Failed to list tools/prompts/resources on url <url> with error [...]`.

#### <a name="mcp-client-call-response"></a>/mcpapi/client/call/tool Response
The response has the following parts:
- **Streamed timeline events**: unless `noEvents` is `true`, the response body starts with plain-text lines that are streamed (and flushed) as the call progresses: a `ServerInfo : ...` line followed by one line per timeline event (e.g. `Initiating Tool Call [echo] ...`, `Calling Tool [echo] ...`, `Tool [echo] called successfully ...`). The first of these lines sends the response headers, so the status is `200` and `Via-Goto` is not added. Use `noEvents: true` to get a pure JSON response (e.g. to pipe it to `jq`) and the status described below.
- **JSON result**: an `MCPResult` object, with the (capitalized) fields below:

```json
{
  "ID": "[echo]@http://localhost:3000/mcp/goto-mcp-1?Goto-MCP-Session-ID=1.1",
  "Server": "http://localhost:3000/mcp/goto-mcp-1?Goto-MCP-Session-ID=1.1",
  "Tool": "echo",
  "ToolCall": { "tool": "echo", "url": "localhost:3000/mcp/goto-mcp-1", ... },
  "ServerInfo": { ... },
  "Timeline": { "TYPE": "TIMELINE", "Events": [ ... ], "Data": { ... }, "UpstreamHeaders": { ... }, ... },
  "UpstreamStatuses": { "echo": 200, "-": { ... } },
  "LastRemoteHeaders": { ... },
  "CallResults": {
    "1.1.1": {
      "RequestID": "1.1.1",
      "Content": [ { "type": "text", "text": "..." } ],
      "Data": { ... },
      "RemoteTimeline": { ... },
      "RemoteResult": { ... },
      "RemoteHeaders": { ... },
      "RequestHeaders": { ... },
      "ResponseHeaders": { ... },
      "ResponseStatus": 200
    }
  },
  "RemoteGotos": { "<remote goto label>": true }
}
```
- `ID`, `Server`, `Tool`, `ToolCall`: identify the call. `Server` is the MCP server URL including the session ID query param. `ToolCall` is the prepared tool call payload.
- `ServerInfo`, `Timeline`: this `Goto` instance's info and the timeline of the call (events, data, upstream headers/statuses). The client doesn't mark the timeline as finished or successful, so its `Finished` and `Success` stay `false`; use the HTTP status and `ResponseStatus` instead. With `noEvents`, `Events` is empty.
- `UpstreamStatuses`: HTTP status of the call keyed by tool name, and (under `-`) the upstream statuses that the remote reported via the `Goto-Upstream-Status` header.
- `CallResults`: one entry per request, keyed by request ID (`<client>.<session>.<n>`; `requestCount` entries for multiple requests). `Content` is the MCP content returned by the tool (omitted with `resultOnly`). `Data` is the tool's structured content. If the remote tool is a `Goto` MCP server tool that returns a timeline, result or upstream headers, they are reported as `RemoteTimeline`, `RemoteResult` and `RemoteHeaders` instead of `Data`. `RequestHeaders`, `ResponseHeaders` and `ResponseStatus` are those of the HTTP request that carried the call. The whole `CallResults` is omitted with `noCallDetails`.
- `LastRemoteHeaders`, `RemoteGotos`: the upstream headers reported by the remote `Goto` tool in its last result, and the `Via-Goto` labels of the remote `Goto` instances that took part in the call (hops).

HTTP status of the response (when `noEvents` is `true`):
- the HTTP status of the last MCP response received from the server, if any (so a status that the MCP server forces, e.g. `401`, is passed through). Under the SSE transport, a `202` that only acknowledges a posted message is ignored.
- otherwise `400` if the call failed, `200` if it succeeded.
- if the payload can't be parsed, or `tool` or `url` is missing, the response is `400` with a plain-text message such as `Failed to parse tool call payload with error [invalid tool call payload]`. If the server can't be reached, the response is `400` with the error message.
- `Via-Goto` response headers are added for the remote `Goto` instances (`RemoteGotos`).

#### /mcpapi/client/details Response
`/mcpapi/client/details` returns a map of client name to `MCPNamedClientPayload` (`{}` if nothing is configured). `/mcpapi/client/{name}/details` returns one `MCPNamedClientPayload`:
```json
{
  "Name": "...",
  "ElicitPayload": { "contents": [...], "actions": [...], ... },
  "SamplePayload": { "contents": [...], "roles": [...], "models": [...], ... },
  "Roots": [ { "uri": "file://...", "name": "..." }, ... ]
}
```

### <a name="mcp-client-behavior"></a>Client Behavior
- **Sessions**: every API call creates its own MCP client and session, which is closed when the call is done. The session ID is added to the MCP server URL as the `Goto-MCP-Session-ID` query param, and the requests of a tool call session carry the `Goto-MCP-Server` and `Goto-MCP-Tool` headers, with each tool call request also carrying a `Goto-MCP-Request-ID` header. Each session works on its own copy of the call's `headers` and `args`, so concurrent or repeated calls never affect each other's headers.
- **Request headers**: the `headers.request` config is applied to every request of the session: `add` and `remove` as configured, and the headers named in `forward` are copied from the REST request that was made to `Goto`. Unless `neat` is `true`, `Host` is set to `authority` and `User-Agent` to the client's caller ID. The list APIs apply `authority` as the `Host` header as well.
- **Transports**: Streamable HTTP by default, SSE with `forceSSE` (tool call) or `sse` (list APIs). HTTP/2 and TLS are selected with `h2` and `tls`; `authority` sets the `Host` header used for the connection.
- **Timeouts**: each tool call request is subject to `requestTimeout` (default `10h`). The list APIs use a 1 hour client timeout.
- **Repeated calls**: with `requestCount` and `concurrent`, requests run in rounds. `initialDelay` is applied once up front, and `requestDelay` between rounds. The call waits (up to 2 seconds) after each round for the responses to complete, so that response trailers (e.g. upstream statuses) are part of the result.
- **Sampling (`createMessage`)**: the client responds using the `sample` payload: model (default `GotoModel`), role (default `none`) and a text that is a JSON object with the random `contents` entry as `Content`, plus a `Timeline` and the `requestParams` of the request. If the request's system prompt contains `elicit`, the `elicit` payload is used instead. The system prompt is returned as the stop reason.
- **Elicitation**: the client responds with a random action from the `elicit` payload's `actions` (default `approve`), and content with the `requestParams` and a `Timeline`. With no `contents` configured, the event reports `No Elicitation Content`.
- **Roots**: the configured roots are advertised to the MCP server for its `roots/list` requests.
- **Callback delays**: if the payload has a `delay`, the client waits before responding. A delay's `count` is shared by all sessions that use the payload.
- **Which payload a session uses**: a session looks up the payload registered under its own client name, `GotoMCP[<listener label>]` (with an `[sse]` suffix for SSE), and otherwise falls back to the entry that was created most recently (updating that entry keeps it the fallback). So a payload set without a name, or under any name, applies to calls as long as it is the most recently created entry; using the same name as the client is the way to pin a payload to it.
- **Startup configuration**: the `payloads.client` section (`sample`, `elicit`, `roots`) of an MCP server in the MCP config (see `pkg/ai/mcp/sample-mcp-config.yaml`) registers these payloads under that MCP server's name at startup.
- **Notifications**: tool/prompt/resource list changes, resource updates and server log messages are accepted and ignored. Progress notifications are added to the call's timeline.

### <a name="mcp-client-samples"></a>Sample Commands
These assume that `Goto` is listening on port `8080`, and an MCP server on `localhost:3000` with URI `/mcp/goto-mcp-1` (see `pkg/ai/mcp/sample-mcp-config.yaml`).
```
# List tools, tool names, or everything
curl -s "localhost:8080/mcpapi/client/list/tools?url=localhost:3000/mcp/goto-mcp-1" | jq .
curl -s "localhost:8080/mcpapi/client/list/tools/names?url=localhost:3000/mcp/goto-mcp-1&authority=mcp.example.com" | jq .
curl -s "localhost:8080/mcpapi/client/list/all?url=localhost:3000/mcp/goto-mcp-1" | jq .

# Call a tool (noEvents: true gives a pure JSON response)
curl -s -X POST localhost:8080/mcpapi/client/call/tool -d '{
  "tool": "echo",
  "url": "localhost:3000/mcp/goto-mcp-1",
  "noEvents": true,
  "args": {"text": "hello"}
}' | jq .

# Call a tool 6 times, 2 at a time, with a 1s delay between rounds and a 5s request timeout, adding a header
curl -s -X POST localhost:8080/mcpapi/client/call/tool -d '{
  "tool": "echo",
  "url": "localhost:3000/mcp/goto-mcp-1",
  "noEvents": true,
  "resultOnly": true,
  "requestCount": 6,
  "concurrent": 2,
  "requestDelay": "1s",
  "requestTimeout": "5s",
  "headers": {"request": {"add": {"foo": "bar"}}},
  "args": {"text": "hello"}
}' | jq .

# Configure how the client answers sampling, elicitation and roots requests
curl -X POST localhost:8080/mcpapi/client/payload/sample -d '{"contents":["sampled on client"],"models":["m1"],"roles":["assistant"],"delay":{"min":"100ms","max":"500ms","count":2}}'
curl -X POST localhost:8080/mcpapi/client/payload/elicit -d '{"contents":["ok"],"actions":["approve","deny"]}'
curl -X POST localhost:8080/mcpapi/client/payload/roots -d '[{"uri":"file:///tmp/work","name":"work"}]'

# Named variants of the above
curl -X POST localhost:8080/mcpapi/client/client1/payload/sample -d '{"contents":["sampled by client1"]}'
curl -X POST localhost:8080/mcpapi/client/client1/payload/roots -d '[{"uri":"file:///tmp/work1"}]'

# Get the configured payloads (add -H "Accept: application/yaml" for YAML)
curl -s localhost:8080/mcpapi/client/details
curl -s localhost:8080/mcpapi/client/client1/details
```
