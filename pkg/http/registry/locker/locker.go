package locker

import (
	"goto/pkg/constants"
	"goto/pkg/http/client/results"
	"goto/pkg/util"
	"strings"
	"sync"
	"time"
)

type LockerData struct {
  Data          string                 `json:"data"`
  SubKeys       map[string]*LockerData `json:"subKeys"`
  FirstReported time.Time              `json:"firstReported"`
  LastReported  time.Time              `json:"lastReported"`
  Locked        bool                   `json:"locked"`
}

type InstanceLocker struct {
  Locker map[string]*LockerData `json:"locker"`
  lock   sync.RWMutex
}

type PeerLocker struct {
  Locker map[string]*InstanceLocker `json:"locker"`
  lock   sync.RWMutex
}

type PeersLockers struct {
  peerLocker  map[string]*PeerLocker
  lock sync.RWMutex
}

func newInstanceLocker() *InstanceLocker {
	instanceLocker := &InstanceLocker{}
	instanceLocker.init()
	return instanceLocker
}

func (il *InstanceLocker) init() {
	il.lock.Lock()
	defer il.lock.Unlock()
	il.Locker = map[string]*LockerData{}
}

func (il *InstanceLocker) store(keys []string, value string) {
  il.lock.Lock()
  defer il.lock.Unlock()
  rootKey := keys[0]
  lockerData := il.Locker[rootKey]
  now := time.Now()
  if lockerData != nil && lockerData.Locked {
    il.Locker[rootKey+"_last"] = lockerData
    il.Locker[rootKey] = nil
  }
  lockerData = il.Locker[rootKey]
  if lockerData == nil {
    lockerData = &LockerData{SubKeys: map[string]*LockerData{}, FirstReported: now}
    il.Locker[rootKey] = lockerData
  }
  for i := 1; i < len(keys); i++ {
    if lockerData.SubKeys[keys[i]] == nil {
      lockerData.SubKeys[keys[i]] = &LockerData{SubKeys: map[string]*LockerData{}, FirstReported: now}
    }
    lockerData = lockerData.SubKeys[keys[i]]
  }
  lockerData.Data = value
  lockerData.LastReported = now
}

func removeSubKey(lockerData *LockerData, keys []string, index int) {
  if index >= len(keys) {
    return
  }
  currentKey := keys[index]
  if lockerData.SubKeys[currentKey] != nil {
    nextLockerData := lockerData.SubKeys[currentKey]
    removeSubKey(nextLockerData, keys, index+1)
    if len(nextLockerData.SubKeys) == 0 {
      delete(lockerData.SubKeys, currentKey)
    }
  }
}

func (il *InstanceLocker) remove(keys []string) {
  il.lock.Lock()
  defer il.lock.Unlock()
  rootKey := keys[0]
  lockerData := il.Locker[rootKey]
  if lockerData != nil {
    removeSubKey(lockerData, keys, 1)
    if len(lockerData.SubKeys) == 0 {
      delete(il.Locker, rootKey)
    }
  }
}

func (il *InstanceLocker) lockKeys(keys []string) {
  il.lock.Lock()
  defer il.lock.Unlock()
  if il.Locker[keys[0]] != nil {
    lockerData := il.Locker[keys[0]]
    for i := 1; i < len(keys); i++ {
      if lockerData.SubKeys[keys[i]] != nil {
        lockerData = lockerData.SubKeys[keys[i]]
      }
    }
    lockerData.Locked = true
  }
}

func newPeerLocker() *PeerLocker {
	peerLocker := &PeerLocker{}
	peerLocker.init()
	return peerLocker
}

func (pl *PeerLocker) init() {
	pl.Locker = map[string]*InstanceLocker{}
}

func (pl *PeerLocker) getInstanceLocker(peerAddress string) *InstanceLocker {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  if pl.Locker[peerAddress] == nil {
		pl.Locker[peerAddress] = newInstanceLocker()
  }
  return pl.Locker[peerAddress]
}

func (pl *PeerLocker) clearInstanceLocker(peerAddress string) bool {
	pl.lock.Lock()
	defer pl.lock.Unlock()
	_, present := pl.Locker[peerAddress]
	if present {
		pl.Locker[peerAddress] = newInstanceLocker()
	}
	return present
}

func NewPeersLocker() *PeersLockers {
	pl := &PeersLockers{}
	pl.Init()
	return pl
}

func (pl *PeersLockers) Init() {
	pl.lock.Lock()
	defer pl.lock.Unlock()
	pl.peerLocker = map[string]*PeerLocker{}
}

func (pl *PeersLockers) InitPeerLocker(peerName string) bool {
  if peerName != "" {
    pl.lock.Lock()
    pl.peerLocker[peerName] = newPeerLocker()
    pl.lock.Unlock()
    return true
  }
  return false
}

func (pl *PeersLockers) getInstanceLocker(peerName string, peerAddress string) *InstanceLocker {
  pl.lock.Lock()
  peerLocker := pl.peerLocker[peerName]
  if peerLocker == nil {
    peerLocker = newPeerLocker()
    pl.peerLocker[peerName] = peerLocker
  }
  pl.lock.Unlock()
  return peerLocker.getInstanceLocker(peerAddress)
}

func (pl *PeersLockers) Store(peerName string, peerAddress string, keys []string, value string) {
  if len(keys) == 0 {
    return
  }
  pl.getInstanceLocker(peerName, peerAddress).store(keys, value)
}

func (pl *PeersLockers) Remove(peerName string, peerAddress string, keys []string) {
  pl.getInstanceLocker(peerName, peerAddress).remove(keys)
}

func (pl *PeersLockers) LockKeys(peerName string, peerAddress string, keys []string) {
  pl.getInstanceLocker(peerName, peerAddress).lockKeys(keys)
}

func (pl *PeersLockers) ClearInstanceLocker(peerName string, peerAddress string) bool {
  pl.lock.Lock()
  peerLocker := pl.peerLocker[peerName]
  pl.lock.Unlock()
  if peerLocker != nil {
		return peerLocker.clearInstanceLocker(peerAddress)
	}
	return false
}

func (pl *PeersLockers) RemovePeerLocker(peerName string) {
	pl.lock.Lock()
	delete(pl.peerLocker, peerName)
	pl.lock.Unlock()
}

func (pl *PeersLockers) GetPeerLocker(peerName, peerAddress string) interface{} {
  if peerName == "" {
    return pl.peerLocker
  }
  pl.lock.RLock()
  peerLocker := pl.peerLocker[peerName]
  pl.lock.RUnlock()
  if peerAddress == "" {
    return peerLocker
  }
  if peerLocker != nil {
		return peerLocker.getInstanceLocker(peerAddress)
  }
  return nil
}

func (pl *PeersLockers) GetTargetsResults() map[string]map[string]*results.TargetResults {
  pl.lock.RLock()
  defer pl.lock.RUnlock()
  summary := map[string]map[string]*results.TargetResults{}
  for peer, peerLocker := range pl.peerLocker {
    summary[peer] = map[string]*results.TargetResults{}
    peerLocker.lock.RLock()
    for _, instanceLocker := range peerLocker.Locker {
      instanceLocker.lock.RLock()
      lockerData := instanceLocker.Locker[constants.LockerClientKey]
      if lockerData != nil {
        for target, targetData := range lockerData.SubKeys {
          if strings.EqualFold(target, constants.LockerInvocationsKey) {
            continue
          }
          if summary[peer][target] == nil {
            summary[peer][target] = &results.TargetResults{Target: target}
            summary[peer][target].Init()
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

func (pl *PeersLockers) GetTargetsSummaryResults() map[string]*results.TargetsSummaryResults {
  clientResultsSummary := pl.GetTargetsResults()
  pl.lock.RLock()
  defer pl.lock.RUnlock()
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