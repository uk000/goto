```
curl -s localhost:8081/events

[
  {
    "title": "Listener Added",
    "summary": "9091-1",
    "data": {
      "listener": {"...":"..."},
      "status": "Listener 9091 added and opened."
    },
    "at": "2021-01-30T19:33:10.58548-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Peer Registered",
    "summary": "peer1",
    "data": {"...":"..."},
    "at": "2021-01-30T19:33:10.589635-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Peer Startup Data",
    "summary": "peer1",
    "data": {
      "Targets": {"...":"..."},
      "Jobs": {"...":"..."},
      "TrackingHeaders": "",
      "Probes": null,
      "Message": ""
    },
    "at": "2021-01-30T19:33:10.590423-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Server Started",
    "summary": "peer1",
    "data": {
      "8081": {
        "listenerID": "",
        "label": "local.local@1.1.1.1:8081",
        "port": 8081,
        "protocol": "HTTP",
        "open": true,
        "tls": false
      },
      "9091": {
        "listenerID": "9091-1",
        "label": "9091",
        "port": 9091,
        "protocol": "http",
        "open": true,
        "tls": false
      }
    },
    "at": "2021-01-30T19:33:10.590837-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Target Added",
    "summary": "target1",
    "data": {"...": "..."},
    "at": "2021-01-30T19:35:51.015874-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Target Invoked",
    "summary": "target1",
    "data": {"...": "..."},
    "at": "2021-01-30T19:35:53.040253-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Invocation Started",
    "data": {"...": "..."},
    "at": "2021-01-30T19:35:53.040272-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "URI First Request",
    "data": {"...": "..."},
    "at": "2021-01-30T19:35:53.041489-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Invocation Response",
    "data": {"...": "..."},
    "at": "2021-01-30T19:35:57.119397-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  },
  {
    "title": "Invocation Repeated Response Status",
    "data": {"...": "..."},
    "at": "2021-01-30T19:44:10.39711-08:00",
    "peer": "peer1",
    "peerHost": "local.local@1.1.1.1:8081"
  }
}
]
```
