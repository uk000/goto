package registry

import (
  "fmt"
  "goto/pkg/client/results"
  "goto/pkg/constants"
  "goto/pkg/events"
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/job"
  "goto/pkg/registry/locker"
  "goto/pkg/util"
  "log"
  "net"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"
)

type Peer struct {
  Name      string `json:"name"`
  Address   string `json:"address"`
  Pod       string `json:"pod"`
  Namespace string `json:"namespace"`
  Node      string `json:"node"`
  Cluster   string `json:"cluster"`
}

type PodEpoch struct {
  Epoch        int       `json:"epoch"`
  Name         string    `json:"name"`
  Address      string    `json:"address"`
  Node         string    `json:"node"`
  Cluster      string    `json:"cluster"`
  FirstContact time.Time `json:"firstContact"`
  LastContact  time.Time `json:"lastContact"`
}

type Pod struct {
  Name         string      `json:"name"`
  Address      string      `json:"address"`
  Node         string      `json:"node"`
  Cluster      string      `json:"cluster"`
  Healthy      bool        `json:"healthy"`
  CurrentEpoch PodEpoch    `json:"currentEpoch"`
  PastEpochs   []*PodEpoch `json:"pastEpochs"`
  URL          string      `json:"url"`
  Offline      bool        `json:"offline"`
  client       *http.Client
  lock         sync.RWMutex
}

type Peers struct {
  Name          string                 `json:"name"`
  Namespace     string                 `json:"namespace"`
  Pods          map[string]*Pod        `json:"pods"`
  PodEpochs     map[string][]*PodEpoch `json:"podEpochs"`
  eventsCounter int
  lock          sync.RWMutex
}

type PeerPods map[string][]*Pod
type PodResults map[string]bool
type PeerResults map[string]map[string]bool

type PeerTarget struct {
  invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PeerJob struct {
  job.Job
}

type PeerJobs map[string]*PeerJob
type PeerJobScripts map[string][]byte
type PeerFiles map[string][]byte

type PeerProbes struct {
  ReadinessProbe  string
  LivenessProbe   string
  ReadinessStatus int
  LivenessStatus  int
}

type PeerData struct {
  Targets             PeerTargets
  Jobs                PeerJobs
  JobScripts          PeerJobScripts
  Files               PeerFiles
  TrackingHeaders     string
  TrackingTimeBuckets string
  Probes              *PeerProbes
  Message             string
}

type Registry struct {
  peers                   map[string]*Peers
  peerTargets             map[string]PeerTargets
  peerJobs                map[string]PeerJobs
  peerJobScripts          map[string]PeerJobScripts
  peerFiles               map[string]PeerFiles
  peerTrackingHeaders     string
  trackingHeaders         []string
  crossTrackingHeaders    http.Header
  peerTrackingTimeBuckets string
  trackingTimeBuckets     [][]int
  peerProbes              *PeerProbes
  labeledLockers          *locker.LabeledLockers
  eventsCounter           int
  peersLock               sync.RWMutex
  lockersLock             sync.RWMutex
  eventsLock              sync.RWMutex
}

var (
  registry = &Registry{
    peers:          map[string]*Peers{},
    peerTargets:    map[string]PeerTargets{},
    peerJobs:       map[string]PeerJobs{},
    peerJobScripts: map[string]PeerJobScripts{},
    peerFiles:      map[string]PeerFiles{},
    labeledLockers: locker.NewLabeledPeersLockers(),
  }
)

func StoreEventInCurrentLocker(data interface{}) {
  event := data.(*events.Event)
  registry.eventsLock.Lock()
  registry.eventsCounter++
  registry.eventsLock.Unlock()
  registry.getCurrentLocker().StorePeerData(global.PeerName, "",
    []string{constants.LockerEventsKey, fmt.Sprintf("%s-%d", event.Title, registry.eventsCounter)}, util.ToJSON(event))
}

func (registry *Registry) reset() {
  registry.peersLock.Lock()
  registry.peers = map[string]*Peers{}
  registry.peerTargets = map[string]PeerTargets{}
  registry.peerJobs = map[string]PeerJobs{}
  registry.peerJobScripts = map[string]PeerJobScripts{}
  registry.peerFiles = map[string]PeerFiles{}
  registry.peersLock.Unlock()
  registry.lockersLock.Lock()
  registry.labeledLockers = locker.NewLabeledPeersLockers()
  registry.lockersLock.Unlock()
}

func (registry *Registry) getCurrentLocker() *locker.CombiLocker {
  registry.lockersLock.RLock()
  defer registry.lockersLock.RUnlock()
  return registry.labeledLockers.GetCurrentLocker()
}

func (registry *Registry) unsafeAddPeer(peer *Peer) {
  now := time.Now()
  if registry.peers[peer.Name] == nil {
    registry.peers[peer.Name] = &Peers{Name: peer.Name, Namespace: peer.Namespace, Pods: map[string]*Pod{}, PodEpochs: map[string][]*PodEpoch{}}
  }
  pod := &Pod{Name: peer.Pod, Address: peer.Address, URL: "http://" + peer.Address,
    Node: peer.Node, Cluster: peer.Cluster, Healthy: true,
    CurrentEpoch: PodEpoch{Name: peer.Pod, Address: peer.Address, Node: peer.Node, Cluster: peer.Cluster, FirstContact: now, LastContact: now}}
  registry.initHttpClientForPeerPod(pod)
  if podEpochs := registry.peers[peer.Name].PodEpochs[peer.Address]; podEpochs != nil {
    for _, oldEpoch := range podEpochs {
      pod.PastEpochs = append(pod.PastEpochs, oldEpoch)
    }
    pod.CurrentEpoch.Epoch = len(podEpochs)
  }
  registry.peers[peer.Name].PodEpochs[peer.Address] = append(registry.peers[peer.Name].PodEpochs[peer.Address], &pod.CurrentEpoch)

  registry.peers[peer.Name].Pods[peer.Address] = pod
  if registry.peerTargets[peer.Name] == nil {
    registry.peerTargets[peer.Name] = PeerTargets{}
  }
  if registry.peerJobs[peer.Name] == nil {
    registry.peerJobs[peer.Name] = PeerJobs{}
  }
  if registry.peerJobScripts[peer.Name] == nil {
    registry.peerJobScripts[peer.Name] = PeerJobScripts{}
  }
  if registry.peerFiles[peer.Name] == nil {
    registry.peerFiles[peer.Name] = PeerFiles{}
  }
}

func (registry *Registry) addPeer(peer *Peer) {
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()
  registry.unsafeAddPeer(peer)
  registry.getCurrentLocker().InitPeerLocker(peer.Name, peer.Address)
}

func (registry *Registry) GetPeer(peerName, peerAddress string) *Pod {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  if registry.peers[peerName] != nil && registry.peers[peerName].Pods[peerAddress] != nil {
    return registry.peers[peerName].Pods[peerAddress]
  }
  return nil
}

func (registry *Registry) rememberPeer(peer *Peer) {
  if pod := registry.GetPeer(peer.Name, peer.Address); pod != nil {
    pod.lock.Lock()
    pod.Healthy = true
    pod.Offline = false
    pod.CurrentEpoch.LastContact = time.Now()
    if pod.client == nil {
      registry.initHttpClientForPeerPod(pod)
    }
    pod.lock.Unlock()
  } else {
    registry.peersLock.Lock()
    defer registry.peersLock.Unlock()
    registry.unsafeAddPeer(peer)
  }
}

func (registry *Registry) removePeer(name string, address string) bool {
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()
  present := false
  if _, present = registry.peers[name]; present {
    delete(registry.peers[name].Pods, address)
  }
  registry.getCurrentLocker().DeactivateInstanceLocker(name, address)
  return present
}

func (registry *Registry) clearPeerEpochs() {
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()
  for name, peers := range registry.peers {
    for address := range peers.PodEpochs {
      currentPod := peers.Pods[address]
      if currentPod == nil {
        delete(peers.PodEpochs, address)
      } else {
        peers.PodEpochs[address] = []*PodEpoch{&currentPod.CurrentEpoch}
        currentPod.PastEpochs = []*PodEpoch{}
      }
    }
    if len(peers.Pods) == 0 {
      delete(registry.peers, name)
      registry.getCurrentLocker().RemovePeerLocker(name)
    }
  }
}

func (registry *Registry) initHttpClientForPeerPod(pod *Pod) {
  tr := &http.Transport{
    MaxIdleConns:        100,
    MaxIdleConnsPerHost: 100,
    IdleConnTimeout:     time.Minute * 10,
    Proxy:               http.ProxyFromEnvironment,
    DialContext: (&net.Dialer{
      Timeout:   time.Minute,
      KeepAlive: time.Minute * 5,
    }).DialContext,
  }
  pod.client = &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func getPodsArray(pods map[string]*Pod) []*Pod {
  copy := []*Pod{}
  for _, pod := range pods {
    if pod.client != nil {
      copy = append(copy, pod)
    }
  }
  return copy
}

func (registry *Registry) loadAllPeerPods() PeerPods {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  peerPods := PeerPods{}
  for name, peer := range registry.peers {
    peerPods[name] = getPodsArray(peer.Pods)
  }
  return peerPods
}

func (registry *Registry) loadPeerPods(peerName string, peerAddress string) PeerPods {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  peerPods := PeerPods{}
  if peerName != "" {
    if peerAddress != "" {
      if registry.peers[peerName] != nil {
        if pod := registry.peers[peerName].Pods[peerAddress]; pod != nil {
          if pod.client != nil {
            peerPods[peerName] = []*Pod{pod}
          }
        }
      }
    } else {
      if registry.peers[peerName] != nil {
        peerPods[peerName] = getPodsArray(registry.peers[peerName].Pods)
      }
    }
  } else {
    for name, peer := range registry.peers {
      peerPods[name] = getPodsArray(peer.Pods)
    }
  }
  return peerPods
}

func (registry *Registry) loadPodsForPeerWithData(peerName string, jobs ...bool) PeerPods {
  if peerName != "" {
    registry.peersLock.RLock()
    defer registry.peersLock.RUnlock()
    peerPods := PeerPods{}
    hasData := registry.peerTargets[peerName] != nil
    if len(jobs) > 0 && jobs[0] {
      hasData = registry.peerJobs[peerName] != nil || registry.peerJobScripts[peerName] != nil
    }
    if hasData {
      hasData = registry.peers[peerName] != nil
    }
    if hasData {
      peerPods[peerName] = getPodsArray(registry.peers[peerName].Pods)
    }
    return peerPods
  }
  return registry.loadAllPeerPods()
}

func (registry *Registry) callPeer(peerName, uri, method string, headers http.Header, payload []byte) map[string]map[string]interface{} {
  result := map[string]map[string]interface{}{}
  resultLock := sync.Mutex{}
  invokeForPodsWithHeadersAndPayload(registry.loadPeerPods(peerName, ""), method, uri, headers, payload, http.StatusOK, 0, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      resultLock.Lock()
      if result[peer] == nil {
        result[peer] = map[string]interface{}{}
      }
      result[peer][pod.Address] = response
      resultLock.Unlock()
    })
  return result
}

func (registry *Registry) checkPeerHealth(peerName string, peerAddress string) PeerResults {
  return invokeForPods(registry.loadPeerPods(peerName, peerAddress), "GET", "/health", http.StatusOK, 1, true,
    func(peer string, pod *Pod, response interface{}, err error) {
      pod.lock.Lock()
      pod.Healthy = err == nil
      pod.lock.Unlock()
      if err == nil {
        log.Printf("Peer %s Address %s is healthy\n", peer, pod.Address)
      } else {
        log.Printf("Peer %s Address %s is unhealthy, error: %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) cleanupUnhealthyPeers(peerName string) PeerResults {
  return invokeForPods(registry.loadPeerPods(peerName, ""), "GET", "/health", http.StatusOK, 1, true,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        pod.lock.Lock()
        pod.Healthy = true
        pod.PastEpochs = nil
        pod.lock.Unlock()
        log.Printf("Peer %s Address %s is healthy\n", peer, pod.Address)
      } else {
        log.Printf("Peer %s Address %s is unhealthy or unavailable, error: %s\n", peer, pod.Address, err.Error())
        registry.removePeer(peer, pod.Address)
      }
    })
}

func clearPeersResultsAndEvents(peersToClear PeerPods, r *http.Request) PeerResults {
  events.ClearEvents()
  result := invokeForPods(peersToClear, "POST", "/events/clear", http.StatusOK, 2, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Events cleared on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to clear events on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })

  events.SendRequestEventJSON(Registry_PeerEventsCleared,
    fmt.Sprintf("Events cleared on %d peer pods", len(peersToClear)), result, r)

  result = invokeForPods(peersToClear, "POST", "/client/results/clear", http.StatusOK, 2, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Results cleared on peer %s address %s\n", peer, pod.Address)

      } else {
        log.Printf("Failed to clear results on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
  events.SendRequestEventJSON(Registry_PeerResultsCleared,
    fmt.Sprintf("Results cleared on %d peer pods", len(peersToClear)), result, r)
  return result
}

func (registry *Registry) clearLocker(peerName, peerAddress string, r *http.Request) PeerResults {
  peersToClear := PeerPods{}
  if peerName != "" && peerAddress != "" {
    if registry.getCurrentLocker().ClearInstanceLocker(peerName, peerAddress) {
      peersToClear = registry.loadPeerPods(peerName, peerAddress)
    }
  } else {
    if registry.getCurrentLocker().InitPeerLocker(peerName, "") {
      peersToClear = registry.loadPeerPods(peerName, "")
    }
  }
  return clearPeersResultsAndEvents(peersToClear, r)
}

func (registry *Registry) addPeerTarget(peerName string, target *PeerTarget) PeerResults {
  registry.peersLock.Lock()
  peerPods := PeerPods{}
  if peerName != "" {
    if registry.peerTargets[peerName] == nil {
      registry.peerTargets[peerName] = PeerTargets{}
    }
    registry.peerTargets[peerName][target.Name] = target
    if registry.peers[peerName] != nil {
      peerPods[peerName] = getPodsArray(registry.peers[peerName].Pods)
    }
  } else {
    for name, peer := range registry.peers {
      if registry.peerTargets[name] == nil {
        registry.peerTargets[name] = PeerTargets{}
      }
      registry.peerTargets[name][target.Name] = target
      peerPods[name] = getPodsArray(peer.Pods)
    }
  }
  registry.peersLock.Unlock()
  return invokeForPodsWithPayload(peerPods, "POST", "/client/targets/add", util.ToJSON(target), http.StatusOK, 1, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        if global.EnableRegistryLogs {
          log.Printf("Pushed target %s to peer %s address %s\n", target.Name, peer, pod.Address)
        }
      } else {
        log.Printf("Failed to push target %s to peer %s address %s with error: %s\n", target.Name, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) removePeerTargets(peerName string, targets []string) PeerResults {
  targetList := strings.Join(targets, ",")
  removed := true
  return invokeForPods(registry.loadPodsForPeerWithData(peerName),
    "POST", fmt.Sprintf("/client/targets/%s/remove", targetList), http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        if global.EnableRegistryLogs {
          log.Printf("Removed targets %s from peer %s address %s\n", targetList, peer, pod.Address)
        }
      } else {
        removed = false
        log.Printf("Failed to remove targets %s from peer %s address %s with error %s\n", targetList, peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if removed {
        registry.peersLock.Lock()
        if registry.peerTargets[peer] != nil {
          if len(targets) > 0 {
            for _, target := range targets {
              delete(registry.peerTargets[peer], target)
            }
          } else {
            delete(registry.peerTargets, peer)
          }
        }
        registry.peersLock.Unlock()
      }
      removed = true
    })
}

func (registry *Registry) clearPeerTargets(peerName string) PeerResults {
  cleared := true
  return invokeForPods(registry.loadPodsForPeerWithData(peerName), "POST", "/client/targets/clear", http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Cleared targets from peer %s address %s\n", peer, pod.Address)
      } else {
        cleared = false
        log.Printf("Failed to clear targets from peer %s address %s, error: %s\n", peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if cleared {
        registry.peersLock.Lock()
        delete(registry.peerTargets, peer)
        registry.peersLock.Unlock()
      }
      cleared = true
    })
}

func (registry *Registry) stopPeerTargets(peerName string, targets string) PeerResults {
  uri := ""
  if len(targets) > 0 {
    uri = "/client/targets/" + targets + "/stop"
  } else {
    uri = "/client/targets/stop/all"
  }
  return invokeForPods(registry.loadPodsForPeerWithData(peerName), "POST", uri, http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Stopped targets %s from peer %s address %s\n", targets, peer, pod.Address)
      } else {
        log.Printf("Failed to stop targets %s from peer %s address %s with error %s\n", targets, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) enableAllOrInvocationsTargetsResultsCollection(enable string, all bool) PeerResults {
  uri := "/client/results/"
  if all {
    results.EnableAllTargetResults(util.IsYes(enable))
    uri += "all/"
  } else {
    results.EnableInvocationResults(util.IsYes(enable))
    uri += "invocations/"
  }
  uri += enable
  return invokeForPods(registry.loadAllPeerPods(), "POST", uri, http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Changed targets results collection on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to change targets Results Collection on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) getPeerTargets(peerName string) PeerTargets {
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()
  return registry.peerTargets[peerName]
}

func (registry *Registry) addPeerJob(peerName string, job *PeerJob) PeerResults {
  registry.peersLock.Lock()
  peerPods := PeerPods{}
  if peerName != "" {
    if registry.peerJobs[peerName] == nil {
      registry.peerJobs[peerName] = PeerJobs{}
    }
    registry.peerJobs[peerName][job.Name] = job
    if registry.peers[peerName] != nil {
      peerPods[peerName] = getPodsArray(registry.peers[peerName].Pods)
    }
  } else {
    for name, peer := range registry.peers {
      if registry.peerJobs[name] == nil {
        registry.peerJobs[name] = PeerJobs{}
      }
      registry.peerJobs[name][job.Name] = job
      peerPods[name] = getPodsArray(peer.Pods)
    }
  }
  registry.peersLock.Unlock()
  return invokeForPodsWithPayload(peerPods, "POST", "/jobs/add", util.ToJSON(job), http.StatusOK, 1, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Pushed job %s to peer %s address %s\n", job.Name, peer, pod.Address)
      } else {
        log.Printf("Failed to push job %s to peer %s address %s with error %s\n", job.Name, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) addPeerJobScriptOrFile(peerName string, filePath, fileName string, content []byte, script bool) PeerResults {
  registry.peersLock.Lock()
  storePeerFile := func(peerName, fileName string) {
    if script {
      if registry.peerJobScripts[peerName] == nil {
        registry.peerJobScripts[peerName] = PeerJobScripts{}
      }
      registry.peerJobScripts[peerName][fileName] = content
    } else {
      if registry.peerFiles[peerName] == nil {
        registry.peerFiles[peerName] = PeerFiles{}
      }
      registry.peerJobScripts[peerName][fileName] = content
    }
  }
  nameKey := fileName
  if !script {
    nameKey = util.BuildFilePath(filePath, fileName)
  }
  peerPods := PeerPods{}
  if peerName != "" {
    storePeerFile(peerName, nameKey)
    if registry.peers[peerName] != nil {
      peerPods[peerName] = getPodsArray(registry.peers[peerName].Pods)
    }
  } else {
    for peerName, peer := range registry.peers {
      storePeerFile(peerName, nameKey)
      peerPods[peerName] = getPodsArray(peer.Pods)
    }
  }
  registry.peersLock.Unlock()
  uri := ""
  if script {
    uri = "/jobs/add/script/" + fileName
  } else {
    uri = fmt.Sprintf("/jobs/store/file/%s?path=%s", fileName, filePath)
  }
  return invokeForPodsWithHeadersAndPayload(peerPods, "POST", uri, nil, content, http.StatusOK, 1, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Pushed job file [%s] with path [%s] to peer [%s] address [%s]\n", fileName, filePath, peer, pod.Address)
      } else {
        log.Printf("Failed to push job file [%s] with path [%s] to peer [%s] address [%s] with error %s\n", fileName, filePath, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) removePeerJobs(peerName string, jobs []string) PeerResults {
  jobList := strings.Join(jobs, ",")
  removed := true
  return invokeForPods(registry.loadPodsForPeerWithData(peerName, true),
    "POST", fmt.Sprintf("/jobs/%s/remove", jobList), http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Removed jobs %s from peer %s address %s\n", jobList, peer, pod.Address)
      } else {
        removed = false
        log.Printf("Failed to remove jobs %s from peer %s address %s with error %s\n", jobList, peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if removed {
        registry.peersLock.Lock()
        if registry.peerJobs[peer] != nil {
          for _, job := range jobs {
            delete(registry.peerJobs[peer], job)
          }
        } else {
          delete(registry.peerJobs, peer)
        }
        registry.peersLock.Unlock()
      }
      removed = true
    })
}

func (registry *Registry) stopPeerJobs(peerName string, jobs string) PeerResults {
  uri := ""
  if len(jobs) > 0 {
    uri = "/jobs/" + jobs + "/stop"
  } else {
    uri = "/jobs/stop/all"
  }
  return invokeForPods(registry.loadPodsForPeerWithData(peerName, true), "POST", uri, http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Stopped jobs %s from peer %s address %s\n", jobs, peer, pod.Address)
      } else {
        log.Printf("Failed to stop jobs %s from peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) getPeerJobs(peerName string) PeerJobs {
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()
  return registry.peerJobs[peerName]
}

func (registry *Registry) invokePeerTargets(peerName string, targets string) PeerResults {
  uri := ""
  if len(targets) > 0 {
    uri = "/client/targets/" + targets + "/invoke"
  } else {
    uri = "/client/targets/invoke/all"
  }
  return invokeForPods(registry.loadPodsForPeerWithData(peerName), "POST", uri, http.StatusOK, 1, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Invoked target %s on peer %s address %s\n", targets, peer, pod.Address)
      } else {
        log.Printf("Failed to invoke targets %s on peer %s address %s with error %s\n", targets, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) invokePeerJobs(peerName string, jobs string) PeerResults {
  uri := ""
  if len(jobs) > 0 {
    uri = "/jobs/" + jobs + "/run"
  } else {
    uri = "/jobs/run/all"
  }
  return invokeForPods(registry.loadPodsForPeerWithData(peerName, true), "POST", uri, http.StatusOK, 1, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Invoked jobs %s on peer %s address %s\n", jobs, peer, pod.Address)
      } else {
        log.Printf("Failed to invoke jobs %s on peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) clearPeerJobs(peerName string) PeerResults {
  cleared := true
  return invokeForPods(registry.loadPodsForPeerWithData(peerName, true), "POST", "/jobs/clear", http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Cleared jobs from peer %s address %s\n", peer, pod.Address)
      } else {
        cleared = false
        log.Printf("Failed to clear jobs from peer %s address %s, error: %s\n", peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if cleared {
        registry.peersLock.Lock()
        delete(registry.peerJobs, peer)
        registry.peersLock.Unlock()
      }
      cleared = true
    })
}

func (registry *Registry) addPeersTrackingHeaders(headers string) PeerResults {
  registry.peerTrackingHeaders = headers
  registry.trackingHeaders, registry.crossTrackingHeaders = util.ParseTrackingHeaders(headers)
  return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/headers/"+headers, http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Pushed tracking headers %s to peer %s address %s\n", headers, peer, pod.Address)
      } else {
        log.Printf("Failed to add tracking headers %s to peer %s address %s with error %s\n", headers, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) clearPeersTrackingHeaders() PeerResults {
  registry.peerTrackingHeaders = ""
  registry.trackingHeaders = []string{}
  registry.crossTrackingHeaders = make(http.Header)
  return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/headers/clear", http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Cleared tracking headers on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to clear tracking headers on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) addPeersTrackingTimeBuckets(b string) PeerResults {
  registry.peerTrackingTimeBuckets = b
  buckets, ok := util.ParseTimeBuckets(b)
  if !ok {
    return nil
  }
  registry.trackingTimeBuckets = buckets
  return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/time/"+b, http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Pushed tracking time buckets %s to peer %s address %s\n", b, peer, pod.Address)
      } else {
        log.Printf("Failed to add tracking time buckets %s to peer %s address %s with error %s\n", b, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) clearPeersTrackingTimeBuckets() PeerResults {
  registry.peerTrackingTimeBuckets = ""
  registry.trackingTimeBuckets = [][]int{}
  return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/time/clear", http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Cleared tracking time buckets on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to clear tracking time buckets on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) setProbe(isReadiness bool, uri string, status int) {
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()
  if registry.peerProbes == nil {
    registry.peerProbes = &PeerProbes{}
  }
  if isReadiness {
    if uri != "" {
      registry.peerProbes.ReadinessProbe = uri
    }
    if status > 0 {
      registry.peerProbes.ReadinessStatus = status
    } else if registry.peerProbes.ReadinessStatus <= 0 {
      registry.peerProbes.ReadinessStatus = 200
    }
  } else {
    if uri != "" {
      registry.peerProbes.LivenessProbe = uri
    }
    if status > 0 {
      registry.peerProbes.LivenessStatus = status
    } else if registry.peerProbes.LivenessStatus <= 0 {
      registry.peerProbes.LivenessStatus = 200
    }
  }

}

func (registry *Registry) sendProbe(probeType, uri string) PeerResults {
  return invokeForPods(registry.loadAllPeerPods(), "POST", fmt.Sprintf("/probes/%s/set?uri=%s", probeType, uri), http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Pushed %s URI %s to peer %s address %s\n", probeType, uri, peer, pod.Address)
      } else {
        log.Printf("Failed to push %s URI %s to peer %s address %s with error %s\n", probeType, uri, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) sendProbeStatus(probeType string, status int) PeerResults {
  return invokeForPods(registry.loadAllPeerPods(), "POST", fmt.Sprintf("/probes/%s/set/status=%d", probeType, status), http.StatusOK, 3, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Pushed %s Status %d to peer %s address %s\n", probeType, status, peer, pod.Address)
      } else {
        log.Printf("Failed to push %s Status %d to peer %s address %s with error %s\n", probeType, status, peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) flushPeersEvents() PeerResults {
  return invokeForPods(registry.loadAllPeerPods(), "POST", "/events/flush", http.StatusOK, 2, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Flushed events on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to flush events on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) clearPeersEvents() PeerResults {
  registry.getCurrentLocker().ClearPeerEvents()
  return invokeForPods(registry.loadAllPeerPods(), "POST", "/events/clear", http.StatusOK, 2, false,
    func(peer string, pod *Pod, response interface{}, err error) {
      if err == nil {
        log.Printf("Cleared events on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to clear events on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (registry *Registry) preparePeerStartupData(peer *Peer, peerData *PeerData) {
  peerData.Targets = registry.peerTargets[peer.Name]
  peerData.Jobs = registry.peerJobs[peer.Name]
  peerData.JobScripts = registry.peerJobScripts[peer.Name]
  peerData.Files = registry.peerFiles[peer.Name]
  peerData.TrackingHeaders = registry.peerTrackingHeaders
  peerData.TrackingTimeBuckets = registry.peerTrackingTimeBuckets
  peerData.Probes = registry.peerProbes
}

func (registry *Registry) clonePeersFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers"); err == nil {
    peers := map[string]*Peers{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peers); err == nil {
      for _, peer := range peers {
        for _, pod := range peer.Pods {
          pod.Offline = true
        }
      }
      registry.peersLock.Lock()
      registry.peers = peers
      registry.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (registry *Registry) cloneLockersFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/lockers?data=y"); err == nil {
    lockers := map[string]*locker.CombiLocker{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &lockers); err == nil {
      registry.lockersLock.Lock()
      registry.labeledLockers.ReplaceLockers(lockers)
      registry.lockersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (registry *Registry) clonePeersTargetsFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/targets"); err == nil {
    peerTargets := map[string]PeerTargets{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peerTargets); err == nil {
      registry.peersLock.Lock()
      registry.peerTargets = peerTargets
      registry.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (registry *Registry) clonePeersJobsFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/jobs"); err == nil {
    peerJobs := map[string]PeerJobs{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peerJobs); err == nil {
      registry.peersLock.Lock()
      registry.peerJobs = peerJobs
      registry.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (registry *Registry) clonePeersTrackingHeadersFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/track/headers"); err == nil {
    peerTrackingHeaders := ""
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peerTrackingHeaders); err == nil {
      registry.peersLock.Lock()
      registry.peerTrackingHeaders = peerTrackingHeaders
      registry.trackingHeaders, registry.crossTrackingHeaders = util.ParseTrackingHeaders(peerTrackingHeaders)
      registry.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (registry *Registry) clonePeersProbesFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/probes"); err == nil {
    peerProbes := &PeerProbes{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, peerProbes); err == nil {
      registry.peersLock.Lock()
      registry.peerProbes = peerProbes
      registry.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func addPeer(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  peer := &Peer{}
  peerData := &PeerData{}
  msg := ""
  payload := util.Read(r.Body)
  if err := util.ReadJson(payload, peer); err == nil {
    if peerName == "" {
      registry.addPeer(peer)
      registry.peersLock.RLock()
      registry.preparePeerStartupData(peer, peerData)
      registry.peersLock.RUnlock()
      msg = fmt.Sprintf("Added Peer: %+v", *peer)
      events.SendRequestEventJSON(Registry_PeerAdded, peer.Name, peer, r)
    } else {
      registry.rememberPeer(peer)
      msg = fmt.Sprintf("Remembered Peer: %+v", *peer)
      peerData.Message = msg
    }
    w.WriteHeader(http.StatusOK)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
    events.SendRequestEventJSON(Registry_PeerRejected, err.Error(),
      map[string]interface{}{"error": err.Error(), "payload": payload}, r)
    peerData.Message = msg
  }
  fmt.Fprintln(w, util.ToJSON(peerData))
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func removePeer(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if address, present := util.GetStringParam(r, "address"); present {
      if registry.removePeer(peerName, address) {
        w.WriteHeader(http.StatusOK)
        msg = fmt.Sprintf("Peer Removed: %s", peerName)
        events.SendRequestEvent(Registry_PeerRemoved, peerName, r)
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
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func checkPeerHealth(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  result := registry.checkPeerHealth(peerName, address)
  events.SendRequestEventJSON(Registry_CheckedPeersHealth,
    fmt.Sprintf("Checked health on %d peers", len(result)), result, r)
  util.WriteJsonPayload(w, result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(util.ToJSON(result), r)
  }
}

func cleanupUnhealthyPeers(w http.ResponseWriter, r *http.Request) {
  result := registry.cleanupUnhealthyPeers(util.GetStringParamValue(r, "peer"))
  events.SendRequestEventJSON(Registry_CleanedUpUnhealthyPeers,
    fmt.Sprintf("Checked health on %d peers", len(result)), result, r)
  util.WriteJsonPayload(w, result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(util.ToJSON(result), r)
  }
}

func getPeers(w http.ResponseWriter, r *http.Request) {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  util.WriteJsonPayload(w, registry.peers)
}

func GetPeers(name string, r *http.Request) map[string]string {
  peers := registry.peers[name]
  data := map[string]string{}
  for _, pod := range peers.Pods {
    data[pod.Name] = pod.Address
  }
  return data
}

func openLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  if label != "" {
    registry.lockersLock.Lock()
    registry.labeledLockers.OpenLocker(label)
    registry.lockersLock.Unlock()
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Locker %s is open and active", label)
    events.SendRequestEvent(Registry_LockerOpened, label, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Locker label needed"
  }
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func closeOrClearLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  close := strings.Contains(r.RequestURI, "close")
  if close && strings.EqualFold(label, constants.LockerDefaultLabel) {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Default locker cannot be closed"
  } else if label != "" {
    registry.lockersLock.Lock()
    registry.labeledLockers.ClearLocker(label, close)
    registry.lockersLock.Unlock()
    w.WriteHeader(http.StatusOK)
    if close {
      msg = fmt.Sprintf("Locker %s is closed", label)
      events.SendRequestEvent(Registry_LockerClosed, label, r)
    } else {
      msg = fmt.Sprintf("Locker %s is cleared", label)
      events.SendRequestEvent(Registry_LockerCleared, label, r)
    }
  } else {
    w.WriteHeader(http.StatusOK)
    registry.lockersLock.Lock()
    registry.labeledLockers.Init()
    registry.lockersLock.Unlock()
    result := clearPeersResultsAndEvents(registry.loadAllPeerPods(), r)
    w.WriteHeader(http.StatusOK)
    util.WriteJsonPayload(w, result)
    events.SendRequestEvent(Registry_AllLockersCleared, label, r)
  }
  if global.EnableRegistryLockerLogs {
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
      locker = locker.GetLockerView(getEvents)
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
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
}

func getAllLockers(w http.ResponseWriter, r *http.Request) {
  msg := ""
  getData := util.GetBoolParamValue(r, "data")
  getEvents := util.GetBoolParamValue(r, "events")
  getPeerLockers := util.GetBoolParamValue(r, "peers")
  level := util.GetIntParamValue(r, "level", 5)
  registry.lockersLock.RLock()
  labeledLockers := registry.labeledLockers
  registry.lockersLock.RUnlock()
  msg = "All labeled lockers reported"
  util.WriteJsonPayload(w, labeledLockers.GetAllLockers(getPeerLockers, getEvents, getData, level))
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
}

func getLockerLabels(w http.ResponseWriter, r *http.Request) {
  registry.lockersLock.RLock()
  labeledLockers := registry.labeledLockers
  registry.lockersLock.RUnlock()
  util.WriteJsonPayload(w, labeledLockers.GetLockerLabels())
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
}

func storeInLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  path, _ := util.GetListParam(r, "path")
  if label != "" && len(path) > 0 {
    data := util.Read(r.Body)
    registry.labeledLockers.GetOrCreateLocker(label).Store(path, data)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Data stored in labeled locker %s for path %+v", label, path)
    events.SendRequestEvent(Registry_LockerDataStored, msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  if global.EnableRegistryLockerLogs {
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
    events.SendRequestEvent(Registry_LockerDataRemoved, msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getFromDataLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  val := util.GetStringParamValue(r, "path")
  path, _ := util.GetListParam(r, "path")
  getData := util.GetBoolParamValue(r, "data")
  level := util.GetIntParamValue(r, "level", 0)
  if level > 0 {
    level = len(path) + level
  }
  if len(path) > 0 {
    data, dataAtKey := registry.labeledLockers.Get(label, path, getData, level)
    msg = fmt.Sprintf("Reported data from path [%s] from locker [%s]", val, label)
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
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLogs {
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
      events.SendRequestEvent(Registry_PeerInstanceLockerCleared, msg, r)
    } else {
      msg = fmt.Sprintf("Peer %s data cleared for all instances", peerName)
      events.SendRequestEvent(Registry_PeerLockerCleared, msg, r)
    }
  } else {
    msg = "All peer lockers cleared"
    events.SendRequestEvent(Registry_AllPeerLockersCleared, "", r)
  }
  if global.EnableRegistryLockerLogs {
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
  if global.EnableRegistryLockerLogs {
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
    if getData {
      result = locker.GetPeerLockers(peerName, peerAddress, getEvents)
    } else {
      result = locker.GetPeerLockersView(peerName, peerAddress, getEvents)
    }
    util.WriteJsonPayload(w, result)
    if peerName != "" {
      msg = fmt.Sprintf("Peer %s data reported", peerName)
    } else {
      msg = "All peer lockers reported"
    }
  }
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
}

func getPeersClientSummaryResults(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  peerName := util.GetStringParamValue(r, "peer")
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
    result := locker.GetPeersClientResults(peerName, registry.trackingHeaders, registry.crossTrackingHeaders, registry.trackingTimeBuckets, detailed, byInstances)
    util.WriteJsonPayload(w, result)
    msg = "Reported peers client results"
  }
  if global.EnableRegistryLockerLogs {
    util.AddLogMessage(msg, r)
  }
}

func flushPeerEvents(w http.ResponseWriter, r *http.Request) {
  msg := "Flushing pending events for all peers"
  result := registry.flushPeersEvents()
  w.WriteHeader(http.StatusOK)
  util.WriteJsonPayload(w, result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func clearPeerEvents(w http.ResponseWriter, r *http.Request) {
  msg := "Clearing events for all peers"
  result := registry.clearPeersEvents()
  w.WriteHeader(http.StatusOK)
  util.WriteJsonPayload(w, result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func getPeerEvents(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  peerNames, _ := util.GetListParam(r, "peers")
  unified := util.GetBoolParamValue(r, "unified")
  reverse := util.GetBoolParamValue(r, "reverse")
  data := util.GetBoolParamValue(r, "data")
  all := strings.EqualFold(label, constants.LockerAll)

  registry.lockersLock.RLock()
  labeledLockers := registry.labeledLockers
  registry.lockersLock.RUnlock()

  var locker *locker.CombiLocker
  if label == "" {
    all = true
  } else if strings.EqualFold(label, constants.LockerCurrent) {
    locker = registry.getCurrentLocker()
    label = locker.Label
  } else {
    locker = registry.labeledLockers.GetLocker(label)
  }

  if !all && locker == nil {
    msg = "Locker not found"
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprint(w, msg)
  } else {
    if len(peerNames) > 0 {
      if all {
        msg = fmt.Sprintf("Registry: Reporting events for peers %s from all lockers", peerNames)
      } else {
        msg = fmt.Sprintf("Registry: Reporting events for peers %s from locker [%s]", peerNames, label)
      }
    } else {
      if all {
        msg = fmt.Sprintf("Registry: Reporting events for all peers from all lockers")
      } else {
        msg = fmt.Sprintf("Registry: Reporting events for all peers from locker [%s]", label)
      }
    }
    result := labeledLockers.GetPeerEvents(label, peerNames, unified, reverse, data)
    util.WriteJsonPayload(w, result)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func searchInPeerEvents(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  peerNames, _ := util.GetListParam(r, "peers")
  key := util.GetStringParamValue(r, "text")
  unified := util.GetBoolParamValue(r, "unified")
  reverse := util.GetBoolParamValue(r, "reverse")
  data := util.GetBoolParamValue(r, "data")
  all := strings.EqualFold(label, constants.LockerAll)

  if key == "" {
    msg = "Cannot search. No key given."
    fmt.Fprintln(w, msg)
  } else {
    registry.lockersLock.RLock()
    labeledLockers := registry.labeledLockers
    registry.lockersLock.RUnlock()

    var locker *locker.CombiLocker
    if label == "" {
      all = true
    } else if strings.EqualFold(label, constants.LockerCurrent) {
      locker = registry.getCurrentLocker()
      label = locker.Label
    } else {
      locker = registry.labeledLockers.GetLocker(label)
    }
    if !all && locker == nil {
      msg = "Locker not found"
      w.WriteHeader(http.StatusNotFound)
      fmt.Fprint(w, msg)
    } else {
      if len(peerNames) > 0 {
        if all {
          msg = fmt.Sprintf("Registry: Reporting searched events for peers %s from all lockers", peerNames)
        } else {
          msg = fmt.Sprintf("Registry: Reporting searched events for peers %s from locker [%s]", peerNames, label)
        }
      } else {
        if all {
          msg = fmt.Sprintf("Registry: Reporting searched events for all peers from all lockers")
        } else {
          msg = fmt.Sprintf("Registry: Reporting searched events for all peers from locker [%s]", label)
        }
      }
      result := labeledLockers.SearchInPeerEvents(label, peerNames, key, unified, reverse, data)
      util.WriteJsonPayload(w, result)
    }
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func checkBadPods(result PeerResults, w http.ResponseWriter) {
  anyBad := false
  for _, pods := range result {
    for _, status := range pods {
      if !status && len(pods) <= 1 {
        anyBad = true
      }
    }
  }
  if anyBad {
    w.WriteHeader(http.StatusFailedDependency)
  } else {
    w.WriteHeader(http.StatusOK)
  }
}

func addPeerTarget(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  t := &PeerTarget{}
  body := util.Read(r.Body)
  if err := util.ReadJson(body, t); err == nil {
    if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
      events.SendRequestEventJSON(Registry_PeerTargetRejected, err.Error(),
        map[string]interface{}{"error": err.Error(), "payload": body}, r)
      log.Println(err)
    } else {
      result := registry.addPeerTarget(peerName, t)
      checkBadPods(result, w)
      msg = util.ToJSON(result)
      events.SendRequestEventJSON(Registry_PeerTargetAdded, t.Name,
        map[string]interface{}{"target": t, "result": result}, r)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to parse json"
    events.SendRequestEventJSON(Registry_PeerTargetRejected, err.Error(),
      map[string]interface{}{"error": err.Error(), "payload": body}, r)
    log.Println(err)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removePeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets, _ := util.GetListParam(r, "targets")
  result := registry.removePeerTargets(peerName, targets)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  events.SendRequestEventJSON(Registry_PeerTargetsRemoved, util.GetStringParamValue(r, "targets"),
    map[string]interface{}{"targets": targets, "result": result}, r)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func stopPeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := registry.stopPeerTargets(peerName, targets)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerTargetsStopped, util.GetStringParamValue(r, "targets"),
    map[string]interface{}{"targets": targets, "result": result}, r)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := registry.invokePeerTargets(peerName, targets)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerTargetsInvoked, util.GetStringParamValue(r, "targets"),
    map[string]interface{}{"targets": targets, "result": result}, r)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeerTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    peerTargets := registry.getPeerTargets(peerName)
    if peerTargets != nil {
      msg = fmt.Sprintf("Registry: Reporting peer targets for peer: %s", peerName)
      util.WriteJsonPayload(w, peerTargets)
    } else {
      w.WriteHeader(http.StatusNoContent)
      msg = fmt.Sprintf("Peer not found: %s\n", peerName)
      fmt.Fprintln(w, "[]")
    }
  } else {
    msg = "Reporting all peer targets"
    registry.peersLock.RLock()
    peerTargets := registry.peerTargets
    registry.peersLock.RUnlock()
    util.WriteJsonPayload(w, peerTargets)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func enableAllClientResultsCollection(w http.ResponseWriter, r *http.Request) {
  result := registry.enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), true)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
  result := registry.enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), false)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func addPeerJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  body := util.Read(r.Body)
  if job, err := job.ParseJobFromPayload(body); err == nil {
    result := registry.addPeerJob(peerName, &PeerJob{*job})
    checkBadPods(result, w)
    events.SendRequestEventJSON(Registry_PeerJobAdded, job.Name,
      map[string]interface{}{"job": job, "result": result}, r)
    msg = util.ToJSON(result)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    events.SendRequestEventJSON(Registry_PeerJobRejected, err.Error(),
      map[string]interface{}{"error": err.Error(), "payload": body}, r)
    msg = "Failed to read job"
    log.Println(err)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func addPeerJobScriptOrFile(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  fileName := util.GetStringParamValue(r, "name")
  filePath := util.GetStringParamValue(r, "path")
  script := strings.Contains(r.RequestURI, "script")

  content := util.ReadBytes(r.Body)
  if fileName != "" && len(content) > 0 {
    result := registry.addPeerJobScriptOrFile(peerName, filePath, fileName, content, script)
    checkBadPods(result, w)
    events.SendRequestEventJSON(Registry_PeerJobFileAdded, fileName,
      map[string]interface{}{"name": fileName, "path": filePath, "script": script, "result": result}, r)
    msg = util.ToJSON(result)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    events.SendRequestEventJSON(Registry_PeerJobFileRejected, fileName,
      map[string]interface{}{"name": fileName, "path": filePath, "script": script}, r)
    msg = "Invalid job script"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removePeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs, _ := util.GetListParam(r, "jobs")
  result := registry.removePeerJobs(peerName, jobs)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerJobsRemoved, util.GetStringParamValue(r, "jobs"),
    map[string]interface{}{"jobs": jobs, "result": result}, r)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func stopPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs := util.GetStringParamValue(r, "jobs")
  result := registry.stopPeerJobs(peerName, jobs)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerJobsStopped, util.GetStringParamValue(r, "jobs"),
    map[string]interface{}{"jobs": jobs, "result": result}, r)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func runPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs := util.GetStringParamValue(r, "jobs")
  result := registry.invokePeerJobs(peerName, jobs)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerJobsInvoked, util.GetStringParamValue(r, "jobs"),
    map[string]interface{}{"jobs": jobs, "result": result}, r)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeerJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if peerName, present := util.GetStringParam(r, "peer"); present {
    peerJobs := registry.getPeerJobs(peerName)
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
    registry.peersLock.RLock()
    peerJobs := registry.peerJobs
    registry.peersLock.RUnlock()
    util.WriteJsonPayload(w, peerJobs)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func clearPeerEpochs(w http.ResponseWriter, r *http.Request) {
  registry.clearPeerEpochs()
  w.WriteHeader(http.StatusOK)
  msg := "Peers Epochs Cleared"
  events.SendRequestEvent(Registry_PeersEpochsCleared, "", r)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
  registry.reset()
  w.WriteHeader(http.StatusOK)
  msg := "Peers Cleared"
  events.SendRequestEvent(Registry_PeersCleared, "", r)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  result := registry.clearPeerTargets(peerName)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerTargetsCleared, peerName,
    map[string]interface{}{"peer": peerName, "result": result}, r)
  msg := util.ToJSON(result)
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func clearPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  result := registry.clearPeerJobs(peerName)
  checkBadPods(result, w)
  events.SendRequestEventJSON(Registry_PeerJobsCleared, peerName,
    map[string]interface{}{"peer": peerName, "result": result}, r)
  msg := util.ToJSON(result)
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func addPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if h, present := util.GetStringParam(r, "headers"); present {
    result := registry.addPeersTrackingHeaders(h)
    events.SendRequestEventJSON(Registry_PeerTrackingHeadersAdded, h,
      map[string]interface{}{"headers": h, "result": result}, r)
    checkBadPods(result, w)
    msg = util.ToJSON(result)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "{\"error\":\"No headers given\"}"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  result := registry.clearPeersTrackingHeaders()
  events.SendRequestEvent(Registry_PeerTrackingHeadersCleared, "", r)
  checkBadPods(result, w)
  msg = util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.ToJSON(registry.peerTrackingHeaders))
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported peer tracking headers", r)
  }
}

func addPeersTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if b, present := util.GetStringParam(r, "buckets"); present {
    if result := registry.addPeersTrackingTimeBuckets(b); result != nil {
      events.SendRequestEventJSON(Registry_PeerTrackingTimeBucketsAdded, b,
        map[string]interface{}{"buckets": b, "result": result}, r)
      checkBadPods(result, w)
      msg = util.ToJSON(result)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "{\"error\":\"Invalid time buckets\"}"
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "{\"error\":\"No headers given\"}"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearPeersTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  result := registry.clearPeersTrackingTimeBuckets()
  events.SendRequestEvent(Registry_PeerTrackingTimeBucketsCleared, "", r)
  checkBadPods(result, w)
  msg = util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeersTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  fmt.Fprintln(w, registry.peerTrackingTimeBuckets)
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported peer tracking time buckets", r)
  }
}

func setPeersProbe(w http.ResponseWriter, r *http.Request) {
  msg := ""
  probeType := util.GetStringParamValue(r, "type")
  isReadiness := strings.EqualFold(probeType, "readiness")
  isLiveness := strings.EqualFold(probeType, "liveness")
  if !isReadiness && !isLiveness {
    msg = "Cannot add. Invalid probe type"
    w.WriteHeader(http.StatusBadRequest)
  } else if uri, present := util.GetStringParam(r, "uri"); !present {
    msg = "Cannot add. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    registry.setProbe(isReadiness, uri, 0)
    result := registry.sendProbe(probeType, uri)
    checkBadPods(result, w)
    events.SendRequestEventJSON(Registry_PeerProbeSet, uri,
      map[string]interface{}{"probeType": probeType, "uri": uri, "result": result}, r)
    msg = util.ToJSON(result)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func setPeersProbeStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  probeType := util.GetStringParamValue(r, "type")
  isReadiness := strings.EqualFold(probeType, "readiness")
  isLiveness := strings.EqualFold(probeType, "liveness")
  if !isReadiness && !isLiveness {
    msg = "Cannot add. Invalid probe type"
    w.WriteHeader(http.StatusBadRequest)
  } else if status, present := util.GetIntParam(r, "status", 200); !present {
    msg = "Cannot set. Invalid status code"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    if status <= 0 {
      status = 200
    }
    registry.setProbe(isReadiness, "", status)
    result := registry.sendProbeStatus(probeType, status)
    checkBadPods(result, w)
    events.SendRequestEventJSON(Registry_PeerProbeStatusSet, probeType,
      map[string]interface{}{"probeType": probeType, "status": status, "result": result}, r)
    msg = util.ToJSON(result)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeersProbes(w http.ResponseWriter, r *http.Request) {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.ToJSON(registry.peerProbes))
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported peer probes", r)
  }
}

func buildPeerAPIs(peer, address string) []string {
  apis := []string{
    fmt.Sprintf("http://%s/version", address),
    fmt.Sprintf("http://%s/client/results", address),
    fmt.Sprintf("http://%s/client/targets", address),
    fmt.Sprintf("http://%s/client/track/headers", address),
    fmt.Sprintf("http://%s/events", address),
    fmt.Sprintf("http://%s/metrics", address),
    fmt.Sprintf("http://%s/listeners", address),
    fmt.Sprintf("http://%s/probes", address),
    fmt.Sprintf("http://%s/request/ignore", address),
    fmt.Sprintf("http://%s/response/headers", address),
    fmt.Sprintf("http://%s/response/payload", address),
    fmt.Sprintf("http://%s/response/status", address),
    fmt.Sprintf("http://%s/response/triggers", address),
    fmt.Sprintf("http://%s/proxy/targets", address),
    fmt.Sprintf("http://%s/jobs", address),
  }
  return apis
}

func getPeersAPIs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  peersAPIs := map[string]map[string][]string{}
  for peer, pods := range registry.loadPeerPods(peerName, "") {
    peersAPIs[peer] = map[string][]string{}
    peersAPIs[peer]["all"] = buildPeerAPIs(peer, fmt.Sprintf("%s/registry/peers/%s/call?uri=", global.PeerAddress, peer))
    for _, pod := range pods {
      apis := buildPeerAPIs(peer, pod.Address)
      apis = append(apis, fmt.Sprintf("http://%s/registry/peers/%s/lockers?data=y&level=3", global.PeerAddress, peer))
      for peer, lockers := range registry.labeledLockers.GetPeerLockers(peer) {
        for label := range lockers {
          apis = append(apis, fmt.Sprintf("http://%s/registry/lockers/%s/peers/%s?data=y&level=3", global.PeerAddress, label, peer))
        }
      }
      peersAPIs[peer][pod.Name] = apis
    }
  }
  util.WriteJsonPayload(w, peersAPIs)
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported useful peer APIs.", r)
  }
}

func callPeer(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  uri := util.GetStringParamValue(r, "uri")
  if uri == "" {
    msg = "Cannot call peer. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, msg)
  } else {
    body := util.ReadBytes(r.Body)
    msg = fmt.Sprintf("Calling peers with URI [%s] Method [%s] Headers [%+v] Body [%s]", uri, r.Method, r.Header, body)
    result := registry.callPeer(peerName, uri, r.Method, r.Header, body)
    events.SendRequestEventJSON(Registry_PeerCalled, uri, map[string]interface{}{
      "peer": peerName, "uri": uri, "method": r.Method, "headers": r.Header, "body": body, "result": result}, r)
    util.WriteJsonPayload(w, result)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func copyPeersToLocker(w http.ResponseWriter, r *http.Request) {
  registry.peersLock.RLock()
  defer registry.peersLock.RUnlock()
  currentLocker := registry.getCurrentLocker()
  currentLocker.Store([]string{"peers"}, util.ToJSON(registry.peers))
  w.WriteHeader(http.StatusOK)
  msg := fmt.Sprintf("Peers [len: %d] stored in labeled locker %s under path 'peers'",
    len(registry.peers), currentLocker.Label)
  events.SendRequestEventJSON(Registry_PeersCopied, msg, registry.peers, r)
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func cloneFromRegistry(w http.ResponseWriter, r *http.Request) {
  msg := ""
  url := util.GetStringParamValue(r, "url")
  if url == "" {
    msg = "Cannot clone. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    var err error
    if err = registry.clonePeersFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers data from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = registry.cloneLockersFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone lockers data from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = registry.clonePeersTargetsFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers targets from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = registry.clonePeersJobsFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers jobs from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = registry.clonePeersTrackingHeadersFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers tracking headers from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = registry.clonePeersProbesFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers probes from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    msg = fmt.Sprintf("Cloned data from registry [%s]", url)
    events.SendRequestEvent(Registry_Cloned, "", r)
  }
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func dumpRegistry(w http.ResponseWriter, r *http.Request) {
  dump := map[string]interface{}{}
  registry.lockersLock.RLock()
  dump["lockers"] = registry.labeledLockers.GetAllLockers(true, true, true, 0)
  registry.lockersLock.RUnlock()
  registry.peersLock.RLock()
  dump["peers"] = registry.peers
  dump["peerTargets"] = registry.peerTargets
  dump["peerJobs"] = registry.peerJobs
  dump["peerTrackingHeaders"] = registry.peerTrackingHeaders
  dump["peerProbes"] = registry.peerProbes
  registry.peersLock.RUnlock()
  fmt.Fprintln(w, util.ToJSON(dump))
  msg := "Registry data dumped"
  events.SendRequestEvent(Registry_Dumped, "", r)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func loadRegistryDump(w http.ResponseWriter, r *http.Request) {
  dump := map[string]interface{}{}
  if err := util.ReadJsonPayload(r, &dump); err != nil {
    fmt.Fprintf(w, "failed to load data with error: %s", err.Error())
    w.WriteHeader(http.StatusInternalServerError)
    return
  }

  msg := ""
  if dump["lockers"] != nil {
    lockersData := util.ToJSON(dump["lockers"])
    if lockersData != "" {
      lockers := map[string]*locker.CombiLocker{}
      if err := util.ReadJson(lockersData, &lockers); err == nil {
        registry.lockersLock.Lock()
        registry.labeledLockers.ReplaceLockers(lockers)
        registry.lockersLock.Unlock()
      } else {
        msg += fmt.Sprintf("[failed to load lockers with error: %s]", err.Error())
      }
    }
  }
  registry.peersLock.Lock()
  defer registry.peersLock.Unlock()

  if dump["peers"] != nil {
    peersData := util.ToJSON(dump["peers"])
    if peersData != "" {
      registry.peers = map[string]*Peers{}
      if err := util.ReadJson(peersData, &registry.peers); err != nil {
        msg += fmt.Sprintf("[failed to load peers with error: %s]", err.Error())
      }
    }
  }
  if dump["peerTargets"] != nil {
    peerTargetsData := util.ToJSON(dump["peerTargets"])
    if peerTargetsData != "" {
      registry.peerTargets = map[string]PeerTargets{}
      if err := util.ReadJson(peerTargetsData, &registry.peerTargets); err != nil {
        msg += fmt.Sprintf("[failed to load peer targets with error: %s]", err.Error())
      }
    }
  }
  if dump["peerJobs"] != nil {
    peerJobsData := util.ToJSON(dump["peerJobs"])
    if peerJobsData != "" {
      registry.peerJobs = map[string]PeerJobs{}
      if err := util.ReadJson(peerJobsData, &registry.peerJobs); err != nil {
        msg += fmt.Sprintf("[failed to load peer jobs with error: %s]", err.Error())
      }
    }
  }
  if dump["peerTrackingHeaders"] != nil {
    registry.peerTrackingHeaders = dump["peerTrackingHeaders"].(string)
    registry.trackingHeaders, registry.crossTrackingHeaders = util.ParseTrackingHeaders(registry.peerTrackingHeaders)
  }
  if dump["peerProbes"] != nil {
    peerProbesData := util.ToJSON(dump["peerProbes"])
    if peerProbesData != "" {
      registry.peerProbes = &PeerProbes{}
      if err := util.ReadJson(peerProbesData, &registry.peerProbes); err != nil {
        msg += fmt.Sprintf("[failed to load peer probes with error: %s]", err.Error())
      }
    }
  }
  if msg == "" {
    msg = "Registry data loaded"
  }
  events.SendRequestEvent(Registry_DumpLoaded, "", r)
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}
