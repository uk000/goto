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

package tcpproxy

import (
	"context"
	"net"
	"sync"
	"time"
)

type ConnTracker struct {
	ProxyPort         int       `json:"proxyPort"`
	Upstream          string    `json:"upstream"`
	RemoteAddress     string    `json:"remoteAddress"`
	StartTime         time.Time `json:"startTime"`
	EndTime           time.Time `json:"endTime"`
	FirstByteInAt     time.Time `json:"firstByteInAt"`
	LastByteInAt      time.Time `json:"lastByteInAt"`
	FirstByteOutAt    time.Time `json:"firstByteOutAt"`
	LastByteOutAt     time.Time `json:"lastByteOutAt"`
	TotalBytesRead    int       `json:"totalBytesRead"`
	TotalBytesWritten int       `json:"totalBytesWritten"`
	TotalReads        int       `json:"totalReads"`
	TotalWrites       int       `json:"totalWrites"`
	DelayCount        int       `json:"delayCount"`
	DropCount         int       `json:"dropCount"`
	Closed            bool      `json:"closed"`
	RemoteClosed      bool      `json:"remoteClosed"`
	ReadError         bool      `json:"readError"`
	WriteError        bool      `json:"writeError"`
}

type TCPSessionTracker struct {
	ProxyPort         int            `json:"proxyPort"`
	Upstream          string         `json:"upstream"`
	DownAddress       string         `json:"downAddress"`
	SNI               string         `json:"sni"`
	DownConnTracker   *ConnTracker   `json:"downConn"`
	UpConnTracker     []*ConnTracker `json:"upConn"`
	up                *TCPUpstream
	downConn          net.Conn
	endpointNames     []string       `json:"-"`
	endpointAddresses []*net.TCPAddr `json:"-"`
	upConns           []*net.TCPConn `json:"-"`
	ctx               context.Context
	cancel            context.CancelFunc
	canceled          bool
}

type TCPTargetTracker struct {
	ProxyPort         int                           `json:"proxyPort"`
	Upstream          string                        `json:"upstream"`
	DownAddress       string                        `json:"downAddress"`
	ConnCount         int                           `json:"connCount"`
	ConnCountsBySNI   map[string]int                `json:"connCountsBySNI"`
	TCPSessionTracker map[string]*TCPSessionTracker `json:"tcpSessions"`
	lock              sync.RWMutex
}

type TCPProxyTracker struct {
	ProxyPort         int                          `json:"proxyPort"`
	ConnCount         int                          `json:"connCount"`
	ConnCountsBySNI   map[string]int               `json:"connCountsBySNI"`
	RejectCountsBySNI map[string]int               `json:"rejectCountsBySNI"`
	TargetTrackers    map[string]*TCPTargetTracker `json:"targetTrackers"`
	lock              sync.RWMutex
}

func NewTCPTracker(port int) *TCPProxyTracker {
	return &TCPProxyTracker{
		ProxyPort:         port,
		ConnCountsBySNI:   map[string]int{},
		RejectCountsBySNI: map[string]int{},
		TargetTrackers:    map[string]*TCPTargetTracker{},
	}
}

func NewTCPTargetTracker(port int, upstream, downAddr string) *TCPTargetTracker {
	return &TCPTargetTracker{
		ProxyPort:         port,
		Upstream:          upstream,
		DownAddress:       downAddr,
		ConnCountsBySNI:   map[string]int{},
		TCPSessionTracker: map[string]*TCPSessionTracker{},
	}
}

func NewTCPSessionTracker(port int, upstream, downAddr string) *TCPSessionTracker {
	return &TCPSessionTracker{
		ProxyPort:       port,
		Upstream:        upstream,
		DownAddress:     downAddr,
		DownConnTracker: &ConnTracker{},
		UpConnTracker:   []*ConnTracker{},
	}
}

func (pt *TCPProxyTracker) GetOrAddTargetSessionTracker(port int, targetName, downAddr string) *TCPSessionTracker {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewTCPTargetTracker(port, targetName, downAddr)
	}
	return pt.TargetTrackers[targetName].GetOrAddSessionTracker(port, targetName, downAddr)
}

func (pt *TCPProxyTracker) IncrementMatchCounts(port int, targetName, downAddr, sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	if sni != "" {
		pt.ConnCountsBySNI[sni]++
	}
	if pt.TargetTrackers[targetName] != nil {
		pt.TargetTrackers[targetName].IncrementMatchCounts(sni)
	}
}

func (pt *TCPProxyTracker) IncrementRejectCount(sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	pt.RejectCountsBySNI[sni]++
}

func (tt *TCPTargetTracker) GetOrAddSessionTracker(port int, targetName, downAddr string) *TCPSessionTracker {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	if tt.TCPSessionTracker[downAddr] == nil {
		tt.TCPSessionTracker[downAddr] = NewTCPSessionTracker(port, targetName, downAddr)
	}
	return tt.TCPSessionTracker[downAddr]
}

func (tt *TCPTargetTracker) IncrementMatchCounts(sni string) {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	tt.ConnCount++
	if sni != "" {
		tt.ConnCountsBySNI[sni]++
	}
}
