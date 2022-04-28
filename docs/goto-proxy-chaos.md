# Scenario: Creating chaos with `Goto` as a proxy between a downstream client and an upstream service
This document provides a brief walkthrough of some of the goto features as a proxy standing between a client and an upstream service, providing you a point in the network where you can observe traffic and introduce some chaos.

Launch a goto server that'll act as an intermediate proxy on port 8080.
```
$ goto --ports 8000,8080
```
Add an upstream service endpoint that'll be used as proxy's upstream destination
```
$ curl -X POST localhost:8000/port=8080/proxy/targets/add/serviceXYZ\?url=localhost:8081
Port [8080]: Added empty proxy target [serviceXYZ] with URL [localhost:8081]
```

Add a URI routing rule for this upstream endpoint that'll route all inbound requests on this port transparently to the upstream. Here we use URI `/` to represent all traffic
```
$ curl -X POST localhost:8000/port=8080/proxy/targets/serviceXYZ/route\?from=/
Port [8080]: Added URI routing for Target [serviceXYZ], URL [localhost:8081], From [/] To []
```

At this point, all traffic on port 8080 should get routed to upstream endpoint 'localhost:8081', and the response from the upstream endpoint should get sent back to the downstream client. Send some client calls to confirm (assuming some service is listening on localhost:8081)
```
$ curl -v localhost:8080/foo
$ curl -v localhost:8080/bar
```

Goto as a proxy add its own response headers to the response too in addition to sending the upstream response headers back to the downstream client. These headers can be useful to confirm on the client when the request reached the proxy, etc.

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

Now the client requests should start seeing an additional delay of 1s on top of the processing time taken for the normal calls. This delay will be applied by goto acting as a proxy, and the goto response headers should show the response time of the upstream service as well as for itself. Looks for these response headers from goto proxy:
```
Goto-Upstream-Took|t1: 868.7µs
Goto-Took|proxy: 1.0046516s
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
Goto-Upstream-Status|t1: 200 OK
Goto-Response-Status|proxy: 502
...
```

The above few examples should give you an idea of what you can achieve using `goto` as a proxy for chaos testing.