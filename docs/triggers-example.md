#### Triggers API Examples:

```
curl -X POST localhost:8080/response/triggers/clear

curl -s localhost:8080/port=8081/response/triggers/add --data '{
	"name": "t1",
	"method":"POST",
	"url":"http://localhost:8082/response/status/clear",
	"enabled": true,
	"triggerOn": [502, 503],
	"startFrom": 2,
	"stopAt": 3
}'

curl -X POST localhost:8080/response/triggers/t1/remove

curl -X POST localhost:8080/response/triggers/t1/enable

curl -X POST localhost:8080/response/triggers/t1/disable

curl -X POST localhost:8080/response/triggers/t1/invoke

curl localhost:8080/response/triggers/counts

curl localhost:8080/response/triggers

```

#### Triggers Details and Results Example

```
$ curl localhost:8080/response/triggers
{
  "Targets": {
    "t1": {
      "name": "t1",
      "method": "POST",
      "url": "http://localhost:8081/response/status/clear",
      "headers": null,
      "body": "",
      "sendID": false,
      "enabled": true,
      "triggerOn": [
        502,
        503
      ],
      "startFrom": 2,
      "stopAt": 3,
      "statusCount": 5,
      "triggerCount": 2
    }
  },
  "TargetsByResponseStatus": {
    "502": {
      "t1": {
        "name": "t1",
        "method": "POST",
        "url": "http://localhost:8081/response/status/clear",
        "headers": null,
        "body": "",
        "sendID": false,
        "enabled": true,
        "triggerOn": [
          502,
          503
        ],
        "startFrom": 2,
        "stopAt": 3,
        "statusCount": 5,
        "triggerCount": 2
      }
    },
    "503": {
      "t1": {
        "name": "t1",
        "method": "POST",
        "url": "http://localhost:8081/response/status/clear",
        "headers": null,
        "body": "",
        "sendID": false,
        "enabled": true,
        "triggerOn": [
          502,
          503
        ],
        "startFrom": 2,
        "stopAt": 3,
        "statusCount": 5,
        "triggerCount": 2
      }
    }
  },
  "TriggerResults": {
    "t1": {
      "200": 2
    }
  }
}

$ curl -s localhost:8080/response/triggers/counts
{
  "t1": {
    "202": 2
  },
  "t3": {
    "200": 3
  }
}
```
