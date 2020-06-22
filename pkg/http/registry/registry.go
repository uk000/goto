package registry

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/http/client/results"
	"goto/pkg/http/invocation"
	"goto/pkg/job"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type Peer struct {
  Name      string `json:"name"`
  Address   string `json:"address"`
  Pod       string `json:"pod"`
  Namespace string `json:"namespace"`
}

type Pod struct {
  Name    string `json:"name"`
  Address string `json:"address"`
}

type Peers struct {
  Name      string         `json:"name"`
  Namespace string         `json:"namespace"`
  Pods      map[string]Pod `json:"pods"`
}

type PeerTarget struct {
  invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PeerJob struct {
  job.Job
}

type PeerJobs map[string]*PeerJob

type LockerData struct {
  Data          string                 `json:"data"`
  SubKeys       map[string]*LockerData `json:"subKeys"`
  FirstReported time.Time              `json:"firstReported"`
  LastReported  time.Time              `json:"lastReported"`
  Locked        bool                    `json:"locked"`
}

type InstanceLocker struct {
  Locker map[string]*LockerData `json:"locker"`
  lock   sync.RWMutex
}

type PeerLocker struct {
  Locker map[string]*InstanceLocker `json:"locker"`
  lock   sync.RWMutex
}

type PortRegistry struct {
  peers       map[string]*Peers
  peerTargets map[string]PeerTargets
  peerJobs    map[string]PeerJobs
  peerLocker  map[string]*PeerLocker
  peersLock   sync.RWMutex
  lockersLock sync.RWMutex
}

var (
  Handler      util.ServerHandler       = util.ServerHandler{Name: "registry", SetRoutes: SetRoutes}
  portRegistry map[string]*PortRegistry = map[string]*PortRegistry{}
  registryLock sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  registryRouter := r.PathPrefix("/registry").Subrouter()
  peersRouter := registryRouter.PathPrefix("/peers").Subrouter()
  util.AddRoute(peersRouter, "/add", addPeer, "POST")
  util.AddRoute(peersRouter, "/{peer}/remove/{address}", removePeer, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/health/{address}", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/{peer}/health", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/health", checkPeerHealth, "GET")
  util.AddRoute(peersRouter, "/{peer}/health/cleanup", cleanupUnhealthyPeers, "POST")
  util.AddRoute(peersRouter, "/health/cleanup", cleanupUnhealthyPeers, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/store/{keys}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/remove/{keys}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/lock/{keys}", lockInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/lockers/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/{peer}/locker", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/lockers", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/lockers/targets/results", getLockerTargetResultsSummary, "GET")

  util.AddRoute(peersRouter, "/{peer}/targets/add", addPeerTarget, "POST")
  util.AddRoute(peersRouter, "/targets/add", addPeerTarget, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/remove", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/remove/all", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/invoke", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/invoke/all", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/targets/invoke/all", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/stop", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/stop/all", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/targets/stop/all", stopPeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets", getPeerTargets, "GET")
  util.AddRoute(peersRouter, "/targets/results/all/{enable}", enableAllTargetsResultsCollection, "POST", "PUT")
  util.AddRoute(peersRouter, "/targets/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")
  util.AddRoute(peersRouter, "/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/targets", getPeerTargets, "GET")

  util.AddRoute(peersRouter, "/{peer}/jobs/add", addPeerJob, "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/remove", removePeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/run", runPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/run/all", runPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/stop", stopPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/stop/all", stopPeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/clear", clearPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs", getPeerJobs, "GET")
  util.AddRoute(peersRouter, "/jobs/clear", clearPeerJobs, "POST")
  util.AddRoute(peersRouter, "/jobs", getPeerJobs, "GET")

  util.AddRoute(peersRouter, "/track/headers/{headers}", addPeersTrackingHeaders, "POST", "PUT")

  util.AddRoute(peersRouter, "/clear", clearPeers, "POST")
  util.AddRoute(peersRouter, "", getPeers, "GET")
}

func getPortRegistry(r *http.Request) *PortRegistry {
  listenerPort := util.GetListenerPort(r)
  registryLock.Lock()
  defer registryLock.Unlock()
  pr := portRegistry[listenerPort]
  if pr == nil {
    pr = &PortRegistry{}
    pr.init()
    portRegistry[listenerPort] = pr
  }
  return pr
}

func (pr *PortRegistry) reset() {
  pr.peersLock.Lock()
  pr.peers = map[string]*Peers{}
  pr.peerTargets = map[string]PeerTargets{}
  pr.peerJobs = map[string]PeerJobs{}
  pr.peersLock.Unlock()
  pr.lockersLock.Lock()
  pr.peerLocker = map[string]*PeerLocker{}
  pr.lockersLock.Unlock()
}

func (pr *PortRegistry) init() {
  pr.peersLock.Lock()
  isEmpty := pr.peers == nil
  pr.peersLock.Unlock()
  if isEmpty {
    pr.reset()
  }
}

func (pr *PortRegistry) addPeer(p *Peer) {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  if pr.peers[p.Name] == nil {
    pr.peers[p.Name] = &Peers{Name: p.Name, Namespace: p.Namespace, Pods: map[string]Pod{}}
  }
  pr.peers[p.Name].Pods[p.Address] = Pod{p.Pod, p.Address}
  if pr.peerTargets[p.Name] == nil {
    pr.peerTargets[p.Name] = PeerTargets{}
  }
  if pr.peerJobs[p.Name] == nil {
    pr.peerJobs[p.Name] = PeerJobs{}
  }
  pr.lockersLock.Lock()
  if pr.peerLocker[p.Name] == nil {
    pr.peerLocker[p.Name] = &PeerLocker{Locker: map[string]*InstanceLocker{}}
  }
  pr.lockersLock.Unlock()
}

func (pr *PortRegistry) removePeer(name string, address string) bool {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  present := false
  if _, present = pr.peers[name]; present {
    delete(pr.peers[name].Pods, address)
    if len(pr.peers[name].Pods) == 0 {
      delete(pr.peers, name)
    }
  }
  return present
}

func invokePeerAPI(method, peerAddress, api string, expectedStatus int) bool {
  client := http.Client{Timeout: 2 * time.Second}
  if strings.EqualFold(method, "GET") {
    if resp, err := client.Get("http://" + peerAddress + api); err == nil {
      util.CloseResponse(resp)
      return true
    }
  } else {
    if resp, err := client.Post("http://"+peerAddress+api, "plain/text", nil); err == nil {
      util.CloseResponse(resp)
      return resp.StatusCode == expectedStatus
    }
  }
  return false
}

func (pr *PortRegistry) checkPeerHealth(peerName string, peerAddress string) map[string]map[string]bool {
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  peersToCheck := map[string][]string{}
  if peerName != "" {
    peersToCheck[peerName] = []string{}
    if peerAddress != "" {
      if pr.peers[peerName] != nil {
        if _, present := pr.peers[peerName].Pods[peerAddress]; present {
          peersToCheck[peerName] = append(peersToCheck[peerName], peerAddress)
        }
      }
    } else {
      if pr.peers[peerName] != nil {
        for address := range pr.peers[peerName].Pods {
          peersToCheck[peerName] = append(peersToCheck[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peersToCheck[name] = []string{}
      for address := range peer.Pods {
        peersToCheck[name] = append(peersToCheck[name], address)
      }
    }
  }
  result := map[string]map[string]bool{}
  for name, peerAddresses := range peersToCheck {
    result[name] = map[string]bool{}
    for _, address := range peerAddresses {
      if invokePeerAPI("GET", address, "/health", http.StatusAccepted) {
        result[name][address] = true
        log.Printf("Peer %s Address %s is healthy\n", name, address)
      } else {
        result[name][address] = false
        log.Printf("Peer %s Address %s is unhealthy\n", name, address)
      }
    }
  }
  return result
}

func (pr *PortRegistry) cleanupUnhealthyPeers(name string) map[string]map[string]bool {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  result := map[string]map[string]bool{}
  for peerName, peers := range pr.peers {
    if name == "" || name == peerName {
      result[peerName] = map[string]bool{}
      pods := peers.Pods
      for address := range pods {
        if invokePeerAPI("GET", address, "/health", 200) {
          result[peerName][address] = true
          log.Printf("Peer %s Address %s is healthy\n", peerName, address)
        } else {
          result[peerName][address] = false
          log.Printf("Peer %s Address %s is unhealthy or unavailable\n", peerName, address)
          delete(pr.peers[peerName].Pods, address)
          if len(pr.peers[peerName].Pods) == 0 {
            delete(pr.peers, peerName)
          }
        }
      }
    }
  }
  return result
}

func (pr *PortRegistry) getInstanceLocker(peerName string, peerAddress string) *InstanceLocker {
  pr.lockersLock.Lock()
  peerLocker := pr.peerLocker[peerName]
  if peerLocker == nil {
    peerLocker = &PeerLocker{Locker: map[string]*InstanceLocker{}}
    pr.peerLocker[peerName] = peerLocker
  }
  pr.lockersLock.Unlock()

  peerLocker.lock.Lock()
  instanceLocker := peerLocker.Locker[peerAddress]
  if instanceLocker == nil {
    instanceLocker = &InstanceLocker{Locker: map[string]*LockerData{}}
    peerLocker.Locker[peerAddress] = instanceLocker
  }
  peerLocker.lock.Unlock()

  return instanceLocker
}

func (pr *PortRegistry) storeInPeerLocker(peerName string, peerAddress string, keys []string, value string) {
  if len(keys) == 0 {
    return
  }
  instanceLocker := pr.getInstanceLocker(peerName, peerAddress)
  instanceLocker.lock.Lock()
  defer instanceLocker.lock.Unlock()
  rootKey := keys[0]
  lockerData := instanceLocker.Locker[rootKey]
  now := time.Now()
  if lockerData != nil && lockerData.Locked {
    instanceLocker.Locker[rootKey+"_last"] = lockerData
    instanceLocker.Locker[rootKey] = nil
  }
  lockerData = instanceLocker.Locker[rootKey]
  if lockerData == nil {
    lockerData = &LockerData{SubKeys: map[string]*LockerData{}, FirstReported: now}
    instanceLocker.Locker[rootKey] = lockerData
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

func (pr *PortRegistry) removeFromPeerLocker(peerName string, peerAddress string, keys []string) {
  instanceLocker := pr.getInstanceLocker(peerName, peerAddress)
  instanceLocker.lock.Lock()
  defer instanceLocker.lock.Unlock()
  rootKey := keys[0]
  lockerData := instanceLocker.Locker[rootKey]
  if lockerData != nil {
    removeSubKey(lockerData, keys, 1)
    if len(lockerData.SubKeys) == 0 {
      delete(instanceLocker.Locker, rootKey)
    }
  }
}

func (pr *PortRegistry) lockInPeerLocker(peerName string, peerAddress string, keys []string) {
  instanceLocker := pr.getInstanceLocker(peerName, peerAddress)
  instanceLocker.lock.Lock()
  defer instanceLocker.lock.Unlock()
  if instanceLocker.Locker[keys[0]] != nil {
    lockerData := instanceLocker.Locker[keys[0]]
    for i := 1; i < len(keys); i++ {
      if lockerData.SubKeys[keys[i]] != nil {
        lockerData = lockerData.SubKeys[keys[i]]
      }
    }
    lockerData.Locked = true
  }
}

func (pr *PortRegistry) clearInstanceLocker(peerName string, peerAddress string) bool {
  pr.lockersLock.Lock()
  peerLocker := pr.peerLocker[peerName]
  pr.lockersLock.Unlock()
  if peerLocker != nil {
    peerLocker.lock.Lock()
    instanceLocker := peerLocker.Locker[peerAddress]
    peerLocker.lock.Unlock()
    if instanceLocker != nil {
      peerLocker.Locker[peerAddress] = &InstanceLocker{Locker: map[string]*LockerData{}}
      return true
    }
  }
  return false
}

func (pr *PortRegistry) clearPeerLocker(peerName string) bool {
  if peerName != "" {
    pr.lockersLock.Lock()
    pr.peerLocker[peerName] = &PeerLocker{Locker: map[string]*InstanceLocker{}}
    pr.lockersLock.Unlock()
    return true
  }
  return false
}

func (pr *PortRegistry) loadPeerAddresses(peerName, peerAddress string, peerAddresses map[string][]string) {
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  if pr.peers[peerName] != nil {
    peerAddresses[peerName] = []string{}
    if peerAddress != "" {
      if _, present := pr.peers[peerName].Pods[peerAddress]; present {
        peerAddresses[peerName] = append(peerAddresses[peerName], peerAddress)
      }
    } else {
      for address := range pr.peers[peerName].Pods {
        peerAddresses[peerName] = append(peerAddresses[peerName], address)
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
}

func (pr *PortRegistry) clearLocker(peerName, peerAddress string) map[string]map[string]bool {
  peersToClear := map[string][]string{}
  if peerName != "" && peerAddress != "" {
    if pr.clearInstanceLocker(peerName, peerAddress) {
      pr.loadPeerAddresses(peerName, peerAddress, peersToClear)
    }
  } else if peerName != "" && peerAddress == "" {
    if pr.clearPeerLocker(peerName) {
      pr.loadPeerAddresses(peerName, "", peersToClear)
    }
  } else {
    pr.lockersLock.Lock()
    pr.peerLocker = map[string]*PeerLocker{}
    pr.lockersLock.Unlock()
    pr.loadPeerAddresses("", "", peersToClear)
  }
  result := map[string]map[string]bool{}
  for name, peerAddresses := range peersToClear {
    result[name] = map[string]bool{}
    for _, address := range peerAddresses {
      if invokePeerAPI("POST", address, "/client/results/clear", http.StatusAccepted) {
        result[name][address] = true
        log.Printf("Results cleared on peer address %s\n", address)
      } else {
        result[name][address] = false
        log.Printf("Failed to clear results on peer address %s\n", address)
      }
    }
  }
  return result
}

func (pr *PortRegistry) getPeerLocker(name, address string) interface{} {
  if name == "" {
    return pr.peerLocker
  }
  pr.lockersLock.RLock()
  peerLocker := pr.peerLocker[name]
  pr.lockersLock.RUnlock()
  if address == "" {
    return peerLocker
  }
  if peerLocker != nil {
    peerLocker.lock.RLock()
    instanceLocker := peerLocker.Locker[address]
    peerLocker.lock.RUnlock()
    return instanceLocker
  }
  return nil
}

func (pr *PortRegistry) getLockerClientResultsSummary() interface{} {
  pr.lockersLock.RLock()
  defer pr.lockersLock.RUnlock()
  summary := map[string]map[string]*results.TargetResults{}
  for peer, peerLocker := range pr.peerLocker {
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

func (pr *PortRegistry) addPeerTarget(peerName string, target *PeerTarget) map[string]map[string]bool {
  pr.peersLock.Lock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerTargets[peerName] == nil {
      pr.peerTargets[peerName] = PeerTargets{}
    }  
    pr.peerTargets[peerName][target.Name] = target
    if pr.peers[peerName] != nil {
      peerAddresses[peerName] = []string{}
      for address := range pr.peers[peerName].Pods {
        peerAddresses[peerName] = append(peerAddresses[peerName], address)
      }
    }
  } else {
    for name, peer := range pr.peers {
      if pr.peerTargets[name] == nil {
        pr.peerTargets[name] = PeerTargets{}
      }  
      pr.peerTargets[name][target.Name] = target
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.Unlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    payload := util.ToJSON(target)
    for _, address := range addresses {
      if resp, err := http.Post("http://"+address+"/client/targets/add", "application/json", strings.NewReader(payload)); err == nil {
        util.CloseResponse(resp)
        result[name][address] = resp.StatusCode == http.StatusAccepted
        log.Printf("Pushed target %s to peer %s address %s with response %s\n", target.Name, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to pushed target %s to peer %s address %s with error: %s\n", target.Name, name, address, err.Error())
      }
    }
  }
  return result
}

func (pr *PortRegistry) removePeerTargets(peerName string, targets []string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerTargets[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  targetList := strings.Join(targets, ",")
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    removed := true
    for _, address := range addresses {
      if resp, err := http.Post(fmt.Sprintf("http://%s/client/targets/%s/remove", address, targetList), "plain/text", nil); err == nil {
        util.CloseResponse(resp)
        result[name][address] = resp.StatusCode == http.StatusAccepted
        log.Printf("Removed targets %s from peer %s address %s with response %s\n", targetList, name, address, resp.Status)
      } else {
        result[name][address] = false
        removed = false
        log.Printf("Failed to remove targets %s from peer %s address %s with error %s\n", targetList, name, address, err.Error())
      }
    }
    if removed {
      pr.peersLock.Lock()
      if pr.peerTargets[name] != nil {
        if len(targets) > 0 {
          for _, target := range targets {
            delete(pr.peerTargets[name], target)
          }
        } else {
          delete(pr.peerTargets, name)
        }
      }
      pr.peersLock.Unlock()
    }
  }
  return result
}

func (pr *PortRegistry) clearPeerTargets(peerName string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerTargets[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    cleared := true
    for _, address := range addresses {
      if resp, err := http.Post("http://"+address+"/client/targets/clear", "plain/text", nil); err == nil {
        util.CloseResponse(resp)
        cleared = resp.StatusCode == http.StatusAccepted
        result[name][address] = cleared
        log.Printf("Cleared targets from peer %s address %s with response %s\n", name, address, resp.Status)
      } else {
        result[name][address] = false
        cleared = false
        log.Printf("Failed to clear targets from peer %s address %s, error: %s\n", name, address, err.Error())
      }
    }
    if cleared {
      pr.peersLock.Lock()
      if pr.peerTargets[name] != nil {
        delete(pr.peerTargets, name)
      }
      pr.peersLock.Unlock()
    }
  }
  return result
}

func (pr *PortRegistry) stopPeerTargets(peerName string, targets string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerTargets[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    for _, address := range addresses {
      var resp *http.Response
      var err error
      if len(targets) > 0 {
        resp, err = http.Post("http://"+address+"/client/targets/"+targets+"/stop", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+address+"/client/targets/stop/all", "plain/text", nil)
      }
      if err == nil {
        result[name][address] = resp.StatusCode == http.StatusAccepted
        util.CloseResponse(resp)
        log.Printf("Stopped targets %s from peer %s address %s with response %s\n", targets, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to stop targets %s from peer %s address %s with error %s\n", targets, name, address, err.Error())
      }
    }
  }
  return result
}

func (pr *PortRegistry) enableAllOrInvocationsTargetsResultsCollection(enable string, all bool) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  for name, peer := range pr.peers {
    peerAddresses[name] = []string{}
    for address := range peer.Pods {
      peerAddresses[name] = append(peerAddresses[name], address)
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    for _, address := range addresses {
      uri := "/client/results/"
      if all {
        results.EnableAllTargetResults(util.IsYes(enable))
        uri += "all/"
      } else {
        results.EnableInvocationResults(util.IsYes(enable))
        uri += "invocations/"
      }
      uri += enable
      result[name][address] = invokePeerAPI("POST", address, uri, http.StatusAccepted)
      log.Printf("Enabled All Targets Results Collection on peer %s address %s with response %t\n", name, address, result[name][address])
    }
  }
  return result
}

func (pr *PortRegistry) getPeerTargets(peerName string) PeerTargets {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  return pr.peerTargets[peerName]
}

func (pr *PortRegistry) addPeerJob(peerName string, job *PeerJob) map[string]map[string]bool {
  pr.peersLock.Lock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerJobs[peerName] == nil {
      pr.peerJobs[peerName] = PeerJobs{}
    }
    pr.peerJobs[peerName][job.ID] = job
    if pr.peers[peerName] != nil {
      peerAddresses[peerName] = []string{}
      for address := range pr.peers[peerName].Pods {
        peerAddresses[peerName] = append(peerAddresses[peerName], address)
      }
    }
  } else {
    for name, peer := range pr.peers {
      if pr.peerJobs[name] == nil {
        pr.peerJobs[name] = PeerJobs{}
      }
      pr.peerJobs[name][job.ID] = job
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.Unlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    payload := util.ToJSON(job)
    for _, address := range addresses {
      if resp, err := http.Post("http://"+address+"/jobs/add", "application/json", strings.NewReader(payload)); err == nil {
        util.CloseResponse(resp)
        result[name][address] = resp.StatusCode == http.StatusAccepted
        log.Printf("Pushed job %s to peer %s address %s with response %s\n", job.ID, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to push job %s to peer %s address %s with error %s\n", job.ID, name, address, err.Error())
      }
    }
  }
  return result
}

func (pr *PortRegistry) removePeerJobs(peerName string, jobs []string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerJobs[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  jobList := strings.Join(jobs, ",")
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    removed := true
    for _, address := range addresses {
      if resp, err := http.Post(fmt.Sprintf("http://%s/jobs/%s/remove", address, jobList), "plain/text", nil); err == nil {
        util.CloseResponse(resp)
        result[name][address] = resp.StatusCode == http.StatusAccepted
        log.Printf("Removed jobs %s from peer %s address %s with response %s\n", jobList, name, address, resp.Status)
      } else {
        result[name][address] = false
        removed = false
        log.Printf("Failed to remove jobs %s from peer %s address %s with error %s\n", jobList, name, address, err.Error())
      }
    }
    if removed {
      pr.peersLock.Lock()
      if pr.peerJobs[name] != nil {
        for _, job := range jobs {
          delete(pr.peerJobs[name], job)
        }
      } else {
        delete(pr.peerJobs, name)
      }
      pr.peersLock.Unlock()
    }
  }
  return result
}

func (pr *PortRegistry) stopPeerJobs(peerName string, jobs string) map[string]map[string]bool {
  pr.peersLock.Lock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerJobs[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.Unlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    for _, address := range addresses {
      var resp *http.Response
      var err error
      if len(jobs) > 0 {
        resp, err = http.Post("http://"+address+"/jobs/"+jobs+"/stop", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+address+"/jobs/stop/all", "plain/text", nil)
      }
      if err == nil {
        util.CloseResponse(resp)
        result[name][address] = resp.StatusCode == http.StatusAccepted
        log.Printf("Stopped jobs %s from peer %s address %s with response %s\n", jobs, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to stop jobs %s from peer %s address %s with error %s\n", jobs, name, address, err.Error())
      }
    }
  }
  return result
}

func (pr *PortRegistry) getPeerJobs(peerName string) PeerJobs {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  return pr.peerJobs[peerName]
}

func (pr *PortRegistry) invokePeerTargets(peerName string, targets string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerTargets[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    for _, address := range addresses {
      var resp *http.Response
      var err error
      if len(targets) > 0 {
        resp, err = http.Post("http://"+address+"/client/targets/"+targets+"/invoke", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+address+"/client/targets/invoke/all", "plain/text", nil)
      }
      if err == nil {
        result[name][address] = resp.StatusCode == http.StatusAccepted
        util.CloseResponse(resp)
        log.Printf("Invoked target %s on peer %s address %s with response %s\n", targets, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to invoke targets %s on peer %s address %s with error %s\n", targets, name, address, err.Error())
      }
    }
  }
  return result
}

func (pr *PortRegistry) invokePeerJobs(peerName string, jobs string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerJobs[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    for _, address := range addresses {
      var resp *http.Response
      var err error
      if len(jobs) > 0 {
        resp, err = http.Post("http://"+address+"/jobs/"+jobs+"/run", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+address+"/jobs/run/all", "plain/text", nil)
      }
      if err == nil {
        result[name][address] = resp.StatusCode == http.StatusAccepted
        util.CloseResponse(resp)
        log.Printf("Invoked jobs %s on peer %s address %s with response %s\n", jobs, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to invoke jobs %s on peer %s address %s with error %s\n", jobs, name, address, err.Error())
      }
    }
  }
  return result
}

func (pr *PortRegistry) clearPeerJobs(peerName string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  if peerName != "" {
    if pr.peerJobs[peerName] != nil {
      if pr.peers[peerName] != nil {
        peerAddresses[peerName] = []string{}
        for address := range pr.peers[peerName].Pods {
          peerAddresses[peerName] = append(peerAddresses[peerName], address)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerAddresses[name] = []string{}
      for address := range peer.Pods {
        peerAddresses[name] = append(peerAddresses[name], address)
      }
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    cleared := true
    for _, address := range addresses {
      if resp, err := http.Post("http://"+address+"/jobs/clear", "plain/text", nil); err == nil {
        util.CloseResponse(resp)
        cleared = resp.StatusCode == http.StatusAccepted
        result[name][address] = cleared
        log.Printf("Cleared jobs from peer %s address %s with response %s\n", name, address, resp.Status)
      } else {
        result[name][address] = false
        cleared = false
        log.Printf("Failed to clear jobs from peer %s address %s, error: %s\n", name, address, err.Error())
      }
    }
    if cleared {
      pr.peersLock.Lock()
      if _, present := pr.peerJobs[name]; present {
        delete(pr.peerJobs, name)
      }
      pr.peersLock.Unlock()
    }
  }
  return result
}

func (pr *PortRegistry) addPeersTrackingHeaders(headers string) map[string]map[string]bool {
  pr.peersLock.RLock()
  peerAddresses := map[string][]string{}
  for name, peer := range pr.peers {
    peerAddresses[name] = []string{}
    for address := range peer.Pods {
      peerAddresses[name] = append(peerAddresses[name], address)
    }
  }
  pr.peersLock.RUnlock()
  result := map[string]map[string]bool{}
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    for _, address := range addresses {
      if resp, err := http.Post("http://"+address+"/client/track/headers/add/"+headers, "plain/text", nil); err == nil {
        util.CloseResponse(resp)
        result[name][address] = resp.StatusCode == http.StatusAccepted
        log.Printf("Pushed tracking headers %s to peer %s address %s with response %s\n", headers, name, address, resp.Status)
      } else {
        result[name][address] = false
        log.Printf("Failed to add tracking headers %s to peer %s address %s\n", headers, name, address)
      }
    }
  }
  return result
}

func addPeer(w http.ResponseWriter, r *http.Request) {
  p := &Peer{}
  msg := ""
  if err := util.ReadJsonPayload(r, p); err == nil {
    pr := getPortRegistry(r)
    pr.addPeer(p)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Added Peer: %+v", *p)
    pr.peersLock.RLock()
    payload := map[string]interface{}{"targets": pr.peerTargets[p.Name], "jobs": pr.peerJobs[p.Name]}
    pr.peersLock.RUnlock()
    fmt.Fprintln(w, util.ToJSON(payload))
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
    fmt.Fprintln(w, msg)
  }
  util.AddLogMessage(msg, r)
}

func removePeer(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if address, present := util.GetStringParam(r, "address"); present {
      if getPortRegistry(r).removePeer(peerName, address) {
        w.WriteHeader(http.StatusAccepted)
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
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func checkPeerHealth(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  result := getPortRegistry(r).checkPeerHealth(peerName, address)
  w.WriteHeader(http.StatusOK)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func cleanupUnhealthyPeers(w http.ResponseWriter, r *http.Request) {
  getPortRegistry(r).cleanupUnhealthyPeers(util.GetStringParamValue(r, "peer"))
  w.WriteHeader(http.StatusOK)
  msg := "Cleaned up unhealthy peers"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func storeInPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  keys, _ := util.GetListParam(r, "keys")
  if peerName != "" && address != "" && len(keys) > 0 {
    data := util.Read(r.Body)
    getPortRegistry(r).storeInPeerLocker(peerName, address, keys, data)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data stored for keys %+v", peerName, keys)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removeFromPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  keys, _ := util.GetListParam(r, "keys")
  if peerName != "" && address != "" && len(keys) > 0 {
    getPortRegistry(r).removeFromPeerLocker(peerName, address, keys)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data removed for keys %+v", peerName, keys)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func lockInPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  keys, _ := util.GetListParam(r, "keys")
  if peerName != "" && address != "" && len(keys) > 0 {
    getPortRegistry(r).lockInPeerLocker(peerName, address, keys)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data for keys %+v is locked", peerName, keys)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  result := getPortRegistry(r).clearLocker(peerName, address)
  w.WriteHeader(http.StatusAccepted)
  util.WriteJsonPayload(w, result)
  if peerName != "" {
    if address != "" {
      msg = fmt.Sprintf("Peer %s Instance %s data cleared", peerName, address)
    } else {
      msg = fmt.Sprintf("Peer %s data cleared for all instances", peerName)
    }
  } else {
    msg = "All peer lockers cleared"
  }
  util.AddLogMessage(msg, r)
}

func getPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  w.WriteHeader(http.StatusOK)
  util.WriteJsonPayload(w, getPortRegistry(r).getPeerLocker(peerName, address))
  if peerName != "" {
    msg = fmt.Sprintf("Peer %s data reported", peerName)
  } else {
    msg = "All peer lockers reported"
  }
  util.AddLogMessage(msg, r)
}

func getLockerTargetResultsSummary(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  util.WriteJsonPayload(w, getPortRegistry(r).getLockerClientResultsSummary())
  util.AddLogMessage("Reported locker targets results summary", r)
}

func addPeerTarget(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  t := &PeerTarget{}
  if err := util.ReadJsonPayload(r, t); err == nil {
    if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
      log.Println(err)
    } else {
      result := getPortRegistry(r).addPeerTarget(peerName, t)
      w.WriteHeader(http.StatusAccepted)
      msg = util.ToJSON(result)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to parse json"
    log.Println(err)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removePeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets, _ := util.GetListParam(r, "targets")
  result := getPortRegistry(r).removePeerTargets(peerName, targets)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopPeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := getPortRegistry(r).stopPeerTargets(peerName, targets)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := getPortRegistry(r).invokePeerTargets(peerName, targets)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getPeerTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    peerTargets := getPortRegistry(r).getPeerTargets(peerName)
    if peerTargets != nil {
      msg = fmt.Sprintf("Reporting peer targets for peer: %s", peerName)
      util.WriteJsonPayload(w, peerTargets)
    } else {
      w.WriteHeader(http.StatusNoContent)
      msg = fmt.Sprintf("Peer not found: %s\n", peerName)
      fmt.Fprintln(w, "[]")
    }
  } else {
    msg = "Reporting all peer targets"
    pr := getPortRegistry(r)
    pr.peersLock.RLock()
    peerTargets := pr.peerTargets
    pr.peersLock.RUnlock()
    util.WriteJsonPayload(w, peerTargets)
  }
  util.AddLogMessage(msg, r)
}

func enableAllTargetsResultsCollection(w http.ResponseWriter, r *http.Request) {
  result := getPortRegistry(r).enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), true)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
  result := getPortRegistry(r).enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), false)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func addPeerJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  if job, err := job.ParseJob(r); err == nil {
    result := getPortRegistry(r).addPeerJob(peerName, &PeerJob{*job})
    w.WriteHeader(http.StatusAccepted)
    msg = util.ToJSON(result)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to read job"
    log.Println(err)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removePeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs, _ := util.GetListParam(r, "jobs")
  result := getPortRegistry(r).removePeerJobs(peerName, jobs)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs := util.GetStringParamValue(r, "jobs")
  result := getPortRegistry(r).stopPeerJobs(peerName, jobs)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func runPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs := util.GetStringParamValue(r, "jobs")
  result := getPortRegistry(r).invokePeerJobs(peerName, jobs)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getPeers(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  util.WriteJsonPayload(w, pr.peers)
}

func getPeerJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    peerJobs := getPortRegistry(r).getPeerJobs(peerName)
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
    pr := getPortRegistry(r)
    pr.peersLock.RLock()
    peerJobs := pr.peerJobs
    pr.peersLock.RUnlock()
    util.WriteJsonPayload(w, peerJobs)
  }
  util.AddLogMessage(msg, r)
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
  getPortRegistry(r).reset()
  w.WriteHeader(http.StatusAccepted)
  msg := "Peers cleared"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  result := getPortRegistry(r).clearPeerTargets(peerName)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func clearPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  result := getPortRegistry(r).clearPeerJobs(peerName)
  w.WriteHeader(http.StatusAccepted)
  msg := util.ToJSON(result)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func addPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if h, present := util.GetStringParam(r, "headers"); present {
    result := getPortRegistry(r).addPeersTrackingHeaders(h)
    w.WriteHeader(http.StatusAccepted)
    msg = util.ToJSON(result)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "{\"error\":\"No headers given\"}"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func GetPeers(name string, r *http.Request) map[string]string {
  peers := getPortRegistry(r).peers[name]
  data := map[string]string{}
  for _, pod := range peers.Pods {
    data[pod.Name] = pod.Address
  }
  return data
}
