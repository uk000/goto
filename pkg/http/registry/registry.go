package registry

import (
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/http/client/target"
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
  Data          string    `json:"data"`
  Locked        bool
  FirstReported time.Time `json:"firstReported"`
  LastReported  time.Time `json:"lastReported"`
}

type PeerLocker map[string]map[string]*LockerData

type PortRegistry struct {
  peers       map[string]*Peers
  peerTargets map[string]PeerTargets
  peerJobs    map[string]PeerJobs
  peerLocker  map[string]PeerLocker
  lock        sync.RWMutex
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
  util.AddRoute(peersRouter, "/{peer}/health/cleanup", cleanupUnhealthyPeers, "POST")
  util.AddRoute(peersRouter, "/health/cleanup", cleanupUnhealthyPeers, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/store/{key}", storeInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/remove/{key}", removeFromPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/lock/{key}", lockKeyInPeerLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/locker/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/lockers/clear", clearLocker, "POST")
  util.AddRoute(peersRouter, "/{peer}/{address}/locker", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/{peer}/locker", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/lockers", getPeerLocker, "GET")
  util.AddRoute(peersRouter, "/lockers/summary", getLockerClientResultsSummary, "GET")

  util.AddRoute(peersRouter, "/{peer}/targets/add", addPeerTarget, "POST")
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
  pr.lock.Lock()
  defer pr.lock.Unlock()
  pr.peers = map[string]*Peers{}
  pr.peerTargets = map[string]PeerTargets{}
  pr.peerJobs = map[string]PeerJobs{}
  pr.peerLocker = map[string]PeerLocker{}
}

func (pr *PortRegistry) init() {
  pr.lock.Lock()
  isEmpty := pr.peers == nil
  pr.lock.Unlock()
  if isEmpty {
    pr.reset()
  }
}

func (pr *PortRegistry) addPeer(p *Peer) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  global.EnableAdminLogging = false
  if pr.peers[p.Name] == nil {
    pr.peers[p.Name] = &Peers{Name: p.Name, Namespace: p.Namespace, Pods: map[string]Pod{}}
  }
  pr.peers[p.Name].Pods[p.Address] = Pod{p.Name, p.Address}
  if pr.peerTargets[p.Name] == nil {
    pr.peerTargets[p.Name] = PeerTargets{}
  }
  if pr.peerJobs[p.Name] == nil {
    pr.peerJobs[p.Name] = PeerJobs{}
  }
  if pr.peerLocker[p.Name] == nil {
    pr.peerLocker[p.Name] = PeerLocker{}
  }
}

func (pr *PortRegistry) removePeer(name string, address string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
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
  client := http.Client{Timeout: 2*time.Second,}
  if strings.EqualFold(method, "GET") {
    if resp, err := client.Get("http://"+peerAddress+api);  err == nil {
      defer resp.Body.Close()
      return true
    }
  } else {
    if resp, err := client.Post("http://"+peerAddress+api, "plain/text", nil);  err == nil {
      defer resp.Body.Close()
      return resp.StatusCode == expectedStatus
    }
  }
  return false
}

func (pr *PortRegistry) checkPeerHealth(name string, address string) bool {
  pr.lock.RLock()
  defer pr.lock.RUnlock()
  if pr.peers[name] != nil {
    if _, present := pr.peers[name].Pods[address]; present {
      return invokePeerAPI("GET", address, "/health", 200)
    }
  }
  return false
}

func (pr *PortRegistry) cleanupUnhealthyPeers(name string) map[string]map[string]bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
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

func (pr *PortRegistry) storeInPeerLocker(name, address, key, value string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  peerLocker := pr.peerLocker[name]
  if peerLocker == nil {
    peerLocker = PeerLocker{}
    pr.peerLocker[name] = peerLocker
  }
  instanceLocker := peerLocker[address]
  if instanceLocker == nil {
    instanceLocker = map[string]*LockerData{}
    peerLocker[address] = instanceLocker
  }
  now := time.Now()
  if instanceLocker[key] != nil && instanceLocker[key].Locked {
    instanceLocker[key+"_last"] = instanceLocker[key]
    instanceLocker[key] = nil
  }
  if instanceLocker[key] == nil {
    instanceLocker[key] = &LockerData{}
    instanceLocker[key].FirstReported = now
  }
  instanceLocker[key].Data = value
  instanceLocker[key].LastReported = now
}

func (pr *PortRegistry) removeFromPeerLocker(name, address, key string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerLocker[name] != nil {
    if pr.peerLocker[name][address] != nil {
      delete(pr.peerLocker[name][address], key)
      if len(pr.peerLocker[name][address]) == 0 {
        delete(pr.peerLocker[name], address)
      }
      if len(pr.peerLocker[name]) == 0 {
        delete(pr.peerLocker, name)
      }
    }
  }
}

func (pr *PortRegistry) lockKeyInPeerLocker(name, address, key string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerLocker[name] != nil && pr.peerLocker[name][address] != nil && pr.peerLocker[name][address][key] != nil {
    pr.peerLocker[name][address][key].Locked = true
  }
}

func (pr *PortRegistry) clearLocker(peerName, peerAddress string) map[string]map[string]bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  peersToClear := map[string][]string{}
  if peerName != "" {
    peersToClear[peerName] = []string{}
    if peerAddress != "" {
      pr.peerLocker[peerName][peerAddress] = map[string]*LockerData{}
      if pr.peers[peerName] != nil {
        if _, present := pr.peers[peerName].Pods[peerAddress]; present {
          peersToClear[peerName] = append(peersToClear[peerName], peerAddress)
        }
      }
    } else {
      pr.peerLocker[peerName] = PeerLocker{}
      if pr.peers[peerName] != nil {
        for address := range pr.peers[peerName].Pods {
          peersToClear[peerName] = append(peersToClear[peerName], address)
        }
      }
    }
  } else {
    pr.peerLocker = map[string]PeerLocker{}
    for name, peer := range pr.peers {
      peersToClear[name] = []string{}
      for address := range peer.Pods {
        peersToClear[name] = append(peersToClear[name], address)
      }
    }
  }
  result := map[string]map[string]bool{}
  for name, peerAddresses := range peersToClear {
    result[name] = map[string]bool{}
    for _, address := range peerAddresses {
      if invokePeerAPI("POST", address, "/client/results/clear", 200) {
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
  pr.lock.RLock()
  defer pr.lock.RUnlock()
  if name != "" {
    if address != "" && pr.peerLocker[name] != nil {
      return pr.peerLocker[name][address]
    } else {
      return pr.peerLocker[name]
    }
  } else {
    return pr.peerLocker
  }
}

func (pr *PortRegistry) getLockerClientResultsSummary() interface{} {
  pr.lock.RLock()
  defer pr.lock.RUnlock()
  summary := map[string]*target.TargetResults{}
  for peer, peerLocker := range pr.peerLocker {
    summary[peer] = &target.TargetResults{}
    target.InitResults(summary[peer])
    for _, instanceLocker := range peerLocker {
      if instanceLocker[constants.ClientResultsKey] != nil {
        if data := instanceLocker[constants.ClientResultsKey].Data; data != "" {
          result := &target.TargetResults{}
          if err := util.ReadJson(data, result); err == nil {
            target.AddDeltaResults(summary[peer], result)
          }
        }
      }
    }
  }
  return summary
}

func (pr *PortRegistry) addPeerTarget(peerName string, t *PeerTarget) map[string]bool {
  pr.lock.Lock()
  if pr.peerTargets[peerName] == nil {
    pr.peerTargets[peerName] = PeerTargets{}
  }
  pr.peerTargets[peerName][t.Name] = t
  peerPods := map[string]Pod{}
  if pr.peers[peerName] != nil {
    peerPods = pr.peers[peerName].Pods
  }
  pr.lock.Unlock()
  result := map[string]bool{}
  for address := range peerPods {
    if resp, err := http.Post("http://"+address+"/client/targets/add", "application/json", strings.NewReader(util.ToJSON(t))); err == nil {
      defer resp.Body.Close()
      result[address] = resp.StatusCode == 200
      log.Printf("Pushed target %s to peer %s address %s\n", t.Name, peerName, address)
    } else {
      result[address] = false
      log.Printf("Failed to pushed target %s to peer %s address %s error: %s\n", t.Name, peerName, address, err.Error())
    }
  }
  return result
}

func (pr *PortRegistry) removePeerTargets(peerName string, targets []string) map[string]map[string]bool {
  pr.lock.RLock()
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
  pr.lock.RUnlock()
  result := map[string]map[string]bool{}
  targetList := strings.Join(targets, ",")
  for name, addresses := range peerAddresses {
    result[name] = map[string]bool{}
    removed := true
    for _, address := range addresses {
      if resp, err := http.Post("http://"+address+"/client/targets/"+targetList+"/remove", "plain/text", nil); err == nil {
        defer resp.Body.Close()
        if resp.StatusCode == 200 {
          result[name][address] = true
          log.Printf("Removed targets %s from peer %s address %s\n", targetList, name, address)
        } else {
          result[name][address] = false
          removed = false
          log.Printf("Failed to remove targets %s from peer %s address %s, possibly due to running traffic\n", targetList, name, address)
        }
      } else {
        result[name][address] = false
        removed = false
        log.Println(err.Error())
      }
    }
    if removed {
      pr.lock.Lock()
      if pr.peerTargets[name] != nil {
        if len(targets) > 0 {
          for _, target := range targets {
            delete(pr.peerTargets[name], target)
          }
        } else {
          delete(pr.peerTargets, name)
        }
      }
      pr.lock.Unlock()
    }
  }
  return result
}

func (pr *PortRegistry) stopPeerTargets(peerName string, targets string) map[string]map[string]bool {
  pr.lock.RLock()
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
  pr.lock.RUnlock()
  result := map[string]map[string]bool{}
  if len(peerAddresses) > 0 {
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
          result[name][address] = resp.StatusCode == 200
          defer resp.Body.Close()
          log.Printf("Stopped targets %s from peer %s address %s\n", targets, name, address)
        } else {
          result[name][address] = false
          log.Printf("Failed to stop targets %s from peer %s address %s with error %s\n", targets, name, address, err.Error())
        }
      }
    }
  }
  return result
}

func (pr *PortRegistry) getPeerTargets(peerName string) PeerTargets {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  return pr.peerTargets[peerName]
}

func (pr *PortRegistry) addPeerJob(peerName string, job *PeerJob) {
  pr.lock.Lock()
  if pr.peerJobs[peerName] == nil {
    pr.peerJobs[peerName] = PeerJobs{}
  }
  pr.peerJobs[peerName][job.ID] = job
  peerPods := map[string]Pod{}
  if pr.peers[peerName] != nil {
    peerPods = pr.peers[peerName].Pods
  }
  pr.lock.Unlock()
  for address := range peerPods {
    if resp, err := http.Post("http://"+address+"/jobs/add", "application/json", strings.NewReader(util.ToJSON(job))); err == nil {
      defer resp.Body.Close()
      log.Printf("Pushed job %s to peer %s address %s\n", job.ID, peerName, address)
    } else {
      log.Println(err.Error())
    }
  }
}

func (pr *PortRegistry) removePeerJobs(peerName string, jobs []string) bool {
  removed := false
  pr.lock.RLock()
  peerPods := map[string]Pod{}
  if pr.peerJobs[peerName] != nil {
    if pr.peers[peerName] != nil {
      peerPods = pr.peers[peerName].Pods
    }
  }
  pr.lock.RUnlock()
  jobList := strings.Join(jobs, ",")
  for a := range peerPods {
    if resp, err := http.Post("http://"+a+"/jobs/"+jobList+"/remove", "plain/text", nil); err == nil {
      defer resp.Body.Close()
      if resp.StatusCode == 200 {
        log.Printf("Removed jobs %s from peer %s address %s\n", jobList, peerName, a)
        removed = true
      } else {
        log.Printf("Failed to remove jobs %s from peer %s address %s, possibly due to running traffic\n", jobList, peerName, a)
      }
    } else {
      log.Println(err.Error())
    }
  }
  if removed {
    pr.lock.Lock()
    if pr.peerJobs[peerName] != nil {
      for _, job := range jobs {
        delete(pr.peerJobs[peerName], job)
      }
    }
    pr.lock.Unlock()
  }
  return removed
}

func (pr *PortRegistry) stopPeerJobs(peerName string, jobs string) {
  pr.lock.Lock()
  peerPods := map[string]Pod{}
  if pr.peerJobs[peerName] != nil {
    if pr.peers[peerName] != nil {
      peerPods = pr.peers[peerName].Pods
    }
  }
  pr.lock.Unlock()
  for a := range peerPods {
    var resp *http.Response
    var err error
    if len(jobs) > 0 {
      resp, err = http.Post("http://"+a+"/jobs/"+jobs+"/stop", "plain/text", nil)
    } else {
      resp, err = http.Post("http://"+a+"/jobs/stop/all", "plain/text", nil)
    }
    if err == nil {
      defer resp.Body.Close()
      log.Printf("Stopped jobs %s from peer %s address %s\n", jobs, peerName, a)
    } else {
      log.Println(err.Error())
    }
  }
}

func (pr *PortRegistry) getPeerJobs(peerName string) PeerJobs {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  return pr.peerJobs[peerName]
}

func (pr *PortRegistry) invokePeerTargets(peerName string, targets string) map[string]map[string]bool {
  pr.lock.RLock()
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
  pr.lock.RUnlock()
  result := map[string]map[string]bool{}
  if len(peerAddresses) > 0 {
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
          result[name][address] = resp.StatusCode == 200
          defer resp.Body.Close()
          log.Printf("Invoked target %s on peer %s address %s\n", targets, name, address)
        } else {
          result[name][address] = false
          log.Printf("Failed to invoke target %s on peer %s address %s with error %s\n", targets, name, address, err.Error())
        }
      }
    }
  }
  return result
}

func (pr *PortRegistry) invokePeerJobs(peerName string, jobs string) error {
  pr.lock.Lock()
  peerPods := map[string]Pod{}
  if pr.peers[peerName] != nil {
    peerPods = pr.peers[peerName].Pods
  }
  pr.lock.Unlock()
  if len(peerPods) > 0 {
    for a := range peerPods {
      var resp *http.Response
      var err error
      if len(jobs) > 0 {
        resp, err = http.Post("http://"+a+"/jobs/"+jobs+"/run", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+a+"/jobs/run/all", "plain/text", nil)
      }
      if err == nil {
        defer resp.Body.Close()
        log.Printf("Invoked jobs %s on peer %s address %s\n", jobs, peerName, a)
      } else {
        log.Println(err.Error())
        return err
      }
    }
    return nil
  }
  return errors.New("Peer not found")
}

func (pr *PortRegistry) clearPeerTargets(peerName string) bool {
  cleared := true
  pr.lock.RLock()
  peerPods := map[string]Pod{}
  if pr.peers[peerName] != nil {
    peerPods = pr.peers[peerName].Pods
  }
  pr.lock.RUnlock()
  for address := range peerPods {
    if resp, err := http.Post("http://"+address+"/client/targets/clear", "plain/text", nil); err == nil {
      defer resp.Body.Close()
      if resp.StatusCode == 200 {
        log.Printf("Cleared targets from peer %s address %s\n", peerName, address)
      } else {
        cleared = false
        log.Printf("Failed to clear targets from peer %s address %s, possibly due to running traffic\n", peerName, address)
      }
    } else {
      cleared = false
      log.Printf("Failed to clear targets from peer %s address %s, error: %s\n", peerName, address, err.Error())
    }
  }
  if cleared {
    pr.lock.Lock()
    if _, present := pr.peerTargets[peerName]; present {
      delete(pr.peerTargets, peerName)
    }
    pr.lock.Unlock()
  }
  return cleared
}

func (pr *PortRegistry) clearAllPeerTargets() {
  pr.lock.Lock()
  peerAddresses := []string{}
  for peerName := range pr.peerTargets {
    if pr.peers[peerName] != nil {
      for a := range pr.peers[peerName].Pods {
        peerAddresses = append(peerAddresses, a)
      }
    }
  }
  pr.peerTargets = map[string]PeerTargets{}
  pr.lock.Unlock()
  for _, a := range peerAddresses {
    if resp, err := http.Post("http://"+a+"/client/targets/clear", "plain/text", nil); err == nil {
      defer resp.Body.Close()
      log.Printf("Cleared targets from peer address %s\n", a)
    } else {
      log.Println(err.Error())
    }
  }
}

func (pr *PortRegistry) clearPeerJobs(peerName string) bool {
  cleared := false
  pr.lock.RLock()
  peerPods := map[string]Pod{}
  if pr.peers[peerName] != nil {
    peerPods = pr.peers[peerName].Pods
  }
  pr.lock.RUnlock()
  for a := range peerPods {
    if resp, err := http.Post("http://"+a+"/jobs/clear", "plain/text", nil); err == nil {
      defer resp.Body.Close()
      if resp.StatusCode == 200 {
        log.Printf("Cleared jobs from peer %s address %s\n", peerName, a)
        cleared = true
      } else {
        log.Printf("Failed to clear jobs from peer %s address %s, possibly due to running traffic\n", peerName, a)
      }
    } else {
      log.Println(err.Error())
    }
  }
  if cleared {
    pr.lock.Lock()
    if _, present := pr.peerJobs[peerName]; present {
      delete(pr.peerJobs, peerName)
    }
    pr.lock.Unlock()
  }
  return cleared
}

func (pr *PortRegistry) clearAllPeerJobs() {
  pr.lock.Lock()
  peerAddresses := []string{}
  for peerName := range pr.peerJobs {
    if pr.peers[peerName] != nil {
      for a := range pr.peers[peerName].Pods {
        peerAddresses = append(peerAddresses, a)
      }
    }
  }
  pr.peerJobs = map[string]PeerJobs{}
  pr.lock.Unlock()
  for _, a := range peerAddresses {
    if resp, err := http.Post("http://"+a+"/jobs/clear", "plain/text", nil); err == nil {
      defer resp.Body.Close()
      log.Printf("Cleared jobs from peer address %s\n", a)
    } else {
      log.Println(err.Error())
    }
  }
}

func addPeer(w http.ResponseWriter, r *http.Request) {
  p := &Peer{}
  msg := ""
  if err := util.ReadJsonPayload(r, p); err == nil {
    pr := getPortRegistry(r)
    pr.addPeer(p)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Added Peer: %+v", *p)
    pr.lock.RLock()
    payload := map[string]interface{}{"targets": pr.peerTargets[p.Name], "jobs": pr.peerJobs[p.Name]}
    pr.lock.RUnlock()
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
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func checkPeerHealth(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if address, present := util.GetStringParam(r, "address"); present {
      if getPortRegistry(r).checkPeerHealth(peerName, address) {
        w.WriteHeader(http.StatusOK)
        msg = fmt.Sprintf("Peer is healthy: %s", peerName)
      } else {
        w.WriteHeader(http.StatusExpectationFailed)
        msg = fmt.Sprintf("Peer is unhealthy: %s", peerName)
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
  key := util.GetStringParamValue(r, "key")
  if peerName != "" && address != "" && key != "" {
    data := util.Read(r.Body)
    getPortRegistry(r).storeInPeerLocker(peerName, address, key, data)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Peer %s data stored for Key: %s", peerName, key)
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
  key := util.GetStringParamValue(r, "key")
  if peerName != "" && address != "" && key != "" {
      getPortRegistry(r).removeFromPeerLocker(peerName, address, key)
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Peer %s data removed for Key: %s", peerName, key)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func lockKeyInPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  key := util.GetStringParamValue(r, "key")
  if peerName != "" && address != "" && key != "" {
    getPortRegistry(r).lockKeyInPeerLocker(peerName, address, key)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Peer %s data for key: %s is locked", peerName, key)
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
  w.WriteHeader(http.StatusOK)
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

func getLockerClientResultsSummary(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  util.WriteJsonPayload(w, getPortRegistry(r).getLockerClientResultsSummary())
  util.AddLogMessage("Reported client results summary", r)
}

func addPeerTarget(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    t := &PeerTarget{}
    if err := util.ReadJsonPayload(r, t); err == nil {
      if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
        w.WriteHeader(http.StatusBadRequest)
        msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
        log.Println(err)
      } else {
        result := getPortRegistry(r).addPeerTarget(peerName, t)
        w.WriteHeader(http.StatusOK)
        msg = util.ToJSON(result)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "Failed to parse json"
      log.Println(err)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No peer given"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removePeerTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  targets, _ := util.GetListParam(r, "targets")
  result := getPortRegistry(r).removePeerTargets(peerName, targets)
  w.WriteHeader(http.StatusOK)
  msg = util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopPeerTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := getPortRegistry(r).stopPeerTargets(peerName, targets)
  w.WriteHeader(http.StatusOK)
  msg = util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func addPeerJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if job, err := job.ParseJob(r); err == nil {
      getPortRegistry(r).addPeerJob(peerName, &PeerJob{*job})
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Added peer job: %s\n", util.ToJSON(job))
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "Failed to read job"
      log.Println(err)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No peer given"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removePeerJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if jobs, present := util.GetListParam(r, "jobs"); present {
      if getPortRegistry(r).removePeerJobs(peerName, jobs) {
        w.WriteHeader(http.StatusOK)
        msg = fmt.Sprintf("Peer %s jobs %+v removed\n", peerName, jobs)
      } else {
        w.WriteHeader(http.StatusNotAcceptable)
        msg = fmt.Sprintf("Failed to remove jobs %s from peer %s, possibly due to running traffic", jobs, peerName)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "No jobs given"
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No peer given"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopPeerJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    jobs := util.GetStringParamValue(r, "jobs")
    getPortRegistry(r).stopPeerJobs(peerName, jobs)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Peer %s jobs %+v stopped\n", peerName, jobs)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No peer given"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := getPortRegistry(r).invokePeerTargets(peerName, targets)
  w.WriteHeader(http.StatusOK)
  msg = util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func runPeerJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    jobs := util.GetStringParamValue(r, "jobs")
    if err := getPortRegistry(r).invokePeerJobs(peerName, jobs); err == nil {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Jobs [%s] invoked on peer [%s]\n", jobs, peerName)
    } else {
      w.WriteHeader(http.StatusNotAcceptable)
      msg = fmt.Sprintf("Count not invoke jobs [%s] on peer [%s]\n", jobs, peerName)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No peers given"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getPeers(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.lock.RLock()
  defer pr.lock.RUnlock()
  util.WriteJsonPayload(w, pr.peers)
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
    pr.lock.RLock()
    peerTargets := pr.peerTargets
    pr.lock.RUnlock()
    util.WriteJsonPayload(w, peerTargets)
  }
  util.AddLogMessage(msg, r)
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
    pr.lock.RLock()
    peerJobs := pr.peerJobs
    pr.lock.RUnlock()
    util.WriteJsonPayload(w, peerJobs)
  }
  util.AddLogMessage(msg, r)
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
  getPortRegistry(r).reset()
  w.WriteHeader(http.StatusOK)
  msg := "Peers cleared"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if getPortRegistry(r).clearPeerTargets(peerName) {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Peer targets removed: %s\n", peerName)
    } else {
      w.WriteHeader(http.StatusNotAcceptable)
      msg = fmt.Sprintf("Failed to clear targets from peer %s, possibly due to running traffic\n", peerName)
    }
  } else {
    getPortRegistry(r).clearAllPeerTargets()
    w.WriteHeader(http.StatusOK)
    msg = "All peer targets removed"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func clearPeerJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if getPortRegistry(r).clearPeerJobs(peerName) {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Peer jobs removed: %s\n", peerName)
    } else {
      w.WriteHeader(http.StatusNotAcceptable)
      msg = fmt.Sprintf("Failed to clear jobs from peer %s, possibly due to running traffic\n", peerName)
    }
  } else {
    getPortRegistry(r).clearAllPeerJobs()
    w.WriteHeader(http.StatusOK)
    msg = "All peer jobs removed"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func GetPeers(name string, r *http.Request) map[string]string {
  peers := getPortRegistry(r).peers[name]
  data := map[string]string{}
  for _, pod := range peers.Pods {
    data[pod.Name] = pod.Address
  }
  return data
}
