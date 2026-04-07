


## A2A Client Features
The `a2a/client` sub-package implements a generic A2A client with admin APIs to let users configure `Goto` as an A2A client to invoke a target agent over A2A protocol. The client APIs are meant to test a remote agent's presence and availability from test scripts using REST APIs without having to implement A2A protocol client in the test script.

### A2A Client APIs
The A2A client APIs allow using a REST API to load a remote agent's card, and invoke a remote agent. `Goto` converts the REST request/payload to A2A request when making the agent call, and serves the A2A response from the agent back to the caller over REST. Streaming responses are supported via the `/call/stream` endpoint.

# <a name="a2a-client-apis"></a>
|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/client/agent/card?url={url}&authority={authority} | Load and get a remote agent's card. The `authority` parameter is optional and sets the `Host` header on the outbound request. |
| POST | /a2a/client/agent/{agent}/call                        | Call a remote agent by name. The `{agent}` path parameter overrides the `name` field in the `AgentCall` payload. Requires `cardURL` in payload to fetch the agent card. |
| POST | /a2a/client/call/stream                               | Call a remote agent with streaming response (chunked transfer). Uses the same `AgentCall` payload as `/call`. Responses are flushed incrementally as they arrive from the agent. |
| POST | /a2a/client/call                                      | Call a remote agent with JSON response. Accepts `AgentCall` schema as payload (must include `cardURL`). |
| POST | /a2a/client/push                                      | Webhook endpoint to receive push notifications from A2A agents. |

### AgentCall Schema

The `AgentCall` JSON payload is used by all call endpoints (`/call`, `/call/stream`, `/agent/{agent}/call`):

|Field|Type|Required|Description|
|-----|----|--------|-----------|
| `name`                 | string            | No  | Agent identifier. Overridden by `{agent}` path parameter when using `/agent/{agent}/call`. |
| `cardURL`              | string            | Yes | URL to fetch the agent card from (e.g., `http://host/.well-known/agent.json`). The client appends `/.well-known/agent.json` if not already present. |
| `agentURL`             | string            | No  | URL endpoint for the agent service. If omitted, the URL from the agent card is used. |
| `authority`            | string            | No  | Overrides the `Host` header on outbound requests to the agent. |
| `h2`                   | bool              | No  | Enable HTTP/2 protocol for the outbound connection. |
| `tls`                  | bool              | No  | Enable TLS for the outbound connection. |
| `message`              | string            | No  | Text input message sent to the agent as a `TextPart`. |
| `data`                 | object            | No  | Arbitrary key-value data sent to the agent as a `DataPart`. |
| `dataOnly`             | bool              | No  | If `true`, suppresses text content in responses and only returns data parts. |
| `delay`                | string            | No  | Delay before initiating the call (e.g., `"500ms"`, `"2s"`). |
| `requestCount`         | int               | No  | Total number of requests to send (default `1`). |
| `concurrent`           | int               | No  | Number of concurrent requests per round (default `1`). Total rounds = `requestCount / concurrent`. |
| `initialDelay`         | string            | No  | Delay before sending the first request. |
| `retryDelay`           | string            | No  | Delay between retries on retriable status codes. |
| `retriableStatusCodes` | array of int      | No  | HTTP status codes that should trigger a retry. |
| `headers`              | Headers object    | No  | Request/response header configuration (see below). |
| `requestId`            | RequestId object  | No  | Request ID generation configuration (see below). |

#### Headers Object

|Field|Type|Description|
|-----|----|-----------|
| `request`  | HeadersConfig | Headers to manipulate on outbound requests to the agent. |
| `response` | HeadersConfig | Headers to manipulate on inbound responses from the agent. |

#### HeadersConfig Object

|Field|Type|Description|
|-----|----|-----------|
| `add`     | map of string to string | Headers to add (key-value pairs). |
| `remove`  | array of string         | Header names to remove. |
| `forward` | array of string         | Header names to forward from the incoming request to the outbound request. |

#### RequestId Object

|Field|Type|Description|
|-----|----|-----------|
| `send`   | bool   | Whether to send a request ID. |
| `uuid`   | bool   | Whether to generate a UUID as the request ID. |
| `header` | string | Header name to use for the request ID. |
| `query`  | string | Query parameter name to use for the request ID. |

### Streaming vs JSON Response

- **`POST /a2a/client/call`** — Collects all agent responses and returns them as a single JSON object (`map[requestID][]response`).
- **`POST /a2a/client/call/stream`** — Flushes each response chunk immediately using HTTP chunked transfer encoding. Useful for long-running agent tasks or real-time progress updates.
- The streaming behavior is determined by whether the request URI contains `"stream"`. If the agent card indicates streaming capability, the client uses the A2A streaming protocol (`StreamMessage`) regardless of endpoint; otherwise it uses unary (`SendMessage`).

```
################################################################################
# Sample curl commands for /a2a and /agent APIs
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
    "data": {
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
    "data": {
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

# Example 2: Call with delay
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-agent",
    "delay": "500ms",
    "message": "Process after delay"
  }'

# Example 3: Load test with multiple concurrent requests
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

# Example 5: Data-only response (suppress text content)
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "data-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/data-agent",
    "dataOnly": true,
    "message": "Return structured data only"
  }'

# --- POST /a2a/client/call/stream ---
# Calls an agent with streaming response (chunked transfer).
curl -s -X POST http://localhost:8080/a2a/client/call/stream \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-stream-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-stream-agent",
    "message": "Stream me some data"
  }'

# --- POST /a2a/client/push ---
# Receives push notifications from A2A agents (webhook endpoint).
curl -s -X POST http://localhost:8080/a2a/client/push \
  -H "Content-Type: application/json" \
  -d '{
    "id": "task-abc-123"
  }'

```
