
# Response Delay
This feature allows adding a delay to all requests except bypass URIs and proxy requests. Delay is specified as duration, e.g. 1s. 

Delay is not applied to the following requests:
- `Goto` admin calls and
- Delay API `/delay`

When a delay is applied to a request, the response carries a header `Response-Delay` with the value of the applied delay.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/delay/set/{delay} | Set a delay for non-management requests (i.e. runtime traffic). `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range each time. |
| PUT, POST | /server/response<br/>/delay/clear       | Remove currently set delay |
| GET       |	/server/response/delay             | Get currently set delay |


<br/>
<details>
<summary>Response Delay Events</summary>

- `Delay Configured`
- `Delay Cleared`
- `Response Delay Applied`: generated when a configured response delay is applied to requests not explicitly asking for a delay, i.e. not generated for `/delay` API call.

</details>

<details>
<summary>Response Delay API Examples</summary>

```
curl -X POST localhost:8080/server/response/delay/clear

curl -X PUT localhost:8080/server/response/delay/set/1s-3s

curl localhost:8080/server/response/delay
```

</details>

# Delay
The URI `/delay/{delay}` allows clients to ask for a specific delay to be applied to the current request. The delay API is not subject to the response delay that may be configured for all responses. Calling the URI as `/delay` responds with no delay, and so does the call as `/delay/0`, `/delay/0s`, etc.
When a delay is passed to this API, the response carries a header `Response-Delay` with the value of the applied delay.

> Note: For requesting a delay along with a specific status, check the `/status` API documentation above.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/delay/`{delay}` | Responds after the given delay. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. To apply delay with a specific response status code, see `/status` API above. |

<br/>
<details>
<summary>Delay API Example</summary>

```
curl -I  localhost:8080/delay/2s
curl -v  localhost:8080/delay/100ms-2s
```
</details>
