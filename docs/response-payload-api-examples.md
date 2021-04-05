#### Response Payload API Examples

```
curl -X POST localhost:8080/server/response/payload/set/default --data '{"test": "default payload"}'

curl -X POST localhost:8080/server/response/payload/set/default/10K

curl -X POST -g localhost:8080/server/response/payload/set/uri?uri=/foo/{f}/bar{b} --data '{"test": "uri was /foo/{}/bar/{}"}'

curl -X POST -g localhost:8080/server/response/payload/set/header/foo/{f} --data '{"test": "header was foo with value {f}"}'

curl -X POST localhost:8080/server/response/payload/set/header/foo=bar --data '{"test": "header was foo with value bar"}'

curl -g -X POST localhost:8080/server/response/payload/set/body~AA,BB,CC?uri=/foo --data '{"test": "body contains AA,BB,CC"}' -HContent-Type:application/json

curl -X POST localhost:8080/server/response/payload/clear

curl localhost:8080/server/response/payload
```

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