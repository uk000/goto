
## A2A Agent Server Features
The `pkg/a2a/server` sub-package implements a generic A2A server with admin APIs to let users configure `Goto` as one or more dynamic A2A agents exposed over same or different ports. Each agent is exposed over a unique URI as `/agent/{agent}`.

The `a2a` admin APIs are exposed with path-prefix `/a2a/...`. Specifically, API `/a2a/agents/add` accepts an [Agent spec](#agent-json-schema) payload, and configures an A2A agent over the given port. API `/a2a/agents/{agent}/payload` lets you configure static streaming payload for a configured agent. Once an agent is configured, a client can call `/agent/{agent}` to interact with the agent over A2A protocol. Client can call `/agent/{agent}/.well-known/agent.json` to get the agent's card.


### A2A Admin APIs
These APIs are used to add/remove/configure A2A agents to be exposed by `Goto`.

# <a name="a2a-agent-apis"></a>

#### Agents
|METHOD|URI|Description|
|------|---|-----------|
| POST | /a2a/agents/add                                | Add one or more agents from the request's payload. [See `Agent JSON Schema` for Payload](#agent-json-schema) |
| POST | /a2a/agents/{agent}/payload                    | Set response payload for an agent (to be used by agents configured with `stream` behavior). |
| GET  | /a2a/agents                                    | Lists all agents from the global agent registry (across all ports). |
| GET  | /a2a/agents/all                                | Lists all agents across all ports. |
| GET  | /a2a/agents/names                              | Lists agent names and their delegate names for the current port. |
| GET  | /a2a/agents/names/all                          | Lists agent names and their delegate names across all ports. |
| GET  | /a2a/agents/{agent}                            | Gets details for a specific agent. |
| GET  | /a2a/agents/{agent}/delegates                  | Gets all delegates of an agent (for agents configured with `federate` behavior). |
| GET  | /a2a/agents/{agent}/delegates/tools            | Gets tool delegates for an agent (for agents configured with `federate` behavior). |
| GET  | /a2a/agents/{agent}/delegates/tools/{delegate} | Gets a specific tool delegate (for agents configured with `federate` behavior). |
| GET  | /a2a/agents/{agent}/delegates/agents           | Gets agent delegates for an agent (for agents configured with `federate` behavior). |
| GET  | /a2a/agents/{agent}/delegates/agents/{delegate}| Gets a specific agent delegate (for agents configured with `federate` behavior). |
| POST | /a2a/agents/clear                              | Clears all agents on all ports and the registry. |

#### Servers
|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/servers                                   | Lists all A2A servers with agents configured on each server. |
| POST | /a2a/servers/clear                             | Clears the A2A server on the current listener port. |

#### Status
|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/status                                                            | Returns all configured forced statuses. |
| POST | /a2a/status/set/{status}                                               | Forces an HTTP status code for A2A agent requests (e.g. `503`, `500x3` for 3 times). |
| POST | /a2a/status/set/{status}?uri={uri}                                     | Forces status only for requests matching a specific URI pattern. |
| POST | /a2a/status/set/{status}/header/{header}                               | Forces status when a specific header is present. |
| POST | /a2a/status/set/{status}/header/{header}={value}                       | Forces status when a header has a specific value. |
| POST | /a2a/status/set/{status}/header/{header}?uri={uri}                     | Forces status when a header is present and URI matches. |
| POST | /a2a/status/set/{status}/header/{header}={value}?uri={uri}             | Forces status when a header has a specific value and URI matches. |
| POST | /a2a/status/set/{status}/header/not/{header}                           | Forces status when a specific header is NOT present. |
| POST | /a2a/status/set/{status}/header/not/{header}?uri={uri}                 | Forces status when a header is NOT present and URI matches. |
| POST | /a2a/status/configure                                                  | Configures status via a JSON `StatusConfig` payload with match rules (URI regex, header matches). |
| POST | /a2a/status/clear                                                      | Clears all forced statuses on the current port. |

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
Exactly one of these should be set to `true`. Determines how the agent processes incoming messages.

|Field|Data Type|Description|
|---|---|---|
| echo      | bool | Echoes back the input message along with server echo information. |
| stream    | bool | Streams response chunks from a configured response payload. |
| federate  | bool | Delegates to downstream MCP tools and/or A2A agents based on trigger keywords. |
| httpProxy | bool | Proxies requests to upstream HTTP services. |

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
| http     | map[string]DelegateHTTPCall  | Named HTTP delegates. Each key is a delegate name. |
| maxCalls | int                          | Maximum number of delegates to invoke per request (default `1`). |
| parallel | bool                         | Whether to invoke matched delegates in parallel. |

#### DelegateToolCall
|Field|Data Type|Description|
|---|---|---|
| triggers    | []string                     | Keywords that trigger this delegate when found in the input message. |
| toolCall    | ToolCall                     | MCP tool call specification. |
| substitutes | map[string]DelegateServer    | Named alternate servers to use when a target hint matches the substitute key. |

#### DelegateAgentCall
|Field|Data Type|Description|
|---|---|---|
| triggers    | []string                     | Keywords that trigger this delegate when found in the input message. |
| agentCall   | AgentCall                    | A2A agent call specification. |
| substitutes | map[string]DelegateServer    | Named alternate servers to use when a target hint matches the substitute key. |

#### DelegateHTTPCall
|Field|Data Type|Description|
|---|---|---|
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
| tool         | string       | Name of the MCP tool to call. |
| url          | string       | URL of the MCP server. |
| sseURL       | string       | Optional SSE URL for the MCP server. |
| server       | string       | Optional server name/identifier. |
| authority    | string       | Optional `Host` header override. |
| h2           | bool         | Enable HTTP/2 protocol for the outbound connection. |
| tls          | bool         | Enable TLS for the outbound connection. |
| forceSSE     | bool         | Force SSE transport. |
| neat         | bool         | Return raw/unprocessed results. |
| dataOnly     | bool         | If `true`, suppresses text content and only returns data parts. |
| delay        | string       | Delay before making the call (e.g. `"500ms"`). |
| args         | ToolCallArgs | Arguments to pass to the MCP tool (see below). |
| headers      | Headers      | Request/response header configuration. |
| requestCount | int          | Total number of requests to send (default `1`). |
| concurrent   | int          | Number of concurrent requests per round. Total rounds = `requestCount / concurrent`. |
| initialDelay | string       | Delay before sending the first request. |

#### ToolCallArgs
|Field|Data Type|Description|
|---|---|---|
| delay    | string            | Delay before tool execution (e.g. `"500ms"`). |
| count    | int               | Repeat count for the tool call. |
| text     | string            | Text input for the tool. |
| remote   | RemoteCallArgs    | Remote call configuration for the tool (see below). |
| metadata | map[string]string | Arbitrary metadata key-value pairs. |

#### RemoteCallArgs
|Field|Data Type|Description|
|---|---|---|
| tool           | string   | Name of the remote tool to call. |
| agent          | string   | Name of the remote agent to call. |
| url            | string   | URL of the remote server. |
| authority      | string   | Optional `Host` header override. |
| sse            | bool     | Use SSE transport. |
| headers        | Headers  | Request/response header configuration. |
| forwardHeaders | []string | Header names to forward from the incoming request. |
| agentMessage   | string   | Message to send to the remote agent. |

#### AgentCall (A2A)
|Field|Data Type|Description|
|---|---|---|
| name                 | string            | Name of the remote agent to call. |
| agentURL             | string            | URL of the remote agent endpoint. |
| cardURL              | string            | URL to fetch the remote agent's card. The client appends `/.well-known/agent.json` if not already present. |
| authority            | string            | Optional `Host` header override. |
| h2                   | bool              | Enable HTTP/2 protocol for the outbound connection. |
| tls                  | bool              | Enable TLS for the outbound connection. |
| dataOnly             | bool              | If `true`, suppresses text content in responses and only returns data parts. |
| delay                | string            | Delay before making the call (e.g. `"500ms"`). |
| message              | string            | Text message to send to the remote agent. |
| data                 | map[string]any    | Data payload to send to the remote agent. |
| headers              | Headers           | Request/response header configuration. |
| requestCount         | int               | Total number of requests to send (default `1`). |
| concurrent           | int               | Number of concurrent requests per round. Total rounds = `requestCount / concurrent`. |
| initialDelay         | string            | Delay before sending the first request. |
| retryDelay           | string            | Delay between retries on retriable status codes. |
| retriableStatusCodes | []int             | HTTP status codes that should trigger a retry. |
| requestId            | RequestId         | Request ID generation configuration. |

#### RequestId
|Field|Data Type|Description|
|---|---|---|
| send   | bool   | Whether to send a request ID. |
| uuid   | bool   | Whether to generate a UUID as the request ID. |
| header | string | Header name to use for the request ID. |
| query  | string | Query parameter name to use for the request ID. |

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

### Agent APIs
The Agent endpoint `/agent/{agent}` lets clients interact with a configured A2A agent. Since `Goto` supports serving multiple agents on the same port, A2A clients should be given the correct URI to call for each agent.

# <a name="agent-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET,POST,DELETE | /agent/{agent}                        | Serves agent requests. |



```
################################################################################
# Sample curl commands for /a2a and /agent APIs
# Base URL placeholder: http://localhost:8080  (adjust as needed)
################################################################################

#===============================================================================
# A2A SERVER - AGENTS
#===============================================================================

# --- GET /a2a/agents ---
# Returns all agents registered on the current listener port.
curl -s http://localhost:8080/a2a/agents | jq .

# --- GET /a2a/agents (YAML response) ---
curl -s -H "Accept: application/yaml" http://localhost:8080/a2a/agents

# --- GET /a2a/agents/all ---
# Returns all agents across all ports (PortServers map).
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
                "backup-server": {
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
                "data": {
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
                "alt-agent": {
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
# A2A SERVER - STATUS
#===============================================================================

# --- GET /a2a/status ---
# Returns all configured statuses.
curl -s http://localhost:8080/a2a/status | jq .

# --- POST /a2a/status/set/{status} ---
# Forces a specific HTTP status code for A2A requests.
# Return 503 for all A2A requests (indefinitely).
curl -s -X POST http://localhost:8080/a2a/status/set/503

# Return 500 three times, then revert to normal.
curl -s -X POST http://localhost:8080/a2a/status/set/500x3

# Return 429 five times.
curl -s -X POST http://localhost:8080/a2a/status/set/429x5

# --- POST /a2a/status/set/{status}?uri={uri} ---
# Forces status only for requests matching a specific URI pattern.
curl -s -X POST "http://localhost:8080/a2a/status/set/502?uri=/agent/my-echo-agent"

# --- POST /a2a/status/set/{status}/header/{header} ---
# Forces status when a specific header is present.
curl -s -X POST http://localhost:8080/a2a/status/set/401/header/X-Bad-Token

# --- POST /a2a/status/set/{status}/header/{header}={value} ---
# Forces status when a header has a specific value.
curl -s -X POST http://localhost:8080/a2a/status/set/403/header/X-Role=guest

# --- POST /a2a/status/set/{status}/header/{header}={value}?uri={uri} ---
# Forces status when header matches AND URI matches.
curl -s -X POST "http://localhost:8080/a2a/status/set/503/header/X-Env=staging?uri=/agent/my-echo-agent"

# --- POST /a2a/status/set/{status}/header/{header}?uri={uri} ---
# Forces status when header is present AND URI matches.
curl -s -X POST "http://localhost:8080/a2a/status/set/500/header/X-Debug?uri=/agent/my-echo-agent"

# --- POST /a2a/status/set/{status}/header/not/{header} ---
# Forces status when a specific header is NOT present.
curl -s -X POST http://localhost:8080/a2a/status/set/401/header/not/Authorization

# --- POST /a2a/status/set/{status}/header/not/{header}?uri={uri} ---
# Forces status when header is NOT present AND URI matches.
curl -s -X POST "http://localhost:8080/a2a/status/set/401/header/not/Authorization?uri=/agent/my-echo-agent"

# --- POST /a2a/status/configure ---
# Configures status via a JSON StatusConfig payload.
curl -s -X POST http://localhost:8080/a2a/status/configure \
  -H "Content-Type: application/json" \
  -d '{
    "statuses": [500, 502, 503],
    "times": 10,
    "match": {
      "uriMatch": {
        "uri": "/agent/my-echo-agent",
        "regex": "/agent/my-.*"
      },
      "headerMatches": [
        {
          "header": "X-Test",
          "value": "true",
          "present": true
        }
      ]
    }
  }'

# --- POST /a2a/status/clear ---
# Clears all forced statuses on the current port.
curl -s -X POST http://localhost:8080/a2a/status/clear


#===============================================================================
# AGENT SERVING ENDPOINT
#===============================================================================

# --- GET /agent/{agent} ---
# Serves an agent via GET (e.g. for agent card discovery or health checks).
curl -s http://localhost:8080/agent/my-echo-agent | jq .

# --- POST /agent/{agent} ---
# Sends a message to an agent (A2A JSON-RPC protocol).
# This is the primary endpoint agents listen on for A2A protocol messages.
# Example: Send a message/task to the agent (A2A JSON-RPC format).
curl -s -X POST http://localhost:8080/agent/my-echo-agent \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-001",
    "method": "message/send",
    "params": {
      "message": {
        "role": "user",
        "parts": [
          {
            "type": "text",
            "text": "Hello echo agent, please respond"
          }
        ]
      }
    }
  }'

# Example: Send a message with data part.
curl -s -X POST http://localhost:8080/agent/my-echo-agent \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-002",
    "method": "message/send",
    "params": {
      "message": {
        "role": "user",
        "parts": [
          {
            "type": "text",
            "text": "Process this data"
          },
          {
            "type": "data",
            "data": {
              "key": "value",
              "items": [1, 2, 3]
            }
          }
        ]
      }
    }
  }'

# Example: Stream a message (SSE streaming via A2A protocol).
curl -s -N -X POST http://localhost:8080/agent/my-stream-agent \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-003",
    "method": "message/stream",
    "params": {
      "message": {
        "role": "user",
        "parts": [
          {
            "type": "text",
            "text": "Stream me some results"
          }
        ]
      }
    }
  }'

# Example: Send to federate agent with trigger words and override JSON.
curl -s -N -X POST http://localhost:8080/agent/my-federate-agent \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer my-token" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-004",
    "method": "message/stream",
    "params": {
      "message": {
        "role": "user",
        "parts": [
          {
            "type": "text",
            "text": "Please search for golang tutorials {\"tool\":\"search-tool\",\"args\":{\"query\":\"golang tutorials\"}}"
          }
        ]
      }
    }
  }'

# Example: Federate agent with target hint syntax.
curl -s -N -X POST http://localhost:8080/agent/my-federate-agent \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "req-005",
    "method": "message/stream",
    "params": {
      "message": {
        "role": "user",
        "parts": [
          {
            "type": "text",
            "text": "summarize the latest report"
          }
        ]
      }
    }
  }'

# --- DELETE /agent/{agent} ---
# Sends a DELETE request to the agent endpoint.
curl -s -X DELETE http://localhost:8080/agent/my-echo-agent

```