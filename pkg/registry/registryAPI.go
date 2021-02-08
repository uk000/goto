package registry

import (
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "registry", SetRoutes: SetRoutes}
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
  util.AddRoute(peersRouter, "/clear/epochs", clearPeerEpochs, "POST")
  util.AddRoute(peersRouter, "/clear", clearPeers, "POST")
  util.AddRoute(peersRouter, "/copyToLocker", copyPeersToLocker, "POST")
  util.AddRoute(peersRouter, "", getPeers, "GET")

  util.AddRoute(registryRouter, "/lockers/open/{label}", openLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/close/{label}", closeOrClearLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/{label}/close", closeOrClearLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/close", closeOrClearLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/{label}/clear", closeOrClearLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/clear", closeOrClearLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/labels", getLockerLabels, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}/store/{path}", storeInLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/{label}/remove/{path}", removeFromLabeledLocker, "POST")
  util.AddRoute(registryRouter, "/lockers/{label}/get/{path}", getFromDataLocker, "GET")
  util.AddRoute(registryRouter, "/lockers/current/get/{path}", getFromDataLocker, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}/data/keys", getDataLockerPaths, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}/data/paths", getDataLockerPaths, "GET")
  util.AddRoute(registryRouter, "/lockers/data/keys", getDataLockerPaths, "GET")
  util.AddRoute(registryRouter, "/lockers/data/paths", getDataLockerPaths, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}/find/{text}", findInDataLockers, "GET")
  util.AddRoute(registryRouter, "/lockers/find/{text}", findInDataLockers, "GET")
  util.AddRouteQ(registryRouter, "/lockers/current", getLabeledLocker, "data", "{data}", "GET")
  util.AddRoute(registryRouter, "/lockers/current", getLabeledLocker, "GET")
  util.AddRouteQ(registryRouter, "/lockers/{label}", getLabeledLocker, "data", "{data}", "GET")
  util.AddRoute(registryRouter, "/lockers/{label}", getLabeledLocker, "GET")
  util.AddRouteQ(registryRouter, "/lockers", getAllLockers, "data", "{data}", "GET")
  util.AddRoute(registryRouter, "/lockers", getAllLockers, "GET")

  util.AddRoute(peersRouter, "/{peer}/{address}/locker/store/{path}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/remove/{path}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/store/{path}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/remove/{path}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/events/store", storePeerEvent, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/lockers/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/events/flush", flushPeerEvents, "POST")
  util.AddRoute(peersRouter, "/events/clear", clearPeerEvents, "POST")

  lockerPeersRouter := registryRouter.PathPrefix("/lockers/{label}/peers").Subrouter()

  util.AddRoute(lockerPeersRouter, "/{peer}/events", getPeerEvents, "GET")
  util.AddRoute(lockerPeersRouter, "/events/unified", getPeerEvents, "GET")
  util.AddRoute(lockerPeersRouter, "/events", getPeerEvents, "GET")
  util.AddRoute(peersRouter, "/{peer}/events", getPeerEvents, "GET")
  util.AddRoute(peersRouter, "/events/unified", getPeerEvents, "GET")
  util.AddRoute(peersRouter, "/events", getPeerEvents, "GET")

  util.AddRouteQ(lockerPeersRouter, "/{peer}/targets/results", getTargetsSummaryResults, "detailed", "{detailed}", "GET")
  util.AddRoute(lockerPeersRouter, "/{peer}/targets/results", getTargetsSummaryResults, "GET")
  util.AddRouteQ(lockerPeersRouter, "/targets/results", getTargetsSummaryResults, "detailed", "{detailed}", "GET")
  util.AddRoute(lockerPeersRouter, "/targets/results", getTargetsSummaryResults, "GET")
  util.AddRouteQ(peersRouter, "/lockers/targets/results", getTargetsSummaryResults, "detailed", "{detailed}", "GET")
  util.AddRoute(peersRouter, "/lockers/targets/results", getTargetsSummaryResults, "GET")
  util.AddRouteQ(peersRouter, "/{peer}/targets/results", getTargetsSummaryResults, "detailed", "{detailed}", "GET")
  util.AddRoute(peersRouter, "/{peer}/targets/results", getTargetsSummaryResults, "GET")

  util.AddRoute(lockerPeersRouter, "/{peer}/{address}/locker/get/{path}", getFromPeerLocker, "GET")
  util.AddRouteQ(lockerPeersRouter, "/{peer}/{address}", getPeerLocker, "data", "{data}", "GET")
  util.AddRoute(lockerPeersRouter, "/{peer}/{address}", getPeerLocker, "GET")
  util.AddRouteQ(lockerPeersRouter, "/{peer}", getPeerLocker, "data", "{data}", "GET")
  util.AddRoute(lockerPeersRouter, "/{peer}", getPeerLocker, "GET")
  util.AddRouteQ(lockerPeersRouter, "", getPeerLocker, "data", "{data}", "GET")
  util.AddRoute(lockerPeersRouter, "", getPeerLocker, "GET")

  util.AddRoute(peersRouter, "/{peer}/{address}/locker/get/{path}", getFromPeerLocker, "GET")
  util.AddRouteQ(peersRouter, "/{peer}/{address}/locker", getPeerLocker, "data", "{data}", "GET")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker", getPeerLocker, "GET")
  util.AddRouteQ(peersRouter, "/{peer}/locker", getPeerLocker, "data", "{data}", "GET")
  util.AddRoute(peersRouter, "/{peer}/locker", getPeerLocker, "GET")
  util.AddRouteQ(peersRouter, "/lockers", getPeerLocker, "data", "{data}", "GET")
  util.AddRoute(peersRouter, "/lockers", getPeerLocker, "GET")

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
  util.AddRoute(peersRouter, "/jobs/add", addPeerJob, "POST")
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
  util.AddRoute(peersRouter, "/track/headers", getPeersTrackingHeaders, "GET")

  util.AddRouteQ(peersRouter, "/probes/{type}/set", setPeersProbe, "uri", "{uri}", "POST", "PUT")
  util.AddRoute(peersRouter, "/probes/{type}/set/status={status}", setPeersProbeStatus, "POST", "PUT")
  util.AddRoute(peersRouter, "/probes", getPeersProbes, "GET")

  util.AddRouteQ(peersRouter, "/{peer}/call", callPeer, "uri", "{uri}", "GET", "POST", "PUT")
  util.AddRouteQ(peersRouter, "/call", callPeer, "uri", "{uri}", "GET", "POST", "PUT")

  util.AddRouteQ(registryRouter, "/cloneFrom", cloneFromRegistry, "url", "{url}", "POST")
  util.AddRoute(registryRouter, "/lockers/{label}/dump/{path}", dumpLockerData, "GET")
  util.AddRoute(registryRouter, "/lockers/all/dump", dumpLockerData, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}/dump", dumpLockerData, "GET")
  util.AddRoute(registryRouter, "/dump", dumpRegistry, "GET")
  util.AddRoute(registryRouter, "/load", loadRegistryDump, "POST")
}
