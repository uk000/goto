
# Response Triggers

`Goto` allows targets to be configured that are triggered based on response status. The triggers can be invoked manually for testing, but their real value is when they get triggered based on response status. Even more valuable when the request was proxied to another upstream service, in which case the trigger is based on the response status of the upstream service.

###### <small>* All `/response/...` APIs below are also served under the `/server/response/...` prefix.</small>

#### Triggers APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     |	/response<br/>/triggers/add              | Add a trigger target (replacing any target with the same name). Body is a trigger target JSON. See [Trigger Target JSON Schema](#trigger-target-json-schema). `200 Port [8080] Added trigger: t1`, or `400 Invalid trigger: <reason>` (missing name, invalid `httpTarget`, invalid URI pattern) |
|PUT, POST| /response<br/>/triggers/`{target}`/remove  | Remove a trigger target. `400 No triggers` if the name is unknown |
|PUT, POST| /response<br/>/triggers/`{target}`/enable  | Enable a trigger target. `400` if the name is unknown |
|PUT, POST| /response<br/>/triggers/`{target}`/disable | Disable a trigger target. `400` if the name is unknown |
|POST     |	/response<br/>/triggers/`{targets}`/invoke | Invoke trigger targets by name (comma-separated list) for manual testing. Returns `200` with a JSON array of invocation results (one per HTTP target), or `404 Triggers not found` if no name matches. Counts toward `triggerCount` and `counts` |
|POST     |	/response<br/>/triggers/clear            | Remove all trigger targets of the port |
|GET 	    |	/response<br/>/triggers/counts             | Report invocation counts for all trigger targets, by the response status of the trigger call: `{"t1": {"200": 3}}` |
|GET 	    |	/response<br/>/triggers/pipes              | List the pipe callbacks registered for the trigger targets, e.g. `{"t1": ["pipe1"]}` (`{}` if none) |
|GET 	    |	/response/triggers             | List all trigger targets of the port and the indexes used to match them (see example) |

Behaviour notes:
* The state is per port. `add` replaces an existing target of the same name (and its indexes).
* Triggers run in the background, after the response of a non-admin request has been produced, based on the status that was actually sent (the forced status, if one is configured). For proxied requests it is the upstream status.
* A target is evaluated when the request/response matches it, and fires when it is `enabled` and its `matchCount` is within `[startFrom, stopAt]`. `matchCount` counts every match, including the ones while the target is disabled or outside the range. `startFrom: 0` means from the first match; `stopAt: 0` means no upper limit. `triggerCount` counts the times the target actually fired (including manual `invoke`).
* **`enabled` defaults to `false`.** A target added without `"enabled": true` never fires on traffic (it can still be `invoke`d manually), until `/enable` is called.
* Matching: if the target has `triggerURIs`, the request URI must match one of them (and, when also given, the response status must be one of `triggerStatuses` and the `triggerHeaders` condition must hold). Otherwise, if it has `triggerStatuses`, the status must match (and the `triggerHeaders` condition must hold, if given). A target with only `triggerHeaders` fires when the `triggerHeaders` condition holds.
* The `triggerHeaders` condition is satisfied when **at least one** of the listed headers is present in the request (with the given value, case-insensitively; an empty value accepts any value). Despite the helper being named `MatchAllHeaders`, it is an OR, not an AND (see bug report REQ-21); headers that are absent from the request are skipped, not failed.
* URI patterns are case-insensitive prefix matches on the request URI (including a query string); `{name}` matches one path segment, and a trailing `*` is accepted. An invalid pattern is rejected with `400`.
* Header values of the `httpTarget` can use placeholders: `{request.uri}` becomes the URI of the triggering request, `{request.headers}` its headers as JSON, and `{<header>}` the value of that header from the response that is being sent (left as is if absent).

<br/>
<a name="trigger-target-json-schema"></a>
<details>
<summary>Trigger Target JSON Schema</summary>

|Field|Data Type|Default|Description|
|---|---|---|---|
| name        | string      | required | Name for this target |
| httpTarget  | object      | none | HTTP call to make when the target fires. A target may have no `httpTarget` if it is only used as a pipe source |
| httpTarget.method | string | `GET` | HTTP method to use for this target |
| httpTarget.url    | string | required (`400 url is required`) | URL for the target. A missing scheme is treated as `http://` |
| httpTarget.headers | `map[string]string` | none | request headers to send with this trigger request (placeholders supported, see above) |
| httpTarget.body   | string | "" | request body to send with this trigger request |
| httpTarget.sendID | bool   | false | Whether or not a unique ID be sent with each request. If this flag is set, a query param `x-request-id` will be added to each request, which can help with tracing requests on the target servers |
| pipe        | bool        | false | If true, the target also feeds the pipe callbacks registered for it (see the pipes feature) |
| enabled     | bool        | **false** | Whether or not the trigger is currently active |
| triggerURIs | []string    | none | URI patterns of the requests for which this target will be triggered (see notes above) |
| triggerHeaders | `map[string]string` | none | Request headers (name to value; empty value = any) of which at least one must be present for the target to be triggered |
| triggerStatuses | []int   | none | List of response statuses for which this target will be triggered |
| startFrom   | int         | 0 | Trigger the target after these many occurrences (matches) of the trigger conditions |
| stopAt      | int         | 0 (no limit) | Stop triggering the target after these many occurrences of the trigger conditions |
| matchCount  | int         | (readonly) | Number of times the target's conditions matched |
| triggerCount | int        | (readonly) | Number of times this target has been triggered |

The previous version of this table listed `method`, `url`, `headers`, `body` and `sendID` at the top level, and `triggerOn` and `statusCount`; the current fields are the ones above (`httpTarget.*`, `triggerStatuses`, `matchCount`).

Minimal target (`name` and `httpTarget.url` are the only required fields; add `triggerStatuses`/`triggerURIs` and `enabled` to make it fire on traffic):
```
{"name": "t1", "enabled": true, "triggerStatuses": [503], "httpTarget": {"url": "localhost:8081/echo"}}
```

Rich target:
```
{
  "name": "t1",
  "enabled": true,
  "httpTarget": {
    "method": "POST",
    "url": "http://localhost:8081/response/status/clear",
    "headers": {"X-Orig-URI": "{request.uri}", "X-Trace": "{Goto-Request-Id}"},
    "body": "triggered",
    "sendID": true
  },
  "triggerURIs": ["/api/{id}/orders*"],
  "triggerHeaders": {"x-env": "test"},
  "triggerStatuses": [502, 503],
  "startFrom": 2,
  "stopAt": 3
}
```

</details>

See [Triggers Example](../../../../docs/triggers-example.md)

