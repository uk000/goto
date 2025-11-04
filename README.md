# goto

> &#x1F4DD;
<small> The Readme reflects master HEAD code and applies to release 0.9.x. Since 0.9.x releases have significant differences than 0.8.x and earlier releases, please refer to the appropriate tag for older documentation.</small>

> &#x1F4DD; <small>Jump to [TOC](#toc) if you'd rather skip the overview and just to specific features and APIs.</small>

## What is `goto`?

#### <i>`"I'm an agent of chaos" - The Joker`</i>

A multi-faceted "No-Code" chaos testing tool to help with automated testing, debugging, bug hunt, runtime analysis and investigations. Mostly when we've a task at hand to test a system, the system to be tested is either a client, a server, or some kind of a gateway/proxy that sits between a client and a server.

To test either of these 3 layers, you need at least one counterparty application:
- To test a client, we need a server to which the client can connect, send requests and get some response back. The server needs to be able to track the lifecycle of the client connections and various requests it received from the client so that the client functionality can be verified.
- To test a server, we need a client that can send some requests to the server and track the responses sent by the server. Again the client should be able to track the lifecycle of connections and requests/responses to be able to verify the server functionality.
- To test an intermediary proxy/gateway, we need both a test client as well as a test server, where the two could route some requests and responses through the intermediary and validate the correctness of the traffic flow.

`Goto` can play all the above roles to fill the missing piece of the puzzle, all based on configs so that you don't have to write any code.

It can act as:
- An [A2A engine](pkg/ai/README.md#a2a-agent-features-server) that can dynamically create "No Code" Agents and Clients. The agents can call other agents (over A2A) or MCP servers, or respond with a custom unary or streaming payload.
- An [MCP engine](pkg/ai/README.md#mcp-admin-apis) that can dynamically create MCP servers and clients. The MCP servers can expose any number of tools, and a tool can expose one of the supported behaviors. Supported behaviors include serving response based on data fetched from a remote HTTP call, a remote MCP call, a remote A2A agent call, or respond with a custom unary or streaming payload.
- A [client](pkg/client/README.md) that can generate HTTP/S, TCP, and gRPC traffic to other services (including other `goto` instances), track summary results of the traffic, and report results via [APIs](pkg/client/README.md#client-apis) as well as publish results to a [Goto registry](pkg/registry/Overview.md). 
- A [server](pkg/server/README.md) that can act as an
  -- HTTP server with arbitrary REST APIs with custom responses.
  -- gRPC server that supports any arbitrary RPC service/methods based on a given set of proto files (or specs extracted from remote reflection)
  -- TCP server that offers a set of TCP behaviors to assist with TCP testing/debugging.
  -- UDP Server that can proxy UDP requests/responses to upstream endpoints.
  --  The server can track and report summary data about the received traffic.
  See the [TOC](#toc) for a complete list of server features. 
- A [proxy](pkg/proxy/README.md) that can act as an HTTP/S, TCP, UDP, gRPC, or MCP passthrough proxy, allowing you to route traffic through a `goto` instance to an upstream server, and inspect the requests/responses.
- A [tunnel](pkg/tunnel/README.md) that allows tunneling of HTTP/S and TCP traffic across multiple hops. This allows testing traffic behavior as it goes through overlay boundaries and through various intermediary proxies/gateways.
- A [job executor](pkg/job/README.md) that can run shell commands/scripts as well as make HTTP calls, collect and report results. It allows chaining of jobs together so that output of one job triggers another job with input. Additionally, jobs can be auto-executed via cron, and can act as a source of data for pipelines (more on this under `pipelines`)
- A [registry](pkg/registry/Overview.md) to orchestrate operations across other `goto` instances, collect and summarize results from those `goto` instances, and make the federated summary results available via APIs. A `goto` registry can also be paired with another `goto` registry instance and export/load data from one to the other to keep another backup of the collected results.
- A [K8s proxy](pkg/k8s/README.md) that can connect to and read resource information from a K8s cluster and make it available via APIs. It also supports watching for resource changes, and act as a source of data for pipelines (see below)
- A complex multi-step [pipeline](pkg/pipe/README.md) orchestration engine that can:
  - Source data from Shell Commands/Scripts, Client-side HTTP calls, Server-side HTTP responses, K8s resources, K8s pod commands, and Tunneled traffic.
  - Feed the sourced data as input to transformation steps and/or to other source steps.
  - Run JSONPath, JQ, Go Template or Regex transformations on the sourced data to extract information that can be fed to other sources or sent as output.
  - Define stages to orchestrate invocation of sources and transformations in a specific sequence.
  - Define watches on sources so that any changes on the source (e.g. K8s resources, cron jobs, or HTTP traffic) triggers the pipeline that the source is attached to, allowing some complex steps of data extraction, transformation and analysis to occur each time some external event occurs.


<br/>

## How to use it?

### Grab or Build
#### Docker images (Alpine Linux based)
  #### core
  - `docker.io/uk0000/goto:0.9.5`, `docker.io/uk0000/goto:0.9.5-arm64`
  - Includes `bash, curl, jq`
  #### net
  - `docker.io/uk0000/goto:0.9.5-net`, `docker.io/uk0000/goto:0.9.5-net-arm64`
  - Includes core pack
  - Includes network utilities like `ncat, nc, nmap, socat, tcpdump, dig, nslookup, iptables, ipvsadm, openssl`
  #### kube
  - `docker.io/uk0000/goto:0.9.5-kube`, `docker.io/uk0000/goto:0.9.5-kube-arm64`
  - Includes core and net packs
  - Includes `kubectl and etcdctl`
  #### perf
  - `docker.io/uk0000/goto:0.9.5-perf`, `docker.io/uk0000/goto:0.9.5-perf-arm64`
  - Includes core and net packs
  - Includes `hey and iftop`
  #### gRPC
  - `docker.io/uk0000/goto:0.9.5-grpc`, `docker.io/uk0000/goto:0.9.5-grpc-arm64`
  - Includes core and net packs
  - Includes `grpcurl`

<br/>
Or, build it locally on your machine

```
go build -o goto .
```

#### <u>Launch</u>
Start `goto` as a server with multiple ports and protocols. 
> &#x1F4DD; <small><i>First port is treated as bootstrap port and uses HTTP protocol</i></small>

  ```
  goto --ports 8080,8081/http,8443/https,6000/grpc,7000/tcp,8000/rpc --rpcPort=3000 --grpcPort=9000
  ```


# Show me ~~the money~~ some use cases please!

Now that you have `goto` running, what can you do with it?
<br/>
Before we look into detailed features and APIs exposed by the tool, let's look at how this tool can be used in a few scenarios to understand it better.

## Basic Scenarios

### Scenario: [Use HTTP client to send requests and track results](docs/scenarios-basic.md#basic-client-usage)

### Scenario: [Use HTTP server to respond to any arbitrary client HTTP requests](docs/scenarios-basic.md#basic-server-usage)

### Scenario: [HTTPS traffic with certificate validation](docs/scenarios-basic.md#basic-https-usage)

### Scenario: [Count number of requests received at each server instance for certain headers](docs/scenarios-basic.md#basic-header-tracking)

## K8S Scenarios

### Scenario: [Run dynamic traffic from K8s pods at startup](docs/scenarios-k8s.md#k8s-traffic-at-startup)

### Scenario: [Deal with transient pods](docs/scenarios-k8s.md#k8s-transient-pods)

### Scenario: [Capture results from pods that may terminate anytime](docs/scenarios-k8s.md#k8s-capture-transient-pod-results)

## Resiliency Scenarios

### Scenario: [Test a client's behavior upon service failure](docs/scenarios-resiliency.md#scenario-test-a-clients-behavior-upon-service-failure)

### Scenario: [Track client hang-ups on server via request/connection timeouts](docs/scenarios-resiliency.md#server-resiliency-client-hangups)

## Outside-the-Box Scenarios

### Scenario: [Create HTTP chaos that HTTP libraries won't let you](docs/http-chaos.md)

<br/>

See [Use Cases](docs/use-cases.md) for more examples.

# <a name="toc"></a>

## TOC

### [Startup Command](#-goto-startup-command)

### Self Reflection
- [Version](#-goto-version)
- [APIs](#-goto-apis)

### AI
- [A2A Agent](pkg/ai/README.md#a2a-agent-features-server)
- [MCP Server and Client](pkg/ai/README.md#mcp-admin-apis)

### Traffic (Client)
- [Goto's HTTP/gRPC/TCP Client Features (To run Traffic)](pkg/client/README.md)
- [Client JSON Schemas](docs/client-api-json-schemas.md)
- [Client APIs and Results Examples](docs/client-api-examples.md)
- [Client gRPC Examples](docs/grpc-client-examples.md)

### gRPC
- [gRPC Server and Client](pkg/rpc/README.md)

### HTTPC Server
- [Server Features](pkg/server/README.md)
- [Goto Headers](pkg/server/README.md#http-headers)
- [Goto Server Logs](pkg/server/README.md#goto-logs)
- [Log APIs](pkg/log/README.md)
- [Events](pkg/events/README.md)
- [Metrics](pkg/metrics/README.md)
- [Listeners](pkg/server/README.md#listeners)
- [Listener Label](pkg/server/README.md#listener-label)
- [Request Headers Tracking](pkg/server/request/README.md#request-headers-tracking)
- [Request Timeout](pkg/server/request/README.md#request-timeout-tracking)
- [URIs](pkg/server/request/README.md#request-uri-tracking)
- [Probes](pkg/server/probes/README.md)
- [Requests Filtering](pkg/server/request/README.md#requests-filtering)
- [Response Delay](pkg/server/response/README.md#response-delay)
- [Response Headers](pkg/server/response/README.md#response-headers)
- [Response Payload](pkg/server/response/README.md#response-payload)
- [Ad-hoc Payload](pkg/server/response/README.md#ad-hoc-payload)
- [Stream (Chunked) Payload](pkg/server/response/README.md#-stream-chunked-payload)
- [Response Status](pkg/server/response/README.md#response-status)
- [Response Triggers](pkg/server/response/README.md#response-triggers)
- [Status API](pkg/server/response/README.md#status)
- [Delay API](pkg/server/response/README.md#delay)
- [Echo API](pkg/server/README.md#echo-api)
- [Catch All](#-catch-all)

### TCP Server
- [TCP Server](pkg/server/tcp/README.md)


### Goto Tunnel
- [Tunnel](pkg/tunnel/README.md)

### Goto Proxy

- [Proxy Features](pkg/proxy/README.md)

### Scripts

- [Scripts Features](pkg/scripts/README.md)

### Jobs

- [Jobs Features](pkg/job/README.md)

### K8s

- [K8s Features](pkg/k8s/README.md)

### Pipelines

- [Pipeline Features](pkg/pipe/README.md)

### Goto Registry

- [Registry Features](pkg/registry/Overview.md)
- [Registry APIs](pkg/registry/README.m)

  <br/>

# <a name="goto-startup-command"></a>

## > Goto Startup Command

To run:

```
goto --ports 8080,8081/http,8443/https,6000/grpc,7000/tcp,8000/rpc --rpcPort=3000 --grpcPort=9000
```

See [Startup Command](cmd/README.md) doc.

###### <small> [Back to TOC](#toc) </small>

# <a name="goto-version"></a>
## > Goto Version

#### APIs
|METHOD|URI|Description|
|---|---|---|
| GET       | /version    | Get version info of this `goto` instance.  |

###### <small> [Back to TOC](#toc) </small>

<br/>

# <a name="goto-apis"></a>
## > Goto APIs
This API returns a list of Goto's admin APIs, grouped by features (prefixes)

#### APIs
|METHOD|URI|Description|
|---|---|---|
| GET       | /apis    | Get a list of all APIs offered by this version of `Goto`.  |

###### <small> [Back to TOC](#toc) </small>

<br/>

# <a name="catch-all"></a>
## > Catch All
Any request that doesn't match any of the defined management APIs, and also doesn't match any proxy targets, gets treated by a catch-all response that sends HTTP 200 response by default (unless an override response code is set)

###### <small> [Back to TOC](#toc) </small>

<br/>
