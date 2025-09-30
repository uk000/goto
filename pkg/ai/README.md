
# AI
The AI package provides APIs and implementation to enable `Goto` to act as an A2A Agent, an A2A client, an MCP server, and an MCP client, all at once.

## A2A Agent Features (Server)
The `a2a` sub-package implements a generic A2A server with admin APIs to let users configure `Goto` as one or more A2A agents exposed over same or different ports.

The `a2a` admin APIs are exposed with path-prefix `/a2a/...`. Specifically, API `/a2a/agents/add` accepts an [Agent spec](#agent-json-schema) payload, and configures an A2A agent over the given port. API `/a2a/agents/{agent}/payload` lets you configure static streaming payload for a configured agent. Once an agent is configured, a client can call `/agent/{agent}` to interact with the agent over A2A protocol. Client can call `/agent/{agent}/.well-known/agent.json` to get the agent's card.


### A2A Admin APIs
These APIs are used to add/remove/configure A2A agents to be exposed by `Goto`.

# <a name="a2a-agent-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST             | /a2a/agents/add                                | Add one or more agents from the request's payload. [See `Agent JSON Schema` for Payload](#agent-json-schema) |
| POST             | /a2a/agent/{agent}/payload                     | Set response payload for an agent (to be used by agents configured with "stream" behavior) |
| GET              | /a2a/agents                                    | Lists all agents. |
| GET              | /a2a/servers                                   | Lists all A2A servers with agents configured on each server. |
| GET              | /a2a/agents/{agent}                            | Gets details for a specific agent. |
| GET              | /a2a/agents/{agent}/delegates                  | Gets delegates of an agent (for agents configured with `federate` behavior). |
| GET              | /a2a/agents/{agent}/delegates/tools            | Gets tool delegates for an agent (for agents configured with `federate` behavior). |
| GET              | /a2a/agents/{agent}/delegates/tools/{delegate} | Gets a specific tool delegate (for agents configured with `federate` behavior). |
| GET              | /a2a/agents/{agent}/delegates/agents           | Gets agent delegates for an agent (for agents configured with `federate` behavior). |
| GET              | /a2a/agents/{agent}/delegates/agents/{delegate}| Gets a specific agent delegate (for agents configured with `federate` behavior). |
| POST             | /a2a/servers/clear                             | Clears all servers. |
| POST             | /a2a/agents/clear                              | Clears all agents. |
| POST             | /a2a/status/set/{status}                       | Configured an HTTP status code that the A2A server will return (instead of serving agent calls). |

<br/>
<details>
<summary>Agent JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| listenerID    | string | Read-only field identifying the listener's port and current generation. |
| label    | string | Label to be applied to the listener. This can also be set/changed via REST API later. |
| hostLabel    | string | The host label is auto-generated and assigned to the listeners to uniquely identify the host while still differentiating between multiple listeners active on the `goto` instance. This is auto-generated using format `<hostname>@<ipaddress>:<port>`. Host Label is also sent back in the `Goto-Host` response header.  |
| port     | int    | Port on which the new listener will listen on. |
| protocol | string | `http`, `http1`, `https`, `https1`, `grpc`, `grpcs`, `tcp`, or `tls`. Protocol `tls` implies TCP+TLS and `grpcs` implies GRPC+TLS as opposed to `tcp` and `grpc` being plain-text versions. |
| open | bool | Controls whether the listener should be opened as soon as it's added. Also reflects the listener's current status when queried. |
| autoCert | bool | Controls whether a TLS certificate should be auto-generated for an HTTPS or TLS listener. If enabled, the TLS cert for the listener is generated using the `CommonName` field if configured, or else the cert common name is defaulted to `goto.goto`. |
| commonName | string | If given, this common name is used to generate self-signed cert for this listener. |
| mutualTLS | bool | Controls whether the HTTPS or TLS listener should enforce mutual-TLS, requiring clients to present a valid certificate that's validated against the configured CA certs of the listener. CA certs can be added to a listener using API `/server/listeners/{port}/ca/add`). |
| tls | bool | Reports whether the listener has been configured for TLS (read-only). |
| tcp | TCPConfig | Supplemental TCP config for a TCP listener. See TCP Config JSON schema under `TCP Server` section. |

</details>

### Agent APIs
The Agent endpoint `/agent/{agent}` lets clients interact with a configured A2A agent. Since `Goto` supports serving multiple agents on the same port, A2A clients should be given the correct URI to call for each agent.

# <a name="agent-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET,POST,DELETE | /agent/{agent}                        | Serves agent requests. |


### A2A Client APIs
The A2A client APIs allow using a REST API to load a remote agent's card, and invoke a remote agent. `Goto` converts the REST request/payload to A2A request when making the agent call, and serves the A2A response from the agent back to the caller over REST.

# <a name="a2a-client-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET | /a2a/client/agent/card?url={url}&authority={authority}      | Load and get a remote agent's card. |
| POST | /a2a/client/agent/{agent}/call                        | Call a remote agent for which card has been load previously via the above `/card` API. Accepts `AgentCall` schema as payload. |
| POST | /a2a/client/call                        | Call a remote agent by loading card during the call. Accepts `AgentCall` schema as payload. |


### MCP Admin APIs
These APIs are used to add/remove/configure MCP servers and tools to be exposed by `Goto`.

# <a name="mcp-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET | /mcpapi/servers      | Get details of the configured MCP servers on the current port. |
| GET | /mcpapi/servers/all      | Get details of the configured MCP servers across all ports. |
| GET | /mcpapi/servers/names      | Get a summary listing of MCP servers and tools/prompts/resources. |
| GET | /mcpapi/servers/{server}      | Get details of a specific MCP server. |
| POST | /mcpapi/servers/add    | Add one or more MCP servers from the request's payload. [See `MCP Server JSON Schema` for Payload](#mcp-server-json-schema) |
| POST | /mcpapi/servers/{server}/route?uri={uri}    | Configures the given MCP server to be served over the given URI. All tools of the MCP server are also configured to be served on their corresponding subpaths under the given URI. |
| POST | /mcpapi/servers/start    | Enables all MCP servers on the current port (servers are enabled by default too). |
| POST | /mcpapi/servers/{server}/start    | Marks an MCP server as enabled (servers are enabled by default too). |
| POST | /mcpapi/servers/stop    | Disables all MCP servers on the current port. |
| POST | /mcpapi/servers/{server}/stop    | Marks an MCP server as disabled to prevent it from serving MCP requests |
| POST | /mcpapi/servers/{server}/tools/add    | Add one or more MCP tools from the request's payload to the given server. [See `MCP Tool JSON Schema` for Payload](#mcp-tool-json-schema) |
| POST | /mcpapi/servers/{server}/tools/add    | Add one or more MCP tools from the request's payload to the given server. [See `MCP Tool JSON Schema` for Payload](#mcp-tool-json-schema) |
| GET | /mcpapi/server/{server}/tools    | Get tools for the given MCP server. |
| GET | /mcpapi/servers/tools    | Get all tools from all MCP servers. |
| POST | /mcpapi/servers/{server}/prompts/add    | Add one or more MCP prompts from the request's payload to the given server. [See `MCP Prompt JSON Schema` for Payload](#mcp-prompt-json-schema) |
| GET | /mcpapi/server/{server}/prompts    | Get prompts for the given MCP server. |
| GET | /mcpapi/servers/prompts    | Get all prompts from all MCP servers. |
| POST | /mcpapi/servers/{server}/resources/add    | Add one or more MCP resources from the request's payload to the given server. [See `MCP Resource JSON Schema` for Payload](#mcp-resource-json-schema) |
| GET | /mcpapi/server/{server}/resources    | Get resources for the given MCP server. |
| GET | /mcpapi/servers/resources    | Get all resources from all MCP servers. |
| POST | /mcpapi/servers/{server}/templates/add    | Add one or more MCP resource templates from the request's payload to the given server. [See `MCP Resource Template JSON Schema` for Payload](#mcp-resource-template-json-schema) |
| GET | /mcpapi/server/{server}/templates    | Get resource templates for the given MCP server. |
| GET | /mcpapi/servers/templates    | Get all resource templates from all MCP servers. |
| POST | /mcpapi/server/{server}/payload/completion(/delay={delay})    | Configure "completion" payload for the given MCP server. This payload is used for "completion" calls from MCP clients. The optional delay subpath allows an artificial delay to be added to the completion response, to mimic server think time. [See `MCP Server Completion JSON Schema` for Payload](#mcp-server-completion-json-schema) |
| POST | /mcpapi/proxy?endpoint={url}&sni={sni}&headers={headers}    | Configures the current port to proxy MCP traffic to an upstream endpoint. |
| POST | /mcpapi/proxy?to={tool}&endpoint={url}&sni={sni}&headers={headers}    | Configures the current port to proxy a specific MCP tool's call to an upstream endpoint. |
