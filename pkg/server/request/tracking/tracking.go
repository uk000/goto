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
package tracking

import (
	"net/http"
	"strings"
	"sync"

	"goto/pkg/metrics"
	"goto/pkg/server/hooks"
	"goto/pkg/util"
)

type HeaderData struct {
	RequestCountsByHeader      int            `json:"requestCounts"`
	RequestCountsByHeaderValue map[string]int `json:"requestCountsByHeaderValue"`
	lock                       sync.RWMutex
}

type TrackingData struct {
	ByHeader       map[string]*HeaderData            `json:"byHeader"`
	ByURI          map[string]int                    `json:"byURI"`
	ByURIAndHeader map[string]map[string]*HeaderData `json:"byURIAndHeader"`
	lock           sync.RWMutex
}

type RequestTracking struct {
	KeyPort map[string]map[int]*TrackingData `json:"byPort"`
	lock    sync.RWMutex
}

var (
	Tracker = &RequestTracking{KeyPort: map[string]map[int]*TrackingData{}}
)

func init() {
	hooks.HeaderTrackingFunc = Track
}

func (rt *RequestTracking) Init(port int, key, uri string, headers []string) {
	rt.initURIAndHeaders(port, key, uri, headers)
}

func (rt *RequestTracking) TrackCall(port int, key, uri string, headers map[string][]string) {
	rtd := rt.getPortRequestTrackingData(port, key)
	rtd.track(uri, headers)
}

func (rt *RequestTracking) getPortRequestTrackingData(port int, key string) *TrackingData {
	rt.lock.Lock()
	defer rt.lock.Unlock()
	keyData, present := rt.KeyPort[key]
	if !present {
		keyData = map[int]*TrackingData{}
		keyData[port] = &TrackingData{}
		keyData[port].init()
		rt.KeyPort[key] = keyData
	}
	return keyData[port]
}

func (rt *RequestTracking) getRequestTrackingData(key string, r *http.Request) *TrackingData {
	return rt.getPortRequestTrackingData(util.GetRequestOrListenerPortNum(r), key)
}

func (rt *RequestTracking) initHeaders(port int, key string, headers []string) {
	rtd := rt.getPortRequestTrackingData(port, key)
	rtd.initHeaders(headers)
}

func (rt *RequestTracking) initURI(port int, key, uri string) {
	rtd := rt.getPortRequestTrackingData(port, key)
	rtd.initURI(uri)
}

func (rt *RequestTracking) initURIAndHeaders(port int, key, uri string, headers []string) {
	rtd := rt.getPortRequestTrackingData(port, key)
	rtd.initURIAndHeaders(uri, headers)
}

func (rt *RequestTracking) initRequestHeaders(key string, r *http.Request) []string {
	if headers, present := util.GetListParam(r, "headers"); present {
		rtd := rt.getRequestTrackingData(key, r)
		rtd.initHeaders(headers)
		return headers
	}
	return nil
}

func (rt *RequestTracking) removeHeaders(key string, r *http.Request) (headers []string, present bool) {
	if headers, present := util.GetListParam(r, "headers"); present {
		rtd := rt.getRequestTrackingData(key, r)
		for _, h := range headers {
			rtd.removeHeader(h)
		}
		return headers, present
	} else {
		return nil, false
	}
}

func (rt *RequestTracking) clear(key string, r *http.Request) {
	rt.getRequestTrackingData(key, r).init()
}

func (rt *RequestTracking) clearHeaderCounts(key string, r *http.Request) []string {
	rtd := rt.getRequestTrackingData(key, r)
	if headersP, hp := util.GetStringParam(r, "headers"); hp {
		headers := strings.Split(headersP, ",")
		rtd.initHeaders(headers)
		return headers
	}
	rtd.clearCounts()
	return nil
}

func (rt *RequestTracking) getHeaders(key string, r *http.Request) []string {
	return rt.getRequestTrackingData(key, r).getHeaders()
}

func (rtd *TrackingData) init() {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	rtd.ByHeader = map[string]*HeaderData{}
	rtd.ByURI = map[string]int{}
	rtd.ByURIAndHeader = map[string]map[string]*HeaderData{}
}

func (rtd *TrackingData) initURI(uri string) {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	rtd.ByURI[uri] = 0
	rtd.ByURIAndHeader[uri] = map[string]*HeaderData{}
}

func (rtd *TrackingData) initURIAndHeaders(uri string, headers []string) {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	rtd.ByURI[uri] = 0
	rtd.ByURIAndHeader[uri] = map[string]*HeaderData{}
	for _, h := range headers {
		rtd.ByURIAndHeader[uri][h] = &HeaderData{}
		rtd.ByURIAndHeader[uri][h].init()
	}
}

func (rtd *TrackingData) initHeaders(headers []string) {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	for _, h := range headers {
		_, present := rtd.ByHeader[h]
		if !present {
			rtd.ByHeader[h] = &HeaderData{}
			rtd.ByHeader[h].init()
		}
	}
}

func (rtd *TrackingData) removeHeader(header string) bool {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	_, present := rtd.ByHeader[header]
	if present {
		delete(rtd.ByHeader, header)
	}
	return present
}

func (rtd *TrackingData) clearCounts() {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	for _, hd := range rtd.ByHeader {
		hd.init()
	}
}

func (rtd *TrackingData) getHeaderData(header string) *HeaderData {
	rtd.lock.RLock()
	defer rtd.lock.RUnlock()
	return rtd.ByHeader[header]
}

func (rtd *TrackingData) getHeaders() []string {
	rtd.lock.RLock()
	defer rtd.lock.RUnlock()
	headers := make([]string, len(rtd.ByHeader))
	i := 0
	for h := range rtd.ByHeader {
		headers[i] = h
		i++
	}
	return headers
}

func (rtd *TrackingData) track(uri string, headers map[string][]string) {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	rtd.ByURI[uri]++
	for h, hd := range rtd.ByURIAndHeader[uri] {
		if headers[h] != nil {
			if hd == nil {
				hd = &HeaderData{}
				hd.init()
				rtd.ByURIAndHeader[uri][h] = hd
			}
			for _, hv := range headers[h] {
				hd.trackRequest(hv)
			}
		}
	}
	for h, hd := range rtd.ByHeader {
		if headers[h] != nil {
			if hd == nil {
				hd = &HeaderData{}
				hd.init()
				rtd.ByHeader[h] = hd
			}
			for _, hv := range headers[h] {
				hd.trackRequest(hv)
			}
		}
	}
}

func (hd *HeaderData) init() {
	hd.lock.Lock()
	defer hd.lock.Unlock()
	hd.RequestCountsByHeaderValue = map[string]int{}
}

func (hd *HeaderData) trackRequest(headerValue string) {
	hd.lock.Lock()
	defer hd.lock.Unlock()
	hd.RequestCountsByHeader++
	if headerValue != "" {
		hd.RequestCountsByHeaderValue[headerValue]++
	}
}

func track(port string, headers http.Header, uri string, rtd *TrackingData) {
	if rtd == nil {
		return
	}
	for h, hd := range rtd.ByHeader {
		if hv := headers.Get(h); hv != "" {
			metrics.UpdateHeaderRequestCount(port, uri, h, hv)
			hd.trackRequest(hv)
		}
	}
}

func Track(port int, key, uri string, matchedHeaders [][2]string) {
	rtd := Tracker.getPortRequestTrackingData(port, key)
	rtd.ByURI[uri]++
	uriHeaders := rtd.ByURIAndHeader[uri]
	if uriHeaders == nil {
		uriHeaders = map[string]*HeaderData{}
		rtd.ByURIAndHeader[uri] = uriHeaders
	}
	for _, hv := range matchedHeaders {
		if len(hv) == 0 {
			continue
		}
		h := hv[0]
		v := ""
		if len(hv) > 1 {
			v = hv[1]
		}
		metrics.UpdateHeaderRequestCount(string(port), uri, h, v)
		if hd := rtd.ByHeader[h]; hd != nil {
			hd.trackRequest(v)
		}
		if hd := uriHeaders[h]; hd != nil {
			hd.trackRequest(v)
		}
	}
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		rs := util.GetRequestStore(r)
		if !rs.IsKnownNonTraffic {
			rtd := Tracker.getRequestTrackingData("", r)
			go func(port string, headers http.Header, rtd *TrackingData) {
				track(port, headers, r.RequestURI, rtd)
			}(util.GetRequestOrListenerPort(r), r.Header, rtd)
		}
	})
}
