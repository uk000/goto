package rpc

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/server/request/tracking"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware         = middleware.NewMiddleware("rpc", setRoutes, nil)
	GetServiceRegistry = map[string]func(int) RPCServiceRegistry{}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	rpcRouter := util.PathRouter(r, "/{rpc:grpc|jsonrpc}/services")
	util.AddRouteWithPort(rpcRouter, "", getServices, "GET")
	util.AddRouteWithPort(rpcRouter, "/{service}", getService, "GET")
	util.AddRouteWithPort(rpcRouter, "/{service}/track", trackService, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/track/headers/{headers}", trackService, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/track/{header}={value}", trackService, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/tracking", getServiceTracking, "GET")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/clear", clearServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/header/{header}={value}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/header/{header}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/body~{regexes}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/body/paths/{paths}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/transform", setServicePayloadTransform, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}={value}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(rpcRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}", setServiceResponsePayload, "POST")
}

func getServices(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	util.WriteYaml(w, GetServiceRegistry[rpcType](util.GetRequestOrListenerPortNum(r)))
	util.AddLogMessage(fmt.Sprintf("All %s services listed", rpcType), r)
}

func getService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	rpcType := util.GetStringParamValue(r, "rpc")
	var rs RPCService
	rs, _, _, msg = CheckService(w, r, GetServiceRegistry[rpcType](util.GetRequestOrListenerPortNum(r)))
	if rs != nil {
		util.WriteYaml(w, rs)
		msg = fmt.Sprintf("Service [%s] details served", rs.GetName())
	} else {
		fmt.Fprintln(w, msg)
	}
	util.AddLogMessage(msg, r)
}

func trackService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else {
		headers, _ := util.GetListParam(r, "headers")
		header := util.GetStringParamValue(r, "header")
		value := util.GetStringParamValue(r, "value")
		rpcType := util.GetStringParamValue(r, "rpc")
		port := util.GetRequestOrListenerPortNum(r)
		sr := GetServiceRegistry[rpcType](port)
		GetRPCTracker(port).TrackService(port, sr.GetRPCService(name), headers, header, value)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Tracking %s Service [%s] with headers [%+v]", rpcType, name, []any{headers, header, value})
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getServiceTracking(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else {
		rpcType := util.GetStringParamValue(r, "rpc")
		port := util.GetRequestOrListenerPortNum(r)
		sr := GetServiceRegistry[rpcType](port)
		service := sr.GetRPCService(name)
		if tracker := GetRPCTracker(port).GetServiceTrackerJSON(service); tracker != nil {
			tracker.PortTracking = tracking.Tracker.KeyPort[service.GetName()]
			util.WriteJsonPayload(w, tracker)
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
			msg = fmt.Sprintf("JSONRPCService [%s] not found", name)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	ClearServiceResponsePayload(w, r, GetServiceRegistry[rpcType](util.GetRequestOrListenerPortNum(r)))
}

func setServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	SetServiceResponsePayload(w, r, GetServiceRegistry[rpcType](util.GetRequestOrListenerPortNum(r)))
}

func setServicePayloadTransform(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	SetServicePayloadTransform(w, r, GetServiceRegistry[rpcType](util.GetRequestOrListenerPortNum(r)))
}
