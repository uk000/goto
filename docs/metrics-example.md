```
# HELP goto_active_client_conn_counts_by_targets Number of active client connections by targets# TYPE goto_active_client_conn_counts_by_targets gauge
goto_active_client_conn_counts_by_targets{target="test-1.1"} 4
goto_active_client_conn_counts_by_targets{target="test-1.2"} 4
# HELP goto_client_failures_by_targets Number of failed client requests by target
# TYPE goto_client_failures_by_targets counter
goto_client_failures_by_targets{target="peer1_to_peer2"} 2
goto_client_failures_by_targets{target="peer1_to_peer3"} 2
# HELP goto_client_requests_by_targets Number of client requests by target
# TYPE goto_client_requests_by_targets counter
goto_client_requests_by_targets{target="test-1.1"} 80
goto_client_requests_by_targets{target="test-1.2"} 80
# HELP goto_connections Number of connections by type
# TYPE goto_connections counter
goto_connections{connType="http"} 9
# HELP goto_requests_by_headers Number of requests by headers
# TYPE goto_requests_by_headers counter
goto_requests_by_headers{requestHeader="foo"} 3
# HELP goto_requests_by_type Number of requests by type
# TYPE goto_requests_by_type counter
goto_requests_by_type{requestType="catchAll"} 4
# HELP goto_requests_by_uris Number of requests by URIs
# TYPE goto_requests_by_uris counter
goto_requests_by_uris{requestURI="/bar"} 1
goto_requests_by_uris{requestURI="/foo"} 3
```
