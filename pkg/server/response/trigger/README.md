
# Response Triggers

`Goto` allows targets to be configured that are triggered based on response status. The triggers can be invoked manually for testing, but their real value is when they get triggered based on response status. Even more valuable when the request was proxied to another upstream service, in which case the trigger is based on the response status of the upstream service.

#### Triggers APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     |	/server/response<br/>/triggers/add              | Add a trigger target. See [Trigger Target JSON Schema](#trigger-target-json-schema) |
|PUT, POST| /server/response<br/>/triggers/{target}/remove  | Remove a trigger target |
|PUT, POST| /server/response<br/>/triggers/{target}/enable  | Enable a trigger target |
|PUT, POST| /server/response<br/>/triggers/{target}/disable | Disable a trigger target |
|POST     |	/server/response<br/>/triggers/`{targets}`/invoke | Invoke trigger targets by name for manual testing |
|POST     |	/server/response<br/>/triggers/clear            | Remove all trigger targets |
|GET 	    |	/server/response<br/>/triggers/counts             | Report invocation counts for all trigger targets |
|GET 	    |	/server/response/triggers             | List all trigger targets |

<br/>
<details>
<summary>Trigger Target JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| name        | string      | Name for this target |
| method      | string      | HTTP method to use for this target |
| url         | string      | URL for the target. |
| headers     | `[][]string`| request headers to send with this trigger request |
| body        | `string`    | request body to send with this trigger request |
| sendID      | bool        | Whether or not a unique ID be sent with each request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| enabled     | bool        | Whether or not the trigger is currently active |
| triggerOn   | []int       | List of response statuses for which this target will be triggered |
| startFrom   | int         | Trigger the target after these many occurrences of the trigger status codes |
| stopAt      | int         | Stop triggering the target after these many occurrences of the trigger status codes |
| statusCount | int         | (readonly) Number of occurrences of the status codes that this trigger listens on |
| triggerCount | int         | (readonly) Number of times this target has been triggered  |

</details>

<details>
<summary>Triggers Events</summary>

- `Trigger Target Added`
- `Trigger Target Removed`
- `Trigger Target Enabled`
- `Trigger Target Disabled`
- `Trigger Target Invoked`

</details>

See [Triggers Example](docs/triggers-example.md)

