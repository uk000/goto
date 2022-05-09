# goto

> &#x1F4DD;
<small> The Readme reflects master HEAD code and applies to release 0.9.x. For documentation of `0.8.x` and prior releases, switch to `v0.8.x` branch or any of the release tags.</small>

> &#x1F4DD; <small>Jump to [TOC](#toc) if you'd rather skip the overview and examples given below.</small>

## What is `goto`?

#### <i>`"I'm an agent of chaos" - The Joker`</i>

A multi-faceted chaos testing tool to help with various kinds of automated testing, debugging, bug hunt, runtime analysis and investigations. Mostly when we've a task at hand to test a system, the system to be tested is either a client, a server, or some kind of a gateway/proxy that sits between a client and a server.

To test either of these 3 layers, you need at least one counterparty application:
- To test a client, we need a server to which the client can connect, send requests and get some response back. The server needs to be able to track the lifecycle of the client connections and various requests it received from the client so that the client functionality can be verified.
- To test a server, we need a client that can send some requests to the server and track the responses sent by the server. Again the client should be able to track the lifecycle of connections and requests/responses to be able to verify the server functionality.
- To test an intermediary gateway, we need both a test client as well as a test server, where the two could route some requests and responses through the intermediary and validate the correctness of the traffic flow.

`Goto` can be used in all the above roles, to fill the missing piece of the puzzle.

It can act as:
- A [client](#goto-client-targets-and-traffic) that can generate HTTP/S, TCP, and GRPC traffic to other services (including other `goto` instances), track summary results of the traffic, and report results via [APIs](client-apis) as well as publish results to a [Goto registry](#registry). 
- A [server](#goto-server-features) that can respond to HTTP/S, GRPC and TCP requests, and can be configured to respond to any custom API. The server features allow for various kinds of chaos testing and debugging/investigations, with the ability to track and report summary data about the received traffic. See the [TOC](#goto-server) for a complete list of server features. 
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
- Add a new listener with GRPC protocol and open immediately
  ```
  curl -X POST localhost:8080/server/listeners/add --data '{"port":9091, "protocol":"grpc", "open":true}'
  ```

- Configure a `goto` client instance to send some requests to an HTTP target
  ```
  curl -s localhost:8080/client/targets/add --data '{"name": "t1", "method": "GET", "url": "http://localhost:8081/foo", "body": "some payload", "replicas": 2, "requestCount": 10}'
  ```

# Show me ~~the money~~ some use cases please!

## `Use Case`: Test a client application's behavior when the upstream service goes down mid-request
Say you have a client application that connects to some remote service for some APIs. You intend to test how your application behaves if the server died before or during a request.

What's needed is:
- A `stand-in` test server that can respond to the exact API your client invokes and send valid responses too (headers, JSON payload, etc.). This requires the server to be configurable such that you can define a custom API along with a custom response for the API.
- We should be able to ask the service to die anytime! But we don't really want the server process to die, because we want the service to also become available at some later point. We want the service downtime to be such that it can be scripted and tested without losing any observable metrics/logs/results server may have collected so far.
- Although the requirement didn't ask for this, wouldn't it be nice if we could reconfigure the API to respond with a slightly different response (v2.0) so that when the Service API is back up again, we can see that the client is indeed getting a different response now! But how do we configure a dead port? Well, how do we configure any port? Hmm... what's a port anyway?

Quite some ask. This example shows how `Goto` can be of help: [Using Goto for upstream service chaos](docs/goto-upstream-chaos.md#scenario-mocking-upstream-service-death).

#
## `Use Case`: Test a client application's behavior against invalid upstream TLS certs
Similar to the previous scenario but going one step further, let's say your client application's users have reported that the application acts funny randomly. You suspect it may have to do with the TLS certs presented by some of the upstream service nodes. In order to validate your hypothesis, you need to be able to manipulate the server TLS certs on-the-fly, to switch between valid and invalid certs.

In addition to the previous scenario's requirements, what we need is:
- The server should give us a way to change the TLS certs used for a port via some backdoor access, preferably by calling some `admin` API so that it can be scripted. 
- The server should provide enough observability so we can validate that a request indeed reached the server with certain parameters, and a response was sent. The traffic observed on the server can be correlated with the behavior observed on the client.

Well, not to despair, we have `goto`. See the solution here: [Using Goto for upstream service TLS chaos](docs/goto-upstream-chaos.md#scenario-mocking-upstream-tls-certs-chaos).

#
## `Use Case Pattern`: Test a client against upstream chaos
Now that you have seen the previous two use cases, you may already be thinking of several similar scenarios where `goto` may help. The pattern here is that a client application needs to be tested against a chaotic upstream service. The upstream service can introduce various kinds of chaos at any point in the client-service interaction, and we need to assess the client behavior in presence of the chaos.
Examples of chaotic situations that the upstream server may present are:
- Service switches from HTTP to HTTP/2
- Service port dies while the client was connected
- Service takes a long time to respond to a request, causing the client to time out.
- Service response time is longer than the HTTP/TCP timeouts configured on any intermediate gateway/proxy.
- Service takes too long to respond and triggers an HTTP server timeout on the service end
- Service switches from HTTPS to HTTP and vice-versa 
- Service switches between various kinds of TLS certs with different encryption parameters, etc.
- Server response exceeds some HTTP protocol limits: too big header, too big payload, etc.

There can be many more chaotic scenarios that you may think of. Many such scenarios can be recreated in the artificial testing setup using some basic configurable patterns that `goto` offers:
- Generic server that can listen and respond on one or more ports with different protocols (HTTP, HTTPS, HTTP/2, TCP, GRPC)
- Being able to open/close ports on the fly to mimic a service going down and recovering
- Change a port's protocol on-the-fly before reopening it
- Add/remove custom TLS certs for certain ports
- Auto-generate TLS certs for certain ports
- Configure a custom URI that the server can respond to with a custom response (status code, headers, payload)
- Introduce artificial delays for all or specific requests
- Configure dynamic response for certain APIs, where the response can be based on values received in the request (performing transformations).

#
## `Use Case`: Test traffic behavior between a pair of client and service in the face of network or proxy chaos
Another aspect of chaos testing, perhaps in a more advanced setup, is where we want to observe the behavior of a client and a service as their communication gets disrupted in the network or in some intermediate proxy/gateway. The key idea is that the chaos gets introduced by an intermediary that sits in the traffic path but is outside the control of both the client and the server. Such intermediary chaos is hard to produce without disrupting the network or forcibly introducing bad behavior in the intermediate gateway/proxy. 

Hmm, hard you say. What's needed is a tool (obvious by now what that tool would be) that can sit between a client and a service, allowing the traffic to flow through it while giving us the knobs that we can turn to disrupt the traffic.

Capabilities needed to make this happen:
- Make certain upstream calls based on downstream requests, and use upstream response to build a dynamic response to send back to the downstream client (performing transformations). This ensures at the minimum that the client and the service are unaware of the presence of a middleman.
- Introduce some chaos into the traffic as an intermediary.

This is what `Goto`'s [Proxy](#proxy) and [Tunnel](#tunnel) features aim to provide. See, it's only hard until you bring `goto` into the mix. See one such example usage in this doc: [Scenario: Creating chaos with Goto as an HTTP proxy](docs/goto-proxy-chaos.md#scenario-creating-chaos-with-goto-as-an-http-proxy).


#
## `Use Case`: Test TCP clients and services with some network or proxy chaos
Taking things up a notch, the previous scenario can be re-imagined with TCP clients and services, trying to create some network or proxy chaos and observing the client/service behaviors.

Capabilities needed to make this happen:
- A TCP proxy that can sit between a client and a service transparently.
- Introduce some chaos into the traffic at this intermediary proxy.

See an example in this doc: [Scenario: Creating TCP chaos with Goto](docs/goto-proxy-chaos.md#scenario-creating-tcp-chaos-with-goto)

Going even further, `goto` proxy allows for routing to multiple upstream TCP endpoints based on SNI from client's TLS handshake. `Goto` would still act as an opaque TCP proxy and actual TLS handshake gets done between the client and the service, but `goto` can inspect the client handshake packets to read the SNI information and route to one of the multiple upstream TCP endpoints that are configured for SNI-based routing. Too much? Well, at least see the example in this doc: [Scenario: TCP Proxy with SNI matching using Goto](docs/goto-proxy-chaos.md#scenario-tcp-proxy-with-sni-matching-using-goto)

See more [Proxy Examples](docs/proxy-example.md)

#
## `Use Case`: Test an upstream GRPC service behavior
`Goto` client module has support for sending GRPC traffic to any arbitrary GRPC service. 
- You'd need a proto file for the upstream service that describes the service methods, inputs, outputs, etc. 
- `Goto` GRPC APIs allow you to upload proto files that `goto` can parse and learn about the available GRPC services. 
- Then it's just a matter of adding targets to the `goto` client module and asking it to trigger the traffic. 

See [Client GRPC Examples](docs/grpc-client-examples.md) for full working samples.


#
## What Else?
- You need to inspect some existing traffic between two applications in order to investigate some issue. The traffic passes through various network layers, and you wish to analyze the state of a request at a certain point in its journey, e.g. what does the request look like once it goes through a K8s ingress gateway. You're staring at the right tool.
- You wish to test a proxy/gateway behavior sitting between a pair of client and service in the face of upstream and/or downstream chaos. Here our focus of testing is an intermediate proxy (e.g. service mesh).

<span style="color:red">TODO: Many more scenarios can benefit from `goto`. More scenarios will be added here soon.</span>

---

## Some Flow Diagrams and Scenarios Explained

Check these flow diagrams to get a visual overview of `Goto` behavior and usage.

### Flow: [Use client APIs to register and invoke traffic to targets ](docs/goto-client-targets.md) <a href="docs/goto-client-targets.md"><img src="docs/Goto-Client-Targets-Thumb.png" width="50" height="50" style="border: 1px solid gray; box-shadow: 1px 1px #888888; vertical-align: middle;" /></a>

### Flow: [Configuring Server Listeners](docs/goto-listeners.md) <a href="docs/goto-listeners.md"><img src="docs/Goto-Listeners-Thumb.png" width="50" height="50" style="border: 1px solid gray; box-shadow: 1px 1px #888888; vertical-align: middle;" /></a>

### Flow: [<small>`Goto`</small> Registry - Peer interactions](docs/goto-registry-peers-interactions.md) <a href="docs/goto-registry-peers-interactions.md"><img src="docs/Goto-Registry-Peers-Thumb.png" width="50" height="50" style="border: 1px solid gray; box-shadow: 1px 1px #888888; vertical-align: middle;" /></a>

### Overview: [Goto Lockers](docs/goto-lockers.md) <a href="docs/goto-lockers.md"><img src="docs/Goto-Lockers-Thumb.png" width="50" height="50" style="border: 1px solid gray; box-shadow: 1px 1px #888888; vertical-align: middle;" /></a>

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

### Goto Client
- [Targets and Traffic](#goto-client-targets-and-traffic)
- [Client APIs](#client-apis)
- [Client Events](#client-events)
- [Client JSON Schemas](docs/client-api-json-schemas.md)
- [Client APIs and Results Examples](docs/client-api-examples.md)
- [Client GRPC Examples](docs/grpc-client-examples.md)

### GRPC Client
- [GRPC APIs](#grpc-apis)

### Goto Server
- [Server Features](#goto-server-features)
- [Goto Response Headers](#-http-headers)
- [Logs](#goto-logs)
- [Goto Version](#-goto-version)
- [Events](#-events)
- [Metrics](#-metrics)
- [Listeners](#-listeners)
- [Listener Label](#-listener-label)
- [TCP Server](#-tcp-server)
- [GRPC Server](#-grpc-server)
- [Request Headers Tracking](#-request-headers-tracking)
- [Request Timeout](#-request-timeout-tracking)
- [URIs](#-uris)
- [Probes](#-probes)
- [Requests Filtering](#-requests-filtering)
- [Response Delay](#-response-delay)
- [Response Headers](#-response-headers)
- [Response Payload](#-response-payload)
- [Ad-hoc Payload](#-ad-hoc-payload)
- [Stream (Chunked) Payload](#-stream-chunked-payload)
- [Response Status](#-response-status)
- [Response Triggers](#-response-triggers)
- [Status API](#-status-api)
- [Delay API](#-delay-api)
- [Echo API](#-echo-api)
- [Catch All](#-catch-all)

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
- [Registry Peers APIs](#registry-peers-apis)
- [Locker Management APIs](#locker-management-apis)
- [Locker Data Path Read APIs](#locker-data-path-read-apis)
- [Lockers Dump APIs](#lockers-dump-apis)
- [Peers Events APIs](#registry-events-apis)
- [Peer Targets Management APIs](#peers-targets-management-apis)
- [Peers Client Results APIs](#peers-client-results-apis)
- [Peer Jobs Management APIs](#peers-jobs-management-apis)
- [Peers Config Management APIs](#peers-config-management-apis)
- [Peer Call APIs](#peers-call-apis)
- [Registry clone, dump and load APIs](#registry-clone-dump-and-load-apis)
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

The application accepts the following command arguments:

<table style="font-size: 0.9em;">
    <thead>
        <tr>
            <th>Argument</th>
            <th>Description</th>
            <th>Default Value</th>
        </tr>
    </thead>
    <tbody>
        <tr>
          <td rowspan="2"><pre>--port {port}</pre></td>
          <td>Primary port the server listens on. Alternately use <strong>--ports</strong> for multiple startup ports. Additional ports can be opened by making listener API calls on this port. See <a href="#-listeners">Listeners</a> feature for more details. </td>
          <td rowspan="2">8080</td>
        </tr>
        <tr>
          <td>* Additional ports can be opened by making listener API calls on this port. See <a href="#-listeners">Listeners</a> feature for more details.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--ports {ports}</pre></td>
          <td>Initial list of ports that the server should start with. Port list is given as comma-separated list of <pre>{port1},<br/>{port2}/{protocol2}/{commonName2},<br/>{port3}/{protocol3}/{commonName3},...</pre>. The first port in the list is used as the primary port and is forced to be HTTP. Protocol is optional, and can be one of <pre>http (default), http1, https, https1, tcp,<br/> tls (tcp+tls), grpc, or grpcs (grpc+tls). </pre> Protocol <strong>https</strong> configures the port to serve HTTP requests with a self-signed TLS cert, whereas protocol <strong>tls</strong> configures a TCP port with self-signed TLS cert, and <strong>grpcs</strong> configures a GRPC port with self-signed TLS cert. <strong>CommonName</strong> is used for generating self-signed certs, and defaults to <strong>goto.goto</strong>. Use protocol <strong>http1</strong> or <strong>https1</strong> to configure a listener port that only serves HTTP/1.1 protocol and explicitly disallows HTTP/2 protocol. </td>
          <td rowspan="2">""</td>
        </tr>
        <tr>
          <td>* For example: <pre>--ports 8080,<br/>8081/http,8083/https,<br/>8443/https/foo.com,<br/>8000/tcp,9000/tls,10000/grpc</pre>  In addition to the startup ports, additional ports can be opened by making listener API calls on this port. See <a href="#-listeners">Listeners</a> feature for more details.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--label `{label}`</pre></td>
          <td>Label that this instance will use to identify itself. </td>
          <td rowspan="2">Goto-`IPAddress:Port` </td>
        </tr>
        <tr>
          <td>* This is used both for setting Goto's default response headers as well as when registering with the registry.</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--startupDelay<br/> {delay}</pre></td>
          <td>Delay the startup by this duration. </td>
          <td rowspan="1">1s</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--shutdownDelay<br/> {delay}</pre></td>
          <td>Delay the shutdown by this duration after receiving SIGTERM. </td>
          <td rowspan="1">1s</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--startupScript<br/> {shell command}</pre></td>
          <td>List of shell commands to execute at goto startup. Multiple commands are specified by passing multiple instances of this arg. The commands are joined with ';' as a separator and executed using 'sh -c'. </td>
          <td rowspan="1"></td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--registry {url}</pre></td>
          <td>URL of the Goto Registry instance that this instance should connect to. </td>
          <td rowspan="2"> "" </td>
        </tr>
        <tr>
          <td>* This is used to get initial configs and optionally report results to the Goto registry. See <a href="#registry-features">Registry</a> feature for more details.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--locker<br/>={true|false}</pre></td>
          <td> Whether this instance should report its results back to the Goto Registry. </td>
          <td rowspan="2"> false </td>
        </tr>
        <tr>
          <td>* An instance can be asked to report its results to the Goto registry in case the  instance is transient, e.g. pods.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--events<br/>={true|false}</pre></td>
          <td> Whether this instance should generate events and build a timeline locally. </td>
          <td rowspan="2"> true </td>
        </tr>
        <tr>
          <td>* Events timeline can be helpful in observing how various operations and traffic were interleaved, and help reason about some outcome.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--publishEvents<br/>={true|false}</pre></td>
          <td> Whether this instance should publish its events to the registry to let registry build a unified timeline of events collected from various peer instances. This flag takes effect only if a registry URL is specified to let this instance connect to a registry instance. </td>
          <td rowspan="2"> false </td>
        </tr>
        <tr>
          <td>* Events timeline can be helpful in observing how various operations and traffic were interleaved, and help reason about some outcome.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--certs `{path}`</pre></td>
          <td> Directory path from where to load TLS root certificates. </td>
          <td rowspan="2"> "/etc/certs" </td>
        </tr>
        <tr>
          <td>* The loaded root certificates are used if available, otherwise system default root certs are used.</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--serverLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable all goto server logging. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--adminLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of admin calls to configure goto. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--metricsLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of calls to metrics URIs. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--probeLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of requests received for URIs configured as liveness and readiness probes. See <a href="#server-probes">Probes</a> for more details. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--clientLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of client activities. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--invocationLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable client's target invocation logs. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--registryLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable all registry logs. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--lockerLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of locker requests on Registry instance. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--eventsLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of store event calls from peers on Registry instance. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--reminderLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable reminder logs received from various peer instances (applicable to goto instances acting as registry). </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--peerHealthLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of requests received from Registry for peer health checks </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestHeaders<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request headers </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request body </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestMiniBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request mini body </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseHeaders<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response headers </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response body </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseMiniBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response mini body </td>
          <td rowspan="1">true</td>
        </tr>
    </tbody>
</table>

Once the server is up and running, rest of the interactions and configurations are done purely via REST APIs.

###### <small> [Back to TOC](#toc) </small>

# <a name="goto-client-targets-and-traffic"></a>

# Goto Client: Targets and Traffic

As a client tool, `goto` offers the feature to configure multiple targets and send http/https/tcp/grpc traffic:

- Allows targets to be configured and invoked via REST APIs
- Configure targets to be invoked ahead of time before invocation, as well as auto-invoke targets upon configuration
- Invoke selective targets or all configured targets in batches
- Control various parameters for a target: number of concurrent, total number of requests, minimum wait time after each replica set invocation per target, various timeouts, etc
- Headers can be set to track results for target invocations, and APIs make those results available for consumption as JSON output.
- Retry requests for specific response codes, and option to use a fallback URL for retries
- Make simultaneous calls to two URLs to perform an A-B comparison of responses. In AB mode, the same request ID (enabled via sendID flag) are used for both A and B calls, but with a suffix `-B` used for B calls. This allows tracking the A and B calls in logs.
- Have client invoke a random URL for each request from a set of URLs
- Generate hybrid traffic that includes HTTP/S, H2, TCP and GRPC requests.

The invocation results get accumulated across multiple invocations until cleared explicitly. Various results APIs can be used to read the accumulated results. Clearing of all results resets the invocation counter too, causing the next invocation to start at counter 1 again. When a peer is connected to a registry instance, it stores all its invocation results in a registry locker. The peer publishes its invocation results to the registry at an interval of 3-5 seconds depending on the flow of results. See Registry APIs for detail on how to query results accumulated from multiple peers.

In addition to keeping the results in the `goto` client instance, those are also stored in a locker on the registry instance if enabled. (See `--locker` command arg). Various events are added to the peer timeline related to target invocations it performs, which are also reported to the registry. These events can be seen in the event timeline on the peer instance as well as its event timeline from the registry.

Client sends header `From-Goto-Host` to pass its identity to the server.


# <a name="client-apis"></a>
#### Client APIs
|METHOD|URI|Description|
|---|---|---|
| POST      | /client/targets/add                   | Add a target for invocation. [See `Client Target JSON Schema` for Payload](#client-target-json-schema) |
| POST      |	/client/targets/`{targets}`/remove      | Remove given targets |
| POST      | /client/targets/`{targets}`/invoke      | Invoke given targets |
| POST      |	/client/targets/invoke/all            | Invoke all targets |
| POST      | /client/targets/`{targets}`/stop        | Stops a running target |
| POST      | /client/targets/stop/all              | Stops all running targets |
| GET       |	/client/targets                       | Get list of currently configured targets |
| GET       |	/client/targets/{target}           | Get details of given target |
| POST      |	/client/targets/clear                 | Remove all targets |
| GET       |	/client/targets/active                | Get list of currently active (running) targets |
| POST      |	/client/targets/cacert/add            | Store CA cert to use for all target invocations |
| POST      |	/client/targets/cacert/remove         | Remove stored CA cert |
| PUT, POST |	/client/track/headers/`{headers}`   | Add headers for tracking response counts per target |
| POST      | /client/track/headers/clear           | Remove all tracked headers |
| GET       |	/client/track/headers                 | Get list of tracked headers |
| PUT, POST |	/client/track/time/`{buckets}`   | Add time buckets for tracking response counts per bucket. Buckets are added as a comma-separated list of `low-high` values in millis, e.g. `0-100,101-300,301-1000` |
| POST      | /client/track/time/clear           | Remove all tracked time buckets |
| GET       |	/client/track/time                 | Get list of tracked time buckets |
| GET       |	/client/results                       | Get combined results for all invocations since last time results were cleared. |
| GET       |	/client/results/invocations           | Get invocation results broken down for each invocation that was triggered since last time results were cleared |
| POST      | /client/results/clear                 | Clear previously accumulated invocation results |
| POST      | /client/results/clear                 | Clear previously accumulated invocation results |
| POST      | /client/results<br/>/all/`{enable}`          | Enable/disable collection of cumulative results across all targets. This gives a high level overview of all traffic, but at a performance overhead. Disabled by default. |
| POST      | /client/results<br/>/invocations/`{enable}`          | Enable/disable collection of results by invocations. This gives more detailed visibility into results per invocation but has performance overhead. Disabled by default. |

###### <small> [Back to TOC](#toc) </small>


#### Client Events
<details>
<summary>Client Events List</summary>

- `Target Added`: an invocation target was added
- `Targets Removed`: one or more invocation targets were removed
- `Targets Cleared`: all invocation targets were removed
- `Tracking Headers Added`: headers added for tracking against invocation responses
- `Tracking Headers Cleared`: all tracking headers were removed
- `Tracking Time Buckets Added`: time buckets added for tracking against invocation responses
- `Tracking Time Buckets Cleared`: all tracking time buckets were removed
- `Client CA Cert Stored`: CA cert was added for validating TLS cert presented by target
- `Client CA Cert Removed`: CA cert was removed
- `Results Cleared`: all collected invocation results were cleared
- `Target Invoked`: one or more invocation targets were invoked
- `Targets Stopped`: one or more invocation targets were stopped
- `Invocation Started`: invocation started for a target
- `Invocation Finished`: invocation finished for a target
- `Invocation Response Status`: Either first invocation response received, or the HTTP response status was different from previous response from a target during an invocation
- `Invocation Repeated Response Status`: All HTTP responses after the first response from a target where the response status code was the same as the previous, are accumulated and reported in summary. This event is sent out when the next response is found to carry a different response status code, or if all requests to a target completed for an invocation.
- `Invocation Failure`: Event reported upon first failed request, or if a request fails after previous successful request.
- `Invocation Repeated Failure`: All request failures after a failed request are accumulated and reported in summary, either when the next request succeeds or when the invocation completes.
</details>
<br/>

See [Client JSON Schemas](docs/client-api-json-schemas.md)

See [Client APIs and Results Examples](docs/client-api-examples.md)

See [Client GRPC Examples](docs/grpc-client-examples.md)

###### <small> [Back to TOC](#toc) </small>

<br/>

# GRPC Client features

`Goto` can act as a generic GRPC client that allows uploading of proto files in order to auto-discover services and methods, and invoke those service methods. The GRPC Client feature is exposed via various APIs and also used by the goto client for generating grpc traffic.

# <a name="grpc-apis"></a>
#### GRPC APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /grpc/protos/add<br/>/`{name}`?`path={path}` | Upload a GRPC proto file as request body payload. The proto will be parsed to extract service definitions. The uploaded proto file gets saved under `./protos` dir. If the optional path param is passed, the given path is appended to `./protos/`. It assumes that the local filesystem is writable. |
| POST      |	/grpc/protos/clear      | Remove all uploaded proto definitions from memory and local filesystem. |
| GET      |	/grpc/protos | Returns a list of uploaded proto files. |
| GET      |	/grpc/protos/list<br/>/services | Returns a list of services parsed from the uploaded proto files. |
| GET      |	/grpc/protos/list<br/>/`{service}`/methods | Returns a list of methods for the given service as parsed from the uploaded proto files. |

###### <small> [Back to TOC](#toc) </small>

<br/>

# <a name="goto-server-features"></a>

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
- `Goto-Host|Proxy`: identifies the `goto` instance that acted as a proxy for this request
- `Via-Goto|Proxy`: label of the `goto` instance that acted as a proxy for this request
- `Goto-Port|Proxy`: port number on which the `goto` proxy instance received this request, which may be different than the upstream service port
- `Goto-Protocol|Proxy`: Protocol over which the `goto` proxy instance served this request, which may be different than the protocol used for the upstream service call
- `Goto-Response-Status|Proxy`: HTTP response status code that `goto` proxy instance responded with. Upstream's response status code is preserved in another header listed below.
- `Goto-In-At|Proxy`: UTC timestamp when the request was received by the `goto` proxy instance
- `Goto-Out-At|Proxy`: UTC timestamp when the `goto` proxy instance finished processing the request and sent a response
- `Goto-Took|Proxy`: Total processing time taken by the `goto` proxy instance to process the request
- `Goto-Proxy-Upstream-Status|<upstream-name>`: status the `goto` proxy's upstream service responded with, which may be different than the status `goto` responded with to the downstream client
- `Goto-Proxy-Upstream-Took|<upstream-name>`: Total roundtrip time the proxy's upstream call took
- `Goto-Proxy-Delay`: any delay added to the request/response by `goto` as a proxy
- `Goto-Proxy-Request-Dropped`: sent back with response when the proxy drops the downstream request based on the drop percentage defined for the upstream target (see proxy feature)
- `Goto-Proxy-Response-Dropped`: sent back with response when the proxy drops the upstream response based on the drop percentage defined for the upstream target (see proxy feature)

#### Tunnel response headers:
- `Goto-Tunnel-Host[<seq>]`: identifies `goto` instance hosts through which this request was tunneled along with the sequence number of each instance in the tunnel chain.
- `Via-Goto-Tunnel[<seq>]`: identifies `goto` instance labels through which this request was tunneled along with the sequence number of each instance in the tunnel chain.
- `Goto-In-At[<seq>]`: UTC timestamp when the request was received by each `goto` in the tunnel chain
- `Goto-Out-At[<seq>]`: UTC timestamp when the request finished processing by the `goto` tunnel instance
- `Goto-Took[<seq>]`: Total processing time taken by the `goto` tunnel instance

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


###### <small> [Back to TOC](#toc) </small>

### Log APIs
The log APIs can be used to see the current logging config and turn logging on/off for various components.

|METHOD|URI|Description|
|---|---|---|
| GET   | /log    | Get the current logging config for all components.  |
| POST | /log/server/{enable} | Enable/disable server logging completely |
| POST | /log/admin/{enable} | Enable/disable logging of admin calls |
| POST | /log/client/{enable} | Enable/disable client logging completely |
| POST | /log/invocation/{enable} | Enable/disable invocation logs |
| POST | /log/invocation<br/>/response/{enable} | Enable/disable logging of response received for each invocation call |
| POST | /log/registry/{enable} | Enable/disable registry logs |
| POST | /log/registry<br/>/locker/{enable} | Enable/disable registry locker logs |
| POST | /log/registry<br/>/events/{enable} | Enable/disable registry events logs |
| POST | /log/registry<br/>/reminder/{enable} | Enable/disable logging of reminder calls that registry receives from peers |
| POST | /log/health/{enable} | Enable/disable logging of health calls that peers receive from registry |
| POST | /log/probe/{enable} | Enable/disable readiness and liveness probe logs |
| POST | /log/metrics/{enable} | Enable/disable metrics logs |
| POST | /log/request<br/>/headers/{enable} | Enable/disable request headers logs |
| POST | /log/request<br/>/minibody/{enable} | Enable/disable request minibody (truncated body) logs (currently only supported for HTTP/1.x requests, not for H/2)  |
| POST | /log/request<br/>/body/{enable} | Enable/disable request body logs (currently only supported for HTTP/1.x requests, not for H/2) |
| POST | /log/response<br/>/headers/{enable} | Enable/disable response headers logs |
| POST | /log/response<br/>/minibody/{enable} | Enable/disable response minibody (truncated body) logs (currently only supported for HTTP/1.x requests, not for H/2) |
| POST | /log/response<br/>/body/{enable} | Enable/disable response body logs (currently only supported for HTTP/1.x requests, not for H/2) |

<details>
<summary>Log API Examples</summary>

```
curl localhost:8080/log

curl -X POST localhost:8080/log/headers/request/y
curl -X POST localhost:8080/log/headers/response/n
curl -X POST localhost:8080/log/invocation/n

```
</details>

# <a name="goto-version"></a>
## > Goto Version
This API returns version info of the `goto` instance

#### APIs
|METHOD|URI|Description|
|---|---|---|
| GET       | /version    | Get version info of this `goto` instance.  |


###### <small> [Back to TOC](#toc) </small>

# <a name="events"></a>
## > Events
`goto` logs various events as it performs operations, responds to admin requests and serves traffic. The Events APIs can be used to read and clear events on a `goto` instance. Additionally, if the `goto` instance is configured to report to a registry, it sends the events to the registry. On the Goto registry, events from various peer instances are collected and merged by peer labels. Registry exposes additional APIs to get the event timeline either for a peer (by peer label) or across all connected peers as a single unified timeline. Registry also allows clearing of events timeline on all connected instances through a single API call. See Registry APIs for additional info.

#### APIs

Param `reverse=y` produces the timeline in reverse chronological order. By default events are returned with `data` set to `...` to reduce the amount of data returned. Param `data=y` returns the events with data.

|METHOD|URI|Description|
|---|---|---|
| POST      | /events/flush    | Publish any pending events to the registry, and clear the instance's events timeline. |
| POST      | /events/clear    | Clear the instance's events timeline. |
| GET       | /events?reverse=`[y/n]`<br/>&data=`[y/n]` | Get events timeline of the instance. To get combined events from all instances, use the registry's peers events APIs instead.  |
| GET       | /events/search/`{text}`?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Search the instance's events timeline. |


<details>
<summary>Server Events Details</summary>

Each `goto` instance publishes these events at startup and shutdown
- `Server Started`
- `GRPC Server Started`
- `Server Stopped`
- `GRPC Server Stopped`

A `goto` peer that's configured to connect to a `goto` registry publishes the following additional events at startup and shutdown:
- `Peer Registered`
- `Peer Startup Data`
- `Peer Deregistered`

A server generates `URI First Request` upon receiving the first request for a URI. Subsequent requests for that URI are tracked and counted as long as it produces the same response status. Once the response status code changes for a URI, it generates `Repeated URI Status` to log the accumulated summary of the URI so far, and the logs `URI Status Changed` to report the new status code. The accumulation and tracking logic then proceeds with this new status code, reporting once the status changes again for that URI.

Various other events are published by `goto` peer instances acting as client and server, and by the `goto` registry instance, which are listed in other sections in this Readme.

</details>

See [Events Example](docs/events-example.md)

###### <small> [Back to TOC](#toc) </small>

# <a name="metrics"></a>

## > Metrics

### Prometheus Metrics

`Goto` exposes custom server metrics as well as golang VM metrics in prometheus format.
<details>
<summary>List of Prometheus metrics exposed by goto</summary>

- `goto_requests_by_type` (vector): Number of requests by type (dimension: requestType)
- `goto_requests_by_headers` (vector): Number of requests by request headers (dimension: requestHeader)
- `goto_requests_by_header_values` (vector): Number of requests by request headers values (dimensions: requestHeader, headerValue)
- `goto_requests_by_uris` (vector): Number of requests by URIs (dimension: uri)
- `goto_requests_by_uris_and_status` (vector): Number of requests by URIs and status code (dimensions: uri, statusCode)
- `goto_requests_by_headers_and_uris` (vector): Number of requests by request headers and URIs (dimensions: requestHeader, uri)
- `goto_requests_by_headers_and_status` (vector): Number of requests by request headers and status codes (dimensions: requestHeader, statusCode)
- `goto_requests_by_port` (vector): Number of requests by ports (dimension: port)
- `goto_requests_by_port_and_uris` (vector): Number of requests by ports and uris (dimensions: port, uri)
- `goto_requests_by_port_and_headers` (vector): Number of requests by ports and request headers (dimensions: port, requestHeader)
- `goto_requests_by_port_and_header_values` (vector): Number of requests by ports and request header values (dimensions: port, requestHeader, headerValue)
- `goto_requests_by_protocol` (vector): Number of requests by protocol (dimension: protocol)
- `goto_requests_by_protocol_and_uris` (vector): Number of requests by protocol and URIs (dimensions: protocol, uri)
- `goto_invocations_by_targets` (vector): Number of client invocations by target (dimension: target)
- `goto_failed_invocations_by_targets` (vector): Number of failed invocations by target (dimension: target)
- `goto_requests_by_client`: Number of server requests by client (dimension: client)
- `goto_proxied_requests` (vector): Number of proxied requests (dimension: proxyTarget)
- `goto_triggers` (vector): Number of triggered requests (dimension: triggerTarget)
- `goto_conn_counts` (vector): Number of connections by type (dimension: connType)
- `goto_tcp_conn_counts` (vector): Number of TCP connections by type (dimension: tcpType)
- `goto_total_conns_by_targets` (vector): Total client connections by targets (dimension: target)
- `goto_active_conn_counts_by_targets` (gauge): Number of active client connections by targets (dimension: target)

</details>
<br/>

`Goto` tracks request counts by various dimensions for validation usage, exposed via API `/stats`:

<details>
<summary>List of server stats tracked by goto</summary>

- `requestCountsByHeaders`
- `requestCountsByURIs`
- `requestCountsByURIsAndStatus`
- `requestCountsByURIsAndHeaders`
- `requestCountsByURIsAndHeaderValues`
- `requestCountsByHeadersAndStatus`
- `requestCountsByHeaderValuesAndStatus`
- `requestCountsByPortAndURIs`
- `requestCountsByPortAndHeaders`
- `requestCountsByPortAndHeaderValues`
- `requestCountsByURIsAndProtocol`

</details>

#### Metrics APIs
|METHOD|URI|Description|
|---|---|---|
| GET   | /metrics       | Custom metrics in prometheus format |
| GET   | /metrics/go    | Go VM metrics in prometheus format |
| POST  | /metrics/clear | Clear custom metrics |
| GET   | /stats         | Server counts |
| POST  | /stats/clear   | Clear server counts |


<br/>

See [Metrics Example](docs/metrics-example.md)

###### <small> [Back to TOC](#toc) </small>

# <a name="listeners"></a>

## > Listeners

#### Features
- The server starts with a bootstrap http listener When startup arg `--ports` is used, the first port in the list is treated as the bootstrap port, forced to be an HTTP port, and isn't allowed to be managed via listeners APIs.
- The `listeners APIs` let you manage/open/close an arbitrary number of HTTP/HTTPS/TCP/TCP+TLS/GRPC listeners (except the default bootstrap listener that is set as HTTP and cannot be modified). 
- All HTTP listener ports respond to the same set of API calls, so any of the admin APIs described in this document as well as any arbitrary URI call for runtime traffic can be done via any active HTTP listener. 
- Any of the TCP operations described in the TCP section can be performed on any active TCP listener, and any of the GRPC operations can be performed on any GRPC listener. 
- The HTTP listeners perform double duty of also acting as GRPC listeners, but listeners explicitly configured as `GRPC` act as `GRPC-only` and don't support HTTP operations. See `GRPC` section later in this doc for details on GRPC operations supported by `goto`.
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
  - Serve GRPC traffic over TLS
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

> &#x1F4DD; <small> See TCP and GRPC Listeners section later for details of TCP or GRPC features </small>

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
| protocol | string | `http`, `http1`, `https`, `https1`, `grpc`, `grpcs`, `tcp`, or `tls`. Protocol `tls` implies TCP+TLS and `grpcs` implies GRPC+TLS as opposed to `tcp` and `grpc` being plain-text versions. |
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
- `GRPC Listener Started`
</details>

See [Listeners Example](docs/listeners-example.md)

###### <small> [Back to TOC](#toc) </small>


# <a name="listener-label"></a>

## > Listener Label

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

###### <small> [Back to TOC](#toc) </small>

# <a name="tcp-server"></a>
## > TCP Server

#### TCP Listeners
- A TCP listener can be created using protocol `tcp` (plain TCP) or `tls` (TCP+TLS). TCP listeners can be opened at startup via command line arg as well as at runtime via admin APIs.
- Startup TCP listeners are opened with default TCP config (described below). Admin APIs allow reconfiguring TCP listeners with additional configs using the listener's `tcp` config JSON schema. Parameters such as listener mode, read/write/idle timeouts, connection lifetime, packet sizes, etc. can be configured. 

#### Server TCP Modes
A TCP listener can operate in 6 different modes to facilitate different kinds of testing: `Payload`, `Echo`, `Stream`, `Payload Validation`, `Conversation`, `Silent Life` and `Close At First Byte`. 

- <strong>Mode: `SilentLife`</strong>
  - If the listener is configured with a `connectionLife` that limits its lifetime, the listener operates in `SilentLife` mode
  - The listener waits for the configured lifetime and closes the client connection. It receives and counts the bytes received, but never responds. 
- <strong>Mode: `CloseAtFirstByte`</strong>
  - If the listener's `connectionLife` is set to zero, the listener operates in `CloseAtFirstByte` mode 
  - The listener waits for the first byte to arrive and then closes the client connection.
- <strong>Mode: `Echo`</strong>
  - The listener echoes back the bytes received from the client. 
  - Field `echoResponseSize` configures the echo buffer size, which is the number of bytes that the listener will need to receive from the client before echoing back. If more data is received than the `echoResponseSize`, it'll echo multiple chunks each of `echoResponseSize` size. 
  - Field `echoResponseDelay` configures the delay server should apply before sending each echo response packet.
  - In `echo` mode, the connection enforces `readTimeout` and `connIdleTimeout` based on the activity: any new bytes received reset the read/idle timeouts. It applies `writeTimeout` when sending the echo response to the client. 
  - If `connectionLife` is set, it controls the overall lifetime of the connection and the connection will close upon reaching the max life regardless of the activity.
- <strong>Mode: `Stream`</strong>
  - The listener starts streaming TCP bytes per the given configuration as soon as a client connects.
  - None of the timeouts or max life applies in streaming mode, and the client connection closes automatically once the streaming completes.
  - The stream behavior is controlled via the following fields: `streamPayloadSize`, `streamChunkSize`, `streamChunkCount`, `streamChunkDelay`, `streamDuration`. Not all of these fields are required, and a combination of some may lead to ambiguity that the server resolves by picking the most sensible combinations of these config params.
- <strong>Mode: `Payload`</strong>
  - The listener serves a set of pre-configured response payload(s) with an optional `responseDelay`.
  - If more than one payload is configured in `responsePayloads` array, the `responseDelay` gets applied before sending each item in the array. 
  - Field `respondAfterRead` controls whether response should be sent immediately or if a read should be performed before sending the response, in which case at least 1 byte must be received from the client before the response(s) are sent. 
  - Field `keepOpen` determines whether the connection is kept open after sending the last item in the array. 
  - If no `connectionLife` is configured explicitly, the connection life defaults to `30s` in this mode, and the connection is kept open for the remaining lifetime (computed from the start of request). 
  - Note that in this mode, the server keeps the connection open even if the client preemptively closes the connection.
- <strong>Mode: `Payload Validation`</strong>
  - The client should first set the payload expectation by calling either `/server/tcp/{port}/expect/payload/{length}` or `/server/tcp/{port}/expect/payload/{length}`, depending on whether server should just validate payload length or the payload content.
  - The server then waits for the duration of the connection lifetime (if not set explicitly for the listener, this feature defaults to `30s` of total connection life), and buffers bytes received from the client.
  - If at any point during the connection life the number of received bytes exceed the expected payload length, the server responds with error and closes connection.
  - If at the end of the connection life, the number of bytes match the payload expectations (either length or both length and content), then the server responds with a success message. The messages returned by the server are one of the following:
    - `[SUCCESS]: Received payload matches expected payload of length [l] on port [p]`
    - `[ERROR:EXCEEDED] - Payload length [l] exceeded expected length [e] on port [p]`
    - `[ERROR:CONTENT] - Payload content of length [l] didn't match expected payload of length [e] on port [p]`
    - `[ERROR:TIMEOUT] - Timed out before receiving payload of expected length [l] on port [p]`
- <strong>Mode: `Conversation`</strong>
  - The listener waits for the client to send a TCP payload with text `HELLO` to which server also responds back with `HELLO`.
  - All subsequent packets from the client should follow the format `BEGIN/`{text}`/END`, and the server echoes the received text back in the format of `ACK/`{text}`/END`.
  - The client can initiate connection closure by sending text `GOODBYE`, or else the connection can close based on various timeouts and connection lifetime config.

In all cases, the client may close the connection proactively causing the ongoing operation to abort.


#### APIs
###### <small>* TCP configuration APIs are always invoked via an HTTP listener, not on the TCP port that's being configured. </small>


|METHOD|URI|Description|
|---|---|---|
| POST, PUT  | /server/tcp/`{port}`/configure   | Reconfigure details of a TCP listener without having to close and restart. Accepts TCP Config JSON as payload. |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/read={duration}  | Set TCP read timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/write={duration}  | Set TCP write timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/idle={duration}  | Set TCP connection idle timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/connection/set<br/>/life={duration}  | Set TCP connection lifetime duration for the port (applies to all TCP connection modes except streaming) |
| POST, PUT  | /server/tcp/`{port}`/echo<br/>/response/set<br/>/delay={duration}  | Set response delay for TCP echo mode for the listener |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/payload={payloadSize}<br/>/duration={duration}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with the given total payload size delivered over the given duration with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/chunksize={chunkSize}<br/>/duration={duration}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with chunks of the given chunk size delivered over the given duration with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/chunksize={chunkSize}<br/>/count={chunkCount}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with total chunks matching the given chunk count of the given chunk size delivered with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`<br/>/expect/payload<br/>/length={length}  | Set expected payload length for payload verification mode (to only validate payload length, not content) |
| POST, PUT  | /server/tcp/`{port}`<br/>/expect/payload  | Set expected payload for payload verification mode, to validate both payload length and content. Expected payload must be sent as request body. |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/validate=`[y/n]` | Enable/disable payload validation mode on a port to support payload length/content validation over connection lifetime (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/stream=`[y/n]`  | Enable or disable streaming on a port without having to restart the listener (useful to disable streaming while retaining the stream configuration) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/echo=`[y/n]` | Enable/disable echo mode on a port to let the port be tested in silent mode (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/conversation=`[y/n]}` | Enable/disable conversation mode on a port to support multiple packets verification (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/silentlife=`[y/n]` | Enable/disable silent life mode on a port (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/closeatfirst=`[y/n]` | Enable/disable `close at first byte` mode on a port (see overview for details) |
| POST, PUT  | /server/tcp/{port}/set/payload=`{enable}` | Enable/disable `payload` mode on a port, allowing for tcp connection to serve a pre-configured payload and close the connection (see overview for details) |
| GET  | /server/tcp/`{port}`/active | Get a list of active client connections for a TCP listener port |
| GET  | /server/tcp/active | Get a list of active client connections for all TCP listener ports |
| GET  | /server/tcp/`{port}`<br/>/history/{mode} | Get history list of client connections for a TCP listener port for the given mode (one of the supported modes given as text: `SilentLife`, `CloseAtFirstByte`, `Echo`, `Stream`, `Conversation`, `PayloadValidation`) |
| GET  | /server/tcp/`{port}`/history | Get history list of client connections for a TCP listener port |
| GET  | /server/tcp/history/{mode} | Get history list of client connections for all TCP listener ports for the given mode (see above) |
| GET  | /server/tcp/history | Get history list of client connections for all TCP listener ports |
| POST  | /server/tcp/`{port}`<br/>/history/clear | Clear history of client connections for a TCP listener port |
| POST  | /server/tcp/history/clear | Clear history of client connections for all TCP listener ports |

<br/>
<details>
<summary>TCP Config JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| readTimeout | duration | Read timeout to apply when reading data sent by the client. |
| writeTimeout | duration | Write timeout to apply when sending data to the client. |
| connectTimeout | duration | Max period that the server will wait during connection handshake. |
| connIdleTimeout | duration | Max period of inactivity (no bytes traveled) on the connection that would trigger closure of the client connection. |
| connectionLife | duration | Max lifetime after which the client connection will be terminated proactively by the server. |
| keepOpen | bool | Controls whether the server should keep the connection open after sending the response. Currently this configuration is only used in `Payload` mode where the server is configured to send a set of pre-configured payloads. |
| payload | bool | Controls whether the listener should operate in `Payload` mode. |
| stream | bool | Controls whether the listener should operate in `Stream` mode. |
| echo | bool | Controls whether the listener should operate in `Echo` mode. |
| conversation | bool | Controls whether the listener should operate in `Conversation` mode. |
| silentLife | bool | Controls whether the listener should operate in `SilentLife` mode. |
| closeAtFirstByte | bool | Controls whether the listener should operate in `CloseAtFirstByte` mode. |
| validatePayloadLength | bool | Controls whether the listener should operate in `Payload Validation` mode for length. |
| validatePayloadContent | bool | Controls whether the listener should operate in `Payload Validation` mode for both content and length. |
| expectedPayloadLength | int | Set the expected payload length explicitly for length verification. Also used to auto-store the expected payload content length when validating content. See API for providing expected payload content. |
| echoResponseSize | int | Configures the size of payload to be echoed back to the client. Server will only echo back when it has these many bytes received from the client. |
| echoResponseDelay | duration | Delay to be applied when sending response back to the client in echo mode. |
| responsePayloads | []string | A list of payloads to be used in `Payload` mode. When more than payloads are configured, each is sent in succession after applying the `responseDelay`. |
| responseDelay | duration | Delay to apply before sending each response payload in `Payload` mode. |
| respondAfterRead | bool | In `Payload` mode, this field controls whether the server should start sending payloads immediately or after waiting to receive at least one byte from the client. |
| streamPayloadSize | int | Configures the total payload size to be streamed via chunks if streaming is enabled for the listener. |
| streamChunkSize | int | Configures the size of each chunk of data to stream if streaming is enabled for the listener. |
| streamChunkCount | int | Configures the total number of chunks to stream if streaming is enabled for the listener. |
| streamChunkDelay | duration | Configures the delay to be added before sending each chunk back if streaming is enabled for the listener. |
| streamDuration | duration | Configures the total duration of stream if streaming is enabled for the listener. |

</details>

<details>
<summary>TCP Events</summary>

- `TCP Configuration Rejected`
- `TCP Configured`
- `TCP Connection Duration Configured`
- `TCP Streaming Configured`
- `TCP Expected Payload Configured`
- `TCP Payload Validation Configured`
- `TCP Mode Configured`
- `TCP Connection History Cleared`
- `New TCP Client Connection`
- `TCP Client Connection Closed`

</details>

See [TCP Example](docs/tcp-example.md)

###### <small> [Back to TOC](#toc) </small>


# <a name="grpc-server"></a>

## > GRPC Server

All HTTP ports that a `goto` instance listens on (including bootstrap port) support both `HTTP/2` and `GRPC` protocol. Any listener that's created with protocol `grpc` works exclusively in `grpc` mode, not supporting HTTP requests and only responding to the GRPC operations described below.

### GRPC Operations

All `grpc` operations exposed by `goto` produce the following proto message as output:

```
message Output {
  string payload = 1;
  string at = 2;
  string gotoHost = 3;
  int32  gotoPort = 4;
  string viaGoto = 5;
}
```

The GRPC response from `goto` also carries the following headers:

- `Goto-Host`
- `Via-Goto`
- `Goto-Protocol`
- `Goto-Port`
- `Goto-Remote-Address`

`Goto` exposes the following `grpc` operations:

1. `Goto.echo`: This is a unary grpc service method that echoes back the given payload with some additional metadata and headers. The `echo` input message is given below. It responds with a single instance of `Output` message described later.
   ```
   message Input {
     string payload = 1;
   }
   ```
2. `Goto.streamOut`: This is a server streaming service method that accepts a `StreamConfig` input message allowing the client to configure the parameters of stream response. It responds with `chunkCount` number of `Output` messages, each output carrying a payload of size `chunkSize`, and there is `interval` delay between two output messages.
   ```
   message StreamConfig {
     int32  chunkSize = 1;
     int32  chunkCount = 2;
     string interval = 3;
     string payload = 4;
   }
   ```
3. `Goto.streamInOut`: This is a bi-directional streaming service method that accepts a stream of `StreamConfig` input messages as described in `streamOut` operation above. Each input `StreamConfig` message requests the server to send a stream response based on the given stream config. For each input message, the service responds with `chunkCount` number of `Output` messages, each output carrying a payload of size `chunkSize`, and there is `interval` delay between two output messages.

<br/>
<details>
<summary> GRPC Tracking Events </summary>

- `GRPC Server Started`
- `GRPC Server Stopped`
- `GRPC Listener Started`
- `GRPC.echo`
- `GRPC.streamOut.start`
- `GRPC.streamOut.end`
- `GRPC.streamInOut.start`
- `GRPC.streamInOut.end`

</details>

See [GRPC Server Example](docs/grpc-server-example.md)

###### <small> [Back to TOC](#toc) </small>


# <a name="request-headers-tracking"></a>

## > Request Headers Tracking

This feature allows tracking request counts by headers.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /server/request/headers<br/>/track/clear									| Remove all tracked headers |
|PUT, POST| /server/request/headers<br/>/track/add/`{headers}`					| Add headers to track |
|PUT, POST|	/server/request/headers<br/>/track/`{headers}`/remove				| Remove given headers from tracking |
|GET      | /server/request/headers<br/>/track/`{header}`/counts				| Get counts for a tracked header |
|PUT, POST| /server/request/headers<br/>/track/counts<br/>/clear/`{headers}`	| Clear counts for given tracked headers |
|POST     | /server/request/headers<br/>/track/counts/clear						| Clear counts for all tracked headers |
|GET      | /server/request/headers<br/>/track/counts									| Get counts for all tracked headers |
|GET      | /server/request/headers/track									      | Get list of tracked headers |


<br/>
<details>
<summary> Request Headers Tracking Events </summary>

- `Tracking Headers Added`
- `Tracking Headers Removed`
- `Tracking Headers Cleared`
- `Tracked Header Counts Cleared`

</details>

<details>
<summary>Request Headers Tracking API Examples</summary>

```
curl -X POST localhost:8080/server/request/headers/track/clear

curl -X PUT localhost:8080/server/request/headers/track/add/x,y

curl -X PUT localhost:8080/server/request/headers/track/remove/x

curl -X POST localhost:8080/server/request/headers/track/counts/clear/x

curl -X POST localhost:8080/server/request/headers/track/counts/clear

curl -X POST localhost:8080/server/request/headers/track/counts/clear

curl localhost:8080/server/request/headers/track
```

</details>

<details>
<summary>Request Header Tracking Results Example</summary>
<p>

```
$ curl localhost:8080/server/request/headers/track/counts

{
  "x": {
    "requestCountsByHeaderValue": {
      "x1": 20
    },
    "requestCountsByHeaderValueAndRequestedStatus": {
      "x1": {
        "418": 20
      }
    },
    "requestCountsByHeaderValueAndResponseStatus": {
      "x1": {
        "418": 20
      }
    }
  },
  "y": {
    "requestCountsByHeaderValue": {
      "y1": 20
    },
    "requestCountsByHeaderValueAndRequestedStatus": {
      "y1": {
        "418": 20
      }
    },
    "requestCountsByHeaderValueAndResponseStatus": {
      "y1": {
        "418": 20
      }
    }
  }
}
```

</p>
</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="request-timeout-tracking"></a>
## > Request Timeout Tracking
This feature allows tracking request timeouts by headers.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /server/request/timeout<br/>/track/headers/`{headers}`  | Add one or more headers. Requests carrying these headers will be tracked for timeouts and reported |
|PUT, POST| /server/request<br/>/timeout/track/all                | Enable request timeout tracking for all requests |
|POST     |	/server/request<br/>/timeout/track/clear              | Clear timeout tracking configs |
|GET      |	/server/request<br/>/timeout/status                   | Get a report of tracked request timeouts so far |


<br/>
<details>
<summary> Request Timeout Tracking Events </summary>

- `Timeout Tracking Headers Added`
- `All Timeout Tracking Enabled`
- `Timeout Tracking Headers Cleared`
- `Timeout Tracked`

</details>

<details>
<summary>Request Timeout Tracking API Examples</summary>

```
curl -X POST localhost:8080/server/request/timeout/track/headers/x,y

curl -X POST localhost:8080/server/request/timeout/track/headers/all

curl -X POST localhost:8080/server/request/timeout/track/clear

curl localhost:8080/server/request/timeout/status
```

</details>

<details>
<summary>Request Timeout Status Result Example</summary>
<p>

```
{
  "all": {
    "connectionClosed": 1,
    "requestCompleted": 0
  },
  "headers": {
    "x": {
      "x1": {
        "connectionClosed": 1,
        "requestCompleted": 5
      },
      "x2": {
        "connectionClosed": 1,
        "requestCompleted": 4
      }
    },
    "y": {
      "y1": {
        "connectionClosed": 0,
        "requestCompleted": 2
      },
      "y2": {
        "connectionClosed": 1,
        "requestCompleted": 4
      }
    }
  }
}
```

</p>
</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="uris"></a>
## > URIs
This feature allows responding with custom status code and delays for specific URIs, and tracking request counts for calls made to specific URIs (ignoring query parameters). URIs can be specified with `*` suffix to match all request URIs carrying the given URI as a prefix.
Note: To configure a `goto` server to respond with custom/random response payloads for specific URIs, see [`Response Payload`](#server-response-payload) feature.

#### URI APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     |	/server/request/uri<br/>/set/status=`{status:count}`?uri=`{uri}` | Set forced response status to respond with for a URI, either for all subsequent calls until cleared, or for a specific number of subsequent calls. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. |
|POST     |	/server/request/uri<br/>/set/delay=`{delay:count}`?uri=`{uri}` | Set forced delay for a URI, either for all subsequent calls until cleared, or for a specific number of subsequent calls. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range each time. |
|GET      |	/server/request<br/>/uri/counts                     | Get request counts for all URIs |
|POST     |	/server/request<br/>/uri/counts/enable              | Enable tracking request counts for all URIs |
|POST     |	/server/request<br/>/uri/counts/disable             | Disable tracking request counts for all URIs |
|POST     |	/server/request<br/>/uri/counts/clear               | Clear request counts for all URIs |
|GET     |	/server/request/uri               | Get current configurations for all configured URIs |


<br/>
<details>
<summary> URIs Events </summary>

- `URI Status Configured`
- `URI Status Cleared`
- `URI Delay Configured`
- `URI Delay Cleared`
- `URI Call Counts Cleared`
- `URI Call Counts Enabled`
- `URI Call Counts Disabled`
- `URI Delay Applied`
- `URI Status Applied`

</details>

<details>
<summary>URI API Examples</summary>

```
curl -X POST localhost:8080/server/request/uri/set/status=418,401,404:2?uri=/foo

curl -X POST localhost:8080/server/request/uri/set/delay=1s-3s:2?uri=/foo

curl localhost:8080/server/request/uri/counts

curl -X POST localhost:8080/server/request/uri/counts/enable

curl -X POST localhost:8080/server/request/uri/counts/disable

curl -X POST localhost:8080/server/request/uri/counts/clear
```

</details>

<details>
<summary>URI Counts Result Example</summary>
<p>

```
{
  "/debug": 18,
  "/echo": 5,
  "/foo": 4,
  "/foo/3/bar/4": 10,
  "/foo/4/bar/5": 10
}
```

</p>
</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="probes"></a>

## > Probes

This feature allows setting readiness and liveness probe URIs, statuses to be returned for those probes, and tracking counts for how many times the probes have been called. `Goto` also tracks when the probe call counts overflow, keeping separate overflow counts. A `goto` instance can be queried for its probe details via `/probes` API.

The probe URIs response includes the request headers echoed back with `Readiness-Request-` or `Liveness-Request-` prefixes, and include the following additional headers:

- `Readiness-Request-Count` and `Readiness-Overflow-Count` for `readiness` probe calls
- `Liveness-Request-Count` and `Liveness-Overflow-Count` for `liveness` probe calls

By default, liveness probe URI is set to `/live` and readiness probe URI is set to `/ready`.

When the server starts shutting down, it waits for a configured grace period (default 5s) to serve existing traffic. During this period, the server will return 404 for the readiness probe if one is configured.

#### Probes APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /probes/readiness<br/>/set?uri=`{uri}` | Set readiness probe URI. Also clears its counts. If not explicitly set, the readiness URI is set to `/ready`.  |
|PUT, POST| /probes/liveness<br/>/set?uri=`{uri}` | Set liveness probe URI. Also clears its counts If not explicitly set, the liveness URI is set to `/live`. |
|PUT, POST| /probes/readiness<br/>/set/status=`{status}` | Set HTTP response status to be returned for readiness URI calls. Default 200. |
|PUT, POST| /probes/liveness<br/>/set/status=`{status}` | Set HTTP response status to be returned for liveness URI calls. Default 200. |
|POST| /probes/counts/clear               | Clear probe counts URIs |
|GET      | /probes                    | Get current config and counts for both probes |


<br/>
<details>
<summary>Probes API Examples</summary>

```
curl -X POST localhost:8080/probes/readiness/set?uri=/ready

curl -X POST localhost:8080/probes/liveness/set?uri=/live

curl -X PUT localhost:8080/probes/readiness/set/status=404

curl -X PUT localhost:8080/probes/liveness/set/status=200

curl -X POST localhost:8080/probes/counts/clear

curl localhost:8080/probes
```

</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="requests-filtering"></a>
## > Requests Filtering

This feature allows bypassing or ignoring some requests based on URIs and Headers match. A status code can be configured to be sent for ignored/bypassed requests. While both `bypass` and `ignore` filtering results in requests skipping additional processing, `bypass` requests are still logged whereas `ignored` requests don't generate any logs. Request counts are tracked for both bypassed and ignored requests.

* Ignore and Bypass configurations are not port specific and apply to all ports.
* APIs for Bypass and Ignore are alike and listed in a single table below. The two feature APIs only differ in the prefix `/server/request/bypass` vs `/server/request/ignore`
* For URI matches, prefix `!` can be used for negative matches. Negative URI matches are treated with conjunction (`AND`) whereas positive URI matches are treated with disjunction (`OR`). A URI gets filtered if: 
    * It matches any positive URI filter
    * It doesn't match all negative URI filters
* When `/` is configured as URI match, base URL both with and without `/` are matched

#### Request Ignore/Bypass APIs

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/add?uri=`{uri}`       | Filter (ignore or bypass) requests based on uri match, where uri can be a regex. `!` prefix in the URI causes it to become a negative match. |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/add/header/`{header}`  | Filter (ignore or bypass) requests based on header name match |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/add/header/`{header}`=`{value}`  | Filter (ignore or bypass) requests where the given header name as well as the value matches, where value can be a regex. |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/remove?uri=`{uri}`    | Remove a URI filter config |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/remove/header<br/>/`{header}`    | Remove a header filter config |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/remove/header<br/>/`{header}`=`{value}`    | Remove a header+value filter config |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`<br/>/set/status=`{status}` | Set status code to be returned for filtered URI requests |
|GET      |	/server/request<br/>/`[ignore\|bypass]`/status              | Get current ignore or bypass status code |
|PUT, POST| /server/request<br/>/`[ignore\|bypass]`/clear               | Remove all filter configs |
|GET      |	/server/request<br/>/`[ignore\|bypass]`/count               | Get ignored or bypassed request count |
|GET      |	/server/request<br/>/`[ignore\|bypass]`                     | Get current ignore or bypass configs |


<br/>
<details>
<summary>Request Filter (Ignore/Bypass) Events</summary>

- `Request Filter Added`
- `Request Filter Removed`
- `Request Filter Status Configured`
- `Request Filters Cleared`

</details>

<details>
<summary>Request Filter (Ignore/Bypass) API Examples</summary>

```
#all APIs can be used for both ignore and bypass

curl -X POST localhost:8080/server/request/ignore/clear
curl -X POST localhost:8080/server/request/bypass/clear

#ignore all requests where URI has /foo prefix
curl -X PUT localhost:8080/server/request/ignore/add?uri=/foo.*

#ignore all requests where URI has /foo prefix and contains bar somewhere
curl -X PUT localhost:8080/server/request/ignore/add?uri=/foo.*bar.*

#ignore all requests where URI does not have /foo prefix
curl -X POST localhost:8080/server/request/ignore/add?uri=!/foo.*

#ignore all requests that carry a header `foo` with value that has `bar` prefix
curl -X PUT localhost:8080/server/request/ignore/add/header/foo=bar.*

curl -X PUT localhost:8080/server/request/bypass/add/header/foo=bar.*

curl -X PUT localhost:8080/server/request/ignore/remove?uri=/bar
curl -X PUT localhost:8080/server/request/bypass/remove?uri=/bar

#set status code to use for ignore and bypass requests
curl -X PUT localhost:8080/server/request/ignore/set/status=418
curl -X PUT localhost:8080/server/request/bypass/set/status=418

curl localhost:8080/server/request/ignore
curl localhost:8080/server/request/bypass

```

</details>

<details>
<summary>Ignore Result Example</summary>
<p>

```
$ curl localhost:8080/server/request/ignore
{
  "uris": {
    "/foo": {}
  },
  "headers": {
    "foo": {
      "bar.*": {}
    }
  },
  "uriUpdates": {
    "/ignoreme": {}
  },
  "headerUpdates": {},
  "status": 200,
  "filteredCount": 1,
  "pendingUpdates": true
}
```

</p>
</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="response-delay"></a>
## > Response Delay
This feature allows adding a delay to all requests except bypass URIs and proxy requests. Delay is specified as duration, e.g. 1s. 

Delay is not applied to the following requests:
- `Goto` admin calls and
- Delay API `/delay`

When a delay is applied to a request, the response carries a header `Response-Delay` with the value of the applied delay.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/delay/set/{delay} | Set a delay for non-management requests (i.e. runtime traffic). `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range each time. |
| PUT, POST | /server/response<br/>/delay/clear       | Remove currently set delay |
| GET       |	/server/response/delay             | Get currently set delay |


<br/>
<details>
<summary>Response Delay Events</summary>

- `Delay Configured`
- `Delay Cleared`
- `Response Delay Applied`: generated when a configured response delay is applied to requests not explicitly asking for a delay, i.e. not generated for `/delay` API call.

</details>

<details>
<summary>Response Delay API Examples</summary>

```
curl -X POST localhost:8080/server/response/delay/clear

curl -X PUT localhost:8080/server/response/delay/set/1s-3s

curl localhost:8080/server/response/delay
```

</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="server-response-headers"></a>
## > Response Headers
This feature allows adding custom response headers to all responses sent by the server.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/headers/add<br/>/`{header}`=`{value}`  | Add a custom header to be sent with all responses |
| PUT, POST | /server/response<br/>/headers/remove/`{header}`       | Remove a previously added custom response header |
| POST      |	/server/response<br/>/headers/clear                 | Remove all configured custom response headers |
| GET       |	/server/response/headers                       | Get list of configured custom response headers |

<br/>
<details>
<summary>Response Headers Events</summary>

- `Response Header Added`
- `Response Header Removed`
- `Response Header Cleared`

</details>

<details>
<summary>Response Headers API Examples</summary>

```
curl -X POST localhost:8080/server/response/headers/clear

curl -X POST localhost:8080/server/response/headers/add/x=x1

curl -X POST localhost:8080/server/response/headers/remove/x

curl localhost:8080/server/response/headers
```

</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="response-payload"></a>

## > Response Payload

This feature lets you configure a `goto` instance to respond to arbitrary URIs with custom payloads based on various match criteria. This enables `goto` to be used as a stand-in server in any test scenario. The custom payloads are only applied to runtime traffic (going against arbitrary URIs), not to admin APIs that `goto` exposes for itself.

#### Payload Types
There are 4 possibilities for response payloads:
1. Configure `goto` to generate a random text of any size
2. Upload a custom static payload of any format, including binary content (e.g. JSON, YAML, Image, Zip/Tar, etc.)
3. Configure a payload template and JSON/text transformation rules. `Goto` will apply the transformations against the incoming request and augment the payload template to generate a dynamic payload. 
4. Configure JSON/text transformations to be applied against request body instead of using a custom payload template. The response payload becomes a derivative of the request payload.

#### Request Processing Path
- When `goto` receives a request, it first determines if this is an admin API call or some arbitrary URI. Admin APIs take their pre-determined path to configure goto
- If not an admin API, goto checks if the request is for a known traffic feature offered by `goto`, e.g. `/delay`, probes, etc. These APIs serve specific purpose and don't serve custom responses.
- If the request is not a known API, `goto` checks if the request is meant to be tunneled, proxied, or bypassed. Tunneled and proxied requests get forwarded to their intended upstream destinations, whereas bypassed/ignored requests are responded to with a minimal response.
- If no tunnel or proxy config found for the request, `goto` checks if there is a custom payload and custom response status code configured for the request (based on URI, headers, query params, etc). If found, the custom response/status is served.
- If no custom config is found for the request, the request is served by a `catch-all` response that echoes back some request details along with some useful server info.
- If custom response configuration is found for a request, `goto` applies any defined transformations if needed and responds with the custom payload along with any custom response status.

#### Request Matching
Custom response payload can be set for any of the following request categories, listed in the order of precedence. The highest precedence match gets applied to build the response payload.

1. URI + headers combination match
2. URI + query combination match
3. URI + body keywords combination match
4. URI + body JSON paths match
5. URI match
6. Headers match
7. Query match
8. `Default` payload, if configured

URIs can be specified with `*` suffix to capture all requests that match the given prefix. For example, `/foo*` matchs `/foo`, `/fooxyz`, `/foo/xyz`, `/foo/xyz?bar=123`, etc.

#### Default payload with Auto-generation
- A default payload (lowest precedence in the list above) gets applied to all URIs that don't find any other match. 
- API `/server/response<br/>/payload/set/default` is used to upload a default custom payload.
- Alternatively, API `/server/response/payload/set/default/{size}` can be used to ask `goto` to auto-generate a random text of the given size and serve it as default payload.
- If both the above APIs are called to both set a custom default payload as well as set a size for the default payload, the custom payload will be adjusted to match the given size (by either trimming the custom payload or appending more characters to it). 
- Payload size can be a numeric value or use common byte size conventions: `K`, `KB`, `M`, `MB`.
- There is no limit on the payload size as such, it's only limited by the memory available to the `goto` process.

#### Payload transformations 
<details>
<summary> &#x1F525; Complex stuff, open at your own risk &#x1F525; </summary>
- API `/server/response/payload/transform` allows configuring payload transformation rules for a given URI.
- Transformation can work off `JSON` and `YAML` request payloads. Payload template must be defined in `JSON` format.
- Multiple transformation sets can be defined for a URI, which are applied in sequence until one of them performs an update for the request. 

A transformation definition has two fields: `payload` and `mappings`. 
- If a transformation is defined with an accompanying payload, the mappings are used to extract data from request payload and applied to this payload, and this payload is served as response.
- If a transformation is defined without a payload, the mappings are used to transform the request payload and the request payload is served back as response.

Each transformation spec can contain multiple mappings. A mapping carries the following fields:
- `source`: This field contains a path separated by `.` that identifies a field in the request payload. For arrays, numeric indexes can be used as a key in the path. For example, path `a.0.b.1` means `payload["a"][0]["b"][1]`. This field is required for the transformation to take effect. A mapping with missing `source` is ignored.
- `target`: This field is optional, and if not given then `source` path is also used as target. The `target` field has dual behavior:
    - If it contains a dot-separated path, it identifies the target field where the value extracted from the source field is applied
    - If it contains a capture pattern using syntax `{text}`, all occurrences of `text` in the target payload are replaced with the value extracted from the source field. A pattern of `{{text}}` causes it to look for and replace all occurrences of `{text}`
- `ifContains` and `ifNotContains`: These fields provide text that is matched against the source field, and the mapping is applied only if the source field contains or doesn't contain the given text correspondingly.
- `mode`: This field can contain one the following values to dictate how the source value is applied to the target field. All these modes can cause a change in the field's type.
  - `replace`: replace the current value of the target field with the source value.
  - `join`: join the current value of the target field with the source value using text concatenation (only makes sense for text fields). 
  - `push`: combine the source value with the target field's current value(s) making it an array (if not already an array), inserting the source value before the given index (or head).
  - `append` (default): combine the source value with the target field's current value(s) making it an array (if not already an array), inserting the source value after the given index (or tail).
- `value`: The `value` field provides a default value to use instead of the source value. For request payload transformation (no payload template given), the `value` field is used as primary value and source field is used as fallback value. For payload template transformation, source field is used as primary value and the given value is used as fallback.

</details>

#### Capturing values from the request to use in the response payload
<details>
<summary>&#x1F525; Complex stuff, open at your own risk &#x1F525; </summary>

 To capture a value from URI, Header, Query or Body JSON Path, use the `{var}` syntax in the match criteria as well as in the payload. The occurrences of `{var}` in the response payload will be replaced with the value of that var as captured from the URI/Header/Query/Body. Additionally, `{var}` allows for URIs to be specified such that some ports of the URI can vary.

 For example, for a configured response payload that matches on request URI:
 ```
 /server/response/payload/set/uri?uri=/foo/{f}/bar{b} 
  --data '{"result": "uri had foo={f}, bar={b}"}'
 ```
when a request comes for URI `/foo/hi/bar123`, the response payload will be `{"result": "uri had foo=hi, bar=123"}`

Similarly, for a configured response payload that matches on request header:
```
/server/response/payload/set/header/foo={x} --data '{"result": "header was foo with value {x}"}'
```
when a request comes with header `foo:123`, the response payload will be `{"result": "header was foo with value 123"}`

Same kind of capture can be done on query params, e.g.:
```
/server/response/payload/set/query/qq={v} --data '{"test": "query qq was set to {v}"}'
```


A combination of captures can be done from URI and Header/Query. Below example shows a capture of {x} from URI and {y} from request header:
```
/server/response/payload/set/header/bar={y}?uri=/foo/{x} --data '{"result": "URI foo with {x} and header bar with {y}"}'
```

For a request 
```
curl -H'bar:123' localhost:8080/foo/abc
```
the response payload will be `{"result": "URI foo with abc and header bar with 123"}`

Lastly, values can be captured from JSON paths that match against the request body. For example, the configuration below captures value at JSON paths `.foo.bar` into var `{a}` and at path `.foo.baz` into var `{b}` from the request body received with URI `/foo`. The captured values are injected into the response body in two places for each variable.

```
curl -v -g -XPOST localhost:8080/server/response/payload/set/body/paths/.foo.bar={a},.foo.baz={b}\?uri=/foo --data '{"bar":"{a}", "baz":"{b}", "message": "{a} is {b}"}' -HContent-Type:application/json
```

For the above config, a request like this:
```
curl localhost:8080/foo --data '{"foo": {"bar": "hi", "baz": "hello"}}'
```

produces the response: `{"bar":"hi", "baz":"hello", "message": "hi is hello"}`.

Below is a more complex example, capturing values from arrays and objects, and producing response json with arrays of different lengths based on input array lengths. The two configurations in the below example work in tandem, showing an example of how mutually exclusive configurations can provide on-the-fly `if-else` semantics based on input payload.

First config to capture `.one.two=two`, `.one.three[0]={three.1}`, `.one.three[1]={three.2}`, `.one.four[1].name={four.name}`. This response is triggered if input payload has 2 elements in array `.one.three` and two elements in array `.one.four`.

```
$ curl -g -X POST localhost:8080/server/response/payload/set/body/paths/.one.two={two},.one.three[0]={three.1},.one.three[1]={three.2},.one.four[1].name={four.name}?uri=/foo --data '{"two":"{two}", "three":["{three.1}", "{three.2}"]}, "message": "two -> {two}, three -> {three.1} {three.2}, four -> {four.name}"}' -H'Content-Type:application/json'
```

Second config to capture `.one.two=two`, `.one.three[0]={three.1}`, `.one.three[1]={three.2}`, `.one.three[2]={three.3}`, `.one.four[0].name={four.name}`. This response is triggered if input payload has 3 elements in array `.one.three` and one element in `.one.four`.

```
$ curl -g -X POST localhost:8080/server/response/payload/set/body/paths/.one.two={two},.one.three[0]={three.1},.one.three[1]={three.2},.one.three[2]={three.3},.one.four[0].name={four.name}?uri=/foo --data '{"two":"{two}", "three":["{three.1}", "{three.2}", "{three.3}"]}, "message": "two -> {two}, three -> {three.1} {three.2} {three.3}, four -> {four.name}"}' -H'Content-Type:application/json'
```

For the above two configs, this request:
```
$ curl -v localhost:8080/foo --data '{"one": {"two": "hi", "three": ["hello", "world"], "four":[{"name": "foo"},{"name":"bar"}]}}'
```
 produces the following output: `{"two":"hi", "three":["hello", "world"]}, "message": "two -> hi, three -> hello world, four -> bar"}`

And this request
```
$ curl localhost:8080/foo --data '{"one": {"two": "hi", "three": ["hello", "world", "there"], "four":[{"name": "foo"}]}}'
```
produces the following output: `{"two":"hi", "three":["hello", "world", "there"]}, "message": "two -> hi, three -> hello world there, four -> foo"}`

</details>

### Response Payload APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST | /server/response<br/>/payload/set/default  | Add a custom payload to be used for ALL URI responses except those explicitly configured with another payload |
| POST | /server/response<br/>/payload/set<br/>/default/`{size}`  | Respond with a random generated payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB |
| POST | /server/response<br/>/payload/set<br/>/default/binary  | Add a binary payload to be used for ALL URI responses except those explicitly configured with another payload. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /server/response<br/>/payload/set<br/>/default/binary/`{size}`  | Respond with a random generated binary payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /server/response<br/>/payload/set<br/>/uri?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI. URI can contain variable placeholders. |
| POST | /server/response<br/>/payload/set<br/>/header/`{header}`  | Add a custom payload to be sent for requests matching the given header name |
| POST | /server/response<br/>/payload/set/header<br/>/`{header}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and the given URI |
| POST | /server/response<br/>/payload/set/header<br/>/`{header}={value}`  | Add a custom payload to be sent for requests matching the given header name and value |
| POST | /server/response<br/>/payload/set/header<br/>/`{header}={value}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and value along with the given URI. |
| POST | /server/response<br/>/payload/set/query/`{q}`  | Add a custom payload to be sent for requests matching the given query param name |
| POST | /server/response<br/>/payload/set/query<br/>/`{q}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and the given URI |
| POST | /server/response<br/>/payload/set<br/>/query/`{q}={value}`  | Add a custom payload to be sent for requests matching the given query param name and value |
| POST | /server/response<br/>/payload/set/query<br/>/`{q}={value}`<br/>?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and value along with the given URI. |
| POST | /server/response/payload<br/>/set/body~{regex}?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of regexp (comma-separated list) in the given order (second expression in the list must appear after the first, and so on) |
| POST | /server/response/payload<br/>/set/body/paths/{paths}?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of JSON paths (comma-separated list). Match is triggered only when all JSON paths match, and the first matched config gets applied. Also see above description and example for how to capture values from JSON paths. |
| POST | /server/response<br/>/payload/transform?uri=`{uri}`  | Add payload transformations for requests matching the given URI. Payload submitted with this URI should be `Payload Transformation Schema` |
| POST | /server/response<br/>/payload/clear  | Clear all configured custom response payloads |
| GET  |	/server/response/payload | Get configured custom payloads |

<br/>

<details>
<summary> Payload Transformation Schema </summary>

#### Payload Transformation Schema
A payload transformation is defined by giving one or more path mappings and an optional payload template.

|Field|Type|Description|
|---|---|---|
| mappings | []JSONTransform | List of mappings to be applied for this transformation. |
| payload | any | If given, this is used as the payload template to be transformed. If not given, the request payload itself is transformed and sent back. |


#### Transformation Mapping Schema

|Field|Type|Description|
|---|---|---|
| source | string | Path (keys separated by `.`) to be used against source payload (request) to get the source field  |
| target | string | Path (keys separated by `.`) to be used against target payload (template or request) to get the target field |
| ifContains | string | If given, the mapping is applied only if this text exists in the source field |
| ifNotContains | string | If given, the mapping is applied only if this text doesn't exist in the source field |
| mode | string | One of: `replace`, `join`, `push`, `append`. See above for details. |
| value | any | Default value. See above for details. |


#### Port Response Payload Config Schema

This schema is used to describe currently configured response payloads for the port on which the API `/server/response/payload` is invoked

|Field|Type|Description|
|---|---|---|
| defaultResponsePayload | ResponsePayload | Default payload if configured |
| responsePayloadByURIs | string->ResponsePayload | Payloads configured for uri match. Includes both static and request transformation payloads |
| responsePayloadByHeaders | string->string->ResponsePayload | Payloads configured for headers match |
| responsePayloadByURIAndHeaders | string->string->string->ResponsePayload | Payloads configured for uri and headers match |
| responsePayloadByQuery | string->string->ResponsePayload | Payloads configured for query params match |
| responsePayloadByURIAndQuery | string->string->string->ResponsePayload | Payloads configured for uri and query params match |
| responsePayloadByURIAndBody | string->string->ResponsePayload | Payloads configured for uri and body keywords match |


#### Response Payload Config Schema

This schema is used to describe currently configured response payload, as the output of `/server/response/payload`

|Field|Type|Description|
|---|---|---|
| payload | string | Payload to serve when this configuration matches |
| contentType | string | Response content-type to use when this configuration matches |
| uriMatch | string | URI match criteria  |
| headerMatch | string | Header match criteria |
| headerValueMatch | string | Header value match criteria |
| queryMatch | string | Query param name match criteria |
| queryValueMatch | string | Query param value match criteria |
| bodyMatch | []string | Keywords to match against request body for non-transformation response payload configuration |
| uriCaptureKeys | []string | Keys to capture values from URI |
| headerCaptureKey | string | Key to capture value from headers |
| queryCaptureKey | string | Key to capture value from query params |
| transforms | []PayloadTransformation | Transformations defined in this config. See `Payload Transformation schema` |

</details>

<details>
<summary> Response Payload Events </summary>

- `Response Payload Configured`
- `Response Payload Cleared`
- `Response Payload Applied`: generated when a configured response payload is applied to a request that wasn't explicitly asking for a custom payload (i.e. not for `/payload` and `/stream` URIs).

</details>
</br>

See [Response Payload API Examples](docs/response-payload-api-examples.md)


###### <small> [Back to TOC](#toc) </small>


# <a name="ad-hoc-payload"></a>
## > Ad-hoc Payload
This URI responds with a random-generated payload of the requested size. Payload size can be a numeric value or use common byte size conventions: `K`, `KB`, `M`, `MB`. Payload size is only limited by the memory available to the `goto` process. The response carries an additional header `Goto-Payload-Length` in addition to the standard header `Content-Length` to identify the size of the response payload.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/payload/`{size}` | Respond with a payload of given size |

<br/>
<details>
<summary>Ad-hoc Payload API Example</summary>

```
curl -v localhost:8080/payload/10K

curl -v localhost:8080/payload/100

```

</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="-stream-chunked-payload"></a>

## > Stream (Chunked) Payload

This URI responds with either pre-configured or random-generated payload where response behavior is controlled by the parameters passed to the API. The feature allows requesting a custom payload size, custom response duration over which to stream the payload, custom chunk size to be used for splitting the payload into chunks, and custom delay to be used in-between chunked responses. Combination of these parameters define the total payload size and the total duration of the response.

Stream responses carry following headers:

- `Goto-Stream-Length: <total payload size>`
- `Goto-Stream-Duration: <total response duration>`
- `Goto-Chunk-Count: <total number of chunks>`
- `Goto-Chunk-Length: <per-chunk size>`
- `Goto-Chunk-Delay: <per-chunk delay>`
- `X-Content-Type-Options: nosniff`
- `Transfer-Encoding: chunked`

#### Stream APIs
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/stream/payload=`{size}`<br/>/duration={duration}<br/>/delay={delay} | Respond with a payload of the given size delivered over the given duration with the given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/duration={duration}<br/>/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered over the given duration with given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/count={count}/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered the given count of times with given delay per chunk. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/duration={duration}<br/>/delay={delay} | Respond with pre-configured default payload split into enough chunks to spread out over the given duration with given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/count={count}/delay={delay} | Respond with pre-configured default payload split into the given count of chunks with the given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |

<br/>
<details>
<summary>Stream Response API Example</summary>

```
curl -v --no-buffer localhost:8080/stream/payload=10K/duration=5s-15s/delay=100ms-1s

curl -v --no-buffer localhost:8080/stream/chunksize=100/duration=5s/delay=500ms-2s

curl -v --no-buffer localhost:8080/stream/chunksize=100/count=5/delay=200ms

curl -v --no-buffer localhost:8080/stream/duration=5s/delay=100ms-300ms

curl -v --no-buffer localhost:8080/stream/count=10/delay=300ms
```

</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="response-status"></a>
## > Response Status
This feature allows setting a forced response status for all requests except bypass URIs. Server also tracks the number of status requests received (via /status URI) and the number of responses sent per status code.

#### Response Status APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/status/set/`{status}`     | Set a forced response status that all non-proxied and non-management requests will be responded with. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. |
| PUT, POST |	/server/response<br/>/status/clear            | Remove currently configured forced response status, so that all subsequent calls will receive their original deemed response |
| PUT, POST | /server/response<br/>/status/counts/clear     | Clear counts tracked for response statuses |
| GET       |	/server/response<br/>/status/counts/`{status}`  | Get request counts for a given status |
| GET       |	/server/response<br/>/status/counts           | Get request counts for all response statuses so far |
| GET       |	/server/response/status                  | Get the currently configured forced response status |

<br/>
<details>
<summary>Response Status Events</summary>

- `Response Status Configured`
- `Response Status Cleared`
- `Response Status Counts Cleared`

</details>

<details>
<summary>Response Status API Examples</summary>

```
curl -X POST localhost:8080/server/response/status/counts/clear

curl -X POST localhost:8080/server/response/status/clear

curl -X PUT localhost:8080/server/response/status/set/502

curl -X PUT localhost:8080/server/response/status/set/0

curl -X POST localhost:8080/server/response/status/counts/clear

curl localhost:8080/server/response/status/counts

curl localhost:8080/server/response/status/counts/502
```

</details>

<details>
<summary>Response Status Tracking Result Example</summary>

<p>

```
{
  "countsByRequestedStatus": {
    "418": 20
  },
  "countsByReportedStatus": {
    "200": 15,
    "202": 4,
    "208": 5,
    "418": 20
  }
}
```

</p>
</details>

###### <small> [Back to TOC](#toc) </small>



# <a name="response-triggers"></a>
# > Response Triggers

`Goto` allows targets to be configured that are triggered based on response status. The triggers can be invoked manually for testing, but their real value is when they get triggered based on response status. Even more valuable when the request was proxied to another upstream service, in which case the trigger is based on the response status of the upstream service.

#### Triggers APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     |	/server/response<br/>/triggers/add              | Add a trigger target. See [Trigger Target JSON Schema](#trigger-target-json-schema) |
|PUT, POST| /server/response<br/>/triggers/{target}/remove  | Remove a trigger target |
|PUT, POST| /server/response<br/>/triggers/{target}/enable  | Enable a trigger target |
|PUT, POST| /server/response<br/>/triggers/{target}/disable | Disable a trigger target |
|POST     |	/server/response<br/>/triggers/`{targets}`/invoke | Invoke trigger targets by name for manual testing |
|POST     |	/server/response<br/>/triggers/clear            | Remove all trigger targets |
|GET 	    |	/server/response<br/>/triggers/counts             | Report invocation counts for all trigger targets |
|GET 	    |	/server/response/triggers             | List all trigger targets |

<br/>
<details>
<summary>Trigger Target JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| name        | string      | Name for this target |
| method      | string      | HTTP method to use for this target |
| url         | string      | URL for the target. |
| headers     | `[][]string`| request headers to send with this trigger request |
| body        | `string`    | request body to send with this trigger request |
| sendID      | bool        | Whether or not a unique ID be sent with each request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| enabled     | bool        | Whether or not the trigger is currently active |
| triggerOn   | []int       | List of response statuses for which this target will be triggered |
| startFrom   | int         | Trigger the target after these many occurrences of the trigger status codes |
| stopAt      | int         | Stop triggering the target after these many occurrences of the trigger status codes |
| statusCount | int         | (readonly) Number of occurrences of the status codes that this trigger listens on |
| triggerCount | int         | (readonly) Number of times this target has been triggered  |

</details>

<details>
<summary>Triggers Events</summary>

- `Trigger Target Added`
- `Trigger Target Removed`
- `Trigger Target Enabled`
- `Trigger Target Disabled`
- `Trigger Target Invoked`

</details>

See [Triggers Example](docs/triggers-example.md)

###### <small> [Back to TOC](#toc) </small>


# <a name="status-api"></a>
## > Status API
The URI `/status/`{status}`` allows clients to ask for a specific status as response code. The given status is reported back, except when forced status is configured in which case the forced status is sent as response.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status/`{status}` or /status=`{status}` | This call either receives the given status, or the forced response status if one is set. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE  |	/status=`{status}`<br/>/delay=`{delay}` | In addition to requesting a status as above, this API also allows a delay to be applied. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`?x-request-id=`{requestId}` | When the status param is passed in the format `<code>:<count>`, the requested response code is returned for `count` number of subsequent calls (starting from the current one) before reverting back to 200. The optional query param `x-request-id` can be used to ask for stateful status for each unique request id, allowing multiple concurrent clients to each receive its own independent stateful response. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`/delay=`{delay}`?x-request-id=`{requestId}` | Same as above, the requested response code is returned for `count` number of subsequent calls but with the given delay applied before a response. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. After `count` responses, the response status reverts to 200.  |
| GET, PUT, POST, OPTIONS, HEAD, DELETE  |	/status=`{status:count}`/flipflop?x-request-id=`{requestId}` | This call responds with the given status for the given count times when called successively with the same count value. Once the status is served `count` times, the next status served is `200`, and subsequent calls start the cycle again. Optional query param `x-request-id` can be used to perform status flip for each unique request, preventing requests from affecting one another. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`/delay=`{delay}`/flipflop?x-request-id=`{requestId}` | Same behavior as above except that the given delay duration param gets applied, allowing you to add artificial delay before responding with the given status. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET  |	/status/flipflop | Reports the current flipflop counter value, i.e. number of times the most recent status has been served. |
| POST |	/status/clear | Clears the current state of all stateful statuses recorded so far. |

<br/>
<details>
<summary>Status API Example</summary>

```
curl -I  localhost:8080/status/418

curl -I  localhost:8080/status/501,502,503

curl -v  localhost:8080/status=501,502,503/delay=100ms-1s

curl -v localhost:8080/status=503:2?x-request-id=1

curl -v localhost:8080/status=503:2/delay=1s?x-request-id=1

curl -I localhost:8080/status=503:3/flipflop?x-request-id=1

curl -v localhost:8080/status=503:3/delay=1s/flipflop?x-request-id=1

curl localhost:8080/status/flipflop

curl -XPOST localhost:8080/status/clear
```
</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="delay-api"></a>
## > Delay API
The URI `/delay/{delay}` allows clients to ask for a specific delay to be applied to the current request. The delay API is not subject to the response delay that may be configured for all responses. Calling the URI as `/delay` responds with no delay, and so does the call as `/delay/0`, `/delay/0s`, etc.
When a delay is passed to this API, the response carries a header `Response-Delay` with the value of the applied delay.

> Note: For requesting a delay along with a specific status, check the `/status` API documentation above.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/delay/`{delay}` | Responds after the given delay. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. To apply delay with a specific response status code, see `/status` API above. |

<br/>
<details>
<summary>Delay API Example</summary>

```
curl -I  localhost:8080/delay/2s
curl -v  localhost:8080/delay/100ms-2s
```
</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="echo-api"></a>
## > Echo API
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

###### <small> [Back to TOC](#toc) </small>


# <a name="catch-all"></a>

## > Catch All

Any request that doesn't match any of the defined management APIs, and also doesn't match any proxy targets, gets treated by a catch-all response that sends HTTP 200 response by default (unless an override response code is set)

###### <small> [Back to TOC](#toc) </small>

<br/>

# <a name="proxy"></a>
# Proxy

`Goto` can act as a TCP or HTTP proxy that sits between a downstream client and an upstream service, allowing you a point in the network where the communication between the downstream client and upstream service can be observed and affected.

### HTTP Proxy
- Any HTTP(S) listener that you open in `goto` is ready to act as an http proxy in addition to the other server duties it performs.
- To use an HTTP(S) listener as a proxy, all you need to do is add one or more upstream targets. The request processing path in `goto` checks for the presence of proxy targets for the listener where a request arrives. If any proxy target is defined on the listener, `goto` matches the request with those targets and forwards it to the matched targets. If no match found, the listener processes the request as a server.
- As an HTTP proxy, `goto` can perform various kind of HTTP filtering and transformations before forwarding a downstream request to the upstream service.
  - URI rewrite
  - Add/remove headers.
  - Add/remove query params.
  - Convert between HTTP and HTTPS protocols.
  - Route a single request to multiple upstream targets and send the combined responses back to the downstream client.
- Note that if you use an HTTPS listener as a proxy, `goto` will perform TLS termination and will redo TLS for upstream HTTPS endpoints. If you want a passthrough proxy, you can use a TCP listener in goto to act as a TCP proxy for HTTP traffic.

### TCP Proxy
- Any TCP listener can act as a TCP proxy if a proxy target is defined for that listener.
- A TCP proxy only supports routing to a single upstream target, except for SNI based routing for TLS connections. This is unlike an HTTP proxy that can route a single request to multiple upstream targets.
- TCP proxy is a passthrough proxy as it transmits bytes opaquely between the downstream and upstream parties. The only time the TCP proxy inspects the bytes is if you configure it to perform SNI based routing.
- You can optionally add SNI match criteria to a TCP proxy target (see the relevant APIs further below in the doc). In this case, `goto` will inspect the client TLS handshake packets to read SNI information and match it against the defined server names. The connection will be routed to the first target for which the defined server name matches client's requested SNI.
  <details>
  <summary>More details about SNI matching</summary>
  <ul style='font-size:0.95em'>
    <li>
    At the time of a new downstream connection, `goto` checks whether TCP proxy targets on this port have been configured with SNI match. If so, `goto` reads SNI from the TLS client handshake data without actually doing the handshake, and uses the SNI DNS hostname to pick an upstream endpoint to route to. The client TLS handshake data is forwarded to the upstream endpoint so the actual handshake still happens between the client and the upstream service.
    </li>
    <li>
    While inspecting the client's TLS handshake, `goto` also logs the Cipher Suites and Signature Algorithms requested by the client. However, these can only be seen in the `goto` logs for now, not exposed via any API.
    </li>
  </ul>
  </details>

### Upstream targets
Upstream targets can be defined in one of the two ways:
- By posting a JSON spec to `/proxy/targets/add` API.
- Build the upstream endpoint incrementally via multiple API calls. Benefit of this approach is that targets can be defined via just API calls without the need for a JSON payload, but it does require multiple API calls to define each target.
    <details>
      <summary>See the steps needed to define a proxy target using APIs</summary>
      
    - First add a target endpoint using API `/proxy/targets/add/{target}?url={url}`
    - Then define one or more URI routing for this endpoint using API `/proxy/targets/{target}/route?from={uri}&to={uri}`. A target should have at least one URI route defined for it to be triggered.
    - Optionally define any necessary header and query match criteria via APIs `/proxy/targets/{target}/match/[header|query]/...`
    - Optionally define any necessary header and query transformations via APIs `/proxy/targets/{target}/headers/[add|remove]/...`, and `/proxy/targets/{target}/query/[add|remove]/...`.
      
    </details>

### Proxy Target Matching
- HTTP proxy targets require URI based matching at the minimum, and additional match criteria can be defined based on HTTP headers and query params. A target can be defined to accept all requests by setting URI match to `/`.
    - URI route match is a pre-requisite for routing a call to upstream endpoints. It serves as a match criteria of its own, in `addition` to the `MatchAny` or `MatchAll` criteria of the target. 
    - So if there's a `MatchAny` criteria defined for the target as `Header=Foo` and a route defined as `/foo -> /bar`, then a request will get routed to the upstream only if it has the URI `/foo` AND the header `Foo:<any value>`.
    - The match criteria added via APIs get added with `MatchAny` semantics. If you need to use `MatchAll` semantics for a target's match criteria, you must define the target using JSON schema.
- TCP targets support SNI based matching if the communication is over TLS. Otherwise TCP proxy only supports a single upstream service.
    <details>
    <summary> &#x1F525; More detailed explanation of HTTP target matching &#x1F525; </summary>

    Proxy target match criteria are based on request URI match and optional headers and query parameters matching.
    An upstream target is defined with a URI routing table, and the upstream target gets triggered for all requests matching any of the URIs defined in the routing table. However, if you need additional filtering of requests before sending those over to the upstream endpoints, you can use the headers and query params match criteria. These criteria can be defined to either match ANY of them or ALL of them for the request to qualify.

    - URI Routing Table: this is defined as the mandatory `routes` field in the target definition. The source URI in the routing table can be specified using variables, e.g. `{foo}`, to indicate the variable portion of a URI. For example, `/foo/{f}/bar/{b}` will match URIs like `/foo/123/bar/abc`, `/foo/something/bar/otherthing`, etc. The variables are captured under the given labels (`f` and `b` in the previous example). The destination URI in the routing table can refer to those captured variables using the syntax described in this example:
      
      ```
      curl http://goto:8080/proxy/targets/add --data \
      '{"name": "target1", "endpoint":"http://somewhere", \
      "routes":{"/foo/{x}/bar/{y}": "/abc/{y:.*}/def/{x:.*}"}, \
      "enabled":true, "sendID": true}'
      ```

      This target will be triggered for requests with the pattern `/foo/<somex>/bar/<somey>` and the request will be forwarded to the target as `http://somewhere/abc/somey/def/somex`, where the values `somex` and `somey` are extracted from the original request and injected into the replacement URI.

      URI match `/` has the special behavior of matching all traffic.

    <br/>

    - Headers: specified in the `matchAll` or `matchAny` field as a list of key-value pairs, with the ability to capture values in named variables and reference those variables in the `addHeaders` list. A target is triggered if any of the headers in the match list are present in the request (headers are matched using OR instead of AND). The variable to capture header value is specified as `{foo}` and can be referenced in the `addHeaders` list again as `{foo}`. This example will make it clear:

      ```
      curl http://goto:8080/proxy/targets/add --data \
      '{"name": "target2", "endpoint":"http://somewhere", "routes":{"/": ""}, \
      "matchAll":{"headers":[["foo", "{x}"], ["bar", "{y}"]]}, \
      "addHeaders":[["abc","{x}"], ["def","{y}"]], "removeHeaders":["foo"], \
      "enabled":true, "sendID": true}'
      ```

      This target will be triggered for requests carrying headers `foo` or `bar`. On the proxied request, additional headers will be set: `abc` with value copied from `foo`, and `def` with value copied from `bar`. Also, header `foo` will be removed from the proxied request.

      So a downstream request `curl -v localhost:8080/bla/bla -H'foo:123' -H'bar:456'` gets sent to the upstream endpoint with the same URI (passthrough) but headers `'bar:456'`, `'abc:123'` and `'def:456'`.
    <br/>

    - Query: specified as a list of key-value pairs, with the ability to capture values in named variables and reference those variables in the `addQuery` list. A target is triggered if any of the query parameters in the match list are present in the request (matched using OR instead of AND). The variable to capture query parameter value is specified as `{foo}` and can be referenced in the `addQuery` list again as `{foo}`. Example:

      ```
      curl http://goto:8080/proxy/targets/add --data \
      '{"name": "target3", "endpoint":"http://somewhere", "routes":{"/": ""},\
      "matchAny":{"query":[["foo", "{x}"], ["bar", "{y}"]]}, \
      "addQuery":[["abc","{x}"], ["def","{y}"]], "removeQuery":["foo"], \
      "enabled":true, "sendID": true}'
      ```

      This target will be triggered for requests that carry either of the query params `foo` or `bar`. On the proxied request, query param `foo` will be removed, and additional query params will be set: `abc` with value copied from `foo`, and `def` with value copied from `bar`. The incoming request `http://goto:8080?foo=123&bar=456` gets proxied as `http://somewhere?abc=123&def=456&bar=456`.

    </details>

### Request Transformation
- Downstream request headers, query params, and payload gets passed to the upstream requests. Transformations can be defined per upstream endpoint, allowing for URI, headers and query params to be added/removed/replaced. This allows for the upstream requests to differ from the downstream request.

### Response
- If the request only matches a single upstream endpoint, the upstream response payload is sent as downstream response payload.
- If the request matches multiple upstream endpoints, the downstream response payload will be a wrapper containing a collection of all those upstream responses.
- Downstream response headers include all the upstream response headers combined with the `goto` headers from the proxy instance. As a result, downstream responses may contain duplicate headers or multiple values for the same header.

### Chaos
In addition to request routing, the `goto` proxy also offers some chaos features:
- A `delay` can be configured per target that's applied to all communication in either direction for that target. For HTTP proxy, the delay is applied before sending the request and response. For TCP proxy, the delay is applied to each packet write.
    > Note that this delay feature is separate from the one offered by `Goto` as a Server, via [URIs](#-uris) and [Response Delay](#-response-delay). The URI delay feature in particular allows for more fine-grained delay configuration that can be used in conjunction with the proxy feature to apply delays to URIs that eventually get routed via proxy.
- A `drop` percentage can be configured per target that specifies what percentage of writes should be skipped in either direction for that target. If configured, `goto` will skip every `100/{pct}` write to/from this target. For TCP targets, the drop pct gets applied across packets in both directions. For HTTP targets, when it's time to drop a write, it'll choose to drop either of the request or the response based on a random coin flip.
  > While technically this is not a true network TCP packet drop, it does allow for some interesting chaos testing where data is randomly lost between the two parties.
  > If you want to drop some TCP writes for HTTP traffic, you can configure the port in `goto` as a TCP port and use the TCP features described below while the client and upstream still communicate over HTTP.
  > For TLS traffic, the initial TLS handshake is also subject to the drop, which can create more chaos by randomly failing TLS handshake.
- The HTTP proxy allows you to add/remove headers and query params, which can be a good way to create unexpected circumstances for the client and service. For example, configure the proxy to remove a required header, or change some header's value, and observe whether the two parties deal with the situation gracefully.
- Additional chaos can be applied via listener API, by closing/reopening a connection.



#### Proxy APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST |	/proxy/targets/clear            | Remove all proxy targets |
| PUT, POST |	/proxy/targets/add  | Add target for proxying requests [see `Proxy Target JSON Schema`](#proxy-target-json-schema) |
| POST | /proxy/targets<br/>/`{target}`/remove  | Remove a proxy target |
| POST | /proxy/targets<br/>/`{target}`/enable  | Enable a proxy target |
| POST | /proxy/targets<br/>/`{target}`/disable | Disable a proxy target |
| PUT, POST |	/proxy/http/targets<br/>/add/`{name}`?<br/>url=`{url}`<br/>&proto=`{proto}`<br/>&from=`{uri}`&to=`{uri}` | Add a new target with the given name and URL. Optional param `proto` can be used to assign a protocol (`HTTP/1.1, HTTP/2`), defaults to HTTP/1.1. Params `from` and `to` are used to define a URI mapping for this target. Additional URI routing can be defined using the `/{target}/route` API given below. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/route?<br/>from=`{uri}`&to=`{uri}` | Add URI routing for the given target from the given downstream URI (`from`) to the given upstream URI (`to`). |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/match/header<br/>/`{key}`[=`{value}`] | Define a header match criteria to match just the header name, or both name and value. <small>Note: Header match criteria added via this API are treated as `MatchAny`. To use `MatchAll` semantics, use JSON payload to define the target.</small> |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/match/query<br/>/`{key}`[=`{value}`] | Define a query param match criteria to match just the param name, or both name and value. <small>Note: Query match criteria added via this API are treated as `MatchAny`. To use `MatchAll` semantics, use JSON payload to define the target.</small> |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/headers<br/>/add/`{key}`=`{value}` | Define a header key/value that should be added to the upstream request. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/headers<br/>/remove/`{key}` | Define a downstream header that should be removed from the upstream request. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/query/add<br/>/`{key}`=`{value}` | Define a query param key/value that should be added to the upstream request. |
| PUT, POST | /proxy/http/targets<br/>/`{target}`/query<br/>/remove/`{key}` | Define a downstream query param that should be removed from the upstream request. |
| PUT, POST |	/proxy/tcp/targets<br/>/add/`{name}`<br/>?<br/>address=`{address}`<br/>&sni=`{sni}` | Add a new TCP upstream target with the given name and address, where the address is in the format `hostname:port`. The optional `sni` param can be a comma-separated list of host names to perform SNI based routing. The presence of `sni` param indicates that the TCP traffic for this proxy port is encrypted. |
| PUT, POST | /proxy/targets<br/>/`{target}`/delay=`{delay}` | Configure a delay to be applied to the requests/responses or tcp packets going to/from the given target |
| POST | /proxy/targets<br/>/`{target}`/delay/clear | Clears any configured delay for the given target |
| PUT, POST | /proxy/targets<br/>/`{target}`/drop=`{pct}` | Configure a percentage of writes to be dropped to/from the given target. If configured, `goto` will skip every `100/{pct}` write to/from this target. For TCP, this results in dropping packets in both directions. For HTTP, it'll choose to drop either of the request or the response based on a random coin flip. |
| POST | /proxy/targets<br/>/`{target}`/drop/clear | Clears any configured drop for the given target so that the traffic can go back to normal |
| GET |	/proxy/targets                  | List all proxy targets |
| GET | /proxy/targets<br/>/`{target}`/report | Get a report of the activity so far for the given target |
| GET | /proxy/report/http | Get a report of the activity so far for all HTTP targets |
| GET | /proxy/report/tcp | Get a report of the activity so far for all TCP targets |
| GET | /proxy/report | Get a report of the activity so far for all targets (HTTP + TCP) |
| GET | /proxy/all/report | Get a combined report of all proxies (all ports) |
| POST | /proxy/report/clear | Clear all the accumulated report counts/info |
| POST | /proxy/all/report/clear | Clear reports of all proxies (all ports)  |
| POST | /proxy/enable  | Enable proxy feature on the port |
| POST | /proxy/disable | Disable proxy feature on the port |

###### <small> [Back to TOC](#goto-proxy) </small>

<br/>
<details>
<summary>Proxy Target JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| name        | string             | Name for this target |
| protocol    | string             | "tcp", "http", "http/2" (can also use "h2", "h2c", "http/2.0") |
| endpoint    | string             | Upstream address for the target. |
| routes        | map[string]string  | URI mapping from downstream source request URI to upstream request URI. Downstream request's URI is matched against this routing table and if a match is found, the corresponding destination URI is used. If the destination URI is set to "", the source request URI is used. |
| sendID        | bool           | Whether or not a unique ID be sent with each request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| addHeaders    | `[][]string`                            | Additional headers to add to the request before proxying |
| removeHeaders | `[]string `                             | Headers to remove from the original request before proxying |
| addQuery      | `[][]string`                            | Additional query parameters to add to the request before proxying |
| removeQuery   | `[]string`                              | Query parameters to remove from the original request before proxying |
| stripURI | string (regex) | Optional regex to capture any portions of the request URI that should be stripped. If given, it's applied on the request URI and the matching pieces get removed from the URI. |
| matchAny        | JSON     | Match criteria based on which runtime traffic gets proxied to this target. See [JSON Schema](#proxy-target-match-criteria-json-schema) and [detailed explanation](#proxy-target-match-criteria) below |
| matchAll        | JSON     | Match criteria based on which runtime traffic gets proxied to this target. See [JSON Schema](#proxy-target-match-criteria-json-schema) and [detailed explanation](#proxy-target-match-criteria) below |
| replicas     | int      | Number of parallel replicated calls to be made to this target for each matched request. This allows each request to result in multiple calls to be made to a target if needed for some test scenarios |
| delayMin      | duration     | Minimum delay to be applied to the requests/responses to/from this target |
| delayMax      | duration     | Max delay to be applied to the requests/responses to/from this target |
| delayCount    | int     | Number of requests for which the delay should be applied. After the count of requests have been affected by the delay, the subsequent calls revert to normal processing. |
| dropPct      | int     | Percent of TCP writes to be dropped in the proxy. This only applies to TCP targets. If given, every Nth write (calculated based on the given percentage) will be skipped in both directions. |
| enabled       | bool     | Whether or not the proxy target is currently active |


#### Proxy Target Match Criteria JSON Schema
|Field|Data Type|Description|
|---|---|---|
| headers | `[][]string`  | Headers names and optional values to match against request headers |
| query   | `[][]string`  | Query parameters with optional values to match against request query |
| sni     | `[]string`  | List of server names to match against client TLS handshake |

#### HTTP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| downstreamRequestCount | int  | Number of downstream requests received  |
| upstreamRequestCount | int  | Number of upstream requests sent |
| requestDropCount | int  | Number of requests dropped |
| responseDropCount | int  | Number of responses dropped |
| downstreamRequestCountsByURI | map[string]int  | Number of downstream requests received, grouped by URIs |
| upstreamRequestCountsByURI | map[string]int  | Number of upstream requests sent, grouped by URIs |
| requestDropCountsByURI | map[string]int  | Number of requests dropped, grouped by URIs |
| responseDropCountsByURI | map[string]int  | Number of responses dropped, grouped by URIs |
| uriMatchCounts | map[string]int  | Number of downstream requests that were forwarded due to URI match, grouped by matching URIs |
| headerMatchCounts | map[string]int  | Number of downstream requests that were forwarded due to header match, grouped by matching headers |
| headerValueMatchCounts | map[string]map[string]int  | Number of downstream requests that were forwarded due to header+value match, grouped by matching headers and values |
| queryMatchCounts | map[string]int  | Number of downstream requests that were forwarded due to query param match, grouped by matching query params |
| queryValueMatchCounts | map[string]map[string]int  | Number of downstream requests that were forwarded due to query param+value match, grouped by matching query params and values |
| targetTrackers | map[string]HTTPTargetTracker  | Tracking info per target. Each `HTTP Target Tracker JSON Schema` has same fields as above. |


#### HTTP Target Tracker JSON Schema
Same fields as `HTTP Proxy Tracker JSON Schema` above


#### TCP Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountsBySNI | map[string]int  | Number of downstream connections received for SNI match, grouped by SNI server names |
| rejectCountsBySNI | map[string]int  | Number of downstream connections rejected due to SNI mismatch, grouped by SNI server names |
| targetTrackers |  map[string]TCPTargetTracker  | Tracking details per target. See `TCP Target Tracker JSON Schema` below.  |


#### TCP Target Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountsBySNI | map[string]int  | Number of downstream connections received for SNI match, grouped by SNI server names |
| tcpSessions |  map[string]TCPSessionTracker  | Tracking details per client session. See `TCP Session Tracker JSON Schema` below.  |


#### TCP Target Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| sni | string  | SNI server name if one was used to match target for this session |
| downstream |  map[string]ConnTracker  | Downstream connection tracking details for this session. See `Connection Tracker JSON Schema` below. |
| upstream |  map[string]ConnTracker  | Upstream connection tracking details for this session. See `Connection Tracker JSON Schema` below. |

#### Connection Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| startTime | string  | Connection start time |
| endTime | string  | Connection end time |
| firstByteInAt | string  | Time of receipt of the first byte of data |
| lastByteInAt | string  | Time of receipt of the last byte of data |
| firstByteOutAt | string  | Time of dispatch of the first byte of data |
| lastByteOutAt | string  | Time of dispatch of the last byte of data |
| totalBytesRead | int  | Total number of bytes read from this connection |
| totalBytesWritten | int  | Total number of bytes written to this connection |
| totalReads | int  | Total number of read operations performed |
| totalWrites | int  | Total number of write operations performed |
| delayCount | int  | Total number of write operations where a delay was applied |
| dropCount | int  | Total number of skipped write operations due to being dropped |
| closed | bool  | Whether the connection is closed |
| remoteClosed | bool  | Whether the connection was closed by remote party |
| readError | bool  | Whether the connection was closed due to a read error |
| writeError | bool  | Whether the connection was closed due a write error |


</details>

<details>
<summary>Proxy Events</summary>

- `Proxy Target Rejected`
- `Proxy Target Added`
- `Proxy Target Removed`
- `Proxy Target Enabled`
- `Proxy Target Disabled`
- `Proxy Target Invoked`

</details>

See [Proxy Example](docs/proxy-example.md)

###### <small> [Back to TOC](#goto-proxy) </small>



# <a name="scripts-features"></a>
# Scripts Features

`Goto` allows scripts to be stored and executed on the `goto` server instance via APIs.


#### Scripts APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  |	/scripts/add/{name}     | Add a script under the given name. Request body payload is used as script content. |
| POST, PUT  |	/scripts/remove/{name} <br/> /scripts/{name}/remove  | Remove a script by name. |
| POST, PUT  |	/scripts/run/{name} <br/> /scripts/{name}/run | Run a script by name and deliver script output as the response payload of this API call. |
| GET  |	/scripts | Get all the stored scripts with their content (lines of a script are delivered as string array) |

###### <small> [Back to TOC](#scripts) </small>


# <a name="jobs-features"></a>
# Jobs Features

`Goto` allows jobs to be configured that can be run manually or auto-start upon addition. Two kinds of jobs are supported:
- HTTP requests to be made to some target URL
- Command execution on local OS.
The job results can be retrieved via API from the `goto` instance, and also stored in lockers on the Goto registry instance if enabled. (See `--locker` command arg)

Jobs can be configured to run periodically using the `cron` field. A cron job starts automatically upon creation, and keeps running at the specified frequency until stopped (using `/jobs/stop` API). A stopped cron job can be restarted using `/jobs/run` API, which restarts the cron frequency.

Jobs can also trigger another job for each line of output produced, as well as upon completion. For command jobs, the output produced is split by newline, and each line of output can be used as input to trigger another command job. A job can specify markers for output fields (split using specified separator), and these markers can be referenced by successor jobs. The markers from a job's output are carried over to all its successor jobs, so a job can use output from a parent job that might be several generations in the past. The triggered job's command arg specifies marker references as `{foo}`, which gets replaced by the value extracted from any predecessor job's output with that marker key. This feature can be used to trigger complex chains of jobs, where each job uses output of the previous job to do something else.

###### <small> [Back to TOC](#jobs) </small>


#### Jobs APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  |	/jobs/add     | Add a job. See [Job JSON Schema](#job-json-schema) |
| POST, PUT  |	/jobs/update | Update a job, using [Job JSON Schema](#job-json-schema) |
| POST, PUT  |	/jobs/add<br/>/script/`{name}` | Add a shell script to be executed as a job, by storing the request body as script content under the given filename at the current working directory of the `goto` process. Also creates a default job with the same name to provide a ready-to-use way to execute the script. |
| POST, PUT  |	/jobs/store<br/>/file/`{name}` | Store request body as a file at the current working directory of the `goto` process. Filed saved with mode `777`.|
| POST, PUT  |	/jobs/store/file<br/>/`{name}`?path=`{path}` | Store request body as a file at the given path with mode `777`. |
| POST  | /jobs/`{jobs}`/remove | Remove given jobs by name, and clears its results |
| POST  | /jobs/clear         | Remove all jobs |
| POST  | /jobs/`{jobs}`/run `[or]` /jobs/run/`{jobs}` | Run given jobs |
| POST  | /jobs/run/all       | Run all configured jobs |
| POST  | /jobs/`{jobs}`/stop | Stop given jobs if running |
| POST  | /jobs/stop/all      | Stop all running jobs |
| GET   | /jobs/{job}/results | Get results for the given job's runs |
| GET   | /jobs/results       | Get results for all jobs |
| POST   | /jobs/results/clear | Clear all job results |
| GET   | /jobs/scripts       | Get a list of all stored scripts |
| GET   | /jobs/              | Get a list of all configured jobs |


<br/>

<details>
<summary> Job JSON Schema </summary>

#### 
|Field|Data Type|Description|
|---|---|---|
| name          | string        | Identifies this job |
| task          | JSON          | Task to be executed for this job. Can be an [HTTP Task](#job-http-task-json-schema) or [Command Task](#job-command-task-json-schema) |
| auto          | bool          | Whether the job should be started automatically as soon as it's posted. |
| delay         | duration      | Minimum delay at start of each iteration of the job. Actual effective delay may be higher than this. |
| initialDelay  | duration      | Minimum delay to wait before starting a job. Actual effective delay may be higher than this. |
| count         | int           | Number of times this job should be executed during a single invocation |
| cron          | string        | This field allows configuring the job to be executed periodically. The frequency can be specified in cron format (`* * * * *`) or as a duration (e.g. `15s`).|
| maxResults    | int           | Number of max results to be received from the job, after which the job is stopped |
| keepResults   | int           | Number of results to be retained from an invocation of the job |
| keepFirst     | bool          | Indicates whether the first invocation result should be retained, reducing the slots for capturing remaining results by (maxResults-1) |
| timeout       | duration      | Duration after which the job is forcefully stopped if not finished |
| outputTrigger | JobTrigger    | ID of another job to trigger for each output produced by this job. For command jobs, words from this job's output can be injected into the command of the next job using positional references (described above) |
| finishTrigger | JobTrigger        | ID of another job to trigger upon completion of this job |

#### Job HTTP Task JSON Schema
|Field|Data Type|Description|
|---|---|---|
| {Invocation Spec} | Target Invocation Spec | See [Client Target JSON Schema](docs/client-api-json-schemas.md) that's shared by the HTTP Jobs to define an HTTP target invocation |
| parseJSON    | bool           | Indicates whether the response payload is expected to be JSON and hence not to treat it as text (to avoid escaping quotes in JSON) |
| transforms   | []Transform | A set of transformations to be applied to the JSON output of the job. See [Response Payload Transformation](#-payload-transformation) section for details of JSON transformation supported by `goto`. |

#### Job Command Task JSON Schema
|Field|Data Type|Description|
|---|---|---|
| cmd             | string         | Command to be executed on the OS. Use `sh` as command if shell features are to be used (e.g. pipe) |
| script          | string         | Name of a stored script. When a script is uploaded using API `/jobs/add/script/{name}`, a script job gets created automatically with the uploaded script name stored in this field. |
| args            | []string       | Arguments to be passed to the OS command |
| outputMarkers   | map[int]string | Specifies marker keys to use to reference the output fields from each line of output. Output is split using the specified separator to extract its keys. Positioning starts at 1 for first the piece of split output. |
| outputSeparator | string         | Text to be used as separator to split each line of output of this command to extract its fields, which are then used by markers |


#### Job Trigger JSON Schema
|Field|Data Type|Description|
|---|---|---|
| name     | string     | Name of the target job to trigger from the current job.  |
| forwardPayload  | bool | Whether to forward the output of the current job as the input payload for the next job. Currently this is only supported for HTTP task jobs. |


#### Job Result JSON Schema
|Field|Data Type|Description|
|---|---|---|
| id     | string     | id uniquely identifies a result item within a job run, using format `<JobRunCounter>.<JobIteration>.<ResultCount>`.  |
| finished  | bool       | whether the job run has finished at the time of producing this result |
| stopped   | bool       | whether the job was stopped at the time of producing this result |
| last      | bool       | whether this result is an output of the last iteration of this job run |
| time      | time       | time when this result was produced |
| data      | string     | Result data |

</details>

<details>
<summary> Jobs Timeline Events </summary>

- `Job Added`
- `Job Script Stored`
- `Job File Stored`
- `Jobs Removed`
- `Jobs Cleared`
- `Job Results Cleared`
- `Job Started`
- `Job Finished`
- `Job Stopped`

</details>

See [Jobs Example](docs/jobs-example.md)

###### <small> [Back to TOC](#jobs) </small>


# <a name="k8s-features"></a>

# K8s Features
`Goto` exposes APIs through which info can be fetched from a k8s cluster that `goto` is connected to. `Goto` can connect to the local K8s cluster when running inside a cluster, or connect to a remote K8s cluster via locally available k8s config. When connected remotely, it relies on the authentication performed by local K8s context.

The `goto` K8s APIs support working with both native K8s resources (e.g. Namespaces, Pods, etc.) as well as custom K8s resources identified via GVK (e.g. Istio VirtualService).

# <a name="k8s-apis"></a>
###  K8s APIs

|METHOD|URI|Description|
|---|---|---|
| GET      | /k8s/{resource}  | Get a list of native k8s resource instances cluster-wide (namespaced or non-namespaced) for the given resource kind |
| GET      | /k8s/{resource}/[`jq`\|`jp`]  | Get a list of native k8s resources and apply a JQ or a JSONPath query to the resource data |
| GET      | /k8s/{resource}/{namespace} | Get a list of namespaced native k8s resources for the given type from a given namespace |
| GET      | /k8s/{resource}<br/>/{namespace}/[`jq`\|`jp`] | Get a list of namespaced native k8s resource from a given namespace, and apply a JQ or a JSONPath query to the resource data |
| GET      | /k8s/{resource}<br/>/{namespace}/{name}  | Get a namespaced native k8s resource by name from the given namespace |
| GET      | /k8s/{resource}<br/>/{namespace}/{name}/[`jq`\|`jp`] | Get a namespaced native k8s resource by name from the given namespace, and apply a JQ or a JSONPath query to the resource data |
| GET      | /k8s/{group}/{version}/{kind} | Get a list of custom k8s resources cluster-wide (namespaced or non-namespaced) by specifying the GVK identifying the resource type |
| GET      | /k8s/{group}/{version}<br/>/{kind}/[`jq`\|`jp`] | Get a list of custom k8s resources cluster-wide (namespaced or non-namespaced) by specifying the GVK identifying the resource type, and apply a JQ or a JSONPath query to the resource data |
| GET      | /k8s/{group}/{version}<br/>/{kind}/{namespace} | Get a list of custom k8s namespaced resources from the given namespace |
| GET      | /k8s/{group}/{version}<br/>/{kind}/{namespace}/[`jq`\|`jp`] | Get a list of custom k8s namespaced resources from the given namespace, and apply a JQ or a JSONPath query to the resource data |
| GET      | /k8s/{group}/{version}<br/>/{kind}/{namespace}/{name} | Get a custom k8s namespaced resource by name from the given namespace |
| GET      | /k8s/{group}/{version}<br/>/{kind}/{namespace}<br/>/{name}/[`jq`\|`jp`] | Get a custom k8s namespaced resource by name from the given namespace, and apply a JQ or a JSONPath query to the resource data |
| POST      | /k8s/clear  | Clear currently cached K8s resources. Necessary when switching K8s content to connect to a different cluster (when `goto` is running outside of a K8s cluster and connecting via locally available K8s config) |


###### <small> [Back to TOC](#k8s) </small>


# <a name="tunnel"></a>
# Tunnel

`Tunnel` feature allows a `goto` instance to act as a L7 tunnel, receiving HTTP/HTTPS/H2 requests from clients and forwarding those to any arbitrary endpoints over the same or a different protocol. This feature can be useful in several scenarios: e.g. 
- A client wishes to reach an endpoint by IP address, but the endpoint is not accessible from the client's network space (e.g. K8S overlay network). In this case, a single `goto` instance deployed inside the overlay network (e.g. K8S cluster) but accessible to the client network space via an FQDN can receive requests from the client and transparently forward those to any overlay IP address that's visible to the `goto` instance.
- Route traffic from a client to a service through `goto` proxy in order to inspect traffic and capture details in both directions.
- Observe network behavior (latencies, packet drops, etc) between two endpoints
- Send a request from a client to two or more service endpoints and analyze results from those, while sending the response to client from whichever endpoint responds first.
- Send a request on a multi-hop journey, routing via multiple goto tunnels, in order to observe latency or other network behaviors.
- Test a client and/or service's behavior if the other party that's communicating with it changed its protocol from HTTP/1 to HTTP/2 without having to change the real applications' code.

There are four different ways in which requests can be tunneled via `Goto`:

#### 1. Configured Tunnels
<div style="padding-left:20px">
  Any listener can be converted into a tunnel by calling `/tunnels/add/{protocol:address:port}` API to add one or more endpoints as tunnel destinations. When a tunnel has more than one endpoint, the requests are forwarded to all the endpoints, and the earliest response gets sent to the client.
</div>

#### 2. On-the-fly Tunneling via URI prefix
<div style="padding-left:20px">
  Any request can be tunneled via `Goto` using URI path format `http://goto.goto/tunnel={endpoints}/some/uri`. `{endpoints}` is a list of endpoints where each endpoint is specified using the format `{protocol:address:port}`. The goto instance receiving the above formatted request will multicast the request to all the given endpoints, to URI `/some/uri` along with the same HTTP parameters that the client used (Method, Headers, Body, TLS). 

  In order to multi-tunnel a request via multiple `goto` instances, multiple tunnel path prefixes can be added, e.g. `http://goto-1:8080/tunnel={goto-2:8080}/tunnel={goto-3:8081}/tunnel={real-service:80}/some/uri`. <p/>
  As you can imagine by analyzing this request, it's processed by the first goto instance (`goto-1:8080`) using the previous logic, which ends up forwarding it to instance `goto-2:8080` with the remaining URI `/tunnel={goto-3:8081}/tunnel={real-service:80}/some/uri`. The second goto instance (`goto-2:8080`) again treats the incoming request as a tunnel request, and forwards it to instance `goto-3:8081` with the remaining URI `/tunnel={real-service:80}/some/uri`. The third goto instance finally tunnels it to the endpoint `real-service:80` with URI `/some/uri`.
</div>

#### 3. On-the-fly Tunneling using special header `Goto-Tunnel`
<div style="padding-left:20px">
  Goto can be asked to tunnel a request by sending it to the `goto` instance with an additional header `Goto-Tunnel:{endpoints}`. Endpoints can be a comma-separated list where each endpoint is of format `{protocol:address:port}`. This approach allows for rerouting some existing traffic via goto, which then sends it to the original intended upstream service without having to modify the URI. The `Goto-Tunnel` header allows for multicasting as well as multi-tunneling. <br/><br/>

  In order to multicast a request to several endpoints, add the `Goto-Tunnel` header multiple times (i.e. with list of HTTP header values). For example:
  ```
  curl -vk https://goto-1:8081/foo -H'Goto-Tunnel:goto-2:8082' -H'Goto-Tunnel:goto-3:8083'
  ```

  In order to send a request through multiple `goto` tunnels, add multiple `goto` endpoint addresses as a comma-separated value to a single `Goto-Tunnel` header. For example:
  ```
  curl -vk https://goto-1:8081/foo -H'Goto-Tunnel:goto-2:8082,goto-3:8083'
  ```

  `Endpoints` (in path prefix or header) can omit the protocol, or specify the protocol from one of: `http` (HTTP/1.1), `https` (HTTP/1.1 with TLS), `h2` (HTTP/2 with TLS) or `h2c` (HTTP/2 over cleartext).

  When the endpoints in a tunnel have different protocols, `Goto` performs protocol conversions between all possible translations (`http` to/from `https` and `HTTP/1.1` to/from `HTTP/2`). The request `Host` and `SNI Authority` are rewritten to match the endpoint host.

  When an endpoint in a tunnel omits protocol in its spec, the protocol used by the original/preceding client request is carried forward.
</div>


#### 4. HTTP(S) Proxy and CONNECT protocol
<div style="padding-left:20px">
  If `Goto` receives a header that indicates that a connected client has been routed via an HTTP(S) Proxy, or if `goto` receives an HTTP CONNECT request, `goto` establishes an on-the-fly tunnel to the destination endpoint.

      Note: The proxy auto-detection support is experimental and not confirmed to work under all circumstances.

  For example, the below curl request gets routed via HTTPS proxy `goto-2.goto`. The `goto` instance running on `goto-2.goto` intercepts the request and tunnels it to the original destination `goto-1.goto`, but gives you a chance to track its details.

  ```
  curl -vk goto-1.goto:8081/foo/bar -H'foo:bar' --proxy https://goto-2.goto:8082 --proxy-cacert goto-2-cert.pem
  ```
</div>

### Tunnel APIs

###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

All `Goto` APIs support tunnel prefix, allowing any `goto` API to be proxied from one instance to another. In addition, any arbitrary API can also be called using the tunnel prefix. See the tunnel feature description above for the specification of `{endpoint}` used in the tunnel APIs. To configure a tunnel on a port using another port, use `port={port}` prefix format.

|METHOD|URI|Description|
|---|---|---|
| ALL       |	/`tunnel={endpoint}`/`...` | URI prefix format to tunnel any request on the fly. |
| POST, PUT |	/tunnels/add<br/>/`{endpoint}`?`uri={uri}`| Adds a tunnel on the listener port to redirect traffic to the given `endpoint`. If the URI query param is specified, only traffic to the given URI is intercepted. |
| POST, PUT |	/tunnels/add/`{endpoint}`<br/>/header/`{key}={value}`<br/>?`uri={uri}` | Adds a tunnel that acts upon requests carrying the given header key and optional value. If value is omitted, just the presence of the header key triggers the tunnel. If a URI is specified as a query param, the header match is performed only on requests to the given URI. |
| POST, PUT |	/tunnels/add<br/>/`endpoint}`/transparent | Add a transparent tunnel that doesn't add goto request headers when forwarding a request to the upstream endpoints. However, goto response headers are still added to the response sent to the downstream client. |
| POST, PUT |	/tunnels/remove<br/>/`{endpoint}`?`uri={uri}` | Remove a configured endpoint tunnel on the listener port. If the URI query param is specified, only the tunnel for that URI is removed. |
| POST, PUT |	/tunnels/remove/`{endpoint}`<br/>/header/`{key}={value}`<br/>?`uri={uri}` | Remove a configured endpoint tunnel on the listener port for the given header match. If the URI query param is specified, tunnels for that URI are removed. |
| POST, PUT |	/tunnels/clear | Clear all tunnels on the listener port on which the API is called. |
| GET |	/tunnels | Get currently configured tunnels. |
| GET |	/tunnels/active | Get currently active tunnels. |
| POST, PUT |	/tunnels/track<br/>/header/`{headers}` | Track tunnel traffic on the listener port for the given headers. |
| POST, PUT |	/tunnels/track<br/>/query/`{params}` | Track tunnel traffic on the listener port for the given query params. |
| POST, PUT |	/tunnels/track/clear | Clear tunnel traffic tracking on the listener port. |
| GET |	/tunnels/track/ | Get tunnel traffic tracking report. |
| POST, PUT |	/tunnels/traffic<br/>/capture=`{yn}` | Enable/disable capture of tunnel traffic on the listener port. |
| GET |	/tunnels/traffic | Get tunnel traffic log if traffic capturing was enabled for the listener port. |


###### <small> [Back to TOC](#goto-tunnel) </small>


# <a name="pipeline-features"></a>

# Pipeline Features
`Goto` pipelines feature allows you to pull data from various kinds of sources, process the source data through one or more transformations, and feed the output back to more sources/transformations or write it out. Pipelines support various kinds of sources (K8s, Jobs, Command Scripts, HTTP traffic, etc.) and transformations (JSONPath, JQ, Go Template, Regex). Additionally, pipelines support `watch` capability, where sources are watched for new data and the associated pipeline is triggered for any upstream changes.

By default all sources and transformation of a pipeline are executed in a single stage. Pipeline stages can be defined to achieve a more complex orchestration where some sources and transformations need to execute before others.


### Pipeline Spec
A pipeline is defined via a JSON payload submitted via API. 

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the pipeline |
| sources | map[string]Source | Set of sources that feed into this pipeline, either all at once or in stages (see stages below). The map key is the source name. |
| transforms | map[string]Transform | Set of transformations that get applied to data produced by sources at various stages. The map key is the transformation name. |
| stages | []PipelineStage | Optional list of stages this pipeline is split into. Each stage consists of a set of sources and transforms. |
| out | []string | Names of sources and transformations whose output is included in the final output of the pipeline. When not specified, the pipeline output includes the output of all its sources and transformations.  |
| running | bool | A read-only status field to indicate whether the pipeline is currently executing. |

<br/>
<details>
<summary> Pipeline Spec Example </summary>

```
{
  "name": "demo-pipe",
  "sources": {
    "source_ns": {"type":"K8s", "spec":"/v1/ns/goto", "watch": true},
    "source_job": {"type":"Job", "spec":"job1"}
  },
  "transforms": {
    "ns_name": {"type": "JQ", "spec": ".source_ns.metadata.name"},
    "result": {"type": "Template", "spec": "{{.source_job}}"}
  },
  "stages": [
    {"label": "stage1", "sources":["source_ns"], "transforms":["ns_name"]},
    {"label": "stage2", "sources":["source_job"], "transforms":["result"]}
  ],
  "out": ["ns_name", "result"]
}
```

</details>
<br/>


## Pipeline Sources
A pipeline source brings data into the pipeline, and can also trigger the pipeline when watched. 

### Pipeline Source Spec

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the source |
| type | string | Identifies the type of the source |
| spec | string | Provides identifying information for the source. A source's spec may use fillers with syntax `{name}` where `name` identifies another source or transformation whose output should be used to substitute the filler. See examples further below. |
| content | string | Used for sources that need some content for execution, e.g. a script. It can use fillers to capture output of other sources/transformations similar to `spec` field. |
| input | any | Optional input to be given to the source at execution. It can use fillers to capture output of other sources/transformations similar to `spec` field. |
| inputSource | string | Optional name of another source whose output should be passed as input to this source |
| parseJSON | bool | Whether the output of this source be parsed as JSON |
| parseNumber | bool | Whether the output of this source be parsed as a number |
| reuseIfExists | bool | Whether an existing instance of this source be reused if already instantiated in a previous execution of this pipeline |
| watch | bool | Whether the source should be watched for new input data and trigger the pipeline |


### Pipeline Source Types
The following kinds of sources are available:

#### <i>Source Type: `Job`</i>
<div style="padding-left:20px">
  A Job source represents the output of a goto [Job](#jobs-features). The source `spec` field refers to an existing Job's name, where the job must be previously defined using [Jobs APIs](#jobs-apis). 
  <br/><br/>
  Job sources should mostly be defined with `reuseIfExists` set to `true`, indicating that the pipeline should use the output of the last run of the linked job. This is even more relevant when the source is configured with `watch` set to true, so that an execution of the job would trigger the pipeline and the pipeline would simply use the output of the job run that triggered it. If `reuseIfExists` field is set to false, the pipeline's execution will trigger a fresh execution of the linked job, and the pipeline would wait for the job to complete and use the result produced by this job run.
  <br/><br/>
  As `goto` supports two types of jobs: `Command` and `HTTP`, hence a pipeline can use Job sources to execute OS scripts as well as make HTTP calls.
  <br/><br/>
  See [Jobs feature](#jobs-features) for details on how to define Command and HTTP jobs.

  <details>
  <summary> Job Source Example </summary>

  ```
  {
    "name": "demo-jobs-pipe",
    "sources": {
      "s_cmd_job": {"type":"Job", "spec":"job1", "reuseIfExists": true, "watch": true},
      "s_http_job": {"type":"Job", "spec":"job2", "parseJSON": true, "reuseIfExists": true, "watch": true}
    }
  }
  ```
  </details>

</div>

#### <i>Source Type: `Script`</i>
<div style="padding-left:20px">

  A Script source provides a way to run an OS script in a pipeline without a predefined job. The source `spec` field provides the script name, the `content` field provides the script, and the `input` or `inputSource` field can be used to provide input for the script if needed. 

  <details>
  <summary> Script Source Example </summary>

  ```
  {
    "name": "demo-script-pipe",
    "sources": {
      "s1": {
        "type":"Script", 
        "spec":"echo-foo", 
        "content":"echo 'FooX\\nBarX\\nFoo2\\nAnother Foo\\nMore Foos\\nDone'"
      },		
      "s2": {
        "type":"Script", 
        "spec":"foo-array", 
        "content":"grep Foo | sed 's/X/!/g' | tr -s ' ' | jq -R -s -c 'gsub(\"^\\\\s+|\\\\s+$\";\"\") | split(\"\\n\")' ", 
        "inputSource": "s1",
        "parseJSON": true
      },
      "s3": {
        "type":"Script", 
        "spec":"count-lines", 
        "content":"wc -l | xargs echo -n Total Lines: ", 
        "inputSource": "s1"
      },
      "s4": {
        "type":"Script", 
        "spec":"hello-world", 
        "content":"sed 's/Foo/World/g' | xargs echo -n Hello ", 
        "input": "{t1}"
      },
      "s5": {
        "type":"Script", 
        "spec":"foo-length", 
        "content":"jq -R -s -c 'length' | xargs echo -n Char Count: ", 
        "input": "{s2}",
        "parseNumber": true
      }
    },
    "transforms": {
      "t1": {"type": "JQ", "spec": ".s2[0]"},
      "t2": {"type": "JQ", "spec": ".s2[2]"},
      "t3": {"type": "JQ", "spec": ".s2 | length"}
    },
    "stages": [
      {"label": "stage1", "sources":["s1"]},
      {"label": "stage2", "sources":["s2", "s3"], "transforms":["t1", "t2", "t3"]},
      {"label": "stage3", "sources":["s4", "s5"]}
    ],
    "out": ["s3", "s4", "s5"]
  }
  ```
  </details>
</div>

#### <i>Source Type: `K8s`</i>
<div style="padding-left:20px">

  A K8s source represents either a single K8s resource or a set of K8s resources, identified by its `spec` field. It queries a K8s cluster to fetch the resource details: either from the local cluster where `goto` instance is deployed, or a remote cluster based on the current kube context set in local kube config.

  The K8s source `spec` identifies the K8s resource using pattern `group/version/kind/namespace/name`. For example, spec value `networking.istio.io/v1beta1/virtualservice/foo/bar` identifies a resource named `bar` under namespace `foo` with resource kind `VirtualService`, group `networking.istio.io`, and version `v1beta1`. 

  For native k8s resources that don't have a group, the group piece is left empty. For example: `/v1/ns/` indicates all namespaces, `/v1/foo/pods` identifies all pods in namespace `foo`, and `/v1//pods` indicates all pods across all namespaces. 

  > See [K8s feature](#k8s-features) for more details about K8s query support in `goto`.

  <details>
  <summary> K8S Source Example </summary>

  ```
  {
    "name": "demo-k8s-pipe",
    "sources": {
      "ns": {"type":"K8s", "spec":"/v1/ns/goto", "watch": true},
      "nspods": {"type":"K8s", "spec":"/v1/pod/{ns_name}"}
    },
    "transforms": {
      "ns_name": {"type": "JQ", "spec": ".ns.metadata.name"},
      "podnames": {"type": "JQ", "spec": ".nspods.items[]|{name: .metadata.name, containers:[.spec.containers[].name]}"}
    },
    "stages": [
      {"label": "stage1", "sources":["ns"], "transforms":["ns_name"]},
      {"label": "stage2", "sources":["nspods"], "transforms":["podnames"]}
    ],
    "out": ["ns_name", "podnames"]
  }
  ```
  </details>
</div>

#### <i>Source Type: `K8sPodExec`</i>
<div style="padding-left:20px">

  This source type allows executing a command on one or more K8s pods. The source `spec` field should be defined in the format `"namespace/pod-label-selector/container-name"`, and the spec `content` field should contain the command(s) to be executed on the selected pods.

  <details>
  <summary> K8S Pod Exec Source Example </summary>
  ```
  {
    "name": "demo-podexec-pipe",
    "sources": {
      "pod_source": {"type":"K8sPodExec", "spec":"gotons/app=goto/goto", "content":"ls /"}
    }
  }
  ```
  </details>

</div>


#### <i>Source Type: `HTTPRequest`</i>
<div style="padding-left:20px">

  This source type allows for pipelines to be triggered based on HTTP requests received by the `goto` server. The feature is achieved via two sets of configurations: 
  1. A [Trigger](#response-triggers) that defines the request/response match criteria (URI, Headers, Status Code) that should match in order for the request to trigger a pipeline.
  2. A pipeline that includes an `HTTPRequest` source that references the trigger name in its `spec` field.

  When the `goto` server receives an HTTP request matching the trigger criteria, the linked pipeline gets triggered and the pipeline source's output carries the HTTP response data along with some metadata as listed below:
  1. `request.trigger`: name of the trigger that matched the request
  2. `request.host`
  3. `request.uri`
  4. `request.headers`
  5. `request.body`
  6. `response.status`
  7. `response.headers`

  <details>
  <summary> HTTP Source Example </summary>

  ```
  #HTTP Trigger definition
  {
    "name": "t1",
    "pipe": true,
    "enabled": true,
    "triggerURIs": ["/foo", "/status/*"],
    "triggerStatuses": [502]
  }

  #Trigger based pipeline definition
  {
    "name": "demo-http-trigger-pipe",
    "sources": {
      "http": {"type":"HTTPRequest", "spec":"t1", "watch": true}
    },
    "transforms": {
      "uri": {"type": "JQ", "spec": ".http.request.uri"},
      "req_headers": {"type": "JQ", "spec": ".http.request.headers"},
      "status": {"type": "JQ", "spec": ".http.response.status"},
      "resp_headers": {"type": "JQ", "spec": ".http.response.headers"}
    }
  }

  #This curl call to the goto instance triggers the pipeline via the trigger that matches on URI + response status code.
  $ curl http://goto:8080/status/502
  ```

  </details>
</div>


#### <i>Source Type: `Tunnel`</i>
<div style="padding-left:20px">

  This source type allows triggering pipelines for HTTP requests tunneled through a `goto` instance. The output behavior of `Tunnel` source type is somewhat similar to the `HTTPRequest` source type, but they differ in which HTTP requests would trigger the pipeline. The `HTTPRequest` source type triggers pipelines for requests served by the `goto` instance itself as a server, whereas the `Tunnel` source type comes into play for HTTP requests meant for other upstream destinations but tunneled via a `goto` instance for inspection.

  The tunnel associated with the pipeline is referenced in the source `spec` field by the tunnel's `Endpoint` identifier that's composed as `<protocol>:<address>:<port>`. See [Tunnel](#tunnel) feature for more details about tunnel creation and handling.

  <details>
  <summary> Tunnel Source Example </summary>

  For an HTTP request tunneled via goto instance `goto-1.goto` to the final destination `goto-2.goto`, a pipeline on `goto-1` instance can use `Tunnel` source that references `goto-2` endpoint in its spec as shown below. The pipeline will be triggered for all requests that pass through `goto-1` with `goto-2` as the final destination.

  ```
  {
    "name": "demo-tunnel-pipe",
    "sources": {
      "http": {"type":"Tunnel", "spec":"http:goto-2.goto:9091", "watch": true}
    },
    "transforms": {
      "uri": {"type": "JQ", "spec": ".http.request.uri"},
      "req_headers": {"type": "JQ", "spec": ".http.request.headers"},
      "status": {"type": "JQ", "spec": ".http.response.status"},
      "resp_headers": {"type": "JQ", "spec": ".http.response.headers"}
    }
  }
  ```

  </details>
</div>

### Pipeline Transformations

Pipeline's transformation steps provide you a way to extract a subset of information from a source's output and/or apply some basic computational logic to the source data to produce some derived information.

A transformation definition provides the implementation-specific transformation query in its `spec` field. Each transformation receives the current working context as input, and so the query can refer to any existing source or transformation by name that's expected to exist in the working context at the time of execution of that transformation. Starting with any existing source or transformation, the query can read data from the source's output.

### Pipeline Transformation Spec

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the transformation, provided as the map key in the pipeline JSON |
| type | string | Identifies the type of the transformation. Supported types are: `JSONPath`, `JQ`, `Template` and `Regex`. |
| spec | string | Provides the query code to be compiled and executed based on the transformation type.

<br/>
The following kinds of transformations are supported:

1. `JSONPath`: based on implementation `k8s.io/client-go/util/jsonpath`
2. `JQ`: based on implementation `github.com/itchyny/gojq`
3. `Template`: based on golang templates feature
4. `Regex`: based on golang regexp package

# <a name="pipe-apis"></a>
###  Pipeline APIs

|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /pipes/create/`{name}` | Create an empty pipeline that will be filled via other APIs |
| POST, PUT | /pipes/add | Add a pipeline using JSON payload |
| POST, PUT | /pipes/`{name}`/clear,<br/>/pipes/clear/`{name}` | Empty the given pipeline |
| POST, PUT | /pipes/remove/`{name}`,<br/>/pipes/`{name}`/remove | Remove the given pipeline |
| POST, PUT | /pipes/`{pipe}`/sources/add | Add a source via JSON payload to the given existing pipeline |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/remove/`{name}` | Remove the given source from the given pipeline |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/add/k8s/`{name}`?<br/>`spec={spec}` | Add a K8s source with the given `name` and `spec` to the given existing pipeline |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/add/script/`{name}` | Add a script source with the given `name` and content (body payload) to the given pipeline |
| POST | /pipes/clear | Remove all defined pipelines |
| POST | /pipes/`{name}`/run | Run the given pipeline manually (as opposed to pipelines getting triggered by sources) |
| GET | /pipes | Get details of currently defined pipelines |
| GET | /pipes/`{name}` | Get details including run logs of the given pipeline |



###### <small> [Back to TOC](#pipelines) </small>

<br/>

# <a name="registry"></a>

# Registry

Any `goto` instance can act as a registry of other `goto` instances, and other worker `goto` instances can be configured to register themselves with the registry. You can treat any instance as the registry and pass its URL to other instances as a command line argument, which tells other instances to register themselves with the given registry at startup.

A `goto` instance can be passed command line arguments '`--registry <url>`' to point it to the `goto` instance acting as a registry. When a `goto` instance receives this command line argument, it invokes the registration API on the registry instance passing its `label` and `IP:Port` to the registry server. The `label` a `goto` instance uses can also be passed to it as a command line argument '`--label <label>`'. Multiple worker `goto` instances can register using the same label but different IP addresses, which would be the case for pods of the same deployment in K8s. The worker instances that register with a registry instance at startup, also deregister themselves with the registry upon shutdown.

By registering a worker instance to a registry instance, we get a few benefits:
1. You can pre-register a list of invocation targets and jobs at the registry instance that should be handed out to the worker instances. These targets/jobs are registered by labels, and the worker instances receive the matching targets+jobs for the labels they register with.
2. The targets and jobs registered at the registry can also be marked for `auto-invocation`. When a worker instance receives a target/job from the registry at startup that's marked for auto-invocation, it immediately invokes that target/job at startup. Additionally, the target/job is retained in the worker instance for later invocation via API as well.
3. In addition to sending targets/jobs to worker instances at the time of registration, the registry instance also pushes targets/jobs to the worker instances as and when more targets/jobs get added to the registry. This has the added benefit of just using the registry instance as the single point of configuration, where you add targets/jobs and those get pushed to all worker instances. Removal of targets/jobs from the registry also gets pushed, so the targets/jobs get removed from the corresponding worker instances. Even targets/jobs that are pushed later can be marked for `auto-invocation`, and the worker instances that receive the target/job will invoke it immediately upon receipt.
4. Registry provides `labeled lockers` as a flexible in-memory data store for capturing any kind of data for debugging purposes. Registry starts with a locker labeled `default`. A new locker can be opened using the `/open` API, and lockers can be closed (discarded) using the `/close` API. The most recently opened locker becomes current and captures data reported from peer instances, whereas other named lockers stay around and can be interacted with using `/store`, `/remove` and `/get` APIs. The `/search` API can find a given search phrase across all keys across all available lockers. Lockers can be opened in a hierarchical structure with child lockers under parent lockers, by using comma-separated names as `label` in `/open` API. 
5. If peer instances are configured to connect to a registry, they store their events and client invocation results into the current labeled locker in the registry. Registry provides APIs to get summary invocation results and a timeline of events across all peers. 
6. Peer instances periodically re-register themselves with the registry in case the registry was restarted and lost all peers info. Re-registering is different from startup registration in that peers don't receive targets and jobs from the registry when they remind the registry about themselves, and hence no auto-invocation happens.
7. A registry instance can be asked to clone data from another registry instance using the `/cloneFrom` API. This allows for quick bootstrapping of a new registry instance based on configuration from an existing registry instance, whether for data analysis purpose or for performing further operations. The pods cloned from the other registry are not used by this registry for any operations. Any new pods connecting to this registry using the same labels cloned from the other registry will be able to use the existing configs.

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="registry-apis"></a>
###  Registry APIs

# <a name="registry-peers-apis"></a> 
#### Registry Peers APIs

|METHOD|URI|Description|
|---|---|---|
| POST      | /registry/peers/add     | Register a worker instance (referred to as peer). See [Peer JSON Schema](#peer-json-schema)|
| POST      | /registry/peers<br/>/`{peer}`/remember | Re-register a peer. Accepts the same request payload as /peers/add API but doesn't respond back with targets and jobs. |
| POST, PUT | /registry/peers/`{peer}`<br/>/remove/`{address}` | Deregister a peer by its label and IP address |
| GET       | /registry/peers/`{peer}`<br/>/health/`{address}` | Check and report health of a specific peer instance based on label and IP address |
| GET       | /registry/peers<br/>/`{peer}`/health | Check and report health of all instances of a peer |
| GET       | /registry/peers/health | Check and report health of all instances of all peers |
| POST      | /registry/peers/`{peer}`<br/>/health/cleanup | Check health of all instances of the given peer label and remove IP addresses that are unresponsive |
| POST      | /registry/peers<br/>/health/cleanup | Check health of all instances of all peers and remove IP addresses that are unresponsive |
| POST      | /registry/peers<br/>/clear/epochs   | Remove epochs for disconnected peers|
| POST      | /registry/peers/clear   | Remove all registered peers|
| POST      | /registry/peers<br/>/copyToLocker   | Copy current set of `Peers JSON` data (output of `/registry/peers` API) to current locker under a pre-defined key named `peers` |
| GET       | /registry/peers         | Get all registered peers. See [Peers JSON Schema](#peers-json-schema) |


###### <small> [Back to TOC](#goto-registry) </small>

# <a name="locker-management-apis"></a>
#### Locker Management APIs

Label `current` can be used with APIs that take a locker label param to get data from the currently active locker. Comma-separated labels can be used to open/manage a nested locker.

|METHOD|URI|Description|
|---|---|---|
| POST      | /registry/lockers<br/>/open/`{label}` | Setup a locker with the given label and make it the current locker where peer results get stored. Comma-separated labels can be used to open nested lockers, where each non-leaf item in the CSV list is used as a parent locker. The leaf locker label becomes the currently active locker. |
| POST      | /registry/lockers<br/>/close/`{label}` | Remove the locker for the given label. |
| POST      | /registry/lockers<br/>/`{label}`/close | Remove the locker for the given label. |
| POST      | /registry/lockers/close | Remove all labeled lockers and empty the default locker.  |
| POST      | /registry/lockers<br/>/`{label}`/clear | Clear the contents of the locker for the given label but keep the locker. |
| POST      | /registry/lockers/clear | Remove all labeled lockers and empty the default locker.  |
| POST      | /registry/lockers<br/>/`{label}`/store/`{path}` | Store payload (body) as data in the given labeled locker at the leaf of the given key path. `path` can be a single key or a comma-separated list of subkeys, in which case data gets stored in the tree under the given path. |
| POST      | /registry/lockers<br/>/`{label}`/remove/`{path}` | Remove stored data, if any, from the given key path in the given labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data gets removed from the leaf of the given path. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/store/`{path}` | Store any arbitrary value for the given `path` in the locker of the peer instance under the currently active labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data is read from the leaf of the given path. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/remove/`{path}` | Remove stored data for the given `path` from the locker of the peer instance under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case the leaf key in the path gets removed. |
| POST      | /registry/peers/`{peer}`<br/>/locker/store/`{path}` | Store any arbitrary value for the given key in the peer locker without associating data to a peer instance under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case data gets stored in the tree under the given complete path. |
| POST      | /registry/peers/`{peer}`<br/>/locker/remove/`{path}` | Remove stored data for the given key from the peer locker under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case the leaf key in the path gets removed. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/events/store | API invoked by peers to publish their events to the currently active locker. Event timeline can be retrieved from the registry via various `/events` GET APIs. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/clear | Clear the locker for the peer instance under the currently active labeled locker |
| POST      | /registry/peers/`{peer}`<br/>/locker/clear | Clear the locker for all instances of the given peer under the currently active labeled locker |
| POST      | /registry/peers<br/>/lockers/clear | Clear all peer lockers under the currently active labeled locker |
| GET       | /registry/lockers/labels | Get a list of all existing locker labels, regardless of whether or not it has data.  |

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="data-sub-lockers-read-apis"></a>
#### Locker Data Path Read APIs

These APIs allow reading data stored at specific paths/keys. Where applicable, query param `data` controls whether locker is returned with or without stored data (default value is `n` and only locker metadata is fetched). Query param `events` controls whether the locker is returned with or without peers' events data. Query param `level` controls how many levels of subkeys are returned (default level is 2). Label `current` can be used to get data from the currently active locker, and label `all` can be used to read data stored under given keys from all lockers. Comma-separated locker labels can be used to read from a nested locker. Comma-separated keys can be used to read from nested keys. 

|METHOD|URI|Description|
|---|---|---|
| GET      | /registry/lockers<br/>/`{label}`/data/keys| Get a list of keys where some data is stored, from the given locker. |
| GET      | /registry/lockers<br/>/`{label}`/data/paths| Get a list of key paths (URIs) where some data is stored, from the given locker. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/data/keys| Get a list of keys where some data is stored, from all lockers.  |
| GET      | /registry/lockers<br/>/data/paths| Get a list of key paths (URIs) where some data is stored, from all lockers. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/search/`{text}` | Get a list of all valid URI paths (containing the locker label and keys) where the given text exists, across all lockers. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/`{label}`/search/`{text}` | Get a list of all valid URI paths where the given text exists in the given locker. The returned URIs are valid for invocation against the base URL of the registry. |
| GET       | /registry/lockers<br/>/data?data=`[y/n]`<br/>&level=`{level}` | Get data sub-lockers from all labeled lockers |
| GET       | /registry/lockers/`{label}`<br/>/data?data=`[y/n]`<br/>&level=`{level}` | Get data sub-lockers from the given labeled locker.  |
| GET      | /registry/lockers<br/>/`{label}`/get/`{path}`?<br/>data=`[y/n]`&level=`{level}` | Read stored data, if any, at the given key path in the given labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data is read from the leaf of the given path. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`<br/>/get/`{path}` | Get the data stored at the given path under the peer instance's locker under the given labeled locker.  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/get/`{path}` | Get the data stored at the given path under the peer locker under the given labeled locker.  |
| GET       | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/get/`{path}` | Get the data stored at the given path under the peer instance's locker under the current labeled locker |
| GET       | /registry/peers/`{peer}`<br/>/locker/get/`{path}` | Get the data stored at the given path under the peer locker under the current labeled locker |

###### <small> [Back to TOC](#goto-registry) </small>


# <a name="lockers-dump-apis"></a>
#### Lockers Dump APIs

These APIs read all contents of a selected locker or all lockers. Where applicable, query param `data` controls whether locker is returned with or without stored data (default value is `n` and only locker metadata is fetched). Query param `events` controls whether the locker is returned with or without peers' events data. Query param `peers` controls whether the returned locker(s) should include peer sub-lockers (containing data published by various peers). Query param `level` controls how many levels of subkeys are returned (default level is 2). Label `current` can be used with APIs that take a locker label param to get data from the currently active locker. Comma-separated labels can be used to read from a nested locker.

|METHOD|URI|Description|
|---|---|---|
| GET       | /registry/lockers/`{label}`?<br/>data=`[y/n]`&events=`[y/n]`<br/>&peers=`[y/n]`&level=`{level}` | Get the given labeled locker.  |
| GET       | /registry/lockers?<br/>data=`[y/n]`&events=`[y/n]`<br/>&peers=`[y/n]`&level=`{level}` | Get all lockers. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`?<br/>data=`[y/n]`&events=`[y/n]`<br/>&level=`{level}` | Get the peer instance's locker from the given labeled locker. |
| GET       | /registry/peers<br/>/`{peer}`/`{address}`/lockers?<br/>data=`[y/n]`&events=`[y/n]`<br/>&level=`{level}` | Get the peer instance's locker from the current active labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get the lockers of all instances of the given peer from the given labeled locker. |
| GET       | /registry/peers/`{peer}`<br/>/lockers?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get locker's data for all instances of the peer from the currently active labeled locker |
| GET       | /registry/lockers/`{label}`<br/>/peers?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get the lockers of all peers from the given labeled locker. |
| GET       | /registry/peers<br/>/lockers?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get the lockers of all peers from the currently active labeled locker. |

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="registry-events-apis"></a>
#### Registry Events APIs

Label `current` and `all` can be used with these APIs to get data from the currently active locker or all lockers. Param `unified=y` produces a single timeline of events combined from various peers. Param `reverse=y` produces the timeline in reverse chronological order. By default events are returned with their `data` field set to `...` to reduce the amount of data returned. Param `data=y` returns the events with data. 

|METHOD|URI|Description|
|---|---|---|
| POST      | /registry/peers<br/>/events/flush | Requests all peer instances to publish any pending events to registry, and clears events timeline on the peer instances. Registry still retains the peers' events in the current locker. |
| POST      | /registry/peers<br/>/events/clear | Requests all peer instances to clear their events timeline, and also removes the peers events from the current registry locker. |
| GET       | /registry/lockers<br/>/`{label}`/peers<br/>/`{peers}`/events?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of the given peers (comma-separated list) from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/events?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of all peers from the given labeled locker, grouped by peer label. |
| GET       | /registry/peers<br/>/`{peer}`/events?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of the given peer from the current locker. |
| GET       | /registry/peers/events?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of all peers from the current locker, grouped by peer label. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peers}`<br/>/events/search/`{text}`?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Search in the events timeline for all instances of the given peers (comma-separated list) from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/events<br/>/search/`{text}`?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Search in the events timeline of all peers from the given labeled locker, grouped by peer label. |
| GET       | /registry/peers/`{peer}`<br/>/events/search/`{text}`?<br/>reverse=`[y/n]`&<br/>data=`[y/n]` | Search in the events timeline for all instances of the given peer from the current locker. |
| GET       | /registry/peers/events<br/>/search/`{text}`?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Search in the events timeline of all peers from the current locker, grouped by peer label. |

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="peers-targets-management-apis"></a>
#### Peers Targets Management APIs

These APIs manage client invocation targets on peers, allowing to add, remove, start and stop specific or all targets, and read client invocation results in a processed JSON format.

|METHOD|URI|Description|
|---|---|---|
| GET       | /registry/peers/targets | Get all registered targets for all peers |
| POST      | /registry/peers<br/>/`{peer}`/targets/add | Add a target to be sent to a peer. See [Peer Target JSON Schema](#peer-target-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/remove | Remove given targets for a peer |
| POST      | /registry/peers<br/>/`{peer}`/targets/clear   | Remove all targets for a peer|
| GET       | /registry/peers/`{peer}`/targets   | Get all targets of a peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/invoke | Invoke given targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/invoke/all | Invoke all targets on the given peer |
| POST, PUT | /registry/peers<br/>/targets/invoke/all | Invoke all targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/stop | Stop given targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/stop/all | Stop all targets on the given peer |
| POST, PUT | /registry/peers<br/>/targets/stop/all | Stop all targets on the given peer |
| POST      | /registry/peers/targets/clear   | Remove all targets from all peers |

###### <small> [Back to TOC](#goto-registry) </small>


# <a name="peers-client-results-apis"></a>
#### Peers Client Results APIs

These APIs allow reading of combined client invocation results collected from all peers. When `detailed=y` query parameter is passed, the results of each category are broken down further by status codes and times buckets.

|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /registry/peers<br/>/client/results<br/>/all/`{enable}`  | Controls whether results should be summarized across all targets. Disabling this when not needed can improve performance. Disabled by default. |
| POST, PUT | /registry/peers<br/>/client/results<br/>/invocations/`{enable}`  | Controls whether results should be captured for individual invocations. Disabling this when not needed can reduce memory usage. Disabled by default. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer (results of all instances grouped together) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/instances<br/>/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer's instances (reported per instance) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for all peers (results of all instances grouped together) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/instances<br/>/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for all peer instances (reported per instance) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer (results of all instances grouped together) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/instances/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer's instances (reported per instance) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for all peers (results of all instances grouped together) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/instances/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for all peer instances (reported per instance) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the current locker, optionally filtered for the given targets (comma-separated list).  |

# <a name="peers-jobs-management-apis"></a>
#### Peers Jobs Management APIs

|METHOD|URI|Description|
|---|---|---|
| GET       | /registry/peers/jobs | Get all registered jobs for all peers |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/add | Add a job to be sent to a peer. See [Peer Job JSON Schema](#peer-job-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/jobs/add | Add a job to be sent to all peers. See [Peer Job JSON Schema](#peer-job-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/add/script/`{name}` | Add a job script with the given name and request body as content, to be sent to instances of a peer. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers<br/>/jobs/add/script/`{name}` | Add a job script with the given name and request body as content, to be sent to all peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/store/file/`{name}` | Add a file with the given name and request body as content, to be sent to instances of a peer, to be saved at the current working directory of the peer `goto` process. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers<br/>/jobs/store/file/`{name}` | Add a file with the given name and request body as content, to be sent to all peers, to be saved at the current working directory of the peer `goto` process. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/store/file/`{name}`?path=`{path}` | Add a file with the given name and request body as content, to be sent to instances of a peer, to be saved at the given path in the peer `goto` process' host. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers<br/>/jobs/store/file/`{name}`?path=`{path}` | Add a file with the given name and request body as content, to be sent to all peers, to be saved at the given path in the peer `goto` process' host. Pushed immediately as well as upon start of a new peer instance. |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/remove | Remove given jobs for a peer. |
| POST | /registry/peers<br/>/jobs/`{jobs}`/remove | Remove given jobs from all peers. |
| POST      | /registry/peers/`{peer}`<br/>/jobs/clear   | Remove all jobs for a peer.|
| POST      | /registry/peers<br/>/jobs/clear   | Remove all jobs from all peers.|
| GET       | /registry/peers/`{peer}`/jobs   | Get all jobs of a peer |
| GET       | /registry/peers/jobs   | Get all jobs of all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/run | Run given jobs on the given peer |
| POST | /registry/peers<br/>/jobs/`{jobs}`/run | Run given jobs on all peers |
| POST| /registry/peers/`{peer}`<br/>/jobs/run/all | Run all jobs on the given peer |
| POST| /registry/peers<br/>/jobs/run/all | Run all jobs on all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/stop | Stop given jobs on the given peer |
| POST | /registry/peers<br/>/jobs/`{jobs}`/stop | Stop given jobs on all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/stop/all | Stop all jobs on the given peer |
| POST | /registry/peers<br/>/jobs/stop/all | Stop all jobs on all peers |

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="peers-config-management-apis"></a>
#### Peers Config Management APIs

|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /registry/peers<br/>/track/headers/`{headers}` | Configure headers to be tracked by client invocations on peers. Pushed immediately as well as upon start of a new peer instance. |
| GET | /registry/peers/track/headers | Get a list of headers configured for tracking by the above `POST` API. |
| POST, PUT | /registry/peers<br/>/track/time/`{buckets}` | Configure time buckets to be tracked by client invocations on peers. Pushed immediately as well as upon start of a new peer instance. Buckets are added as a comma-separated list of low-high values in millis, e.g. `0-100,101-300,301-1000` |
| GET | /registry/peers/track/time | Get a list of time buckets configured for tracking by the above `POST` API. |
| POST, PUT | /registry/peers/probes<br/>/readiness/set?uri=`{uri}` | Configure readiness probe URI for peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/probes<br/>/liveness/set?uri=`{uri}` | Configure liveness probe URI for peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/probes<br/>/readiness/set/status=`{status}` | Configure readiness probe status for peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/probes<br/>/liveness/set/status=`{status}` | Configure readiness probe status for peers. Pushed immediately as well as upon start of a new peer instance. |
| GET | /registry/peers/probes | Get probe configuration given to registry via any of the above 4 probe APIs. |

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="peers-call-apis"></a>
#### Peers Call APIs

|METHOD|URI|Description|
|---|---|---|
| GET, POST, PUT | /registry/peers/`{peer}`<br/>/call?uri=`{uri}` | Invoke the given `URI` on the given `peer`, using the HTTP method and payload from this request |
| GET, POST, PUT | /registry/peers/call?uri=`{uri}` | Invoke the given `URI` on all `peers`, using the HTTP method and payload from this request |
| GET | /registry/peers/`{peer}`/apis | Get a list of useful APIs ready for invocation on or related to the given peer |
| GET | /registry/peers/apis | Get a list of useful APIs ready for invocation on or related to all peers |

###### <small> [Back to TOC](#goto-registry) </small>

# <a name="registry-clone-dump-and-load-apis"></a>
#### Registry Clone, Dump and Load APIs

|METHOD|URI|Description|
|---|---|---|
| POST | /registry/cloneFrom?url={url} | Clone data from another registry instance at the given URL. The current goto instance will download `peers`, `lockers`, `targets`, `jobs`, `tracking headers` and `probes`. The peer pods downloaded from the other registry are not used for any invocation by this registry, it just becomes available locally for informational purposes. Any new pods connecting to this registry using the same peer labels will use the downloaded targets, jobs, etc. |
| GET | /registry/dump | Dump current registry configs and locker data in json format. |
| POST | /registry/load | Load registry configs and locker data from json dump produced via `/dump` API. |


<details>
<summary> Registry Timeline Events </summary>

- `Registry: Peer Added`
- `Registry: Peer Rejected`
- `Registry: Peer Removed`
- `Registry: Checked Peers Health`
- `Registry: Cleaned Up Unhealthy Peers`
- `Registry: Locker Opened`
- `Registry: Locker Closed`
- `Registry: Locker Cleared`
- `Registry: All Lockers Cleared`
- `Registry: Locker Data Stored`
- `Registry: Locker Data Removed`
- `Registry: Peer Instance Locker Cleared`
- `Registry: Peer Locker Cleared`
- `Registry: All Peer Lockers Cleared`
- `Registry: Peer Events Cleared`
- `Registry: Peer Results Cleared`
- `Registry: Peer Target Rejected`
- `Registry: Peer Target Added`
- `Registry: Peer Targets Removed`
- `Registry: Peer Targets Stopped`
- `Registry: Peer Targets Invoked`
- `Registry: Peer Job Rejected`
- `Registry: Peer Job Added`
- `Registry: Peer Job File Added`
- `Registry: Peer Job Rejected`
- `Registry: Peer Job Script Rejected`
- `Registry: Peer Jobs Removed`
- `Registry: Peer Jobs Stopped`
- `Registry: Peer Jobs Invoked`
- `Registry: Peers Epochs Cleared`
- `Registry: Peers Cleared`
- `Registry: Peer Targets Cleared`
- `Registry: Peer Jobs Cleared`
- `Registry: Peer Tracking Headers Added`
- `Registry: Peer Probe Set`
- `Registry: Peer Probe Status Set`
- `Registry: Peer Called`
- `Registry: Peers Copied`
- `Registry: Lockers Dumped`
- `Registry: Dumped`
- `Registry: Dump Loaded`
- `Registry: Cloned`

#### Peer Timeline Events for registry interactions
- `Peer Registered`
- `Peer Startup Data`
- `Peer Deregistered`

</details>

See [Registry Schema JSONs and APIs Examples](docs/registry-api-examples.md)

###### <small> [Back to TOC](#goto-registry) </small>
