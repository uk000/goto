See [Metrics](../pkg/metrics/README.md) for the list of metrics, the `/stats` schema and the APIs. The output below was captured from a `goto` instance on port 8080 after this sequence of calls (the calls to `/metrics` and `/stats` are counted too, which is why `/metrics` and `/stats/clear` show up):

```
curl -X POST localhost:8080/metrics/clear
curl -X POST localhost:8080/stats/clear
curl -X POST localhost:8080/request/track/headers/add/x
curl localhost:8080/foo
curl -H 'x: y' localhost:8080/foo
curl localhost:8080/echo
```

#### Prometheus metrics: `GET /metrics`

```
$ curl localhost:8080/metrics

# HELP goto_requests_by_client Number of server requests by client
# TYPE goto_requests_by_client counter
goto_requests_by_client{client="[::1]"} 5
# HELP goto_requests_by_header_values Number of requests by header values
# TYPE goto_requests_by_header_values counter
goto_requests_by_header_values{headerValue="y",requestHeader="x"} 1
# HELP goto_requests_by_headers Number of requests by headers
# TYPE goto_requests_by_headers counter
goto_requests_by_headers{requestHeader="x"} 1
# HELP goto_requests_by_headers_and_uris Number of requests by headers and uris
# TYPE goto_requests_by_headers_and_uris counter
goto_requests_by_headers_and_uris{requestHeader="x",uri="/foo"} 1
# HELP goto_requests_by_port Number of requests by port
# TYPE goto_requests_by_port counter
goto_requests_by_port{port="8080"} 6
# HELP goto_requests_by_port_and_header_values Number of requests by port and request header values
# TYPE goto_requests_by_port_and_header_values counter
goto_requests_by_port_and_header_values{headerValue="y",port="8080",requestHeader="x"} 1
# HELP goto_requests_by_port_and_headers Number of requests by port and request headers
# TYPE goto_requests_by_port_and_headers counter
goto_requests_by_port_and_headers{port="8080",requestHeader="x"} 1
# HELP goto_requests_by_port_and_uris Number of requests by port and uris
# TYPE goto_requests_by_port_and_uris counter
goto_requests_by_port_and_uris{port="8080",uri="/echo"} 1
goto_requests_by_port_and_uris{port="8080",uri="/foo"} 2
goto_requests_by_port_and_uris{port="8080",uri="/metrics/clear"} 1
goto_requests_by_port_and_uris{port="8080",uri="/request/track/headers/add/x"} 1
goto_requests_by_port_and_uris{port="8080",uri="/stats/clear"} 1
# HELP goto_requests_by_protocol Number of requests by protocol
# TYPE goto_requests_by_protocol counter
goto_requests_by_protocol{protocol="HTTP/1.1"} 3
# HELP goto_requests_by_protocol_and_uris Number of requests by protocol and uris
# TYPE goto_requests_by_protocol_and_uris counter
goto_requests_by_protocol_and_uris{protocol="HTTP/1.1",uri="/foo"} 2
goto_requests_by_protocol_and_uris{protocol="HTTP/1.1",uri="/stats/clear"} 1
# HELP goto_requests_by_type Number of requests by type
# TYPE goto_requests_by_type counter
goto_requests_by_type{requestType="catchAll"} 2
goto_requests_by_type{requestType="echo"} 1
# HELP goto_requests_by_uris Number of requests by URIs
# TYPE goto_requests_by_uris counter
goto_requests_by_uris{uri="/echo"} 1
goto_requests_by_uris{uri="/foo"} 2
goto_requests_by_uris{uri="/metrics/clear"} 1
goto_requests_by_uris{uri="/request/track/headers/add/x"} 1
goto_requests_by_uris{uri="/stats/clear"} 1
```

Metrics of the other features have no sample until the feature is used. The names and labels are as follows (the values here are only illustrative):

```
goto_invocations_by_targets{target="test-1.1"} 80
goto_failed_invocations_by_targets{target="peer1_to_peer2"} 2
goto_active_conn_counts_by_targets{target="test-1.1"} 4
goto_total_conns_by_targets{target="test-1.1"} 4
goto_conn_counts{connType="tcp"} 3
goto_tcp_conn_counts{tcpType="Echo"} 3
goto_proxied_requests{proxyTarget="upstream1"} 5
goto_triggers{triggerTarget="t1"} 2
```

#### Server stats: `GET /stats`

```
$ curl localhost:8080/stats
{
  "requestCountsByHeaders": {
    "x": 1
  },
  "requestCountsByURIs": {
    "/echo": 1,
    "/foo": 2,
    "/metrics": 1,
    "/request/track/headers/add/x": 1,
    "/stats/clear": 1
  },
  "requestCountsByURIsAndHeaders": {
    "/foo": {
      "x": 1
    }
  },
  "requestCountsByURIsAndHeaderValues": {
    "/foo": {
      "x": {
        "y": 1
      }
    }
  },
  "requestCountsByPortAndURIs": {
    "8080": {
      "/echo": 1,
      "/foo": 2,
      "/metrics": 1,
      "/request/track/headers/add/x": 1,
      "/stats/clear": 1
    }
  },
  "requestCountsByPortAndHeaders": {
    "8080": {
      "x": 1
    }
  },
  "requestCountsByPortAndHeaderValues": {
    "8080": {
      "x": {
        "y": 1
      }
    }
  },
  "requestCountsByURIsAndProtocol": {
    "/foo": {
      "HTTP/1.1": 2
    }
  }
}
```

#### Clearing

```
$ curl -X POST localhost:8080/metrics/clear
Metrics cleared

$ curl -X POST localhost:8080/stats/clear
Stats cleared
```

`/metrics/go` serves the Go runtime and process metrics (`go_gc_duration_seconds`, `go_goroutines`, `go_memstats_*`, ...) in the same text format, and is not changed by `/metrics/clear`.
