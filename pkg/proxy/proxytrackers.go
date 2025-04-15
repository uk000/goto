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

func newHTTPCounts() *HTTPCounts {
	return &HTTPCounts{
		DownstreamRequestCountsByURI: map[string]int{},
		UpstreamRequestCountsByURI:   map[string]int{},
		RequestDropCountsByURI:       map[string]int{},
		ResponseDropCountsByURI:      map[string]int{},
		URIMatchCounts:               map[string]int{},
		HeaderMatchCounts:            map[string]int{},
		HeaderValueMatchCounts:       map[string]map[string]int{},
		QueryMatchCounts:             map[string]int{},
		QueryValueMatchCounts:        map[string]map[string]int{},
	}
}

func (hc *HTTPCounts) incrementMatchCounts(uri, header, headerValue, query, queryValue string) {
	hc.lock.Lock()
	defer hc.lock.Unlock()
	if uri != "" {
		hc.URIMatchCounts[uri]++
	}
	if header != "" {
		if headerValue != "" {
			if hc.HeaderValueMatchCounts[header] == nil {
				hc.HeaderValueMatchCounts[header] = map[string]int{}
			}
			hc.HeaderValueMatchCounts[header][headerValue]++
		} else {
			hc.HeaderMatchCounts[header]++
		}
	}
	if query != "" {
		if queryValue != "" {
			if hc.QueryValueMatchCounts[query] == nil {
				hc.QueryValueMatchCounts[query] = map[string]int{}
			}
			hc.QueryValueMatchCounts[query][queryValue]++
		} else {
			hc.QueryMatchCounts[query]++
		}
	}
}

func (hc *HTTPCounts) incrementDropCount(uri string, requestDropped bool) {
	hc.lock.Lock()
	defer hc.lock.Unlock()
	if requestDropped {
		hc.RequestDropCount++
	} else {
		hc.ResponseDropCount++
	}
	if uri != "" {
		if requestDropped {
			hc.RequestDropCountsByURI[uri]++
		} else {
			hc.ResponseDropCountsByURI[uri]++
		}
	}
}

func newHTTPTracker() *HTTPProxyTracker {
	return &HTTPProxyTracker{
		HTTPCounts:     newHTTPCounts(),
		TargetTrackers: map[string]*HTTPTargetTracker{},
	}
}

func newHTTPTargetTracker() *HTTPTargetTracker {
	return &HTTPTargetTracker{
		HTTPCounts: newHTTPCounts(),
	}
}

func (pt *HTTPProxyTracker) incrementRequestCounts(requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.DownstreamRequestCount++
	pt.DownstreamRequestCountsByURI[requestURI]++
}

func (pt *HTTPProxyTracker) incrementTargetRequestCounts(t *ProxyTarget, requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.UpstreamRequestCount++
	pt.UpstreamRequestCountsByURI[requestURI]++
	if pt.TargetTrackers[t.Name] == nil {
		pt.TargetTrackers[t.Name] = newHTTPTargetTracker()
	}
	pt.TargetTrackers[t.Name].lock.Lock()
	pt.TargetTrackers[t.Name].DownstreamRequestCount++
	pt.TargetTrackers[t.Name].DownstreamRequestCountsByURI[requestURI]++
	pt.TargetTrackers[t.Name].lock.Unlock()
}

func (pt *HTTPProxyTracker) incrementTargetMatchCounts(t *ProxyTarget, uri, header, headerValue, query, queryValue string) {
	pt.incrementMatchCounts(uri, header, headerValue, query, queryValue)
	if pt.TargetTrackers[t.Name] == nil {
		pt.TargetTrackers[t.Name] = newHTTPTargetTracker()
	}
	pt.TargetTrackers[t.Name].incrementMatchCounts(uri, header, headerValue, query, queryValue)
}

func (pt *HTTPProxyTracker) incrementTargetDropCount(t *ProxyTarget, requestURI string, requestDropped bool) {
	pt.incrementDropCount(requestURI, requestDropped)
	if pt.TargetTrackers[t.Name] == nil {
		pt.TargetTrackers[t.Name] = newHTTPTargetTracker()
	}
	pt.TargetTrackers[t.Name].incrementDropCount(requestURI, requestDropped)
}

func newTCPTracker() *TCPProxyTracker {
	return &TCPProxyTracker{
		ConnCountsBySNI:   map[string]int{},
		RejectCountsBySNI: map[string]int{},
		TargetTrackers:    map[string]*TCPTargetTracker{},
	}
}

func newTCPTargetTracker() *TCPTargetTracker {
	return &TCPTargetTracker{
		ConnCountsBySNI:   map[string]int{},
		TCPSessionTracker: map[string]*TCPSessionTracker{},
	}
}

func newTCPSessionTracker() *TCPSessionTracker {
	return &TCPSessionTracker{
		Downstream: &ConnTracker{},
		Upstream:   &ConnTracker{},
	}
}

func (pt *TCPProxyTracker) getOrAddTargetSessionTracker(targetName, downAddr string) *TCPSessionTracker {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = newTCPTargetTracker()
	}
	return pt.TargetTrackers[targetName].getOrAddSessionTracker(downAddr)
}

func (pt *TCPProxyTracker) incrementMatchCounts(targetName, sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	if sni != "" {
		pt.ConnCountsBySNI[sni]++
	}
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = newTCPTargetTracker()
	}
	pt.TargetTrackers[targetName].incrementMatchCounts(sni)
}

func (pt *TCPProxyTracker) incrementRejectCount(sni string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	pt.RejectCountsBySNI[sni]++
}

func (tt *TCPTargetTracker) getOrAddSessionTracker(downAddr string) *TCPSessionTracker {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	if tt.TCPSessionTracker[downAddr] == nil {
		tt.TCPSessionTracker[downAddr] = newTCPSessionTracker()
	}
	return tt.TCPSessionTracker[downAddr]
}

func (tt *TCPTargetTracker) incrementMatchCounts(sni string) {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	tt.ConnCount++
	if sni != "" {
		tt.ConnCountsBySNI[sni]++
	}
}
