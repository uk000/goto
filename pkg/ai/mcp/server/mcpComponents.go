package mcpserver

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
	SetPayload(b []byte, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int)
}

type MCPComponent struct {
	Kind     string           `json:"-"`
	Server   *MCPServer       `json:"-"`
	Response *payload.Payload `json:"response,omitempty"`
	IsProxy  bool             `json:"proxy,omitempty"`
	Name     string           `json:"-"`
	Label    string           `json:"-"`
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
	m.Label = fmt.Sprintf("[%s/%s/%s]", m.Server.GetName(), m.Kind, m.Name)
}

func (m *MCPComponent) SetPayload(b []byte, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int) {
	if isStream {
		if isJSON {
			m.Response = payload.NewStreamJSONPayload(nil, b, streamCount, delayMin, delayMax, delayCount)
		} else {
			m.Response = payload.NewStreamTextPayload(nil, b, streamCount, delayMin, delayMax, delayCount)
		}
	} else if isJSON {
		m.Response = payload.NewJSONPayload(nil, b, delayMin, delayMax, delayCount)
	} else {
		m.Response = payload.NewRawPayload(b, "", delayMin, delayMax, delayCount)
	}
	if m.Response == nil {
		m.Response = payload.NewJSONPayload(util.JSONFromMap(map[string]any{"data": string(b)}), nil, delayMin, delayMax, delayCount)
	}
}
