/**
 * Copyright 2025 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package metrics

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"goto/pkg/util"
)

type PrometheusMetrics struct {
	registry                           *prometheus.Registry
	requestCounts                      *prometheus.CounterVec
	requestCountsByHeaders             *prometheus.CounterVec
	requestCountsByHeaderValues        *prometheus.CounterVec
	requestCountsByURIs                *prometheus.CounterVec
	requestCountsByURIsAndStatus       *prometheus.CounterVec
	requestCountsByHeadersAndURIs      *prometheus.CounterVec
	requestCountsByHeadersAndStatus    *prometheus.CounterVec
	requestCountsByPort                *prometheus.CounterVec
	requestCountsByPortAndURIs         *prometheus.CounterVec
	requestCountsByPortAndHeaders      *prometheus.CounterVec
	requestCountsByPortAndHeaderValues *prometheus.CounterVec
	requestCountsByProtocol            *prometheus.CounterVec
	requestCountsByProtocolAndURIs     *prometheus.CounterVec
	requestCountsByTargets             *prometheus.CounterVec
	failureCountsByTargets             *prometheus.CounterVec
	requestCountsByClient              *prometheus.CounterVec
	proxiedRequestCounts               *prometheus.CounterVec
	triggerCounts                      *prometheus.CounterVec
	connCounts                         *prometheus.CounterVec
	tcpConnections                     *prometheus.CounterVec
	totalConnectionsByTargets          *prometheus.CounterVec
	activeConnCountsByTargets          *prometheus.GaugeVec
}

type ServerStats struct {
	RequestCountsByHeaders               map[string]int                       `json:"requestCountsByHeaders"`
	RequestCountsByURIs                  map[string]int                       `json:"requestCountsByURIs"`
	RequestCountsByURIsAndStatus         map[string]map[string]int            `json:"requestCountsByURIsAndStatus"`
	RequestCountsByURIsAndHeaders        map[string]map[string]int            `json:"requestCountsByURIsAndHeaders"`
	RequestCountsByURIsAndHeaderValues   map[string]map[string]map[string]int `json:"requestCountsByURIsAndHeaderValues"`
	RequestCountsByHeadersAndStatus      map[string]map[string]int            `json:"requestCountsByHeadersAndStatus"`
	RequestCountsByHeaderValuesAndStatus map[string]map[string]map[string]int `json:"requestCountsByHeaderValuesAndStatus"`
	RequestCountsByPortAndURIs           map[string]map[string]int            `json:"requestCountsByPortAndURIs"`
	RequestCountsByPortAndHeaders        map[string]map[string]int            `json:"requestCountsByPortAndHeaders"`
	RequestCountsByPortAndHeaderValues   map[string]map[string]map[string]int `json:"requestCountsByPortAndHeaderValues"`
	RequestCountsByURIsAndProtocol       map[string]map[string]int            `json:"requestCountsByURIsAndProtocol"`
	lock                                 sync.RWMutex
}

var (
	Handler         = util.ServerHandler{"metrics", SetRoutes, Middleware}
	promMetrics     = NewPrometheusMetrics()
	serverStats     = NewServerStats()
	ConnTracker     = make(chan string, 10)
	stopConnTracker = make(chan bool, 2)
)

func Startup() {
	go func() {
	ConnTracker:
		for {
			select {
			case target := <-ConnTracker:
				IncrementTargetConnCount(target)
			case <-stopConnTracker:
				break ConnTracker
			}
		}
	}()
}

func Shutdown() {
	stopConnTracker <- true
}

func NewServerStats() *ServerStats {
	ss := &ServerStats{}
	ss.init()
	return ss
}

func NewPrometheusMetrics() *PrometheusMetrics {
	pm := &PrometheusMetrics{}
	pm.registry = prometheus.NewRegistry()
	factory := promauto.With(pm.registry)
	pm.requestCounts = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_type", Help: "Number of requests by type"}, []string{"requestType"})
	pm.requestCountsByHeaders = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_headers", Help: "Number of requests by headers"}, []string{"requestHeader"})
	pm.requestCountsByHeaderValues = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_header_values", Help: "Number of requests by header values"}, []string{"requestHeader", "headerValue"})
	pm.requestCountsByURIs = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_uris", Help: "Number of requests by URIs"}, []string{"uri"})
	pm.requestCountsByURIsAndStatus = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_uris_and_status", Help: "Number of requests by uris and status code"}, []string{"uri", "statusCode"})
	pm.requestCountsByHeadersAndURIs = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_headers_and_uris", Help: "Number of requests by headers and uris"}, []string{"requestHeader", "uri"})
	pm.requestCountsByHeadersAndStatus = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_headers_and_status", Help: "Number of requests by headers and status code"}, []string{"requestHeader", "statusCode"})
	pm.requestCountsByPort = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_port", Help: "Number of requests by port"}, []string{"port"})
	pm.requestCountsByPortAndURIs = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_port_and_uris", Help: "Number of requests by port and uris"}, []string{"port", "uri"})
	pm.requestCountsByPortAndHeaders = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_port_and_headers", Help: "Number of requests by port and request headers"}, []string{"port", "requestHeader"})
	pm.requestCountsByPortAndHeaderValues = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_port_and_header_values", Help: "Number of requests by port and request header values"}, []string{"port", "requestHeader", "headerValue"})
	pm.requestCountsByProtocol = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_protocol", Help: "Number of requests by protocol"}, []string{"protocol"})
	pm.requestCountsByProtocolAndURIs = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_protocol_and_uris", Help: "Number of requests by protocol and uris"}, []string{"protocol", "uri"})
	pm.requestCountsByTargets = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_invocations_by_targets", Help: "Number of client invocations by target"}, []string{"target"})
	pm.failureCountsByTargets = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_failed_invocations_by_targets", Help: "Number of failed invocations by target"}, []string{"target"})
	pm.requestCountsByClient = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_requests_by_client", Help: "Number of server requests by client"}, []string{"client"})
	pm.proxiedRequestCounts = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_proxied_requests", Help: "Number of proxied requests"}, []string{"proxyTarget"})
	pm.triggerCounts = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_triggers", Help: "Number of triggered requests"}, []string{"triggerTarget"})
	pm.connCounts = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_conn_counts", Help: "Number of connections by type"}, []string{"connType"})
	pm.tcpConnections = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_tcp_conn_counts", Help: "Number of TCP connections by type"}, []string{"tcpType"})
	pm.totalConnectionsByTargets = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "goto_total_conns_by_targets", Help: "Total connections by targets"}, []string{"target"})
	pm.activeConnCountsByTargets = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "goto_active_conn_counts_by_targets", Help: "Number of active connections by targets"}, []string{"target"})
	return pm
}

func (pm *PrometheusMetrics) reset() {
	pm.requestCounts.Reset()
	pm.requestCountsByHeaders.Reset()
	pm.requestCountsByHeaderValues.Reset()
	pm.requestCountsByURIs.Reset()
	pm.requestCountsByURIsAndStatus.Reset()
	pm.requestCountsByHeadersAndURIs.Reset()
	pm.requestCountsByHeadersAndStatus.Reset()
	pm.requestCountsByPort.Reset()
	pm.requestCountsByPortAndURIs.Reset()
	pm.requestCountsByPortAndHeaders.Reset()
	pm.requestCountsByPortAndHeaderValues.Reset()
	pm.requestCountsByProtocol.Reset()
	pm.requestCountsByProtocolAndURIs.Reset()
	pm.requestCountsByTargets.Reset()
	pm.failureCountsByTargets.Reset()
	pm.requestCountsByClient.Reset()
	pm.proxiedRequestCounts.Reset()
	pm.triggerCounts.Reset()
	pm.connCounts.Reset()
	pm.tcpConnections.Reset()
	pm.totalConnectionsByTargets.Reset()
	pm.activeConnCountsByTargets.Reset()
}

func (ss *ServerStats) init() {
	ss.lock.Lock()
	defer ss.lock.Unlock()
	ss.RequestCountsByURIs = map[string]int{}
	ss.RequestCountsByURIsAndStatus = map[string]map[string]int{}
	ss.RequestCountsByHeaders = map[string]int{}
	ss.RequestCountsByURIsAndHeaders = map[string]map[string]int{}
	ss.RequestCountsByURIsAndHeaderValues = map[string]map[string]map[string]int{}
	ss.RequestCountsByHeadersAndStatus = map[string]map[string]int{}
	ss.RequestCountsByHeaderValuesAndStatus = map[string]map[string]map[string]int{}
	ss.RequestCountsByPortAndURIs = map[string]map[string]int{}
	ss.RequestCountsByPortAndHeaders = map[string]map[string]int{}
	ss.RequestCountsByPortAndHeaderValues = map[string]map[string]map[string]int{}
	ss.RequestCountsByURIsAndProtocol = map[string]map[string]int{}
}

func UpdateRequestCount(reqType string) {
	go promMetrics.requestCounts.WithLabelValues(reqType).Inc()
}

func UpdateHeaderRequestCount(port, uri, header, headerValue, statusCode string) {
	go func() {
		promMetrics.requestCountsByHeaders.WithLabelValues(header).Inc()
		promMetrics.requestCountsByHeaderValues.WithLabelValues(header, headerValue).Inc()
		promMetrics.requestCountsByHeadersAndURIs.WithLabelValues(header, uri).Inc()
		promMetrics.requestCountsByHeadersAndStatus.WithLabelValues(header, statusCode).Inc()
		promMetrics.requestCountsByPortAndHeaders.WithLabelValues(port, header).Inc()
		promMetrics.requestCountsByPortAndHeaderValues.WithLabelValues(port, header, headerValue).Inc()
		serverStats.lock.Lock()
		defer serverStats.lock.Unlock()
		serverStats.RequestCountsByHeaders[header]++

		if serverStats.RequestCountsByURIsAndHeaders[uri] == nil {
			serverStats.RequestCountsByURIsAndHeaders[uri] = map[string]int{}
		}
		serverStats.RequestCountsByURIsAndHeaders[uri][header]++

		if serverStats.RequestCountsByURIsAndHeaderValues[uri] == nil {
			serverStats.RequestCountsByURIsAndHeaderValues[uri] = map[string]map[string]int{}
		}
		if serverStats.RequestCountsByURIsAndHeaderValues[uri][header] == nil {
			serverStats.RequestCountsByURIsAndHeaderValues[uri][header] = map[string]int{}
		}
		serverStats.RequestCountsByURIsAndHeaderValues[uri][header][headerValue]++

		if serverStats.RequestCountsByHeadersAndStatus[header] == nil {
			serverStats.RequestCountsByHeadersAndStatus[header] = map[string]int{}
		}
		serverStats.RequestCountsByHeadersAndStatus[header][statusCode]++

		if serverStats.RequestCountsByHeaderValuesAndStatus[header] == nil {
			serverStats.RequestCountsByHeaderValuesAndStatus[header] = map[string]map[string]int{}
		}
		if serverStats.RequestCountsByHeaderValuesAndStatus[header][headerValue] == nil {
			serverStats.RequestCountsByHeaderValuesAndStatus[header][headerValue] = map[string]int{}
		}
		serverStats.RequestCountsByHeaderValuesAndStatus[header][headerValue][statusCode]++

		if serverStats.RequestCountsByPortAndHeaders[port] == nil {
			serverStats.RequestCountsByPortAndHeaders[port] = map[string]int{}
		}
		serverStats.RequestCountsByPortAndHeaders[port][header]++

		if serverStats.RequestCountsByPortAndHeaderValues[port] == nil {
			serverStats.RequestCountsByPortAndHeaderValues[port] = map[string]map[string]int{}
		}
		if serverStats.RequestCountsByPortAndHeaderValues[port][header] == nil {
			serverStats.RequestCountsByPortAndHeaderValues[port][header] = map[string]int{}
		}
		serverStats.RequestCountsByPortAndHeaderValues[port][header][headerValue]++
	}()
}

func UpdateURIRequestCount(uri, statusCode string) {
	go func() {
		promMetrics.requestCountsByURIs.WithLabelValues(uri).Inc()
		promMetrics.requestCountsByURIsAndStatus.WithLabelValues(uri, statusCode).Inc()
		serverStats.lock.Lock()
		defer serverStats.lock.Unlock()
		serverStats.RequestCountsByURIs[uri]++
		if serverStats.RequestCountsByURIsAndStatus[uri] == nil {
			serverStats.RequestCountsByURIsAndStatus[uri] = map[string]int{}
		}
		serverStats.RequestCountsByURIsAndStatus[uri][statusCode]++
	}()
}

func UpdatePortRequestCount(port, uri string) {
	go func() {
		promMetrics.requestCountsByPort.WithLabelValues(port).Inc()
		promMetrics.requestCountsByPortAndURIs.WithLabelValues(port, uri).Inc()
		serverStats.lock.Lock()
		defer serverStats.lock.Unlock()
		if serverStats.RequestCountsByPortAndURIs[port] == nil {
			serverStats.RequestCountsByPortAndURIs[port] = map[string]int{}
		}
		serverStats.RequestCountsByPortAndURIs[port][uri]++
	}()
}

func UpdateProtocolRequestCount(protocol, uri string) {
	go func() {
		promMetrics.requestCountsByProtocol.WithLabelValues(protocol).Inc()
		promMetrics.requestCountsByProtocolAndURIs.WithLabelValues(protocol, uri).Inc()
		serverStats.lock.Lock()
		defer serverStats.lock.Unlock()
		if serverStats.RequestCountsByURIsAndProtocol[uri] == nil {
			serverStats.RequestCountsByURIsAndProtocol[uri] = map[string]int{}
		}
		serverStats.RequestCountsByURIsAndProtocol[uri][protocol]++
	}()
}

func UpdateTargetRequestCount(target string) {
	go promMetrics.requestCountsByTargets.WithLabelValues(target).Inc()
}

func UpdateTargetFailureCount(target string) {
	go promMetrics.failureCountsByTargets.WithLabelValues(target).Inc()
}

func UpdateClientRequestCount(client string) {
	go promMetrics.requestCountsByClient.WithLabelValues(client).Inc()
}

func UpdateProxiedRequestCount(target string) {
	go promMetrics.proxiedRequestCounts.WithLabelValues(target).Inc()
}

func UpdateTriggerCount(target string) {
	go promMetrics.triggerCounts.WithLabelValues(target).Inc()
}

func UpdateConnCount(connType string) {
	go promMetrics.connCounts.WithLabelValues(connType).Inc()
}

func UpdateTCPConnCount(tcpType string) {
	go promMetrics.tcpConnections.WithLabelValues(tcpType).Inc()
}

func IncrementTargetConnCount(target string) {
	go promMetrics.totalConnectionsByTargets.WithLabelValues(target).Inc()
}

func UpdateActiveTargetConnCount(target string, count int) {
	go promMetrics.activeConnCountsByTargets.WithLabelValues(target).Set(float64(count))
}

func clearMetrics(w http.ResponseWriter, r *http.Request) {
	promMetrics.reset()
	fmt.Fprintln(w, "Metrics cleared")
}

func clearStats(w http.ResponseWriter, r *http.Request) {
	serverStats.init()
	fmt.Fprintln(w, "Stats cleared")
}

func getStats(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, serverStats)
}

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	metricsRouter := r.PathPrefix("/metrics").Subrouter()
	util.AddRoute(metricsRouter, "", promhttp.HandlerFor(promMetrics.registry, promhttp.HandlerOpts{}).ServeHTTP, "GET")
	util.AddRoute(metricsRouter, "/go", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP, "GET")
	util.AddRoute(metricsRouter, "/clear", clearMetrics, "POST")
	statsRouter := r.PathPrefix("/stats").Subrouter()
	util.AddRoute(statsRouter, "/clear", clearStats, "POST")
	util.AddRoute(statsRouter, "", getStats, "GET")
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		UpdateRequestCount("all")
		if util.IsMetricsRequest(r) {
			UpdateRequestCount("metrics")
			util.AddLogMessage("Metrics reported", r)
		} else if util.IsAdminRequest(r) {
			UpdateRequestCount("admin")
		}
	})
}
