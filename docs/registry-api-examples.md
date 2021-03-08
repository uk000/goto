
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
<summary>Targets Summary Results Example</summary>
<p>

```json
$ curl -s localhost:8080/registry/peers/lockers/targets/results

{
  "peer1": {
    "targetInvocationCounts": {
      "t1": 14,
      "t2": 14
    },
    "targetFirstResponses": {
      "t1": "2020-08-20T14:29:36.969395-07:00",
      "t2": "2020-08-20T14:29:36.987895-07:00"
    },
    "targetLastResponses": {
      "t1": "2020-08-20T14:31:05.068302-07:00",
      "t2": "2020-08-20T14:31:05.08426-07:00"
    },
    "countsByStatusCodes": {
      "200": 12,
      "400": 2,
      "418": 10,
      "502": 2,
      "503": 2
    },
    "countsByHeaders": {
      "goto-host": 28,
      "request-from-goto": 28,
      "request-from-goto-host": 28,
      "via-goto": 28
    },
    "countsByHeaderValues": {
      "goto-host": {
        "pod.local@1.0.0.1:8082": 14,
        "pod.local@1.0.0.1:9092": 14
      },
      "request-from-goto": {
        "peer1": 28
      },
      "request-from-goto-host": {
        "pod.local@1.0.0.1:8081": 28
      },
      "via-goto": {
        "peer2": 28
      }
    },
    "countsByTargetStatusCodes": {
      "t1": {
        "200": 2,
        "400": 1,
        "418": 10,
        "502": 1
      },
      "t2": {
        "200": 10,
        "400": 1,
        "502": 1,
        "503": 2
      }
    },
    "countsByTargetHeaders": {
      "t1": {
        "goto-host": 14,
        "request-from-goto": 14,
        "request-from-goto-host": 14,
        "via-goto": 14
      },
      "t2": {
        "goto-host": 14,
        "request-from-goto": 14,
        "request-from-goto-host": 14,
        "via-goto": 14
      }
    },
    "countsByTargetHeaderValues": {
      "t1": {
        "goto-host": {
          "pod.local@1.0.0.1:8082": 6,
          "pod.local@1.0.0.1:9092": 8
        },
        "request-from-goto": {
          "peer1": 14
        },
        "request-from-goto-host": {
          "pod.local@1.0.0.1:8081": 14
        },
        "via-goto": {
          "peer2": 14
        }
      },
      "t2": {
        "goto-host": {
          "pod.local@1.0.0.1:8082": 8,
          "pod.local@1.0.0.1:9092": 6
        },
        "request-from-goto": {
          "peer1": 14
        },
        "request-from-goto-host": {
          "pod.local@1.0.0.1:8081": 14
        },
        "via-goto": {
          "peer2": 14
        }
      }
    },
    "headerCounts": {
      "goto-host": {
        "Header": "goto-host",
        "count": {
          "count": 28,
          "retries": 3,
          "firstResponse": "2020-08-20T14:29:36.969404-07:00",
          "lastResponse": "2020-08-20T14:31:05.084277-07:00"
        },
        "countsByValues": {
          "pod.local@1.0.0.1:8082": {
            "count": 14,
            "retries": 1,
            "firstResponse": "2020-08-20T14:29:36.987905-07:00",
            "lastResponse": "2020-08-20T14:31:05.068314-07:00"
          },
          "pod.local@1.0.0.1:9092": {
            "count": 14,
            "retries": 2,
            "firstResponse": "2020-08-20T14:29:36.969405-07:00",
            "lastResponse": "2020-08-20T14:31:05.084278-07:00"
          }
        },
        "countsByStatusCodes": {
          "200": {
            "count": 12,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:36.987905-07:00",
            "lastResponse": "2020-08-20T14:31:05.084277-07:00"
          },
          "400": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.32723-07:00",
            "lastResponse": "2020-08-20T14:31:04.083364-07:00"
          },
          "418": {
            "count": 10,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969404-07:00",
            "lastResponse": "2020-08-20T14:31:05.068313-07:00"
          },
          "502": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.348091-07:00",
            "lastResponse": "2020-08-20T14:31:04.066585-07:00"
          },
          "503": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:45.858562-07:00",
            "lastResponse": "2020-08-20T14:30:49.907579-07:00"
          }
        },
        "countsByValuesStatusCodes": {
          "pod.local@1.0.0.1:8082": {
            "200": {
              "count": 6,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:36.987906-07:00",
              "lastResponse": "2020-08-20T14:30:16.81373-07:00"
            },
            "418": {
              "count": 5,
              "retries": 1,
              "firstResponse": "2020-08-20T14:30:32.028522-07:00",
              "lastResponse": "2020-08-20T14:31:05.068314-07:00"
            },
            "502": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.348092-07:00",
              "lastResponse": "2020-08-20T14:31:04.066586-07:00"
            },
            "503": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:45.858563-07:00",
              "lastResponse": "2020-08-20T14:29:45.858563-07:00"
            }
          },
          "pod.local@1.0.0.1:9092": {
            "200": {
              "count": 6,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:15.79568-07:00",
              "lastResponse": "2020-08-20T14:31:05.084278-07:00"
            },
            "400": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.32723-07:00",
              "lastResponse": "2020-08-20T14:31:04.083366-07:00"
            },
            "418": {
              "count": 5,
              "retries": 2,
              "firstResponse": "2020-08-20T14:29:36.969405-07:00",
              "lastResponse": "2020-08-20T14:30:03.332312-07:00"
            },
            "503": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:49.907581-07:00",
              "lastResponse": "2020-08-20T14:30:49.907581-07:00"
            }
          }
        },
        "crossHeaders": {
          "request-from-goto-host": {
            "Header": "request-from-goto-host",
            "count": {
              "count": 28,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969409-07:00",
              "lastResponse": "2020-08-20T14:31:05.084279-07:00"
            },
            "countsByValues": {
              "pod.local@1.0.0.1:8081": {
                "count": 28,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.96941-07:00",
                "lastResponse": "2020-08-20T14:31:05.08428-07:00"
              }
            },
            "countsByStatusCodes": {
              "200": {
                "count": 12,
                "retries": 0,
                "firstResponse": "2020-08-20T14:29:36.987917-07:00",
                "lastResponse": "2020-08-20T14:31:05.08428-07:00"
              },
              "400": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.327239-07:00",
                "lastResponse": "2020-08-20T14:31:04.083377-07:00"
              },
              "418": {
                "count": 10,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969409-07:00",
                "lastResponse": "2020-08-20T14:31:05.068316-07:00"
              },
              "502": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.348102-07:00",
                "lastResponse": "2020-08-20T14:31:04.066594-07:00"
              },
              "503": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:29:45.858565-07:00",
                "lastResponse": "2020-08-20T14:30:49.907593-07:00"
              }
            },
            "countsByValuesStatusCodes": {
              "pod.local@1.0.0.1:8081": {
                "200": {
                  "count": 12,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:36.987918-07:00",
                  "lastResponse": "2020-08-20T14:31:05.08428-07:00"
                },
                "400": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.32724-07:00",
                  "lastResponse": "2020-08-20T14:31:04.083378-07:00"
                },
                "418": {
                  "count": 10,
                  "retries": 3,
                  "firstResponse": "2020-08-20T14:29:36.96941-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068317-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.348102-07:00",
                  "lastResponse": "2020-08-20T14:31:04.066595-07:00"
                },
                "503": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:45.858566-07:00",
                  "lastResponse": "2020-08-20T14:30:49.907594-07:00"
                }
              }
            },
            "crossHeaders": {},
            "crossHeadersByValues": {},
            "firstResponse": "2020-08-20T14:31:06.784698-07:00",
            "lastResponse": "2020-08-20T14:31:06.785334-07:00"
          }
        },
        "crossHeadersByValues": {
          "pod.local@1.0.0.1:8082": {
            "request-from-goto-host": {
              "Header": "request-from-goto-host",
              "count": {
                "count": 14,
                "retries": 1,
                "firstResponse": "2020-08-20T14:29:36.987921-07:00",
                "lastResponse": "2020-08-20T14:31:05.068318-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8081": {
                  "count": 14,
                  "retries": 1,
                  "firstResponse": "2020-08-20T14:29:36.987922-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068319-07:00"
                }
              },
              "countsByStatusCodes": {
                "200": {
                  "count": 6,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:36.987922-07:00",
                  "lastResponse": "2020-08-20T14:30:16.813733-07:00"
                },
                "418": {
                  "count": 5,
                  "retries": 1,
                  "firstResponse": "2020-08-20T14:30:32.028527-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068319-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.348103-07:00",
                  "lastResponse": "2020-08-20T14:31:04.066596-07:00"
                },
                "503": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:45.858567-07:00",
                  "lastResponse": "2020-08-20T14:29:45.858567-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8081": {
                  "200": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:29:36.987922-07:00",
                    "lastResponse": "2020-08-20T14:30:16.813734-07:00"
                  },
                  "418": {
                    "count": 5,
                    "retries": 1,
                    "firstResponse": "2020-08-20T14:30:32.028528-07:00",
                    "lastResponse": "2020-08-20T14:31:05.06832-07:00"
                  },
                  "502": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:02.348104-07:00",
                    "lastResponse": "2020-08-20T14:31:04.066597-07:00"
                  },
                  "503": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:29:45.858568-07:00",
                    "lastResponse": "2020-08-20T14:29:45.858568-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:31:06.784789-07:00",
              "lastResponse": "2020-08-20T14:31:06.785385-07:00"
            }
          },
          "pod.local@1.0.0.1:9092": {
            "request-from-goto-host": {
              "Header": "request-from-goto-host",
              "count": {
                "count": 14,
                "retries": 2,
                "firstResponse": "2020-08-20T14:29:36.969411-07:00",
                "lastResponse": "2020-08-20T14:31:05.084281-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8081": {
                  "count": 14,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969412-07:00",
                  "lastResponse": "2020-08-20T14:31:05.084281-07:00"
                }
              },
              "countsByStatusCodes": {
                "200": {
                  "count": 6,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:15.795684-07:00",
                  "lastResponse": "2020-08-20T14:31:05.084281-07:00"
                },
                "400": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.327241-07:00",
                  "lastResponse": "2020-08-20T14:31:04.08338-07:00"
                },
                "418": {
                  "count": 5,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969412-07:00",
                  "lastResponse": "2020-08-20T14:30:03.332315-07:00"
                },
                "503": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:49.907596-07:00",
                  "lastResponse": "2020-08-20T14:30:49.907596-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8081": {
                  "200": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:15.795685-07:00",
                    "lastResponse": "2020-08-20T14:31:05.084282-07:00"
                  },
                  "400": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:02.327242-07:00",
                    "lastResponse": "2020-08-20T14:31:04.083381-07:00"
                  },
                  "418": {
                    "count": 5,
                    "retries": 2,
                    "firstResponse": "2020-08-20T14:29:36.969412-07:00",
                    "lastResponse": "2020-08-20T14:30:03.332315-07:00"
                  },
                  "503": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:49.907597-07:00",
                    "lastResponse": "2020-08-20T14:30:49.907597-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:31:06.784846-07:00",
              "lastResponse": "2020-08-20T14:31:06.785418-07:00"
            }
          }
        },
        "firstResponse": "2020-08-20T14:31:06.784664-07:00",
        "lastResponse": "2020-08-20T14:31:06.785251-07:00"
      },
      "request-from-goto": {
        "Header": "request-from-goto",
        "count": {
          "count": 28,
          "retries": 3,
          "firstResponse": "2020-08-20T14:29:36.969402-07:00",
          "lastResponse": "2020-08-20T14:31:05.084275-07:00"
        },
        "countsByValues": {
          "peer1": {
            "count": 28,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969402-07:00",
            "lastResponse": "2020-08-20T14:31:05.084276-07:00"
          }
        },
        "countsByStatusCodes": {
          "200": {
            "count": 12,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:36.987902-07:00",
            "lastResponse": "2020-08-20T14:31:05.084275-07:00"
          },
          "400": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.327227-07:00",
            "lastResponse": "2020-08-20T14:31:04.083361-07:00"
          },
          "418": {
            "count": 10,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969402-07:00",
            "lastResponse": "2020-08-20T14:31:05.06831-07:00"
          },
          "502": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.348089-07:00",
            "lastResponse": "2020-08-20T14:31:04.066582-07:00"
          },
          "503": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:45.858559-07:00",
            "lastResponse": "2020-08-20T14:30:49.907575-07:00"
          }
        },
        "countsByValuesStatusCodes": {
          "peer1": {
            "200": {
              "count": 12,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:36.987903-07:00",
              "lastResponse": "2020-08-20T14:31:05.084276-07:00"
            },
            "400": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.327228-07:00",
              "lastResponse": "2020-08-20T14:31:04.083363-07:00"
            },
            "418": {
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969403-07:00",
              "lastResponse": "2020-08-20T14:31:05.068311-07:00"
            },
            "502": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.34809-07:00",
              "lastResponse": "2020-08-20T14:31:04.066582-07:00"
            },
            "503": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:45.85856-07:00",
              "lastResponse": "2020-08-20T14:30:49.907577-07:00"
            }
          }
        },
        "crossHeaders": {},
        "crossHeadersByValues": {},
        "firstResponse": "2020-08-20T14:31:06.784906-07:00",
        "lastResponse": "2020-08-20T14:31:06.785524-07:00"
      },
      "request-from-goto-host": {
        "Header": "request-from-goto-host",
        "count": {
          "count": 28,
          "retries": 3,
          "firstResponse": "2020-08-20T14:29:36.969414-07:00",
          "lastResponse": "2020-08-20T14:31:05.084282-07:00"
        },
        "countsByValues": {
          "pod.local@1.0.0.1:8081": {
            "count": 28,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969414-07:00",
            "lastResponse": "2020-08-20T14:31:05.084283-07:00"
          }
        },
        "countsByStatusCodes": {
          "200": {
            "count": 12,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:36.987924-07:00",
            "lastResponse": "2020-08-20T14:31:05.084283-07:00"
          },
          "400": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.327243-07:00",
            "lastResponse": "2020-08-20T14:31:04.083385-07:00"
          },
          "418": {
            "count": 10,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969414-07:00",
            "lastResponse": "2020-08-20T14:31:05.068388-07:00"
          },
          "502": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.348106-07:00",
            "lastResponse": "2020-08-20T14:31:04.066599-07:00"
          },
          "503": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:45.85857-07:00",
            "lastResponse": "2020-08-20T14:30:49.9076-07:00"
          }
        },
        "countsByValuesStatusCodes": {
          "pod.local@1.0.0.1:8081": {
            "200": {
              "count": 12,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:36.987925-07:00",
              "lastResponse": "2020-08-20T14:31:05.084283-07:00"
            },
            "400": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.327244-07:00",
              "lastResponse": "2020-08-20T14:31:04.083386-07:00"
            },
            "418": {
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969415-07:00",
              "lastResponse": "2020-08-20T14:31:05.068389-07:00"
            },
            "502": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.348107-07:00",
              "lastResponse": "2020-08-20T14:31:04.0666-07:00"
            },
            "503": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:45.858571-07:00",
              "lastResponse": "2020-08-20T14:30:49.907601-07:00"
            }
          }
        },
        "crossHeaders": {
          "goto-host": {
            "Header": "goto-host",
            "count": {
              "count": 28,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969416-07:00",
              "lastResponse": "2020-08-20T14:31:05.084284-07:00"
            },
            "countsByValues": {
              "pod.local@1.0.0.1:8082": {
                "count": 14,
                "retries": 1,
                "firstResponse": "2020-08-20T14:29:36.987927-07:00",
                "lastResponse": "2020-08-20T14:31:05.068391-07:00"
              },
              "pod.local@1.0.0.1:9092": {
                "count": 14,
                "retries": 2,
                "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                "lastResponse": "2020-08-20T14:31:05.084284-07:00"
              }
            },
            "countsByStatusCodes": {
              "200": {
                "count": 12,
                "retries": 0,
                "firstResponse": "2020-08-20T14:29:36.987926-07:00",
                "lastResponse": "2020-08-20T14:31:05.084284-07:00"
              },
              "400": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.327245-07:00",
                "lastResponse": "2020-08-20T14:31:04.083387-07:00"
              },
              "418": {
                "count": 10,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                "lastResponse": "2020-08-20T14:31:05.068391-07:00"
              },
              "502": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.348108-07:00",
                "lastResponse": "2020-08-20T14:31:04.066601-07:00"
              },
              "503": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:29:45.858572-07:00",
                "lastResponse": "2020-08-20T14:30:49.907602-07:00"
              }
            },
            "countsByValuesStatusCodes": {
              "pod.local@1.0.0.1:8082": {
                "200": {
                  "count": 6,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:36.987927-07:00",
                  "lastResponse": "2020-08-20T14:30:16.813737-07:00"
                },
                "418": {
                  "count": 5,
                  "retries": 1,
                  "firstResponse": "2020-08-20T14:30:32.028533-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068392-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.348108-07:00",
                  "lastResponse": "2020-08-20T14:31:04.066602-07:00"
                },
                "503": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:45.858573-07:00",
                  "lastResponse": "2020-08-20T14:29:45.858573-07:00"
                }
              },
              "pod.local@1.0.0.1:9092": {
                "200": {
                  "count": 6,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:15.795688-07:00",
                  "lastResponse": "2020-08-20T14:31:05.084284-07:00"
                },
                "400": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.327245-07:00",
                  "lastResponse": "2020-08-20T14:31:04.083388-07:00"
                },
                "418": {
                  "count": 5,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                  "lastResponse": "2020-08-20T14:30:03.332319-07:00"
                },
                "503": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:49.907603-07:00",
                  "lastResponse": "2020-08-20T14:30:49.907603-07:00"
                }
              }
            },
            "crossHeaders": {},
            "crossHeadersByValues": {},
            "firstResponse": "2020-08-20T14:31:06.784568-07:00",
            "lastResponse": "2020-08-20T14:31:06.785578-07:00"
          }
        },
        "crossHeadersByValues": {
          "pod.local@1.0.0.1:8081": {
            "goto-host": {
              "Header": "goto-host",
              "count": {
                "count": 28,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                "lastResponse": "2020-08-20T14:31:05.084285-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8082": {
                  "count": 14,
                  "retries": 1,
                  "firstResponse": "2020-08-20T14:29:36.987928-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068393-07:00"
                },
                "pod.local@1.0.0.1:9092": {
                  "count": 14,
                  "retries": 2,
                  "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                  "lastResponse": "2020-08-20T14:31:05.084285-07:00"
                }
              },
              "countsByStatusCodes": {
                "200": {
                  "count": 12,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:36.987928-07:00",
                  "lastResponse": "2020-08-20T14:31:05.084285-07:00"
                },
                "400": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.327246-07:00",
                  "lastResponse": "2020-08-20T14:31:04.083389-07:00"
                },
                "418": {
                  "count": 10,
                  "retries": 3,
                  "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068393-07:00"
                },
                "502": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:30:02.348109-07:00",
                  "lastResponse": "2020-08-20T14:31:04.066603-07:00"
                },
                "503": {
                  "count": 2,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:29:45.858574-07:00",
                  "lastResponse": "2020-08-20T14:30:49.907604-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8082": {
                  "200": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:29:36.987929-07:00",
                    "lastResponse": "2020-08-20T14:30:16.813738-07:00"
                  },
                  "418": {
                    "count": 5,
                    "retries": 1,
                    "firstResponse": "2020-08-20T14:30:32.028535-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068394-07:00"
                  },
                  "502": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:02.348109-07:00",
                    "lastResponse": "2020-08-20T14:31:04.066603-07:00"
                  },
                  "503": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:29:45.858575-07:00",
                    "lastResponse": "2020-08-20T14:29:45.858575-07:00"
                  }
                },
                "pod.local@1.0.0.1:9092": {
                  "200": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:15.795689-07:00",
                    "lastResponse": "2020-08-20T14:31:05.084285-07:00"
                  },
                  "400": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:02.327246-07:00",
                    "lastResponse": "2020-08-20T14:31:04.083389-07:00"
                  },
                  "418": {
                    "count": 5,
                    "retries": 2,
                    "firstResponse": "2020-08-20T14:29:36.969419-07:00",
                    "lastResponse": "2020-08-20T14:30:03.33232-07:00"
                  },
                  "503": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:30:49.907605-07:00",
                    "lastResponse": "2020-08-20T14:30:49.907605-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:31:06.7846-07:00",
              "lastResponse": "2020-08-20T14:31:06.785616-07:00"
            }
          }
        },
        "firstResponse": "2020-08-20T14:31:06.784537-07:00",
        "lastResponse": "2020-08-20T14:31:06.785552-07:00"
      },
      "via-goto": {
        "Header": "via-goto",
        "count": {
          "count": 28,
          "retries": 3,
          "firstResponse": "2020-08-20T14:29:36.969398-07:00",
          "lastResponse": "2020-08-20T14:31:05.084263-07:00"
        },
        "countsByValues": {
          "peer2": {
            "count": 28,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969399-07:00",
            "lastResponse": "2020-08-20T14:31:05.084263-07:00"
          }
        },
        "countsByStatusCodes": {
          "200": {
            "count": 12,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:36.987899-07:00",
            "lastResponse": "2020-08-20T14:31:05.084263-07:00"
          },
          "400": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.327224-07:00",
            "lastResponse": "2020-08-20T14:31:04.083356-07:00"
          },
          "418": {
            "count": 10,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969399-07:00",
            "lastResponse": "2020-08-20T14:31:05.068305-07:00"
          },
          "502": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:30:02.348086-07:00",
            "lastResponse": "2020-08-20T14:31:04.066579-07:00"
          },
          "503": {
            "count": 2,
            "retries": 0,
            "firstResponse": "2020-08-20T14:29:45.858555-07:00",
            "lastResponse": "2020-08-20T14:30:49.90757-07:00"
          }
        },
        "countsByValuesStatusCodes": {
          "peer2": {
            "200": {
              "count": 12,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:36.9879-07:00",
              "lastResponse": "2020-08-20T14:31:05.084264-07:00"
            },
            "400": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.327225-07:00",
              "lastResponse": "2020-08-20T14:31:04.083357-07:00"
            },
            "418": {
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.9694-07:00",
              "lastResponse": "2020-08-20T14:31:05.068306-07:00"
            },
            "502": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.348087-07:00",
              "lastResponse": "2020-08-20T14:31:04.06658-07:00"
            },
            "503": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:29:45.858557-07:00",
              "lastResponse": "2020-08-20T14:30:49.907572-07:00"
            }
          }
        },
        "crossHeaders": {},
        "crossHeadersByValues": {},
        "firstResponse": "2020-08-20T14:31:06.784634-07:00",
        "lastResponse": "2020-08-20T14:31:06.785653-07:00"
      }
    },
    "targetHeaderCounts": {
      "t1": {
        "goto-host": {
          "Header": "goto-host",
          "count": {
            "count": 14,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969404-07:00",
            "lastResponse": "2020-08-20T14:31:05.068313-07:00"
          },
          "countsByValues": {
            "pod.local@1.0.0.1:8082": {
              "count": 6,
              "retries": 1,
              "firstResponse": "2020-08-20T14:30:32.028521-07:00",
              "lastResponse": "2020-08-20T14:31:05.068314-07:00"
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
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969404-07:00",
              "lastResponse": "2020-08-20T14:31:05.068313-07:00"
            },
            "502": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:31:04.066585-07:00",
              "lastResponse": "2020-08-20T14:31:04.066585-07:00"
            }
          },
          "countsByValuesStatusCodes": {
            "pod.local@1.0.0.1:8082": {
              "418": {
                "count": 5,
                "retries": 1,
                "firstResponse": "2020-08-20T14:30:32.028522-07:00",
                "lastResponse": "2020-08-20T14:31:05.068314-07:00"
              },
              "502": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:31:04.066586-07:00",
                "lastResponse": "2020-08-20T14:31:04.066586-07:00"
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
              "Header": "request-from-goto-host",
              "count": {
                "count": 14,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969409-07:00",
                "lastResponse": "2020-08-20T14:31:05.068316-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8081": {
                  "count": 14,
                  "retries": 3,
                  "firstResponse": "2020-08-20T14:29:36.96941-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068317-07:00"
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
                  "count": 10,
                  "retries": 3,
                  "firstResponse": "2020-08-20T14:29:36.969409-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068316-07:00"
                },
                "502": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:31:04.066594-07:00",
                  "lastResponse": "2020-08-20T14:31:04.066594-07:00"
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
                    "count": 10,
                    "retries": 3,
                    "firstResponse": "2020-08-20T14:29:36.96941-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068317-07:00"
                  },
                  "502": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:31:04.066595-07:00",
                    "lastResponse": "2020-08-20T14:31:04.066595-07:00"
                  }
                }
              },
              "crossHeaders": {},
              "crossHeadersByValues": {},
              "firstResponse": "2020-08-20T14:31:06.784967-07:00",
              "lastResponse": "2020-08-20T14:31:06.784967-07:00"
            }
          },
          "crossHeadersByValues": {
            "pod.local@1.0.0.1:8082": {
              "request-from-goto-host": {
                "Header": "request-from-goto-host",
                "count": {
                  "count": 6,
                  "retries": 1,
                  "firstResponse": "2020-08-20T14:30:32.028526-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068318-07:00"
                },
                "countsByValues": {
                  "pod.local@1.0.0.1:8081": {
                    "count": 6,
                    "retries": 1,
                    "firstResponse": "2020-08-20T14:30:32.028527-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068319-07:00"
                  }
                },
                "countsByStatusCodes": {
                  "418": {
                    "count": 5,
                    "retries": 1,
                    "firstResponse": "2020-08-20T14:30:32.028527-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068319-07:00"
                  },
                  "502": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:31:04.066596-07:00",
                    "lastResponse": "2020-08-20T14:31:04.066596-07:00"
                  }
                },
                "countsByValuesStatusCodes": {
                  "pod.local@1.0.0.1:8081": {
                    "418": {
                      "count": 5,
                      "retries": 1,
                      "firstResponse": "2020-08-20T14:30:32.028528-07:00",
                      "lastResponse": "2020-08-20T14:31:05.06832-07:00"
                    },
                    "502": {
                      "count": 1,
                      "retries": 0,
                      "firstResponse": "2020-08-20T14:31:04.066597-07:00",
                      "lastResponse": "2020-08-20T14:31:04.066597-07:00"
                    }
                  }
                },
                "crossHeaders": {},
                "crossHeadersByValues": {},
                "firstResponse": "2020-08-20T14:31:06.784996-07:00",
                "lastResponse": "2020-08-20T14:31:06.784996-07:00"
              }
            },
            "pod.local@1.0.0.1:9092": {
              "request-from-goto-host": {
                "Header": "request-from-goto-host",
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
                "firstResponse": "2020-08-20T14:31:06.785012-07:00",
                "lastResponse": "2020-08-20T14:31:06.785012-07:00"
              }
            }
          },
          "firstResponse": "2020-08-20T14:31:06.784933-07:00",
          "lastResponse": "2020-08-20T14:31:06.784933-07:00"
        },
        "request-from-goto": {
          "Header": "request-from-goto",
          "count": {
            "count": 14,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969402-07:00",
            "lastResponse": "2020-08-20T14:31:05.06831-07:00"
          },
          "countsByValues": {
            "peer1": {
              "count": 14,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969402-07:00",
              "lastResponse": "2020-08-20T14:31:05.06831-07:00"
            }
          },
          "countsByStatusCodes": {
            "200": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:15.795676-07:00",
              "lastResponse": "2020-08-20T14:30:16.801426-07:00"
            },
            "400": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.327227-07:00",
              "lastResponse": "2020-08-20T14:30:02.327227-07:00"
            },
            "418": {
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969402-07:00",
              "lastResponse": "2020-08-20T14:31:05.06831-07:00"
            },
            "502": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:31:04.066582-07:00",
              "lastResponse": "2020-08-20T14:31:04.066582-07:00"
            }
          },
          "countsByValuesStatusCodes": {
            "peer1": {
              "200": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:15.795677-07:00",
                "lastResponse": "2020-08-20T14:30:16.801436-07:00"
              },
              "400": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.327228-07:00",
                "lastResponse": "2020-08-20T14:30:02.327228-07:00"
              },
              "418": {
                "count": 10,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969403-07:00",
                "lastResponse": "2020-08-20T14:31:05.068311-07:00"
              },
              "502": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:31:04.066582-07:00",
                "lastResponse": "2020-08-20T14:31:04.066582-07:00"
              }
            }
          },
          "crossHeaders": {},
          "crossHeadersByValues": {},
          "firstResponse": "2020-08-20T14:31:06.785034-07:00",
          "lastResponse": "2020-08-20T14:31:06.785034-07:00"
        },
        "request-from-goto-host": {
          "Header": "request-from-goto-host",
          "count": {
            "count": 14,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969414-07:00",
            "lastResponse": "2020-08-20T14:31:05.068387-07:00"
          },
          "countsByValues": {
            "pod.local@1.0.0.1:8081": {
              "count": 14,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969414-07:00",
              "lastResponse": "2020-08-20T14:31:05.068388-07:00"
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
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969414-07:00",
              "lastResponse": "2020-08-20T14:31:05.068388-07:00"
            },
            "502": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:31:04.066599-07:00",
              "lastResponse": "2020-08-20T14:31:04.066599-07:00"
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
                "count": 10,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969415-07:00",
                "lastResponse": "2020-08-20T14:31:05.068389-07:00"
              },
              "502": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:31:04.0666-07:00",
                "lastResponse": "2020-08-20T14:31:04.0666-07:00"
              }
            }
          },
          "crossHeaders": {
            "goto-host": {
              "Header": "goto-host",
              "count": {
                "count": 14,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.969416-07:00",
                "lastResponse": "2020-08-20T14:31:05.068391-07:00"
              },
              "countsByValues": {
                "pod.local@1.0.0.1:8082": {
                  "count": 6,
                  "retries": 1,
                  "firstResponse": "2020-08-20T14:30:32.028532-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068391-07:00"
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
                  "count": 10,
                  "retries": 3,
                  "firstResponse": "2020-08-20T14:29:36.969417-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068391-07:00"
                },
                "502": {
                  "count": 1,
                  "retries": 0,
                  "firstResponse": "2020-08-20T14:31:04.066601-07:00",
                  "lastResponse": "2020-08-20T14:31:04.066601-07:00"
                }
              },
              "countsByValuesStatusCodes": {
                "pod.local@1.0.0.1:8082": {
                  "418": {
                    "count": 5,
                    "retries": 1,
                    "firstResponse": "2020-08-20T14:30:32.028533-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068392-07:00"
                  },
                  "502": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:31:04.066602-07:00",
                    "lastResponse": "2020-08-20T14:31:04.066602-07:00"
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
              "firstResponse": "2020-08-20T14:31:06.78509-07:00",
              "lastResponse": "2020-08-20T14:31:06.78509-07:00"
            }
          },
          "crossHeadersByValues": {
            "pod.local@1.0.0.1:8081": {
              "goto-host": {
                "Header": "goto-host",
                "count": {
                  "count": 14,
                  "retries": 3,
                  "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                  "lastResponse": "2020-08-20T14:31:05.068393-07:00"
                },
                "countsByValues": {
                  "pod.local@1.0.0.1:8082": {
                    "count": 6,
                    "retries": 1,
                    "firstResponse": "2020-08-20T14:30:32.028534-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068393-07:00"
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
                    "count": 10,
                    "retries": 3,
                    "firstResponse": "2020-08-20T14:29:36.969418-07:00",
                    "lastResponse": "2020-08-20T14:31:05.068393-07:00"
                  },
                  "502": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2020-08-20T14:31:04.066603-07:00",
                    "lastResponse": "2020-08-20T14:31:04.066603-07:00"
                  }
                },
                "countsByValuesStatusCodes": {
                  "pod.local@1.0.0.1:8082": {
                    "418": {
                      "count": 5,
                      "retries": 1,
                      "firstResponse": "2020-08-20T14:30:32.028535-07:00",
                      "lastResponse": "2020-08-20T14:31:05.068394-07:00"
                    },
                    "502": {
                      "count": 1,
                      "retries": 0,
                      "firstResponse": "2020-08-20T14:31:04.066603-07:00",
                      "lastResponse": "2020-08-20T14:31:04.066603-07:00"
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
                "firstResponse": "2020-08-20T14:31:06.785126-07:00",
                "lastResponse": "2020-08-20T14:31:06.785126-07:00"
              }
            }
          },
          "firstResponse": "2020-08-20T14:31:06.785061-07:00",
          "lastResponse": "2020-08-20T14:31:06.785061-07:00"
        },
        "via-goto": {
          "Header": "via-goto",
          "count": {
            "count": 14,
            "retries": 3,
            "firstResponse": "2020-08-20T14:29:36.969398-07:00",
            "lastResponse": "2020-08-20T14:31:05.068305-07:00"
          },
          "countsByValues": {
            "peer2": {
              "count": 14,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969399-07:00",
              "lastResponse": "2020-08-20T14:31:05.068306-07:00"
            }
          },
          "countsByStatusCodes": {
            "200": {
              "count": 2,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:15.795672-07:00",
              "lastResponse": "2020-08-20T14:30:16.801423-07:00"
            },
            "400": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:30:02.327224-07:00",
              "lastResponse": "2020-08-20T14:30:02.327224-07:00"
            },
            "418": {
              "count": 10,
              "retries": 3,
              "firstResponse": "2020-08-20T14:29:36.969399-07:00",
              "lastResponse": "2020-08-20T14:31:05.068305-07:00"
            },
            "502": {
              "count": 1,
              "retries": 0,
              "firstResponse": "2020-08-20T14:31:04.066579-07:00",
              "lastResponse": "2020-08-20T14:31:04.066579-07:00"
            }
          },
          "countsByValuesStatusCodes": {
            "peer2": {
              "200": {
                "count": 2,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:15.795674-07:00",
                "lastResponse": "2020-08-20T14:30:16.801424-07:00"
              },
              "400": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:30:02.327225-07:00",
                "lastResponse": "2020-08-20T14:30:02.327225-07:00"
              },
              "418": {
                "count": 10,
                "retries": 3,
                "firstResponse": "2020-08-20T14:29:36.9694-07:00",
                "lastResponse": "2020-08-20T14:31:05.068306-07:00"
              },
              "502": {
                "count": 1,
                "retries": 0,
                "firstResponse": "2020-08-20T14:31:04.06658-07:00",
                "lastResponse": "2020-08-20T14:31:04.06658-07:00"
              }
            }
          },
          "crossHeaders": {},
          "crossHeadersByValues": {},
          "firstResponse": "2020-08-20T14:31:06.785151-07:00",
          "lastResponse": "2020-08-20T14:31:06.785151-07:00"
        }
      },
      "t2": {}
    }
  },
  "peer2": {
    "targetInvocationCounts": {},
    "targetFirstResponses": {},
    "targetLastResponses": {},
    "countsByStatusCodes": {},
    "countsByHeaders": {},
    "countsByHeaderValues": {},
    "countsByTargetStatusCodes": {},
    "countsByTargetHeaders": {},
    "countsByTargetHeaderValues": {},
    "detailedHeaderCounts": {},
    "detailedTargetHeaderCounts": {}
  }
}

```
</p>
</details>

<br/>

<details>
<summary>Targets Detailed Results Example</summary>
<p>

```json
$ curl -s localhost:8080/registry/peers/lockers/targets/results?detailed=Y
{
  "peer1": {
    "t1": {
      "target": "t1",
      "invocationCounts": 40,
      "firstResponses": "2020-06-23T08:30:29.719768-07:00",
      "lastResponses": "2020-06-23T08:30:48.780715-07:00",
      "countsByStatus": {
        "200 OK": 40
      },
      "countsByStatusCodes": {
        "200": 40
      },
      "countsByHeaders": {},
      "countsByHeaderValues": {},
      "countsByURIs": {
        "/echo": 40
      }
    },
    "t2-2": {
      "target": "t2",
      "invocationCounts": 31,
      "firstResponses": "2020-06-23T08:30:44.816036-07:00",
      "lastResponses": "2020-06-23T08:30:59.868265-07:00",
      "countsByStatus": {
        "200 OK": 31
      },
      "countsByStatusCodes": {
        "200": 31
      },
      "countsByHeaders": {},
      "countsByHeaderValues": {},
      "countsByURIs": {
        "/echo": 31
      }
    }
  },
  "peer2": {
    "t1": {
      "target": "t1",
      "invocationCounts": 40,
      "firstResponses": "2020-06-23T08:30:29.719768-07:00",
      "lastResponses": "2020-06-23T08:30:48.780715-07:00",
      "countsByStatus": {
        "200 OK": 40
      },
      "countsByStatusCodes": {
        "200": 40
      },
      "countsByHeaders": {},
      "countsByHeaderValues": {},
      "countsByURIs": {
        "/echo": 40
      }
    },
    "t2-2": {
      "target": "t2",
      "invocationCounts": 31,
      "firstResponses": "2020-06-23T08:30:44.816036-07:00",
      "lastResponses": "2020-06-23T08:30:59.868265-07:00",
      "countsByStatus": {
        "200 OK": 31
      },
      "countsByStatusCodes": {
        "200": 31
      },
      "countsByHeaders": {},
      "countsByHeaderValues": {},
      "countsByURIs": {
        "/echo": 31
      }
    }
  }
}
```
</p>
</details>
