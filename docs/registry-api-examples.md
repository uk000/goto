
### Peer JSON Schema 
(to register a peer via /registry/peers/add)

|Field|Data Type|Description|
|---|---|---|
| name      | string | Name/Label of a peer |
| namespace | string | Namespace of the peer instance (if available, else `local`) |
| pod       | string | Pod/Hostname of the peer instance |
| address   | string | IP address of the peer instance |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |

### Peers JSON Schema 
Map of peer labels to peer details, where each peer details include the following info
(output of /registry/peers)

|Field|Data Type|Description|
|---|---|---|
| name      | string | Name/Label of a peer |
| namespace | string | Namespace of the peer instance (if available, else `local`) |
| pods      | map string->PodDetails | Map of Pod Addresses to Pod Details. See [Pod Details JSON Schema] below(#pod-details-json-schema) |
| podEpochs | map string->[]PodEpoch   | Past lives of this pod since last cleanup. |


### Pod Details JSON Schema 

|Field|Data Type|Description|
|---|---|---|
| name      | string | Pod/Host Name |
| address   | string | Pod Address |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |
| url       | string | URL where this peer is reachable |
| healthy   | bool   | Whether the pod was found to be healthy at last interaction |
| offline   | bool   | Whether the pod is determined to be offline. Cloned and dump-loaded pods are marked as offline until they reconnect to the registry |
| currentEpoch | PodEpoch   | Current lifetime details of this pod |
| pastEpochs | []PodEpoch   | Past lives of this pod since last cleanup. |


### Pod Epoch JSON Schema 

|Field|Data Type|Description|
|---|---|---|
| epoch      | int | Epoch count of this pod |
| name      | string | Pod/Host Name |
| address   | string | Pod Address |
| node      | string | Host node where the peer is located |
| cluster   | string | Cluster/DC ID where the peer is located |
| firstContact   | time | First time this pod connected (at registration) |
| lastContact   | time | Last time this pod sent its reminder |


### Peer Target JSON Schema
** Same as [Client Target JSON Schema](../README.md#client-target-json-schema)

### Peer Job JSON Schema
** Same as [Jobs JSON Schema](../README.md#job-json-schema)


### Registry APIs Examples:

```
curl -X POST http://localhost:8080/registry/peers/clear

curl localhost:8080/registry/peers/add --data '
{ 
"name": "peer1",
"namespace": "test",
"pod": "podXYZ",
"address":	"1.1.1.1:8081"
}'
curl -X POST http://localhost:8080/registry/peers/peer1/remove/1.1.1.1:8081

curl http://localhost:8080/registry/peers/peer1/health/1.1.1.1:8081

curl -X POST http://localhost:8080/registry/peers/peer1/health/cleanup

curl -X POST http://localhost:8080/registry/peers/health/cleanup

curl localhost:8080/registry/peers

curl -X POST http://localhost:8080/registry/peers/peer1/targets/clear

curl localhost:8080/registry/peers/peer1/targets/add --data '
{ 
"name": "t1",
"method":	"POST",
"url": "http://somewhere/foo",
"headers":[["x", "x1"],["y", "y1"]],
"body": "{\"test\":\"this\"}",
"replicas": 2, 
"requestCount": 2, 
"delay": "200ms", 
"sendID": true,
"autoInvoke": true
}'

curl -X POST http://localhost:8080/registry/peers/peer1/targets/t1,t2/remove

curl http://localhost:8080/registry/peers/peer1/targets

curl -X POST http://localhost:8080/registry/peers/peer1/targets/t1,t2/invoke

curl -X POST http://localhost:8080/registry/peers/peer1/targets/invoke/all

curl -X POST http://localhost:8080/registry/peers/peer1/jobs/clear

curl localhost:8080/registry/peers/peer1/jobs/add --data '
{ 
"id": "job1",
"task": {
	"name": "job1",
	"method":	"POST",
	"url": "http://somewhere/echo",
	"headers":[["x", "x1"],["y", "y1"]],
	"body": "{\"test\":\"this\"}",
	"replicas": 1, 
  "requestCount": 1, 
	"delay": "200ms",
	"parseJSON": true
},
"auto": true,
"count": 10,
"keepFirst": true,
"maxResults": 5,
"delay": "1s"
}'

curl localhost:8080/registry/peers/peer1/jobs/add --data '
{ 
"id": "job2",
"task": {"cmd": "sh", "args": ["-c", "date +%s; echo Hello; sleep 1;"]},
"auto": true,
"count": 10,
"keepFirst": true,
"maxResults": 5,
"delay": "1s"
}'

curl -X POST http://localhost:8080/registry/peers/jobs/store/file/some.conf?path=/tmp/foo/conf --data-binary @somepath/some-file.txt

curl -X POST http://localhost:8080/registry/peers/peer1/jobs/add/script/peer1-script-job --data-binary @somepath/somescript.sh

curl -X POST http://localhost:8080/registry/peers/jobs/add/script/all-peers-script-job --data-binary @somepath/somescript.sh

curl -X POST http://localhost:8080/registry/peers/jobs/all-peers-script-job/run

curl -X POST http://localhost:8080/registry/peers/peer1/jobs/job1,job2/remove

curl http://localhost:8080/registry/peers/jobs

curl http://localhost:8080/registry/peers/peer1/jobs

curl -X POST http://localhost:8080/registry/peers/peer1/jobs/job1,job2/invoke

curl -X POST http://localhost:8080/registry/peers/peer1/jobs/invoke/all

#store data in the peer1 locker under subkeys A->B->C
curl -X POST http://localhost:8080/registry/peers/peer1/locker/store/A,B,C --data '{"some":"data"}'

#call URI `/request/headers/track/add/x` on all instances of peer1
curl -X POST http://localhost:8080/registry/peers/peer1/call?uri=/request/headers/track/add/x

curl -s http://localhost:8080/registry/peers/call?uri=/request/headers/track

#store data in the `current` locker under path `A->B->C`
curl -X POST http://localhost:8080/registry/lockers/current/store/A,B,C --data '{"some":"data"}'

#store data in a locker named `lockerA` under path `A->B->C`
curl -X POST http://localhost:8080/registry/lockers/lockerA/store/A,B,C --data '{"some":"data"}'

#see paths where data is stored in all lockers
curl -s localhost:8080/registry/lockers/data/paths

#find all paths where text `foo` appears
curl -s localhost:8080/registry/lockers/search/foo

#get data from lockerA at path A->B->C
curl -v localhost:8080/registry/lockers/lockerA/get/XX,1,2

#search across all peer events and get results in unified reverse order
curl -s localhost:8080/registry/lockers/all/peers/events/search/some search text?unified=y&reverse=y&data=y

#dump all contents of lockerA
curl -s localhost:8080/registry/lockers/lockerA/dump

#dump all contents of all lockers
curl -s localhost:8080/registry/lockers/all/dump

#generate a dump of registry
curl -s localhost:8080/registry/dump

#Load registry data from a previously generated dump
curl -X POST http://localhost:8080/registry/load --data-binary @registry.dump

```
<br/>

<details>
<summary>Registry Peers List Example</summary>
<p>

```json
$ curl -s localhost:8080/registry/peers | jq
{
  "peer1": {
    "name": "peer1",
    "namespace": "local",
    "pods": {
      "1.0.0.1:8081": {
        "name": "peer1",
        "address": "1.0.0.1:8081",
        "node": "vm-1",
        "cluster": "cluster-1",
        "url": "http://1.0.0.1:8081",
        "healthy": true,
        "offline": false,
        "currentEpoch": {
          "epoch": 2,
          "name": "peer1",
          "address": "1.0.0.1:8081",
          "node": "vm-1",
          "cluster": "cluster-1",
          "firstContact": "2020-07-08T12:29:03.076479-07:00",
          "lastContact": "2020-07-08T12:29:03.076479-07:00"
        },
        "pastEpochs": [
          {
            "epoch": 0,
            "name": "peer1",
            "address": "1.0.0.1:8081",
            "node": "vm-1",
            "cluster": "cluster-1",
            "firstContact": "2020-07-08T12:28:06.986875-07:00",
            "lastContact": "2020-07-08T12:28:06.986875-07:00"
          },
          {
            "epoch": 1,
            "name": "peer1",
            "address": "1.0.0.1:8081",
            "node": "vm-1",
            "cluster": "cluster-1",
            "firstContact": "2020-07-08T12:28:45.276196-07:00",
            "lastContact": "2020-07-08T12:28:45.276196-07:00"
          }
        ]
      },
      "1.0.0.2:8081": {
        "name": "peer1",
        "address": "1.0.0.2:8081",
        "node": "vm-1",
        "cluster": "cluster-1",
        "url": "http://1.0.0.2:8081",
        "healthy": true,
        "offline": false,
        "currentEpoch": {
          "epoch": 0,
          "name": "peer1",
          "address": "1.0.0.2:8081",
          "node": "vm-1",
          "cluster": "cluster-1",
          "firstContact": "2020-07-08T12:29:00.066019-07:00",
          "lastContact": "2020-07-08T12:29:00.066019-07:00"
        },
        "pastEpochs": null
      }
    }
  },
  "peer2": {
    "name": "peer2",
    "namespace": "local",
    "pods": {
      "2.2.2.2:8082": {
        "name": "peer2",
        "address": "2.2.2.2:8082",
        "node": "vm-2",
        "cluster": "cluster-2",
        "url": "http://2.2.2.2:8082",
        "healthy": true,
        "offline": false,
        "currentEpoch": {
          "epoch": 1,
          "name": "peer2",
          "address": "2.2.2.2:8082",
          "node": "vm-2",
          "cluster": "cluster-2",
          "firstContact": "2020-07-08T12:29:00.066019-07:00",
          "lastContact": "2020-07-08T12:29:00.066019-07:00"
        },
        "pastEpochs": [
          {
            "epoch": 0,
            "name": "peer2",
            "address": "2.2.2.2:8082",
            "node": "vm-2",
            "cluster": "cluster-2",
            "firstContact": "2020-07-08T12:28:06.986736-07:00",
            "lastContact": "2020-07-08T12:28:36.993819-07:00"
          }
        ]
      }
    }
  }
}
```
</p>
</details>

<br/>

<details>
<summary>Registry Locker Example</summary>
<p>

```json
$ curl -s localhost:8080/registry/peers/lockers

{
  "default": {
    "label": "default",
    "peerLockers": {
      "peer1": {
        "instanceLockers": {
          "1.0.0.1:8081": {
            "locker": {
              "client": {
                "data": "",
                "subKeys": {
                  "peer1_to_peer2": {
                    "data": "...",
                    "subKeys": {},
                    "firstReported": "2020-11-20T23:34:58.059154-08:00",
                    "lastReported": "2020-11-20T23:35:02.299239-08:00"
                  },
                  "peer1_to_peer3": {
                    "data": "...",
                    "subKeys": {},
                    "firstReported": "2020-11-20T23:34:58.057888-08:00",
                    "lastReported": "2020-11-20T23:35:02.297347-08:00"
                  }
                },
                "firstReported": "2020-11-20T23:34:58.052197-08:00",
                "lastReported": "0001-01-01T00:00:00Z"
              }
            },
            "active": true
          },
          "1.0.0.1:9091": {
            "locker": {
              "client": {
                "data": "",
                "subKeys": {
                  "peer1_to_peer2": {
                    "data": "...",
                    "subKeys": {},
                    "firstReported": "2020-11-20T23:34:58.057506-08:00",
                    "lastReported": "2020-11-20T23:35:02.281845-08:00"
                  },
                  "peer1_to_peer4": {
                    "data": "...",
                    "subKeys": {},
                    "firstReported": "2020-11-20T23:34:58.053469-08:00",
                    "lastReported": "2020-11-20T23:35:02.276481-08:00"
                  }
                },
                "firstReported": "2020-11-20T23:34:58.053469-08:00",
                "lastReported": "0001-01-01T00:00:00Z"
              }
            },
            "active": true
          }
        },
        "locker": {
          "locker": {},
          "active": true
        }
      },
      "peer2": {
        "instanceLockers": {
          "1.0.0.1:8082": {
            "locker": {
              "client": {
                "data": "",
                "subKeys": {
                  "peer2_to_peer1": {
                    "data": "...",
                    "subKeys": {},
                    "firstReported": "2020-11-20T23:34:58.068331-08:00",
                    "lastReported": "2020-11-20T23:35:02.301491-08:00"
                  },
                  "peer2_to_peer5": {
                    "data": "...",
                    "subKeys": {},
                    "firstReported": "2020-11-20T23:34:58.055716-08:00",
                    "lastReported": "2020-11-20T23:35:02.27662-08:00"
                  }
                },
                "firstReported": "2020-11-20T23:34:58.052091-08:00",
                "lastReported": "0001-01-01T00:00:00Z"
              }
            },
            "active": true
          },
        },
        "locker": {
          "locker": {},
          "active": true
        }
      }
    },
    "dataLocker": {
      "locker": {},
      "active": true
    },
    "current": true
  },
  "lockerA": {
    "label": "lockerA",
    "peerLockers": {},
    "dataLocker": {
      "locker": {
        "AA": {
          "data": "",
          "subKeys": {
            "B": {
              "data": "...",
              "subKeys": {},
              "firstReported": "2020-11-20T23:47:17.564845-08:00",
              "lastReported": "2020-11-20T23:47:17.564845-08:00"
            }
          },
          "firstReported": "2020-11-20T23:47:17.564845-08:00",
          "lastReported": "0001-01-01T00:00:00Z"
        }
      },
      "active": true
    },
    "current": false
  },
  "lockerB": {
    "label": "lockerB",
    "peerLockers": {},
    "dataLocker": {
      "locker": {
        "XX": {
          "data": "",
          "subKeys": {
            "XY": {
              "data": "...",
              "subKeys": {},
              "firstReported": "2020-11-20T23:46:52.861559-08:00",
              "lastReported": "0001-01-01T00:00:00Z"
            }
          },
          "firstReported": "2020-11-20T23:46:52.861559-08:00",
          "lastReported": "0001-01-01T00:00:00Z"
        }
      },
      "active": true
    },
    "current": false
  }
}

```
</p>
</details>

<br/>

<details>
<summary>Peers Client Summary Results Example</summary>
<p>

```json

$ curl -s localhost:8080/registry/peers/client/results?detailed=n

{
    "peer1": {
        "countsByStatusCodes": {
            "200": {
                "count": 44
            },
            "418": {
                "count": 47
            },
            "501": {
                "count": 58
            },
            "502": {
                "count": 9
            },
            "503": {
                "count": 2
            }
        },
        "countsByHeaders": {
            "goto-host": 160,
            "request-from-goto": 160,
            "via-goto": 160
        },
        "countsByHeaderValues": {
            "goto-host": {
                "Localhost.local@1.1.1.1:8082": 40,
                "Localhost.local@1.1.1.1:8083": 40,
                "Localhost.local@1.1.1.1:8084": 40,
                "Localhost.local@1.1.1.1:8085": 40
            },
            "request-from-goto": {
                "peer1": 160
            },
            "via-goto": {
                "peer2": 40,
                "peer3": 40,
                "peer4": 40,
                "peer5": 40
            }
        },
        "countsByURIs": {
            "/status/200,418,501,502,503/delay/100ms-600ms": {
                "count": 160
            }
        },
        "countsByRetries": {
            "1": {
                "count": 32
            },
            "2": {
                "count": 26
            }
        },
        "countsByRetryReasons": {
            "502 Bad Gateway": {
                "count": 26
            },
            "503 Service Unavailable": {
                "count": 32
            }
        },
        "countsByTimeBuckets": {
            "[101 200]": {
                "count": 32
            },
            "[201 300]": {
                "count": 33
            },
            "[301 500]": {
                "count": 62
            },
            "[501 1000]": {
                "count": 33
            }
        },
        "targetInvocationCounts": {
            "peer1_to_peer2": 40,
            "peer1_to_peer3": 40,
            "peer1_to_peer4": 40,
            "peer1_to_peer5": 40
        },
        "targetFirstResponses": {
            "peer1_to_peer2": "2021-03-22T00:31:51.044662-07:00",
            "peer1_to_peer3": "2021-03-22T00:31:50.996957-07:00",
            "peer1_to_peer4": "2021-03-22T00:31:50.957113-07:00",
            "peer1_to_peer5": "2021-03-22T00:31:53.775167-07:00"
        },
        "targetLastResponses": {
            "peer1_to_peer2": "2021-03-22T00:32:30.008988-07:00",
            "peer1_to_peer3": "2021-03-22T00:32:31.583897-07:00",
            "peer1_to_peer4": "2021-03-22T00:32:39.503793-07:00",
            "peer1_to_peer5": "2021-03-22T00:32:35.929634-07:00"
        },
        "resultsByTargets": {
            "peer1_to_peer2": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 14
                    },
                    "418": {
                        "count": 11
                    },
                    "501": {
                        "count": 12
                    },
                    "502": {
                        "count": 3
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8082": 40
                    },
                    "request-from-goto": {
                        "peer1": 40
                    },
                    "via-goto": {
                        "peer2": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 5
                    },
                    "2": {
                        "count": 7
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 7
                    },
                    "503 Service Unavailable": {
                        "count": 5
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 13
                    },
                    "[201 300]": {
                        "count": 9
                    },
                    "[301 500]": {
                        "count": 12
                    },
                    "[501 1000]": {
                        "count": 6
                    }
                }
            },
            "peer1_to_peer3": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 6
                    },
                    "418": {
                        "count": 10
                    },
                    "501": {
                        "count": 21
                    },
                    "502": {
                        "count": 2
                    },
                    "503": {
                        "count": 1
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8083": 40
                    },
                    "request-from-goto": {
                        "peer1": 40
                    },
                    "via-goto": {
                        "peer3": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 7
                    },
                    "2": {
                        "count": 6
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 3
                    },
                    "503 Service Unavailable": {
                        "count": 10
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 5
                    },
                    "[201 300]": {
                        "count": 10
                    },
                    "[301 500]": {
                        "count": 17
                    },
                    "[501 1000]": {
                        "count": 8
                    }
                }
            },
            "peer1_to_peer4": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 15
                    },
                    "418": {
                        "count": 13
                    },
                    "501": {
                        "count": 9
                    },
                    "502": {
                        "count": 2
                    },
                    "503": {
                        "count": 1
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8084": 40
                    },
                    "request-from-goto": {
                        "peer1": 40
                    },
                    "via-goto": {
                        "peer4": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 8
                    },
                    "2": {
                        "count": 8
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 8
                    },
                    "503 Service Unavailable": {
                        "count": 8
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 10
                    },
                    "[201 300]": {
                        "count": 5
                    },
                    "[301 500]": {
                        "count": 16
                    },
                    "[501 1000]": {
                        "count": 9
                    }
                }
            },
            "peer1_to_peer5": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 9
                    },
                    "418": {
                        "count": 13
                    },
                    "501": {
                        "count": 16
                    },
                    "502": {
                        "count": 2
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8085": 40
                    },
                    "request-from-goto": {
                        "peer1": 40
                    },
                    "via-goto": {
                        "peer5": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 12
                    },
                    "2": {
                        "count": 5
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 8
                    },
                    "503 Service Unavailable": {
                        "count": 9
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 4
                    },
                    "[201 300]": {
                        "count": 9
                    },
                    "[301 500]": {
                        "count": 17
                    },
                    "[501 1000]": {
                        "count": 10
                    }
                }
            }
        }
    },
    "peer2": {
        "countsByStatusCodes": {
            "200": {
                "count": 50
            },
            "418": {
                "count": 49
            },
            "501": {
                "count": 54
            },
            "502": {
                "count": 5
            },
            "503": {
                "count": 2
            }
        },
        "countsByHeaders": {
            "goto-host": 160,
            "request-from-goto": 160,
            "via-goto": 160
        },
        "countsByHeaderValues": {
            "goto-host": {
                "Localhost.local@1.1.1.1:8081": 40,
                "Localhost.local@1.1.1.1:8083": 40,
                "Localhost.local@1.1.1.1:8084": 40,
                "Localhost.local@1.1.1.1:8085": 40
            },
            "request-from-goto": {
                "peer2": 160
            },
            "via-goto": {
                "peer1": 40,
                "peer3": 40,
                "peer4": 40,
                "peer5": 40
            }
        },
        "countsByURIs": {
            "/status/200,418,501,502,503/delay/100ms-600ms": {
                "count": 160
            }
        },
        "countsByRetries": {
            "1": {
                "count": 39
            },
            "2": {
                "count": 20
            }
        },
        "countsByRetryReasons": {
            "502 Bad Gateway": {
                "count": 23
            },
            "503 Service Unavailable": {
                "count": 36
            }
        },
        "countsByTimeBuckets": {
            "[101 200]": {
                "count": 29
            },
            "[201 300]": {
                "count": 38
            },
            "[301 500]": {
                "count": 67
            },
            "[501 1000]": {
                "count": 26
            }
        },
        "targetInvocationCounts": {
            "peer2_to_peer1": 40,
            "peer2_to_peer3": 40,
            "peer2_to_peer4": 40,
            "peer2_to_peer5": 40
        },
        "targetFirstResponses": {
            "peer2_to_peer1": "2021-03-22T00:31:50.996966-07:00",
            "peer2_to_peer3": "2021-03-22T00:31:53.365017-07:00",
            "peer2_to_peer4": "2021-03-22T00:31:51.378186-07:00",
            "peer2_to_peer5": "2021-03-22T00:31:53.546589-07:00"
        },
        "targetLastResponses": {
            "peer2_to_peer1": "2021-03-22T00:32:37.828519-07:00",
            "peer2_to_peer3": "2021-03-22T00:32:35.758997-07:00",
            "peer2_to_peer4": "2021-03-22T00:32:34.273392-07:00",
            "peer2_to_peer5": "2021-03-22T00:32:35.841207-07:00"
        },
        "resultsByTargets": {
            "peer2_to_peer1": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 13
                    },
                    "418": {
                        "count": 12
                    },
                    "501": {
                        "count": 13
                    },
                    "502": {
                        "count": 2
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8081": 40
                    },
                    "request-from-goto": {
                        "peer2": 40
                    },
                    "via-goto": {
                        "peer1": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 6
                    },
                    "2": {
                        "count": 7
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 5
                    },
                    "503 Service Unavailable": {
                        "count": 8
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 8
                    },
                    "[201 300]": {
                        "count": 10
                    },
                    "[301 500]": {
                        "count": 16
                    },
                    "[501 1000]": {
                        "count": 6
                    }
                }
            },
            "peer2_to_peer3": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 16
                    },
                    "418": {
                        "count": 8
                    },
                    "501": {
                        "count": 12
                    },
                    "502": {
                        "count": 2
                    },
                    "503": {
                        "count": 2
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8083": 40
                    },
                    "request-from-goto": {
                        "peer2": 40
                    },
                    "via-goto": {
                        "peer3": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 8
                    },
                    "2": {
                        "count": 6
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 4
                    },
                    "503 Service Unavailable": {
                        "count": 10
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 9
                    },
                    "[201 300]": {
                        "count": 8
                    },
                    "[301 500]": {
                        "count": 19
                    },
                    "[501 1000]": {
                        "count": 4
                    }
                }
            },
            "peer2_to_peer4": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 7
                    },
                    "418": {
                        "count": 13
                    },
                    "501": {
                        "count": 19
                    },
                    "502": {
                        "count": 1
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8084": 40
                    },
                    "request-from-goto": {
                        "peer2": 40
                    },
                    "via-goto": {
                        "peer4": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 10
                    },
                    "2": {
                        "count": 4
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 5
                    },
                    "503 Service Unavailable": {
                        "count": 9
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 6
                    },
                    "[201 300]": {
                        "count": 10
                    },
                    "[301 500]": {
                        "count": 15
                    },
                    "[501 1000]": {
                        "count": 9
                    }
                }
            },
            "peer2_to_peer5": {
                "countsByStatusCodes": {
                    "200": {
                        "count": 14
                    },
                    "418": {
                        "count": 16
                    },
                    "501": {
                        "count": 10
                    }
                },
                "countsByHeaders": {
                    "goto-host": 40,
                    "request-from-goto": 40,
                    "via-goto": 40
                },
                "countsByHeaderValues": {
                    "goto-host": {
                        "Localhost.local@1.1.1.1:8085": 40
                    },
                    "request-from-goto": {
                        "peer2": 40
                    },
                    "via-goto": {
                        "peer5": 40
                    }
                },
                "countsByURIs": {
                    "/status/200,418,501,502,503/delay/100ms-600ms": {
                        "count": 40
                    }
                },
                "countsByRetries": {
                    "1": {
                        "count": 15
                    },
                    "2": {
                        "count": 3
                    }
                },
                "countsByRetryReasons": {
                    "502 Bad Gateway": {
                        "count": 9
                    },
                    "503 Service Unavailable": {
                        "count": 9
                    }
                },
                "countsByTimeBuckets": {
                    "[101 200]": {
                        "count": 6
                    },
                    "[201 300]": {
                        "count": 10
                    },
                    "[301 500]": {
                        "count": 17
                    },
                    "[501 1000]": {
                        "count": 7
                    }
                }
            }
        }
    }
}
```

</p>
</details>

<br/>

<details>
<summary>Peers Client Detailed Results Example</summary>
<p>

```json

$ curl -s localhost:8080/registry/peers/client/results?detailed=Y
{
    "peer1": {
        "peer1_to_peer2": {
            "target": "peer1_to_peer2",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:51.044662-07:00",
            "lastResponse": "2021-03-22T00:32:30.008988-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 14,
                "418 I'm a teapot": 11,
                "501 Not Implemented": 12,
                "502 Bad Gateway": 3
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 19,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8082": {
                            "count": 40,
                            "retries": 19,
                            "firstResponse": "2021-03-22T00:31:51.044757-07:00",
                            "lastResponse": "2021-03-22T00:32:30.009008-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.044756-07:00",
                            "lastResponse": "2021-03-22T00:32:30.009007-07:00"
                        },
                        "418": {
                            "count": 11,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:51.078527-07:00",
                            "lastResponse": "2021-03-22T00:32:28.13585-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:57.705096-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018166-07:00"
                        },
                        "502": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.153752-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239665-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8082": {
                            "200": {
                                "count": 14,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:51.044758-07:00",
                                "lastResponse": "2021-03-22T00:32:30.009008-07:00"
                            },
                            "418": {
                                "count": 11,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:31:51.078529-07:00",
                                "lastResponse": "2021-03-22T00:32:28.135851-07:00"
                            },
                            "501": {
                                "count": 12,
                                "retries": 8,
                                "firstResponse": "2021-03-22T00:31:57.705097-07:00",
                                "lastResponse": "2021-03-22T00:32:29.018167-07:00"
                            },
                            "502": {
                                "count": 3,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:32:03.153755-07:00",
                                "lastResponse": "2021-03-22T00:32:27.239666-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.480271-07:00",
                    "lastResponse": "2021-03-22T00:46:09.48247-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 19,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 40,
                            "retries": 19,
                            "firstResponse": "2021-03-22T00:31:51.044751-07:00",
                            "lastResponse": "2021-03-22T00:32:30.009003-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.04475-07:00",
                            "lastResponse": "2021-03-22T00:32:30.009003-07:00"
                        },
                        "418": {
                            "count": 11,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:51.07852-07:00",
                            "lastResponse": "2021-03-22T00:32:28.135848-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:57.705091-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018161-07:00"
                        },
                        "502": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.153749-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239662-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 14,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:51.044752-07:00",
                                "lastResponse": "2021-03-22T00:32:30.009004-07:00"
                            },
                            "418": {
                                "count": 11,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:31:51.078522-07:00",
                                "lastResponse": "2021-03-22T00:32:28.135849-07:00"
                            },
                            "501": {
                                "count": 12,
                                "retries": 8,
                                "firstResponse": "2021-03-22T00:31:57.705092-07:00",
                                "lastResponse": "2021-03-22T00:32:29.018161-07:00"
                            },
                            "502": {
                                "count": 3,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:32:03.15375-07:00",
                                "lastResponse": "2021-03-22T00:32:27.239663-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.480266-07:00",
                    "lastResponse": "2021-03-22T00:46:09.482472-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 19,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 40,
                            "retries": 19,
                            "firstResponse": "2021-03-22T00:31:51.044723-07:00",
                            "lastResponse": "2021-03-22T00:32:30.008999-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.044722-07:00",
                            "lastResponse": "2021-03-22T00:32:30.008998-07:00"
                        },
                        "418": {
                            "count": 11,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:51.078491-07:00",
                            "lastResponse": "2021-03-22T00:32:28.135844-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:57.705085-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018157-07:00"
                        },
                        "502": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.153744-07:00",
                            "lastResponse": "2021-03-22T00:32:27.23966-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 14,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:51.044745-07:00",
                                "lastResponse": "2021-03-22T00:32:30.009-07:00"
                            },
                            "418": {
                                "count": 11,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:31:51.078505-07:00",
                                "lastResponse": "2021-03-22T00:32:28.135845-07:00"
                            },
                            "501": {
                                "count": 12,
                                "retries": 8,
                                "firstResponse": "2021-03-22T00:31:57.705087-07:00",
                                "lastResponse": "2021-03-22T00:32:29.018158-07:00"
                            },
                            "502": {
                                "count": 3,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:32:03.153746-07:00",
                                "lastResponse": "2021-03-22T00:32:27.239661-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.480268-07:00",
                    "lastResponse": "2021-03-22T00:46:09.482473-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 14,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:51.044683-07:00",
                    "lastResponse": "2021-03-22T00:32:30.00899-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.044774-07:00",
                            "lastResponse": "2021-03-22T00:32:14.542304-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:58.783957-07:00",
                            "lastResponse": "2021-03-22T00:32:24.504442-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:08.798565-07:00",
                            "lastResponse": "2021-03-22T00:32:30.009024-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:52.157221-07:00",
                            "lastResponse": "2021-03-22T00:32:17.743344-07:00"
                        }
                    }
                },
                "418": {
                    "count": 11,
                    "retries": 2,
                    "firstResponse": "2021-03-22T00:31:51.078481-07:00",
                    "lastResponse": "2021-03-22T00:32:28.13584-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.097784-07:00",
                            "lastResponse": "2021-03-22T00:32:15.805726-07:00"
                        },
                        "[201 300]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.078546-07:00",
                            "lastResponse": "2021-03-22T00:32:04.915442-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:02.579828-07:00",
                            "lastResponse": "2021-03-22T00:32:28.135859-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.118028-07:00",
                            "lastResponse": "2021-03-22T00:32:12.118028-07:00"
                        }
                    }
                },
                "501": {
                    "count": 12,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:57.705077-07:00",
                    "lastResponse": "2021-03-22T00:32:29.01815-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 6,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:57.705119-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018181-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.758821-07:00",
                            "lastResponse": "2021-03-22T00:32:18.527018-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:58.032574-07:00",
                            "lastResponse": "2021-03-22T00:32:19.653328-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:21.678999-07:00",
                            "lastResponse": "2021-03-22T00:32:21.678999-07:00"
                        }
                    }
                },
                "502": {
                    "count": 3,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:32:03.153738-07:00",
                    "lastResponse": "2021-03-22T00:32:27.239656-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.153775-07:00",
                            "lastResponse": "2021-03-22T00:32:03.153775-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:10.390367-07:00",
                            "lastResponse": "2021-03-22T00:32:10.390367-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:27.239678-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239678-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 19,
                    "firstResponse": "2021-03-22T00:31:51.044713-07:00",
                    "lastResponse": "2021-03-22T00:32:30.008993-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.044715-07:00",
                            "lastResponse": "2021-03-22T00:32:30.008993-07:00"
                        },
                        "418": {
                            "count": 11,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:51.078485-07:00",
                            "lastResponse": "2021-03-22T00:32:28.135842-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:57.70508-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018153-07:00"
                        },
                        "502": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.153741-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239657-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 13,
                            "retries": 11,
                            "firstResponse": "2021-03-22T00:31:51.044771-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018178-07:00"
                        },
                        "[201 300]": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.078543-07:00",
                            "lastResponse": "2021-03-22T00:32:24.504439-07:00"
                        },
                        "[301 500]": {
                            "count": 12,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:58.032569-07:00",
                            "lastResponse": "2021-03-22T00:32:30.009021-07:00"
                        },
                        "[501 1000]": {
                            "count": 6,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:52.157219-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239673-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 5,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:54.097739-07:00",
                    "lastResponse": "2021-03-22T00:32:27.741496-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:14.542272-07:00",
                            "lastResponse": "2021-03-22T00:32:14.542272-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:54.09774-07:00",
                            "lastResponse": "2021-03-22T00:32:12.117993-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:58.032541-07:00",
                            "lastResponse": "2021-03-22T00:32:27.741498-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:54.097779-07:00",
                            "lastResponse": "2021-03-22T00:32:27.741562-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:58.032571-07:00",
                            "lastResponse": "2021-03-22T00:31:58.032571-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.118025-07:00",
                            "lastResponse": "2021-03-22T00:32:12.118025-07:00"
                        }
                    }
                },
                "2": {
                    "count": 7,
                    "retries": 14,
                    "firstResponse": "2021-03-22T00:31:57.705068-07:00",
                    "lastResponse": "2021-03-22T00:32:27.239651-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:07.915094-07:00",
                            "lastResponse": "2021-03-22T00:32:07.915094-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.705069-07:00",
                            "lastResponse": "2021-03-22T00:32:23.736636-07:00"
                        },
                        "502": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.153733-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239653-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:57.705114-07:00",
                            "lastResponse": "2021-03-22T00:32:23.736668-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:10.390344-07:00",
                            "lastResponse": "2021-03-22T00:32:19.653321-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:27.239675-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239675-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 7,
                    "retries": 12,
                    "firstResponse": "2021-03-22T00:31:57.705073-07:00",
                    "lastResponse": "2021-03-22T00:32:23.736638-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:07.915096-07:00",
                            "lastResponse": "2021-03-22T00:32:14.542274-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.117997-07:00",
                            "lastResponse": "2021-03-22T00:32:12.117997-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.705075-07:00",
                            "lastResponse": "2021-03-22T00:32:23.736638-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:10.390307-07:00",
                            "lastResponse": "2021-03-22T00:32:10.390307-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:57.705116-07:00",
                            "lastResponse": "2021-03-22T00:32:23.736669-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:10.390346-07:00",
                            "lastResponse": "2021-03-22T00:32:19.653325-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.118026-07:00",
                            "lastResponse": "2021-03-22T00:32:12.118026-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 5,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:54.097743-07:00",
                    "lastResponse": "2021-03-22T00:32:27.741501-07:00",
                    "byStatusCodes": {
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.097744-07:00",
                            "lastResponse": "2021-03-22T00:31:54.097744-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:58.032543-07:00",
                            "lastResponse": "2021-03-22T00:32:27.741502-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.153737-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239655-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:54.097782-07:00",
                            "lastResponse": "2021-03-22T00:32:27.741565-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:58.032572-07:00",
                            "lastResponse": "2021-03-22T00:31:58.032572-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:27.239677-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239677-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 13,
                    "retries": 11,
                    "firstResponse": "2021-03-22T00:31:51.044769-07:00",
                    "lastResponse": "2021-03-22T00:32:29.018176-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.04477-07:00",
                            "lastResponse": "2021-03-22T00:32:14.542297-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.097776-07:00",
                            "lastResponse": "2021-03-22T00:32:15.805724-07:00"
                        },
                        "501": {
                            "count": 6,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:57.705111-07:00",
                            "lastResponse": "2021-03-22T00:32:29.018177-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.153768-07:00",
                            "lastResponse": "2021-03-22T00:32:03.153768-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 9,
                    "retries": 0,
                    "firstResponse": "2021-03-22T00:31:51.07854-07:00",
                    "lastResponse": "2021-03-22T00:32:24.504437-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:58.783954-07:00",
                            "lastResponse": "2021-03-22T00:32:24.504438-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.078541-07:00",
                            "lastResponse": "2021-03-22T00:32:04.91544-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.758819-07:00",
                            "lastResponse": "2021-03-22T00:32:18.527013-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 12,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:58.032566-07:00",
                    "lastResponse": "2021-03-22T00:32:30.009018-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:08.798561-07:00",
                            "lastResponse": "2021-03-22T00:32:30.00902-07:00"
                        },
                        "418": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:02.579826-07:00",
                            "lastResponse": "2021-03-22T00:32:28.135857-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:58.032568-07:00",
                            "lastResponse": "2021-03-22T00:32:19.653316-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:10.390338-07:00",
                            "lastResponse": "2021-03-22T00:32:10.390338-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 6,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:52.157217-07:00",
                    "lastResponse": "2021-03-22T00:32:27.239672-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:52.157218-07:00",
                            "lastResponse": "2021-03-22T00:32:17.74334-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.118022-07:00",
                            "lastResponse": "2021-03-22T00:32:12.118022-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:21.678994-07:00",
                            "lastResponse": "2021-03-22T00:32:21.678994-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:27.239673-07:00",
                            "lastResponse": "2021-03-22T00:32:27.239673-07:00"
                        }
                    }
                }
            }
        },
        "peer1_to_peer3": {
            "target": "peer1_to_peer3",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:50.996957-07:00",
            "lastResponse": "2021-03-22T00:32:31.583897-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 6,
                "418 I'm a teapot": 10,
                "501 Not Implemented": 21,
                "502 Bad Gateway": 2,
                "503 Service Unavailable": 1
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 19,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8083": {
                            "count": 40,
                            "retries": 19,
                            "firstResponse": "2021-03-22T00:31:50.996988-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583915-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 6,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.331028-07:00",
                            "lastResponse": "2021-03-22T00:32:25.692069-07:00"
                        },
                        "418": {
                            "count": 10,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:50.996988-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583915-07:00"
                        },
                        "501": {
                            "count": 21,
                            "retries": 9,
                            "firstResponse": "2021-03-22T00:31:51.84418-07:00",
                            "lastResponse": "2021-03-22T00:32:28.097979-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:21.850446-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456821-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.544-07:00",
                            "lastResponse": "2021-03-22T00:32:15.544-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8083": {
                            "200": {
                                "count": 6,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:31:51.33103-07:00",
                                "lastResponse": "2021-03-22T00:32:25.692071-07:00"
                            },
                            "418": {
                                "count": 10,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:50.996997-07:00",
                                "lastResponse": "2021-03-22T00:32:31.583915-07:00"
                            },
                            "501": {
                                "count": 21,
                                "retries": 9,
                                "firstResponse": "2021-03-22T00:31:51.844182-07:00",
                                "lastResponse": "2021-03-22T00:32:28.097981-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:21.850447-07:00",
                                "lastResponse": "2021-03-22T00:32:22.456822-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:32:15.544001-07:00",
                                "lastResponse": "2021-03-22T00:32:15.544001-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.48078-07:00",
                    "lastResponse": "2021-03-22T00:46:09.481934-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 19,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 40,
                            "retries": 19,
                            "firstResponse": "2021-03-22T00:31:50.996985-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583912-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 6,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.331023-07:00",
                            "lastResponse": "2021-03-22T00:32:25.692065-07:00"
                        },
                        "418": {
                            "count": 10,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:50.996984-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583912-07:00"
                        },
                        "501": {
                            "count": 21,
                            "retries": 9,
                            "firstResponse": "2021-03-22T00:31:51.844145-07:00",
                            "lastResponse": "2021-03-22T00:32:28.097976-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:21.850443-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456815-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.543996-07:00",
                            "lastResponse": "2021-03-22T00:32:15.543996-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 6,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:31:51.331024-07:00",
                                "lastResponse": "2021-03-22T00:32:25.692065-07:00"
                            },
                            "418": {
                                "count": 10,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:50.996985-07:00",
                                "lastResponse": "2021-03-22T00:32:31.583912-07:00"
                            },
                            "501": {
                                "count": 21,
                                "retries": 9,
                                "firstResponse": "2021-03-22T00:31:51.844146-07:00",
                                "lastResponse": "2021-03-22T00:32:28.097977-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:21.850444-07:00",
                                "lastResponse": "2021-03-22T00:32:22.456816-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:32:15.543997-07:00",
                                "lastResponse": "2021-03-22T00:32:15.543997-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.480782-07:00",
                    "lastResponse": "2021-03-22T00:46:09.481936-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 19,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer3": {
                            "count": 40,
                            "retries": 19,
                            "firstResponse": "2021-03-22T00:31:50.99698-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583909-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 6,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.331014-07:00",
                            "lastResponse": "2021-03-22T00:32:25.692059-07:00"
                        },
                        "418": {
                            "count": 10,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:50.99698-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583909-07:00"
                        },
                        "501": {
                            "count": 21,
                            "retries": 9,
                            "firstResponse": "2021-03-22T00:31:51.844139-07:00",
                            "lastResponse": "2021-03-22T00:32:28.09797-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:21.850439-07:00",
                            "lastResponse": "2021-03-22T00:32:22.45681-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.543993-07:00",
                            "lastResponse": "2021-03-22T00:32:15.543993-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer3": {
                            "200": {
                                "count": 6,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:31:51.331017-07:00",
                                "lastResponse": "2021-03-22T00:32:25.69206-07:00"
                            },
                            "418": {
                                "count": 10,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:50.996981-07:00",
                                "lastResponse": "2021-03-22T00:32:31.58391-07:00"
                            },
                            "501": {
                                "count": 21,
                                "retries": 9,
                                "firstResponse": "2021-03-22T00:31:51.844141-07:00",
                                "lastResponse": "2021-03-22T00:32:28.097971-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:21.85044-07:00",
                                "lastResponse": "2021-03-22T00:32:22.456811-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:32:15.543994-07:00",
                                "lastResponse": "2021-03-22T00:32:15.543994-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.480784-07:00",
                    "lastResponse": "2021-03-22T00:46:09.481937-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 6,
                    "retries": 1,
                    "firstResponse": "2021-03-22T00:31:51.331004-07:00",
                    "lastResponse": "2021-03-22T00:32:25.692054-07:00",
                    "byTimeBuckets": {
                        "[301 500]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.331048-07:00",
                            "lastResponse": "2021-03-22T00:32:25.692085-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:56.171948-07:00",
                            "lastResponse": "2021-03-22T00:31:58.19697-07:00"
                        }
                    }
                },
                "418": {
                    "count": 10,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:50.996972-07:00",
                    "lastResponse": "2021-03-22T00:32:31.583905-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:50.99703-07:00",
                            "lastResponse": "2021-03-22T00:31:50.99703-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:09.04708-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583932-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.083707-07:00",
                            "lastResponse": "2021-03-22T00:32:27.411408-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:09.060064-07:00",
                            "lastResponse": "2021-03-22T00:32:28.470934-07:00"
                        }
                    }
                },
                "501": {
                    "count": 21,
                    "retries": 9,
                    "firstResponse": "2021-03-22T00:31:51.844132-07:00",
                    "lastResponse": "2021-03-22T00:32:28.097964-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.318905-07:00",
                            "lastResponse": "2021-03-22T00:32:28.097999-07:00"
                        },
                        "[201 300]": {
                            "count": 7,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:52.558577-07:00",
                            "lastResponse": "2021-03-22T00:32:24.805632-07:00"
                        },
                        "[301 500]": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.844199-07:00",
                            "lastResponse": "2021-03-22T00:32:26.530802-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:57.304174-07:00",
                            "lastResponse": "2021-03-22T00:32:07.074391-07:00"
                        }
                    }
                },
                "502": {
                    "count": 2,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:32:21.850433-07:00",
                    "lastResponse": "2021-03-22T00:32:22.456803-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.456842-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456842-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:21.850461-07:00",
                            "lastResponse": "2021-03-22T00:32:21.850461-07:00"
                        }
                    }
                },
                "503": {
                    "count": 1,
                    "retries": 2,
                    "firstResponse": "2021-03-22T00:32:15.543988-07:00",
                    "lastResponse": "2021-03-22T00:32:15.543988-07:00",
                    "byTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.544014-07:00",
                            "lastResponse": "2021-03-22T00:32:15.544014-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 19,
                    "firstResponse": "2021-03-22T00:31:50.996974-07:00",
                    "lastResponse": "2021-03-22T00:32:31.583906-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 6,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.331008-07:00",
                            "lastResponse": "2021-03-22T00:32:25.692057-07:00"
                        },
                        "418": {
                            "count": 10,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:50.996975-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583907-07:00"
                        },
                        "501": {
                            "count": 21,
                            "retries": 9,
                            "firstResponse": "2021-03-22T00:31:51.844135-07:00",
                            "lastResponse": "2021-03-22T00:32:28.097966-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:21.850435-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456805-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.54399-07:00",
                            "lastResponse": "2021-03-22T00:32:15.54399-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 5,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:50.997029-07:00",
                            "lastResponse": "2021-03-22T00:32:28.097996-07:00"
                        },
                        "[201 300]": {
                            "count": 10,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:52.558574-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583926-07:00"
                        },
                        "[301 500]": {
                            "count": 17,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:51.331046-07:00",
                            "lastResponse": "2021-03-22T00:32:27.411405-07:00"
                        },
                        "[501 1000]": {
                            "count": 8,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.171944-07:00",
                            "lastResponse": "2021-03-22T00:32:28.470922-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 7,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:56.171921-07:00",
                    "lastResponse": "2021-03-22T00:32:31.583899-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:56.171922-07:00",
                            "lastResponse": "2021-03-22T00:31:56.171922-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:24.878803-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583901-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:01.587157-07:00",
                            "lastResponse": "2021-03-22T00:32:16.455839-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.152509-07:00",
                            "lastResponse": "2021-03-22T00:32:12.152509-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:16.455876-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583928-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:01.587178-07:00",
                            "lastResponse": "2021-03-22T00:32:24.878855-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.171946-07:00",
                            "lastResponse": "2021-03-22T00:32:28.470924-07:00"
                        }
                    }
                },
                "2": {
                    "count": 6,
                    "retries": 12,
                    "firstResponse": "2021-03-22T00:31:57.304121-07:00",
                    "lastResponse": "2021-03-22T00:32:22.456796-07:00",
                    "byStatusCodes": {
                        "501": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.304122-07:00",
                            "lastResponse": "2021-03-22T00:32:07.644161-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:21.850429-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456798-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.543984-07:00",
                            "lastResponse": "2021-03-22T00:32:15.543984-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.456836-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456836-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:07.644202-07:00",
                            "lastResponse": "2021-03-22T00:32:21.850457-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.544011-07:00",
                            "lastResponse": "2021-03-22T00:32:15.544011-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:57.304163-07:00",
                            "lastResponse": "2021-03-22T00:32:07.074385-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 3,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:32:01.587158-07:00",
                    "lastResponse": "2021-03-22T00:32:21.850431-07:00",
                    "byStatusCodes": {
                        "501": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:01.587159-07:00",
                            "lastResponse": "2021-03-22T00:32:01.587159-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:21.850432-07:00",
                            "lastResponse": "2021-03-22T00:32:21.850432-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.543987-07:00",
                            "lastResponse": "2021-03-22T00:32:15.543987-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:21.85046-07:00",
                            "lastResponse": "2021-03-22T00:32:21.85046-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:01.587179-07:00",
                            "lastResponse": "2021-03-22T00:32:15.544012-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 10,
                    "retries": 14,
                    "firstResponse": "2021-03-22T00:31:56.171923-07:00",
                    "lastResponse": "2021-03-22T00:32:31.583903-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:56.171923-07:00",
                            "lastResponse": "2021-03-22T00:31:56.171923-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:24.878806-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583903-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:57.304126-07:00",
                            "lastResponse": "2021-03-22T00:32:16.455842-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.456801-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456801-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:12.152512-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456839-07:00"
                        },
                        "[201 300]": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:07.644204-07:00",
                            "lastResponse": "2021-03-22T00:32:31.58393-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:24.878857-07:00",
                            "lastResponse": "2021-03-22T00:32:24.878857-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.171947-07:00",
                            "lastResponse": "2021-03-22T00:32:28.470926-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 5,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:50.997027-07:00",
                    "lastResponse": "2021-03-22T00:32:28.097994-07:00",
                    "byStatusCodes": {
                        "418": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:50.997028-07:00",
                            "lastResponse": "2021-03-22T00:31:50.997028-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.3189-07:00",
                            "lastResponse": "2021-03-22T00:32:28.097995-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.456832-07:00",
                            "lastResponse": "2021-03-22T00:32:22.456832-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 10,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:52.55857-07:00",
                    "lastResponse": "2021-03-22T00:32:31.583923-07:00",
                    "byStatusCodes": {
                        "418": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:09.047076-07:00",
                            "lastResponse": "2021-03-22T00:32:31.583925-07:00"
                        },
                        "501": {
                            "count": 7,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:52.558572-07:00",
                            "lastResponse": "2021-03-22T00:32:24.805629-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:21.850455-07:00",
                            "lastResponse": "2021-03-22T00:32:21.850455-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 17,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:51.331044-07:00",
                    "lastResponse": "2021-03-22T00:32:27.411403-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.331044-07:00",
                            "lastResponse": "2021-03-22T00:32:25.692082-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.083703-07:00",
                            "lastResponse": "2021-03-22T00:32:27.411404-07:00"
                        },
                        "501": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.844194-07:00",
                            "lastResponse": "2021-03-22T00:32:26.5308-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:15.544008-07:00",
                            "lastResponse": "2021-03-22T00:32:15.544008-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 8,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:56.171942-07:00",
                    "lastResponse": "2021-03-22T00:32:28.470921-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:56.171943-07:00",
                            "lastResponse": "2021-03-22T00:31:58.196968-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:09.060041-07:00",
                            "lastResponse": "2021-03-22T00:32:28.470921-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:57.304159-07:00",
                            "lastResponse": "2021-03-22T00:32:07.074382-07:00"
                        }
                    }
                }
            }
        },
        "peer1_to_peer4": {
            "target": "peer1_to_peer4",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:50.957113-07:00",
            "lastResponse": "2021-03-22T00:32:39.503793-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 15,
                "418 I'm a teapot": 13,
                "501 Not Implemented": 9,
                "502 Bad Gateway": 2,
                "503 Service Unavailable": 1
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 24,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8084": {
                            "count": 40,
                            "retries": 24,
                            "firstResponse": "2021-03-22T00:31:50.95716-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503828-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 15,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:51.351238-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705698-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.957159-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990427-07:00"
                        },
                        "501": {
                            "count": 9,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:32:05.532465-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503827-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:12.977722-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661912-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532395-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532395-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8084": {
                            "200": {
                                "count": 15,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:51.35124-07:00",
                                "lastResponse": "2021-03-22T00:32:33.705699-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:50.957161-07:00",
                                "lastResponse": "2021-03-22T00:32:32.990428-07:00"
                            },
                            "501": {
                                "count": 9,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:32:05.532466-07:00",
                                "lastResponse": "2021-03-22T00:32:39.503828-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:12.977722-07:00",
                                "lastResponse": "2021-03-22T00:32:17.661913-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:32:04.532396-07:00",
                                "lastResponse": "2021-03-22T00:32:04.532396-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.479673-07:00",
                    "lastResponse": "2021-03-22T00:46:09.483059-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 24,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 40,
                            "retries": 24,
                            "firstResponse": "2021-03-22T00:31:50.95715-07:00",
                            "lastResponse": "2021-03-22T00:32:39.50382-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 15,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:51.351233-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705696-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.957149-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990423-07:00"
                        },
                        "501": {
                            "count": 9,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:32:05.532461-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503819-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:12.977718-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661906-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532389-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532389-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 15,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:51.351235-07:00",
                                "lastResponse": "2021-03-22T00:32:33.705696-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:50.957151-07:00",
                                "lastResponse": "2021-03-22T00:32:32.990424-07:00"
                            },
                            "501": {
                                "count": 9,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:32:05.532462-07:00",
                                "lastResponse": "2021-03-22T00:32:39.503821-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:12.977719-07:00",
                                "lastResponse": "2021-03-22T00:32:17.661907-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:32:04.532391-07:00",
                                "lastResponse": "2021-03-22T00:32:04.532391-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.479676-07:00",
                    "lastResponse": "2021-03-22T00:46:09.483055-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 24,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer4": {
                            "count": 40,
                            "retries": 24,
                            "firstResponse": "2021-03-22T00:31:50.957143-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503815-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 15,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:51.351206-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705689-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.957142-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990418-07:00"
                        },
                        "501": {
                            "count": 9,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:32:05.532458-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503814-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:12.977715-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661902-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532384-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532384-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer4": {
                            "200": {
                                "count": 15,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:51.351208-07:00",
                                "lastResponse": "2021-03-22T00:32:33.70569-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:50.957144-07:00",
                                "lastResponse": "2021-03-22T00:32:32.990419-07:00"
                            },
                            "501": {
                                "count": 9,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:32:05.532459-07:00",
                                "lastResponse": "2021-03-22T00:32:39.503816-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:12.977716-07:00",
                                "lastResponse": "2021-03-22T00:32:17.661903-07:00"
                            },
                            "503": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:32:04.532385-07:00",
                                "lastResponse": "2021-03-22T00:32:04.532385-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.47967-07:00",
                    "lastResponse": "2021-03-22T00:46:09.483058-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 15,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:51.351121-07:00",
                    "lastResponse": "2021-03-22T00:32:33.705684-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.115971-07:00",
                            "lastResponse": "2021-03-22T00:32:33.672237-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:52.090973-07:00",
                            "lastResponse": "2021-03-22T00:31:52.641063-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.911188-07:00",
                            "lastResponse": "2021-03-22T00:32:28.972299-07:00"
                        },
                        "[501 1000]": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.351251-07:00",
                            "lastResponse": "2021-03-22T00:32:33.70572-07:00"
                        }
                    }
                },
                "418": {
                    "count": 13,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:50.957126-07:00",
                    "lastResponse": "2021-03-22T00:32:32.990412-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:50.957211-07:00",
                            "lastResponse": "2021-03-22T00:32:09.438313-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:54.288075-07:00",
                            "lastResponse": "2021-03-22T00:32:30.417527-07:00"
                        },
                        "[301 500]": {
                            "count": 6,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.514954-07:00",
                            "lastResponse": "2021-03-22T00:32:29.850086-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:58.536495-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990444-07:00"
                        }
                    }
                },
                "501": {
                    "count": 9,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:32:05.532451-07:00",
                    "lastResponse": "2021-03-22T00:32:39.503806-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:10.08254-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503887-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:05.532478-07:00",
                            "lastResponse": "2021-03-22T00:32:23.93014-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.770036-07:00",
                            "lastResponse": "2021-03-22T00:32:08.770036-07:00"
                        }
                    }
                },
                "502": {
                    "count": 2,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:32:12.977711-07:00",
                    "lastResponse": "2021-03-22T00:32:17.661895-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:12.977735-07:00",
                            "lastResponse": "2021-03-22T00:32:12.977735-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:17.661934-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661934-07:00"
                        }
                    }
                },
                "503": {
                    "count": 1,
                    "retries": 2,
                    "firstResponse": "2021-03-22T00:32:04.532377-07:00",
                    "lastResponse": "2021-03-22T00:32:04.532377-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532415-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532415-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 24,
                    "firstResponse": "2021-03-22T00:31:50.957135-07:00",
                    "lastResponse": "2021-03-22T00:32:39.503808-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 15,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:51.351174-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705686-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.957137-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990414-07:00"
                        },
                        "501": {
                            "count": 9,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:32:05.532454-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503809-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:12.977712-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661898-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532379-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532379-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 10,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:50.957209-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503877-07:00"
                        },
                        "[201 300]": {
                            "count": 5,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:52.090972-07:00",
                            "lastResponse": "2021-03-22T00:32:30.417523-07:00"
                        },
                        "[301 500]": {
                            "count": 16,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:51.911184-07:00",
                            "lastResponse": "2021-03-22T00:32:29.850084-07:00"
                        },
                        "[501 1000]": {
                            "count": 9,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:51.351249-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705712-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 8,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:55.115944-07:00",
                    "lastResponse": "2021-03-22T00:32:33.705678-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:55.115945-07:00",
                            "lastResponse": "2021-03-22T00:32:33.70568-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:57.752846-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990409-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.770008-07:00",
                            "lastResponse": "2021-03-22T00:32:08.770008-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.115969-07:00",
                            "lastResponse": "2021-03-22T00:32:27.15723-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:57.752865-07:00",
                            "lastResponse": "2021-03-22T00:32:22.094095-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.536478-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705714-07:00"
                        }
                    }
                },
                "2": {
                    "count": 8,
                    "retries": 16,
                    "firstResponse": "2021-03-22T00:32:04.243014-07:00",
                    "lastResponse": "2021-03-22T00:32:39.503797-07:00",
                    "byStatusCodes": {
                        "418": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:04.243016-07:00",
                            "lastResponse": "2021-03-22T00:32:30.4175-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:18.611557-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503799-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:12.977707-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661889-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532372-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532372-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:32:04.53241-07:00",
                            "lastResponse": "2021-03-22T00:32:39.50388-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:17.661928-07:00",
                            "lastResponse": "2021-03-22T00:32:30.417524-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:04.243042-07:00",
                            "lastResponse": "2021-03-22T00:32:18.611584-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 8,
                    "retries": 13,
                    "firstResponse": "2021-03-22T00:31:55.115946-07:00",
                    "lastResponse": "2021-03-22T00:32:30.417502-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.115947-07:00",
                            "lastResponse": "2021-03-22T00:32:22.094073-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:30.417503-07:00",
                            "lastResponse": "2021-03-22T00:32:30.417503-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:08.77001-07:00",
                            "lastResponse": "2021-03-22T00:32:25.156236-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:12.97771-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661893-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:55.11597-07:00",
                            "lastResponse": "2021-03-22T00:32:25.156262-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:17.661932-07:00",
                            "lastResponse": "2021-03-22T00:32:30.417526-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:18.611585-07:00",
                            "lastResponse": "2021-03-22T00:32:22.094097-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.770034-07:00",
                            "lastResponse": "2021-03-22T00:32:08.770034-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 8,
                    "retries": 11,
                    "firstResponse": "2021-03-22T00:31:57.752847-07:00",
                    "lastResponse": "2021-03-22T00:32:39.503801-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:27.157209-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705683-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:57.752847-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990411-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:39.503804-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503804-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532375-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532375-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:04.532413-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503884-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:57.752866-07:00",
                            "lastResponse": "2021-03-22T00:32:04.243043-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:58.536484-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705717-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 10,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:31:50.957207-07:00",
                    "lastResponse": "2021-03-22T00:32:39.503867-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.115966-07:00",
                            "lastResponse": "2021-03-22T00:32:33.672234-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:50.957208-07:00",
                            "lastResponse": "2021-03-22T00:32:09.438309-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:10.082537-07:00",
                            "lastResponse": "2021-03-22T00:32:39.503875-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:12.977729-07:00",
                            "lastResponse": "2021-03-22T00:32:12.977729-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.532406-07:00",
                            "lastResponse": "2021-03-22T00:32:04.532406-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 5,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:52.090971-07:00",
                    "lastResponse": "2021-03-22T00:32:30.417521-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:52.090972-07:00",
                            "lastResponse": "2021-03-22T00:31:52.641059-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:54.288071-07:00",
                            "lastResponse": "2021-03-22T00:32:30.417522-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:17.661925-07:00",
                            "lastResponse": "2021-03-22T00:32:17.661925-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 16,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:51.91118-07:00",
                    "lastResponse": "2021-03-22T00:32:29.850083-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.911182-07:00",
                            "lastResponse": "2021-03-22T00:32:28.972296-07:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.514952-07:00",
                            "lastResponse": "2021-03-22T00:32:29.850084-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:05.532475-07:00",
                            "lastResponse": "2021-03-22T00:32:23.930135-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 9,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:51.351248-07:00",
                    "lastResponse": "2021-03-22T00:32:33.70571-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 5,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:51.351248-07:00",
                            "lastResponse": "2021-03-22T00:32:33.705711-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:58.536474-07:00",
                            "lastResponse": "2021-03-22T00:32:32.990438-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.770029-07:00",
                            "lastResponse": "2021-03-22T00:32:08.770029-07:00"
                        }
                    }
                }
            }
        },
        "peer1_to_peer5": {
            "target": "peer1_to_peer5",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:53.775167-07:00",
            "lastResponse": "2021-03-22T00:32:35.929634-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 9,
                "418 I'm a teapot": 13,
                "501 Not Implemented": 16,
                "502 Bad Gateway": 2
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 22,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8085": {
                            "count": 40,
                            "retries": 22,
                            "firstResponse": "2021-03-22T00:31:53.775193-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929646-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:54.8465-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007033-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.775193-07:00",
                            "lastResponse": "2021-03-22T00:32:32.339202-07:00"
                        },
                        "501": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.077141-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929646-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.788507-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622285-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8085": {
                            "200": {
                                "count": 9,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:54.846501-07:00",
                                "lastResponse": "2021-03-22T00:32:34.007034-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:53.775194-07:00",
                                "lastResponse": "2021-03-22T00:32:32.339203-07:00"
                            },
                            "501": {
                                "count": 16,
                                "retries": 10,
                                "firstResponse": "2021-03-22T00:31:56.077143-07:00",
                                "lastResponse": "2021-03-22T00:32:35.929647-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:00.788509-07:00",
                                "lastResponse": "2021-03-22T00:32:30.622286-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.481333-07:00",
                    "lastResponse": "2021-03-22T00:46:09.483642-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 22,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 40,
                            "retries": 22,
                            "firstResponse": "2021-03-22T00:31:53.775191-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929644-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:54.846495-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007031-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.775191-07:00",
                            "lastResponse": "2021-03-22T00:32:32.339199-07:00"
                        },
                        "501": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.077132-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929644-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.7885-07:00",
                            "lastResponse": "2021-03-22T00:32:30.62228-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 9,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:54.846497-07:00",
                                "lastResponse": "2021-03-22T00:32:34.007031-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:53.775192-07:00",
                                "lastResponse": "2021-03-22T00:32:32.3392-07:00"
                            },
                            "501": {
                                "count": 16,
                                "retries": 10,
                                "firstResponse": "2021-03-22T00:31:56.077133-07:00",
                                "lastResponse": "2021-03-22T00:32:35.929644-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:00.788502-07:00",
                                "lastResponse": "2021-03-22T00:32:30.622282-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.481329-07:00",
                    "lastResponse": "2021-03-22T00:46:09.483639-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 22,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer5": {
                            "count": 40,
                            "retries": 22,
                            "firstResponse": "2021-03-22T00:31:53.775188-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929641-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:54.84649-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007028-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.775188-07:00",
                            "lastResponse": "2021-03-22T00:32:32.339173-07:00"
                        },
                        "501": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.077124-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929641-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.788492-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622277-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer5": {
                            "200": {
                                "count": 9,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:54.846491-07:00",
                                "lastResponse": "2021-03-22T00:32:34.007029-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:53.775189-07:00",
                                "lastResponse": "2021-03-22T00:32:32.339174-07:00"
                            },
                            "501": {
                                "count": 16,
                                "retries": 10,
                                "firstResponse": "2021-03-22T00:31:56.077126-07:00",
                                "lastResponse": "2021-03-22T00:32:35.929641-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:00.788494-07:00",
                                "lastResponse": "2021-03-22T00:32:30.622278-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.481331-07:00",
                    "lastResponse": "2021-03-22T00:46:09.48364-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 9,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:54.846484-07:00",
                    "lastResponse": "2021-03-22T00:32:34.007025-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:24.091441-07:00",
                            "lastResponse": "2021-03-22T00:32:24.091441-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:18.170366-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007044-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:00.179563-07:00",
                            "lastResponse": "2021-03-22T00:32:31.60229-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.846514-07:00",
                            "lastResponse": "2021-03-22T00:32:15.195885-07:00"
                        }
                    }
                },
                "418": {
                    "count": 13,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:53.775183-07:00",
                    "lastResponse": "2021-03-22T00:32:32.339167-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:01.430317-07:00",
                            "lastResponse": "2021-03-22T00:32:22.598506-07:00"
                        },
                        "[201 300]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:03.225577-07:00",
                            "lastResponse": "2021-03-22T00:32:32.339214-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:04.065077-07:00",
                            "lastResponse": "2021-03-22T00:32:16.764256-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:53.775204-07:00",
                            "lastResponse": "2021-03-22T00:32:25.103536-07:00"
                        }
                    }
                },
                "501": {
                    "count": 16,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:31:56.077114-07:00",
                    "lastResponse": "2021-03-22T00:32:35.929636-07:00",
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:59.235121-07:00",
                            "lastResponse": "2021-03-22T00:32:28.45988-07:00"
                        },
                        "[301 500]": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:04.163899-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929656-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:56.077165-07:00",
                            "lastResponse": "2021-03-22T00:32:25.472672-07:00"
                        }
                    }
                },
                "502": {
                    "count": 2,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:32:00.788483-07:00",
                    "lastResponse": "2021-03-22T00:32:30.622272-07:00",
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:30.622301-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622301-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:00.788534-07:00",
                            "lastResponse": "2021-03-22T00:32:00.788534-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 22,
                    "firstResponse": "2021-03-22T00:31:53.775184-07:00",
                    "lastResponse": "2021-03-22T00:32:35.929638-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:54.846487-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007026-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.775184-07:00",
                            "lastResponse": "2021-03-22T00:32:32.339169-07:00"
                        },
                        "501": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.077118-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929639-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.788487-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622274-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:01.430314-07:00",
                            "lastResponse": "2021-03-22T00:32:24.091439-07:00"
                        },
                        "[201 300]": {
                            "count": 9,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:59.235118-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007039-07:00"
                        },
                        "[301 500]": {
                            "count": 17,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:00.179561-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929654-07:00"
                        },
                        "[501 1000]": {
                            "count": 10,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:53.7752-07:00",
                            "lastResponse": "2021-03-22T00:32:25.472668-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 12,
                    "retries": 12,
                    "firstResponse": "2021-03-22T00:31:53.77518-07:00",
                    "lastResponse": "2021-03-22T00:32:28.459843-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:15.195865-07:00",
                            "lastResponse": "2021-03-22T00:32:15.195865-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.775181-07:00",
                            "lastResponse": "2021-03-22T00:32:10.699295-07:00"
                        },
                        "501": {
                            "count": 8,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:59.235099-07:00",
                            "lastResponse": "2021-03-22T00:32:28.459844-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:59.235119-07:00",
                            "lastResponse": "2021-03-22T00:32:28.459877-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:04.065069-07:00",
                            "lastResponse": "2021-03-22T00:32:10.614968-07:00"
                        },
                        "[501 1000]": {
                            "count": 5,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.775201-07:00",
                            "lastResponse": "2021-03-22T00:32:25.47267-07:00"
                        }
                    }
                },
                "2": {
                    "count": 5,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:31:56.077108-07:00",
                    "lastResponse": "2021-03-22T00:32:34.007022-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:34.007023-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007023-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.598481-07:00",
                            "lastResponse": "2021-03-22T00:32:22.598481-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.077109-07:00",
                            "lastResponse": "2021-03-22T00:31:56.077109-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.788477-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622268-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.598502-07:00",
                            "lastResponse": "2021-03-22T00:32:22.598502-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:30.622297-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007041-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:00.788527-07:00",
                            "lastResponse": "2021-03-22T00:32:00.788527-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.077161-07:00",
                            "lastResponse": "2021-03-22T00:31:56.077161-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 8,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:32:04.065029-07:00",
                    "lastResponse": "2021-03-22T00:32:34.007024-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:34.007025-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007025-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:04.065031-07:00",
                            "lastResponse": "2021-03-22T00:32:04.065031-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:07.518763-07:00",
                            "lastResponse": "2021-03-22T00:32:28.459848-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:30.622271-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622271-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 4,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:15.786646-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007042-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:04.065076-07:00",
                            "lastResponse": "2021-03-22T00:32:10.614969-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:21.344303-07:00",
                            "lastResponse": "2021-03-22T00:32:21.344303-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 9,
                    "retries": 12,
                    "firstResponse": "2021-03-22T00:31:53.775182-07:00",
                    "lastResponse": "2021-03-22T00:32:25.472647-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:15.195866-07:00",
                            "lastResponse": "2021-03-22T00:32:15.195866-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.775182-07:00",
                            "lastResponse": "2021-03-22T00:32:22.598482-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:56.077113-07:00",
                            "lastResponse": "2021-03-22T00:32:25.472647-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:00.788482-07:00",
                            "lastResponse": "2021-03-22T00:32:00.788482-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:22.598505-07:00",
                            "lastResponse": "2021-03-22T00:32:22.598505-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.235121-07:00",
                            "lastResponse": "2021-03-22T00:31:59.235121-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:00.788531-07:00",
                            "lastResponse": "2021-03-22T00:32:07.051352-07:00"
                        },
                        "[501 1000]": {
                            "count": 5,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:53.775203-07:00",
                            "lastResponse": "2021-03-22T00:32:25.472672-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 4,
                    "retries": 2,
                    "firstResponse": "2021-03-22T00:32:01.43031-07:00",
                    "lastResponse": "2021-03-22T00:32:24.091436-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:24.091438-07:00",
                            "lastResponse": "2021-03-22T00:32:24.091438-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:01.430313-07:00",
                            "lastResponse": "2021-03-22T00:32:22.5985-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 9,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:59.235116-07:00",
                    "lastResponse": "2021-03-22T00:32:34.007038-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:18.170362-07:00",
                            "lastResponse": "2021-03-22T00:32:34.007039-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:03.225573-07:00",
                            "lastResponse": "2021-03-22T00:32:32.339211-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:59.235117-07:00",
                            "lastResponse": "2021-03-22T00:32:28.459874-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:30.622295-07:00",
                            "lastResponse": "2021-03-22T00:32:30.622295-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 17,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:32:00.179559-07:00",
                    "lastResponse": "2021-03-22T00:32:35.929653-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:00.17956-07:00",
                            "lastResponse": "2021-03-22T00:32:31.602287-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:04.065066-07:00",
                            "lastResponse": "2021-03-22T00:32:16.764252-07:00"
                        },
                        "501": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:04.163894-07:00",
                            "lastResponse": "2021-03-22T00:32:35.929654-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:00.788523-07:00",
                            "lastResponse": "2021-03-22T00:32:00.788523-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 10,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:53.775198-07:00",
                    "lastResponse": "2021-03-22T00:32:25.472667-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.84651-07:00",
                            "lastResponse": "2021-03-22T00:32:15.195882-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:53.775199-07:00",
                            "lastResponse": "2021-03-22T00:32:25.103534-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:56.077157-07:00",
                            "lastResponse": "2021-03-22T00:32:25.472667-07:00"
                        }
                    }
                }
            }
        }
    },
    "peer2": {
        "peer2_to_peer1": {
            "target": "peer2_to_peer1",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:50.996966-07:00",
            "lastResponse": "2021-03-22T00:32:37.828519-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 13,
                "418 I'm a teapot": 12,
                "501 Not Implemented": 13,
                "502 Bad Gateway": 2
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 20,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8081": {
                            "count": 40,
                            "retries": 20,
                            "firstResponse": "2021-03-22T00:31:50.997023-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828539-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.997022-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148856-07:00"
                        },
                        "418": {
                            "count": 12,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.185878-07:00",
                            "lastResponse": "2021-03-22T00:32:30.791756-07:00"
                        },
                        "501": {
                            "count": 13,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.542354-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828538-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.405883-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851803-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8081": {
                            "200": {
                                "count": 13,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:50.997023-07:00",
                                "lastResponse": "2021-03-22T00:32:36.148857-07:00"
                            },
                            "418": {
                                "count": 12,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:51.185879-07:00",
                                "lastResponse": "2021-03-22T00:32:30.791757-07:00"
                            },
                            "501": {
                                "count": 13,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:31:56.542355-07:00",
                                "lastResponse": "2021-03-22T00:32:37.82854-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:58.405885-07:00",
                                "lastResponse": "2021-03-22T00:32:28.851804-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.472437-07:00",
                    "lastResponse": "2021-03-22T00:46:09.475639-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 20,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 40,
                            "retries": 20,
                            "firstResponse": "2021-03-22T00:31:50.997019-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828533-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.997018-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148853-07:00"
                        },
                        "418": {
                            "count": 12,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.185864-07:00",
                            "lastResponse": "2021-03-22T00:32:30.791753-07:00"
                        },
                        "501": {
                            "count": 13,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.542351-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828533-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.40588-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851797-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 13,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:50.997019-07:00",
                                "lastResponse": "2021-03-22T00:32:36.148854-07:00"
                            },
                            "418": {
                                "count": 12,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:51.185875-07:00",
                                "lastResponse": "2021-03-22T00:32:30.791754-07:00"
                            },
                            "501": {
                                "count": 13,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:31:56.542352-07:00",
                                "lastResponse": "2021-03-22T00:32:37.828534-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:58.405881-07:00",
                                "lastResponse": "2021-03-22T00:32:28.851798-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.472441-07:00",
                    "lastResponse": "2021-03-22T00:46:09.475641-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 20,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 40,
                            "retries": 20,
                            "firstResponse": "2021-03-22T00:31:50.997015-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828529-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.997014-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148851-07:00"
                        },
                        "418": {
                            "count": 12,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.185858-07:00",
                            "lastResponse": "2021-03-22T00:32:30.791749-07:00"
                        },
                        "501": {
                            "count": 13,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.542348-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828528-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.405873-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851792-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 13,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:50.997016-07:00",
                                "lastResponse": "2021-03-22T00:32:36.148852-07:00"
                            },
                            "418": {
                                "count": 12,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:51.18586-07:00",
                                "lastResponse": "2021-03-22T00:32:30.791751-07:00"
                            },
                            "501": {
                                "count": 13,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:31:56.542349-07:00",
                                "lastResponse": "2021-03-22T00:32:37.82853-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:58.405875-07:00",
                                "lastResponse": "2021-03-22T00:32:28.851793-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.472443-07:00",
                    "lastResponse": "2021-03-22T00:46:09.475642-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 13,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:50.996985-07:00",
                    "lastResponse": "2021-03-22T00:32:36.148846-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:50.997058-07:00",
                            "lastResponse": "2021-03-22T00:32:08.074949-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:36.148872-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148872-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:52.959449-07:00",
                            "lastResponse": "2021-03-22T00:32:23.517602-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:02.218096-07:00",
                            "lastResponse": "2021-03-22T00:32:29.892088-07:00"
                        }
                    }
                },
                "418": {
                    "count": 12,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:51.185851-07:00",
                    "lastResponse": "2021-03-22T00:32:30.791743-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.290677-07:00",
                            "lastResponse": "2021-03-22T00:32:01.943654-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:17.888413-07:00",
                            "lastResponse": "2021-03-22T00:32:21.254049-07:00"
                        },
                        "[301 500]": {
                            "count": 7,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.185921-07:00",
                            "lastResponse": "2021-03-22T00:32:30.79177-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.11555-07:00",
                            "lastResponse": "2021-03-22T00:32:01.11555-07:00"
                        }
                    }
                },
                "501": {
                    "count": 13,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:56.542343-07:00",
                    "lastResponse": "2021-03-22T00:32:37.828522-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:02.640393-07:00",
                            "lastResponse": "2021-03-22T00:32:36.825893-07:00"
                        },
                        "[201 300]": {
                            "count": 5,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.542369-07:00",
                            "lastResponse": "2021-03-22T00:32:22.020217-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.7211-07:00",
                            "lastResponse": "2021-03-22T00:32:12.201758-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:37.828557-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828557-07:00"
                        }
                    }
                },
                "502": {
                    "count": 2,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:58.405867-07:00",
                    "lastResponse": "2021-03-22T00:32:28.851785-07:00",
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.405904-07:00",
                            "lastResponse": "2021-03-22T00:32:28.85182-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 20,
                    "firstResponse": "2021-03-22T00:31:50.996987-07:00",
                    "lastResponse": "2021-03-22T00:32:37.828525-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 13,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:50.996988-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148848-07:00"
                        },
                        "418": {
                            "count": 12,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:51.185854-07:00",
                            "lastResponse": "2021-03-22T00:32:30.791746-07:00"
                        },
                        "501": {
                            "count": 13,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.542344-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828525-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.40587-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851788-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:50.997056-07:00",
                            "lastResponse": "2021-03-22T00:32:36.825891-07:00"
                        },
                        "[201 300]": {
                            "count": 10,
                            "retries": 11,
                            "firstResponse": "2021-03-22T00:31:56.542365-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148867-07:00"
                        },
                        "[301 500]": {
                            "count": 16,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:51.18592-07:00",
                            "lastResponse": "2021-03-22T00:32:30.791767-07:00"
                        },
                        "[501 1000]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.115547-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828553-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 6,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:32:00.664017-07:00",
                    "lastResponse": "2021-03-22T00:32:21.254008-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:00.664019-07:00",
                            "lastResponse": "2021-03-22T00:32:16.780754-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:21.254011-07:00",
                            "lastResponse": "2021-03-22T00:32:21.254011-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:11.592766-07:00",
                            "lastResponse": "2021-03-22T00:32:12.20172-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.074945-07:00",
                            "lastResponse": "2021-03-22T00:32:08.074945-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:21.254044-07:00",
                            "lastResponse": "2021-03-22T00:32:21.254044-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.664686-07:00",
                            "lastResponse": "2021-03-22T00:32:16.780777-07:00"
                        }
                    }
                },
                "2": {
                    "count": 7,
                    "retries": 14,
                    "firstResponse": "2021-03-22T00:31:56.542321-07:00",
                    "lastResponse": "2021-03-22T00:32:36.148843-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:23.51758-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148843-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:17.888376-07:00",
                            "lastResponse": "2021-03-22T00:32:17.888376-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:56.542322-07:00",
                            "lastResponse": "2021-03-22T00:32:08.284917-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.405862-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851781-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 5,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.542367-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148868-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:08.284935-07:00",
                            "lastResponse": "2021-03-22T00:32:23.517599-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 5,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:58.405864-07:00",
                    "lastResponse": "2021-03-22T00:32:36.148845-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:08.074916-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148846-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:17.888379-07:00",
                            "lastResponse": "2021-03-22T00:32:17.888379-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:58.405865-07:00",
                            "lastResponse": "2021-03-22T00:31:58.405865-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:08.074947-07:00",
                            "lastResponse": "2021-03-22T00:32:08.074947-07:00"
                        },
                        "[201 300]": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:58.405902-07:00",
                            "lastResponse": "2021-03-22T00:32:36.14887-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:16.780778-07:00",
                            "lastResponse": "2021-03-22T00:32:16.780778-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 8,
                    "retries": 12,
                    "firstResponse": "2021-03-22T00:31:56.54234-07:00",
                    "lastResponse": "2021-03-22T00:32:28.851783-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:00.664023-07:00",
                            "lastResponse": "2021-03-22T00:32:23.517582-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:21.254013-07:00",
                            "lastResponse": "2021-03-22T00:32:21.254013-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.542341-07:00",
                            "lastResponse": "2021-03-22T00:32:12.201723-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:28.851784-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851784-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 3,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:56.542368-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851817-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:32:00.664689-07:00",
                            "lastResponse": "2021-03-22T00:32:23.517601-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 8,
                    "retries": 1,
                    "firstResponse": "2021-03-22T00:31:50.997054-07:00",
                    "lastResponse": "2021-03-22T00:32:36.82589-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:50.997055-07:00",
                            "lastResponse": "2021-03-22T00:32:08.074942-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.290674-07:00",
                            "lastResponse": "2021-03-22T00:32:01.943651-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:02.640389-07:00",
                            "lastResponse": "2021-03-22T00:32:36.82589-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 10,
                    "retries": 11,
                    "firstResponse": "2021-03-22T00:31:56.542363-07:00",
                    "lastResponse": "2021-03-22T00:32:36.148865-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:36.148866-07:00",
                            "lastResponse": "2021-03-22T00:32:36.148866-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:17.888404-07:00",
                            "lastResponse": "2021-03-22T00:32:21.254041-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.542364-07:00",
                            "lastResponse": "2021-03-22T00:32:22.020215-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:58.405896-07:00",
                            "lastResponse": "2021-03-22T00:32:28.851812-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 16,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:51.185914-07:00",
                    "lastResponse": "2021-03-22T00:32:30.791766-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 5,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:52.959446-07:00",
                            "lastResponse": "2021-03-22T00:32:23.517597-07:00"
                        },
                        "418": {
                            "count": 7,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:51.185919-07:00",
                            "lastResponse": "2021-03-22T00:32:30.791767-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.721096-07:00",
                            "lastResponse": "2021-03-22T00:32:12.20175-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-22T00:32:01.115543-07:00",
                    "lastResponse": "2021-03-22T00:32:37.82855-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:02.218091-07:00",
                            "lastResponse": "2021-03-22T00:32:29.892085-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:01.115545-07:00",
                            "lastResponse": "2021-03-22T00:32:01.115545-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:37.828552-07:00",
                            "lastResponse": "2021-03-22T00:32:37.828552-07:00"
                        }
                    }
                }
            }
        },
        "peer2_to_peer3": {
            "target": "peer2_to_peer3",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:53.365017-07:00",
            "lastResponse": "2021-03-22T00:32:35.758997-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 16,
                "418 I'm a teapot": 8,
                "501 Not Implemented": 12,
                "502 Bad Gateway": 2,
                "503 Service Unavailable": 2
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 20,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8083": {
                            "count": 40,
                            "retries": 20,
                            "firstResponse": "2021-03-22T00:31:53.365053-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759012-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 16,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:54.14011-07:00",
                            "lastResponse": "2021-03-22T00:32:24.545969-07:00"
                        },
                        "418": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.338297-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692091-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.365053-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759012-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:55.841833-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076112-07:00"
                        },
                        "503": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.349044-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028795-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8083": {
                            "200": {
                                "count": 16,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:54.140111-07:00",
                                "lastResponse": "2021-03-22T00:32:24.54597-07:00"
                            },
                            "418": {
                                "count": 8,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:31:59.338298-07:00",
                                "lastResponse": "2021-03-22T00:32:32.692092-07:00"
                            },
                            "501": {
                                "count": 12,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:53.365054-07:00",
                                "lastResponse": "2021-03-22T00:32:35.759013-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:55.841834-07:00",
                                "lastResponse": "2021-03-22T00:32:16.076114-07:00"
                            },
                            "503": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:03.349045-07:00",
                                "lastResponse": "2021-03-22T00:32:32.028796-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.474559-07:00",
                    "lastResponse": "2021-03-22T00:46:09.476824-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 20,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 40,
                            "retries": 20,
                            "firstResponse": "2021-03-22T00:31:53.365049-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759009-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 16,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:54.140108-07:00",
                            "lastResponse": "2021-03-22T00:32:24.545965-07:00"
                        },
                        "418": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.338295-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692086-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.365049-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759009-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:55.84183-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076107-07:00"
                        },
                        "503": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.349038-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028793-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 16,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:54.140109-07:00",
                                "lastResponse": "2021-03-22T00:32:24.545966-07:00"
                            },
                            "418": {
                                "count": 8,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:31:59.338295-07:00",
                                "lastResponse": "2021-03-22T00:32:32.692087-07:00"
                            },
                            "501": {
                                "count": 12,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:53.36505-07:00",
                                "lastResponse": "2021-03-22T00:32:35.75901-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:55.841831-07:00",
                                "lastResponse": "2021-03-22T00:32:16.076108-07:00"
                            },
                            "503": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:03.349039-07:00",
                                "lastResponse": "2021-03-22T00:32:32.028793-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.474554-07:00",
                    "lastResponse": "2021-03-22T00:46:09.476825-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 20,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer3": {
                            "count": 40,
                            "retries": 20,
                            "firstResponse": "2021-03-22T00:31:53.365044-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759006-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 16,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:54.140104-07:00",
                            "lastResponse": "2021-03-22T00:32:24.54596-07:00"
                        },
                        "418": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.338284-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692082-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.365044-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759006-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:55.841814-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076102-07:00"
                        },
                        "503": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.349033-07:00",
                            "lastResponse": "2021-03-22T00:32:32.02879-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer3": {
                            "200": {
                                "count": 16,
                                "retries": 7,
                                "firstResponse": "2021-03-22T00:31:54.140105-07:00",
                                "lastResponse": "2021-03-22T00:32:24.545962-07:00"
                            },
                            "418": {
                                "count": 8,
                                "retries": 1,
                                "firstResponse": "2021-03-22T00:31:59.338293-07:00",
                                "lastResponse": "2021-03-22T00:32:32.692083-07:00"
                            },
                            "501": {
                                "count": 12,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:53.365045-07:00",
                                "lastResponse": "2021-03-22T00:32:35.759006-07:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:31:55.841828-07:00",
                                "lastResponse": "2021-03-22T00:32:16.076104-07:00"
                            },
                            "503": {
                                "count": 2,
                                "retries": 4,
                                "firstResponse": "2021-03-22T00:32:03.349035-07:00",
                                "lastResponse": "2021-03-22T00:32:32.028791-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.474557-07:00",
                    "lastResponse": "2021-03-22T00:46:09.476827-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 16,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:54.140099-07:00",
                    "lastResponse": "2021-03-22T00:32:24.545954-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:11.951911-07:00",
                            "lastResponse": "2021-03-22T00:32:24.545988-07:00"
                        },
                        "[201 300]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:54.140123-07:00",
                            "lastResponse": "2021-03-22T00:32:14.318726-07:00"
                        },
                        "[301 500]": {
                            "count": 7,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:56.757417-07:00",
                            "lastResponse": "2021-03-22T00:32:23.206999-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:06.962997-07:00",
                            "lastResponse": "2021-03-22T00:32:20.142032-07:00"
                        }
                    }
                },
                "418": {
                    "count": 8,
                    "retries": 1,
                    "firstResponse": "2021-03-22T00:31:59.33828-07:00",
                    "lastResponse": "2021-03-22T00:32:32.692075-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:23.847544-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692109-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:59.338307-07:00",
                            "lastResponse": "2021-03-22T00:32:20.871569-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.880567-07:00",
                            "lastResponse": "2021-03-22T00:32:26.229661-07:00"
                        }
                    }
                },
                "501": {
                    "count": 12,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:53.365037-07:00",
                    "lastResponse": "2021-03-22T00:32:35.759002-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:53.365066-07:00",
                            "lastResponse": "2021-03-22T00:32:22.311761-07:00"
                        },
                        "[201 300]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.885625-07:00",
                            "lastResponse": "2021-03-22T00:32:26.176168-07:00"
                        },
                        "[301 500]": {
                            "count": 7,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.865318-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759026-07:00"
                        }
                    }
                },
                "502": {
                    "count": 2,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:31:55.841809-07:00",
                    "lastResponse": "2021-03-22T00:32:16.076095-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:16.076135-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076135-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.841851-07:00",
                            "lastResponse": "2021-03-22T00:31:55.841851-07:00"
                        }
                    }
                },
                "503": {
                    "count": 2,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:32:03.349027-07:00",
                    "lastResponse": "2021-03-22T00:32:32.028786-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.349062-07:00",
                            "lastResponse": "2021-03-22T00:32:03.349062-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:32.028811-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028811-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 20,
                    "firstResponse": "2021-03-22T00:31:53.365039-07:00",
                    "lastResponse": "2021-03-22T00:32:35.759003-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 16,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:54.140101-07:00",
                            "lastResponse": "2021-03-22T00:32:24.545957-07:00"
                        },
                        "418": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:59.338282-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692078-07:00"
                        },
                        "501": {
                            "count": 12,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.365039-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759003-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:55.84181-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076098-07:00"
                        },
                        "503": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.349029-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028787-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 9,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:53.365063-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692106-07:00"
                        },
                        "[201 300]": {
                            "count": 8,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.140121-07:00",
                            "lastResponse": "2021-03-22T00:32:26.176166-07:00"
                        },
                        "[301 500]": {
                            "count": 19,
                            "retries": 9,
                            "firstResponse": "2021-03-22T00:31:55.841841-07:00",
                            "lastResponse": "2021-03-22T00:32:35.75902-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:06.962988-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028803-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 8,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:53.365033-07:00",
                    "lastResponse": "2021-03-22T00:32:35.758999-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:06.962947-07:00",
                            "lastResponse": "2021-03-22T00:32:20.142005-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:26.229625-07:00",
                            "lastResponse": "2021-03-22T00:32:26.229625-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.365034-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:53.365064-07:00",
                            "lastResponse": "2021-03-22T00:32:11.951908-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:21.67439-07:00",
                            "lastResponse": "2021-03-22T00:32:21.67439-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:17.666381-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759022-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:06.962991-07:00",
                            "lastResponse": "2021-03-22T00:32:20.142029-07:00"
                        }
                    }
                },
                "2": {
                    "count": 6,
                    "retries": 12,
                    "firstResponse": "2021-03-22T00:31:55.841805-07:00",
                    "lastResponse": "2021-03-22T00:32:32.028781-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:04.897907-07:00",
                            "lastResponse": "2021-03-22T00:32:10.312408-07:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:55.841806-07:00",
                            "lastResponse": "2021-03-22T00:32:16.07609-07:00"
                        },
                        "503": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.349022-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028783-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.349058-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076129-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:55.841842-07:00",
                            "lastResponse": "2021-03-22T00:32:10.312431-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:32.028805-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028805-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 4,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:32:04.897909-07:00",
                    "lastResponse": "2021-03-22T00:32:32.028784-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:04.897909-07:00",
                            "lastResponse": "2021-03-22T00:32:04.897909-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:17.666352-07:00",
                            "lastResponse": "2021-03-22T00:32:17.666352-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:16.076093-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076093-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:32.028785-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028785-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:16.076132-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076132-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:04.897947-07:00",
                            "lastResponse": "2021-03-22T00:32:17.666386-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:32.028807-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028807-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 10,
                    "retries": 13,
                    "firstResponse": "2021-03-22T00:31:53.365036-07:00",
                    "lastResponse": "2021-03-22T00:32:35.759001-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:06.962951-07:00",
                            "lastResponse": "2021-03-22T00:32:20.142007-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:26.229627-07:00",
                            "lastResponse": "2021-03-22T00:32:26.229627-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.365036-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759001-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.841808-07:00",
                            "lastResponse": "2021-03-22T00:31:55.841808-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.349025-07:00",
                            "lastResponse": "2021-03-22T00:32:03.349025-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.365065-07:00",
                            "lastResponse": "2021-03-22T00:32:11.95191-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:21.674394-07:00",
                            "lastResponse": "2021-03-22T00:32:21.674394-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:55.841843-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759025-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:06.962994-07:00",
                            "lastResponse": "2021-03-22T00:32:20.142031-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 9,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:53.365061-07:00",
                    "lastResponse": "2021-03-22T00:32:32.692102-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:11.951906-07:00",
                            "lastResponse": "2021-03-22T00:32:24.545983-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:23.84754-07:00",
                            "lastResponse": "2021-03-22T00:32:32.692104-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:53.365062-07:00",
                            "lastResponse": "2021-03-22T00:32:22.311756-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:16.076123-07:00",
                            "lastResponse": "2021-03-22T00:32:16.076123-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.349054-07:00",
                            "lastResponse": "2021-03-22T00:32:03.349054-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 8,
                    "retries": 1,
                    "firstResponse": "2021-03-22T00:31:54.140119-07:00",
                    "lastResponse": "2021-03-22T00:32:26.176163-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:54.14012-07:00",
                            "lastResponse": "2021-03-22T00:32:14.318723-07:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:59.338304-07:00",
                            "lastResponse": "2021-03-22T00:32:20.871565-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:54.885622-07:00",
                            "lastResponse": "2021-03-22T00:32:26.176165-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 19,
                    "retries": 9,
                    "firstResponse": "2021-03-22T00:31:55.84184-07:00",
                    "lastResponse": "2021-03-22T00:32:35.759019-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 7,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:56.757413-07:00",
                            "lastResponse": "2021-03-22T00:32:23.206995-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:12.880564-07:00",
                            "lastResponse": "2021-03-22T00:32:26.229647-07:00"
                        },
                        "501": {
                            "count": 7,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.865313-07:00",
                            "lastResponse": "2021-03-22T00:32:35.759019-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:55.84184-07:00",
                            "lastResponse": "2021-03-22T00:31:55.84184-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 4,
                    "retries": 4,
                    "firstResponse": "2021-03-22T00:32:06.962983-07:00",
                    "lastResponse": "2021-03-22T00:32:32.028802-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:06.962984-07:00",
                            "lastResponse": "2021-03-22T00:32:20.142026-07:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:32.028803-07:00",
                            "lastResponse": "2021-03-22T00:32:32.028803-07:00"
                        }
                    }
                }
            }
        },
        "peer2_to_peer4": {
            "target": "peer2_to_peer4",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:51.378186-07:00",
            "lastResponse": "2021-03-22T00:32:34.273392-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 7,
                "418 I'm a teapot": 13,
                "501 Not Implemented": 19,
                "502 Bad Gateway": 1
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 18,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8084": {
                            "count": 40,
                            "retries": 18,
                            "firstResponse": "2021-03-22T00:31:51.378236-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273413-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 7,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.839372-07:00",
                            "lastResponse": "2021-03-22T00:32:23.809145-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:52.105322-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273412-07:00"
                        },
                        "501": {
                            "count": 19,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:51.378235-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311332-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727704-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727704-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8084": {
                            "200": {
                                "count": 7,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:53.839373-07:00",
                                "lastResponse": "2021-03-22T00:32:23.809146-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:52.105323-07:00",
                                "lastResponse": "2021-03-22T00:32:34.273413-07:00"
                            },
                            "501": {
                                "count": 19,
                                "retries": 8,
                                "firstResponse": "2021-03-22T00:31:51.378237-07:00",
                                "lastResponse": "2021-03-22T00:32:33.311333-07:00"
                            },
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:31:56.727705-07:00",
                                "lastResponse": "2021-03-22T00:31:56.727705-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.473071-07:00",
                    "lastResponse": "2021-03-22T00:46:09.476189-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 18,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 40,
                            "retries": 18,
                            "firstResponse": "2021-03-22T00:31:51.378228-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273409-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 7,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.839369-07:00",
                            "lastResponse": "2021-03-22T00:32:23.809143-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:52.10532-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273409-07:00"
                        },
                        "501": {
                            "count": 19,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:51.378227-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311326-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727697-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727697-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 7,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:53.83937-07:00",
                                "lastResponse": "2021-03-22T00:32:23.809143-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:52.105321-07:00",
                                "lastResponse": "2021-03-22T00:32:34.27341-07:00"
                            },
                            "501": {
                                "count": 19,
                                "retries": 8,
                                "firstResponse": "2021-03-22T00:31:51.378229-07:00",
                                "lastResponse": "2021-03-22T00:32:33.311327-07:00"
                            },
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:31:56.727699-07:00",
                                "lastResponse": "2021-03-22T00:31:56.727699-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.473065-07:00",
                    "lastResponse": "2021-03-22T00:46:09.476191-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 18,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer4": {
                            "count": 40,
                            "retries": 18,
                            "firstResponse": "2021-03-22T00:31:51.378223-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273404-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 7,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.839365-07:00",
                            "lastResponse": "2021-03-22T00:32:23.809136-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:52.105318-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273403-07:00"
                        },
                        "501": {
                            "count": 19,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:51.378222-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311323-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727693-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727693-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer4": {
                            "200": {
                                "count": 7,
                                "retries": 3,
                                "firstResponse": "2021-03-22T00:31:53.839368-07:00",
                                "lastResponse": "2021-03-22T00:32:23.809138-07:00"
                            },
                            "418": {
                                "count": 13,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:52.105319-07:00",
                                "lastResponse": "2021-03-22T00:32:34.273405-07:00"
                            },
                            "501": {
                                "count": 19,
                                "retries": 8,
                                "firstResponse": "2021-03-22T00:31:51.378224-07:00",
                                "lastResponse": "2021-03-22T00:32:33.311324-07:00"
                            },
                            "502": {
                                "count": 1,
                                "retries": 2,
                                "firstResponse": "2021-03-22T00:31:56.727694-07:00",
                                "lastResponse": "2021-03-22T00:31:56.727694-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.473069-07:00",
                    "lastResponse": "2021-03-22T00:46:09.476192-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 7,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:53.839362-07:00",
                    "lastResponse": "2021-03-22T00:32:23.80913-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:16.879419-07:00",
                            "lastResponse": "2021-03-22T00:32:16.879419-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:57.506534-07:00",
                            "lastResponse": "2021-03-22T00:32:23.80916-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:53.839381-07:00",
                            "lastResponse": "2021-03-22T00:31:53.839381-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:22.19385-07:00",
                            "lastResponse": "2021-03-22T00:32:22.19385-07:00"
                        }
                    }
                },
                "418": {
                    "count": 13,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:52.105314-07:00",
                    "lastResponse": "2021-03-22T00:32:34.273396-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:03.667985-07:00",
                            "lastResponse": "2021-03-22T00:32:03.667985-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:52.105332-07:00",
                            "lastResponse": "2021-03-22T00:32:15.284782-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:53.00952-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273444-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:05.551609-07:00",
                            "lastResponse": "2021-03-22T00:32:21.559437-07:00"
                        }
                    }
                },
                "501": {
                    "count": 19,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:51.378211-07:00",
                    "lastResponse": "2021-03-22T00:32:33.311316-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:08.079017-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311352-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:58.261474-07:00",
                            "lastResponse": "2021-03-22T00:32:24.872347-07:00"
                        },
                        "[301 500]": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:03.390774-07:00",
                            "lastResponse": "2021-03-22T00:32:30.405464-07:00"
                        },
                        "[501 1000]": {
                            "count": 5,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:51.378277-07:00",
                            "lastResponse": "2021-03-22T00:32:20.470867-07:00"
                        }
                    }
                },
                "502": {
                    "count": 1,
                    "retries": 2,
                    "firstResponse": "2021-03-22T00:31:56.727686-07:00",
                    "lastResponse": "2021-03-22T00:31:56.727686-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727724-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727724-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 18,
                    "firstResponse": "2021-03-22T00:31:51.378214-07:00",
                    "lastResponse": "2021-03-22T00:32:34.273399-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 7,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.839363-07:00",
                            "lastResponse": "2021-03-22T00:32:23.809133-07:00"
                        },
                        "418": {
                            "count": 13,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:52.105316-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273399-07:00"
                        },
                        "501": {
                            "count": 19,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:51.378215-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311318-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727689-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727689-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 6,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:56.727718-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311345-07:00"
                        },
                        "[201 300]": {
                            "count": 10,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:52.105331-07:00",
                            "lastResponse": "2021-03-22T00:32:24.872341-07:00"
                        },
                        "[301 500]": {
                            "count": 15,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.009518-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273428-07:00"
                        },
                        "[501 1000]": {
                            "count": 9,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:51.37825-07:00",
                            "lastResponse": "2021-03-22T00:32:22.193839-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 10,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:32:02.444505-07:00",
                    "lastResponse": "2021-03-22T00:32:33.31131-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:02.444506-07:00",
                            "lastResponse": "2021-03-22T00:32:22.193808-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:03.667944-07:00",
                            "lastResponse": "2021-03-22T00:32:15.284734-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:07.394261-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311312-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:03.667981-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311347-07:00"
                        },
                        "[201 300]": {
                            "count": 5,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:02.444529-07:00",
                            "lastResponse": "2021-03-22T00:32:24.872343-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:07.394285-07:00",
                            "lastResponse": "2021-03-22T00:32:07.394285-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:22.193842-07:00",
                            "lastResponse": "2021-03-22T00:32:22.193842-07:00"
                        }
                    }
                },
                "2": {
                    "count": 4,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:56.72768-07:00",
                    "lastResponse": "2021-03-22T00:32:30.405441-07:00",
                    "byStatusCodes": {
                        "418": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:19.457762-07:00",
                            "lastResponse": "2021-03-22T00:32:19.457762-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.828175-07:00",
                            "lastResponse": "2021-03-22T00:32:30.405442-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727681-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727681-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.72772-07:00",
                            "lastResponse": "2021-03-22T00:31:56.72772-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:30.405462-07:00",
                            "lastResponse": "2021-03-22T00:32:30.405462-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.828211-07:00",
                            "lastResponse": "2021-03-22T00:32:19.457798-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 5,
                    "retries": 7,
                    "firstResponse": "2021-03-22T00:31:56.727684-07:00",
                    "lastResponse": "2021-03-22T00:32:33.311314-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:02.444508-07:00",
                            "lastResponse": "2021-03-22T00:32:02.444508-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:00.828178-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311315-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727685-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727685-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:56.727722-07:00",
                            "lastResponse": "2021-03-22T00:32:33.31135-07:00"
                        },
                        "[201 300]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:02.44453-07:00",
                            "lastResponse": "2021-03-22T00:32:02.44453-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:07.394287-07:00",
                            "lastResponse": "2021-03-22T00:32:07.394287-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:00.828213-07:00",
                            "lastResponse": "2021-03-22T00:32:00.828213-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 9,
                    "retries": 11,
                    "firstResponse": "2021-03-22T00:32:03.667947-07:00",
                    "lastResponse": "2021-03-22T00:32:30.405443-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:08.865963-07:00",
                            "lastResponse": "2021-03-22T00:32:22.193811-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:03.667948-07:00",
                            "lastResponse": "2021-03-22T00:32:19.457765-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:11.970637-07:00",
                            "lastResponse": "2021-03-22T00:32:30.405444-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.667983-07:00",
                            "lastResponse": "2021-03-22T00:32:11.970674-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:08.866-07:00",
                            "lastResponse": "2021-03-22T00:32:24.872345-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:30.405463-07:00",
                            "lastResponse": "2021-03-22T00:32:30.405463-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:19.457801-07:00",
                            "lastResponse": "2021-03-22T00:32:22.193845-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 6,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:56.727716-07:00",
                    "lastResponse": "2021-03-22T00:32:33.311342-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:16.879417-07:00",
                            "lastResponse": "2021-03-22T00:32:16.879417-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:03.667977-07:00",
                            "lastResponse": "2021-03-22T00:32:03.667977-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:08.079015-07:00",
                            "lastResponse": "2021-03-22T00:32:33.311344-07:00"
                        },
                        "502": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.727717-07:00",
                            "lastResponse": "2021-03-22T00:31:56.727717-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 10,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:52.105329-07:00",
                    "lastResponse": "2021-03-22T00:32:24.872339-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:57.506531-07:00",
                            "lastResponse": "2021-03-22T00:32:23.809156-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:52.10533-07:00",
                            "lastResponse": "2021-03-22T00:32:15.284773-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:58.26147-07:00",
                            "lastResponse": "2021-03-22T00:32:24.87234-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 15,
                    "retries": 3,
                    "firstResponse": "2021-03-22T00:31:53.009516-07:00",
                    "lastResponse": "2021-03-22T00:32:34.273426-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:53.839379-07:00",
                            "lastResponse": "2021-03-22T00:31:53.839379-07:00"
                        },
                        "418": {
                            "count": 5,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:31:53.009517-07:00",
                            "lastResponse": "2021-03-22T00:32:34.273427-07:00"
                        },
                        "501": {
                            "count": 9,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:03.390765-07:00",
                            "lastResponse": "2021-03-22T00:32:30.40546-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 9,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:51.378248-07:00",
                    "lastResponse": "2021-03-22T00:32:22.193836-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:22.193837-07:00",
                            "lastResponse": "2021-03-22T00:32:22.193837-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:05.551605-07:00",
                            "lastResponse": "2021-03-22T00:32:21.559434-07:00"
                        },
                        "501": {
                            "count": 5,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:51.378249-07:00",
                            "lastResponse": "2021-03-22T00:32:20.470864-07:00"
                        }
                    }
                }
            }
        },
        "peer2_to_peer5": {
            "target": "peer2_to_peer5",
            "invocationCounts": 40,
            "firstResponse": "2021-03-22T00:31:53.546589-07:00",
            "lastResponse": "2021-03-22T00:32:35.841207-07:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 14,
                "418 I'm a teapot": 16,
                "501 Not Implemented": 10
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 40,
                    "retries": 21,
                    "header": "goto-host",
                    "countsByValues": {
                        "Localhost.local@1.1.1.1:8085": {
                            "count": 40,
                            "retries": 21,
                            "firstResponse": "2021-03-22T00:31:53.546679-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841225-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.837445-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444355-07:00"
                        },
                        "418": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.273199-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841225-07:00"
                        },
                        "501": {
                            "count": 10,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.546679-07:00",
                            "lastResponse": "2021-03-22T00:32:35.163535-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Localhost.local@1.1.1.1:8085": {
                            "200": {
                                "count": 14,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:31:57.837447-07:00",
                                "lastResponse": "2021-03-22T00:32:34.444356-07:00"
                            },
                            "418": {
                                "count": 16,
                                "retries": 10,
                                "firstResponse": "2021-03-22T00:31:56.2732-07:00",
                                "lastResponse": "2021-03-22T00:32:35.841226-07:00"
                            },
                            "501": {
                                "count": 10,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:53.54668-07:00",
                                "lastResponse": "2021-03-22T00:32:35.163536-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.473815-07:00",
                    "lastResponse": "2021-03-22T00:46:09.47511-07:00"
                },
                "request-from-goto": {
                    "count": 40,
                    "retries": 21,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 40,
                            "retries": 21,
                            "firstResponse": "2021-03-22T00:31:53.546668-07:00",
                            "lastResponse": "2021-03-22T00:32:35.84122-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.837439-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444351-07:00"
                        },
                        "418": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.273194-07:00",
                            "lastResponse": "2021-03-22T00:32:35.84122-07:00"
                        },
                        "501": {
                            "count": 10,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.546667-07:00",
                            "lastResponse": "2021-03-22T00:32:35.16353-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 14,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:31:57.83744-07:00",
                                "lastResponse": "2021-03-22T00:32:34.444352-07:00"
                            },
                            "418": {
                                "count": 16,
                                "retries": 10,
                                "firstResponse": "2021-03-22T00:31:56.273195-07:00",
                                "lastResponse": "2021-03-22T00:32:35.841221-07:00"
                            },
                            "501": {
                                "count": 10,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:53.546669-07:00",
                                "lastResponse": "2021-03-22T00:32:35.163531-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.47382-07:00",
                    "lastResponse": "2021-03-22T00:46:09.475112-07:00"
                },
                "via-goto": {
                    "count": 40,
                    "retries": 21,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer5": {
                            "count": 40,
                            "retries": 21,
                            "firstResponse": "2021-03-22T00:31:53.546661-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841216-07:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.837436-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444346-07:00"
                        },
                        "418": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.27319-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841215-07:00"
                        },
                        "501": {
                            "count": 10,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.54666-07:00",
                            "lastResponse": "2021-03-22T00:32:35.163525-07:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer5": {
                            "200": {
                                "count": 14,
                                "retries": 6,
                                "firstResponse": "2021-03-22T00:31:57.837437-07:00",
                                "lastResponse": "2021-03-22T00:32:34.444348-07:00"
                            },
                            "418": {
                                "count": 16,
                                "retries": 10,
                                "firstResponse": "2021-03-22T00:31:56.273192-07:00",
                                "lastResponse": "2021-03-22T00:32:35.841216-07:00"
                            },
                            "501": {
                                "count": 10,
                                "retries": 5,
                                "firstResponse": "2021-03-22T00:31:53.546662-07:00",
                                "lastResponse": "2021-03-22T00:32:35.163526-07:00"
                            }
                        }
                    },
                    "firstResponse": "2021-03-22T00:46:09.473824-07:00",
                    "lastResponse": "2021-03-22T00:46:09.475114-07:00"
                }
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 14,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:57.837431-07:00",
                    "lastResponse": "2021-03-22T00:32:34.444341-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:57.837458-07:00",
                            "lastResponse": "2021-03-22T00:32:27.763705-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:06.485475-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444376-07:00"
                        },
                        "[301 500]": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:05.777817-07:00",
                            "lastResponse": "2021-03-22T00:32:31.378171-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.981014-07:00",
                            "lastResponse": "2021-03-22T00:32:20.90844-07:00"
                        }
                    }
                },
                "418": {
                    "count": 16,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:31:56.273183-07:00",
                    "lastResponse": "2021-03-22T00:32:35.84121-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:35.841241-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841241-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:58.541588-07:00",
                            "lastResponse": "2021-03-22T00:32:01.777909-07:00"
                        },
                        "[301 500]": {
                            "count": 10,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.273217-07:00",
                            "lastResponse": "2021-03-22T00:32:28.264625-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:17.085445-07:00",
                            "lastResponse": "2021-03-22T00:32:17.085445-07:00"
                        }
                    }
                },
                "501": {
                    "count": 10,
                    "retries": 5,
                    "firstResponse": "2021-03-22T00:31:53.546621-07:00",
                    "lastResponse": "2021-03-22T00:32:35.163519-07:00",
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:07.805613-07:00",
                            "lastResponse": "2021-03-22T00:32:07.805613-07:00"
                        },
                        "[201 300]": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:25.121628-07:00",
                            "lastResponse": "2021-03-22T00:32:35.163548-07:00"
                        },
                        "[301 500]": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:53.546701-07:00",
                            "lastResponse": "2021-03-22T00:32:28.570364-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:18.915585-07:00",
                            "lastResponse": "2021-03-22T00:32:22.024025-07:00"
                        }
                    }
                }
            },
            "countsByURIs": {
                "/status/200,418,501,502,503/delay/100ms-600ms": {
                    "count": 40,
                    "retries": 21,
                    "firstResponse": "2021-03-22T00:31:53.546652-07:00",
                    "lastResponse": "2021-03-22T00:32:35.841212-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 14,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:57.837433-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444343-07:00"
                        },
                        "418": {
                            "count": 16,
                            "retries": 10,
                            "firstResponse": "2021-03-22T00:31:56.273185-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841213-07:00"
                        },
                        "501": {
                            "count": 10,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.546653-07:00",
                            "lastResponse": "2021-03-22T00:32:35.163522-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 6,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:57.837455-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841237-07:00"
                        },
                        "[201 300]": {
                            "count": 10,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:58.541586-07:00",
                            "lastResponse": "2021-03-22T00:32:35.163546-07:00"
                        },
                        "[301 500]": {
                            "count": 17,
                            "retries": 8,
                            "firstResponse": "2021-03-22T00:31:53.546692-07:00",
                            "lastResponse": "2021-03-22T00:32:31.378165-07:00"
                        },
                        "[501 1000]": {
                            "count": 7,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.981011-07:00",
                            "lastResponse": "2021-03-22T00:32:22.024019-07:00"
                        }
                    }
                }
            },
            "countsByRetries": {
                "1": {
                    "count": 15,
                    "retries": 15,
                    "firstResponse": "2021-03-22T00:31:53.546614-07:00",
                    "lastResponse": "2021-03-22T00:32:34.444336-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 6,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:03.980994-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444337-07:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:32:01.77789-07:00",
                            "lastResponse": "2021-03-22T00:32:28.264591-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:53.546616-07:00",
                            "lastResponse": "2021-03-22T00:32:25.121599-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:27.763699-07:00",
                            "lastResponse": "2021-03-22T00:32:27.763699-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:01.777907-07:00",
                            "lastResponse": "2021-03-22T00:32:34.44437-07:00"
                        },
                        "[301 500]": {
                            "count": 6,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:53.546695-07:00",
                            "lastResponse": "2021-03-22T00:32:31.378167-07:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.981012-07:00",
                            "lastResponse": "2021-03-22T00:32:22.024021-07:00"
                        }
                    }
                },
                "2": {
                    "count": 3,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:56.273178-07:00",
                    "lastResponse": "2021-03-22T00:32:18.915557-07:00",
                    "byStatusCodes": {
                        "418": {
                            "count": 2,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:56.273179-07:00",
                            "lastResponse": "2021-03-22T00:32:00.011176-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:18.915558-07:00",
                            "lastResponse": "2021-03-22T00:32:18.915558-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:00.011241-07:00",
                            "lastResponse": "2021-03-22T00:32:00.011241-07:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:31:56.273211-07:00",
                            "lastResponse": "2021-03-22T00:31:56.273211-07:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:18.915582-07:00",
                            "lastResponse": "2021-03-22T00:32:18.915582-07:00"
                        }
                    }
                }
            },
            "countsByRetryReasons": {
                "502 Bad Gateway": {
                    "count": 9,
                    "retries": 11,
                    "firstResponse": "2021-03-22T00:31:56.273181-07:00",
                    "lastResponse": "2021-03-22T00:32:27.763664-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:07.114397-07:00",
                            "lastResponse": "2021-03-22T00:32:27.763665-07:00"
                        },
                        "418": {
                            "count": 5,
                            "retries": 7,
                            "firstResponse": "2021-03-22T00:31:56.273181-07:00",
                            "lastResponse": "2021-03-22T00:32:17.085407-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:25.121601-07:00",
                            "lastResponse": "2021-03-22T00:32:25.121601-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[101 200]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:27.763702-07:00",
                            "lastResponse": "2021-03-22T00:32:27.763702-07:00"
                        },
                        "[201 300]": {
                            "count": 4,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:32:00.011243-07:00",
                            "lastResponse": "2021-03-22T00:32:25.121627-07:00"
                        },
                        "[301 500]": {
                            "count": 2,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:56.273216-07:00",
                            "lastResponse": "2021-03-22T00:32:12.89559-07:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:09.698747-07:00",
                            "lastResponse": "2021-03-22T00:32:17.085442-07:00"
                        }
                    }
                },
                "503 Service Unavailable": {
                    "count": 9,
                    "retries": 10,
                    "firstResponse": "2021-03-22T00:31:53.546619-07:00",
                    "lastResponse": "2021-03-22T00:32:34.444339-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:03.980995-07:00",
                            "lastResponse": "2021-03-22T00:32:34.44434-07:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:04.889428-07:00",
                            "lastResponse": "2021-03-22T00:32:28.264594-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:31:53.546619-07:00",
                            "lastResponse": "2021-03-22T00:32:22.023988-07:00"
                        }
                    },
                    "byTimeBuckets": {
                        "[201 300]": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:34.444373-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444373-07:00"
                        },
                        "[301 500]": {
                            "count": 5,
                            "retries": 5,
                            "firstResponse": "2021-03-22T00:31:53.546698-07:00",
                            "lastResponse": "2021-03-22T00:32:31.378169-07:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 4,
                            "firstResponse": "2021-03-22T00:32:03.981013-07:00",
                            "lastResponse": "2021-03-22T00:32:22.024023-07:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[101 200]": {
                    "count": 6,
                    "retries": 1,
                    "firstResponse": "2021-03-22T00:31:57.837453-07:00",
                    "lastResponse": "2021-03-22T00:32:35.841235-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:57.837454-07:00",
                            "lastResponse": "2021-03-22T00:32:27.763695-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:35.841236-07:00",
                            "lastResponse": "2021-03-22T00:32:35.841236-07:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-22T00:32:07.805608-07:00",
                            "lastResponse": "2021-03-22T00:32:07.805608-07:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 10,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:31:58.541584-07:00",
                    "lastResponse": "2021-03-22T00:32:35.163544-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:06.485471-07:00",
                            "lastResponse": "2021-03-22T00:32:34.444367-07:00"
                        },
                        "418": {
                            "count": 4,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:31:58.541586-07:00",
                            "lastResponse": "2021-03-22T00:32:01.777905-07:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:25.121623-07:00",
                            "lastResponse": "2021-03-22T00:32:35.163545-07:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 17,
                    "retries": 8,
                    "firstResponse": "2021-03-22T00:31:53.54669-07:00",
                    "lastResponse": "2021-03-22T00:32:31.378162-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:05.777812-07:00",
                            "lastResponse": "2021-03-22T00:32:31.378163-07:00"
                        },
                        "418": {
                            "count": 10,
                            "retries": 6,
                            "firstResponse": "2021-03-22T00:31:56.273209-07:00",
                            "lastResponse": "2021-03-22T00:32:28.264619-07:00"
                        },
                        "501": {
                            "count": 4,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:31:53.546691-07:00",
                            "lastResponse": "2021-03-22T00:32:28.570361-07:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 7,
                    "retries": 6,
                    "firstResponse": "2021-03-22T00:32:03.981009-07:00",
                    "lastResponse": "2021-03-22T00:32:22.024016-07:00",
                    "byStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 2,
                            "firstResponse": "2021-03-22T00:32:03.98101-07:00",
                            "lastResponse": "2021-03-22T00:32:20.908437-07:00"
                        },
                        "418": {
                            "count": 1,
                            "retries": 1,
                            "firstResponse": "2021-03-22T00:32:17.085435-07:00",
                            "lastResponse": "2021-03-22T00:32:17.085435-07:00"
                        },
                        "501": {
                            "count": 3,
                            "retries": 3,
                            "firstResponse": "2021-03-22T00:32:18.91558-07:00",
                            "lastResponse": "2021-03-22T00:32:22.024018-07:00"
                        }
                    }
                }
            }
        }
    }
}

```

</p>
</details>
