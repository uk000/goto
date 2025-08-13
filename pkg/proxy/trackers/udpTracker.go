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
