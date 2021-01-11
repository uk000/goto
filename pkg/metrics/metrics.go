package metrics

import (
  "fmt"
  "net/http"

  "github.com/gorilla/mux"
  "github.com/prometheus/client_golang/prometheus"
  "github.com/prometheus/client_golang/prometheus/promauto"
  "github.com/prometheus/client_golang/prometheus/promhttp"

  "goto/pkg/util"
)

var (
  Handler        = util.ServerHandler{"metrics", SetRoutes, Middleware}
  customRegistry = prometheus.NewRegistry()
  factory        = promauto.With(customRegistry)
  requestCounts  = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_requests_by_type", Help: "Number of requests by type"}, []string{"requestType"})
  requestCountsByHeaders = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_requests_by_headers", Help: "Number of requests by headers"}, []string{"requestHeader"})
  requestCountsByURIs = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_requests_by_uris", Help: "Number of requests by URIs"}, []string{"requestURI"})
  proxiedRequestCounts = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_proxied_requests", Help: "Number of proxied requests"}, []string{"proxyTarget"})
  triggerCounts = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_triggers", Help: "Number of triggered requests"}, []string{"triggerTarget"})
  connCounts = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_connections", Help: "Number of connections by type"}, []string{"connType"})
  tcpConnections = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_tcp_connections", Help: "Number of TCP connections by type"}, []string{"tcpType"})
)

func UpdateRequestCount(reqType string) {
  requestCounts.WithLabelValues(reqType).Inc()
}

func UpdateHeaderRequestCount(header string) {
  requestCountsByHeaders.WithLabelValues(header).Inc()
}

func UpdateURIRequestCount(uri string) {
  requestCountsByURIs.WithLabelValues(uri).Inc()
}

func UpdateProxiedRequestCount(target string) {
  proxiedRequestCounts.WithLabelValues(target).Inc()
}

func UpdateTriggerCount(target string) {
  triggerCounts.WithLabelValues(target).Inc()
}

func UpdateConnCount(connType string) {
  connCounts.WithLabelValues(connType).Inc()
}

func UpdateTCPConnCount(tcpType string) {
  tcpConnections.WithLabelValues(tcpType).Inc()
}

func ClearMetrics() {
  requestCounts.Reset()
  requestCountsByHeaders.Reset()
  requestCountsByURIs.Reset()
  proxiedRequestCounts.Reset()
  triggerCounts.Reset()
  connCounts.Reset()
  tcpConnections.Reset()
}

func clearMetrics(w http.ResponseWriter, r *http.Request) {
  ClearMetrics()
  fmt.Fprintln(w, "Metrics cleared")
}

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  metricsRouter := r.PathPrefix("/metrics").Subrouter()
  util.AddRoute(metricsRouter, "", promhttp.HandlerFor(customRegistry, promhttp.HandlerOpts{}).ServeHTTP, "GET")
  util.AddRoute(metricsRouter, "/go", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP, "GET")
  util.AddRoute(metricsRouter, "/clear", clearMetrics, "POST")
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    UpdateRequestCount("all")
    if util.IsAdminRequest(r) {
      UpdateRequestCount("admin")
    } else if util.IsMetricsRequest(r) {
      UpdateRequestCount("metrics")
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
