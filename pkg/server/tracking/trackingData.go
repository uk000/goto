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

package tracking

import (
	"encoding/json"
	"net/http"
	"sync"
)

type HeaderValueCounts struct {
	CountsByHeader      int            `json:"countsByHeader"`
	CountsByHeaderValue map[string]int `json:"countsByHeaderValue"`
	lock                sync.RWMutex
}

type HeaderTrackingData map[string]*HeaderValueCounts

type StatusCounts map[int]int

type HeaderValueStatusCounts map[string]map[int]int

type TrackingData struct {
	ByURI                   map[string]int                     `json:"byURI"`
	ByReqHeader             HeaderTrackingData                 `json:"byRequestHeader"`
	ByURIAndReqHeader       map[string]HeaderTrackingData      `json:"byURIAndRequestHeader"`
	ByRespHeader            HeaderTrackingData                 `json:"byResponseHeader"`
	ByRespStatus            StatusCounts                       `json:"byResponseStatus"`
	ByURIAndRespHeader      map[string]HeaderTrackingData      `json:"byURIAndResponseHeader"`
	ByURIAndRespStatus      map[string]StatusCounts            `json:"byURIAndResponseStatus"`
	ByRespHeaderAndStatus   map[string]HeaderValueStatusCounts `json:"byResponseHeaderAndStatus"`
	ByUpURI                 map[string]int                     `json:"byUpstreamURI"`
	ByUpReqHeader           HeaderTrackingData                 `json:"byUpstreamRequestHeader"`
	ByUpRespHeader          HeaderTrackingData                 `json:"byUpstreamResponseHeader"`
	ByUpRespStatus          StatusCounts                       `json:"byUpstreamResponseStatus"`
	ByUpURIAndReqHeader     map[string]HeaderTrackingData      `json:"byUpstreamURIAndRequestHeader"`
	ByUpURIAndRespHeader    map[string]HeaderTrackingData      `json:"byUpstreamURIAndResponseHeader"`
	ByUpURIAndRespStatus    map[string]StatusCounts            `json:"byUpstreamURIAndResponseStatus"`
	ByUpRespHeaderAndStatus map[string]HeaderValueStatusCounts `json:"byUpstreamResponseHeaderAndStatus"`
	lock                    sync.RWMutex
}

// init resets the tracking data to empty tracking maps, dropping the tracked URIs and headers.
func (td *TrackingData) init() {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByURI = map[string]int{}
	td.ByReqHeader = newHeaderTrackingData()
	td.ByURIAndReqHeader = map[string]HeaderTrackingData{}
	td.ByRespHeader = newHeaderTrackingData()
	td.ByRespStatus = newStatusCounts()
	td.ByURIAndRespHeader = map[string]HeaderTrackingData{}
	td.ByURIAndRespStatus = map[string]StatusCounts{}
	td.ByRespHeaderAndStatus = map[string]HeaderValueStatusCounts{}
	td.ByUpURI = map[string]int{}
	td.ByUpReqHeader = newHeaderTrackingData()
	td.ByUpRespHeader = newHeaderTrackingData()
	td.ByUpRespStatus = newStatusCounts()
	td.ByUpURIAndReqHeader = map[string]HeaderTrackingData{}
	td.ByUpURIAndRespHeader = map[string]HeaderTrackingData{}
	td.ByUpURIAndRespStatus = map[string]StatusCounts{}
	td.ByUpRespHeaderAndStatus = map[string]HeaderValueStatusCounts{}
}

// clearCounts zeroes all the counts while keeping the URIs and headers being tracked.
func (td *TrackingData) clearCounts() {
	td.lock.Lock()
	defer td.lock.Unlock()
	for uri := range td.ByURI {
		td.ByURI[uri] = 0
	}
	td.ByReqHeader.clearCounts()
	for _, htd := range td.ByURIAndReqHeader {
		htd.clearCounts()
	}
	td.ByRespHeader.clearCounts()
	td.ByRespStatus = newStatusCounts()
	for _, htd := range td.ByURIAndRespHeader {
		htd.clearCounts()
	}
	for uri := range td.ByURIAndRespStatus {
		td.ByURIAndRespStatus[uri] = newStatusCounts()
	}
	for h := range td.ByRespHeaderAndStatus {
		td.ByRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
	}
	for uri := range td.ByUpURI {
		td.ByUpURI[uri] = 0
	}
	td.ByUpReqHeader.clearCounts()
	td.ByUpRespHeader.clearCounts()
	td.ByUpRespStatus = newStatusCounts()
	for _, htd := range td.ByUpURIAndReqHeader {
		htd.clearCounts()
	}
	for _, htd := range td.ByUpURIAndRespHeader {
		htd.clearCounts()
	}
	for uri := range td.ByUpURIAndRespStatus {
		td.ByUpURIAndRespStatus[uri] = newStatusCounts()
	}
	for h := range td.ByUpRespHeaderAndStatus {
		td.ByUpRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
	}
}

// addRequestTracking starts tracking the request URI and headers, for the URI alone and for the headers in the URI and overall.
func (td *TrackingData) addRequestTracking(uri string, headers []string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	if uri != "" {
		td.ByURI[uri] = 0
		if len(headers) > 0 {
			if td.ByURIAndReqHeader[uri] == nil {
				td.ByURIAndReqHeader[uri] = newHeaderTrackingData()
			}
			td.ByURIAndReqHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByReqHeader.add(headers)
	}
}

// addResponseTracking starts tracking the response status and headers, for the URI and overall.
func (td *TrackingData) addResponseTracking(uri string, headers []string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	if uri != "" {
		if td.ByURIAndRespStatus[uri] == nil {
			td.ByURIAndRespStatus[uri] = newStatusCounts()
		}
		if len(headers) > 0 {
			if td.ByURIAndRespHeader[uri] == nil {
				td.ByURIAndRespHeader[uri] = newHeaderTrackingData()
			}
			td.ByURIAndRespHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByRespHeader.add(headers)
		for _, h := range headers {
			if td.ByRespHeaderAndStatus[h] == nil {
				td.ByRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
			}
		}
	}
}

// addUpstreamRequestTracking starts tracking the upstream request URI and headers, for the URI alone and for the headers in the URI and overall.
func (td *TrackingData) addUpstreamRequestTracking(uri string, headers []string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	if uri != "" {
		td.ByUpURI[uri] = 0
		if len(headers) > 0 {
			if td.ByUpURIAndReqHeader[uri] == nil {
				td.ByUpURIAndReqHeader[uri] = newHeaderTrackingData()
			}
			td.ByUpURIAndReqHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByUpReqHeader.add(headers)
	}
}

// addUpstreamResponseTracking starts tracking the upstream response status and headers, for the URI and overall.
func (td *TrackingData) addUpstreamResponseTracking(uri string, headers []string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	if uri != "" {
		if td.ByUpURIAndRespStatus[uri] == nil {
			td.ByUpURIAndRespStatus[uri] = newStatusCounts()
		}
		if len(headers) > 0 {
			if td.ByUpURIAndRespHeader[uri] == nil {
				td.ByUpURIAndRespHeader[uri] = newHeaderTrackingData()
			}
			td.ByUpURIAndRespHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByUpRespHeader.add(headers)
		for _, h := range headers {
			if td.ByUpRespHeaderAndStatus[h] == nil {
				td.ByUpRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
			}
		}
	}
}

// trackRequest counts a request by its URI and by the tracked headers.
func (td *TrackingData) trackRequest(uri string, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByURI[uri]++
	if htd := td.ByURIAndReqHeader[uri]; htd != nil {
		htd.track(headers)
	}
	td.ByReqHeader.track(headers)
}

// trackResponse counts a response by status and tracked headers, overall and for its URI.
func (td *TrackingData) trackResponse(uri string, statusCode int, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByRespStatus.track(statusCode)
	td.ByRespHeader.track(headers)
	if sc := td.ByURIAndRespStatus[uri]; sc != nil {
		sc.track(statusCode)
	}
	// A response is counted once for the headers of its URI, whatever number of headers it has.
	if htd := td.ByURIAndRespHeader[uri]; htd != nil {
		htd.track(headers)
	}
	trackHeaderStatuses(td.ByRespHeaderAndStatus, headers, statusCode)
}

// trackUpstreamRequest counts an upstream request by its URI and by the tracked headers.
func (td *TrackingData) trackUpstreamRequest(uri string, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByUpURI[uri]++
	if htd := td.ByUpURIAndReqHeader[uri]; htd != nil {
		htd.track(headers)
	}
	td.ByUpReqHeader.track(headers)
}

// trackUpstreamResponse counts an upstream response by status and tracked headers, overall and for its URI.
func (td *TrackingData) trackUpstreamResponse(uri string, statusCode int, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByUpRespStatus.track(statusCode)
	td.ByUpRespHeader.track(headers)
	if sc := td.ByUpURIAndRespStatus[uri]; sc != nil {
		sc.track(statusCode)
	}
	if htd := td.ByUpURIAndRespHeader[uri]; htd != nil {
		htd.track(headers)
	}
	trackHeaderStatuses(td.ByUpRespHeaderAndStatus, headers, statusCode)
}

// trackHeaderStatuses counts the status of a response for the value that it has for each of the tracked headers.
func trackHeaderStatuses(tracked map[string]HeaderValueStatusCounts, headers map[string][]string, statusCode int) {
	for h, counts := range tracked {
		if values := headerValues(headers, h); len(values) > 0 {
			counts.track(values[0], statusCode)
		}
	}
}

// newHeaderTrackingData makes an empty set of tracked headers.
func newHeaderTrackingData() HeaderTrackingData {
	return HeaderTrackingData{}
}

// newStatusCounts makes an empty map of counts by status.
func newStatusCounts() StatusCounts {
	return map[int]int{}
}

// newHeaderValueStatusCounts makes an empty map of status counts by header value.
func newHeaderValueStatusCounts() HeaderValueStatusCounts {
	return map[string]map[int]int{}
}

// track counts one response of the status.
func (sc StatusCounts) track(status int) {
	sc[status]++
}

// track counts one response of the status for the header value, ignoring an empty value.
func (sc HeaderValueStatusCounts) track(hv string, status int) {
	if hv != "" {
		if sc[hv] == nil {
			sc[hv] = map[int]int{}
		}
		sc[hv][status]++
	}
}

// add starts tracking the headers, with fresh counts.
func (htd HeaderTrackingData) add(headers []string) {
	for _, h := range headers {
		htd[h] = newHeaderValueCounts()
	}
}

// track counts the values that the message headers have for each of the tracked headers.
func (htd HeaderTrackingData) track(headers map[string][]string) {
	for h, hvc := range htd {
		for _, hv := range headerValues(headers, h) {
			hvc.track(hv)
		}
	}
}

// headerValues finds the values of a header whatever the case of the name it was added with, since the names of the
// headers of requests and responses are canonicalized (e.g. x-request-id is sent as X-Request-Id).
func headerValues(headers map[string][]string, name string) []string {
	if values := headers[name]; values != nil {
		return values
	}
	return http.Header(headers).Values(name)
}

// clearCounts zeroes the counts of every tracked header.
func (htd HeaderTrackingData) clearCounts() {
	for h := range htd {
		htd[h].clearCounts()
	}
}

// newHeaderValueCounts makes empty counts for a tracked header.
func newHeaderValueCounts() *HeaderValueCounts {
	hvc := &HeaderValueCounts{}
	hvc.init()
	return hvc
}

// init zeroes the counts of the header and its values.
func (hvc *HeaderValueCounts) init() {
	hvc.lock.Lock()
	defer hvc.lock.Unlock()
	hvc.CountsByHeader = 0
	hvc.CountsByHeaderValue = map[string]int{}
}

// track counts one occurrence of the header, and of its non-empty value.
func (hvc *HeaderValueCounts) track(headerValue string) {
	hvc.lock.Lock()
	defer hvc.lock.Unlock()
	hvc.CountsByHeader++
	if headerValue != "" {
		hvc.CountsByHeaderValue[headerValue]++
	}
}

// clearCounts zeroes the counts of the header and its values.
func (hvc *HeaderValueCounts) clearCounts() {
	hvc.lock.Lock()
	defer hvc.lock.Unlock()
	hvc.CountsByHeader = 0
	hvc.CountsByHeaderValue = map[string]int{}
}

// The JSON encoding of the tracking data is done with the locks of the data held, as it is read while requests are
// being tracked. The locks are taken in the order Tracking, TrackingData, HeaderValueCounts, which is the order that
// they are held in while counting.

type trackingDataJSON TrackingData

// MarshalJSON encodes the tracking data under its read lock, as it is updated while requests are tracked.
func (td *TrackingData) MarshalJSON() ([]byte, error) {
	td.lock.RLock()
	defer td.lock.RUnlock()
	return json.Marshal((*trackingDataJSON)(td))
}

type headerValueCountsJSON HeaderValueCounts

// MarshalJSON encodes the header counts under their read lock, as they are updated while requests are tracked.
func (hvc *HeaderValueCounts) MarshalJSON() ([]byte, error) {
	hvc.lock.RLock()
	defer hvc.lock.RUnlock()
	return json.Marshal((*headerValueCountsJSON)(hvc))
}
