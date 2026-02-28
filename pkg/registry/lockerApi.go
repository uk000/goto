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
	"goto/pkg/registry/locker"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

func addLockerMaintenanceRoutes(registryRouter, peersRouter *mux.Router) {
	util.AddRoute(registryRouter, "/lockers/{label}/open", openLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/close", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/clear", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/close", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/clear", closeOrClearLabeledLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}?/{address}?/lockers/clear", clearLocker, "POST")
}

func addLockerStoreRoutes(registryRouter, peersRouter *mux.Router) {
	util.AddRoute(registryRouter, "/lockers/{label}/store/{path}", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/lockers/{label}/remove/{path}", removeFromLabeledLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}/{address}?/locker/store/{path}", storeInPeerLocker, "POST")
	util.AddRoute(peersRouter, "/{peer}/{address}?/locker/remove/{path}", removeFromPeerLocker, "POST")

}

func addLockerEventsRoutes(peersRouter *mux.Router) {
	util.AddRoute(peersRouter, "/{peer}/{address}/events/store", storePeerEvent, "POST")
	util.AddRouteMultiQ(peersRouter, "/{peer}?/events/search/{text}", searchInPeerEvents, []string{"data", "reverse"}, "GET")
	util.AddRoute(peersRouter, "/{peer}?/events/search/{text}", searchInPeerEvents, "GET")
	util.AddRouteMultiQ(peersRouter, "/{peer}?/events", getPeerEvents, []string{"data", "reverse"}, "GET")
	util.AddRoute(peersRouter, "/{peer}?/events", getPeerEvents, "GET")
	util.AddRoute(peersRouter, "/events/flush", flushPeerEvents, "POST")
	util.AddRoute(peersRouter, "/events/clear", clearPeerEvents, "POST")
}

func addPeerLockerReadRoutes(peersRouter *mux.Router) {
	util.AddRouteMultiQ(peersRouter, "/{peer}/{address}?/locker/get/{path}", getFromPeerLocker, []string{"data", "level"}, "GET")
	util.AddRoute(peersRouter, "/{peer}/{address}?/locker/get/{path}", getFromPeerLocker, "GET")
	util.AddRoute(peersRouter, "/{peer}?/instances/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(peersRouter, "/{peer}?/client/results/details/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(peersRouter, "/{peer}?/client/results/summary/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(peersRouter, "/{peer}?/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRouteMultiQ(peersRouter, "/{peer}?/{address}?/lockers", getPeerLocker, []string{"data", "events", "level"}, "GET")
	util.AddRoute(peersRouter, "/{peer}?/{address}?/lockers", getPeerLocker, "GET")

}

func addLockerReadRoutes(registryRouter *mux.Router) {
	util.AddRouteMultiQ(registryRouter, "/lockers", getAllLockers, []string{"data", "events", "peers", "level"}, "GET")
	util.AddRoute(registryRouter, "/lockers/all", getAllLockers, "GET")
	util.AddRoute(registryRouter, "/lockers", getAllLockers, "GET")
	util.AddRouteMultiQ(registryRouter, "/lockers/{label}", getLabeledLocker, []string{"data", "events", "peers", "level"}, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}", getLabeledLocker, "GET")
	util.AddRouteMultiQ(registryRouter, "/lockers/{label}?/data", getDataLockers, []string{"data", "level"}, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/data", getDataLockers, "GET")
	util.AddRouteMultiQ(registryRouter, "/lockers/{label}/get/{path}", getFromDataLocker, []string{"data", "level"}, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}/get/{path}", getFromDataLocker, "GET")
	util.AddRoute(registryRouter, "/lockers/labels", getLockerLabels, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/data/keys", getDataLockerPaths, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/data/paths", getDataLockerPaths, "GET")
	util.AddRoute(registryRouter, "/lockers/{label}?/search/{text}", searchInDataLockers, "GET")
}

func addContextLockerRoutes(registryRouter *mux.Router) {
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/open", openLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/close", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/clear", closeOrClearLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/store/{path}", storeInLabeledLocker, "POST")
	util.AddRoute(registryRouter, "/context/{context}/lockers/{label}/get/{path}", getFromDataLocker, "GET")
}

func addLockerPeerRoutes(registryRouter *mux.Router) {
	lockerPeersRouter := registryRouter.PathPrefix("/lockers/{label}/peers").Subrouter()
	util.AddRouteMultiQ(lockerPeersRouter, "/{peers}?/events", getPeerEvents, []string{"data", "reverse"}, "GET")
	util.AddRoute(lockerPeersRouter, "/{peers}?/events", getPeerEvents, "GET")
	util.AddRouteMultiQ(lockerPeersRouter, "/{peers}?/events/search/{text}", searchInPeerEvents, []string{"data", "reverse"}, "GET")
	util.AddRoute(lockerPeersRouter, "/{peers}?/events/search/{text}", searchInPeerEvents, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/instances/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/details/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/summary/targets={targets}?", getPeersClientResults, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/client/results/targets={targets}?", getPeersClientResults, "GET")
	util.AddRouteMultiQ(lockerPeersRouter, "/{peer}?/{address}?", getPeerLocker, []string{"data", "events", "level"}, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}?/{address}?", getPeerLocker, "GET")
	util.AddRouteMultiQ(lockerPeersRouter, "/{peer}/{address}?/get/{path}", getFromPeerLocker, []string{"data", "level"}, "GET")
	util.AddRoute(lockerPeersRouter, "/{peer}/{address}?/locker/get/{path}", getFromPeerLocker, "GET")
}

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
		events.SendRequestEvent(events.Registry_LockerOpened, label, r)
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
	} else if label != "" {
		ll.ClearLocker(label, close)
		w.WriteHeader(http.StatusOK)
		if close {
			msg = fmt.Sprintf("[Context: %s] Locker %s is closed", context, label)
			events.SendRequestEvent(events.Registry_LockerClosed, label, r)
		} else {
			msg = fmt.Sprintf("[Context: %s] Locker %s is cleared", context, label)
			events.SendRequestEvent(events.Registry_LockerCleared, label, r)
		}
	} else {
		w.WriteHeader(http.StatusOK)
		ll.Init()
		result := clearPeersResultsAndEvents(registry.loadAllPeerPods(), r)
		w.WriteHeader(http.StatusOK)
		util.WriteJsonPayload(w, result)
		events.SendRequestEvent(events.Registry_AllLockersCleared, label, r)
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
	getEvents := util.GetBoolParamValue(r, "events")
	getPeerLockers := util.GetBoolParamValue(r, "peers")
	level := util.GetIntParamValue(r, "level", 2)
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
			locker = locker.GetLockerView(getEvents, getData)
		}
		if !getPeerLockers {
			locker = locker.GetLockerWithoutPeers()
		}
		if !getEvents {
			locker = locker.GetLockerWithoutEvents()
		}
		msg = fmt.Sprintf("Labeled locker [%s] reported", label)
		util.WriteJsonPayload(w, locker)
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
	util.WriteJsonPayload(w, output)
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getAllLockers(w http.ResponseWriter, r *http.Request) {
	msg := ""
	getAll := strings.Contains(r.RequestURI, "/all")
	getData := util.GetBoolParamValue(r, "data") || getAll
	getEvents := util.GetBoolParamValue(r, "events") || getAll
	getPeerLockers := util.GetBoolParamValue(r, "peers") || getAll
	level := util.GetIntParamValue(r, "level", 5)
	registry.lockersLock.RLock()
	labeledLockers := registry.labeledLockers
	contextLockers := registry.contextLockers
	registry.lockersLock.RUnlock()
	msg = "All labeled lockers reported"
	data := map[string]any{}
	ll := labeledLockers.GetAllLockers(getPeerLockers, getEvents, getData, level)
	cl := contextLockers.GetAllLockers(getPeerLockers, getEvents, getData, level)
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
		util.WriteJsonPayload(w, labeledLockers.SearchInDataLockers(label, key))
		msg = fmt.Sprintf("Reported results for key %s search", key)
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
		ll.GetOrCreateLocker(label).Store(path, data)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Data stored in [Context: %s] labeled locker %s for path %+v", context, label, path)
		events.SendRequestEvent(events.Registry_LockerDataStored, msg, r)
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
		events.SendRequestEvent(events.Registry_LockerDataRemoved, msg, r)
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
			util.WriteJsonPayload(w, data)
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
	address := util.GetStringParamValue(r, "address")
	path, _ := util.GetListParam(r, "path")
	if peerName != "" && len(path) > 0 {
		data := util.Read(r.Body)
		registry.getCurrentLocker().StorePeerData(peerName, address, path, data)
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

func storePeerEvent(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	registry.peersLock.RLock()
	peer := registry.peers[peerName]
	registry.peersLock.RUnlock()
	if peer != nil {
		data := util.Read(r.Body)
		peer.lock.Lock()
		peer.eventsCounter++
		index := peer.eventsCounter
		peer.lock.Unlock()
		registry.getCurrentLocker().StorePeerData(peerName, "", []string{"events", strconv.Itoa(index)}, data)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Peer [%s] event [%d] stored", peerName, index)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No Peer"
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func removeFromPeerLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	peerName := util.GetStringParamValue(r, "peer")
	address := util.GetStringParamValue(r, "address")
	path, _ := util.GetListParam(r, "path")
	if peerName != "" && len(path) > 0 {
		registry.getCurrentLocker().RemovePeerData(peerName, address, path)
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
	address := util.GetStringParamValue(r, "address")
	result := registry.clearLocker(peerName, address, r)
	w.WriteHeader(http.StatusOK)
	util.WriteJsonPayload(w, result)
	if peerName != "" {
		if address != "" {
			msg = fmt.Sprintf("Peer %s Instance %s data cleared", peerName, address)
			events.SendRequestEvent(events.Registry_PeerInstanceLockerCleared, msg, r)
		} else {
			msg = fmt.Sprintf("Peer %s data cleared for all instances", peerName)
			events.SendRequestEvent(events.Registry_PeerLockerCleared, msg, r)
		}
	} else {
		msg = "All peer lockers cleared"
		events.SendRequestEvent(events.Registry_AllPeerLockersCleared, "", r)
	}
	if global.Flags.EnableRegistryLockerLogs {
		util.AddLogMessage(msg, r)
	}
}

func getFromPeerLocker(w http.ResponseWriter, r *http.Request) {
	msg := ""
	label := util.GetStringParamValue(r, "label")
	peerName := util.GetStringParamValue(r, "peer")
	peerAddress := util.GetStringParamValue(r, "address")
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
		data, dataAtKey := registry.labeledLockers.GetFromPeerInstanceLocker(label, peerName, peerAddress, path, getData, level)
		if dataAtKey {
			fmt.Fprint(w, data)
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
	peerAddress := util.GetStringParamValue(r, "address")
	getData := util.GetBoolParamValue(r, "data")
	getEvents := util.GetBoolParamValue(r, "events")
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
		result = locker.GetPeerLockersView(peerName, peerAddress, getData, getEvents)
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
	msg := ""
	label := util.GetStringParamValue(r, "label")
	peerName := util.GetStringParamValue(r, "peer")
	targets, _ := util.GetListParam(r, "targets")
	detailed := strings.Contains(r.RequestURI, "details")
	byInstances := strings.Contains(r.RequestURI, "instances")
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
