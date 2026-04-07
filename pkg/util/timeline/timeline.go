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
	aicommon "goto/pkg/ai/common"
	"goto/pkg/global"
	"goto/pkg/types"
	"net/http"
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
	RemoteTarget    string
	RemoteURL       string
	RemoteLabel     string
	OutboundArgs    any
	OutboundHeaders any
	HeadersConfig   *types.Headers
	RequestCount    int
	Concurrent      int
}

type GotoClientInfo struct {
	*InstanceInfo
	*RemoteInfo
}

type GotoServerInfo struct {
	ServerInfo *InstanceInfo
}

type Event struct {
	Idx        int `json:"Index"`
	Label      string
	Text       string
	At         time.Time
	Client     *GotoClientInfo
	Server     *GotoServerInfo
	RemoteData any
}

type Timeline struct {
	TYPE            string `json:"TYPE,omitempty"`
	Port            int
	Label           string
	Server          *GotoServerInfo
	Events          []*Event
	Data            map[string]any
	Finished        bool
	Success         bool
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
		Finished:       false,
		stream:         stream,
		updateNotifier: updateNotifier,
		endNotifier:    endNotifier,
		eventCounter:   atomic.Int32{},
	}
	return t
}

func (t *Timeline) SetStreamPreferred(stream chan *types.Pair[string, any]) {
	t.stream = stream
	t.streamPreferred = true
}

func (t *Timeline) StartTimeline(label, text string, server *GotoServerInfo) {
	event := t.NewEvent(label, text, nil, server, nil)
	t.send(text, nil, false, false)
	t.send("ServerInfo", server, true, false)
	t.lock.Lock()
	defer t.lock.Unlock()
	t.Events = append(t.Events, event)
}

func (t *Timeline) EndTimeline(label, text string, data any, success bool) {
	t.Finished = true
	t.Success = success
	if data != nil {
		t.AddData(text, data, true)
	} else {
		t.AddEvent(label, text, nil, nil, false)
	}
}

func (t *Timeline) AddEvent(label, text string, client *GotoClientInfo, remote any, json bool) {
	event := t.NewEvent(label, text, client, nil, remote)
	t.send(text, nil, false, t.Finished)
	if client != nil {
		t.send("ClientInfo", client, true, t.Finished)
	}
	if remote != nil {
		t.send(label, remote, json, t.Finished)
	}
	t.lock.Lock()
	defer t.lock.Unlock()
	t.Events = append(t.Events, event)
}

func (t *Timeline) AddData(key string, data any, json bool) {
	t.send(key, data, json, false)
	t.lock.Lock()
	defer t.lock.Unlock()
	t.Data[key] = data
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

func (t *Timeline) NewEvent(label, text string, client *GotoClientInfo, server *GotoServerInfo, remote any) *Event {
	return &Event{
		Label:      label,
		Text:       text,
		Idx:        int(t.eventCounter.Add(1)),
		At:         time.Now(),
		Client:     client,
		Server:     server,
		RemoteData: remote,
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
		RemoteTarget:    target,
		RemoteURL:       url,
		RemoteLabel:     server,
		OutboundArgs:    outArgs,
		OutboundHeaders: outHeaders,
		HeadersConfig:   &types.Headers{},
		RequestCount:    requestCount,
		Concurrent:      concurrent,
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
