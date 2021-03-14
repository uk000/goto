
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
<summary>Targets Summary Results Example</summary>
<p>

```json

$ curl -s localhost:8080/registry/peers/targets/results
{
  "peer1": {
      "targetInvocationCounts": {
          "t-1": 9,
          "t-2": 16
      },
      "targetFirstResponses": {
          "t-1": "2021-03-14T01:54:50.608552-08:00",
          "t-2": "2021-03-14T01:54:50.608693-08:00"
      },
      "targetLastResponses": {
          "t-1": "2021-03-14T01:54:52.249732-08:00",
          "t-2": "2021-03-14T01:54:52.460961-08:00"
      },
      "countsByStatusCodes": {
          "200": 6,
          "400": 3,
          "401": 1,
          "403": 2,
          "418": 7,
          "500": 2,
          "503": 4
      },
      "countsByStatusCodeTimeBuckets": {
          "200": {
              "[101 200]": 4,
              "[201 300]": 2
          },
          "400": {
              "[301 500]": 3
          },
          "401": {
              "[301 500]": 1
          },
          "403": {
              "[301 500]": 2
          },
          "418": {
              "[0 100]": 7
          },
          "500": {
              "[301 500]": 2
          },
          "503": {
              "[301 500]": 2,
              "[501 1000]": 2
          }
      },
      "countsByHeaders": {
          "goto-host": 25,
          "request-from-goto": 25,
          "via-goto": 25
      },
      "countsByURIs": {
          "/foo": 4,
          "/a": 2,
          "/bar": 2,
          "/b": 4,
          "/status/418": 7,
          "/c": 6
      },
      "countsByURIStatusCodes": {
          "/foo": {
              "200": 4
          },
          "/a": {
              "200": 2
          },
          "/bar": {
              "401": 1,
              "403": 1
          },
          "/b": {
              "400": 3,
              "403": 1
          },
          "/status/418": {
              "418": 7
          },
          "/c": {
              "500": 2,
              "503": 4
          }
      },
      "countsByURITimeBuckets": {
          "/foo": {
              "[101 200]": 4
          },
          "/a": {
              "[201 300]": 2
          },
          "/bar": {
              "[301 500]": 2
          },
          "/b": {
              "[301 500]": 4
          },
          "/status/418": {
              "[0 100]": 7
          },
          "/c": {
              "[301 500]": 4,
              "[501 1000]": 2
          }
      },
      "countsByTimeBuckets": {
          "[0 100]": 7,
          "[101 200]": 4,
          "[201 300]": 2,
          "[301 500]": 10,
          "[501 1000]": 2
      },
      "countsByTimeBucketStatusCodes": {
          "[0 100]": {
              "418": 7
          },
          "[101 200]": {
              "200": 4
          },
          "[201 300]": {
              "200": 2
          },
          "[301 500]": {
              "400": 3,
              "401": 1,
              "403": 2,
              "500": 2,
              "503": 2
          },
          "[501 1000]": {
              "503": 2
          }
      },
      "countsByHeaderValues": {
          "goto-host": {
              "localhost.local@1.1.1.1:8080": 25
          },
          "request-from-goto": {
              "peer1": 25
          },
          "via-goto": {
              "Registry": 25
          }
      },
      "countsByTargetStatusCodes": {
          "t-1": {
              "200": 4,
              "401": 1,
              "403": 1,
              "418": 3
          },
          "t-2": {
              "200": 2,
              "400": 3,
              "403": 1,
              "418": 4,
              "500": 2,
              "503": 4
          }
      },
      "countsByTargetStatusCodeTimeBuckets": {
          "t-1": {
              "200": {
                  "[101 200]": 4
              },
              "401": {
                  "[301 500]": 1
              },
              "403": {
                  "[301 500]": 1
              },
              "418": {
                  "[0 100]": 3
              }
          },
          "t-2": {
              "200": {
                  "[201 300]": 2
              },
              "400": {
                  "[301 500]": 3
              },
              "403": {
                  "[301 500]": 1
              },
              "418": {
                  "[0 100]": 4
              },
              "500": {
                  "[301 500]": 2
              },
              "503": {
                  "[301 500]": 2,
                  "[501 1000]": 2
              }
          }
      },
      "countsByTargetHeaders": {
          "t-1": {
              "goto-host": 9,
              "request-from-goto": 9,
              "via-goto": 9
          },
          "t-2": {
              "goto-host": 16,
              "request-from-goto": 16,
              "via-goto": 16
          }
      },
      "countsByTargetHeaderValues": {
          "t-1": {
              "goto-host": {
                  "localhost.local@1.1.1.1:8080": 9
              },
              "request-from-goto": {
                  "peer1": 9
              },
              "via-goto": {
                  "Registry": 9
              }
          },
          "t-2": {
              "goto-host": {
                  "localhost.local@1.1.1.1:8080": 16
              },
              "request-from-goto": {
                  "peer1": 16
              },
              "via-goto": {
                  "Registry": 16
              }
          }
      },
      "countsByTargetURIs": {
          "t-1": {
              "/foo": 4,
              "/bar": 2,
              "/status/418": 3
          },
          "t-2": {
              "/a": 2,
              "/b": 4,
              "/status/418": 4,
              "/c": 6
          }
      },
      "countsByTargetURIStatusCodes": {
          "t-1": {
              "/foo": {
                  "200": 4
              },
              "/bar": {
                  "401": 1,
                  "403": 1
              },
              "/status/418": {
                  "418": 3
              }
          },
          "t-2": {
              "/a": {
                  "200": 2
              },
              "/b": {
                  "400": 3,
                  "403": 1
              },
              "/status/418": {
                  "418": 4
              },
              "/c": {
                  "500": 2,
                  "503": 4
              }
          }
      },
      "countsByTargetURITimeBuckets": {
          "t-1": {
              "/foo": {
                  "[101 200]": 4
              },
              "/bar": {
                  "[301 500]": 2
              },
              "/status/418": {
                  "[0 100]": 3
              }
          },
          "t-2": {
              "/a": {
                  "[201 300]": 2
              },
              "/b": {
                  "[301 500]": 4
              },
              "/status/418": {
                  "[0 100]": 4
              },
              "/c": {
                  "[301 500]": 4,
                  "[501 1000]": 2
              }
          }
      },
      "countsByTargetTimeBuckets": {
          "t-1": {
              "[0 100]": 3,
              "[101 200]": 4,
              "[301 500]": 2
          },
          "t-2": {
              "[0 100]": 4,
              "[201 300]": 2,
              "[301 500]": 8,
              "[501 1000]": 2
          }
      },
      "countsByTargetTimeBucketStatusCodes": {
          "t-1": {
              "[0 100]": {
                  "418": 3
              },
              "[101 200]": {
                  "200": 4
              },
              "[301 500]": {
                  "401": 1,
                  "403": 1
              }
          },
          "t-2": {
              "[0 100]": {
                  "418": 4
              },
              "[201 300]": {
                  "200": 2
              },
              "[301 500]": {
                  "400": 3,
                  "403": 1,
                  "500": 2,
                  "503": 2
              },
              "[501 1000]": {
                  "503": 2
              }
          }
      }
  },
  "peer2": {
      "targetInvocationCounts": {
          "t-1": 15,
          "t-2": 16
      },
      "targetFirstResponses": {
          "t-1": "2021-03-14T01:54:50.719806-08:00",
          "t-2": "2021-03-14T01:54:50.615399-08:00"
      },
      "targetLastResponses": {
          "t-1": "2021-03-14T01:54:52.423373-08:00",
          "t-2": "2021-03-14T01:54:52.403925-08:00"
      },
      "countsByStatusCodes": {
          "200": 11,
          "400": 1,
          "401": 1,
          "402": 2,
          "403": 2,
          "418": 8,
          "500": 2,
          "501": 2,
          "502": 2
      },
      "countsByStatusCodeTimeBuckets": {
          "200": {
              "[0 100]": 1,
              "[101 200]": 3,
              "[201 300]": 2,
              "[301 500]": 5
          },
          "400": {
              "[301 500]": 1
          },
          "401": {
              "[301 500]": 1
          },
          "402": {
              "[301 500]": 2
          },
          "403": {
              "[301 500]": 2
          },
          "418": {
              "[0 100]": 8
          },
          "500": {
              "[301 500]": 1,
              "[501 1000]": 1
          },
          "501": {
              "[301 500]": 1,
              "[501 1000]": 1
          },
          "502": {
              "[501 1000]": 2
          }
      },
      "countsByHeaders": {
          "goto-host": 31,
          "request-from-goto": 31,
          "via-goto": 31
      },
      "countsByURIs": {
          "/foo": 4,
          "/foo2": 7,
          "/bar": 6,
          "/status/418": 8,
          "/bar2": 6
      },
      "countsByURIStatusCodes": {
          "/foo": {
              "200": 4
          },
          "/foo2": {
              "200": 7
          },
          "/bar": {
              "400": 1,
              "401": 1,
              "402": 2,
              "403": 2
          },
          "/status/418": {
              "418": 8
          },
          "/bar2": {
              "500": 2,
              "501": 2,
              "502": 2
          }
      },
      "countsByURITimeBuckets": {
          "/foo": {
              "[0 100]": 1,
              "[101 200]": 3
          },
          "/foo2": {
              "[201 300]": 2,
              "[301 500]": 5
          },
          "/bar": {
              "[301 500]": 6
          },
          "/status/418": {
              "[0 100]": 8
          },
          "/bar2": {
              "[301 500]": 2,
              "[501 1000]": 4
          }
      },
      "countsByTimeBuckets": {
          "[0 100]": 9,
          "[101 200]": 3,
          "[201 300]": 2,
          "[301 500]": 13,
          "[501 1000]": 4
      },
      "countsByTimeBucketStatusCodes": {
          "[0 100]": {
              "200": 1,
              "418": 8
          },
          "[101 200]": {
              "200": 3
          },
          "[201 300]": {
              "200": 2
          },
          "[301 500]": {
              "200": 5,
              "400": 1,
              "401": 1,
              "402": 2,
              "403": 2,
              "500": 1,
              "501": 1
          },
          "[501 1000]": {
              "500": 1,
              "501": 1,
              "502": 2
          }
      },
      "countsByHeaderValues": {
          "goto-host": {
              "localhost.local@1.1.1.1:8081": 31
          },
          "request-from-goto": {
              "peer2": 31
          },
          "via-goto": {
              "peer1": 31
          }
      },
      "countsByTargetStatusCodes": {
          "t-1": {
              "200": 4,
              "403": 1,
              "418": 6,
              "500": 1,
              "501": 1,
              "502": 2
          },
          "t-2": {
              "200": 7,
              "400": 1,
              "401": 1,
              "402": 2,
              "403": 1,
              "418": 2,
              "500": 1,
              "501": 1
          }
      },
      "countsByTargetStatusCodeTimeBuckets": {
          "t-1": {
              "200": {
                  "[0 100]": 1,
                  "[101 200]": 3
              },
              "403": {
                  "[301 500]": 1
              },
              "418": {
                  "[0 100]": 6
              },
              "500": {
                  "[301 500]": 1
              },
              "501": {
                  "[301 500]": 1
              },
              "502": {
                  "[501 1000]": 2
              }
          },
          "t-2": {
              "200": {
                  "[201 300]": 2,
                  "[301 500]": 5
              },
              "400": {
                  "[301 500]": 1
              },
              "401": {
                  "[301 500]": 1
              },
              "402": {
                  "[301 500]": 2
              },
              "403": {
                  "[301 500]": 1
              },
              "418": {
                  "[0 100]": 2
              },
              "500": {
                  "[501 1000]": 1
              },
              "501": {
                  "[501 1000]": 1
              }
          }
      },
      "countsByTargetHeaders": {
          "t-1": {
              "goto-host": 15,
              "request-from-goto": 15,
              "via-goto": 15
          },
          "t-2": {
              "goto-host": 16,
              "request-from-goto": 16,
              "via-goto": 16
          }
      },
      "countsByTargetHeaderValues": {
          "t-1": {
              "goto-host": {
                  "localhost.local@1.1.1.1:8081": 15
              },
              "request-from-goto": {
                  "peer2": 15
              },
              "via-goto": {
                  "peer1": 15
              }
          },
          "t-2": {
              "goto-host": {
                  "localhost.local@1.1.1.1:8081": 16
              },
              "request-from-goto": {
                  "peer2": 16
              },
              "via-goto": {
                  "peer1": 16
              }
          }
      },
      "countsByTargetURIs": {
          "t-1": {
              "/foo": 4,
              "/bar": 1,
              "/status/418": 6,
              "/bar2": 4
          },
          "t-2": {
              "/foo2": 7,
              "/bar": 5,
              "/status/418": 2,
              "/bar2": 2
          }
      },
      "countsByTargetURIStatusCodes": {
          "t-1": {
              "/foo": {
                  "200": 4
              },
              "/bar": {
                  "403": 1
              },
              "/status/418": {
                  "418": 6
              },
              "/bar2": {
                  "500": 1,
                  "501": 1,
                  "502": 2
              }
          },
          "t-2": {
              "/foo2": {
                  "200": 7
              },
              "/bar": {
                  "400": 1,
                  "401": 1,
                  "402": 2,
                  "403": 1
              },
              "/status/418": {
                  "418": 2
              },
              "/bar2": {
                  "500": 1,
                  "501": 1
              }
          }
      },
      "countsByTargetURITimeBuckets": {
          "t-1": {
              "/foo": {
                  "[0 100]": 1,
                  "[101 200]": 3
              },
              "/bar": {
                  "[301 500]": 1
              },
              "/status/418": {
                  "[0 100]": 6
              },
              "/bar2": {
                  "[301 500]": 2,
                  "[501 1000]": 2
              }
          },
          "t-2": {
              "/foo2": {
                  "[201 300]": 2,
                  "[301 500]": 5
              },
              "/bar": {
                  "[301 500]": 5
              },
              "/status/418": {
                  "[0 100]": 2
              },
              "/bar2": {
                  "[501 1000]": 2
              }
          }
      },
      "countsByTargetTimeBuckets": {
          "t-1": {
              "[0 100]": 7,
              "[101 200]": 3,
              "[301 500]": 3,
              "[501 1000]": 2
          },
          "t-2": {
              "[0 100]": 2,
              "[201 300]": 2,
              "[301 500]": 10,
              "[501 1000]": 2
          }
      },
      "countsByTargetTimeBucketStatusCodes": {
          "t-1": {
              "[0 100]": {
                  "200": 1,
                  "418": 6
              },
              "[101 200]": {
                  "200": 3
              },
              "[301 500]": {
                  "403": 1,
                  "500": 1,
                  "501": 1
              },
              "[501 1000]": {
                  "502": 2
              }
          },
          "t-2": {
              "[0 100]": {
                  "418": 2
              },
              "[201 300]": {
                  "200": 2
              },
              "[301 500]": {
                  "200": 5,
                  "400": 1,
                  "401": 1,
                  "402": 2,
                  "403": 1
              },
              "[501 1000]": {
                  "500": 1,
                  "501": 1
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
<summary>Targets Detailed Results Example</summary>
<p>

```json

$ curl -s localhost:8080/registry/peers/targets/results?detailed=Y
{
    "peer1": {
        "t-1": {
            "target": "t-1",
            "invocationCounts": 9,
            "firstResponse": "2021-03-14T01:54:50.608552-08:00",
            "lastResponse": "2021-03-14T01:54:52.249732-08:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 4,
                "401 Unauthorized": 1,
                "403 Forbidden": 1,
                "418 I'm a teapot": 3
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.757233-08:00",
                    "lastResponse": "2021-03-14T01:54:52.012611-08:00",
                    "countsByTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.75725-08:00",
                            "lastResponse": "2021-03-14T01:54:52.012626-08:00"
                        }
                    }
                },
                "401": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.044642-08:00",
                    "lastResponse": "2021-03-14T01:54:51.044642-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044673-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044673-08:00"
                        }
                    }
                },
                "403": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.249734-08:00",
                    "lastResponse": "2021-03-14T01:54:52.249734-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.249768-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249768-08:00"
                        }
                    }
                },
                "418": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.608553-08:00",
                    "lastResponse": "2021-03-14T01:54:51.840666-08:00",
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608586-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840686-08:00"
                        }
                    }
                }
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 9,
                    "retries": 0,
                    "header": "goto-host",
                    "countsByValues": {
                        "localhost.local@1.1.1.1:8080": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608564-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249757-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.757243-08:00",
                            "lastResponse": "2021-03-14T01:54:52.01262-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044658-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044658-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.249756-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249756-08:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608564-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840676-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "localhost.local@1.1.1.1:8080": {
                            "200": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.757244-08:00",
                                "lastResponse": "2021-03-14T01:54:52.01262-08:00"
                            },
                            "401": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.04466-08:00",
                                "lastResponse": "2021-03-14T01:54:51.04466-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.249757-08:00",
                                "lastResponse": "2021-03-14T01:54:52.249757-08:00"
                            },
                            "418": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.608565-08:00",
                                "lastResponse": "2021-03-14T01:54:51.840677-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.224043-07:00",
                    "lastResponse": "2021-03-14T03:04:56.224043-07:00"
                },
                "request-from-goto": {
                    "count": 9,
                    "retries": 0,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608561-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249754-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.75724-08:00",
                            "lastResponse": "2021-03-14T01:54:52.012617-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044653-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044653-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.249754-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249754-08:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608561-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840674-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.757241-08:00",
                                "lastResponse": "2021-03-14T01:54:52.012618-08:00"
                            },
                            "401": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.044655-08:00",
                                "lastResponse": "2021-03-14T01:54:51.044655-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.249754-08:00",
                                "lastResponse": "2021-03-14T01:54:52.249754-08:00"
                            },
                            "418": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.608561-08:00",
                                "lastResponse": "2021-03-14T01:54:51.840675-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.224051-07:00",
                    "lastResponse": "2021-03-14T03:04:56.224051-07:00"
                },
                "via-goto": {
                    "count": 9,
                    "retries": 0,
                    "header": "via-goto",
                    "countsByValues": {
                        "Registry": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608558-08:00",
                            "lastResponse": "2021-03-14T01:54:52.24975-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.757237-08:00",
                            "lastResponse": "2021-03-14T01:54:52.012614-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044648-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044648-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.249738-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249738-08:00"
                        },
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608557-08:00",
                            "lastResponse": "2021-03-14T01:54:51.84067-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Registry": {
                            "200": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.757238-08:00",
                                "lastResponse": "2021-03-14T01:54:52.012615-08:00"
                            },
                            "401": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.04465-08:00",
                                "lastResponse": "2021-03-14T01:54:51.04465-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.249751-08:00",
                                "lastResponse": "2021-03-14T01:54:52.249751-08:00"
                            },
                            "418": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.608559-08:00",
                                "lastResponse": "2021-03-14T01:54:51.840671-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.224056-07:00",
                    "lastResponse": "2021-03-14T03:04:56.224056-07:00"
                }
            },
            "countsByURIs": {
                "/foo": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.757234-08:00",
                    "lastResponse": "2021-03-14T01:54:52.012612-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.757235-08:00",
                            "lastResponse": "2021-03-14T01:54:52.012613-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[101 200]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.757249-08:00",
                            "lastResponse": "2021-03-14T01:54:52.012625-08:00"
                        }
                    }
                },
                "/a": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.044644-08:00",
                    "lastResponse": "2021-03-14T01:54:52.249735-08:00",
                    "countsByStatusCodes": {
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044645-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044645-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.249736-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249736-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044672-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249767-08:00"
                        }
                    }
                },
                "/status/418": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.608554-08:00",
                    "lastResponse": "2021-03-14T01:54:51.840667-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608555-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840667-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608579-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840686-08:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[0 100]": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.608578-08:00",
                    "lastResponse": "2021-03-14T01:54:51.840685-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608579-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840685-08:00"
                        }
                    }
                },
                "[101 200]": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.757248-08:00",
                    "lastResponse": "2021-03-14T01:54:52.012625-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.757248-08:00",
                            "lastResponse": "2021-03-14T01:54:52.012625-08:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.044669-08:00",
                    "lastResponse": "2021-03-14T01:54:52.249762-08:00",
                    "countsByStatusCodes": {
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.04467-08:00",
                            "lastResponse": "2021-03-14T01:54:51.04467-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.249766-08:00",
                            "lastResponse": "2021-03-14T01:54:52.249766-08:00"
                        }
                    }
                }
            }
        },
        "t-2": {
            "target": "t-2",
            "invocationCounts": 25,
            "firstResponse": "2021-03-14T01:54:50.608693-08:00",
            "lastResponse": "2021-03-14T01:54:53.545757-08:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 3,
                "400 Bad Request": 3,
                "403 Forbidden": 3,
                "418 I'm a teapot": 6,
                "500 Internal Server Error": 4,
                "503 Service Unavailable": 6
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.866186-08:00",
                    "lastResponse": "2021-03-14T01:54:53.455471-08:00",
                    "countsByTimeBuckets": {
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.866219-08:00",
                            "lastResponse": "2021-03-14T01:54:50.901145-08:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:53.455494-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455494-08:00"
                        }
                    }
                },
                "400": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.013157-08:00",
                    "lastResponse": "2021-03-14T01:54:52.402317-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.01318-08:00",
                            "lastResponse": "2021-03-14T01:54:52.402342-08:00"
                        }
                    }
                },
                "403": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.888845-08:00",
                    "lastResponse": "2021-03-14T01:54:53.354948-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888853-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354976-08:00"
                        }
                    }
                },
                "418": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.608694-08:00",
                    "lastResponse": "2021-03-14T01:54:52.987296-08:00",
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.60872-08:00",
                            "lastResponse": "2021-03-14T01:54:52.987302-08:00"
                        }
                    }
                },
                "500": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.093586-08:00",
                    "lastResponse": "2021-03-14T01:54:52.976385-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093607-08:00",
                            "lastResponse": "2021-03-14T01:54:52.938748-08:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.97641-08:00",
                            "lastResponse": "2021-03-14T01:54:52.97641-08:00"
                        }
                    }
                },
                "503": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.888816-08:00",
                    "lastResponse": "2021-03-14T01:54:53.545757-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.304581-08:00",
                            "lastResponse": "2021-03-14T01:54:52.921657-08:00"
                        },
                        "[501 1000]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888827-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545771-08:00"
                        }
                    }
                }
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 25,
                    "retries": 0,
                    "header": "goto-host",
                    "countsByValues": {
                        "localhost.local@1.1.1.1:8080": {
                            "count": 25,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608707-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545765-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.866202-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455487-08:00"
                        },
                        "400": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.013171-08:00",
                            "lastResponse": "2021-03-14T01:54:52.402331-08:00"
                        },
                        "403": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.88885-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354966-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608707-08:00",
                            "lastResponse": "2021-03-14T01:54:52.9873-08:00"
                        },
                        "500": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093599-08:00",
                            "lastResponse": "2021-03-14T01:54:52.976399-08:00"
                        },
                        "503": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888822-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545765-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "localhost.local@1.1.1.1:8080": {
                            "200": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.866203-08:00",
                                "lastResponse": "2021-03-14T01:54:53.455487-08:00"
                            },
                            "400": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.013172-08:00",
                                "lastResponse": "2021-03-14T01:54:52.402333-08:00"
                            },
                            "403": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.88885-08:00",
                                "lastResponse": "2021-03-14T01:54:53.354967-08:00"
                            },
                            "418": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.608707-08:00",
                                "lastResponse": "2021-03-14T01:54:52.9873-08:00"
                            },
                            "500": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.093599-08:00",
                                "lastResponse": "2021-03-14T01:54:52.976399-08:00"
                            },
                            "503": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.888822-08:00",
                                "lastResponse": "2021-03-14T01:54:53.545765-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.225001-07:00",
                    "lastResponse": "2021-03-14T03:04:56.225001-07:00"
                },
                "request-from-goto": {
                    "count": 25,
                    "retries": 0,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 25,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608704-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545764-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.866198-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455486-08:00"
                        },
                        "400": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.013167-08:00",
                            "lastResponse": "2021-03-14T01:54:52.402328-08:00"
                        },
                        "403": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888848-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354961-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608703-08:00",
                            "lastResponse": "2021-03-14T01:54:52.987299-08:00"
                        },
                        "500": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093596-08:00",
                            "lastResponse": "2021-03-14T01:54:52.976395-08:00"
                        },
                        "503": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.88882-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545763-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.866199-08:00",
                                "lastResponse": "2021-03-14T01:54:53.455486-08:00"
                            },
                            "400": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.013168-08:00",
                                "lastResponse": "2021-03-14T01:54:52.402329-08:00"
                            },
                            "403": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.888848-08:00",
                                "lastResponse": "2021-03-14T01:54:53.354963-08:00"
                            },
                            "418": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.608704-08:00",
                                "lastResponse": "2021-03-14T01:54:52.987299-08:00"
                            },
                            "500": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.093597-08:00",
                                "lastResponse": "2021-03-14T01:54:52.976396-08:00"
                            },
                            "503": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.88882-08:00",
                                "lastResponse": "2021-03-14T01:54:53.545764-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.225007-07:00",
                    "lastResponse": "2021-03-14T03:04:56.225007-07:00"
                },
                "via-goto": {
                    "count": 25,
                    "retries": 0,
                    "header": "via-goto",
                    "countsByValues": {
                        "Registry": {
                            "count": 25,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.6087-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545761-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.866192-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455483-08:00"
                        },
                        "400": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.013162-08:00",
                            "lastResponse": "2021-03-14T01:54:52.402322-08:00"
                        },
                        "403": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888847-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354955-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608699-08:00",
                            "lastResponse": "2021-03-14T01:54:52.987297-08:00"
                        },
                        "500": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093594-08:00",
                            "lastResponse": "2021-03-14T01:54:52.97639-08:00"
                        },
                        "503": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888818-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545761-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "Registry": {
                            "200": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.866194-08:00",
                                "lastResponse": "2021-03-14T01:54:53.455484-08:00"
                            },
                            "400": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.013164-08:00",
                                "lastResponse": "2021-03-14T01:54:52.402324-08:00"
                            },
                            "403": {
                                "count": 3,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.888847-08:00",
                                "lastResponse": "2021-03-14T01:54:53.354957-08:00"
                            },
                            "418": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.608701-08:00",
                                "lastResponse": "2021-03-14T01:54:52.987297-08:00"
                            },
                            "500": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.093595-08:00",
                                "lastResponse": "2021-03-14T01:54:52.976392-08:00"
                            },
                            "503": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.888819-08:00",
                                "lastResponse": "2021-03-14T01:54:53.545762-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.224995-07:00",
                    "lastResponse": "2021-03-14T03:04:56.224995-07:00"
                }
            },
            "countsByURIs": {
                "/a": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.866188-08:00",
                    "lastResponse": "2021-03-14T01:54:53.455472-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.86619-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455481-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.866217-08:00",
                            "lastResponse": "2021-03-14T01:54:50.901145-08:00"
                        },
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:53.455493-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455493-08:00"
                        }
                    }
                },
                "/b": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.013159-08:00",
                    "lastResponse": "2021-03-14T01:54:53.35495-08:00",
                    "countsByStatusCodes": {
                        "400": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.013159-08:00",
                            "lastResponse": "2021-03-14T01:54:52.40232-08:00"
                        },
                        "403": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888846-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354951-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.013179-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354975-08:00"
                        }
                    }
                },
                "/status/418": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.608695-08:00",
                    "lastResponse": "2021-03-14T01:54:52.987297-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608696-08:00",
                            "lastResponse": "2021-03-14T01:54:52.987297-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608719-08:00",
                            "lastResponse": "2021-03-14T01:54:52.987301-08:00"
                        }
                    }
                },
                "/c": {
                    "count": 10,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.09359-08:00",
                    "lastResponse": "2021-03-14T01:54:53.545758-08:00",
                    "countsByStatusCodes": {
                        "500": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093591-08:00",
                            "lastResponse": "2021-03-14T01:54:52.976387-08:00"
                        },
                        "503": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888817-08:00",
                            "lastResponse": "2021-03-14T01:54:53.545759-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093606-08:00",
                            "lastResponse": "2021-03-14T01:54:52.938747-08:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888825-08:00",
                            "lastResponse": "2021-03-14T01:54:53.54577-08:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[0 100]": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.608718-08:00",
                    "lastResponse": "2021-03-14T01:54:52.987301-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.608718-08:00",
                            "lastResponse": "2021-03-14T01:54:52.987301-08:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.866214-08:00",
                    "lastResponse": "2021-03-14T01:54:50.901144-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.866215-08:00",
                            "lastResponse": "2021-03-14T01:54:50.901144-08:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 13,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.013177-08:00",
                    "lastResponse": "2021-03-14T01:54:53.455491-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:53.455492-08:00",
                            "lastResponse": "2021-03-14T01:54:53.455492-08:00"
                        },
                        "400": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.013177-08:00",
                            "lastResponse": "2021-03-14T01:54:52.402339-08:00"
                        },
                        "403": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888852-08:00",
                            "lastResponse": "2021-03-14T01:54:53.354974-08:00"
                        },
                        "500": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.093605-08:00",
                            "lastResponse": "2021-03-14T01:54:52.938746-08:00"
                        },
                        "503": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.30458-08:00",
                            "lastResponse": "2021-03-14T01:54:52.921655-08:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.888824-08:00",
                    "lastResponse": "2021-03-14T01:54:53.54577-08:00",
                    "countsByStatusCodes": {
                        "500": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.976408-08:00",
                            "lastResponse": "2021-03-14T01:54:52.976408-08:00"
                        },
                        "503": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.888825-08:00",
                            "lastResponse": "2021-03-14T01:54:53.54577-08:00"
                        }
                    }
                }
            }
        }
    },
    "peer2": {
        "t-1": {
            "target": "t-1",
            "invocationCounts": 16,
            "firstResponse": "2021-03-14T01:54:50.719806-08:00",
            "lastResponse": "2021-03-14T01:54:52.749653-08:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 4,
                "403 Forbidden": 1,
                "418 I'm a teapot": 6,
                "500 Internal Server Error": 2,
                "501 Not Implemented": 1,
                "502 Bad Gateway": 2
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.719808-08:00",
                    "lastResponse": "2021-03-14T01:54:52.423373-08:00",
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.423384-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423384-08:00"
                        },
                        "[101 200]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719896-08:00",
                            "lastResponse": "2021-03-14T01:54:51.996053-08:00"
                        }
                    }
                },
                "403": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.312474-08:00",
                    "lastResponse": "2021-03-14T01:54:52.312474-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312504-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312504-08:00"
                        }
                    }
                },
                "418": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.22864-08:00",
                    "lastResponse": "2021-03-14T01:54:52.323022-08:00",
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228659-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323032-08:00"
                        }
                    }
                },
                "500": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.048987-08:00",
                    "lastResponse": "2021-03-14T01:54:52.749654-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.049023-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749685-08:00"
                        }
                    }
                },
                "501": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.844725-08:00",
                    "lastResponse": "2021-03-14T01:54:51.844725-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.844745-08:00",
                            "lastResponse": "2021-03-14T01:54:51.844745-08:00"
                        }
                    }
                },
                "502": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.196078-08:00",
                    "lastResponse": "2021-03-14T01:54:51.216049-08:00",
                    "countsByTimeBuckets": {
                        "[501 1000]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.196113-08:00",
                            "lastResponse": "2021-03-14T01:54:51.216077-08:00"
                        }
                    }
                }
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 16,
                    "retries": 0,
                    "header": "goto-host",
                    "countsByValues": {
                        "localhost.local@1.1.1.1:8081": {
                            "count": 16,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719866-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749671-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719866-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423379-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312485-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312485-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228652-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323028-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.049012-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749671-08:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.844735-08:00",
                            "lastResponse": "2021-03-14T01:54:51.844735-08:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.196094-08:00",
                            "lastResponse": "2021-03-14T01:54:51.216065-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "localhost.local@1.1.1.1:8081": {
                            "200": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.719867-08:00",
                                "lastResponse": "2021-03-14T01:54:52.42338-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.312485-08:00",
                                "lastResponse": "2021-03-14T01:54:52.312485-08:00"
                            },
                            "418": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.228653-08:00",
                                "lastResponse": "2021-03-14T01:54:52.323028-08:00"
                            },
                            "500": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.049013-08:00",
                                "lastResponse": "2021-03-14T01:54:52.749672-08:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.844736-08:00",
                                "lastResponse": "2021-03-14T01:54:51.844736-08:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.196096-08:00",
                                "lastResponse": "2021-03-14T01:54:51.216066-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.227133-07:00",
                    "lastResponse": "2021-03-14T03:04:56.227133-07:00"
                },
                "request-from-goto": {
                    "count": 16,
                    "retries": 0,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 16,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719863-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749667-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719862-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423378-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312483-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312483-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228649-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323026-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.048992-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749665-08:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.844732-08:00",
                            "lastResponse": "2021-03-14T01:54:51.844732-08:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.196091-08:00",
                            "lastResponse": "2021-03-14T01:54:51.21606-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.719863-08:00",
                                "lastResponse": "2021-03-14T01:54:52.423378-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.312483-08:00",
                                "lastResponse": "2021-03-14T01:54:52.312483-08:00"
                            },
                            "418": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.228649-08:00",
                                "lastResponse": "2021-03-14T01:54:52.323026-08:00"
                            },
                            "500": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.048993-08:00",
                                "lastResponse": "2021-03-14T01:54:52.749667-08:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.844733-08:00",
                                "lastResponse": "2021-03-14T01:54:51.844733-08:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.196093-08:00",
                                "lastResponse": "2021-03-14T01:54:51.216061-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.227123-07:00",
                    "lastResponse": "2021-03-14T03:04:56.227123-07:00"
                },
                "via-goto": {
                    "count": 16,
                    "retries": 0,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 16,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719858-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749661-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719857-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423376-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312481-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312481-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228645-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323024-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.04899-08:00",
                            "lastResponse": "2021-03-14T01:54:52.74966-08:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.844729-08:00",
                            "lastResponse": "2021-03-14T01:54:51.844729-08:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.196086-08:00",
                            "lastResponse": "2021-03-14T01:54:51.216054-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.719859-08:00",
                                "lastResponse": "2021-03-14T01:54:52.423376-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.312482-08:00",
                                "lastResponse": "2021-03-14T01:54:52.312482-08:00"
                            },
                            "418": {
                                "count": 6,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.228646-08:00",
                                "lastResponse": "2021-03-14T01:54:52.323025-08:00"
                            },
                            "500": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.048991-08:00",
                                "lastResponse": "2021-03-14T01:54:52.749662-08:00"
                            },
                            "501": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.84473-08:00",
                                "lastResponse": "2021-03-14T01:54:51.84473-08:00"
                            },
                            "502": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.196089-08:00",
                                "lastResponse": "2021-03-14T01:54:51.216056-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.227128-07:00",
                    "lastResponse": "2021-03-14T03:04:56.227128-07:00"
                }
            },
            "countsByURIs": {
                "/foo": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.719809-08:00",
                    "lastResponse": "2021-03-14T01:54:52.423374-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.71981-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423374-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.423384-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423384-08:00"
                        },
                        "[101 200]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719895-08:00",
                            "lastResponse": "2021-03-14T01:54:51.996052-08:00"
                        }
                    }
                },
                "/bar": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.312478-08:00",
                    "lastResponse": "2021-03-14T01:54:52.312478-08:00",
                    "countsByStatusCodes": {
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312479-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312479-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312503-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312503-08:00"
                        }
                    }
                },
                "/status/418": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.228641-08:00",
                    "lastResponse": "2021-03-14T01:54:52.323022-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228642-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323023-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228658-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323031-08:00"
                        }
                    }
                },
                "/a": {
                    "count": 5,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.048988-08:00",
                    "lastResponse": "2021-03-14T01:54:52.749656-08:00",
                    "countsByStatusCodes": {
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.048988-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749657-08:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.844727-08:00",
                            "lastResponse": "2021-03-14T01:54:51.844727-08:00"
                        },
                        "502": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.19608-08:00",
                            "lastResponse": "2021-03-14T01:54:51.216052-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.049021-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749684-08:00"
                        },
                        "[501 1000]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.196111-08:00",
                            "lastResponse": "2021-03-14T01:54:51.216076-08:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[0 100]": {
                    "count": 7,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.228656-08:00",
                    "lastResponse": "2021-03-14T01:54:52.423382-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.423383-08:00",
                            "lastResponse": "2021-03-14T01:54:52.423383-08:00"
                        },
                        "418": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.228657-08:00",
                            "lastResponse": "2021-03-14T01:54:52.323031-08:00"
                        }
                    }
                },
                "[101 200]": {
                    "count": 3,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.719881-08:00",
                    "lastResponse": "2021-03-14T01:54:51.996052-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.719882-08:00",
                            "lastResponse": "2021-03-14T01:54:51.996052-08:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.049019-08:00",
                    "lastResponse": "2021-03-14T01:54:52.749683-08:00",
                    "countsByStatusCodes": {
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.312501-08:00",
                            "lastResponse": "2021-03-14T01:54:52.312501-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.04902-08:00",
                            "lastResponse": "2021-03-14T01:54:52.749683-08:00"
                        },
                        "501": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.844743-08:00",
                            "lastResponse": "2021-03-14T01:54:51.844743-08:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.196108-08:00",
                    "lastResponse": "2021-03-14T01:54:51.216075-08:00",
                    "countsByStatusCodes": {
                        "502": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.196108-08:00",
                            "lastResponse": "2021-03-14T01:54:51.216075-08:00"
                        }
                    }
                }
            }
        },
        "t-2": {
            "target": "t-2",
            "invocationCounts": 25,
            "firstResponse": "2021-03-14T01:54:50.615399-08:00",
            "lastResponse": "2021-03-14T01:54:53.47273-08:00",
            "retriedInvocationCounts": 0,
            "countsByStatus": {
                "200 OK": 9,
                "400 Bad Request": 2,
                "401 Unauthorized": 1,
                "402 Payment Required": 2,
                "403 Forbidden": 1,
                "418 I'm a teapot": 2,
                "500 Internal Server Error": 2,
                "501 Not Implemented": 2,
                "503 Service Unavailable": 4
            },
            "countsByStatusCodes": {
                "200": {
                    "count": 9,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.815256-08:00",
                    "lastResponse": "2021-03-14T01:54:53.472731-08:00",
                    "countsByTimeBuckets": {
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.815273-08:00",
                            "lastResponse": "2021-03-14T01:54:51.373726-08:00"
                        },
                        "[301 500]": {
                            "count": 7,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.094609-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472742-08:00"
                        }
                    }
                },
                "400": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.84071-08:00",
                    "lastResponse": "2021-03-14T01:54:53.452839-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.840749-08:00",
                            "lastResponse": "2021-03-14T01:54:53.452851-08:00"
                        }
                    }
                },
                "401": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:51.044504-08:00",
                    "lastResponse": "2021-03-14T01:54:51.044504-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044526-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044526-08:00"
                        }
                    }
                },
                "402": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.966185-08:00",
                    "lastResponse": "2021-03-14T01:54:51.840677-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.966209-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840692-08:00"
                        }
                    }
                },
                "403": {
                    "count": 1,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.246417-08:00",
                    "lastResponse": "2021-03-14T01:54:52.246417-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.246462-08:00",
                            "lastResponse": "2021-03-14T01:54:52.246462-08:00"
                        }
                    }
                },
                "418": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.6154-08:00",
                    "lastResponse": "2021-03-14T01:54:52.403926-08:00",
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615476-08:00",
                            "lastResponse": "2021-03-14T01:54:52.403945-08:00"
                        }
                    }
                },
                "500": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.393071-08:00",
                    "lastResponse": "2021-03-14T01:54:52.866603-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.866624-08:00",
                            "lastResponse": "2021-03-14T01:54:52.866624-08:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393081-08:00",
                            "lastResponse": "2021-03-14T01:54:52.393081-08:00"
                        }
                    }
                },
                "501": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.393035-08:00",
                    "lastResponse": "2021-03-14T01:54:52.963001-08:00",
                    "countsByTimeBuckets": {
                        "[501 1000]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.39306-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963012-08:00"
                        }
                    }
                },
                "503": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.871133-08:00",
                    "lastResponse": "2021-03-14T01:54:53.435768-08:00",
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.871193-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435782-08:00"
                        },
                        "[501 1000]": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.955909-08:00",
                            "lastResponse": "2021-03-14T01:54:52.955909-08:00"
                        }
                    }
                }
            },
            "countsByHeaders": {
                "goto-host": {
                    "count": 25,
                    "retries": 0,
                    "header": "goto-host",
                    "countsByValues": {
                        "localhost.local@1.1.1.1:8081": {
                            "count": 25,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615412-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472737-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.815265-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472736-08:00"
                        },
                        "400": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.84074-08:00",
                            "lastResponse": "2021-03-14T01:54:53.452847-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044515-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044515-08:00"
                        },
                        "402": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.966197-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840686-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.24645-08:00",
                            "lastResponse": "2021-03-14T01:54:52.24645-08:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615412-08:00",
                            "lastResponse": "2021-03-14T01:54:52.403937-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393077-08:00",
                            "lastResponse": "2021-03-14T01:54:52.866613-08:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393054-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963009-08:00"
                        },
                        "503": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.871179-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435776-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "localhost.local@1.1.1.1:8081": {
                            "200": {
                                "count": 9,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.815266-08:00",
                                "lastResponse": "2021-03-14T01:54:53.472737-08:00"
                            },
                            "400": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.840742-08:00",
                                "lastResponse": "2021-03-14T01:54:53.452847-08:00"
                            },
                            "401": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.044516-08:00",
                                "lastResponse": "2021-03-14T01:54:51.044516-08:00"
                            },
                            "402": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.966197-08:00",
                                "lastResponse": "2021-03-14T01:54:51.840687-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.246451-08:00",
                                "lastResponse": "2021-03-14T01:54:52.246451-08:00"
                            },
                            "418": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.615413-08:00",
                                "lastResponse": "2021-03-14T01:54:52.403938-08:00"
                            },
                            "500": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.393078-08:00",
                                "lastResponse": "2021-03-14T01:54:52.866614-08:00"
                            },
                            "501": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.393055-08:00",
                                "lastResponse": "2021-03-14T01:54:52.963009-08:00"
                            },
                            "503": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.871181-08:00",
                                "lastResponse": "2021-03-14T01:54:53.435777-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.226194-07:00",
                    "lastResponse": "2021-03-14T03:04:56.226194-07:00"
                },
                "request-from-goto": {
                    "count": 25,
                    "retries": 0,
                    "header": "request-from-goto",
                    "countsByValues": {
                        "peer2": {
                            "count": 25,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.61541-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472735-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.815263-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472735-08:00"
                        },
                        "400": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.840718-08:00",
                            "lastResponse": "2021-03-14T01:54:53.452844-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044513-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044513-08:00"
                        },
                        "402": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.966194-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840683-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.246447-08:00",
                            "lastResponse": "2021-03-14T01:54:52.246447-08:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.61541-08:00",
                            "lastResponse": "2021-03-14T01:54:52.403934-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393075-08:00",
                            "lastResponse": "2021-03-14T01:54:52.866611-08:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393051-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963006-08:00"
                        },
                        "503": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.871172-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435774-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer2": {
                            "200": {
                                "count": 9,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.815264-08:00",
                                "lastResponse": "2021-03-14T01:54:53.472735-08:00"
                            },
                            "400": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.840719-08:00",
                                "lastResponse": "2021-03-14T01:54:53.452845-08:00"
                            },
                            "401": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.044513-08:00",
                                "lastResponse": "2021-03-14T01:54:51.044513-08:00"
                            },
                            "402": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.966194-08:00",
                                "lastResponse": "2021-03-14T01:54:51.840684-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.246448-08:00",
                                "lastResponse": "2021-03-14T01:54:52.246448-08:00"
                            },
                            "418": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.61541-08:00",
                                "lastResponse": "2021-03-14T01:54:52.403934-08:00"
                            },
                            "500": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.393076-08:00",
                                "lastResponse": "2021-03-14T01:54:52.866611-08:00"
                            },
                            "501": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.393052-08:00",
                                "lastResponse": "2021-03-14T01:54:52.963007-08:00"
                            },
                            "503": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.871174-08:00",
                                "lastResponse": "2021-03-14T01:54:53.435774-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.226205-07:00",
                    "lastResponse": "2021-03-14T03:04:56.226205-07:00"
                },
                "via-goto": {
                    "count": 25,
                    "retries": 0,
                    "header": "via-goto",
                    "countsByValues": {
                        "peer1": {
                            "count": 25,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615408-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472734-08:00"
                        }
                    },
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.81526-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472734-08:00"
                        },
                        "400": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.840714-08:00",
                            "lastResponse": "2021-03-14T01:54:53.452842-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044509-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044509-08:00"
                        },
                        "402": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.96619-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840681-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.246422-08:00",
                            "lastResponse": "2021-03-14T01:54:52.246422-08:00"
                        },
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615407-08:00",
                            "lastResponse": "2021-03-14T01:54:52.40393-08:00"
                        },
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393073-08:00",
                            "lastResponse": "2021-03-14T01:54:52.866607-08:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393048-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963004-08:00"
                        },
                        "503": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.871164-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435772-08:00"
                        }
                    },
                    "countsByValuesStatusCodes": {
                        "peer1": {
                            "200": {
                                "count": 9,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.815261-08:00",
                                "lastResponse": "2021-03-14T01:54:53.472734-08:00"
                            },
                            "400": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.840715-08:00",
                                "lastResponse": "2021-03-14T01:54:53.452843-08:00"
                            },
                            "401": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:51.04451-08:00",
                                "lastResponse": "2021-03-14T01:54:51.04451-08:00"
                            },
                            "402": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.966191-08:00",
                                "lastResponse": "2021-03-14T01:54:51.840682-08:00"
                            },
                            "403": {
                                "count": 1,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.246445-08:00",
                                "lastResponse": "2021-03-14T01:54:52.246445-08:00"
                            },
                            "418": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:50.615408-08:00",
                                "lastResponse": "2021-03-14T01:54:52.403931-08:00"
                            },
                            "500": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.393074-08:00",
                                "lastResponse": "2021-03-14T01:54:52.866608-08:00"
                            },
                            "501": {
                                "count": 2,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.393049-08:00",
                                "lastResponse": "2021-03-14T01:54:52.963004-08:00"
                            },
                            "503": {
                                "count": 4,
                                "retries": 0,
                                "firstResponse": "2021-03-14T01:54:52.871167-08:00",
                                "lastResponse": "2021-03-14T01:54:53.435773-08:00"
                            }
                        }
                    },
                    "crossHeaders": {},
                    "crossHeadersByValues": {},
                    "firstResponse": "2021-03-14T03:04:56.226213-07:00",
                    "lastResponse": "2021-03-14T03:04:56.226213-07:00"
                }
            },
            "countsByURIs": {
                "/c": {
                    "count": 9,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.815257-08:00",
                    "lastResponse": "2021-03-14T01:54:53.472731-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 9,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.815257-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472732-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[201 300]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.815272-08:00",
                            "lastResponse": "2021-03-14T01:54:51.373726-08:00"
                        },
                        "[301 500]": {
                            "count": 7,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.094607-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472742-08:00"
                        }
                    }
                },
                "/bar": {
                    "count": 6,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.966187-08:00",
                    "lastResponse": "2021-03-14T01:54:53.45284-08:00",
                    "countsByStatusCodes": {
                        "400": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.840711-08:00",
                            "lastResponse": "2021-03-14T01:54:53.45284-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044506-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044506-08:00"
                        },
                        "402": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.966187-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840678-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.246419-08:00",
                            "lastResponse": "2021-03-14T01:54:52.246419-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 6,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.966208-08:00",
                            "lastResponse": "2021-03-14T01:54:53.45285-08:00"
                        }
                    }
                },
                "/status/418": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.615404-08:00",
                    "lastResponse": "2021-03-14T01:54:52.403927-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615404-08:00",
                            "lastResponse": "2021-03-14T01:54:52.403928-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[0 100]": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615475-08:00",
                            "lastResponse": "2021-03-14T01:54:52.403944-08:00"
                        }
                    }
                },
                "/a": {
                    "count": 8,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.393036-08:00",
                    "lastResponse": "2021-03-14T01:54:53.435769-08:00",
                    "countsByStatusCodes": {
                        "500": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393072-08:00",
                            "lastResponse": "2021-03-14T01:54:52.866605-08:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393045-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963002-08:00"
                        },
                        "503": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.871137-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435769-08:00"
                        }
                    },
                    "countsByTimeBuckets": {
                        "[301 500]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.866623-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435781-08:00"
                        },
                        "[501 1000]": {
                            "count": 4,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393059-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963012-08:00"
                        }
                    }
                }
            },
            "countsByTimeBuckets": {
                "[0 100]": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.615473-08:00",
                    "lastResponse": "2021-03-14T01:54:52.403943-08:00",
                    "countsByStatusCodes": {
                        "418": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.615475-08:00",
                            "lastResponse": "2021-03-14T01:54:52.403944-08:00"
                        }
                    }
                },
                "[201 300]": {
                    "count": 2,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.815271-08:00",
                    "lastResponse": "2021-03-14T01:54:51.373725-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.815272-08:00",
                            "lastResponse": "2021-03-14T01:54:51.373725-08:00"
                        }
                    }
                },
                "[301 500]": {
                    "count": 17,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:50.966206-08:00",
                    "lastResponse": "2021-03-14T01:54:53.472741-08:00",
                    "countsByStatusCodes": {
                        "200": {
                            "count": 7,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.094607-08:00",
                            "lastResponse": "2021-03-14T01:54:53.472741-08:00"
                        },
                        "400": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.840746-08:00",
                            "lastResponse": "2021-03-14T01:54:53.452849-08:00"
                        },
                        "401": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:51.044523-08:00",
                            "lastResponse": "2021-03-14T01:54:51.044523-08:00"
                        },
                        "402": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:50.966206-08:00",
                            "lastResponse": "2021-03-14T01:54:51.840691-08:00"
                        },
                        "403": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.246459-08:00",
                            "lastResponse": "2021-03-14T01:54:52.246459-08:00"
                        },
                        "500": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.866622-08:00",
                            "lastResponse": "2021-03-14T01:54:52.866622-08:00"
                        },
                        "503": {
                            "count": 3,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.871189-08:00",
                            "lastResponse": "2021-03-14T01:54:53.435781-08:00"
                        }
                    }
                },
                "[501 1000]": {
                    "count": 4,
                    "retries": 0,
                    "firstResponse": "2021-03-14T01:54:52.393058-08:00",
                    "lastResponse": "2021-03-14T01:54:52.963012-08:00",
                    "countsByStatusCodes": {
                        "500": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.39308-08:00",
                            "lastResponse": "2021-03-14T01:54:52.39308-08:00"
                        },
                        "501": {
                            "count": 2,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.393058-08:00",
                            "lastResponse": "2021-03-14T01:54:52.963012-08:00"
                        },
                        "503": {
                            "count": 1,
                            "retries": 0,
                            "firstResponse": "2021-03-14T01:54:52.955907-08:00",
                            "lastResponse": "2021-03-14T01:54:52.955907-08:00"
                        }
                    }
                }
            }
        }
    },
    "registry": {}
}

```

</p>
</details>
