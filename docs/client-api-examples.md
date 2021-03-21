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
$ curl localhost:8080/client/results
{
    "peer1_to_peer2": {
        "target": "peer1_to_peer2",
        "invocationCounts": 20,
        "firstResponse": "2021-03-22T00:49:31.160218-07:00",
        "lastResponse": "2021-03-22T00:50:00.224392-07:00",
        "retriedInvocationCounts": 10,
        "countsByStatus": {
            "200 OK": 8,
            "418 I'm a teapot": 4,
            "501 Not Implemented": 5,
            "502 Bad Gateway": 2,
            "503 Service Unavailable": 1
        },
        "countsByHeaders": {
            "goto-host": {
                "count": 20,
                "retries": 14,
                "header": "goto-host",
                "countsByValues": {
                    "Localhost.local@1.1.1.1:8082": {
                        "count": 20,
                        "retries": 14,
                        "firstResponse": "2021-03-22T00:49:31.160244-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224404-07:00"
                    }
                },
                "countsByStatusCodes": {
                    "200": {
                        "count": 8,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.160243-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224403-07:00"
                    },
                    "418": {
                        "count": 4,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.92287-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545658-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:33.042171-07:00",
                        "lastResponse": "2021-03-22T00:49:59.150647-07:00"
                    },
                    "502": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.357974-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297769-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986431-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986431-07:00"
                    }
                },
                "countsByValuesStatusCodes": {
                    "Localhost.local@1.1.1.1:8082": {
                        "200": {
                            "count": 8,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:31.160244-07:00",
                            "lastResponse": "2021-03-22T00:50:00.224404-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:34.922872-07:00",
                            "lastResponse": "2021-03-22T00:49:53.545658-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:33.042172-07:00",
                            "lastResponse": "2021-03-22T00:49:59.150648-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:49:37.357976-07:00",
                            "lastResponse": "2021-03-22T00:49:45.29777-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:40.986432-07:00",
                            "lastResponse": "2021-03-22T00:49:40.986432-07:00"
                        }
                    }
                },
                "firstResponse": "2021-03-22T00:49:31.160243-07:00",
                "lastResponse": "2021-03-22T00:50:00.224403-07:00"
            },
            "request-from-goto": {
                "count": 20,
                "retries": 14,
                "header": "request-from-goto",
                "countsByValues": {
                    "peer1": {
                        "count": 20,
                        "retries": 14,
                        "firstResponse": "2021-03-22T00:49:31.160239-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224401-07:00"
                    }
                },
                "countsByStatusCodes": {
                    "200": {
                        "count": 8,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.160239-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224401-07:00"
                    },
                    "418": {
                        "count": 4,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922866-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545657-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:33.042168-07:00",
                        "lastResponse": "2021-03-22T00:49:59.150642-07:00"
                    },
                    "502": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.35797-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297767-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986426-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986426-07:00"
                    }
                },
                "countsByValuesStatusCodes": {
                    "peer1": {
                        "200": {
                            "count": 8,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:31.16024-07:00",
                            "lastResponse": "2021-03-22T00:50:00.224401-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:34.922867-07:00",
                            "lastResponse": "2021-03-22T00:49:53.545657-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:33.042169-07:00",
                            "lastResponse": "2021-03-22T00:49:59.150643-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:49:37.357971-07:00",
                            "lastResponse": "2021-03-22T00:49:45.297767-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:40.986427-07:00",
                            "lastResponse": "2021-03-22T00:49:40.986427-07:00"
                        }
                    }
                },
                "firstResponse": "2021-03-22T00:49:31.160238-07:00",
                "lastResponse": "2021-03-22T00:50:00.2244-07:00"
            },
            "via-goto": {
                "count": 20,
                "retries": 14,
                "header": "via-goto",
                "countsByValues": {
                    "peer2": {
                        "count": 20,
                        "retries": 14,
                        "firstResponse": "2021-03-22T00:49:31.160235-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224397-07:00"
                    }
                },
                "countsByStatusCodes": {
                    "200": {
                        "count": 8,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.160234-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224397-07:00"
                    },
                    "418": {
                        "count": 4,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922861-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545655-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:33.042164-07:00",
                        "lastResponse": "2021-03-22T00:49:59.150637-07:00"
                    },
                    "502": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.357965-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297763-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986422-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986422-07:00"
                    }
                },
                "countsByValuesStatusCodes": {
                    "peer2": {
                        "200": {
                            "count": 8,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:31.160236-07:00",
                            "lastResponse": "2021-03-22T00:50:00.224398-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:34.922863-07:00",
                            "lastResponse": "2021-03-22T00:49:53.545655-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:33.042165-07:00",
                            "lastResponse": "2021-03-22T00:49:59.150639-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:49:37.357967-07:00",
                            "lastResponse": "2021-03-22T00:49:45.297764-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:40.986423-07:00",
                            "lastResponse": "2021-03-22T00:49:40.986423-07:00"
                        }
                    }
                },
                "firstResponse": "2021-03-22T00:49:31.160232-07:00",
                "lastResponse": "2021-03-22T00:50:00.224396-07:00"
            }
        },
        "countsByStatusCodes": {
            "200": {
                "count": 8,
                "retries": 3,
                "firstResponse": "2021-03-22T00:49:31.160226-07:00",
                "lastResponse": "2021-03-22T00:50:00.224393-07:00",
                "byTimeBuckets": {
                    "[201 300]": {
                        "count": 4,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.160302-07:00",
                        "lastResponse": "2021-03-22T00:49:58.516234-07:00"
                    },
                    "[301 500]": {
                        "count": 3,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:42.898591-07:00",
                        "lastResponse": "2021-03-22T00:49:57.070256-07:00"
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:50:00.224415-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224415-07:00"
                    }
                }
            },
            "418": {
                "count": 4,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:34.922854-07:00",
                "lastResponse": "2021-03-22T00:49:53.545653-07:00",
                "byTimeBuckets": {
                    "[301 500]": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:38.218268-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545667-07:00"
                    },
                    "[501 1000]": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:34.92289-07:00",
                        "lastResponse": "2021-03-22T00:49:42.00162-07:00"
                    }
                }
            },
            "501": {
                "count": 5,
                "retries": 3,
                "firstResponse": "2021-03-22T00:49:33.04216-07:00",
                "lastResponse": "2021-03-22T00:49:59.150631-07:00",
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 2,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:57.747886-07:00",
                        "lastResponse": "2021-03-22T00:49:59.150663-07:00"
                    },
                    "[201 300]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:33.042186-07:00",
                        "lastResponse": "2021-03-22T00:49:33.042186-07:00"
                    },
                    "[301 500]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:51.729876-07:00",
                        "lastResponse": "2021-03-22T00:49:51.729876-07:00"
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:55.461243-07:00",
                        "lastResponse": "2021-03-22T00:49:55.461243-07:00"
                    }
                }
            },
            "502": {
                "count": 2,
                "retries": 4,
                "firstResponse": "2021-03-22T00:49:37.357956-07:00",
                "lastResponse": "2021-03-22T00:49:45.297759-07:00",
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.357999-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297782-07:00"
                    }
                }
            },
            "503": {
                "count": 1,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:40.986414-07:00",
                "lastResponse": "2021-03-22T00:49:40.986414-07:00",
                "byTimeBuckets": {
                    "[301 500]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986453-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986453-07:00"
                    }
                }
            }
        },
        "countsByURIs": {
            "/status/200,418,501,502,503/delay/100ms-600ms": {
                "count": 20,
                "retries": 14,
                "firstResponse": "2021-03-22T00:49:31.160228-07:00",
                "lastResponse": "2021-03-22T00:50:00.224394-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 8,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.160229-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224395-07:00"
                    },
                    "418": {
                        "count": 4,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922856-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545654-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:33.042161-07:00",
                        "lastResponse": "2021-03-22T00:49:59.150634-07:00"
                    },
                    "502": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.35796-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297761-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986417-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986417-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 4,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.35799-07:00",
                        "lastResponse": "2021-03-22T00:49:59.15066-07:00",
                        "byStatusCodes": {
                            "501": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:57.747884-07:00",
                                "lastResponse": "2021-03-22T00:49:59.15066-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:49:37.357991-07:00",
                                "lastResponse": "2021-03-22T00:49:45.297778-07:00"
                            }
                        }
                    },
                    "[201 300]": {
                        "count": 5,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:31.160285-07:00",
                        "lastResponse": "2021-03-22T00:49:58.516232-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 4,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:31.160301-07:00",
                                "lastResponse": "2021-03-22T00:49:58.516233-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:33.042181-07:00",
                                "lastResponse": "2021-03-22T00:49:33.042181-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 7,
                        "retries": 6,
                        "firstResponse": "2021-03-22T00:49:38.218265-07:00",
                        "lastResponse": "2021-03-22T00:49:57.070253-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 3,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:42.898588-07:00",
                                "lastResponse": "2021-03-22T00:49:57.070253-07:00"
                            },
                            "418": {
                                "count": 2,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:38.218266-07:00",
                                "lastResponse": "2021-03-22T00:49:53.545665-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:51.72987-07:00",
                                "lastResponse": "2021-03-22T00:49:51.72987-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:40.986446-07:00",
                                "lastResponse": "2021-03-22T00:49:40.986446-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 4,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922884-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224413-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:50:00.224413-07:00",
                                "lastResponse": "2021-03-22T00:50:00.224413-07:00"
                            },
                            "418": {
                                "count": 2,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:34.922885-07:00",
                                "lastResponse": "2021-03-22T00:49:42.001618-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:55.461235-07:00",
                                "lastResponse": "2021-03-22T00:49:55.461235-07:00"
                            }
                        }
                    }
                }
            }
        },
        "countsByRetries": {
            "1": {
                "count": 6,
                "retries": 6,
                "firstResponse": "2021-03-22T00:49:33.042155-07:00",
                "lastResponse": "2021-03-22T00:49:55.461197-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:49.965891-07:00",
                        "lastResponse": "2021-03-22T00:49:49.965891-07:00"
                    },
                    "418": {
                        "count": 2,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922848-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545651-07:00"
                    },
                    "501": {
                        "count": 3,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:33.042156-07:00",
                        "lastResponse": "2021-03-22T00:49:55.461198-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[201 300]": {
                        "count": 2,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:33.042182-07:00",
                        "lastResponse": "2021-03-22T00:49:49.965923-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:49.965924-07:00",
                                "lastResponse": "2021-03-22T00:49:49.965924-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:33.042183-07:00",
                                "lastResponse": "2021-03-22T00:49:33.042183-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 2,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.729872-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545665-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:53.545666-07:00",
                                "lastResponse": "2021-03-22T00:49:53.545666-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:51.729873-07:00",
                                "lastResponse": "2021-03-22T00:49:51.729873-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 2,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922886-07:00",
                        "lastResponse": "2021-03-22T00:49:55.461236-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:34.922887-07:00",
                                "lastResponse": "2021-03-22T00:49:34.922887-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:55.461238-07:00",
                                "lastResponse": "2021-03-22T00:49:55.461238-07:00"
                            }
                        }
                    }
                }
            },
            "2": {
                "count": 4,
                "retries": 8,
                "firstResponse": "2021-03-22T00:49:37.357947-07:00",
                "lastResponse": "2021-03-22T00:49:48.26367-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:48.263672-07:00",
                        "lastResponse": "2021-03-22T00:49:48.263672-07:00"
                    },
                    "502": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.35795-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297755-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986408-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986408-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.357993-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297779-07:00",
                        "byStatusCodes": {
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:49:37.357994-07:00",
                                "lastResponse": "2021-03-22T00:49:45.297779-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:40.986448-07:00",
                        "lastResponse": "2021-03-22T00:49:48.263705-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:48.263706-07:00",
                                "lastResponse": "2021-03-22T00:49:48.263706-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:40.986449-07:00",
                                "lastResponse": "2021-03-22T00:49:40.986449-07:00"
                            }
                        }
                    }
                }
            }
        },
        "countsByRetryReasons": {
            "502 Bad Gateway": {
                "count": 6,
                "retries": 8,
                "firstResponse": "2021-03-22T00:49:34.92285-07:00",
                "lastResponse": "2021-03-22T00:49:53.545652-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 2,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:48.263675-07:00",
                        "lastResponse": "2021-03-22T00:49:49.965894-07:00"
                    },
                    "418": {
                        "count": 2,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:34.922852-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545652-07:00"
                    },
                    "501": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:51.729852-07:00",
                        "lastResponse": "2021-03-22T00:49:51.729852-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:45.297758-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297758-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:45.29778-07:00",
                        "lastResponse": "2021-03-22T00:49:45.29778-07:00",
                        "byStatusCodes": {
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:45.297781-07:00",
                                "lastResponse": "2021-03-22T00:49:45.297781-07:00"
                            }
                        }
                    },
                    "[201 300]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:49.965926-07:00",
                        "lastResponse": "2021-03-22T00:49:49.965926-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:49.965928-07:00",
                                "lastResponse": "2021-03-22T00:49:49.965928-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 3,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:48.263708-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545666-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:48.26371-07:00",
                                "lastResponse": "2021-03-22T00:49:48.26371-07:00"
                            },
                            "418": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:53.545667-07:00",
                                "lastResponse": "2021-03-22T00:49:53.545667-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:51.729875-07:00",
                                "lastResponse": "2021-03-22T00:49:51.729875-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:34.922888-07:00",
                        "lastResponse": "2021-03-22T00:49:34.922888-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:34.922889-07:00",
                                "lastResponse": "2021-03-22T00:49:34.922889-07:00"
                            }
                        }
                    }
                }
            },
            "503 Service Unavailable": {
                "count": 4,
                "retries": 6,
                "firstResponse": "2021-03-22T00:49:33.042158-07:00",
                "lastResponse": "2021-03-22T00:49:55.4612-07:00",
                "byStatusCodes": {
                    "501": {
                        "count": 2,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:33.042158-07:00",
                        "lastResponse": "2021-03-22T00:49:55.461201-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:37.357954-07:00",
                        "lastResponse": "2021-03-22T00:49:37.357954-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986412-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986412-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:37.357997-07:00",
                        "lastResponse": "2021-03-22T00:49:37.357997-07:00",
                        "byStatusCodes": {
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:37.357998-07:00",
                                "lastResponse": "2021-03-22T00:49:37.357998-07:00"
                            }
                        }
                    },
                    "[201 300]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:33.042184-07:00",
                        "lastResponse": "2021-03-22T00:49:33.042184-07:00",
                        "byStatusCodes": {
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:33.042184-07:00",
                                "lastResponse": "2021-03-22T00:49:33.042184-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986451-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986451-07:00",
                        "byStatusCodes": {
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:40.986452-07:00",
                                "lastResponse": "2021-03-22T00:49:40.986452-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:55.46124-07:00",
                        "lastResponse": "2021-03-22T00:49:55.46124-07:00",
                        "byStatusCodes": {
                            "501": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:55.461241-07:00",
                                "lastResponse": "2021-03-22T00:49:55.461241-07:00"
                            }
                        }
                    }
                }
            }
        },
        "countsByTimeBuckets": {
            "[101 200]": {
                "count": 4,
                "retries": 4,
                "firstResponse": "2021-03-22T00:49:37.357988-07:00",
                "lastResponse": "2021-03-22T00:49:59.150658-07:00",
                "byStatusCodes": {
                    "501": {
                        "count": 2,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:57.747883-07:00",
                        "lastResponse": "2021-03-22T00:49:59.150659-07:00"
                    },
                    "502": {
                        "count": 2,
                        "retries": 4,
                        "firstResponse": "2021-03-22T00:49:37.357989-07:00",
                        "lastResponse": "2021-03-22T00:49:45.297777-07:00"
                    }
                }
            },
            "[201 300]": {
                "count": 5,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:31.160283-07:00",
                "lastResponse": "2021-03-22T00:49:58.516231-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 4,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.160284-07:00",
                        "lastResponse": "2021-03-22T00:49:58.516232-07:00"
                    },
                    "501": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:33.042179-07:00",
                        "lastResponse": "2021-03-22T00:49:33.042179-07:00"
                    }
                }
            },
            "[301 500]": {
                "count": 7,
                "retries": 6,
                "firstResponse": "2021-03-22T00:49:38.218262-07:00",
                "lastResponse": "2021-03-22T00:49:57.070251-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 3,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:42.898586-07:00",
                        "lastResponse": "2021-03-22T00:49:57.070252-07:00"
                    },
                    "418": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:38.218263-07:00",
                        "lastResponse": "2021-03-22T00:49:53.545664-07:00"
                    },
                    "501": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:51.729869-07:00",
                        "lastResponse": "2021-03-22T00:49:51.729869-07:00"
                    },
                    "503": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:40.986444-07:00",
                        "lastResponse": "2021-03-22T00:49:40.986444-07:00"
                    }
                }
            },
            "[501 1000]": {
                "count": 4,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:34.922882-07:00",
                "lastResponse": "2021-03-22T00:50:00.22441-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:50:00.224412-07:00",
                        "lastResponse": "2021-03-22T00:50:00.224412-07:00"
                    },
                    "418": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:34.922883-07:00",
                        "lastResponse": "2021-03-22T00:49:42.001617-07:00"
                    },
                    "501": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:55.461233-07:00",
                        "lastResponse": "2021-03-22T00:49:55.461233-07:00"
                    }
                }
            }
        }
    },
    "peer1_to_peer3": {
        "target": "peer1_to_peer3",
        "invocationCounts": 20,
        "firstResponse": "2021-03-22T00:49:31.987936-07:00",
        "lastResponse": "2021-03-22T00:49:52.61453-07:00",
        "retriedInvocationCounts": 4,
        "countsByStatus": {
            "200 OK": 5,
            "418 I'm a teapot": 9,
            "501 Not Implemented": 5,
            "502 Bad Gateway": 1
        },
        "countsByHeaders": {
            "goto-host": {
                "count": 20,
                "retries": 6,
                "header": "goto-host",
                "countsByValues": {
                    "Localhost.local@1.1.1.1:8083": {
                        "count": 20,
                        "retries": 6,
                        "firstResponse": "2021-03-22T00:49:31.987987-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614544-07:00"
                    }
                },
                "countsByStatusCodes": {
                    "200": {
                        "count": 5,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:32.840419-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523191-07:00"
                    },
                    "418": {
                        "count": 9,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.987986-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614544-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:34.701535-07:00",
                        "lastResponse": "2021-03-22T00:49:47.395771-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888401-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888401-07:00"
                    }
                },
                "countsByValuesStatusCodes": {
                    "Localhost.local@1.1.1.1:8083": {
                        "200": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:49:32.84042-07:00",
                            "lastResponse": "2021-03-22T00:49:44.523192-07:00"
                        },
                        "418": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:31.987988-07:00",
                            "lastResponse": "2021-03-22T00:49:52.614545-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:49:34.701537-07:00",
                            "lastResponse": "2021-03-22T00:49:47.395772-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:51.888402-07:00",
                            "lastResponse": "2021-03-22T00:49:51.888402-07:00"
                        }
                    }
                },
                "firstResponse": "2021-03-22T00:49:31.987985-07:00",
                "lastResponse": "2021-03-22T00:49:52.614543-07:00"
            },
            "request-from-goto": {
                "count": 20,
                "retries": 6,
                "header": "request-from-goto",
                "countsByValues": {
                    "peer1": {
                        "count": 20,
                        "retries": 6,
                        "firstResponse": "2021-03-22T00:49:31.98798-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614541-07:00"
                    }
                },
                "countsByStatusCodes": {
                    "200": {
                        "count": 5,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:32.840414-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523186-07:00"
                    },
                    "418": {
                        "count": 9,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.987979-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614541-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:34.70153-07:00",
                        "lastResponse": "2021-03-22T00:49:47.395767-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888398-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888398-07:00"
                    }
                },
                "countsByValuesStatusCodes": {
                    "peer1": {
                        "200": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:49:32.840415-07:00",
                            "lastResponse": "2021-03-22T00:49:44.523187-07:00"
                        },
                        "418": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:31.987981-07:00",
                            "lastResponse": "2021-03-22T00:49:52.614542-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:49:34.701531-07:00",
                            "lastResponse": "2021-03-22T00:49:47.395768-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:51.888399-07:00",
                            "lastResponse": "2021-03-22T00:49:51.888399-07:00"
                        }
                    }
                },
                "firstResponse": "2021-03-22T00:49:31.987979-07:00",
                "lastResponse": "2021-03-22T00:49:52.61454-07:00"
            },
            "via-goto": {
                "count": 20,
                "retries": 6,
                "header": "via-goto",
                "countsByValues": {
                    "peer3": {
                        "count": 20,
                        "retries": 6,
                        "firstResponse": "2021-03-22T00:49:31.987974-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614538-07:00"
                    }
                },
                "countsByStatusCodes": {
                    "200": {
                        "count": 5,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:32.840409-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523181-07:00"
                    },
                    "418": {
                        "count": 9,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.987973-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614538-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:34.701525-07:00",
                        "lastResponse": "2021-03-22T00:49:47.395761-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888393-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888393-07:00"
                    }
                },
                "countsByValuesStatusCodes": {
                    "peer3": {
                        "200": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:49:32.84041-07:00",
                            "lastResponse": "2021-03-22T00:49:44.523182-07:00"
                        },
                        "418": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:49:31.987975-07:00",
                            "lastResponse": "2021-03-22T00:49:52.614538-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:49:34.701527-07:00",
                            "lastResponse": "2021-03-22T00:49:47.395763-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:49:51.888394-07:00",
                            "lastResponse": "2021-03-22T00:49:51.888394-07:00"
                        }
                    }
                },
                "firstResponse": "2021-03-22T00:49:31.987971-07:00",
                "lastResponse": "2021-03-22T00:49:52.614537-07:00"
            }
        },
        "countsByStatusCodes": {
            "200": {
                "count": 5,
                "retries": 1,
                "firstResponse": "2021-03-22T00:49:32.8404-07:00",
                "lastResponse": "2021-03-22T00:49:44.523176-07:00",
                "byTimeBuckets": {
                    "[201 300]": {
                        "count": 2,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:36.34222-07:00",
                        "lastResponse": "2021-03-22T00:49:37.704497-07:00"
                    },
                    "[301 500]": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:32.84044-07:00",
                        "lastResponse": "2021-03-22T00:49:32.84044-07:00"
                    },
                    "[501 1000]": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:33.901439-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523209-07:00"
                    }
                }
            },
            "418": {
                "count": 9,
                "retries": 3,
                "firstResponse": "2021-03-22T00:49:31.987964-07:00",
                "lastResponse": "2021-03-22T00:49:52.614533-07:00",
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 3,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:36.979917-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551936-07:00"
                    },
                    "[201 300]": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.988006-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614554-07:00"
                    },
                    "[301 500]": {
                        "count": 4,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:42.493875-07:00",
                        "lastResponse": "2021-03-22T00:49:49.155596-07:00"
                    }
                }
            },
            "501": {
                "count": 5,
                "retries": 0,
                "firstResponse": "2021-03-22T00:49:34.701517-07:00",
                "lastResponse": "2021-03-22T00:49:47.395754-07:00",
                "byTimeBuckets": {
                    "[201 300]": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:34.701552-07:00",
                        "lastResponse": "2021-03-22T00:49:34.701552-07:00"
                    },
                    "[301 500]": {
                        "count": 3,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:35.585099-07:00",
                        "lastResponse": "2021-03-22T00:49:45.392289-07:00"
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:47.39579-07:00",
                        "lastResponse": "2021-03-22T00:49:47.39579-07:00"
                    }
                }
            },
            "502": {
                "count": 1,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:51.888389-07:00",
                "lastResponse": "2021-03-22T00:49:51.888389-07:00",
                "byTimeBuckets": {
                    "[301 500]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888415-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888415-07:00"
                    }
                }
            }
        },
        "countsByURIs": {
            "/status/200,418,501,502,503/delay/100ms-600ms": {
                "count": 20,
                "retries": 6,
                "firstResponse": "2021-03-22T00:49:31.987966-07:00",
                "lastResponse": "2021-03-22T00:49:52.614534-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 5,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:32.840404-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523178-07:00"
                    },
                    "418": {
                        "count": 9,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.987967-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614535-07:00"
                    },
                    "501": {
                        "count": 5,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:34.701521-07:00",
                        "lastResponse": "2021-03-22T00:49:47.395758-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888391-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888391-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 3,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:36.979913-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551932-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 3,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:36.979914-07:00",
                                "lastResponse": "2021-03-22T00:49:41.551933-07:00"
                            }
                        }
                    },
                    "[201 300]": {
                        "count": 5,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.988-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614553-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:36.342218-07:00",
                                "lastResponse": "2021-03-22T00:49:37.704496-07:00"
                            },
                            "418": {
                                "count": 2,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:31.988001-07:00",
                                "lastResponse": "2021-03-22T00:49:52.614553-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:34.701548-07:00",
                                "lastResponse": "2021-03-22T00:49:34.701548-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 9,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:32.840437-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888408-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:32.840438-07:00",
                                "lastResponse": "2021-03-22T00:49:32.840438-07:00"
                            },
                            "418": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:42.493872-07:00",
                                "lastResponse": "2021-03-22T00:49:49.155593-07:00"
                            },
                            "501": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:35.585097-07:00",
                                "lastResponse": "2021-03-22T00:49:45.392288-07:00"
                            },
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:51.888409-07:00",
                                "lastResponse": "2021-03-22T00:49:51.888409-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 3,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:33.901438-07:00",
                        "lastResponse": "2021-03-22T00:49:47.395786-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 2,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:33.901438-07:00",
                                "lastResponse": "2021-03-22T00:49:44.523202-07:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-22T00:49:47.395787-07:00",
                                "lastResponse": "2021-03-22T00:49:47.395787-07:00"
                            }
                        }
                    }
                }
            }
        },
        "countsByRetries": {
            "1": {
                "count": 2,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:31.987958-07:00",
                "lastResponse": "2021-03-22T00:49:44.523169-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:44.523171-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523171-07:00"
                    },
                    "418": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.98796-07:00",
                        "lastResponse": "2021-03-22T00:49:31.98796-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[201 300]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.988002-07:00",
                        "lastResponse": "2021-03-22T00:49:31.988002-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:31.988003-07:00",
                                "lastResponse": "2021-03-22T00:49:31.988003-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:44.523204-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523204-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:44.523205-07:00",
                                "lastResponse": "2021-03-22T00:49:44.523205-07:00"
                            }
                        }
                    }
                }
            },
            "2": {
                "count": 2,
                "retries": 4,
                "firstResponse": "2021-03-22T00:49:41.551915-07:00",
                "lastResponse": "2021-03-22T00:49:51.888384-07:00",
                "byStatusCodes": {
                    "418": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:41.551917-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551917-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888385-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888385-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:41.551934-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551934-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:41.551934-07:00",
                                "lastResponse": "2021-03-22T00:49:41.551934-07:00"
                            }
                        }
                    },
                    "[301 500]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.88841-07:00",
                        "lastResponse": "2021-03-22T00:49:51.88841-07:00",
                        "byStatusCodes": {
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:51.888411-07:00",
                                "lastResponse": "2021-03-22T00:49:51.888411-07:00"
                            }
                        }
                    }
                }
            }
        },
        "countsByRetryReasons": {
            "502 Bad Gateway": {
                "count": 2,
                "retries": 3,
                "firstResponse": "2021-03-22T00:49:44.523173-07:00",
                "lastResponse": "2021-03-22T00:49:51.888386-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:44.523175-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523175-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888388-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888388-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[301 500]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888412-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888412-07:00",
                        "byStatusCodes": {
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:51.888414-07:00",
                                "lastResponse": "2021-03-22T00:49:51.888414-07:00"
                            }
                        }
                    },
                    "[501 1000]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:44.523207-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523207-07:00",
                        "byStatusCodes": {
                            "200": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:44.523207-07:00",
                                "lastResponse": "2021-03-22T00:49:44.523207-07:00"
                            }
                        }
                    }
                }
            },
            "503 Service Unavailable": {
                "count": 2,
                "retries": 3,
                "firstResponse": "2021-03-22T00:49:31.987962-07:00",
                "lastResponse": "2021-03-22T00:49:41.551918-07:00",
                "byStatusCodes": {
                    "418": {
                        "count": 2,
                        "retries": 3,
                        "firstResponse": "2021-03-22T00:49:31.987963-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551918-07:00"
                    }
                },
                "byTimeBuckets": {
                    "[101 200]": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:41.551935-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551935-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:49:41.551936-07:00",
                                "lastResponse": "2021-03-22T00:49:41.551936-07:00"
                            }
                        }
                    },
                    "[201 300]": {
                        "count": 1,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.988004-07:00",
                        "lastResponse": "2021-03-22T00:49:31.988004-07:00",
                        "byStatusCodes": {
                            "418": {
                                "count": 1,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:49:31.988005-07:00",
                                "lastResponse": "2021-03-22T00:49:31.988005-07:00"
                            }
                        }
                    }
                }
            }
        },
        "countsByTimeBuckets": {
            "[101 200]": {
                "count": 3,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:36.979908-07:00",
                "lastResponse": "2021-03-22T00:49:41.551931-07:00",
                "byStatusCodes": {
                    "418": {
                        "count": 3,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:36.979911-07:00",
                        "lastResponse": "2021-03-22T00:49:41.551932-07:00"
                    }
                }
            },
            "[201 300]": {
                "count": 5,
                "retries": 1,
                "firstResponse": "2021-03-22T00:49:31.987998-07:00",
                "lastResponse": "2021-03-22T00:49:52.614552-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 2,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:36.342218-07:00",
                        "lastResponse": "2021-03-22T00:49:37.704495-07:00"
                    },
                    "418": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:31.987999-07:00",
                        "lastResponse": "2021-03-22T00:49:52.614552-07:00"
                    },
                    "501": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:34.701546-07:00",
                        "lastResponse": "2021-03-22T00:49:34.701546-07:00"
                    }
                }
            },
            "[301 500]": {
                "count": 9,
                "retries": 2,
                "firstResponse": "2021-03-22T00:49:32.840433-07:00",
                "lastResponse": "2021-03-22T00:49:51.888407-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:32.840435-07:00",
                        "lastResponse": "2021-03-22T00:49:32.840435-07:00"
                    },
                    "418": {
                        "count": 4,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:42.493871-07:00",
                        "lastResponse": "2021-03-22T00:49:49.155592-07:00"
                    },
                    "501": {
                        "count": 3,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:35.585092-07:00",
                        "lastResponse": "2021-03-22T00:49:45.392287-07:00"
                    },
                    "502": {
                        "count": 1,
                        "retries": 2,
                        "firstResponse": "2021-03-22T00:49:51.888408-07:00",
                        "lastResponse": "2021-03-22T00:49:51.888408-07:00"
                    }
                }
            },
            "[501 1000]": {
                "count": 3,
                "retries": 1,
                "firstResponse": "2021-03-22T00:49:33.901436-07:00",
                "lastResponse": "2021-03-22T00:49:47.395783-07:00",
                "byStatusCodes": {
                    "200": {
                        "count": 2,
                        "retries": 1,
                        "firstResponse": "2021-03-22T00:49:33.901437-07:00",
                        "lastResponse": "2021-03-22T00:49:44.523201-07:00"
                    },
                    "501": {
                        "count": 1,
                        "retries": 0,
                        "firstResponse": "2021-03-22T00:49:47.395785-07:00",
                        "lastResponse": "2021-03-22T00:49:47.395785-07:00"
                    }
                }
            }
        }
    }
}

```

</p>
</details>


#### Sample Invocation Result

<details>
<summary>Result Example</summary>
<p>

```json

$ curl localhost:8081/client/results/invocations
{
    "1": {
        "invocationIndex": 1,
        "target": {
            "name": "peer1_to_peer3",
            "protocol": "HTTP/1.1",
            "method": "GET",
            "url": "http://localhost:8083/status/200,418,501,502,503/delay/100ms-600ms",
            "burls": null,
            "headers": [
                [
                    "x",
                    "x1"
                ],
                [
                    "From-Goto",
                    "peer1"
                ],
                [
                    "From-Goto-Host",
                    "Localhost.lan.local@1.1.1.1:8081"
                ]
            ],
            "body": "",
            "autoPayload": "10K",
            "replicas": 1,
            "requestCount": 20,
            "initialDelay": "500ms",
            "delay": "500ms",
            "retries": 2,
            "retryDelay": "500ms",
            "retriableStatusCodes": [
                502,
                503
            ],
            "keepOpen": "",
            "sendID": false,
            "connTimeout": "5s",
            "connIdleTimeout": "5m",
            "requestTimeout": "30s",
            "autoInvoke": true,
            "fallback": false,
            "ab": false,
            "random": false,
            "streamPayload": null,
            "streamDelay": "10ms",
            "binary": false,
            "collectResponse": false,
            "expectation": null,
            "autoUpgrade": false,
            "verifyTLS": false
        },
        "status": {
            "completedReplicas": 20,
            "successCount": 20,
            "failureCount": 0,
            "retriesCount": 6,
            "abCount": 0,
            "totalRequests": 26,
            "stopRequested": false,
            "stopped": false,
            "closed": true
        },
        "results": [
            {
                "targetName": "peer1_to_peer3",
                "targetID": "peer1_to_peer3[1][1]",
                "status": "200 OK",
                "statusCode": 200,
                "requestPayloadSize": 0,
                "responsePayloadSize": 0,
                "firstByteInAt": "2021-03-22 07:53:44.51114 +0000 UTC",
                "lastByteInAt": "2021-03-22 07:53:44.51114 +0000 UTC",
                "firstByteOutAt": "",
                "lastByteOutAt": "",
                "retries": 0,
                "url": "http://localhost:8083/status/200,418,501,502,503/delay/100ms-600ms",
                "uri": "/status/200,418,501,502,503/delay/100ms-600ms",
                "requestID": "",
                "headers": {
                    "content-length": [
                        "0"
                    ],
                    "date": [
                        "Mon, 22 Mar 2021 07:53:44 GMT"
                    ],
                    "goto-host": [
                        "Localhost.lan.local@1.1.1.1:8083"
                    ],
                    "goto-in-at": [
                        "2021-03-22 07:53:44.179794 +0000 UTC"
                    ],
                    "goto-out-at": [
                        "2021-03-22 07:53:44.510799 +0000 UTC"
                    ],
                    "goto-port": [
                        "8083"
                    ],
                    "goto-protocol": [
                        "HTTP"
                    ],
                    "goto-remote-address": [
                        "[::1]:58209"
                    ],
                    "goto-requested-status": [
                        "200"
                    ],
                    "goto-response-delay": [
                        "328ms"
                    ],
                    "goto-response-status": [
                        "200"
                    ],
                    "goto-took": [
                        "331.004794ms"
                    ],
                    "request-from-goto": [
                        "peer1"
                    ],
                    "request-from-goto-host": [
                        "Localhost.lan.local@1.1.1.1:8081"
                    ],
                    "request-host": [
                        "localhost:8083"
                    ],
                    "request-protocol": [
                        "HTTP/1.1"
                    ],
                    "request-targetid": [
                        "peer1_to_peer3[1][1]"
                    ],
                    "request-uri": [
                        "/status/200,418,501,502,503/delay/100ms-600ms"
                    ],
                    "request-user-agent": [
                        "Go-http-client/1.1"
                    ],
                    "request-x": [
                        "x1"
                    ],
                    "status": [
                        "200 OK"
                    ],
                    "via-goto": [
                        "peer3"
                    ]
                },
                "retryURL": "",
                "lastRetryReason": "",
                "tookNanos": 333921294,
                "errors": {}
            },
            {
                "targetName": "peer1_to_peer3",
                "targetID": "peer1_to_peer3[1][2]",
                "status": "501 Not Implemented",
                "statusCode": 501,
                "requestPayloadSize": 0,
                "responsePayloadSize": 0,
                "firstByteInAt": "2021-03-22 07:53:45.414609 +0000 UTC",
                "lastByteInAt": "2021-03-22 07:53:45.414609 +0000 UTC",
                "firstByteOutAt": "",
                "lastByteOutAt": "",
                "retries": 0,
                "url": "http://localhost:8083/status/200,418,501,502,503/delay/100ms-600ms",
                "uri": "/status/200,418,501,502,503/delay/100ms-600ms",
                "requestID": "",
                "headers": {
                    "content-length": [
                        "0"
                    ],
                    "date": [
                        "Mon, 22 Mar 2021 07:53:45 GMT"
                    ],
                    "goto-host": [
                        "Localhost.lan.local@1.1.1.1:8083"
                    ],
                    "goto-in-at": [
                        "2021-03-22 07:53:45.013794 +0000 UTC"
                    ],
                    "goto-out-at": [
                        "2021-03-22 07:53:45.414276 +0000 UTC"
                    ],
                    "goto-port": [
                        "8083"
                    ],
                    "goto-protocol": [
                        "HTTP"
                    ],
                    "goto-remote-address": [
                        "[::1]:58209"
                    ],
                    "goto-requested-status": [
                        "501"
                    ],
                    "goto-response-delay": [
                        "398ms"
                    ],
                    "goto-response-status": [
                        "501"
                    ],
                    "goto-took": [
                        "400.480792ms"
                    ],
                    "request-from-goto": [
                        "peer1"
                    ],
                    "request-from-goto-host": [
                        "Localhost.lan.local@1.1.1.1:8081"
                    ],
                    "request-host": [
                        "localhost:8083"
                    ],
                    "request-protocol": [
                        "HTTP/1.1"
                    ],
                    "request-targetid": [
                        "peer1_to_peer3[1][2]"
                    ],
                    "request-uri": [
                        "/status/200,418,501,502,503/delay/100ms-600ms"
                    ],
                    "request-user-agent": [
                        "Go-http-client/1.1"
                    ],
                    "request-x": [
                        "x1"
                    ],
                    "status": [
                        "501 Not Implemented"
                    ],
                    "via-goto": [
                        "peer3"
                    ]
                },
                "retryURL": "",
                "lastRetryReason": "",
                "tookNanos": 401216867,
                "errors": {}
            }
        ],
        "finished": true
    },
    "2": {
        "invocationIndex": 2,
        "target": {
            "name": "peer1_to_peer2",
            "protocol": "HTTP/1.1",
            "method": "GET",
            "url": "http://localhost:8082/status/200,418,501,502,503/delay/100ms-600ms",
            "burls": null,
            "headers": [
                [
                    "x",
                    "x1"
                ],
                [
                    "From-Goto",
                    "peer1"
                ],
                [
                    "From-Goto-Host",
                    "Localhost.lan.local@1.1.1.1:8081"
                ]
            ],
            "body": "",
            "autoPayload": "10K",
            "replicas": 1,
            "requestCount": 20,
            "initialDelay": "500ms",
            "delay": "500ms",
            "retries": 2,
            "retryDelay": "500ms",
            "retriableStatusCodes": [
                502,
                503
            ],
            "keepOpen": "",
            "sendID": false,
            "connTimeout": "5s",
            "connIdleTimeout": "5m",
            "requestTimeout": "30s",
            "autoInvoke": true,
            "fallback": false,
            "ab": false,
            "random": false,
            "streamPayload": null,
            "streamDelay": "10ms",
            "binary": false,
            "collectResponse": false,
            "expectation": null,
            "autoUpgrade": false,
            "verifyTLS": false
        },
        "status": {
            "completedReplicas": 17,
            "successCount": 17,
            "failureCount": 0,
            "retriesCount": 11,
            "abCount": 0,
            "totalRequests": 28,
            "stopRequested": false,
            "stopped": false,
            "closed": false
        },
        "results": [
            {
                "targetName": "peer1_to_peer2",
                "targetID": "peer1_to_peer2[1][1]",
                "status": "418 I'm a teapot",
                "statusCode": 418,
                "requestPayloadSize": 0,
                "responsePayloadSize": 0,
                "firstByteInAt": "2021-03-22 07:53:44.718669 +0000 UTC",
                "lastByteInAt": "2021-03-22 07:53:44.718669 +0000 UTC",
                "firstByteOutAt": "",
                "lastByteOutAt": "",
                "retries": 0,
                "url": "http://localhost:8082/status/200,418,501,502,503/delay/100ms-600ms",
                "uri": "/status/200,418,501,502,503/delay/100ms-600ms",
                "requestID": "",
                "headers": {
                    "content-length": [
                        "0"
                    ],
                    "date": [
                        "Mon, 22 Mar 2021 07:53:44 GMT"
                    ],
                    "goto-host": [
                        "Localhost.lan.local@1.1.1.1:8082"
                    ],
                    "goto-in-at": [
                        "2021-03-22 07:53:44.179623 +0000 UTC"
                    ],
                    "goto-out-at": [
                        "2021-03-22 07:53:44.718339 +0000 UTC"
                    ],
                    "goto-port": [
                        "8082"
                    ],
                    "goto-protocol": [
                        "HTTP"
                    ],
                    "goto-remote-address": [
                        "[::1]:58215"
                    ],
                    "goto-requested-status": [
                        "418"
                    ],
                    "goto-response-delay": [
                        "536ms"
                    ],
                    "goto-response-status": [
                        "418"
                    ],
                    "goto-took": [
                        "538.71573ms"
                    ],
                    "request-from-goto": [
                        "peer1"
                    ],
                    "request-from-goto-host": [
                        "Localhost.lan.local@1.1.1.1:8081"
                    ],
                    "request-host": [
                        "localhost:8082"
                    ],
                    "request-protocol": [
                        "HTTP/1.1"
                    ],
                    "request-targetid": [
                        "peer1_to_peer2[1][1]"
                    ],
                    "request-uri": [
                        "/status/200,418,501,502,503/delay/100ms-600ms"
                    ],
                    "request-user-agent": [
                        "Go-http-client/1.1"
                    ],
                    "request-x": [
                        "x1"
                    ],
                    "status": [
                        "418 I'm a teapot"
                    ],
                    "via-goto": [
                        "peer2"
                    ]
                },
                "retryURL": "",
                "lastRetryReason": "",
                "tookNanos": 542000068,
                "errors": {}
            },
            {
                "targetName": "peer1_to_peer2",
                "targetID": "peer1_to_peer2[1][2]",
                "status": "501 Not Implemented",
                "statusCode": 501,
                "requestPayloadSize": 0,
                "responsePayloadSize": 0,
                "firstByteInAt": "2021-03-22 07:53:46.238653 +0000 UTC",
                "lastByteInAt": "2021-03-22 07:53:46.238653 +0000 UTC",
                "firstByteOutAt": "",
                "lastByteOutAt": "",
                "retries": 1,
                "url": "http://localhost:8082/status/200,418,501,502,503/delay/100ms-600ms",
                "uri": "/status/200,418,501,502,503/delay/100ms-600ms",
                "requestID": "",
                "headers": {
                    "content-length": [
                        "0"
                    ],
                    "date": [
                        "Mon, 22 Mar 2021 07:53:46 GMT"
                    ],
                    "goto-host": [
                        "Localhost.lan.local@1.1.1.1:8082"
                    ],
                    "goto-in-at": [
                        "2021-03-22 07:53:46.136092 +0000 UTC"
                    ],
                    "goto-out-at": [
                        "2021-03-22 07:53:46.238349 +0000 UTC"
                    ],
                    "goto-port": [
                        "8082"
                    ],
                    "goto-protocol": [
                        "HTTP"
                    ],
                    "goto-remote-address": [
                        "[::1]:58215"
                    ],
                    "goto-requested-status": [
                        "501"
                    ],
                    "goto-response-delay": [
                        "101ms"
                    ],
                    "goto-response-status": [
                        "501"
                    ],
                    "goto-took": [
                        "102.25697ms"
                    ],
                    "request-from-goto": [
                        "peer1"
                    ],
                    "request-from-goto-host": [
                        "Localhost.lan.local@1.1.1.1:8081"
                    ],
                    "request-host": [
                        "localhost:8082"
                    ],
                    "request-protocol": [
                        "HTTP/1.1"
                    ],
                    "request-targetid": [
                        "peer1_to_peer2[1][2]"
                    ],
                    "request-uri": [
                        "/status/200,418,501,502,503/delay/100ms-600ms"
                    ],
                    "request-user-agent": [
                        "Go-http-client/1.1"
                    ],
                    "request-x": [
                        "x1"
                    ],
                    "status": [
                        "501 Not Implemented"
                    ],
                    "via-goto": [
                        "peer2"
                    ]
                },
                "retryURL": "",
                "lastRetryReason": "503 Service Unavailable",
                "tookNanos": 103018309,
                "errors": {}
            }
       ],
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
