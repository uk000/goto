# Util Package

This package provides utility functions for common operations.

## REST APIs

This package does not expose REST APIs. It provides utility functions for:
- HTTP request/response handling
- JSON/YAML parsing and serialization
- String manipulation
- Parameter extraction
- Routing helpers
- Logging utilities

The sections below document the helpers whose behaviour shapes the public APIs of the other packages (route registration, URL parameter syntax, body reading). Where a helper has a documented quirk, the API docs that rely on it point here.

## Route registration helpers (`muxRoutesUtil.go`)

All `/request`, `/response`, `/stream` and `/routing` APIs are registered with these helpers on gorilla/mux routers.

|Helper|Behaviour|
|---|---|
| `PathRouter(r, path)` / `PathPrefix(r, path)` | Create a sub-router for `path`; all routes registered on it are also registered on its "co-routers" (this is how every API is served both under its own prefix, e.g. `/response/...`, and under the alias `/server/response/...`, and under `/port={port}/...`). |
| `AddRoute(r, path, f, methods...)` | Register `f` for `path` and for its trailing-slash variant. Optional segments written as `/{x}?` (or `/name={x}?`) are expanded into every concrete path (e.g. `/set/{grpc}?/default` serves `/set/default` and `/set/{grpc}/default`). No methods means any method. |
| `AddRouteQ(r, path, f, query, methods...)` | Route that matches only when the query param `query` is present; its value is bound to the route var `{query}` (readable with `GetStringParam`). |
| `AddRouteQO(r, path, f, query, methods...)` | Same, plus a variant without the query param (param optional). |
| `AddRouteWithMultiQ(r, path, f, sets, methods...)` | One route per cumulative set of query params, most specific first, e.g. `{count},{delay},{chunksize}` registers `(count)`, `(count,delay)` and `(count,delay,chunksize)` (this is what `/stream` uses). A route without query params is registered only if the first set is empty. |
| `GetAltPaths(path, key)` | The expansion of optional segments used by the above. |

Notes and gotchas:
* A path template given to `AddRoute*` must start with `/` (gorilla/mux silently drops a route whose template doesn't, which is why the `/status={status}` forms never worked, see the status README).
* Route registration on the shared `MatchRouter` (used for payload URIs) is serialised against other registrations but not against request serving, and registered routes are never removed.
* Routes are matched in registration order; the first match wins (`/headers/{headers}?uri=` must be registered before `/headers/{headers}`, for instance).

## Parameter helpers (`muxutil.go`)

All read from the matched route's vars (path variables, plus query params registered via `AddRouteQ*`); `GetListParam` also reads the raw query string.

|Helper|Syntax / behaviour|
|---|---|
| `GetStringParam(r, name, default...)` / `GetStringParamValue` | The var, `present=false` if empty. |
| `GetIntParam(r, name, default...)` | Parsed as a base-10 int32; an unparseable value silently becomes `0` (but `present` is true). |
| `GetListParam(r, name)` | Query param, else route var; a single value is split on `,`. Returns `present=false` if nothing (or only an empty first value) was given. |
| `GetStatusParam(r)` | Route var `status` of the form `code[,code]*[:times]`. Non-numeric codes are dropped silently; `times` defaults to `-1` (unlimited) and `0` also means unlimited. `present` is true if at least one numeric code was parsed. **No range check**: callers must validate (100-999) before writing the code to a response, or `net/http` panics. |
| `GetDurationParam(r, name)` (delegates to `types.ParseDurationRange`) | `<low>[-<high>][:<count>]` with Go durations, e.g. `500ms`, `1s-3s`, `1s-3s:5`; bounds are put in order; `ok=false` if a bound does not parse; a non-positive or unparseable count means unlimited (`0`). |
| `GetSizeParam` / `ParseSize(s)` | Size in bytes: plain number, or with a unit suffix `K`/`KB` (x1000) or `M`/`MB` (x1000000); a bare unit means 1 of it (`K` = 1000). Not 1024-based. Parse errors are ignored and the value saturates at the int32 maximum before the multiplier, so e.g. `9999999999M` becomes a huge size (callers that allocate must cap it). |
| `GetBoolParamValue` | `IsYes(value)` semantics, false if absent. |

## Body and payload helpers (`ioutil.go`, `payloadUtil.go`, `json.go`)

* `ReadBytes(r)` / `Read(r)`: read the entire body into memory, with no size limit. Used by the request body middleware for every non-admin HTTP/1 request.
* `ReReader` (`CreateOrGetReReader`, `Rewind`, `Close`, `ReallyClose`): wraps a request/response body so it can be read again. Its `Close` re-arms the reader rather than closing it; the real close is `ReallyClose`.
* `ResponseTracker.Close`: drains the remaining response body before closing (so closing a tracked large/endless response blocks until EOF).
* `ReadJsonPayload`, `ReadJsonOrYamlPayloadFromBody`, `WriteJsonPayload`, `WriteJsonOrYAMLPayload`, `ToJSONText`, `ToPrettyJSONText`: JSON (and YAML) request/response helpers used by all config APIs (invalid JSON gives an error; APIs usually turn it into `400`).
* `JSON`/`JSONValue`/`JSONPath`/`JQ`/`Transform`: path-based JSON reading and transformation used by payload transforms and captures.
* `TransformPayload(source, transforms, isYaml)`: applies JSON transforms to a payload.
* `ExcludeHeader`/`IncludeHeader`/`IsHeaderExcluded` (`httputil.go`): guarded list of header names goto skips when copying/logging headers (`/response/headers/remove` adds to it).

## URI matching (`uriUtil.go`, `regexpUtil.go`)

* `BuildURIRegexp(uri, prefix)` builds the case-insensitive, query-tolerant regexp used for URI matching by triggers (and hooks). `{var}` matches one path segment, a trailing `*` is a glob, and `prefix=true` makes it match any suffix. Other regexp metacharacters in the URI pass through to the regexp (an invalid one gives an error, or in `GetURIRegexp`/`BuildURIMatcher` a panic via `MustCompile`).
* `BuildURIMatcher(uri, prefix, handler)` additionally registers a route for the URI on the shared `MatchRouter`, which is matched before the traffic middleware chain (used by response payloads). An empty URI becomes a match-everything route.
* `Unglob(s)`: strips a trailing `*` and reports whether there was one.
