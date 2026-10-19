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

package aicommon

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"goto/pkg/types"
)

func TestNewCallArgs(t *testing.T) {
	a := NewCallArgs()
	if a.Metadata == nil || len(a.Metadata) != 0 {
		t.Errorf("metadata = %v, want an empty map", a.Metadata)
	}
	if a.RemoteArgs == nil || !a.RemoteArgs.IsEmpty() {
		t.Errorf("remote args = %+v, want empty ones", a.RemoteArgs)
	}
	if a.Delay != nil {
		t.Errorf("delay = %+v, want none for a zero delay", a.Delay)
	}
	if NewCallArgs() == a || NewCallArgs().Metadata == nil {
		t.Error("NewCallArgs shares what it returns")
	}
}

func TestNonNil(t *testing.T) {
	a := &ToolCallArgs{}
	a.NonNil()
	if a.Metadata == nil || a.ToolDef == nil || a.Headers == nil || a.Headers.Request == nil || a.Headers.Response == nil ||
		a.ForwardHeaders == nil || a.AgentArgs == nil || a.RemoteArgs == nil {
		t.Errorf("args = %+v, want everything initialized", a)
	}
	if a.RemoteArgs.Metadata == nil {
		t.Errorf("remote args = %+v, want them initialized", a.RemoteArgs)
	}
	// What is there is kept.
	b := &ToolCallArgs{
		Metadata: map[string]string{"k": "v"}, ToolDef: map[string]any{"d": 1}, Headers: types.NewHeaders(),
		ForwardHeaders: []string{"X"}, AgentArgs: map[string]any{"a": 1}, RemoteArgs: &ToolCallArgs{Text: "remote"},
	}
	b.Headers.Request.Add["H"] = "1"
	b.NonNil()
	if b.Metadata["k"] != "v" || b.ToolDef["d"] != 1 || b.Headers.Request.Add["H"] != "1" || b.ForwardHeaders[0] != "X" ||
		b.AgentArgs["a"] != 1 || b.RemoteArgs.Text != "remote" {
		t.Errorf("args = %+v, want the existing values kept", b)
	}
	b.NonNil()
	if b.Metadata["k"] != "v" {
		t.Error("NonNil is not idempotent")
	}
}

func TestUpdateFrom(t *testing.T) {
	t.Run("scalars", func(t *testing.T) {
		a := &ToolCallArgs{Count: 1, Size: 2, DelayText: "1s", Text: "t", Status: 200, ToolName: "tool", AgentName: "agent",
			URL: "u", Authority: "auth", AgentMessage: "m"}
		a.UpdateFrom(&ToolCallArgs{})
		want := &ToolCallArgs{Count: 1, Size: 2, DelayText: "1s", Text: "t", Status: 200, ToolName: "tool", AgentName: "agent",
			URL: "u", Authority: "auth", AgentMessage: "m", Metadata: map[string]string{}}
		if !reflect.DeepEqual(a, want) {
			t.Errorf("args after an empty update = %+v, want %+v", a, want)
		}
		a.UpdateFrom(&ToolCallArgs{Count: 5, Size: 6, DelayText: "2s", Text: "t2", Status: 500, ToolName: "tool2", AgentName: "agent2",
			URL: "u2", Authority: "auth2", AgentMessage: "m2", SSE: true})
		want = &ToolCallArgs{Count: 5, Size: 6, DelayText: "2s", Text: "t2", Status: 500, ToolName: "tool2", AgentName: "agent2",
			URL: "u2", Authority: "auth2", AgentMessage: "m2", SSE: true, Metadata: map[string]string{}}
		if !reflect.DeepEqual(a, want) {
			t.Errorf("args after the update = %+v, want %+v", a, want)
		}
		// SSE can't be turned off by an update.
		a.UpdateFrom(&ToolCallArgs{SSE: false})
		if !a.SSE {
			t.Error("SSE was turned off by an update that doesn't have it")
		}
	})
	t.Run("nil", func(t *testing.T) {
		a := &ToolCallArgs{Text: "t"}
		a.UpdateFrom(nil)
		if a.Text != "t" || a.Metadata != nil {
			t.Errorf("args = %+v, want them untouched by a nil update", a)
		}
	})
	t.Run("metadata is merged", func(t *testing.T) {
		a := &ToolCallArgs{}
		a.UpdateFrom(&ToolCallArgs{Metadata: map[string]string{"a": "1"}})
		a.UpdateFrom(&ToolCallArgs{Metadata: map[string]string{"b": "2", "a": "3"}})
		if !reflect.DeepEqual(a.Metadata, map[string]string{"a": "3", "b": "2"}) {
			t.Errorf("metadata = %v", a.Metadata)
		}
	})
	t.Run("collections are replaced when given", func(t *testing.T) {
		headers := types.NewHeaders()
		a := &ToolCallArgs{ForwardHeaders: []string{"old"}, AgentArgs: map[string]any{"old": 1}}
		a.UpdateFrom(&ToolCallArgs{Headers: headers, ForwardHeaders: []string{"new"}, AgentArgs: map[string]any{"new": 2}})
		if a.Headers != headers || !reflect.DeepEqual(a.ForwardHeaders, []string{"new"}) || !reflect.DeepEqual(a.AgentArgs, map[string]any{"new": 2}) {
			t.Errorf("args = %+v", a)
		}
		a.UpdateFrom(&ToolCallArgs{})
		if a.Headers != headers || len(a.ForwardHeaders) != 1 || len(a.AgentArgs) != 1 {
			t.Errorf("args = %+v, want an update without them to keep them", a)
		}
	})
	t.Run("remote args", func(t *testing.T) {
		a := &ToolCallArgs{}
		a.UpdateFrom(&ToolCallArgs{RemoteArgs: &ToolCallArgs{Text: "remote", Count: 3, RemoteArgs: &ToolCallArgs{Text: "deeper"}}})
		if a.RemoteArgs == nil || a.RemoteArgs.Text != "remote" || a.RemoteArgs.Count != 3 || a.RemoteArgs.RemoteArgs == nil || a.RemoteArgs.RemoteArgs.Text != "deeper" {
			t.Errorf("remote args = %+v", a.RemoteArgs)
		}
		a.UpdateFrom(&ToolCallArgs{RemoteArgs: &ToolCallArgs{Text: "again"}})
		if a.RemoteArgs.Text != "again" || a.RemoteArgs.Count != 3 {
			t.Errorf("remote args = %+v, want a merge", a.RemoteArgs)
		}
	})
}

func TestUpdateFromInputArgs(t *testing.T) {
	a := NewCallArgs()
	a.UpdateFromInputArgs(map[string]any{
		"count":          float64(3),
		"delay":          "1s-2s",
		"text":           "t",
		"status":         "503",
		"tool":           "tool",
		"agent":          "agent",
		"url":            "http://u",
		"authority":      "auth",
		"sse":            "true",
		"headers":        map[string]string{"X-A": "a"},
		"forwardHeaders": []string{"X-F"},
		"message":        "msg",
		"agentArgs":      map[string]string{"k": "v"},
		"args":           map[string]any{"text": "remote-text", "count": 9},
	})
	if a.Count != 3 || a.DelayText != "1s-2s" || a.Text != "t" || a.Status != 503 || a.ToolName != "tool" || a.AgentName != "agent" ||
		a.URL != "http://u" || a.Authority != "auth" || !a.SSE || a.AgentMessage != "msg" {
		t.Errorf("args = %+v", a)
	}
	if a.Headers == nil || a.Headers.Request.Add["X-A"] != "a" || !reflect.DeepEqual(a.Headers.Request.Forward, []string{"X-F"}) {
		t.Errorf("headers = %+v", a.Headers)
	}
	if a.AgentArgs["k"] != "v" {
		t.Errorf("agent args = %+v", a.AgentArgs)
	}
	if a.RemoteArgs.Text != "remote-text" || a.RemoteArgs.Count != 9 {
		t.Errorf("remote args = %+v", a.RemoteArgs)
	}
}

func TestUpdateFromInputArgsMessageAliasesAndRemoteArgs(t *testing.T) {
	for _, key := range []string{"message", "prompt", "input"} {
		a := NewCallArgs()
		a.UpdateFromInputArgs(map[string]any{key: "from-" + key})
		if a.AgentMessage != "from-"+key {
			t.Errorf("%s: message = %q", key, a.AgentMessage)
		}
	}
	a := NewCallArgs()
	a.UpdateFromInputArgs(map[string]any{"remoteArgs": map[string]any{"tool": "remote-tool"}})
	if a.RemoteArgs.ToolName != "remote-tool" {
		t.Errorf("remote args = %+v, want the ones given as remoteArgs", a.RemoteArgs)
	}
	a = &ToolCallArgs{}
	a.UpdateFromInputArgs(map[string]any{"args": map[string]any{"tool": "t"}, "remoteArgs": map[string]any{"tool": "ignored"}})
	if a.RemoteArgs == nil || a.RemoteArgs.ToolName != "t" {
		t.Errorf("remote args = %+v, want args to win over remoteArgs", a.RemoteArgs)
	}
	// Remote args that are not a map are ignored.
	a = NewCallArgs()
	a.UpdateFromInputArgs(map[string]any{"args": "not a map"})
	if !a.RemoteArgs.IsEmpty() {
		t.Errorf("remote args = %+v, want none", a.RemoteArgs)
	}
	// Nothing to update from.
	a = &ToolCallArgs{Text: "kept"}
	a.UpdateFromInputArgs(nil)
	a.UpdateFromInputArgs(map[string]any{})
	if a.Text != "kept" {
		t.Errorf("args = %+v", a)
	}
}

func TestUpdateFromInputArgsCreatesWhatItNeeds(t *testing.T) {
	a := &ToolCallArgs{}
	a.UpdateFromInputArgs(map[string]any{
		"headers": map[string]string{"X": "1"}, "forwardHeaders": []string{"Y"}, "agentArgs": map[string]string{"k": "v"},
	})
	if a.Headers == nil || a.Headers.Request.Add["X"] != "1" || a.Headers.Request.Forward[0] != "Y" || a.AgentArgs["k"] != "v" {
		t.Errorf("args = %+v", a)
	}
}

func TestUpdateDelay(t *testing.T) {
	configured := types.NewDelay(time.Second, 2*time.Second, 1)
	a := &ToolCallArgs{}
	a.UpdateDelay(configured)
	if a.Delay != configured {
		t.Errorf("delay = %+v, want the one that was given", a.Delay)
	}
	a.DelayText = "10ms-20ms"
	a.UpdateDelay(configured)
	if a.Delay == configured || a.Delay == nil || a.Delay.Min.Duration != 10*time.Millisecond || a.Delay.Max.Duration != 20*time.Millisecond {
		t.Errorf("delay = %+v, want the one of the text", a.Delay)
	}
	a.DelayText = "not a delay"
	a.UpdateDelay(configured)
	if a.Delay != configured {
		t.Errorf("delay = %+v, want the given one for a text that doesn't parse", a.Delay)
	}
	a.DelayText = ""
	a.UpdateDelay(nil)
	if a.Delay != nil {
		t.Errorf("delay = %+v, want none", a.Delay)
	}
}

func TestAddMetadata(t *testing.T) {
	a := &ToolCallArgs{}
	a.AddMetadata("a", "1")
	a.AddMetadata("b", "2")
	a.AddMetadata("a", "3")
	if !reflect.DeepEqual(a.Metadata, map[string]string{"a": "3", "b": "2"}) {
		t.Errorf("metadata = %v", a.Metadata)
	}
}

func TestClone(t *testing.T) {
	headers := types.NewHeaders()
	headers.Request.Add["X-Req"] = "1"
	headers.Request.Forward = []string{"X-F"}
	headers.Response.Add["X-Resp"] = "2"
	a := &ToolCallArgs{
		DelayText: "1s", Count: 1, Size: 2, Text: "t", Status: 200, ResultOnly: true, NoEvents: true,
		Metadata: map[string]string{"m": "1"}, ToolDef: map[string]any{"d": 1}, ToolName: "tool", AgentName: "agent",
		URL: "u", Authority: "a", SSE: true, Headers: headers, ForwardHeaders: []string{"F"}, AgentMessage: "msg",
		AgentArgs: map[string]any{"k": "v"}, RemoteArgs: &ToolCallArgs{Text: "remote", Metadata: map[string]string{"r": "1"}},
	}
	c := a.Clone()
	if c == a {
		t.Fatal("Clone returned the same args")
	}
	if !reflect.DeepEqual(c, a) {
		t.Errorf("clone = %+v, want it equal to %+v", c, a)
	}
	c.Metadata["new"] = "2"
	c.ToolDef["new"] = 2
	c.AgentArgs["new"] = 2
	c.ForwardHeaders[0] = "changed"
	c.Headers.Request.Add["new"] = "x"
	c.Headers.Request.Forward[0] = "changed"
	c.Headers.Response.Add["new"] = "x"
	c.RemoteArgs.Text = "changed"
	c.RemoteArgs.Metadata["new"] = "2"
	if len(a.Metadata) != 1 || len(a.ToolDef) != 1 || len(a.AgentArgs) != 1 || a.ForwardHeaders[0] != "F" ||
		len(a.Headers.Request.Add) != 1 || a.Headers.Request.Forward[0] != "X-F" || len(a.Headers.Response.Add) != 1 ||
		a.RemoteArgs.Text != "remote" || len(a.RemoteArgs.Metadata) != 1 {
		t.Errorf("the original changed with its clone: %+v, remote %+v, headers %+v", a, a.RemoteArgs, a.Headers)
	}
}

func TestCloneOfSparseArgs(t *testing.T) {
	a := &ToolCallArgs{Text: "t"}
	c := a.Clone()
	if !reflect.DeepEqual(a, c) {
		t.Errorf("clone = %+v, want %+v: nil parts have to stay nil", c, a)
	}
	c.AddMetadata("k", "v")
	if a.Metadata != nil {
		t.Errorf("metadata = %v, want the original to be left alone", a.Metadata)
	}
	var none *ToolCallArgs
	noPanic(t, "Clone of nil args", func() {
		if none.Clone() != nil {
			t.Error("the clone of nil args is not nil")
		}
	})
}

func TestIsEmpty(t *testing.T) {
	if !(&ToolCallArgs{}).IsEmpty() {
		t.Error("zero args are not empty")
	}
	if !NewCallArgs().IsEmpty() {
		t.Error("new args are not empty")
	}
	for name, a := range map[string]*ToolCallArgs{
		"text":            {Text: "t"},
		"delay text":      {DelayText: "1s"},
		"count":           {Count: 1},
		"delay":           {Delay: types.NewDelay(time.Second, time.Second, 1)},
		"tool":            {ToolName: "t"},
		"agent":           {AgentName: "a"},
		"url":             {URL: "u"},
		"authority":       {Authority: "a"},
		"headers":         {Headers: types.NewHeaders()},
		"forward headers": {ForwardHeaders: []string{"X"}},
		"message":         {AgentMessage: "m"},
		"metadata":        {Metadata: map[string]string{"k": "v"}},
		"agent args":      {AgentArgs: map[string]any{"k": "v"}},
		"remote args":     {RemoteArgs: &ToolCallArgs{Text: "t"}},
	} {
		if a.IsEmpty() {
			t.Errorf("args with %s are empty", name)
		}
	}
}

func TestToolCallArgsJSON(t *testing.T) {
	a := &ToolCallArgs{
		DelayText: "1s", Count: 2, Size: 3, Text: "t", Status: 201, ResultOnly: true, NoEvents: true,
		Metadata: map[string]string{"m": "1"}, ToolName: "tool", AgentName: "agent", URL: "u", Authority: "a", SSE: true,
		ForwardHeaders: []string{"F"}, AgentMessage: "msg", AgentArgs: map[string]any{"k": "v"},
		RemoteArgs: &ToolCallArgs{Text: "remote"}, Delay: types.NewDelay(time.Second, time.Second, 1),
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"delay", "count", "size", "text", "status", "resultOnly", "noEvents", "metadata", "tool", "agent", "url",
		"authority", "sse", "forwardHeaders", "agentMessage", "agentArgs", "remoteArgs"} {
		if _, found := fields[k]; !found {
			t.Errorf("JSON %s lacks the field %q", b, k)
		}
	}
	if fields["delay"] != "1s" {
		t.Errorf("delay = %v, want the delay text, the computed delay is not serialized", fields["delay"])
	}
	back := &ToolCallArgs{}
	if err := json.Unmarshal(b, back); err != nil {
		t.Fatal(err)
	}
	a.Delay = nil
	if !reflect.DeepEqual(back, a) {
		t.Errorf("args from JSON = %+v, want %+v", back, a)
	}
	empty, _ := json.Marshal(&ToolCallArgs{})
	if string(empty) != "{}" {
		t.Errorf("JSON of empty args = %s, want {}", empty)
	}
}
