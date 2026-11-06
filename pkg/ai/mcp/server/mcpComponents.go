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

package mcpserver

import (
	"encoding/json"
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
	URI       string           `json:"uri,omitempty"`
	ServerURI string           `json:"serverURI,omitempty"`
	Response  *payload.Payload `json:"response,omitempty"`
	IsProxy   bool             `json:"proxy,omitempty"`
	Kind      string           `json:"-"`
	Server    *MCPServer       `json:"-"`
	Name      string           `json:"-"`
	Label     string           `json:"-"`
}

type MCPCallLog struct {
}

// GetName returns the name of the component.
func (m *MCPComponent) GetName() string {
	return m.Name
}

// GetKind returns the kind of the component (tools, prompts, resources or templates).
func (m *MCPComponent) GetKind() string {
	return m.Kind
}

// SetName sets the name of the component and rebuilds its label.
func (m *MCPComponent) SetName(name string) {
	m.Name = name
	m.BuildLabel()
}

// BuildLabel builds the label of the component, which has the ID of its server. A component that isn't added to a server
// yet doesn't have one.
func (m *MCPComponent) BuildLabel() {
	serverID := ""
	if m.Server != nil {
		serverID = m.Server.ID
	}
	m.Label = fmt.Sprintf("%s/%s/%s", serverID, m.Kind, m.Name)
}

// SetPayload sets what the component answers with. A body that is meant to be JSON but isn't is not lost: it is kept as
// the "data" of a JSON payload, as the text that it is.
func (m *MCPComponent) SetPayload(b []byte, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int) {
	if isJSON && len(b) > 0 && !json.Valid(b) {
		wrapped := util.JSONFromMap(map[string]any{"data": string(b)})
		if isStream {
			m.Response = payload.NewStreamJSONPayload([]util.JSON{wrapped}, nil, streamCount, delayMin, delayMax, delayCount)
		} else {
			m.Response = payload.NewJSONPayload(wrapped, nil, delayMin, delayMax, delayCount)
		}
		return
	}
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
}
