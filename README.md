#
# goto

## What is it?
An HTTP server+client testing tool in one. 

## Why?
It's hard to find some of these features together in a single tool

## What Features?
See below

## How to use it?
It's available as a docker image: https://hub.docker.com/repository/docker/uk0000/goto.
Or build it locally on your machine
```
go build -o goto .
```

<br/>

#
# Scenarios

Before we look into detailed features and APIs exposed by the tool, let's look at how this tool can be used in a few scenarios to understand it better.

### Scenario: Test a client's behavior upon service failure
Suppose you have a client application that connects to a service for some API (`/my/api`). Either the client, or a sidecar/proxy (e.g. envoy), has some in-built resiliency capability so that it retries upon certain kind of failures (e.g. if the service responds with `HTTP 503`). The client or the proxy (e.g. envoy) may possibly even attempt to reconnect to a different endpoint of the service.

This `goto` tool is the ideal tool to goto [yeah, intended :)] to test such resiliency behavior of the client or the proxy, in two possible ways:

1) Run `goto` as a server that the client/proxy sends requests to, and `goto` can be configured to respond with various kinds of responses.
2) Run `goto` as a forwarding proxy layer in front of real server application, let it intercept all the calls and forward those to the server application. When you want to fail the service temporarily, ask `goto` to temporarily respond with a failure code, e.g. `HTTP 503`.

Let's look at the second setup in more details as that's more exciting of the two. 

1. Assume the real service application is accessible over URL `http://realserver`. Currently your client app connects to this server, and you want to test the resiliency behavior between this pair for URI `/my/fancy/api`.

   ```
   curl -v http://realserver/my/fancy/api
   ```

2. Run `goto` server somewhere (local machine, a pod, a VM). Let's suppose the `goto` tool is accessible over URL `http://goto:8080`. You configure the client to connect to goto's url now.

    ```
    #run goto
    goto --port 8080

    #confirm it's responding
    curl -v http://goto:8080
    ```

3. Add a forwarding proxy target on `goto` to intercept traffic for URI `/my/fancy/api` and forward it to real server application at `http://realserver`

    ```
    curl http://goto:8080/request/proxy/targets/add --data \
    '{"name": "myServer", "match":{"uris":["/my/fancy/api"]}, "url":"http://realserver", "enabled":true}'
    ```

    Now `goto` will proxy all requests to the server application. Confirm it:

    ```
    curl -v http://goto:8080/my/fancy/api
    ```

4. Reconfigure your client app to connect to this new URL: `http://goto:8080/my/fancy/api`. Client requests will be forwarded to the server with all headers and payload, and response sent back to the client. Some additional response headers are added by `goto` to show that the request was indeed proxied via it. These response headers are described later in this document.

<br/>

5. Now it's time to introduce some chaos. We'll ask the `goto` to respond with `HTTP 503` response code for exactly next 2 requests.
   
    ```
    curl -X PUT http://goto:8080/response/status/set/503:2
    ```
   
    The path parameter `503:2` has a syntax of `<Status Code>:<Number of Responses>`. So, `503:2` tells `goto` to respond with `503` status for next 2 requests of any non-admin URI calls. Admin URIs are the ones that are used to configure `goto`, like the one we just used: `/response/status/set`. You can find out more about various admin URIs later in the doc. 

<br/>

6. Now the client will receive `HTTP 503` for next 2 requests. Have the client send requests now, and observe client's behavior for next 2 failures followed by subsequent successes.
   
    ```
    curl -v http://goto:8080/my/fancy/api
    curl -v http://goto:8080/my/fancy/api
    curl -v http://goto:8080/my/fancy/api
    ```

<br/>

As this small scenario demonstrated, `goto` lets you inject controlled failure on the fly in the traffic flow between a client and a service for some complex chaos testing. The above scenario was still relatively simpler, as we didn't even test against multiple service pods/instances. We could have run one `goto` for each service pod, and each of those `goto` could be configured to respond with some specific response codes for a specific number of times, and then you'd run your traffic and observe some coordinated failures and recoveries. The possibilities of such chaos testing are endless. The `goto` tool makes is possible to script such controlled chaos testing.

<br/>

### Scenario: Count number of requests received at each service instance (Pod/VM) for certain headers
One of the basic things we may want to track is, to observe a client's or proxy's behavior in terms of distributing traffic load across various endpoints of a service. While many clients/proxies may provide metrics to inform you about the number of requests it sent per service endpoint (IP), but what if you wanted to track it by headers: i.e., how many requests received per service endpoint per header.

The `goto` tool can be used to achieve this simply by putting a `goto` instance in proxy mode in front of each service instance, and enable tracking for the specific headers you wish to track. Let's look at the sample API calls with the assumption of two service instances `http://service-1` and `http://service-2`, and a `goto` instance in front of each service, `http://goto-1` and `http://goto-2`.

Clear and add tracking headers to `goto` instances:

```
curl -X POST http://goto-1:8080/request/headers/track/clear

curl -X PUT http://goto-1:8080/request/headers/track/add/foo,bar

curl -X POST http://goto-2:8080/request/headers/track/clear

curl -X PUT http://goto-2:8080/request/headers/track/add/foo,bar
```

The above API calls configure the `goto` instances to track headers `foo` and `bar`. 

Now add proxy target(s) with the relevant match criteria to each `goto` instance:

```
  curl http://goto-1:8080/request/proxy/targets/add --data '{"name": "service-1", \
  "url":"http://service-1", \
  "match":{"uris":["/"]}, \
  "enabled":true}'

  curl http://goto-2:8080/request/proxy/targets/add --data '{"name": "service-2", \
  "url":"http://service-2", \
  "match":{"uris":["/"]}, \
  "enabled":true}'
```

Both `goto` instances have now been configured to forward all traffic (URI match `/`) to the corresponding service instances. Now we send some traffic with various headers:

```
  curl http://goto-1:8080/some/uri -Hfoo:foo1
  curl http://goto-1:8080/some/uri -Hfoo:foo1 -Hbar:bar1
  curl http://goto-2:8080/some/uri -Hbar:bar2
  curl http://goto-2:8080/some/uri -Hfoo:foo2 -Hbar:bar2
```

Once the traffic we want to observe has flown, we ask the `goto` instances to give us counts for the tracked headers:

```
  curl http://goto-1:8080/request/headers/track/counts |  jq
  curl http://goto-2:8080/request/headers/track/counts |  jq
```

Header tracking counts results payload from a `goto` instance will look like this:

```
{
  "foo": {
    "RequestCountsByHeaderValue": {
      "1": 8
    },
    "RequestCountsByHeaderValueAndRequestedStatus": {},
    "RequestCountsByHeaderValueAndResponseStatus": {
      "1": {
        "200": 8
      }
    }
  },
  "x": {
    "RequestCountsByHeaderValue": {
      "x1": 2,
      "x2": 1
    },
    "RequestCountsByHeaderValueAndRequestedStatus": {},
    "RequestCountsByHeaderValueAndResponseStatus": {
      "x1": {
        "200": 2
      },
      "x2": {
        "200": 1
      }
    }
  },
  "y": {
    "RequestCountsByHeaderValue": {},
    "RequestCountsByHeaderValueAndRequestedStatus": {},
    "RequestCountsByHeaderValueAndResponseStatus": {}
  },
  "z": {
    "RequestCountsByHeaderValue": {
      "z4": 12
    },
    "RequestCountsByHeaderValueAndRequestedStatus": {},
    "RequestCountsByHeaderValueAndResponseStatus": {
      "z4": {
        "200": 12
      }
    }
  }
}
```

<br/>

### Scenario: Track Request/Connection Timeouts

Say you want to monitor/track how often a client (or proxy/sidecar) performs a request/connection timeout, and the client/server/proxy/sidecar behavior when the request or connection times out. This tool provides a deterministic way to simulate the timeout behavior.

<br/>

1. With this application running as the server, enable timeout tracking on the server side either for all requests or for certain headers.

   ```
   #enable timeout tracking for all requests
   curl -X POST localhost:8080/request/timeout/track/all

   ```

2. Set a large delay on all responses on the server. Make sure the delay duration is larger than the timeout config on the client application or sidecar that you intend to test.

   ```
   curl -X PUT localhost:8080/response/delay/set/10s
   ```

3. Run the client application with its configured timeout. The example below shows curl, but this would be a real application being investigated

    ```
    curl -v -m 5 localhost:8080/someuri
    curl -v -m 5 localhost:8080/someuri
    ```

4. Check the timeout stats tracked by the server

    ```
    curl localhost:8080/request/timeout/status
    ```

    The timeout stats would look like this:

    ```
    {
      "all": {
        "ConnectionClosed": 8,
        "RequestCompleted": 2
      },
      "headers": {
        "x": {
          "x1": {
            "ConnectionClosed": 2,
            "RequestCompleted": 0
          }
        },
        "y": {
          "y2": {
            "ConnectionClosed": 2,
            "RequestCompleted": 1
          }
        }
      }
    }
    ```

<br/>

  <span style="color:red">
  TODO: There are many more possible scenarios to describe here, to show how this tool can be used for various kinds of chaos testing and investigations.
  </span>

<br/>

#
# Features

It's an HTTP client and server built into a single application. 

As a server, it can act as an HTTP proxy that lets you intercept HTTP requests and get some insights (e.g. based on headers) before forwarding it to its destination. But it can also respond to requests as a server all by itself, while still capturing interesting stats and counters that can be used to correlate information against the client.

As a client, it allows sending requests to various destinations and tracking responses by headers and response status code.

The application exposes both client and server features via various management REST APIs as described below. Additionally, it can respond to all undefined URIs with a configurable status code.

First things first, run the application:
```
go run main.go --port 8080
```
Or, build and run
```
go build -o goto .
./goto
```

Once the server is up and running, rest of the interactions and configurations are done purely via REST APIs.

Let's look at the APIs for server features.

<br/>

#
# Server Features
The server is useful to be run as a test server for testing some client application, proxy/sidecar, gateway, etc. Or, the server can also be used as a proxy to be put in between a client and a target server application, so that traffic flows through this server where headers can be inspected/tracked before proxying the requests further. The server can add headers, replace request URI with some other URI, add artificial delays to the response, respond with a specific status, monitor request/connection timeouts, etc. The server tracks all the configured parameters, applying those to runtime traffic and building metrics, which can be viewed via various APIs.

<br/>

#
## Listeners


The server starts with a single http listener on port given to it as command line arg (defaults to 8080). It exposes listener APIs to let you manage additional HTTP listeners (TCP support will come in the future). The ability to launch and shutdown listeners lets you do some chaos testing. All listener ports respond to the same set of API calls, so any of the APIs described below as well as runtime traffic proxying can be done via any active listener.


#### APIs
|METHOD|URI|Description|
|---|---|---|
| POST       | /listeners/add           | Add a listener|
| POST, PUT  | /listeners/{port}/remove | Remove a listener|
| POST, PUT  | /listeners/{port}/open   | Open an added listener to accept traffic|
| POST, PUT  | /listeners/{port}/close  | Close an added listener|
| GET        | /listeners               | Get a list of listeners |

#### Listener JSON Schema
|Field|Data Type|Description|
|---|---|---|
|label    |string | Label to be applied to the listener. This can also be set/changed via REST API later|
|port     |int    | Port on which the new listener will listen on|
|protocol |string | Currently only `http`. TCP support will come soon.|


#### Listener API Examples:
```
curl localhost:8080/listeners/add --data '{"port":8081, "protocol":"http", "label":"Server-8081"}'

curl -X POST localhost:8080/listeners/8081/remove

curl -X PUT localhost:8080/listeners/8081/open

curl -X PUT localhost:8080/listeners/8081/close

curl localhost:8081/listeners
```

<br/>
<br/>

#
## Listener Label

By default, each listener adds a header `Via-Goto: <port>` to each response it sends, where <port> is the port on which the listener is running (default being 8080). A custom label can be added to a listener using the label APIs described below. In addition to `Via-Goto`, each listener also adds another header `Goto-Host` that carries the pod/host name, pod namespace (or `local` if not running as a kubernetes pod), and pod/host IP address to identify where the response came from.

#### APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /label/set/{label}  | Set label for this port |
| PUT       | /label/clear        | Remove label for this port |
| GET       | /label              | Get current label of this port |

#### Listener Label API Examples:
```
curl -X PUT localhost:8080/label/set/Server-8080

curl -X PUT localhost:8080/label/clear

curl localhost:8080/label
```

<br/>
<br/>

#
## Request Headers Tracking

#### APIs
|METHOD|URI|Description|
|---|---|---|
|POST     | /request/headers/track/clear									|Remove all tracked headers|
|PUT, POST| /request/headers/track/add/{headers}					|Add headers to track|
|PUT, POST|	/request/headers/track/{header}/remove				|Remove a specific tracked header|
|GET      | /request/headers/track/{header}/counts				|Get counts for a tracked header|
|PUT, POST| /request/headers/track/counts/clear/{headers}	|Clear counts for given tracked headers|
|POST     | /request/headers/track/counts/clear						|Clear counts for all tracked headers|
|GET      | /request/headers/track/counts									|Get counts for all tracked headers|

#### Request Headers Tracking API Examples:
```
curl -X POST localhost:8080/request/headers/track/clear

curl -X PUT localhost:8080/request/headers/track/add/x,y

curl -X PUT localhost:8080/request/headers/track/remove/x

curl -X POST localhost:8080/request/headers/track/counts/clear/x

curl -X POST localhost:8080/request/headers/track/counts/clear

curl -X POST localhost:8080/request/headers/track/counts/clear
```

#### Request Tracking Results Example
```
$ curl localhost:8080/request/headers/track/counts

{
  "x": {
    "RequestCountsByHeaderValue": {
      "x1": 20
    },
    "RequestCountsByHeaderValueAndRequestedStatus": {
      "x1": {
        "418": 20
      }
    },
    "RequestCountsByHeaderValueAndResponseStatus": {
      "x1": {
        "418": 20
      }
    }
  },
  "y": {
    "RequestCountsByHeaderValue": {
      "y1": 20
    },
    "RequestCountsByHeaderValueAndRequestedStatus": {
      "y1": {
        "418": 20
      }
    },
    "RequestCountsByHeaderValueAndResponseStatus": {
      "y1": {
        "418": 20
      }
    }
  }
}
```

<br/>
<br/>

#
## Request Proxying

The APIs allow proxy targets to be configured, and those can also be invoked manually for testing the configuration. However, the real fun happens when the proxy targets are matched with runtime traffic based on the match criteria specified in a proxy target's spec (based on headers or URIs), and one or more matching targets get invoked for a given request.

#### APIs
|METHOD|URI|Description|
|---|---|---|
|POST     |	/request/proxy/targets/add              | Add target for proxying requests |
|PUT, POST| /request/proxy/targets/{target}/remove  | Remove a proxy traget |
|PUT, POST| /request/proxy/targets/{target}/enable  | Enable a proxy target |
|PUT, POST| /request/proxy/targets/{target}/disable | Disable a proxy target |
|POST     |	/request/proxy/targets/{targets}/invoke | Invoke proxy targets by name |
|POST     |	/request/proxy/targets/invoke/{targets} | Invoke proxy targets by name |
|POST     |	/request/proxy/targets/clear            | Remove all proxy targets |
|GET 	    |	/request/proxy/targets                  | Invoke all proxy targets |


#### Proxy Target JSON Schema
|Field|Data Type|Description|
|---|---|---|
| name          | string                                | Name for this target |
| url           | string                                | URL for the target. Request's URI or Override URI gets added to the URL for each proxied request. |
| sendId        | bool           | Whether or not a unique ID be sent with each request. If this flag is set, a query param 'id' will be added to each request, which can help with tracing requests on the target servers |
| replaceURI    | string                                | URI to be used in place of the original request URI.|
| addHeaders    | `[][]string`                            | Additional headers to add to the request before proxying |
| removeHeaders | `[]string `                             | Headers to remove from the original request before proxying |
| addQuery      | `[][]string`                            | Additional query parameters to add to the request before proxying |
| removeQuery   | `[]string`                              | Query parametes to remove from the original request before proxying |
| match        | `{headers: [][]string, uris: []string, , query: [][]string}`     | Match criteria based on which runtime traffic gets proxied to this target. See detailed explanation below |
| replicas     | int     | Number of parallel replicated calls to be made to this target for each matched request. This allows each request to result in multiple calls to be made to a target if needed for some test scenarios |
| enabled       | bool     | Whether or not the proxy target is currently active |


##### Proxy Target Match Criteria
Proxy target match criteria specify the URIs, headers and query parameters, matching either of which will cause the request to be proxied to the target.

- URIs: specified as a list of URIs, with `{foo}` to be used for variable portion of a URI. E.g., `/foo/{f}/bar/{b}` will match URIs like `/foo/123/bar/abc`, `/foo/something/bar/otherthing`, etc. The variables are captured under the given labels (f and b in previous example). If the target is configured with `replaceURI` to proxy the request to a different URI than the original request, the `replaceURI` can refer to those capturing variables using the syntax described in this example:
  
  ```
  curl http://goto:8080/request/proxy/targets/add --data \
  '{"name": "target1", "url":"http://somewhere", \
  "match":{"uris":["/foo/{x}/bar/{y}"]}, \
  "replaceURI":"/abc/{y:.*}/def/{x:.*}", \
  "enabled":true, "sendID": true}'
  ```
  
  This target will be triggerd for requests with the pattern `/foo/<somex>/bar/<somey>` and the request will be forwarded to the target as `http://somewhere/abc/somey/def/somex`, where the values `somex` and `somey` are extracted from the original request and injected into the replacement URI.

  URI match `/` has the special behavior of matching all traffic.

<br/>

- Headers: specified as a list of key-value pairs, with the ability to capture values in named variables and reference those variables in the `addHeaders` list. A target is triggered if any of the headers in the match list are present in the request (headers are matched using OR instead of AND). The variable to capture header value is specified as `{foo}`, and can be referenced in the `addHeaders` list again as `{foo}`. This example will make it clear:

  ```
  curl http://goto:8080/request/proxy/targets/add --data \
  '{"name": "target2", "url":"http://somewhere", \
  "match":{"headers":[["foo", "{x}"], ["bar", "{y}"]]}, \
  "addHeaders":[["abc","{x}"], ["def","{y}"]], "removeHeaders":["foo"], \
  "enabled":true, "sendID": true}'
  ```

  This target will be triggered for requests carrying headers `foo` or `bar`. On the proxied request, additional headers will be set: `abc` with value copied from `foo`, an `def` with value copied from `bar`. Also, header `foo` will be removed from the proxied request.

<br/>

- Query: specified as a list of key-value pairs, with the ability to capture values in named variables and reference those variables in the `addQuery` list. A target is triggered if any of the query parameters in the match list are present in the request (matched using OR instead of AND). The variable to capture query parameter value is specified as `{foo}`, and can be referenced in the `addQuery` list again as `{foo}`. Example:

    ```
  curl http://goto:8080/request/proxy/targets/add --data \
  '{"name": "target3", "url":"http://somewhere", \
  "match":{"query":[["foo", "{x}"], ["bar", "{y}"]]}, \
  "addQuery":[["abc","{x}"], ["def","{y}"]], "removeQuery":["foo"], \
  "enabled":true, "sendID": true}'
  ```

  This target will be triggered for requests with carrying query params `foo` or `bar`. On the proxied request, query param `foo` will be removed, and additional query params will be set: `abc` with value copied from `foo`, an `def` with value copied from `bar`. For incoming request `http://goto:8080?foo=123&bar=456` gets proxied as `http://somewhere?abc=123&def=456&bar=456`. 

<br/>

#### Request Proxying API Examples:
```
curl -s -X POST localhost:8080/request/proxy/targets/clear

curl -s localhost:8081/request/proxy/targets/add --data '{"name": "t1", \
"match":{"uris":["/x/{x}/y/{y}"], "query":[["foo", "{f}"]]}, \
"url":"http://localhost:8083", \
"replaceURI":"/abc/{y:.*}/def/{x:.*}", \
"addHeaders":[["z","z1"]], \
"addQuery":[["bar","{f}"]], \
"removeQuery":["foo"], \
"replicas":1, "enabled":true, "sendID": true}'

curl -s localhost:8081/request/proxy/targets/add --data '{"name": "t2", \
"match":{"headers":[["foo"]]}, \
"url":"http://localhost:8083", \
"replaceURI":"/echo", \
"addHeaders":[["z","z2"]], \
"replicas":1, "enabled":true, "sendID": false}'

curl -s localhost:8082/request/proxy/targets/add --data '{"name": "t3", \
"match":{"headers":[["x", "{x}"], ["y", "{y}"]], "uris":["/foo"]}, \
"url":"http://localhost:8083", \
"replaceURI":"/echo", \
"addHeaders":[["z","{x}"], ["z","{y}"]], \
"removeHeaders":["x", "y"], \
"replicas":1, "enabled":true, "sendID": true}'

curl -s -X PUT localhost:8080/request/proxy/targets/t1/remove

curl -s -X PUT localhost:8080/request/proxy/targets/t2/disable

curl -s -X PUT localhost:8080/request/proxy/targets/t2/enable

curl -v -X POST localhost:8080/request/proxy/targets/t1/invoke

curl -s localhost:8080/request/proxy/targets
```

<br/>

#
## Request Timeout


#### APIs
|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /request/timeout/track/headers/{headers}  | Add one or more headers. Requests carrying these headers will be tracked for timeouts and reported |
|PUT, POST| /request/timeout/track/all                | Enable request timeout tracking for all requests |
|POST     |	/request/timeout/track/clear              | Clear timeout tracking configs |
|POST     |	/request/timeout/status                   | Get a report of tracked request timeouts so far |


#### Request Timeout API Examples
```
curl -X POST localhost:8080/request/timeout/track/headers/x,y

curl -X POST localhost:8080/request/timeout/track/headers/all

curl -X POST localhost:8080/request/timeout/track/clear

curl localhost:8080/request/timeout/status
```



<br/>
<br/>

#
## Request URI Bypass

#### APIs
|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /request/uri/bypass/add?uri={uri}       | Add a bypass URI |
|PUT, POST| /request/uri/bypass/remove?uri={uri}    | Remove a bypass URI |
|PUT, POST| /request/uri/bypass/clear               | Remove all bypass URIs |
|PUT, POST| /request/uri/bypass/status/set/{status} | Set status code to be returned for bypass URI requests |
|GET      |	/request/uri/bypass/list                | Get list of bypass URIs |
|GET      |	/request/uri/bypass                     | Get list of bypass URIs |
|GET      |	/request/uri/bypass/status              | Get current bypass URI status code |
|GET      |	/request/uri/bypass/counts?uri={uri}    | Get request counts for a given bypass URI |


#### Request URI Bypass API Examples
```
curl -X POST localhost:8080/request/uri/bypass/clear

curl -X PUT localhost:8080/request/uri/bypass/add\?uri=/foo

curl -X PUT localhost:8081/request/uri/bypass/remove\?uri=/bar

curl -X PUT localhost:8080/request/uri/bypass/status/set/418

curl localhost:8081/request/uri/bypass/list

curl localhost:8080/request/uri/bypass

curl localhost:8080/request/uri/bypass/status

curl localhost:8080/request/uri/bypass/counts\?uri=/foo
```



<br/>
<br/>

#
## Response Delay

#### APIs
|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /response/delay/set/{delay} | Set a delay for non-management requests (i.e. runtime traffic) |
| PUT, POST | /response/delay/clear       | Remove currently set delay |
| GET       |	/response/delay             | Get currently set delay |

* Delay is specified as duration, e.g. 1s

#### Response Delay API Examples

```
curl -X POST localhost:8080/response/delay/clear

curl -X PUT localhost:8080/response/delay/set/2s

curl localhost:8080/response/delay
```


<br/>
<br/>

#
## Response Headers

#### APIs
|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /response/headers/add/{header}/{value}  | Add a custom header to be sent with all resopnses |
| PUT, POST | /response/headers/remove/{header}       | Remove a previously added custom response header |
| POST      |	/response/headers/clear                 | Remove all configured custom response headers |
| GET       |	/response/headers/list                  | Get list of configured custom response headers |
| GET       |	/response/headers                       | Get list of configured custom response headers |

#### Response Headers API Examples
```
curl -X POST localhost:8080/response/headers/clear

curl -X POST localhost:8080/response/headers/add/x/x1

curl localhost:8080/response/headers/list

curl -X POST localhost:8080/response/headers/remove/x

curl localhost:8080/response/headers
```



<br/>
<br/>

#
## Response Status


#### APIs
|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /response/status/set/{status}     | Set a forced response status that all non-proxied and non-management requests will be responded with |
| PUT, POST |	/response/status/clear            | Remove currently configured forced response status, so that all subsequent calls will receive their original deemed response |
| PUT, POST | /response/status/counts/clear     | Clear counts tracked for response statuses |
| GET       |	/response/status/counts/{status}  | Get request counts for a given status |
| GET       |	/response/status/counts           | Get request counts for all response statuses so far |
| GET       |	/response/status                  | Get the currently configured forced response status |

#### Response Status API Examples
```
curl -X POST localhost:8080/response/status/counts/clear

curl -X POST localhost:8080/response/status/clear

curl -X PUT localhost:8080/response/status/set/502

curl -X PUT localhost:8080/response/status/set/0

curl -X POST localhost:8080/response/status/counts/clear

curl localhost:8080/response/status/counts

curl localhost:8080/response/status/counts/502
```

#### Response Status Tracking Result Example
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



<br/>
<br/>

#
## Status API

#### APIs
|METHOD|URI|Description|
|---|---|---|
| GET       |	/status/{status}                  | This call either receives the given status, or the forced response status if one is set |

#### Status Call Examples
```
curl -I  localhost:8080/status/418
```



<br/>
<br/>

#
## CatchAll

Any request that doesn't match any of the defined management APIs, and also doesn't match any proxy targets, gets treated by a catch-all response that sends HTTP 200 response by default (unless an override response code is set)


<br/>
<br/>
<br/>

#
# Client Features
As a client tool, the server allows targets to be configured and invoked via REST APIs. Headers can be set to track results for target invocations, and APIs make those results available for consumption as JSON output. the invocation results get accumulated across multiple invocations until cleard explicitly.


#### APIs
|METHOD|URI|Description|
|---|---|---|
| POST      | /client/targets/add                   | Add a target for invocation |
| PUT, POST |	/client/targets/{target}/remove       | Remove a target |
| POST      | /client/targets/{targets}/invoke      | Invoke given targets |
| POST      |	/client/targets/invoke/all            | Invoke all targets |
| POST      | /client/targets/{targets}/stop        | Stops a running target |
| POST      | /client/targets/stop/all              | Stops all running targets |
| GET       |	/client/targets/list                  | Get list of currently configured targets |
| GET       |	/client/targets                       | Get list of currently configured targets |
| POST      |	/client/targets/clear                 | Remove all targets |
| PUT, POST |	/client/reporting/set/{flag}          | Set whether target call responses should be sent back as response with the `invoke` call |
| GET       |	/client/reporting                     | Get current state of the reporting flag |
| PUT, POST |	/client/track/headers/add/{headers}   | Add headers for tracking response counts per target (i.e. number of responses received per target per header) |
| PUT, POST |	/client/track/headers/remove/{headers}| Remove headers from tracking set |
| POST      | /client/track/headers/clear           | Remove all tracked headers |
| GET       |	/client/track/headers/list            | Get list of tracked headers |
| GET       |	/client/track/headers                 | Get list of tracked headers |
| GET       |	/client/results                       | Get invocation results in JSON format. |
| POST      | /client/results/clear                 | Clear previously accumulated invocation results |


#### Client Target JSON Schema
|Field|Data Type|Description|
|---|---|---|
| name         | string         | Name for this target |
| method       | string         | Name for this target |
| url          | string         | URL for the target   |
| headers      | [][]string     | Headers to be sent for this target |
| body         | string         | Request body to use for this target|
| replicas     | int            | Number of parallel replicated calls to be made to this target for |
| requestCount | int            | Number of requests to be made to all replicas of this client. The final request count becomes replicas * requestCount  |
| delay        | duration       | Minimum delay to be added per request. The actual added delay will be the max of all the targets being invoked in a given round of invocation |
| sendId       | bool           | Whether or not a unique ID be sent with each client request. If this flag is set, a query param 'id' will be added to each request, which can help with tracing requests on the target servers |

#### Client API Examples
```
curl -s localhost:8080/client/targets/add --data '{"name": "t1", \
"method":"GET", "url":"http://localhost:8080/status/418", \
"headers":[["foo", "bar"],["x", "x1"],["y", "y1"]], \
"replicas": 2, "delay": "1s", "requestCount": 5, "sendId": true}'

curl -X PUT localhost:8080/client/targets/t3/remove

curl -s localhost:8080/client/targets/list

curl -X POST localhost:8080/client/targets/t2,t3/invoke

curl -X POST localhost:8080/client/targets/invoke/all

curl -X POST localhost:8080/client/targets/t2,t3/stop

curl -X POST localhost:8080/client/targets/stop/all

curl -X POST localhost:8080/client/targets/clear

curl -X PUT localhost:8080/client/reporting/set/n

curl localhost:8080/client/reporting

curl -X POST localhost:8080/client/track/headers/clear

curl -X PUT localhost:8080/client/track/headers/add/Goto-Host,Via-Goto,x,y,z,foo

curl -X PUT localhost:8080/client/track/headers/remove/foo

curl localhost:8080/client/track/headers/list

curl -X POST localhost:8080/client/results/clear

curl -s localhost:8080/client/results
```

#### Sample Client Invocation Result (including error reporting example)
```
{
  "CountsByStatus": {
    "200 OK": 3,
    "418 I'm a teapot": 2,
    "Put \"http://localhost:8082/debug?x-request-id=t3[1]\": dial tcp [::1]:8082: connect: connection refused": 1,
  },
  "CountsByStatusCodes": {
    "0": 1,
    "200": 3,
    "418": 2
  },
  "CountsByHeaders": {
    "server": 4,
    "server-host": 4,
    "x": 2,
    "y": 2
  },
  "CountsByHeaderValues": {
    "server": {
      "8080": 2,
      "8081": 2,
    },
    "server-host": {
      "ServerA": 4,
      "ServerB": 1
    },
    "x": {
      "x2": 2
    },
    "y": {
      "y2": 2
    }
  },
  "CountsByTargetStatus": {
    "t1": {
      "418 I'm a teapot": 2
    },
    "t2": {
      "200 OK": 2
    },
    "t3": {
      "200 OK": 1,
      "Put \"http://localhost:8082/debug?x-request-id=t3[1]\": dial tcp [::1]:8082: connect: connection refused": 1,
    }
  },
  "CountsByTargetStatusCode": {
    "t1": {
      "418": 2
    },
    "t2": {
      "200": 2
    },
    "t3": {
      "0": 1,
      "200": 1
    }
  },
  "CountsByTargetHeaders": {
    "t1": {
      "server": 2,
      "server-host": 2
    },
    "t2": {
      "server": 2,
      "server-host": 2,
      "x": 2,
      "y": 2
    },
    "t3": {
      "server": 1,
      "server-host": 1
    }
  },
  "CountsByTargetHeaderValues": {
    "t1": {
      "server": {
        "8080": 2
      },
      "server-host": {
        "ServerA": 2
      }
    },
    "t2": {
      "server": {
        "8081": 2
      },
      "server-host": {
        "ServerA": 2
      },
      "x": {
        "x2": 2
      },
      "y": {
        "y2": 2
      }
    },
    "t3": {
      "server": {
        "8082": 1
      },
      "server-host": {
        "ServerB": 1
      }
    }
  }
}
```