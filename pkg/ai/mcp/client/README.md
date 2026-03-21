
## MCP Client Features
The `ai/mcp/client` sub-package implements a generic MCP client with admin APIs to let users invoke a target MCP server via MCP protocol. The client APIs are meant to test a remote MCP server's presence and availability from test scripts using REST APIs without having to implement MCP protocol client in the test script.

### MCP Client APIs
The MCP client APIs allow using a REST API to list an MCP server's tools, call an MCP tool with REST<->MCP conversion, and set the client side behavior for `elicit` and `sample` reverse invocations from MCP server to the client.



# <a name="mcp-client-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET | /mcpapi/client/details      | Get client-side payloads of all MCP clients. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET | /mcpapi/client/{name}/details      | Get client-side payloads configured for a named MCP client. If the named client is not found, the default client payload is returned. Returns JSON by default, or YAML if `Accept: application/yaml` header is set. |
| GET, POST | /mcpapi/client/list/all      | List all tools, prompts, and resources from the MCP server pointed to by the `url` query param. Additionally accepts `sse:true/false` and `authority` query params. |
| GET, POST | /mcpapi/client/list/tools      | List only tools from the MCP server pointed to by the `url` query param. Additionally accepts `sse:true/false` and `authority` query params. |
| GET, POST | /mcpapi/client/list/tools/names      | List only the names of tools from the MCP server pointed to by the `url` query param. Additionally accepts `sse:true/false` and `authority` query params. |
| POST | /mcpapi/client/call      | Call an MCP tool on a remote MCP server. Accepts a `ToolCall` JSON payload in the request body. Connects to the specified MCP server, invokes the tool, and returns the result with hop tracing. |
| POST | /mcpapi/client/payload/{kind}      | Set the default client-side payload for `elicit` or `sample` reverse invocations. `{kind}` must be `sample` or `elicit`. Accepts an `MCPClientPayload` JSON body. |
| POST | /mcpapi/client/{name}/payload/{kind}      | Set a named client-side payload for `elicit` or `sample` reverse invocations. `{kind}` must be `sample` or `elicit`. Accepts an `MCPClientPayload` JSON body. |
| POST | /mcpapi/client/payload/roots      | Set the default client-side MCP roots. Accepts a JSON array of `Root` objects in the request body. |
| POST | /mcpapi/client/{name}/payload/roots      | Set MCP roots for a named client. Accepts a JSON array of `Root` objects in the request body. |

---

### Schemas

#### ToolCall
|Field|Data Type|Required|Description|
|---|---|---|---|
| tool | string | Yes | MCP tool name to invoke. |
| url | string | Yes | MCP server URL to connect to. |
| sseURL | string | No | Alternate SSE URL for the MCP server. |
| server | string | No | Server identifier. Defaults to `authority` if not set. |
| authority | string | No | Authority (Host header value) to use when connecting to the MCP server. |
| forceSSE | bool | No | If `true`, forces use of SSE transport instead of Streamable HTTP transport. Default `false`. |
| neat | bool | No | If `true`, returns raw/unprocessed MCP content and merges structured content directly into the output. Default `false`. |
| delay | string | No | Delay duration range to apply before the tool call, e.g. `"100ms-500ms"` or `"1s-5s:3"`. Parsed as a min-max range with optional count. |
| args | map[string]any | No | Arguments to pass to the MCP tool. |
| headers | Headers | No | Request/response header manipulation configuration. See `Headers` schema. |

#### MCPClientPayload
Configures the client-side behavior when the MCP server sends `elicit` or `sample` (createMessage) reverse invocations back to the client. A random item is selected from each array for each invocation.

|Field|Data Type|Required|Description|
|---|---|---|---|
| contents | []string | Yes | Array of content strings. A random entry is selected for each reverse invocation response. |
| roles | []string | No | Array of role strings (e.g. `"user"`, `"assistant"`). A random entry is selected for sampling responses. |
| models | []string | No | Array of model name strings. A random entry is selected for sampling responses. Defaults to `"GotoModel"` if not set. |
| actions | []string | No | Array of action strings (e.g. `"approve"`, `"deny"`). A random entry is selected for elicitation responses. Defaults to `"approve"` if not set. |
| delay | Delay | No | Optional delay to apply before responding to the reverse invocation. See `Delay` schema. |

#### Root
The request body is a JSON array of `Root` objects: `[Root, ...]`

|Field|Data Type|Required|Description|
|---|---|---|---|
| uri | string | Yes | URI identifying the root. Must start with `file://`. |
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
| count | int | Number of times to apply the delay. |

When used as a string (e.g. in `ToolCall.delay`), the format is `"<min>-<max>"` or `"<min>-<max>:<count>"`, e.g. `"100ms-500ms"` or `"1s-5s:3"`.

#### Query Parameters for /mcpapi/client/list/* APIs
|Parameter|Data Type|Required|Description|
|---|---|---|---|
| url | string | Yes | MCP server URL to connect to for listing. |
| sse | bool | No | If `true`, use SSE transport. Default `false` (uses Streamable HTTP). |
| authority | string | No | Authority (Host header) to use when connecting to the MCP server. |

### Response Formats

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

#### /mcpapi/client/call Response
Returns a JSON object containing the tool call result along with client info and hop tracing:
```json
{
  "content": "...",
  "structuredContent": { ... },
  "hops": [ ... ],
  ...
}
```
- `content`: Parsed tool response content. If `neat` is `true`, raw MCP content objects are returned. Otherwise, text content is parsed from JSON where possible.
- `structuredContent`: Present when the MCP server returns structured content. If `neat` is `true`, structured fields are merged directly into the top-level output.
- `hops`: Trace of call hops through the system for debugging and observability.

#### /mcpapi/client/details Response
Returns the `MCPNamedClientPayload` for a specific client (or all clients if no name given):
```json
{
  "ElicitPayload": { "contents": [...], "actions": [...], ... },
  "SamplePayload": { "contents": [...], "roles": [...], "models": [...], ... },
  "Roots": [ { "uri": "file://...", "name": "..." }, ... ]
}
```
