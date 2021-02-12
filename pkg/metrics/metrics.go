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
  requestCountsByTargets = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_client_requests_by_targets", Help: "Number of client requests by target"}, []string{"target"})
  failureCountsByTargets = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_client_failures_by_targets", Help: "Number of failed client requests by target"}, []string{"target"})
  requestCountsByClient = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_requests_by_client", Help: "Number of requests by client"}, []string{"client"})
  proxiedRequestCounts = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_proxied_requests", Help: "Number of proxied requests"}, []string{"proxyTarget"})
  triggerCounts = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_triggers", Help: "Number of triggered requests"}, []string{"triggerTarget"})
  connCounts = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_conn_counts", Help: "Number of connections by type"}, []string{"connType"})
  tcpConnections = factory.NewCounterVec(prometheus.CounterOpts{
    Name: "goto_tcp_conn_counts", Help: "Number of TCP connections by type"}, []string{"tcpType"})
  activeConnCountsByTargets = factory.NewGaugeVec(prometheus.GaugeOpts{
    Name: "goto_active_client_conn_counts_by_targets", Help: "Number of active connections by targets"}, []string{"target"})
)

func UpdateRequestCount(reqType string) {
  go requestCounts.WithLabelValues(reqType).Inc()
}

func UpdateHeaderRequestCount(header string) {
  go requestCountsByHeaders.WithLabelValues(header).Inc()
}

func UpdateURIRequestCount(uri string) {
  go requestCountsByURIs.WithLabelValues(uri).Inc()
}

func UpdateTargetRequestCount(target string) {
  go requestCountsByTargets.WithLabelValues(target).Inc()
}

func UpdateTargetFailureCount(target string) {
  go failureCountsByTargets.WithLabelValues(target).Inc()
}

func UpdateClientRequestCount(client string) {
  go requestCountsByClient.WithLabelValues(client).Inc()
}

func UpdateProxiedRequestCount(target string) {
  go proxiedRequestCounts.WithLabelValues(target).Inc()
}

func UpdateTriggerCount(target string) {
  go triggerCounts.WithLabelValues(target).Inc()
}

func UpdateConnCount(connType string) {
  go connCounts.WithLabelValues(connType).Inc()
}

func UpdateTCPConnCount(tcpType string) {
  go tcpConnections.WithLabelValues(tcpType).Inc()
}

func UpdateTargetConnCount(target string, count int) {
  go activeConnCountsByTargets.WithLabelValues(target).Set(float64(count))
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
