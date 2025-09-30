# goto

> &#x1F4DD;
<small> The Readme reflects master HEAD code and applies to release 0.9.x. For documentation of `0.8.x` and prior releases, switch to `v0.8.x` branch or any of the release tags.</small>

> &#x1F4DD; <small>Jump to [TOC](#toc) if you'd rather skip the overview and examples given below.</small>

## What is `goto`?

#### <i>`"I'm an agent of chaos" - The Joker`</i>

A multi-faceted chaos testing tool to help with various features to help with automated testing, debugging, bug hunt, runtime analysis and investigations. Mostly when we've a task at hand to test a system, the system to be tested is either a client, a server, or some kind of a gateway/proxy that sits between a client and a server.

To test either of these 3 layers, you need at least one counterparty application:
- To test a client, we need a server to which the client can connect, send requests and get some response back. The server needs to be able to track the lifecycle of the client connections and various requests it received from the client so that the client functionality can be verified.
- To test a server, we need a client that can send some requests to the server and track the responses sent by the server. Again the client should be able to track the lifecycle of connections and requests/responses to be able to verify the server functionality.
- To test an intermediary gateway, we need both a test client as well as a test server, where the two could route some requests and responses through the intermediary and validate the correctness of the traffic flow.

`Goto` can be used in all the above roles, to fill the missing piece of the puzzle.

It can act as:
- A [client](#goto-client-targets-and-traffic) that can generate HTTP/S, TCP, and gRPC traffic to other services (including other `goto` instances), track summary results of the traffic, and report results via [APIs](client-apis) as well as publish results to a [Goto registry](#registry). 
- A [server](#goto-server-features) that can respond to HTTP/S, gRPC and TCP requests, and can be configured to respond to any custom API. The server features allow for various kinds of chaos testing and debugging/investigations, with the ability to track and report summary data about the received traffic. See the [TOC](#goto-server) for a complete list of server features. 
- A [tunneling proxy](#tunnel) that can act as an HTTP/S or TCP passthrough tunnel, allowing any arbitrary traffic from a source to a destination to be re-routed via a `goto` instance and giving you the opportunity to inspect the traffic.
- A [job executor](#jobs-features) that can run shell commands/scripts as well as make HTTP calls, collect and report results. It allows chaining of jobs together so that output of one job triggers another job with input. Additionally, jobs can be auto-executed via cron, and can act as a source of data for pipelines (more on this under `pipelines`)
- A [registry](#registry) to orchestrate operations across other `goto` instances, collect and summarize results from those `goto` instances, and make the federated summary results available via APIs. A `goto` registry can also be paired with another `goto` registry instance and export/load data from one to the other to keep another backup of the collected results.
- A [K8s proxy](#k8s-features) that can connect to and read resource information from a K8s cluster and make it available via APIs. It also supports watching for resource changes, and act as a source of data for pipelines (see below)
- A complex multi-step [pipeline](#pipeline-features) orchestration engine that can:
  - Source data from Shell Commands/Scripts, Client-side HTTP calls, Server-side HTTP responses, K8s resources, K8s pod commands, and Tunneled traffic.
  - Feed the sourced data as input to transformation steps and/or to other source steps.
  - Run JSONPath, JQ, Go Template or Regex transformations on the sourced data to extract information that can be fed to other sources or sent as output.
  - Define stages to orchestrate invocation of sources and transformations in a specific sequence.
  - Define watches on sources so that any changes on the source (e.g. K8s resources, cron jobs, or HTTP traffic) triggers the pipeline that the source is attached to, allowing some complex steps of data extraction, transformation and analysis to occur each time some external event occurs.
  ### AI Features
    #### A2A
    `Goto` can act as an A2A Agent that connects and communicates with other A2A agents and MCP servers. See [A2A Feature](pkg/ai/README.md#a2a-agent-features-server) docs for details.
    #### MCP
    `Goto` can act as a generic MCP server that lets you configure one or more custom MCP tools that can either respond with a custom payload, or can implement one of the few supported behaviors (e.g. an MCP tool that makes a remote HTTP call to a REST server to fetch content and serves the content over MCP, or an MCP tool that makes a remote MCP call to another MCP server to invoke a tool, and includes the remote MCP response back into its own MCP response). See [MCP Feature](pkg/ai/README.md#mcp-admin-apis) docs for details.

<br/>

## How to use it?

#### <u>Grab or Build</u>
It's available as a docker image: `docker.io/uk0000/goto:latest`
> &#x1F4DD; <small><i>The docker image is built with several useful utilities included: `curl`, `wget`, `nmap`, `iputils`, `openssl`, `jq`, etc.</i></small>

<br/>
Or, build it locally on your machine

```
go build -o goto .
```

#### <u>Launch</u>
Start `goto` as a server with multiple ports and protocols. 
> &#x1F4DD; <small><i>First port is treated as bootstrap port and uses HTTP protocol</i></small>

  ```
  goto --ports 8080,8081/http,8082/grpc,8000/tcp
  ```


#### <u>Use Admin APIs to Prepare Goto Client and/or Server</u>
For Example:
- Add a new listener with gRPC protocol and open immediately
  ```
  curl -X POST localhost:8080/server/listeners/add --data '{"port":9091, "protocol":"grpc", "open":true}'
  ```

- Configure a `goto` client instance to send some requests to an HTTP target
  ```
  curl -s localhost:8080/client/targets/add --data '{"name": "t1", "method": "GET", "url": "http://localhost:8081/foo", "body": "some payload", "replicas": 2, "requestCount": 10}'
  ```

# Show me ~~the money~~ some use cases please!

See [Use Cases](docs/use-cases.md) doc.

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

# <a name="toc"></a>

## TOC

### [Startup Command](#goto-startup-command)

### AI
- [A2A Agent](pkg/ai/README.md#a2a-agent-features-server)
- [MCP Server and Client](pkg/ai/README.md#mcp-admin-apis)

### Traffic (Client)
- [Targets and Traffic](#goto-client-targets-and-traffic)
- [Client APIs](#client-apis)
- [Client Events](#client-events)
- [Client JSON Schemas](docs/client-api-json-schemas.md)
- [Client APIs and Results Examples](docs/client-api-examples.md)
- [Client gRPC Examples](docs/grpc-client-examples.md)

### gRPC
- [gRPC Server and Client](#grpc-server)

### HTTPC Server
- [Server Features](#goto-server-features)
- [Goto Response Headers](#http-headers)
- [Logs](#goto-logs)
- [Goto Version](#goto-version)
- [Events](#events)
- [Metrics](#metrics)
- [Listeners](#listeners)
- [Listener Label](#listener-label)
- [Request Headers Tracking](#request-headers-tracking)
- [Request Timeout](#request-timeout-tracking)
- [URIs](#uris)
- [Probes](#probes)
- [Requests Filtering](#requests-filtering)
- [Response Delay](#response-delay)
- [Response Headers](#response-headers)
- [Response Payload](#response-payload)
- [Ad-hoc Payload](#ad-hoc-payload)
- [Stream (Chunked) Payload](#stream-chunked-payload)
- [Response Status](#response-status)
- [Response Triggers](#response-triggers)
- [Status API](#status-api)
- [Delay API](#delay-api)
- [Echo API](#echo-api)
- [Catch All](#catch-all)

### TCP Server
- [TCP Server](#tcp-server-feature)


### Goto Tunnel
- [Tunnel](#tunnel)

### Goto Proxy

- [Proxy Features](#proxy)

### Scripts

- [Scripts Features](#scripts-features)

### Jobs

- [Jobs Features](#jobs-features)

### K8s

- [K8s Features](#k8s-features)

### Pipelines

- [Pipeline Features](#pipeline-features)

### Goto Registry

- [Registry Features](#registry)

  <br/>

# <a name="goto-startup-command"></a>

# Goto Startup Command

First things first, run the application:

```
go run main.go --port 8080
```

Or, build and run

```
go build -o goto .
./goto
```

See [Startup Command](cmd/README.md) doc.

###### <small> [Back to TOC](#toc) </small>

# <a name="goto-version-apis"></a>
## > Goto Version
These APIs get Goto's own details

#### APIs
|METHOD|URI|Description|
|---|---|---|
| GET       | /version    | Get version info of this `goto` instance.  |
| GET       | /apis    | Get a list of all APIs offered by this version of `Goto`.  |


###### <small> [Back to TOC](#toc) </small>

# <a name="goto-client-targets-and-traffic"></a>
###### <small> [Back to TOC](#toc) </small>

# Goto Client: Targets and Traffic
See [Client package documentation](pkg/client/README.md) for details of Goto's Client feature and APIs.

<br/>

# <a name="goto-server-features"></a>

# Goto Server Features

See [Server](pkg/server/README.md) doc.

###### <small> [Back to TOC](#toc) </small>

## > Log APIs

See [Log](pkg/log/README.md) package doc.

# <a name="events"></a>
## > Events
See [Events Package](pkg/events/README.md) for documentation of Goto's Events.

###### <small> [Back to TOC](#toc) </small>

# <a name="metrics"></a>

## > Metrics

See [Metrics](pkg/metrics/README.md) package doc.

###### <small> [Back to TOC](#toc) </small>

# <a name="listeners"></a>

## > Listeners


See [Listeners](pkg/server/README.md#listeners) section in server package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="listener-label"></a>

## > Listener Label

See [Listener Label](pkg/server/README.md#listener-label) section in server package doc.


###### <small> [Back to TOC](#toc) </small>

# <a name="tcp-server-feature"></a>
## > TCP Server Feature

See [TCP Server](pkg/server/tcp/README.md) package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="grpc-server"></a>

## > gRPC Server

See [RPC Package](pkg/rpc/README.md) docs for an overview of `Goto` gRPC feature and APIs.


###### <small> [Back to TOC](#toc) </small>


# <a name="request-headers-tracking"></a>

## > Request Headers Tracking

See [Request Header Tracking](pkg/server/request/README.md#request-headers-tracking) section in Request package doc.


###### <small> [Back to TOC](#toc) </small>

<a name="request-timeout-tracking"></a>
## > Request Timeout Tracking

See [Request Timeout Tracking](pkg/server/request/README.md#request-timeout-tracking) section in Request package doc.

###### <small> [Back to TOC](#toc) </small>

# <a name="uris"></a>
## > URIs

See [Request URI Tracking](pkg/server/request/README.md#request-uri-tracking) section in Request package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="probes"></a>
## > Probes

See [Server Probes](pkg/server/probes/README.md) package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="requests-filtering"></a>
## > Requests Filtering

See [Request Filtering](pkg/server/request/README.md#requests-filtering) section in Request package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="response-delay"></a>
## > Response Delay

See [Response Delay](pkg/server/response/README.md#response-delay) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="server-response-headers"></a>
## > Response Headers

See [Response Delay](pkg/server/response/README.md#response-headers) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="response-payload"></a>
## > Response Payload

See [Response Delay](pkg/server/response/README.md#response-payload) section in Response package doc.


###### <small> [Back to TOC](#toc) </small>


# <a name="ad-hoc-payload"></a>
## > Ad-hoc Payload

See [Ad-Hoc Payload](pkg/server/response/README.md#ad-hoc-payload) section in Response package doc.


###### <small> [Back to TOC](#toc) </small>


# <a name="response-status"></a>
## > Response Status

See [Response Status](pkg/server/response/README.md#response-status) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="response-triggers"></a>
## > Response Triggers

See [Response Triggers](pkg/server/response/README.md#response-triggers) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="status-api"></a>
## > Status API

See [Response Status](pkg/server/response/README.md#status) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="delay-api"></a>
## > Delay API

See [Response Delay](pkg/server/response/README.md#delay) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="echo-api"></a>
## > Echo API

See [Echo](pkg/server/README.md#echo-api) section in Response package doc.

###### <small> [Back to TOC](#toc) </small>


# <a name="catch-all"></a>

## > Catch All

Any request that doesn't match any of the defined management APIs, and also doesn't match any proxy targets, gets treated by a catch-all response that sends HTTP 200 response by default (unless an override response code is set)

###### <small> [Back to TOC](#toc) </small>

<br/>

# <a name="proxy"></a>
# Proxy

See [Proxy Package](pkg/proxy/README.md) for details about Goto's Proxy feature and APIs.


###### <small> [Back to TOC](#goto-proxy) </small>



# <a name="scripts-features"></a>
# Scripts Features

See [Scripts Package](pkg/scripts/README.md) for details about Goto's Scripts feature and APIs.

###### <small> [Back to TOC](#scripts) </small>


# <a name="jobs-features"></a>
# Jobs Features

See [Job Package](pkg/job/README.md) for details about Goto's Jobs feature and APIs.

###### <small> [Back to TOC](#jobs) </small>


# <a name="k8s-features"></a>

# K8s Features

See [K8s Package](pkg/k8s/README.md) for details about Goto's K8s feature and APIs.

###### <small> [Back to TOC](#k8s) </small>


# <a name="tunnel"></a>
# Tunnel


See [Tunnel Package](pkg/tunnel/README.md) for details about Goto's Tunnel feature and APIs.


###### <small> [Back to TOC](#goto-tunnel) </small>


# <a name="pipeline-features"></a>
# Pipeline Features


See [Pipeline Package](pkg/pipe/README.md) for details about Goto's Pipeline feature and APIs.


###### <small> [Back to TOC](#pipelines) </small>

<br/>

# <a name="registry"></a>

# Registry
See [Registry Overview](pkg/registry/Overview.md) doc for an overview and examples of Goto's Registry feature.

See [Registry APIs](pkg/registry/README.md) for the list of Registry REST APIs.

###### <small> [Back to TOC](#goto-registry) </small>
