package registry

import (
  "fmt"
  "goto/pkg/client/results"
  "goto/pkg/constants"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/job"
  "goto/pkg/registry/locker"
  "goto/pkg/util"
  "log"
  "net"
  "net/http"
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
  Name      string                 `json:"name"`
  Namespace string                 `json:"namespace"`
  Pods      map[string]*Pod        `json:"pods"`
  PodEpochs map[string][]*PodEpoch `json:"podEpochs"`
}

type PeerTarget struct {
  invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PeerJob struct {
  job.Job
}

type PeerJobs map[string]*PeerJob

type PeerProbes struct {
  ReadinessProbe  string
  LivenessProbe   string
  ReadinessStatus int
  LivenessStatus  int
}

type PeerData struct {
  Targets         PeerTargets
  Jobs            PeerJobs
  TrackingHeaders string
  Probes          *PeerProbes
  Message         string
}

type PortRegistry struct {
  peers                map[string]*Peers
  peerTargets          map[string]PeerTargets
  peerJobs             map[string]PeerJobs
  peerTrackingHeaders  string
  trackingHeaders      []string
  crossTrackingHeaders map[string][]string
  peerProbes           *PeerProbes
  labeledLockers       *locker.LabeledLockers
  peersLock            sync.RWMutex
  lockersLock          sync.RWMutex
}

var (
  portRegistry map[string]*PortRegistry = map[string]*PortRegistry{}
  registryLock sync.RWMutex
)

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

func getCurrentLocker(r *http.Request) *locker.CombiLocker {
  return getPortRegistry(r).getCurrentLocker()
}

func getLockerForLabel(r *http.Request, label string) *locker.CombiLocker {
  return getPortRegistry(r).getLabeledLocker(label)
}

func (pr *PortRegistry) reset() {
  pr.peersLock.Lock()
  pr.peers = map[string]*Peers{}
  pr.peerTargets = map[string]PeerTargets{}
  pr.peerJobs = map[string]PeerJobs{}
  pr.peersLock.Unlock()
  pr.lockersLock.Lock()
  pr.labeledLockers = locker.NewLabeledPeersLockers()
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

func (pr *PortRegistry) getCurrentLocker() *locker.CombiLocker {
  pr.lockersLock.RLock()
  defer pr.lockersLock.RUnlock()
  return pr.labeledLockers.GetCurrentLocker()
}

func (pr *PortRegistry) getLabeledLocker(label string) *locker.CombiLocker {
  pr.lockersLock.RLock()
  defer pr.lockersLock.RUnlock()
  return pr.labeledLockers.GetLocker(label)
}

func (pr *PortRegistry) unsafeAddPeer(peer *Peer) {
  now := time.Now()
  if pr.peers[peer.Name] == nil {
    pr.peers[peer.Name] = &Peers{Name: peer.Name, Namespace: peer.Namespace, Pods: map[string]*Pod{}, PodEpochs: map[string][]*PodEpoch{}}
  }
  pod := &Pod{Name: peer.Pod, Address: peer.Address, URL: "http://" + peer.Address,
    Node: peer.Node, Cluster: peer.Cluster, Healthy: true,
    CurrentEpoch: PodEpoch{Name: peer.Pod, Address: peer.Address, Node: peer.Node, Cluster: peer.Cluster, FirstContact: now, LastContact: now}}
  pr.initHttpClientForPeerPod(pod)
  if podEpochs := pr.peers[peer.Name].PodEpochs[peer.Address]; podEpochs != nil {
    for _, oldEpoch := range podEpochs {
      pod.PastEpochs = append(pod.PastEpochs, oldEpoch)
    }
    pod.CurrentEpoch.Epoch = len(podEpochs)
  }
  pr.peers[peer.Name].PodEpochs[peer.Address] = append(pr.peers[peer.Name].PodEpochs[peer.Address], &pod.CurrentEpoch)

  pr.peers[peer.Name].Pods[peer.Address] = pod
  if pr.peerTargets[peer.Name] == nil {
    pr.peerTargets[peer.Name] = PeerTargets{}
  }
  if pr.peerJobs[peer.Name] == nil {
    pr.peerJobs[peer.Name] = PeerJobs{}
  }
}

func (pr *PortRegistry) addPeer(peer *Peer) {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  pr.unsafeAddPeer(peer)
  pr.getCurrentLocker().InitPeerLocker(peer.Name, peer.Address)
}

func (pr *PortRegistry) GetPeer(peerName, peerAddress string) *Pod {
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  if pr.peers[peerName] != nil && pr.peers[peerName].Pods[peerAddress] != nil {
    return pr.peers[peerName].Pods[peerAddress]
  }
  return nil
}

func (pr *PortRegistry) rememberPeer(peer *Peer) {
  if pod := pr.GetPeer(peer.Name, peer.Address); pod != nil {
    pod.lock.Lock()
    pod.Healthy = true
    pod.Offline = false
    pod.CurrentEpoch.LastContact = time.Now()
    if pod.client == nil {
      pr.initHttpClientForPeerPod(pod)
    }
    pod.lock.Unlock()
  } else {
    pr.peersLock.Lock()
    defer pr.peersLock.Unlock()
    pr.unsafeAddPeer(peer)
  }
}

func (pr *PortRegistry) removePeer(name string, address string) bool {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  present := false
  if _, present = pr.peers[name]; present {
    delete(pr.peers[name].Pods, address)
  }
  pr.getCurrentLocker().DeactivateInstanceLocker(name, address)
  return present
}

func (pr *PortRegistry) clearPeerEpochs() {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  for name, peers := range pr.peers {
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
      delete(pr.peers, name)
      pr.getCurrentLocker().RemovePeerLocker(name)
    }
  }
}

func (pr *PortRegistry) initHttpClientForPeerPod(pod *Pod) {
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

func (pr *PortRegistry) loadAllPeerPods() map[string][]*Pod {
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  peerPods := map[string][]*Pod{}
  for name, peer := range pr.peers {
    peerPods[name] = []*Pod{}
    for _, pod := range peer.Pods {
      if pod.client != nil {
        peerPods[name] = append(peerPods[name], pod)
      }
    }
  }
  return peerPods
}

func (pr *PortRegistry) loadPeerPods(peerName string, peerAddress string) map[string][]*Pod {
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  peerPods := map[string][]*Pod{}
  if peerName != "" {
    peerPods[peerName] = []*Pod{}
    if peerAddress != "" {
      if pr.peers[peerName] != nil {
        if pod := pr.peers[peerName].Pods[peerAddress]; pod != nil {
          if pod.client != nil {
            peerPods[peerName] = append(peerPods[peerName], pod)
          }
        }
      }
    } else {
      if pr.peers[peerName] != nil {
        for _, pod := range pr.peers[peerName].Pods {
          if pod.client != nil {
            peerPods[peerName] = append(peerPods[peerName], pod)
          }
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerPods[name] = []*Pod{}
      for _, pod := range peer.Pods {
        if pod.client != nil {
          peerPods[name] = append(peerPods[name], pod)
        }
      }
    }
  }
  return peerPods
}

func (pr *PortRegistry) loadPodsForPeerWithData(peerName string, jobs ...bool) map[string][]*Pod {
  if peerName != "" {
    pr.peersLock.RLock()
    defer pr.peersLock.RUnlock()
    peerPods := map[string][]*Pod{}
    hasData := pr.peerTargets[peerName] != nil
    if len(jobs) > 0 && jobs[0] {
      hasData = pr.peerJobs[peerName] != nil
    }
    if hasData {
      hasData = pr.peers[peerName] != nil
    }
    if hasData {
      peerPods[peerName] = []*Pod{}
      for _, pod := range pr.peers[peerName].Pods {
        if pod.client != nil {
          peerPods[peerName] = append(peerPods[peerName], pod)
        }
      }
    }
    return peerPods
  }
  return pr.loadAllPeerPods()
}

func (pr *PortRegistry) callPeer(peerName, uri, method string, headers map[string][]string, payload string) map[string]map[string]string {
  result := map[string]map[string]string{}
  resultLock := sync.Mutex{}
  invokeForPodsWithHeadersAndPayload(pr.loadPeerPods(peerName, ""), method, uri, headers, payload, http.StatusOK, 0, false,
    func(peer string, pod *Pod, response string, err error) {
      resultLock.Lock()
      if result[peer] == nil {
        result[peer] = map[string]string{}
      }
      result[peer][pod.Address] = response
      resultLock.Unlock()
    })
  return result
}

func (pr *PortRegistry) checkPeerHealth(peerName string, peerAddress string) map[string]map[string]bool {
  return invokeForPods(pr.loadPeerPods(peerName, peerAddress), "GET", "/health", http.StatusOK, 1, true,
    func(peer string, pod *Pod, response string, err error) {
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

func (pr *PortRegistry) cleanupUnhealthyPeers(peerName string) map[string]map[string]bool {
  return invokeForPods(pr.loadPeerPods(peerName, ""), "GET", "/health", http.StatusOK, 1, true,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        pod.lock.Lock()
        pod.Healthy = true
        pod.PastEpochs = nil
        pod.lock.Unlock()
        log.Printf("Peer %s Address %s is healthy\n", peer, pod.Address)
      } else {
        log.Printf("Peer %s Address %s is unhealthy or unavailable, error: %s\n", peer, pod.Address, err.Error())
        pr.removePeer(peer, pod.Address)
      }
    })
}

func (pr *PortRegistry) clearLocker(peerName, peerAddress string) map[string]map[string]bool {
  peersToClear := map[string][]*Pod{}
  if peerName != "" && peerAddress != "" {
    if pr.getCurrentLocker().ClearInstanceLocker(peerName, peerAddress) {
      peersToClear = pr.loadPeerPods(peerName, peerAddress)
    }
  } else {
    if pr.getCurrentLocker().InitPeerLocker(peerName, "") {
      peersToClear = pr.loadPeerPods(peerName, "")
    }
  }
  return invokeForPods(peersToClear, "POST", "/client/results/clear", http.StatusAccepted, 2, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Results cleared on peer %s address %s\n", peer, pod.Address)

      } else {
        log.Printf("Failed to clear results on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) addPeerTarget(peerName string, target *PeerTarget) map[string]map[string]bool {
  pr.peersLock.Lock()
  peerPods := map[string][]*Pod{}
  if peerName != "" {
    if pr.peerTargets[peerName] == nil {
      pr.peerTargets[peerName] = PeerTargets{}
    }
    pr.peerTargets[peerName][target.Name] = target
    if pr.peers[peerName] != nil {
      peerPods[peerName] = []*Pod{}
      for _, pod := range pr.peers[peerName].Pods {
        peerPods[peerName] = append(peerPods[peerName], pod)
      }
    }
  } else {
    for name, peer := range pr.peers {
      if pr.peerTargets[name] == nil {
        pr.peerTargets[name] = PeerTargets{}
      }
      pr.peerTargets[name][target.Name] = target
      peerPods[name] = []*Pod{}
      for _, pod := range peer.Pods {
        peerPods[name] = append(peerPods[name], pod)
      }
    }
  }
  pr.peersLock.Unlock()
  return invokeForPodsWithHeadersAndPayload(peerPods, "POST", "/client/targets/add", nil, util.ToJSON(target), http.StatusAccepted, 1, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        if global.EnableRegistryLogs {
          log.Printf("Pushed target %s to peer %s address %s\n", target.Name, peer, pod.Address)
        }
      } else {
        log.Printf("Failed to push target %s to peer %s address %s with error: %s\n", target.Name, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) removePeerTargets(peerName string, targets []string) map[string]map[string]bool {
  targetList := strings.Join(targets, ",")
  removed := true
  return invokeForPods(pr.loadPodsForPeerWithData(peerName),
    "POST", fmt.Sprintf("/client/targets/%s/remove", targetList), http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
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
        pr.peersLock.Lock()
        if pr.peerTargets[peer] != nil {
          if len(targets) > 0 {
            for _, target := range targets {
              delete(pr.peerTargets[peer], target)
            }
          } else {
            delete(pr.peerTargets, peer)
          }
        }
        pr.peersLock.Unlock()
      }
      removed = true
    })
}

func (pr *PortRegistry) clearPeerTargets(peerName string) map[string]map[string]bool {
  cleared := true
  return invokeForPods(pr.loadPodsForPeerWithData(peerName), "POST", "/client/targets/clear", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Cleared targets from peer %s address %s\n", peer, pod.Address)
      } else {
        cleared = false
        log.Printf("Failed to clear targets from peer %s address %s, error: %s\n", peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if cleared {
        pr.peersLock.Lock()
        delete(pr.peerTargets, peer)
        pr.peersLock.Unlock()
      }
      cleared = true
    })
}

func (pr *PortRegistry) stopPeerTargets(peerName string, targets string) map[string]map[string]bool {
  uri := ""
  if len(targets) > 0 {
    uri = "/client/targets/" + targets + "/stop"
  } else {
    uri = "/client/targets/stop/all"
  }
  return invokeForPods(pr.loadPodsForPeerWithData(peerName), "POST", uri, http.StatusOK, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Stopped targets %s from peer %s address %s\n", targets, peer, pod.Address)
      } else {
        log.Printf("Failed to stop targets %s from peer %s address %s with error %s\n", targets, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) enableAllOrInvocationsTargetsResultsCollection(enable string, all bool) map[string]map[string]bool {
  uri := "/client/results/"
  if all {
    results.EnableAllTargetResults(util.IsYes(enable))
    uri += "all/"
  } else {
    results.EnableInvocationResults(util.IsYes(enable))
    uri += "invocations/"
  }
  uri += enable
  return invokeForPods(pr.loadAllPeerPods(), "POST", uri, http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Changed targets results collection on peer %s address %s\n", peer, pod.Address)
      } else {
        log.Printf("Failed to change targets Results Collection on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) getPeerTargets(peerName string) PeerTargets {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  return pr.peerTargets[peerName]
}

func (pr *PortRegistry) addPeerJob(peerName string, job *PeerJob) map[string]map[string]bool {
  pr.peersLock.Lock()
  peerPods := map[string][]*Pod{}
  if peerName != "" {
    if pr.peerJobs[peerName] == nil {
      pr.peerJobs[peerName] = PeerJobs{}
    }
    pr.peerJobs[peerName][job.ID] = job
    if pr.peers[peerName] != nil {
      peerPods[peerName] = []*Pod{}
      for _, pod := range pr.peers[peerName].Pods {
        peerPods[peerName] = append(peerPods[peerName], pod)
      }
    }
  } else {
    for name, peer := range pr.peers {
      if pr.peerJobs[name] == nil {
        pr.peerJobs[name] = PeerJobs{}
      }
      pr.peerJobs[name][job.ID] = job
      peerPods[name] = []*Pod{}
      for _, pod := range peer.Pods {
        peerPods[name] = append(peerPods[name], pod)
      }
    }
  }
  pr.peersLock.Unlock()
  return invokeForPodsWithHeadersAndPayload(peerPods, "POST", "/jobs/add", nil, util.ToJSON(job), http.StatusAccepted, 1, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Pushed job %s to peer %s address %s\n", job.ID, peer, pod.Address)
      } else {
        log.Printf("Failed to push job %s to peer %s address %s with error %s\n", job.ID, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) removePeerJobs(peerName string, jobs []string) map[string]map[string]bool {
  jobList := strings.Join(jobs, ",")
  removed := true
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true),
    "POST", fmt.Sprintf("/jobs/%s/remove", jobList), http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Removed jobs %s from peer %s address %s\n", jobList, peer, pod.Address)
      } else {
        removed = false
        log.Printf("Failed to remove jobs %s from peer %s address %s with error %s\n", jobList, peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if removed {
        pr.peersLock.Lock()
        if pr.peerJobs[peer] != nil {
          for _, job := range jobs {
            delete(pr.peerJobs[peer], job)
          }
        } else {
          delete(pr.peerJobs, peer)
        }
        pr.peersLock.Unlock()
      }
      removed = true
    })
}

func (pr *PortRegistry) stopPeerJobs(peerName string, jobs string) map[string]map[string]bool {
  uri := ""
  if len(jobs) > 0 {
    uri = "/jobs/" + jobs + "/stop"
  } else {
    uri = "/jobs/stop/all"
  }
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true), "POST", uri, http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Stopped jobs %s from peer %s address %s\n", jobs, peer, pod.Address)
      } else {
        log.Printf("Failed to stop jobs %s from peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) getPeerJobs(peerName string) PeerJobs {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  return pr.peerJobs[peerName]
}

func (pr *PortRegistry) invokePeerTargets(peerName string, targets string) map[string]map[string]bool {
  uri := ""
  if len(targets) > 0 {
    uri = "/client/targets/" + targets + "/invoke"
  } else {
    uri = "/client/targets/invoke/all"
  }
  return invokeForPods(pr.loadPodsForPeerWithData(peerName), "POST", uri, http.StatusAccepted, 1, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Invoked target %s on peer %s address %s\n", targets, peer, pod.Address)
      } else {
        log.Printf("Failed to invoke targets %s on peer %s address %s with error %s\n", targets, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) invokePeerJobs(peerName string, jobs string) map[string]map[string]bool {
  uri := ""
  if len(jobs) > 0 {
    uri = "/jobs/" + jobs + "/run"
  } else {
    uri = "/jobs/run/all"
  }
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true), "POST", uri, http.StatusAccepted, 1, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Invoked jobs %s on peer %s address %s\n", jobs, peer, pod.Address)
      } else {
        log.Printf("Failed to invoke jobs %s on peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) clearPeerJobs(peerName string) map[string]map[string]bool {
  cleared := true
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true), "POST", "/jobs/clear", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Cleared jobs from peer %s address %s\n", peer, pod.Address)
      } else {
        cleared = false
        log.Printf("Failed to clear jobs from peer %s address %s, error: %s\n", peer, pod.Address, err.Error())
      }
    },
    func(peer string) {
      if cleared {
        pr.peersLock.Lock()
        delete(pr.peerJobs, peer)
        pr.peersLock.Unlock()
      }
      cleared = true
    })
}

func (pr *PortRegistry) addPeersTrackingHeaders(headers string) map[string]map[string]bool {
  pr.peerTrackingHeaders = headers
  pr.trackingHeaders, pr.crossTrackingHeaders = util.ParseTrackingHeaders(headers)
  return invokeForPods(pr.loadAllPeerPods(), "POST", "/client/track/headers/add/"+headers, http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Pushed tracking headers %s to peer %s address %s\n", headers, peer, pod.Address)
      } else {
        log.Printf("Failed to add tracking headers %s to peer %s address %s with error %s\n", headers, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) setProbe(isReadiness bool, uri string, status int) {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  if pr.peerProbes == nil {
    pr.peerProbes = &PeerProbes{}
  }
  if isReadiness {
    if uri != "" {
      pr.peerProbes.ReadinessProbe = uri
    }
    if status > 0 {
      pr.peerProbes.ReadinessStatus = status
    } else if pr.peerProbes.ReadinessStatus <= 0 {
      pr.peerProbes.ReadinessStatus = 200
    }
  } else {
    if uri != "" {
      pr.peerProbes.LivenessProbe = uri
    }
    if status > 0 {
      pr.peerProbes.LivenessStatus = status
    } else if pr.peerProbes.LivenessStatus <= 0 {
      pr.peerProbes.LivenessStatus = 200
    }
  }

}

func (pr *PortRegistry) sendProbe(probeType, uri string) map[string]map[string]bool {
  return invokeForPods(pr.loadAllPeerPods(), "POST", fmt.Sprintf("/probe/%s/set?uri=%s", probeType, uri), http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Pushed %s URI %s to peer %s address %s\n", probeType, uri, peer, pod.Address)
      } else {
        log.Printf("Failed to push %s URI %s to peer %s address %s with error %s\n", probeType, uri, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) sendProbeStatus(probeType string, status int) map[string]map[string]bool {
  return invokeForPods(pr.loadAllPeerPods(), "POST", fmt.Sprintf("/probe/%s/status/set/%d", probeType, status), http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, response string, err error) {
      if err == nil {
        log.Printf("Pushed %s Status %d to peer %s address %s\n", probeType, status, peer, pod.Address)
      } else {
        log.Printf("Failed to push %s Status %d to peer %s address %s with error %s\n", probeType, status, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) preparePeerStartupData(peer *Peer, peerData *PeerData) {
  peerData.Targets = pr.peerTargets[peer.Name]
  peerData.Jobs = pr.peerJobs[peer.Name]
  peerData.TrackingHeaders = pr.peerTrackingHeaders
  peerData.Probes = pr.peerProbes
}

func (pr *PortRegistry) clonePeersFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers"); err == nil {
    peers := map[string]*Peers{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peers); err == nil {
      for _, peer := range peers {
        for _, pod := range peer.Pods {
          pod.Offline = true
        }
      }
      pr.peersLock.Lock()
      pr.peers = peers
      pr.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (pr *PortRegistry) cloneLockersFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/lockers?data=y"); err == nil {
    lockers := map[string]*locker.CombiLocker{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &lockers); err == nil {
      pr.lockersLock.Lock()
      pr.labeledLockers.ReplaceLockers(lockers)
      pr.lockersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (pr *PortRegistry) clonePeersTargetsFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/targets"); err == nil {
    peerTargets := map[string]PeerTargets{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peerTargets); err == nil {
      pr.peersLock.Lock()
      pr.peerTargets = peerTargets
      pr.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (pr *PortRegistry) clonePeersJobsFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/jobs"); err == nil {
    peerJobs := map[string]PeerJobs{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peerJobs); err == nil {
      pr.peersLock.Lock()
      pr.peerJobs = peerJobs
      pr.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (pr *PortRegistry) clonePeersTrackingHeadersFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/track/headers"); err == nil {
    peerTrackingHeaders := ""
    if err := util.ReadJsonPayloadFromBody(resp.Body, &peerTrackingHeaders); err == nil {
      pr.peersLock.Lock()
      pr.peerTrackingHeaders = peerTrackingHeaders
      pr.trackingHeaders, pr.crossTrackingHeaders = util.ParseTrackingHeaders(peerTrackingHeaders)
      pr.peersLock.Unlock()
      return nil
    } else {
      return err
    }
  } else {
    return err
  }
}

func (pr *PortRegistry) clonePeersProbesFrom(registryURL string) error {
  if resp, err := http.Get(registryURL + "/registry/peers/probes"); err == nil {
    peerProbes := &PeerProbes{}
    if err := util.ReadJsonPayloadFromBody(resp.Body, peerProbes); err == nil {
      pr.peersLock.Lock()
      pr.peerProbes = peerProbes
      pr.peersLock.Unlock()
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
  if err := util.ReadJsonPayload(r, peer); err == nil {
    pr := getPortRegistry(r)
    if peerName == "" {
      pr.addPeer(peer)
      pr.peersLock.RLock()
      pr.preparePeerStartupData(peer, peerData)
      pr.peersLock.RUnlock()
      msg = fmt.Sprintf("Added Peer: %+v", *peer)
    } else {
      pr.rememberPeer(peer)
      msg = fmt.Sprintf("Remembered Peer: %+v", *peer)
      peerData.Message = msg
    }
    w.WriteHeader(http.StatusAccepted)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
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
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func checkPeerHealth(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  result := getPortRegistry(r).checkPeerHealth(peerName, address)
  w.WriteHeader(http.StatusOK)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func cleanupUnhealthyPeers(w http.ResponseWriter, r *http.Request) {
  result := getPortRegistry(r).cleanupUnhealthyPeers(util.GetStringParamValue(r, "peer"))
  w.WriteHeader(http.StatusOK)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeers(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  util.WriteJsonPayload(w, pr.peers)
}

func GetPeers(name string, r *http.Request) map[string]string {
  peers := getPortRegistry(r).peers[name]
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
    pr := getPortRegistry(r)
    pr.lockersLock.Lock()
    pr.labeledLockers.OpenLocker(label)
    pr.lockersLock.Unlock()
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Locker %s is open and active", label)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Locker label needed"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func closeLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  if strings.EqualFold(label, constants.LockerDefaultLabel) {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Default locker cannot be closed"
  } else if label != "" {
    pr := getPortRegistry(r)
    pr.lockersLock.Lock()
    pr.labeledLockers.CloseLocker(label)
    pr.lockersLock.Unlock()
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Locker %s is emptied and closed", label)
  } else {
    w.WriteHeader(http.StatusAccepted)
    pr := getPortRegistry(r)
    pr.lockersLock.Lock()
    pr.labeledLockers.Init()
    pr.lockersLock.Unlock()
    msg = "All lockers are emptied and closed"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  getData := util.GetBoolParamValue(r, "data")
  pr := getPortRegistry(r)
  var locker interface{}
  if label != "" {
    if getData {
      locker = pr.getLabeledLocker(label)
      msg = fmt.Sprintf("Labeled locker %s reported with data", label)
    } else {
      locker = pr.getLabeledLocker(label).GetLockerView()
      msg = fmt.Sprintf("Labeled locker %s view reported without data", label)
    }
  } else {
    if getData {
      locker = pr.getCurrentLocker()
      msg = fmt.Sprintf("Labeled locker %s reported with data", label)
    } else {
      locker = pr.getCurrentLocker().GetLockerView()
      msg = fmt.Sprintf("Labeled locker %s view reported without data", label)
    }
    msg = "All labeled lockers reported"
  }
  util.WriteJsonPayload(w, locker)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func getAllLockers(w http.ResponseWriter, r *http.Request) {
  msg := ""
  getData := util.GetBoolParamValue(r, "data")
  pr := getPortRegistry(r)
  pr.lockersLock.RLock()
  labeledLockers := pr.labeledLockers
  pr.lockersLock.RUnlock()
  var locker interface{}
  if getData {
    locker = labeledLockers.GetAllLockers()
    msg = "All labeled locker reported with data"
  } else {
    locker = labeledLockers.GetAllLockersView()
    msg = "All labeled lockers view reported without data"
  }
  util.WriteJsonPayload(w, locker)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func getLockerLabels(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.lockersLock.RLock()
  labeledLockers := pr.labeledLockers
  pr.lockersLock.RUnlock()
  util.WriteJsonPayload(w, labeledLockers.GetLockerLabels())
  if global.EnableRegistryLogs {
    util.AddLogMessage("Locker labels reported", r)
  }
}

func getDataLockerPaths(w http.ResponseWriter, r *http.Request) {
  label := util.GetStringParamValue(r, "label")
  pr := getPortRegistry(r)
  pr.lockersLock.RLock()
  labeledLockers := pr.labeledLockers
  pr.lockersLock.RUnlock()
  util.WriteJsonPayload(w, labeledLockers.GetDataLockerPaths(label))
  if global.EnableRegistryLogs {
    util.AddLogMessage("Data Locker paths reported", r)
  }
}

func findInDataLockers(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  key := util.GetStringParamValue(r, "text")
  pr := getPortRegistry(r)
  pr.lockersLock.RLock()
  labeledLockers := pr.labeledLockers
  pr.lockersLock.RUnlock()
  if key != "" {
    util.WriteJsonPayload(w, labeledLockers.FindInDataLockers(label, key))
    msg = fmt.Sprintf("Reported results for key %s lookup", key)
  } else {
    msg = "Cannot find. No key given."
    fmt.Fprintln(w, msg)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func storeInLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  path, _ := util.GetListParam(r, "path")
  if label != "" && len(path) > 0 {
    data := util.Read(r.Body)
    getLockerForLabel(r, label).Store(path, data)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Data stored in labeled locker %s for path %+v", label, path)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removeFromLabeledLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  path, _ := util.GetListParam(r, "path")
  if label != "" && len(path) > 0 {
    getLockerForLabel(r, label).Remove(path)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Data removed from labeled locker %s for path %+v", label, path)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getFromDataLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  val := util.GetStringParamValue(r, "path")
  path, _ := util.GetListParam(r, "path")
  if len(path) > 0 {
    var locker *locker.CombiLocker
    if label != "" {
      locker = getLockerForLabel(r, label)
    } else {
      locker = getCurrentLocker(r)
    }
    if locker != nil {
      data, dataAtKey := locker.Get(path)
      if dataAtKey {
        fmt.Fprint(w, data)
      } else {
        util.WriteJsonPayload(w, data)
      }
      msg = fmt.Sprintf("Reported data from path [%s]\n", val)
    } else {
      msg = "Locker not found"
      w.WriteHeader(http.StatusNotFound)
      fmt.Fprint(w, msg)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
    fmt.Fprint(w, msg)
  }
  if global.EnableRegistryLogs {
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
    getCurrentLocker(r).StorePeerData(peerName, address, path, data)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data stored for path %+v", peerName, path)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  if global.EnableRegistryLogs {
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
    getCurrentLocker(r).RemovePeerData(peerName, address, path)
    w.WriteHeader(http.StatusAccepted)
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
  if global.EnableRegistryLogs {
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

  if len(path) == 0 || peerName == "" || peerAddress == "" {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
    fmt.Fprint(w, msg)
  } else {
    var locker *locker.CombiLocker
    if label == "" {
      locker = getCurrentLocker(r)
    } else {
      locker = getLockerForLabel(r, label)
    }
    if locker == nil {
      msg = "Locker not found"
      w.WriteHeader(http.StatusNotFound)
      fmt.Fprint(w, msg)
    } else {
      data, dataAtKey := locker.GetFromPeerInstanceLocker(peerName, peerAddress, path)
      if dataAtKey {
        fmt.Fprint(w, data)
      } else {
        util.WriteJsonPayload(w, data)
      }
      msg = fmt.Sprintf("Reported data from path [%s]\n", val)
    }
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func getPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  peerName := util.GetStringParamValue(r, "peer")
  peerAddress := util.GetStringParamValue(r, "address")
  getData := util.GetBoolParamValue(r, "data")
  var locker *locker.CombiLocker
  if label == "" || strings.EqualFold(label, constants.LockerCurrent) {
    locker = getCurrentLocker(r)
  } else {
    locker = getLockerForLabel(r, label)
  }
  if locker == nil {
    msg = "Locker not found"
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprint(w, msg)
  } else {
    var result interface{}
    if getData {
      result = locker.GetPeerOrAllLockers(peerName, peerAddress)
    } else {
      result = locker.GetPeerOrAllLockersView(peerName, peerAddress)
    }
    util.WriteJsonPayload(w, result)
    if peerName != "" {
      msg = fmt.Sprintf("Peer %s data reported", peerName)
    } else {
      msg = "All peer lockers reported"
    }
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func getTargetsSummaryResults(w http.ResponseWriter, r *http.Request) {
  msg := ""
  label := util.GetStringParamValue(r, "label")
  detailed := util.IsYes(util.GetStringParamValue(r, "detailed"))
  var locker *locker.CombiLocker
  if label == "" || strings.EqualFold(label, constants.LockerCurrent) {
    locker = getCurrentLocker(r)
  } else {
    locker = getLockerForLabel(r, label)
  }
  if locker == nil {
    msg = "Locker not found"
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprint(w, msg)
  } else {
    var result interface{}
    pr := getPortRegistry(r)
    if detailed {
      result = getCurrentLocker(r).GetTargetsResults(pr.trackingHeaders, pr.crossTrackingHeaders)
    } else {
      result = getCurrentLocker(r).GetTargetsSummaryResults(pr.trackingHeaders, pr.crossTrackingHeaders)
    }
    util.WriteJsonPayload(w, result)
    msg = "Reported locker targets results summary"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func checkBadPods(result map[string]map[string]bool, w http.ResponseWriter) {
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
    w.WriteHeader(http.StatusAccepted)
  }
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
      checkBadPods(result, w)
      msg = util.ToJSON(result)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to parse json"
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
  result := getPortRegistry(r).removePeerTargets(peerName, targets)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func stopPeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := getPortRegistry(r).stopPeerTargets(peerName, targets)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  targets := util.GetStringParamValue(r, "targets")
  result := getPortRegistry(r).invokePeerTargets(peerName, targets)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
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
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func enableAllTargetsResultsCollection(w http.ResponseWriter, r *http.Request) {
  result := getPortRegistry(r).enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), true)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
  result := getPortRegistry(r).enableAllOrInvocationsTargetsResultsCollection(util.GetStringParamValue(r, "enable"), false)
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
  if job, err := job.ParseJob(r); err == nil {
    result := getPortRegistry(r).addPeerJob(peerName, &PeerJob{*job})
    checkBadPods(result, w)
    msg = util.ToJSON(result)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to read job"
    log.Println(err)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removePeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs, _ := util.GetListParam(r, "jobs")
  result := getPortRegistry(r).removePeerJobs(peerName, jobs)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func stopPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs := util.GetStringParamValue(r, "jobs")
  result := getPortRegistry(r).stopPeerJobs(peerName, jobs)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func runPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  jobs := util.GetStringParamValue(r, "jobs")
  result := getPortRegistry(r).invokePeerJobs(peerName, jobs)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
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
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func clearPeerEpochs(w http.ResponseWriter, r *http.Request) {
  getPortRegistry(r).clearPeerEpochs()
  w.WriteHeader(http.StatusAccepted)
  msg := "Peers epochs cleared"
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
  getPortRegistry(r).reset()
  w.WriteHeader(http.StatusAccepted)
  msg := "Peers cleared"
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  result := getPortRegistry(r).clearPeerTargets(peerName)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func clearPeerJobs(w http.ResponseWriter, r *http.Request) {
  peerName := util.GetStringParamValue(r, "peer")
  result := getPortRegistry(r).clearPeerJobs(peerName)
  checkBadPods(result, w)
  msg := util.ToJSON(result)
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func addPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if h, present := util.GetStringParam(r, "headers"); present {
    result := getPortRegistry(r).addPeersTrackingHeaders(h)
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

func getPeersTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.ToJSON(pr.peerTrackingHeaders))
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported peer tracking headers", r)
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
    pr := getPortRegistry(r)
    pr.setProbe(isReadiness, uri, 0)
    result := pr.sendProbe(probeType, uri)
    checkBadPods(result, w)
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
    pr := getPortRegistry(r)
    pr.setProbe(isReadiness, "", status)
    result := pr.sendProbeStatus(probeType, status)
    checkBadPods(result, w)
    msg = util.ToJSON(result)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getPeersProbes(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.ToJSON(pr.peerProbes))
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported peer probes", r)
  }
}

func callPeer(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  uri := util.GetStringParamValue(r, "uri")
  if uri == "" {
    msg = "Cannot call peer. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    pr := getPortRegistry(r)
    result := pr.callPeer(peerName, uri, r.Method, r.Header, util.Read(r.Body))
    msg = util.ToJSON(result)
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func copyPeersToLocker(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  currentLocker := getCurrentLocker(r)
  currentLocker.Store([]string{"peers"}, util.ToJSON(pr.peers))
  w.WriteHeader(http.StatusAccepted)
  msg := fmt.Sprintf("Peers info stored in labeled locker %s under path 'peers'", currentLocker.Label)
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
    pr := getPortRegistry(r)
    var err error
    if err = pr.clonePeersFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers data from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = pr.cloneLockersFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone lockers data from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = pr.clonePeersTargetsFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers targets from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = pr.clonePeersJobsFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers jobs from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = pr.clonePeersTrackingHeadersFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers tracking headers from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    if err = pr.clonePeersProbesFrom(url); err != nil {
      msg = fmt.Sprintf("Failed to clone peers probes from registry [%s], error: %s", err.Error())
      w.WriteHeader(http.StatusInternalServerError)
    }
    msg = fmt.Sprintf("Cloned data from registry [%s]", url)
  }
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func dumpLockerData(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pr := getPortRegistry(r)
  label := util.GetStringParamValue(r, "label")
  val := util.GetStringParamValue(r, "path")
  path, _ := util.GetListParam(r, "path")

  if label != "" {
    var locker *locker.CombiLocker
    if strings.EqualFold(label, constants.LockerCurrent) {
      locker = getCurrentLocker(r)
    } else {
      locker = getLockerForLabel(r, label)
    }
    if strings.EqualFold(val, constants.LockerPeers) {
      util.WriteJsonPayload(w, locker.PeerLockers)
      msg = fmt.Sprintf("Dumped peer lockers [%s]\n", label)
    } else if len(path) > 0 {
      data, dataAtKey := locker.Get(path)
      if dataAtKey {
        fmt.Fprint(w, data)
      } else {
        util.WriteJsonPayload(w, data)
      }
      msg = fmt.Sprintf("Dumped data from locker [%s] path [%s]\n", label, val)
    } else {
      util.WriteJsonPayload(w, locker)
      msg = fmt.Sprintf("Dumped locker [%s]\n", label)
    }
  } else {
    util.WriteJsonPayload(w, pr.labeledLockers.GetAllLockers())
    msg = "Dumped all lockers\n"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func dumpRegistry(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  dump := map[string]interface{}{}
  pr.lockersLock.RLock()
  dump["lockers"] = pr.labeledLockers.GetAllLockers()
  pr.lockersLock.RUnlock()
  pr.peersLock.RLock()
  dump["peers"] = pr.peers
  dump["peerTargets"] = pr.peerTargets
  dump["peerJobs"] = pr.peerJobs
  dump["peerTrackingHeaders"] = pr.peerTrackingHeaders
  dump["peerProbes"] = pr.peerProbes
  pr.peersLock.RUnlock()
  fmt.Fprintln(w, util.ToJSON(dump))
  msg := "Registry data dumped"
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
  pr := getPortRegistry(r)

  msg := ""
  if dump["lockers"] != nil {
    lockersData := util.ToJSON(dump["lockers"])
    if lockersData != "" {
      lockers := map[string]*locker.CombiLocker{}
      if err := util.ReadJson(lockersData, &lockers); err == nil {
        pr.lockersLock.Lock()
        pr.labeledLockers.ReplaceLockers(lockers)
        pr.lockersLock.Unlock()
      } else {
        msg += fmt.Sprintf("[failed to load lockers with error: %s]", err.Error())
      }
    }
  }
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()

  if dump["peers"] != nil {
    peersData := util.ToJSON(dump["peers"])
    if peersData != "" {
      pr.peers = map[string]*Peers{}
      if err := util.ReadJson(peersData, &pr.peers); err != nil {
        msg += fmt.Sprintf("[failed to load peers with error: %s]", err.Error())
      }
    }
  }
  if dump["peerTargets"] != nil {
    peerTargetsData := util.ToJSON(dump["peerTargets"])
    if peerTargetsData != "" {
      pr.peerTargets = map[string]PeerTargets{}
      if err := util.ReadJson(peerTargetsData, &pr.peerTargets); err != nil {
        msg += fmt.Sprintf("[failed to load peer targets with error: %s]", err.Error())
      }
    }
  }
  if dump["peerJobs"] != nil {
    peerJobsData := util.ToJSON(dump["peerJobs"])
    if peerJobsData != "" {
      pr.peerJobs = map[string]PeerJobs{}
      if err := util.ReadJson(peerJobsData, &pr.peerJobs); err != nil {
        msg += fmt.Sprintf("[failed to load peer jobs with error: %s]", err.Error())
      }
    }
  }
  if dump["peerTrackingHeaders"] != nil {
    pr.peerTrackingHeaders = dump["peerTrackingHeaders"].(string)
    pr.trackingHeaders, pr.crossTrackingHeaders = util.ParseTrackingHeaders(pr.peerTrackingHeaders)
  }
  if dump["peerProbes"] != nil {
    peerProbesData := util.ToJSON(dump["peerProbes"])
    if peerProbesData != "" {
      pr.peerProbes = &PeerProbes{}
      if err := util.ReadJson(peerProbesData, &pr.peerProbes); err != nil {
        msg += fmt.Sprintf("[failed to load peer probes with error: %s]", err.Error())
      }
    }
  }
  if msg == "" {
    msg = "Registry data loaded"
  }
  fmt.Fprintln(w, msg)
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}
