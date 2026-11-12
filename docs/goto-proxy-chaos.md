# Scenario: Creating chaos with Goto as an HTTP proxy
This document provides a brief walk-through of some of the goto features as a proxy standing between a client and an upstream service, providing you a point in the network where you can observe traffic and introduce some chaos.

Launch a goto server that'll act as an intermediate proxy on port 8080.
```
$ goto --ports 8000,8080
```
Add a proxy target with an upstream service endpoint that'll be used as proxy's upstream destination, and a trigger that routes the matching inbound requests on this port to the upstream. Here we match the URI prefixes `/foo` and `/bar`, and ask the proxy to return the upstream response as it is (`transparent`). The target is created enabled; targets are disabled unless `enabled` is set.
```
$ curl -X POST localhost:8000/port=8080/proxy/http/targets/add --data '{
  "name": "serviceXYZ", "enabled": true,
  "endpoints": {"ep1": {"url": "http://localhost:8081"}},
  "triggers": {"t1": {"matchAny": [{"uriPrefix": "/foo"}, {"uriPrefix": "/bar"}], "endpoints": ["ep1"]}},
  "trafficConfig": {"transparent": true}
}'
```
The response is the added target, as YAML. The port `8080` must already be a listener, as it was started with `goto --ports 8000,8080`.

> &#x1F4DD; Avoid a catch-all trigger (`uriPrefix: /`) on a port that you keep configuring: the proxy would also capture the admin calls to that port (including the ones above that use `/port=8080/...`), which are then forwarded to the upstream and not executed.

At this point, requests on port 8080 with URIs starting with `/foo` or `/bar` should get routed to upstream endpoint 'localhost:8081', and the response from the upstream endpoint should get sent back to the downstream client. Send some client calls to confirm (assuming some service is listening on localhost:8081)
```
$ curl -v localhost:8080/foo
$ curl -v localhost:8080/bar
```

Goto as a proxy add its own response headers to the response too in addition to sending the upstream response headers back to the downstream client: `Goto-Proxy-Upstream-Status` (the status of each upstream call, e.g. `{"serviceXYZ":{"ep1":200}}`), `Goto-Proxy-Response-Status`, `Goto-Proxy-In-At`, `Goto-Proxy-Out-At`, `Goto-Proxy-Head-Took` and, as a trailer, `Goto-Proxy-Took`. These headers can be useful to confirm on the client when the request reached the proxy, etc. Without `transparent`, the proxy answers with a JSON envelope of the upstream responses keyed by target and endpoint name instead.

Now we can introduce some chaos in the proxy. We can start by shutting down the port to mimic network outage.
```
$ curl -X POST localhost:8000/server/listeners/8080/close
```

Once you verify the client and server behavior upon such network outage, you can bring the network back by reopening the port.
```
$ curl -X POST localhost:8000/server/listeners/8080/open
```

Now the proxy traffic should start flowing transparently again.

Another form of network chaos is when network starts slowing down. We can simulate that with goto. We can set a delay for all requests or specific URIs, and for a specific number of requests or for all subsequent requests until explicitly cleared.

```
$ curl -X POST localhost:8000/port=8080/server/request/uri/set/delay=1s?uri=/foo
Port [8080] will delay all requests for URI [/foo] by [1s] forever
```

> &#x1F4DD; Alternately, you can set a delay per target in its traffic config, by adding `"trafficConfig": {"delay": {"min": "1s", "max": "1s"}, "transparent": true}` to the target (or its trigger). The delay is applied before the upstream call, and is reported in the `Goto-Proxy-Delay` response header. The value is picked once, when the target is added (for a range such as `100ms`-`1s`, every request of the target then gets that same value), so add the target again to pick a new one. The traffic config delay applies to a target, whereas the URI delay API used above allows setting a delay per URI.

Now the client requests should start seeing an additional delay of 1s on top of the processing time taken for the normal calls. This delay will be applied by goto acting as a proxy, and the goto response headers should show the time taken by goto before the response headers were sent. Look for this response header from goto proxy:
```
Goto-Proxy-Head-Took: 1.0046516s
```

We can go one step further and recreate a scenario where an intermediate gateway starts acting, randomly throwing 502 or 503 even though the upstream service is acting fine.

`Goto` allows setting a specific response status code that it'll use to respond, either for all requests or for specific URIs, and either for a specific number of requests or for all subsequent requests until cleared. For the current scenario, we'll do just that, asking the proxy goto instance to respond with `502`.

```
$ curl -X POST localhost:8000/port=8080/server/request/uri/set/status=502?uri=/foo
```

If you send client traffic to the goto proxy now, the client should see a 502 regardless of what upstream service responded with. Goto will report via response headers the status code it received from the upstream service and the status code it responded with to the downstream client, to make it clear where the 502 is coming from.

```
$ curl -v localhost:8080/foo
...
< HTTP/1.1 502 Bad Gateway
...
< Goto-Proxy-Upstream-Status: {"serviceXYZ":{"ep1":200}}
< Goto-Proxy-Response-Status: 502
...
```

Another way to make the proxy answer with other statuses is the proxy response mapping, which translates the upstream's statuses. For example, to answer 503 when the upstream returns any 5xx status, or when the upstream can't be reached at all (the status range `[0, 0]`; without it the proxy answers 200 with an upstream status of 0):
```
$ curl -X POST localhost:8000/port=8080/proxy/http/responses/set --data '[
  {"upResponseRange": [500, 599], "proxyResponse": 503},
  {"upResponseRange": [0, 0], "proxyResponse": 503}
]'
```

You can check proxy report at this point for validation
```
$ curl -s localhost:8000/port=8080/proxy/http | jq
$ curl -s localhost:8000/port=8080/proxy/http/trackers | jq
$ curl -s localhost:8000/port=8080/proxy/http/targets/serviceXYZ/tracker | jq
```

The above few examples should give you an idea of what you can achieve using `goto` as a proxy for chaos testing.

See more [Proxy Examples](proxy-example.md)

#
# Scenario: Creating TCP chaos with Goto

Launch a goto server that'll act as a TCP proxy. We'll need a TCP port for this. While we could have opened a new TCP port on the fly too, we'll take the shortcut of starting `goto` with one via bootstrap.
```
$ goto --ports 8000,9000/tcp
```

Now we need to add an upstream TCP service endpoint. We assume there is a TCP service available on port 7000. If you don't have one handy, you could very well run another `goto` that listens on `7000/tcp`!
```
$ curl -X POST localhost:8000/port=9000/proxy/tcp/upstreams/add --data '{
  "SomeTCPService": {"endpoints": {"ep1": {"address": "localhost:7000"}}}
}'
```
The response is the upstreams of the port as YAML. The given upstreams replace the ones that the port had. (A shortcut that also opens the TCP port if it is not open yet: `curl -X POST "localhost:8000/proxy/tcp/9000?address=localhost:7000"`.)

At this point, `goto` will happily accept TCP connections on port `9000` and proxy it to port `7000`. Feel free to try it out.
```
# If the upstream service on port 7000 is some TCP service that can be handled via telnet, do that
$ telnet localhost 9000

# Or, if the upstream service on port 7000 is an HTTP server after all, use curl 
$ curl -v http://localhost:9000
```

Now it's time to introduce some TCP chaos. We can ask `goto` proxy to apply some delay to the TCP packets going to/from this endpoint. The delay is a field of the upstream, so we add the upstream again with a delay (this replaces the upstream of the same name):
```
$ curl -X POST localhost:8000/port=9000/proxy/tcp/upstreams/add --data '{
  "SomeTCPService": {"endpoints": {"ep1": {"address": "localhost:7000"}}, "delay": {"min": "1s", "max": "1s"}}
}'
```

We could have set the delay to be a probabilistic range. E.g. let's change it to apply delay between 1s and 3s, and do it only for next 5 writes (the `count`).
```
$ curl -X POST localhost:8000/port=9000/proxy/tcp/upstreams/add --data '{
  "SomeTCPService": {"endpoints": {"ep1": {"address": "localhost:7000"}}, "delay": {"min": "1s", "max": "3s", "count": 5}}
}'
```

Go ahead and run the TCP traffic again, and see the delay in action. `Goto` logs on the proxy instance should also show how much delay it applied, which would be some random value between 1s-3s and would only do it for next 5 packets.

> &#x1F4DD; A TCP connection that stays silent for more than 5 seconds in both directions is closed by the proxy, so a delay above that on an idle connection ends the connection.

> &#x1F4DD; Note that the math can only be approximate at best for `Goto` as a TCP proxy, since it simply applies the delays to packet writes. The number of packets `goto` proxy writes may not be 1-to-1 with the number of requests/responses client/service sends.

Now let's ask `goto` proxy to drop a certain percentage of packets, with the `dropPct` field of the upstream.
```
$ curl -X POST localhost:8000/port=9000/proxy/tcp/upstreams/add --data '{
  "SomeTCPService": {"endpoints": {"ep1": {"address": "localhost:7000"}}, "dropPct": 25}
}'
```

Now run the traffic again and observe that some requests/responses simply disappear and the client/service are left waiting. Now you can verify the behavior of your client/service if it was left hanging dry like this.

See more [Proxy Examples](proxy-example.md)

#
# Scenario: TCP Proxy with SNI matching using Goto
As a TCP proxy, `goto` supports either simple opaque routing to one endpoint, or performing TLS SNI name match to pick one of the multiple upstream endpoints. Let's put this claim to test.

Launch a goto server that'll act as a TCP proxy. While we expect `goto` to perform SNI matching, the `goto` TCP port itself will not do TLS but rather it's transparent to the client and the service.
```
$ goto --ports 8000,9000/tcp
```

Now we'll add two upstream TCP service endpoints each with an SNI hostname match.
```
$ curl -X POST localhost:8000/port=9000/proxy/tcp/upstreams/add --data '{
  "ServiceA": {"endpoints": {"ep1": {"address": "localhost:8081"}}, "match": {"sni": "a.com"}},
  "ServiceB": {"endpoints": {"ep1": {"address": "localhost:8082"}}, "match": {"sni": "b.com"}}
}'
```
Both upstreams have to be given in one call, as the call replaces the upstreams that the port had. The `sni` is a comma-separated list of server names or regular expressions.

Now all that's left to do is send two TLS requests, one with server name set to `a.com` and another with `b.com`. We could use some TCP service and openssl client, but for now curl will give us what we want. We assume that there are two HTTPS services running on ports `8081` and `8082`. If not, you know which app to use to spin one up! Let's spin those up anyway for fun.
```
$ goto --ports 8001,8081/https --cn a.com
$ goto --ports 8002,8082/https --cn b.com
```

Now we have our upstream services running too. Let's send two curl requests to the proxy.
```
$ curl -vk https://a.com:9000 --resolve a.com:9000:127.0.0.1
...
* Server certificate:
*  subject: O=a.com; OU=a.com; CN=a.com
...
< HTTP/2 200
< content-type: application/json
< goto-host: local@192.168.0.111:8081
...
```
You should see a response coming from the `goto` instance running on port `8081`. The good thing about gotos is that they identify themselves via HTTP response headers like such: `goto-host: local@192.168.0.111:8081`. Also note the CN in the server certificate for proof.


Let's send the second curl request to the proxy.
```
$ curl -vk https://b.com:9000 --resolve b.com:9000:127.0.0.1
...
* Server certificate:
*  subject: O=b.com; OU=b.com; CN=b.com
...
< HTTP/2 200
< content-type: application/json
< goto-host: local@192.168.0.111:8082
...
```
The response shows it came from the second `goto` instance with the appropriate server certificate.

This shows that the proxy was able to route to the correct upstream endpoints based on the SNI received from the client TLS handshake while still letting the upstream services do the real handshake.

Beyond this point, the two upstreams can be configured with their corresponding chaos params (`delay`, `dropPct`, `retries`, etc, set in the same `upstreams/add` call) and multiple clients can be tested against two upstream services. The report of the proxy has the tracker of its sessions and connections: `curl -s localhost:8000/port=9000/proxy/tcp | jq`.

See more [Proxy Examples](proxy-example.md)
