/**
 * Copyright 2021 uk
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
  util.AddRoute(peersRouter, "/{peer}?/health/{address}?", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/{peer}?/health/cleanup", cleanupUnhealthyPeers, "POST")
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
  util.AddRouteMultiQ(registryRouter, "/lockers/{label}/get/{path}", getFromDataLocker, "GET", "data", "level")
  util.AddRoute(registryRouter, "/lockers/{label}/get/{path}", getFromDataLocker, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}?/data/keys", getDataLockerPaths, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}?/data/paths", getDataLockerPaths, "GET")
  util.AddRoute(registryRouter, "/lockers/{label}?/search/{text}", searchInDataLockers, "GET")
  util.AddRouteMultiQ(registryRouter, "/lockers/{label}?/data", getDataLockers, "GET", "data", "level")
  util.AddRoute(registryRouter, "/lockers/{label}?/data", getDataLockers, "GET")
  util.AddRouteMultiQ(registryRouter, "/lockers/{label}", getLabeledLocker, "GET", "data", "events", "peers", "level")
  util.AddRoute(registryRouter, "/lockers/{label}", getLabeledLocker, "GET")
  util.AddRouteMultiQ(registryRouter, "/lockers", getAllLockers, "GET", "data", "events", "peers", "level")
  util.AddRoute(registryRouter, "/lockers", getAllLockers, "GET")

  util.AddRoute(peersRouter, "/{peer}/{address}?/locker/store/{path}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}?/locker/remove/{path}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/events/store", storePeerEvent, "POST")
  util.AddRoute(peersRouter, "/{peer}?/{address}?/lockers/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/events/flush", flushPeerEvents, "POST")
  util.AddRoute(peersRouter, "/events/clear", clearPeerEvents, "POST")

  lockerPeersRouter := registryRouter.PathPrefix("/lockers/{label}/peers").Subrouter()

  util.AddRouteMultiQ(lockerPeersRouter, "/{peers}?/events", getPeerEvents, "GET", "data", "reverse")
  util.AddRoute(lockerPeersRouter, "/{peers}?/events", getPeerEvents, "GET")
  util.AddRouteMultiQ(peersRouter, "/{peer}?/events", getPeerEvents, "GET", "data", "reverse")
  util.AddRoute(peersRouter, "/{peer}?/events", getPeerEvents, "GET")

  util.AddRouteMultiQ(lockerPeersRouter, "/{peers}?/events/search/{text}", searchInPeerEvents, "GET", "data", "reverse")
  util.AddRoute(lockerPeersRouter, "/{peers}?/events/search/{text}", searchInPeerEvents, "GET")
  util.AddRouteMultiQ(peersRouter, "/{peer}?/events/search/{text}", searchInPeerEvents, "GET", "data", "reverse")
  util.AddRoute(peersRouter, "/{peer}?/events/search/{text}", searchInPeerEvents, "GET")

  util.AddRoute(lockerPeersRouter, "/{peer}?/instances/client/results/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(peersRouter, "/{peer}?/instances/client/results/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/details/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(peersRouter, "/{peer}?/client/results/details/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/summary/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(peersRouter, "/{peer}?/client/results/summary/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/targets={targets}?", getPeersClientResults, "GET")
  util.AddRoute(peersRouter, "/{peer}?/client/results/targets={targets}?", getPeersClientResults, "GET")

  util.AddRoute(peersRouter, "/client/results/all/{enable}", enableAllClientResultsCollection, "POST", "PUT")
  util.AddRoute(peersRouter, "/client/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")

  util.AddRouteMultiQ(lockerPeersRouter, "/{peer}/{address}?/get/{path}", getFromPeerLocker, "GET", "data", "level")
  util.AddRoute(lockerPeersRouter, "/{peer}/{address}?/locker/get/{path}", getFromPeerLocker, "GET")
  util.AddRouteMultiQ(lockerPeersRouter, "/{peer}?/{address}?", getPeerLocker, "GET", "data", "events", "level")
  util.AddRoute(lockerPeersRouter, "/{peer}?/{address}?", getPeerLocker, "GET")

  util.AddRouteMultiQ(peersRouter, "/{peer}/{address}?/locker/get/{path}", getFromPeerLocker, "GET", "data", "level")
  util.AddRoute(peersRouter, "/{peer}/{address}?/locker/get/{path}", getFromPeerLocker, "GET")
  util.AddRouteMultiQ(peersRouter, "/{peer}?/{address}?/lockers", getPeerLocker, "GET", "data", "events", "level")
  util.AddRoute(peersRouter, "/{peer}?/{address}?/lockers", getPeerLocker, "GET")

  util.AddRoute(peersRouter, "/{peer}?/targets/add", addPeerTarget, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/remove", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/remove/all", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/invoke", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}?/targets/invoke/all", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/stop", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}?/targets/stop/all", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}?/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/{peer}?/targets", getPeerTargets, "GET")

  util.AddRoute(peersRouter, "/{peer}?/jobs/add", addPeerJob, "POST", "PUT")
  util.AddRoute(peersRouter, "/{peer}/jobs/add/script/{name}", addPeerJobScriptOrFile, "POST", "PUT")
  util.AddRouteQ(peersRouter, "/{peer}/jobs/store/file/{name}", addPeerJobScriptOrFile, "path", "POST", "PUT")
  util.AddRoute(peersRouter, "/{peer}/jobs/store/file/{name}", addPeerJobScriptOrFile, "POST", "PUT")
  util.AddRoute(peersRouter, "/jobs/add/script/{name}", addPeerJobScriptOrFile, "POST", "PUT")
  util.AddRouteQ(peersRouter, "/jobs/store/file/{name}", addPeerJobScriptOrFile, "path", "POST", "PUT")
  util.AddRoute(peersRouter, "/jobs/store/file/{name}", addPeerJobScriptOrFile, "POST", "PUT")
  util.AddRoute(peersRouter, "/{peer}?/jobs/{jobs}/remove", removePeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}?/jobs/{jobs}/run", runPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}?/jobs/run/all", runPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}?/jobs/{jobs}/stop", stopPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}?/jobs/stop/all", stopPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}?/jobs/clear", clearPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}?/jobs", getPeerJobs, "GET")

  util.AddRoute(peersRouter, "/track/headers/clear", clearPeersTrackingHeaders, "POST", "PUT")
  util.AddRoute(peersRouter, "/track/headers/{headers}", addPeersTrackingHeaders, "POST", "PUT")
  util.AddRoute(peersRouter, "/track/headers", getPeersTrackingHeaders, "GET")

  util.AddRoute(peersRouter, "/track/time/clear", clearPeersTrackingTimeBuckets, "POST", "PUT")
  util.AddRoute(peersRouter, "/track/time/{buckets}", addPeersTrackingTimeBuckets, "POST", "PUT")
  util.AddRoute(peersRouter, "/track/headers", getPeersTrackingTimeBuckets, "GET")

  util.AddRouteQ(peersRouter, "/probes/{type}/set", setPeersProbe, "uri", "POST", "PUT")
  util.AddRoute(peersRouter, "/probes/{type}/set/status={status}", setPeersProbeStatus, "POST", "PUT")
  util.AddRoute(peersRouter, "/probes", getPeersProbes, "GET")

  util.AddRouteQ(peersRouter, "/{peer}?/call", callPeer, "uri", "GET", "POST", "PUT")
  util.AddRoute(peersRouter, "/{peer}?/apis", getPeersAPIs, "GET")

  util.AddRouteQ(registryRouter, "/cloneFrom", cloneFromRegistry, "url", "POST")
  util.AddRoute(registryRouter, "/dump", dumpRegistry, "GET")
  util.AddRoute(registryRouter, "/load", loadRegistryDump, "POST")
}
