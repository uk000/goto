/**
 * Copyright 2025 uk
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

package trackers

import "sync"

type UDPProxyTracker struct {
	ConnCount                   int                       `json:"connCount"`
	PacketCount                 int                       `json:"packetCount"`
	PacketCountByUpstream       map[string]int            `json:"packetCountByUpstream"`
	PacketCountByDomain         map[string]int            `json:"packetCountByDomain"`
	PacketCountByUpstreamDomain map[string]map[string]int `json:"packetCountByUpstreamDomain"`
	lock                        sync.RWMutex
}

func NewUDPTracker() *UDPProxyTracker {
	return &UDPProxyTracker{
		PacketCountByUpstream:       map[string]int{},
		PacketCountByDomain:         map[string]int{},
		PacketCountByUpstreamDomain: map[string]map[string]int{},
	}
}

func (ut *UDPProxyTracker) IncrementConnCount() {
	ut.lock.Lock()
	defer ut.lock.Unlock()
	ut.ConnCount++
}

func (ut *UDPProxyTracker) IncrementPacketCounts(upstream, domain string) {
	ut.lock.Lock()
	defer ut.lock.Unlock()
	ut.PacketCount++
	ut.PacketCountByUpstream[upstream]++
	ut.PacketCountByDomain[domain]++
	if ut.PacketCountByUpstreamDomain[upstream] == nil {
		ut.PacketCountByUpstreamDomain[upstream] = map[string]int{}
	}
	ut.PacketCountByUpstreamDomain[upstream][domain]++
}
