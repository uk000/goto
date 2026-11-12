#### Response Payload API Examples

(All `/server/response/payload/...` URIs below can equally be written as `/response/payload/...`. See the [Response Payload README](../pkg/server/response/payload/README.md) for the full API tables and schemas. Use `curl -g` whenever the URL contains `{...}`, otherwise curl treats the braces as a URL glob.)

```
curl -X POST localhost:8080/server/response/payload/set/default --data '{"test": "default payload"}'

curl -X POST localhost:8080/server/response/payload/set/default/10K

# URI payload with captures {f} and {b}. Note: the URI is passed as a query param of /payload/set (not /payload/set/uri)
curl -X POST -g 'localhost:8080/server/response/payload/set?uri=/foo/{f}/bar{b}' --data '{"test": "uri was /foo/{f}/bar{b}"}'

# header name with a captured value {f} (header name = foo)
curl -X POST -g 'localhost:8080/server/response/payload/set/header/foo={f}' --data '{"test": "header was foo with value {f}"}'

curl -X POST localhost:8080/server/response/payload/set/header/foo=bar --data '{"test": "header was foo with value bar"}'

# header + URI
curl -X POST -g 'localhost:8080/server/response/payload/set/header/bar={y}?uri=/foo/{x}' --data '{"result": "URI foo with {x} and header bar with {y}"}'

# query param + URI
curl -X POST -g 'localhost:8080/server/response/payload/set/query/qq={v}?uri=/q' --data '{"test": "query qq was set to {v}"}'

curl -g -X POST 'localhost:8080/server/response/payload/set/body~AA,BB,CC?uri=/foo' --data '{"test": "body contains AA,BB,CC"}' -HContent-Type:application/json

curl -v -g -XPOST localhost:8080/response/payload/set/body/paths/.foo.bar={a},.foo.baz={b}\?uri=/foo --data '{"bar":"{a}", "baz":"{b}", "message": "{a} is {b}"}' -HContent-Type:application/json

# set the content type of the payload with the ResponseContent-Type header (not Content-Type)
curl -X POST -H 'ResponseContent-Type: application/xml' 'localhost:8080/response/payload/set?uri=/xml' --data '<a/>'

# stream payload: JSON array items repeated up to count (written back-to-back; the delay is not applied for HTTP)
curl -X POST -g 'localhost:8080/response/payload/set/stream/count=5/delay=500ms?uri=/st' --data '[{"a":1},{"a":2}]'

# payload with its own match rules (see the README for the full schema)
curl -X POST localhost:8080/response/payload/set/matches -d '{"payload":"hello","matches":[{"uriPrefix":"/hi"}]}'

# an empty body removes the payload of a URI
curl -X POST 'localhost:8080/response/payload/set?uri=/xml'

# random payload of a given size (no config needed)
curl localhost:8080/response/payload/2K

curl -X POST localhost:8080/server/response/payload/clear

curl localhost:8080/server/response/payload
```

Sample replies:
```
$ curl -X POST -g 'localhost:8080/response/payload/set?uri=/foo/{f}/bar{b}' --data '{"result": "uri had foo={f}, bar={b}"}'
Port [8080] Payload set for URI [/foo/{f}/bar{b}] : content-type [plain/text], length [38]
$ curl localhost:8080/foo/hi/bar123
{"result": "uri had foo=hi, bar=123"}

$ curl -X POST -g 'localhost:8080/response/payload/set/body/paths/.foo.bar={a},.foo.baz={b}?uri=/foo' ...
Port [8080] Payload set for URI [/foo] and match [.foo.bar={a},.foo.baz={b}] : content-type [plain/text], length [51]
$ curl localhost:8080/foo --data '{"foo": {"bar": "hi", "baz": "hello"}}'
{"bar":"hi", "baz":"hello", "message": "hi is hello"}
```

> Caution: a `set` call without `?uri=` (default, header-only or query-only payloads) leaves a route behind that can't be removed, which makes non-matching requests return an empty `200` until goto is restarted (see the README's note and bug report REQ-03). Prefer URI-scoped payloads.

#### Response Payload Transformation Examples

```
#-------------------------------------------------

# This transformation config...

curl -X POST -H'content-type:application/json' localhost:8080/server/response/payload/transform\?uri=/foo --data '
[{"mappings": [
	{"source": "foo.x", "target": "{{xx}}", "mode": "replace"}, 
	{"source": "foo.x", "value": {"faa":"baa"}, "mode": "replace"}, 
	{"source": "foo.bar.0", "value": {"b":0}, "mode": "append"}, 
	{"source": "foo.bar.1", "value": {"c":1}, "mode": "push"}
]}]'

#...transforms this YAML request payload...
foo:
  x: this is x
  bar:
  - a: hello
  - b: "{xx}"
  - c: hi

#...to this YAML response payload:
foo:
  bar:
  - a: hello
  - c: 1
  - b: 0
  - b: 'this is x'
  - c: hi
  x:
    faa: baa

#-------------------------------------------------

# This transformation config...

curl -X POST -H'content-type:application/json' localhost:8080/server/response/payload/transform?uri=/foo --data '[
{
	"mappings": [
		{"source": "foo.x", "ifContains": "test", "target": "foo.x", "value": {"xx":"hi"}, "mode": "replace"}, 
		{"source": "foo.x", "ifNotContains": "test", "target": "foo.y", "value": {"xx":"hi"}, "mode": "append"}, 
		{"source": "foo.bar.0.aa", "target": "foo.bar.1.aa", "value": {"b":0}, "mode": "push"}, 
		{"source": "foo.bar.1", "target": "foo.bar.1", "value": {"c":1}, "mode": "append"}
	], 
	"payload": {
		"foo":{"bar": [{"x":"hi"}, {"aa":"b"}], "x": "hello", "y": "hello"}} }
]'

#...transforms this YAML request payload...
foo:
  x: this is x test
  bar:
  - a: hello
  - b: "{xx}"
  - c: hi
  - d: dd

#...to this YAML response payload:
foo:
  bar:
  - x: hi
  - aa:
    - b: 0
    - b
  - b: '{xx}'
  x: this is x test
  "y": hello


#...but transforms this YAML request payload...
foo:
  x: this is x
  bar:
  - a: hello
  - b: "{xx}"
  - c: hi
  - d: dd

#...to this YAML response payload:
foo:
  bar:
  - x: hi
  - aa:
    - b: 0
    - b
  - b: '{xx}'
  x: hello
  "y":
  - hello
  - this is x

#-------------------------------------------------

# This transformation config...

curl -X POST -H'content-type:application/json' localhost:8080/server/response/payload/transform\?uri=/foo --data '[{
	"mappings": [
		{"source": "foo.x", "target": "{{xx}}", "mode": "replace"}, 
		{"source": "foo.bar.0", "target": "faa.baa.0", "value": {"aa": "this is {{xx}}"}, "mode": "replace"},
		{"source": "foo.bar.1", "target": "faa.baa", "mode": "append"},
		{"source": "foo.bar.1.b", "target": "faa.baa.2.z", "mode": "replace"}
	], 
	"payload": {
		"faa":{"baa": [{"x":"aa"}, {"y":"{xx}"}, {"z":"zz"}]}} 
}]'


#...transforms this YAML request payload...
foo:
  x: this is x
  bar:
  - a: hello
  - b: "{xx}"
  - c: hi


#...to this YAML response payload:
faa:
  baa:
  - a: hello
  - "y": 'this is x'
  - z: 'this is x'
  - b: 'this is x'

#-------------------------------------------------

```