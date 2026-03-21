

## A2A Client Features
The `a2a/client` sub-package implements a generic A2A client with admin APIs to let users configure `Goto` as an A2A client to invoke a target agent over A2A protocol. The client APIs are meant to test a remote agent's presence and availability from test scripts using REST APIs without having to implement A2A protocol client in the test script.

### A2A Client APIs
The A2A client APIs allow using a REST API to load a remote agent's card, and invoke a remote agent. `Goto` converts the REST request/payload to A2A request when making the agent call, and serves the A2A response from the agent back to the caller over REST.

# <a name="a2a-client-apis"></a>
|METHOD|URI|Description|
|------|---|-----------|
| GET  | /a2a/client/agent/card?url={url}&authority={authority} | Load and get a remote agent's card. The `authority` parameter is optional and sets the `Host` header on the outbound request. |
| POST | /a2a/client/agent/{agent}/call                        | Call a remote agent for which card has been loaded previously via the above `/card` API. Accepts `AgentCall` schema as payload. |
| POST | /a2a/client/call                                      | Call a remote agent by loading card during the call. Accepts `AgentCall` schema as payload (must include `cardURL` if card is not already loaded). |
| POST | /a2a/client/push                                      | Webhook endpoint to receive push notifications from A2A agents. |


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
# Calls a previously loaded agent by name. Payload is AgentCall JSON.
curl -s -X POST http://localhost:8080/a2a/client/agent/my-echo-agent/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-echo-agent",
    "agentURL": "http://localhost:9090/agent/my-echo-agent",
    "message": "Hello, agent! Please echo this back.",
    "data": {
      "key1": "value1",
      "key2": 42
    }
  }'

# --- POST /a2a/client/call ---
# Calls an agent directly. Must include cardURL if agent card is not loaded.
# Example 1: With cardURL (agent card not previously loaded)
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

# Example 2: Minimal call (agent card already loaded via /a2a/client/agent/card)
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-echo-agent",
    "message": "Simple message to echo"
  }'

# Example 3: Call with delay
curl -s -X POST http://localhost:8080/a2a/client/call \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-stream-agent",
    "cardURL": "http://localhost:9090/.well-known/agent.json",
    "agentURL": "http://localhost:9090/agent/my-stream-agent",
    "delay": "500ms",
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