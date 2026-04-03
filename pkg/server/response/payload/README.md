
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

1. URI + headers combination match
2. URI + query combination match
3. URI + body keywords combination match
4. URI + body JSON paths match
5. URI match
6. Headers match
7. Query match
8. `Default` payload, if configured

#### Config Payload for Request Matching
`Goto` can be configured via a YAML (startup) or JSON (API) config that configures a custom response payload for a port for specific URI matches. JSON Schema for response payload config:

```
curl -X POST localhost:8080/server/response/payload/clear

echo

cat token.json
{
"scope": "openid full",
"authorization_details": [],
"client_id": "someclient",
"guid": "someguid",
"iss": "{iss}",
"jti": "something",
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
  "base64Encode": true,
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
  "base64Decode": true,
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
 /server/response/payload/set/uri?uri=/foo/{f}/bar{b} 
  --data '{"result": "uri had foo={f}, bar={b}"}'
 ```
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
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST | /server/response<br/>/payload/set/matches  | Add a custom payload using JSON payload with Request Matching and Capture rules specified in the JSON payload. See Payload schema for detals. |
| POST | /server/response<br/>/payload/set/default  | Add a custom payload to be used for ALL URI responses except those explicitly configured with another payload |
| POST | /server/response<br/>/payload/set<br/>/default/`{size}`  | Respond with a random generated payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB |
| POST | /server/response<br/>/payload/set<br/>/default/binary  | Add a binary payload to be used for ALL URI responses except those explicitly configured with another payload. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /server/response<br/>/payload/set<br/>/default/binary/`{size}`  | Respond with a random generated binary payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /server/response<br/>/payload/set<br/>/uri?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI. URI can contain variable placeholders. |
| POST | /server/response<br/>/payload/set<br/>/header/`{header}`  | Add a custom payload to be sent for requests matching the given header name |
| POST | /server/response<br/>/payload/set/header<br/>/`{header}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and the given URI |
| POST | /server/response<br/>/payload/set/header<br/>/`{header}={value}`  | Add a custom payload to be sent for requests matching the given header name and value |
| POST | /server/response<br/>/payload/set/header<br/>/`{header}={value}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and value along with the given URI. |
| POST | /server/response<br/>/payload/set/query/`{q}`  | Add a custom payload to be sent for requests matching the given query param name |
| POST | /server/response<br/>/payload/set/query<br/>/`{q}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and the given URI |
| POST | /server/response<br/>/payload/set<br/>/query/`{q}={value}`  | Add a custom payload to be sent for requests matching the given query param name and value |
| POST | /server/response<br/>/payload/set/query<br/>/`{q}={value}`<br/>?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and value along with the given URI. |
| POST | /server/response/payload<br/>/set/body~{regex}?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of regexp (comma-separated list) in the given order (second expression in the list must appear after the first, and so on) |
| POST | /server/response/payload<br/>/set/body/paths/{paths}?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of JSON paths (comma-separated list). Match is triggered only when all JSON paths match, and the first matched config gets applied. Also see above description and example for how to capture values from JSON paths. |
| POST | /server/response<br/>/payload/transform?uri=`{uri}`  | Add payload transformations for requests matching the given URI. Payload submitted with this URI should be `Payload Transformation Schema` |
| POST | /server/response<br/>/payload/clear  | Clear all configured custom response payloads |
| GET  |	/server/response/payload | Get configured custom payloads |

<br/>

### gRPC Response Payload APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| POST | /server/grpc/response<br/>/payload/set/default  | Add a custom payload to be used for ALL URI responses except those explicitly configured with another payload |
| POST | /server/grpc/response<br/>/payload/set<br/>/default/`{size}`  | Respond with a random generated payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB |
| POST | /server/grpc/response<br/>/payload/set<br/>/default/binary  | Add a binary payload to be used for ALL URI responses except those explicitly configured with another payload. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /server/grpc/response<br/>/payload/set<br/>/default/binary/`{size}`  | Respond with a random generated binary payload of the given size for all URIs except those explicitly configured with another payload. Size can be a numeric value or use common byte size conventions: K, KB, M, MB. If no content type is sent with the API, `application/octet-stream` is used. |
| POST | /server/grpc/response<br/>/payload/set<br/>/uri?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI. URI can contain variable placeholders. |
| POST | /server/grpc/response<br/>/payload/set<br/>/header/`{header}`  | Add a custom payload to be sent for requests matching the given header name |
| POST | /server/grpc/response<br/>/payload/set/header<br/>/`{header}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and the given URI |
| POST | /server/grpc/response<br/>/payload/set/header<br/>/`{header}={value}`  | Add a custom payload to be sent for requests matching the given header name and value |
| POST | /server/grpc/response<br/>/payload/set/header<br/>/`{header}={value}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given header name and value along with the given URI. |
| POST | /server/grpc/response<br/>/payload/set/query/`{q}`  | Add a custom payload to be sent for requests matching the given query param name |
| POST | /server/grpc/response<br/>/payload/set/query<br/>/`{q}`?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and the given URI |
| POST | /server/grpc/response<br/>/payload/set<br/>/query/`{q}={value}`  | Add a custom payload to be sent for requests matching the given query param name and value |
| POST | /server/grpc/response<br/>/payload/set/query<br/>/`{q}={value}`<br/>?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given query param name and value along with the given URI. |
| POST | /server/grpc/response/payload<br/>/set/body~{regex}?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of regexp (comma-separated list) in the given order (second expression in the list must appear after the first, and so on) |
| POST | /server/grpc/response/payload<br/>/set/body/paths/{paths}?uri=`{uri}`  | Add a custom payload to be sent for requests matching the given URI where the body contains the given list of JSON paths (comma-separated list). Match is triggered only when all JSON paths match, and the first matched config gets applied. Also see above description and example for how to capture values from JSON paths. |
| POST | /server/grpc/response<br/>/payload/transform?uri=`{uri}`  | Add payload transformations for requests matching the given URI. Payload submitted with this URI should be `Payload Transformation Schema` |

<br/>


#### Response Payload Spec

|Field|Type|Description|
|---|---|---|
| payload | string or []byte | Text or binary payload to be returned from goto |
| streamPayload | string or []byte | Collection of string/bytes to be streamed back as payload |
| contentType | string | Content type to be set for the returned payload. If not set, defaults to JSON. |
| matches | []RequestMatch | Collection of request match objects specifying the match rules |
| capture | RequestCapture | Request capture object specifies the headers and queries from which additional values to be captured (in addition to those specified in matches) |
| base64Encode | bool | Specifies if the response should be base64 encoded before delivery |
| base64Decode | bool | Specifies if the response should be base64 decoded before delivery |
| detectJSON | bool | Specifies if Goto should detect JSON inside Header/Query values, and output those in the response payload as JSONs instead of escaped text. This should be set to true when also encoding json as base64 strings. |
| escapeJSON | bool | Specifies if Goto should escape JSON strings with backslash quotes if those contain embedded JSONs. |

#### Response Payload - Request Match Spec

|Field|Type|Description|
|---|---|---|
| uriPrefix | string or []byte | Text or binary payload to be returned from goto. Required for a match spec.  |
| headers | object | Object specifying the header name and value to be matched on. Optional. When specified, all headers must match (AND) along with URL and queries. |
| queries | object | Object specifying the query name and value to be matched on. Optional. When specified, all headers must match (AND) along with URL and headers.  |
| bodyRegexes | []string | List of regular expressions to be matched against the body  |


#### Response Payload - Request Capture Spec

|Field|Type|Description|
|---|---|---|
| headers | object | Object specifying the header name and variable name to be captured from the request. If the given header name is found in the request, the corresponding value will be captured under the given variable name.  |
| queries | object | Object specifying the query name and variable name to be captured from the request. If the given header name is found in the request, the corresponding value will be captured under the given variable name.  |


#### Payload Transformation Schema
A payload transformation is defined by giving one or more path mappings and an optional payload template.

|Field|Type|Description|
|---|---|---|
| mappings | []JSONTransform | List of mappings to be applied for this transformation. |
| payload | any | If given, this is used as the payload template to be transformed. If not given, the request payload itself is transformed and sent back. |


#### Transformation Mapping Schema

|Field|Type|Description|
|---|---|---|
| source | string | Path (keys separated by `.`) to be used against source payload (request) to get the source field  |
| target | string | Path (keys separated by `.`) to be used against target payload (template or request) to get the target field |
| ifContains | string | If given, the mapping is applied only if this text exists in the source field |
| ifNotContains | string | If given, the mapping is applied only if this text doesn't exist in the source field |
| mode | string | One of: `replace`, `join`, `push`, `append`. See above for details. |
| value | any | Default value. See above for details. |


#### Port Response Payload Config Summary

This schema is used to describe currently configured response payloads for the port on which the API `/server/response/payload` is invoked

|Field|Type|Description|
|---|---|---|
| defaultResponsePayload | ResponsePayload | Default payload if configured |
| responsePayloadByURIs | string->ResponsePayload | Payloads configured for uri match. Includes both static and request transformation payloads |
| responsePayloadByHeaders | string->string->ResponsePayload | Payloads configured for headers match |
| responsePayloadByURIAndHeaders | string->string->string->ResponsePayload | Payloads configured for uri and headers match |
| responsePayloadByQuery | string->string->ResponsePayload | Payloads configured for query params match |
| responsePayloadByURIAndQuery | string->string->string->ResponsePayload | Payloads configured for uri and query params match |
| responsePayloadByURIAndBody | string->string->ResponsePayload | Payloads configured for uri and body keywords match |


#### Response Payload Config Details

This schema is used to describe currently configured response payload, as the output of `/server/response/payload`

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
| uriCaptureKeys | []string | Keys to capture values from URI |
| headerCaptureKey | string | Key to capture value from headers |
| queryCaptureKey | string | Key to capture value from query params |
| transforms | []PayloadTransformation | Transformations defined in this config. See `Payload Transformation schema` |


<details>
<summary> Response Payload Events </summary>

- `Response Payload Configured`
- `Response Payload Cleared`
- `Response Payload Applied`: generated when a configured response payload is applied to a request that wasn't explicitly asking for a custom payload (i.e. not for `/payload` and `/stream` URIs).

</details>
</br>

See [Response Payload API Examples](docs/response-payload-api-examples.md)


# Ad-hoc Payload
This URI responds with a random-generated payload of the requested size. Payload size can be a numeric value or use common byte size conventions: `K`, `KB`, `M`, `MB`. Payload size is only limited by the memory available to the `goto` process. The response carries an additional header `Goto-Payload-Length` in addition to the standard header `Content-Length` to identify the size of the response payload.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/server/response/payload/`{size}` | Responds with an auto-generated payload of given size |

<br/>
<details>
<summary>Ad-hoc Payload API Example</summary>

```
curl -v localhost:8080/payload/10K

curl -v localhost:8080/payload/100

```

</details>

###### <small> [Back to TOC](#toc) </small>
