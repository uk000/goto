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

package tcpproxy

import (
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/proxy/trackers"
	gototls "goto/pkg/tls"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	tcpProxyByPort = map[int]*TCPProxy{}
	proxyLock      sync.RWMutex
)

type TCPProxy struct {
	Port      int                       `json:"port"`
	Enabled   bool                      `json:"enabled"`
	Upstreams map[string]*TCPUpstream   `json:"upstreams"`
	Tracker   *trackers.TCPProxyTracker `json:"tracker"`
	stopChan  chan bool
	lock      sync.RWMutex
}

type TCPUpstream struct {
	Name               string         `json:"name"`
	Protocol           string         `json:"protocol"`
	Endpoint           string         `json:"endpoint"`
	Match              *UpstreamMatch `json:"match"`
	Delay              *types.Delay   `json:"delay"`
	Retries            int            `json:"retries"`
	RetryDelay         time.Duration  `json:"retryDelay"`
	DropPct            int            `json:"dropPct"`
	writeSinceLastDrop int
	isRunning          bool
	conn               *net.UDPConn
	stopChan           chan bool
	lock               sync.RWMutex
}

type UpstreamMatch struct {
	SNI       []string
	sniRegexp *regexp.Regexp
}

func newTCPProxy(port int) *TCPProxy {
	p := &TCPProxy{
		Port:      port,
		Enabled:   true,
		Upstreams: map[string]*TCPUpstream{},
	}
	p.initTracker()
	return p
}

func WillProxyTCP(port int) bool {
	p := getTCPProxyForPort(port)
	return p.Enabled && p.hasAnyTargets()
}

func getTCPProxyForPort(port int) *TCPProxy {
	proxyLock.RLock()
	proxy := tcpProxyByPort[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxy = newTCPProxy(port)
		proxyLock.Lock()
		tcpProxyByPort[port] = proxy
		proxyLock.Unlock()
	}
	return proxy
}

func ProxyTCPConnection(port int, downConn net.Conn) {
	defer downConn.Close()
	startTime := time.Now()
	p := getTCPProxyForPort(port)
	if p.hasSNITargets() {
		p.proxyTCPWithSNI(downConn, startTime)
	} else {
		p.proxyTCPOpaque(downConn, startTime)
	}
}

func (p *TCPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Tracker = trackers.NewTCPTracker()
}

func (p *TCPProxy) hasAnyTargets() bool {
	return len(p.Upstreams) > 0
}

func (p *TCPProxy) addUpstream(up *TCPUpstream) {
	p.lock.Lock()
	p.Upstreams[up.Name] = up
	p.lock.Unlock()
}

func (p *TCPProxy) addNewUpstream(name, address, sni string, retries int, delayMin, delayMax time.Duration) {
	up := &TCPUpstream{
		Name:     name,
		Protocol: "tcp",
		Endpoint: address,
		Retries:  retries,
		Delay:    types.NewDelay(delayMin, delayMax, -1),
	}
	if sni != "" {
		snis := strings.Split(sni, ",")
		snisRegexp := "(" + strings.Join(snis, "|") + ")"
		up.Match = &UpstreamMatch{
			SNI:       snis,
			sniRegexp: regexp.MustCompile(snisRegexp),
		}
	}
	p.lock.Lock()
	p.Upstreams[up.Name] = up
	p.lock.Unlock()
}

func (p *TCPProxy) getMatchingTCPUpstream(sni string) *TCPUpstream {
	for _, up := range p.Upstreams {
		if sni == "" {
			return up
		}
		if up.Match != nil && up.Match.sniRegexp != nil {
			if up.Match.sniRegexp.MatchString(sni) {
				return up
			}
		}
	}
	return nil
}

func (p *TCPProxy) proxyTCPOpaque(downConn net.Conn, startTime time.Time) {
	if up := p.getMatchingTCPUpstream(""); up != nil {
		p.Tracker.IncrementMatchCounts(up.Name, "")
		st := p.Tracker.GetOrAddTargetSessionTracker(up.Name, downConn.RemoteAddr().String())
		st.Downstream.StartTime = startTime
		p.proxyPipe(downConn, up, nil)
	} else {
		p.Tracker.IncrementRejectCount("")
		log.Printf("TCP Proxy[%d]: No matching target found for downstream client [%s]\n", p.Port, downConn.RemoteAddr().String())
	}
}

func (p *TCPProxy) proxyTCPWithSNI(downConn net.Conn, startTime time.Time) {
	sni, buff, err := gototls.ReadTLSSNIFromConn(downConn)
	if err != nil {
		log.Printf("TCP Proxy[%d]: Error while reading downstream SNI from Connection [%s]: %s\n", p.Port, downConn.RemoteAddr().String(), err.Error())
	} else {
		log.Printf("TCP Proxy[%d]: Read downstream SNI = [%s]\n", p.Port, sni)
	}
	if up := p.getMatchingTCPUpstream(sni); up != nil {
		st := p.Tracker.GetOrAddTargetSessionTracker(up.Name, downConn.RemoteAddr().String())
		st.SNI = sni
		st.Downstream.StartTime = startTime
		st.Downstream.FirstByteInAt = startTime
		st.Downstream.TotalBytesRead = buff.Len()
		st.Downstream.TotalReads = 1
		p.Tracker.IncrementMatchCounts(up.Name, sni)
		p.proxyPipe(downConn, up, buff.Bytes())
	} else {
		p.Tracker.IncrementRejectCount(sni)
		log.Printf("TCP Proxy[%d]: No matching target found for SNI [%s] from downstream [%s]\n", p.Port, sni, downConn.RemoteAddr().String())
	}
}

func (p *TCPProxy) proxyPipe(downConn net.Conn, up *TCPUpstream, pendingData []byte) {
	addr, err := net.ResolveTCPAddr("tcp", up.Endpoint)
	if err != nil {
		log.Printf("TCP Proxy[%d]: Error while resolving upstream address: %s\n", p.Port, err.Error())
		return
	}
	done := make(chan bool, 2)
	downAddr := downConn.RemoteAddr().String()
	st := p.Tracker.GetOrAddTargetSessionTracker(up.Name, downAddr)
	st.Upstream.StartTime = time.Now()
	var upConn *net.TCPConn
	defer func() {
		log.Printf("TCP Proxy[%d]: Closing proxy pipe between [%s] and [%s]\n", p.Port, downAddr, up.Endpoint)
		close(done)
		if upConn != nil {
			upConn.Close()
		}
		st.Downstream.Closed = true
		st.Upstream.Closed = true
	}()
	retryDelay := up.RetryDelay
	if retryDelay == 0 {
		retryDelay = 10 * time.Second
	}
	retryBudget := up.Retries + 1
	for retryBudget > 0 {
		retryBudget--
		st.Upstream.Closed = false
		st.Upstream.RemoteClosed = false
		st.Upstream.ReadError = false
		st.Upstream.WriteError = false
		log.Printf("TCP Proxy[%d]: Opening proxy pipe between [%s] and [%s] with Retry [%d]\n", p.Port, downAddr, up.Endpoint, (up.Retries - retryBudget))
		upConn, err = net.DialTCP("tcp", nil, addr)
		if err != nil {
			log.Printf("TCP Proxy[%d]: Error while dialing upstream address: %s\n", p.Port, err.Error())
			if retryBudget > 0 {
				log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", p.Port, retryDelay)
				time.Sleep(retryDelay)
			}
			continue
		}
		retryBudget := up.Retries
		if len(pendingData) > 0 {
			err = util.Write(pendingData, upConn)
			if err != nil {
				if err == io.EOF {
					st.Upstream.RemoteClosed = true
					log.Printf("TCP Proxy[%d]: Connection [%s] closed by upstream\n", p.Port, up.Endpoint)
				} else {
					st.Upstream.WriteError = true
					log.Printf("TCP Proxy[%d]: Error writing to upstream connection [%s]: %s\n", p.Port, up.Endpoint, err.Error())
				}
				return
			} else {
				st.Upstream.TotalBytesWritten = len(pendingData)
				st.Upstream.TotalWrites = 1
			}
		}
		wg := &sync.WaitGroup{}
		wg.Add(2)
		go p.pipe(downConn, upConn, false, st.Downstream, st.Upstream, up, wg, done)
		go p.pipe(upConn, downConn, true, st.Upstream, st.Downstream, up, wg, done)
		wg.Wait()
		if st.Upstream.RemoteClosed && retryBudget > 0 {
			log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", p.Port, retryDelay)
			time.Sleep(retryDelay)
		} else if st.Downstream.RemoteClosed {
			break
		}
	}
}

func (p *TCPProxy) pipe(from, to net.Conn, inverse bool, fromTracker, toTracker *trackers.ConnTracker, target *TCPUpstream, wg *sync.WaitGroup, done chan bool) {
	defer wg.Done()
	downLabel := "downstream"
	upLabel := "upstream"
	if inverse {
		downLabel = "upstream"
		upLabel = "downstream"
	}
	fromAddr := from.RemoteAddr().String()
	toAddr := to.RemoteAddr().String()
	buff := make([]byte, global.ServerConfig.MaxMTUSize)
	for {
		select {
		case <-done:
			return
		default:
		}
		from.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := from.Read(buff)
		readTime := time.Now()
		if err != nil {
			if oe, ok := err.(*net.OpError); ok && oe != nil {
				if oe.Timeout() {
					continue
				}
				if se, ok := oe.Err.(*os.SyscallError); ok && se != nil {
					if se.Err.Error() == syscall.ECONNRESET.Error() {
						fromTracker.RemoteClosed = true
						log.Printf("TCP Proxy[%d]: Connection [%s] reset by %s\n", p.Port, fromAddr, downLabel)
					}
				}
			} else if err == io.EOF {
				fromTracker.RemoteClosed = true
				log.Printf("TCP Proxy[%d]: Connection [%s] closed by %s\n", p.Port, fromAddr, downLabel)
			}
			if !fromTracker.RemoteClosed {
				fromTracker.ReadError = true
				log.Printf("TCP Proxy[%d]: Error reading from %s connection [%s]: %s\n", p.Port, downLabel, fromAddr, err.Error())
			}
			done <- true
			return
		} else {
			fromTracker.TotalReads++
			fromTracker.TotalBytesRead += n
			if fromTracker.FirstByteInAt.IsZero() {
				fromTracker.FirstByteInAt = readTime
			}
			fromTracker.LastByteInAt = readTime
		}
		if p.applyDelay(target, toAddr, nil) {
			toTracker.DelayCount++
		}
		if p.shouldDrop(target) {
			toTracker.DropCount++
			log.Printf("TCP Proxy[%d]: Packet dropped for %s connection [%s]\n", p.Port, upLabel, toAddr)
		} else {
			err = util.Write(buff[:n], to)
			writeTime := time.Now()
			if err != nil {
				if err == io.EOF {
					toTracker.RemoteClosed = true
					log.Printf("TCP Proxy[%d]: Connection [%s] closed by %s\n", p.Port, toAddr, upLabel)
				} else {
					toTracker.WriteError = true
					log.Printf("TCP Proxy[%d]: Error writing to %s connection [%s]: %s\n", p.Port, upLabel, toAddr, err.Error())
				}
				done <- true
				return
			} else {
				toTracker.TotalWrites++
				toTracker.TotalBytesWritten += n
				if toTracker.FirstByteOutAt.IsZero() {
					toTracker.FirstByteOutAt = writeTime
				}
				toTracker.LastByteOutAt = writeTime
			}
		}
	}
}

func (p *TCPProxy) shouldDrop(target *TCPUpstream) bool {
	target.lock.Lock()
	defer target.lock.Unlock()
	if target.DropPct <= 0 {
		return false
	}
	target.writeSinceLastDrop++
	if target.writeSinceLastDrop >= (100 / target.DropPct) {
		target.writeSinceLastDrop = 0
		return true
	}
	return false
}

func (p *TCPProxy) applyDelay(target *TCPUpstream, who string, w http.ResponseWriter) bool {
	delay := target.applyDelay()
	if global.Flags.EnableProxyDebugLogs && delay != "" {
		log.Printf("[DEBUG] Proxy[%d]: Delayed [%s] for Target [%s] by [%s]\n", p.Port, who, target.Name, delay)
		if w != nil {
			w.Header().Add(constants.HeaderGotoProxyDelay, delay)
		}
		return true
	}
	return false
}

func (t *TCPUpstream) applyDelay() (delay string) {
	if t.Delay != nil {
		if global.Flags.EnableProxyDebugLogs {
			log.Printf("[DEBUG] Target [%s]: Delaying Upstream by [%s]\n", t.Name, delay)
		}
		t.Delay.ComputeAndApply()
	}
	return
}

func (p *TCPProxy) hasSNITargets() bool {
	for _, target := range p.Upstreams {
		if target.Match != nil && target.Match.sniRegexp != nil {
			return true
		}
	}
	return false
}
