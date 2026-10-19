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
	"fmt"
	"sync"
	"testing"
)

func TestNewUDPTrackerIsEmpty(t *testing.T) {
	tr := NewUDPTracker()
	if tr.ConnCount != 0 || tr.PacketCount != 0 || tr.PacketCountByUpstream == nil || tr.PacketCountByDomain == nil ||
		tr.PacketCountByUpstreamDomain == nil || len(tr.PacketCountByUpstream)+len(tr.PacketCountByDomain)+len(tr.PacketCountByUpstreamDomain) != 0 {
		t.Errorf("tracker = %+v", tr)
	}
}

func TestIncrementConnCount(t *testing.T) {
	tr := NewUDPTracker()
	tr.IncrementConnCount()
	tr.IncrementConnCount()
	if tr.ConnCount != 2 {
		t.Errorf("conn count = %d, want 2", tr.ConnCount)
	}
}

func TestIncrementPacketCounts(t *testing.T) {
	tr := NewUDPTracker()
	tr.IncrementPacketCounts("u1:53", "a.com")
	tr.IncrementPacketCounts("u1:53", "a.com")
	tr.IncrementPacketCounts("u1:53", "b.com")
	tr.IncrementPacketCounts("u2:53", "a.com")
	// a packet that isn't a DNS query is a packet for its upstream, but of no domain
	tr.IncrementPacketCounts("u2:53", "")
	tr.IncrementPacketCounts("u3:53", "")
	if tr.PacketCount != 6 {
		t.Errorf("packet count = %d, want 6", tr.PacketCount)
	}
	if want := map[string]int{"u1:53": 3, "u2:53": 2, "u3:53": 1}; !equalCounts(tr.PacketCountByUpstream, want) {
		t.Errorf("by upstream = %v, want %v", tr.PacketCountByUpstream, want)
	}
	if want := map[string]int{"a.com": 3, "b.com": 1}; !equalCounts(tr.PacketCountByDomain, want) {
		t.Errorf("by domain = %v, want %v", tr.PacketCountByDomain, want)
	}
	if want := map[string]int{"a.com": 2, "b.com": 1}; !equalCounts(tr.PacketCountByUpstreamDomain["u1:53"], want) {
		t.Errorf("by upstream and domain, u1 = %v, want %v", tr.PacketCountByUpstreamDomain["u1:53"], want)
	}
	if want := map[string]int{"a.com": 1}; !equalCounts(tr.PacketCountByUpstreamDomain["u2:53"], want) {
		t.Errorf("by upstream and domain, u2 = %v, want %v", tr.PacketCountByUpstreamDomain["u2:53"], want)
	}
	if _, found := tr.PacketCountByUpstreamDomain["u3:53"]; found {
		t.Errorf("an upstream that got no query of a domain has no counts of domains: %v", tr.PacketCountByUpstreamDomain)
	}
}

func TestTrackClient(t *testing.T) {
	tr := NewUDPTracker()
	if !tr.TrackClient("1.1.1.1:1") || !tr.TrackClient("1.1.1.1:2") {
		t.Fatal("new clients must be counted")
	}
	if tr.TrackClient("1.1.1.1:1") || tr.TrackClient("1.1.1.1:2") {
		t.Error("a client that is known must not be counted again")
	}
	if tr.ConnCount != 2 {
		t.Errorf("conn count = %d, want 2", tr.ConnCount)
	}
	// it goes with the connections that are counted by hand
	tr.IncrementConnCount()
	if tr.ConnCount != 3 {
		t.Errorf("conn count = %d, want 3", tr.ConnCount)
	}
}

func TestTrackClientWithoutClientMap(t *testing.T) {
	tr := &UDPProxyTracker{PacketCountByUpstream: map[string]int{}}
	if !tr.TrackClient("a") || tr.TrackClient("a") || tr.ConnCount != 1 {
		t.Errorf("a tracker that was made without a map of clients must still count the clients: %+v", tr)
	}
}

// The memory spent on remembering clients is bounded: when the limit is reached the clients are forgotten, and counted
// again when they come back.
func TestTrackClientMemoryIsBounded(t *testing.T) {
	tr := NewUDPTracker()
	for i := 0; i < maxTrackedClients; i++ {
		if !tr.TrackClient(fmt.Sprintf("c%d", i)) {
			t.Fatalf("client %d was not counted", i)
		}
	}
	if tr.TrackClient("c0") {
		t.Fatal("a client of those that are remembered must not be counted again")
	}
	if !tr.TrackClient("one-too-many") {
		t.Fatal("the client over the limit must be counted")
	}
	if len(tr.clients) > maxTrackedClients {
		t.Errorf("%d clients are remembered, want at most %d", len(tr.clients), maxTrackedClients)
	}
	if tr.ConnCount != maxTrackedClients+1 {
		t.Errorf("conn count = %d, want %d", tr.ConnCount, maxTrackedClients+1)
	}
	if !tr.TrackClient("c0") {
		t.Error("a client that was forgotten counts as new")
	}
}

func TestUDPTrackerJSON(t *testing.T) {
	tr := NewUDPTracker()
	tr.TrackClient("c1")
	tr.IncrementPacketCounts("u:53", "a.com")
	tr.IncrementPacketCounts("u:53", "")
	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	if len(got) != 5 || got["connCount"] != float64(1) || got["packetCount"] != float64(2) {
		t.Errorf("json = %s", data)
	}
	byUpstream, _ := got["packetCountByUpstream"].(map[string]any)
	byDomain, _ := got["packetCountByDomain"].(map[string]any)
	byBoth, _ := got["packetCountByUpstreamDomain"].(map[string]any)
	if byUpstream["u:53"] != float64(2) || byDomain["a.com"] != float64(1) || len(byDomain) != 1 || byBoth["u:53"] == nil {
		t.Errorf("json = %s", data)
	}
	if !json.Valid(data) {
		t.Errorf("invalid json: %s", data)
	}
	// the proxy that holds the tracker is shown with it
	p := newUDPProxy(7)
	p.UDPTracker = tr
	data, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Port       int
		Enabled    bool
		Upstreams  map[string]any
		UDPTracker map[string]any
	}
	if err := json.Unmarshal(data, &shown); err != nil || shown.Port != 7 || !shown.Enabled || shown.UDPTracker["packetCount"] != float64(2) {
		t.Errorf("proxy json = %s, err = %v", data, err)
	}
}

func TestUDPTrackerConcurrently(t *testing.T) {
	tr := NewUDPTracker()
	const workers, rounds = 12, 500
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readers sync.WaitGroup
	marshalErrs := make(chan error, 1)
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := json.Marshal(tr); err != nil {
					marshalErrs <- err
					return
				}
			}
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				upstream := fmt.Sprintf("u%d", w%3)
				domain := ""
				if i%2 == 0 {
					domain = fmt.Sprintf("d%d.com", i%4)
				}
				tr.IncrementPacketCounts(upstream, domain)
				tr.TrackClient(fmt.Sprintf("client-%d", i%50))
				tr.IncrementConnCount()
			}
		}()
	}
	waitGroup(t, &wg, "workers")
	close(stop)
	waitGroup(t, &readers, "reader")
	select {
	case err := <-marshalErrs:
		t.Error(err)
	default:
	}
	total := workers * rounds
	if tr.PacketCount != total {
		t.Errorf("packet count = %d, want %d", tr.PacketCount, total)
	}
	sum := 0
	for _, n := range tr.PacketCountByUpstream {
		sum += n
	}
	if sum != total {
		t.Errorf("packets by upstream add up to %d, want %d", sum, total)
	}
	// half of the packets have a domain
	sum = 0
	for _, n := range tr.PacketCountByDomain {
		sum += n
	}
	if sum != total/2 {
		t.Errorf("packets by domain add up to %d, want %d", sum, total/2)
	}
	sum = 0
	for _, domains := range tr.PacketCountByUpstreamDomain {
		for _, n := range domains {
			sum += n
		}
	}
	if sum != total/2 {
		t.Errorf("packets by upstream and domain add up to %d, want %d", sum, total/2)
	}
	// 50 distinct clients, each counted once, and every worker round counted by hand
	if want := 50 + total; tr.ConnCount != want {
		t.Errorf("conn count = %d, want %d", tr.ConnCount, want)
	}
}
