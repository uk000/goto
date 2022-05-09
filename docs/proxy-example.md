#### Request Proxying API Examples:

```
curl -X POST localhost:8080/proxy/targets/clear

# Add an HTTP target using JSON payload, with URI match and URI rewrite
curl http://localhost:8080/proxy/targets/add --data '{"name": "t1", "protocol": "HTTP/2.0", "endpoint":"http://localhost:8081", "routes":{"/foo/{x}/bar/{y}": "/abc/{y:.*}/def/{x:.*}"}, "enabled":true}'

# Send a request to the proxy for the above target. The upstream call receives URI `/abc/456/def/123`
curl -v localhost:8080/foo/123/bar/456

# Add an HTTP target using JSON payload, with headers match and headers rewrite.
curl http://localhost:8080/proxy/targets/add --data '{"name": "t2", "endpoint":"http://localhost:8081", "routes":{"/": ""}, "matchAll":{"headers":[["foo", "{x}"], ["bar", "{y}"]]}, "addHeaders":[["abc","{x}"], ["def","{y}"]], "removeHeaders":["foo"], "enabled":true}'

# Send a request to the proxy for the above target. Upstream call receives headers 'Abc:123', 'Def:456' and 'Bar:456'.
curl -v localhost:8080 -H'foo:123' -H'bar:456'

# Add an HTTP target via API
curl -X POST localhost:8080/proxy/http/targets/add/t1\?url=localhost:8081\&from=/
curl -X POST localhost:8080/proxy/http/targets/t1/route\?from=/foo\&to=/bar
curl -X POST localhost:8080/proxy/http/targets/t1/match/header/foo=1
curl -X POST localhost:8080/proxy/http/targets/t1/match/query/bar=123

# Send a request to the proxy for the above target. Upstream call receives headers 'Abc:123', 'Def:456' and 'Bar:456'.
curl -v localhost:8080 -H'foo:1' -H'bar:123'

# The upstream URL may be HTTPS while the proxy port is plain HTTP, and vice versa.
curl -X POST localhost:8000/port=8080/proxy/targets/add/t2\?url=https://localhost:8443\&from=/

# Add a TCP target for proxy port 9000, with upstream listening on TCP port 7000
curl -X POST localhost:8000/port=9000/proxy/tcp/targets/add/t1\?address=localhost:7000

# Add a TCP target for proxy port 9000 with multiple upstream endpoints using TLS+SNI match
curl -X POST localhost:8000/port=9000/proxy/tcp/targets/add/t1\?address=localhost:7001\&sni=a.com,b.com
curl -X POST localhost:8000/port=9000/proxy/tcp/targets/add/t2\?address=localhost:7002\&sni=c.com

# Invoke TCP sessions for the above targets by passing correct servername from the client.
curl -vk https://a.com:9000 --resolve a.com:9000:127.0.0.1
openssl s_client -connect localhost:9000 -servername c.com

# Remove a target
curl -X POST localhost:8080/port=9000/proxy/targets/t1/remove

# Disable a target
curl -X POST localhost:8080/port=9000/proxy/targets/t2/disable

# Enable a target
curl -X POST localhost:8080/port=9000/proxy/targets/t2/enable

# Get all targets
curl -s localhost:8080/port=9000/proxy/targets | jq

# Get proxy report for a target
curl -s localhost:8080/proxy/targets/t1/report | jq
curl -s localhost:8000/port=9000/proxy/targets/t1/report | jq

# Get proxy report for all TCP, or all HTTP, or just all targets for a port
curl -s localhost:8080/proxy/report/http | jq
curl -s localhost:8000/port=9000/proxy/report/tcp | jq
curl -s localhost:8080/proxy/report | jq

# Get a combined proxy report for all ports
curl -s localhost:8080/proxy/all/report | jq

# Clear tracking info for a port
curl -X POST localhost:8000/port=9000/proxy/report/clear

# Clear tracking info for all port
curl -X POST localhost:8000/proxy/all/report/clear

```
