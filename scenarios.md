# Goto Usage Scenarios

## Scenario: Use HTTP client to send requests and track results

A very simple use case is to send HTTP traffic to one or more servers for a period of time, and collect the results per destination. To add more to it, you may want to send different kinds of HTTP requests (methods, headers), receive same/different response headers from those destinations, and track how the various destinations responded over the duration of the test in terms of response count per status code, per header, etc.

`Goto` as a client tool allows you to achieve this via some simple API calls.

1. Let's assume `goto` is running somewhere, accessible via `http://goto:8080`

2. We'll start with adding target destinations to the `goto` client. It might be a good idea to clear any previously added targets before adding new ones.
    ```
    $ curl -X POST http://goto:8080/client/targets/clear
    Targets cleared

    $ curl -s goto:8080/client/targets/add --data '
              { 
              "name": "target1",
              "method":	"POST",
              "url": "http://server-1/some/api",
              "headers":[["a", "a1"],["b", "b1"]],
              "body": "{\"test\":\"this\"}",
              "replicas": 2, "requestCount": 100, 
              "delay": "50ms", "sendID": true
              }'
    Added target: {"Name":"target1","Method":"POST","URL":"http://server-1/some/api","Headers":[["x","x1"],["y","y1"]],"Body":"{\"test\":\"this\"}","BodyReader":null,"Replicas":2,"RequestCount":100,"Delay":"50ms","SendID":true}

    $ curl -s goto:8080/client/targets/add --data '
              { 
              "name": "target2",
              "method":	"PUT",
              "url": "http://server-2/another/api",
              "headers":[["c", "c2"]],
              "body": "{\"some\":\"thing\"}",
              "replicas": 1, "requestCount": 200, 
              "delay": "30ms", "sendID": true
              }'
    Added target: {"Name":"target2","Method":"PUT","URL":"http://server-1/another/api","Headers":[["x","x2"]],"Body":"{\"some\":\"thing\"}","BodyReader":null,"Replicas":1,"RequestCount":200,"Delay":"30ms","SendID":true}

    #verify the targets were added
    $ curl -s goto:8080/client/targets | jq
    ```

    The client allows targets to be invoked with custom headers and body. Additionally, `replicas` field controls concurrency per target, allowing you to send multiple requests in parallel to each target. The above example asks for 2 concurrent requests for target1 and 1 concurrent request for target2. Field `requestCount` configures how many total requests to send per replica of a target. So, target1 with 2 `replicas` and 100 `requestCount` means a total of 200 requests, where 2 requests are sent in parallel, then next 2, and so on. Field `delay` controls the amount of time the client should wait before sending next replica requests. In the above example, the client will wait for 100ms after each pair of concurrent requests to target1. A combination of these three fields allow you to come up with many variety of traffic patterns, spreading the traffic over a period of time while also keeping a certain concurrency level.  

3. With the targets in place, let's ask `goto` client to track some response headers.
    ```
    $ curl -X POST goto:8080/client/track/headers/clear
    All tracking headers cleared

    $ curl -X PUT goto:8080/client/track/headers/add/x,y,z,foo
    Header x,y,z,foo will be tracked

    #check the tracked headers
    $ curl goto:8080/client/track/headers
    x,y,z,foo
    ```

4. Time to start the traffic. But before that, let's ask `goto` to run the load in async mode without blocking the invocation call. We'll get the results later via another API call.
    ```
    $ curl -X PUT goto:8080/client/blocking/set/N
    Invocation will not block for results

    $ curl -X POST goto:8080/client/targets/invoke/all
    Targets invoked
    ```
5. Now we get some coffee, sit back and relax. Once the job finishes, we ask `goto` for results. Assume that the servers responded with headers x (values x1 and x2) and y (values y1), and all responses were successful with HTTP 200 status, the results would look like this:
    ```
    $ curl -s goto:8080/client/results | jq
    {
      "CountsByStatus": {
        "200 OK": 400
      },
      "CountsByStatusCodes": {
        "200": 400
      },
      "CountsByHeaders": {
        "x": 400,
        "y": 200
      },
      "CountsByHeaderValues": {
        "x": {
          "x1": 200,
          "x2": 200
        },
        "y": {
          "y1": 200
        }
      },
      "CountsByTargetStatus": {
        "target1": {
          "200 OK": 200
        },
        "target2": {
          "200 OK": 200
        }
      },
      "CountsByTargetStatusCode": {
        "target1": {
          "200": 200
        },
        "target2": {
          "200": 200
        }
      },
      "CountsByTargetHeaders": {
        "target1": {
          "x": 200,
          "y": 200
        },
        "target2": {
          "x": 200
        }
      },
      "CountsByTargetHeaderValues": {
        "target1": {
          "x": {
            "x1": 200
          },
          "y": {
            "y1": 200
          }
        },
        "target2": {
          "x": {
            "x2": 200
          }
        }
      }
    }
    ```

The scenario showed how `goto` can be used for simple traffic tracing and verifying server health/availability. It's not meant to perform load testing (there are good tools available for that, like fortio), so it doesn't track latencies etc. But this


## Scenario: Test a client's behavior upon service failure

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

## Scenario: Count number of requests received at each service instance (Pod/VM) for certain headers
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

## Scenario: Track Request/Connection Timeouts

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
