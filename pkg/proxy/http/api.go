package httpproxy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("proxy", setRoutes, nil)
)

func setRoutes(r *mux.Router, root *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	httpProxyRouter := util.PathPrefix(proxyRouter, "/http")
	util.AddRoute(httpProxyRouter, "/{o:enable|disable}", enableProxy, "POST", "PUT")

	httpTargetsRouter := util.PathPrefix(httpProxyRouter, "/targets")
	util.AddRoute(httpTargetsRouter, "/add", addHTTPTarget, "POST", "PUT")
	util.AddRoute(httpTargetsRouter, "/clear", clearHTTPTarget, "POST", "PUT")
	util.AddRoute(httpTargetsRouter, "/{target}/remove", removeHTTPTarget, "POST", "PUT")
	util.AddRoute(httpTargetsRouter, "/{target}/{o:enable|disable}", enableHTTPTarget, "POST", "PUT")
	util.AddRoute(httpTargetsRouter, "", getProxyTargets, "GET")
	util.AddRoute(httpTargetsRouter, "/{target}/tracker", getProxyTargetTracker, "GET")
	util.AddRoute(httpProxyRouter, "/trackers/{all}?", getProxyTrackers, "GET")
	util.AddRoute(proxyRouter, "/trackers/{all}?/clear", clearProxyTrackers, "POST")
}

func enableProxy(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	o := util.GetStringParamValue(r, "o")
	enable := strings.EqualFold(o, "enable")
	getPortProxy(port).enable(enable)
	msg := fmt.Sprintf("Proxy [%d] %sd", port, o)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addHTTPTarget(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	msg := ""
	if target, err := parseTarget(r.Body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse proxy target with error: %s", err.Error())
		fmt.Fprintln(w, msg)
	} else {
		if target.Port > 0 {
			port = target.Port
		}
		proxy := getPortProxy(port)
		if err := proxy.addTarget(target); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Failed to process target with error: %s", err.Error())
			fmt.Fprintln(w, msg)
		} else {
			util.WriteJsonOrYAMLPayload(w, target, true)
			msg = fmt.Sprintf("Proxy [%d] added target [%s]", port, target.Name)
		}
	}
	util.AddLogMessage(msg, r)
}

func clearHTTPTarget(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	getPortProxy(port).clearTargets()
	msg := fmt.Sprintf("Proxy [%d] targets cleared", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeHTTPTarget(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	target := util.GetStringParamValue(r, "target")
	msg := ""
	if getPortProxy(port).removeTarget(target) {
		msg = fmt.Sprintf("Proxy [%d] target [%s] removed", port, target)
	} else {
		msg = fmt.Sprintf("Proxy [%d] target [%s] doesn't exist", port, target)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func enableHTTPTarget(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	target := util.GetStringParamValue(r, "target")
	o := util.GetStringParamValue(r, "o")
	enable := strings.EqualFold(o, "enable")
	msg := ""
	if getPortProxy(port).enableTarget(target, enable) {
		msg = fmt.Sprintf("Proxy [%d] target [%s] %sd", port, target, o)
	} else {
		msg = fmt.Sprintf("Proxy [%d] target [%s] doesn't exist", port, target)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getProxyTargets(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := getPortProxy(port)
	util.AddLogMessage("Reporting proxy targets", r)
	result := map[string]any{}
	result["port"] = port
	result["http"] = proxy.Targets
	util.WriteJsonPayload(w, result)
}

func checkAndGetTarget(proxy *Proxy, w http.ResponseWriter, r *http.Request) *Target {
	name := util.GetStringParamValue(r, "target")
	target := proxy.getTarget(name)
	if target == nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid target: %s\n", name)
	}
	return target
}

func getProxyTargetTracker(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := getPortProxy(port)
	target := checkAndGetTarget(proxy, w, r)
	result := map[string]any{}
	result["port"] = port
	result["target"] = target.Name
	if t := proxy.HTTPTracker.TargetTrackers[target.Name]; t != nil {
		result["http"] = t
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage(fmt.Sprintf("Proxy[%d]: HTTP Target [%s] Reported", port, target.Name), r)
}

func getProxyTrackers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := strings.Contains(r.RequestURI, "all")
	result := map[string]any{}
	if all {
		for port, proxy := range portProxy {
			result[strconv.Itoa(port)] = map[string]any{
				"enabled": proxy.Enabled,
				"targets": proxy.Targets,
				"http":    proxy.HTTPTracker,
			}
		}
	} else {
		proxy := getPortProxy(port)
		result["port"] = port
		result["enabled"] = proxy.Enabled
		result["targets"] = proxy.Targets
		result["http"] = proxy.HTTPTracker
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage(fmt.Sprintf("Proxy[%d]: HTTP Tracking Data Reported", port), r)
}

func clearProxyTrackers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := strings.Contains(r.RequestURI, "all")
	if all {
		for _, proxy := range portProxy {
			proxy.initTracker()
		}
	} else {
		proxy := getPortProxy(port)
		proxy.initTracker()
	}
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy[%d]: HTTP Proxy Tracking Info Cleared", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
