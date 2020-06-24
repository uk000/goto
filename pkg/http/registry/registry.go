package registry

import (
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/http/client/results"
	"goto/pkg/http/invocation"
	"goto/pkg/http/registry/locker"
	"goto/pkg/job"
	"goto/pkg/util"
	"log"
	"net"
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
  Healthy bool   `json:"healthy"`
  host    string
  client  *http.Client
}

type Peers struct {
  Name      string          `json:"name"`
  Namespace string          `json:"namespace"`
  Pods      map[string]*Pod `json:"pods"`
}

type PeerTarget struct {
  invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PeerJob struct {
  job.Job
}

type PeerJobs map[string]*PeerJob

type PortRegistry struct {
  peers       map[string]*Peers
  peerTargets map[string]PeerTargets
  peerJobs    map[string]PeerJobs
  peerLocker  *locker.PeersLockers
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
  util.AddRouteQ(peersRouter, "/lockers/targets/results", getTargetsSummaryResults, "detailed", "{detailed}", "GET")
  util.AddRoute(peersRouter, "/lockers/targets/results", getTargetsSummaryResults, "GET")

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

func getPortRegistryLocker(r *http.Request) *locker.PeersLockers {
  return getPortRegistry(r).peerLocker
}

func (pr *PortRegistry) reset() {
  pr.peersLock.Lock()
  pr.peers = map[string]*Peers{}
  pr.peerTargets = map[string]PeerTargets{}
  pr.peerJobs = map[string]PeerJobs{}
  pr.peersLock.Unlock()
  pr.lockersLock.Lock()
  pr.peerLocker = locker.NewPeersLocker()
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

func (pr *PortRegistry) addPeer(peer *Peer) {
  pr.peersLock.Lock()
  defer pr.peersLock.Unlock()
  if pr.peers[peer.Name] == nil {
    pr.peers[peer.Name] = &Peers{Name: peer.Name, Namespace: peer.Namespace, Pods: map[string]*Pod{}}
  }
  pod := &Pod{Name: peer.Pod, Address: peer.Address, host: "http://" + peer.Address, Healthy: true}
  pr.initHttpClientForPeerPod(pod)
  pr.peers[peer.Name].Pods[peer.Address] = pod
  if pr.peerTargets[peer.Name] == nil {
    pr.peerTargets[peer.Name] = PeerTargets{}
  }
  if pr.peerJobs[peer.Name] == nil {
    pr.peerJobs[peer.Name] = PeerJobs{}
  }
  pr.peerLocker.InitPeerLocker(peer.Name)
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
  pr.peerLocker.RemovePeerLocker(name)
  return present
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

func invokePeerAPI(pod *Pod, method, api string, payload string, expectedStatus int) (bool, error) {
  if strings.EqualFold(method, "GET") {
    if resp, err := pod.client.Get(pod.host + api); err == nil {
      util.CloseResponse(resp)
      if resp.StatusCode == expectedStatus {
        return true, nil
      } else {
        return false, fmt.Errorf("Expected status %d but received %d", expectedStatus, resp.StatusCode)
      }
    } else {
      return false, err
    }
  } else {
    var payloadReader *strings.Reader
    contentType := "plain/text"
    if len(payload) > 0 {
      payloadReader = strings.NewReader(payload)
      contentType = "application/json"
    } else {
      payloadReader = strings.NewReader("")
    }
    if resp, err := pod.client.Post(pod.host+api, contentType, payloadReader); err == nil {
      util.CloseResponse(resp)
      if resp.StatusCode == expectedStatus {
        return true, nil
      } else {
        return false, fmt.Errorf("Expected status %d but received %d", expectedStatus, resp.StatusCode)
      }
    } else {
      return false, err
    }
  }
}

func invokeForPods(peerPods map[string][]*Pod, method string, uri string, payload string, expectedStatus int, retryCount int, useUnhealthy bool,
  onPodDone func(string, *Pod, error), onPeerDone ...func(string)) map[string]map[string]bool {
  result := map[string]map[string]bool{}
  for peer, pods := range peerPods {
    result[peer] = map[string]bool{}
    for _, pod := range pods {
      if !useUnhealthy && !pod.Healthy {
        log.Printf("Skipping bad pod %s for peer %s for URI %s.\n", pod.Address, peer, uri)
        result[peer][pod.Address] = false
        continue
      }
      var success bool
      var err error
      for i := 0; i < retryCount; i++ {
        success, err = invokePeerAPI(pod, method, uri, payload, expectedStatus)
        if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
          log.Printf("Peer %s Pod %s timed out for URI %s. Retrying... %d\n", peer, pod.Address, uri, i+1)
          continue
        } else {
          break
        }
      }
      result[peer][pod.Address] = success
      if success && err == nil {
        onPodDone(peer, pod, nil)
      } else if err != nil {
        if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
          if len(pods) > 1 {
            log.Printf("Peer %s Pod %s has too many timouts. Marking pod as bad and removing from future operations\n", peer, pod.Address)
            pod.Healthy = false
          } else {
            log.Printf("Peer %s Pod %s has timed out but not marking pod as bad since it's the only pod available for the peer\n", peer, pod.Address)
            pod.Healthy = true
          }
        }
        onPodDone(peer, pod, err)
      } else {
        onPodDone(peer, pod, errors.New(""))
      }
    }
    if len(onPeerDone) > 0 {
      onPeerDone[0](peer)
    }
  }
  return result
}

func (pr *PortRegistry) loadAllPeerPods() map[string][]*Pod {
  pr.peersLock.RLock()
  defer pr.peersLock.RUnlock()
  peerPods := map[string][]*Pod{}
  for name, peer := range pr.peers {
    peerPods[name] = []*Pod{}
    for _, pod := range peer.Pods {
      peerPods[name] = append(peerPods[name], pod)
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
          peerPods[peerName] = append(peerPods[peerName], pod)
        }
      }
    } else {
      if pr.peers[peerName] != nil {
        for _, pod := range pr.peers[peerName].Pods {
          peerPods[peerName] = append(peerPods[peerName], pod)
        }
      }
    }
  } else {
    for name, peer := range pr.peers {
      peerPods[name] = []*Pod{}
      for _, pod := range peer.Pods {
        peerPods[name] = append(peerPods[name], pod)
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
        peerPods[peerName] = append(peerPods[peerName], pod)
      }
    }
    return peerPods
  }
  return pr.loadAllPeerPods()
}

func (pr *PortRegistry) checkPeerHealth(peerName string, peerAddress string) map[string]map[string]bool {
  return invokeForPods(pr.loadPeerPods(peerName, peerAddress), "GET", "/health", "", http.StatusOK, 2, true,
    func(peer string, pod *Pod, err error) {
      if err == nil {
        pod.Healthy = true
        log.Printf("Peer %s Address %s is healthy\n", peer, pod.Address)
      } else {
        pod.Healthy = false
        log.Printf("Peer %s Address %s is unhealthy, error: %s\n", peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) cleanupUnhealthyPeers(peerName string) map[string]map[string]bool {
  return invokeForPods(pr.loadPeerPods(peerName, ""), "GET", "/health", "", http.StatusOK, 2, true,
    func(peer string, pod *Pod, err error) {
      if err == nil {
        pod.Healthy = true
        log.Printf("Peer %s Address %s is healthy\n", peer, pod.Address)
      } else {
        log.Printf("Peer %s Address %s is unhealthy or unavailable, error: %s\n", peer, pod.Address, err.Error())
        delete(pr.peers[peer].Pods, pod.Address)
        if len(pr.peers[peer].Pods) == 0 {
          delete(pr.peers, peer)
        }
      }
    })
}

func (pr *PortRegistry) clearLocker(peerName, peerAddress string) map[string]map[string]bool {
  peersToClear := map[string][]*Pod{}
  if peerName != "" && peerAddress != "" {
    if pr.peerLocker.ClearInstanceLocker(peerName, peerAddress) {
      peersToClear = pr.loadPeerPods(peerName, peerAddress)
    }
  } else if peerName != "" && peerAddress == "" {
    if pr.peerLocker.InitPeerLocker(peerName) {
      peersToClear = pr.loadPeerPods(peerName, "")
    }
  } else {
    pr.peerLocker.Init()
    peersToClear = pr.loadPeerPods("", "")
  }
  return invokeForPods(peersToClear, "POST", "/client/results/clear", "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(peerPods, "POST", "/client/targets/add", util.ToJSON(target), http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
    "POST", fmt.Sprintf("/client/targets/%s/remove", targetList), "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadPodsForPeerWithData(peerName), "POST", "/client/targets/clear", "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadPodsForPeerWithData(peerName), "POST", uri, "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadAllPeerPods(), "POST", uri, "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(peerPods, "POST", "/jobs/add", util.ToJSON(job), http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
    "POST", fmt.Sprintf("/jobs/%s/remove", jobList), "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true), "POST", uri, "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadPodsForPeerWithData(peerName), "POST", uri, "", http.StatusAccepted, 1, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true), "POST", uri, "", http.StatusAccepted, 1, false,
    func(peer string, pod *Pod, err error) {
      if err == nil {
        log.Printf("Invoked jobs %s on peer %s address %s\n", jobs, peer, pod.Address)
      } else {
        log.Printf("Failed to invoke jobs %s on peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
      }
    })
}

func (pr *PortRegistry) clearPeerJobs(peerName string) map[string]map[string]bool {
  cleared := true
  return invokeForPods(pr.loadPodsForPeerWithData(peerName, true), "POST", "/jobs/clear", "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
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
  return invokeForPods(pr.loadAllPeerPods(), "POST", "/client/track/headers/add/"+headers, "", http.StatusAccepted, 3, false,
    func(peer string, pod *Pod, err error) {
      if err == nil {
        log.Printf("Pushed tracking headers %s to peer %s address %s\n", headers, peer, pod.Address)
      } else {
        log.Printf("Failed to add tracking headers %s to peer %s address %s with error %s\n", headers, peer, pod.Address, err.Error())
      }
    })
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
  getPortRegistry(r).cleanupUnhealthyPeers(util.GetStringParamValue(r, "peer"))
  w.WriteHeader(http.StatusOK)
  msg := "Cleaned up unhealthy peers"
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func storeInPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  keys, _ := util.GetListParam(r, "keys")
  if peerName != "" && address != "" && len(keys) > 0 {
    data := util.Read(r.Body)
    getPortRegistryLocker(r).Store(peerName, address, keys, data)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data stored for keys %+v", peerName, keys)
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
  keys, _ := util.GetListParam(r, "keys")
  if peerName != "" && address != "" && len(keys) > 0 {
    getPortRegistryLocker(r).Remove(peerName, address, keys)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data removed for keys %+v", peerName, keys)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Not enough parameters to access locker"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func lockInPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  keys, _ := util.GetListParam(r, "keys")
  if peerName != "" && address != "" && len(keys) > 0 {
    getPortRegistryLocker(r).LockKeys(peerName, address, keys)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Peer %s data for keys %+v is locked", peerName, keys)
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

func getPeerLocker(w http.ResponseWriter, r *http.Request) {
  msg := ""
  peerName := util.GetStringParamValue(r, "peer")
  address := util.GetStringParamValue(r, "address")
  w.WriteHeader(http.StatusOK)
  util.WriteJsonPayload(w, getPortRegistryLocker(r).GetPeerLocker(peerName, address))
  if peerName != "" {
    msg = fmt.Sprintf("Peer %s data reported", peerName)
  } else {
    msg = "All peer lockers reported"
  }
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
}

func getTargetsSummaryResults(w http.ResponseWriter, r *http.Request) {
  detailed := util.IsYes(util.GetStringParamValue(r, "detailed"))
  var result interface{}
  if detailed {
    result = getPortRegistryLocker(r).GetTargetsResults()
  } else {
    result = getPortRegistryLocker(r).GetTargetsSummaryResults()
  }
  w.WriteHeader(http.StatusAlreadyReported)
  util.WriteJsonPayload(w, result)
  if global.EnableRegistryLogs {
    util.AddLogMessage("Reported locker targets results summary", r)
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
  if global.EnableRegistryLogs {
    util.AddLogMessage(msg, r)
  }
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

func GetPeers(name string, r *http.Request) map[string]string {
  peers := getPortRegistry(r).peers[name]
  data := map[string]string{}
  for _, pod := range peers.Pods {
    data[pod.Name] = pod.Address
  }
  return data
}
