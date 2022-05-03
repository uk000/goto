#### Request Proxying API Examples:

```
curl -X POST localhost:8080/proxy/targets/clear

curl http://localhost:8080/proxy/targets/add --data '{"name": "target1", "endpoint":"http://localhost:8081", "enabled":true, "routes":{"/foo/{x}/bar/{y}": "/abc/{y:.*}/def/{x:.*}"}}'

curl http://localhost:8080/proxy/targets/add --data '{"name": "target1", "endpoint":"http://localhost:8081", "enabled":true, "routes":{"/": ""}, "matchAll":{"headers":[["foo", "{x}"], ["bar", "{y}"]]}, "addHeaders":[["abc","{x}"], ["def","{y}"]], "removeHeaders":["foo"]}'


curl -X POST localhost:8000/port=8080/proxy/targets/add/t1\?url=localhost:8081; 
curl -X POST localhost:8000/port=8080/proxy/targets/t1/route\?from=/foo\&to=/bar;
curl -X POST localhost:8000/port=8080/proxy/targets/t1/match/header/foo=1;
curl -X POST localhost:8000/port=8080/proxy/targets/t1/match/query/bar=123;

curl -X POST localhost:8000/port=8080/proxy/targets/add/t2\?url=localhost:8082;
curl -X POST localhost:8000/port=8080/proxy/targets/t2/route\?from=/foo;
curl -X POST localhost:8000/port=8080/proxy/targets/t2/match/header/foo=2;

curl -X POST localhost:8000/port=8080/proxy/targets/add/t3\?url=localhost:8083;
curl -X POST localhost:8000/port=8080/proxy/targets/t3/route\?from=/

curl -X PUT localhost:8080/proxy/targets/t1/remove

curl -X PUT localhost:8080/proxy/targets/t2/disable

curl -X PUT localhost:8080/proxy/targets/t2/enable

curl -v -X POST localhost:8080/proxy/targets/t1/invoke

curl localhost:8080/proxy/targets

curl localhost:8080/proxy/counts

```

#### Proxy Target Counts Result Example

```
{
  "countsByTargets": {
    "t1": 4,
    "t2": 3,
    "t3": 3
  },
  "countsByHeaders": {
    "foo": 2,
    "x": 1,
    "y": 1
  },
  "countsByHeaderValues": {},
  "countsByHeaderTargets": {
    "foo": {
      "t1": 2
    },
    "x": {
      "t2": 1
    },
    "y": {
      "t3": 1
    }
  },
  "countsByHeaderValueTargets": {},
  "countsByUris": {
    "/debug": 1,
    "/foo": 2,
    "/x/22/y/33": 1,
    "/x/22/y/33?foo=123&bar=456": 1
  },
  "countsByUriTargets": {
    "/debug": {
      "pt4": 1
    },
    "/foo": {
      "pt3": 2
    },
    "/x/22/y/33": {
      "t1": 1
    },
    "/x/22/y/33?foo=123&bar=456": {
      "t1": 1
    }
  },
  "countsByQuery": {
    "foo": 4
  },
  "countsByQueryValues": {},
  "countsByQueryTargets": {
    "foo": {
      "pt1": 1,
      "pt5": 3
    }
  },
  "countsByQueryValueTargets": {}
}
```
