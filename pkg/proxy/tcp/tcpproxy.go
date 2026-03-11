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
	"context"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
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
	portProxy = map[int]*TCPProxy{}
	proxyLock sync.RWMutex
)

type TCPProxy struct {
	Port      int                     `yaml:"port" json:"port"`
	Enabled   bool                    `yaml:"enabled" json:"enabled"`
	Upstreams map[string]*TCPUpstream `yaml:"upstreams" json:"upstreams"`
	Tracker   *TCPProxyTracker        `yaml:"tracker" json:"tracker"`
	stopChan  chan bool
	lock      sync.RWMutex
}

type TCPEndpoint struct {
	Name    string `yaml:"name" json:"name"`
	Address string `yaml:"address" json:"address"`
}

type TCPUpstream struct {
	Name               string                  `yaml:"name" json:"name"`
	Endpoints          map[string]*TCPEndpoint `yaml:"endpoints" json:"endpoints"`
	Match              *UpstreamMatch          `yaml:"match" json:"match"`
	Delay              *types.Delay            `yaml:"delay" json:"delay"`
	Retries            int                     `yaml:"retries" json:"retries"`
	RetryDelay         *types.Delay            `yaml:"retryDelay" json:"retryDelay"`
	DropPct            int                     `yaml:"dropPct" json:"dropPct"`
	proxyPort          int
	writeSinceLastDrop int
	isRunning          bool
	conn               *net.UDPConn
	stopChan           chan bool
	lock               sync.RWMutex
}

type UpstreamMatch struct {
	SNI       string
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
	p := GetPortProxy(port)
	return p.Enabled && p.hasAnyTargets()
}

func GetPortProxy(port int) *TCPProxy {
	proxyLock.RLock()
	proxy := portProxy[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxy = newTCPProxy(port)
		proxyLock.Lock()
		portProxy[port] = proxy
		proxyLock.Unlock()
	}
	return proxy
}

func ClearAllProxies() {
	proxyLock.Lock()
	defer proxyLock.Unlock()
	portProxy = map[int]*TCPProxy{}
}

func parseUpstreams(r io.Reader) (map[string]*TCPUpstream, error) {
	upstreams := map[string]*TCPUpstream{}
	if err := util.ReadJsonPayloadFromBody(r, &upstreams); err != nil {
		return nil, err
	}
	if err := ValidateUpstreams(upstreams); err != nil {
		return nil, err
	}
	return upstreams, nil
}

func ValidateUpstreams(upstreams map[string]*TCPUpstream) error {
	for name, upstream := range upstreams {
		if upstream.Name == "" {
			upstream.Name = name
		}
		if upstream.Name == "" {
			return errors.New("upstream name missing")
		}
		if upstream.Endpoints == nil {
			return errors.New("upstream endpoints missing")
		}
		for name, ep := range upstream.Endpoints {
			if ep.Address == "" {
				return fmt.Errorf("target endpoint [%s] missing address/port", name)
			}
		}
	}
	return nil
}

func ProxyTCPConnection(port int, downConn net.Conn) {
	defer downConn.Close()
	startTime := time.Now()
	p := GetPortProxy(port)
	if p.hasSNITargets() {
		p.proxyTCPWithSNI(downConn, startTime)
	} else {
		p.proxyTCPOpaque(downConn, startTime)
	}
}

func (p *TCPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Tracker = NewTCPTracker(p.Port)
}

func (p *TCPProxy) hasAnyTargets() bool {
	return len(p.Upstreams) > 0
}

func (p *TCPProxy) AddUpstreams(upstreams map[string]*TCPUpstream) {
	for _, upstream := range upstreams {
		if upstream.Match != nil && len(upstream.Match.SNI) > 0 {
			snis := strings.Split(upstream.Match.SNI, ",")
			upstream.Match.sniRegexp = regexp.MustCompile("(" + strings.Join(snis, "|") + ")")
		}
		upstream.proxyPort = p.Port
	}
	p.lock.Lock()
	p.Upstreams = upstreams
	p.lock.Unlock()
}

func (p *TCPProxy) addNewUpstream(name, address string) {
	p.lock.Lock()
	p.Upstreams[address] = &TCPUpstream{
		Name: address,
		Endpoints: map[string]*TCPEndpoint{
			name: {Name: name, Address: address},
		},
	}
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
		downAddr := downConn.RemoteAddr().String()
		p.Tracker.IncrementMatchCounts(p.Port, up.Name, downAddr, "")
		st := p.Tracker.GetOrAddTargetSessionTracker(p.Port, up.Name, downAddr)
		st.downConn = downConn
		st.up = up
		st.DownConnTracker.StartTime = startTime
		st.proxyPipe(nil)
	} else {
		p.Tracker.IncrementRejectCount("")
		log.Printf("TCP Proxy[%d]: No matching target found for downstream client [%s]\n", p.Port, downConn.RemoteAddr().String())
	}
}

func (p *TCPProxy) proxyTCPWithSNI(downConn net.Conn, startTime time.Time) {
	sni, buff, err := gototls.ReadTLSSNIFromConn(downConn)
	downAddr := downConn.RemoteAddr().String()
	if err != nil {
		log.Printf("TCP Proxy[%d]: Error while reading downstream SNI from Connection [%s]: %s\n", p.Port, downAddr, err.Error())
	} else {
		log.Printf("TCP Proxy[%d]: Read downstream SNI = [%s]\n", p.Port, sni)
	}
	if up := p.getMatchingTCPUpstream(sni); up != nil {
		st := p.Tracker.GetOrAddTargetSessionTracker(p.Port, up.Name, downAddr)
		st.SNI = sni
		st.DownConnTracker.StartTime = startTime
		st.DownConnTracker.FirstByteInAt = startTime
		st.DownConnTracker.TotalBytesRead = buff.Len()
		st.DownConnTracker.TotalReads = 1
		p.Tracker.IncrementMatchCounts(p.Port, up.Name, downAddr, sni)
		st.proxyPipe(buff.Bytes())
	} else {
		p.Tracker.IncrementRejectCount(sni)
		log.Printf("TCP Proxy[%d]: No matching target found for SNI [%s] from downstream [%s]\n", p.Port, sni, downAddr)
	}
}

func (session *TCPSessionTracker) prepareEndpoints() {
	session.endpointAddresses = []*net.TCPAddr{}
	session.endpointNames = []string{}
	for _, ep := range session.up.Endpoints {
		addr, err := net.ResolveTCPAddr("tcp", ep.Address)
		if err != nil {
			log.Printf("TCP Proxy[%d]: Error while resolving upstream address: %s\n", session.ProxyPort, err.Error())
			return
		}
		session.endpointAddresses = append(session.endpointAddresses, addr)
		session.endpointNames = append(session.endpointNames, addr.String())
		t := &ConnTracker{}
		session.UpConnTracker = append(session.UpConnTracker, t)
		t.StartTime = time.Now()
	}
}

func (session *TCPSessionTracker) connectEndpoints() {
	connectUpstream := func(addr *net.TCPAddr, retryBudget int) *net.TCPConn {
		for retryBudget > 0 {
			retryBudget--
			upConn, err := net.DialTCP("tcp", nil, addr)
			if err != nil {
				log.Printf("TCP Proxy[%d]: Error while dialing upstream address: %s\n", session.ProxyPort, err.Error())
				if retryBudget > 0 {
					if session.up.RetryDelay != nil {
						delay := session.up.RetryDelay.Compute()
						log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", session.ProxyPort, delay)
						session.up.RetryDelay.Apply()
					}
				}
			} else {
				return upConn
			}
		}
		return nil
	}
	for _, addr := range session.endpointAddresses {
		upConn := connectUpstream(addr, session.up.Retries+1)
		session.upConns = append(session.upConns, upConn)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.ctx = ctx
	session.cancel = cancel
}

func (session *TCPSessionTracker) readFromDownstream() []chan []byte {
	inputChans := make([]chan []byte, len(session.endpointAddresses))
	for i := range session.endpointAddresses {
		inputChans[i] = make(chan []byte, 2)
	}
	go session.pipeRead("downstream", session.downConn, session.DownConnTracker, inputChans, nil)
	return inputChans
}

func (session *TCPSessionTracker) sendUpstream(upConn *net.TCPConn, pendingData []byte, dataChan chan []byte, upTracker *ConnTracker, done *util.Channel[bool], wg *sync.WaitGroup) {
	retryBudget := session.up.Retries
	for retryBudget > 0 {
		retryBudget--
		upTracker.Closed = false
		upTracker.RemoteClosed = false
		upTracker.ReadError = false
		upTracker.WriteError = false
		if len(pendingData) > 0 {
			err := util.Write(pendingData, upConn)
			if err != nil {
				if err == io.EOF {
					upTracker.RemoteClosed = true
					log.Printf("TCP Proxy[%d]: Connection [%v] closed by upstream\n", session.ProxyPort, upConn)
				} else {
					upTracker.WriteError = true
					log.Printf("TCP Proxy[%d]: Error writing to upstream connection [%v]: %s\n", session.ProxyPort, upConn, err.Error())
				}
				break
			} else {
				upTracker.TotalBytesWritten = len(pendingData)
				upTracker.TotalWrites = 1
			}
		}
		session.pipeWrite("upstream", upConn, upTracker, session.up, dataChan, done, nil)
		if upTracker.RemoteClosed && retryBudget > 0 {
			if session.up.RetryDelay != nil {
				delay := session.up.RetryDelay.Compute()
				log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", session.ProxyPort, delay)
				session.up.RetryDelay.Apply()
			}
		} else if session.DownConnTracker.RemoteClosed {
			break
		}
	}
	if wg != nil {
		wg.Done()
	}
}

func (session *TCPSessionTracker) proxyPipe(pendingData []byte) {
	if len(session.up.Endpoints) == 0 {
		log.Printf("TCP Proxy[%d]: No upstream endpoints\n", session.ProxyPort)
		return
	}
	session.prepareEndpoints()
	session.connectEndpoints()
	inputChans := session.readFromDownstream()
	done := util.NewChannel[bool]()
	defer func() {
		session.DownConnTracker.Closed = true
		for _, upConn := range session.upConns {
			upConn.Close()
		}
		for _, upTracker := range session.UpConnTracker {
			upTracker.Closed = true
		}
	}()

	wg := &sync.WaitGroup{}
	for i, upConn := range session.upConns {
		wg.Add(1)
		go session.sendUpstream(upConn, pendingData, inputChans[i], session.UpConnTracker[i], done, wg)
		if i == 0 {
			wg.Add(1)
			go session.pipe(upConn, session.downConn, true, session.UpConnTracker[i], session.DownConnTracker, done, wg)
		} else {
			go session.pipeRead("upstream", upConn, session.UpConnTracker[i], nil, done)
		}
	}
	wg.Wait()
	log.Printf("TCP Proxy[%d]: Finished proxying connection [%s] to upstream [%s] endpoints [%v]\n", session.ProxyPort, session.DownAddress, session.up.Name, session.endpointNames)
}

func (session *TCPSessionTracker) pipe(from, to net.Conn, inverse bool, fromTracker, toTracker *ConnTracker, done *util.Channel[bool], wg *sync.WaitGroup) {
	downLabel := "downstream"
	upLabel := "upstream"
	if inverse {
		downLabel = "upstream"
		upLabel = "downstream"
	}
	dataChan := make([]chan []byte, 1)
	dataChan[0] = make(chan []byte, 2)
	go session.pipeRead(downLabel, from, fromTracker, dataChan, done)
	go session.pipeWrite(upLabel, to, toTracker, session.up, dataChan[0], done, wg)
	done.Wait()
}

func (session *TCPSessionTracker) pipeRead(downLabel string, from net.Conn, fromTracker *ConnTracker, inputChans []chan []byte, done *util.Channel[bool]) {
	fromAddr := from.RemoteAddr().String()
	buff := make([]byte, global.ServerConfig.MaxMTUSize)
	for {
		if done != nil {
			if done.ReadNoWait() {
				log.Printf("TCP Proxy[%d]: [READ] Pipe from [%s] received done signal\n", session.ProxyPort, fromAddr)
				return
			}
		}
		readDone := make(chan bool)
		go func() {
			from.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := from.Read(buff)
			close(readDone)
			readTime := time.Now()
			if err != nil {
				if oe, ok := err.(*net.OpError); ok && oe != nil {
					if oe.Timeout() {
						if session.canceled {
							log.Printf("TCP Proxy[%d]: [READ] [%s] connection [%s] session closed\n", session.ProxyPort, downLabel, fromAddr)
						} else {
							log.Printf("TCP Proxy[%d]: [READ] [%s] connection [%s] Read timed out\n", session.ProxyPort, downLabel, fromAddr)
						}
					} else if se, ok := oe.Err.(*os.SyscallError); ok && se != nil {
						if se.Err.Error() == syscall.ECONNRESET.Error() {
							fromTracker.RemoteClosed = true
							log.Printf("TCP Proxy[%d]: Connection [%s] reset by %s\n", session.ProxyPort, fromAddr, downLabel)
						} else {
							log.Printf("TCP Proxy[%d]: [READ] Error reading from %s connection [%s]: %s\n", session.ProxyPort, downLabel, fromAddr, err.Error())
						}
					}
				} else if err == io.EOF {
					fromTracker.RemoteClosed = true
					log.Printf("TCP Proxy[%d]: [READ] Connection [%s] closed by %s\n", session.ProxyPort, fromAddr, downLabel)
				}
				if !fromTracker.RemoteClosed {
					fromTracker.ReadError = true
				}
				if done != nil {
					done.Close()
				}
				session.canceled = true
				session.cancel()
				return
			} else {
				log.Printf("TCP Proxy[%d]: [READ] [%d] bytes from %s [%s]\n", session.ProxyPort, n, downLabel, fromAddr)
				fromTracker.TotalReads++
				fromTracker.TotalBytesRead += n
				if fromTracker.FirstByteInAt.IsZero() {
					fromTracker.FirstByteInAt = readTime
				}
				fromTracker.LastByteInAt = readTime
				if len(inputChans) > 0 {
					for _, c := range inputChans {
						c <- buff[:n]
					}
				} else {
					log.Printf("TCP Proxy[%d]: [READ] Discarded [%d] bytes from %s [%s]\n", session.ProxyPort, n, downLabel, fromAddr)
				}
			}
		}()
		select {
		case <-readDone:
			continue
		case <-session.ctx.Done():
			session.canceled = true
			from.SetReadDeadline(time.Now())
			<-readDone
			return
		}
	}
}

func (session *TCPSessionTracker) pipeWrite(upLabel string, to net.Conn, toTracker *ConnTracker, target *TCPUpstream, dataChan chan []byte, done *util.Channel[bool], wg *sync.WaitGroup) {
	toAddr := to.RemoteAddr().String()
	for {
		if target.applyDelay(toAddr, nil) {
			toTracker.DelayCount++
		}
		select {
		case <-done.Ch:
			if session.canceled {
				log.Printf("TCP Proxy[%d]: [WRITE] [%s] connection [%s] session closed\n", session.ProxyPort, upLabel, toAddr)
			} else {
				log.Printf("TCP Proxy[%d]: [WRITE] [%s] connection [%s] closed\n", session.ProxyPort, upLabel, toAddr)
			}
			if wg != nil {
				wg.Done()
			}
			return
		case buff := <-dataChan:
			if target.shouldDrop() {
				toTracker.DropCount++
				log.Printf("TCP Proxy[%d]: [WRITE] Packet dropped for %s connection [%s]\n", session.ProxyPort, upLabel, toAddr)
			} else {
				err := util.Write(buff, to)
				writeTime := time.Now()
				if err != nil {
					if err == io.EOF {
						toTracker.RemoteClosed = true
						log.Printf("TCP Proxy[%d]: [WRITE] Connection [%s] closed by %s\n", session.ProxyPort, toAddr, upLabel)
					} else {
						toTracker.WriteError = true
						log.Printf("TCP Proxy[%d]: [WRITE] Error writing to %s connection [%s]: %s\n", session.ProxyPort, upLabel, toAddr, err.Error())
					}
					log.Printf("TCP Proxy[%d]: [WRITE] Pipe to [%s] marking done=true on error\n", session.ProxyPort, toAddr)
					done.Close()
					if wg != nil {
						wg.Done()
					}
					return
				} else {
					size := len(buff)
					log.Printf("TCP Proxy[%d]: [WRITE] Wrote [%d] bytes to %s [%s]\n", session.ProxyPort, size, upLabel, toAddr)
					toTracker.TotalWrites++
					toTracker.TotalBytesWritten += len(buff)
					if toTracker.FirstByteOutAt.IsZero() {
						toTracker.FirstByteOutAt = writeTime
					}
					toTracker.LastByteOutAt = writeTime
				}
			}
		}
	}
}

func (t *TCPUpstream) shouldDrop() bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.DropPct <= 0 {
		return false
	}
	t.writeSinceLastDrop++
	if t.writeSinceLastDrop >= (100 / t.DropPct) {
		t.writeSinceLastDrop = 0
		return true
	}
	return false
}

func (t *TCPUpstream) applyDelay(who string, w http.ResponseWriter) bool {
	if t.Delay != nil {
		delay := t.Delay.Compute()
		if global.Flags.EnableProxyDebugLogs {
			log.Printf("[DEBUG] Target [%s]: Delaying Upstream by [%s]\n", t.Name, delay)
		}
		t.Delay.Apply()
		if global.Flags.EnableProxyDebugLogs && delay != 0 {
			log.Printf("[DEBUG] Proxy[%d]: Delayed [%s] for Target [%s] by [%s]\n", t.proxyPort, who, t.Name, delay)
			if w != nil {
				w.Header().Add(constants.HeaderGotoProxyDelay, delay.String())
			}
			return true
		}
	}
	return false
}

func (p *TCPProxy) hasSNITargets() bool {
	for _, target := range p.Upstreams {
		if target.Match != nil && target.Match.sniRegexp != nil {
			return true
		}
	}
	return false
}
