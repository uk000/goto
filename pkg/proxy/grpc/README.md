
### gRPC Proxy Startup Config
```
proxy:
  - grpc:
      port: 8888
      enabled: true
      services:
        Goto:
          toService: Goto
          methods:
            echo: echo
          upstream:
            id: grpc-8000
            endpoint: localhost:8000
            authority: localhost
          config:
            delay:
              min: 0s
              max: 0s
```

#### GRPC Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| GET |	/grpc/proxy/status | Get GRPC Proxy Details. |
| POST |	/grpc/proxy/clear   | Clear GRPC Proxies. |
| POST | /grpc/proxy/{service}/{upstream}/tee/{teeport} | Setup GRPC Proxy for the given service to the given upstream endpoint (expecting same service to be available at the upstream endpoint).  |
| POST | /grpc/proxy/{service}/{upstream}/delay/{delay} | Same as above, with an additional delay to be added to all requests/responses |
| POST | /grpc/proxy/{service}/{upstream}/tee/{teeport} | Same as abuve, but also captures a copy of the requests/responses to be replayed for any service that connects to the `teeport` port  |
| POST | /grpc/proxy/{service}/{upstream}/{targetService} | Setup GRPC Proxy for the given service to the given upstream endpoint, to a different service as identified by `targetService`. The `targetService` should accept the same input/output payload spec. |
| POST | /grpc/proxy/{service}/{upstream}/{targetService}/delay/{delay} | Same as above, with an additional delay to be added to all requests/responses |
| POST | /grpc/proxy/{service}/{upstream}/{targetService}/tee/{teeport} | Same as above, but also captures a copy of the requests/responses to be replayed for any service that connects to the `teeport` port  |

### Get Reports
- **GET** `/proxy/report/grpc`

#### GRPC Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountByUpstream | map[string]int  | Number of connections per upstream endpoint |
| requestCountByUpstream | map[string]int  | Number of requests per upstream endpoint |
| requestCountByService | map[string]int  | Number of requests per requested Servcice |
| requestCountByServiceMethod | map[string]map[string]int  | Number of requests per RPC methdMCP server per Tool |
| responseCountCountByUpstream | map[string]int  | Number of responses per upstream endpoint |
| responseCountCountByServer | map[string]int  | Number of responses per requested MCP server |
| responseCountsCountByServerTool | map[string]map[string]int  | Number of responses per MCP Tool for each server |
| messageCountByType | map[string]int  | Number of messages by message type |

