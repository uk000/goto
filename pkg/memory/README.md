# Memory

`Memory` lets a `goto` port remember values it saw in the requests it served, and lets you read them back through an API. Values are kept in named **contexts** (a context is a map of text keys to text values, kept in the process memory of `goto`, per port). A context can be filled in two ways:
- by calling an API that stores a key and a value in it, or
- by **extraction**: you describe what to pick out of the requests (headers, query params, body text matched by a regex, body JSON fields), and a request that carries a context header has those values stored in the context that is named by the header value.

This is not a memory allocator or a cache: there are no APIs to allocate, free, clear or limit memory, and the contexts are never expired or bounded in size (see the notes at the end). A context is reset only by `POST /memory/contexts/add/{context}`.

> **Status note.** On the current code the extraction part does not run for HTTP requests: the memory middleware is on the `Unintercepted` middleware list of `pkg/server/server.go`, whose handlers are never invoked for requests that reach the catch-all (bug INF-01 in the scan report). The configuration and reading APIs below work as described. The extraction examples and their outputs below were run on a build with that wiring fixed, and are marked "(extraction)". Until it is fixed, a request with the context header leaves the memory unchanged.

###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read the memory of one port via another. Each port has its own memory.</small>

## Concepts

|Term|Meaning|
|---|---|
| context | A named bag of `key: value` items, e.g. `user1`. The name `""` (empty) is the default context; in URIs it is written `-`. |
| context key (`ctxkey`) | The name of an **extractor**: a set of rules that says how to recognize the context of a request and what to extract from it. Not the same as the context name. |
| context header | The request header that tells which context a request belongs to. When the extractor `ctxA` has context header `X-Ctx`, a request with `X-Ctx: user1` is extracted into the context `user1`. |

How a request is processed (extraction):
1. The request must be traffic. Calls to goto's own API URIs (including `/echo`) are known non-traffic and are skipped; use any other URI, which is served by the catch-all.
2. The port's memory must have at least one extraction rule configured.
3. The first extractor (in unspecified order, if you have several) whose context header is present on the request is selected. The header value is the context name. The context is created if it doesn't exist.
4. The rules of that extractor are applied: the listed headers, the listed query params, then the body: optional transform, then the regexes, then the JSON paths. Every value that is found is stored under its key (a later value overwrites an earlier one for the same key; a regex that matches several times keeps the last match).
5. Headers and query params that are empty or absent, regex that don't match and JSON paths that don't match are not stored.

## Memory APIs

|METHOD|URI|Description|
|---|---|---|
| POST | /memory/contexts/add/`{context}` | Create the context. If it exists, its items are discarded (reset). |
| POST | /memory/context/`{context}`/add/`{key}`=`{value}` | Store the key and value in the context (created if needed). Both must be non-empty and must not have a `/` in them (the URI does not match otherwise: 404). The split is at the last `=`, so `add/k=v=w` stores key `k=v` with value `w`. |
| POST | /memory/context/`{ctxkey}`/from/header/`{header}` | Set the context header of the extractor `ctxkey`. |
| POST | /memory/context/`{ctxkey}`/from/uri?`uri={uri}` | Registered, but not usable: it always answers 400 `Missing header key` (see notes). |
| POST | /memory/context/`{ctxkey}`/memory/from/header/`{header}` | Extract this request header into the context, under the header name. Can be called many times. |
| POST | /memory/context/`{ctxkey}`/memory/from/query/`{query}` | Extract this query param, under the param name. |
| POST | /memory/context/`{ctxkey}`/memory/from/body/regex/`{key}`=`{regex}` | Extract the text that the regex matches in the request body, under `key`. The regex is case-insensitive. |
| POST | /memory/context/`{ctxkey}`/memory/from/body/paths/`{paths}` | Extract JSON fields of the request body. `{paths}` is a comma-separated list of `key={jsonpath}`. |
| POST | /memory/context/`{ctxkey}`/memory/from/body/transform | Set body transformations that are applied to the body before the regexes and JSON paths. Payload: JSON array of transforms. Replaces the earlier ones. |
| GET | /memory/contexts/memory | Get the items of all the contexts of the port, by context name. |
| GET | /memory/context/`{context}`/memory | Get the items of one context. `{context}` = `-` is the default context, which returns all contexts like the URI above. |
| GET | /memory/context/`{context}`/memory/get/`{key}` | Get the value of one key, as plain text. An unknown key gives an empty line. |
| GET | /memory/contexts | Get the extractors of the port, by context key. |
| GET | /memory/context/`{ctxkey}` | Get one extractor. An unknown key gives an empty response (status 200). |

Reading a context that doesn't exist creates it (empty).

### Responses

The configuration APIs respond with a plain text message and status 200. These are the 400 responses that can happen (the other checks of the handlers, for empty names, can't be reached because the routes need non-empty values):

|Message|When|
|---|---|
| `Missing header key` | `POST .../from/uri?uri=...` (always, see notes) |
| `Failed to add Body Regex match [key: regex] with error: ...` | The regex does not compile, e.g. `body/regex/k=(` |
| `Invalid payload` | The transform payload is not a JSON array of transforms |
| `Invalid payload: invalid transform: null` / `invalid transform: null mapping` | A transform or a mapping is `null` |
| `Invalid payload: invalid condition [...]: ...` | `ifContains` or `ifNotContains` of a mapping is not a valid regex |

An empty array `[]` is accepted and clears the transforms of the extractor.

### Read payloads

`GET /memory/context/{context}/memory` (and every entry of `/memory/contexts/memory`):

|Field|Type|Description|
|---|---|---|
| `context` | string | Always empty in the current code. Use the key of the map in `/memory/contexts/memory`. |
| `items` | map of string to string | The values that were stored |

`GET /memory/context/{ctxkey}` (and every entry of `/memory/contexts`) is an extractor:

|Field|Type|Description|
|---|---|---|
| `contextKey` | string | The name of the extractor (`ctxkey`) |
| `contextHeaderMatch` | string | The context header (set by `from/header/{header}`) |
| `headerMatches` | list of string | Headers to extract |
| `queryMatches` | list of string | Query params to extract |
| `bodyRegexMatches` | map of string to string | `key` to regex source (the regex is compiled case-insensitively) |
| `bodyJSONPathMatches` | list of `{"paths": {key: jsonpath}}` | One entry for each call of `body/paths`; keys come from the `key=` part, or are numbered `1`, `2`, ... when a path has no key |
| `bodyTransformMatches` | list of transforms | The transforms set with `body/transform` |

### Transform payload

`POST /memory/context/{ctxkey}/memory/from/body/transform` takes a JSON array. The transforms use the same schema as the response payload transformations, documented in the [payload README](../server/response/payload/README.md#payload-transformations):

|Field|Type|Description|
|---|---|---|
| `payload` | any JSON | Optional template. When given, the mappings copy values from the request body into a copy of the template, and the result is what the regexes and JSON paths are applied to. |
| `mappings` | list | Each mapping has `source` (dot path in the request body, e.g. `user.name`), `target` (dot path or `{text}` pattern in the template), `ifContains`, `ifNotContains` (regex conditions on the source value), `mode` (`replace`, `join`, `push`, `append`) and `value` (a default value). |

JSON request bodies are transformed as JSON, bodies with a YAML content type are transformed as YAML. A body that is not JSON/YAML is passed on unchanged.

## Examples

Create and read a context (verified on `orig` and the patched build):
```
curl -X POST localhost:8080/memory/contexts/add/session1
Added Context [session1] to memory of port [8080]

curl -X POST localhost:8080/memory/context/session1/add/color=blue
Stored [color: blue] into Context [session1] in memory of port [8080]

curl -X POST localhost:8080/memory/context/session1/add/note=a%20b
Stored [note: a b] into Context [session1] in memory of port [8080]

curl -s localhost:8080/memory/context/session1/memory
{
  "context": "",
  "items": {
    "color": "blue",
    "note": "a b"
  }
}

curl -s localhost:8080/memory/context/session1/memory/get/color
blue
```
A value with a `/` in it can't be stored through `add/{key}={value}`: the route does not match (404), and the body is not used as the value, because the route needs a non-empty value in the URI.

Configure extraction (minimal: pick one header into the context named by another header):
```
curl -X POST localhost:8080/memory/context/ctxA/from/header/X-Ctx
Added Context Key [ctxA] Match on header [X-Ctx] for memory context on port [8080]

curl -X POST localhost:8080/memory/context/ctxA/memory/from/header/X-User
Added Context Key [ctxA] Memory Extraction from Header [X-User] for memory of port [8080]
```

Rich: headers, query param, body regex and body JSON paths together (use `curl -g` so that `[` `]` `{` `}` in the URI are not treated as globs):
```
curl -g -X POST localhost:8080/memory/context/ctxA/memory/from/query/q1
curl -g -X POST 'localhost:8080/memory/context/ctxA/memory/from/body/regex/num=[0-9]+'
curl -g -X POST 'localhost:8080/memory/context/ctxA/memory/from/body/paths/id={.id},nm={.user.name},second={.list[1]}'
```

Send a request (any URI that is not an API URI), then read what was extracted (extraction):
```
curl -s -o /dev/null 'localhost:8080/foo?q1=qv' -H 'X-Ctx: user1' -H 'X-User: bob' \
     -H 'Content-Type: application/json' \
     -d '{"id":"7","user":{"name":"amy"},"list":[10,20,30],"note":"abc 123 and 456"}'

curl -s localhost:8080/memory/contexts/memory
{
  "": {
    "context": "",
    "items": {}
  },
  "user1": {
    "context": "",
    "items": {
      "X-User": "bob",
      "id": "7",
      "nm": "amy",
      "num": "456",
      "q1": "qv",
      "second": "20"
    }
  }
}
```
(The default context `""` is listed because reading `/memory/contexts/memory` creates it. `num` is `456`, the last of the two regex matches.)

Transform the body with a template before the JSON paths are applied (extraction):
```
curl -X POST localhost:8080/memory/context/ctxA/memory/from/body/transform \
     -d '[{"payload":{"customer":"","kind":"order"},"mappings":[{"source":"user.name","target":"customer","mode":"replace"}]}]'
curl -g -X POST 'localhost:8080/memory/context/ctxA/memory/from/body/paths/cust={.customer},kind={.kind}'
```
A request with body `{"user":{"name":"amy"}}` then stores `cust: amy` and `kind: order`.

Read the extractors:
```
curl -s localhost:8080/memory/context/ctxA
{
  "contextKey": "ctxA",
  "contextHeaderMatch": "X-Ctx",
  "headerMatches": ["X-User"],
  "queryMatches": ["q1"],
  "bodyRegexMatches": {"num": "[0-9]+"},
  "bodyJSONPathMatches": [{"paths": {"id": "{.id}", "nm": "{.user.name}", "second": "{.list[1]}"}}],
  "bodyTransformMatches": []
}
```

Another port: `curl localhost:8080/port=8081/memory/contexts/memory` reads the memory of the listener on 8081.

## Notes
- The body JSON paths are applied to a JSON **object** body. A JSON array body, a YAML body and text that is not JSON are skipped by the JSON paths (checked: a YAML body and a JSON array body stored nothing), while the regexes work on the raw (or transformed) text. A path that does not match stores nothing.
- A transform without a `payload` template did not add a new target field to the request body in my test (a `fullname` target was not found by a JSON path afterwards); use a `payload` template as in the example.
- `/memory/context/{ctxkey}/from/uri?uri=...` is registered but the handler only looks for a `{header}` value, so it always answers `400 Missing header key`. There is no URI-based context match.
- `jsonpath` expressions use the Kubernetes JSONPath syntax (`{.user.name}`, `{.list[1]}`). Several paths in one call are separated with commas, so a path that has a comma in it can't be given that way. A `=` in a path splits it as `key=path`.
- There are no limits: an unknown context name in the context header creates a new context for every distinct value, a read of an unknown context creates it, and the body of every extracted request is read in full. Memory grows until the process is restarted (bug INF-07).
- The extraction picks the first extractor whose context header is present; when several extractors are configured with different context headers and a request carries more than one of them, which one applies is not defined.
