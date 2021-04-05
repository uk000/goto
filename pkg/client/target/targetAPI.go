package target

import (
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "client", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  targetsRouter := r.PathPrefix("/targets").Subrouter()
  util.AddRoute(targetsRouter, "/add", addTarget, "POST")
  util.AddRoute(targetsRouter, "/{targets}/remove", removeTargets, "POST")
  util.AddRoute(targetsRouter, "/{targets}/invoke", invokeTargets, "POST")
  util.AddRoute(targetsRouter, "/invoke/all", invokeTargets, "POST")
  util.AddRoute(targetsRouter, "/{targets}/stop", stopTargets, "POST")
  util.AddRoute(targetsRouter, "/stop/all", stopTargets, "POST")
  util.AddRoute(targetsRouter, "/clear", clearTargets, "POST")
  util.AddRoute(targetsRouter, "/active", getActiveTargets, "GET")
  util.AddRoute(targetsRouter, "/cacert/add", addCACert, "PUT", "POST")
  util.AddRoute(targetsRouter, "/cacert/remove", removeCACert, "PUT", "POST")
  util.AddRoute(targetsRouter, "/{target}?", getTargets, "GET")

  util.AddRoute(r, "/track/headers/clear", clearTrackingHeaders, "POST")
  util.AddRoute(r, "/track/headers/{headers}", addTrackingHeaders, "POST", "PUT")
  util.AddRoute(r, "/track/headers", getTrackingHeaders, "GET")

  util.AddRoute(r, "/track/time/clear", clearTrackingTimeBuckets, "POST")
  util.AddRoute(r, "/track/time/{buckets}", addTrackingTimeBuckets, "POST", "PUT")
  util.AddRoute(r, "/track/time", getTrackingTimeBuckets, "GET")

  util.AddRoute(r, "/results/all/{enable}", enableAllTargetsResultsCollection, "POST", "PUT")
  util.AddRoute(r, "/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")
  util.AddRoute(r, "/results", getResults, "GET")
  util.AddRoute(r, "/results/invocations", getInvocationResults, "GET")
  util.AddRoute(r, "/results/clear", clearResults, "POST")
}
