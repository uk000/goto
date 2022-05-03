# Using Goto for upstream service chaos

## Scenario: Mocking Upstream Service Death

Launch goto server with two ports. We want 8080 to be closeable, so first port in the list is 8000 since the first port cannot be closed.
```
$ goto --ports 8000,8080
```

Call admin API on the correct port (8080) to configure a custom response for a custom API /foo/bar
```
$ curl -X POST -g localhost:8080/server/response/payload/set/uri?uri=/foo/{somefoo}/bar/{somebar} --data '{"version: "1.0", "foo": "{somefoo}", "bar": "{somebar}"}'
Port [8080] Payload set for URI [/foo/{somefoo}/bar/{somebar}] : content-type [application/x-www-form-urlencoded], length [57]
```

Confirm that goto indeed responds with a valid payload for a given request
```
$ curl -s localhost:8080/foo/f1/bar/b1
{"version: "1.0", "foo": "f1", "bar": "b1"}
```

At this time, start client application and send requests to port 8080

Once client traffic is running, we'll ask goto to close port 8080
```
$ curl -X POST localhost:8000/server/listeners/8080/close
```

Verify that the port is closed
```
$ curl -v localhost:8080/foo/f1/bar/b1
curl: (7) Failed to connect to localhost port 8080: Connection refused
```

Call admin API to reconfigure the API response. Since the port 8080 is closed, we'll ask goto on its main port 8000 to reconfigure port 8080!!
```
$ curl -X POST -g localhost:8000/port=8080/server/response/payload/set/uri?uri=/foo/{somefoo}/bar/{somebar} --data '{"version: "2.0", "foo": "{somefoo}", "bar": "{somebar}"}'
Port [8080] Payload set for URI [/foo/{somefoo}/bar/{somebar}] : content-type [application/x-www-form-urlencoded], length [57]
```

Once client traffic is confirmed to be broken and expected client behavior is verified, we'll ask goto to reopen port 8080
```
$ curl -X POST localhost:8000/server/listeners/8080/open
```

Confirm that goto responds on the reopened port with the new payload v2.0
```
$ curl -s localhost:8080/foo/f2/bar/b2
{"version: "2.0", "foo": "f2", "bar": "b2"}
```

Verify the client behavior again to ensure that it behaves as expected (whether it's expected to reconnect and resume traffic, or otherwise).

#
## Scenario: Mocking Upstream TLS Certs Chaos

Launch goto server with three ports (we only need two, third is just for fun). 8443 is an HTTPS port and closeable. Goto auto-generates TLS cert for 8443 using the given CN=foo.com.
```
$ goto --ports 8000,8080,8443/https/foo.com
```

If you want to test with a real cert because your service validates the cert for authenticity, you can upload the real cert for the port now. If you don't wish to use a custom cert and are fine with the auto-generated cert, skip the next 3 commands and continue.
```
$ curl -X PUT localhost:8000/server/listeners/8443/cert/add --data-binary @/some/path/real-cert.crt
Cert added for listener 8443

$ curl -X PUT localhost:8000/server/listeners/8443/key/add --data-binary @/some/path/real-cert.key
Key added for listener 8443

$ curl -X POST localhost:8000/server/listeners/8443/reopen
TLS Listener reopened on port 8443
```
At this point, we can verify that the TLS port is responding with the expected cert
```
$ curl -vk https://localhost:8443/
```

Call admin API on port 8000 to configure a custom response for a custom API /foo/bar for port 8443. We could configure 8443 directly too, but then our script would have to deal with HTTPS.
```
$ curl -X POST -g localhost:8000/port=8443/server/response/payload/set/uri?uri=/foo/{somefoo}/bar/{somebar} --data '{"version: "1.0", "foo": "{somefoo}", "bar": "{somebar}"}'
Port [8443] Payload set for URI [/foo/{somefoo}/bar/{somebar}] : content-type [application/x-www-form-urlencoded], length [57]
```

Confirm that goto indeed responds with a valid payload for a given request
```
$ curl -vk https://localhost:8443/foo/f1/bar/b1
{"version: "1.0", "foo": "f1", "bar": "b1"}
```

At this time, start client application and send requests to port 8443

Once client traffic is running, we'll ask goto to replace the cert with a different cert.

We can upload a self-signed invalid cert to goto using the same 3 commands as before.
```
$ curl -X PUT localhost:8000/server/listeners/8443/cert/add --data-binary @/some/path/invalid-cert.crt
$ curl -X PUT localhost:8000/server/listeners/8443/key/add --data-binary @/some/path/invalid-cert.key
$ curl -X POST localhost:8000/server/listeners/8443/reopen
```

Alternately we can also ask goto to auto-generate a new cert for the port using a new CN. Which path you take here depends on the specific testing requirement.
```
$ curl -X POST localhost:8000/server/listeners/8443/cert/auto/bar.com
Cert auto-generated for listener 8443
```

Call admin API to reconfigure the API response with new payload v2.0 so we can confirm the response is new.
```
$ curl -X POST -g localhost:8000/port=8080/server/response/payload/set/uri?uri=/foo/{somefoo}/bar/{somebar} --data '{"version: "2.0", "foo": "{somefoo}", "bar": "{somebar}"}'
Port [8080] Payload set for URI [/foo/{somefoo}/bar/{somebar}] : content-type [application/x-www-form-urlencoded], length [57]
```

Test the client traffic against this reopened port that uses a different cert now.
```
$ curl -vk https://localhost:8443/foo/f1/bar/b1
{"version: "2.0", "foo": "f1", "bar": "b1"}
```

Verify the client behavior and validate against your hypothesis.

#