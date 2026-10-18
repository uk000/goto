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

package locker

import (
	"encoding/json"
	"fmt"
	"goto/pkg/client/results"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/util"
	"regexp"
	"strings"
	"sync"
	"time"
)

type PeersClientResults map[string]map[string]*results.TargetResults
type PeersInstanceClientResults map[string]map[string]map[string]*results.TargetResults

type LockerData struct {
	Data          any       `json:"data,omitempty"`
	SubKeys       Locker    `json:"subKeys,omitempty"`
	FirstReported time.Time `json:"firstReported,omitempty"`
	LastReported  time.Time `json:"lastReported,omitempty"`
	level         int
}

type Locker map[string]*LockerData

type DataLocker struct {
	Locker Locker `json:"locker"`
	Active bool   `json:"active"`
	lock   sync.RWMutex
}

type DataLockers map[string]*DataLocker

type PeerLocker struct {
	DataLocker      `json:"dataLocker,omitempty"`
	InstanceLockers DataLockers `json:"instanceLockers,omitempty"`
	lock            sync.RWMutex
}

type PeerMultiLockers map[string]map[string]*PeerLocker
type PeerEvents map[string][]*events.Event

type CombiLocker struct {
	Label       string                 `json:"label"`
	PathLabel   string                 `json:"-"`
	PeerLockers map[string]*PeerLocker `json:"peerLockers,omitempty"`
	DataLocker  *DataLocker            `json:"dataLocker,omitempty"`
	SubLockers  CombiLockers           `json:"subLockers,omitempty"`
	Current     bool                   `json:"current"`
	FirstOpened time.Time              `json:"firstOpened,omitempty"`
	LastOpened  time.Time              `json:"lastOpened,omitempty"`
	parent      *CombiLocker
	lock        sync.RWMutex
}

type CombiLockers map[string]*CombiLocker

type LabeledLockers struct {
	lockers       CombiLockers
	allLockers    CombiLockers
	currentLocker *CombiLocker
	lock          sync.RWMutex
}

type ContextLockers struct {
	labeledLockers map[string]*LabeledLockers
	lock           sync.RWMutex
}

// hasData tells whether a locker entry holds a value. Data is an interface (a string, or a list of values stored
// via "add"), so a plain comparison against "" is true for a nil Data and must not be used.
func hasData(data any) bool {
	if util.IsNil(data) {
		return false
	}
	if s, ok := data.(string); ok {
		return s != ""
	}
	return true
}

func newLockerData(now time.Time, level int) *LockerData {
	return &LockerData{SubKeys: Locker{}, FirstReported: now, level: level}
}

func createOrGetLockerData(locker Locker, key string, now time.Time, level int) *LockerData {
	lockerData := locker[key]
	if lockerData == nil {
		lockerData = newLockerData(now, level)
		locker[key] = lockerData
	}
	return lockerData
}

func unsafeStoreKeysInLocker(locker Locker, keys []string, value string, add bool) {
	if len(keys) == 0 {
		return
	}
	rootKey := keys[0]
	now := time.Now()
	lockerData := createOrGetLockerData(locker, rootKey, now, 1)
	lockerData.LastReported = now
	for i := 1; i < len(keys); i++ {
		lockerData.SubKeys[keys[i]] = createOrGetLockerData(lockerData.SubKeys, keys[i], now, i+1)
		lockerData = lockerData.SubKeys[keys[i]]
		lockerData.LastReported = now
	}
	if add {
		values := []any{}
		if !util.IsNil(lockerData.Data) {
			if v2, ok := lockerData.Data.([]any); ok {
				values = v2
			} else {
				values = append(values, lockerData.Data)
			}
		}
		values = append(values, value)
		lockerData.Data = values
	} else {
		lockerData.Data = value
	}
}

func unsafeRemoveSubKeys(lockerData *LockerData, keys []string, index int) {
	if len(keys) == 0 || index >= len(keys) {
		return
	}
	currentKey := keys[index]
	if lockerData.SubKeys[currentKey] != nil {
		nextLockerData := lockerData.SubKeys[currentKey]
		unsafeRemoveSubKeys(nextLockerData, keys, index+1)
		if len(nextLockerData.SubKeys) == 0 {
			delete(lockerData.SubKeys, currentKey)
		}
	}
}

func unsafeRemoveKeysFromLocker(locker Locker, keys []string) {
	if len(keys) == 0 {
		return
	}
	rootKey := keys[0]
	lockerData := locker[rootKey]
	if lockerData != nil {
		unsafeRemoveSubKeys(lockerData, keys, 1)
		if len(lockerData.SubKeys) == 0 {
			delete(locker, rootKey)
		}
	}
}

func unsafeReadKeys(locker Locker, keys []string, data bool, level int) (interface{}, bool, bool) {
	if len(keys) > 0 && locker[keys[0]] != nil {
		lockerData := locker[keys[0]]
		for i := 1; i < len(keys); i++ {
			if lockerData.SubKeys[keys[i]] != nil {
				lockerData = lockerData.SubKeys[keys[i]]
			} else {
				lockerData = nil
				break
			}
		}
		if lockerData != nil {
			if hasData(lockerData.Data) {
				return lockerData.Data, true, true
			} else if !data || level > 0 {
				ldView := newLockerData(lockerData.FirstReported, lockerData.level)
				ldView.LastReported = lockerData.LastReported
				if level > 0 {
					unsafeTrimLocker(lockerData.SubKeys, ldView.SubKeys, level)
				}
				if !data {
					unsafeGetLockerView(lockerData.SubKeys, ldView.SubKeys, data)
				}
				return ldView, true, false
			} else {
				return lockerData, true, false
			}
		}
	}
	return "", false, false
}

func unsafeSearchOrGetPaths(locker Locker, pathPrefix string, isTop, flat bool, pattern *regexp.Regexp) interface{} {
	var paths interface{}
	mapPaths := map[string]interface{}{}
	var flatPaths []interface{}
	if locker != nil {
		for key, ld := range locker {
			if key == constants.LockerEventsKey || pattern != nil && !pattern.MatchString(key) {
				continue
			}
			if ld != nil {
				var currentPathPrefix string
				if isTop {
					currentPathPrefix = pathPrefix + key
				} else {
					currentPathPrefix = pathPrefix + "," + key
				}
				subData := unsafeSearchOrGetPaths(ld.SubKeys, currentPathPrefix, false, flat, pattern)
				if flat {
					if hasData(ld.Data) {
						flatPaths = append(flatPaths, currentPathPrefix)
					}
					if subKeys, ok := subData.([]interface{}); ok && len(subKeys) > 0 {
						flatPaths = append(flatPaths, subKeys...)
					}
					paths = flatPaths
				} else {
					if subKeys, ok := subData.(map[string]interface{}); ok && len(subKeys) > 0 {
						if hasData(ld.Data) {
							subKeys["."] = currentPathPrefix
						}
						mapPaths[key] = subKeys
					} else if hasData(ld.Data) {
						mapPaths[key] = currentPathPrefix
					}
					paths = mapPaths
				}
			}
		}
	}
	return paths
}

func unsafeGetLockerView(locker, lockerView Locker, data bool) {
	if locker != nil && lockerView != nil {
		for key, ld := range locker {
			if ld != nil {
				ldView := newLockerData(ld.FirstReported, ld.level)
				if ld.Data != nil {
					if data {
						if val, ok := ld.Data.(string); ok {
							m := map[string]any{}
							if err := json.Unmarshal([]byte(val), &m); err == nil {
								ldView.Data = m
							} else {
								ldView.Data = ld.Data
							}
						} else {
							ldView.Data = ld.Data
						}
					} else {
						ldView.Data = "..."
					}
				}
				ldView.LastReported = ld.LastReported
				unsafeGetLockerView(ld.SubKeys, ldView.SubKeys, data)
				lockerView[key] = ldView
			} else {
				lockerView[key] = nil
			}
		}
	}
}

func unsafeTrimLocker(locker, lockerView Locker, level int) {
	if locker != nil && lockerView != nil && level > 0 {
		for key, ld := range locker {
			if ld != nil {
				ldView := newLockerData(ld.FirstReported, ld.level)
				if hasData(ld.Data) {
					ldView.Data = ld.Data
				}
				ldView.LastReported = ld.LastReported
				if level > 1 {
					unsafeTrimLocker(ld.SubKeys, ldView.SubKeys, level-1)
				} else {
					if len(ld.SubKeys) > 0 {
						ldView.SubKeys = map[string]*LockerData{"...": nil}
					}
				}
				lockerView[key] = ldView
			} else {
				lockerView[key] = nil
			}
		}
	}
}

func (ld *LockerData) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{}
	if ld.level == 1 {
		data["firstReported"] = ld.FirstReported
		data["lastReported"] = ld.LastReported
	}
	if len(ld.SubKeys) > 0 {
		for k, v := range ld.SubKeys {
			if util.IsNil(v) {
				data[k] = ""
			} else {
				data[k] = v
			}
		}
	}
	if hasData(ld.Data) {
		data["data"] = ld.Data
	}
	return json.Marshal(data)
}

func newDataLocker() *DataLocker {
	dataLocker := &DataLocker{}
	dataLocker.init()
	return dataLocker
}

func (dl *DataLocker) init() {
	dl.lock.Lock()
	defer dl.lock.Unlock()
	dl.Locker = map[string]*LockerData{}
	dl.Active = true
}

func (dl *DataLocker) MarshalJSON() ([]byte, error) {
	return json.Marshal(dl.Locker)
}

func (dl *DataLocker) Store(keys []string, value string, add bool) {
	if len(keys) == 0 {
		return
	}
	dl.lock.Lock()
	defer dl.lock.Unlock()
	unsafeStoreKeysInLocker(dl.Locker, keys, value, add)
}

func (dl *DataLocker) Remove(keys []string) {
	dl.lock.Lock()
	defer dl.lock.Unlock()
	unsafeRemoveKeysFromLocker(dl.Locker, keys)
}

func (dl *DataLocker) Get(keys []string, data bool, level int) (interface{}, bool, bool) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()
	return unsafeReadKeys(dl.Locker, keys, data, level)
}

func (dl *DataLocker) Trim(level int) *DataLocker {
	dl.lock.RLock()
	defer dl.lock.RUnlock()
	dataView := newDataLocker()
	dataView.Active = dl.Active
	unsafeTrimLocker(dl.Locker, dataView.Locker, level)
	return dataView
}

func (dl *DataLocker) Deactivate() {
	dl.Active = false
}

func newPeerLocker() *PeerLocker {
	peerLocker := &PeerLocker{}
	peerLocker.init()
	return peerLocker
}

func (pl *PeerLocker) init() {
	pl.lock.Lock()
	defer pl.lock.Unlock()
	pl.InstanceLockers = DataLockers{}
	pl.DataLocker.init()
}

func (pl *PeerLocker) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{}
	if len(pl.DataLocker.Locker) > 0 {
		for k, v := range pl.DataLocker.Locker {
			data[k] = v
		}
	}
	if len(pl.InstanceLockers) > 0 {
		data["instanceLockers"] = pl.InstanceLockers
	}
	return json.Marshal(data)
}

func (pl *PeerLocker) createOrGetInstanceLocker(peerLabel string) *DataLocker {
	pl.lock.RLock()
	il := pl.InstanceLockers[peerLabel]
	pl.lock.RUnlock()
	if il == nil {
		pl.lock.Lock()
		il = newDataLocker()
		pl.InstanceLockers[peerLabel] = il
		pl.lock.Unlock()
	}
	return il
}

func (pl *PeerLocker) clearInstanceLocker(peerLabel string) bool {
	pl.lock.RLock()
	_, present := pl.InstanceLockers[peerLabel]
	pl.lock.RUnlock()
	if present {
		pl.lock.Lock()
		pl.InstanceLockers[peerLabel] = newDataLocker()
		pl.lock.Unlock()
	}
	return present
}

func (pl *PeerLocker) removeInstanceLocker(peerLabel string) {
	pl.lock.Lock()
	defer pl.lock.Unlock()
	delete(pl.InstanceLockers, peerLabel)
}

func (pl *PeerLocker) Store(keys []string, value string) {
	pl.lock.Lock()
	defer pl.lock.Unlock()
	pl.DataLocker.Store(keys, value, false)
}

func (pl *PeerLocker) Remove(keys []string) {
	pl.lock.Lock()
	defer pl.lock.Unlock()
	pl.DataLocker.Remove(keys)
}

func (pl *PeerLocker) GetLockerWithoutEvents() *PeerLocker {
	plView := newPeerLocker()
	plView.Active = pl.Active
	plView.InstanceLockers = pl.InstanceLockers
	for k, ld := range pl.Locker {
		if k != constants.LockerEventsKey {
			plView.Locker[k] = ld
		}
	}
	return plView
}

func (pl *PeerLocker) GetLockerView(data, events bool) *PeerLocker {
	pl.lock.RLock()
	defer pl.lock.RUnlock()
	plView := newPeerLocker()
	plView.Active = pl.Active
	unsafeGetLockerView(pl.Locker, plView.Locker, data)
	if !events {
		delete(plView.Locker, constants.LockerEventsKey)
	}
	for label, il := range pl.InstanceLockers {
		ilView := newDataLocker()
		plView.InstanceLockers[label] = ilView
		unsafeGetLockerView(il.Locker, ilView.Locker, data)
		ilView.Active = il.Active
	}
	return plView
}

func (pl *PeerLocker) Trim(level int) *PeerLocker {
	pl.lock.RLock()
	defer pl.lock.RUnlock()
	plView := newPeerLocker()
	unsafeTrimLocker(pl.DataLocker.Locker, plView.DataLocker.Locker, level)
	plView.DataLocker.Active = pl.DataLocker.Active
	for label, il := range pl.InstanceLockers {
		ilView := newDataLocker()
		plView.InstanceLockers[label] = ilView
		unsafeTrimLocker(il.Locker, ilView.Locker, level)
		ilView.Active = il.Active
	}
	return plView
}

func (pl *PeerLocker) SearchOrGetDataLockerPaths(uriPrefix string, pattern *regexp.Regexp) interface{} {
	pl.lock.RLock()
	defer pl.lock.RUnlock()
	return unsafeSearchOrGetPaths(pl.DataLocker.Locker, uriPrefix, true, uriPrefix == "", pattern)
}

func NewCombiLocker(label string, parent *CombiLocker) *CombiLocker {
	pathLabel := label
	if parent != nil {
		pathLabel = parent.PathLabel + "," + label
	}
	cl := &CombiLocker{Label: label, PathLabel: pathLabel, parent: parent}
	cl.Init()
	return cl
}

func (cl *CombiLocker) Clone(peers, events, data, raw bool) *CombiLocker {
	pathLabel := cl.Label
	if cl.parent != nil {
		pathLabel = cl.parent.PathLabel + "," + cl.Label
	}
	cl2 := &CombiLocker{Label: cl.Label, PathLabel: pathLabel, parent: cl.parent}
	cl2.Init()
	cl2.FirstOpened = cl.FirstOpened
	cl2.LastOpened = cl.LastOpened
	cl2.Current = cl.Current
	if raw {
		cl2.DataLocker = cl.DataLocker
	} else {
		cl2.DataLocker.Active = cl.DataLocker.Active
		unsafeGetLockerView(cl.DataLocker.Locker, cl2.DataLocker.Locker, data)
	}
	for label, sub := range cl.SubLockers {
		cl2.SubLockers[label] = sub.GetLockerView(events, data)
	}
	if peers {
		for peer, pl := range cl.PeerLockers {
			cl2.PeerLockers[peer] = pl.GetLockerView(data, events)
		}
	}
	return cl2
}

func (cl *CombiLocker) Init() {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	cl.PeerLockers = map[string]*PeerLocker{}
	cl.DataLocker = newDataLocker()
	cl.SubLockers = map[string]*CombiLocker{}
	cl.Open()
}

func (cl *CombiLocker) Open() {
	if !cl.Current {
		now := time.Now()
		if cl.FirstOpened.IsZero() {
			cl.FirstOpened = now
		}
		cl.LastOpened = now
		cl.Current = true
	}
}

func (cl *CombiLocker) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{
		"label":       cl.Label,
		"dataLocker":  cl.DataLocker.Locker,
		"peerLockers": cl.PeerLockers,
		"subLockers":  cl.SubLockers,
		"current":     cl.Current,
		"firstOpened": cl.FirstOpened,
		"lastOpened":  cl.LastOpened,
	}
	return json.Marshal(data)
}

func (cl *CombiLocker) GetLabels() map[string]interface{} {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	labels := map[string]interface{}{}
	subLabels := map[string]interface{}{}
	for _, sub := range cl.SubLockers {
		for k, v := range sub.GetLabels() {
			subLabels[k] = v
		}
	}
	currentLabel := map[string]interface{}{
		"firstOpened": cl.FirstOpened,
		"lastOpened":  cl.LastOpened,
		"current":     cl.Current,
	}
	if len(subLabels) > 0 {
		subLabels["."] = currentLabel
		labels[cl.PathLabel] = subLabels
	} else {
		labels[cl.PathLabel] = currentLabel
	}
	return labels
}

func (cl *CombiLocker) GetSubLocker(labels []string) *CombiLocker {
	if len(labels) == 0 {
		return nil
	}
	cl.lock.Lock()
	defer cl.lock.Unlock()
	sub := cl.SubLockers[labels[0]]
	if sub != nil && len(labels) > 1 {
		sub = sub.GetSubLocker(labels[1:])
	}
	return sub
}

func (cl *CombiLocker) InitPeerLocker(peerName string, peerLabel string) bool {
	if peerName != "" {
		peerName = strings.ToLower(peerName)
		cl.lock.Lock()
		if peerLabel == "" || cl.PeerLockers[peerName] == nil {
			cl.PeerLockers[peerName] = newPeerLocker()
		}
		if peerLabel != "" {
			cl.PeerLockers[peerName].clearInstanceLocker(peerLabel)
		}
		cl.lock.Unlock()
	} else {
		cl.PeerLockers = map[string]*PeerLocker{}
	}
	return true
}

func (cl *CombiLocker) createOrGetPeerLocker(peerName string) *PeerLocker {
	peerName = strings.ToLower(peerName)
	cl.lock.Lock()
	defer cl.lock.Unlock()
	peerLocker := cl.PeerLockers[peerName]
	if peerLocker == nil {
		peerLocker = newPeerLocker()
		cl.PeerLockers[peerName] = peerLocker
	}
	return peerLocker
}

func (cl *CombiLocker) getPeerLocker(peerName string) *PeerLocker {
	peerName = strings.ToLower(peerName)
	cl.lock.Lock()
	defer cl.lock.Unlock()
	return cl.PeerLockers[peerName]
}

func (cl *CombiLocker) getPeerLockers(peerName string) PeerMultiLockers {
	peerName = strings.ToLower(peerName)
	lockers := PeerMultiLockers{}
	cl.lock.Lock()
	defer cl.lock.Unlock()
	if peerName != "" {
		lockers[peerName] = map[string]*PeerLocker{}
		if l := cl.PeerLockers[peerName]; l != nil {
			lockers[peerName][cl.PathLabel] = l
		}
	} else {
		for p, pl := range cl.PeerLockers {
			if pl != nil {
				lockers[p] = map[string]*PeerLocker{}
				lockers[p][cl.PathLabel] = pl
			}
		}
	}
	for _, sub := range cl.SubLockers {
		for peer, pls := range sub.getPeerLockers(peerName) {
			for label, pl := range pls {
				if pl != nil {
					if lockers[peer] == nil {
						lockers[peer] = map[string]*PeerLocker{}
					}
					lockers[peer][label] = pl
				}
			}
		}
	}
	return lockers
}

func (cl *CombiLocker) getPeersLockers(peerNames []string) map[string]*PeerLocker {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	peerLockers := map[string]*PeerLocker{}
	if len(peerNames) > 0 {
		for _, peerName := range peerNames {
			peerName = strings.ToLower(peerName)
			if cl.PeerLockers[peerName] != nil {
				peerLockers[peerName] = cl.PeerLockers[peerName]
			}
		}
	} else {
		for peer, peerLocker := range cl.PeerLockers {
			peerLockers[peer] = peerLocker
		}
	}
	return peerLockers
}

func (cl *CombiLocker) createOrGetInstanceLocker(peerName string, peerLabel string) *DataLocker {
	return cl.createOrGetPeerLocker(peerName).createOrGetInstanceLocker(peerLabel)
}

func (cl *CombiLocker) DeactivateInstanceLocker(peerName string, peerLabel string) {
	cl.createOrGetPeerLocker(peerName).createOrGetInstanceLocker(peerLabel).Deactivate()
}

func (cl *CombiLocker) StorePeerData(peerName, peerLabel string, keys []string, value string) {
	if len(keys) == 0 {
		return
	}
	if peerLabel == "" {
		cl.createOrGetPeerLocker(peerName).Store(keys, value)
	} else {
		cl.createOrGetInstanceLocker(peerName, peerLabel).Store(keys, value, false)
	}
}

func (cl *CombiLocker) RemovePeerData(peerName, peerLabel string, keys []string) {
	if len(keys) == 0 {
		return
	}
	if peerLabel == "" {
		cl.createOrGetPeerLocker(peerName).Remove(keys)
	} else {
		cl.createOrGetInstanceLocker(peerName, peerLabel).Remove(keys)
	}
}

func (cl *CombiLocker) Store(keys []string, value string, add bool) {
	cl.lock.Lock()
	dl := cl.DataLocker
	cl.lock.Unlock()
	dl.Store(keys, value, add)
}

func (cl *CombiLocker) Remove(keys []string) {
	cl.lock.Lock()
	dl := cl.DataLocker
	cl.lock.Unlock()
	dl.Remove(keys)
}

func (cl *CombiLocker) GetFromSubLockers(keys []string, data bool, level int) map[string]interface{} {
	if len(keys) == 0 {
		return nil
	}
	result := map[string]interface{}{}
	for _, sub := range cl.SubLockers {
		if subData, present, _ := sub.Get(keys, data, level); present {
			result[sub.PathLabel] = subData
		}
		for subPath, subData := range sub.GetFromSubLockers(keys, data, level) {
			if subData != nil {
				result[subPath] = subData
			}
		}
	}
	return result
}

func (cl *CombiLocker) Get(keys []string, data bool, level int) (interface{}, bool, bool) {
	if len(keys) == 0 {
		return nil, false, false
	}
	cl.lock.Lock()
	dl := cl.DataLocker
	cl.lock.Unlock()
	result, present, dataAtKey := dl.Get(keys, data, level)
	if dataAtKey {
		return result, present, dataAtKey
	}
	if present {
		return map[string]interface{}{strings.Join(keys, ","): result}, true, false
	}
	return nil, false, false
}

func (cl *CombiLocker) ClearInstanceLocker(peerName, peerLabel string) bool {
	peerName = strings.ToLower(peerName)
	cl.lock.Lock()
	peerLocker := cl.PeerLockers[peerName]
	cl.lock.Unlock()
	if peerLocker != nil {
		return peerLocker.clearInstanceLocker(peerLabel)
	}
	return false
}

func (cl *CombiLocker) RemovePeerLocker(peerName string) {
	peerName = strings.ToLower(peerName)
	cl.lock.Lock()
	defer cl.lock.Unlock()
	delete(cl.PeerLockers, peerName)
}

func (cl *CombiLocker) GetFromPeerInstanceLocker(peerName, peerLabel string, keys []string, data bool, level int) (interface{}, bool, bool) {
	if len(keys) == 0 || peerName == "" {
		return nil, false, false
	}
	peerName = strings.ToLower(peerName)
	cl.lock.RLock()
	pl := cl.PeerLockers[peerName]
	cl.lock.RUnlock()
	if pl == nil {
		return nil, false, false
	}
	instanceLockers := DataLockers{}
	pl.lock.RLock()
	if peerLabel != "" {
		if pl.InstanceLockers[peerLabel] != nil {
			instanceLockers[peerLabel] = pl.InstanceLockers[peerLabel]
		}
	} else {
		for label, il := range pl.InstanceLockers {
			instanceLockers[label] = il
		}
	}
	pl.lock.RUnlock()
	plResult, plPresent, plDataAtKey := pl.Get(keys, data, level)
	if len(instanceLockers) == 0 {
		return plResult, plPresent, plDataAtKey
	}
	if !plPresent && len(instanceLockers) == 1 {
		for _, il := range instanceLockers {
			result, present, dataAtKey := il.Get(keys, true, 0)
			if dataAtKey {
				return result, present, dataAtKey
			}
			if present {
				return map[string]interface{}{strings.Join(keys, ","): result}, true, false
			}
			return nil, false, false
		}
	}
	result := map[string]interface{}{}
	for label, il := range instanceLockers {
		instanceData, present, _ := il.Get(keys, true, 0)
		if present {
			result[label] = instanceData
		}
	}
	if len(result) == 0 {
		return plResult, plPresent, plDataAtKey
	}
	if plPresent {
		result[peerName] = plResult
	}
	return result, true, false
}

func (cl *CombiLocker) GetPeerLockers(peerName, peerLabel string, events bool) map[string]map[string]interface{} {
	peerName = strings.ToLower(peerName)
	peerLockers := cl.getPeerLockers(peerName)
	if len(peerLockers) == 0 {
		return nil
	}
	lockers := map[string]map[string]interface{}{}
	for label, pls := range peerLockers {
		if lockers[label] == nil {
			lockers[label] = map[string]interface{}{}
		}
		for peer, pl := range pls {
			if !events {
				pl = pl.GetLockerWithoutEvents()
			}
			if peerLabel == "" {
				lockers[label][peer] = pl
			} else {
				lockers[label][peer] = pl.createOrGetInstanceLocker(peerLabel)
			}
		}
	}
	return lockers
}

func (cl *CombiLocker) GetPeerLockersView(peerName, peerLabel string, data, events bool) map[string]map[string]interface{} {
	peerName = strings.ToLower(peerName)
	peerLockers := cl.getPeerLockers(peerName)
	if len(peerLockers) == 0 {
		return nil
	}
	lockers := map[string]map[string]interface{}{}
	for label, pls := range peerLockers {
		if lockers[label] == nil {
			lockers[label] = map[string]interface{}{}
		}
		for peer, pl := range pls {
			plView := pl.GetLockerView(data, events)
			if peerLabel == "" {
				lockers[label][peer] = plView
			} else {
				lockers[label][peer] = plView.createOrGetInstanceLocker(peerLabel)
			}
		}
	}
	return lockers
}

func (cl *CombiLocker) GetDataLockers() DataLockers {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	dataLockers := map[string]*DataLocker{}
	dataLockers[cl.PathLabel] = cl.DataLocker
	for _, sub := range cl.SubLockers {
		for pathLabel, subDL := range sub.GetDataLockers() {
			dataLockers[pathLabel] = subDL
		}
	}
	return dataLockers
}

func (cl *CombiLocker) GetLockerView(events, data bool) *CombiLocker {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	return cl.Clone(true, events, data, false)
}

func (cl *CombiLocker) GetLockerWithoutPeers() *CombiLocker {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	return cl.Clone(false, false, true, false)
}

func (cl *CombiLocker) GetLockerWithoutEvents() *CombiLocker {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	return cl.Clone(true, false, true, false)
}

func (cl *CombiLocker) GetDataLockerView() *DataLocker {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	dataView := newDataLocker()
	dataView.Active = cl.DataLocker.Active
	unsafeGetLockerView(cl.DataLocker.Locker, dataView.Locker, false)
	return dataView
}

func (cl *CombiLocker) Trim(level int) *CombiLocker {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	combiView := NewCombiLocker(cl.Label, cl.parent)
	for label, sub := range cl.SubLockers {
		combiView.SubLockers[label] = sub.Trim(level)
	}
	for peer, pl := range cl.PeerLockers {
		combiView.PeerLockers[peer] = pl.Trim(level)
	}
	combiView.DataLocker = cl.DataLocker.Trim(level)
	combiView.DataLocker.Active = cl.DataLocker.Active
	combiView.Current = cl.Current
	return combiView
}

func (cl *CombiLocker) searchOrGetPathsFromSubLockers(uriPrefix string, pattern *regexp.Regexp) map[string]map[string]interface{} {
	subPaths := map[string]map[string]interface{}{}
	for _, sub := range cl.SubLockers {
		subPaths[sub.Label] = map[string]interface{}{}
		subResults := sub.SearchOrGetDataLockerPaths(uriPrefix, pattern, false)
		for key, paths := range subResults {
			if paths != nil {
				subPaths[sub.Label][key] = paths
			}
		}
	}
	return subPaths
}

func (cl *CombiLocker) searchOrGetPathsFromPeerLockers(uriPrefix string, pattern *regexp.Regexp) map[string]interface{} {
	peerPaths := map[string]interface{}{}
	for peer, pl := range cl.PeerLockers {
		if peerData := pl.SearchOrGetDataLockerPaths(uriPrefix, pattern); peerData != nil {
			peerPaths[peer] = peerData
		}
	}
	return peerPaths
}

func (cl *CombiLocker) SearchOrGetDataLockerPaths(uriPrefix string, pattern *regexp.Regexp, isTop bool) map[string]interface{} {
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	dataPaths := map[string]interface{}{}
	if !isTop && uriPrefix != "" {
		dataPaths["."] = uriPrefix + cl.PathLabel + "/data/paths"
	}
	lockerGetURI := ""
	if uriPrefix != "" {
		lockerGetURI = uriPrefix + cl.PathLabel + "/get/"
	}
	if cl.DataLocker != nil && cl.DataLocker.Locker != nil && len(cl.DataLocker.Locker) > 0 {
		dataPaths["data"] = unsafeSearchOrGetPaths(cl.DataLocker.Locker, lockerGetURI, true, uriPrefix == "", pattern)
	}
	if cl.PeerLockers != nil {
		dataPaths["peerLockers"] = cl.searchOrGetPathsFromPeerLockers(lockerGetURI, pattern)
	}
	if subPaths := cl.searchOrGetPathsFromSubLockers(uriPrefix, pattern); len(subPaths) > 0 {
		dataPaths["subLockers"] = subPaths
	}
	return dataPaths
}

func (cl *CombiLocker) getPeersClientResults(peerName string, targets []string, trackingHeaders []string, crossTrackingHeaders map[string][]string,
	trackingTimeBuckets [][]int, detailed, byInstances bool) (PeersClientResults, PeersInstanceClientResults) {
	peersClientResults := PeersClientResults{}
	peersInstanceClientResults := PeersInstanceClientResults{}
	peerLockers := map[string]*PeerLocker{}
	cl.lock.RLock()
	defer cl.lock.RUnlock()
	if peerName != "" {
		peerName = strings.ToLower(peerName)
		if cl.PeerLockers[peerName] != nil {
			peerLockers[peerName] = cl.PeerLockers[peerName]
		}
	} else {
		peerLockers = cl.PeerLockers
	}
	for peer, peerLocker := range peerLockers {
		if byInstances {
			peersInstanceClientResults[peer] = map[string]map[string]*results.TargetResults{}
		} else {
			peersClientResults[peer] = map[string]*results.TargetResults{}
		}
		peerLocker.lock.RLock()
		for label, instanceLocker := range peerLocker.InstanceLockers {
			if byInstances {
				peersInstanceClientResults[peer][label] = map[string]*results.TargetResults{}
			}
			instanceLocker.lock.RLock()
			lockerData := instanceLocker.Locker[constants.LockerClientKey]
			if lockerData != nil {
				for target, targetData := range lockerData.SubKeys {
					if strings.EqualFold(target, constants.LockerInvocationsKey) {
						continue
					}
					if len(targets) > 0 && !util.IsStringInArray(target, targets) {
						continue
					}
					if !byInstances && peersClientResults[peer][target] == nil {
						peersClientResults[peer][target] = results.NewTargetResults(target, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
					}
					if byInstances && peersInstanceClientResults[peer][label][target] == nil {
						peersInstanceClientResults[peer][label][target] = results.NewTargetResults(target, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
					}
					if data := targetData.Data; data != "" {
						result := &results.TargetResults{}
						if val, ok := data.(string); ok {
							if err := util.ReadJson(val, result); err == nil {
								if byInstances {
									results.AddDeltaResults(peersInstanceClientResults[peer][label][target], result, detailed)
								} else {
									results.AddDeltaResults(peersClientResults[peer][target], result, detailed)
								}
							} else {
								fmt.Printf("Error parsing peer result json: %s\n", err.Error())
							}
						}
					}
				}
			}
			instanceLocker.lock.RUnlock()
		}
		peerLocker.lock.RUnlock()
	}
	return peersClientResults, peersInstanceClientResults
}

func (cl *CombiLocker) GetPeersClientResults(peerName string, targets []string, trackingHeaders []string, crossTrackingHeaders map[string][]string,
	trackingTimeBuckets [][]int, detailed, byInstances bool) interface{} {

	var summaryResults interface{}
	peersClientResults, peersInstanceClientResults := cl.getPeersClientResults(peerName, targets, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets, detailed, byInstances)
	summaryPeersResults := map[string]*results.ClientTargetsAggregateResultsView{}
	summaryPeersInstanceResults := map[string]map[string]*results.ClientTargetsAggregateResultsView{}
	detailedResults := map[string]interface{}{}

	if byInstances {
		for peer, instanceResults := range peersInstanceClientResults {
			summaryPeersInstanceResults[peer] = map[string]*results.ClientTargetsAggregateResultsView{}
			for label, targetsResults := range instanceResults {
				summaryPeersInstanceResults[peer][label] = results.NewClientTargetsAggregateResults()
				for _, targetResult := range targetsResults {
					summaryPeersInstanceResults[peer][label].AddTargetResult(targetResult, detailed)
				}
			}
		}
		if detailed {
			detailedResults = map[string]interface{}{
				"summary": summaryPeersInstanceResults,
				"details": peersInstanceClientResults,
			}
		} else {
			summaryResults = summaryPeersInstanceResults
		}
	} else {
		for peer, targetsResults := range peersClientResults {
			summaryPeersResults[peer] = results.NewClientTargetsAggregateResults()
			for _, targetResult := range targetsResults {
				summaryPeersResults[peer].AddTargetResult(targetResult, detailed)
			}
		}
		if detailed {
			detailedResults = map[string]interface{}{
				"summary": summaryPeersResults,
				"details": peersClientResults,
			}
		} else {
			summaryResults = summaryPeersResults
		}
	}
	if detailed {
		return detailedResults
	}
	return summaryResults
}

func convertLockerDataToEvent(l *LockerData) *events.Event {
	event := &events.Event{}
	if l.Data != nil {
		if val, ok := l.Data.(string); ok {
			if err := util.ReadJson(val, event); err == nil {
				return event
			} else {
				fmt.Printf("Error while parsing event: %s\n", err.Error())
			}
		}
	}
	return nil
}

func sortPeerEvents(peerEvents PeerEvents, reverse bool) {
	for _, pe := range peerEvents {
		events.SortEvents(pe, reverse)
	}
}

// eventEntries returns the stored events of a peer: those stored at the peer level as well as those stored by its
// instances (events are posted per peer instance via /registry/peers/{peer}/{address}/events/store).
// The caller must hold pl.lock.
func (pl *PeerLocker) eventEntries() []*LockerData {
	var entries []*LockerData
	collect := func(l Locker) {
		if eventsLocker := l[constants.LockerEventsKey]; eventsLocker != nil {
			for _, ld := range eventsLocker.SubKeys {
				if ld != nil {
					entries = append(entries, ld)
				}
			}
		}
	}
	collect(pl.DataLocker.Locker)
	for _, il := range pl.InstanceLockers {
		if il != nil {
			il.lock.RLock()
			collect(il.Locker)
			il.lock.RUnlock()
		}
	}
	return entries
}

func (cl *CombiLocker) GetPeerEvents(peerNames []string, unified, reverse, data bool) PeerEvents {
	eventsMap := PeerEvents{}
	peerLockers := cl.getPeersLockers(peerNames)
	for peer, pl := range peerLockers {
		pl.lock.RLock()
		entries := pl.eventEntries()
		pl.lock.RUnlock()
		for _, ld := range entries {
			if event := convertLockerDataToEvent(ld); event != nil {
				if !data {
					event.Data = "..."
				}
				key := peer
				if unified {
					key = "all"
				}
				eventsMap[key] = append(eventsMap[key], event)
			}
		}
	}
	sortPeerEvents(eventsMap, reverse)
	return eventsMap
}

func (cl *CombiLocker) SearchInPeerEvents(peerNames []string, pattern *regexp.Regexp, unified, reverse, data bool) PeerEvents {
	eventsMap := PeerEvents{}
	peerLockers := cl.getPeersLockers(peerNames)
	for peer, pl := range peerLockers {
		pl.lock.RLock()
		entries := pl.eventEntries()
		pl.lock.RUnlock()
		for _, ld := range entries {
			if val, ok := ld.Data.(string); ok && pattern.MatchString(val) {
				if event := convertLockerDataToEvent(ld); event != nil {
					if !data {
						event.Data = "..."
					}
					key := peer
					if unified {
						key = "all"
					}
					eventsMap[key] = append(eventsMap[key], event)
				}
			}
		}
	}
	sortPeerEvents(eventsMap, reverse)
	return eventsMap
}

func (cl *CombiLocker) ClearPeerEvents() {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	now := time.Now()
	for _, pl := range cl.PeerLockers {
		pl.lock.Lock()
		pl.DataLocker.Locker[constants.LockerEventsKey] = newLockerData(now, 1)
		for _, il := range pl.InstanceLockers {
			if il != nil {
				il.lock.Lock()
				if il.Locker[constants.LockerEventsKey] != nil {
					il.Locker[constants.LockerEventsKey] = newLockerData(now, 1)
				}
				il.lock.Unlock()
			}
		}
		pl.lock.Unlock()
	}
}

func NewLabeledLockers() *LabeledLockers {
	ll := &LabeledLockers{}
	ll.Init()
	return ll
}

func (ll *LabeledLockers) Init() {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	ll.lockers = CombiLockers{}
	ll.allLockers = CombiLockers{}
	ll.lockers[constants.LockerDefaultLabel] = NewCombiLocker(constants.LockerDefaultLabel, nil)
	ll.allLockers[constants.LockerDefaultLabel] = ll.lockers[constants.LockerDefaultLabel]
	ll.currentLocker = ll.lockers[constants.LockerDefaultLabel]
	ll.currentLocker.Current = true
}

func (ll *LabeledLockers) unsafeCreateOrGetLocker(label string) (*CombiLocker, *CombiLocker) {
	labels := strings.Split(label, ",")
	l := labels[0]
	if l == "" {
		return nil, nil
	}
	locker := ll.lockers[l]
	var parent *CombiLocker
	if locker == nil {
		locker = NewCombiLocker(l, nil)
		ll.lockers[l] = locker
		ll.allLockers[l] = locker
	}
	pathLabel := l
	for i := 1; i < len(labels); i++ {
		parent = locker
		l := labels[i]
		pathLabel += "," + l
		if locker.SubLockers[l] == nil {
			locker.SubLockers[l] = NewCombiLocker(l, parent)
			ll.allLockers[pathLabel] = locker.SubLockers[l]
		}
		locker = locker.SubLockers[l]
	}
	return locker, parent
}

func (ll *LabeledLockers) OpenLocker(label string) {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	if ll.currentLocker != nil {
		ll.currentLocker.lock.Lock()
		ll.currentLocker.Current = false
		ll.currentLocker.lock.Unlock()
	}
	ll.currentLocker, _ = ll.unsafeCreateOrGetLocker(label)
	ll.currentLocker.Open()
}

func (ll *LabeledLockers) deleteLocker(locker *CombiLocker) {
	if locker.parent == nil {
		delete(ll.lockers, locker.Label)
	} else {
		delete(locker.parent.SubLockers, locker.Label)
	}
	delete(ll.allLockers, locker.PathLabel)
	for _, subLocker := range locker.SubLockers {
		ll.deleteLocker(subLocker)
	}
}

func (ll *LabeledLockers) ClearLocker(label string, close bool) {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	locker := ll.allLockers[label]
	if locker != nil {
		if close {
			ll.deleteLocker(locker)
			if locker == ll.currentLocker {
				ll.currentLocker = ll.lockers[constants.LockerDefaultLabel]
				ll.currentLocker.Current = true
			}
		} else {
			for _, subLocker := range locker.SubLockers {
				if subLocker != nil {
					ll.deleteLocker(subLocker)
				}
			}
			wasCurrent := locker.Current
			locker.Init()
			// Init opens the locker, which must not make a non-current locker current
			locker.Current = wasCurrent
		}
	}
}

// ReplaceLockers replaces all lockers with the given ones (e.g. loaded from a dump). The parent links, path labels
// and the index of all lockers (including sub-lockers) are rebuilt, the default locker is added if missing, and the
// current locker is restored (the default locker if none of the given lockers is current).
func (ll *LabeledLockers) ReplaceLockers(lockers CombiLockers) {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	newLockers := CombiLockers{}
	allLockers := CombiLockers{}
	var current *CombiLocker
	currentDepth := 0
	var relink func(cl, parent *CombiLocker)
	relink = func(cl, parent *CombiLocker) {
		cl.parent = parent
		if parent == nil {
			cl.PathLabel = cl.Label
		} else {
			cl.PathLabel = parent.PathLabel + "," + cl.Label
		}
		allLockers[cl.PathLabel] = cl
		if cl.PeerLockers == nil {
			cl.PeerLockers = map[string]*PeerLocker{}
		}
		if cl.DataLocker == nil {
			cl.DataLocker = newDataLocker()
		} else if cl.DataLocker.Locker == nil {
			cl.DataLocker.Locker = Locker{}
		}
		if cl.SubLockers == nil {
			cl.SubLockers = CombiLockers{}
		}
		if cl.Current {
			// More than one locker can be flagged current (e.g. opening a,b flags both a and b), the deepest one
			// is the one that was opened last.
			depth := strings.Count(cl.PathLabel, ",")
			if current == nil || depth > currentDepth || depth == currentDepth && cl.PathLabel < current.PathLabel {
				current = cl
				currentDepth = depth
			}
		}
		for label, sub := range cl.SubLockers {
			if sub == nil {
				delete(cl.SubLockers, label)
				continue
			}
			if sub.Label == "" {
				sub.Label = label
			}
			relink(sub, cl)
		}
	}
	for _, cl := range lockers {
		// Entries without a label are not lockers. E.g. /registry/lockers also reports the lockers of each context,
		// as a map of lockers keyed by the context, which decodes as a locker without a label.
		if cl == nil || cl.Label == "" {
			continue
		}
		newLockers[cl.Label] = cl
		relink(cl, nil)
	}
	if newLockers[constants.LockerDefaultLabel] == nil {
		defaultLocker := NewCombiLocker(constants.LockerDefaultLabel, nil)
		defaultLocker.Current = false
		newLockers[constants.LockerDefaultLabel] = defaultLocker
		allLockers[constants.LockerDefaultLabel] = defaultLocker
	}
	if current == nil {
		current = newLockers[constants.LockerDefaultLabel]
		current.Open()
	}
	ll.lockers = newLockers
	ll.allLockers = allLockers
	ll.currentLocker = current
}

func (ll *LabeledLockers) GetLocker(label string) *CombiLocker {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	if strings.EqualFold(label, constants.LockerCurrent) {
		return ll.currentLocker
	}
	return ll.allLockers[label]
}

func (ll *LabeledLockers) GetOrCreateLocker(label string) *CombiLocker {
	ll.lock.Lock()
	defer ll.lock.Unlock()
	if strings.EqualFold(label, constants.LockerCurrent) {
		return ll.currentLocker
	}
	locker, _ := ll.unsafeCreateOrGetLocker(label)
	return locker
}

func (ll *LabeledLockers) GetLockerLabels() map[string]interface{} {
	ll.lock.RLock()
	defer ll.lock.RUnlock()
	labels := map[string]interface{}{}
	for _, cl := range ll.lockers {
		for label, subLabels := range cl.GetLabels() {
			labels[label] = subLabels
		}
	}
	return labels
}

func (ll *LabeledLockers) getLockers(locker string, topOnly bool) CombiLockers {
	ll.lock.RLock()
	defer ll.lock.RUnlock()
	lockers := CombiLockers{}
	if locker == "" || strings.EqualFold(locker, constants.LockerAll) {
		var sourceLockers CombiLockers
		if topOnly {
			sourceLockers = ll.lockers
		} else {
			sourceLockers = ll.allLockers
		}
		for lname, l := range sourceLockers {
			lockers[lname] = l
		}
	} else if strings.EqualFold(locker, constants.LockerCurrent) {
		lockers[ll.currentLocker.Label] = ll.currentLocker
	} else if ll.allLockers[locker] != nil {
		lockers[locker] = ll.allLockers[locker]
	}
	return lockers
}

func isAllLockers(label string) bool {
	return label == "" || strings.EqualFold(label, constants.LockerAll)
}

func (ll *LabeledLockers) getMatchingOrTopLockers(locker string) CombiLockers {
	return ll.getLockers(locker, true)
}

func (ll *LabeledLockers) getMatchingOrAllLockers(locker string) CombiLockers {
	return ll.getLockers(locker, false)
}

func (ll *LabeledLockers) GetDataLockerPaths(locker string, pathURIs bool) map[string]map[string]interface{} {
	lockers := ll.getMatchingOrTopLockers(locker)
	lockerPathsByLabels := map[string]map[string]interface{}{}
	pathPrefix := ""
	if pathURIs {
		pathPrefix = "/registry/lockers/"
	}
	for label, cl := range lockers {
		lockerPathsByLabels[label] = cl.SearchOrGetDataLockerPaths(pathPrefix, nil, true)
	}
	return lockerPathsByLabels
}

func (ll *LabeledLockers) SearchInDataLockers(locker string, key string) map[string]map[string]interface{} {
	lockersToSearch := ll.getMatchingOrTopLockers(locker)
	dataPaths := map[string]map[string]interface{}{}
	pattern := regexp.MustCompile("(?i)" + key)
	pathPrefix := "/registry/lockers/"
	for label, cl := range lockersToSearch {
		dataPaths[label] = cl.SearchOrGetDataLockerPaths(pathPrefix, pattern, true)
	}
	return dataPaths
}

func (ll *LabeledLockers) GetPeerEvents(locker string, peerNames []string, unified, reverse, data bool) map[string][]*events.Event {
	lockers := ll.getMatchingOrAllLockers(locker)
	eventsMap := map[string][]*events.Event{}
	for _, l := range lockers {
		lockerEvents := l.GetPeerEvents(peerNames, unified, reverse, data)
		for peer, peerEvents := range lockerEvents {
			if unified {
				peer = locker
				if peer == "" {
					peer = "all"
				}
			}
			for _, event := range peerEvents {
				eventsMap[peer] = append(eventsMap[peer], event)
			}
		}
	}
	sortPeerEvents(eventsMap, reverse)
	return eventsMap
}

func (ll *LabeledLockers) SearchInPeerEvents(locker string, peerNames []string, key string, unified, reverse, data bool) map[string][]*events.Event {
	lockersToSearch := ll.getMatchingOrAllLockers(locker)
	eventsMap := map[string][]*events.Event{}
	pattern := regexp.MustCompile("(?i)" + key)
	for _, l := range lockersToSearch {
		lockerEvents := l.SearchInPeerEvents(peerNames, pattern, unified, reverse, data)
		for peer, peerEvents := range lockerEvents {
			if unified {
				peer = locker
				if peer == "" {
					peer = "all"
				}
			}
			for _, event := range peerEvents {
				eventsMap[peer] = append(eventsMap[peer], event)
			}
		}
	}
	sortPeerEvents(eventsMap, reverse)
	return eventsMap
}

func (ll *LabeledLockers) GetCurrentLocker() *CombiLocker {
	ll.lock.RLock()
	defer ll.lock.RUnlock()
	return ll.currentLocker
}

func (ll *LabeledLockers) GetPeerLockers(peerName string) PeerMultiLockers {
	result := PeerMultiLockers{}
	ll.lock.RLock()
	defer ll.lock.RUnlock()
	for _, cl := range ll.lockers {
		if cl != nil {
			for peer, pls := range cl.getPeerLockers(peerName) {
				if result[peer] == nil {
					result[peer] = map[string]*PeerLocker{}
				}
				for label, pl := range pls {
					if pl != nil {
						result[peer][label] = pl
					}
				}
			}
		}
	}
	return result
}

func (ll *LabeledLockers) GetAllLockers(peers, events, data bool, level int) CombiLockers {
	sourceLockers := ll.getMatchingOrTopLockers("")
	lockers := CombiLockers{}
	if peers && events && data {
		for label, cl := range sourceLockers {
			if cl != nil {
				if level > 0 {
					lockers[label] = cl.Trim(level)
				} else {
					lockers[label] = cl
				}
			}
		}
	} else {
		for label, cl := range sourceLockers {
			if cl != nil {
				if level > 0 {
					cl = cl.Trim(level)
				}
				if !data {
					cl = cl.GetLockerView(events, data)
				}
				if !peers {
					cl = cl.GetLockerWithoutPeers()
				}
				if !events {
					cl = cl.GetLockerWithoutEvents()
				}
				lockers[label] = cl
			}
		}
	}
	return lockers
}

func (ll *LabeledLockers) GetDataLockers(label string) DataLockers {
	sourceLockers := ll.getMatchingOrTopLockers(label)
	dataLockers := map[string]*DataLocker{}
	if !isAllLockers(label) {
		// A specific (or "current") locker: sourceLockers holds just that locker, so don't index it by the label
		// as given, which wouldn't match for "current".
		for lockerLabel, cl := range sourceLockers {
			if cl != nil {
				dataLockers[lockerLabel] = cl.DataLocker
			}
		}
	} else {
		for _, cl := range sourceLockers {
			if cl != nil {
				for pathLabel, dl := range cl.GetDataLockers() {
					dataLockers[pathLabel] = dl
				}
			}
		}
	}
	return dataLockers
}

func (ll *LabeledLockers) GetDataLockersView(label string) map[string]*DataLocker {
	sourceLockers := ll.getMatchingOrTopLockers(label)
	view := map[string]*DataLocker{}
	if !isAllLockers(label) {
		for lockerLabel, cl := range sourceLockers {
			if cl != nil {
				view[lockerLabel] = cl.GetDataLockerView()
			}
		}
	} else {
		for label, cl := range sourceLockers {
			if cl != nil {
				view[label] = cl.GetDataLockerView()
			}
		}
	}
	return view
}

func (ll *LabeledLockers) Get(label string, keys []string, data bool, level int) (interface{}, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	lockers := ll.getMatchingOrTopLockers(label)
	result := map[string]interface{}{}
	if len(lockers) == 1 && !isAllLockers(label) {
		// The single matching locker is not necessarily keyed by the label as given (e.g. "current").
		for _, cl := range lockers {
			if cl == nil {
				return nil, false
			}
			if level > 0 {
				cl = cl.Trim(level)
			}
			if directsubResult, present, dataAtKey := cl.Get(keys, data, level); present {
				return directsubResult, dataAtKey
			}
			for subLabel, subResult := range cl.GetFromSubLockers(keys, data, level) {
				if result[subLabel] == nil {
					result[subLabel] = subResult
				}
			}
			return result, false
		}
	}
	for _, cl := range lockers {
		if cl != nil {
			if subResult, present, _ := cl.Get(keys, data, level); present {
				result[cl.PathLabel] = subResult
			}
			for subLabel, subResult := range cl.GetFromSubLockers(keys, data, level) {
				if result[subLabel] == nil {
					result[subLabel] = subResult
				}
			}
		}
	}
	return result, false
}

func (ll *LabeledLockers) GetFromPeerInstanceLocker(label, peerName, peerLabel string, keys []string, data bool, level int) (interface{}, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	lockers := ll.getMatchingOrAllLockers(label)
	if len(lockers) == 1 && !isAllLockers(label) {
		for _, cl := range lockers {
			if cl == nil {
				return nil, false
			}
			if level > 0 {
				cl = cl.Trim(level)
			}
			result, _, dataAtKey := cl.GetFromPeerInstanceLocker(peerName, peerLabel, keys, data, level)
			return result, dataAtKey
		}
	}
	result := map[string]interface{}{}
	for _, cl := range lockers {
		if cl != nil {
			if data, present, _ := cl.GetFromPeerInstanceLocker(peerName, peerLabel, keys, data, level); present {
				result[cl.PathLabel] = data
			}
		}
	}
	return result, false
}

func NewContextLockers() *ContextLockers {
	return &ContextLockers{labeledLockers: map[string]*LabeledLockers{}}
}

func (cl *ContextLockers) Init() {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	cl.labeledLockers = map[string]*LabeledLockers{}
}

func (cl *ContextLockers) GetContextLocker(context string) *LabeledLockers {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	if cl.labeledLockers[context] == nil {
		cl.labeledLockers[context] = NewLabeledLockers()
	}
	return cl.labeledLockers[context]
}

func (cl *ContextLockers) ClearContextLocker(context string) *LabeledLockers {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	cl.labeledLockers[context] = NewLabeledLockers()
	return cl.labeledLockers[context]
}

func (cl *ContextLockers) GetAllLockers(peers, events, data bool, level int) map[string]CombiLockers {
	result := map[string]CombiLockers{}
	for context, ll := range cl.labeledLockers {
		sourceLockers := ll.getMatchingOrTopLockers("")
		lockers := CombiLockers{}
		if peers && events && data {
			for label, cl := range sourceLockers {
				if cl != nil {
					if level > 0 {
						lockers[label] = cl.Trim(level)
					} else {
						lockers[label] = cl
					}
				}
			}
		} else {
			for label, cl := range sourceLockers {
				if cl != nil {
					if level > 0 {
						cl = cl.Trim(level)
					}
					if !data {
						cl = cl.GetLockerView(events, data)
					}
					if !peers {
						cl = cl.GetLockerWithoutPeers()
					}
					if !events {
						cl = cl.GetLockerWithoutEvents()
					}
					lockers[label] = cl
				}
			}
		}
		result[context] = lockers
	}
	return result
}
