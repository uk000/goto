package registry

import (
	"errors"
	"fmt"
	"goto/pkg/http/invocation"
	"goto/pkg/http/registry/peer"
	"goto/pkg/job/jobtypes"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type PortRegistry struct {
  peers       map[string]*peer.Peers
  peerTargets map[string]peer.PeerTargets
  peerJobs    map[string]peer.PeerJobs
  lock        sync.RWMutex
}

var (
  Handler      util.ServerHandler       = util.ServerHandler{Name: "registry", SetRoutes: SetRoutes}
  portRegistry map[string]*PortRegistry = map[string]*PortRegistry{}
  PeerName     string
  RegistryURL  string
  registryLock sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  registryRouter := r.PathPrefix("/registry").Subrouter()
  peersRouter := registryRouter.PathPrefix("/peers").Subrouter()
  util.AddRoute(peersRouter, "/add", addPeer, "POST")
  util.AddRoute(peersRouter, "/{peer}/remove/{address}", removePeer, "PUT", "POST")

  util.AddRoute(peersRouter, "/{peer}/targets/add", addPeerTarget, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/remove", removePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/{targets}/invoke", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/invoke/all", invokePeerTargets, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets", getPeerTargets, "GET")
  util.AddRoute(peersRouter, "/targets", getPeerTargets, "GET")

  util.AddRoute(peersRouter, "/{peer}/jobs/add", addPeerJob, "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/remove", removePeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/{jobs}/invoke", invokePeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/invoke/all", invokePeerJobs, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs/clear", clearPeerJobs, "POST")
  util.AddRoute(peersRouter, "/{peer}/jobs", getPeerJobs, "GET")
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

func (pr *PortRegistry) init() {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  pr.peers = map[string]*peer.Peers{}
  pr.peerTargets = map[string]peer.PeerTargets{}
  pr.peerJobs = map[string]peer.PeerJobs{}
}

func (pr *PortRegistry) addPeer(p *peer.Peer) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peers[p.Name] == nil {
    pr.peers[p.Name] = &peer.Peers{Name: p.Name, Addresses: map[string]int{p.Address: 1}}
  } else {
    pr.peers[p.Name].Addresses[p.Address]++
  }
  if pr.peerTargets[p.Name] == nil {
    pr.peerTargets[p.Name] = peer.PeerTargets{}
  }
}

func (pr *PortRegistry) removePeer(name string, address string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  present := false
  if _, present = pr.peers[name]; present {
    delete(pr.peers[name].Addresses, address)
    if len(pr.peers[name].Addresses) == 0 {
      delete(pr.peers, name)
    }
  }
  return present
}

func (pr *PortRegistry) addPeerTarget(peerName string, t *peer.PeerTarget) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerTargets[peerName] == nil {
    pr.peerTargets[peerName] = peer.PeerTargets{}
  }
  pr.peerTargets[peerName][t.Name] = t
  if pr.peers[peerName] != nil {
    for a := range pr.peers[peerName].Addresses {
      if resp, err := http.Post("http://"+a+"/client/targets/add", "application/json", strings.NewReader(util.ToJSON(t))); err == nil {
        defer resp.Body.Close()
        log.Printf("Pushed target %s to peer %s address %s\n", t.Name, peerName, a)
      } else {
        log.Println(err.Error())
      }
    }
  }
}

func (pr *PortRegistry) removePeerTargets(peerName string, targets []string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerTargets[peerName] != nil {
    for _, target := range targets {
      if pr.peerTargets[peerName][target] != nil {
        if pr.peers[peerName] != nil {
          for a := range pr.peers[peerName].Addresses {
            if resp, err := http.Post("http://"+a+"/client/targets/"+target+"/remove", "plain/text", nil); err == nil {
              defer resp.Body.Close()
              log.Printf("Removed target %s from peer %s address %s\n", target, peerName, a)
            } else {
              log.Println(err.Error())
            }
          }
        }
        delete(pr.peerTargets[peerName], target)
      }
    }
    return true
  }
  return false
}

func (pr *PortRegistry) addPeerJob(peerName string, job *peer.PeerJob) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerJobs[peerName] == nil {
    pr.peerJobs[peerName] = peer.PeerJobs{}
  }
  pr.peerJobs[peerName][job.ID] = job
  if pr.peers[peerName] != nil {
    for a := range pr.peers[peerName].Addresses {
      if resp, err := http.Post("http://"+a+"/jobs/add", "application/json", strings.NewReader(util.ToJSON(job))); err == nil {
        defer resp.Body.Close()
        log.Printf("Pushed job %s to peer %s address %s\n", job.ID, peerName, a)
      } else {
        log.Println(err.Error())
      }
    }
  }
}

func (pr *PortRegistry) removePeerJobs(peerName string, jobs []string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerJobs[peerName] != nil {
    for _, job := range jobs {
      if pr.peerJobs[peerName][job] != nil {
        if pr.peers[peerName] != nil {
          for a := range pr.peers[peerName].Addresses {
            if resp, err := http.Post("http://"+a+"/jobs/"+job+"/remove", "plain/text", nil); err == nil {
              defer resp.Body.Close()
              log.Printf("Removed job %s from peer %s address %s\n", job, peerName, a)
            } else {
              log.Println(err.Error())
            }
          }
        }
        delete(pr.peerJobs[peerName], job)
      }
    }
    return true
  }
  return false
}

func (pr *PortRegistry) invokePeerTargets(peerName string, targets string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peers[peerName] != nil {
    for a := range pr.peers[peerName].Addresses {
      var resp *http.Response
      var err error
      if len(targets) > 0 {
        resp, err = http.Post("http://"+a+"/client/targets/"+targets+"/invoke", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+a+"/client/targets/invoke/all", "plain/text", nil)
      }
      if err == nil {
        defer resp.Body.Close()
        log.Printf("Invoked target %s on peer %s address %s\n", targets, peerName, a)
      } else {
        log.Println(err.Error())
        return err
      }
    }
    return nil
  }
  return errors.New("Peer not found")
}

func (pr *PortRegistry) invokePeerJobs(peerName string, jobs string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peers[peerName] != nil {
    for a := range pr.peers[peerName].Addresses {
      var resp *http.Response
      var err error
      if len(jobs) > 0 {
        resp, err = http.Post("http://"+a+"/jobs/"+jobs+"/invoke", "plain/text", nil)
      } else {
        resp, err = http.Post("http://"+a+"/jobs/invoke/all", "plain/text", nil)
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
  pr.lock.Lock()
  defer pr.lock.Unlock()
  present := false
  if _, present = pr.peerTargets[peerName]; present {
    if pr.peers[peerName] != nil {
      for a := range pr.peers[peerName].Addresses {
        if resp, err := http.Post("http://"+a+"/client/targets/clear", "plain/text", nil); err == nil {
          defer resp.Body.Close()
          log.Printf("Cleared targets from peer %s address %s\n", peerName, a)
        } else {
          log.Println(err.Error())
        }
      }
    }
    delete(pr.peerTargets, peerName)
  }
  return present
}

func (pr *PortRegistry) clearPeerJobs(peerName string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  present := false
  if _, present = pr.peerJobs[peerName]; present {
    if pr.peers[peerName] != nil {
      for a := range pr.peers[peerName].Addresses {
        if resp, err := http.Post("http://"+a+"/jobs/clear", "plain/text", nil); err == nil {
          defer resp.Body.Close()
          log.Printf("Cleared jobs from peer %s address %s\n", peerName, a)
        } else {
          log.Println(err.Error())
        }
      }
    }
    delete(pr.peerJobs, peerName)
  }
  return present
}

func addPeer(w http.ResponseWriter, r *http.Request) {
  p := &peer.Peer{}
  if err := util.ReadJsonPayload(r, p); err == nil {
    pr := getPortRegistry(r)
    pr.addPeer(p)
    util.AddLogMessage(fmt.Sprintf("Added Peer: %+v", *p), r)
    w.WriteHeader(http.StatusOK)
    pr.lock.RLock()
    defer pr.lock.RUnlock()
    payload := map[string]interface{}{"targets": pr.peerTargets[p.Name], "jobs": pr.peerJobs[p.Name]}
    fmt.Fprintln(w, util.ToJSON(payload))
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Failed to parse json\n")
    log.Println(err)
  }
}

func removePeer(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if address, present := util.GetStringParam(r, "address"); present {
      if getPortRegistry(r).removePeer(peer, address) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer Removed: %s\n", peer)
      } else {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer not found: %s\n", peer)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintln(w, "No address given")
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
  }
}

func addPeerTarget(w http.ResponseWriter, r *http.Request) {
  if peerName, present := util.GetStringParam(r, "peer"); present {
    t := &peer.PeerTarget{}
    if err := util.ReadJsonPayload(r, t); err == nil {
      if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
        w.WriteHeader(http.StatusBadRequest)
        fmt.Fprintf(w, "Invalid target spec: %s\n", err.Error())
        log.Println(err)
      } else {
        getPortRegistry(r).addPeerTarget(peerName, t)
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Added peer target: %s\n", util.ToJSON(t))
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Failed to parse json\n")
      log.Println(err)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "No peer given\n")
  }
}

func removePeerTargets(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if targets, present := util.GetListParam(r, "targets"); present {
      if getPortRegistry(r).removePeerTargets(peer, targets) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer %s targets %+v removed\n", peer, targets)
      } else {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer %s targets %+v not found\n", peer, targets)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintln(w, "No targets given")
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
  }
}

func addPeerJob(w http.ResponseWriter, r *http.Request) {
  if peerName, present := util.GetStringParam(r, "peer"); present {
    if job, err := jobtypes.ParseJob(r); err == nil {
      getPortRegistry(r).addPeerJob(peerName, &peer.PeerJob{*job})
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Added peer job: %s\n", util.ToJSON(job))
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Failed to read job\n")
      log.Println(err)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "No peer given\n")
  }
}

func removePeerJobs(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if jobs, present := util.GetListParam(r, "jobs"); present {
      if getPortRegistry(r).removePeerTargets(peer, jobs) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer %s jobs %+v removed\n", peer, jobs)
      } else {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer %s jobs %+v not found\n", peer, jobs)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintln(w, "No jobs given")
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
  }
}

func invokePeerTargets(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    targets := util.GetStringParamValue(r, "targets")
    if err := getPortRegistry(r).invokePeerTargets(peer, targets); err == nil {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Targets [%s] invoked on peer [%s]\n", targets, peer)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Failed to invoke targets [%s] on peer [%s]\n", targets, peer)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peers given")
  }
}

func invokePeerJobs(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    jobs := util.GetStringParamValue(r, "jobs")
    if err := getPortRegistry(r).invokePeerJobs(peer, jobs); err == nil {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Jobs [%s] invoked on peer [%s]\n", jobs, peer)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Failed to invoke jobs [%s] on peer [%s]\n", jobs, peer)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peers given")
  }
}

func getPeers(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  peers := []peer.Peer{}
  for _, p := range pr.peers {
    for address := range p.Addresses {
      peers = append(peers, peer.Peer{p.Name, address})
    }
  }
  util.WriteJsonPayload(w, peers)
}

func getPeerTargets(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if getPortRegistry(r).peerTargets[peer] != nil {
      util.WriteJsonPayload(w, getPortRegistry(r).peerTargets[peer])
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Peer not found: %s\n", peer)
    }
  } else {
    util.WriteJsonPayload(w, getPortRegistry(r).peerTargets)
  }
}

func getPeerJobs(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if getPortRegistry(r).peerJobs[peer] != nil {
      util.WriteJsonPayload(w, getPortRegistry(r).peerJobs[peer])
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "No jobs for peer %s\n", peer)
    }
  } else {
    util.WriteJsonPayload(w, getPortRegistry(r).peerJobs)
  }
}

func clearPeers(w http.ResponseWriter, r *http.Request) {
  getPortRegistry(r).init()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "Peers cleared")
}

func clearPeerTargets(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if getPortRegistry(r).clearPeerTargets(peer) {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Peer targets removed: %s\n", peer)
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Peer not found: %s\n", peer)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
  }
}

func clearPeerJobs(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if getPortRegistry(r).clearPeerJobs(peer) {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Peer jobs removed: %s\n", peer)
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "No jobs for peer %s\n", peer)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
  }
}

func GetPeer(name string, r *http.Request) *peer.Peers {
  return getPortRegistry(r).peers[name]
}
