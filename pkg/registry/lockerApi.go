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
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/registry/locker"
	"goto/pkg/util"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
)

func addLockerMaintenanceRoutes(registryRouter, peersRouter *mux.Router) {
	util.AddRoute(registryRouter, "/lockers/{label}/open", openLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/open/{label}", openLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/close", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/close/{label}", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/clear", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/clear/{label}", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/close", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/clear", closeOrClearLabeledLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}?/{peerlabel}?/lockers/clear", clearLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}/locker/clear", clearLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}/{peerlabel}/locker/clear", clearLocker, "POST")
}

func addLockerStoreRoutes(registryRouter, peersRouter *mux.Router) {
	util.AddRoute(registryRouter, "/lockers/{label}/store/{path}", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/{path}/store", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/{path}/add", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/remove/{path}", removeFromLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/{path}/remove", removeFromLabeledLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}/{peerlabel}?/locker/store/{path}", storeInPeerLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}/{peerlabel}?/locker/remove/{path}", removeFromPeerLocker, "POST")

}

func addPeerLockerReadRoutes(peersRouter *mux.Router) {
	util.AddRouteWithMultiQ(peersRouter, "/{peer}/{peerlabel}?/locker/get/{path}", getFromPeerLocker, [][]string{{}, {"data", "level"}}, "GET")
	util.AddRoute(peersRouter, "/{peer}?/instances/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(peersRouter, "/{peer}?/client/results/details/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(peersRouter, "/{peer}?/client/results/summary/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(peersRouter, "/{peer}?/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRouteWithMultiQ(peersRouter, "/{peer}?/{peerlabel}?/lockers", getPeerLocker, peerLockerQueryParams, "GET")
	addPeerResultsCompatRoutes(peersRouter)
}

// Backward compatible routes to report client results of peers. v0.8.x served them at .../targets/results
// (summary, or details with detailed=y), main at .../client/results/{summary,details}.
func addPeerResultsCompatRoutes(router *mux.Router) {
	util.AddRouteWithMultiQ(router, "/{peer}?/targets/results", getTargetsResultsCompat, [][]string{{}, {"detailed"}}, "GET")
}

func addLockerReadRoutes(registryRouter *mux.Router) {
	util.AddRouteWithMultiQ(registryRouter, "/lockers", getAllLockers, [][]string{{}, {"data", "peers", "level"}}, "GET")
	util.AddRoute(registryRouter, "/lockers/all", getAllLockers, "GET")
	util.AddRoute(registryRouter, "/lockers/labels", getLockerLabels, "GET")
	// Routes like /lockers/data and /lockers/search/{text} have to come before /lockers/{label}, or the first
	// segment after /lockers/ would be taken as a locker label.
	util.AddRouteWithMultiQ(registryRouter, "/lockers/{label}?/data", getDataLockers, [][]string{{}, {"data", "level"}, {"md"}}, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/data/keys", getDataLockerPaths, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/data/paths", getDataLockerPaths, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/search/{text}", searchInDataLockers, "GET")
	util.AddRouteWithMultiQ(registryRouter, "/lockers/{label}/get/{path}", getFromDataLocker, [][]string{{}, {"data", "level"}, {"md"}}, "GET")
	util.AddRouteWithMultiQ(registryRouter, "/lockers/{label}", getLabeledLocker, [][]string{{}, {"data", "peers", "level"}, {"md"}}, "GET")
}

func addContextLockerRoutes(registryRouter *mux.Router) {
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/open", openLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/close", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/clear", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/store/{path}", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/{path}/store", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/{path}/add", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/get/{path}", getFromDataLocker, "GET")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/{path}/get", getFromDataLocker, "GET")
}

// addLockerPeerRoutes adds the routes to read peer data from a given locker, under /lockers/{label}/peers.
// {label} is the label of the locker. The instance (address) of a peer is {peerlabel}.
func addLockerPeerRoutes(registryRouter *mux.Router) {
	lockerPeersRouter := registryRouter.PathPrefix("/lockers/{label}/peers").Subrouter()
	util.AddRoute(lockerPeersRouter, "/{peer}?/instances/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/details/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/summary/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/targets={targets}?", getPeersClientResults, "GET")
	addPeerResultsCompatRoutes(lockerPeersRouter)
	util.AddRouteWithMultiQ(lockerPeersRouter, "/{peer}/{peerlabel}?/get/{path}", getFromPeerLocker, [][]string{{}, {"data", "level"}}, "GET")
	util.AddRouteWithMultiQ(lockerPeersRouter, "/{peer}/{peerlabel}?/locker/get/{path}", getFromPeerLocker, [][]string{{}, {"data", "level"}}, "GET")
	util.AddRouteWithMultiQ(lockerPeersRouter, "/{peer}?/{peerlabel}?", getPeerLocker, peerLockerQueryParams, "GET")
}

// Query params to report peer lockers.
var peerLockerQueryParams = [][]string{{}, {"data", "level"}}

func openLabeledLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	context := util.GetStringParamValue(r, "context")
	var ll *locker.LabeledLockers
	registry.lockersLock.Lock()
	if context != "" {
		ll = registry.contextLockers.GetContextLocker(context)
	} else {
		ll = registry.labeledLockers
	}
	registry.lockersLock.Unlock()
	if label != "" {
		ll.OpenLocker(label)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("[Context: %s] Locker %s is open and active", context, label)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Locker label needed"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func closeOrClearLabeledLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	close := strings.Contains(r.RequestURI, "close")
	context := util.GetStringParamValue(r, "context")
	var ll *locker.LabeledLockers
	registry.lockersLock.Lock()
	if context != "" {
		ll = registry.contextLockers.GetContextLocker(context)
	} else {
		ll = registry.labeledLockers
	}
	registry.lockersLock.Unlock()
	if close && strings.EqualFold(label, constants.LockerDefaultLabel) {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Default locker cannot be closed"
	} else if label != "" && ll.GetLocker(label) == nil {
		w.WriteHeader(http.StatusNotFound)
		msg = "Locker not found"
	} else if label != "" {
		ll.ClearLocker(label, close)
		w.WriteHeader(http.StatusOK)
		if close {
			msg = fmt.Sprintf("[Context: %s] Locker %s is closed", context, label)
		} else {
			msg = fmt.Sprintf("[Context: %s] Locker %s is cleared", context, label)
		}
	} else {
		w.WriteHeader(http.StatusOK)
		ll.Init()
		result := clearPeersResults(registry.loadAllPeerPods(), r)
		w.WriteHeader(http.StatusOK)
		util.WriteJsonPayload(w, result)
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getLabeledLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	getData := util.GetBoolParamValue(r, "data")
	getPeerLockers := util.GetBoolParamValue(r, "peers")
	level := util.GetIntParamValue(r, "level", 2)
	sendMD := util.GetBoolParamValue(r, "md")
	var locker *locker.CombiLocker
	if label != "" {
		locker = registry.labeledLockers.GetLocker(label)
	} else {
		locker = registry.getCurrentLocker()
	}
	if locker == nil {
		msg = "Locker not found"
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, msg)
	} else {
		if level > 0 {
			locker = locker.Trim(level)
		}
		if !getData {
			locker = locker.GetLockerView(getData)
		}
		if !getPeerLockers {
			locker = locker.GetLockerWithoutPeers()
		}
		msg = fmt.Sprintf("Labeled locker [%s] reported", label)
		if sendMD {
			w.Header().Set(constants.HeaderContentType, constants.ContentTypeMarkdown)
			fmt.Fprint(w, renderLockersMarkdown(locker))
		} else {
			util.WriteJsonPayload(w, locker)
		}
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getDataLockers(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	getData := util.GetBoolParamValue(r, "data")
	level := util.GetIntParamValue(r, "level", 2)
	sendMD := util.GetBoolParamValue(r, "md")
	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	registry.lockersLock.RUnlock()
	var lockers map[string]*locker.DataLocker
	if getData {
		lockers = labeledLockers.GetDataLockers(label)
		msg = "All data lockers reported with data"
	} else {
		lockers = labeledLockers.GetDataLockersView(label)
		msg = "All data lockers view reported without data"
	}
	output := map[string]*locker.DataLocker{}
	for label, dl := range lockers {
		if level > 0 {
			output[label] = dl.Trim(level)
		} else {
			output[label] = dl
		}
	}
	if sendMD {
		w.Header().Set(constants.HeaderContentType, constants.ContentTypeMarkdown)
		fmt.Fprint(w, renderLockersMarkdown(output))
	} else {
		util.WriteJsonPayload(w, output)
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getAllLockers(w http.ResponseWriter, r *http.Request) {
	msg := ""
	getAll := strings.Contains(r.RequestURI, "/all")
	getData := util.GetBoolParamValue(r, "data") || getAll
	getPeerLockers := util.GetBoolParamValue(r, "peers") || getAll
	level := util.GetIntParamValue(r, "level", 5)
	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	contextLockers := registry.contextLockers
	registry.lockersLock.RUnlock()
	msg = "All labeled lockers reported"
	data := map[string]any{}
	ll := labeledLockers.GetAllLockers(getPeerLockers, getData, level)
	cl := contextLockers.GetAllLockers(getPeerLockers, getData, level)
	for k, v := range ll {
		data[k] = v
	}
	for k, v := range cl {
		data[k] = v
	}
	util.WriteJsonPayload(w, data)
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getLockerLabels(w http.ResponseWriter, r *http.Request) {
	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	registry.lockersLock.RUnlock()
	util.WriteJsonPayload(w, labeledLockers.GetLockerLabels())
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage("Locker labels reported", r)
	}
}

func getDataLockerPaths(w http.ResponseWriter, r *http.Request) {
	label := util.GetStringParamValue(r, "label")
	paths := strings.Contains(r.RequestURI, "paths")
	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	registry.lockersLock.RUnlock()
	util.WriteJsonPayload(w, labeledLockers.GetDataLockerPaths(label, paths))
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage("Data Locker paths reported", r)
	}
}

func searchInDataLockers(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	key := util.GetStringParamValue(r, "text")
	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	registry.lockersLock.RUnlock()
	if key != "" {
		if _, err := regexp.Compile("(?i)" + key); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Cannot search. Invalid search expression: %s", err.Error())
			fmt.Fprintln(w, msg)
		} else {
			util.WriteJsonPayload(w, labeledLockers.SearchInDataLockers(label, key))
			msg = fmt.Sprintf("Reported results for key %s search", key)
		}
	} else {
		msg = "Cannot search. No key given."
		fmt.Fprintln(w, msg)
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func storeInLabeledLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	path, _ := util.GetListParam(r, "path")
	context := util.GetStringParamValue(r, "context")
	add := strings.Contains(r.RequestURI, "/add")
	var ll *locker.LabeledLockers
	registry.lockersLock.Lock()
	if context != "" {
		ll = registry.contextLockers.GetContextLocker(context)
	} else {
		ll = registry.labeledLockers
	}
	registry.lockersLock.Unlock()
	if label != "" && len(path) > 0 {
		data := util.Read(r.Body)
		ll.GetOrCreateLocker(label).Store(path, data, add)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Data stored in [Context: %s] labeled locker %s for path %+v", context, label, path)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Not enough parameters to access locker"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func removeFromLabeledLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	path, _ := util.GetListParam(r, "path")
	if label != "" && len(path) > 0 {
		registry.labeledLockers.GetOrCreateLocker(label).Remove(path)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Data removed from labeled locker %s for path %+v", label, path)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Not enough parameters to access locker"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getFromDataLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	path, _ := util.GetListParam(r, "path")
	getData := util.GetBoolParamValue(r, "data")
	level := util.GetIntParamValue(r, "level", 0)
	sendMD := util.GetBoolParamValue(r, "md")
	if level > 0 {
		level = len(path) + level
	}
	context := util.GetStringParamValue(r, "context")
	var ll *locker.LabeledLockers
	registry.lockersLock.Lock()
	if context != "" {
		ll = registry.contextLockers.GetContextLocker(context)
	} else {
		ll = registry.labeledLockers
	}
	registry.lockersLock.Unlock()
	if len(path) > 0 {
		data, dataAtKey := ll.Get(label, path, getData, level)
		msg = fmt.Sprintf("Reported data from [Context: %s] Locker [%s] Path %+v", context, label, path)
		if dataAtKey {
			fmt.Fprint(w, data)
		} else if data != nil {
			if sendMD {
				w.Header().Set(constants.HeaderContentType, constants.ContentTypeMarkdown)
				fmt.Fprint(w, renderLockersMarkdown(data))
			} else {
				util.WriteJsonPayload(w, data)
			}
		} else {
			fmt.Fprint(w, "{}")
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Not enough parameters to access locker"
		fmt.Fprint(w, msg)
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func storeInPeerLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	peerLabel := util.GetStringParamValue(r, "peerlabel")
	path, _ := util.GetListParam(r, "path")
	if peerName != "" && len(path) > 0 {
		data := util.Read(r.Body)
		registry.getCurrentLocker().StorePeerData(peerName, peerLabel, path, data)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Peer %s data stored for path %+v", peerName, path)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Not enough parameters to access locker"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func removeFromPeerLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	peerLabel := util.GetStringParamValue(r, "peerlabel")
	path, _ := util.GetListParam(r, "path")
	if peerName != "" && len(path) > 0 {
		registry.getCurrentLocker().RemovePeerData(peerName, peerLabel, path)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Peer %s data removed for path %+v", peerName, path)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Not enough parameters to access locker"
	}
	if global.Flags.EnableRegistryLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	peerLabel := util.GetStringParamValue(r, "peerlabel")
	result := registry.clearLocker(peerName, peerLabel, r)
	w.WriteHeader(http.StatusOK)
	util.WriteJsonPayload(w, result)
	if peerName != "" {
		if peerLabel != "" {
			msg = fmt.Sprintf("Peer %s Instance %s data cleared", peerName, peerLabel)
		} else {
			msg = fmt.Sprintf("Peer %s data cleared for all instances", peerName)
		}
	} else {
		msg = "All peer lockers cleared"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getFromPeerLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label", constants.LockerCurrent)
	peerName := util.GetStringParamValue(r, "peer")
	peerLabel := util.GetStringParamValue(r, "peerlabel")
	val := util.GetStringParamValue(r, "path")
	path, _ := util.GetListParam(r, "path")
	getData := util.GetBoolParamValue(r, "data")
	level := util.GetIntParamValue(r, "level", 0)
	level = len(path) + level

	if len(path) == 0 || peerName == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Not enough parameters to access locker"
		fmt.Fprint(w, msg)
	} else {
		data, dataAtKey := registry.labeledLockers.GetFromPeerInstanceLocker(label, peerName, peerLabel, path, getData, level)
		if dataAtKey {
			fmt.Fprint(w, data)
		} else if text, isText := data.(string); util.IsNil(data) || isText && text == "" {
			// nothing at the path
			util.WriteJsonPayload(w, map[string]interface{}{})
		} else {
			util.WriteJsonPayload(w, data)
		}
		msg = fmt.Sprintf("Reported data from path [%s]\n", val)
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getPeerLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	peerName := util.GetStringParamValue(r, "peer")
	peerLabel := util.GetStringParamValue(r, "peerlabel")
	getData := util.GetBoolParamValue(r, "data")
	level := util.GetIntParamValue(r, "level", 2)
	var locker *locker.CombiLocker
	if label == "" || strings.EqualFold(label, constants.LockerCurrent) {
		locker = registry.getCurrentLocker()
	} else {
		locker = registry.labeledLockers.GetLocker(label)
	}
	if locker == nil {
		msg = "Locker not found"
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, msg)
	} else {
		var result interface{}
		if level > 0 {
			locker = locker.Trim(level)
		}
		result = locker.GetPeerLockersView(peerName, peerLabel, getData)
		util.WriteJsonPayload(w, result)
		if peerName != "" {
			msg = fmt.Sprintf("Peer %s data reported", peerName)
		} else {
			msg = "All peer lockers reported"
		}
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getPeersClientResults(w http.ResponseWriter, r *http.Request) {
	detailed := strings.Contains(r.RequestURI, "details")
	byInstances := strings.Contains(r.RequestURI, "instances")
	reportPeersClientResults(w, r, util.GetStringParamValue(r, "peer"), detailed, byInstances)
}

// getTargetsResultsCompat serves the v0.8.x API for client results of peers: .../targets/results[?detailed=y].
func getTargetsResultsCompat(w http.ResponseWriter, r *http.Request) {
	peerName := util.GetStringParamValue(r, "peer")
	if strings.EqualFold(peerName, "lockers") {
		// v0.8.x reported the results of all peers from the current locker at /registry/peers/lockers/targets/results
		peerName = ""
	}
	reportPeersClientResults(w, r, peerName, util.GetBoolParamValue(r, "detailed"), false)
}

func reportPeersClientResults(w http.ResponseWriter, r *http.Request, peerName string, detailed, byInstances bool) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	targets, _ := util.GetListParam(r, "targets")
	var locker *locker.CombiLocker
	if label == "" || strings.EqualFold(label, constants.LockerCurrent) {
		locker = registry.getCurrentLocker()
	} else {
		locker = registry.labeledLockers.GetLocker(label)
	}
	if locker == nil {
		msg = "Locker not found"
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, msg)
	} else {
		result := locker.GetPeersClientResults(peerName, targets, registry.trackingHeaders, registry.crossTrackingHeaders, registry.trackingTimeBuckets, detailed, byInstances)
		util.WriteJsonPayload(w, result)
		msg = "Reported peers client results"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}
