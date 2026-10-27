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

package timeline

import (
	"encoding/json"
	"fmt"
	aicommon "goto/pkg/ai/common"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TimelineUpdateNotifierFunc func(string, any, bool) error
type TimelineEndNotifierFunc func(string, any, bool)

type InstanceInfo struct {
	Label     string
	Host      string
	Listener  string
	Pod       string
	PodIP     string
	Node      string
	Namespace string
	Cluster   string
	Metadata  map[string]any
}

type RemoteInfo struct {
	RemoteTarget           string
	RemoteURL              string
	RemoteLabel            string
	OutboundRequestArgs    any
	OutboundRequestHeaders any
	HeadersConfig          *types.Headers
	RequestCount           int
	Concurrent             int
}

// GotoClientInfo is shared between the goroutines that make a call and the ones that report on it. What is stored in it
// after it is made, with StoreHeaders, is guarded by its lock, which JSON of it is written under.
type GotoClientInfo struct {
	*InstanceInfo
	*RemoteInfo
	lock sync.RWMutex
}

type GotoServerInfo struct {
	ServerInfo *InstanceInfo
}

type Event struct {
	Idx            int `json:"Index"`
	Label          string
	Text           string
	At             time.Time
	Client         *GotoClientInfo
	RemoteServer   *GotoServerInfo
	RemoteClient   *GotoClientInfo
	RemoteTimeline *Timeline
	RemoteText     string
	RemoteData     any
}

type Timeline struct {
	TYPE             string `json:"TYPE,omitempty"`
	Port             int
	Label            string
	Server           *GotoServerInfo
	Events           []*Event
	Data             map[string]any
	RemoteCalls      map[string]map[string]any
	UpstreamHeaders  map[string]any
	Status           int
	UpstreamStatuses map[string]any
	Finished         bool
	Success          bool
	ResultOnly       bool
	NoEvents         bool
	RemoteGotos      map[string]bool
	stream           chan *types.Pair[string, any]
	streamPreferred  bool
	updateNotifier   TimelineUpdateNotifierFunc
	endNotifier      TimelineEndNotifierFunc
	eventCounter     atomic.Int32
	// lock guards the fields above, and the choice of the stream, which is made by SetStreamPreferred. It is not held
	// while the notifiers are called: they report on the timeline, and the timeline has to be readable for them.
	lock sync.RWMutex
	// sendLock keeps the notifications in the order of the updates that they report. It is taken before lock and never
	// the other way around.
	sendLock sync.Mutex
}

var (
	TIMELINE string = "TIMELINE"
)

func Ptr(s string) *string { return &s }

func NewTimeline(port int, label string, metadata map[string]any, inboundArgs *aicommon.ToolCallArgs, inboundHeaders http.Header,
	stream chan *types.Pair[string, any], updateNotifier TimelineUpdateNotifierFunc, endNotifier TimelineEndNotifierFunc) *Timeline {
	t := &Timeline{
		TYPE:            TIMELINE,
		Port:            port,
		Label:           label,
		Server:          CreateOrGetGotoServerInfo(port, metadata, inboundArgs, inboundHeaders),
		Events:          []*Event{},
		Data:            map[string]any{},
		RemoteCalls:     map[string]map[string]any{},
		UpstreamHeaders: map[string]any{},
		Finished:        false,
		stream:          stream,
		updateNotifier:  updateNotifier,
		endNotifier:     endNotifier,
		eventCounter:    atomic.Int32{},
	}
	t.send("ServerInfo", t.Server.ServerInfo, true, false)
	return t
}

func LoadTimeline(data map[string]any) (t *Timeline, e error) {
	t = &Timeline{}
	e = json.Unmarshal(util.ToJSONBytes(data), t)
	return
}

func IsTimeline(data any) bool {
	if _, ok := data.(*Timeline); ok {
		return true
	} else if m, ok := data.(map[string]any); ok {
		if typ, ok := m["TYPE"].(string); ok && typ == TIMELINE {
			return true
		}
	}
	return false
}

func IsResult(data any) bool {
	if m, ok := data.(map[string]any); ok {
		if m["Result"] != nil {
			return true
		}
	}
	return false
}

func IsClientInfo(data any) bool {
	if util.IsNil(data) {
		return false
	}
	if _, ok := data.(*GotoClientInfo); ok {
		return true
	} else if m, ok := data.(map[string]any); ok {
		if m["ClientInfo"] != nil {
			return true
		}
	}
	return false
}

func CheckAndGetClientInfo(data any) (*GotoClientInfo, bool) {
	if util.IsNil(data) {
		return nil, false
	}
	if c, ok := data.(*GotoClientInfo); ok {
		return c, true
	} else if m, ok := data.(map[string]any); ok {
		if m["ClientInfo"] != nil {
			c := &GotoClientInfo{}
			if err := json.Unmarshal(util.ToJSONBytes(m), c); err == nil {
				return c, true
			}
		}
	}
	return nil, false
}
func IsServerInfo(data any) bool {
	if util.IsNil(data) {
		return false
	}
	if _, ok := data.(*GotoServerInfo); ok {
		return true
	} else if m, ok := data.(map[string]any); ok {
		if m["ServerInfo"] != nil {
			return true
		}
	}
	return false
}

func CheckAndGetServerInfo(data any) (*GotoServerInfo, bool) {
	if util.IsNil(data) {
		return nil, false
	}
	if s, ok := data.(*GotoServerInfo); ok {
		return s, true
	} else {
		s := &GotoServerInfo{}
		if err := util.ReadJsonFromAny(data, s); err == nil {
			return s, true
		} else if m, ok := data.(map[string]any); ok {
			if m["ServerInfo"] != nil {
				if err := json.Unmarshal(util.ToJSONBytes(m), s); err == nil {
					return s, true
				}
			}
		}
	}
	return nil, false
}

func CheckAndGetTimeline(data any) *Timeline {
	if IsTimeline(data) {
		if t, ok := data.(*Timeline); ok {
			return t
		} else if m, ok := data.(map[string]any); ok {
			t, _ = LoadTimeline(m)
			return t
		}
	}
	return nil
}

func CheckAndGetResultOrHeaders(data any) (result, headers map[string]any) {
	if m, ok := data.(map[string]any); ok {
		for k := range m {
			if strings.Contains(k, "Result") {
				return m, nil
			} else if strings.Contains(k, "headers") || strings.Contains(k, "Headers") {
				if m2, ok := m[k].(map[string]any); ok {
					return nil, m2
				}
			}
		}
	}
	return nil, nil
}

func (t *Timeline) SetStreamPreferred(stream chan *types.Pair[string, any]) {
	if stream != nil {
		t.lock.Lock()
		defer t.lock.Unlock()
		t.stream = stream
		t.streamPreferred = true
	}
}

func (t *Timeline) EndTimeline(label, text string, status int, upstreamStatuses map[string]any, data any, success bool) {
	t.lock.Lock()
	t.Finished = true
	t.Success = success
	if status > 0 {
		t.Status = status
	}
	t.UpstreamStatuses = upstreamStatuses
	t.lock.Unlock()
	if data != nil {
		t.AddData(text, data, true)
	} else {
		t.AddEvent(label, text)
	}
}

func (t *Timeline) AddEvent(label, text string) {
	t.addEvent(label, text, nil, "", nil, nil, nil, false)
}

func (t *Timeline) AddEventWithClient(label, text string, client *GotoClientInfo) {
	t.addEvent(label, text, client, "", nil, nil, nil, false)
}

func (t *Timeline) AddEventWithRemote(label, text string, remoteText string, remoteServer *GotoServerInfo, remoteClient *GotoClientInfo, remoteData any, isJson bool) {
	t.addEvent(label, text, nil, remoteText, remoteServer, remoteClient, remoteData, isJson)
}

func (t *Timeline) addEvent(label, text string, client *GotoClientInfo, remoteText string, remoteServer *GotoServerInfo, remoteClient *GotoClientInfo, remoteData any, isJson bool) {
	var remoteTimeline *Timeline
	if remoteData != nil {
		remoteTimeline = CheckAndGetTimeline(remoteData)
		if remoteClient != nil || remoteServer != nil || remoteTimeline != nil {
			remoteData = nil
		}
	}
	// The notifications are sent in the order of the events, which are numbered and stored in that order too.
	t.sendLock.Lock()
	defer t.sendLock.Unlock()
	t.lock.Lock()
	noEvents, finished := t.NoEvents, t.Finished
	if !noEvents {
		t.Events = append(t.Events, t.NewEvent(label, text, client, remoteClient, remoteServer, remoteTimeline, remoteText, remoteData))
	}
	t.lock.Unlock()
	if !noEvents {
		if remoteText != "" {
			t.send(fmt.Sprintf("%s: %s", text, remoteText), nil, false, finished)
		} else {
			t.send(text, nil, false, finished)
		}
	}
	// if client != nil {
	// 	t.send("ClientInfo", client, true, finished)
	// }
	// if remoteClient != nil {
	// 	t.send("RemoteClientInfo", client, true, finished)
	// }
	if !util.IsNil(remoteServer) && !util.IsNil(remoteServer.ServerInfo) {
		t.send("RemoteServerInfo", remoteServer.ServerInfo, true, finished)
	}
	if remoteData != nil {
		t.send(label, remoteData, isJson, finished)
	}
}

func (t *Timeline) AddData(key string, data any, json bool) {
	t.sendLock.Lock()
	defer t.sendLock.Unlock()
	t.send(key, data, json, false)
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.Data == nil {
		t.Data = map[string]any{}
	}
	t.Data[key] = data
}

func (t *Timeline) AddRemoteCall(remoteID, callID string, data any) {
	// t.lock.Lock()
	// defer t.lock.Unlock()
	// if t.RemoteCalls[remoteID] == nil {
	// 	t.RemoteCalls[remoteID] = map[string]any{}
	// }
	// t.RemoteCalls[remoteID][callID] = data
}

// send reports an update to the notifier of the timeline, or to its stream if there is no notifier to report to. It is
// called with sendLock held, and without lock: the notifiers are free to read the timeline.
func (t *Timeline) send(key string, data any, json, finish bool) {
	t.lock.RLock()
	stream, streamPreferred, success := t.stream, t.streamPreferred, t.Success
	t.lock.RUnlock()
	sent := false
	if finish {
		if t.endNotifier != nil {
			t.endNotifier(key, data, success)
			sent = true
		}
	} else if !streamPreferred {
		if t.updateNotifier != nil {
			t.updateNotifier(key, data, json)
			sent = true
		}
	}
	if !sent && stream != nil {
		stream <- types.NewPair[string, any](key, data)
	}
}

func (t *Timeline) NewEvent(label, text string, client, remoteClient *GotoClientInfo, remoteServer *GotoServerInfo, timeline *Timeline, remoteText string, remoteData any) *Event {
	return &Event{
		Label:          label,
		Text:           text,
		Idx:            int(t.eventCounter.Add(1)),
		At:             time.Now(),
		Client:         client,
		RemoteClient:   remoteClient,
		RemoteServer:   remoteServer,
		RemoteTimeline: timeline,
		RemoteText:     remoteText,
		RemoteData:     remoteData,
	}
}

func CreateInstanceInfo(port int, label string, metadata map[string]any, inboundArgs *aicommon.ToolCallArgs, inboundHeaders http.Header) *InstanceInfo {
	if inboundArgs != nil && inboundArgs.IsEmpty() {
		inboundArgs = nil
	}
	return &InstanceInfo{
		Label:     label,
		Host:      global.Self.HostLabel,
		Listener:  global.Funcs.GetListenerLabelForPort(port),
		Pod:       global.Self.PodName,
		PodIP:     global.Self.PodIP,
		Node:      global.Self.NodeName,
		Namespace: global.Self.Namespace,
		Cluster:   global.Self.Cluster,
		Metadata:  metadata,
	}
}

func CreateRemoteInfo(target, url, server string, outArgs, outHeaders any, requestCount, concurrent int) *RemoteInfo {
	return &RemoteInfo{
		RemoteTarget:           target,
		RemoteURL:              url,
		RemoteLabel:            server,
		OutboundRequestArgs:    outArgs,
		OutboundRequestHeaders: outHeaders,
		HeadersConfig:          &types.Headers{},
		RequestCount:           requestCount,
		Concurrent:             concurrent,
	}
}

func CreateOrGetGotoServerInfo(port int, metadata map[string]any, inboundArgs *aicommon.ToolCallArgs, inboundHeaders http.Header) *GotoServerInfo {
	return &GotoServerInfo{
		ServerInfo: CreateInstanceInfo(port, global.Self.Name, metadata, inboundArgs, inboundHeaders),
	}
}

func BuildGotoClientInfo(port int, label, target, url, server string, inHeaders, outHeaders http.Header,
	inArgs, outArgs *aicommon.ToolCallArgs, requestCount, concurrent int, metadata map[string]any) *GotoClientInfo {
	return &GotoClientInfo{
		InstanceInfo: CreateInstanceInfo(port, label, metadata, inArgs, inHeaders),
		RemoteInfo:   CreateRemoteInfo(target, url, server, outArgs, outHeaders, requestCount, concurrent),
	}
}

// StoreHeaders keeps the headers that the client sent. The map belongs to the caller, as it did before: this is about the
// reference to it, which the reports of the client are written from.
func (c *GotoClientInfo) StoreHeaders(request http.Header) {
	if c == nil {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.RemoteInfo == nil {
		c.RemoteInfo = &RemoteInfo{}
	}
	c.OutboundRequestHeaders = request
}

// MarshalJSON writes the client as its embedded infos, which is what it is without the lock, while it is not changed.
func (c *GotoClientInfo) MarshalJSON() ([]byte, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return json.Marshal(struct {
		*InstanceInfo
		*RemoteInfo
	}{c.InstanceInfo, c.RemoteInfo})
}

// clone is a copy of the client that is not changed by what is stored in the client after it.
func (c *GotoClientInfo) clone() *GotoClientInfo {
	if c == nil {
		return nil
	}
	c.lock.RLock()
	defer c.lock.RUnlock()
	c2 := &GotoClientInfo{}
	if c.InstanceInfo != nil {
		info := *c.InstanceInfo
		c2.InstanceInfo = &info
	}
	if c.RemoteInfo != nil {
		remote := *c.RemoteInfo
		c2.RemoteInfo = &remote
	}
	return c2
}

func (s *GotoServerInfo) clone() *GotoServerInfo {
	if s == nil {
		return nil
	}
	s2 := *s
	if s.ServerInfo != nil {
		info := *s.ServerInfo
		s2.ServerInfo = &info
	}
	return &s2
}

// timelineJSON has the fields of a timeline that are reported, as they are in the JSON of a timeline.
type timelineJSON struct {
	TYPE             string `json:"TYPE,omitempty"`
	Port             int
	Label            string
	Server           *GotoServerInfo
	Events           []*Event
	Data             map[string]any
	RemoteCalls      map[string]map[string]any
	UpstreamHeaders  map[string]any
	Status           int
	UpstreamStatuses map[string]any
	Finished         bool
	Success          bool
	ResultOnly       bool
	NoEvents         bool
	RemoteGotos      map[string]bool
}

// MarshalJSON writes the timeline while it is read-locked, so a timeline that goes on to change is written as it was at
// some point, and not in pieces.
func (t *Timeline) MarshalJSON() ([]byte, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return json.Marshal(&timelineJSON{
		TYPE:             t.TYPE,
		Port:             t.Port,
		Label:            t.Label,
		Server:           t.Server,
		Events:           t.Events,
		Data:             t.Data,
		RemoteCalls:      t.RemoteCalls,
		UpstreamHeaders:  t.UpstreamHeaders,
		Status:           t.Status,
		UpstreamStatuses: t.UpstreamStatuses,
		Finished:         t.Finished,
		Success:          t.Success,
		ResultOnly:       t.ResultOnly,
		NoEvents:         t.NoEvents,
		RemoteGotos:      t.RemoteGotos,
	})
}

func copyMap[V any](m map[string]V) map[string]V {
	if m == nil {
		return nil
	}
	m2 := make(map[string]V, len(m))
	for k, v := range m {
		m2[k] = v
	}
	return m2
}

// Snapshot is a copy of the timeline as it is now, which is not changed by what is added to the timeline after it, and
// does not change the timeline. It has no stream and no notifiers, so what is added to it is not reported to anyone.
// The values that were added as data are shared with the timeline, the events and the maps of the timeline are not.
func (t *Timeline) Snapshot() *Timeline {
	t.lock.RLock()
	defer t.lock.RUnlock()
	s := &Timeline{
		TYPE:             t.TYPE,
		Port:             t.Port,
		Label:            t.Label,
		Server:           t.Server.clone(),
		Data:             copyMap(t.Data),
		UpstreamHeaders:  copyMap(t.UpstreamHeaders),
		Status:           t.Status,
		UpstreamStatuses: copyMap(t.UpstreamStatuses),
		Finished:         t.Finished,
		Success:          t.Success,
		ResultOnly:       t.ResultOnly,
		NoEvents:         t.NoEvents,
		RemoteGotos:      copyMap(t.RemoteGotos),
	}
	if t.Events != nil {
		s.Events = make([]*Event, len(t.Events))
		for i, e := range t.Events {
			if e == nil {
				continue
			}
			e2 := *e
			e2.Client = e.Client.clone()
			e2.RemoteClient = e.RemoteClient.clone()
			e2.RemoteServer = e.RemoteServer.clone()
			if e.RemoteTimeline != nil {
				e2.RemoteTimeline = e.RemoteTimeline.Snapshot()
			}
			s.Events[i] = &e2
		}
	}
	if t.RemoteCalls != nil {
		s.RemoteCalls = make(map[string]map[string]any, len(t.RemoteCalls))
		for k, v := range t.RemoteCalls {
			s.RemoteCalls[k] = copyMap(v)
		}
	}
	s.eventCounter.Store(t.eventCounter.Load())
	return s
}
