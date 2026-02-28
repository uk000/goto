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

import (
	"sync"
	"time"
)

type ConnTracker struct {
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
	SNI        string       `json:"sni"`
	Downstream *ConnTracker `json:"downstream"`
	Upstream   *ConnTracker `json:"upstream"`
}

type TCPTargetTracker struct {
	ConnCount         int                           `json:"connCount"`
	ConnCountsBySNI   map[string]int                `json:"connCountsBySNI"`
	TCPSessionTracker map[string]*TCPSessionTracker `json:"tcpSessions"`
	lock              sync.RWMutex
}

type TCPProxyTracker struct {
	ConnCount         int                          `json:"connCount"`
	ConnCountsBySNI   map[string]int               `json:"connCountsBySNI"`
	RejectCountsBySNI map[string]int               `json:"rejectCountsBySNI"`
	TargetTrackers    map[string]*TCPTargetTracker `json:"targetTrackers"`
	lock              sync.RWMutex
}

func NewTCPTracker() *TCPProxyTracker {
	return &TCPProxyTracker{
		ConnCountsBySNI:   map[string]int{},
		RejectCountsBySNI: map[string]int{},
		TargetTrackers:    map[string]*TCPTargetTracker{},
	}
}

func NewTCPTargetTracker() *TCPTargetTracker {
	return &TCPTargetTracker{
		ConnCountsBySNI:   map[string]int{},
		TCPSessionTracker: map[string]*TCPSessionTracker{},
	}
}

func NewTCPSessionTracker() *TCPSessionTracker {
	return &TCPSessionTracker{
		Downstream: &ConnTracker{},
		Upstream:   &ConnTracker{},
	}
}

func (pt *TCPProxyTracker) GetOrAddTargetSessionTracker(targetName, downAddr string) *TCPSessionTracker {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewTCPTargetTracker()
	}
	return pt.TargetTrackers[targetName].GetOrAddSessionTracker(downAddr)
}

func (pt *TCPProxyTracker) IncrementMatchCounts(targetName, sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	if sni != "" {
		pt.ConnCountsBySNI[sni]++
	}
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewTCPTargetTracker()
	}
	pt.TargetTrackers[targetName].IncrementMatchCounts(sni)
}

func (pt *TCPProxyTracker) IncrementRejectCount(sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	pt.RejectCountsBySNI[sni]++
}

func (tt *TCPTargetTracker) GetOrAddSessionTracker(downAddr string) *TCPSessionTracker {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	if tt.TCPSessionTracker[downAddr] == nil {
		tt.TCPSessionTracker[downAddr] = NewTCPSessionTracker()
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
