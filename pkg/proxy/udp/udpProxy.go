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
	"encoding/binary"
	"errors"
	"fmt"
	"goto/pkg/server/listeners"
	"goto/pkg/types"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxPacketSize = 65535

var (
	udpProxyByPort = map[int]*UDPProxy{}
	proxyLock      sync.RWMutex

	// upstreamTimeout is how long the proxy waits for an upstream to answer a packet.
	upstreamTimeout = 2 * time.Second
	// maxInFlight is how many packets the proxy of a port deals with at a time. The packets that arrive while that many
	// are being dealt with are dropped.
	maxInFlight = 1024

	bufferPool = sync.Pool{New: func() any {
		b := make([]byte, maxPacketSize)
		return &b
	}}
)

type UDPUpstream struct {
	Name         string       `json:"name"`
	Protocol     string       `json:"protocol"`
	Endpoint     string       `json:"endpoint"`
	Delay        *types.Delay `json:"delay"`
	upstreamAddr *net.UDPAddr
	isRunning    bool
	lock         sync.RWMutex
}

type UDPProxy struct {
	Port       int                     `json:"port"`
	Enabled    bool                    `json:"enabled"`
	Upstreams  map[string]*UDPUpstream `json:"upstreams"`
	UDPTracker *UDPProxyTracker        `json:"udpTracker"`
	// the listener that the proxy reads packets from, and the number of the upstream that gets the next packet
	conn     net.PacketConn
	next     int
	inFlight chan struct{}
	lock     sync.RWMutex
}

func newUDPProxy(port int) *UDPProxy {
	return &UDPProxy{
		Port:       port,
		Enabled:    true,
		Upstreams:  map[string]*UDPUpstream{},
		UDPTracker: NewUDPTracker(),
		inFlight:   make(chan struct{}, maxInFlight),
	}
}

func newUDPUpstream(name, address string) *UDPUpstream {
	return &UDPUpstream{
		Name:     name,
		Protocol: "udp",
		Endpoint: address,
	}
}

func getUDPProxyForPort(port int) *UDPProxy {
	proxyLock.RLock()
	proxy := udpProxyByPort[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxyLock.Lock()
		if proxy = udpProxyByPort[port]; proxy == nil {
			proxy = newUDPProxy(port)
			udpProxyByPort[port] = proxy
		}
		proxyLock.Unlock()
	}
	return proxy
}

func findUDPProxy(port int) *UDPProxy {
	proxyLock.RLock()
	defer proxyLock.RUnlock()
	return udpProxyByPort[port]
}

// ProxyUDPUpstream starts proxying the packets that arrive at the UDP listener of the port to the upstream, which is
// the address of a UDP server. The packets and the answers to them are delayed by a time between the given ones.
// Packets go to the upstreams that the port has in turn.
func ProxyUDPUpstream(port int, upstream string, delayMin, delayMax time.Duration) {
	if err := proxyUDPUpstream(port, upstream, delayMin, delayMax); err != nil {
		log.Printf("UDP Proxy[%d]: Failed to proxy to upstream [%s]: %s\n", port, upstream, err.Error())
	}
}

func proxyUDPUpstream(port int, upstream string, delayMin, delayMax time.Duration) error {
	l := listeners.GetListenerForPort(port)
	if l == nil || l.UDPConn == nil {
		return fmt.Errorf("no open UDP listener on port [%d]", port)
	}
	name := fmt.Sprintf("%d-%s", port, upstream)
	return getUDPProxyForPort(port).startUpstream(name, upstream, l.UDPConn, delayMin, delayMax)
}

// SetUDPDelay sets the delay of the upstream, or of all the upstreams of the port if no upstream is given.
func SetUDPDelay(port int, upstream string, delayMin, delayMax time.Duration) {
	setUDPDelayOfPort(port, upstream, delayMin, delayMax)
}

// setUDPDelayOfPort tells how many upstreams it set the delay of.
func setUDPDelayOfPort(port int, upstream string, delayMin, delayMax time.Duration) int {
	if p := findUDPProxy(port); p != nil {
		return p.setUDPDelay(upstream, delayMin, delayMax)
	}
	return 0
}

// StopUDPUpstream stops proxying to the upstream. The upstream is kept, and starts again if it's asked for again.
func StopUDPUpstream(port int, upstream string) {
	if p := findUDPProxy(port); p != nil {
		p.stopUpstream(upstream)
	}
}

func (p *UDPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.UDPTracker = NewUDPTracker()
}

func (p *UDPProxy) tracker() *UDPProxyTracker {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.UDPTracker
}

func (p *UDPProxy) setUDPDelay(upstream string, delayMin, delayMax time.Duration) int {
	p.lock.RLock()
	defer p.lock.RUnlock()
	n := 0
	for endpoint, up := range p.Upstreams {
		if upstream == "" || upstream == endpoint {
			up.setUDPDelay(delayMin, delayMax)
			n++
		}
	}
	return n
}

// startUpstream sets the upstream up, and has the proxy read packets from the connection, unless it already does.
func (p *UDPProxy) startUpstream(name, upstream string, conn net.PacketConn, delayMin, delayMax time.Duration) error {
	addr, err := resolveUpstream(upstream)
	if err != nil {
		return err
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	up := p.Upstreams[upstream]
	if up == nil {
		up = newUDPUpstream(name, upstream)
		p.Upstreams[upstream] = up
	}
	up.start(addr, delayMin, delayMax)
	if conn != nil && p.conn != conn {
		p.conn = conn
		go p.serve(conn)
	}
	return nil
}

// resolveUpstream makes an address of the upstream, which must be a host and a port.
func resolveUpstream(upstream string) (*net.UDPAddr, error) {
	addr, err := net.ResolveUDPAddr("udp", upstream)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream address [%s]: %w", upstream, err)
	}
	if addr.Port <= 0 {
		return nil, fmt.Errorf("invalid upstream address [%s]: no port", upstream)
	}
	return addr, nil
}

func (p *UDPProxy) stopUpstream(upstream string) {
	p.lock.RLock()
	up := p.Upstreams[upstream]
	p.lock.RUnlock()
	if up != nil {
		up.Stop()
	}
}

func (p *UDPProxy) removeUpstream(upstream string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if up := p.Upstreams[upstream]; up != nil {
		up.Stop()
		delete(p.Upstreams, upstream)
	}
}

// nextUpstream picks the upstream for the next packet, going through the running ones in the order of their addresses.
func (p *UDPProxy) nextUpstream() *UDPUpstream {
	p.lock.Lock()
	defer p.lock.Unlock()
	endpoints := make([]string, 0, len(p.Upstreams))
	for endpoint, up := range p.Upstreams {
		if up.running() {
			endpoints = append(endpoints, endpoint)
		}
	}
	if len(endpoints) == 0 {
		return nil
	}
	sort.Strings(endpoints)
	up := p.Upstreams[endpoints[p.next%len(endpoints)]]
	p.next++
	return up
}

func (p *UDPProxy) isServing(conn net.PacketConn) bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.conn == conn
}

func (p *UDPProxy) stoppedServing(conn net.PacketConn) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.conn == conn {
		p.conn = nil
	}
}

// serve reads the packets that arrive at the connection, and has them proxied, until the connection is closed.
func (p *UDPProxy) serve(conn net.PacketConn) {
	defer p.stoppedServing(conn)
	buff := make([]byte, maxPacketSize)
	for {
		n, client, err := conn.ReadFrom(buff)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || !p.isServing(conn) {
				return
			}
			log.Printf("UDP Proxy[%d]: ReadFrom error: %s\n", p.Port, err.Error())
			time.Sleep(10 * time.Millisecond)
			continue
		}
		up := p.nextUpstream()
		if up == nil {
			log.Printf("UDP Proxy[%d]: No upstream is running, dropped a packet from [%s]\n", p.Port, client)
			continue
		}
		select {
		case p.inFlight <- struct{}{}:
		default:
			log.Printf("UDP Proxy[%d]: Too many packets in flight, dropped a packet from [%s]\n", p.Port, client)
			continue
		}
		// The packet is dealt with while the next one is read, so it can't stay in the read buffer
		packet := append([]byte(nil), buff[:n]...)
		tracker := p.tracker()
		go func() {
			defer func() { <-p.inFlight }()
			tracker.TrackClient(client.String())
			up.handlePacket(conn, client, packet, tracker)
		}()
	}
}

// handlePacket sends the packet to the upstream, and the answer of the upstream back to the client. Every packet goes
// out of a socket of its own, so that what an upstream answers to a packet is never taken for the answer to another.
func (up *UDPUpstream) handlePacket(listenerConn net.PacketConn, clientAddr net.Addr, packet []byte, tracker *UDPProxyTracker) {
	domain := extractDomain(packet)
	address := up.Endpoint
	tracker.IncrementPacketCounts(address, domain)
	delay := up.applyDelay()
	conn, err := net.DialUDP("udp", nil, up.addr())
	if err != nil {
		log.Printf("Failed to connect to upstream [%s] for domain [%s]: error [%s]\n", address, domain, err)
		return
	}
	defer conn.Close()
	if _, err = conn.Write(packet); err != nil {
		log.Printf("Failed to send packet to upstream [%s] for domain [%s]: error [%s]\n", address, domain, err)
		return
	}
	bp := bufferPool.Get().(*[]byte)
	defer bufferPool.Put(bp)
	conn.SetReadDeadline(time.Now().Add(upstreamTimeout))
	n, err := conn.Read(*bp)
	if err != nil {
		log.Printf("Failed to read packet from upstream [%s] for domain [%s]: error [%s]\n", address, domain, err)
		return
	}
	delay += up.applyDelay()
	if _, err = listenerConn.WriteTo((*bp)[:n], clientAddr); err != nil {
		log.Printf("Failed to send packet to downstream [%s] for domain [%s]: error [%s]\n", clientAddr, domain, err)
		return
	}
	log.Printf("Proxied UDP query from downstream [%s] to upstream [%s] for domain [%s] with delay [%s]\n", clientAddr, address, domain, delay)
}

// applyDelay waits for the delay of the upstream, if it has one, and tells how long it waited.
func (up *UDPUpstream) applyDelay() time.Duration {
	d := up.nextDelay()
	if d > 0 {
		time.Sleep(d)
	}
	return d
}

// nextDelay computes the next delay that the upstream asks for, and uses up a count of it.
func (up *UDPUpstream) nextDelay() time.Duration {
	up.lock.Lock()
	d := up.Delay
	up.lock.Unlock()
	return d.Claim()
}

func (up *UDPUpstream) addr() *net.UDPAddr {
	up.lock.RLock()
	defer up.lock.RUnlock()
	return up.upstreamAddr
}

func (up *UDPUpstream) running() bool {
	up.lock.RLock()
	defer up.lock.RUnlock()
	return up.isRunning
}

func (up *UDPUpstream) setUDPDelay(delayMin, delayMax time.Duration) {
	up.lock.Lock()
	defer up.lock.Unlock()
	up.Delay = types.NewDelay(delayMin, delayMax, -1)
}

func (up *UDPUpstream) start(addr *net.UDPAddr, delayMin, delayMax time.Duration) {
	up.lock.Lock()
	defer up.lock.Unlock()
	up.upstreamAddr = addr
	up.isRunning = true
	up.Delay = types.NewDelay(delayMin, delayMax, -1)
}

// Stop makes the upstream stop getting packets. It can be started again.
func (up *UDPUpstream) Stop() {
	up.lock.Lock()
	defer up.lock.Unlock()
	up.isRunning = false
}

// extractDomain gets the name that a DNS query asks about. It returns an empty string for packets that aren't a DNS
// query.
func extractDomain(packet []byte) string {
	if len(packet) < 12 {
		return ""
	}
	qdCount := binary.BigEndian.Uint16(packet[4:6])
	if qdCount == 0 {
		return ""
	}
	i := 12
	var labels []string
	for {
		if i >= len(packet) {
			return ""
		}
		l := int(packet[i])
		if l == 0 {
			break
		}
		if l&0xC0 != 0 {
			// not a label, but a pointer to one, which a question doesn't have
			return ""
		}
		i++
		if i+l > len(packet) {
			return ""
		}
		labels = append(labels, string(packet[i:i+l]))
		i += l
	}
	return strings.Join(labels, ".")
}
