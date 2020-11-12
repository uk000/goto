package locker

import (
	"goto/pkg/constants"
	"goto/pkg/http/client/results"
	"goto/pkg/util"
	"strconv"
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
  lockCounter   int
}

type InstanceLocker struct {
  Locker map[string]*LockerData `json:"locker"`
  lock   sync.RWMutex
}

type PeerLocker struct {
  InstanceLockers        map[string]*InstanceLocker `json:"instanceLockers"`
  Locker                 map[string]*LockerData `json:"locker"`
  lockedCounter int
  lock          sync.RWMutex
}

type PeersLockers struct {
  peerLocker map[string]*PeerLocker
  lock       sync.RWMutex
}

func createOrCopyLockerData(locker map[string]*LockerData, key string, now time.Time) *LockerData {
  lockCounter := 0
  lockerData := locker[key]
  if lockerData != nil && lockerData.Locked {
    lockCounter = lockerData.lockCounter+1
    locker[key+"_"+strconv.Itoa(lockCounter)] = lockerData
    locker[key] = nil
    lockerData = nil
  }
  if lockerData == nil {
    lockerData = &LockerData{SubKeys: map[string]*LockerData{}, FirstReported: now, lockCounter: lockCounter}
    locker[key] = lockerData
  }
  return lockerData
}

func unsafeStoreKeysInLocker(locker map[string]*LockerData, keys []string, value string) {
  rootKey := keys[0]
  now := time.Now()
  lockerData := createOrCopyLockerData(locker, rootKey, now)
  for i := 1; i < len(keys); i++ {
    lockerData.SubKeys[keys[i]] = createOrCopyLockerData(lockerData.SubKeys, keys[i], now)
    lockerData = lockerData.SubKeys[keys[i]]
  }
  lockerData.Data = value
  lockerData.LastReported = now
}

func unsafeRemoveSubKeys(lockerData *LockerData, keys []string, index int) {
  if index >= len(keys) {
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
  rootKey := keys[0]
  lockerData := locker[rootKey]
  if lockerData != nil {
    unsafeRemoveSubKeys(lockerData, keys, 1)
    if len(lockerData.SubKeys) == 0 {
      delete(locker, rootKey)
    }
  }
}

func unsafeLockKeys(locker map[string]*LockerData, keys []string) {
  if locker[keys[0]] != nil {
    lockerData := locker[keys[0]]
    for i := 1; i < len(keys); i++ {
      if lockerData.SubKeys[keys[i]] != nil {
        lockerData = lockerData.SubKeys[keys[i]]
      }
    }
    lockerData.Locked = true
  }
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
  unsafeStoreKeysInLocker(il.Locker, keys, value)
}

func (il *InstanceLocker) remove(keys []string) {
  il.lock.Lock()
  defer il.lock.Unlock()
  unsafeRemoveKeysFromLocker(il.Locker, keys)
}

func (il *InstanceLocker) lockKeys(keys []string) {
  il.lock.Lock()
  defer il.lock.Unlock()
  unsafeLockKeys(il.Locker, keys)
}

func (il *InstanceLocker) lockLocker() {
  il.lock.Lock()
  defer il.lock.Unlock()
  for _, lockerData := range il.Locker {
    lockerData.Locked = true
  }
}

func newPeerLocker() *PeerLocker {
  peerLocker := &PeerLocker{}
  peerLocker.init()
  return peerLocker
}

func (pl *PeerLocker) init() {
  pl.InstanceLockers = map[string]*InstanceLocker{}
  pl.Locker = map[string]*LockerData{}
}

func (pl *PeerLocker) getInstanceLocker(peerAddress string) *InstanceLocker {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  if pl.InstanceLockers[peerAddress] == nil {
    pl.InstanceLockers[peerAddress] = newInstanceLocker()
  }
  return pl.InstanceLockers[peerAddress]
}

func (pl *PeerLocker) clearInstanceLocker(peerAddress string) bool {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  _, present := pl.InstanceLockers[peerAddress]
  if present {
    pl.InstanceLockers[peerAddress] = newInstanceLocker()
  }
  return present
}

func (pl *PeerLocker) removeInstanceLocker(peerAddress string) {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  delete(pl.InstanceLockers, peerAddress)
}

func (pl *PeerLocker) store(keys []string, value string) {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  unsafeStoreKeysInLocker(pl.Locker, keys, value)
}

func (pl *PeerLocker) remove(keys []string) {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  unsafeRemoveKeysFromLocker(pl.Locker, keys)
}

func (pl *PeerLocker) lockInstanceLocker(peerAddress string) {
  il := pl.getInstanceLocker(peerAddress)
  il.lockLocker()
  pl.lock.Lock()
  defer pl.lock.Unlock()
  pl.lockedCounter++
  pl.InstanceLockers[peerAddress+"-"+strconv.Itoa(pl.lockedCounter)] = il
  delete(pl.InstanceLockers, peerAddress)
}

func (pl *PeerLocker) lockKeys(keys []string) {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  unsafeLockKeys(pl.Locker, keys)
}

func (pl *PeerLocker) lockLocker() {
  pl.lock.Lock()
  defer pl.lock.Unlock()
  pl.lockedCounter++
  lockedIndex := strconv.Itoa(pl.lockedCounter)
  for key := range pl.Locker {
    pl.Locker[key+"-"+lockedIndex] = pl.Locker[key]
    delete(pl.Locker, key)
  }
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

func (pl *PeersLockers) InitPeerLocker(peerName string, peerAddress string) bool {
  if peerName != "" {
    pl.lock.Lock()
    if peerAddress == "" || pl.peerLocker[peerName] == nil {
      pl.peerLocker[peerName] = newPeerLocker()
    } else {
      pl.peerLocker[peerName].clearInstanceLocker(peerAddress)
    }
    pl.lock.Unlock()
    return true
  }
  return false
}

func (pl *PeersLockers) getPeerLocker(peerName string) *PeerLocker {
  pl.lock.Lock()
  peerLocker := pl.peerLocker[peerName]
  if peerLocker == nil {
    peerLocker = newPeerLocker()
    pl.peerLocker[peerName] = peerLocker
  }
  pl.lock.Unlock()
  return peerLocker
}

func (pl *PeersLockers) getInstanceLocker(peerName string, peerAddress string) *InstanceLocker {
  return pl.getPeerLocker(peerName).getInstanceLocker(peerAddress)
}

func (pl *PeersLockers) Store(peerName, peerAddress string, keys []string, value string) {
  if len(keys) == 0 {
    return
  }
  if peerAddress == "" {
    pl.getPeerLocker(peerName).store(keys, value)
  } else {
    pl.getInstanceLocker(peerName, peerAddress).store(keys, value)
  }
}

func (pl *PeersLockers) Remove(peerName, peerAddress string, keys []string) {
  if len(keys) == 0 {
    return
  }
  if peerAddress == "" {
    pl.getPeerLocker(peerName).remove(keys)
  } else {
    pl.getInstanceLocker(peerName, peerAddress).remove(keys)
  }
}

func (pl *PeersLockers) LockKeysInPeerLocker(peerName, peerAddress string, keys []string) {
  if len(keys) == 0 {
    return
  }
  if peerAddress == "" {
    pl.getPeerLocker(peerName).lockKeys(keys)
  } else {
    pl.getInstanceLocker(peerName, peerAddress).lockKeys(keys)
  }
}

func (pl *PeersLockers) LockPeerLocker(peerName,  peerAddress string) {
  if peerAddress == "" {
    pl.getPeerLocker(peerName).lockLocker()
  } else {
    pl.getPeerLocker(peerName).lockInstanceLocker(peerAddress)
  }
}

func (pl *PeersLockers) ClearInstanceLocker(peerName, peerAddress string) bool {
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

func (pl *PeersLockers) GetTargetsResults(trackingHeaders []string, crossTrackingHeaders map[string][]string) map[string]map[string]*results.TargetResults {
  pl.lock.RLock()
  defer pl.lock.RUnlock()
  summary := map[string]map[string]*results.TargetResults{}
  for peer, peerLocker := range pl.peerLocker {
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

func (pl *PeersLockers) GetTargetsSummaryResults(trackingHeaders []string, crossTrackingHeaders map[string][]string) map[string]*results.TargetsSummaryResults {
  clientResultsSummary := pl.GetTargetsResults(trackingHeaders, crossTrackingHeaders)
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
