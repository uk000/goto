#### Request Proxying API Examples:

The commands below assume `goto` is started with the ports `8080` (the HTTP port that proxy APIs are called on), `8000` (another HTTP port), `9000` (a TCP listener) and `9091` (a gRPC listener), e.g. `--ports 8080,8000,9000/tcp --grpcPort 9091`, and an upstream listening on `8081`. The `/port={port}/...` prefix configures another port of the instance via the port that the request is sent to; the port must already be a listener. See the [HTTP proxy](../pkg/proxy/http/README.md), [TCP proxy](../pkg/proxy/tcp/README.md), [UDP proxy](../pkg/proxy/udp/README.md) and [gRPC proxy](../pkg/proxy/grpc/README.md) READMEs for all the fields and APIs.

##### HTTP proxy

```
# Remove all the HTTP proxy targets of the port
curl -X POST localhost:8080/proxy/http/targets/clear

# Add an HTTP target using a JSON payload, with a URI match and a URI rewrite that uses the captured path variables
# (variable names must be lower case)
curl -X POST localhost:8080/proxy/http/targets/add --data '{
  "name": "t1", "enabled": true,
  "endpoints": {"ep1": {"url": "http://localhost:8081"}},
  "triggers": {"trig1": {"matchAny": [{"uriPrefix": "/foo/{x}/bar/{y}"}], "endpoints": ["ep1"]}},
  "transform": {"uriMap": {"/foo/{x}/bar/{y}": "/abc/{y}/def/{x}"}}
}'

# Send a request to the proxy for the above target. The upstream call receives URI `/abc/456/def/123`.
# The proxy answers with a JSON envelope of the upstream responses, keyed by target name and endpoint name.
curl -v localhost:8080/foo/123/bar/456

# Add an HTTP target using a YAML payload, with a header match and a header rewrite, and return the upstream response as it is
cat > t2.yaml <<'EOF'
name: t2
enabled: true
endpoints:
  ep1:
    url: http://localhost:8081
triggers:
  trig1:
    matchAny:
      - uriPrefix: /hdr
        headers:
          foo: "1"
    endpoints:
      - ep1
transform:
  headers:
    add:
      abc: "123"
    remove:
      - foo
trafficConfig:
  transparent: true
EOF
curl -X POST localhost:8080/proxy/http/targets/add --data-binary @t2.yaml

# Send a request to the proxy for the above target. The upstream call receives the header 'Abc: 123' and not 'Foo'.
curl -v localhost:8080/hdr -H'foo:1'

# Add, remove and clear headers of the upstream calls of an existing target (or of one of its triggers) via API
curl -X POST localhost:8080/proxy/http/targets/t2/headers/add/def=456
curl -X POST localhost:8080/proxy/http/targets/t2/trig1/headers/add/ghi=789
curl -X POST localhost:8080/proxy/http/targets/t2/headers/remove/bar
curl -X POST localhost:8080/proxy/http/targets/t2/headers/clear/def

# The upstream URL may be HTTPS while the proxy port is plain HTTP, and vice versa. Configure port 8080 via port 8000.
curl -X POST localhost:8000/port=8080/proxy/http/targets/add --data '{
  "name": "t3", "enabled": true,
  "endpoints": {"ep1": {"url": "https://localhost:8443"}},
  "triggers": {"trig1": {"matchAny": [{"uriPrefix": "/secure"}], "endpoints": ["ep1"]}}
}'

# Disable, enable and remove a target; disable and enable the HTTP proxy of the port altogether
curl -X POST localhost:8080/proxy/http/targets/t2/disable
curl -X POST localhost:8080/proxy/http/targets/t2/enable
curl -X POST localhost:8080/proxy/http/targets/t2/remove
curl -X POST localhost:8080/proxy/http/disable
curl -X POST localhost:8080/proxy/http/enable

# Get the proxy (targets and tracker) of the port, just its targets, and the targets of all ports
curl -s localhost:8080/proxy/http | jq
curl -s localhost:8080/proxy/http/targets | jq
curl -s localhost:8080/proxy/http/targets/all | jq

# Get the report of one target, of the HTTP proxy of the port, and of the HTTP proxies of all ports
curl -s localhost:8080/proxy/http/targets/t1/tracker | jq
curl -s localhost:8080/proxy/http/trackers | jq
curl -s localhost:8080/proxy/http/trackers/all | jq

# Clear the tracking info of the port
curl -X POST localhost:8080/proxy/trackers/clear
```

##### TCP proxy

```
# Open TCP port 9100 (if it is not open yet) and proxy it to the upstream localhost:7000. Other upstreams of the port are kept.
curl -X POST "localhost:8080/proxy/tcp/9100?address=localhost:7000"

# Set the upstreams of TCP port 9000, which must already be a TCP listener. Replaces the upstreams that the port had.
# One upstream, with a delay of 100ms before each write
curl -X POST localhost:8080/port=9000/proxy/tcp/upstreams/add --data '{
  "t1": {"endpoints": {"ep1": {"address": "localhost:7000"}}, "delay": {"min": "100ms", "max": "100ms"}}
}'

# Several upstreams chosen by the TLS SNI of the client
curl -X POST localhost:8080/port=9000/proxy/tcp/upstreams/add --data '{
  "t1": {"endpoints": {"ep1": {"address": "localhost:7001"}}, "match": {"sni": "a.com,b.com"}},
  "t2": {"endpoints": {"ep1": {"address": "localhost:7002"}}, "match": {"sni": "c.com"}}
}'

# Invoke TCP sessions for the above upstreams by passing the correct servername from the client.
curl -vk https://a.com:9000 --resolve a.com:9000:127.0.0.1
openssl s_client -connect localhost:9000 -servername c.com

# Get the TCP proxy (upstreams and tracker) of a port, just its upstreams, and the TCP proxies of all ports
curl -s localhost:8080/port=9000/proxy/tcp | jq
curl -s localhost:8080/port=9000/proxy/tcp/upstreams | jq
curl -s localhost:8080/proxy/tcp/all | jq

# Make port 9000 an HTTP CONNECT proxy, and use it
curl -X POST localhost:8080/port=9000/proxy/tcp/http/connect/enable
curl -x http://localhost:9000 https://example.com
```

There is no API to remove or disable a single TCP upstream: set the upstreams again (the given ones replace the existing ones, `{}` removes all of them).

##### UDP proxy and gRPC proxy

```
# Proxy UDP port 9200 to the upstream localhost:7100, with a delay of 200ms
curl -X POST localhost:8080/proxy/udp/9200/localhost:7100
curl -X POST localhost:8080/proxy/udp/9200/delay/200ms

# Proxy the gRPC service `Goto` of the gRPC port 9091 to an upstream gRPC server (plaintext upstream), and check the status
curl -X POST localhost:8080/port=9091/proxy/grpc/Goto/localhost:7200
curl -s localhost:8080/port=9091/proxy/grpc/status | jq
curl -X POST localhost:8080/port=9091/proxy/grpc/clear
```

The gRPC port of a proxy must be given with the `/port=` prefix (without it the proxy is set up for the port that the request is sent to). The service that is proxied must be in the gRPC service registry (a built-in service, or one added with a proto file); a service that `goto` also serves locally is replaced by the proxy for all the gRPC ports of the instance, and stays so after `/proxy/grpc/clear`.

##### Clearing the tracking info

```
# Clear the tracking info of the proxies of the port, and of all ports
curl -X POST localhost:8080/proxy/trackers/clear
curl -X POST localhost:8080/proxy/trackers/all/clear
```
