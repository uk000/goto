
# Response Headers
This feature allows adding custom response headers to all responses sent by the server.

#### APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/headers/add<br/>/`{header}`=`{value}`  | Add a custom header to be sent with all responses |
| PUT, POST | /server/response<br/>/headers/remove/`{header}`       | Remove a previously added custom response header |
| POST      |	/server/response<br/>/headers/clear                 | Remove all configured custom response headers |
| GET       |	/server/response/headers                       | Get list of configured custom response headers |

<br/>
<details>
<summary>Response Headers Events</summary>

- `Response Header Added`
- `Response Header Removed`
- `Response Header Cleared`

</details>

<details>
<summary>Response Headers API Examples</summary>

```
curl -X POST localhost:8080/server/response/headers/clear

curl -X POST localhost:8080/server/response/headers/add/x=x1

curl -X POST localhost:8080/server/response/headers/remove/x

curl localhost:8080/server/response/headers
```

</details>
