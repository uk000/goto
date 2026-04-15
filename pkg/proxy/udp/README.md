
# UDP Proxy

### UDP Proxy Operations
- **POST** `/proxy/udp/{port}/{endpoint}`
- **POST** `/proxy/udp/{port}/{endpoint}/delay/{delay}`
- **POST** `/proxy/udp/{port}/delay/{delay}`


#### UDP Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/proxy/udp/{port}/{endpoint}?sni={sni}           | Setup UDP proxy on the given port, forwarding to the given endpoint. Optionally specify an SNI match for TLS traffic. |
| POST |	/proxy/udp/{port}/{endpoint}/retries/{retries}?sni={sni}   | Setup UDP proxy on the given port, forwarding to the given endpoint, and retry failed connections as well as failed packet writes up to the given number of retries |

#### UDP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| packetCount | int  | Number of packets received  |
| packetCountByUpstream | map[string]int  | Number of packets sent per upstream endpoint |
| packetCountByDomain | map[string]int  | Number of packets sent per DNS domain (for proxying to DNS servers) |
| packetCountByUpstreamDomain | map[string]int  | Number of packets sent per upstream endpoint per DNS domain (for proxying to DNS servers) |
