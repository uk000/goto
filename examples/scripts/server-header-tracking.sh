set -v
curl -X POST localhost:8080/request/headers/track/clear
curl -X POST localhost:8081/request/headers/track/clear
curl -X POST localhost:8082/request/headers/track/clear
curl -X POST localhost:8083/request/headers/track/clear

curl -X PUT localhost:8080/request/headers/track/add/x,y,z,foo
curl -X PUT localhost:8081/request/headers/track/add/x,y,z,foo
curl -X PUT localhost:8082/request/headers/track/add/x,y,z,foo
curl -X PUT localhost:8083/request/headers/track/add/x,y,z,foo

curl -s localhost:8080/request/headers/track
curl -s localhost:8081/request/headers/track
curl -s localhost:8082/request/headers/track
curl -s localhost:8083/request/headers/track
