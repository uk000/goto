
## A2A Client Features
The `a2a/client` sub-package implements a generic A2A client with admin APIs to let users configure `Goto` as an A2A client to invoke a target agent over A2A protocol. The client APIs are meant to test a remote agent's presence and availability from test scripts using REST APIs without having to implement A2A protocol client in the test script.

`Goto` converts the REST request/payload (JSON or YAML) into an A2A message when making the agent call, and serves the A2A response from the agent back to the caller over REST. Highlights:
- Load a remote agent's card, optionally with a custom `Host` (authority).
- Call a remote agent over unary (`SendMessage`) or streaming (`StreamMessage`) A2A protocol, chosen by the capabilities advertised in the agent card. The REST response is either a single collected JSON object (`/call`) or a chunked stream (`/call/stream`).
- Send multiple requests in rounds with configurable concurrency, and track each request under its own request ID.
- Add, remove and forward headers on the outbound requests, and shape the agent's response headers recorded in the result.
- Configurable request timeout, plus initial and per-round delays on the streaming path.
- Report the agent's HTTP status back to the caller, along with `Goto-Upstream-Status` and `Via-Goto` response headers that carry the status and the `Goto` instances seen along the call chain.
- A `/push` webhook endpoint that can be given to agents as a push notification target (it only validates the notification payload; see [A2A Client APIs](#a2a-client-apis)).

Contents:
- [A2A Client APIs](#a2a-client-apis)
- [AgentCall Schema](#agentcall-schema) ([Headers](#headers-object), [HeadersConfig](#headersconfig-object), [RequestId](#requestid-object))
- [Request Execution](#request-execution)
- [Response Format](#response-format)
- [Streaming vs JSON Response](#streaming-vs-json-response)
- [Sample curl Commands](#sample-curl-commands)

### A2A Client APIs
The A2A client APIs allow using a REST API to load a remote agent's card, and invoke a remote agent. `Goto` converts the REST request/payload to A2A request when making the agent call, and serves the A2A response from the agent back to the caller over REST. Streaming responses are supported via the `/call/stream` endpoint.

# <a name="a2a-client-apis"></a>
|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/client/agent/card?url={url}&authority={authority} | Load and get a remote agent's card. The `url` parameter is required (`http://` is assumed if no scheme is given, and `/.well-known/agent.json` is appended if missing). The `authority` parameter is optional and sets the `Host` header on the card request. Returns `400` if the card cannot be fetched. |
| POST | /a2a/client/agent/{agent}/call                        | Call a remote agent by name. The `{agent}` path parameter overrides the `name` field in the `AgentCall` payload. Requires `cardURL` in payload to fetch the agent card. |
| POST | /a2a/client/call/stream                               | Call a remote agent with streaming response (chunked transfer). Uses the same `AgentCall` payload as `/call`. Responses are flushed incrementally as they arrive from the agent. |
| POST | /a2a/client/call/result                               | Same handler and behavior as `/call` (collected JSON response). Exists as an alias, e.g. for proxy trigger configs that map a URI to it (`/callagent: /a2a/client/call/result`). |
| POST | /a2a/client/call                                      | Call a remote agent with JSON response. Accepts `AgentCall` schema as payload (must include `cardURL`). |
| POST | /a2a/client/push                                      | Webhook endpoint to receive push notifications from A2A agents. It only checks that the body is a JSON object (`400` otherwise) and replies `200`; the notification is not stored or acted upon, and the client does not register push notification configs with agents on its own. |

### AgentCall Schema

The `AgentCall` payload (JSON or YAML) is used by all call endpoints (`/call`, `/call/result`, `/call/stream`, `/agent/{agent}/call`). The same schema is used for the `agentCall` entries of A2A server agent delegates (see [sample-a2a-config.yaml](../sample-a2a-config.yaml) and the [A2A server](../server/README.md) docs).

|Field|Type|Required|Description|
|-----|----|--------|-----------|
| `name`                 | string            | No  | Agent identifier. Overridden by `{agent}` path parameter when using `/agent/{agent}/call`. Used in request IDs, logs and the `<name> (A2A)` entry of the response `headers`. |
| `cardURL`              | string            | Yes | URL to fetch the agent card from (e.g., `http://host/.well-known/agent.json`). The client prepends `http://` if no scheme is given, and appends `/.well-known/agent.json` if not already present. A call without `cardURL` fails with `502`. |
| `agentURL`             | string            | No  | URL endpoint for the agent service. If omitted, the URL from the agent card is used. |
| `authority`            | string            | No  | Sets the `Host` header on the agent card request, and is used as the TLS server name (SNI) of the outbound connection. To set the `Host` header on the agent message requests, add a `Host` header via `headers.request.add`. |
| `h2`                   | bool              | No  | Enable HTTP/2 protocol for the outbound connection. |
| `tls`                  | bool              | No  | Enable TLS for the outbound connection. |
| `message`              | string            | No  | Text input message sent to the agent as a `TextPart`. |
| `args`                 | object            | No  | Arbitrary key-value data sent to the agent as a `DataPart`. A data part is sent on every call, as it also carries `resultOnly` (see below), even when `args` is empty. Formerly named `data`, which is no longer recognized. |
| `requestCount`         | int               | No  | Total number of requests to send (default `1`). |
| `concurrent`           | int               | No  | Number of concurrent requests per round (default `1`, never more than `requestCount`). Total rounds = `ceil(requestCount / concurrent)`; the last round carries the remaining requests. |
| `initialDelay`         | string            | No  | Delay before sending the first request, e.g. `"500ms"` or a range `"100ms-500ms"`. Applied only when the agent is called over the streaming protocol (agent card has `capabilities.streaming: true`). |
| `requestDelay`         | string            | No  | Delay between rounds of requests (not applied after the last round), same format as `initialDelay`. Applied only when the agent is called over the streaming protocol. |
| `requestTimeout`       | string            | No  | Go duration (e.g. `"30s"`, `"5m"`) used as the HTTP client timeout for the agent card fetch and the agent requests. It bounds the whole exchange with the agent, including reading a streamed response. Defaults to `10h` on these endpoints. A value that cannot be parsed is treated as unset by the parser, and the client then falls back to a 1 hour timeout. |
| `headers`              | Headers object    | No  | Request/response header configuration (see below). |
| `resultOnly`           | bool              | No  | Sent to the remote agent as a `resultOnly` entry in the data part on every call. `Goto` agents use it to trim their reply to the result. |
| `noEvents`             | bool              | No  | Omits timeline events from results that are relayed through MCP. No effect on the REST responses of these endpoints. |
| `noCallDetails`        | bool              | No  | Omits the call details from results that are relayed through MCP. No effect on the REST responses of these endpoints. |
| `delay`                | string            | No  | **Not applied by the A2A client.** Use `initialDelay`/`requestDelay`. |
| `retryDelay`           | string            | No  | **Not applied by the A2A client.** The client does not retry calls. |
| `retriableStatusCodes` | array of int      | No  | **Not applied by the A2A client.** The client does not retry calls. |
| `requestId`            | RequestId object  | No  | **Not applied by the A2A client.** The client always generates its own request IDs (see [Request Execution](#request-execution)). |
| `forcedStatus`         | int               | No  | **Not applied by the A2A client.** |
| `stream`               | bool              | No  | **Not applied by these REST endpoints.** The endpoint (`/call` vs `/call/stream`) selects the REST response style. |

The `dataOnly` field no longer exists.

#### Headers Object

|Field|Type|Description|
|-----|----|-----------|
| `request`  | HeadersConfig | Headers to manipulate on outbound requests to the agent. |
| `response` | HeadersConfig | Headers to manipulate on inbound responses from the agent, before they are recorded in the result. |

#### HeadersConfig Object

|Field|Type|Description|
|-----|----|-----------|
| `add`     | map of string to string | Headers to add (key-value pairs). |
| `remove`  | array of string         | Header names to remove. |
| `forward` | array of string         | Header names to forward from the incoming request (to `Goto`) to the outbound request. Request `forward` is applied to the agent requests, and also to the agent card request. |

#### RequestId Object

The `requestId` field is parsed but **not applied** by the A2A client: it always generates its own request IDs and ignores `send`, `uuid`, `header` and `query`.

|Field|Type|Description|
|-----|----|-----------|
| `send`   | bool   | Whether to send a request ID. |
| `uuid`   | bool   | Whether to generate a UUID as the request ID. |
| `header` | string | Header name to use for the request ID. |
| `query`  | string | Query parameter name to use for the request ID. |

### Request Execution

- Requests are sent in rounds: the requests of a round run concurrently, and the rounds run one after the other.
- Each request gets an ID of the form `[<name> @ <agentURL>] [Request# <round>.<n>]` (e.g. `[my-agent @ http://localhost:9090/agent/my-agent/] [Request# 1.1]`). The ID is the key of the request's entry in the response, and is sent to the agent in the `Goto-A2A-Request-ID` header.
- Besides `Goto-A2A-Request-ID`, the client adds `Goto-A2A-Server` (the agent URL) and `Goto-A2A-Agent` (the agent card name) headers to the outbound agent requests, followed by the `headers.request` add/remove/forward configuration.
- `initialDelay` and `requestDelay` apply only to agents whose card advertises streaming (see [Streaming vs JSON Response](#streaming-vs-json-response)).
- The errors of the requests are collected per request ID. If the error text carries `HTTP status NNN`, that status becomes the status of the REST response.

### Response Format

The format below applies to `/call`, `/call/result` and `/agent/{agent}/call`. For `/call/stream` see [Streaming vs JSON Response](#streaming-vs-json-response).

- **Body**: a JSON object keyed by request ID. Each request's entry has `messages` (the text replies of the agent, in order), plus any data parts the agent returned (a data part with a single field is merged in under that field name, otherwise it is nested under a generated key). A top-level `headers` entry holds `<name> (A2A)` with the `RequestHeaders` and `ResponseHeaders` of the call, merged with any upstream headers that a remote `Goto` agent reported. Illustrative example (the content depends on the agent):
  ```json
  {
    "[my-agent @ http://localhost:9090/agent/my-agent/] [Request# 1.1]": {
      "messages": ["echo: Process this request please"]
    },
    "headers": {
      "my-agent (A2A)": {
        "RequestHeaders":  {"Goto-A2A-Request-ID": ["..."], "Goto-A2A-Server": ["..."], "Goto-A2A-Agent": ["..."]},
        "ResponseHeaders": {"Content-Type": ["application/json"]}
      }
    }
  }
  ```
- **Status**: mirrors the HTTP status of the agent's last response (e.g. `503` if the agent returns `503`), so test scripts can assert on it. It is `502` when there is no response from the agent, including a missing or unreachable agent card, and when the call fails with an error that carries no HTTP status. An unparsable payload returns `400`. The response body is still written when the agent call fails.
- **Headers**: `Goto-Upstream-Status` carries a JSON object with the call name mapped to the agent's HTTP status, and a `-` entry holding the upstream statuses that the agent itself reported through its own `Goto-Upstream-Status` header (when it is a `Goto` instance). `Via-Goto` headers list the remote `Goto` instances that took part in the call, as reported in the response headers from the agent.

### Streaming vs JSON Response

- **`POST /a2a/client/call`** (also `/a2a/client/call/result` and `/a2a/client/agent/{agent}/call`) — Collects all agent responses and returns them as a single JSON object keyed by request ID (see [Response Format](#response-format)).
- **`POST /a2a/client/call/stream`** — Flushes each response chunk immediately using HTTP chunked transfer encoding: text replies are written as lines, data parts as JSON objects, and a final JSON object with the call's `headers` is written at the end. Useful for long-running agent tasks or real-time progress updates. Because the response is flushed while the call is in progress, the HTTP status line is committed with the first chunk (normally `200`), so the status mirroring and the `Goto-Upstream-Status`/`Via-Goto` response headers described above are only reliable on the non-streaming endpoints.
- The REST response style is decided by the endpoint that is called. The A2A protocol used towards the agent is decided by the agent card, independent of the endpoint: if the card has `capabilities.streaming: true`, the client uses the A2A streaming protocol (`StreamMessage`); otherwise it uses unary (`SendMessage`). The collected endpoints work with both.

### Sample curl Commands

```
################################################################################
# Sample curl commands for the A2A client APIs
# Base URL placeholder: http://localhost:8080  (adjust port as needed)
################################################################################

#===============================================================================
# A2A CLIENT
#===============================================================================

# --- GET /a2a/client/agent/card?url={url} ---
# Fetches an agent card from a remote URL.
curl -s "http://localhost:8080/a2a/client/agent/card?url=http://localhost:9090" | jq .

# With full agent.json path:
curl -s "http://localhost:8080/a2a/client/agent/card?url=http://localhost:9090/.well-known/agent.json" | jq .

# --- GET /a2a/client/agent/card?url={url}&authority={authority} ---
# Fetches an agent card with a custom Host header (authority).
curl -s "http://localhost:8080/a2a/client/agent/card?url=http://localhost:9090&authority=my-agent.example.com" | jq .

# --- POST /a2a/client/agent/{agent}/call ---
# Calls an agent by name ({agent} overrides the "name" field in payload).
curl -s -X POST http://localhost:8080/a2a/client/agent/my-echo-agent/call \
  -H "Content-Type: application/json" \
  -d '{
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-echo-agent",
    "message": "Hello, agent! Please echo this back.",
    "args": {
      "key1": "value1",
      "key2": 42
    }
  }'

# --- POST /a2a/client/call ---
# Calls an agent directly with JSON response.
# Example 1: Full payload with all options
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "remote-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/remote-agent",
    "authority": "remote-agent.example.com",
    "message": "Process this request please",
    "args": {
      "context": "test",
      "items": ["item1", "item2"]
    },
    "headers": {
      "request": {
        "add": {
          "Authorization": "Bearer my-token",
          "X-Request-Id": "req-12345"
        },
        "forward": ["X-Trace-Id"]
      },
      "response": {
        "add": {
          "X-Processed-By": "a2a-client"
        }
      }
    }
  }'

# Example 2: Call with a request timeout (applies to the card fetch and the agent requests)
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-agent",
    "requestTimeout": "30s",
    "message": "Process within 30 seconds"
  }'

# Example 3: Load test with multiple concurrent requests
# (10 requests in 5 rounds of 2 concurrent requests each)
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-agent",
    "message": "Load test message",
    "requestCount": 10,
    "concurrent": 2
  }'

# Example 4: Call with HTTP/2 and TLS
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "secure-agent",
    "cardURL": "https://secure-host/.well-known/agent.json",
    "agentURL": "https://secure-host/agent/secure-agent",
    "h2": true,
    "tls": true,
    "message": "Secure request over HTTP/2"
  }'

# Example 5: Ask a remote Goto agent to report only the result
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "goto-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/goto-agent",
    "resultOnly": true,
    "message": "Return only the result"
  }'

# Example 6: Show the HTTP status and the Goto-Upstream-Status/Via-Goto response headers
# (the status mirrors the agent's last response status; 502 if the agent could not be reached)
curl -si -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-agent",
    "message": "Check status"
  }'

# --- POST /a2a/client/call/result ---
# Same as /a2a/client/call.
curl -s -X POST http://localhost:8080/a2a/client/call/result \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-agent",
    "message": "Collect the result"
  }'

# --- POST /a2a/client/call/stream ---
# Calls an agent with streaming response (chunked transfer). Use -N so that curl doesn't buffer the output.
curl -sN -X POST http://localhost:8080/a2a/client/call/stream \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-stream-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-stream-agent",
    "message": "Stream me some data"
  }'

# Streaming agent (card has capabilities.streaming: true) with an initial delay,
# and a delay between rounds (initialDelay/requestDelay apply to the streaming protocol only)
curl -sN -X POST http://localhost:8080/a2a/client/call/stream \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-stream-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-stream-agent",
    "message": "Stream me some data",
    "requestCount": 4,
    "concurrent": 2,
    "initialDelay": "500ms",
    "requestDelay": "1s"
  }'

# --- POST /a2a/client/push ---
# Receives push notifications from A2A agents (webhook endpoint).
# Replies 200 for a JSON body and 400 otherwise; the notification is not stored.
curl -s -X POST http://localhost:8080/a2a/client/push \
  -H "Content-Type: application/json" \
  -d '{
    "id": "task-abc-123"
  }'

```
