set -v
curl -w "\n" -X PUT localhost:8081/response/status/set/501
curl -w "\n" -X PUT localhost:8082/response/status/set/502
curl -w "\n" -X PUT localhost:8083/response/status/set/503

curl -w "\n" -I localhost:8081/status/418
curl -w "\n" -I localhost:8082/status/418
curl -w "\n" -I localhost:8083/status/418
