
## MCP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/mcpapi/proxy?endpoint={endpoint}&sni={sni}&headers={headers} | Setup MCP proxy on the request's port, forwarding to the given upstream endpoint. Optionally specify an SNI match for TLS traffic, and optional headers to be added to the upstream request. |
| POST |	/mcpapi/proxy/{tool}?to={tool}&endpoint={endpoint}&sni={sni}&headers={headers}   | Setup MCP proxy on the MCP server for the given MCP tool, forwarding to the given tool at the given upstream. Optionally specify an SNI match for TLS traffic, and optional headers to be added to the upstream request. |



## MCP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountByUpstream | map[string]int  | Number of connections per upstream endpoint |
| requestCountByUpstream | map[string]int  | Number of requests per upstream endpoint |
| requestCountByServer | map[string]int  | Number of requests per requested MCP server |
| requestCountByServerTool | map[string]map[string]int  | Number of requests per MCP server per Tool |
| responseCountCountByUpstream | map[string]int  | Number of responses per upstream endpoint |
| responseCountCountByServer | map[string]int  | Number of responses per requested MCP server |
| responseCountsCountByServerTool | map[string]map[string]int  | Number of responses per MCP Tool for each server |
| messageCountByType | map[string]int  | Number of messages by message type |

