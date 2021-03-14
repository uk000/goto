#### Client API Examples
<details>
<summary>API Examples</summary>

```
#Add target
curl localhost:8080/client/targets/add --data '
{
  "name": "t1",
  "method":	"POST",
  "url": "http://somewhere:8080/foo",
  "protocol":"HTTP/2.0",
  "headers":[["x", "x1"],["y", "y1"]],
  "body": "{\"test\":\"this\"}",
  "replicas": 2, 
  "requestCount": 2,
  "initialDelay": "1s",
  "delay": "200ms", 
  "sendID": true,
  "autoInvoke": true
}'

curl -s localhost:8080/client/targets/add --data '
{
  "name": "ab",
  "method": "POST",
  "url": "http://localhost:8081/foo",
  "burls": ["http://localhost:8080/b1", "http://localhost:8080/b2", "http://localhost:8080/b3"],
  "body": "some body",
  "abMode": true,
  "replicas": 2,
  "requestCount": 2,
  "sendID": true
}'

curl -s localhost:8080/client/targets/add --data '
{
  "name": "ab",
  "method": "POST",
  "url": "http://localhost:8081/foo",
  "burls": ["http://localhost:8080/bar"]
  "body": "some body",
  "fallback": true,
  "replicas": 2,
  "requestCount": 2,
  "sendID": true
}'


#List targets
curl localhost:8080/client/targets

#Remove select target
curl -X POST localhost:8080/client/target/t1,t2/remove

#Clear all configured targets
curl -X POST localhost:8080/client/targets/clear

#Invoke select targets
curl -X POST localhost:8080/client/targets/t2,t3/invoke

#Invoke all targets
curl -X POST localhost:8080/client/targets/invoke/all

#Stop select targets across all running batches
curl -X POST localhost:8080/client/targets/t2,t3/stop

#Stop all targets across all running batches
curl -X POST localhost:8080/client/targets/stop/all

#Set blocking mode
curl -X POST localhost:8080/client/blocking/set/n

#Get blocking mode
curl localhost:8080/client/blocking

#Clear tracked headers
curl -X POST localhost:8080/client/track/headers/clear

#Add headers to track
curl -X PUT localhost:8080/client/track/headers/Request-From-Goto|Goto-Host,Via-Goto,x|y|z,foo

#Remove headers from tracking
curl -X PUT localhost:8080/client/track/headers/remove/foo

#Get list of tracked headers
curl localhost:8080/client/track/headers

#Add time buckets to track
curl -X PUT localhost:8080/client/track/time/0-100,101-200,201-500,501-1000,1001-5000

#Clear results
curl -X POST localhost:8080/client/results/clear

#Remove results for specific targets
curl -X POST localhost:8080/client/results/t1,t2/clear

#Get results per invocation
curl localhost:8080/client/results/invocations

#Get results
curl localhost:8080/client/results
```
</details>

#### Sample Client Results

<details>
<summary>Result Example</summary>
<p>

```json

{
  "": {
    "target": "",
    "invocationCounts": 0,
    "firstResponse": "0001-01-01T00:00:00Z",
    "lastResponse": "0001-01-01T00:00:00Z",
    "retriedInvocationCounts": 0,
    "countsByStatus": {},
    "countsByStatusCodes": {},
    "countsByHeaders": {},
    "countsByURIs": {}
  },
  "t1": {
    "target": "t1",
    "invocationCounts": 20,
    "firstResponse": "2020-08-20T14:29:36.969395-07:00",
    "lastResponse": "2020-08-20T14:36:28.740753-07:00",
    "retriedInvocationCounts": 3,
    "countsByStatus": {
      "200 OK": 2,
      "400 Bad Request": 1,
      "418 I'm a teapot": 15,
      "502 Bad Gateway": 2
    },
    "countsByStatusCodes": {
      "200": 2,
      "400": 1,
      "418": 15,
      "502": 2
    },
    "countsByHeaders": {
      "goto-host": {
        "header": "goto-host",
        "count": {
          "count": 20,
          "retries": 4,
          "firstResponse": "2020-08-20T14:29:36.969404-07:00",
          "lastResponse": "2020-08-20T14:36:28.740769-07:00"
        },
        "countsByValues": {
          "pod.local@1.0.0.1:8082": {
            "count": 12,
            "retries": 2,
            "firstResponse": "2020-08-20T14:30:32.028521-07:00",
            "lastResponse": "2020-08-20T14:36:28.74077-07:00"
          },
          "pod.local@1.0.0.1:9092": {
            "count": 8,
            "retries": 2,
            "firstResponse": "2020-08-20T14:29:36.969405-07:00",
            "lastResponse": "2020-08-20T14:30:16.801438-07:00"
          }
        },
        "countsByStatusCodes": {
          "200": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:15.795679-07:00",
            "lastResponse": "2020-08-20T14:30:16.801438-07:00"
          },
          "400": {
            "count": 1,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.32723-07:00",
            "lastResponse": "2020-08-20T14:30:02.32723-07:00"
          },
          "418": {
            "count": 15,
            "retries": 4,
            "firstResponse": "2020-08-20T14:29:36.969404-07:00",
            "lastResponse": "2020-08-20T14:36:28.740769-07:00"
          },
          "502": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:31:04.066585-07:00",
            "lastResponse": "2020-08-20T14:36:14.802755-07:00"
          }
        },
        "countsByValuesStatusCodes": {
          "pod.local@1.0.0.1:8082": {
            "418": {
              "count": 10,
              "retries": 2,
              "firstResponse": "2020-08-20T14:30:32.028522-07:00",
              "lastResponse": "2020-08-20T14:36:28.740771-07:00"
            },
            "502": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:31:04.066586-07:00",
              "lastResponse": "2020-08-20T14:36:14.802756-07:00"
            }
          },
          "pod.local@1.0.0.1:9092": {
            "200": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:15.79568-07:00",
              "lastResponse": "2020-08-20T14:30:16.801439-07:00"
            },
            "400": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.32723-07:00",
              "lastResponse": "2020-08-20T14:30:02.32723-07:00"
            },
            "418": {
              "count": 5,
              "retries": 2,
              "firstResponse": "2020-08-20T14:29:36.969405-07:00",
              "lastResponse": "2020-08-20T14:30:03.332312-07:00"
            }
          }
        },
        "crossHeaders": {
          "request-from-goto-host": {
            "header": "request-from-goto-host",
            "count": {
              "count": 20,
              "retries": 4,
              "firstResponse": "2020-08-20T14:29:36.969409-07:00",
              "lastResponse": "2020-08-20T14:36:28.740773-07:00"
            },
            "countsByValues": {
              "pod.local@1.0.0.1:8081": {
                "count": 20,
                "retries": 4,
                "firstResponse": "2020-08-20T14:29:36.96941-07:00",
                "lastResponse": "2020-08-20T14:36:28.740774-07:00"
              }
            },
            "countsByStatusCodes": {
              "200": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:15.795682-07:00",
                "lastResponse": "2020-08-20T14:30:16.80144-07:00"
              },
              "400": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.327239-07:00",
                "lastResponse": "2020-08-20T14:30:02.327239-07:00"
              },
              "418": {
                "count": 15,
                "retries": 4,
                "firstResponse": "2020-08-20T14:29:36.969409-07:00",
                "lastResponse": "2020-08-20T14:36:28.740773-07:00"
              },
              "502": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:31:04.066594-07:00",
                "lastResponse": "2020-08-20T14:36:14.802766-07:00"
              }
            },
            "countsByValuesStatusCodes": {
              "pod.local@1.0.0.1:8081": {
                "200": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:15.795683-07:00",
                  "lastResponse": "2020-08-20T14:30:16.801441-07:00"
                },
                "400": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.32724-07:00",
                  "lastResponse": "2020-08-20T14:30:02.32724-07:00"
                },
                "418": {
                  "count": 15,
                  "retries": 4,
                  "firstResponse": "2020-08-20T14:29:36.96941-07:00",
                  "lastResponse": "2020-08-20T14:36:28.740774-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:31:04.066595-07:00",
                  "lastResponse": "2020-08-20T14:36:14.802767-07:00"
                }
              }
            },
            "crossHeaders": {},
            "crossHeadersByValues": {},
            "firstResponse": "2020-08-20T14:29:36.969409-07:00",
            "lastResponse": "2020-08-20T14:36:28.740772-07:00"
          }
        },
        "crossHeadersByValues": {
          "pod.local@1.0.0.1:8082": {
            "request-from-goto-host": {
              "header": "request-from-goto-host",
              "count": {
                "count": 12,
                "retries": 2,
                "firstResponse": "2020-08-20T14:30:32.028526-07:00",
                "lastResponse": "2020-08-20T14:36:28.740776-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8081": {
                  "count": 12,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:30:32.028527-07:00",
                  "lastResponse": "2020-08-20T14:36:28.740777-07:00"
                }
              },
              "countsByStatusCodes": {
                "418": {
                  "count": 10,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:30:32.028527-07:00",
                  "lastResponse": "2020-08-20T14:36:28.740776-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:31:04.066596-07:00",
                  "lastResponse": "2020-08-20T14:36:14.802769-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8081": {
                  "418": {
                    "count": 10,
                    "retries": 2,
                    "firstResponse": "2020-08-20T14:30:32.028528-07:00",
                    "lastResponse": "2020-08-20T14:36:28.740777-07:00"
                  },
                  "502": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:31:04.066597-07:00",
                    "lastResponse": "2020-08-20T14:36:14.802769-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:30:32.028526-07:00",
              "lastResponse": "2020-08-20T14:36:28.740775-07:00"
            }
          },
          "pod.local@1.0.0.1:9092": {
            "request-from-goto-host": {
              "header": "request-from-goto-host",
              "count": {
                "count": 8,
                "retries": 2,
                "firstResponse": "2020-08-20T14:29:36.969411-07:00",
                "lastResponse": "2020-08-20T14:30:16.801442-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8081": {
                  "count": 8,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969412-07:00",
                  "lastResponse": "2020-08-20T14:30:16.801444-07:00"
                }
              },
              "countsByStatusCodes": {
                "200": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:15.795684-07:00",
                  "lastResponse": "2020-08-20T14:30:16.801444-07:00"
                },
                "400": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.327241-07:00",
                  "lastResponse": "2020-08-20T14:30:02.327241-07:00"
                },
                "418": {
                  "count": 5,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969412-07:00",
                  "lastResponse": "2020-08-20T14:30:03.332315-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8081": {
                  "200": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:15.795685-07:00",
                    "lastResponse": "2020-08-20T14:30:16.801445-07:00"
                  },
                  "400": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:02.327242-07:00",
                    "lastResponse": "2020-08-20T14:30:02.327242-07:00"
                  },
                  "418": {
                    "count": 5,
                    "retries": 2,
                    "firstResponse": "2020-08-20T14:29:36.969412-07:00",
                    "lastResponse": "2020-08-20T14:30:03.332315-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:29:36.969411-07:00",
              "lastResponse": "2020-08-20T14:30:16.801441-07:00"
            }
          }
        },
        "firstResponse": "2020-08-20T14:29:36.969404-07:00",
        "lastResponse": "2020-08-20T14:36:28.740768-07:00"
      },
      "request-from-goto-host": {
        "header": "request-from-goto-host",
        "count": {
          "count": 20,
          "retries": 4,
          "firstResponse": "2020-08-20T14:29:36.969414-07:00",
          "lastResponse": "2020-08-20T14:36:28.74078-07:00"
        },
        "countsByValues": {
          "pod.local@1.0.0.1:8081": {
            "count": 20,
            "retries": 4,
            "firstResponse": "2020-08-20T14:29:36.969414-07:00",
            "lastResponse": "2020-08-20T14:36:28.740782-07:00"
          }
        },
        "countsByStatusCodes": {
          "200": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:15.795686-07:00",
            "lastResponse": "2020-08-20T14:30:16.801447-07:00"
          },
          "400": {
            "count": 1,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.327243-07:00",
            "lastResponse": "2020-08-20T14:30:02.327243-07:00"
          },
          "418": {
            "count": 15,
            "retries": 4,
            "firstResponse": "2020-08-20T14:29:36.969414-07:00",
            "lastResponse": "2020-08-20T14:36:28.740781-07:00"
          },
          "502": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:31:04.066599-07:00",
            "lastResponse": "2020-08-20T14:36:14.802771-07:00"
          }
        },
        "countsByValuesStatusCodes": {
          "pod.local@1.0.0.1:8081": {
            "200": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:15.795687-07:00",
              "lastResponse": "2020-08-20T14:30:16.801448-07:00"
            },
            "400": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.327244-07:00",
              "lastResponse": "2020-08-20T14:30:02.327244-07:00"
            },
            "418": {
              "count": 15,
              "retries": 4,
              "firstResponse": "2020-08-20T14:29:36.969415-07:00",
              "lastResponse": "2020-08-20T14:36:28.740783-07:00"
            },
            "502": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:31:04.0666-07:00",
              "lastResponse": "2020-08-20T14:36:14.802772-07:00"
            }
          }
        },
        "crossHeaders": {
          "goto-host": {
            "header": "goto-host",
            "count": {
              "count": 20,
              "retries": 4,
              "firstResponse": "2020-08-20T14:29:36.969416-07:00",
              "lastResponse": "2020-08-20T14:36:28.740784-07:00"
            },
            "countsByValues": {
              "pod.local@1.0.0.1:8082": {
                "count": 12,
                "retries": 2,
                "firstResponse": "2020-08-20T14:30:32.028532-07:00",
                "lastResponse": "2020-08-20T14:36:28.740785-07:00"
              },
              "pod.local@1.0.0.1:9092": {
                "count": 8,
                "retries": 2,
                "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                "lastResponse": "2020-08-20T14:30:16.801449-07:00"
              }
            },
            "countsByStatusCodes": {
              "200": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:15.795687-07:00",
                "lastResponse": "2020-08-20T14:30:16.801449-07:00"
              },
              "400": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.327245-07:00",
                "lastResponse": "2020-08-20T14:30:02.327245-07:00"
              },
              "418": {
                "count": 15,
                "retries": 4,
                "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                "lastResponse": "2020-08-20T14:36:28.740784-07:00"
              },
              "502": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:31:04.066601-07:00",
                "lastResponse": "2020-08-20T14:36:14.802773-07:00"
              }
            },
            "countsByValuesStatusCodes": {
              "pod.local@1.0.0.1:8082": {
                "418": {
                  "count": 10,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:30:32.028533-07:00",
                  "lastResponse": "2020-08-20T14:36:28.740785-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:31:04.066602-07:00",
                  "lastResponse": "2020-08-20T14:36:14.802774-07:00"
                }
              },
              "pod.local@1.0.0.1:9092": {
                "200": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:15.795688-07:00",
                  "lastResponse": "2020-08-20T14:30:16.801449-07:00"
                },
                "400": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.327245-07:00",
                  "lastResponse": "2020-08-20T14:30:02.327245-07:00"
                },
                "418": {
                  "count": 5,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                  "lastResponse": "2020-08-20T14:30:03.332319-07:00"
                }
              }
            },
            "crossHeaders": {},
            "crossHeadersByValues": {},
            "firstResponse": "2020-08-20T14:29:36.969416-07:00",
            "lastResponse": "2020-08-20T14:36:28.740784-07:00"
          }
        },
        "crossHeadersByValues": {
          "pod.local@1.0.0.1:8081": {
            "goto-host": {
              "header": "goto-host",
              "count": {
                "count": 20,
                "retries": 4,
                "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                "lastResponse": "2020-08-20T14:36:28.740786-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8082": {
                  "count": 12,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:30:32.028534-07:00",
                  "lastResponse": "2020-08-20T14:36:28.740787-07:00"
                },
                "pod.local@1.0.0.1:9092": {
                  "count": 8,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                  "lastResponse": "2020-08-20T14:30:16.80145-07:00"
                }
              },
              "countsByStatusCodes": {
                "200": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:15.795689-07:00",
                  "lastResponse": "2020-08-20T14:30:16.80145-07:00"
                },
                "400": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.327246-07:00",
                  "lastResponse": "2020-08-20T14:30:02.327246-07:00"
                },
                "418": {
                  "count": 15,
                  "retries": 4,
                  "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                  "lastResponse": "2020-08-20T14:36:28.740787-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:31:04.066603-07:00",
                  "lastResponse": "2020-08-20T14:36:14.802774-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8082": {
                  "418": {
                    "count": 10,
                    "retries": 2,
                    "firstResponse": "2020-08-20T14:30:32.028535-07:00",
                    "lastResponse": "2020-08-20T14:36:28.740788-07:00"
                  },
                  "502": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:31:04.066603-07:00",
                    "lastResponse": "2020-08-20T14:36:14.802775-07:00"
                  }
                },
                "pod.local@1.0.0.1:9092": {
                  "200": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:15.795689-07:00",
                    "lastResponse": "2020-08-20T14:30:16.80145-07:00"
                  },
                  "400": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:02.327246-07:00",
                    "lastResponse": "2020-08-20T14:30:02.327246-07:00"
                  },
                  "418": {
                    "count": 5,
                    "retries": 2,
                    "firstResponse": "2020-08-20T14:29:36.969419-07:00",
                    "lastResponse": "2020-08-20T14:30:03.33232-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:29:36.969418-07:00",
              "lastResponse": "2020-08-20T14:36:28.740786-07:00"
            }
          }
        },
        "firstResponse": "2020-08-20T14:29:36.969414-07:00",
        "lastResponse": "2020-08-20T14:36:28.74078-07:00"
      },
    },
    "countsByURIs": {
      "/delay/200ms": {
          "uri": "/delay/200ms",
          "count": {
              "value": 9,
              "retries": 0,
              "firstResponse": "2021-03-11T22:29:34.336611-08:00",
              "lastResponse": "2021-03-11T22:29:34.76857-08:00"
          },
          "countsByStatusCodes": {
              "200": {
                  "value": 7,
                  "retries": 0,
                  "firstResponse": "2021-03-11T22:29:34.336612-08:00",
                  "lastResponse": "2021-03-11T22:29:34.76857-08:00"
              },
              "502": {
                  "value": 2,
                  "retries": 0,
                  "firstResponse": "2021-03-11T22:29:34.336719-08:00",
                  "lastResponse": "2021-03-11T22:29:34.336802-08:00"
              }
          },
          "firstResponse": "2021-03-11T22:29:34.336611-08:00",
          "lastResponse": "2021-03-11T22:29:34.76857-08:00"
      }
  },
  "countsByTimeBuckets": {
      "[201 300]": {
          "bucket": "[201 300]",
          "count": {
              "value": 9,
              "retries": 0,
              "firstResponse": "2021-03-11T22:29:34.336608-08:00",
              "lastResponse": "2021-03-11T22:29:34.768568-08:00"
          },
          "countsByStatusCodes": {
              "200": {
                  "value": 7,
                  "retries": 0,
                  "firstResponse": "2021-03-11T22:29:34.336609-08:00",
                  "lastResponse": "2021-03-11T22:29:34.768568-08:00"
              },
              "502": {
                  "value": 2,
                  "retries": 0,
                  "firstResponse": "2021-03-11T22:29:34.336718-08:00",
                  "lastResponse": "2021-03-11T22:29:34.3368-08:00"
              }
          },
          "firstResponse": "2021-03-11T22:29:34.336608-08:00",
          "lastResponse": "2021-03-11T22:29:34.768568-08:00"
      }
    }
  },
  "t2": {}
}

```
</p>
</details>


#### Sample Invocation Result

<details>
<summary>Result Example</summary>
<p>

```json
{
  "1": {
    "invocationIndex": 1,
    "target": {
      "name": "peer1_to_peer4",
      "method": "GET",
      "url": "http://1.0.0.4/echo",
      "headers": [
        [
          "Goto-Client",
          "peer1"
        ]
      ],
      "body": "",
      "replicas": 2,
      "requestCount": 20,
      "initialDelay": "2s",
      "delay": "1s",
      "keepOpen": "",
      "sendID": false,
      "connTimeout": "",
      "connIdleTimeout": "",
      "requestTimeout": "",
      "verifyTLS": false,
      "collectResponse": false,
      "autoInvoke": true
    },
    "status": {
      "completedRequestCount": 13,
      "stopRequested": true,
      "stopped": true,
      "closed": true
    },
    "results": {
      "target": "",
      "invocationCounts": 13,
      "firstResponses": "2020-06-23T13:52:33.546148-07:00",
      "lastResponses": "2020-06-23T13:52:45.561606-07:00",
      "countsByStatus": {
        "200 OK": 13
      },
      "countsByStatusCodes": {
        "200": 13
      },
      "countsByHeaders": {
        "goto-host": 13,
        "via-goto": 13
      },
      "countsByHeaderValues": {
        "goto-host": {
          "1.0.0.4": 13
        },
        "via-goto": {
          "peer4": 13
        }
      },
      "countsByURIs": {
        "/echo": 13
      }
    },
    "finished": true
  },
  "2": {
    "invocationIndex": 2,
    "target": {
      "name": "peer1_to_peer3",
      "method": "GET",
      "url": "http://1.0.0.3/echo",
      "headers": [
        [
          "Goto-Client",
          "peer1"
        ]
      ],
      "body": "",
      "replicas": 2,
      "requestCount": 20,
      "initialDelay": "2s",
      "delay": "1s",
      "keepOpen": "",
      "sendID": false,
      "connTimeout": "",
      "connIdleTimeout": "",
      "requestTimeout": "",
      "verifyTLS": false,
      "collectResponse": false,
      "autoInvoke": true
    },
    "status": {
      "completedRequestCount": 13,
      "stopRequested": true,
      "stopped": true,
      "closed": true
    },
    "results": {
      "target": "",
      "invocationCounts": 13,
      "firstResponses": "2020-06-23T13:52:33.546295-07:00",
      "lastResponses": "2020-06-23T13:52:45.562684-07:00",
      "countsByStatus": {
        "200 OK": 13
      },
      "countsByStatusCodes": {
        "200": 13
      },
      "countsByHeaders": {
        "goto-host": 13,
        "via-goto": 13
      },
      "countsByHeaderValues": {
        "goto-host": {
          "1.0.0.3": 13
        },
        "via-goto": {
          "peer3": 13
        }
      },
      "countsByURIs": {
        "/echo": 13
      }
    },
    "finished": true
  }
}
```
</p>
</details>



#### Sample Active Targets Result

<details>
<summary>Result Example</summary>
<p>

```json
{
  "activeCount": 4,
  "activeInvocations": {
    "peer1_to_peer2": {
      "1": {
        "completedRequestCount": 4,
        "stopRequested": false,
        "stopped": false,
        "closed": false
      }
    },
    "peer1_to_peer3": {
      "2": {
        "completedRequestCount": 6,
        "stopRequested": false,
        "stopped": false,
        "closed": false
      }
    },
    "peer1_to_peer4": {
      "3": {
        "completedRequestCount": 5,
        "stopRequested": false,
        "stopped": false,
        "closed": false
      }
    },
    "peer1_to_peer5": {
      "4": {
        "completedRequestCount": 4,
        "stopRequested": false,
        "stopped": false,
        "closed": false
      }
    }
  }
}

```

<br/>
</p>
</details>
