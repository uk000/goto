# Goto Resiliency Testing Usage Scenarios

# <a name="client-resiliency-upon-service-failure"></a> Scenario: Test a client's behavior upon service failure

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
    '{"name": "myServer", "match":{"uris":["/my/fancy/api"]}, "endpoint":"http://realserver", "routes":{"/": ""}, "enabled":true}'
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

6. Now the client will receive `HTTP 503` for next 2 requests. Have the client send requests and observe client's behavior for next 2 failures followed by subsequent successes.
   
    ```
    curl -v http://goto:8080/my/fancy/api
    curl -v http://goto:8080/my/fancy/api
    curl -v http://goto:8080/my/fancy/api
    ```

<br/>

As this small scenario demonstrated, `goto` lets you inject controlled failure on the fly in the traffic flow between a client and a service for some complex chaos testing. The above scenario was still relatively simpler, as we didn't even test against multiple service pods/instances. We could have run one `goto` for each service pod, and each of those `goto` could be configured to respond with some specific response codes for a specific number of times, and then you'd run your traffic and observe some coordinated failures and recoveries. The possibilities of such chaos testing are endless. The `goto` tool makes is possible to script such controlled chaos testing.

<br/>


# <a name="server-resiliency-client-hangups"></a> Scenario: Track client hangups on server via request/connection timeouts 

Say you want to monitor/track how often a client (or proxy/sidecar) performs a request/connection timeout, and the client/server/proxy/sidecar behavior when the request or connection times out. This tool provides a deterministic way to simulate the timeout behavior.

<br/>

1. With this application running as the server, enable timeout tracking on the server side either for all requests or for certain headers.

   ```
   #enable timeout tracking for all requests
   curl -X POST goto:8080/request/timeout/track/all

   ```

2. Set a large delay on all responses on the server. Make sure the delay duration is larger than the timeout config on the client application or sidecar that you intend to test.

   ```
   curl -X PUT goto:8080/response/delay/set/10s
   ```

3. Run the client application with its configured timeout. The example below shows curl, but this would be a real application being investigated

    ```
    curl -v -m 5 goto:8080/someuri
    curl -v -m 5 goto:8080/someuri
    ```

4. Check the timeout stats tracked by the server

    ```
    curl goto:8080/request/timeout/status
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
