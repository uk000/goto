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
	"encoding/json"
	"sync"
	"time"
)

// connTrackerData is the data of a ConnTracker. A ConnTracker is updated by the goroutines that pump the connection
// while the admin APIs report it, so the data is only read or written with the lock of the ConnTracker held.
type connTrackerData struct {
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
	ConnectFailed     bool      `json:"connectFailed"`
}

type ConnTracker struct {
	connTrackerData
	lock sync.Mutex
}

type TCPSessionTracker struct {
	ProxyPort       int            `json:"proxyPort"`
	Upstream        string         `json:"upstream"`
	DownAddress     string         `json:"downAddress"`
	SNI             string         `json:"sni"`
	DownConnTracker *ConnTracker   `json:"downConn"`
	UpConnTracker   []*ConnTracker `json:"upConn"`
	lock            sync.RWMutex
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

// NewTCPTracker creates a tracker of the TCP proxy of the port, with empty counters.
func NewTCPTracker(port int) *TCPProxyTracker {
	return &TCPProxyTracker{
		ProxyPort:         port,
		ConnCountsBySNI:   map[string]int{},
		RejectCountsBySNI: map[string]int{},
		TargetTrackers:    map[string]*TCPTargetTracker{},
	}
}

// NewTCPTargetTracker creates a tracker of the target of the proxy, with empty counters and no sessions.
func NewTCPTargetTracker(port int, upstream, downAddr string) *TCPTargetTracker {
	return &TCPTargetTracker{
		ProxyPort:         port,
		Upstream:          upstream,
		DownAddress:       downAddr,
		ConnCountsBySNI:   map[string]int{},
		TCPSessionTracker: map[string]*TCPSessionTracker{},
	}
}

// NewTCPSessionTracker creates a tracker of the session of a downstream address, with a tracker for the downstream connection.
func NewTCPSessionTracker(port int, upstream, downAddr string) *TCPSessionTracker {
	return &TCPSessionTracker{
		ProxyPort:       port,
		Upstream:        upstream,
		DownAddress:     downAddr,
		DownConnTracker: &ConnTracker{},
		UpConnTracker:   []*ConnTracker{},
	}
}

// snapshot returns a copy of the tracked data, taken under the lock.
func (ct *ConnTracker) snapshot() connTrackerData {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	return ct.connTrackerData
}

// MarshalJSON serializes a snapshot of the tracked data.
func (ct *ConnTracker) MarshalJSON() ([]byte, error) {
	return json.Marshal(ct.snapshot())
}

// init resets the tracked data for a connection that starts at the given time.
func (ct *ConnTracker) init(port int, upstream, remoteAddr string, startTime time.Time) {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.connTrackerData = connTrackerData{
		ProxyPort:     port,
		Upstream:      upstream,
		RemoteAddress: remoteAddr,
		StartTime:     startTime,
	}
}

// recordPreRead accounts for bytes that were read from the connection before the proxy started to pump it (the TLS
// client hello that gets inspected to find the SNI).
func (ct *ConnTracker) recordPreRead(n int, at time.Time) {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.TotalBytesRead = n
	ct.TotalReads = 1
	ct.FirstByteInAt = at
	ct.LastByteInAt = at
}

// recordRead counts a read of n bytes, and notes its time as that of the first and last byte in.
func (ct *ConnTracker) recordRead(n int, at time.Time) {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.TotalReads++
	ct.TotalBytesRead += n
	if ct.FirstByteInAt.IsZero() {
		ct.FirstByteInAt = at
	}
	ct.LastByteInAt = at
}

// recordWrite counts a write of n bytes, and notes its time as that of the first and last byte out.
func (ct *ConnTracker) recordWrite(n int, at time.Time) {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.TotalWrites++
	ct.TotalBytesWritten += n
	if ct.FirstByteOutAt.IsZero() {
		ct.FirstByteOutAt = at
	}
	ct.LastByteOutAt = at
}

// recordDelay counts a delayed write.
func (ct *ConnTracker) recordDelay() {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.DelayCount++
}

// recordDrop counts a dropped packet.
func (ct *ConnTracker) recordDrop() {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.DropCount++
}

// markRemoteClosed notes that the remote side closed the connection.
func (ct *ConnTracker) markRemoteClosed() {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.RemoteClosed = true
}

// markReadError notes that reading from the connection failed.
func (ct *ConnTracker) markReadError() {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.ReadError = true
}

// markWriteError notes that writing to the connection failed.
func (ct *ConnTracker) markWriteError() {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.WriteError = true
}

// markConnectFailed notes that connecting failed.
func (ct *ConnTracker) markConnectFailed() {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	ct.ConnectFailed = true
}

// markClosed notes that the connection is closed, and when it was, the first time that it is called.
func (ct *ConnTracker) markClosed(at time.Time) {
	ct.lock.Lock()
	defer ct.lock.Unlock()
	if !ct.Closed {
		ct.Closed = true
		ct.EndTime = at
	}
}

// MarshalJSON serializes the session without the lock held while the connection trackers are written.
func (st *TCPSessionTracker) MarshalJSON() ([]byte, error) {
	st.lock.RLock()
	type sessionJSON struct {
		ProxyPort       int            `json:"proxyPort"`
		Upstream        string         `json:"upstream"`
		DownAddress     string         `json:"downAddress"`
		SNI             string         `json:"sni"`
		DownConnTracker *ConnTracker   `json:"downConn"`
		UpConnTracker   []*ConnTracker `json:"upConn"`
	}
	s := sessionJSON{
		ProxyPort:       st.ProxyPort,
		Upstream:        st.Upstream,
		DownAddress:     st.DownAddress,
		SNI:             st.SNI,
		DownConnTracker: st.DownConnTracker,
		UpConnTracker:   append([]*ConnTracker{}, st.UpConnTracker...),
	}
	st.lock.RUnlock()
	return json.Marshal(s)
}

// begin readies the session for a new downstream connection. Sessions are tracked per client address, so a client
// that connects again from the same address gets its session back, and whatever the previous connection left behind
// is dropped.
func (st *TCPSessionTracker) begin(downConn string, startTime time.Time) {
	st.lock.Lock()
	defer st.lock.Unlock()
	st.SNI = ""
	st.DownConnTracker = &ConnTracker{}
	st.DownConnTracker.init(st.ProxyPort, st.Upstream, downConn, startTime)
	st.UpConnTracker = []*ConnTracker{}
}

// setSNI records the SNI of the session.
func (st *TCPSessionTracker) setSNI(sni string) {
	st.lock.Lock()
	defer st.lock.Unlock()
	st.SNI = sni
}

// downTracker returns the tracker of the downstream connection of the session.
func (st *TCPSessionTracker) downTracker() *ConnTracker {
	st.lock.RLock()
	defer st.lock.RUnlock()
	return st.DownConnTracker
}

// addUpTracker creates a tracker for a connection to an upstream endpoint, adds it to the session and returns it.
func (st *TCPSessionTracker) addUpTracker(upstream, remoteAddr string, startTime time.Time) *ConnTracker {
	t := &ConnTracker{}
	t.init(st.ProxyPort, upstream, remoteAddr, startTime)
	st.lock.Lock()
	defer st.lock.Unlock()
	st.UpConnTracker = append(st.UpConnTracker, t)
	return t
}

// MarshalJSON serializes the target tracker under the read lock.
func (tt *TCPTargetTracker) MarshalJSON() ([]byte, error) {
	tt.lock.RLock()
	defer tt.lock.RUnlock()
	type targetJSON struct {
		ProxyPort         int                           `json:"proxyPort"`
		Upstream          string                        `json:"upstream"`
		DownAddress       string                        `json:"downAddress"`
		ConnCount         int                           `json:"connCount"`
		ConnCountsBySNI   map[string]int                `json:"connCountsBySNI"`
		TCPSessionTracker map[string]*TCPSessionTracker `json:"tcpSessions"`
	}
	return json.Marshal(targetJSON{
		ProxyPort:         tt.ProxyPort,
		Upstream:          tt.Upstream,
		DownAddress:       tt.DownAddress,
		ConnCount:         tt.ConnCount,
		ConnCountsBySNI:   tt.ConnCountsBySNI,
		TCPSessionTracker: tt.TCPSessionTracker,
	})
}

// MarshalJSON serializes the proxy tracker under the read lock.
func (pt *TCPProxyTracker) MarshalJSON() ([]byte, error) {
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	type proxyJSON struct {
		ProxyPort         int                          `json:"proxyPort"`
		ConnCount         int                          `json:"connCount"`
		ConnCountsBySNI   map[string]int               `json:"connCountsBySNI"`
		RejectCountsBySNI map[string]int               `json:"rejectCountsBySNI"`
		TargetTrackers    map[string]*TCPTargetTracker `json:"targetTrackers"`
	}
	return json.Marshal(proxyJSON{
		ProxyPort:         pt.ProxyPort,
		ConnCount:         pt.ConnCount,
		ConnCountsBySNI:   pt.ConnCountsBySNI,
		RejectCountsBySNI: pt.RejectCountsBySNI,
		TargetTrackers:    pt.TargetTrackers,
	})
}

// GetOrAddTargetSessionTracker returns the session tracker of the downstream address under the target, creating the target tracker and the session tracker if needed.
func (pt *TCPProxyTracker) GetOrAddTargetSessionTracker(port int, targetName, downAddr string) *TCPSessionTracker {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewTCPTargetTracker(port, targetName, downAddr)
	}
	return pt.TargetTrackers[targetName].GetOrAddSessionTracker(port, targetName, downAddr)
}

// IncrementMatchCounts counts a connection for the proxy, the SNI and the target.
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

// IncrementRejectCount counts a connection that was rejected for its SNI.
func (pt *TCPProxyTracker) IncrementRejectCount(sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	pt.RejectCountsBySNI[sni]++
}

// GetOrAddSessionTracker returns the session tracker of the downstream address, creating it if it doesn't exist.
func (tt *TCPTargetTracker) GetOrAddSessionTracker(port int, targetName, downAddr string) *TCPSessionTracker {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	if tt.TCPSessionTracker[downAddr] == nil {
		tt.TCPSessionTracker[downAddr] = NewTCPSessionTracker(port, targetName, downAddr)
	}
	return tt.TCPSessionTracker[downAddr]
}

// IncrementMatchCounts counts a connection for the target and its SNI.
func (tt *TCPTargetTracker) IncrementMatchCounts(sni string) {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	tt.ConnCount++
	if sni != "" {
		tt.ConnCountsBySNI[sni]++
	}
}
