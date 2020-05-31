set -v
curl -X PUT localhost:8080/label/set/Server8080
curl -s localhost:8080/listeners/add --data '{"port":8081, "protocol":"http"}'
curl -s -X PUT localhost:8080/listeners/8081/open
curl -X PUT localhost:8081/label/set/Server8081
curl -s localhost:8081/listeners/add --data '{"port":8082, "protocol":"http"}'
curl -s -X PUT localhost:8081/listeners/8082/open
curl -X PUT localhost:8082/label/set/Server8082
curl -s localhost:8081/listeners/add --data '{"port":8083, "protocol":"http"}'
curl -s -X PUT localhost:8081/listeners/8083/open
curl -X PUT localhost:8083/label/set/Server8083
curl -s localhost:8080/listeners
curl -s -X PUT localhost:8080/listeners/8081/close
curl -s -X PUT localhost:8080/listeners/8082/close
curl -s -X PUT localhost:8080/listeners/8083/close
curl -s -X PUT localhost:8080/listeners/8083/open
curl -s -X PUT localhost:8080/listeners/8082/open
curl -s -X PUT localhost:8080/listeners/8081/open
curl -s -X PUT localhost:8080/listeners/8083/remove
curl -s -X PUT localhost:8080/listeners/8082/remove
curl -s -X PUT localhost:8080/listeners/8081/remove
