# Goto K8S Usage Scenarios

# <a name="k8s-traffic-at-startup"></a> Scenario: Run dynamic traffic from K8s pods at startup

What if you want to test traffic flowing to/from pods amid chaos where pods keep dying and getting respawned? Or what if you're testing in the presence of a canary deployment tool like `flagger`, where canary pods get spawned on the fly and later get terminated, and you need those canary pods to send/receive traffic.

Sending traffic to such ephemeral pods is straight-forward, but triggering traffic from those pods automatically as soon as the pods come up has some challenges. You'd need a container that starts sending traffic as soon as it starts. And then, what if the traffic has to be controlled dynamically based on your current testing scenario, . 

Now it gets tricky, because you need to be able to tell the pod where to send the traffic once it's up. But remember, the pod is not even up yet, flagger is still in the middle on spawning your canary pods. Or perhaps K8s is in the middle of recycling the pod. What do you do now? Keep polling K8s for the pod availability, and once the pod is available, connect to it by IP and push configuration to the container there so that it can start sending traffic per the current testing requirement? This is exactly what `goto` can do, but that too automatically without any manual intervention. How? Glad that you asked.

`Goto` has a `Registry` feature where one or more `goto` instances can act as configuration storage for other instances. The worker `goto` instances are configured to connect to the registry instance(s) at startup. You can configure traffic details at registry, to be passed to all pods based on their labels. As soon as a `goto` worker container comes up and connects to the registry, the registry sends to the worker its assigned traffic workload, some/all of which may be configured to be run automatically. As soon as a `goto` instance receives traffic config that's meant to be auto invoked, it starts running that traffic like there's no tomorrow.

Let's see the APIs involved to achieve this scenario:

1. Let's run one goto instance as registry. We run it on port 8000 and give it a label `registry` just for run. This instance is not meant to send/receive real traffic. Note that there's nothing special about a registry instance, any `goto` instance can act as a registry. Let's assume this instance is available at `http://goto-registry`
   ```
   $ goto --port 8000 --label registry
   ```
2. On this registry instance, we configure some traffic that would be sent to any `goto` instances that connect to registry with a specific label `peer1`.
   ```
   $ curl -s http://goto-registry/registry/peers/peer1/targets/add --data '
      { 
      "name": "t1",
      "method":	"POST",
      "url": "http://somewhere/foo",
      "headers":[["x", "x1"],["y", "y1"]],
      "body": "{\"test\":\"this\"}",
      "replicas": 2,
      "requestCount": 200, 
      "delay": "50ms", 
      "sendID": false,
      "autoInvoke": true
      }'
   ```
   Traffic workload is defined in terms of `targets`. Each target is a specific endpoint that we need the goto worker instance to send traffic to.    Note that this target `t1` was marked for auto invocation via flag `autoInvoke`.

3. Now the basic registry work is done. Time to configure an instance with label `peer1`. This could be a K8s deployment, but for now we'll just look at simple command line examples. The goto instance is given its label and the URL of the registry it must connect to via command args.
   ```
   $ goto --port 8080 --label peer1 --registry http://goto-registry 
   ```
   This instance is told that it's called `peer1` and it should talk to registry at `http://goto-registry` for further instructions.
   Translating the above to a K8S deployment spec would be easy:
   ```
         containers:
        - image: uk0000/goto:latest
          args: ["--port", "8080", "--label", "peer1", "--registry", "http://goto-registry"]
          name: goto
          ports:
            - containerPort: 8080
   ```
   Let's assume that this `goto` instance is available at `http://goto-peer1`.

4. Once `peer1` pods are up and running, we can check what traffic workload each pod has received from the registry.
   ```
   $ curl http://goto-peer1/client/targets
   ```
   The above API call should show the target t1 that we configured at the registry. Since the target `t1` was marked for auto invocation, it must have already launched the traffic and started collecting results. Let's check the `peer1` pods for results.
   ```
   $ curl http://goto-peer1/client/results
   ``` 
   If all goes as planned (which it usually does with `goto`), you should see some traffic results.
5. Any new config you add on the registry for is automatically pushed to all worker instances in real-time as well as upon worker startup. And not just client traffic configs, but also jobs. What are jobs? Well, that's the story for another scenario. Or checkout [Job feature documentation](../README.md#jobs-features)


# <a name="k8s-transient-pods"></a> Scenario: Deal with transient pods

On the subject of transient pods that come up and go down randomly (due to chaos testing or canary deployment testing), testing your application's behavior in the presence of such transiency is challenging. Due to canary deployment or K8S HPA scaling, pods may come up and go down at random points non-deterministically. To be able to perform some deterministic testing amidst such non-determinism is a challenge. In order to connect the dots and reason about some behavior of a traffic that originates from a non-deterministic source, and receives response from a non-deterministic destination, you'd (or your test harness would) need to know which K8S pod started at what point and shut down at which point.

If `goto` is added as a container to your deployment (or `goto` was the primary container for testing purpose), the `goto` instances can help you record the timeline of your pod lifecycles and correlate it with the test results. 

When a `goto` instance is told to connect and work with a `goto` registry, the instances reports to the registry at startup, periodic pings (every 5s), and reports at death too. Registry `goto` instance serves as the book-keeper of this info, building a timeline of the lifecycle of pods of a deployment as well as container restarts for a pod. The `/peers` API in the registry instance returns the collective results of all the `goto` instances that connected to it.

Let's see it in action to understand it better. Just like previous scenario, we have a registry instance and a `goto` worker instance connected to the registry.

   ```
   $ goto --port 8080 --label registry
   ```
   ```
   $ goto --port 8081 --label peer1 --registry http://goto-registry:8080
   ```

When the `peer1` instance comes up, it registers itself with the registry. After this point, the peer details can be seen on the registry:
   ```
   $ curl -s http://goto-registry:8080/registry/peers
    {
      "peer1": {
        "name": "peer1",
        "namespace": "local",
        "pods": {
          "172.28.255.164:8081": {
            "name": "peer1-host",
            "address": "1.0.0.1:8081",
            "healthy": true,
            "currentEpoch": {
              "epoch": 0,
              "name": "peer1-host",
              "address": "1.0.0.1:8081",
              "firstContact": "2020-08-06T14:48:26.001994-07:00",
              "lastContact": "2020-08-06T14:48:26.001994-07:00"
            },
            "pastEpochs": null
          }
        },
        "podEpochs": {
          "1.0.0.1:8081": [
            {
              "epoch": 0,
              "name": "peer1-host",
              "address": "1.0.0.1:8081",
              "firstContact": "2020-08-06T14:48:26.001994-07:00",
              "lastContact": "2020-08-06T14:48:26.001994-07:00"
            }
          ]
        }
      }
    }
   ```

For a peer (named `peer1` in the example above), registry records the following details:
- `name`: peer's name (governed by the label passed to the peer at startup, or defaults to peer's hostname)
- `namespace`: K8s namespace when peer is running on k8s, otherwise uses `local` by default
- `pods`: list of instances connected to registry using this peer name. For each instance, it records the IP address, port, host name, flag telling whether this instance was found to be healthy at last interaction. `currentEpoch` and `pastEpochs` record the various lifetimes of this instance. If the instance only connects once, there'll just be one epoch shown as `currentEpoch`. If the instance gets restarted without getting a change to de-registre, and reconnects at startup again with the same IP, the old `currentEpoch` is moved to `pastEpochs`, and new epoch is recorded under `currentEpoch`. For an epoch, there's in index that tells the sequence number of this epoch, while `firstContact` and `lastContact` records when this instance connected for the first time, and the last time this instance reminded registry of its presence.
- `podEpochs`: records all appearances of various instances for a peer name. When an instance de-registers, it's removed from the `pods` list but all its past appearances are kept `podEpochs`. These `podEpochs` stay around until explicitly cleaned using API `/registry/peeers/clear/epochs`.

The peer1 instance keeps pinging the registry, reminding registry of its presence. This way, if registry instance restarted for some reason, the instances re-register with the registry. When an instance goes down, it attempts to de-register from the registry. However, sometimes the instance might be unable to de-register (e.g. due to pod running with envoy sidecar, and the sidecar becoming unavailable or being unable to route traffic). When registry receives the notification from the instance about the imminent shutdown, it removes the peer instance from the `pods` list but the instance details remain in the `podEpochs` list. So `podEpochs` records the complete timeline of all pods/instances of a peer. Another example of the list:

```
{
  "peer1": {
    "name": "peer1",
    "namespace": "local",
    "pods": {
      "1.0.0.1:8081": {
        "name": "peer1-host1",
        "address": "1.0.0.1:8081",
        "healthy": true,
        "currentEpoch": {
          "epoch": 2,
          "name": "peer1-host1",
          "address": "1.0.0.1:8081",
          "firstContact": "2020-08-06T17:57:05.650996-07:00",
          "lastContact": "2020-08-06T17:57:05.650996-07:00"
        },
        "pastEpochs": [
          {
            "epoch": 0,
            "name": "peer1-host1",
            "address": "1.0.0.1:8081",
            "firstContact": "2020-08-06T14:48:26.001994-07:00",
            "lastContact": "2020-08-06T17:46:28.163734-07:00"
          },
          {
            "epoch": 1,
            "name": "peer1-host1",
            "address": "1.0.0.1:8081",
            "firstContact": "2020-08-06T17:46:35.637055-07:00",
            "lastContact": "2020-08-06T17:56:35.688563-07:00"
          }
        ]
      },
      "1.0.0.2:9091": {
        "name": "peer1-host2",
        "address": "1.0.0.2:9091",
        "healthy": true,
        "currentEpoch": {
          "epoch": 1,
          "name": "peer1-host2",
          "address": "1.0.0.2:9091",
          "firstContact": "2020-08-06T17:57:03.412247-07:00",
          "lastContact": "2020-08-06T17:57:03.412247-07:00"
        },
        "pastEpochs": [
          {
            "epoch": 0,
            "name": "peer1-host2",
            "address": "1.0.0.2:9091",
            "firstContact": "2020-08-06T17:56:57.866404-07:00",
            "lastContact": "2020-08-06T17:56:57.866404-07:00"
          }
        ]
      }
    },
    "podEpochs": {
      "1.0.0.1:8081": [
        {
          "epoch": 0,
          "name": "peer1-host1",
          "address": "1.0.0.1:8081",
          "firstContact": "2020-08-06T14:48:26.001994-07:00",
          "lastContact": "2020-08-06T17:46:28.163734-07:00"
        },
        {
          "epoch": 1,
          "name": "peer1-host1",
          "address": "1.0.0.1:8081",
          "firstContact": "2020-08-06T17:46:35.637055-07:00",
          "lastContact": "2020-08-06T17:56:35.688563-07:00"
        },
        {
          "epoch": 2,
          "name": "peer1-host1",
          "address": "1.0.0.1:8081",
          "firstContact": "2020-08-06T17:57:05.650996-07:00",
          "lastContact": "2020-08-06T17:57:05.650996-07:00"
        }
      ],
      "1.0.0.2:9091": [
        {
          "epoch": 0,
          "name": "peer1-host2",
          "address": "1.0.0.2:9091",
          "firstContact": "2020-08-06T17:56:57.866404-07:00",
          "lastContact": "2020-08-06T17:56:57.866404-07:00"
        },
        {
          "epoch": 1,
          "name": "peer1-host2",
          "address": "1.0.0.2:9091",
          "firstContact": "2020-08-06T17:57:03.412247-07:00",
          "lastContact": "2020-08-06T17:57:03.412247-07:00"
        }
      ]
    }
  }
}

```



# <a name="k8s-capture-transient-pod-results"></a> Scenario: Capture results from pods that may terminate anytime

On the subject of transient pods that come up and go down randomly (due to chaos testing or canary deployment testing), another challenge is to collect results from such instances. You could keep polling the pods for results until they go down. However, `goto` as a traffic client can help with this too.

`Regitsry` feature in `Goto` includes a `Locker` feature, which allow worker instances to post results to the `registry` instance in real-time (at configuration periodicity) as the traffic is executed.

Worker instances use the following keys:
- `client` to store the summary results of their target invocations as a client
- `client_<invocation-index>` to store target invocation results per invocation batch
- `job_<jobID>_<run-index>` to store results of each job run

Let's look at the commands/APIs involved. To a worker instance, we need to pass the following command args:
```
$ goto --registry http://goto-registry --locker true
```
Flag `locker` tells the `goto` instance whether or not to also send results to the registry in addition to getting configs from registry. Naturally `registry` URL must also be passed to the instance, as the instance must connect to a registry first before it can even think about storing some results in the locker.

On the registry side, there's nothing specific needed to tell a `goto` instance to act as registry; any `goto` instance can act as a registry. 

To get all locker results for all peers:
```
$ curl http://goto-registry/registry/peer/lockers
```

To get locker results for a peer:
```
$ curl http://goto-registry/registry/peer/peer1/locker
```

To clear locker of a peer:
```
$ curl -X POST http://goto-registry/registry/peer/peer1/locker/clear
```

To clear lockers of all peers at a registry:
```
$ curl -X POST http://goto-registry/registry/peers/lockers/clear
```

<details>
<summary>Sample results from the registry locker</summary>
<p>

```
    {
      "peer1": {
        "client": {
          "Data": "{\"targetInvocationCounts\":{\"t11\":400,\"t12\":400},...",
          "FirstReported": "2020-06-09T18:28:17.877231-07:00",
          "LastReported": "2020-06-09T18:28:29.955605-07:00"
        },
        "client_1": {
          "Data": "{\"targetInvocationCounts\":{\"t11\":400},\"target...",
          "FirstReported": "2020-06-09T18:28:17.879187-07:00",
          "LastReported": "2020-06-09T18:28:29.958954-07:00"
        },
        "client_2": {
          "Data": "{\"targetInvocationCounts\":{\"t12\":400}...",
          "FirstReported": "2020-06-09T18:28:17.889567-07:00",
          "LastReported": "2020-06-09T18:28:29.945121-07:00"
        },
        "job_job1_1": {
          "Data": "[{\"Index\":\"1.1\",\"Finished\":false,\"Data\":{...}]",
          "FirstReported": "2020-06-09T18:28:17.879195-07:00",
          "LastReported": "2020-06-09T18:28:27.529454-07:00"
        },
        "job_job2_2": {
          "Data": "[{\"Index\":\"2.1\",\"Finished\":false,\"Data\":\"1...}]",
          "FirstReported": "2020-06-09T18:28:18.985445-07:00",
          "LastReported": "2020-06-09T18:28:37.428542-07:00"
        }
      },
      "peer2": {
        "client": {
          "Data": "{\"targetInvocationCounts\":{\"t22\":4}...}",
          "FirstReported": "2020-06-09T18:28:19.782433-07:00",
          "LastReported": "2020-06-09T18:28:20.023149-07:00"
        },
        "client_1": {
          "Data": "{\"targetInvocationCounts\":{\"t22\":4}...}",
          "FirstReported": "2020-06-09T18:28:19.91232-07:00",
          "LastReported": "2020-06-09T18:28:20.027295-07:00"
        },
        "job_job1_1": {
          "Data": "[{\"Index\":\"1.1\",\"Finished\":false,\"ResultTime\":\"2020...\",\"Data\":\"...}]",
          "FirstReported": "2020-06-09T18:28:19.699578-07:00",
          "LastReported": "2020-06-09T18:28:22.778416-07:00"
        },
        "job_job2_2": {
          "Data": "[{\"Index\":\"2.1\",\"Finished\":false,\"ResultTime\":\"2020-0...\",\"Data\":\"...}]",
          "FirstReported": "2020-06-09T18:28:20.79828-07:00",
          "LastReported": "2020-06-09T18:28:59.698923-07:00"
        }
      }
    }
```

</p>
</details>



