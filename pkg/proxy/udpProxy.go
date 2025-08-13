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

package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"goto/pkg/proxy/trackers"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

var (
	udpProxyByPort = map[int]*UDPProxy{}
)

type UDPUpstream struct {
	ProxyTarget
	upstreamAddr *net.UDPAddr
	conn         *net.UDPConn
}

type UDPProxy struct {
	Proxy
	UDPTracker *trackers.UDPProxyTracker `json:"udpTracker"`
	stopChan   chan bool
	lock       sync.RWMutex
}

func newUDPProxy(port int) *UDPProxy {
	return &UDPProxy{
		Proxy:      *newProxy(port),
		UDPTracker: trackers.NewUDPTracker(),
		stopChan:   make(chan bool),
		lock:       sync.RWMutex{},
	}
}

func newUDPUpstream(name, address string, delayMin, delayMax time.Duration) *UDPUpstream {
	return &UDPUpstream{
		ProxyTarget: *newProxyTarget(name, "udp", address),
	}
}

func getUDPProxyForPort(port int) *UDPProxy {
	proxyLock.RLock()
	proxy := udpProxyByPort[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxyLock.Lock()
		defer proxyLock.Unlock()
		proxy = newUDPProxy(port)
		udpProxyByPort[port] = proxy
	}
	return proxy
}

func ProxyUDPUpstream(port int, upstream string, delayMin, delayMax time.Duration) {
	name := fmt.Sprintf("%d-%s", port, upstream)
	getUDPProxyForPort(port).startUpstream(name, upstream, delayMin, delayMax)
}

func SetUDPDelay(port int, upstream string, delayMin, delayMax time.Duration) {
	getUDPProxyForPort(port).setUDPDelay(upstream, delayMin, delayMax)
}

func StopUDPUpstream(port int, upstream string) {
	getUDPProxyForPort(port).stopUpstream(upstream)
}

func (p *UDPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.UDPTracker = trackers.NewUDPTracker()
}

func (p *UDPProxy) setUDPDelay(upstream string, delayMin, delayMax time.Duration) {
	p.lock.Lock()
	defer p.lock.Unlock()
	up := p.Targets[upstream]
	if up != nil {
		up.SetDelay(delayMin, delayMax, -1)
	}
}

func (p *UDPProxy) startUpstream(name, upstream string, delayMin, delayMax time.Duration) {
	p.lock.Lock()
	defer p.lock.Unlock()
	t := p.Targets[upstream]
	if t == nil {
		up := newUDPUpstream(name, upstream, delayMin, delayMax)
		up.connect()
		p.Targets[upstream] = up
		t = up
	} else if t.IsRunning() {
		t.Stop()
	}
	t.SetDelay(delayMin, delayMax, -1)
	go p.runProxy(t.(*UDPUpstream))
}

func (p *UDPProxy) stopUpstream(upstream string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.Targets[upstream] != nil && p.Targets[upstream].IsRunning() {
		p.Targets[upstream].Stop()
	}
}

func (p *UDPProxy) removeUpstream(upstream string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.Targets[upstream] != nil {
		p.Targets[upstream].Close()
		delete(p.Targets, upstream)
	}
}

func (p *UDPProxy) runProxy(up *UDPUpstream) error {
	l := listeners.GetListenerForPort(p.Port)
	for {
		select {
		case <-p.stopChan:
			return nil
		case <-up.stopChan:
			return nil
		default:
		}

		packet, clientAddr, err := up.readFromDownstream(l.UDPConn)
		if err != nil {
			log.Println("ReadFrom error:", err)
			continue
		}
		if up.conn == nil {
			up.connect()
		}
		if up.conn != nil {
			go up.handlePacket(l.UDPConn, clientAddr, packet, p.UDPTracker)
		} else {
			log.Printf("Upstream [%s] not connected\n", up.Endpoint)
		}
	}
}

func (up *UDPUpstream) readFromDownstream(conn *net.UDPConn) (packet []byte, clientAddr net.Addr, err error) {
	if conn == nil || conn.RemoteAddr() == nil {
		err = errors.New("connection is nil")
		return
	}
	packet = make([]byte, 4096)
	_, clientAddr, err = conn.ReadFrom(packet)
	if err != nil {
		log.Println("Error reading packet from downstream [%s]:error:", conn.RemoteAddr().String(), err)
	}
	return
}

func (up *UDPUpstream) handlePacket(listenerConn net.PacketConn, clientAddr net.Addr, packet []byte, tracker *trackers.UDPProxyTracker) {
	delay := util.RandomDuration(up.DelayMin, up.DelayMax)
	time.Sleep(delay)
	domain := extractDomain(packet)
	up.lock.RLock()
	address := up.Endpoint
	up.lock.RUnlock()
	if domain != "" {
		tracker.IncrementPacketCounts(address, domain)
	}
	_, err := up.conn.Write(packet)
	if err != nil {
		log.Printf("Failed to send packet to upstream [%s] for domain [%s]: error [%s]\n", address, domain, err)
		return
	}
	resp := make([]byte, 4096)
	up.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := up.conn.ReadFrom(resp)
	if err != nil {
		log.Printf("Failed to read packet from upstream [%s] for domain [%s]: error [%s]\n", address, domain, err)
		return
	}
	time.Sleep(delay)
	_, err = listenerConn.WriteTo(resp[:n], clientAddr)
	if err != nil {
		log.Printf("Failed to send packet to downstream [%s] for domain [%s]: error [%s]\n", clientAddr, domain, err)
		return
	}
	log.Printf("Proxied UDP query from downstream [%s] to upstream [%s] for domain [%s] with delay [%s]\n", clientAddr, address, domain, delay)
}

func (up *UDPUpstream) setUDPDelay(delayMin, delayMax time.Duration) {
	up.lock.Lock()
	defer up.lock.Unlock()
	up.DelayMin = delayMin
	up.DelayMax = delayMax
}

func (up *UDPUpstream) connect() (err error) {
	if up.conn != nil {
		up.Stop()
	}
	up.lock.Lock()
	defer up.lock.Unlock()
	up.upstreamAddr, err = net.ResolveUDPAddr("udp", up.Endpoint)
	if err != nil {
		return err
	}
	up.conn, err = net.DialUDP("udp", nil, up.upstreamAddr)
	return
}

func (up *UDPUpstream) Stop() {
	up.lock.Lock()
	defer up.lock.Unlock()
	if up.stopChan != nil {
		up.stopChan <- true
		up.stopChan = nil
	}
	if up.conn != nil {
		up.conn.Close()
		up.conn = nil
	}
}

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
		i++
		if i+l > len(packet) {
			return ""
		}
		labels = append(labels, string(packet[i:i+l]))
		i += l
	}
	return strings.Join(labels, ".")
}

// func (p *UDPDNSProxy) StartBidirectional() error {
// 	listener, err := net.ListenPacket("udp", p.ListenAddr)
// 	if err != nil {
// 		return err
// 	}
// 	defer listener.Close()

// 	upstreamAddr, err := net.ResolveUDPAddr("udp", p.UpstreamAddr)
// 	if err != nil {
// 		return err
// 	}

// 	upstreamListener, err := net.ListenPacket("udp", "")
// 	if err != nil {
// 		return err
// 	}
// 	defer upstreamListener.Close()

// 	clientMap := sync.Map{} // map[string]net.Addr

// 	// Downstream -> Upstream
// 	go func() {
// 		buf := make([]byte, 4096)
// 		for {
// 			select {
// 			case <-p.stopCh:
// 				return
// 			default:
// 			}
// 			n, clientAddr, err := listener.ReadFrom(buf)
// 			if err != nil {
// 				log.Println("ReadFrom downstream error:", err)
// 				continue
// 			}
// 			packet := make([]byte, n)
// 			copy(packet, buf[:n])

// 			// Track client for upstream response
// 			clientMap.Store(clientAddr.String(), clientAddr)

// 			go func(pkt []byte, cAddr net.Addr) {
// 				time.Sleep(p.Delay)
// 				domain := extractDomain(pkt)
// 				if domain != "" {
// 					p.mu.Lock()
// 					p.PacketCounts[domain]++
// 					p.mu.Unlock()
// 				}
// 				_, err := upstreamListener.WriteTo(pkt, upstreamAddr)
// 				if err != nil {
// 					log.Println("WriteTo upstream error:", err)
// 				}
// 			}(packet, clientAddr)
// 		}
// 	}()

// 	// Upstream -> Downstream
// 	buf := make([]byte, 4096)
// 	for {
// 		select {
// 		case <-p.stopCh:
// 			return nil
// 		default:
// 		}
// 		n, _, err := upstreamListener.ReadFrom(buf)
// 		if err != nil {
// 			log.Println("ReadFrom upstream error:", err)
// 			continue
// 		}
// 		packet := make([]byte, n)
// 		copy(packet, buf[:n])

// 		// For DNS, we can't always know the client, so here we just forward to all known clients
// 		clientMap.Range(func(_, v interface{}) bool {
// 			clientAddr := v.(net.Addr)
// 			time.Sleep(p.Delay)
// 			_, err := listener.WriteTo(packet, clientAddr)
// 			if err != nil {
// 				log.Println("WriteTo client error:", err)
// 			}
// 			return true
// 		})
// 	}
// }
