
# HTTP Proxy Examples

The examples below are the `http` entries of the `proxy` list of a startup config file (see the [HTTP Proxy README](README.md) for all the fields). The same target definitions, as JSON or YAML, can be sent at run time to `POST /proxy/http/targets/add`, and a proxy is switched on with `POST /proxy/http/enable`. Notes that apply to all the examples:
- A proxy and its targets are disabled unless `enabled: true` is set on them, as in the examples.
- A request is proxied by every enabled target that has a trigger matching it. Within one target a trigger with an exact `uri` is tried before triggers with a `uriPrefix`.
- The response to the caller is a JSON envelope of the upstream responses, keyed by target and endpoint names, unless the traffic config sets `transparent`, `jsonPayload` or `yamlPayload`.
- Names of URI variables (`{version}`) must be lower case. The URI of a trigger is lower-cased when it is registered, so a variable such as `{userId}` is never filled in.
- `vars` constraints on header variables work. Constraints on variables captured from the URI path are not satisfied at present, so the triggers of the examples that use them do not match (see the notes under those examples).

### Simple Proxy with a single endpoint and match on all traffic
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://localhost:8084
              method: GET
              protocol: HTTP/1.1
              authority: goto.uk
              tls: false
              requestCount: 1
              concurrent: 1
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /foo
                - uri: /bar
              endpoints:
                - ep1

```
This target is triggered for requests whose URI starts with `/foo`, or is exactly `/bar`, and the request is forwarded to the upstream as-is by just changing the host URL but retaining the URI, headers and queries. Use `uriPrefix: /` to match all traffic, but be aware that a catch-all trigger also matches the admin API calls to that port (also the ones sent with the `/port={port}/` prefix), which are then proxied instead of served, so the target can no longer be disabled or removed through the APIs; prefer specific prefixes on a port that you still want to configure. Requests that match no trigger are served by goto as usual.



### Match on URI with path variable and header with regex validation
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://localhost:8084
              method: GET
              protocol: HTTP/1.1
              authority: goto.uk
              tls: false
              requestCount: 1
              concurrent: 1
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /foo/{foo}
                  headers:
                    bar: "{bar}"
                  vars:
                    foo:
                      in:
                        - "1"
                        - "2"
                    bar:
                      regex: aa.
              endpoints:
                - ep1

```
This target will be triggered for requests with URI pattern `/foo/<foovalue>/<anything else>`, where `<foovalue>` must be either `1` or `2`, and the request must also carry a header `bar` with value matching regex pattern `aa.`. For example, a request `GET /foo/1/something` with header `bar: aab` will match, while `GET /foo/3/something` will not.

Note: the constraint on `foo`, a variable of the URI path, is not satisfied by any request at present, so this trigger does not match anything. Without the `foo` constraint (keeping `uriPrefix: /foo/{foo}` and the `bar` header constraint) the trigger matches as described for the header.

---

### Match on URI prefix with exact header value
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/v1
                  headers:
                    x-api-version: "v1"
              endpoints:
                - ep1

```
This target matches requests whose URI starts with `/api/v1` and that carry the header `x-api-version` with the exact value `v1`. The full request URI, headers, and query params are forwarded as-is to the upstream.

---

### Match on header presence only (no value check)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /
                  headers:
                    x-debug: ""
              endpoints:
                - ep1

```
Setting a header value to empty string `""` matches on header presence alone regardless of its value. All requests carrying the `x-debug` header (with any value) will be proxied.

---

### Multiple matchAny criteria (OR logic)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/users
                - uriPrefix: /api/accounts
                - uriPrefix: /api/profiles
              endpoints:
                - ep1

```
Multiple entries under `matchAny` use OR logic. A request matching any one of the URI prefixes `/api/users`, `/api/accounts`, or `/api/profiles` will be proxied. Each `matchAny` entry is evaluated independently; the trigger fires as soon as any one matches.

---

### Multiple triggers with different endpoints
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            mcp1:
              url: http://user-service:9091
            mcp2:
              url: http://order-service:9092
          triggers:
            userTrigger:
              matchAny:
                - uriPrefix: /mcp
              endpoints:
                - mcp1
            orderTrigger:
              matchAny:
                - uri: /mcp/server1
              endpoints:
                - mcp2

```
A single target can define multiple endpoints and multiple triggers. Each trigger routes to different endpoints based on the matched URI. Only one trigger of a target is used for a request, and an exact `uri` trigger is tried before the `uriPrefix` ones: a request to `/mcp/server1` goes to `order-service:9092` only (`orderTrigger`), and any other request under `/mcp`, e.g. `/mcp/tools`, goes to `user-service:9091` (`userTrigger`). If two triggers of a target match a request by prefix, which one is used is not predictable.

---

### Fan-out: single trigger invoking multiple endpoints
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            primary:
              url: http://primary-service:9091
            mirror:
              url: http://mirror-service:9092
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/data
              endpoints:
                - primary
                - mirror

```
A trigger listing multiple endpoints fans out the proxied request to all listed endpoints concurrently. The response from all endpoints is collected and returned to the caller as a JSON envelope keyed by target and endpoint names (`{"target1":{"primary":[...],"mirror":[...]}}`), and the `Goto-Proxy-Upstream-Status` response header lists the status of each, e.g. `{"target1":{"mirror":200,"primary":200}}`. The status of the proxy response is the status of the response that was collected last.

---

### URI variable capture forwarded to upstream
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/{version}/resources/{resourceid}
              endpoints:
                - ep1

```
Path variables `{version}` and `{resourceid}` are captured by the router (use lower case names). Since no transform is defined, the request is forwarded to the upstream with the same URI path. A request `GET /api/v2/resources/42/details` matches and is forwarded as `GET http://upstream-service:9090/api/v2/resources/42/details`.

---

### URI variable with exact match validation
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/{version}/data
                  vars:
                    version:
                      exact: "v2"
              endpoints:
                - ep1

```
The `vars` block validates the captured path variable `version` against an exact value. Only requests where `{version}` is exactly `v2` will match. A request to `/api/v2/data` matches, while `/api/v1/data` does not.

Note: at present a constraint on a variable of the URI path is not satisfied by any request, so this trigger does not match `/api/v2/data` either. Constraints on variables captured from headers (see the header example below) work.

---

### URI variable with inRegex validation (multiple patterns)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/{version}/resources/{resourceid}
                  vars:
                    version:
                      in:
                        - "v1"
                        - "v2"
                        - "v3"
                    resourceid:
                      inRegex:
                        - "^[0-9]+$"
                        - "^[a-f0-9-]{36}$"
              endpoints:
                - ep1

```
The `in` constraint allows the `version` variable to be one of `v1`, `v2`, or `v3`. The `inRegex` constraint allows the `resourceid` to match any of the listed patterns—here, either a numeric ID or a UUID. A request to `/api/v2/resources/123/info` matches, as does `/api/v1/resources/550e8400-e29b-41d4-a716-446655440000/info`.

Note: as with the exact validation above, constraints on URI path variables are not satisfied at present, so this trigger does not match.

---

### Header variable capture with regex validation
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/data
                  headers:
                    x-tenant-id: "{tenantId}"
                    x-api-key: "{apiKey}"
                  vars:
                    tenantId:
                      regex: "^tenant-[a-z]+$"
                    apiKey:
                      regex: "^[A-Za-z0-9]{32}$"
              endpoints:
                - ep1

```
Header values wrapped in `{varName}` syntax are captured as variables and validated using the `vars` block. The `x-tenant-id` header value must match `^tenant-[a-z]+$` and the `x-api-key` must be a 32-character alphanumeric string. Both headers must be present and valid for the trigger to match. The captured header values are checked against `vars` but are not available to the transforms (a `{tenantId}` in a header or query transform stays literal).

---

### Transform: URI mapping (rewrite downstream URI to different upstream URI)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/v1/users
                - uriPrefix: /api/v1/accounts
              endpoints:
                - ep1
          transform:
            uriMap:
              /api/v1/users: /internal/user-service/users
              /api/v1/accounts: /internal/account-service/accounts

```
The `uriMap` rewrites the downstream URI to a different upstream URI. A request to `/api/v1/users` is forwarded to `http://upstream-service:9090/internal/user-service/users`. The mapping is keyed on the `uriPrefix` (or `uri`) value of the match, and the whole request path is replaced by the mapped value: a request to `/api/v1/users/42` is also forwarded to `/internal/user-service/users`, the rest of its path is not appended. The lookup order is the full request URI, then the matched URI, then the matched URI followed by `*`. `uriMap` is ignored when `stripURI` is set.

---

### Transform: URI mapping with wildcard
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/v1/users
              endpoints:
                - ep1
          transform:
            uriMap:
              /api/v1/users*: /backend/users

```
When the full request URI and the matched URI are not keys of the `uriMap`, the proxy tries the matched URI with `*` appended (`/api/v1/users*` here; a key with `/*`, such as `/api/v1/users/*`, is never looked up). A request to `/api/v1/users/42/profile` will match the trigger via `uriPrefix: /api/v1/users`, and the wildcard entry `/api/v1/users*` remaps the whole URI to `/backend/users`.

---

### Transform: stripURI to remove a URI segment
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /gateway/api
              endpoints:
                - ep1
          transform:
            stripURI: "/gateway"

```
The `stripURI` value is compiled into a regex `^(.*)(\/gateway)(/.+).*$` and the replacement `$1$3` removes the matched segment. A request to `/gateway/api/v1/users` is forwarded as `http://upstream-service:9090/api/v1/users`. The `/gateway` prefix is stripped and the rest of the path is preserved.

---

### Transform: add and remove headers
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1
          transform:
            headers:
              add:
                x-forwarded-by: "goto-proxy"
                x-upstream-env: "production"
              remove:
                - "x-debug"
                - "x-trace-id"

```
The `headers` transform adds new headers and removes unwanted ones before forwarding upstream. The downstream `x-debug` and `x-trace-id` headers are stripped, and `x-forwarded-by` and `x-upstream-env` are added. All other downstream headers are passed through.

---

### Transform: add headers referencing captured variables
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/{version}/tenants/{tenantid}
              endpoints:
                - ep1
          transform:
            headers:
              add:
                x-api-version: "{version}"
                x-tenant-id: "{tenantid}"

```
Header `add` values can reference captured URI variables using `{varName}` syntax. A request to `/api/v2/tenants/acme/resources` results in the upstream request carrying headers `x-api-version: v2` and `x-tenant-id: acme`, injected from the captured path variables. The variable names have to be in lower case: a variable declared as `{tenantId}` is not filled in and the upstream would get the literal text `{tenantId}`.

---

### Transform: add and remove query parameters
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api/search
              endpoints:
                - ep1
          transform:
            queries:
              add:
                source: "proxy"
                format: "json"
              remove:
                - "debug"
                - "verbose"

```
The `queries` transform adds and removes query parameters. A request to `/api/search?q=test&debug=true&verbose=1` is forwarded as `http://upstream-service:9090/api/search?q=test&source=proxy&format=json`, with `debug` and `verbose` removed and `source` and `format` added.

---

### Transform: requestId injection
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1
          transform:
            requestId:
              send: true
              uuid: true
              header: "x-request-id"

```
The `requestId` configuration generates a UUID and injects it as the `x-request-id` header on the upstream request. This enables end-to-end request tracing across services.

---

### Transform at trigger level (overrides target-level)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            publicTrigger:
              matchAny:
                - uriPrefix: /public/api
              endpoints:
                - ep1
              transform:
                stripURI: "/public"
                headers:
                  add:
                    x-source: "public"
            internalTrigger:
              matchAny:
                - uriPrefix: /internal/api
              endpoints:
                - ep1
              transform:
                stripURI: "/internal"
                headers:
                  add:
                    x-source: "internal"
          transform:
            headers:
              add:
                x-source: "default"

```
When a trigger defines its own `transform`, it overrides the target-level transform entirely. Requests to `/public/api/v1/data` are forwarded as `/api/v1/data` with header `x-source: public`. Requests to `/internal/api/v1/data` are forwarded as `/api/v1/data` with header `x-source: internal`. The target-level transform with `x-source: default` is only used by triggers that do not define their own transform.

---

### TrafficConfig: delay and retries
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1
          trafficConfig:
            delay:
              min: 100ms
              max: 500ms
            retries: 3
            retryDelay:
              min: 1s
              max: 2s
            retryOn:
              - 502
              - 503
              - 504

```
The `trafficConfig` adds a delay between 100ms and 500ms before the upstream requests. The delay is picked once, at random, when the target is added, so every request of the target gets that same delay (the `count` of a delay is not applied). If the upstream responds with 502, 503, or 504, the proxy retries up to 3 times, waiting between retries for a delay picked from 1s to 2s the same way. The delay that was applied is returned in the `Goto-Proxy-Delay` response header.

---

### TrafficConfig at trigger level (overrides target-level)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            fastTrigger:
              matchAny:
                - uriPrefix: /api/fast
              endpoints:
                - ep1
              trafficConfig:
                retries: 1
                retryOn:
                  - 503
            slowTrigger:
              matchAny:
                - uriPrefix: /api/slow
              endpoints:
                - ep1
              trafficConfig:
                delay:
                  min: 2s
                  max: 5s
                retries: 5
                retryDelay:
                  min: 3s
                retryOn:
                  - 502
                  - 503
                  - 504
          trafficConfig:
            retries: 3
            retryOn:
              - 502
              - 503

```
Like transforms, `trafficConfig` at the trigger level overrides the target-level config. The `fastTrigger` uses minimal retries, while `slowTrigger` uses aggressive delays and retries. The target-level `trafficConfig` applies only to triggers that do not define their own.

---

### TrafficConfig: payload collection with clean response
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1
          trafficConfig:
            payload: true
            transparent: true

```
Setting `payload: true` causes the proxy to collect the upstream response body (into the `payload` of each response in the JSON envelope). When `transparent: true`, the proxy returns the first upstream endpoint's response headers and payload as they are to the caller instead of wrapping all responses in a JSON envelope keyed by target and endpoint names (the proxy's own `Goto-Proxy-*` headers are added). This is useful when you want the proxy to act transparently. There is no `clean` flag; `jsonPayload: true` and `yamlPayload: true` return the first upstream payload re-written as JSON or YAML.

---

### Proxy response mapping (translate upstream status to proxy status)
```
proxy:
  - http:
      port: 8080
      enabled: true
      proxyResponses:
        - upResponseRange: [500, 599]
          proxyResponse: 502
        - upResponseRange: [400, 499]
          proxyResponse: 200
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1

```
The `proxyResponses` array defines rules that map upstream response status codes to proxy response status codes. If the upstream returns any 5xx status, the proxy returns 502 to the caller. If the upstream returns any 4xx status, the proxy returns 200. The first matching rule wins, and a status that no rule matches is returned as it is. A range of one value (`[404]`) matches that status only, and `[0, 0]` matches the calls where the upstream could not be reached at all (without a rule for it the proxy answers 200 with an upstream status of 0). The rules can also be set at run time with `POST /proxy/http/responses/set`.

---

### Multiple targets matching the same request (multi-target fan-out)
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        loggingTarget:
          enabled: true
          endpoints:
            logger:
              url: http://log-service:9091
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - logger
        backendTarget:
          enabled: true
          endpoints:
            backend:
              url: http://backend-service:9092
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - backend

```
Multiple targets can match the same incoming request. When both `loggingTarget` and `backendTarget` match a request to `/api/data`, the proxy invokes both targets concurrently. The response is a JSON envelope containing responses from all matched targets keyed by target name and endpoint name, and the status of the proxy response is that of the response collected last.

---

### Concurrent replicas per endpoint
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: http://upstream-service:9090
              requestCount: 3
              concurrent: 2
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1

```
Setting `concurrent: 2` runs 2 parallel replicas of the upstream call, and each replica sends `requestCount: 3` requests one after the other, so each request to the proxy results in 6 upstream requests and 6 responses (all listed under `ep1` of the response envelope). This is useful for load testing or warming up upstream caches.

---

### HTTP/2 upstream with TLS and custom authority
```
proxy:
  - http:
      port: 8080
      enabled: true
      targets:
        target1:
          enabled: true
          endpoints:
            ep1:
              url: https://upstream-service:443
              protocol: HTTP/2
              authority: api.upstream.example.com
              tls: true
          triggers:
            trigger1:
              matchAny:
                - uriPrefix: /api
              endpoints:
                - ep1

```
The endpoint is configured to use HTTP/2 over TLS with a custom `:authority` pseudo-header. This is useful when proxying to backends behind a load balancer that routes based on the Host/authority header.

---

### Full-featured example: URI variables, header capture, transforms, and traffic config
```
proxy:
  - http:
      port: 8080
      enabled: true
      proxyResponses:
        - upResponseRange: [500, 599]
          proxyResponse: 503
      targets:
        apiGateway:
          enabled: true
          endpoints:
            userService:
              url: http://user-service:9091
              protocol: HTTP/1.1
            orderService:
              url: http://order-service:9092
              protocol: HTTP/1.1
          triggers:
            userTrigger:
              matchAny:
                - uriPrefix: /gateway/{version}/users/{userid}
                  headers:
                    x-tenant: "{tenant}"
                  vars:
                    version:
                      in:
                        - "v1"
                        - "v2"
                    userid:
                      regex: "^[0-9]+$"
                    tenant:
                      inRegex:
                        - "^tenant-[a-z]+$"
                        - "^org-[0-9]+$"
              endpoints:
                - userService
              transform:
                stripURI: "/gateway"
                headers:
                  add:
                    x-api-version: "{version}"
                    x-user-id: "{userid}"
                    x-tenant-id: "{tenant}"
                    x-forwarded-by: "goto-proxy"
                  remove:
                    - "x-debug"
                queries:
                  add:
                    source: "proxy"
                  remove:
                    - "verbose"
                requestId:
                  send: true
                  uuid: true
                  header: "x-request-id"
              trafficConfig:
                delay:
                  min: 50ms
                  max: 200ms
                retries: 2
                retryDelay:
                  min: 500ms
                  max: 1s
                retryOn:
                  - 502
                  - 503
                payload: true
                transparent: true
            orderTrigger:
              matchAny:
                - uriPrefix: /gateway/{version}/orders
                  vars:
                    version:
                      exact: "v2"
              endpoints:
                - orderService
              transform:
                uriMap:
                  /gateway/{version}/orders: /internal/orders
                headers:
                  add:
                    x-forwarded-by: "goto-proxy"
              trafficConfig:
                retries: 3
                retryOn:
                  - 503
                payload: true

```
This comprehensive example demonstrates:
- **URI variables** (`{version}`, `{userid}`) with `in`, `regex`, and `inRegex` validation
- **Header variable capture** (`{tenant}`) validated against multiple regex patterns
- **stripURI** to remove `/gateway` prefix before forwarding
- **Header transforms** that inject captured variables and static values, and remove debug headers
- **Query transforms** that add a source param and remove verbose
- **requestId** generation with UUID in a custom header
- **Traffic config** with a delay, retries on specific status codes, and transparent passthrough of the upstream response
- **URI mapping** on a separate trigger to rewrite the entire path
- **Proxy response mapping** to translate upstream 5xx errors to 503
- **Multiple triggers** routing to different endpoints based on URI patterns

Notes on this example as the proxy runs today: the `vars` constraints on the URI variables (`version` and `userid` of `userTrigger`, `version` of `orderTrigger`) are not satisfied, so neither trigger matches as written; the `tenant` header constraint would be enforced, but `{tenant}` is not filled into `x-tenant-id` (it is sent as the literal text), while `{version}` and `{userid}` are filled in. Remove the URI `vars` constraints to have the triggers match.
