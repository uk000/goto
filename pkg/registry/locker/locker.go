package locker

import (
  "fmt"
  "goto/pkg/client/results"
  "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/util"
  "regexp"
  "sort"
  "strings"
  "sync"
  "time"
)

type LockerData struct {
  Data          string                 `json:"data"`
  SubKeys       map[string]*LockerData `json:"subKeys"`
  FirstReported time.Time              `json:"firstReported"`
  LastReported  time.Time              `json:"lastReported"`
}

type DataLocker struct {
  Locker map[string]*LockerData `json:"locker"`
  Active bool                   `json:"active"`
  lock   sync.RWMutex
}

type PeerLocker struct {
  DataLocker
  InstanceLockers map[string]*DataLocker `json:"instanceLockers"`
  lock            sync.RWMutex
}

type CombiLocker struct {
  Label        string                  `json:"label"`
  PathLabel    string                  `json:"pathLabel"`
  PeerLockers  map[string]*PeerLocker  `json:"peerLockers"`
  DataLocker   *DataLocker             `json:"dataLocker"`
  ChildLockers map[string]*CombiLocker `json:"childLockers"`
  Current      bool                    `json:"current"`
  parent       *CombiLocker
  lock         sync.RWMutex
}

type LabeledLockers struct {
  lockers       map[string]*CombiLocker
  currentLocker *CombiLocker
  lock          sync.RWMutex
}

func newLockerData(now time.Time) *LockerData {
  return &LockerData{SubKeys: map[string]*LockerData{}, FirstReported: now}
}

func createOrGetLockerData(locker map[string]*LockerData, key string, now time.Time) *LockerData {
  lockerData := locker[key]
  if lockerData == nil {
    lockerData = newLockerData(now)
    locker[key] = lockerData
  }
  return lockerData
}

func unsafeStoreKeysInLocker(locker map[string]*LockerData, keys []string, value string) {
  if len(keys) == 0 {
    return
  }
  rootKey := keys[0]
  now := time.Now()
  lockerData := createOrGetLockerData(locker, rootKey, now)
  lockerData.LastReported = now
  for i := 1; i < len(keys); i++ {
    lockerData.SubKeys[keys[i]] = createOrGetLockerData(lockerData.SubKeys, keys[i], now)
    lockerData = lockerData.SubKeys[keys[i]]
    lockerData.LastReported = now
  }
  lockerData.Data = value
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

func unsafeRemoveKeysFromLocker(locker map[string]*LockerData, keys []string) {
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

func unsafeReadKeys(locker map[string]*LockerData, keys []string, data bool, level int) (interface{}, bool, bool) {
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
      if lockerData.Data != "" {
        return lockerData.Data, true, true
      } else if !data || level > 0 {
        ldView := newLockerData(lockerData.FirstReported)
        ldView.LastReported = lockerData.LastReported
        if level > 0 {
          unsafeTrimLocker(lockerData.SubKeys, ldView.SubKeys, level)
        }
        if !data {
          unsafeGetLockerView(lockerData.SubKeys, ldView.SubKeys)
        }
        return ldView, true, false
      } else {
        return lockerData, true, false
      }
    }
  }
  return "", false, false
}

func unsafeGetPaths(locker map[string]*LockerData) []string {
  paths := []string{}
  if locker != nil {
    for key, ld := range locker {
      if ld != nil {
        subKeys := unsafeGetPaths(ld.SubKeys)
        currentPaths := []string{}
        if len(subKeys) > 0 {
          for _, sub := range subKeys {
            currentPaths = append(currentPaths, key+","+sub)
          }
        }
        if ld.Data != "" {
          currentPaths = append(currentPaths, key)
        }
        paths = append(paths, currentPaths...)
      }
    }
  }
  return paths
}

func unsafeSearchKey(locker map[string]*LockerData, pattern *regexp.Regexp) []string {
  results := []string{}
  if locker != nil {
    for key, ld := range locker {
      if pattern.MatchString(key) {
        results = append(results, key)
      }
      if ld != nil {
        subPaths := unsafeSearchKey(ld.SubKeys, pattern)
        for _, subPath := range subPaths {
          if subPath != "" {
            results = append(results, key+","+subPath)
          }
        }
      }
    }
  }
  return results
}

func unsafeGetLockerView(locker map[string]*LockerData, lockerView map[string]*LockerData) {
  if locker != nil && lockerView != nil {
    for key, ld := range locker {
      if ld != nil {
        ldView := newLockerData(ld.FirstReported)
        if ld.Data != "" {
          ldView.Data = "..."
        }
        ldView.LastReported = ld.LastReported
        unsafeGetLockerView(ld.SubKeys, ldView.SubKeys)
        lockerView[key] = ldView
      } else {
        lockerView[key] = nil
      }
    }
  }
}

func unsafeTrimLocker(locker map[string]*LockerData, lockerView map[string]*LockerData, level int) {
  if locker != nil && lockerView != nil && level > 0 {
    for key, ld := range locker {
      if ld != nil {
        ldView := newLockerData(ld.FirstReported)
        if ld.Data != "" {
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

func (dl *DataLocker) Store(keys []string, value string) {
  if len(keys) == 0 {
    return
  }
  dl.lock.Lock()
  defer dl.lock.Unlock()
  unsafeStoreKeysInLocker(dl.Locker, keys, value)
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
  pl.InstanceLockers = map[string]*DataLocker{}
  pl.DataLocker.init()
}

func (pl *PeerLocker) createOrGetInstanceLocker(peerAddress string) *DataLocker {
  pl.lock.RLock()
  il := pl.InstanceLockers[peerAddress]
  pl.lock.RUnlock()
  if il == nil {
    pl.lock.Lock()
    il = newDataLocker()
    pl.InstanceLockers[peerAddress] = il
    pl.lock.Unlock()
  }
  return il
}

func (pl *PeerLocker) clearInstanceLocker(peerAddress string) bool {
  pl.lock.RLock()
  _, present := pl.InstanceLockers[peerAddress]
  pl.lock.RUnlock()
  if present {
    pl.lock.Lock()
    pl.InstanceLockers[peerAddress] = newDataLocker()
    pl.lock.Unlock()
  }
  return present
}

func (pl *PeerLocker) removeInstanceLocker(peerAddress string) {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  delete(pl.InstanceLockers, peerAddress)
}

func (pl *PeerLocker) Store(keys []string, value string) {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  pl.DataLocker.Store(keys, value)
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

func (pl *PeerLocker) GetLockerView(events bool) *PeerLocker {
  pl.lock.RLock()
  defer pl.lock.RUnlock()
  plView := newPeerLocker()
  plView.Active = pl.Active
  unsafeGetLockerView(pl.Locker, plView.Locker)
  if !events {
    delete(plView.Locker, constants.LockerEventsKey)
  }
  for address, il := range pl.InstanceLockers {
    ilView := newDataLocker()
    plView.InstanceLockers[address] = ilView
    unsafeGetLockerView(il.Locker, ilView.Locker)
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
  for address, il := range pl.InstanceLockers {
    ilView := newDataLocker()
    plView.InstanceLockers[address] = ilView
    unsafeTrimLocker(il.Locker, ilView.Locker, level)
    ilView.Active = il.Active
  }
  return plView
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

func (cl *CombiLocker) Init() {
  cl.lock.Lock()
  defer cl.lock.Unlock()
  cl.PeerLockers = map[string]*PeerLocker{}
  cl.DataLocker = newDataLocker()
  cl.ChildLockers = map[string]*CombiLocker{}
}

func (cl *CombiLocker) GetLabels() map[string]interface{} {
  cl.lock.Lock()
  defer cl.lock.Unlock()
  labels := map[string]interface{}{}
  childLabels := map[string]interface{}{}
  for _, child := range cl.ChildLockers {
    for k, v := range child.GetLabels() {
      childLabels[k] = v
    }
  }
  labels[cl.Label] = childLabels
  return labels
}

func (cl *CombiLocker) GetChildLocker(labels []string) *CombiLocker {
  if len(labels) == 0 {
    return nil
  }
  cl.lock.Lock()
  defer cl.lock.Unlock()
  child := cl.ChildLockers[labels[0]]
  if child != nil && len(labels) > 1 {
    child = child.GetChildLocker(labels[1:])
  }
  return child
}

func (cl *CombiLocker) InitPeerLocker(peerName string, peerAddress string) bool {
  if peerName != "" {
    peerName = strings.ToLower(peerName)
    cl.lock.Lock()
    if peerAddress == "" || cl.PeerLockers[peerName] == nil {
      cl.PeerLockers[peerName] = newPeerLocker()
    }
    if peerAddress != "" {
      cl.PeerLockers[peerName].clearInstanceLocker(peerAddress)
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

func (cl *CombiLocker) createOrGetInstanceLocker(peerName string, peerAddress string) *DataLocker {
  return cl.createOrGetPeerLocker(peerName).createOrGetInstanceLocker(peerAddress)
}

func (cl *CombiLocker) DeactivateInstanceLocker(peerName string, peerAddress string) {
  cl.createOrGetPeerLocker(peerName).createOrGetInstanceLocker(peerAddress).Deactivate()
}

func (cl *CombiLocker) StorePeerData(peerName, peerAddress string, keys []string, value string) {
  if len(keys) == 0 {
    return
  }
  if peerAddress == "" {
    cl.createOrGetPeerLocker(peerName).Store(keys, value)
  } else {
    cl.createOrGetInstanceLocker(peerName, peerAddress).Store(keys, value)
  }
}

func (cl *CombiLocker) RemovePeerData(peerName, peerAddress string, keys []string) {
  if len(keys) == 0 {
    return
  }
  if peerAddress == "" {
    cl.createOrGetPeerLocker(peerName).Remove(keys)
  } else {
    cl.createOrGetInstanceLocker(peerName, peerAddress).Remove(keys)
  }
}

func (cl *CombiLocker) Store(keys []string, value string) {
  cl.lock.Lock()
  dl := cl.DataLocker
  cl.lock.Unlock()
  dl.Store(keys, value)
}

func (cl *CombiLocker) Remove(keys []string) {
  cl.lock.Lock()
  dl := cl.DataLocker
  cl.lock.Unlock()
  dl.Remove(keys)
}

func (cl *CombiLocker) GetFromChildLockers(keys []string, data bool, level int) map[string]interface{} {
  if len(keys) == 0 {
    return nil
  }
  result := map[string]interface{}{}
  for _, child := range cl.ChildLockers {
    if childData, present, _ := child.Get(keys, data, level); present {
      result[child.PathLabel] = childData
    }
    for childPath, childData := range child.GetFromChildLockers(keys, data, level) {
      if childData != nil {
        result[childPath] = childData
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

func (cl *CombiLocker) ClearInstanceLocker(peerName, peerAddress string) bool {
  peerName = strings.ToLower(peerName)
  cl.lock.Lock()
  peerLocker := cl.PeerLockers[peerName]
  cl.lock.Unlock()
  if peerLocker != nil {
    return peerLocker.clearInstanceLocker(peerAddress)
  }
  return false
}

func (cl *CombiLocker) RemovePeerLocker(peerName string) {
  peerName = strings.ToLower(peerName)
  cl.lock.Lock()
  defer cl.lock.Unlock()
  delete(cl.PeerLockers, peerName)
}

func (cl *CombiLocker) GetFromPeerInstanceLocker(peerName, peerAddress string, keys []string, data bool, level int) (interface{}, bool, bool) {
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
  instanceLockers := map[string]*DataLocker{}
  pl.lock.RLock()
  if peerAddress != "" {
    if pl.InstanceLockers[peerAddress] != nil {
      instanceLockers[peerAddress] = pl.InstanceLockers[peerAddress]
    }
  } else {
    for address, il := range pl.InstanceLockers {
      instanceLockers[address] = il
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
  for address, il := range instanceLockers {
    instanceData, present, _ := il.Get(keys, true, 0)
    if present {
      result[address] = instanceData
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

func (cl *CombiLocker) GetPeerOrAllLockers(peerName, peerAddress string, events bool) interface{} {
  if peerName == "" {
    if events {
      return cl
    } else {
      return cl.GetLockerWithoutEvents()
    }
  }
  peerLocker := cl.createOrGetPeerLocker(peerName)
  if !events {
    peerLocker = peerLocker.GetLockerWithoutEvents()
  }
  if peerAddress == "" {
    return peerLocker
  }
  if peerLocker != nil {
    return peerLocker.createOrGetInstanceLocker(peerAddress)
  }
  return nil
}

func (cl *CombiLocker) GetPeerOrAllLockersView(peerName, peerAddress string, events bool) interface{} {
  if peerName == "" {
    return cl.GetLockerView(events)
  }
  peerName = strings.ToLower(peerName)
  cl.lock.RLock()
  pl := cl.PeerLockers[peerName]
  cl.lock.RUnlock()
  if pl == nil {
    return nil
  }
  plView := pl.GetLockerView(events)
  if peerAddress == "" {
    return plView
  }
  return plView.createOrGetInstanceLocker(peerAddress)
}

func (cl *CombiLocker) GetDataLockers() map[string]*DataLocker {
  cl.lock.Lock()
  defer cl.lock.Unlock()
  dataLockers := map[string]*DataLocker{}
  dataLockers[cl.PathLabel] = cl.DataLocker
  for _, child := range cl.ChildLockers {
    for pathLabel, childDL := range child.GetDataLockers() {
      dataLockers[pathLabel] = childDL
    }
  }
  return dataLockers
}

func (cl *CombiLocker) GetLockerView(events bool) *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label, cl.parent)
  for label, child := range cl.ChildLockers {
    combiView.ChildLockers[label] = child.GetLockerView(events)
  }
  for peer, pl := range cl.PeerLockers {
    combiView.PeerLockers[peer] = pl.GetLockerView(events)
  }
  combiView.Current = cl.Current
  combiView.DataLocker.Active = cl.DataLocker.Active
  unsafeGetLockerView(cl.DataLocker.Locker, combiView.DataLocker.Locker)
  return combiView
}

func (cl *CombiLocker) GetLockerWithoutPeers() *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label, cl.parent)
  for label, child := range cl.ChildLockers {
    combiView.ChildLockers[label] = child.GetLockerWithoutPeers()
  }
  combiView.Current = cl.Current
  combiView.DataLocker = cl.DataLocker
  return combiView
}

func (cl *CombiLocker) GetLockerWithoutEvents() *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label, cl.parent)
  for label, child := range cl.ChildLockers {
    combiView.ChildLockers[label] = child.GetLockerWithoutEvents()
  }
  for peer, pl := range cl.PeerLockers {
    combiView.PeerLockers[peer] = pl.GetLockerWithoutEvents()
  }
  combiView.Current = cl.Current
  combiView.DataLocker = cl.DataLocker
  return combiView
}

func (cl *CombiLocker) GetDataLockerView() *DataLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  dataView := newDataLocker()
  dataView.Active = cl.DataLocker.Active
  unsafeGetLockerView(cl.DataLocker.Locker, dataView.Locker)
  return dataView
}

func (cl *CombiLocker) Trim(level int) *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label, cl.parent)
  for label, child := range cl.ChildLockers {
    combiView.ChildLockers[label] = child.Trim(level)
  }
  for peer, pl := range cl.PeerLockers {
    combiView.PeerLockers[peer] = pl.Trim(level)
  }
  combiView.DataLocker = cl.DataLocker.Trim(level)
  combiView.DataLocker.Active = cl.DataLocker.Active
  combiView.Current = cl.Current
  return combiView
}

func (cl *CombiLocker) GetDataLockerPaths() map[string][]string {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  dataPaths := map[string][]string{}
  if cl.DataLocker != nil && cl.DataLocker.Locker != nil && len(cl.DataLocker.Locker) > 0 {
    dataPaths[cl.PathLabel] = unsafeGetPaths(cl.DataLocker.Locker)
    sort.Strings(dataPaths[cl.PathLabel])
  }
  for _, child := range cl.ChildLockers {
    for k, v := range child.GetDataLockerPaths() {
      dataPaths[k] = v
    }
  }
  return dataPaths
}

func (cl *CombiLocker) Search(pattern *regexp.Regexp) []string {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  dataPaths := []string{}
  if cl.DataLocker != nil && cl.DataLocker.Locker != nil && len(cl.DataLocker.Locker) > 0 {
    for _, dataPath := range unsafeSearchKey(cl.DataLocker.Locker, pattern) {
      if dataPath != "" {
        dataPaths = append(dataPaths, "/registry/lockers/"+cl.PathLabel+"/get/"+dataPath)
      }
    }
  }
  for _, child := range cl.ChildLockers {
    dataPaths = append(dataPaths, child.Search(pattern)...)
  }
  sort.Strings(dataPaths)
  return dataPaths
}

func (cl *CombiLocker) GetTargetsResults(peerName string, trackingHeaders []string, crossTrackingHeaders map[string][]string) map[string]map[string]*results.TargetResults {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  summary := map[string]map[string]*results.TargetResults{}
  peerLockers := map[string]*PeerLocker{}
  if peerName != "" {
    peerName = strings.ToLower(peerName)
    if cl.PeerLockers[peerName] != nil {
      peerLockers[peerName] = cl.PeerLockers[peerName]
    }
  } else {
    peerLockers = cl.PeerLockers
  }
  for peer, peerLocker := range peerLockers {
    summary[peer] = map[string]*results.TargetResults{}
    peerLocker.lock.RLock()
    for _, instanceLocker := range peerLocker.InstanceLockers {
      instanceLocker.lock.RLock()
      lockerData := instanceLocker.Locker[constants.LockerClientKey]
      if lockerData != nil {
        for target, targetData := range lockerData.SubKeys {
          if strings.EqualFold(target, constants.LockerInvocationsKey) {
            continue
          }
          if summary[peer][target] == nil {
            summary[peer][target] = &results.TargetResults{Target: target}
            summary[peer][target].Init(trackingHeaders, crossTrackingHeaders)
          }
          if data := targetData.Data; data != "" {
            result := &results.TargetResults{}
            if err := util.ReadJson(data, result); err == nil {
              results.AddDeltaResults(summary[peer][target], result)
            }
          }
        }
      }
      instanceLocker.lock.RUnlock()
    }
    peerLocker.lock.RUnlock()
  }
  return summary
}

func (cl *CombiLocker) GetTargetsSummaryResults(peerName string, trackingHeaders []string, crossTrackingHeaders map[string][]string) map[string]*results.TargetsSummaryResults {
  clientResultsSummary := cl.GetTargetsResults(peerName, trackingHeaders, crossTrackingHeaders)
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  result := map[string]*results.TargetsSummaryResults{}
  for peer, targetsResults := range clientResultsSummary {
    result[peer] = &results.TargetsSummaryResults{}
    result[peer].Init()
    for _, targetResult := range targetsResults {
      result[peer].AddTargetResult(targetResult)
    }
  }
  return result
}

func (cl *CombiLocker) getPeerLockers(peerNames []string) map[string]*PeerLocker {
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

func convertLockerDataToEvent(l *LockerData) *events.Event {
  event := &events.Event{}
  if err := util.ReadJson(l.Data, event); err == nil {
    return event
  } else {
    fmt.Printf("Error while parsing event: %s\n", err.Error())
  }
  return nil
}

func sortPeerEvents(eventsMap map[string][]*events.Event, reverse bool) {
  for _, peerEvents := range eventsMap {
    events.SortEvents(peerEvents, reverse)
  }
}

func (cl *CombiLocker) GetPeerEvents(peerNames []string, unified, reverse, data bool) map[string][]*events.Event {
  eventsMap := map[string][]*events.Event{}
  peerLockers := cl.getPeerLockers(peerNames)
  for peer, pl := range peerLockers {
    pl.lock.RLock()
    eventsLocker := pl.DataLocker.Locker[constants.LockerEventsKey]
    if eventsLocker != nil {
      for _, ld := range eventsLocker.SubKeys {
        if event := convertLockerDataToEvent(ld); event != nil {
          if unified {
            peer = "all"
          }
          if !data {
            event.Data = "..."
          }
          eventsMap[peer] = append(eventsMap[peer], event)
        }
      }
    }
    pl.lock.RUnlock()
  }
  sortPeerEvents(eventsMap, reverse)
  return eventsMap
}

func (cl *CombiLocker) SearchInPeerEvents(peerNames []string, pattern *regexp.Regexp, unified, reverse, data bool) map[string][]*events.Event {
  eventsMap := map[string][]*events.Event{}
  peerLockers := cl.getPeerLockers(peerNames)
  for peer, pl := range peerLockers {
    pl.lock.RLock()
    eventsLocker := pl.DataLocker.Locker[constants.LockerEventsKey]
    if eventsLocker != nil {
      for _, ld := range eventsLocker.SubKeys {
        if pattern.MatchString(ld.Data) {
          if event := convertLockerDataToEvent(ld); event != nil {
            if !data {
              event.Data = "..."
            }
            if unified {
              peer = "all"
            }
            eventsMap[peer] = append(eventsMap[peer], event)
          }
        }
      }
    }
    pl.lock.RUnlock()
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
    pl.DataLocker.Locker[constants.LockerEventsKey] = newLockerData(now)
    pl.lock.Unlock()
  }
}

func NewLabeledPeersLockers() *LabeledLockers {
  ll := &LabeledLockers{}
  ll.Init()
  return ll
}

func (ll *LabeledLockers) Init() {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  ll.lockers = map[string]*CombiLocker{}
  ll.lockers[constants.LockerDefaultLabel] = NewCombiLocker(constants.LockerDefaultLabel, nil)
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
  }
  pathLabel := l
  for i := 1; i < len(labels); i++ {
    parent = locker
    l := labels[i]
    pathLabel += "," + l
    if locker.ChildLockers[l] == nil {
      locker.ChildLockers[l] = NewCombiLocker(l, parent)
    }
    locker = locker.ChildLockers[l]
  }
  return locker, parent
}

func (ll *LabeledLockers) unsafeGetLocker(label string) (*CombiLocker, *CombiLocker) {
  labels := strings.Split(label, ",")
  l := labels[0]
  if l == "" || ll.lockers[l] == nil {
    return nil, nil
  }
  locker := ll.lockers[l]
  var parent *CombiLocker
  for i := 1; i < len(labels); i++ {
    l = labels[i]
    parent = locker
    locker = locker.ChildLockers[l]
    if locker == nil {
      break
    }
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
  ll.currentLocker.Current = true
}

func (ll *LabeledLockers) ClearLocker(label string, close bool) {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  locker, parent := ll.unsafeGetLocker(label)
  if locker != nil {
    if close {
      if parent == nil {
        delete(ll.lockers, locker.Label)
      } else {
        delete(parent.ChildLockers, locker.Label)
      }
      if locker == ll.currentLocker {
        ll.currentLocker = ll.lockers[constants.LockerDefaultLabel]
        ll.currentLocker.Current = true
      }
    } else {
      locker.Init()
    }
  }
}

func (ll *LabeledLockers) ReplaceLockers(lockers map[string]*CombiLocker) {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  ll.lockers = lockers
  for _, l := range lockers {
    if l.Current {
      ll.currentLocker = l
      break
    }
  }
}

func (ll *LabeledLockers) GetLocker(label string) *CombiLocker {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  if strings.EqualFold(label, constants.LockerCurrent) {
    return ll.currentLocker
  }
  locker, _ := ll.unsafeGetLocker(label)
  return locker
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
    for label, childLabels := range cl.GetLabels() {
      labels[label] = childLabels
    }
  }
  return labels
}

func (ll *LabeledLockers) getLockers(locker string) map[string]*CombiLocker {
  ll.lock.RLock()
  defer ll.lock.RUnlock()
  lockers := map[string]*CombiLocker{}
  if locker == "" || strings.EqualFold(locker, constants.LockerAll) {
    for lname, l := range ll.lockers {
      lockers[lname] = l
    }
  } else if strings.EqualFold(locker, constants.LockerCurrent) {
    lockers[ll.currentLocker.Label] = ll.currentLocker
  } else if ll.lockers[locker] != nil {
    lockers[locker] = ll.lockers[locker]
  } else {
    labels := strings.Split(locker, ",")
    if cl := ll.lockers[labels[0]]; cl != nil {
      for i := 1; i < len(labels); i++ {
        if cl = cl.ChildLockers[labels[i]]; cl == nil {
          break
        }
      }
      if cl != nil {
        lockers[locker] = cl
      }
    }
  }
  return lockers
}

func (ll *LabeledLockers) GetDataLockerPaths(locker string, pathURIs bool) interface{} {
  lockers := ll.getLockers(locker)
  lockerPathsByLabels := map[string]map[string][]string{}
  for label, cl := range lockers {
    lockerPathsByLabels[label] = cl.GetDataLockerPaths()
  }
  if !pathURIs {
    return lockerPathsByLabels
  }
  dataPathURIs := map[string][]string{}
  for label, dataPaths := range lockerPathsByLabels {
    for childLockerPath, childDataPaths := range dataPaths {
      for _, childDataPath := range childDataPaths {
        uri := fmt.Sprintf("/registry/lockers/%s/get/%s", childLockerPath, childDataPath)
        dataPathURIs[label] = append(dataPathURIs[label], uri)
      }
    }
    if len(dataPathURIs[label]) > 0 {
      sort.Strings(dataPathURIs[label])
    }
  }
  return dataPathURIs
}

func (ll *LabeledLockers) SearchInDataLockers(locker string, key string) []string {
  lockersToSearch := ll.getLockers(locker)
  dataPaths := []string{}
  pattern := regexp.MustCompile("(?i)" + key)
  for _, cl := range lockersToSearch {
    dataPaths = append(dataPaths, cl.Search(pattern)...)
  }
  sort.Strings(dataPaths)
  return dataPaths
}

func (ll *LabeledLockers) GetPeerEvents(locker string, peerNames []string, unified, reverse, data bool) map[string][]*events.Event {
  lockers := ll.getLockers(locker)
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
  lockersToSearch := ll.getLockers(locker)
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

func (ll *LabeledLockers) GetAllLockers(peers, events, data bool, level int) map[string]*CombiLocker {
  sourceLockers := ll.getLockers("")
  var lockers map[string]*CombiLocker
  if peers && events && data {
    for label, cl := range sourceLockers {
      if level > 0 {
        lockers[label] = cl.Trim(level)
      } else {
        lockers[label] = cl
      }
    }
  } else {
    lockers = map[string]*CombiLocker{}
    for label, cl := range sourceLockers {
      if cl != nil {
        if level > 0 {
          cl = cl.Trim(level)
        }
        if !data {
          cl = cl.GetLockerView(events)
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

func (ll *LabeledLockers) GetDataLockers(label string) map[string]*DataLocker {
  sourceLockers := ll.getLockers(label)
  dataLockers := map[string]*DataLocker{}
  if label != "" && len(sourceLockers) > 0 {
    dataLockers[label] = sourceLockers[label].DataLocker
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
  sourceLockers := ll.getLockers("")
  view := map[string]*DataLocker{}
  if label != "" && len(sourceLockers) > 0 {
    view[label] = sourceLockers[label].GetDataLockerView()
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
  lockers := ll.getLockers(label)
  result := map[string]interface{}{}
  if len(lockers) == 1 {
    if cl := lockers[label]; cl != nil {
      if level > 0 {
        cl = cl.Trim(level)
      }
      directChildResult, present, dataAtKey := cl.Get(keys, data, level)
      for childLabel, childResult := range cl.GetFromChildLockers(keys, data, level) {
        if result[childLabel] == nil {
          result[childLabel] = childResult
        }
      }
      if len(result) == 0 {
        return directChildResult, dataAtKey
      }
      if present {
        result[cl.PathLabel] = directChildResult
      }
      return result, false
    }
    return nil, false
  }
  for _, cl := range lockers {
    if cl != nil {
      if childResult, present, _ := cl.Get(keys, data, level); present {
        result[cl.PathLabel] = childResult
      }
      for childLabel, childResult := range cl.GetFromChildLockers(keys, data, level) {
        if result[childLabel] == nil {
          result[childLabel] = childResult
        }
      }
    }
  }
  return result, false
}

func (ll *LabeledLockers) GetFromPeerInstanceLocker(label, peerName, peerAddress string, keys []string, data bool, level int) (interface{}, bool) {
  if len(keys) == 0 {
    return nil, false
  }
  lockers := ll.getLockers(label)
  if len(lockers) == 1 {
    if cl := lockers[label]; cl != nil {
      if level > 0 {
        cl = cl.Trim(level)
      }
      result, _, dataAtKey := cl.GetFromPeerInstanceLocker(peerName, peerAddress, keys, data, level)
      return result, dataAtKey
    }
    return nil, false
  }
  result := map[string]interface{}{}
  for _, cl := range lockers {
    if cl != nil {
      if childResult, present, _ := cl.GetFromPeerInstanceLocker(peerName, peerAddress, keys, data, level); present {
        result[cl.PathLabel] = childResult
      }
    }
  }
  return result, false
}
