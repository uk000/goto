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
	Label          string
	Host           string
	Listener       string
	Pod            string
	PodIP          string
	Node           string
	Namespace      string
	Cluster        string
	InboundArgs    *aicommon.ToolCallArgs
	InboundHeaders http.Header
	Metadata       map[string]any
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

type GotoClientInfo struct {
	*InstanceInfo
	*RemoteInfo
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
	TYPE            string `json:"TYPE,omitempty"`
	Port            int
	Label           string
	Server          *GotoServerInfo
	Events          []*Event
	Data            map[string]any
	RemoteCalls     map[string]map[string]any
	Finished        bool
	Success         bool
	ResultOnly      bool
	NoEvents        bool
	stream          chan *types.Pair[string, any]
	streamPreferred bool
	updateNotifier  TimelineUpdateNotifierFunc
	endNotifier     TimelineEndNotifierFunc
	eventCounter    atomic.Int32
	lock            sync.RWMutex
}

var (
	TIMELINE string = "TIMELINE"
)

func Ptr(s string) *string { return &s }

func NewTimeline(port int, label string, metadata map[string]any, inboundArgs *aicommon.ToolCallArgs, inboundHeaders http.Header,
	stream chan *types.Pair[string, any], updateNotifier TimelineUpdateNotifierFunc, endNotifier TimelineEndNotifierFunc) *Timeline {
	t := &Timeline{
		TYPE:           TIMELINE,
		Port:           port,
		Label:          "TIMELINE>" + label,
		Server:         CreateOrGetGotoServerInfo(port, metadata, inboundArgs, inboundHeaders),
		Events:         []*Event{},
		Data:           map[string]any{},
		RemoteCalls:    map[string]map[string]any{},
		Finished:       false,
		stream:         stream,
		updateNotifier: updateNotifier,
		endNotifier:    endNotifier,
		eventCounter:   atomic.Int32{},
	}
	t.send("ServerInfo", t.Server, true, false)
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
		if m["TYPE"] != nil && m["TYPE"].(string) == TIMELINE {
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
	} else if m, ok := data.(map[string]any); ok {
		if m["ServerInfo"] != nil {
			s := &GotoServerInfo{}
			if err := json.Unmarshal(util.ToJSONBytes(m), s); err == nil {
				return s, true
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

func CheckAndGetResult(data any) map[string]any {
	if m, ok := data.(map[string]any); ok {
		for k := range m {
			if strings.Contains(k, "Result") {
				return m
			}
		}
	}
	return nil
}

func (t *Timeline) SetStreamPreferred(stream chan *types.Pair[string, any]) {
	t.stream = stream
	t.streamPreferred = true
}

func (t *Timeline) EndTimeline(label, text string, data any, success bool) {
	t.Finished = true
	t.Success = success
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
	if !t.NoEvents {
		event := t.NewEvent(label, text, client, remoteClient, remoteServer, remoteTimeline, remoteText, remoteData)
		t.lock.Lock()
		defer t.lock.Unlock()
		t.Events = append(t.Events, event)
		if remoteText != "" {
			t.send(fmt.Sprintf("%s: %s", text, remoteText), nil, false, t.Finished)
		} else {
			t.send(text, nil, false, t.Finished)
		}
	}
	if client != nil {
		t.send("ClientInfo", client, true, t.Finished)
	}
	if remoteClient != nil {
		t.send("ClientInfo", client, true, t.Finished)
	}
	if remoteServer != nil {
		t.send("ServerInfo", remoteServer, true, t.Finished)
	}
	if remoteData != nil {
		t.send(label, remoteData, isJson, t.Finished)
	}
}

func (t *Timeline) AddData(key string, data any, json bool) {
	t.send(key, data, json, false)
	t.lock.Lock()
	defer t.lock.Unlock()
	t.Data[key] = data
}

func (t *Timeline) AddRemoteCall(remoteID, callID string, data any) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.RemoteCalls[remoteID] == nil {
		t.RemoteCalls[remoteID] = map[string]any{}
	}
	t.RemoteCalls[remoteID][callID] = data
}

func (t *Timeline) send(key string, data any, json, finish bool) {
	sent := false
	if finish {
		if t.endNotifier != nil {
			t.endNotifier(key, data, t.Success)
			sent = true
		}
	} else if !t.streamPreferred {
		if t.updateNotifier != nil {
			t.updateNotifier(key, data, json)
			sent = true
		}
	}
	if !sent && t.stream != nil {
		t.stream <- types.NewPair[string, any](key, data)
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
		Label:          label,
		Host:           global.Self.HostLabel,
		Listener:       global.Funcs.GetListenerLabelForPort(port),
		Pod:            global.Self.PodName,
		PodIP:          global.Self.PodIP,
		Node:           global.Self.NodeName,
		Namespace:      global.Self.Namespace,
		Cluster:        global.Self.Cluster,
		InboundArgs:    inboundArgs,
		InboundHeaders: inboundHeaders,
		Metadata:       metadata,
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

func (c *GotoClientInfo) StoreHeaders(request http.Header) {
	c.OutboundRequestHeaders = request
}
