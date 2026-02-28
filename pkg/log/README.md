# Log Package

The log APIs can be used to see the current logging config and turn logging on/off for various components.

|METHOD|URI|Description|
|---|---|---|
| GET   | /log    | Get the current logging config for all components.  |
| POST | /log/server/{enable} | Enable/disable server logging completely |
| POST | /log/admin/{enable} | Enable/disable logging of admin calls |
| POST | /log/client/{enable} | Enable/disable client logging completely |
| POST | /log/invocation/{enable} | Enable/disable invocation logs |
| POST | /log/invocation<br/>/response/{enable} | Enable/disable logging of response received for each invocation call |
| POST | /log/registry/{enable} | Enable/disable registry logs |
| POST | /log/registry<br/>/locker/{enable} | Enable/disable registry locker logs |
| POST | /log/registry<br/>/events/{enable} | Enable/disable registry events logs |
| POST | /log/registry<br/>/reminder/{enable} | Enable/disable logging of reminder calls that registry receives from peers |
| POST | /log/health/{enable} | Enable/disable logging of health calls that peers receive from registry |
| POST | /log/probe/{enable} | Enable/disable readiness and liveness probe logs |
| POST | /log/metrics/{enable} | Enable/disable metrics logs |
| POST | /log/request<br/>/headers/{enable} | Enable/disable request headers logs |
| POST | /log/request<br/>/minibody/{enable} | Enable/disable request minibody (truncated body) logs (currently only supported for HTTP/1.x requests, not for H/2)  |
| POST | /log/request<br/>/body/{enable} | Enable/disable request body logs (currently only supported for HTTP/1.x requests, not for H/2) |
| POST | /log/response<br/>/headers/{enable} | Enable/disable response headers logs |
| POST | /log/response<br/>/minibody/{enable} | Enable/disable response minibody (truncated body) logs (currently only supported for HTTP/1.x requests, not for H/2) |
| POST | /log/response<br/>/body/{enable} | Enable/disable response body logs (currently only supported for HTTP/1.x requests, not for H/2) |

<details>
<summary>Log API Examples</summary>

```
curl localhost:8080/log

curl -X POST localhost:8080/log/headers/request/y
curl -X POST localhost:8080/log/headers/response/n
curl -X POST localhost:8080/log/invocation/n

```
</details>
