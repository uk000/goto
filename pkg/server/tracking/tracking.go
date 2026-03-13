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

func init() {
	hooks.HeaderTrackingFunc = TrackRequest
}

func AddRequestTracking(port int, key, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addRequestTracking(uri, headers)
}

func AddUpstreamRequestTracking(port int, key, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addUpstreamRequestTracking(uri, headers)
}

func AddResponseTracking(port int, key string, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addResponseTracking(uri, headers)
}

func AddUpstreamResponseTracking(port int, key string, uri string, headers []string) {
	Tracker.getKeyPort(port, key).addUpstreamResponseTracking(uri, headers)
}

func TrackRequest(port int, key, uri string, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackRequest(uri, headers)
}

func TrackResponse(port int, key string, uri string, statusCode int, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackResponse(uri, statusCode, headers)
}

func TrackUpstreamRequest(port int, key, uri string, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackUpstreamRequest(uri, headers)
}

func TrackUpstreamResponse(port int, key string, uri string, statusCode int, headers map[string][]string) {
	td := Tracker.getKeyPort(port, key)
	td.trackUpstreamResponse(uri, statusCode, headers)
}

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

func (t *Tracking) clear(port int, key string) {
	t.getKeyPort(port, key).init()
}

func (t *Tracking) clearCounts(port int, key string) {
	t.getKeyPort(port, key).clearCounts()
}
