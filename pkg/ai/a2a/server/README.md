
## A2A Agent Server Features
The `pkg/a2a/server` sub-package implements a generic A2A server with admin APIs to let users configure `Goto` as one or more dynamic A2A agents exposed over same or different ports. Each agent is exposed over a unique URI as `/agent/{agent}`.

The `a2a` admin APIs are exposed with path-prefix `/a2a/...`. Specifically, API `/a2a/agents/add` accepts an [Agent spec](#agent-json-schema) payload, and configures an A2A agent over the given port. API `/a2a/agents/{agent}/payload` lets you configure static streaming payload for a configured agent. Once an agent is configured, a client can call `/agent/{agent}/` (use the trailing slash, otherwise `Goto` redirects with a `307` and a client that doesn't re-post the body fails) to interact with the agent over A2A protocol. Client can call `/agent/{agent}/.well-known/agent.json` to get the agent's card.

Agents are kept per port: an agent added via an API call is served on the port that the admin request arrived on (use the `/port={port}/...` prefix to add an agent on another listener port). Besides the per-port A2A server, every agent is also recorded in a process-wide agent registry (listed by `GET /a2a/agents` and `GET /ai/agents`, see the [AI registry APIs](../../README.md#ai-registry-apis)).

Contents:
- [A2A Admin APIs](#a2a-agent-apis) (agents, servers, [status](#a2a-status-apis))
- [Startup configuration](#a2a-startup-config)
- [Agent JSON Schema](#agent-json-schema)
- [Agent behaviors and message hints](#agent-behaviors)
- [Agent APIs (A2A protocol endpoint)](#agent-apis)
- [Sample curl commands](#a2a-sample-curls)


### A2A Admin APIs
These APIs are used to add/remove/configure A2A agents to be exposed by `Goto`.

# <a name="a2a-agent-apis"></a>

#### Agents
|METHOD|URI|Description|
|------|---|-----------|
| POST | /a2a/agents/add                                | Add one or more agents (JSON or YAML array) from the request's payload, to the A2A server of the request's port and to the agent registry. An agent with the same name on the same port is replaced. [See `Agent JSON Schema` for Payload](#agent-json-schema). Returns `200` with `Port [N] Added Agents: [names]`, or `400` if the payload can't be parsed, is empty, or an agent has no card (`Port [N] Added Agents: [...]. Failed Agents: [#1: agent has no card]`). |
| POST | /a2a/agents/{agent}/payload                    | Set the response payload of an agent (used by agents configured with `stream` behavior), from a [Payload](#payload-response) JSON body, e.g. `{"isStream":true,"streamCount":2,"textStream":["new chunk"],"delay":{"min":"10ms","max":"10ms"}}`. Replaces the payload that was given in the agent's `config.response`; streams that are already running keep the payload they started with. Returns `200` with `Payload set successfully for agent [name]`, `400` with `Bad agent [name]` if the agent isn't on the request's port, or `400` if the body isn't a valid payload. |
| GET  | /a2a/agents                                    | Lists the agents of the process-wide agent registry, as a map of agent name to port to agent, e.g. `{"echo1":{"19080":{...}}}`. Returns YAML if the `Accept: application/yaml` header is set. The registry gets an entry for each agent added on any port via `/a2a/agents/add` or `/ai/agents/add`, or loaded from a startup config. |
| GET  | /a2a/agents/all                                | Lists the A2A servers of all ports with the agents of each (same payload as `GET /a2a/servers`). |
| GET  | /a2a/agents/names                              | Lists agent names and their delegate names for the current port, e.g. `{"19080":{"fed1":{"tools":["echo"]}}}`. |
| GET  | /a2a/agents/names/all                          | Lists agent names and their delegate names across all ports. |
| GET  | /a2a/agents/{agent}                            | Gets details for a specific agent (the agent as it was added, with delegate trackers; `behavior` and `config` are `{}` if none was given). Returns `200` with the agent (JSON, or YAML with `Accept: application/yaml`), or `400` with `Bad agent [name]` if the agent isn't on the request's port. |
| GET  | /a2a/agents/{agent}/delegates                  | Gets all delegates of an agent (the `delegates` config: `tools`, `agents`, `http`, `maxCalls`, `parallel`), each with its `tracker` (`callCount`, `responseCount`, `done`). Delegates can only be set by adding the agent, there are no APIs to add or change them separately. `400` with `Bad agent [name]` or `No delegates for agent [name]`. |
| GET  | /a2a/agents/{agent}/delegates/tools            | Gets tool delegates for an agent (map of delegate name to delegate). `400` with `No Tool delegates for agent [name]` if it has none. |
| GET  | /a2a/agents/{agent}/delegates/tools/{delegate} | Gets a specific tool delegate. `400` with `No Tool delegate [delegate] for agent [name]` if there is none. |
| GET  | /a2a/agents/{agent}/delegates/agents           | Gets agent delegates for an agent (map of delegate name to delegate). `400` with `No Agent delegates for agent [name]` if it has none. |
| GET  | /a2a/agents/{agent}/delegates/agents/{delegate}| Gets a specific agent delegate. `400` with `No Agents delegate [delegate] for agent [name]` if there is none. |
| POST | /a2a/agents/clear                              | Clears the A2A servers of all ports and the agent registry. Returns `200` with `Agents cleared on all ports and registry`. |

#### Servers
|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/servers                                   | Lists the A2A servers of all ports (by port: server `ID`, `Port`, `agents` by name, `enabled`). JSON, or YAML with `Accept: application/yaml`. |
| POST | /a2a/servers/clear                             | Replaces the A2A server of the request's port with an empty one, so all agents of that port stop being served (the agent registry isn't changed). Returns `200` with `Server cleared on port: N`. |

# <a name="a2a-status-apis"></a>
#### Status
The status APIs force an HTTP status on the calls to an agent (`/agent/{agent}/...`), to test how a client reacts to a failing agent. They are under the agent that they are for: `/a2a/agents/{agent}/status/...`. The agent must exist on the request's port, otherwise the set APIs return `400` with `Bad agent [name]`. (There are no `/a2a/status/...` APIs.)

|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/agents/{agent}/status                                                  | Returns the forced statuses that are set on the request's port, for all agents of the port (not only `{agent}`), as `{"<port>": [StatusConfig, ...]}`. Doesn't check that `{agent}` exists. |
| POST | /a2a/agents/{agent}/status/set/{status}                                     | Forces an HTTP status for the calls to the agent. `{status}` is `code`, `code,code,...` or `code:times`, e.g. `503` (until cleared), `503:2` (twice, then the agent responds normally), `500,502,503:6`. `400 Invalid Status` for any other format (e.g. `500x3`). |
| POST | /a2a/agents/{agent}/status/set/{status}/header/{header}                     | Forces the status only for calls that have the header (any value). |
| POST | /a2a/agents/{agent}/status/set/{status}/header/{header}={value}             | Forces the status only for calls that have the header with the given value. |
| POST | /a2a/agents/{agent}/status/set/{status}/header/not/{header}                 | Forces the status only for calls that don't have the header. |
| POST | /a2a/agents/{agent}/status/configure                                        | Configures a forced status with a `StatusConfig` JSON body (statuses, times, URI and header match). `{agent}` isn't used by this API (the match decides what the status applies to), see [StatusConfig](#a2a-statusconfig). Returns the applied config, or `400` if the body can't be parsed. |
| POST | /a2a/agents/{agent}/status/clear                                            | Clears all the forced statuses of the request's port, for all agents (not only `{agent}`). Returns `Status cleared on port [N]`. |

Every `set` URI above takes an optional query param `methods`, the HTTP methods that the status is for: a comma separated list such as `methods=GET`, `methods=GET,POST`, or `methods=*` for all methods. Without it the status is for `POST`, which is how agents are called, so the fetches of the agent card (`GET /agent/{agent}/.well-known/agent.json`) are not affected. A status for `GET` (or for another method that isn't `POST`) also applies to the requests with that method to a path under the agent, such as the ones that fetch the card of the agent or read from its stream. `methods=*` (also `any`, `all`) is every method. `400 Invalid methods [...]` if a name has characters other than letters (names aren't otherwise checked). The statuses of different methods for the same agent are separate: each has its own `times` count, and setting one again changes it and leaves the others as they are, e.g. `/a2a/agents/{agent}/status/set/503:2?methods=GET` and `/a2a/agents/{agent}/status/set/502` give GET requests `503` twice, and POST requests `502`.

Behaviour of a forced status:
- The `set` APIs apply to the endpoint of the agent on the request's port (`/agent/{agent}/`, with the agent name in lower case; a call to it with or without the trailing slash matches), and respond with a line such as `A2A Port [19080] will respond with forced statuses [503] for next [2] requests for  URI:[Prefix: /agent/echo1/]`.
- A call that gets a forced status is not served by the agent: the response has the status, and headers `Goto-Forced-Status: <status>` and `Goto-Forced-Status-Remaining: <times left>` (`-1` when it has no `times`). A forced status of `200` lets the call through.
- When several codes are given (`500,501`), one of them is picked at random for each call (they are not cycled in order).
- `times` counts the calls that were given the status; after that the agent serves normally.
- Header-matched statuses take effect only for calls that match the header rule, other calls are served normally.
- Each `set` or `configure` call adds a status rule to the port. A rule with a URI, header or method match is tried before a rule that matches every call, a rule that has been used up is skipped, and the first matching rule in the order they were added is used. `set` for the same URI, header and methods again replaces that rule's statuses and `times`.

<a name="a2a-statusconfig"></a>
###### StatusConfig (body of `/status/configure`, and the items reported by `GET .../status`)
|Field|Data Type|Default|Description|
|---|---|---|---|
| statuses | []int | none, required | HTTP status codes to force (`0` is taken as `200`). One is picked at random for each matching call. `400` with `no status` if empty, `invalid status` if negative. |
| times | int | `0` (until cleared) | Number of matching calls to force the status for. `0` or `-1` = no limit; below `-1` is `400 invalid status times`. |
| match | StatusMatch | none (matches every call, of any method) | What the status applies to. |
| port | int | the request's port | The port that the status is for, can be given to configure another port. |
| lastStatusIndex, lastStatus, statusCount | int | | Counters reported by `GET` (output only). |

|StatusMatch field|Data Type|Description|
|---|---|---|
| uri | object | URI match, all the given parts must hold: `prefix`, `exact`, `regex` (strings, matched against the lower-cased request path, the regex case-insensitively), `not` (bool, negates the parts). An invalid `regex` is a `400`. |
| headers | []object | Header matches, all must hold: `header` (name), `value` (optional, case-insensitive equal), `present` (bool, default `true`; `false` = header must be absent). |
| methods | []string | HTTP methods that the status applies to. Without it every method matches (the `set` APIs default to `POST` instead). |

```
# minimal
curl -X POST localhost:8080/a2a/agents/echo1/status/set/503:2
# rich: 500 or 501, three times, for POSTs under /agent/echo*
curl -X POST localhost:8080/a2a/agents/echo1/status/configure -d '{"statuses":[500,501],"times":3,"match":{"uri":{"regex":"/agent/echo.*"},"methods":["POST"]}}'
```

<br/>

# <a name="a2a-startup-config"></a>
#### Startup configuration
Agents can also be loaded at startup from the `a2a` section of a config yaml file, in a folder given with `--config` (see the [command line options](../../../../cmd/README.md)). The file is loaded once the listeners are up, and the agents are removed again when the file is removed. The same section is what `goto ctl apply` sends to a running instance, as one `POST /port={port}/a2a/agents/add` per server entry. The keys are the field names of the [Agent JSON schema](#agent-json-schema). See [sample-a2a-config.yaml](../sample-a2a-config.yaml) for a long example.

```
a2a:
  servers:
    - port: 8080                  # the port that the agents of this entry are served on
      agents:
        echo1:                    # a label for the log; the agent's name is card.name
          card:
            name: echo1
            version: "1.0"
            description: an echo agent
            url: http://localhost:8080/agent/echo1/
            capabilities:
              streaming: true
            skills:
              - id: echo
                name: Echo
          behavior:
            echo: true
          config: {}              # required here, even if empty
```

|Key|Description|
|---|---|
| `a2a.servers[].port` | The port to serve the agents of the entry on. |
| `a2a.servers[].agents` | Map of a label to [Agent](#agent-json-schema). The label is only used in the log. |
| `a2a.servers[].response` | Accepted, but not used. |

Differences from `POST /a2a/agents/add`: an agent that has no `card` or no `config` is skipped, with the log line `Skipping agent [label] due to missing Card/Config` (the API accepts an agent without a `config`), and a failure to add one agent doesn't stop the others from loading (the reason is in the log). The `http` delegates in the sample file, and the `forwardHeaders` of their `httpCall`, have no effect: see [DelegateHTTPCall](#delegatehttpcall).

<br/>

# <a name="agent-json-schema"></a>

<details>
<summary>Agent JSON Schema</summary>

The `/a2a/agents/add` API accepts a JSON array of Agent objects. Each Agent has the following structure:

#### Agent (top-level)
|Field|Data Type|Required|Description|
|---|---|---|---|
| card     | AgentCard     | Yes | The A2A agent card describing the agent's identity, capabilities, and skills. |
| behavior | AgentBehavior | No  | Controls how the agent processes messages. One of the behavior flags should be set to `true`. |
| config   | AgentConfig   | No  | Agent configuration including delay, response payload, and delegate definitions. |

#### AgentCard
|Field|Data Type|Required|Description|
|---|---|---|---|
| name                | string              | Yes | Name of the agent. |
| description         | string              | Yes | Description of the agent. |
| url                 | string              | Yes | Endpoint URL where the agent is hosted (e.g. `http://localhost:8080/agent/my-agent`). |
| version             | string              | Yes | Agent version string. |
| capabilities        | AgentCapabilities   | Yes | Declared capabilities of the agent. |
| skills              | []AgentSkill        | Yes | List of skills the agent provides. |
| defaultInputModes   | []string            | No  | Default input content types (e.g. `["text/plain"]`). |
| defaultOutputModes  | []string            | No  | Default output content types (e.g. `["text/plain"]`). |
| provider            | AgentProvider       | No  | Provider information (`organization`, `url`). |
| iconUrl             | string              | No  | URL to the agent's icon. |
| documentationUrl    | string              | No  | Link to documentation. |
| securitySchemes     | map[string]object   | No  | Security schemes supported by the agent. |
| security            | []map[string][]string | No | Security requirements for the agent. |
| protocolVersion     | string              | No  | A2A protocol version. |
| preferredTransport  | string              | No  | Preferred transport mechanism. |

#### AgentCapabilities
|Field|Data Type|Required|Description|
|---|---|---|---|
| streaming              | *bool | No | Whether the agent supports streaming responses. |
| pushNotifications      | *bool | No | Whether the agent can push notifications. |
| stateTransitionHistory | *bool | No | Whether the agent can provide task state history. |

#### AgentSkill
|Field|Data Type|Required|Description|
|---|---|---|---|
| id          | string   | Yes | Unique identifier for the skill. |
| name        | string   | Yes | Human-readable name of the skill. |
| description | string   | No  | Detailed description of the skill. |
| tags        | []string | No  | Tags for categorization. |
| examples    | []string | No  | Usage examples. |
| inputModes  | []string | No  | Supported input data modes/types. |
| outputModes | []string | No  | Supported output data modes/types. |

#### AgentBehavior
Exactly one of these should be set to `true`. Determines how the agent processes incoming messages. If none is set, the agent behaves as `echo`. If more than one is set, the first of `echo`, `stream`, `federate`, `httpProxy` (in this order) is used. See [Agent behaviors and message hints](#agent-behaviors) for what each does.

|Field|Data Type|Description|
|---|---|---|
| echo      | bool | Unary (`message/send`): replies with a `data` part holding the echo of the HTTP request (`Goto-*`/`Request-*` fields and headers, as JSON text). Streaming (`message/stream`): reports an event with the received message text, then completes with `Echo Response Sent`. |
| stream    | bool | Streams response chunks from a configured response payload. Supports only streaming (`message/stream`); `message/send` fails with `500` `doesn't support Unary behavior`. |
| federate  | bool | Delegates to downstream MCP tools and/or A2A agents based on trigger keywords. |
| httpProxy | bool | Placeholder: the agent is accepted, but it doesn't do anything yet. A `message/send` to it fails with `500` `processor returned nil result`. |

#### AgentConfig
|Field|Data Type|Description|
|---|---|---|
| delay     | Delay          | Delay range to apply between processing steps. |
| response  | Payload        | Static response payload (used primarily with `stream` behavior). |
| delegates | DelegateConfig | Delegate definitions for tools, agents, and HTTP calls (used with `federate` behavior). |

#### Delay
|Field|Data Type|Description|
|---|---|---|
| min   | string | Minimum delay duration (e.g. `"10ms"`, `"1s"`). |
| max   | string | Maximum delay duration. Actual delay is randomized between min and max. |
| count | int    | Optional number of times to apply the delay. |

<a name="payload-response"></a>
#### Payload (Response)
|Field|Data Type|Description|
|---|---|---|
| isStream    | bool     | Whether this payload should be streamed. |
| streamCount | int      | Number of chunks to stream. |
| delay       | Delay    | Delay between streamed chunks. |
| text        | string   | Single text response. |
| json        | object   | Single JSON response. |
| raw         | any      | Single raw response. |
| textStream  | []string | Array of text chunks for streaming. |
| jsonStream  | []object | Array of JSON chunks for streaming. |
| rawStream   | []any    | Array of raw chunks for streaming. |

#### DelegateConfig
|Field|Data Type|Description|
|---|---|---|
| tools    | map[string]DelegateToolCall  | Named tool delegates. Each key is a delegate name. |
| agents   | map[string]DelegateAgentCall | Named agent delegates. Each key is a delegate name. |
| http     | map[string]DelegateHTTPCall  | Named HTTP delegates. Accepted, validated and listed (`/a2a/agents/names`), but not invoked: no trigger runs them yet. |
| maxCalls | int                          | Maximum number of tool and agent delegates to invoke per request (default `1`, applied when the agent has tool or agent delegates). |
| parallel | bool                         | Whether to invoke matched delegates in parallel. |

The key of each delegate is its name: it is how a message selects the delegate directly (`name:[...]` input hint, or a target hint, see [Agent behaviors and message hints](#agent-behaviors)). A `triggers` entry is used as a case-insensitive regular expression that is searched for in the message text (a trigger matches wherever it appears in the text, e.g. `echo` also matches `echoing`), so use plain words: a trigger that isn't a valid regular expression (e.g. `c++`, `(`) makes adding the agent fail.

#### DelegateToolCall
|Field|Data Type|Description|
|---|---|---|
| disabled    | bool                         | If `true`, the delegate is never invoked (default `false`). |
| triggers    | []string                     | Keywords that trigger this delegate when found in the input message. |
| toolCall    | ToolCall                     | MCP tool call specification. |
| substitutes | map[string]DelegateServer    | Named alternate servers to use when a target hint matches the substitute key. |

#### DelegateAgentCall
|Field|Data Type|Description|
|---|---|---|
| disabled    | bool                         | If `true`, the delegate is never invoked (default `false`). |
| triggers    | []string                     | Keywords that trigger this delegate when found in the input message. |
| agentCall   | AgentCall                    | A2A agent call specification. |
| substitutes | map[string]DelegateServer    | Named alternate servers to use when a target hint matches the substitute key. |

#### DelegateHTTPCall
Not invoked yet (see `DelegateConfig`).

|Field|Data Type|Description|
|---|---|---|
| disabled    | bool                         | If `true`, the delegate is skipped (default `false`). |
| triggers    | []string                     | Keywords that trigger this delegate when found in the input message. |
| httpCall    | HTTPCall                     | HTTP call specification. |
| substitutes | map[string]DelegateServer    | Named alternate servers to use when a target hint matches the substitute key. |

#### DelegateServer
|Field|Data Type|Description|
|---|---|---|
| url       | string | URL of the alternate server. |
| authority | string | Optional `Host` header override for the alternate server. |

#### ToolCall (MCP)
|Field|Data Type|Description|
|---|---|---|
The same schema as the `ToolCall` of the [MCP client APIs](../../mcp/client/README.md#mcp-client-schemas), which has the complete field list and defaults. The fields that are used when a delegate calls a tool:

|Field|Data Type|Description|
|---|---|---|
| tool         | string       | Name of the MCP tool to call. Required. |
| url          | string       | URL of the MCP server. Required. `http://` is assumed if it has no scheme (`https://` if `tls` is `true`). |
| sseURL       | string       | URL of the SSE endpoint of the MCP server. Only the MCP server's `remote` tool behavior uses it (instead of `url`, when its call is over SSE); a delegate call uses `url`, and `forceSSE` for SSE. |
| server       | string       | Optional server name/identifier (sent in the `Goto-MCP-Server` header). Defaults to `authority`. |
| authority    | string       | Optional `Host` header override. |
| h2           | bool         | Enable HTTP/2 protocol for the outbound connection. |
| tls          | bool         | Enable TLS for the outbound connection. |
| forceSSE     | bool         | Use the SSE transport instead of Streamable HTTP. |
| neat         | bool         | Don't set the default `Host` and `User-Agent` request headers. |
| delay        | string       | Delay text passed to the tool as `args.delay`, for the tool to apply on the server side (e.g. `"500ms"`, `"100ms-1s:3"`). |
| args         | ToolCallArgs | Arguments to pass to the MCP tool (see below). |
| headers      | Headers      | Request/response header configuration. |
| requestCount | int          | Total number of requests to send (default `1`). |
| concurrent   | int          | Number of concurrent requests per round. Total rounds = `requestCount / concurrent`. |
| initialDelay | string       | Client-side delay before sending the first request. |
| requestDelay | string       | Client-side delay between rounds. |
| requestTimeout | string     | Per request timeout, e.g. `"30s"` (default `10h`). |
| resultOnly, noEvents, noCallDetails | bool | Trim the result that is reported back (see the MCP client README). |
| forcedStatus | int          | Accepted, not applied by the MCP client. |

There is no `dataOnly` field.

#### ToolCallArgs
The arguments are sent to the MCP tool, so what they mean depends on the tool (see the [MCP server tool behaviors](../../mcp/server/README.md)). The fields are the same as those of the [MCP client `ToolCallArgs`](../../mcp/client/README.md#toolcallargs):

|Field|Data Type|Description|
|---|---|---|
| text     | string            | Text input for the tool (e.g. what `echo` returns). |
| count    | int               | Count input for the tool (e.g. number of stream responses). |
| size     | int               | Size input for the tool (e.g. length of generated text). |
| delay    | string            | Delay text for the tool to apply (e.g. `"500ms"`). Defaults to the `delay` of the tool call. |
| status   | int               | Status for the `status` tool to report. |
| resultOnly, noEvents | bool  | Ask the tool for a trimmed response. |
| metadata | map[string]string | Arbitrary metadata key-value pairs. |
| tool, url, authority, sse, headers, forwardHeaders | | For tools that call another MCP server: the remote tool name, URL, authority, SSE transport flag, header configuration and names of headers to forward. |
| agent, agentMessage, agentArgs | | For tools that call an A2A agent: the agent name, message and data. |
| remoteArgs | ToolCallArgs    | Arguments for the remote tool's own downstream call. |
| toolDef  | map[string]any    | Tool definition input, for tools that accept one (e.g. `addTool`). |

There is no `remote` field: use `remoteArgs`, and the remote fields above.

#### AgentCall (A2A)
|Field|Data Type|Description|
|---|---|---|
The same schema as the `AgentCall` of the [A2A client APIs](../client/README.md#agentcall-schema), which has the complete field list and defaults. The fields that are used when a delegate calls an agent:

|Field|Data Type|Description|
|---|---|---|
| name                 | string            | Name of the remote agent to call. |
| agentURL             | string            | URL of the remote agent endpoint. Defaults to the URL of the agent card. |
| cardURL              | string            | URL to fetch the remote agent's card. Required. The client appends `/.well-known/agent.json` if not already present. |
| authority            | string            | Optional `Host` header override (also the TLS server name). |
| h2                   | bool              | Enable HTTP/2 protocol for the outbound connection. |
| tls                  | bool              | Enable TLS for the outbound connection. |
| message              | string            | Text message to send to the remote agent. Replaced by an `input` in an embedded JSON of the incoming message, or by the value of a `name:[value]` hint. |
| args                 | map[string]any    | Data sent to the remote agent as a data part (named `data` in older versions, which is no longer recognized). |
| headers              | Headers           | Request/response header configuration. |
| requestCount         | int               | Total number of requests to send (default `1`). |
| concurrent           | int               | Number of concurrent requests per round. Total rounds = `requestCount / concurrent`. |
| initialDelay, requestDelay | string      | Delays before the first request / between rounds (applied only when the agent's card has `streaming: true`). |
| requestTimeout       | string            | Timeout, e.g. `"30s"` (default `10h`). |
| resultOnly, noEvents, noCallDetails | bool | Trim the result that is reported back. |
| delay, retryDelay, retriableStatusCodes, requestId, forcedStatus, stream | | Accepted, not applied by the A2A client. |

There is no `dataOnly` field.

#### HTTPCall
|Field|Data Type|Description|
|---|---|---|
| url       | string  | URL to call. |
| authority | string  | `Host` header override. |
| delay     | Delay   | Optional delay before making the call. |
| headers   | Headers | Request/response header configuration. |

#### Headers
|Field|Data Type|Description|
|---|---|---|
| request  | HeadersConfig | Headers to manipulate on outgoing requests. |
| response | HeadersConfig | Headers to manipulate on incoming responses. |

#### HeadersConfig
|Field|Data Type|Description|
|---|---|---|
| add     | map[string]string | Headers to add (key-value pairs). |
| remove  | []string          | Header names to remove. |
| forward | []string          | Header names to forward from the original request. |

</details>

# <a name="agent-behaviors"></a>
### Agent behaviors and message hints
The text of the message that a client sends to an agent can carry hints, which `Goto` reads (case-insensitively) to decide what to do. The text of all the `text` parts of the message is used, and the keys of a `data` part of the message are read as features (see below).

|Hint in the message text|Used by|Meaning|
|---|---|---|
| `status N` | federate | Status `N` to ask the delegates of the call to force (only delegates that an embedded JSON names, see below). |
| `port N` | federate | Only the delegates whose URL contains `N` are called. |
| `to {word}`, `on {word}`, `with {word}` | federate | Target hint (`{word}` is made of letters, digits and `_`, so a hyphenated name can't be targeted): if `{word}` is a key of the `substitutes` of a matched delegate, the call goes to that substitute server (its `url` and `authority`); otherwise if `{word}` is the name of another tool (or agent) delegate of the agent, that delegate's call is used. The hint words are removed from the text before the triggers are matched. |
| `{name}:[{value}]` | federate | Selects the tool or agent delegate named `{name}` directly, whatever the triggers. For an agent delegate, `{value}` replaces the `message` that is sent to the remote agent. The same form turns on a feature for any agent, e.g. `timeline:[true]`, `events:[false]`. |
| `{"tool":"{name}","url":"{url}"}` (flat JSON, no nested `{}`) | federate | Selects the tool delegate (`"tool"`) or agent delegate (`"agent"`) `{name}`, and overrides its `url`; `"input"` replaces the `message` of an agent delegate. Nested objects (such as `"args"`) are not supported: the JSON has to be flat. |
| `show {feature}`, `use {feature}`, `hide {feature}`, `without {feature}`, `no {feature}`, `not {feature}` | all | Turns the feature on or off. Features: `content`/`message` (the text content of the result), `timeline` (include the timeline in the result), `events` (the progress events), `result` or `resultOnly`, `data` (send the data as artifacts, not as part of the status updates). A `data` part of the message can set the same features, e.g. `{"timeline": true, "noEvents": true, "resultOnly": true}`. |
| `5 sec`, `2 min`, `1 hour` | stream | Delay between the streamed messages (overrides the delay of the agent). |
| a number | stream | Number of messages to stream (overrides the `streamCount` of the payload). |
| `{current}`, `{total}` | stream | Replaced by the number of the message and the total count in each streamed message. |
| `stop` | stream | Stops the streams that are running on the agent (every message that contains the word `stop` is a stop request, and doesn't start a stream); `stop error` makes the stopped streams end with an error. |

How the delegates of a `federate` agent are chosen: (1) delegates named by a `{name}:[...]` hint or an embedded JSON; (2) delegates that have a trigger that is equal to the whole message (after the hints are removed); (3) if none was chosen so far, delegates whose trigger regular expression is found in the message text. At most `maxCalls` tool and agent delegates are called. They are called in parallel if `parallel` is `true`. The results are reported back as artifacts (`message/stream`).

Responses by behavior:
- `echo`: `message/send` replies with the echo of the request (one `data` part); `message/stream` sends a status update with the message that was received, and completes.
- `stream`: `message/stream` sends `task_status_update` events (progress, with the delay applied between messages), and completes when all messages are sent; `message/send` isn't supported (`500`).
- `federate`: `message/stream` sends `task_artifact_update` events (`ServerInfo`, then for each delegate its `Result`, `UpstreamHeaders` and more) and `task_status_update` events (progress messages such as `Matched Tools: [echo-del@http://...]`, ending with `completed`). `message/send` reports `No Tools` / `No Agents` entries, and doesn't report the results of the delegates (known limitation).
- `httpProxy`: does nothing yet.

### Agent APIs
The Agent endpoint `/agent/{agent}/` lets clients interact with a configured A2A agent over the A2A protocol (JSON-RPC over HTTP). Since `Goto` supports serving multiple agents on the same port, A2A clients should be given the correct URI to call for each agent (the `url` of the agent's card).

# <a name="agent-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST | /agent/{agent}/ | The A2A JSON-RPC endpoint. Use the trailing slash (`POST /agent/{agent}` is answered with a `307` redirect to it). Content type must be `application/json`. Supports `message/send` (unary), `message/stream` (SSE, `Content-Type: text/event-stream`), `tasks/get`, `tasks/cancel`, `tasks/resubscribe` and `tasks/pushNotificationConfig/set` (these are handled by the A2A library: an unknown task is `500` with error `-32001 Task not found`). Message parts must have `"kind":"text"` or `"kind":"data"`; a part without a `kind` is rejected with `400` `unsupported part kind`. |
| GET  | /agent/{agent}/.well-known/agent.json | Returns the agent card (`/.well-known/agent-card.json` works too). |
| GET,DELETE | /agent/{agent}/ | Not part of the protocol: `GET` returns a `400` JSON-RPC error, `DELETE` returns `400` `HTTP method DELETE not allowed, use POST`. |

Response codes: `200` JSON-RPC result (or SSE stream); `400` JSON-RPC error `-32600` (invalid request, bad content type or method), `-32602` (invalid params, e.g. a part without `kind`), `-32700` (parse error); `404` if the agent isn't on the port (plain text `Failed to serve agent [name] on port [N]: ...`) or the JSON-RPC method is unknown; `500` for processing errors (`-32603`, `-32001` task not found); or the status that was forced for the agent (see [Status](#a2a-status-apis)).

#### Message request
A minimal `message/send` (echo agent) and a richer `message/stream` (data part with features, and hints in the text):
```
curl -s -X POST localhost:8080/agent/my-echo-agent/ -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":"1","method":"message/send","params":{"message":{"kind":"message","role":"user","messageId":"m1","parts":[{"kind":"text","text":"hello"}]}}}'
curl -s -N -X POST localhost:8080/agent/my-federate-agent/ -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":"2","method":"message/stream","params":{"message":{"kind":"message","role":"user","messageId":"m2","parts":[{"kind":"text","text":"please echo this {\"tool\":\"echo-del\",\"url\":\"http://localhost:9090/mcp/s2\"}"},{"kind":"data","data":{"timeline":true}}]}}}'
```



# <a name="a2a-sample-curls"></a>
#### Sample curl commands
```
################################################################################
# Sample curl commands for /a2a and /agent APIs
# Base URL placeholder: http://localhost:8080  (adjust as needed)
################################################################################

#===============================================================================
# A2A SERVER - AGENTS
#===============================================================================

# --- GET /a2a/agents ---
# Returns the agent registry: agent name -> port -> agent.
curl -s http://localhost:8080/a2a/agents | jq .

# --- GET /a2a/agents (YAML response) ---
curl -s -H "Accept: application/yaml" http://localhost:8080/a2a/agents

# --- GET /a2a/agents/all ---
# Returns the A2A servers of all ports, with their agents (same as /a2a/servers).
curl -s http://localhost:8080/a2a/agents/all | jq .

# --- GET /a2a/agents/names ---
# Returns agent names and their delegate names for the current port.
curl -s http://localhost:8080/a2a/agents/names | jq .

# --- GET /a2a/agents/names/all ---
# Returns agent names and their delegate names across all ports.
curl -s http://localhost:8080/a2a/agents/names/all | jq .

# --- GET /a2a/agents/{agent} ---
# Returns details for a specific agent by name.
curl -s http://localhost:8080/a2a/agents/my-echo-agent | jq .

# --- GET /a2a/agents/{agent}/delegates ---
# Returns all delegates (tools + agents + http) for a specific agent.
curl -s http://localhost:8080/a2a/agents/my-federate-agent/delegates | jq .

# --- GET /a2a/agents/{agent}/delegates/tools ---
# Returns tool delegates for a specific agent.
curl -s http://localhost:8080/a2a/agents/my-federate-agent/delegates/tools | jq .

# --- GET /a2a/agents/{agent}/delegates/tools/{delegate} ---
# Returns a specific tool delegate by name.
curl -s http://localhost:8080/a2a/agents/my-federate-agent/delegates/tools/my-tool | jq .

# --- GET /a2a/agents/{agent}/delegates/agents ---
# Returns agent delegates for a specific agent.
curl -s http://localhost:8080/a2a/agents/my-federate-agent/delegates/agents | jq .

# --- GET /a2a/agents/{agent}/delegates/agents/{delegate} ---
# Returns a specific agent delegate by name.
curl -s http://localhost:8080/a2a/agents/my-federate-agent/delegates/agents/downstream-agent | jq .

# --- POST /a2a/agents/add ---
# Adds one or more agents. Payload is a JSON array of Agent objects.
# Example 1: Simple echo agent
curl -s -X POST http://localhost:8080/a2a/agents/add \
  -H "Content-Type: application/json" \
  -d '[
    {
      "card": {
        "name": "my-echo-agent",
        "description": "An echo agent that mirrors input back",
        "url": "http://localhost:8080/agent/my-echo-agent",
        "version": "1.0.0",
        "capabilities": {
          "streaming": false
        },
        "skills": [
          {
            "id": "echo",
            "name": "Echo",
            "description": "Echoes back the input message"
          }
        ]
      },
      "behavior": {
        "echo": true
      }
    }
  ]'

# Example 2: Streaming agent with response payload
curl -s -X POST http://localhost:8080/a2a/agents/add \
  -H "Content-Type: application/json" \
  -d '[
    {
      "card": {
        "name": "my-stream-agent",
        "description": "A streaming agent that sends chunked responses",
        "url": "http://localhost:8080/agent/my-stream-agent",
        "version": "1.0.0",
        "capabilities": {
          "streaming": true
        },
        "skills": [
          {
            "id": "stream",
            "name": "Stream",
            "description": "Streams chunked text responses"
          }
        ]
      },
      "behavior": {
        "stream": true
      },
      "config": {
        "delay": {
          "min": "10ms",
          "max": "100ms"
        },
        "response": {
          "isStream": true,
          "streamCount": 5,
          "textStream": [
            "First chunk of data",
            "Second chunk of data",
            "Third chunk of data",
            "Fourth chunk of data",
            "Fifth chunk of data"
          ]
        }
      }
    }
  ]'

# Example 3: Federate agent with tool and agent delegates
curl -s -X POST http://localhost:8080/a2a/agents/add \
  -H "Content-Type: application/json" \
  -d '[
    {
      "card": {
        "name": "my-federate-agent",
        "description": "A federate agent that delegates to tools and other agents",
        "url": "http://localhost:8080/agent/my-federate-agent",
        "version": "1.0.0",
        "capabilities": {
          "streaming": true
        },
        "skills": [
          {
            "id": "federate",
            "name": "Federate",
            "description": "Delegates to downstream tools and agents"
          }
        ]
      },
      "behavior": {
        "federate": true
      },
      "config": {
        "delay": {
          "min": "50ms",
          "max": "200ms"
        },
        "delegates": {
          "maxCalls": 3,
          "parallel": true,
          "tools": {
            "search-tool": {
              "triggers": ["search", "find", "lookup"],
              "toolCall": {
                "tool": "search-tool",
                "url": "http://localhost:9090/mcp",
                "args": {
                  "query": "default-query"
                },
                "headers": {
                  "request": {
                    "add": {"X-Tool-Auth": "token-123"},
                    "forward": ["Authorization"]
                  }
                }
              },
              "substitutes": {
                "backup": {
                  "url": "http://localhost:9091/mcp",
                  "authority": "backup.local"
                }
              }
            }
          },
          "agents": {
            "downstream-agent": {
              "triggers": ["summarize", "analyze"],
              "agentCall": {
                "name": "downstream-agent",
                "agentURL": "http://localhost:9090/agent/downstream-agent",
                "cardURL": "http://localhost:9090/.well-known/agent.json",
                "message": "Please process this request",
                "args": {
                  "context": "federated-call"
                },
                "headers": {
                  "request": {
                    "add": {"X-Caller": "my-federate-agent"},
                    "forward": ["Authorization", "X-Request-Id"]
                  }
                }
              },
              "substitutes": {
                "alt": {
                  "url": "http://localhost:9092/agent/alt-agent",
                  "authority": "alt.local"
                }
              }
            }
          },
          "http": {
            "webhook": {
              "triggers": ["notify", "webhook"],
              "httpCall": {
                "url": "http://localhost:9090/webhook",
                "authority": "webhook.local",
                "headers": {
                  "request": {
                    "add": {"Content-Type": "application/json"}
                  }
                }
              }
            }
          }
        }
      }
    }
  ]'

# Example 4: HTTP proxy agent
curl -s -X POST http://localhost:8080/a2a/agents/add \
  -H "Content-Type: application/json" \
  -d '[
    {
      "card": {
        "name": "my-proxy-agent",
        "description": "An HTTP proxy agent",
        "url": "http://localhost:8080/agent/my-proxy-agent",
        "version": "1.0.0",
        "capabilities": {
          "streaming": false
        },
        "skills": [
          {
            "id": "proxy",
            "name": "HTTP Proxy",
            "description": "Proxies requests to upstream HTTP services"
          }
        ]
      },
      "behavior": {
        "httpProxy": true
      }
    }
  ]'

# --- POST /a2a/agents/{agent}/payload ---
# Sets the response payload for an agent (raw JSON body).
curl -s -X POST http://localhost:8080/a2a/agents/my-stream-agent/payload \
  -H "Content-Type: application/json" \
  -d '{
    "isStream": true,
    "streamCount": 3,
    "delay": {
      "min": "50ms",
      "max": "200ms"
    },
    "textStream": [
      "Updated chunk 1",
      "Updated chunk 2",
      "Updated chunk 3"
    ]
  }'

# --- POST /a2a/agents/{agent}/payload (JSON payload variant) ---
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/payload \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Static response text",
    "json": {
      "status": "ok",
      "message": "Hello from agent"
    }
  }'

# --- POST /a2a/agents/clear ---
# Clears all agents on all ports and the registry.
curl -s -X POST http://localhost:8080/a2a/agents/clear

#===============================================================================
# A2A SERVER - SERVERS
#===============================================================================

# --- GET /a2a/servers ---
# Returns all A2A servers (PortServers map).
curl -s http://localhost:8080/a2a/servers | jq .

# --- POST /a2a/servers/clear ---
# Clears the A2A server on the current listener port.
curl -s -X POST http://localhost:8080/a2a/servers/clear

#===============================================================================
# A2A SERVER - STATUS  (per agent; the status applies to POST calls unless ?methods= is given)
#===============================================================================

# --- GET /a2a/agents/{agent}/status ---
# Returns all the forced statuses of the current port (all agents).
curl -s http://localhost:8080/a2a/agents/my-echo-agent/status | jq .

# --- POST /a2a/agents/{agent}/status/set/{status} ---
# Force 503 for all calls to the agent (until cleared).
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/set/503

# Force 500 three times, then serve normally.
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/set/500:3

# Force one of 500, 502 or 503 (picked at random) for the next 10 calls.
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/set/500,502,503:10

# Force 504 for the GET calls too (fetching the agent card), and for all methods.
curl -s -X POST "http://localhost:8080/a2a/agents/my-echo-agent/status/set/504?methods=GET"
curl -s -X POST "http://localhost:8080/a2a/agents/my-echo-agent/status/set/504?methods=*"

# --- POST /a2a/agents/{agent}/status/set/{status}/header/{header}[={value}] ---
# Force the status only for calls that have the header (or the header with the value).
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/set/401/header/X-Bad-Token
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/set/403/header/X-Role=guest

# --- POST /a2a/agents/{agent}/status/set/{status}/header/not/{header} ---
# Force the status for calls that don't have the header.
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/set/401/header/not/Authorization

# --- POST /a2a/agents/{agent}/status/configure ---
# Configure with a StatusConfig: 500 or 501, 3 times, for POSTs to URIs that match the regex.
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/configure \
  -H "Content-Type: application/json" \
  -d '{"statuses":[500,501],"times":3,"match":{"uri":{"regex":"/agent/my-.*"},"methods":["POST"]}}'

# Force 418 once, for a call that has header X-Chaos: yes (any method).
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/configure \
  -d '{"statuses":[418],"times":1,"match":{"headers":[{"header":"X-Chaos","value":"yes"}]}}'

# --- POST /a2a/agents/{agent}/status/clear ---
# Clears all forced statuses of the current port.
curl -s -X POST http://localhost:8080/a2a/agents/my-echo-agent/status/clear


#===============================================================================
# AGENT SERVING ENDPOINT   (note the trailing slash, and "kind" on every part)
#===============================================================================

# --- GET /agent/{agent}/.well-known/agent.json ---
# Fetches the card of the agent.
curl -s http://localhost:8080/agent/my-echo-agent/.well-known/agent.json | jq .

# --- POST /agent/{agent}/ ---
# message/send: unary A2A call (echo agent replies with the echo of the request).
curl -s -X POST http://localhost:8080/agent/my-echo-agent/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-001",
    "method": "message/send",
    "params": {
      "message": {
        "kind": "message",
        "role": "user",
        "messageId": "msg-001",
        "parts": [
          {
            "kind": "text",
            "text": "Hello echo agent, please respond"
          }
        ]
      }
    }
  }'

# message/send with a text part and a data part.
curl -s -X POST http://localhost:8080/agent/my-echo-agent/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-002",
    "method": "message/send",
    "params": {
      "message": {
        "kind": "message",
        "role": "user",
        "messageId": "msg-002",
        "parts": [
          {
            "kind": "text",
            "text": "Process this data"
          },
          {
            "kind": "data",
            "data": {
              "key": "value",
              "items": [1, 2, 3]
            }
          }
        ]
      }
    }
  }'

# message/stream: streaming A2A call (SSE). Here "3 sec" is a delay hint and "2" the number of messages.
curl -s -N -X POST http://localhost:8080/agent/my-stream-agent/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-003",
    "method": "message/stream",
    "params": {
      "message": {
        "kind": "message",
        "role": "user",
        "messageId": "msg-003",
        "parts": [
          {
            "kind": "text",
            "text": "Stream me 2 results with 3 sec delay"
          }
        ]
      }
    }
  }'

# Send to a federate agent: "search" is a trigger word, and the flat JSON selects the delegate and overrides its URL.
curl -s -N -X POST http://localhost:8080/agent/my-federate-agent/ \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer my-token" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-004",
    "method": "message/stream",
    "params": {
      "message": {
        "kind": "message",
        "role": "user",
        "messageId": "msg-004",
        "parts": [
          {
            "kind": "text",
            "text": "Please search for golang tutorials {\"tool\":\"search-tool\",\"url\":\"http://localhost:9091/mcp\"}"
          }
        ]
      }
    }
  }'

# Federate agent with a target hint: "on backup" uses the "backup" substitute of the matched delegate.
curl -s -N -X POST http://localhost:8080/agent/my-federate-agent/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-005",
    "method": "message/stream",
    "params": {
      "message": {
        "kind": "message",
        "role": "user",
        "messageId": "msg-005",
        "parts": [
          {
            "kind": "text",
            "text": "search for the latest report on backup"
          }
        ]
      }
    }
  }'

# Any method other than POST is not part of the protocol (GET and DELETE return 400).
curl -s -X DELETE http://localhost:8080/agent/my-echo-agent/

```
