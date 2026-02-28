package tracking

import (
	"fmt"
	"goto/pkg/events"
	"goto/pkg/server/hooks"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tracking", setRoutes, nil)
)

func setRoutes(r, parent, root *mux.Router) {
	serverRouter := util.PathRouter(r, "/server")
	configureRoutes(util.PathRouter(serverRouter, "/request"))
	configureRoutes(util.PathRouter(serverRouter, "/response"))
}

func configureRoutes(r *mux.Router) {
	configureTrackingRoutes(util.PathRouter(r, "/track"))
	configureTrackingRoutes(util.PathRouter(r, "/upstream/track"))
}

func configureTrackingRoutes(r *mux.Router) {
	router := util.PathRouter(r, "/uri")
	util.AddRouteMultiQ(router, "", trackURIAndHeaders, []string{"key", "uri"}, "POST")
	util.AddRouteMultiQ(router, "/headers/{headers}", trackURIAndHeaders, []string{"key", "uri"}, "POST")
	util.AddRouteMultiQ(router, "/headers/{header}={value}", trackURIAndHeaderValue, []string{"key", "uri"}, "POST")
	hrouter := util.PathRouter(r, "/headers")
	util.AddRouteQ(hrouter, "/add/{headers}", trackURIAndHeaders, "key", "POST")
	util.AddRouteQ(r, "/clear", clear, "key", "POST")
	util.AddRouteQ(r, "/clear/counts", clearCounts, "key", "POST")
	util.AddRouteQ(router, "/counts", getCounts, "key", "GET")
}

func trackURIAndHeaders(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	key := util.GetStringParamValue(r, "key")
	uri := util.GetStringParamValue(r, "uri")
	headers, present := util.GetListParam(r, "headers")
	isRequest := strings.Contains(r.RequestURI, "/request")
	isUpstream := strings.Contains(r.RequestURI, "/upstream")
	msg := ""
	if (isRequest || isUpstream) && (uri != "" || present) {
		if isRequest {
			AddRequestTracking(port, key, uri, headers)
		} else {
			AddUpstreamRequestTracking(port, key, uri, headers)
		}
		hooks.GetPortHooks(port).AddHTTPTracking(key, uri, util.TransformHeaders(headers), false)
		msg = fmt.Sprintf("Port [%d] will track URI [%s] and Headers [%+v]", port, uri, headers)
		events.SendRequestEvent("Tracking URI and Headers Added", msg, r)
	} else {
		msg = "Invalid URI/Headers"
		w.WriteHeader(http.StatusBadRequest)
	}
}

func trackURIAndHeaderValue(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	key := util.GetStringParamValue(r, "key")
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	isRequest := strings.Contains(r.RequestURI, "/request")
	isUpstream := strings.Contains(r.RequestURI, "/upstream")
	msg := ""
	if (isRequest || isUpstream) && uri != "" && header != "" && value != "" {
		if isRequest {
			AddRequestTracking(port, key, uri, []string{header})
		} else {
			AddUpstreamRequestTracking(port, key, uri, []string{header})
		}
		hooks.GetPortHooks(port).AddHTTPTracking(key, uri, hooks.Headers{[2]string{header, value}}, false)
		msg = fmt.Sprintf("Port [%d] will track URI [%s] with Header:Value [%s:%s]", port, uri, header, value)
		events.SendRequestEvent("Tracking URI and Headers Added", msg, r)
	} else {
		msg = "Invalid URI/Header/Value"
		w.WriteHeader(http.StatusBadRequest)
	}
}

func clear(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	key := util.GetStringParamValue(r, "key")
	Tracker.clear(port, key)
	msg := fmt.Sprintf("Port [%d] Key [%s] tracking cleared", port, key)
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("Tracking Cleared", msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func clearCounts(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	key := util.GetStringParamValue(r, "key")
	Tracker.clearCounts(port, key)
	msg := fmt.Sprintf("Port [%d] Key [%s] tracking counts cleared", port, key)
	events.SendRequestEvent("Tracking Counts Cleared", msg, r)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getCounts(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage(fmt.Sprintf("Port [%s] reporting tracking data", util.GetRequestOrListenerPort(r)), r)
	util.WriteJsonPayload(w, Tracker)
}
