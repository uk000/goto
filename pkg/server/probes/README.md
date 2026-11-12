
# Probes

This feature allows setting readiness and liveness probe URIs, statuses to be returned for those probes, and tracking counts for how many times the probes have been called. `Goto` also tracks when the probe call counts overflow, keeping separate overflow counts. A `goto` instance can be queried for its probe details via `/probes` API.

Probe settings are kept per listener port: each port has its own probe URIs, statuses and counts, and a port gets the defaults the first time it is used. An API call configures the port that it is made on (or the port in a `/port={port}/...` prefix).

The probe URIs response includes the request headers echoed back with `Readiness-Request-` or `Liveness-Request-` prefixes, and include the following additional headers:

- `Readiness-Request-Count` and `Readiness-Overflow-Count` for `readiness` probe calls
- `Liveness-Request-Count` and `Liveness-Overflow-Count` for `liveness` probe calls

By default, liveness probe URI is set to `/live` and readiness probe URI is set to `/ready`.

When the server receives a stop signal, it waits for the shutdown delay (`--shutdownDelay` [startup argument](../../../cmd/README.md), default `1s`) to serve existing traffic. During this period, a request to the readiness probe URI of its port is answered with status `404` and with the request headers echoed back as `Stopping-Readiness-Request-*`.

> &#x1F4DD; <small><i>Known issue: on the HTTP listeners of this version, a request to a probe URI is not intercepted by the probe middleware. `GET /ready` is answered by the catch-all (status 200, the echo body) whatever status was configured, the `Readiness-Request-*`, `...-Count` and `...-Overflow-Count` headers are not added, and the counts in `GET /probes` stay `0`. The configuration APIs below work and their values are reported correctly; the configured status and the counting are applied to gRPC calls only.</i></small>

#### Probes APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|PUT, POST| /probes/readiness<br/>/set?uri=`{uri}` | Set readiness probe URI. Also clears its counts. If not explicitly set, the readiness URI is set to `/ready`.  |
|PUT, POST| /probes/liveness<br/>/set?uri=`{uri}` | Set liveness probe URI. Also clears its counts If not explicitly set, the liveness URI is set to `/live`. |
|PUT, POST| /probes/readiness<br/>/set/status=`{status}` | Set HTTP response status to be returned for readiness URI calls. Default 200. |
|PUT, POST| /probes/liveness<br/>/set/status=`{status}` | Set HTTP response status to be returned for liveness URI calls. Default 200. |
|POST| /probes/counts/clear               | Clear probe counts URIs |
|GET      | /probes                    | Get current config and counts for both probes |

- `{uri}`: the query parameter `uri` is required, and it is stored in lower case. The request URI of a probe call is compared with it ignoring the case, and in full: it includes the query string, so a probe URI of `/ready` does not match `/ready?x=1`.
- `{status}`: `{code}` or `{code}:{count}`. `{count}` is the number of probe calls that get `{code}`; after that the status goes back to `200`. Without a count the status stays until it is changed. A list of codes (`503,504`) is accepted but only the first code is used. A value that has no valid code is rejected. The call does not change the counts.
- `{type}` in the URIs is `readiness` or `liveness` (not case sensitive). Any other value is rejected with `400`.
- Both `PUT` and `POST` work on the `set` APIs. `/probes` and `/probes/counts/clear` take `GET` and `POST` respectively only (anything else is a `404`).

|API|Status|Body|
|---|---|---|
| set?uri | 200 | `Port [20080] Probe [readiness] URI [/myready] added, count reset` |
| set?uri | 404 | the `uri` query parameter is missing (the route does not match; plain `404 page not found`) |
| set?uri, set/status | 400 | `Cannot add. Invalid probe type` |
| set/status | 200 | `Port [20080] Probe [readiness] Status [503] set` or, with a count, `Port [20080] Probe [readiness] Status [503] set with remaining count [3]` |
| set/status | 400 | `Cannot set. Invalid status code` |
| counts/clear | 200 | `Port [20080] Counts Cleared` |
| GET /probes | 200 | JSON, see below |

<br/>
<details>
<summary>Probes JSON Schema (GET /probes)</summary>

The response is the probe settings of the port that the call was made on.

|Field|Data Type|Description|
|---|---|---|
| port | string | The port that the probes belong to |
| readinessProbe | string | Readiness probe URI. Default `/ready` |
| readinessStatus | int | Status that readiness calls get. Default `200` |
| readinessStatusRemainingCount | int | How many more readiness calls get `readinessStatus`: `-1` when the status was set without a count (or after the count ran out), a positive number while a count is running, `0` if the status was never set |
| readinessCount | uint64 | Readiness calls counted since the URI was set or the counts were cleared |
| readinessOverflowCount | uint64 | Number of times `readinessCount` wrapped around |
| livenessProbe | string | Liveness probe URI. Default `/live` |
| livenessStatus | int | Status that liveness calls get. Default `200` |
| livenessStatusRemainingCount | int | As `readinessStatusRemainingCount`, for liveness |
| livenessCount | uint64 | Liveness calls counted |
| livenessOverflowCount | uint64 | Number of times `livenessCount` wrapped around |

```
{
  "port": "20080",
  "readinessProbe": "/myready",
  "readinessStatus": 503,
  "readinessStatusRemainingCount": 3,
  "readinessCount": 0,
  "readinessOverflowCount": 0,
  "livenessProbe": "/live",
  "livenessStatus": 200,
  "livenessStatusRemainingCount": 0,
  "livenessCount": 0,
  "livenessOverflowCount": 0
}
```

</details>

<br/>
<details>
<summary>Probes API Examples</summary>

```
curl -X POST localhost:8080/probes/readiness/set?uri=/ready

curl -X POST localhost:8080/probes/liveness/set?uri=/live

curl -X PUT localhost:8080/probes/readiness/set/status=404

curl -X PUT localhost:8080/probes/liveness/set/status=200

# the next 3 readiness calls get 503, then the status is 200 again
curl -X POST localhost:8080/probes/readiness/set/status=503:3
# Port [8080] Probe [readiness] Status [503] set with remaining count [3]

curl -X POST localhost:8080/probes/counts/clear
# Port [8080] Counts Cleared

curl localhost:8080/probes

# configure the probes of the listener on port 9000 through port 8080
curl -X POST localhost:8080/port=9000/probes/readiness/set/status=500
# Port [9000] Probe [readiness] Status [500] set
```

</details>
