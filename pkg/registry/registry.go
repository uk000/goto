/**
 * Copyright 2026 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package registry

import (
	"encoding/json"
	"fmt"
	"goto/pkg/client/results"
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
	lock      sync.RWMutex
}

type PeerPods map[string][]*Pod
type PodResults map[string]bool
type PeerResults map[string]map[string]bool

type PeerTarget struct {
	*invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PeerJob struct {
	*job.Job
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
	contextLockers          *locker.ContextLockers
	peersLock               sync.RWMutex
	lockersLock             sync.RWMutex
}

var (
	registry = &Registry{
		peers:          map[string]*Peers{},
		peerTargets:    map[string]PeerTargets{},
		peerJobs:       map[string]PeerJobs{},
		peerJobScripts: map[string]PeerJobScripts{},
		peerFiles:      map[string]PeerFiles{},
		labeledLockers: locker.NewLabeledLockers(),
		contextLockers: locker.NewContextLockers(),
	}
)

// init registers GetPeers as the global peers lookup function.
func init() {
	global.Funcs.GetPeers = GetPeers
}

// reset clears all peers, targets, jobs, scripts and files, and replaces the labeled lockers with fresh ones.
func (registry *Registry) reset() {
	registry.peersLock.Lock()
	registry.peers = map[string]*Peers{}
	registry.peerTargets = map[string]PeerTargets{}
	registry.peerJobs = map[string]PeerJobs{}
	registry.peerJobScripts = map[string]PeerJobScripts{}
	registry.peerFiles = map[string]PeerFiles{}
	registry.peersLock.Unlock()
	registry.lockersLock.Lock()
	registry.labeledLockers = locker.NewLabeledLockers()
	registry.lockersLock.Unlock()
}

// getCurrentLocker returns the currently active locker of the labeled lockers.
func (registry *Registry) getCurrentLocker() *locker.CombiLocker {
	registry.lockersLock.RLock()
	defer registry.lockersLock.RUnlock()
	return registry.labeledLockers.GetCurrentLocker()
}

// unsafeAddPeer adds the peer pod with a fresh epoch that keeps its past epochs; the caller must hold peersLock.
func (registry *Registry) unsafeAddPeer(peer *Peer) {
	now := time.Now()
	if registry.peers[peer.Name] == nil {
		registry.peers[peer.Name] = &Peers{Name: peer.Name, Namespace: peer.Namespace, Pods: map[string]*Pod{}, PodEpochs: map[string][]*PodEpoch{}}
	}
	if registry.peers[peer.Name].Pods == nil {
		registry.peers[peer.Name].Pods = map[string]*Pod{}
	}
	if registry.peers[peer.Name].PodEpochs == nil {
		registry.peers[peer.Name].PodEpochs = map[string][]*PodEpoch{}
	}
	pod := &Pod{Name: peer.Pod, Address: peer.Address, URL: "http://" + peer.Address,
		Node: peer.Node, Cluster: peer.Cluster, Healthy: true,
		CurrentEpoch: PodEpoch{Name: peer.Pod, Address: peer.Address, Node: peer.Node, Cluster: peer.Cluster, FirstContact: now, LastContact: now}}
	registry.initHttpClientForPeerPod(pod)
	if podEpochs := registry.peers[peer.Name].PodEpochs[peer.Address]; podEpochs != nil {
		pod.PastEpochs = append(pod.PastEpochs, podEpochs...)
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

// addPeer adds the peer under the peers lock and initializes its peer locker in the current locker.
func (registry *Registry) addPeer(peer *Peer) {
	registry.peersLock.Lock()
	defer registry.peersLock.Unlock()
	registry.unsafeAddPeer(peer)
	registry.getCurrentLocker().InitPeerLocker(peer.Name, peer.Address)
}

// GetPeer returns the pod registered for the peer name and address, or nil if there is none.
func (registry *Registry) GetPeer(peerName, peerAddress string) *Pod {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	if registry.peers[peerName] != nil && registry.peers[peerName].Pods[peerAddress] != nil {
		return registry.peers[peerName].Pods[peerAddress]
	}
	return nil
}

// rememberPeer refreshes the contact time and health of a known peer pod, or adds the peer if it is not known yet.
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

// removePeer removes the peer pod at the address and deactivates its instance locker, returning whether the peer was known.
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

// clearPeerEpochs drops the past epochs of all pods, and deletes peers (and their peer lockers) left without pods.
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

// initHttpClientForPeerPod creates the pooled HTTP client with a 10s timeout that the registry uses to call the pod.
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

// getPodsArray returns the pods from the map that have an HTTP client set.
func getPodsArray(pods map[string]*Pod) []*Pod {
	copy := []*Pod{}
	for _, pod := range pods {
		if pod != nil && pod.client != nil {
			copy = append(copy, pod)
		}
	}
	return copy
}

// normalizePeers cleans up peers that were decoded from JSON (a loaded registry dump, or the peers of another
// registry), which can hold null peers and pods, or peers without the pods and epochs maps, that the peer APIs
// would otherwise run into. It returns the peers without the null ones, with the maps allocated and the null pods and
// epochs dropped, and the pods marked offline if asked to (they have no connection to a live peer until the peer
// registers again).
func normalizePeers(peers map[string]*Peers, markOffline bool) map[string]*Peers {
	normalized := map[string]*Peers{}
	for name, peer := range peers {
		if peer == nil {
			continue
		}
		if peer.Name == "" {
			peer.Name = name
		}
		if peer.Pods == nil {
			peer.Pods = map[string]*Pod{}
		}
		if peer.PodEpochs == nil {
			peer.PodEpochs = map[string][]*PodEpoch{}
		}
		for address, pod := range peer.Pods {
			if pod == nil {
				delete(peer.Pods, address)
				continue
			}
			if pod.Address == "" {
				pod.Address = address
			}
			pod.PastEpochs = dropNilEpochs(pod.PastEpochs)
			if markOffline {
				pod.Offline = true
			}
		}
		for address, epochs := range peer.PodEpochs {
			if epochs = dropNilEpochs(epochs); len(epochs) == 0 {
				delete(peer.PodEpochs, address)
			} else {
				peer.PodEpochs[address] = epochs
			}
		}
		normalized[name] = peer
	}
	return normalized
}

// dropNilEpochs returns the epochs without the nil ones, or the epochs as they are if there are none.
func dropNilEpochs(epochs []*PodEpoch) []*PodEpoch {
	for _, e := range epochs {
		if e == nil {
			kept := make([]*PodEpoch, 0, len(epochs))
			for _, e := range epochs {
				if e != nil {
					kept = append(kept, e)
				}
			}
			return kept
		}
	}
	return epochs
}

// loadAllPeerPods returns the callable pods of all peers.
func (registry *Registry) loadAllPeerPods() PeerPods {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	peerPods := PeerPods{}
	for name, peer := range registry.peers {
		peerPods[name] = getPodsArray(peer.Pods)
	}
	return peerPods
}

// loadPeerPods returns the callable pods of one peer pod, one peer, or all peers, depending on the peerAddress and peerName given.
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

// loadPodsForPeerWithData returns the pods of the peer if it has targets (or jobs, if asked) saved, or of all peers if none is named.
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

// callPeer calls the URI on the pods of the peer (or all peers), and returns the responses keyed by peer and pod address.
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

// checkPeerHealth calls /health on the peer pods and marks each pod healthy or unhealthy by the outcome.
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

// cleanupUnhealthyPeers calls /health on the peer pods, removes the pods that fail it and resets the past epochs of the healthy ones.
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

// clearPeersResults asks the given peer pods to clear their client results.
func clearPeersResults(peersToClear PeerPods, r *http.Request) PeerResults {
	result := invokeForPods(peersToClear, "POST", "/client/results/clear", http.StatusOK, 2, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Results cleared on peer %s address %s\n", peer, pod.Address)

			} else {
				log.Printf("failed to clear results on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
			}
		})
	return result
}

// clearLocker clears the locker of a peer instance, a peer, or all peers, and asks the affected pods to clear their client results.
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
	return clearPeersResults(peersToClear, r)
}

// addPeerTarget saves the target for a peer (or all peers) and pushes it to the peer pods.
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
	return invokeForPodsWithPayload(peerPods, "POST", "/client/targets/add", util.ToJSONText(target), http.StatusOK, 1, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				if global.Flags.EnableRegistryLogs {
					log.Printf("Pushed target %s to peer %s address %s\n", target.Name, peer, pod.Address)
				}
			} else {
				log.Printf("failed to push target %s to peer %s address %s with error: %s\n", target.Name, peer, pod.Address, err.Error())
			}
		})
}

// removePeerTargets removes the targets from the peer pods and then from the registry if all pods succeeded; no targets means clear all.
func (registry *Registry) removePeerTargets(peerName string, targets []string) PeerResults {
	if len(targets) == 0 {
		// The client has no remove-all route (/client/targets//remove is invalid), so removing all means clearing.
		return registry.clearPeerTargets(peerName)
	}
	targetList := strings.Join(targets, ",")
	removed := true
	return invokeForPods(registry.loadPodsForPeerWithData(peerName),
		"POST", fmt.Sprintf("/client/targets/%s/remove", targetList), http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				if global.Flags.EnableRegistryLogs {
					log.Printf("Removed targets %s from peer %s address %s\n", targetList, peer, pod.Address)
				}
			} else {
				removed = false
				log.Printf("failed to remove targets %s from peer %s address %s with error %s\n", targetList, peer, pod.Address, err.Error())
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

// clearPeerTargets clears the targets on the peer pods and drops them from the registry if all pods succeeded.
func (registry *Registry) clearPeerTargets(peerName string) PeerResults {
	cleared := true
	return invokeForPods(registry.loadPodsForPeerWithData(peerName), "POST", "/client/targets/clear", http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Cleared targets from peer %s address %s\n", peer, pod.Address)
			} else {
				cleared = false
				log.Printf("failed to clear targets from peer %s address %s, error: %s\n", peer, pod.Address, err.Error())
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

// stopPeerTargets asks the peer pods to stop the given targets, or all targets if none are given.
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
				log.Printf("failed to stop targets %s from peer %s address %s with error %s\n", targets, peer, pod.Address, err.Error())
			}
		})
}

// enableAllOrInvocationsTargetsResultsCollection toggles results collection for all targets or invocations, locally and on peer pods.
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
				log.Printf("failed to change targets Results Collection on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
			}
		})
}

// encodeUnderPeersLock encodes the value that get returns as indented JSON while holding the peers read lock, and
// returns the JSON text and whether the value was non-nil. The registry's peer maps (peers, targets, jobs, ...) are
// updated under the peers lock, so encoding a live map after the lock is released can run into a concurrent map
// iteration and write, which is a fatal error that cannot be recovered. Callers write the text to the response after
// the lock is released, so that a slow client cannot hold up writers.
func (registry *Registry) encodeUnderPeersLock(get func() any) (string, bool) {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	return encodeIndentedJSON(get())
}

// encodeIndentedJSON encodes the value as indented JSON, returning the text and whether the value was non-nil.
func encodeIndentedJSON(v any) (string, bool) {
	if util.IsNil(v) {
		return "", false
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("Failed to encode registry data: %s\n", err.Error())
		return "", true
	}
	return string(b), true
}

// addPeerJob saves the job for a peer (or all peers) and pushes it to the peer pods.
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
	return invokeForPodsWithPayload(peerPods, "POST", "/jobs/add", util.ToJSONText(job), http.StatusOK, 1, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Pushed job %s to peer %s address %s\n", job.Name, peer, pod.Address)
			} else {
				log.Printf("failed to push job %s to peer %s address %s with error %s\n", job.Name, peer, pod.Address, err.Error())
			}
		})
}

// addPeerJobScriptOrFile stores a job script or file for one or all peers and pushes it to their pods.
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
			registry.peerFiles[peerName][fileName] = content
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
				log.Printf("failed to push job file [%s] with path [%s] to peer [%s] address [%s] with error %s\n", fileName, filePath, peer, pod.Address, err.Error())
			}
		})
}

// removePeerJobs removes the jobs from the peer pods and then from the registry if all pods succeeded; no jobs means clear all.
func (registry *Registry) removePeerJobs(peerName string, jobs []string) PeerResults {
	if len(jobs) == 0 {
		return registry.clearPeerJobs(peerName)
	}
	jobList := strings.Join(jobs, ",")
	removed := true
	return invokeForPods(registry.loadPodsForPeerWithData(peerName, true),
		"POST", fmt.Sprintf("/jobs/%s/remove", jobList), http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Removed jobs %s from peer %s address %s\n", jobList, peer, pod.Address)
			} else {
				removed = false
				log.Printf("failed to remove jobs %s from peer %s address %s with error %s\n", jobList, peer, pod.Address, err.Error())
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

// stopPeerJobs asks the peer pods to stop the given jobs, or all jobs if none are given.
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
				log.Printf("failed to stop jobs %s from peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
			}
		})
}

// invokePeerTargets asks the peer pods to invoke the given targets, or all targets if none are given.
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
				log.Printf("failed to invoke targets %s on peer %s address %s with error %s\n", targets, peer, pod.Address, err.Error())
			}
		})
}

// invokePeerJobs asks the peer pods to run the given jobs, or all jobs if none are given.
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
				log.Printf("failed to invoke jobs %s on peer %s address %s with error %s\n", jobs, peer, pod.Address, err.Error())
			}
		})
}

// clearPeerJobs clears the jobs on the peer pods and drops them from the registry if all pods succeeded.
func (registry *Registry) clearPeerJobs(peerName string) PeerResults {
	cleared := true
	return invokeForPods(registry.loadPodsForPeerWithData(peerName, true), "POST", "/jobs/clear", http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Cleared jobs from peer %s address %s\n", peer, pod.Address)
			} else {
				cleared = false
				log.Printf("failed to clear jobs from peer %s address %s, error: %s\n", peer, pod.Address, err.Error())
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

// addPeersTrackingHeaders saves the tracking headers in the registry and pushes them to all peer pods.
func (registry *Registry) addPeersTrackingHeaders(headers string) PeerResults {
	registry.peerTrackingHeaders = headers
	registry.trackingHeaders, registry.crossTrackingHeaders = util.ParseTrackingHeaders(headers)
	return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/headers/"+headers, http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Pushed tracking headers %s to peer %s address %s\n", headers, peer, pod.Address)
			} else {
				log.Printf("failed to add tracking headers %s to peer %s address %s with error %s\n", headers, peer, pod.Address, err.Error())
			}
		})
}

// clearPeersTrackingHeaders clears the tracking headers in the registry and on all peer pods.
func (registry *Registry) clearPeersTrackingHeaders() PeerResults {
	registry.peerTrackingHeaders = ""
	registry.trackingHeaders = []string{}
	registry.crossTrackingHeaders = make(http.Header)
	return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/headers/clear", http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Cleared tracking headers on peer %s address %s\n", peer, pod.Address)
			} else {
				log.Printf("failed to clear tracking headers on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
			}
		})
}

// addPeersTrackingTimeBuckets saves the tracking time buckets and pushes them to all peer pods, returning nil if the buckets are invalid.
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
				log.Printf("failed to add tracking time buckets %s to peer %s address %s with error %s\n", b, peer, pod.Address, err.Error())
			}
		})
}

// clearPeersTrackingTimeBuckets clears the tracking time buckets in the registry and on all peer pods.
func (registry *Registry) clearPeersTrackingTimeBuckets() PeerResults {
	registry.peerTrackingTimeBuckets = ""
	registry.trackingTimeBuckets = [][]int{}
	return invokeForPods(registry.loadAllPeerPods(), "POST", "/client/track/time/clear", http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Cleared tracking time buckets on peer %s address %s\n", peer, pod.Address)
			} else {
				log.Printf("failed to clear tracking time buckets on peer %s address %s with error %s\n", peer, pod.Address, err.Error())
			}
		})
}

// setProbe records the readiness or liveness probe URI and/or status in the registry, defaulting the status to 200.
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

// sendProbe pushes the probe URI of the given type to all peer pods.
func (registry *Registry) sendProbe(probeType, uri string) PeerResults {
	return invokeForPods(registry.loadAllPeerPods(), "POST", fmt.Sprintf("/probes/%s/set?uri=%s", probeType, uri), http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Pushed %s URI %s to peer %s address %s\n", probeType, uri, peer, pod.Address)
			} else {
				log.Printf("failed to push %s URI %s to peer %s address %s with error %s\n", probeType, uri, peer, pod.Address, err.Error())
			}
		})
}

// sendProbeStatus pushes the probe status of the given type to all peer pods.
func (registry *Registry) sendProbeStatus(probeType string, status int) PeerResults {
	return invokeForPods(registry.loadAllPeerPods(), "POST", fmt.Sprintf("/probes/%s/set/status=%d", probeType, status), http.StatusOK, 3, false,
		func(peer string, pod *Pod, response interface{}, err error) {
			if err == nil {
				log.Printf("Pushed %s Status %d to peer %s address %s\n", probeType, status, peer, pod.Address)
			} else {
				log.Printf("failed to push %s Status %d to peer %s address %s with error %s\n", probeType, status, peer, pod.Address, err.Error())
			}
		})
}

// preparePeerStartupData fills in the targets, jobs, files, tracking settings and probes that a newly registered peer gets at startup.
func (registry *Registry) preparePeerStartupData(peer *Peer, peerData *PeerData) {
	peerData.Targets = registry.peerTargets[peer.Name]
	peerData.Jobs = registry.peerJobs[peer.Name]
	peerData.JobScripts = registry.peerJobScripts[peer.Name]
	peerData.Files = registry.peerFiles[peer.Name]
	peerData.TrackingHeaders = registry.peerTrackingHeaders
	peerData.TrackingTimeBuckets = registry.peerTrackingTimeBuckets
	peerData.Probes = registry.peerProbes
}

// clonePeersFrom replaces the peers with those fetched from another registry, marking their pods offline.
func (registry *Registry) clonePeersFrom(registryURL string) error {
	if resp, err := http.Get(registryURL + "/registry/peers"); err == nil {
		peers := map[string]*Peers{}
		if err := util.ReadJsonPayloadFromBody(resp.Body, &peers); err == nil {
			peers = normalizePeers(peers, true)
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

// cloneLockersFrom replaces the lockers with those fetched from another registry.
func (registry *Registry) cloneLockersFrom(registryURL string) error {
	if resp, err := http.Get(registryURL + "/registry/lockers?data=y&peers=y&level=0"); err == nil {
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

// clonePeersTargetsFrom replaces the peer targets with those fetched from another registry.
func (registry *Registry) clonePeersTargetsFrom(registryURL string) error {
	if resp, err := http.Get(registryURL + "/registry/peers/targets"); err == nil {
		peerTargets := map[string]PeerTargets{}
		if err := util.ReadJsonPayloadFromBody(resp.Body, &peerTargets); err == nil {
			peerTargets = normalizePeerTargets(peerTargets)
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

// normalizePeerTargets makes sure that the targets decoded from JSON (a loaded registry dump, or the targets of
// another registry) have a non-nil map for every peer, and no null targets, since targets are added to the
// per-peer maps and read from them without checks.
func normalizePeerTargets(peerTargets map[string]PeerTargets) map[string]PeerTargets {
	normalized := map[string]PeerTargets{}
	for peer, targets := range peerTargets {
		normalized[peer] = PeerTargets{}
		for name, target := range targets {
			if target != nil {
				normalized[peer][name] = target
			}
		}
	}
	return normalized
}

// normalizePeerJobs makes sure that the jobs decoded from JSON (a loaded registry dump, or the jobs of another
// registry) have a non-nil map for every peer, and no null jobs, and fills in the job names.
func normalizePeerJobs(peerJobs map[string]PeerJobs) map[string]PeerJobs {
	normalized := map[string]PeerJobs{}
	for peer, jobs := range peerJobs {
		normalized[peer] = PeerJobs{}
		for name, pj := range jobs {
			if pj != nil {
				normalized[peer][name] = pj
			}
		}
	}
	fillJobNames(normalized)
	return normalized
}

// fillJobNames sets the name of jobs from their key, for jobs that came from a v0.8.x registry
// (which identified jobs by "id", so they have no name in the payload).
func fillJobNames(peerJobs map[string]PeerJobs) {
	for _, jobs := range peerJobs {
		for name, pj := range jobs {
			if pj != nil && pj.Job != nil && pj.Name == "" {
				pj.Name = name
			}
		}
	}
}

// clonePeersJobsFrom replaces the peer jobs with those fetched from another registry.
func (registry *Registry) clonePeersJobsFrom(registryURL string) error {
	if resp, err := http.Get(registryURL + "/registry/peers/jobs"); err == nil {
		peerJobs := map[string]PeerJobs{}
		if err := util.ReadJsonPayloadFromBody(resp.Body, &peerJobs); err == nil {
			peerJobs = normalizePeerJobs(peerJobs)
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

// clonePeersTrackingHeadersFrom replaces the peer tracking headers with those fetched from another registry.
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

// clonePeersProbesFrom replaces the peer probes with those fetched from another registry.
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

// GetPeers returns a map of pod name to address for the pods of the named peer, empty if the peer is not known.
func GetPeers(name string, r *http.Request) map[string]string {
	registry.peersLock.RLock()
	defer registry.peersLock.RUnlock()
	data := map[string]string{}
	peers := registry.peers[name]
	if peers == nil {
		return data
	}
	for _, pod := range peers.Pods {
		data[pod.Name] = pod.Address
	}
	return data
}
