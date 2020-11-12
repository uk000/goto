## Goto Basic Usage Scenarios

<br/>

## <a name="basic-client-usage"></a> Scenario: Use HTTP client to send requests and track results
#

A very simple use case is to send HTTP traffic to one or more servers for a period of time and collect the results per destination. To add more to it, you may want to send different kinds of HTTP requests (methods, headers), receive same/different response headers from those destinations, and track how the various destinations responded over the duration of the test in terms of response counts by status code, by header, etc.

`Goto` as a client tool allows you to script a test like. You can run multiple instances of `goto` as clients and servers, and then use APIs to:
- configure requests and responses, 
- trigger tests in multiple steps/iterations, and 
- get results

1. Let's use one `goto` client instance and multiple `goto` server instances for the setup. You could run multiple clients as well if needed, to trigger traffic from multiple sources. We'll assume the following URLs represent the running `goto` instances:
   - Client: `http://goto-client:8080`
   - Server 1: `http://goto-server-1:8080`
   - Server 2: `http://goto-server-2:8080`
   - Server 3: `http://goto-server-3:8080`

2. We'll start with two servers as target destinations. It might be a good idea to clear any previously added targets before adding new ones.
    ```
    $ curl -X POST http://goto-client:8080/client/targets/clear
    Targets cleared
    
    $ curl -s goto-client:8080/client/targets/add --data '
      { 
      "name": "target1",
      "method":	"POST",
      "url": "http://goto-server-1:8080/some/api",
      "headers":[["x", "x1"],["y", "y1"]],
      "body": "{\"test\":\"this\"}",
      "replicas": 2, "requestCount": 200, 
      "delay": "200ms", "sendID": true
      }'
   Added target: {"name":"target1","method":"POST","url":"http://goto-server-1:8080/some/api","headers":[["x","x1"],["y","y1"]],"body":"{\"test\":\"this\"}","bodyReader":null,"replicas":2,"requestCount":200,"delay":"200ms","keepOpen":"","sendID":true}

    $ curl -s goto-client:8080/client/targets/add --data '
      { 
      "name": "target2",
      "method":	"PUT",
      "url": "http://goto-server-2:8080/another/api",
      "headers":[["x", "x2"], ["y", "y2"]],
      "body": "{\"some\":\"thing\"}",
      "replicas": 1, "requestCount": 200, 
      "delay": "200ms", "sendID": true
      }'
    Added target: {"name":"target2","method":"PUT","url":"http://goto-server-2:8080/another/api","headers":[["x","x2"],["y","y2"]],"body":"{\"some\":\"thing\"}","bodyReader":null,"replicas":1,"requestCount":200,"delay":"200ms","keepOpen":"","sendID":true}

    #verify the targets were added
    $ curl -s goto-client:8080/client/targets | jq
    ```

    The client allows targets to be invoked with custom headers and body. Additionally, `replicas` field controls concurrency per target, allowing you to send multiple requests in parallel to each target. The above example asks for 2 concurrent requests for target1 and 1 concurrent request for target2. Field `requestCount` configures how many total requests to send per replica of a target. So, target1 with 2 `replicas` and 100 `requestCount` means a total of 200 requests, where 2 requests are sent in parallel, then next 2, and so on. Field `delay` controls the amount of time the client should wait before sending next replica requests. In the above example, the client will wait for 100ms after each pair of concurrent requests to target1. A combination of these three fields allow you to come up with many variety of traffic patterns, spreading the traffic over a period of time while also keeping a certain concurrency level.  

3. With the targets in place, let's ask `goto` client to track some response headers.
    ```
    $ curl -X POST goto-client:8080/client/track/headers/clear
    All tracking headers cleared

    $ curl -X PUT goto-client:8080/client/track/headers/add/x,y,z,foo
    Header x,y,z,foo will be tracked

    #check the tracked headers
    $ curl goto-client:8080/client/track/headers
    x,y,z,foo
    ```

4. The advantage of using goto servers as targets for this experiment is that we can ask those server instances to respond with HTTP error codes some of the times. Let's do just that. You can learn more about this specific `goto` server feature in other scenarios as well as from API docs.
    ```
    $ curl -X PUT Server8081/response/status/set/502:20
    Will respond with forced status: 502 times 20
    
    $ curl -X PUT Server8082/response/status/set/503:20
    Will respond with forced status: 503 times 20
    ```


5. Time to start the traffic. But before that, let's ask `goto` to run the load in async mode without blocking the invocation call. We'll get the results later via another API call.
    ```
    $ curl -X PUT goto-client:8080/client/blocking/set/N
    Invocation will not block for results

    $ curl -X POST goto-client:8080/client/targets/invoke/all
    Targets invoked
    ```

6. While these two targets are running, we add another target. Why? Just because we can.
    ```
    $ curl -s goto-client:8080/client/targets/add --data '
      { 
      "name": "target3",
      "method":	"OPTIONS",
      "url": "http://goto-server-3:8080/foo",
      "headers":[["foo", "bar1"], ["x", "x1"], ["y", "y1"]],
      "body": "{\"some\":\"thing\"}",
      "replicas": 3, "requestCount": 100, 
      "delay": "20ms", "sendID": true
      }'
    Added target: {"name":"target3","method":"OPTIONS","url":"http://goto-server-3:8080/foo","headers":[["foo","bar1"],["x","x1"],["y","y1"]],"body":"{\"some\":\"thing\"}","bodyReader":null,"replicas":3,"requestCount":100,"delay":"20ms","keepOpen":"","sendID":true}
    ```

7. Now we invoke this third target separately. This will start on its own while the previous two were also running.
    ```
    $ curl -X POST goto-client:8080/client/targets/target3/invoke
    Targets invoked
    ```

8. Now we have 3 targets running. Let's stop the first two in their tracks. Why? You know, just because...
    ```
    $ curl -X POST goto-client:8080/client/targets/target1,target2/stop
    Targets stopped
    ```

9. We could do more of these add/remove/stop/start operations on the targets until we get tired. Once we're done, we collect the results

      <details>
      <summary>Collect Results</summary>
      <p>

      ```json

        $ curl -s goto-client:8080/client/results | jq
          {
            "targetInvocationCounts": {
              "target1": 41,
              "target2": 20,
              "target3": 600,
              "target4": 300
            },
            "countsByStatus": {
              "200 OK": 880,
              "403 Forbidden": 40,
              "502 Bad Gateway": 20,
              "503 Service Unavailable": 20,
              "Post \"http://goto-server-1:8080/some/api?x-request-id=9fb6462c-85ff-4b51-9a1d-2da28f8fefc5\": dial tcp 1.1.1.1:8080: connect: connection refused": 1
            },
            "countsByStatusCodes": {
              "200": 880,
              "403": 40,
              "502": 20,
              "503": 20,
              "0": 1
            },
            "countsByHeaders": {
              "foo": 900,
              "goto-host": 960,
              "via-goto": 960,
              "x": 660,
              "y": 960
            },
            "countsByHeaderValues": {
              "foo": {
                "bar1": 600,
                "bar2": 300
              },
              "goto-host": {
                "1.1.1.1": 40
                "2.2.2.2": 20
                "3.3.3.3": 960
              },
              "via-goto": {
                "Server8081": 40,
                "Server8082": 20,
                "Server8083": 900
              },
              "x": {
                "x1": 640,
                "x2": 20
              },
              "y": {
                "x2": 300,
                "y1": 640,
                "y2": 20
              }
            },
            "countsByTargetStatus": {
              "target1": {
                "200 OK": 20,
                "502 Bad Gateway": 20,
                "Post \"http://goto-server-1:8080/some/api?x-request-id=9fb6462c-85ff-4b51-9a1d-2da28f8fefc5\": dial tcp 1.1.1.1:8080: connect: connection refused": 1
              },
              "target2": {
                "503 Service Unavailable": 20
              },
              "target3": {
                "200 OK": 570,
                "403 Forbidden": 30
              },
              "target4": {
                "200 OK": 290,
                "403 Forbidden": 10
              }
            },
            "countsByTargetStatusCode": {
              "target1": {
                "200": 20,
                "502": 20,
                "0": 1,
              },
              "target2": {
                "503": 20
              },
              "target3": {
                "200": 570,
                "403": 30
              },
              "target4": {
                "200": 290,
                "403": 10
              }
            },
            "countsByTargetHeaders": {
              "target1": {
                "goto-host": 40,
                "via-goto": 40,
                "x": 40,
                "y": 40
              },
              "target2": {
                "goto-host": 20,
                "via-goto": 20,
                "x": 20,
                "y": 20
              },
              "target3": {
                "foo": 600,
                "goto-host": 600,
                "via-goto": 600,
                "x": 600,
                "y": 600
              },
              "target4": {
                "foo": 300,
                "goto-host": 300,
                "via-goto": 300,
                "y": 300
              }
            },
            "countsByTargetHeaderValues": {
              "target1": {
                "goto-host": {
                  "1.1.1.1": 40
                },
                "via-goto": {
                  "Server8081": 40
                },
                "x": {
                  "x1": 40
                },
                "y": {
                  "y1": 40
                }
              },
              "target2": {
                "goto-host": {
                  "2.2.2.2": 20
                },
                "via-goto": {
                  "Server8082": 20
                },
                "x": {
                  "x2": 20
                },
                "y": {
                  "y2": 20
                }
              },
              "target3": {
                "foo": {
                  "bar1": 600
                },
                "goto-host": {
                  "3.3.3.3": 600
                },
                "via-goto": {
                  "Server8083": 600
                },
                "x": {
                  "x1": 600
                },
                "y": {
                  "y1": 600
                }
              },
              "target4": {
                "foo": {
                  "bar2": 300
                },
                "goto-host": {
                  "3.3.3.3": 300
                },
                "via-goto": {
                  "Server8083": 300
                },
                "y": {
                  "x2": 300
                }
              }
            }
          }
      ```
        
      </p>
      </details>

The results are counted overall and per-target, grouped by status, status code, header names, and header name + value. The detailed description of these result fields can be found in the Client API documentation in Readme.
- `countsByStatus`
- `countsByStatusCodes`
- `countsByHeaders`
- `countsByHeaderValues`
- `countsByTargetStatus`
- `countsByTargetStatusCode`
- `countsByTargetHeaders`
- `countsByTargetHeaderValues`

<br/>

## <a name="basic-server-usage"></a> Scenario: Use HTTP server to respond to any arbitrary client HTTP requests
#

A simple testing need is to have an HTTP application that can respond to some custom REST API calls with a given payload, so that we don't have to write custom code for the server application. You may need such server application during development time, as a stand-in for the real server application that you want to defer building but need it now to build the UI/client. Or, you may need such server application to test some existing client application, to inspect its requests (URIs invoked, headers passed, payload, etc.).

As an http server application, `goto` responds to all arbitrary URI requests, with either default status of 200, or a custom response status you configure. It also logs the details of the incoming requests: HTTP method, request URI, request headers, remote client address, etc. You can set custom response headers to be sent back for all requests, and also custom response payload. You can configure some URIs to bypass the custom response setup, in which case those URIs will respond with the default 200 status. You can even specify a default bypass response status!

Let's see some of these features in action.

1. Let's assume `goto` instance is running at `http://goto:8080`

2. 
- Certain HTTP response codes for a specific number of requests.
- Certain HTTP response headers to be added to each response
- Add a delay to responses 
`Goto` as a server application allows y



## <a name="basic-https-usage"></a> Scenario: HTTPS traffic with certificate validation
#

`Goto` client tool can be used to send HTTPS traffic and verify TLS certificates for some targets while skipping TLS validation for some other targets.

To perform TLS cert validation, `goto` needs to be given Root CA certificates. Generally, `goto`'s philosophy is to configure everything via APIs, but there are a few things that must be configured at startup. You saw some of those in the previous scenario (port, label, registry url). Root certificates location is one of them, and for good reason: if some HTTPS traffic must be invoked at a goto instance at startup (as described in previous scenario), it would need the TLS certificates right at startup.

Configuring Root CA certificates location is a simple command line argument:
```
$ goto --certs /some/location/on/local/filesystem
```
`Goto` will look for all files with extensions `.crt` and `.pem` in this directory and add those to a root cert bundle that it'd use later for HTTPS traffic. If no directory is given at startup, it looks at `/etc/certs` by default.

Additionally, for each HTTPS target, you can use the `verifyTLS` field to control whether or not the target's TLS certificate should be verified. Another relevant piece of config is the `Host` header. For non-TLS traffic, `Host` header can be used if you're sending traffic via a gateway/proxy that routes traffic based on host header. For HTTPS traffic, such gateways rely on SNI authority instead of Host header. However, `goto` will look at the `Host` header and put that as the ServerName for SNI identification (SNI stands for `Server Name Indication`, so "SNI Identification" becomes "Server Name Indication Identification"... hmmm, weird.). Let's see an example payload with both `verifyTLS` and `Host` header.
```
$ curl -v http://goto-instance/client/targets/add --data '
{ 
"name": "t1",
"method":	"GET",
"url": "https://somewhere.com",
"headers":[["Host", "somewhere.else.com"]],
"verifyTLS": true
}'
```
This config would lead to HTTPS traffic being sent to `somewhere.com`, but SNI authority set to `somewhere.else.com`, and it would verify the TLS certificates presented by the target server against `somewhere.else.com` host and using the Root CAs read from the command line arg location. Sweet!

<br/>

## <a name="basic-header-tracking"></a> Scenario: Count number of requests received at each server instance for certain headers
#

One of the basic things we may want to track is, to observe a client's or proxy's behavior in terms of distributing traffic load across various endpoints of a service. While many clients/proxies may provide metrics to inform you about the number of requests it sent per service endpoint (IP), but what if you wanted to track it by headers: i.e., how many requests received per service endpoint per header.

The `goto` tool can be used to achieve this simply by putting a `goto` instance in proxy mode in front of each service instance and enable tracking for the specific headers you wish to track. Let's look at the sample API calls with the assumption of two service instances `http://service-1` and `http://service-2`, and a `goto` instance in front of each service, `http://goto-1` and `http://goto-2`.

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


  <details>
  <summary>Results</summary>
  <p>

  ```json

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
  
  </p>
  </details>

<br/>
