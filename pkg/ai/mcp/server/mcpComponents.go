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
	URI      string           `json:"uri,omitempty"`
	Response *payload.Payload `json:"response,omitempty"`
	IsProxy  bool             `json:"proxy,omitempty"`
	Kind     string           `json:"-"`
	Server   *MCPServer       `json:"-"`
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
