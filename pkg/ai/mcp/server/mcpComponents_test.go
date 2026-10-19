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
	"testing"
	"time"
)

func TestComponentIsNamedAndLabeledForItsServer(t *testing.T) {
	port := isolate(t)
	s := NewMCPServer(newPayload(port, "comp-a"))
	c := &MCPComponent{Server: s, Kind: KindTools}
	c.SetName("thing")
	if c.GetName() != "thing" || c.GetKind() != KindTools {
		t.Errorf("name = %q, kind = %q", c.GetName(), c.GetKind())
	}
	if want := s.ID + "/tools/thing"; c.Label != want {
		t.Errorf("label = %q, want %q", c.Label, want)
	}
	c.SetName("renamed")
	if want := s.ID + "/tools/renamed"; c.Label != want {
		t.Errorf("label after renaming = %q, want %q", c.Label, want)
	}
}

func TestComponentPayloadIsMadeOfTheKindThatIsAskedFor(t *testing.T) {
	for name, tt := range map[string]struct {
		body             string
		isJSON, isStream bool
		count            int
	}{
		"raw text":        {"some text", false, false, 0},
		"JSON":            {`{"a":1}`, true, false, 0},
		"text stream":     {"chunk", false, true, 3},
		"JSON stream":     {`{"a":1}`, true, true, 3},
		"JSON that isn't": {"not JSON at all", true, false, 0},
	} {
		c := &MCPComponent{}
		c.SetPayload([]byte(tt.body), tt.isJSON, tt.isStream, tt.count, time.Millisecond, 2*time.Millisecond, 2)
		if c.Response == nil {
			t.Errorf("%s: there is no payload", name)
			continue
		}
		if tt.isStream && c.Response.StreamCount != tt.count {
			t.Errorf("%s: stream count = %d, want %d", name, c.Response.StreamCount, tt.count)
		}
	}
}

func TestComponentPayloadThatIsNotJSONIsKeptAsData(t *testing.T) {
	c := &MCPComponent{}
	c.SetPayload([]byte("not JSON at all"), true, false, 0, 0, 0, 0)
	if c.Response == nil || c.Response.JSON == nil {
		t.Fatalf("payload = %+v, want one that has the text as JSON data", c.Response)
	}
	expectContains(t, c.Response.JSON.ToJSONText(), "not JSON at all")
}

func TestGetComponentTypeTellsTheKind(t *testing.T) {
	for kind, want := range map[string][4]bool{
		KindTools:     {true, false, false, false},
		KindPrompts:   {false, true, false, false},
		KindResources: {false, false, true, false},
		KindTemplates: {false, false, false, true},
		"other":       {false, false, false, false},
		"":            {false, false, false, false},
	} {
		a, b, c, d := GetComponentType(kind)
		if got := [4]bool{a, b, c, d}; got != want {
			t.Errorf("GetComponentType(%q) = %v, want %v", kind, got, want)
		}
	}
	// The API has its own names for the kinds, which are the plural ones of the same four.
	for kind, want := range map[string][4]bool{
		"tools": {true, false, false, false}, "prompts": {false, true, false, false},
		"resources": {false, false, true, false}, "templates": {false, false, false, true}, "x": {},
	} {
		a, b, c, d := getComponent(kind)
		if got := [4]bool{a, b, c, d}; got != want {
			t.Errorf("getComponent(%q) = %v, want %v", kind, got, want)
		}
	}
}
