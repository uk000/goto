
## MCP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

The MCP proxy lets a port forward the `tools/call` requests for selected MCP tools to an upstream MCP endpoint. The APIs are under `/proxy/mcp/...` (not under `/mcpapi`), and the proxy is configured per port: a target that is set up with these APIs applies to the MCP requests that arrive on the port of the admin request. MCP servers that have a tool with `"proxy": true` set up the same kind of target for that tool when the tool is added (see the [MCP server README](../../ai/mcp/server/README.md)).

|METHOD|URI|Description|
|---|---|---|
| POST |	/proxy/mcp/proxy?endpoint={endpoint}&headers={headers} | Setup an MCP proxy target on the request's port for all tools (`*`, named `*`), forwarding to the given upstream endpoint. `headers` is optional. |
| POST |	/proxy/mcp/proxy/{tool}?endpoint={endpoint}&to={tool}&sni={sni}&headers={headers}   | Setup an MCP proxy target for the given MCP tool (the target is named after it), forwarding to the given upstream endpoint, optionally to a differently named tool (`to`). `headers` is optional. |

Query params:
- `endpoint` (required): URL of the upstream MCP endpoint, e.g. `http://upstream:9090/mcp/s2`. Without it the route doesn't match (`404`).
- `to` (only on `/proxy/mcp/proxy/{tool}`): name of the tool on the upstream. Optional (if omitted the target has no tool rename).
- `headers` (optional): headers to add to the upstream request, as a comma separated list of `Name:Value` (e.g. `X-A:1,X-B:2`). The first `:` splits name and value, and a value can't contain a comma. Entries without a `:` or with an empty name are ignored. Read from the URL query on both URIs.
- `server` (optional, read from the URL query): name of the target, defaults to `{tool}`, or `*` if there is no tool.
- `sni` is part of the URI pattern of `/proxy/mcp/proxy/{tool}` (the route with `sni` has to be given with `endpoint` and `to` as well) but isn't used.

Query combinations that are routed: `/proxy` takes `endpoint`, or `endpoint` and `headers`. `/proxy/{tool}` takes `endpoint`; `endpoint` and `to`; or `endpoint`, `to`, `sni` and `headers` together (`headers` is read in all forms).

Responses:
- `200` with `Setup MCP proxy at port [19080] for server [echo1] tool [echo1] to endpoint [http://localhost:19090/mcp/s2] tool [echo]` when the target was registered. Registering a target for a tool that already has one replaces it.
- `400` with `Failed to setup MCP proxy at port [N] for server [S] tool [T]: no endpoint given` if the endpoint is empty (e.g. `?endpoint=`).
- `404` if `endpoint` isn't in the query.

```
# minimal: proxy one tool to the same-named tool of another MCP server
curl -X POST "localhost:8080/proxy/mcp/proxy/echo?endpoint=http://localhost:9090/mcp/s2"
# rich: proxy the tool `search` of this port to the tool `lookup` of the upstream, adding headers to the upstream request
curl -X POST "localhost:8080/proxy/mcp/proxy/search?endpoint=http://localhost:9090/mcp/s2&to=lookup&headers=X-Tenant:acme,X-Env:test"
# proxy every tool of the port
curl -X POST "localhost:8080/proxy/mcp/proxy?endpoint=http://localhost:9090/mcp/s2"
```

Notes:
- A target is used for a `tools/call` request of an MCP request on the port (not an admin request) whose tool name has a target, or else for the `*` target. Other MCP requests (`initialize`, `tools/list`, ...) and requests when a status is forced for the request aren't proxied.
- The proxy of a port is enabled by default, and is used as soon as the port has a target. There are no APIs to list, remove or disable the targets, or to read the tracker below: they last until the process restarts.
- Known limitation: the request that matches a target is not forwarded to the upstream yet. It is not served by the MCP server either: the caller gets a plain `200` response with the usual `Goto` request echo JSON and `Goto-Proxy-*` headers instead of an MCP result. Setting a `*` target therefore breaks the tool calls of all the MCP servers of the port until restart.

## MCP Proxy Tracker JSON Schema
The tracker of a proxy counts its traffic (it embeds the HTTP proxy tracker fields too). These counters are kept per port, and aren't reported by an API yet.

|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountByUpstream | map[string]int  | Number of connections per upstream endpoint |
| requestCountByUpstream | map[string]int  | Number of requests per upstream endpoint |
| requestCountByServer | map[string]int  | Number of requests per requested MCP server |
| requestCountByServerTool | map[string]map[string]int  | Number of requests per MCP server per Tool |
| responseCountByUpstream | map[string]int  | Number of responses per upstream endpoint |
| responseCountByServer | map[string]int  | Number of responses per requested MCP server |
| responseCountByServerTool | map[string]map[string]int  | Number of responses per MCP Tool for each server |
| messageCountByType | map[string]int  | Number of messages by message type |
