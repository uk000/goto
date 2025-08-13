package grpcclient

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc/metadata"
)

var (
	Middleware = middleware.NewMiddleware("rpc", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	router := util.PathRouter(r, "/grpc")

	util.AddRouteWithPort(router, "/call/{endpoint}/{service}/{method}", callServiceMethod, "POST")

}

func callServiceMethod(w http.ResponseWriter, r *http.Request) {
	msg := ""
	defer func() {
		if msg != "" {
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	}()
	endpoint := util.GetStringParamValue(r, "endpoint")
	serviceName := util.GetStringParamValue(r, "service")
	methodName := util.GetStringParamValue(r, "method")
	if endpoint == "" || serviceName == "" || methodName == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing endpoint/service/method"
		return
	}
	client, err := CreateGRPCClient(nil, "", endpoint, "", "", &GRPCOptions{IsTLS: false, VerifyTLS: false, KeepOpen: 1 * time.Minute})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	err = client.LoadServiceMethodFromReflection(serviceName, methodName)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	method := client.Service.Methods[methodName]
	if method == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Invalid method"
		return
	}
	var content [][]byte
	if method.IsClientStream {
		content = util.ReadArrayOfArrays(r.Body)
	} else {
		content = [][]byte{util.ReadBytes(r.Body)}
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "plain/text"
	}
	resp, err := client.Invoke(methodName, nil, content)
	if err != nil || resp == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	w.WriteHeader(http.StatusOK)
	response := map[string]any{}
	response["headers"] = metadata.Join(resp.ResponseHeaders, resp.ResponseTrailers)
	if len(resp.ResponsePayload) > 0 {
		jsons := []util.JSON{}
		for _, r := range resp.ResponsePayload {
			jsons = append(jsons, util.FromJSONText(r))
		}
		response["payload"] = jsons
	} else {
		response["payload"] = "<No Payload>"

	}
	util.WriteJson(w, response)
	util.AddLogMessage(fmt.Sprintf("Invoked Service [%s] Method [%s] with Response [%+v]", serviceName, methodName, response), r)
}
