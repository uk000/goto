/**
 * Copyright 2026 uk
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
	"bytes"
	"encoding/json"
	"fmt"
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

func setRoutes(r *mux.Router) {
	registryRouter := middleware.RootPath("/registry")
	peersRouter := registryRouter.PathPrefix("/peers").Subrouter()
	addLockerMaintenanceRoutes(registryRouter, peersRouter)
	addLockerStoreRoutes(registryRouter, peersRouter)
	addPeerLockerReadRoutes(peersRouter)
	addLockerReadRoutes(registryRouter)
	addLockerPeerRoutes(registryRouter)
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
	// Backward compatible: v0.8.x paths for the same operations.
	util.AddRoute(peersRouter, "/targets/results/all/{enable}", enableAllClientResultsCollection, "POST", "PUT")
	util.AddRoute(peersRouter, "/targets/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")
}

func addPeerJobsRoutes(peersRouter *mux.Router) {
	util.AddRoute(peersRouter, "/{peer}?/jobs/add", addPeerJob, "POST", "PUT")
	util.AddRoute(peersRouter, "/{peer}/jobs/add/script/{name}", addPeerJobScriptOrFile, "POST", "PUT")
	util.AddRouteQO(peersRouter, "/{peer}/jobs/store/file/{name}", addPeerJobScriptOrFile, "path", "POST", "PUT")
	util.AddRoute(peersRouter, "/jobs/add/script/{name}", addPeerJobScriptOrFile, "POST", "PUT")
	util.AddRouteQO(peersRouter, "/jobs/store/file/{name}", addPeerJobScriptOrFile, "path", "POST", "PUT")
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
	util.AddRoute(peersRouter, "/track/time", getPeersTrackingTimeBuckets, "GET")
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
		} else {
			registry.rememberPeer(peer)
			msg = fmt.Sprintf("Remembered Peer: %+v", *peer)
			peerData.Message = msg
		}
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("failed to parse json with error: %s", err.Error())
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
	util.WriteJsonPayload(w, result)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(util.ToJSONText(result), r)
	}
}

func cleanupUnhealthyPeers(w http.ResponseWriter, r *http.Request) {
	result := registry.cleanupUnhealthyPeers(util.GetStringParamValue(r, "peer"))
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

// checkBadPods responds with 424 if, for any targeted peer, the operation did not succeed on any of its pods.
// A peer counts as successful as soon as one of its pods succeeded. Peers without pods are not failures.
func checkBadPods(result PeerResults, w http.ResponseWriter) {
	anyBad := false
	for _, pods := range result {
		if len(pods) == 0 {
			continue
		}
		succeeded := false
		for _, status := range pods {
			if status {
				succeeded = true
				break
			}
		}
		if !succeeded {
			anyBad = true
			break
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
			log.Println(err)
		} else {
			result := registry.addPeerTarget(peerName, t)
			checkBadPods(result, w)
			msg = util.ToJSONText(result)
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "failed to parse json"
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
	msg := util.ToJSONText(result)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	targets := util.GetStringParamValue(r, "targets")
	result := registry.invokePeerTargets(peerName, targets)
	checkBadPods(result, w)
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
	if job, err := job.ParseJobFromPayload(body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = "failed to read job"
		log.Println(err)
	} else {
		if job.Name == "" {
			// v0.8.x payloads identify a job by "id"
			legacy := struct {
				ID string `json:"id"`
			}{}
			if util.ReadJson(body, &legacy) == nil {
				job.Name = legacy.ID
			}
		}
		if job.Name == "" {
			// A nameless job would get stored under an empty key, and can never be removed or run by name.
			w.WriteHeader(http.StatusBadRequest)
			msg = "job name is required"
		} else {
			result := registry.addPeerJob(peerName, &PeerJob{job})
			checkBadPods(result, w)
			msg = util.ToJSONText(result)
		}
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
		msg = util.ToJSONText(result)
	} else {
		w.WriteHeader(http.StatusBadRequest)
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
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
	registry.reset()
	w.WriteHeader(http.StatusOK)
	msg := "Peers Cleared"
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	result := registry.clearPeerTargets(peerName)
	checkBadPods(result, w)
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
	if registry.peerProbes == nil {
		// The probes are cloned from this response, so it needs to be valid json even when probes aren't set.
		fmt.Fprintln(w, "null")
	} else {
		fmt.Fprintln(w, util.ToJSONText(registry.peerProbes))
	}
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
	currentLocker.Store([]string{"peers"}, util.ToJSONText(registry.peers), false)
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Peers [len: %d] stored in labeled locker %s under path 'peers'",
		len(registry.peers), currentLocker.Label)
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
	dump["lockers"] = registry.labeledLockers.GetAllLockers(true, true, 0)
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
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}

func loadRegistryDump(w http.ResponseWriter, r *http.Request) {
	dump := map[string]json.RawMessage{}
	if err := util.ReadJsonPayload(r, &dump); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "failed to load data with error: %s\n", err.Error())
		return
	}

	// Parse all sections first, and replace registry data only if every section parsed fine, so that a bad
	// payload neither wipes existing data nor leaves the registry half-loaded.
	var errs []string
	parse := func(section string, target any) bool {
		raw := bytes.TrimSpace(dump[section])
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			return false
		}
		if err := json.Unmarshal(raw, target); err != nil {
			errs = append(errs, fmt.Sprintf("[failed to load %s with error: %s]", section, err.Error()))
			return false
		}
		return true
	}
	lockers := map[string]*locker.CombiLocker{}
	peers := map[string]*Peers{}
	peerTargets := map[string]PeerTargets{}
	peerJobs := map[string]PeerJobs{}
	peerTrackingHeaders := ""
	peerProbes := &PeerProbes{}
	hasLockers := parse("lockers", &lockers)
	hasPeers := parse("peers", &peers)
	hasPeerTargets := parse("peerTargets", &peerTargets)
	hasPeerJobs := parse("peerJobs", &peerJobs)
	hasTrackingHeaders := parse("peerTrackingHeaders", &peerTrackingHeaders)
	hasPeerProbes := parse("peerProbes", peerProbes)

	msg := ""
	if len(errs) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Registry data not loaded. " + strings.Join(errs, "")
	} else {
		if hasLockers {
			registry.lockersLock.Lock()
			registry.labeledLockers.ReplaceLockers(lockers)
			registry.lockersLock.Unlock()
		}
		registry.peersLock.Lock()
		if hasPeers {
			registry.peers = peers
		}
		if hasPeerTargets {
			registry.peerTargets = peerTargets
		}
		if hasPeerJobs {
			fillJobNames(peerJobs)
			registry.peerJobs = peerJobs
		}
		if hasTrackingHeaders {
			registry.peerTrackingHeaders = peerTrackingHeaders
			registry.trackingHeaders, registry.crossTrackingHeaders = util.ParseTrackingHeaders(peerTrackingHeaders)
		}
		if hasPeerProbes {
			registry.peerProbes = peerProbes
		}
		registry.peersLock.Unlock()
		msg = "Registry data loaded"
	}
	fmt.Fprintln(w, msg)
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
}
