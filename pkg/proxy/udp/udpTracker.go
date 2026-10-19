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

package udpproxy

import (
	"encoding/json"
	"sync"
)

// maxTrackedClients bounds the memory that is spent on telling clients apart: when that many are known, the proxy
// starts to forget them, and counts the ones it sees again as new.
const maxTrackedClients = 10000

type UDPProxyTracker struct {
	ConnCount                   int                       `json:"connCount"`
	PacketCount                 int                       `json:"packetCount"`
	PacketCountByUpstream       map[string]int            `json:"packetCountByUpstream"`
	PacketCountByDomain         map[string]int            `json:"packetCountByDomain"`
	PacketCountByUpstreamDomain map[string]map[string]int `json:"packetCountByUpstreamDomain"`
	clients                     map[string]struct{}
	lock                        sync.RWMutex
}

func NewUDPTracker() *UDPProxyTracker {
	return &UDPProxyTracker{
		PacketCountByUpstream:       map[string]int{},
		PacketCountByDomain:         map[string]int{},
		PacketCountByUpstreamDomain: map[string]map[string]int{},
		clients:                     map[string]struct{}{},
	}
}

func (ut *UDPProxyTracker) IncrementConnCount() {
	ut.lock.Lock()
	defer ut.lock.Unlock()
	ut.ConnCount++
}

// TrackClient counts the client as a connection, if it's one that the proxy hasn't seen before. UDP has no
// connections, so a client is what the proxy takes for one.
func (ut *UDPProxyTracker) TrackClient(addr string) bool {
	ut.lock.Lock()
	defer ut.lock.Unlock()
	if ut.clients == nil {
		ut.clients = map[string]struct{}{}
	}
	if _, known := ut.clients[addr]; known {
		return false
	}
	if len(ut.clients) >= maxTrackedClients {
		ut.clients = map[string]struct{}{}
	}
	ut.clients[addr] = struct{}{}
	ut.ConnCount++
	return true
}

// IncrementPacketCounts counts a packet for the upstream it goes to. If the packet is a DNS query, the domain that it
// asks about is counted too, otherwise the domain is empty.
func (ut *UDPProxyTracker) IncrementPacketCounts(upstream, domain string) {
	ut.lock.Lock()
	defer ut.lock.Unlock()
	ut.PacketCount++
	ut.PacketCountByUpstream[upstream]++
	if domain == "" {
		return
	}
	ut.PacketCountByDomain[domain]++
	if ut.PacketCountByUpstreamDomain[upstream] == nil {
		ut.PacketCountByUpstreamDomain[upstream] = map[string]int{}
	}
	ut.PacketCountByUpstreamDomain[upstream][domain]++
}

func (ut *UDPProxyTracker) MarshalJSON() ([]byte, error) {
	ut.lock.RLock()
	defer ut.lock.RUnlock()
	type trackerJSON struct {
		ConnCount                   int                       `json:"connCount"`
		PacketCount                 int                       `json:"packetCount"`
		PacketCountByUpstream       map[string]int            `json:"packetCountByUpstream"`
		PacketCountByDomain         map[string]int            `json:"packetCountByDomain"`
		PacketCountByUpstreamDomain map[string]map[string]int `json:"packetCountByUpstreamDomain"`
	}
	return json.Marshal(trackerJSON{
		ConnCount:                   ut.ConnCount,
		PacketCount:                 ut.PacketCount,
		PacketCountByUpstream:       ut.PacketCountByUpstream,
		PacketCountByDomain:         ut.PacketCountByDomain,
		PacketCountByUpstreamDomain: ut.PacketCountByUpstreamDomain,
	})
}
