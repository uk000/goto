
### gRPC Proxy Startup Config
```
proxy:
  - grpc:
      port: 8888
      enabled: true
      services:
        Goto:
          toService: Goto
          methods:
            echo: echo
          upstream:
            id: grpc-8000
            endpoint: localhost:8000
            authority: localhost
          config:
            delay:
              min: 0s
              max: 0s
```

- `enabled` must be `true`: a gRPC proxy that is not enabled in the config is skipped (`GRPC Proxy [8888] not enabled. Skipping.` in the log).
- The key of each entry under `services` is the proxied (downstream) service, which must be a service known to `goto` (a built-in one, an uploaded proto, or one learned via reflection, see the gRPC server docs). `toService` is the service called at the upstream (defaults to the same service), `methods` maps the proxied method names to the upstream's (omit it to proxy all methods), `upstream.endpoint` is `host:port` of the upstream (plaintext gRPC only, no TLS to the upstream), `upstream.authority` the optional `:authority`, and `config.delay` the delay (`min`, `max`, `count`) applied to every call.
- A proxy for a service name that `goto` also serves locally (e.g. the built-in `Goto`) replaces the local implementation for the whole process, see the note under the APIs below.

#### GRPC Proxy APIs
###### <small>* These APIs apply to the port that the request arrives on, and can be invoked with prefix `/port={port}/...` to configure/read data of one port via another. The port has to be a gRPC port (e.g. started with `--grpcPort` or added as a `grpc` listener): the APIs do not check that, and on a non-gRPC port they configure a proxy that never sees traffic.</small>

|METHOD|URI|Description|
|---|---|---|
| GET |	/proxy/grpc/status | Get GRPC Proxy Details of the port (see the JSON below). |
| POST |	/proxy/grpc/clear   | Clear all GRPC service proxies and tee services of the port and reset its tracker. Answers `200` with an empty body. |
| POST | /proxy/grpc/`{service}`/`{upstream}` | Setup GRPC Proxy for the given service to the given upstream endpoint (`host:port`), expecting the same service to be available at the upstream endpoint. |
| POST | /proxy/grpc/`{service}`/`{upstream}`/delay/`{delay}` | Same as above, with an additional delay to be added to all requests/responses. `{delay}` is a duration or range with an optional count (`100ms`, `100ms-1s`, `1s:5`). An invalid delay is ignored (no delay is set and the call still succeeds). |
| POST | /proxy/grpc/`{service}`/`{upstream}`/tee/`{teeport}` | Same as the first, but also captures a copy of the requests/responses to be replayed for any service that connects to the `teeport` port  |
| POST | /proxy/grpc/`{service}`/`{upstream}`/`{targetService}` | Setup GRPC Proxy for the given service to the given upstream endpoint, to a different service as identified by `targetService`. The `targetService` should accept the same input/output payload spec. |
| POST | /proxy/grpc/`{service}`/`{upstream}`/`{targetService}`/delay/`{delay}` | Same as above, with an additional delay to be added to all requests/responses |
| POST | /proxy/grpc/`{service}`/`{upstream}`/`{targetService}`/tee/`{teeport}` | Same as above, but also captures a copy of the requests/responses to be replayed for any service that connects to the `teeport` port  |

Responses of the setup routes (`text/plain`): `200` `Service [Goto] will be proxied on port [25091] to upstream [localhost:25094] target service [Goto]`; `400` `Service proto not defined [NoSuch]` if the service (or target service) is not known to `goto`; `400` `Failed to setup gRPC proxy for Service [X]on port [N] to upstream [U] target service [T] with error: ...` (unreachable/invalid endpoint, unknown method). Setting up a service again replaces its previous proxy. Setting up a proxy restarts the gRPC server of the instance, so the connections of its gRPC clients are dropped.

Caveat: the proxied service is replaced registry-wide, not only on the port of the proxy. If the service is also served locally (like the built-in `Goto`; `Goto2` and `Goto3` have no implementation, so proxying them takes nothing away), its local handler on all the other gRPC ports stops answering with its real payload after the proxy is set up, and `POST /proxy/grpc/clear` does not restore it (restart the instance). Use a service name that is not otherwise served, e.g. an uploaded proto of the upstream service, or use `{targetService}` with a different name.

```
# gRPC port 9091 of this goto (started with --grpcPort 9091) proxies service 'Goto' of the upstream at localhost:9000
curl -X POST localhost:8080/port=9091/proxy/grpc/Goto/localhost:9000
Service [Goto] will be proxied on port [9091] to upstream [localhost:9000] target service [Goto]

# with a delay of 200ms to 500ms for each call
curl -X POST localhost:8080/port=9091/proxy/grpc/Goto/localhost:9000/delay/200ms-500ms

# proxy calls for service Goto2 to service Goto at the upstream, and tee a copy of the traffic to port 9092.
# (Goto2 and Goto3 have no implementation of their own, so they are good names for a proxied service: calls to them are
# answered by the upstream's Goto, and the local Goto is not touched. Without a proxy, calls to Goto2 are UNIMPLEMENTED.)
curl -X POST localhost:8080/port=9091/proxy/grpc/Goto2/localhost:9000/Goto/tee/9092
grpcurl -plaintext -d '{"text":"hi"}' localhost:9091 Goto2.echo

# See what the proxy did
curl -s localhost:8080/port=9091/proxy/grpc/status | jq

# Remove the proxies of the port
curl -X POST localhost:8080/port=9091/proxy/grpc/clear
```

#### GRPC Proxy Status JSON
`GET /proxy/grpc/status` returns `{"port": N, "enabled": true, "services": {...}, "teeServices": {...}, "tracker": {...}}`:

|Field|Data Type|Description|
|---|---|---|
| port | int | The port of the proxy |
| enabled | bool | Whether the proxy is enabled |
| services | map[string]ServiceProxy | One entry per proxied service, keyed by the proxied service name. `ServiceProxy` has `label`, `port`, `fromService`, `toService`, `methods` (map of proxied method to target method, `{"*": ""}` for all methods), `upstream` (`id`, `endpoint`, `authority`, `activeSessions` and `pastSessions` maps of the proxied calls with their `id`, `downstreamAddr`, `method` and message `log`), and `config.delay` (`min`, `max`, `count`, or null) |
| teeServices | map[string]map[string]SessionLog | Captured call logs by service and method for the tee ports |
| tracker | object | Counters, see below |

### Get Reports
- **GET** `/proxy/grpc/status` (there is no `/proxy/report/grpc`)

#### GRPC Proxy Tracker JSON Schema
|Field|Data Type|Description|
|---|---|---|
| connCount | int  | Number of downstream connections received  |
| connCountByUpstream | map[string]int  | Number of connections per upstream |
| requestCountByUpstream | map[string]int  | Number of requests per upstream |
| requestCountByService | map[string]int  | Number of requests per requested Service |
| requestCountByServiceMethod | map[string]map[string]int  | Number of requests per RPC method for each requested Service |
| responseCountByUpstream | map[string]int  | Number of responses per upstream |
| responseCountByService | map[string]int  | Number of responses per requested Service |
| responseCountByServiceMethod | map[string]map[string]int  | Number of responses per RPC method for each requested Service |
| messageCountByType | map[string]int  | Number of messages by message type (e.g. `Input`, `Output`) |
