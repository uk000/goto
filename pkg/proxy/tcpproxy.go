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
	"fmt"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/proxy/trackers"
	gototls "goto/pkg/tls"
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
)

type TCPProxy struct {
	*Proxy
	Tracker *trackers.TCPProxyTracker `json:"tracker"`
}

func newTCPProxy(port int) *TCPProxy {
	p := &TCPProxy{Proxy: newProxy(port)}
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

func (p *TCPProxy) addProxyTarget(w http.ResponseWriter, r *http.Request) {
	target := newProxyTarget("", "", "")
	payload := util.Read(r.Body)
	if err := util.ReadJson(payload, target); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
		events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": payload}, r)
		return
	}
	if target.MatchAll != nil && target.MatchAny != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := "Only one of matchAll and matchAny should be specified"
		fmt.Fprintln(w, msg)
		events.SendRequestEventJSON("Proxy Target Rejected", msg,
			map[string]interface{}{"error": msg, "payload": payload}, r)
		return
	}
	target.isTCP = true
	p.lock.Lock()
	p.Targets[target.Name] = target
	p.lock.Unlock()
}

func (p *TCPProxy) addNewProxyTarget(name, address, sni string, retries int) {
	target := newProxyTarget(name, "tcp", address)
	target.isTCP = true
	target.Retries = retries
	target.Protocol = "tcp"
	if sni != "" {
		snis := strings.Split(sni, ",")
		snisRegexp := "(" + strings.Join(snis, "|") + ")"
		target.MatchAny = &ProxyTargetMatch{
			SNI:       snis,
			sniRegexp: regexp.MustCompile(snisRegexp),
		}
	}
	p.lock.Lock()
	p.Targets[target.Name] = target
	p.lock.Unlock()
}

func (p *TCPProxy) getMatchingTCPTarget(sni string) *TargetMatchInfo {
	for _, t := range p.Targets {
		target := t.(*ProxyTarget)
		if sni == "" {
			return &TargetMatchInfo{target: target}
		}
		if target.MatchAny != nil && target.MatchAny.sniRegexp != nil {
			if target.MatchAny.sniRegexp.MatchString(sni) {
				return &TargetMatchInfo{target: target, SNI: sni}
			}
		}
		if target.MatchAll != nil && target.MatchAll.sniRegexp != nil {
			if target.MatchAll.sniRegexp.MatchString(sni) {
				return &TargetMatchInfo{target: target, SNI: sni}
			}
		}
	}
	return nil
}

func (p *TCPProxy) proxyTCPOpaque(downConn net.Conn, startTime time.Time) {
	if m := p.getMatchingTCPTarget(""); m != nil {
		p.Tracker.IncrementMatchCounts(m.target.Name, "")
		st := p.Tracker.GetOrAddTargetSessionTracker(m.target.Name, downConn.RemoteAddr().String())
		st.Downstream.StartTime = startTime
		p.proxyPipe(downConn, m.target, nil)
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
	if m := p.getMatchingTCPTarget(sni); m != nil {
		st := p.Tracker.GetOrAddTargetSessionTracker(m.target.Name, downConn.RemoteAddr().String())
		st.SNI = sni
		st.Downstream.StartTime = startTime
		st.Downstream.FirstByteInAt = startTime
		st.Downstream.TotalBytesRead = buff.Len()
		st.Downstream.TotalReads = 1
		p.Tracker.IncrementMatchCounts(m.target.Name, sni)
		p.proxyPipe(downConn, m.target, buff.Bytes())
	} else {
		p.Tracker.IncrementRejectCount(sni)
		log.Printf("TCP Proxy[%d]: No matching target found for SNI [%s] from downstream [%s]\n", p.Port, sni, downConn.RemoteAddr().String())
	}
}

func (p *TCPProxy) proxyPipe(downConn net.Conn, target *ProxyTarget, pendingData []byte) {
	addr, err := net.ResolveTCPAddr("tcp", target.Endpoint)
	if err != nil {
		log.Printf("TCP Proxy[%d]: Error while resolving upstream address: %s\n", p.Port, err.Error())
		return
	}
	done := make(chan bool, 2)
	downAddr := downConn.RemoteAddr().String()
	st := p.Tracker.GetOrAddTargetSessionTracker(target.Name, downAddr)
	st.Upstream.StartTime = time.Now()
	var upConn *net.TCPConn
	defer func() {
		log.Printf("TCP Proxy[%d]: Closing proxy pipe between [%s] and [%s]\n", p.Port, downAddr, target.Endpoint)
		close(done)
		if upConn != nil {
			upConn.Close()
		}
		st.Downstream.Closed = true
		st.Upstream.Closed = true
	}()
	retryDelay := target.RetryDelay
	if retryDelay == 0 {
		retryDelay = 10 * time.Second
	}
	retryBudget := target.Retries + 1
	for retryBudget > 0 {
		retryBudget--
		st.Upstream.Closed = false
		st.Upstream.RemoteClosed = false
		st.Upstream.ReadError = false
		st.Upstream.WriteError = false
		log.Printf("TCP Proxy[%d]: Opening proxy pipe between [%s] and [%s] with Retry [%d]\n", p.Port, downAddr, target.Endpoint, (target.Retries - retryBudget))
		upConn, err = net.DialTCP("tcp", nil, addr)
		if err != nil {
			log.Printf("TCP Proxy[%d]: Error while dialing upstream address: %s\n", p.Port, err.Error())
			if retryBudget > 0 {
				log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", p.Port, retryDelay)
				time.Sleep(retryDelay)
			}
			continue
		}
		retryBudget := target.Retries
		if len(pendingData) > 0 {
			err = util.Write(pendingData, upConn)
			if err != nil {
				if err == io.EOF {
					st.Upstream.RemoteClosed = true
					log.Printf("TCP Proxy[%d]: Connection [%s] closed by upstream\n", p.Port, target.Endpoint)
				} else {
					st.Upstream.WriteError = true
					log.Printf("TCP Proxy[%d]: Error writing to upstream connection [%s]: %s\n", p.Port, target.Endpoint, err.Error())
				}
				return
			} else {
				st.Upstream.TotalBytesWritten = len(pendingData)
				st.Upstream.TotalWrites = 1
			}
		}
		wg := &sync.WaitGroup{}
		wg.Add(2)
		go p.pipe(downConn, upConn, false, st.Downstream, st.Upstream, target, wg, done)
		go p.pipe(upConn, downConn, true, st.Upstream, st.Downstream, target, wg, done)
		wg.Wait()
		if st.Upstream.RemoteClosed && retryBudget > 0 {
			log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", p.Port, retryDelay)
			time.Sleep(retryDelay)
		} else if st.Downstream.RemoteClosed {
			break
		}
	}
}

func (p *TCPProxy) pipe(from, to net.Conn, inverse bool, fromTracker, toTracker *trackers.ConnTracker, target *ProxyTarget, wg *sync.WaitGroup, done chan bool) {
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

func (p *TCPProxy) hasSNITargets() bool {
	for _, t := range p.Targets {
		target := t.(*ProxyTarget)
		if target.MatchAll != nil && target.MatchAll.sniRegexp != nil ||
			target.MatchAny != nil && target.MatchAny.sniRegexp != nil {
			return true
		}
	}
	return false
}
