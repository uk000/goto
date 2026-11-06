# Log Package

The log APIs can be used to see the current logging config and turn logging on/off for various components. The settings are global to the `goto` instance (not per port), they take effect immediately for the next request, and they are not persisted: a restart goes back to the defaults given by the [startup arguments](../../cmd/README.md).

|METHOD|URI|Description|
|---|---|---|
| GET   | /log    | Get the current logging config for all components.  |
| POST, PUT | /log/server/{enable} | Enable/disable server logging completely |
| POST, PUT | /log/admin/{enable} | Enable/disable logging of admin calls |
| POST, PUT | /log/client/{enable} | Enable/disable client logging completely |
| POST, PUT | /log/invocation/{enable} | Enable/disable invocation logs |
| POST, PUT | /log/invocation<br/>/response/{enable} | Enable/disable logging of response received for each invocation call |
| POST, PUT | /log/registry/{enable} | Enable/disable registry logs |
| POST, PUT | /log/registry<br/>/locker/{enable} | Enable/disable registry locker logs |
| POST, PUT | /log/registry<br/>/reminder/{enable} | Enable/disable logging of reminder calls that registry receives from peers |
| POST, PUT | /log/health/{enable} | Enable/disable logging of health calls that peers receive from registry |
| POST, PUT | /log/probe/{enable} | Enable/disable readiness and liveness probe logs |
| POST, PUT | /log/metrics/{enable} | Enable/disable metrics logs |
| POST, PUT | /log/request<br/>/headers/{enable} | Enable/disable request headers logs |
| POST, PUT | /log/request<br/>/minibody/{enable} | Enable/disable request minibody (truncated body) logs (currently only supported for HTTP/1.x requests, not for H/2). Enabling it turns the full request body logging off. |
| POST, PUT | /log/request<br/>/body/{enable} | Enable/disable request body logs (currently only supported for HTTP/1.x requests, not for H/2). Enabling it turns the request minibody logging off. |
| POST, PUT | /log/request<br/>/headers/ignore/{header} | Never log the given request header (case insensitive) when request headers are logged. There is no API to undo it; a restart does. |
| POST, PUT | /log/response<br/>/headers/{enable} | Enable/disable response headers logs |
| POST, PUT | /log/response<br/>/minibody/{enable} | Enable/disable response minibody (truncated body) logs (currently only supported for HTTP/1.x requests, not for H/2). Enabling it turns the full response body logging off. |
| POST, PUT | /log/response<br/>/body/{enable} | Enable/disable response body logs (currently only supported for HTTP/1.x requests, not for H/2). Enabling it turns the response minibody logging off. |
| POST, PUT | /log/mcp<br/>/verbose={verbose} | Enable/disable verbose logging of MCP activity: which MCP server and tool is chosen for a request on a port (and why), the headers of the requests that the MCP client sends out, and the messages of the MCP client callbacks. Off by default (there is no startup argument for it). `{verbose}` is read like `{enable}`. |

#### Values
- `{enable}` and `{verbose}`: `y`, `yes`, `true` and `1` (not case sensitive) turn the logging on. Any other value (`n`, `no`, `false`, `0`, or a typo such as `maybe`) turns it off, and the call still answers `200`.
- Only `POST` and `PUT` change a setting; `GET` on those URIs answers `404`. `GET /log` is the only read API.

#### Responses
All the APIs answer status `200` with a one-line text:

|URI|Body|
|---|---|
| /log/server/{enable} | `All Server logging set to [true]` |
| /log/admin/{enable} | `All Admin logging set to [true]` |
| /log/client/{enable} | `Client logging set to [true]` |
| /log/invocation/{enable} | `Invocation logging set to [true]` |
| /log/invocation/response/{enable} | `Invocation Response logging set to [true]` |
| /log/registry/{enable} | `All Registry logging set to [true]` |
| /log/registry/locker/{enable} | `Registry Locker logging set to [true]` |
| /log/registry/reminder/{enable} | `Registry Reminder logging set to [true]` |
| /log/health/{enable} | `Health logging set to [true]` |
| /log/probe/{enable} | `Probe logging set to [true]` |
| /log/metrics/{enable} | `Metrics logging set to [true]` |
| /log/request/headers/{enable} | `Request Headers logging set to [true]` |
| /log/request/minibody/{enable} | `Request Mini Body logging set to [true]` |
| /log/request/body/{enable} | `Request Body logging set to [true]` |
| /log/response/headers/{enable} | `Response Headers logging set to [true]` |
| /log/response/minibody/{enable} | `Response Mini Body logging set to [true]` |
| /log/response/body/{enable} | `Response Body logging set to [true]` |
| /log/request/headers/ignore/{header} | `Header [X-Foo] will not be logged` |
| /log/mcp/verbose={verbose} | an empty line (the setting is applied, nothing is confirmed) |

> The handler picks the setting from words in the URI, so keep to the URIs above. For example `POST /log/request/headers/ignore/` (no header name) is taken as `request/headers/{enable}` with the value `ignore`, and turns request header logging off.

<details>
<summary>GET /log response</summary>

JSON object with one boolean per category. The two `request` and `response` entries report the logging of the request and response *headers*. The categories that have no entry here (`invocation/response`, bodies, minibodies, `mcp`) can be set but cannot be read back.

|Field|Set by API|Startup argument|Default|
|---|---|---|---|
| server | `/log/server/{enable}` | `--serverLogs` | true |
| admin | `/log/admin/{enable}` | `--adminLogs` | true |
| client | `/log/client/{enable}` | `--clientLogs` | true |
| invocation | `/log/invocation/{enable}` | `--invocationLogs` | true |
| registry | `/log/registry/{enable}` | `--registryLogs` | true |
| locker | `/log/registry/locker/{enable}` | `--lockerLogs` | false |
| reminder | `/log/registry/reminder/{enable}` | `--reminderLogs` | false |
| health | `/log/health/{enable}` | `--peerHealthLogs` | true |
| probe | `/log/probe/{enable}` | `--probeLogs` | false |
| metrics | `/log/metrics/{enable}` | `--metricsLogs` | true |
| proxy | none (read-only) | `--proxyDebugLogs` | false |
| request | `/log/request/headers/{enable}` | `--logRequestHeaders` | true |
| response | `/log/response/headers/{enable}` | `--logResponseHeaders` | false |

```
$ curl localhost:8080/log
{
  "admin": true,
  "client": true,
  "health": true,
  "invocation": true,
  "locker": false,
  "metrics": true,
  "probe": false,
  "proxy": false,
  "registry": true,
  "reminder": false,
  "request": true,
  "response": false,
  "server": true
}
```

</details>

<details>
<summary>Log API Examples</summary>

```
curl localhost:8080/log

curl -X POST localhost:8080/log/request/headers/y
# Request Headers logging set to [true]
curl -X POST localhost:8080/log/response/headers/n
# Response Headers logging set to [false]
curl -X POST localhost:8080/log/invocation/n
# Invocation logging set to [false]

curl -X PUT localhost:8080/log/request/body/true
# Request Body logging set to [true]

curl -X POST localhost:8080/log/request/headers/ignore/Authorization
# Header [Authorization] will not be logged

curl -X POST localhost:8080/log/mcp/verbose=true
```
</details>
