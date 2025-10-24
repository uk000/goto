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

#### Request Matching
Custom response payload can be set for any of the following request categories, listed in the order of precedence. The highest precedence match gets applied to build the response payload.

1. URI + headers combination match
2. URI + query combination match
3. URI + body keywords combination match
4. URI + body JSON paths match
5. URI match
6. Headers match
7. Query match
8. `Default` payload, if configured

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

### Response Payload APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
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

<details>
<summary> Payload Transformation Schema </summary>

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


#### Port Response Payload Config Schema

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


#### Response Payload Config Schema

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

</details>

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
| GET, PUT, POST  |	/payload/`{size}` | Respond with a payload of given size |

<br/>
<details>
<summary>Ad-hoc Payload API Example</summary>

```
curl -v localhost:8080/payload/10K

curl -v localhost:8080/payload/100

```

</details>

###### <small> [Back to TOC](#toc) </small>


# <a name="-stream-chunked-payload"></a>

## > Stream (Chunked) Payload

This URI responds with either pre-configured or random-generated payload where response behavior is controlled by the parameters passed to the API. The feature allows requesting a custom payload size, custom response duration over which to stream the payload, custom chunk size to be used for splitting the payload into chunks, and custom delay to be used in-between chunked responses. Combination of these parameters define the total payload size and the total duration of the response.

Stream responses carry following headers:

- `Goto-Stream-Length: <total payload size>`
- `Goto-Stream-Duration: <total response duration>`
- `Goto-Chunk-Count: <total number of chunks>`
- `Goto-Chunk-Length: <per-chunk size>`
- `Goto-Chunk-Delay: <per-chunk delay>`
- `X-Content-Type-Options: nosniff`
- `Transfer-Encoding: chunked`

#### Stream APIs
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/stream/payload=`{size}`<br/>/duration={duration}<br/>/delay={delay} | Respond with a payload of the given size delivered over the given duration with the given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/duration={duration}<br/>/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered over the given duration with given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/count={count}/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered the given count of times with given delay per chunk. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/duration={duration}<br/>/delay={delay} | Respond with pre-configured default payload split into enough chunks to spread out over the given duration with given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/count={count}/delay={delay} | Respond with pre-configured default payload split into the given count of chunks with the given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |

<br/>
<details>
<summary>Stream Response API Example</summary>

```
curl -v --no-buffer localhost:8080/stream/payload=10K/duration=5s-15s/delay=100ms-1s

curl -v --no-buffer localhost:8080/stream/chunksize=100/duration=5s/delay=500ms-2s

curl -v --no-buffer localhost:8080/stream/chunksize=100/count=5/delay=200ms

curl -v --no-buffer localhost:8080/stream/duration=5s/delay=100ms-300ms

curl -v --no-buffer localhost:8080/stream/count=10/delay=300ms
```

</details>


# Response Status
This feature allows setting a forced response status for all requests except bypass URIs. Server also tracks the number of status requests received (via /status URI) and the number of responses sent per status code.

#### Response Status APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
| PUT, POST | /server/response<br/>/status/set/`{status}`     | Set a forced response status that all non-proxied and non-management requests will be responded with. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used each time. Syntax of status param is `<status1,status2,...>:<times>`, where `times` is the number of times the given forced statuses will be used, after which the requests will be served the normal response status. |
| PUT, POST | /server/response<br/>/status/set/`{status}`?uri=`{uri}`     | Set a forced response status for a specific URL. All non-proxied requests for the URI will be responded with the given `status`. Status syntax is `<status1,status2,...>:<times>`. |
| PUT, POST |	/server/response<br/>/status/clear            | Remove currently configured forced response status, so that all subsequent calls will receive their original deemed response |
| PUT, POST | /server/response<br/>/status/counts/clear     | Clear counts tracked for response statuses |
| GET       |	/server/response<br/>/status/counts/`{status}`  | Get request counts for a given status |
| GET       |	/server/response<br/>/status/counts           | Get request counts for all response statuses so far |
| GET       |	/server/response/status                  | Get the currently configured forced response statuses for all ports |

<br/>
<details>
<summary>Response Status Events</summary>

- `Response Status Configured`
- `Response Status Cleared`
- `Response Status Counts Cleared`

</details>

<details>
<summary>Response Status API Examples</summary>

```
curl -X POST localhost:8080/server/response/status/counts/clear

curl -X POST localhost:8080/server/response/status/clear

curl -X PUT localhost:8080/server/response/status/set/502

curl -X PUT localhost:8080/server/response/status/set/0

curl -X POST localhost:8080/server/response/status/counts/clear

curl localhost:8080/server/response/status/counts

curl localhost:8080/server/response/status/counts/502

curl -X POST localhost:8080/server/response/status/configure --data <<EOF
{
	"port": 8080,
	"statuses": [403],
	"times": -1,
	"match": {
		"uri": "/foo",
		"headers": [
			{
				"header": "foo",
				"present": false
			},
			{
				"header": "bar",
        "value": "foo"
			}
		]
	}
}
EOF
```

</details>

<details>
<summary>Response Status Tracking Result Example</summary>

<p>

```
{
  "countsByRequestedStatus": {
    "418": 20
  },
  "countsByReportedStatus": {
    "200": 15,
    "202": 4,
    "208": 5,
    "418": 20
  }
}
```

</p>
</details>


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



# Status
The URI `/status/`{status}`` allows clients to ask for a specific status as response code. The given status is reported back, except when forced status is configured in which case the forced status is sent as response.

#### API
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status/`{status}` or /status=`{status}` | This call either receives the given status, or the forced response status if one is set. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE  |	/status=`{status}`<br/>/delay=`{delay}` | In addition to requesting a status as above, this API also allows a delay to be applied. `status` can be either a single status code or a comma-separated list of codes, in which case a randomly selected code will be used. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`?x-request-id=`{requestId}` | When the status param is passed in the format `<code>:<count>`, the requested response code is returned for `count` number of subsequent calls (starting from the current one) before reverting back to 200. The optional query param `x-request-id` can be used to ask for stateful status for each unique request id, allowing multiple concurrent clients to each receive its own independent stateful response. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`/delay=`{delay}`?x-request-id=`{requestId}` | Same as above, the requested response code is returned for `count` number of subsequent calls but with the given delay applied before a response. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. After `count` responses, the response status reverts to 200.  |
| GET, PUT, POST, OPTIONS, HEAD, DELETE  |	/status=`{status:count}`/flipflop?x-request-id=`{requestId}` | This call responds with the given status for the given count times when called successively with the same count value. Once the status is served `count` times, the next status served is `200`, and subsequent calls start the cycle again. Optional query param `x-request-id` can be used to perform status flip for each unique request, preventing requests from affecting one another. |
| GET, PUT, POST, OPTIONS, HEAD, DELETE |	/status=`{status:count}`/delay=`{delay}`/flipflop?x-request-id=`{requestId}` | Same behavior as above except that the given delay duration param gets applied, allowing you to add artificial delay before responding with the given status. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET  |	/status/flipflop | Reports the current flipflop counter value, i.e. number of times the most recent status has been served. |
| POST |	/status/clear | Clears the current state of all stateful statuses recorded so far. |

<br/>
<details>
<summary>Status API Example</summary>

```
curl -I  localhost:8080/status/418

curl -I  localhost:8080/status/501,502,503

curl -v  localhost:8080/status=501,502,503/delay=100ms-1s

curl -v localhost:8080/status=503:2?x-request-id=1

curl -v localhost:8080/status=503:2/delay=1s?x-request-id=1

curl -I localhost:8080/status=503:3/flipflop?x-request-id=1

curl -v localhost:8080/status=503:3/delay=1s/flipflop?x-request-id=1

curl localhost:8080/status/flipflop

curl -XPOST localhost:8080/status/clear
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
