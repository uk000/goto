package mcp

import (
	"fmt"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
	"time"
)

type IMCPComponent interface {
	GetName() string
	GetKind() string
	SetName(string)
	BuildLabel()
	SetPayload(b []byte, url string, isRemote, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int)
}

type IMCPServer interface {
	GetID() string
	GetHost() string
	GetName() string
	GetPort() int
}

type MCPBehavior struct {
	Ping      bool `json:"ping,omitempty"`
	Echo      bool `json:"echo,omitempty"`
	Time      bool `json:"time,omitempty"`
	Elicit    bool `json:"elicit,omitempty"`
	Sample    bool `json:"sample,omitempty"`
	ListRoots bool `json:"listRoots,omitempty"`
}

type MCPComponent struct {
	Kind         string           `json:"-"`
	Server       IMCPServer       `json:"-"`
	Payload      *payload.Payload `json:"payload,omitempty"`
	Behavior     MCPBehavior      `json:"behavior,omitempty"`
	IsRemote     bool             `json:"remote,omitempty"`
	IsProxy      bool             `json:"proxy,omitempty"`
	IsFetch      bool             `json:"fetch,omitempty"`
	RemoteTool   string           `json:"remoteTool,omitempty"`
	RemoteURL    string           `json:"remoteURL,omitempty"`
	RemoteSSEURL string           `json:"remoteSSEURL,omitempty"`
	RemoteServer string           `json:"remoteServer,omitempty"`
	Name         string           `json:"-"`
	Label        string           `json:"-"`
}

type MCPCallLog struct {
}

func (m *MCPComponent) GetName() string {
	return m.Name
}

func (m *MCPComponent) GetKind() string {
	return m.Kind
}

func (m *MCPComponent) SetName(name string) {
	m.Name = name
	m.BuildLabel()
}

func (m *MCPComponent) BuildLabel() {
	m.Label = fmt.Sprintf("[%s/%s.%s]", m.Server.GetName(), m.Kind, m.Name)
}

func (m *MCPComponent) SetPayload(b []byte, url string, isRemote, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int) {
	if isRemote {
		m.IsRemote = true
		m.RemoteURL = url
		return
	}
	if isStream {
		if isJSON {
			m.Payload = payload.NewStreamJSONPayload(nil, b, streamCount, delayMin, delayMax, delayCount)
		} else {
			m.Payload = payload.NewStreamTextPayload(nil, b, streamCount, delayMin, delayMax, delayCount)
		}
	} else if isJSON {
		m.Payload = payload.NewJSONPayload(nil, b, delayMin, delayMax, delayCount)
	} else {
		m.Payload = payload.NewRawPayload(b, "", delayMin, delayMax, delayCount)
	}
	if m.Payload == nil {
		m.Payload = payload.NewJSONPayload(util.JSONFromMap(map[string]any{"data": string(b)}), nil, delayMin, delayMax, delayCount)
	}
}
