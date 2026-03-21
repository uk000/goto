
# HTTP Proxy Examples

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
                - uriPrefix: /
              endpoints:
                - ep1

```
This target will be triggered for all requests and the request will be forwarded to the target as-is by just changing the host URL but retaining the URI, headers and queries.



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
            userService:
              url: http://user-service:9091
            orderService:
              url: http://order-service:9092
          triggers:
            userTrigger:
              matchAny:
                - uriPrefix: /api/users
              endpoints:
                - userService
            orderTrigger:
              matchAny:
                - uriPrefix: /api/orders
              endpoints:
                - orderService

```
A single target can define multiple endpoints and multiple triggers. Each trigger routes to different endpoints based on the matched URI. Requests to `/api/users/*` go to `user-service:9091` and requests to `/api/orders/*` go to `order-service:9092`.

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
A trigger listing multiple endpoints fans out the proxied request to all listed endpoints concurrently. The response from all endpoints is collected and returned to the caller as a JSON envelope keyed by target and endpoint names.

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
                - uriPrefix: /api/{version}/resources/{resourceId}
              endpoints:
                - ep1

```
Path variables `{version}` and `{resourceId}` are captured by the router. Since no transform is defined, the request is forwarded to the upstream with the same URI path. A request `GET /api/v2/resources/42/details` matches and is forwarded as `GET http://upstream-service:9090/api/v2/resources/42/details`.

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
                - uriPrefix: /api/{version}/resources/{resourceId}
                  vars:
                    version:
                      in:
                        - "v1"
                        - "v2"
                        - "v3"
                    resourceId:
                      inRegex:
                        - "^[0-9]+$"
                        - "^[a-f0-9-]{36}$"
              endpoints:
                - ep1

```
The `in` constraint allows the `version` variable to be one of `v1`, `v2`, or `v3`. The `inRegex` constraint allows the `resourceId` to match any of the listed patterns—here, either a numeric ID or a UUID. A request to `/api/v2/resources/123/info` matches, as does `/api/v1/resources/550e8400-e29b-41d4-a716-446655440000/info`.

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
Header values wrapped in `{varName}` syntax are captured as variables and validated using the `vars` block. The `x-tenant-id` header value must match `^tenant-[a-z]+$` and the `x-api-key` must be a 32-character alphanumeric string. Both headers must be present and valid for the trigger to match.

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
The `uriMap` rewrites the downstream URI to a different upstream URI. A request to `/api/v1/users` is forwarded to `http://upstream-service:9090/internal/user-service/users`. The mapping is keyed on the `uriPrefix` value from the match.

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
              /api/v1/users/*: /backend/users

```
When the exact `uriMap` key does not match, the proxy tries the key with `/*` appended. A request to `/api/v1/users/42/profile` will match the trigger via `uriPrefix: /api/v1/users`, and the uriMap wildcard entry `/api/v1/users/*` remaps the URI to `/backend/users`.

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
                - uriPrefix: /api/{version}/tenants/{tenantId}
              endpoints:
                - ep1
          transform:
            headers:
              add:
                x-api-version: "{version}"
                x-tenant-id: "{tenantId}"

```
Header `add` values can reference captured URI variables using `{varName}` syntax. A request to `/api/v2/tenants/acme/resources` results in the upstream request carrying headers `x-api-version: v2` and `x-tenant-id: acme`, injected from the captured path variables.

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
The `trafficConfig` adds a random delay between 100ms and 500ms before each upstream request. If the upstream responds with 502, 503, or 504, the proxy retries up to 3 times with a random delay between 1s and 2s between retries.

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
            clean: true

```
Setting `payload: true` causes the proxy to collect the upstream response body. When `clean: true`, the proxy returns the first upstream endpoint's response payload directly to the caller instead of wrapping all responses in a JSON envelope keyed by target and endpoint names. This is useful when you want the proxy to act transparently.

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
The `proxyResponses` array defines rules that map upstream response status codes to proxy response status codes. If the upstream returns any 5xx status, the proxy returns 502 to the caller. If the upstream returns any 4xx status, the proxy returns 200. The first matching rule wins.

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
Multiple targets can match the same incoming request. When both `loggingTarget` and `backendTarget` match a request to `/api/data`, the proxy invokes both targets concurrently. The response is a JSON envelope containing responses from all matched targets keyed by target name and endpoint name.

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
Setting `requestCount: 3` sends 3 sequential requests to the upstream, and `concurrent: 2` runs 2 of those in parallel. This is useful for load testing or warming up upstream caches.

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
                - uriPrefix: /gateway/{version}/users/{userId}
                  headers:
                    x-tenant: "{tenant}"
                  vars:
                    version:
                      in:
                        - "v1"
                        - "v2"
                    userId:
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
                    x-user-id: "{userId}"
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
                clean: true
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
- **URI variables** (`{version}`, `{userId}`) with `in`, `regex`, and `inRegex` validation
- **Header variable capture** (`{tenant}`) validated against multiple regex patterns
- **stripURI** to remove `/gateway` prefix before forwarding
- **Header transforms** that inject captured variables and static values, and remove debug headers
- **Query transforms** that add a source param and remove verbose
- **requestId** generation with UUID in a custom header
- **Traffic config** with random delay, retries on specific status codes, and clean payload passthrough
- **URI mapping** on a separate trigger to rewrite the entire path
- **Proxy response mapping** to translate upstream 5xx errors to 503
- **Multiple triggers** routing to different endpoints based on URI patterns
