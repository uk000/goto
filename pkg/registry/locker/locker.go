package locker

import (
  "goto/pkg/client/results"
  "goto/pkg/constants"
  "goto/pkg/util"
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
  InstanceLockers map[string]*DataLocker `json:"instanceLockers"`
  Locker          *DataLocker            `json:"locker"`
  lock            sync.RWMutex
}

type CombiLocker struct {
  Label       string                 `json:"label"`
  PeerLockers map[string]*PeerLocker `json:"peerLockers"`
  DataLocker  *DataLocker            `json:"dataLocker"`
  Current     bool                   `json:"current"`
  lock        sync.RWMutex
}

type LabeledLockers struct {
  lockers       map[string]*CombiLocker
  currentLocker *CombiLocker
  lock          sync.RWMutex
}

func createOrGetLockerData(locker map[string]*LockerData, key string, now time.Time) *LockerData {
  lockerData := locker[key]
  if lockerData == nil {
    lockerData = &LockerData{SubKeys: map[string]*LockerData{}, FirstReported: now}
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
  for i := 1; i < len(keys); i++ {
    lockerData.SubKeys[keys[i]] = createOrGetLockerData(lockerData.SubKeys, keys[i], now)
    lockerData = lockerData.SubKeys[keys[i]]
  }
  lockerData.Data = value
  lockerData.LastReported = now
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

func unsafeReadKeys(locker map[string]*LockerData, keys []string) (interface{}, bool) {
  if len(keys) > 0 && locker[keys[0]] != nil {
    lockerData := locker[keys[0]]
    for i := 1; i < len(keys); i++ {
      if lockerData.SubKeys[keys[i]] != nil {
        lockerData = lockerData.SubKeys[keys[i]]
      }
    }
    if lockerData != nil {
      if lockerData.Data != "" {
        return lockerData.Data, true
      } else {
        return lockerData, false
      }
    }
  }
  return "", false
}

func unsafeGetPaths(locker map[string]*LockerData) [][]string {
  paths := [][]string{}
  if locker != nil {
    for key, ld := range locker {
      if ld != nil {
        subKeys := unsafeGetPaths(ld.SubKeys)
        currentPaths := [][]string{}
        if len(subKeys) > 0 {
          for _, sub := range subKeys {
            currentSubKeys := []string{key}
            currentSubKeys = append(currentSubKeys, sub...)
            currentPaths = append(currentPaths, currentSubKeys)
          }
        }
        if ld.Data != "" {
          currentPaths = append(currentPaths, []string{key})
        }
        paths = append(paths, currentPaths...)
      }
    }
  }
  return paths
}

func unsafeFindKey(locker map[string]*LockerData, keyToFind string) []string {
  results := []string{}
  if locker != nil {
    lower := strings.ToLower(keyToFind)
    for key, ld := range locker {
      if strings.Contains(strings.ToLower(key), lower) {
        results = append(results, key)
      }
      if ld != nil {
        subPaths := unsafeFindKey(ld.SubKeys, keyToFind)
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
        ldView := createOrGetLockerData(lockerView, key, ld.FirstReported)
        if ld.Data != "" {
          ldView.Data = "..."
        }
        ldView.LastReported = ld.LastReported
        unsafeGetLockerView(ld.SubKeys, ldView.SubKeys)
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

func (dl *DataLocker) Get(keys []string) (interface{}, bool) {
  dl.lock.RLock()
  defer dl.lock.RUnlock()
  return unsafeReadKeys(dl.Locker, keys)
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
  pl.Locker = newDataLocker()
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
  dl := pl.Locker
  pl.lock.Unlock()
  dl.Store(keys, value)
}

func (pl *PeerLocker) Remove(keys []string) {
  pl.lock.Lock()
  dl := pl.Locker
  pl.lock.Unlock()
  dl.Remove(keys)
}

func (pl *PeerLocker) GetLockerView() *PeerLocker {
  pl.lock.RLock()
  defer pl.lock.RUnlock()
  plView := newPeerLocker()
  unsafeGetLockerView(pl.Locker.Locker, plView.Locker.Locker)
  plView.Locker.Active = pl.Locker.Active
  for address, il := range pl.InstanceLockers {
    ilView := plView.createOrGetInstanceLocker(address)
    unsafeGetLockerView(il.Locker, ilView.Locker)
    ilView.Active = il.Active
  }
  return plView
}

func NewCombiLocker(label string) *CombiLocker {
  cl := &CombiLocker{Label: label}
  cl.Init()
  return cl
}

func (cl *CombiLocker) Init() {
  cl.lock.Lock()
  defer cl.lock.Unlock()
  cl.PeerLockers = map[string]*PeerLocker{}
  cl.DataLocker = newDataLocker()
}

func (cl *CombiLocker) InitPeerLocker(peerName string, peerAddress string) bool {
  if peerName != "" {
    cl.lock.Lock()
    if peerAddress == "" || cl.PeerLockers[peerName] == nil {
      cl.PeerLockers[peerName] = newPeerLocker()
    } else {
      cl.PeerLockers[peerName].clearInstanceLocker(peerAddress)
    }
    cl.lock.Unlock()
  } else {
    cl.PeerLockers = map[string]*PeerLocker{}
  }
  return true
}

func (cl *CombiLocker) createOrGetPeerLocker(peerName string) *PeerLocker {
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

func (cl *CombiLocker) Get(keys []string) (interface{}, bool) {
  if len(keys) == 0 {
    return nil, false
  }
  cl.lock.Lock()
  dl := cl.DataLocker
  cl.lock.Unlock()
  result, dataAtKey := dl.Get(keys)
  if dataAtKey {
    return result, dataAtKey
  }
  return map[string]interface{}{strings.Join(keys, ","): result}, dataAtKey
}

func (cl *CombiLocker) ClearInstanceLocker(peerName, peerAddress string) bool {
  cl.lock.Lock()
  peerLocker := cl.PeerLockers[peerName]
  cl.lock.Unlock()
  if peerLocker != nil {
    return peerLocker.clearInstanceLocker(peerAddress)
  }
  return false
}

func (cl *CombiLocker) RemovePeerLocker(peerName string) {
  cl.lock.Lock()
  defer cl.lock.Unlock()
  delete(cl.PeerLockers, peerName)
}

func (cl *CombiLocker) GetFromPeerInstanceLocker(peerName, peerAddress string, keys []string) (interface{}, bool) {
  if len(keys) == 0 || peerName == "" || peerAddress == "" {
    return nil, false
  }
  cl.lock.RLock()
  pl := cl.PeerLockers[peerName]
  cl.lock.RUnlock()
  if pl == nil {
    return nil, false
  }
  pl.lock.RLock()
  il := pl.InstanceLockers[peerAddress]
  pl.lock.RUnlock()
  if il == nil {
    return nil, false
  }
  result, dataAtKey := il.Get(keys)
  if dataAtKey {
    return result, dataAtKey
  }
  return map[string]interface{}{strings.Join(keys, ","): result}, false
}

func (cl *CombiLocker) GetPeerOrAllLockers(peerName, peerAddress string) interface{} {
  if peerName == "" {
    return cl
  }
  peerLocker := cl.createOrGetPeerLocker(peerName)
  if peerAddress == "" {
    return peerLocker
  }
  if peerLocker != nil {
    return peerLocker.createOrGetInstanceLocker(peerAddress)
  }
  return nil
}

func (cl *CombiLocker) GetPeerOrAllLockersView(peerName, peerAddress string) interface{} {
  if peerName == "" {
    return cl.GetLockerView()
  }
  cl.lock.RLock()
  pl := cl.PeerLockers[peerName]
  cl.lock.RUnlock()
  if pl == nil {
    return nil
  }
  plView := pl.GetLockerView()
  if peerAddress == "" {
    return plView
  }
  return plView.createOrGetInstanceLocker(peerAddress)
}

func (cl *CombiLocker) GetLockerView() *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label)
  combiView.PeerLockers = map[string]*PeerLocker{}
  for peer, pl := range cl.PeerLockers {
    combiView.PeerLockers[peer] = pl.GetLockerView()
  }
  combiView.Current = cl.Current
  combiView.DataLocker.Active = cl.DataLocker.Active
  unsafeGetLockerView(cl.DataLocker.Locker, combiView.DataLocker.Locker)
  return combiView
}

func (cl *CombiLocker) GetTargetsResults(trackingHeaders []string, crossTrackingHeaders map[string][]string) map[string]map[string]*results.TargetResults {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  summary := map[string]map[string]*results.TargetResults{}
  for peer, peerLocker := range cl.PeerLockers {
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

func (cl *CombiLocker) GetTargetsSummaryResults(trackingHeaders []string, crossTrackingHeaders map[string][]string) map[string]*results.TargetsSummaryResults {
  clientResultsSummary := cl.GetTargetsResults(trackingHeaders, crossTrackingHeaders)
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

func NewLabeledPeersLockers() *LabeledLockers {
  ll := &LabeledLockers{}
  ll.Init()
  return ll
}

func (ll *LabeledLockers) Init() {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  ll.lockers = map[string]*CombiLocker{}
  ll.lockers[constants.LockerDefaultLabel] = NewCombiLocker(constants.LockerDefaultLabel)
  ll.currentLocker = ll.lockers[constants.LockerDefaultLabel]
  ll.currentLocker.Current = true
}

func (ll *LabeledLockers) OpenLocker(label string) {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  if ll.currentLocker != nil {
    ll.currentLocker.lock.Lock()
    ll.currentLocker.Current = false
    ll.currentLocker.lock.Unlock()
  }
  if ll.lockers[label] == nil {
    ll.lockers[label] = NewCombiLocker(label)
  }
  ll.currentLocker = ll.lockers[label]
  ll.currentLocker.Current = true
}

func (ll *LabeledLockers) CloseLocker(label string) {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  locker := ll.lockers[label]
  if locker != nil {
    delete(ll.lockers, label)
    if locker == ll.currentLocker {
      ll.currentLocker = ll.lockers[constants.LockerDefaultLabel]
      ll.currentLocker.Current = true
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
  if ll.lockers[label] == nil {
    ll.lockers[label] = NewCombiLocker(label)
  }
  return ll.lockers[label]
}

func (ll *LabeledLockers) GetLockerLabels() []string {
  ll.lock.RLock()
  defer ll.lock.RUnlock()
  labels := []string{}
  for label := range ll.lockers {
    labels = append(labels, label)
  }
  sort.Strings(labels)
  return labels
}

func (ll *LabeledLockers) GetDataLockerPaths(locker string, pathURIs bool) interface{} {
  ll.lock.RLock()
  defer ll.lock.RUnlock()
  lockerPathsByLabels := map[string][][]string{}
  if locker != "" {
    if strings.EqualFold(locker, constants.LockerCurrent) {
      lockerPathsByLabels[locker] = unsafeGetPaths(ll.currentLocker.DataLocker.Locker)
    } else if ll.lockers[locker] != nil {
      lockerPathsByLabels[locker] = unsafeGetPaths(ll.lockers[locker].DataLocker.Locker)
    }
  } else {
    for label, cl := range ll.lockers {
      if cl.DataLocker != nil && cl.DataLocker.Locker != nil && len(cl.DataLocker.Locker) > 0 {
        lockerPathsByLabels[label] = unsafeGetPaths(cl.DataLocker.Locker)
      }
    }
  }
  if !pathURIs {
    return lockerPathsByLabels
  }
  dataPathURIs := map[string][]string{}
  for label, dataPaths := range lockerPathsByLabels {
    for _, pathKeys := range dataPaths {
      if len(pathKeys) > 0 {
        dataPathURIs[label] = append(dataPathURIs[label], "/registry/lockers/"+label+"/get/"+strings.Join(pathKeys, ","))
      }
    }
  }
  return dataPathURIs
}

func (ll *LabeledLockers) findInLockers(lockers map[string]*CombiLocker, key string) []string {
  ll.lock.RLock()
  defer ll.lock.RUnlock()
  keyPaths := []string{}
  for label, cl := range lockers {
    if cl.DataLocker != nil && cl.DataLocker.Locker != nil && len(cl.DataLocker.Locker) > 0 {
      subPaths := unsafeFindKey(cl.DataLocker.Locker, key)
      for i, dataPath := range subPaths {
        if dataPath != "" {
          subPaths[i] = "/registry/lockers/"+label+"/get/"+dataPath
        }
      }
    }
  }
  sort.Strings(keyPaths)
  return keyPaths
}

func (ll *LabeledLockers) FindInDataLockers(locker string, key string) []string {
  ll.lock.RLock()
  lockersToSearch := map[string]*CombiLocker{}
  if locker != "" {
    if strings.EqualFold(locker, constants.LockerCurrent) {
      lockersToSearch[ll.currentLocker.Label] = ll.currentLocker
    } else if ll.lockers[locker] != nil {
      lockersToSearch[locker] = ll.lockers[locker]
    }
  } else {
    lockersToSearch = ll.lockers
  }
  ll.lock.RUnlock()
  return ll.findInLockers(lockersToSearch, key)
}

func (ll *LabeledLockers) GetCurrentLocker() *CombiLocker {
  return ll.currentLocker
}

func (ll *LabeledLockers) GetAllLockers() map[string]*CombiLocker {
  return ll.lockers
}

func (ll *LabeledLockers) GetAllLockersView() map[string]*CombiLocker {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  view := map[string]*CombiLocker{}
  for label, cl := range ll.lockers {
    if cl != nil {
      view[label] = cl.GetLockerView()
    }
  }
  return view
}
