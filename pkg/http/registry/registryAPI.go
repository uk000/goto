package registry

import (
	"goto/pkg/util"

	"github.com/gorilla/mux"
)
var (
  Handler      util.ServerHandler       = util.ServerHandler{Name: "registry", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  registryRouter := r.PathPrefix("/registry").Subrouter()
  peersRouter := registryRouter.PathPrefix("/peers").Subrouter()
  util.AddRoute(peersRouter, "/add", addPeer, "POST")
  util.AddRoute(peersRouter, "/{peer}/remember", addPeer, "POST")
  util.AddRoute(peersRouter, "/{peer}/remove/{address}", removePeer, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/health/{address}", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/{peer}/health", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/health", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/{peer}/health/cleanup", cleanupUnhealthyPeers, "POST")
  util.AddRoute(peersRouter, "/health/cleanup", cleanupUnhealthyPeers, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/store/{keys}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/remove/{keys}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/lock/{keys}", lockInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/lock", lockPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/store/{keys}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/remove/{keys}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/lock/{keys}", lockInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/lock", lockPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/lockers/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/{peer}/locker", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/lockers", getPeerLocker, "GET")
  util.AddRouteQ(peersRouter, "/lockers/targets/results", getTargetsSummaryResults, "detailed", "{detailed}", "GET")
  util.AddRoute(peersRouter, "/lockers/targets/results", getTargetsSummaryResults, "GET")

  util.AddRoute(peersRouter, "/{peer}/targets/add", addPeerTarget, "POST")
  util.AddRoute(peersRouter, "/targets/add", addPeerTarget, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/remove", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/remove/all", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/invoke", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/invoke/all", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/targets/invoke/all", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/stop", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/stop/all", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/targets/stop/all", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets", getPeerTargets, "GET")
  util.AddRoute(peersRouter, "/targets/results/all/{enable}", enableAllTargetsResultsCollection, "POST", "PUT")
  util.AddRoute(peersRouter, "/targets/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")
  util.AddRoute(peersRouter, "/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/targets", getPeerTargets, "GET")

  util.AddRoute(peersRouter, "/{peer}/jobs/add", addPeerJob, "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/remove", removePeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/run", runPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/run/all", runPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/stop", stopPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/stop/all", stopPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/clear", clearPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs", getPeerJobs, "GET")
  util.AddRoute(peersRouter, "/jobs/clear", clearPeerJobs, "POST")
  util.AddRoute(peersRouter, "/jobs", getPeerJobs, "GET")

  util.AddRoute(peersRouter, "/track/headers/{headers}", addPeersTrackingHeaders, "POST", "PUT")
  util.AddRouteQ(peersRouter, "/probe/{type}/set", setProbe, "uri", "{uri}", "POST", "PUT")
  util.AddRoute(peersRouter, "/probe/{type}/status/set/{status}", setProbeStatus, "POST", "PUT")

  util.AddRouteQ(peersRouter, "/{peer}/call", callPeer, "uri", "{uri}", "GET", "POST", "PUT")
  util.AddRouteQ(peersRouter, "/call", callPeer, "uri", "{uri}", "GET", "POST", "PUT")

  util.AddRoute(peersRouter, "/clear/epochs", clearPeerEpochs, "POST")
  util.AddRoute(peersRouter, "/clear", clearPeers, "POST")
  util.AddRoute(peersRouter, "", getPeers, "GET")
}
