<a name="toc"></a>

# Goto Server Features

`Goto` as a server is useful for doing feature testing as well as chaos testing of client applications, proxies/sidecars, gateways, etc. Or, the server can also be used as a proxy to be put in between a client and a target server application, so that traffic flows through this server where headers can be inspected/tracked before forwarding the requests further. The server can add headers, replace request URI with some other URI, add artificial delays to the response, respond with a specific status, monitor request/connection timeouts, etc. The server tracks all the configured parameters, applying those to runtime traffic and building metrics, which can be viewed via various APIs.

###### <small> [Back to TOC](#toc) </small>

# <a name="goto-response-headers)"></a>
## HTTP Headers
#### Server response headers

`Goto` adds the following common response headers to all http responses it sends:

- `Goto-Host`: identifies the `goto` instance via an auto-generated identity in the format: <PodName>.<Namespace>[<PodIP>](<NodeName>[<HostIP>]@<Cluster>). `Goto` reads the identity of the instance from these environment variables: `POD_NAME` (falls back to the host name, then `local`), `POD_IP` (falls back to the IP address of the local outbound interface, then `localhost`), `NAMESPACE` (falls back to the service account namespace file in a K8s pod, then `local`), `NODE_NAME` (default `LocalNode`), `HOST_IP` (default `0.0.0.0`) and `CLUSTER` (default `LocalCluster`).

  Example, with none of the variables set: `myhost.local[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)`.
- `Via-Goto`: carries the label of the listener that served the request. By default the label is `[{ip}:{port}][{pod}@{namespace}@{cluster}]`; when `goto` is given the `--label` startup argument, that value takes the place of `{ip}` (`--label foo --ports 8080` gives `[foo:8080][{pod}@{namespace}@{cluster}]`). On the response headers it is followed by the protocol of the instance, e.g. `[foo:8080][...](HTTP)`.
- `Goto-Port`: carries the port number on which the request was received
- `Goto-Protocol`: identifies whether the request was received over `HTTP` or `HTTPS`
- `Goto-Remote-Address`: remote client's address as visible to `goto`
- `Goto-Response-Status`: HTTP response status code that `goto` responded with. This additional header is useful to verify if the final response code got changed by an intermediary proxy/gateway. 
- `Goto-In-At`: UTC timestamp when the request was received by `goto`
- `Goto-Out-At`: UTC timestamp when `goto` finished processing the request and sent a response
- `Goto-Took`: Total processing time taken by `goto` to process the request

The following response headers are added conditionally under different scenarios:

#### Client request headers:

- `From-Goto`, `From-Goto-Host`: Sent by `goto` client with each traffic invocation, passing the label and host id of the client `goto` instance
- `Goto-Request-ID`, `Goto-Target-ID`, `Goto-Target-URL`: Sent by `goto` client with each traffic invocation. RequestID and TargetID are auto-generated from the target name and request counters. TargetURL header identifies the URL that `goto` invoked, which can be useful when an intermediate proxy rewrites the URL.
- `Goto-Retry-Count`: Sent by `goto` client instance when traffic invocations are retried (if a target was configured for retries)

#### Proxy response headers
- `Goto-Proxy-Host`: identifies the `goto` instance that acted as a proxy for this request
- `Proxy-Via-Goto`: label of the `goto` instance that acted as a proxy for this request
- `Goto-Proxy-Port`: port number on which the `goto` proxy instance received this request, which may be different than the upstream service port
- `Goto-Proxy-Protocol`: Protocol over which the `goto` proxy instance served this request, which may be different than the protocol used for the upstream service call
- `Goto-Proxy-Response-Status`: HTTP response status code that `goto` proxy instance responded with. Upstream's response status code is preserved in another header listed below.
- `Goto-Proxy-In-At`: UTC timestamp when the request was received by the `goto` proxy instance
- `Goto-Proxy-Out-At`: UTC timestamp when the `goto` proxy instance finished processing the request and sent a response
- `Goto-Proxy-Head-Took`: Time taken by the `goto` proxy instance from receiving the request to sending the head of the response
- `Goto-Proxy-Took`: Time taken by the `goto` proxy instance from the first byte of the request to the last byte of the response. It is a trailer, so it is only there if the response has no `Content-Length` and the body is read to the end
- `Goto-Proxy-Upstream-Status_<upstream-name>`: status the `goto` proxy's upstream service responded with, which may be different than the status `goto` responded with to the downstream client
- `Goto-Proxy-Upstream-Took_<upstream-name>`: Total roundtrip time the proxy's upstream call took
- `Goto-Proxy-Delay`: any delay added to the request/response by `goto` as a proxy
- `Goto-Proxy-Request-Dropped`: sent back with response when the proxy drops the downstream request based on the drop percentage defined for the upstream target (see proxy feature)
- `Goto-Proxy-Response-Dropped`: sent back with response when the proxy drops the upstream response based on the drop percentage defined for the upstream target (see proxy feature)

#### CONNECT proxy response headers
A port in CONNECT mode (see the [CONNECT proxy](../proxy/tcp/README.md#http-connect-proxy)) has headers of its own, which are none of the ones above, so the two proxies can be told apart. The proxy sends them to any client, `curl -x` included (`-v` shows the answer to the CONNECT request).
- `Goto-Connect-Proxy-Took`: time taken by the proxy from the first byte of the request to the answer to the CONNECT request, and, in the responses of a tunnel that carries HTTP, to the head of each response
- `Goto-Connect-Proxy-Delay`: any delay that the proxy added, in the same two places
- `Goto-Connect-Proxy-DNS-Took`, `Goto-Connect-Proxy-Dial-Took`: how much of the time of the answer to the CONNECT request went to resolving the name of the destination, and to connecting to it (only in the answer)
- `Goto-Connect-Proxy-Host`, `Goto-Connect-Proxy-Port`, `Goto-Connect-Proxy-Remote-Address`, `Goto-Connect-Proxy-Upstream-Status`: the identity of the proxy, the address of the client, and the status that the destination answered with (only in the responses of a tunnel that carries HTTP)

#### Tunnel response headers:
- `Goto-Tunnel-Host-<seq>`: identifies `goto` instance hosts through which this request was tunneled along with the sequence number of each instance in the tunnel chain.
- `Via-Goto-Tunnel-<seq>`: identifies `goto` instance labels through which this request was tunneled along with the sequence number of each instance in the tunnel chain.
- `Goto-In-At-<seq>`: UTC timestamp when the request was received by each `goto` in the tunnel chain
- `Goto-Out-At-<seq>`: UTC timestamp when the request finished processing by the `goto` tunnel instance
- `Goto-Took-<seq>`: Total processing time taken by the `goto` tunnel instance

#### Use-case based response headers:
- `Goto-Response-Delay`: set if `goto` applied a configured delay to the response.
- `Goto-Payload-Length`, `Goto-Payload-Content-Type`: set if `goto` sent a configured response payload
- `Goto-Chunk-Count`, `Goto-Chunk-Length`, `Goto-Chunk-Delay`, `Goto-Stream-Length`, `Goto-Stream-Duration`: set when client requests a streaming response
- `Goto-Requested-Status`: set when `/status` API request is made requesting a specific status
- `Goto-Forced-Status`, `Goto-Forced-Status-Remaining`: set when a configured custom response status is applied to a response that didn't have a URI-specific response status
- `Goto-URI-Status`, `Goto-URI-Status-Remaining`: set when a configured custom response status is applied to a uri
- `Goto-Status-Flip`: set when a flip-flop status request resulted in a status change from the previous response's status (see the feature to understand it better)
- `Goto-Filtered-Request`: set when a request is filtered due to a configured `ignore` or `bypass` filter
- `Request-*`: prefix is added to all request headers and the request headers are sent back as response headers

#### Probe response headers
- `Readiness-Request-*`: prefix is added to all request headers for Readiness probe requests
- `Liveness-Request-*`: prefix is added to all request headers for Liveness probe requests
- `Readiness-Request-Count`: header added to readiness probe responses, carrying the number of readiness requests received so far
- `Readiness-Overflow-Count`: header added to readiness probe responses, carrying the number of times readiness request count has overflown
- `Liveness-Request-Count`: header added to liveness probe responses, carrying the number of liveness requests received so far
- `Liveness-Overflow-Count`: header added to liveness probe responses, carrying the number of times liveness request count has overflown
- `Stopping-Readiness-Request-*`: set when a readiness probe is received while `goto` server is shutting down


###### <small> [Back to TOC](#toc) </small>

# <a name="goto-server-logs"></a>

### Goto Logs

`goto` server logs are generated with a useful pattern to help figuring out the steps `goto` took for a request. Each log line tells the complete story about request details, how the request was processed, and response sent. Each log line contains the following segments separated by `-->`:

<details>
<summary>Goto Log Format</summary>

- Request Timestamp
- Listener Label: label of the listener that served the request
- Local and Remote addresses (if available)
- Request Protocol
- Request Host (from Host request header)
- Request Content Length
- Request Headers (if logging enabled)
- Request Body Length (if logging enabled)
- Request Body or Request Mini Body (first and last 50 characters from request body) (if logging enabled)
- Request URI, Protocol and Method
- Action(s) taken by `goto` (e.g. delaying a request, echoing back, responding with custom payload, etc.)
- Response Headers (if logging enabled)
- Response Status Code (final code sent to client after applying any configured overrides)
- Response Body Length
- Response Body or Response Mini Body (first and last 50 characters from response body) (if logging enabled)

`goto` client, invocation, job, proxy and tunnel logs produce multiline logs for the tasks being performed.

#### Sample server log line:

```
2021/07/31 16:38:07.400110 [Goto] --> LocalAddr: [[::1]:8080], RemoteAddr: [[::1]:52103], Protocol [HTTP/1.1], Host: [localhost:8080], Content Length: [154] --> Request Headers: {"Accept":["*/*"],"Content-Length":["154"],"Content-Type":["application/x-www-form-urlencoded"],"User-Agent":["curl/7.76.1"]} --> Request Body Length: [154] --> Request Mini Body: [a1234567890b1234567890c1234567890d1234567890e12345...567890k1234567890l1234567890m1234567890n1234567890] --> Request URI: [/hello], Protocol: [HTTP/1.1], Method: [POST] --> Responding with configured payload of length [154] and content type [text/plain] for URI [/hello] --> Response Status Code: [200] --> Response Body Length: [154] --> Response Mini Body: [a1234567890b1234567890c1234567890d1234567890e12345...567890k1234567890l1234567890m1234567890n1234567890]
```
</details>

# <a name="listeners"></a>
## Listeners

#### Features
- The server starts with a bootstrap http listener. When startup arg `--ports` is used, the first port in the list is treated as the bootstrap port and is forced to be an HTTP port (`--port` sets it when `--ports` is not used; the default is 8080). Two more listeners are always added at startup: a JSON-RPC listener on `--rpcPort` (default 3000, serves MCP and A2A traffic) and a gRPC listener on `--grpcPort` (default 1234).
- The bootstrap port is not meant to be managed via listeners APIs. The APIs do not reject it, but the server itself holds the port, so a restart cannot bind it: an `add` (or `update`) on the bootstrap port waits for 25s (5 attempts, 5s apart) and then answers `200 Listener {port} already present, failed to restart.`. A startup config file (`--config`) cannot reconfigure the bootstrap, JSON-RPC and gRPC ports either; it logs an error and skips them.
- The `listeners APIs` let you manage/open/close an arbitrary number of HTTP/HTTPS/TCP/TCP+TLS/gRPC/UDP listeners (except the default bootstrap listener that is set as HTTP and cannot be modified). 
- All HTTP listener ports respond to the same set of API calls, so any of the admin APIs described in this document as well as any arbitrary URI call for runtime traffic can be done via any active HTTP listener. 
- Any of the TCP operations described in the TCP section can be performed on any active TCP listener, and any of the gRPC operations can be performed on any gRPC listener. 
- The HTTP listeners perform double duty of also acting as gRPC listeners, but listeners explicitly configured as `gRPC` act as `gRPC-only` and don't support HTTP operations. See `gRPC` section later in this doc for details on gRPC operations supported by `goto`.
- A TLS listener accepts plain-text clients as well as TLS clients on the same port: `goto` looks at the first byte of each connection to tell them apart.
- `/server/listeners` API lets you view a list of configured listeners, including the default bootstrap listener. Both `/server/listeners` and `/server/listener` (singular) are accepted in all the URIs below.
- Listeners can also be declared in a startup config file (key `listeners`, see the `--config` argument in the [command arguments](../../cmd/README.md#startup-config-files)); the sample config is [sample-listeners-config.yaml](listeners/sample-listeners-config.yaml).

#### <a name="listener-startup-ports"></a>Startup listeners: the `--ports` argument
`--ports` takes a comma-separated list of `{port}[/{protocol}[/{option}]]` entries, for example:

```
--ports 8080,8081/http,8443/https,8444/https/mtls,8445/https/mtlss,8000/tcp,8001/tcp/->8443,9000/tls,10000/grpc,3000/rpc
```

> A `udp` entry (`5353/udp`) is accepted by the argument parser but no UDP listener is opened for it at startup, so add UDP listeners with the listener API (`{"port":5353,"protocol":"udp","open":true}`) or through a startup config file.

- The first entry is the bootstrap HTTP port, and its protocol is always `http` (anything else given for it is ignored).
- `{protocol}` is any value from the protocols table below. When it is left out the port is an `http` port.
- `{option}` is one of `mtls` (the TLS listener requires a client certificate, without verifying it), `mtlss` (requires a client certificate and verifies it against the CA certs of the listener), or `->{port}` (forward: a `tcp` listener relays its connections to the listener on that port). The common name of the auto-generated certs is not part of the entry: it comes from the `--cn` argument (default `goto.goto`).
- An invalid port number (not an integer, or outside 1-65535) stops the process at startup (`Error: Invalid port [N]`). A port that is repeated in the list is logged as `Error: Duplicate port [N]` and skipped.
- Startup listeners get the label `[{ip}:{port}][{pod}@{namespace}@{cluster}]` (when `--label` is given it replaces `{ip}`, for every listener, the bootstrap one included). TLS startup listeners use auto-generated certs.

#### <a name="listener-protocols"></a>Protocols
`protocol` (in the listener JSON, or in `--ports`) is not case sensitive. These values are understood:

|`protocol`|Serves|TLS|Reported `l8Proto`|Notes|
|---|---|---|---|---|
| `http` | HTTP/1.x and cleartext HTTP/2 (h2c) | no | `h2c` | |
| `http1` | HTTP/1.x only | no | `http` | Stored as protocol `http`. Use to test clients when HTTP/2 is explicitly disallowed. |
| `https` | HTTP/1.x and HTTP/2 | yes | `h2` | A cert is auto-generated unless one is added. |
| `https1` | HTTP/1.x only | yes | `https` | Stored as protocol `https`. |
| `httpss` | HTTP/1.x and HTTP/2 | yes | `h2` | Same as `https` with `verifyClientCert` on (a client cert is verified if the client presents one). |
| `grpc` | gRPC only (no HTTP APIs) | no | `grpc` | |
| `grpcs` | gRPC only | yes | `grpc` | Stored as protocol `grpc`. **Known issue:** the listener does not offer ALPN `h2` in the TLS handshake, so gRPC clients that enforce ALPN (grpc-go 1.67 and later, including the goto client) cannot connect to it. Tracked as bug RPX-27 in `bug-report/`. |
| `grpcss` | gRPC only | yes | `grpc` | Same as `grpcs` with `verifyClientCert` on (same ALPN known issue). |
| `xds` | gRPC with the XDS server | no | `xds` | Stored as protocol `grpc`. |
| `jsonrpc`, `rpc` | JSON-RPC over HTTP/2 (MCP, A2A) | no | `jsonrpc` | Stored as protocol `http`. |
| `jsonrpcs`, `rpcs` | JSON-RPC over HTTP/2 | yes | `jsonrpc` | Stored as protocol `https`. |
| `tcp` | raw TCP, see the [TCP Server](tcp/README.md) | no | `tcp` | |
| `tls` | raw TCP over TLS | yes | `tcp` | |
| `udp` | UDP, see [UDP APIs](#udp-apis) | no | `udp` | |

> A `protocol` that is not in this table is not rejected: the listener is created as a `tcp` listener. A typo such as `htp` therefore gives a TCP listener.

> For any TLS protocol, `goto` sets `autoCert` when no certificate was given, so the listener serves a self-signed cert generated for its `commonName` (default `goto.goto`).

#### Prefixing APIs with `/port={port}`
- Several configuration APIs (used to configure server features on `goto` instances) support `/port={port}/...` URI prefix to allow use of one listener to configure another listener's HTTP features. This allows for configuring another listener that might be closed or otherwise inaccessible when the configuration call is being made.
- For example, the API `http://localhost:8081/probes/readiness/set/status=503` that's meant to configure readiness probe for listener on port 8081, can also be invoked via another port as `http://localhost:8080/port=8081/probes/readiness/set/status=503`. 

#### TLS Listeners
`Goto` provide following kind of TLS listeners, configured via `protocol` field (in listener config JSON or at startup):
- Protocol: `https`
  - Serve HTTPS traffic over both HTTP/1.x and HTTP/2 protocol.
- Protocol: `https1`
  - Serve HTTPS traffic over HTTP/1.x only with no HTTP/2 upgrade support. This can be useful for testing a client behavior when server explicitly disallows HTTP/2.
- Protocol: `grpcs`
  - Serve gRPC traffic over TLS
- Protocol: `tls`
  - Serve TCP traffic over TLS

#### Client certificates (mutual TLS)
Two fields of the listener decide what the listener asks of the client, and they combine like this:

|`mTLS`|`verifyClientCert`|Server behaviour|
|---|---|---|
| false | false | The client is not asked for a certificate. |
| false | true | The client may present a certificate; it is verified if it does. |
| true | false | The client must present a certificate, but it is not verified. |
| true | true | The client must present a certificate that is verified against the CA certs of the listener (added with `/server/listeners/{port}/ca/add`). |

For HTTP and gRPC listeners, `verifyClientCert` can only be switched on by choosing protocol `httpss` or `grpcss` (see the limitation in the schema below); in `--ports` use `https/mtlss` for `mTLS` plus verification, or `https/mtls` for `mTLS` without it.

The certificates that clients present are recorded per listener, and can be read with `GET /server/listeners/client/certs` (all listeners) or `GET /server/listeners/{port}/client/certs`, and cleared with `POST` on `.../client/certs/clear`.

#### Auto Certs 
- TLS listeners created via startup command arg use auto-generated certs by default, but custom certs can be deployed for these listeners via admin APIs.
- For TLS listeners created via admin APIs (JSON), you can choose to use auto-certs or custom certs.
- Common name of the auto-generated certs can be supplied both for startup listeners as well as via admin APIs. If no common name supplied, the default common name `goto.goto` is used.
- API `/server/listeners/{port}/cert/auto/{domain}?spiffeID={spiffeID}` allows you to convert any existing listener to TLS with auto-cert. The API auto-generates a cert for the listener if not already present, and reopens it in TLS mode. The `spiffeID` query parameter must be present (the URI answers `404` without it), but its value can be empty (`?spiffeID=`). When a value is given, it is added to the certificate as a URI SAN.
- APIs `/server/listeners/{port}/cert/add` and `/server/listeners/{port}/key/add` allow you to add your own cert and key to a listener (PEM, in the request body). After invoking these two APIs to upload cert and private key, you must explicitly call `/server/listeners/{port}/reopen` to make the listener serve TLS traffic.
- `cert/remove` drops the cert and key, turns TLS off for the listener and reopens it, so it serves plain traffic.

> &#x1F4DD; <small><i>Known issues with converting a listener that was added as plain `http`: `cert/auto/{domain}` answers success and reports `"tls": true`, but the socket keeps serving plain-text until the listener is reopened once more, so call `POST /server/listeners/{port}/reopen` after it. `cert/autosni` answers success but a plain listener stays plain (`"tls": false`), however often it is reopened. `cert/autosni` works on a listener that is TLS from the start: add it as `{"protocol":"https","autoSNI":true}`, or call `cert/autosni` on a listener that is already serving TLS.</i></small>


#### Auto SNI
- API `/server/listeners/{port}/cert/autosni` allows you to configure a TLS listener to auto-generate certs on-the-fly to match the SNI presented by the client. The listener behavior in this mode is different than `cert/auto/{domain}` API mode in that:
  - In `auto-cert` mode, the listener auto-generates one cert for the given common name and always uses that one cert regardless of what  SNI the client presents. This leaves open the possibility of a mismatch between the server name requested by the client and the cert presented by the server.
  - In `cert/autosni` mode, the listener always presents correct certs matching the SNI requested by the client. This ensures there will never be a server name mismatch.

> Whether you should use auto-cert mode or auto-SNI mode depends on your testing requirements. `Auto-Cert` mode allows for negative testing with cert mismatch, whereas `Auto-SNI` ensures there will never be a cert mismatch (although still self-signed certs).

> &#x1F4DD; <small><i>Goto maintains a cert cache for auto-sni listeners so that it only generates a cert upon first call for a server name on a listener port, and reuses that cert upon subsequent calls for the same server name</i></small>

> &#x1F4DD; <small> See TCP and gRPC Listeners section later for details of TCP or gRPC features </small>

### Listeners APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  | /server/listeners/add           | Add a listener. [See Listener Payload JSON Schema](#listener-json-schema). If a listener already exists on the port it is restarted with the new config, as for `update`. |
| POST, PUT  | /server/listeners/update        | Update an existing listener (same behaviour as `add`: the payload replaces the listener on that port, which is restarted if it was open). |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/auto/`{domain}`<br/>?spiffeID=`{spiffeID}`   | Auto-generate certificate for the given domain and service on this listener. Listener is automatically reopened as a TLS listener serving this cert. The `spiffeID` query parameter is mandatory, and can be empty. |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/autosni   | The listener will auto-generate a certificate on-the-fly to always match the SNI presented by the client in any arbitrary call. Listener is automatically reopened as a TLS listener after this API call. |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/add   | Add/update certificate (PEM, request body) for a listener. Presence of both cert and key results in the port serving HTTPS traffic when opened/reopened. |
| POST, PUT  | /server/listeners<br/>/`{port}`/key/add   | Add/update private key (PEM, request body) for a listener. Presence of both cert and key results in the port serving HTTPS traffic when opened/reopened. |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/remove   | Remove certificate and key for a listener and reopen it to serve HTTP traffic instead of HTTPS. |
| GET  | /server/listeners/{port}/cert   | Get the certificate currently being used by the given listener (PEM, `application/octet-stream`; an empty body if the listener has none). |
| GET  | /server/listeners/{port}/key   | Get the private key currently being used by the given listener (PEM, `application/octet-stream`; an empty body if the listener has none). |
| POST, PUT  | /server/listeners<br/>/{port}/ca/add   | Add a CA root certificate (PEM, request body) to be used for client mutual TLS on this listener. If mTLS is enabled on a listener, one or more CA certificates must be added for the listener to validate client certificates. The listener is reopened. |
| POST, PUT  | /server/listeners/{port}/ca/clear   | Remove all CA root certificates configured on this listener, and reopen it. |
| GET  | /server/listeners/client/certs<br/>/server/listeners/{port}/client/certs | Get the certificates that clients presented to all listeners, or to one listener (YAML, one entry per port, `{}` when none). |
| POST | /server/listeners/client/certs/clear<br/>/server/listeners/{port}/client/certs/clear | Clear the recorded client certificates of all listeners, or of one listener. Answers `200` with an empty body. |
| POST, PUT  | /server/listeners<br/>/`{port}`/remove | Remove a listener|
| POST, PUT  | /server/listeners<br/>/`{port}`/open   | Open an added listener to accept traffic|
| POST, PUT  | /server/listeners<br/>/`{port}`/reopen | Close and reopen an existing listener if already opened, otherwise open it. (`open` does the same: it reopens a listener that is open.) |
| POST, PUT  | /server/listeners<br/>/`{port}`/close  | Close an added listener|
| GET        | /server/listeners/`{port}`               | Get details of a chosen listener. A port that has no listener answers `200` with an empty body; a `{port}` that is not a number returns the list of all listeners. |
| GET        | /server/listeners               | Get a list of listeners, by port. The list of listeners in the output includes the default startup port even though the default port cannot be mutated by other listener APIs. |
| GET        | /server/listeners/ports         | Get the L8 protocol (`h2c`, `h2`, `tcp`, `udp`, `grpc`, `jsonrpc`, ...) of each listener port, e.g. `{"8080":"h2c","9000":"tcp"}`. |


<br/>
<a name="listener-json-schema"></a>
<details>
<summary>Listener JSON Schema</summary>

The payload of `add`/`update` is a Listener. All fields except `port` are optional. JSON field names are matched case-insensitively (the sample YAML config writes `mtls`, the output writes `mTLS`). The same schema is used in a startup config file, where the `listeners` map is keyed by port and the `port` field is taken from the key.

|Field|Data Type|Description|
|---|---|---|
| listenerID    | string | Read-only field identifying the listener's port and current generation, e.g. `8081-2`. Empty until the listener is opened for the first time. |
| label    | string | Label to be applied to the listener. This can also be set/changed via REST API later. Defaults to `[{ip}:{port}][{pod}@{namespace}@{cluster}]`. |
| hostLabel    | string | The host label is auto-generated and assigned to the listeners to uniquely identify the host while still differentiating between multiple listeners active on the `goto` instance. Its format is `<pod>.<namespace>[<podIP>](<node>[<hostIP>]@<cluster>)`. Host Label is also sent back in the `Goto-Host` response header.  |
| port     | int    | Port on which the new listener will listen on. Required, 1-65535 (otherwise `[Invalid port number: N]`). |
| forward  | int    | For a `tcp` listener: the port of another listener of this `goto` instance to which every connection is relayed. 0 means no forwarding. The target is looked up again each time a listener is added or removed. |
| protocol | string | See the [protocols table](#listener-protocols): `http`, `http1`, `https`, `https1`, `httpss`, `grpc`, `grpcs`, `grpcss`, `xds`, `jsonrpc`/`rpc`, `jsonrpcs`/`rpcs`, `tcp`, `tls`, or `udp`. Protocol `tls` implies TCP+TLS and `grpcs` implies gRPC+TLS as opposed to `tcp` and `grpc` being plain-text versions. Unknown values give a `tcp` listener. The output shows the stored (normalised) value. |
| l8Proto | string | Read-only. The L8 protocol derived from `protocol`: `h2c`, `http`, `h2`, `https`, `grpc`, `xds`, `jsonrpc`, `tcp`, `udp`. |
| uds | string | Optional Unix domain socket path. If set, the listener binds this socket instead of TCP `port` (which remains the listener's ID). Not supported for UDP. See [Unix Domain Sockets](../../docs/listeners-example.md#unix-domain-sockets-uds). |  
| alpn | ALPN | Optional ALPN config of a TLS listener, see [ALPN config](#listener-alpn). |
| open | bool | Controls whether the listener should be opened as soon as it's added. Also reflects the listener's current status when queried. It is `false` when left out. |
| autoCert | bool | Controls whether a TLS certificate should be auto-generated for an HTTPS or TLS listener. If enabled, the TLS cert for the listener is generated using the `CommonName` field if configured, or else the cert common name is defaulted to `goto.goto`. Set automatically for a TLS protocol that has no cert. |
| autoSNI | bool | If enabled, the listener generates a certificate on the fly for the SNI that each client presents (see `cert/autosni`). |
| commonName | string | If given, this common name is used to generate self-signed cert for this listener. |
| spiffeID | string | SPIFFE ID to embed in the generated cert (or the one loaded from a SPIFFE workload API). |
| altNames | []string | Subject alternate names for the generated cert. |
| tls | bool | Reports whether the listener serves TLS. It is derived from `protocol` (`https`, `https1`, `httpss`, `grpcs`, `grpcss`, `jsonrpcs`, `tls`) and from the cert APIs: choose a TLS protocol to get TLS, rather than setting this field. |
| mTLS | bool | Controls whether the HTTPS or TLS listener should require clients to present a certificate. Whether the certificate is verified depends on `verifyClientCert`, see [Client certificates](#client-certificates-mutual-tls). CA certs can be added to a listener using API `/server/listeners/{port}/ca/add`. |
| verifyClientCert | bool | Controls whether a client certificate is verified against the listener's CA certs. Set by protocols `httpss` and `grpcss`. **Known limitation:** for the other HTTP and gRPC protocols (`http`, `https`, `grpcs`, ...) the value in the payload is overwritten with `false` when the listener is added, so use `httpss`/`grpcss` (with `mTLS: true`) to get verified client certificates. The value is kept for `tcp` and `tls` listeners. |
| tcp | TCPConfig | Supplemental TCP config for a TCP listener. See TCP Config JSON schema under `TCP Server` section. **Known limitation:** a `tcp` block inside the listener payload (also in a startup config file) is not applied to the listener, and the listener then resets its connections. Add the listener without the block and call `POST /server/tcp/{port}/configure` instead. |
| isHTTP, isH2, isGRPC, isJSONRPC, isTCP, isUDP, isXDS | bool | Read-only flags derived from `protocol`. |
| generation | int | Read-only. Counts the times the listener has been opened or reopened (0 if never opened). |
| serverCert | object | Read-only. For a TLS listener, the subject, DNS names, issuer and start time of the certificate that it serves (`null` for a plain listener). |

</details>

<a name="listener-alpn"></a>
<details>
<summary>ALPN config</summary>

The `alpn` field of a TLS listener controls which application protocols the listener negotiates, and can optionally run a scripted exchange of bytes ("handshake") on a TCP connection once a protocol has been negotiated.

|Field|Data Type|Description|
|---|---|---|
| protos | []string | ALPN protocol names offered in addition to `h2` (which an HTTP/2 listener always offers). |
| keepDefault | bool | Used by `goto` as a client (transport) only; it has no effect on a listener. |
| handshake.protos | []string | The handshake runs only if the negotiated protocol is in this list. If empty, it runs for any protocol. |
| handshake.seq | []step | The steps, run in order. A step has either a `read` or a `write`. |
| read.len | uint | Number of bytes to read in this step. |
| read.nextSize | bool | The first 4 bytes read are a big-endian unsigned integer that is the number of bytes to read in the next `read` step (`len` must be at least 4). |
| read.collect | bool | Keep the bytes that were read in this step (the "preamble" data). |
| write.preamble | string | Bytes to write, hex-encoded. |

Sample (from [sample-listeners-config.yaml](listeners/sample-listeners-config.yaml)):

```yaml
8000:
  protocol: tcp
  open: true
  forward: 8443
  alpn:
    handshake:
      protos: ["istio-peer-exchange"]
      seq:
      - read: {len: 8}
      - read: {len: 4, nextSize: true}
      - read: {collect: true}
      - write: {preamble: "a16e3c2e54424d45000000140a120a044e414d45120a0a08746573742d706f64"}
```

</details>

<br/>
<details>
<summary>Listener API Examples</summary>

Minimal payload (an HTTP listener that is added but not opened; opening it is a separate call):

```
curl -X POST localhost:8080/server/listeners/add --data '{"port":8081}'
# Listener 8081 added.
curl -X POST localhost:8080/server/listeners/8081/open
# Listener opened on port 8081
```

Rich payload (an HTTPS listener with an auto-generated cert for `foo.com` with extra SANs, client certificates required and verified, and ALPN protocols):

```
curl -X POST localhost:8080/server/listeners/add --data '{
  "port": 8443, "protocol": "https", "label": "https-8443", "open": true,
  "autoCert": true, "commonName": "foo.com", "altNames": ["a.foo.com", "b.foo.com"],
  "mTLS": true, "verifyClientCert": true,
  "alpn": {"protos": ["istio", "istio-h2"]}
}'
# Listener 8443 added and opened.
curl -X POST localhost:8080/server/listeners/8443/ca/add --data-binary @ca.pem
# CA Cert added for listener 8443, and reopened
```

Certificate APIs:

```
curl -X POST 'localhost:8080/server/listeners/8443/cert/auto/bar.com?spiffeID='
# Cert auto-generated for listener 8443
curl -X POST localhost:8080/server/listeners/8443/cert/autosni
# Listener [8443] configured to auto-generate cert for any SNI (on a listener that already serves TLS)
curl -X POST localhost:8080/server/listeners/8443/cert/add --data-binary @cert.pem
# Cert added for listener 8443
curl -X POST localhost:8080/server/listeners/8443/key/add --data-binary @key.pem
# Key added for listener 8443
curl -X POST localhost:8080/server/listeners/8443/reopen
# TLS Listener reopened on port 8443
curl localhost:8080/server/listeners/8443/cert
curl -X POST localhost:8080/server/listeners/8443/cert/remove
# Cert and Key removed for listener 8443, and reopened
curl localhost:8080/server/listeners/client/certs
```

Other calls:

```
curl localhost:8080/server/listeners/8443
curl localhost:8080/server/listeners/ports
curl -X POST localhost:8080/server/listeners/8443/close
# Listener on port 8443 closed
curl -X POST localhost:8080/server/listeners/8443/remove
# Listener on port 8443 removed
```

</details>

<br/>
<details>
<summary>Listener API responses</summary>

Responses are plain text, one line.

|API|Status|Body|
|---|---|---|
| add, update | 200 | `Listener {port} added.` (not opened) / `Listener {port} added and opened.` / `Listener {port} already present, restarted.` / `Listener {port} already present, failed to restart.` / `Listener {port} added but failed to open.` (the last two are still status 200) |
| add, update | 400 | `[Invalid port number: {port}]` (port missing, 0 or above 65535) / `Failed to parse json with error: {error}` (invalid JSON or an empty body) |
| open, reopen | 200 | `Listener opened on port {port}` / `TLS Listener opened on port {port}` / `Listener reopened on port {port}` / `TLS Listener reopened on port {port}` |
| open, reopen | 500 | `Failed to listen on port {port}` (the port could not be bound) |
| close | 200 / 400 | `Listener on port {port} closed` / `Port {port} not open` |
| remove | 200 | `Listener on port {port} removed` |
| any API with `{port}` | 400 | `Port {port}: No listener on the port, or listener not closeable` (no listener was added on the port) |
| cert/auto | 200 / 400 / 500 | `Cert auto-generated for listener {port}` / `Missing domain for cert auto-generation for listener {port}` / `Failed to auto-generate cert for listener {port}` (or `Failed to reopen listener {port} for auto-generate cert`, with status 200) |
| cert/autosni | 200 | `Listener [{port}] configured to auto-generate cert for any SNI` |
| cert/add, key/add | 200 / 400 | `Cert added for listener {port}` / `Key added for listener {port}` / `No payload` (empty body) |
| cert/remove | 200 / 500 | `Cert and Key removed for listener {port}, and reopened` / `Listener [{port}] cert removal failed with error {error}` |
| ca/add | 200 / 400 / 500 | `CA Cert added for listener {port}, and reopened` / `No payload` / `CA Cert added for listener {port} but failed to reopen` |
| ca/clear | 200 / 500 | `CA Certs cleared for listener {port}, and reopened` / `CA Certs cleared for listener {port} but failed to reopen` |
| GET /server/listeners | 200 | JSON, a map of port to Listener (see [Listeners Example](../../docs/listeners-example.md)) |
| GET /server/listeners/{port} | 200 | JSON Listener; empty body when the port has no listener |
| GET /server/listeners/ports | 200 | JSON map of port to L8 protocol |

</details>

<br/>

#### Behaviour notes
- `add` and `update` are the same operation. The listener in the payload is built from scratch (fields that you leave out take their defaults, they are not merged with the old listener) and replaces the one on that port; if the port had a listener, it is restarted, and its `generation` goes up.
- A restart or open retries 5 times, 5 seconds apart, when the port is still in use, so an API call that cannot get the port blocks for up to 25 seconds before it answers.
- `open: true` in an `add` payload opens the listener immediately. A listener that is added with `open` false (or left out) only gets a `listenerID` and `generation` once `open` (or `reopen`) is called.
- Cert and key uploaded with `cert/add` and `key/add` are kept, but not served until the listener is reopened. `cert/auto`, `cert/autosni`, `cert/remove`, `ca/add` and `ca/clear` reopen the listener themselves.
- UDP listeners cannot be closed or removed once opened (`close` answers `400 Port {port} not open`, and `remove` leaves the socket bound); this is a known issue.
- A TLS listener also answers plain-text clients on the same port (checked with `curl http://` against an `https` listener).

See [Listeners Example](../../docs/listeners-example.md)



# <a name="listener-label"></a>

## Listener Label

By default, each listener adds a header `Via-Goto: <port>` to each response it sends, where `<port>` is the port on which the listener is running (default being 8080). A custom label can be added to a listener using the label APIs described below. In addition to `Via-Goto`, each listener also adds another header `Goto-Host` that carries the pod/host name, pod namespace (or `local` if not running as a K8s pod), and pod/host IP address to identify where the response came from.

#### Listener Label APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /server/label/set/`{label}`  | Set label for this port |
| POST      | /server/label/clear        | Remove label for this port (a `PUT` is answered with `404`) |
| GET       | /server/label              | Get current label of this port |

<details>
<summary>Listener Label API Examples</summary>

```
curl -X PUT localhost:8080/server/label/set/Server-8080

curl -X POST localhost:8080/server/label/clear

curl localhost:8080/server/label
```

Responses (status 200):

```
$ curl -X POST localhost:8080/server/label/set/Server-8080
Will use label Server-8080 for all responses on port 8080
$ curl localhost:8080/server/label
Server Label: Server-8080
$ curl -X POST localhost:8080/server/label/clear
Port [8080] Label Cleared
```

> Known issue: on the bootstrap port of a fresh instance, `label/clear` answers as above but `GET /server/label` still shows the label that was set; the other listeners go back to their default label.

</details>


# <a name="echo-api"></a>
# Echo API
This URI echoes back the headers and payload sent by the client. The responses carry the usual `Goto-*` headers. A forced response status (`/response/status/set`) does not apply to the echo URIs: they keep answering `200` (checked with a forced `503` on the port).

#### API
|METHOD|URI|Description|
|---|---|---|
| ALL       |	/echo         | Responds by echoing request headers and some additional request details (JSON) |
| ALL       |	/echo/body    | Echoes request body |
| ALL       |	/echo/headers | Responds by echoing request headers in response payload (JSON, with the `RequestHeaders` and a `Goto-Info` block) |
| ALL       |	/echo/stream  | Streams the request body back line by line, flushing after each line, with the request headers echoed back as `Request-*` response headers. Meant for HTTP/2 (or HTTP/1.1 chunked) requests. |
| GET (websocket upgrade) |	/echo/ws      | Upgrades to a websocket, sends the request headers as the first message, and then streams every message the client sends back to it. |

<br/>
<details>
<summary>Echo API Example</summary>

```
curl -I  localhost:8080/echo

curl -X POST 'localhost:8080/echo?x=1' -d hello
{
  "Goto-Client-Cert": "",
  "Goto-Host": "pod.ns[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)",
  "Goto-Listener": "Server-8080",
  "Goto-Port": 8080,
  "Goto-Remote-Address": "[::1]:55521",
  "Goto-Server-Cert": "",
  "Goto-TLS": false,
  "Goto-mTLS": false,
  "Request-Host": "localhost:8080",
  "Request-Method": "POST",
  "Request-PayloadSize": 0,
  "Request-Protocol": "HTTP/1.1",
  "Request-Query": "x=1",
  "Request-URI": "/echo?x=1",
  "Response-PayloadSize": 0,
  "Via-Goto": "Server-8080",
  "headers": {
    "Request-Accept": ["*/*"],
    "Request-Content-Length": ["5"],
    "Request-Content-Type": ["application/x-www-form-urlencoded"],
    "Request-User-Agent": ["curl/8.7.1"]
  }
}

curl -X POST localhost:8080/echo/body -d 'hello body'
hello body

curl -X PUT localhost:8080/echo/stream --data-binary $'line1\nline2\n'
line1
line2
```

> Known issue: `Request-PayloadSize` is 0 in the echo response even when the request has a body, because the request body size is recorded by a middleware that is not running for HTTP requests.
</details>

###### <small> [Back to TOC](#toc) </small>

# <a name="udp-apis"></a>
## UDP APIs

A UDP listener (`"protocol":"udp"`, added through the listener API or a startup config file; `--ports 5353/udp` does not open one) can proxy datagrams to an upstream, optionally with a delay. The APIs below are served by any HTTP listener. See the [UDP proxy](../proxy/udp/README.md) for details of what the proxy does with the traffic.

|METHOD|URI|Description|
|---|---|---|
| POST | /server/udp/`{port}`/proxy/`{upstream}` | Start proxying UDP datagrams received on `{port}` to `{upstream}` (`host:port`). A UDP listener is opened on the port if there is none. |
| POST | /server/udp/`{port}`/proxy/`{upstream}`<br/>/delay/`{delay}` | Same, and also delays the datagrams by `{delay}`: a duration (`50ms`) or a range `{min}-{max}` (`100ms-200ms`). A delay that cannot be parsed is taken as no delay. |
| POST | /server/udp/`{port}`/delay/`{upstream}`/`{delay}` | Change the delay of the proxy of an upstream. |
| POST | /server/udp/`{port}`/stop/`{upstream}` | Stop proxying `{port}` to `{upstream}`. |

|Status|Body|
|---|---|
| 200 | `Proxying UDP on port [20065] to upstream [localhost:20099] with delay [0s-0s]` |
| 200 | `Delay configured for UDP port [20065]` |
| 200 | `Stopped Proxying UDP on port [20065] to upstream [localhost:20099]` (also for an upstream that was not being proxied) |
| 400 | `Port 20067 is not a UDP listener` / `No listener for port 20099` (delay and stop, when the port has a listener of another kind, or none) |
| 400 | `Invalid delay value [xyz]` (delay) |
| 400 | `Invalid port [0] or upstream address []` / `Failed to open UDP listener on port [N] with error: ...` (proxy) |

<details>
<summary>UDP API Examples</summary>

```
curl -X POST localhost:8080/server/udp/5353/proxy/10.0.0.8:53
curl -X POST localhost:8080/server/udp/5353/proxy/10.0.0.8:53/delay/100ms-200ms
curl -X POST localhost:8080/server/udp/5353/delay/10.0.0.8:53/50ms
curl -X POST localhost:8080/server/udp/5353/stop/10.0.0.8:53
```
</details>

> A UDP listener cannot be closed or removed through the listener APIs once it is open (see the limitation in the listener behaviour notes).

###### <small> [Back to TOC](#toc) </small>

# <a name="goto-info-apis"></a>
## Server Information APIs (`/goto`)

|METHOD|URI|Description|
|---|---|---|
| GET | /goto/version | Version and commit of the running `goto`. |
| GET | /goto/routes<br/>/goto/apis | The routes that this `goto` serves, as a tree: root path, then sub-root, then sub-path, each with its methods. `routes` and `apis` give the same output. |
| GET | /goto/{routes\|apis}/level=`{level}` | The same tree cut to `level` 1 (root paths), 2 (sub-roots) or 3 (sub-paths). |
| GET | /goto/{routes\|apis}?q=`{q}` | Only the routes whose root, sub-root or path contains `{q}`. |
| GET | /goto/{routes\|apis}?url=`{url}`[&q=`{q}`] | The routes as ready-to-run `curl` commands against `{url}`. |
| GET | /goto/{routes\|apis}/refresh | Rebuild the list of routes from the router, then report it. The list is built on the first call and cached after that. |

The output is JSON by default; send `Accept: application/yaml` for YAML, or `Accept: text/plain` for a sorted plain list (`/probes [GET]`).

<details>
<summary>Server Information API Examples</summary>

```
$ curl localhost:8080/goto/version
{
  "commit": "",
  "version": "0.9.20"
}

$ curl -H "Accept: text/plain" 'localhost:8080/goto/routes?q=probes'
/probes [GET]
/probes/counts/clear [POST]
/probes/{type}/set/status={status} [PUT POST]
/probes/{type}/set?uri={uri} [PUT POST]
/registry/peers/probes [GET]
/registry/peers/probes/{type}/set/status={status} [POST PUT]
/registry/peers/probes/{type}/set?uri={uri} [POST PUT]

$ curl localhost:8080/goto/routes/level=1
{
  "/a2a": {},
  "/agent": {},
  "/ai": {},
  ...
  "/ui": {}
}

$ curl 'localhost:8080/goto/apis?q=probes&url=localhost:8080'
{
  "/probes": {
    "": {
      "": [ { "/probes": "[curl -v -XGET localhost:8080/probes]" } ]
    },
    ...
```
</details>

> Routes that are registered without a method (the echo APIs, the label GET) do not appear in this list.

###### <small> [Back to TOC](#toc) </small>

# <a name="hooks-api"></a>
## Hooks (`/hooks`)

Hooks are how a feature of `goto` (the request and header tracking, and the JSON-RPC (MCP and A2A) call tracker) gets to see the requests and the gRPC streams of a port without owning a route. They are registered from Go code, not through REST, so there is no API to add one. The only REST API is:

|METHOD|URI|Description|
|---|---|---|
| GET | /hooks | Lists the ports that have hooks, as YAML. |

```
$ curl localhost:8080/hooks
"8080": {}
"3000": {}
```

> Known limitation: the details of the hooks (URIs, ids, matches) are not exported, so each port shows as an empty object; the API tells which ports have a hook table, nothing more.

###### <small> [Back to TOC](#toc) </small>

# <a name="ui-api"></a>
## UI (`/ui`)

|METHOD|URI|Description|
|---|---|---|
| GET | /ui | A web terminal page (xterm.js) that connects to `/ui/ws` and prints what the server broadcasts there: the requests and responses that the listener handles, live. What you type in it is sent over the socket. |
| GET | /ui/api | The `goto API Console` page, for trying out the `goto` APIs from the browser. |
| GET | /ui/ws | Websocket endpoint for the page. Needs a websocket upgrade request (a plain GET answers `400 Bad Request`). While at least one websocket is connected, every request that the listener handles, with its headers, status, response headers and response body, is broadcast to the connected sockets. |

The pages are embedded in the binary; the terminal page loads its `xterm` scripts from public CDNs, so the browser needs internet access.

###### <small> [Back to TOC](#toc) </small>

# <a name="catch-all"></a>
## Catch-all

A request to any URI that no API or feature handles (any method) gets the echo response (see the Echo API below) with two more fields, `At` (local time of the response) and `Goto-Port` (the port that served the request). The response is JSON, or YAML when the request has `Accept: application/yaml`. These requests are counted under the request type `catchAll` in the [metrics](../metrics/README.md).

###### <small> [Back to TOC](#toc) </small>

# Server Package REST APIs

This package provides core server functionality and various REST APIs for server management, request/response handling, and configuration. This is the index of the APIs of the server package; the details of each are in the sections above, or in the linked docs.

## Server Information APIs
<a name="get-version"></a><a name="get-routesapis"></a>
- **GET** `/goto/version`
- **GET** `/goto/routes`, `/goto/apis` (also `/goto/{routes|apis}/level={level}`, `/goto/{routes|apis}/refresh`, `?q={q}`, `?q={q}&url={url}`)
- **GET** `/hooks`
- **GET** `/ui`, `/ui/api`, `/ui/ws`

## Listener Management APIs
<a name="addupdate-listener"></a><a name="certificate-management"></a><a name="ca-certificate-management"></a><a name="listener-control"></a><a name="get-listeners"></a>
Both `/server/listeners` and `/server/listener` are accepted.

- **POST/PUT** `/server/listeners/add`
- **POST/PUT** `/server/listeners/update`
- **PUT/POST** `/server/listeners/{port}/cert/auto/{domain}?spiffeID={spiffeID}`
- **PUT/POST** `/server/listeners/{port}/cert/autosni`
- **PUT/POST** `/server/listeners/{port}/cert/add`
- **PUT/POST** `/server/listeners/{port}/key/add`
- **PUT/POST** `/server/listeners/{port}/cert/remove`
- **GET** `/server/listeners/{port}/cert`
- **GET** `/server/listeners/{port}/key`
- **PUT/POST** `/server/listeners/{port}/ca/add`
- **PUT/POST** `/server/listeners/{port}/ca/clear`
- **GET** `/server/listeners/client/certs`, `/server/listeners/{port}/client/certs`
- **POST** `/server/listeners/client/certs/clear`, `/server/listeners/{port}/client/certs/clear`
- **PUT/POST** `/server/listeners/{port}/remove`
- **PUT/POST** `/server/listeners/{port}/open`
- **PUT/POST** `/server/listeners/{port}/reopen`
- **PUT/POST** `/server/listeners/{port}/close`
- **GET** `/server/listeners`, `/server/listeners/{port}`, `/server/listeners/ports`
- **PUT/POST** `/server/label/set/{label}`, **POST** `/server/label/clear`, **GET** `/server/label`

## TCP and UDP Server APIs
- `/server/tcp/...`: see the [TCP Server](tcp/README.md)
- **POST** `/server/udp/{port}/proxy/{upstream}`, `/server/udp/{port}/proxy/{upstream}/delay/{delay}`, `/server/udp/{port}/delay/{upstream}/{delay}`, `/server/udp/{port}/stop/{upstream}`

## Response Payload and Stream APIs
<a name="response-payload-apis"></a><a name="set-response-payload"></a><a name="payload-transform"></a><a name="clear-payload"></a><a name="get-payload"></a><a name="dynamic-payload-endpoints"></a>
These are served under `/response/payload/...` (for example `POST /response/payload/set/default/{size}`, `GET /response/payload`) and `/stream` (query form only, for example `GET /stream?duration={duration}&delay={delay}&chunksize={chunksize}` or `GET /stream?count={count}&delay={delay}`; there are no `/stream/count=...` path forms). See [Response Payload](response/payload/README.md) and [Stream](stream/README.md) for the full lists.

## Probe Management APIs
<a name="set-probes"></a><a name="clear-probe-counts"></a><a name="get-probes"></a>
- **PUT/POST** `/probes/{type}/set?uri={uri}`
- **PUT/POST** `/probes/{type}/set/status={status}`
- **POST** `/probes/counts/clear`
- **GET** `/probes`

See [Probes](probes/README.md).

## Log, Metrics and Stats APIs
- `/log/...`: see the [log package](../log/README.md)
- **GET** `/metrics`, `/metrics/go`, **POST** `/metrics/clear`, **GET** `/stats`, **POST** `/stats/clear`: see [Metrics](../metrics/README.md)

## Notes
- `{port}` - Listener port number
- `{type}` - Probe type: "readiness" or "liveness"
- `{count}` - Number of streaming chunks
- `{delay}` - A duration such as `100ms` or `2s` (for the payload and stream APIs; see their docs)
- `{size}` - Payload size in bytes
- Most endpoints support port-specific operations via the `/port={port}/...` URI prefix
- Payload data should be sent in the request body for set operations
