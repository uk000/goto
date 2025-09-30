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

package registry

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/job"
	"goto/pkg/registry/locker"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("registry", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	registryRouter := r.PathPrefix("/registry").Subrouter()
	peersRouter := registryRouter.PathPrefix("/peers").Subrouter()
	addLockerMaintenanceRoutes(registryRouter, peersRouter)
	addLockerStoreRoutes(registryRouter, peersRouter)
	addLockerEventsRoutes(peersRouter)
	addPeerLockerReadRoutes(peersRouter)
	addLockerReadRoutes(registryRouter)
	addLockerPeerRoutes(peersRouter)
	addContextLockerRoutes(registryRouter)
	addPeerManagementRoutes(peersRouter)
	addPeerTargetsRoutes(peersRouter)
	addPeerJobsRoutes(peersRouter)
	addPeerTrackingRoutes(peersRouter)
	addPeerProbesRoutes(peersRouter)

	util.AddRouteQ(registryRouter, "/cloneFrom", cloneFromRegistry, "url", "POST")
	util.AddRoute(registryRouter, "/dump", dumpRegistry, "GET")
	util.AddRoute(registryRouter, "/load", loadRegistryDump, "POST")
}

func addPeerManagementRoutes(peersRouter *mux.Router) {
	util.AddRoute(peersRouter, "/add", addPeer, "POST")
	util.AddRoute(peersRouter, "/{peer}/remember", addPeer, "POST")
	util.AddRoute(peersRouter, "/{peer}/remove/{address}", removePeer, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}?/health/{address}?", checkPeerHealth, "GET")
	util.AddRoute(peersRouter, "/{peer}?/health/cleanup", cleanupUnhealthyPeers, "POST")
	util.AddRoute(peersRouter, "/clear/epochs", clearPeerEpochs, "POST")
	util.AddRoute(peersRouter, "/clear", clearPeers, "POST")
	util.AddRoute(peersRouter, "/copyToLocker", copyPeersToLocker, "POST")
	util.AddRoute(peersRouter, "", getPeers, "GET")
	util.AddRouteQ(peersRouter, "/{peer}?/call", callPeer, "uri", "GET", "POST", "PUT")
	util.AddRoute(peersRouter, "/{peer}?/apis", getPeersAPIs, "GET")
}

func addPeerTargetsRoutes(peersRouter *mux.Router) {
	util.AddRoute(peersRouter, "/{peer}?/targets/add", addPeerTarget, "POST")
	util.AddRoute(peersRouter, "/{peer}/targets/{targets}/remove", removePeerTargets, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}/targets/remove/all", removePeerTargets, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}/targets/{targets}/invoke", invokePeerTargets, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}?/targets/invoke/all", invokePeerTargets, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}/targets/{targets}/stop", stopPeerTargets, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}?/targets/stop/all", stopPeerTargets, "PUT", "POST")
	util.AddRoute(peersRouter, "/{peer}?/targets/clear", clearPeerTargets, "POST")
	util.AddRoute(peersRouter, "/{peer}?/targets", getPeerTargets, "GET")
	util.AddRoute(peersRouter, "/client/results/all/{enable}", enableAllClientResultsCollection, "POST", "PUT")
	util.AddRoute(peersRouter, "/client/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")
}

func addPeerJobsRoutes(peersRouter *mux.Router) {
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
}

func addPeerTrackingRoutes(peersRouter *mux.Router) {
	util.AddRoute(peersRouter, "/track/headers/clear", clearPeersTrackingHeaders, "POST", "PUT")
	util.AddRoute(peersRouter, "/track/headers/{headers}", addPeersTrackingHeaders, "POST", "PUT")
	util.AddRoute(peersRouter, "/track/headers", getPeersTrackingHeaders, "GET")
	util.AddRoute(peersRouter, "/track/time/clear", clearPeersTrackingTimeBuckets, "POST", "PUT")
	util.AddRoute(peersRouter, "/track/time/{buckets}", addPeersTrackingTimeBuckets, "POST", "PUT")
	util.AddRoute(peersRouter, "/track/headers", getPeersTrackingTimeBuckets, "GET")
}

func addPeerProbesRoutes(peersRouter *mux.Router) {
	util.AddRouteQ(peersRouter, "/probes/{type}/set", setPeersProbe, "uri", "POST", "PUT")
	util.AddRoute(peersRouter, "/probes/{type}/set/status={status}", setPeersProbeStatus, "POST", "PUT")
	util.AddRoute(peersRouter, "/probes", getPeersProbes, "GET")
}

func addPeer(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	peer := &Peer{}
	peerData := &PeerData{}
	msg := ""
	payload := util.Read(r.Body)
	if err := util.ReadJson(payload, peer); err == nil {
		if peerName == "" {
			registry.addPeer(peer)
			registry.peersLock.RLock()
			registry.preparePeerStartupData(peer, peerData)
			registry.peersLock.RUnlock()
			msg = fmt.Sprintf("Added Peer: %+v", *peer)
			events.SendRequestEventJSON(events.Registry_PeerAdded, peer.Name, peer, r)
		} else {
			registry.rememberPeer(peer)
			msg = fmt.Sprintf("Remembered Peer: %+v", *peer)
			peerData.Message = msg
		}
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("failed to parse json with error: %s", err.Error())
		events.SendRequestEventJSON(events.Registry_PeerRejected, err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": payload}, r)
		peerData.Message = msg
	}
	fmt.Fprintln(w, util.ToJSONText(peerData))
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func removePeer(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if peerName, present := util.GetStringParam(r, "peer"); present {
		if address, present := util.GetStringParam(r, "address"); present {
			if registry.removePeer(peerName, address) {
				w.WriteHeader(http.StatusOK)
				msg = fmt.Sprintf("Peer Removed: %s", peerName)
				events.SendRequestEvent(events.Registry_PeerRemoved, peerName, r)
			} else {
				w.WriteHeader(http.StatusNotAcceptable)
				msg = fmt.Sprintf("Peer not found: %s", peerName)
			}
		} else {
			w.WriteHeader(http.StatusBadRequest)
			msg = "No address given"
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No peer given"
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func checkPeerHealth(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	address := util.GetStringParamValue(r, "address")
	result := registry.checkPeerHealth(peerName, address)
	events.SendRequestEventJSON(events.Registry_CheckedPeersHealth,
		fmt.Sprintf("Checked health on %d peers", len(result)), result, r)
	util.WriteJsonPayload(w, result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(util.ToJSONText(result), r)
	}
}

func cleanupUnhealthyPeers(w http.ResponseWriter, r *http.Request) {
	result := registry.cleanupUnhealthyPeers(util.GetStringParamValue(r, "peer"))
	events.SendRequestEventJSON(events.Registry_CleanedUpUnhealthyPeers,
		fmt.Sprintf("Checked health on %d peers", len(result)), result, r)
	util.WriteJsonPayload(w, result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(util.ToJSONText(result), r)
	}
}

func getPeers(w http.ResponseWriter, r *http.Request) {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	util.WriteJsonPayload(w, registry.peers)
}

func flushPeerEvents(w http.ResponseWriter, r *http.Request) {
	msg := "Flushing pending events for all peers"
	result := registry.flushPeersEvents()
	w.WriteHeader(http.StatusOK)
	util.WriteJsonPayload(w, result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func clearPeerEvents(w http.ResponseWriter, r *http.Request) {
	msg := "Clearing events for all peers"
	result := registry.clearPeersEvents()
	w.WriteHeader(http.StatusOK)
	util.WriteJsonPayload(w, result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func getPeerEvents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	peerNames, _ := util.GetListParam(r, "peers")
	unified := util.GetBoolParamValue(r, "unified")
	reverse := util.GetBoolParamValue(r, "reverse")
	data := util.GetBoolParamValue(r, "data")
	all := strings.EqualFold(label, constants.LockerAll)

	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	registry.lockersLock.RUnlock()

	var locker *locker.CombiLocker
	if label == "" {
		all = true
	} else if strings.EqualFold(label, constants.LockerCurrent) {
		locker = registry.getCurrentLocker()
		label = locker.Label
	} else {
		locker = registry.labeledLockers.GetLocker(label)
	}

	if !all && locker == nil {
		msg = "Locker not found"
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, msg)
	} else {
		if len(peerNames) > 0 {
			if all {
				msg = fmt.Sprintf("Registry: Reporting events for peers %s from all lockers", peerNames)
			} else {
				msg = fmt.Sprintf("Registry: Reporting events for peers %s from locker [%s]", peerNames, label)
			}
		} else {
			if all {
				msg = "Registry: Reporting events for all peers from all lockers"
			} else {
				msg = fmt.Sprintf("Registry: Reporting events for all peers from locker [%s]", label)
			}
		}
		result := labeledLockers.GetPeerEvents(label, peerNames, unified, reverse, data)
		util.WriteJsonPayload(w, result)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func searchInPeerEvents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	peerNames, _ := util.GetListParam(r, "peers")
	key := util.GetStringParamValue(r, "text")
	unified := util.GetBoolParamValue(r, "unified")
	reverse := util.GetBoolParamValue(r, "reverse")
	data := util.GetBoolParamValue(r, "data")
	all := strings.EqualFold(label, constants.LockerAll)

	if key == "" {
		msg = "Cannot search. No key given."
		fmt.Fprintln(w, msg)
	} else {
		registry.lockersLock.RLock()
		labeledLockers := registry.labeledLockers
		registry.lockersLock.RUnlock()

		var locker *locker.CombiLocker
		if label == "" {
			all = true
		} else if strings.EqualFold(label, constants.LockerCurrent) {
			locker = registry.getCurrentLocker()
			label = locker.Label
		} else {
			locker = registry.labeledLockers.GetLocker(label)
		}
		if !all && locker == nil {
			msg = "Locker not found"
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, msg)
		} else {
			if len(peerNames) > 0 {
				if all {
					msg = fmt.Sprintf("Registry: Reporting searched events for peers %s from all lockers", peerNames)
				} else {
					msg = fmt.Sprintf("Registry: Reporting searched events for peers %s from locker [%s]", peerNames, label)
				}
			} else {
				if all {
					msg = "Registry: Reporting searched events for all peers from all lockers"
				} else {
					msg = fmt.Sprintf("Registry: Reporting searched events for all peers from locker [%s]", label)
				}
			}
			result := labeledLockers.SearchInPeerEvents(label, peerNames, key, unified, reverse, data)
			util.WriteJsonPayload(w, result)
		}
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func checkBadPods(result PeerResults, w http.ResponseWriter) {
	anyBad := false
	for _, pods := range result {
		for _, status := range pods {
			if !status && len(pods) <= 1 {
				anyBad = true
			}
		}
	}
	if anyBad {
		w.WriteHeader(http.StatusFailedDependency)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func addPeerTarget(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	t := &PeerTarget{}
	body := util.Read(r.Body)
	if err := util.ReadJson(body, t); err == nil {
		if err := invocation.ValidateSpec(t.InvocationSpec); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
			events.SendRequestEventJSON(events.Registry_PeerTargetRejected, err.Error(),
				map[string]interface{}{"error": err.Error(), "payload": body}, r)
			log.Println(err)
		} else {
			result := registry.addPeerTarget(peerName, t)
			checkBadPods(result, w)
			msg = util.ToJSONText(result)
			events.SendRequestEventJSON(events.Registry_PeerTargetAdded, t.Name,
				map[string]interface{}{"target": t, "result": result}, r)
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "failed to parse json"
		events.SendRequestEventJSON(events.Registry_PeerTargetRejected, err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": body}, r)
		log.Println(err)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func removePeerTargets(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	targets, _ := util.GetListParam(r, "targets")
	result := registry.removePeerTargets(peerName, targets)
	checkBadPods(result, w)
	msg := util.ToJSONText(result)
	events.SendRequestEventJSON(events.Registry_PeerTargetsRemoved, util.GetStringParamValue(r, "targets"),
		map[string]interface{}{"targets": targets, "result": result}, r)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func stopPeerTargets(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	targets := util.GetStringParamValue(r, "targets")
	result := registry.stopPeerTargets(peerName, targets)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerTargetsStopped, util.GetStringParamValue(r, "targets"),
		map[string]interface{}{"targets": targets, "result": result}, r)
	msg := util.ToJSONText(result)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	targets := util.GetStringParamValue(r, "targets")
	result := registry.invokePeerTargets(peerName, targets)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerTargetsInvoked, util.GetStringParamValue(r, "targets"),
		map[string]interface{}{"targets": targets, "result": result}, r)
	msg := util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getPeerTargets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if peerName, present := util.GetStringParam(r, "peer"); present {
		peerTargets := registry.getPeerTargets(peerName)
		if peerTargets != nil {
			msg = fmt.Sprintf("Registry: Reporting peer targets for peer: %s", peerName)
			util.WriteJsonPayload(w, peerTargets)
		} else {
			w.WriteHeader(http.StatusNoContent)
			msg = fmt.Sprintf("Peer not found: %s\n", peerName)
			fmt.Fprintln(w, "[]")
		}
	} else {
		msg = "Reporting all peer targets"
		registry.peersLock.RLock()
		peerTargets := registry.peerTargets
		registry.peersLock.RUnlock()
		util.WriteJsonPayload(w, peerTargets)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func enableAllClientResultsCollection(w http.ResponseWriter, r *http.Request) {
	result := registry.enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), true)
	checkBadPods(result, w)
	msg := util.ToJSONText(result)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
	result := registry.enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), false)
	checkBadPods(result, w)
	msg := util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func addPeerJob(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	body := util.Read(r.Body)
	if job, err := job.ParseJobFromPayload(body); err == nil {
		result := registry.addPeerJob(peerName, &PeerJob{job})
		checkBadPods(result, w)
		events.SendRequestEventJSON(events.Registry_PeerJobAdded, job.Name,
			map[string]interface{}{"job": job, "result": result}, r)
		msg = util.ToJSONText(result)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		events.SendRequestEventJSON(events.Registry_PeerJobRejected, err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": body}, r)
		msg = "failed to read job"
		log.Println(err)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func addPeerJobScriptOrFile(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	fileName := util.GetStringParamValue(r, "name")
	filePath := util.GetStringParamValue(r, "path")
	script := strings.Contains(r.RequestURI, "script")

	content := util.ReadBytes(r.Body)
	if fileName != "" && len(content) > 0 {
		result := registry.addPeerJobScriptOrFile(peerName, filePath, fileName, content, script)
		checkBadPods(result, w)
		events.SendRequestEventJSON(events.Registry_PeerJobFileAdded, fileName,
			map[string]interface{}{"name": fileName, "path": filePath, "script": script, "result": result}, r)
		msg = util.ToJSONText(result)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		events.SendRequestEventJSON(events.Registry_PeerJobFileRejected, fileName,
			map[string]interface{}{"name": fileName, "path": filePath, "script": script}, r)
		msg = "Invalid job script"
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func removePeerJobs(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	jobs, _ := util.GetListParam(r, "jobs")
	result := registry.removePeerJobs(peerName, jobs)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerJobsRemoved, util.GetStringParamValue(r, "jobs"),
		map[string]interface{}{"jobs": jobs, "result": result}, r)
	msg := util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func stopPeerJobs(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	jobs := util.GetStringParamValue(r, "jobs")
	result := registry.stopPeerJobs(peerName, jobs)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerJobsStopped, util.GetStringParamValue(r, "jobs"),
		map[string]interface{}{"jobs": jobs, "result": result}, r)
	msg := util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func runPeerJobs(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	jobs := util.GetStringParamValue(r, "jobs")
	result := registry.invokePeerJobs(peerName, jobs)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerJobsInvoked, util.GetStringParamValue(r, "jobs"),
		map[string]interface{}{"jobs": jobs, "result": result}, r)
	msg := util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getPeerJobs(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if peerName, present := util.GetStringParam(r, "peer"); present {
		peerJobs := registry.getPeerJobs(peerName)
		if peerJobs != nil {
			msg = fmt.Sprintf("Reporting peer jobs for peer: %s", peerName)
			util.WriteJsonPayload(w, peerJobs)
		} else {
			w.WriteHeader(http.StatusNoContent)
			msg = fmt.Sprintf("No jobs for peer %s\n", peerName)
			fmt.Fprintln(w, "[]")
		}
	} else {
		msg = "Reporting all peer jobs"
		registry.peersLock.RLock()
		peerJobs := registry.peerJobs
		registry.peersLock.RUnlock()
		util.WriteJsonPayload(w, peerJobs)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func clearPeerEpochs(w http.ResponseWriter, r *http.Request) {
	registry.clearPeerEpochs()
	w.WriteHeader(http.StatusOK)
	msg := "Peers Epochs Cleared"
	events.SendRequestEvent(events.Registry_PeersEpochsCleared, "", r)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
	registry.reset()
	w.WriteHeader(http.StatusOK)
	msg := "Peers Cleared"
	events.SendRequestEvent(events.Registry_PeersCleared, "", r)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	result := registry.clearPeerTargets(peerName)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerTargetsCleared, peerName,
		map[string]interface{}{"peer": peerName, "result": result}, r)
	msg := util.ToJSONText(result)
	fmt.Fprintln(w, msg)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func clearPeerJobs(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	result := registry.clearPeerJobs(peerName)
	checkBadPods(result, w)
	events.SendRequestEventJSON(events.Registry_PeerJobsCleared, peerName,
		map[string]interface{}{"peer": peerName, "result": result}, r)
	msg := util.ToJSONText(result)
	fmt.Fprintln(w, msg)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func addPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if h, present := util.GetStringParam(r, "headers"); present {
		result := registry.addPeersTrackingHeaders(h)
		events.SendRequestEventJSON(events.Registry_PeerTrackingHeadersAdded, h,
			map[string]interface{}{"headers": h, "result": result}, r)
		checkBadPods(result, w)
		msg = util.ToJSONText(result)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "{\"error\":\"No headers given\"}"
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	result := registry.clearPeersTrackingHeaders()
	events.SendRequestEvent(events.Registry_PeerTrackingHeadersCleared, "", r)
	checkBadPods(result, w)
	msg = util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(registry.peerTrackingHeaders))
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage("Reported peer tracking headers", r)
	}
}

func addPeersTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if b, present := util.GetStringParam(r, "buckets"); present {
		if result := registry.addPeersTrackingTimeBuckets(b); result != nil {
			events.SendRequestEventJSON(events.Registry_PeerTrackingTimeBucketsAdded, b,
				map[string]interface{}{"buckets": b, "result": result}, r)
			checkBadPods(result, w)
			msg = util.ToJSONText(result)
		} else {
			w.WriteHeader(http.StatusBadRequest)
			msg = "{\"error\":\"Invalid time buckets\"}"
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "{\"error\":\"No headers given\"}"
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearPeersTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	result := registry.clearPeersTrackingTimeBuckets()
	events.SendRequestEvent(events.Registry_PeerTrackingTimeBucketsCleared, "", r)
	checkBadPods(result, w)
	msg = util.ToJSONText(result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getPeersTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	fmt.Fprintln(w, registry.peerTrackingTimeBuckets)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage("Reported peer tracking time buckets", r)
	}
}

func setPeersProbe(w http.ResponseWriter, r *http.Request) {
	msg := ""
	probeType := util.GetStringParamValue(r, "type")
	isReadiness := strings.EqualFold(probeType, "readiness")
	isLiveness := strings.EqualFold(probeType, "liveness")
	if !isReadiness && !isLiveness {
		msg = "Cannot add. Invalid probe type"
		w.WriteHeader(http.StatusBadRequest)
	} else if uri, present := util.GetStringParam(r, "uri"); !present {
		msg = "Cannot add. Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
	} else {
		registry.setProbe(isReadiness, uri, 0)
		result := registry.sendProbe(probeType, uri)
		checkBadPods(result, w)
		events.SendRequestEventJSON(events.Registry_PeerProbeSet, uri,
			map[string]interface{}{"probeType": probeType, "uri": uri, "result": result}, r)
		msg = util.ToJSONText(result)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func setPeersProbeStatus(w http.ResponseWriter, r *http.Request) {
	msg := ""
	probeType := util.GetStringParamValue(r, "type")
	isReadiness := strings.EqualFold(probeType, "readiness")
	isLiveness := strings.EqualFold(probeType, "liveness")
	if !isReadiness && !isLiveness {
		msg = "Cannot add. Invalid probe type"
		w.WriteHeader(http.StatusBadRequest)
	} else if status, present := util.GetIntParam(r, "status", 200); !present {
		msg = "Cannot set. Invalid status code"
		w.WriteHeader(http.StatusBadRequest)
	} else {
		if status <= 0 {
			status = 200
		}
		registry.setProbe(isReadiness, "", status)
		result := registry.sendProbeStatus(probeType, status)
		checkBadPods(result, w)
		events.SendRequestEventJSON(events.Registry_PeerProbeStatusSet, probeType,
			map[string]interface{}{"probeType": probeType, "status": status, "result": result}, r)
		msg = util.ToJSONText(result)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getPeersProbes(w http.ResponseWriter, r *http.Request) {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(registry.peerProbes))
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage("Reported peer probes", r)
	}
}

func buildPeerAPIs(peer, address string) []string {
	apis := []string{
		fmt.Sprintf("http://%s/version", address),
		fmt.Sprintf("http://%s/client/results", address),
		fmt.Sprintf("http://%s/client/targets", address),
		fmt.Sprintf("http://%s/client/track/headers", address),
		fmt.Sprintf("http://%s/events", address),
		fmt.Sprintf("http://%s/metrics", address),
		fmt.Sprintf("http://%s/listeners", address),
		fmt.Sprintf("http://%s/probes", address),
		fmt.Sprintf("http://%s/request/ignore", address),
		fmt.Sprintf("http://%s/response/headers", address),
		fmt.Sprintf("http://%s/response/payload", address),
		fmt.Sprintf("http://%s/response/status", address),
		fmt.Sprintf("http://%s/response/triggers", address),
		fmt.Sprintf("http://%s/proxy/targets", address),
		fmt.Sprintf("http://%s/jobs", address),
	}
	return apis
}

func getPeersAPIs(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	peersAPIs := map[string]map[string][]string{}
	for peer, pods := range registry.loadPeerPods(peerName, "") {
		peersAPIs[peer] = map[string][]string{}
		peersAPIs[peer]["all"] = buildPeerAPIs(peer, fmt.Sprintf("%s/registry/peers/%s/call?uri=", global.Self.Address, peer))
		for _, pod := range pods {
			apis := buildPeerAPIs(peer, pod.Address)
			apis = append(apis, fmt.Sprintf("http://%s/registry/peers/%s/lockers?data=y&level=3", global.Self.Address, peer))
			for peer, lockers := range registry.labeledLockers.GetPeerLockers(peer) {
				for label := range lockers {
					apis = append(apis, fmt.Sprintf("http://%s/registry/lockers/%s/peers/%s?data=y&level=3", global.Self.Address, label, peer))
				}
			}
			peersAPIs[peer][pod.Name] = apis
		}
	}
	util.WriteJsonPayload(w, peersAPIs)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage("Reported useful peer APIs.", r)
	}
}

func callPeer(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	uri := util.GetStringParamValue(r, "uri")
	if uri == "" {
		msg = "Cannot call peer. Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, msg)
	} else {
		body := util.ReadBytes(r.Body)
		msg = fmt.Sprintf("Calling peers with URI [%s] Method [%s] Headers [%+v] Body [%s]", uri, r.Method, r.Header, body)
		result := registry.callPeer(peerName, uri, r.Method, r.Header, body)
		events.SendRequestEventJSON(events.Registry_PeerCalled, uri, map[string]interface{}{
			"peer": peerName, "uri": uri, "method": r.Method, "headers": r.Header, "body": body, "result": result}, r)
		util.WriteJsonPayload(w, result)
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func copyPeersToLocker(w http.ResponseWriter, r *http.Request) {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	currentLocker := registry.getCurrentLocker()
	currentLocker.Store([]string{"peers"}, util.ToJSONText(registry.peers))
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Peers [len: %d] stored in labeled locker %s under path 'peers'",
		len(registry.peers), currentLocker.Label)
	events.SendRequestEventJSON(events.Registry_PeersCopied, msg, registry.peers, r)
	fmt.Fprintln(w, msg)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func cloneFromRegistry(w http.ResponseWriter, r *http.Request) {
	msg := ""
	url := util.GetStringParamValue(r, "url")
	if url == "" {
		msg = "Cannot clone. Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
	} else {
		var err error
		if err = registry.clonePeersFrom(url); err != nil {
			msg = fmt.Sprintf("failed to clone peers data from registry [%s], error: %s", url, err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else if err = registry.cloneLockersFrom(url); err != nil {
			msg = fmt.Sprintf("failed to clone lockers data from registry [%s], error: %s", url, err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else if err = registry.clonePeersTargetsFrom(url); err != nil {
			msg = fmt.Sprintf("failed to clone peers targets from registry [%s], error: %s", url, err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else if err = registry.clonePeersJobsFrom(url); err != nil {
			msg = fmt.Sprintf("failed to clone peers jobs from registry [%s], error: %s", url, err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else if err = registry.clonePeersTrackingHeadersFrom(url); err != nil {
			msg = fmt.Sprintf("failed to clone peers tracking headers from registry [%s], error: %s", url, err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else if err = registry.clonePeersProbesFrom(url); err != nil {
			msg = fmt.Sprintf("failed to clone peers probes from registry [%s], error: %s", url, err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			msg = fmt.Sprintf("Cloned data from registry [%s]", url)
			events.SendRequestEvent(events.Registry_Cloned, "", r)
		}
	}
	fmt.Fprintln(w, msg)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func dumpRegistry(w http.ResponseWriter, r *http.Request) {
	dump := map[string]interface{}{}
	registry.lockersLock.RLock()
	dump["lockers"] = registry.labeledLockers.GetAllLockers(true, true, true, 0)
	registry.lockersLock.RUnlock()
	registry.peersLock.RLock()
	dump["peers"] = registry.peers
	dump["peerTargets"] = registry.peerTargets
	dump["peerJobs"] = registry.peerJobs
	dump["peerTrackingHeaders"] = registry.peerTrackingHeaders
	dump["peerProbes"] = registry.peerProbes
	registry.peersLock.RUnlock()
	fmt.Fprintln(w, util.ToJSONText(dump))
	msg := "Registry data dumped"
	events.SendRequestEvent(events.Registry_Dumped, "", r)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func loadRegistryDump(w http.ResponseWriter, r *http.Request) {
	dump := map[string]interface{}{}
	if err := util.ReadJsonPayload(r, &dump); err != nil {
		fmt.Fprintf(w, "failed to load data with error: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	msg := ""
	if dump["lockers"] != nil {
		lockersData := util.ToJSONText(dump["lockers"])
		if lockersData != "" {
			lockers := map[string]*locker.CombiLocker{}
			if err := util.ReadJson(lockersData, &lockers); err == nil {
				registry.lockersLock.Lock()
				registry.labeledLockers.ReplaceLockers(lockers)
				registry.lockersLock.Unlock()
			} else {
				msg += fmt.Sprintf("[failed to load lockers with error: %s]", err.Error())
			}
		}
	}
	registry.peersLock.Lock()
	defer registry.peersLock.Unlock()

	if dump["peers"] != nil {
		peersData := util.ToJSONText(dump["peers"])
		if peersData != "" {
			registry.peers = map[string]*Peers{}
			if err := util.ReadJson(peersData, &registry.peers); err != nil {
				msg += fmt.Sprintf("[failed to load peers with error: %s]", err.Error())
			}
		}
	}
	if dump["peerTargets"] != nil {
		peerTargetsData := util.ToJSONText(dump["peerTargets"])
		if peerTargetsData != "" {
			registry.peerTargets = map[string]PeerTargets{}
			if err := util.ReadJson(peerTargetsData, &registry.peerTargets); err != nil {
				msg += fmt.Sprintf("[failed to load peer targets with error: %s]", err.Error())
			}
		}
	}
	if dump["peerJobs"] != nil {
		peerJobsData := util.ToJSONText(dump["peerJobs"])
		if peerJobsData != "" {
			registry.peerJobs = map[string]PeerJobs{}
			if err := util.ReadJson(peerJobsData, &registry.peerJobs); err != nil {
				msg += fmt.Sprintf("[failed to load peer jobs with error: %s]", err.Error())
			}
		}
	}
	if dump["peerTrackingHeaders"] != nil {
		registry.peerTrackingHeaders = dump["peerTrackingHeaders"].(string)
		registry.trackingHeaders, registry.crossTrackingHeaders = util.ParseTrackingHeaders(registry.peerTrackingHeaders)
	}
	if dump["peerProbes"] != nil {
		peerProbesData := util.ToJSONText(dump["peerProbes"])
		if peerProbesData != "" {
			registry.peerProbes = &PeerProbes{}
			if err := util.ReadJson(peerProbesData, &registry.peerProbes); err != nil {
				msg += fmt.Sprintf("[failed to load peer probes with error: %s]", err.Error())
			}
		}
	}
	if msg == "" {
		msg = "Registry data loaded"
	}
	events.SendRequestEvent(events.Registry_DumpLoaded, "", r)
	fmt.Fprintln(w, msg)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}
