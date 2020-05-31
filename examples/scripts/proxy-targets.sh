set -v
curl -s -X POST localhost:8080/request/proxy/targets/clear
curl -s -X POST localhost:8081/request/proxy/targets/clear
curl -s -X POST localhost:8082/request/proxy/targets/clear
curl -s -X POST localhost:8083/request/proxy/targets/clear

curl -s localhost:8081/request/proxy/targets/add --data '{"name": "pt1", "match":{"uris":["/x/{x}/y/{y}"], "query":[["foo", "{f}"]]}, "url":"http://localhost:8083", "replaceURI":"/abc/{y:.*}/def/{x:.*}", "addHeaders":[["z","z1"]], "addQuery":[["bar","{f}"]], "removeQuery":["foo"], "replicas":1, "enabled":true, "sendID": true}'

curl -s localhost:8081/request/proxy/targets/add --data '{"name": "pt2", "match":{"headers":[["foo"]]}, "url":"http://localhost:8083", "replaceURI":"/echo", "addHeaders":[["z","z2"]], "replicas":1, "enabled":true, "sendID": false}'

curl -s localhost:8082/request/proxy/targets/add --data '{"name": "pt3", "match":{"headers":[["x", "{x}"]], "uris":["/foo"]}, "url":"http://localhost:8083", "replaceURI":"/echo", "addHeaders":[["z","{x}"]], "removeHeaders":["x"], "replicas":1, "enabled":true}'

curl -s localhost:8082/request/proxy/targets/add --data '{"name": "pt4", "match":{"headers":[["foo"]], "uris":["/debug"]}, "url":"http://localhost:8081", "replaceURI":"/echo", "addHeaders":[["z","z4"]], "replicas":1, "enabled":true, "sendID": true}'

curl -s -w "\n" localhost:8080/request/proxy/targets
curl -s -w "\n" localhost:8081/request/proxy/targets
curl -s -w "\n" localhost:8082/request/proxy/targets
curl -s -w "\n" localhost:8083/request/proxy/targets

curl -s -X PUT localhost:8081/request/proxy/targets/pt1/disable
curl -s -X PUT localhost:8081/request/proxy/targets/pt2/disable
curl -s -X PUT localhost:8082/request/proxy/targets/pt3/disable
curl -s -X PUT localhost:8082/request/proxy/targets/pt4/disable

curl -s -X PUT localhost:8081/request/proxy/targets/pt1/enable
curl -s -X PUT localhost:8081/request/proxy/targets/pt2/enable
curl -s -X PUT localhost:8082/request/proxy/targets/pt3/enable
curl -s -X PUT localhost:8082/request/proxy/targets/pt4/enable

curl -s -X PUT localhost:8081/request/proxy/targets/pt1/remove
curl -s -X PUT localhost:8081/request/proxy/targets/pt2/remove
curl -s -X PUT localhost:8082/request/proxy/targets/pt3/remove
curl -s -X PUT localhost:8082/request/proxy/targets/pt4/remove

curl -s -w "\n" localhost:8080/request/proxy/targets
curl -s -w "\n" localhost:8081/request/proxy/targets
curl -s -w "\n" localhost:8082/request/proxy/targets
curl -s -w "\n" localhost:8083/request/proxy/targets
