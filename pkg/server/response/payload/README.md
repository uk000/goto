<a name="toc"></a>

# Response Payload

This feature lets you configure a `goto` instance to respond to arbitrary URIs with custom payloads based on various match criteria. This enables `goto` to be used as a stand-in server in any test scenario. The custom payloads are only applied to runtime traffic (going against arbitrary URIs), not to admin APIs that `goto` exposes for itself.

#### Payload Types
There are 4 possibilities for response payloads:
1. Configure `goto` to generate a random text of any size
2. Upload a custom static payload of any format, including binary content (e.g. JSON, YAML, Image, Zip/Tar, etc.)
3. Configure a payload template and JSON/text transformation rules. `Goto` will apply the transformations against the incoming request and augment the payload template to generate a dynamic payload. 
4. Configure JSON/text transformations to be applied against request body instead of using a custom payload template. The response payload becomes a derivative of the request payload.

#### Request Processing Path
- When `goto` receives a request, it first determines if this is an admin API call or some arbitrary URI. Admin APIs take their pre-determined path to configure goto
- If not an admin API, goto checks if the request is for a known traffic feature offered by `goto`, e.g. `/delay`, probes, etc. These APIs serve specific purpose and don't serve custom responses.
- If the request is not a known API, `goto` checks if the request is meant to be tunneled, proxied, or bypassed. Tunneled and proxied requests get forwarded to their intended upstream destinations, whereas bypassed/ignored requests are responded to with a minimal response.
- If no tunnel or proxy config found for the request, `goto` checks if there is a custom payload and custom response status code configured for the request (based on URI, headers, query params, etc). If found, the custom response/status is served.
- If no custom config is found for the request, the request is served by a `catch-all` response that echoes back some request details along with some useful server info.
- If custom response configuration is found for a request, `goto` applies any defined transformations if needed and responds with the custom payload along with any custom response status.

#### API-based Request Matching Configs
Custom response payload can be set for any of the following request categories, listed in the order of precedence. The highest precedence match gets applied to build the response payload.

0. Payloads configured via `set/matches` (their own `matches` rules; see [Response Payload Spec](#response-payload-spec)) - tried first, the longest URI prefix first
1. URI + headers combination match
2. URI + query combination match
3. URI + body keywords combination match
4. URI + body JSON paths match
5. URI match
6. Headers match
7. Query match
8. `Default` payload, if configured

Each category is tried against all the URI configs that match the request (longest URI first) before the next category is tried. Within a category, header/query names are tried in alphabetical order, and for a name the payload of the exact value wins over the "any value" payload. Header names, query names and values are matched case-insensitively; URIs are matched case-insensitively as prefixes (a request URI may carry a query string).

Requests whose URI contains `/stream` or `/payload` (the stream and ad-hoc payload APIs), admin APIs and known non-traffic URIs (probes, `/health`, `/metrics`, ...) never get a custom payload.

Custom payloads are always served with status `200`. They are answered by the shared URI matcher, which runs before the forced-status and response-delay handling, so `/response/status/set/...`, `/response/status/configure` and `/response/delay/set/...` do **not** apply to a request that is served from a payload (see [`Response Status`](../status/README.md) and the known issue at the end of this file).

Content type: the payload `contentType` is set from the **`ResponseContent-Type`** request header of the `set` API call (this exact, unusual name; `Content-Type` and `Response-Content-Type` are ignored for this purpose). If the header is not given, it defaults to `plain/text` for the `set/...` APIs (`application/octet-stream` for the `binary` variants or when the call's `Content-Type` is a binary type), and to `application/json` for `transform` and `set/matches`. Responses carry `Content-Type`, `Goto-Payload-Content-Type` and `Goto-Payload-Length` (or `Goto-Payload-Count` for stream payloads).

#### Config Payload for Request Matching
`Goto` can be configured via a YAML (startup) or JSON (API) config that configures a custom response payload for a port for specific URI matches. JSON Schema for response payload config:

```
curl -X POST localhost:8080/server/response/payload/clear

echo

cat token.json
{"alg":"RS256","typ":"JWT"}
{{}}
{
  "scope": "openid full",
  "authorization_details": [],
  "client_id": "goto",
  "guid": "P3FdowzFirAdGheyD31vbnYQNcngiT3K",
  "iss": "{iss}",
  "jti": "A0hNFLdZ",
  "aud": "{aud}",
  "sub": "{sub}",
  "upn": "{sub}",
  "nbf": 1774763490,
  "iat": 1774763610,
  "userid": "fakeuser",
  "win": "1234",
  "exp": 1774806810,
  "memberOf": "{memberOf}",
  "co": "{co}",
  "st": "{st}"
}
{{}}
[SIGNATURE]

jq -Rs '{"payload": .,
  "matches": [
    {
      "uriPrefix": "/token"
    }
  ],
  "capture": {
    "headers": {
      "aud": "{aud}",
      "sub": "{sub}",
      "exp": "{exp}",
      "iss": "{iss}",
      "memberOf": "{memberOf}",
      "co": "{co}",
      "st": "{st}"
    }
  },
  "contentType": "text/plain",
  "parts": {
    "pre": {
      "splitWith": "{{}}"
    },
    "post": {
      "joinWith": ".",
      "base64Encode": [true, true, false],
      sign: [true, true, false]
    }
  },
  "replace": {"=": ""},
  "detectJSON": true,
  "escapeJSON": false
}' token.json | curl -X POST localhost:8080/server/response/payload/set/matches -d @-

echo

curl -X POST localhost:8080/server/response/payload/set/matches -d '{
  "payload": "{token}",
  "matches": [
    {"uriPrefix": "/idp/userinfo.openid"}
  ],
  "capture": {
    "headers": {
      "Authorization": "Bearer {token}"
    }
  }, 
  "contentType": "application/json",
  "parts": {
    "post": {
      "splitWith": ".",
      "base64Decode": [true, true, false],
      "sign": [true, true, false],
      "keep": [false, true, false],
      "joinWith": "."
    }
  },
  "detectJSON": false,
  "escapeJSON": false
}'

curl -s localhost:8080/server/response/payload | jq
echo

echo "Sending Request to get token"

token=$(curl -s localhost:8080/token -H'iss: iss123' -H'aud: aud123' -H'sub: sub123' -H'memberOf:["a", "b", "c"]' -H'co: US' -H'st: CA')

echo $token
echo

echo "Sending Request with bearer token"

curl -s localhost:8080/idp/userinfo.openid -H "Authorization: Bearer $token" | jq
echo
```

URIs can be specified with `*` suffix to capture all requests that match the given prefix. For example, `/foo*` matchs `/foo`, `/fooxyz`, `/foo/xyz`, `/foo/xyz?bar=123`, etc.

#### Default payload with Auto-generation
- A default payload (lowest precedence in the list above) gets applied to all URIs that don't find any other match. 
- API `/server/response<br/>/payload/set/default` is used to upload a default custom payload.
- Alternatively, API `/server/response/payload/set/default/{size}` can be used to ask `goto` to auto-generate a random text of the given size and serve it as default payload.
- If both the above APIs are called to both set a custom default payload as well as set a size for the default payload, the custom payload will be adjusted to match the given size (by either trimming the custom payload or appending more characters to it). 
- Payload size can be a numeric value or use common byte size conventions: `K`, `KB`, `M`, `MB`.
- There is no limit on the payload size as such, it's only limited by the memory available to the `goto` process.

#### Payload transformations 
<details>
<summary> &#x1F525; Complex stuff, open at your own risk &#x1F525; </summary>
- API `/server/response/payload/transform` allows configuring payload transformation rules for a given URI.
- Transformation can work off `JSON` and `YAML` request payloads. Payload template must be defined in `JSON` format.
- Multiple transformation sets can be defined for a URI, which are applied in sequence until one of them performs an update for the request. 

A transformation definition has two fields: `payload` and `mappings`. 
- If a transformation is defined with an accompanying payload, the mappings are used to extract data from request payload and applied to this payload, and this payload is served as response.
- If a transformation is defined without a payload, the mappings are used to transform the request payload and the request payload is served back as response.

Each transformation spec can contain multiple mappings. A mapping carries the following fields:
- `source`: This field contains a path separated by `.` that identifies a field in the request payload. For arrays, numeric indexes can be used as a key in the path. For example, path `a.0.b.1` means `payload["a"][0]["b"][1]`. This field is required for the transformation to take effect. A mapping with missing `source` is ignored.
- `target`: This field is optional, and if not given then `source` path is also used as target. The `target` field has dual behavior:
    - If it contains a dot-separated path, it identifies the target field where the value extracted from the source field is applied
    - If it contains a capture pattern using syntax `{text}`, all occurrences of `text` in the target payload are replaced with the value extracted from the source field. A pattern of `{{text}}` causes it to look for and replace all occurrences of `{text}`
- `ifContains` and `ifNotContains`: These fields provide text that is matched against the source field, and the mapping is applied only if the source field contains or doesn't contain the given text correspondingly.
- `mode`: This field can contain one the following values to dictate how the source value is applied to the target field. All these modes can cause a change in the field's type.
  - `replace`: replace the current value of the target field with the source value.
  - `join`: join the current value of the target field with the source value using text concatenation (only makes sense for text fields). 
  - `push`: combine the source value with the target field's current value(s) making it an array (if not already an array), inserting the source value before the given index (or head).
  - `append` (default): combine the source value with the target field's current value(s) making it an array (if not already an array), inserting the source value after the given index (or tail).
- `value`: The `value` field provides a default value to use instead of the source value. For request payload transformation (no payload template given), the `value` field is used as primary value and source field is used as fallback value. For payload template transformation, source field is used as primary value and the given value is used as fallback.

</details>

#### Capturing values from the request to use in the response payload
<details>
<summary>&#x1F525; Complex stuff, open at your own risk &#x1F525; </summary>

 To capture a value from URI, Header, Query or Body JSON Path, use the `{var}` syntax in the match criteria as well as in the payload. The occurrences of `{var}` in the response payload will be replaced with the value of that var as captured from the URI/Header/Query/Body. Additionally, `{var}` allows for URIs to be specified such that some ports of the URI can vary.

 For example, for a configured response payload that matches on request URI:
 ```
 curl -g -X POST 'localhost:8080/response/payload/set?uri=/foo/{f}/bar{b}'
  --data '{"result": "uri had foo={f}, bar={b}"}'
 ```
(use `curl -g`, otherwise curl treats the braces as a URL glob). Note: the earlier doc wrote this as `/payload/set/uri?uri=...`, which stores the payload in the *gRPC* store, because any word after `set/` is taken as the `{grpc}` selector.
when a request comes for URI `/foo/hi/bar123`, the response payload will be `{"result": "uri had foo=hi, bar=123"}`

Similarly, for a configured response payload that matches on request header:
```
/server/response/payload/set/header/foo={x} --data '{"result": "header was foo with value {x}"}'
```
when a request comes with header `foo:123`, the response payload will be `{"result": "header was foo with value 123"}`

Same kind of capture can be done on query params, e.g.:
```
/server/response/payload/set/query/qq={v} --data '{"test": "query qq was set to {v}"}'
```


A combination of captures can be done from URI and Header/Query. Below example shows a capture of {x} from URI and {y} from request header:
```
/server/response/payload/set/header/bar={y}?uri=/foo/{x} --data '{"result": "URI foo with {x} and header bar with {y}"}'
```

For a request 
```
curl -H'bar:123' localhost:8080/foo/abc
```
the response payload will be `{"result": "URI foo with abc and header bar with 123"}`

Lastly, values can be captured from JSON paths that match against the request body. For example, the configuration below captures value at JSON paths `.foo.bar` into var `{a}` and at path `.foo.baz` into var `{b}` from the request body received with URI `/foo`. The captured values are injected into the response body in two places for each variable.

```
curl -v -g -XPOST localhost:8080/server/response/payload/set/body/paths/.foo.bar={a},.foo.baz={b}\?uri=/foo --data '{"bar":"{a}", "baz":"{b}", "message": "{a} is {b}"}' -HContent-Type:application/json
```

For the above config, a request like this:
```
curl localhost:8080/foo --data '{"foo": {"bar": "hi", "baz": "hello"}}'
```

produces the response: `{"bar":"hi", "baz":"hello", "message": "hi is hello"}`.

Below is a more complex example, capturing values from arrays and objects, and producing response json with arrays of different lengths based on input array lengths. The two configurations in the below example work in tandem, showing an example of how mutually exclusive configurations can provide on-the-fly `if-else` semantics based on input payload.

First config to capture `.one.two=two`, `.one.three[0]={three.1}`, `.one.three[1]={three.2}`, `.one.four[1].name={four.name}`. This response is triggered if input payload has 2 elements in array `.one.three` and two elements in array `.one.four`.

```
$ curl -g -X POST localhost:8080/server/response/payload/set/body/paths/.one.two={two},.one.three[0]={three.1},.one.three[1]={three.2},.one.four[1].name={four.name}?uri=/foo --data '{"two":"{two}", "three":["{three.1}", "{three.2}"]}, "message": "two -> {two}, three -> {three.1} {three.2}, four -> {four.name}"}' -H'Content-Type:application/json'
```

Second config to capture `.one.two=two`, `.one.three[0]={three.1}`, `.one.three[1]={three.2}`, `.one.three[2]={three.3}`, `.one.four[0].name={four.name}`. This response is triggered if input payload has 3 elements in array `.one.three` and one element in `.one.four`.

```
$ curl -g -X POST localhost:8080/server/response/payload/set/body/paths/.one.two={two},.one.three[0]={three.1},.one.three[1]={three.2},.one.three[2]={three.3},.one.four[0].name={four.name}?uri=/foo --data '{"two":"{two}", "three":["{three.1}", "{three.2}", "{three.3}"]}, "message": "two -> {two}, three -> {three.1} {three.2} {three.3}, four -> {four.name}"}' -H'Content-Type:application/json'
```

For the above two configs, this request:
```
$ curl -v localhost:8080/foo --data '{"one": {"two": "hi", "three": ["hello", "world"], "four":[{"name": "foo"},{"name":"bar"}]}}'
```
 produces the following output: `{"two":"hi", "three":["hello", "world"]}, "message": "two -> hi, three -> hello world, four -> bar"}`

And this request
```
$ curl localhost:8080/foo --data '{"one": {"two": "hi", "three": ["hello", "world", "there"], "four":[{"name": "foo"}]}}'
```
produces the following output: `{"two":"hi", "three":["hello", "world", "there"]}, "message": "two -> hi, three -> hello world there, four -> foo"}`

</details>

### HTTP Response Payload APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another. All `/response/payload/...` URIs are also served as `/server/response/payload/...`.</small>

**Conventions used by all the `set` APIs**
- The payload is the request **body** (any bytes). Use `-H 'ResponseContent-Type: <type>'` to set the response content type (see above).
- All of them reply `200` with a one-line text message, **including when the request is refused** (e.g. invalid regex, missing matches): check the message, failures start with `Failed...` or `Invalid...`.
- `{grpc}` is an optional path segment: `/payload/set/default` configures the HTTP payloads, `/payload/set/grpc/default` configures the gRPC payloads (any word in that position selects the gRPC payloads; use `grpc`). There are no `/server/grpc/response/payload/...` URIs.
- Sending an **empty body** to a URI/header/query (and URI+header/query/body) `set` API removes the payload previously set for that key.
- URIs, header names/values and query names/values are stored lower-cased. A header or query with no `={value}` matches any value. A value of the form `{var}` captures the value into `{var}` instead of matching it, and `{var}` in the payload is replaced by it (not for binary payloads).
- Add `?uri={uri}` where shown to scope the payload to a URI; URI may contain `{var}` placeholders (use `curl -g`) and a `*` suffix.
- Known issue: a `set` call **without** `?uri=` (default, header or query payloads) registers a match-all route on the shared router that is never removed (see bug report REQ-03): afterwards, even after `payload/clear`, non-admin requests that have no matching payload get an empty `200` instead of the echo, and bypass the response delay and forced status, until goto is restarted.

|METHOD|URI|Description|
|---|---|---|
| POST | /response<br/>/payload/set/matches  | Add a custom payload using JSON payload (an object, or an array of objects) with Request Matching and Capture rules specified in the JSON payload. See [Response Payload Spec](#response-payload-spec). `matches` (each with a `uriPrefix`) and `payload` (or `streamPayload`) are required. |
| POST | /response<br/>/payload/set/default  | Add a custom payload to be used for ALL URI responses except those explicitly configured with another payload |
| POST | /response<br/>/payload/set<br/>/default/`{size}`  | Respond with a random generated payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB (decimal: `1K` = 1000). If a body is also sent, it is trimmed or padded with random characters to that size. Sizes are not capped (a huge size panics or exhausts memory). |
| POST | /response<br/>/payload/set<br/>/default/binary  | Add a binary payload to be used for ALL URI responses except those explicitly configured with another payload. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /response<br/>/payload/set<br/>/default/binary/`{size}`  | Respond with a random generated binary payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /response<br/>/payload/set?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI. URI can contain variable placeholders. (Earlier docs wrote `/payload/set/uri?uri=...`: that stores into the gRPC payloads.) |
| POST | /response<br/>/payload/set<br/>/header/`{header}`  | Add a custom payload to be sent for requests matching the given header name |
| POST | /response<br/>/payload/set/header<br/>/`{header}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and the given URI |
| POST | /response<br/>/payload/set/header<br/>/`{header}={value}`  | Add a custom payload to be sent for requests matching the given header name and value |
| POST | /response<br/>/payload/set/header<br/>/`{header}={value}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and value along with the given URI. |
| POST | /response<br/>/payload/set/query/`{q}`  | Add a custom payload to be sent for requests matching the given query param name |
| POST | /response<br/>/payload/set/query<br/>/`{q}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and the given URI |
| POST | /response<br/>/payload/set<br/>/query/`{q}={value}`  | Add a custom payload to be sent for requests matching the given query param name and value |
| POST | /response<br/>/payload/set/query<br/>/`{q}={value}`<br/>?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and value along with the given URI. |
| POST | /response/payload<br/>/set/body~`{regexes}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of regexp (comma-separated list) in the given order (second expression in the list must appear after the first, and so on; case-insensitive). `uri` is required. An invalid regex is refused. |
| POST | /response/payload<br/>/set/body/paths/`{paths}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of JSON paths (comma-separated list, each optionally `.path={var}` to capture the value). Match is triggered only when all JSON paths match, and the first matched config (alphabetical by key) gets applied. `uri` is required. Also see above description and example for how to capture values from JSON paths. |
| POST | /response/payload<br/>/set/stream/count=`{count}`/delay=`{delay}`<br/>[/header/`{header}`][?uri=`{uri}`] | Add a stream payload: the body is expected to be a JSON array, whose items are repeated/cut to exactly `count` items and sent as the response, each item written and flushed separately (items are concatenated without a separator, e.g. `{"a":1}{"a":2}{"a":1}`). Can be scoped to a URI and/or a header name. `Goto-Payload-Count` carries the count. The `delay` is stored (and used by gRPC) but is **not applied** between HTTP chunks (the whole stream is written immediately); use [`/stream`](../../stream/README.md) for delayed chunks. |
| POST | /response<br/>/payload/transform?uri=`{uri}`  | Add payload transformations for requests matching the given URI. Payload submitted with this URI should be a list of `Payload Transformation Schema` (JSON). Replies `200` with the stored transforms; a body that is not valid JSON, or a missing `uri`, gets a `200` message starting `Invalid transformation`. An empty list `[]` removes the payload/transforms currently configured for the URI (the reply text still says "transform paths set"). |
| POST | /response<br/>/payload/transform/delay=`{delay}`?uri=`{uri}`  | Same, with a delay recorded for the payload (not applied to HTTP responses, see stream note) |
| POST | /response<br/>/payload/clear  | Clear all configured custom response payloads (HTTP and gRPC) of the port |
| GET  |	/response/payload | Get configured custom payloads of the port (see [Port Response Payload Config Summary](#port-response-payload-config-summary)) |

<br/>

### gRPC Response Payload APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

The gRPC payload APIs are the same URIs as above with a `grpc` segment after `set/` (or before `transform`); there are no separate `/server/grpc/response/...` routes.

|METHOD|URI|Description|
|---|---|---|
| POST | /response/payload<br/>/set/grpc/default  | Add a custom payload to be used for ALL URI responses except those explicitly configured with another payload |
| POST | /response/payload<br/>/set/grpc/default/`{size}`  | Respond with a random generated payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB |
| POST | /response/payload<br/>/set/grpc/default/binary  | Add a binary payload to be used for ALL URI responses except those explicitly configured with another payload. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /response/payload<br/>/set/grpc/default/binary/`{size}`  | Respond with a random generated binary payload of the given size. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /response/payload<br/>/set/grpc?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI. URI can contain variable placeholders. |
| POST | /response/payload<br/>/set/grpc/header/`{header}`[={value}][?uri=`{uri}`]  | Add a custom payload to be sent for requests matching the given header name (and value, and URI) |
| POST | /response/payload<br/>/set/grpc/query/`{q}`[={value}][?uri=`{uri}`]  | Add a custom payload to be sent for requests matching the given query param name (and value, and URI) |
| POST | /response/payload<br/>/set/grpc/body~`{regexes}`?uri=`{uri}`  | Body keyword match, as for HTTP |
| POST | /response/payload<br/>/set/grpc/body/paths/`{paths}`?uri=`{uri}`  | Body JSON path match, as for HTTP |
| POST | /response/payload<br/>/set/grpc/stream/count=`{count}`/delay=`{delay}`[/header/`{header}`][?uri=`{uri}`]  | Stream payload for gRPC server streaming (the delay applies between messages) |
| POST | /response/payload<br/>/set/grpc/matches  | `matches`-style payload for gRPC |
| POST | /response/payload<br/>/grpc/transform[/delay=`{delay}`]?uri=`{uri}`  | Add payload transformations for requests matching the given URI. Payload submitted with this URI should be `Payload Transformation Schema` |

<br/>


#### <a name="response-payload-spec"></a>Response Payload Spec

Body of `POST /response/payload/set/matches` (a single object, or an array of objects). Defaults shown apply when the field is omitted.

|Field|Type|Default|Description|
|---|---|---|---|
| payload | string (or raw JSON value) | none | Text or binary payload to be returned from goto. Either `payload` or `streamPayload` is required. `{var}` markers are replaced by captured values |
| streamPayload | []string/[]bytes | none | Collection of string/bytes to be streamed back as payload (used when `payload` is empty) |
| contentType | string | `application/json` | Content type to be set for the returned payload |
| matches | []RequestMatch | none, **required** | Collection of request match objects specifying the match rules. Matches are OR-ed; the payload is served when any of them matches |
| capture | RequestCapture | none | Request capture object specifies the headers and queries from which additional values to be captured (in addition to those specified in matches) |
| base64Encode | bool | false | Specifies if the response should be base64 (raw URL alphabet) encoded before delivery |
| base64Decode | bool | false | Specifies if the response should be base64 decoded before delivery |
| detectJSON | bool | false | Specifies if Goto should detect JSON inside Header/Query values, and output those in the response payload as JSONs instead of escaped text. This should be set to true when also encoding json as base64 strings. |
| escapeJSON | bool | false | Specifies if Goto should escape JSON strings with backslash quotes if those contain embedded JSONs. |
| replace | map string:string | none | Text replacements applied to the final payload (e.g. `{"=": ""}` to strip padding) |
| parts | PayloadParts | none | Split the payload in parts (`pre.splitWith`), apply per-part `base64Encode`/`base64Decode` lists, and post-process (`post.splitWith`, `keep`, `base64Encode`, `base64Decode`, `sign` (RSA-SHA256 signature of the flagged parts, substituted for `[SIGNATURE]`), `joinWith`). Each list has one boolean per part. See the JWT-style example above. |
| delay | Delay | none | Delay to record for the payload (min/max/count); only used for gRPC streaming |
| streamCount | int | 0 | If > 0, `payload` must be a JSON array and is repeated/cut into `streamCount` stream items |

Read-only/derived fields reported by `GET /response/payload`: `isStream`, `isBinary`, `isJSON`, `uriMatch`, `headerMatch`, `headerValueMatch`, `queryMatch`, `queryValueMatch`, `bodyMatch`, `bodyPaths`, `uriCaptureKeys`, `headerCaptureKey`, `queryCaptureKey`, `transforms`. Do not send `headerCaptureKey`/`queryCaptureKey` in `set/matches` (they are internal; sending `queryCaptureKey` makes every request that has a query string fail, see bug report REQ-08).

Minimal:
```
curl -X POST localhost:8080/response/payload/set/matches -d '{"payload":"hello","matches":[{"uriPrefix":"/hi"}]}'
```

Rich (captures a URI variable, a header and a query value; matches a literal header):
```
curl -g -X POST localhost:8080/response/payload/set/matches -d '{
  "payload": "hello {n} from {h}, q={qq}",
  "contentType": "text/plain",
  "matches": [
    {"uriPrefix": "/m/{n}", "headers": {"x-h": "{h}", "x-lit": "yes"}, "queries": {"qq": "{qq}"}}
  ]
}'
curl -H 'x-h: HH' -H 'x-lit: yes' 'localhost:8080/m/77?qq=5'      # "hello 77 from HH, q=5"
```
Note: `{var}` in a match's `uriPrefix` is matched but, in the current build, is **not** substituted in the payload (only header/query captures are); use `set?uri=/m/{n}` for URI captures.

#### Response Payload - Request Match Spec

|Field|Type|Default|Description|
|---|---|---|---|
| uriPrefix | string | none, **required** | URI prefix to match (case-insensitive; may contain `{var}` placeholders and a `*` suffix). Required for a match spec.  |
| headers | map string:string | none | Header name to value to be matched on. A value of `{var}` (a filler) captures the header into `var` and only requires the header to be present; any other value must equal the header value (case-insensitive). When specified, all headers must match (AND) along with URL and queries. |
| queries | map string:string | none | Query name to value to be matched on, with the same rules as headers. When specified, all queries must match (AND) along with URL and headers.  |
| bodyRegexes | []string | none | Accepted, but currently **not evaluated** when matching `set/matches` payloads (use the `set/body~...` API for body keyword matches) |


#### Response Payload - Request Capture Spec

|Field|Type|Description|
|---|---|---|
| headers | map string:string | Header name to capture pattern, e.g. `{"Authorization": "Bearer {token}"}`. If the given header name is found in the request, the part of its value matching the pattern is captured under the variable (`token`) |
| queries | map string:string | Query name to capture pattern. If the given query name is found in the request, the value is captured under the given variable name.  |


#### Payload Transformation Schema
A payload transformation is defined by giving one or more path mappings and an optional payload template. The body of `POST /response/payload/transform?uri=` is a JSON array of these.

|Field|Type|Description|
|---|---|---|
| mappings | []JSONTransform | List of mappings to be applied for this transformation. |
| payload | any | If given, this is used as the payload template to be transformed. If not given, the request payload itself is transformed and sent back. |

The transformed request body can be JSON or YAML (YAML is detected from the request `Content-Type`, and answered as YAML).


#### Transformation Mapping Schema

|Field|Type|Default|Description|
|---|---|---|---|
| source | string | required | Path (keys separated by `.`) to be used against source payload (request) to get the source field. A mapping without `source` is ignored |
| target | string | `source` | Path (keys separated by `.`) to be used against target payload (template or request) to get the target field, or a `{text}` / `{{text}}` capture pattern |
| ifContains | string | none | If given, the mapping is applied only if this text exists in the source field |
| ifNotContains | string | none | If given, the mapping is applied only if this text doesn't exist in the source field |
| mode | string | `append` | One of: `replace`, `join`, `push`, `append`. See above for details. |
| value | any | none | Default value. See above for details. |

Minimal (transform the request body, no template):
```
curl -X POST -H'content-type:application/json' 'localhost:8080/response/payload/transform?uri=/tr' \
  --data '[{"mappings":[{"source":"name","target":"who","mode":"replace"}]}]'
```
Rich (template + mappings):
```
curl -X POST -H'content-type:application/json' 'localhost:8080/response/payload/transform?uri=/tr' --data '
[{"payload":{"out":"static","who":"x"},"mappings":[{"source":"name","target":"who","mode":"replace"}]}]'

curl -X POST -d '{"name":"bob"}' localhost:8080/tr
{"out":"static","who":"bob"}
```


#### Port Response Payload Config Summary

This schema is used to describe currently configured response payloads for the port on which the API `/response/payload` is invoked: `{"Port": 8080, "http": ProtoPayloads, "rpc": ProtoPayloads}`, where each `ProtoPayloads` has:

|Field|Type|Description|
|---|---|---|
| defaultPayload | ResponsePayload | Default payload if configured (`null` if not) |
| payloadByURIWithMatches | string->[]ResponsePayload | Payloads configured via `set/matches`, by URI prefix |
| payloadByURIs | string->ResponsePayload | Payloads configured for uri match. Includes both static and request transformation payloads |
| responsePayloadByHeaders | string->string->ResponsePayload | Payloads configured for headers match (header name -> value ("" for any) -> payload) |
| responsePayloadByURIAndHeaders | string->string->string->ResponsePayload | Payloads configured for uri and headers match |
| responsePayloadByQuery | string->string->ResponsePayload | Payloads configured for query params match |
| responsePayloadByURIAndQuery | string->string->string->ResponsePayload | Payloads configured for uri and query params match |
| responsePayloadByURIAndBody | string->string->ResponsePayload | Payloads configured for uri and body keywords/JSON paths match |

(The earlier doc named these `defaultResponsePayload`, `responsePayloadByURIs`, ... : the names above are what the code reports.)


#### Response Payload Config Details

This schema is used to describe currently configured response payload, as the output of `/response/payload`

|Field|Type|Description|
|---|---|---|
| payload | string | Payload to serve when this configuration matches |
| contentType | string | Response content-type to use when this configuration matches |
| uriMatch | string | URI match criteria  |
| headerMatch | string | Header match criteria |
| headerValueMatch | string | Header value match criteria |
| queryMatch | string | Query param name match criteria |
| queryValueMatch | string | Query param value match criteria |
| bodyMatch | []string | Keywords to match against request body for non-transformation response payload configuration |
| bodyPaths | map string:string | JSON paths of a body path match, as `var -> {path}` (e.g. `.foo.bar={a}` is reported as `"a": "{.foo.bar}"`) |
| uriCaptureKeys | []string | Keys to capture values from URI |
| headerCaptureKey | string | Key to capture value from headers |
| queryCaptureKey | string | Key to capture value from query params |
| transforms | []PayloadTransformation | Transformations defined in this config. See `Payload Transformation schema` |
| isStream, isBinary, isJSON | bool | Derived flags |
| delay | Delay | Recorded stream delay (min/max/count) |

Sample `GET /response/payload` after `set?uri=/foo` and a default payload:
```
{
  "Port": 8080,
  "http": {
    "defaultPayload": {"payload": "dflt", "contentType": "plain/text"},
    "payloadByURIWithMatches": {},
    "payloadByURIs": {"/foo": {"payload": "foo", "contentType": "plain/text", "uriMatch": "/foo"}},
    "responsePayloadByHeaders": {},
    "responsePayloadByURIAndHeaders": {},
    "responsePayloadByQuery": {},
    "responsePayloadByURIAndQuery": {},
    "responsePayloadByURIAndBody": {}
  },
  "rpc": { ...same shape... }
}
```


</br>

See [Response Payload API Examples](../../../../docs/response-payload-api-examples.md)


# Ad-hoc Payload
This URI responds with a random-generated payload of the requested size. Payload size can be a numeric value or use common byte size conventions: `K`, `KB`, `M`, `MB` (decimal). Payload size is only limited by the memory available to the `goto` process. The response carries an additional header `Goto-Payload-Length` in addition to the standard header `Content-Length` to identify the size of the response payload. A size that is `0` or unparseable gives the default of 100 bytes. The content type is `plain/text`.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/response/payload/`{size}` | Responds with an auto-generated payload of given size (also served as `/server/response/payload/{size}`) |

Note: this API lives under `/response/payload/{size}`. A request to `/payload/{size}` (without the `/response` prefix), as shown in earlier docs, is not an API: it just gets the catch-all echo response.

<br/>
<details>
<summary>Ad-hoc Payload API Example</summary>

```
curl -v localhost:8080/response/payload/10K

curl -v localhost:8080/response/payload/100

```

</details>

###### <small> [Back to TOC](#toc) </small>
