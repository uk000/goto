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
  Data          string    `json:"data,omitempty"`
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

func unsafeStoreKeysInLocker(locker Locker, keys []string, value string) {
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
      if lockerData.Data != "" {
        return lockerData.Data, true, true
      } else if !data || level > 0 {
        ldView := newLockerData(lockerData.FirstReported, lockerData.level)
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
          if ld.Data != "" {
            flatPaths = append(flatPaths, currentPathPrefix)
          }
          if subKeys, ok := subData.([]interface{}); ok && len(subKeys) > 0 {
            flatPaths = append(flatPaths, subKeys...)
          }
          paths = flatPaths
        } else {
          if subKeys, ok := subData.(map[string]interface{}); ok && len(subKeys) > 0 {
            if ld.Data != "" {
              subKeys["."] = currentPathPrefix
            }
            mapPaths[key] = subKeys
          } else if ld.Data != "" {
            mapPaths[key] = currentPathPrefix
          }
          paths = mapPaths
        }
      }
    }
  }
  return paths
}

func unsafeGetLockerView(locker, lockerView Locker) {
  if locker != nil && lockerView != nil {
    for key, ld := range locker {
      if ld != nil {
        ldView := newLockerData(ld.FirstReported, ld.level)
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

func unsafeTrimLocker(locker, lockerView Locker, level int) {
  if locker != nil && lockerView != nil && level > 0 {
    for key, ld := range locker {
      if ld != nil {
        ldView := newLockerData(ld.FirstReported, ld.level)
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

func (ld *LockerData) MarshalJSON() ([]byte, error) {
  data := map[string]interface{}{}
  if ld.level == 1 {
    data["firstReported"] = ld.FirstReported
    data["lastReported"] = ld.LastReported
  }
  if len(ld.SubKeys) > 0 {
    for k, v := range ld.SubKeys {
      data[k] = v
    }
  }
  if ld.Data != "" {
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
  instanceLockers := DataLockers{}
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

func (cl *CombiLocker) GetPeerLockers(peerName, peerAddress string, events bool) map[string]map[string]interface{} {
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
      if peerAddress == "" {
        lockers[label][peer] = pl
      } else {
        lockers[label][peer] = pl.createOrGetInstanceLocker(peerAddress)
      }
    }
  }
  return lockers
}

func (cl *CombiLocker) GetPeerLockersView(peerName, peerAddress string, events bool) map[string]map[string]interface{} {
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
      plView := pl.GetLockerView(events)
      if peerAddress == "" {
        lockers[label][peer] = plView
      } else {
        lockers[label][peer] = plView.createOrGetInstanceLocker(peerAddress)
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

func (cl *CombiLocker) GetLockerView(events bool) *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label, cl.parent)
  for label, sub := range cl.SubLockers {
    combiView.SubLockers[label] = sub.GetLockerView(events)
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
  for label, sub := range cl.SubLockers {
    combiView.SubLockers[label] = sub.GetLockerWithoutPeers()
  }
  combiView.Current = cl.Current
  combiView.DataLocker = cl.DataLocker
  return combiView
}

func (cl *CombiLocker) GetLockerWithoutEvents() *CombiLocker {
  cl.lock.RLock()
  defer cl.lock.RUnlock()
  combiView := NewCombiLocker(cl.Label, cl.parent)
  for label, sub := range cl.SubLockers {
    combiView.SubLockers[label] = sub.GetLockerWithoutEvents()
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

func (cl *CombiLocker) getPeersClientResults(peerName string, trackingHeaders []string, crossTrackingHeaders map[string][]string,
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
    for address, instanceLocker := range peerLocker.InstanceLockers {
      if byInstances {
        peersInstanceClientResults[peer][address] = map[string]*results.TargetResults{}
      }
      instanceLocker.lock.RLock()
      lockerData := instanceLocker.Locker[constants.LockerClientKey]
      if lockerData != nil {
        for target, targetData := range lockerData.SubKeys {
          if strings.EqualFold(target, constants.LockerInvocationsKey) {
            continue
          }
          if !byInstances && peersClientResults[peer][target] == nil {
            peersClientResults[peer][target] = &results.TargetResults{Target: target}
            peersClientResults[peer][target].Init(trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
          }
          if byInstances && peersInstanceClientResults[peer][address][target] == nil {
            peersInstanceClientResults[peer][address][target] = &results.TargetResults{Target: target}
            peersInstanceClientResults[peer][address][target].Init(trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
          }
          if data := targetData.Data; data != "" {
            result := &results.TargetResults{}
            if err := util.ReadJson(data, result); err == nil {
              if byInstances {
                results.AddDeltaResults(peersInstanceClientResults[peer][address][target], result, detailed)
              } else {
                results.AddDeltaResults(peersClientResults[peer][target], result, detailed)
              }
            } else {
              fmt.Printf("Error parsing peer result json: %s\n", err.Error())
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

func (cl *CombiLocker) GetPeersClientResults(peerName string, trackingHeaders []string, crossTrackingHeaders map[string][]string,
  trackingTimeBuckets [][]int, detailed, byInstances bool) interface{} {

  var summaryResults interface{}
  peersClientResults, peersInstanceClientResults := cl.getPeersClientResults(peerName, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets, detailed, byInstances)
  summaryPeersResults := map[string]*results.ClientTargetsAggregateResults{}
  summaryPeersInstanceResults := map[string]map[string]*results.ClientTargetsAggregateResults{}
  detailedResults := map[string]interface{}{}

  if byInstances {
    for peer, instanceResults := range peersInstanceClientResults {
      summaryPeersInstanceResults[peer] = map[string]*results.ClientTargetsAggregateResults{}
      for address, targetsResults := range instanceResults {
        summaryPeersInstanceResults[peer][address] = &results.ClientTargetsAggregateResults{}
        summaryPeersInstanceResults[peer][address].Init()
        for _, targetResult := range targetsResults {
          summaryPeersInstanceResults[peer][address].AddTargetResult(targetResult, detailed)
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
      summaryPeersResults[peer] = &results.ClientTargetsAggregateResults{}
      summaryPeersResults[peer].Init()
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
  if err := util.ReadJson(l.Data, event); err == nil {
    return event
  } else {
    fmt.Printf("Error while parsing event: %s\n", err.Error())
  }
  return nil
}

func sortPeerEvents(peerEvents PeerEvents, reverse bool) {
  for _, pe := range peerEvents {
    events.SortEvents(pe, reverse)
  }
}

func (cl *CombiLocker) GetPeerEvents(peerNames []string, unified, reverse, data bool) PeerEvents {
  eventsMap := PeerEvents{}
  peerLockers := cl.getPeersLockers(peerNames)
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

func (cl *CombiLocker) SearchInPeerEvents(peerNames []string, pattern *regexp.Regexp, unified, reverse, data bool) PeerEvents {
  eventsMap := PeerEvents{}
  peerLockers := cl.getPeersLockers(peerNames)
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
    pl.DataLocker.Locker[constants.LockerEventsKey] = newLockerData(now, 1)
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
      locker.Init()
    }
  }
}

func (ll *LabeledLockers) ReplaceLockers(lockers CombiLockers) {
  ll.lock.Lock()
  defer ll.lock.Unlock()
  ll.lockers = lockers
  for _, l := range lockers {
    ll.allLockers[l.PathLabel] = l
    if l.Current {
      ll.currentLocker = l
    }
  }
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

func (ll *LabeledLockers) GetDataLockers(label string) DataLockers {
  sourceLockers := ll.getMatchingOrTopLockers(label)
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
  sourceLockers := ll.getMatchingOrTopLockers(label)
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
  lockers := ll.getMatchingOrTopLockers(label)
  result := map[string]interface{}{}
  if len(lockers) == 1 {
    if cl := lockers[label]; cl != nil {
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
    return nil, false
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

func (ll *LabeledLockers) GetFromPeerInstanceLocker(label, peerName, peerAddress string, keys []string, data bool, level int) (interface{}, bool) {
  if len(keys) == 0 {
    return nil, false
  }
  lockers := ll.getMatchingOrAllLockers(label)
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
      if data, present, _ := cl.GetFromPeerInstanceLocker(peerName, peerAddress, keys, data, level); present {
        result[cl.PathLabel] = data
      }
    }
  }
  return result, false
}
