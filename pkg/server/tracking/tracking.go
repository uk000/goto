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
	"goto/pkg/server/hooks"
	"sync"
)

type Tracking struct {
	KeyPort map[string]map[int]*TrackingData `json:"keyPortTracking"`
	lock    sync.RWMutex
}

var (
	Tracker = &Tracking{KeyPort: map[string]map[int]*TrackingData{}}
)

// init hooks TrackRequest in as the header tracking function of the server hooks.
func init() {
	hooks.HeaderTrackingFunc = TrackRequest
}

// AddRequestTracking starts tracking the URI and headers of requests for the port and key.
func AddRequestTracking(port int, key, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addRequestTracking(uri, headers)
}

// AddUpstreamRequestTracking starts tracking the URI and headers of upstream requests for the port and key.
func AddUpstreamRequestTracking(port int, key, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addUpstreamRequestTracking(uri, headers)
}

// AddResponseTracking starts tracking the URI, status and headers of responses for the port and key.
func AddResponseTracking(port int, key string, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addResponseTracking(uri, headers)
}

// AddUpstreamResponseTracking starts tracking the URI, status and headers of upstream responses for the port and key.
func AddUpstreamResponseTracking(port int, key string, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addUpstreamResponseTracking(uri, headers)
}

// TrackRequest counts a request for the port and key against what is being tracked.
func TrackRequest(port int, key, uri string, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackRequest(uri, headers)
}

// TrackResponse counts a response for the port and key against what is being tracked.
func TrackResponse(port int, key string, uri string, statusCode int, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackResponse(uri, statusCode, headers)
}

// TrackUpstreamRequest counts an upstream request for the port and key against what is being tracked.
func TrackUpstreamRequest(port int, key, uri string, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackUpstreamRequest(uri, headers)
}

// TrackUpstreamResponse counts an upstream response for the port and key against what is being tracked.
func TrackUpstreamResponse(port int, key string, uri string, statusCode int, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackUpstreamResponse(uri, statusCode, headers)
}

// getKeyPort gives the tracking data of the port and key, creating it if it doesn't exist.
func (t *Tracking) getKeyPort(port int, key string) *TrackingData {
	t.lock.Lock()
	defer t.lock.Unlock()
	keyData, present := t.KeyPort[key]
	if !present {
		keyData = map[int]*TrackingData{}
		t.KeyPort[key] = keyData
	}
	if keyData[port] == nil {
		keyData[port] = &TrackingData{}
		keyData[port].init()
	}
	return keyData[port]
}

// clear drops everything tracked for the port and key.
func (t *Tracking) clear(port int, key string) {
	t.getKeyPort(port, key).init()
}

// clearCounts zeroes the counts for the port and key, keeping what is tracked.
func (t *Tracking) clearCounts(port int, key string) {
	t.getKeyPort(port, key).clearCounts()
}

// MarshalJSON encodes the tracking data of all keys and ports under the read lock.
func (t *Tracking) MarshalJSON() ([]byte, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return json.Marshal(struct {
		KeyPort map[string]map[int]*TrackingData `json:"keyPortTracking"`
	}{t.KeyPort})
}
