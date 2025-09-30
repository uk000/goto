
# Goto Server Features

`Goto` as a server is useful for doing feature testing as well as chaos testing of client applications, proxies/sidecars, gateways, etc. Or, the server can also be used as a proxy to be put in between a client and a target server application, so that traffic flows through this server where headers can be inspected/tracked before forwarding the requests further. The server can add headers, replace request URI with some other URI, add artificial delays to the response, respond with a specific status, monitor request/connection timeouts, etc. The server tracks all the configured parameters, applying those to runtime traffic and building metrics, which can be viewed via various APIs.

###### <small> [Back to TOC](#toc) </small>

# <a name="goto-response-headers)"></a>
## HTTP Headers
#### Server response headers

`Goto` adds the following common response headers to all http responses it sends:

- `Goto-Host`: identifies the `goto` instance. This header's value will include hostname, IP, Port, Namespace and Cluster information if available to `Goto` from the following Environment variables: `POD_NAME`, `POD_IP`, `NODE_NAME`, `CLUSTER`, `NAMESPACE`. It falls back to using the local compute's IP address if `POD_IP` is not defined. For other fields, it defaults to fixed value `local`.
- `Via-Goto`: carries the label of the listener that served the request. For the bootstrap port, the label used is the one given to `goto` as `--label` startup argument (defaults to auto-generated label).
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
- `Proxy-Goto-Host`: identifies the `goto` instance that acted as a proxy for this request
- `Proxy-Via-Goto`: label of the `goto` instance that acted as a proxy for this request
- `Proxy-Goto-Port`: port number on which the `goto` proxy instance received this request, which may be different than the upstream service port
- `Proxy-Goto-Protocol`: Protocol over which the `goto` proxy instance served this request, which may be different than the protocol used for the upstream service call
- `Proxy-Goto-Response-Status`: HTTP response status code that `goto` proxy instance responded with. Upstream's response status code is preserved in another header listed below.
- `Proxy-Goto-In-At`: UTC timestamp when the request was received by the `goto` proxy instance
- `Proxy-Goto-Out-At`: UTC timestamp when the `goto` proxy instance finished processing the request and sent a response
- `Proxy-Goto-Took`: Total processing time taken by the `goto` proxy instance to process the request
- `Goto-Proxy-Upstream-Status_<upstream-name>`: status the `goto` proxy's upstream service responded with, which may be different than the status `goto` responded with to the downstream client
- `Proxy-Goto-Upstream-Took_<upstream-name>`: Total roundtrip time the proxy's upstream call took
- `Proxy-Goto-Delay`: any delay added to the request/response by `goto` as a proxy
- `Proxy-Goto-Request-Dropped`: sent back with response when the proxy drops the downstream request based on the drop percentage defined for the upstream target (see proxy feature)
- `Proxy-Goto-Response-Dropped`: sent back with response when the proxy drops the upstream response based on the drop percentage defined for the upstream target (see proxy feature)

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
- The server starts with a bootstrap http listener When startup arg `--ports` is used, the first port in the list is treated as the bootstrap port, forced to be an HTTP port, and isn't allowed to be managed via listeners APIs.
- The `listeners APIs` let you manage/open/close an arbitrary number of HTTP/HTTPS/TCP/TCP+TLS/gRPC listeners (except the default bootstrap listener that is set as HTTP and cannot be modified). 
- All HTTP listener ports respond to the same set of API calls, so any of the admin APIs described in this document as well as any arbitrary URI call for runtime traffic can be done via any active HTTP listener. 
- Any of the TCP operations described in the TCP section can be performed on any active TCP listener, and any of the gRPC operations can be performed on any gRPC listener. 
- The HTTP listeners perform double duty of also acting as gRPC listeners, but listeners explicitly configured as `gRPC` act as `gRPC-only` and don't support HTTP operations. See `gRPC` section later in this doc for details on gRPC operations supported by `goto`.
- `/server/listeners` API lets you view a list of configured listeners, including the default bootstrap listener.

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

#### Auto Certs 
- TLS listeners created via startup command arg use auto-generated certs by default, but custom certs can be deployed for these listeners via admin APIs.
- For TLS listeners created via admin APIs (JSON), you can choose to use auto-certs or custom certs.
- Common name of the auto-generated certs can be supplied both for startup listeners as well as via admin APIs. If no common name supplied, the default common name `goto.goto` is used.
- API `/server/listeners/{port}/cert/auto/{domain}` allows you to convert any existing listener to TLS with auto-cert. The API auto-generates a cert for the listener if not already present, and reopens it in TLS mode.
- APIs `/server/listeners/{port}/cert/add` and `/server/listeners/{port}/key/add` allow you to add your own cert and key to a listener. After invoking these two APIs to upload cert and private key, you must explicitly call `/server/listeners/{port}/reopen` to make the listener serve TLS traffic.


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
| POST       | /server/listeners/add           | Add a listener. [See Listener Payload JSON Schema](#listener-json-schema)|
| POST       | /server/listeners/update        | Update an existing listener.|
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/auto/`{domain}`   | Auto-generate certificate for the given domain and service on this listener. Listener is automatically reopened as a TLS listener serving this cert. |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/autosni   | The listener will auto-generate a certificate on-the-fly to always match the SNI presented by the client in any arbitrary call. Listener is automatically reopened as a TLS listener after this API call. |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/add   | Add/update certificate for a listener. Presence of both cert and key results in the port serving HTTPS traffic when opened/reopened. |
| POST, PUT  | /server/listeners<br/>/`{port}`/key/add   | Add/update private key for a listener. Presence of both cert and key results in the port serving HTTPS traffic when opened/reopened. |
| POST, PUT  | /server/listeners<br/>/`{port}`/cert/remove   | Remove certificate and key for a listener and reopen it to serve HTTP traffic instead of HTTPS. |
| GET  | /server/listeners/{port}/cert   | Get the certificate currently being used by the given listener. |
| GET  | /server/listeners/{port}/key   | Get the private key currently being used by the given listener. |
| POST, PUT  | /server/listeners<br/>/{port}/ca/add   | Add a CA root certificate to be used for client mutual TLS on this listener. If mTLS is enabled on a listener, one or more CA certificates must be added for the listener to validate client certificates. |
| POST, PUT  | /server/listeners/{port}/ca/clear   | Remove all CA root certificates configured on this listener. |
| POST, PUT  | /server/listeners<br/>/`{port}`/remove | Remove a listener|
| POST, PUT  | /server/listeners<br/>/`{port}`/open   | Open an added listener to accept traffic|
| POST, PUT  | /server/listeners<br/>/`{port}`/reopen | Close and reopen an existing listener if already opened, otherwise open it |
| POST, PUT  | /server/listeners<br/>/`{port}`/close  | Close an added listener|
| GET        | /server/listeners/`{port}`               | Get details of a chosen listener. |
| GET        | /server/listeners               | Get a list of listeners. The list of listeners in the output includes the default startup port even though the default port cannot be mutated by other listener APIs. |


<br/>
<details>
<summary>Listener JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| listenerID    | string | Read-only field identifying the listener's port and current generation. |
| label    | string | Label to be applied to the listener. This can also be set/changed via REST API later. |
| hostLabel    | string | The host label is auto-generated and assigned to the listeners to uniquely identify the host while still differentiating between multiple listeners active on the `goto` instance. This is auto-generated using format `<hostname>@<ipaddress>:<port>`. Host Label is also sent back in the `Goto-Host` response header.  |
| port     | int    | Port on which the new listener will listen on. |
| protocol | string | `http`, `http1`, `https`, `https1`, `grpc`, `grpcs`, `tcp`, or `tls`. Protocol `tls` implies TCP+TLS and `grpcs` implies gRPC+TLS as opposed to `tcp` and `grpc` being plain-text versions. |
| open | bool | Controls whether the listener should be opened as soon as it's added. Also reflects the listener's current status when queried. |
| autoCert | bool | Controls whether a TLS certificate should be auto-generated for an HTTPS or TLS listener. If enabled, the TLS cert for the listener is generated using the `CommonName` field if configured, or else the cert common name is defaulted to `goto.goto`. |
| commonName | string | If given, this common name is used to generate self-signed cert for this listener. |
| mutualTLS | bool | Controls whether the HTTPS or TLS listener should enforce mutual-TLS, requiring clients to present a valid certificate that's validated against the configured CA certs of the listener. CA certs can be added to a listener using API `/server/listeners/{port}/ca/add`). |
| tls | bool | Reports whether the listener has been configured for TLS (read-only). |
| tcp | TCPConfig | Supplemental TCP config for a TCP listener. See TCP Config JSON schema under `TCP Server` section. |

</details>

<details>
<summary>Listener Events</summary>

- `Listener Rejected`
- `Listener Added`
- `Listener Updated`
- `Listener Removed`
- `Listener Cert Added`
- `Listener Key Added`
- `Listener Cert Removed`
- `Listener Cert Generated`
- `Listener Label Updated`
- `Listener Opened`
- `Listener Reopened`
- `Listener Closed`
- `gRPC Listener Started`
</details>

See [Listeners Example](listeners-example.md)



# <a name="listener-label"></a>

## Listener Label

By default, each listener adds a header `Via-Goto: <port>` to each response it sends, where `<port>` is the port on which the listener is running (default being 8080). A custom label can be added to a listener using the label APIs described below. In addition to `Via-Goto`, each listener also adds another header `Goto-Host` that carries the pod/host name, pod namespace (or `local` if not running as a K8s pod), and pod/host IP address to identify where the response came from.

#### Listener Label APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /server/label/set/`{label}`  | Set label for this port |
| PUT       | /server/label/clear        | Remove label for this port |
| GET       | /server/label              | Get current label of this port |

<details>
<summary>Listener Label API Examples</summary>

```
curl -X PUT localhost:8080/server/label/set/Server-8080

curl -X PUT localhost:8080/server/label/clear

curl localhost:8080/server/label
```

</details>


# Echo API
This URI echoes back the headers and payload sent by the client. The response is also subject to any forced response status and will carry custom headers if any are configured.

#### API
|METHOD|URI|Description|
|---|---|---|
| ALL       |	/echo         | Responds by echoing request headers and some additional request details |
| ALL       |	/echo/body    | Echoes request body |
| ALL       |	/echo/headers | Responds by echoing request headers in response payload |
| PUT, POST |	/echo/stream  | For http/2 requests, this API streams the request body back as response body. For http/1, it acts similar to `/echo` API. |
| PUT, POST |	/echo/ws      | Stream the request payload back over a websocket. |

<br/>
<details>
<summary>Echo API Example</summary>

```
curl -I  localhost:8080/echo
```
</details>







# Server Package REST APIs

This package provides core server functionality and various REST APIs for server management, request/response handling, and configuration.

## Server Information APIs

### Get Version
- **GET** `/version`

### Get Routes/APIs
- **GET** `/routes`
- **GET** `/apis`
- **GET** `/routes/port`
- **GET** `/apis/port`

## Listener Management APIs

### Add/Update Listener
- **POST/PUT** `/server/listeners/add`
- **POST/PUT** `/server/listeners/update`

### Certificate Management
- **PUT/POST** `/server/listeners/{port}/cert/auto/{domain}`
- **PUT/POST** `/server/listeners/{port}/cert/autosni`
- **PUT/POST** `/server/listeners/{port}/cert/add`
- **PUT/POST** `/server/listeners/{port}/key/add`
- **PUT/POST** `/server/listeners/{port}/cert/remove`
- **GET** `/server/listeners/{port}/cert`
- **GET** `/server/listeners/{port}/key`

### CA Certificate Management
- **PUT/POST** `/server/listeners/{port}/ca/add`
- **PUT/POST** `/server/listeners/{port}/ca/clear`

### Listener Control
- **PUT/POST** `/server/listeners/{port}/remove`
- **PUT/POST** `/server/listeners/{port}/open`
- **PUT/POST** `/server/listeners/{port}/reopen`
- **PUT/POST** `/server/listeners/{port}/close`

### Get Listeners
- **GET** `/server/listeners/{port}?`
- **GET** `/server/listeners/ports`

## Response Payload APIs

### Set Response Payload
- **POST** `/payload/set/stream/count={count}/delay={delay}?uri={uri}`
- **POST** `/payload/set/stream/count={count}/delay={delay}/header/{header}?uri={uri}`
- **POST** `/payload/set/default/binary/{size}`
- **POST** `/payload/set/default/binary`
- **POST** `/payload/set/default/{size}`
- **POST** `/payload/set/default`
- **POST** `/payload/set/uri?uri={uri}`
- **POST** `/payload/set/header/{header}={value}?uri={uri}`
- **POST** `/payload/set/header/{header}?uri={uri}`
- **POST** `/payload/set/query/{q}={value}?uri={uri}`
- **POST** `/payload/set/query/{q}?uri={uri}`
- **POST** `/payload/set/body~{regexes}?uri={uri}`
- **POST** `/payload/set/body/paths/{paths}?uri={uri}`

### Payload Transform
- **POST** `/payload/transform?uri={uri}`

### Clear Payload
- **POST** `/payload/clear`

### Get Payload
- **GET** `/payload`

### Dynamic Payload Endpoints
- **GET/PUT/POST** `/payload/{size}`
- **GET/PUT/POST** `/stream/payload={payloadSize}/duration={duration}/delay={delay}`
- **GET/PUT/POST** `/stream/chunksize={chunkSize}/duration={duration}/delay={delay}`
- **GET/PUT/POST** `/stream/chunksize={chunk}/count={count}/delay={delay}`
- **GET/PUT/POST** `/stream/duration={duration}/delay={delay}`
- **GET/PUT/POST** `/stream/count={count}/delay={delay}`

## Probe Management APIs

### Set Probes
- **PUT/POST** `/probes/{type}/set?uri={uri}`
- **PUT/POST** `/probes/{type}/set/status={status}`

### Clear Probe Counts
- **POST** `/probes/counts/clear`

### Get Probes
- **GET** `/probes`

## Notes
- `{port}` - Listener port number
- `{type}` - Probe type: "readiness" or "liveness"
- `{count}` - Number of streaming chunks
- `{delay}` - Delay in milliseconds
- `{size}` - Payload size in bytes
- Most endpoints support port-specific operations via query parameters or headers
- Payload data should be sent in the request body for set operations
