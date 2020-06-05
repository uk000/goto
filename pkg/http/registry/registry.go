package registry

import (
	"fmt"
	"goto/pkg/http/invocation"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type Peer struct {
  Name    string `json:"name"`
  Address string `json:"address"`
}

type Peers struct {
  Name      string         `json:"name"`
  Addresses map[string]int `json:"addresses"`
}

type PeerTarget struct {
  invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PortRegistry struct {
  peers       map[string]*Peers
  peerTargets map[string]PeerTargets
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
  util.AddRoute(peersRouter, "/{peer}/targets/{target}/remove", removePeerTarget, "PUT", "POST")
  util.AddRoute(peersRouter, "/{peer}/targets/clear", clearPeerTargets, "POST")
  util.AddRoute(peersRouter, "/{peer}/targets", getPeerTargets, "GET")
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
  pr.peers = map[string]*Peers{}
  pr.peerTargets = map[string]PeerTargets{}
}

func (pr *PortRegistry) addPeer(p *Peer) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peers[p.Name] == nil {
    pr.peers[p.Name] = &Peers{Name: p.Name, Addresses: map[string]int{p.Address: 1}}
  } else {
    pr.peers[p.Name].Addresses[p.Address]++
  }
  if pr.peerTargets[p.Name] == nil {
    pr.peerTargets[p.Name] = PeerTargets{}
  }
}

func (pr *PortRegistry) addPeerTarget(peer string, t *PeerTarget) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerTargets[peer] == nil {
    pr.peerTargets[peer] = PeerTargets{}
  }
  pr.peerTargets[peer][t.Name] = t
  if pr.peers[peer] != nil {
    for a := range pr.peers[peer].Addresses {
      if resp, err := http.Post("http://"+a+"/client/targets/add", "application/json", strings.NewReader(util.ToJSON(t))); err == nil {
        defer resp.Body.Close()
        log.Printf("Pushed target %s to peer %s address %s\n", t.Name, peer, a)
      } else {
        log.Println(err.Error())
      }
    }
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

func (pr *PortRegistry) removePeerTarget(peer string, target string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if pr.peerTargets[peer] != nil {
    if pr.peerTargets[peer][target] != nil {
      if pr.peers[peer] != nil {
        for a := range pr.peers[peer].Addresses {
          if resp, err := http.Post("http://"+a+"/client/targets/"+target+"/remove", "plain/text", nil); err == nil {
            defer resp.Body.Close()
            log.Printf("Removed target %s from peer %s address %s\n", target, peer, a)
          } else {
            log.Println(err.Error())
          }
        }
      }
      delete(pr.peerTargets[peer], target)
    }
    return true
  }
  return false
}

func (pr *PortRegistry) clearPeerTargets(peer string) bool {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  present := false
  if _, present = pr.peerTargets[peer]; present {
    for a := range pr.peers[peer].Addresses {
      if resp, err := http.Post("http://"+a+"/client/targets/clear", "plain/text", nil); err == nil {
        defer resp.Body.Close()
        log.Printf("Cleared targets from peer %s address %s\n", peer, a)
      } else {
        log.Println(err.Error())
      }
    }
    delete(pr.peerTargets, peer)
  }
  return present
}

func addPeer(w http.ResponseWriter, r *http.Request) {
  p := &Peer{}
  if err := util.ReadJsonPayload(r, p); err == nil {
    pr := getPortRegistry(r)
    pr.addPeer(p)
    w.WriteHeader(http.StatusOK)
    pr.lock.RLock()
    defer pr.lock.RUnlock()
    fmt.Fprintln(w, util.ToJSON(pr.peerTargets[p.Name]))
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Failed to parse json\n")
    log.Println(err)
  }
}

func addPeerTarget(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    t := &PeerTarget{}
    if err := util.ReadJsonPayload(r, t); err == nil {
      if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
        w.WriteHeader(http.StatusBadRequest)
        fmt.Fprintf(w, "Invalid target spec: %s\n", err.Error())
        log.Println(err)
      } else {
        getPortRegistry(r).addPeerTarget(peer, t)
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

func removePeerTarget(w http.ResponseWriter, r *http.Request) {
  if peer, present := util.GetStringParam(r, "peer"); present {
    if target, present := util.GetStringParam(r, "target"); present {
      if getPortRegistry(r).removePeerTarget(peer, target) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer %s target %s removed\n", peer, target)
      } else {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Peer %s target %s not found\n", peer, target)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintln(w, "No target given")
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
  }
}

func getPeers(w http.ResponseWriter, r *http.Request) {
  pr := getPortRegistry(r)
  peers := []Peer{}
  for _, p := range pr.peers {
    for address := range p.Addresses {
      peers = append(peers, Peer{p.Name, address})
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
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No peer given")
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

func GetPeer(name string, r *http.Request) *Peers {
  return getPortRegistry(r).peers[name]
}
