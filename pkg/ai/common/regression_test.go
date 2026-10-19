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
	"sync"
	"testing"
	"time"

	"goto/pkg/types"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func noPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	fn()
}

// waitWithin waits for the goroutines of the group, and fails the test when they don't finish in time, which is how a
// deadlock shows up.
func waitWithin(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("the goroutines did not finish within %s, they are deadlocked", d)
	}
}

// Clone copied the struct only, so that the clone and the original shared their maps, slices and nested args, and an
// update of the clone changed the original.
func TestRegressionCloneDoesNotShareState(t *testing.T) {
	headers := types.NewHeaders()
	headers.Request.Add["X"] = "1"
	a := &ToolCallArgs{
		Metadata: map[string]string{"m": "1"}, ToolDef: map[string]any{"d": 1}, AgentArgs: map[string]any{"k": "v"},
		ForwardHeaders: []string{"F"}, Headers: headers, RemoteArgs: &ToolCallArgs{Metadata: map[string]string{"r": "1"}},
	}
	c := a.Clone()
	c.AddMetadata("new", "2")
	c.ToolDef["new"] = 2
	c.AgentArgs["new"] = 2
	c.ForwardHeaders[0] = "changed"
	c.Headers.Request.Add["new"] = "2"
	c.RemoteArgs.AddMetadata("new", "2")
	c.UpdateFrom(&ToolCallArgs{Metadata: map[string]string{"merged": "3"}})
	want := &ToolCallArgs{
		Metadata: map[string]string{"m": "1"}, ToolDef: map[string]any{"d": 1}, AgentArgs: map[string]any{"k": "v"},
		ForwardHeaders: []string{"F"}, Headers: headers, RemoteArgs: &ToolCallArgs{Metadata: map[string]string{"r": "1"}},
	}
	if !reflect.DeepEqual(a, want) {
		t.Errorf("original = %+v (remote %+v), want it untouched", a, a.RemoteArgs)
	}
}

// Clones are made to be used by concurrent requests, which add their own metadata.
func TestRegressionConcurrentClonesAreIndependent(t *testing.T) {
	a := &ToolCallArgs{Metadata: map[string]string{"m": "1"}}
	const workers = 16
	clones := make([]*ToolCallArgs, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := a.Clone()
			c.AddMetadata("worker", string(rune('a'+i)))
			clones[i] = c
		}(i)
	}
	waitWithin(t, &wg, 30*time.Second)
	for i, c := range clones {
		if len(c.Metadata) != 2 || c.Metadata["worker"] != string(rune('a'+i)) {
			t.Errorf("clone %d metadata = %v", i, c.Metadata)
		}
	}
	if len(a.Metadata) != 1 {
		t.Errorf("original metadata = %v", a.Metadata)
	}
}

// The args that an LLM or a JSON request gives are decoded to map[string]any and []any, and used to be asserted to
// map[string]string and []string, which panicked.
func TestRegressionUpdateFromInputArgsWithDecodedJSON(t *testing.T) {
	payload := `{
		"count": 4, "delay": "1s", "text": "t", "status": 202, "tool": "tool", "agent": "agent", "url": "http://u",
		"authority": "auth", "sse": true, "message": "m",
		"headers": {"X-A": "a", "X-N": 5},
		"forwardHeaders": ["X-F1", "X-F2"],
		"agentArgs": {"k": "v", "n": 2, "nested": {"a": 1}},
		"args": {"text": "remote", "headers": {"X-R": "r"}, "forwardHeaders": ["X-RF"], "agentArgs": {"rk": "rv"}}
	}`
	input := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatal(err)
	}
	a := NewCallArgs()
	noPanic(t, "UpdateFromInputArgs", func() { a.UpdateFromInputArgs(input) })
	if a.Count != 4 || a.Status != 202 || !a.SSE || a.Text != "t" || a.DelayText != "1s" {
		t.Errorf("args = %+v", a)
	}
	if a.Headers == nil || a.Headers.Request.Add["X-A"] != "a" || a.Headers.Request.Add["X-N"] != "5" {
		t.Errorf("headers = %+v, want the given ones", a.Headers)
	}
	if a.Headers == nil || !reflect.DeepEqual(a.Headers.Request.Forward, []string{"X-F1", "X-F2"}) {
		t.Errorf("forward headers = %+v", a.Headers)
	}
	if a.AgentArgs["k"] != "v" || a.AgentArgs["n"] != float64(2) || a.AgentArgs["nested"] == nil {
		t.Errorf("agent args = %+v", a.AgentArgs)
	}
	r := a.RemoteArgs
	if r.Text != "remote" || r.Headers.Request.Add["X-R"] != "r" || r.Headers.Request.Forward[0] != "X-RF" || r.AgentArgs["rk"] != "rv" {
		t.Errorf("remote args = %+v, headers %+v", r, r.Headers)
	}
}

// Values of another type than the ones that are expected are left out, instead of ending the process.
func TestRegressionUpdateFromInputArgsWithUnexpectedTypes(t *testing.T) {
	a := NewCallArgs()
	a.Text = "kept"
	noPanic(t, "UpdateFromInputArgs", func() {
		a.UpdateFromInputArgs(map[string]any{
			"text": 5, "delay": 1.5, "tool": []string{"x"}, "agent": true, "url": 1, "authority": map[string]any{},
			"message": 3, "prompt": 4, "input": 5, "headers": "not a map", "forwardHeaders": "not a list", "agentArgs": []any{1},
		})
	})
	if a.Text != "kept" {
		t.Errorf("text = %q, want it kept for a value that is not text", a.Text)
	}
}

// Headers of an args that had a headers value without request config used to be written through a nil config.
func TestRegressionUpdateFromInputArgsWithPartialHeaders(t *testing.T) {
	a := &ToolCallArgs{Headers: &types.Headers{}}
	noPanic(t, "UpdateFromInputArgs", func() {
		a.UpdateFromInputArgs(map[string]any{"headers": map[string]string{"X": "1"}, "forwardHeaders": []string{"F"}})
	})
	if a.Headers.Request == nil || a.Headers.Request.Add["X"] != "1" || a.Headers.Request.Forward[0] != "F" {
		t.Errorf("headers = %+v", a.Headers)
	}
}

// Messages that are built in the code have text parts that are values, and the ones that are decoded from JSON have
// pointers. The text of the first kind used to be lost.
func TestRegressionGetInputTextFromMessageWithValueParts(t *testing.T) {
	m := a2aproto.NewMessage(a2aproto.MessageRoleUser, []a2aproto.Part{
		a2aproto.NewTextPart("built "), a2aproto.NewTextPart("in code"), a2aproto.NewDataPart(map[string]any{"k": "v"}),
	})
	if got := GetInputTextFromMessage(&m); got != "built in code" {
		t.Errorf("text = %q, want the text of the value parts", got)
	}
	mixed := &a2aproto.Message{Parts: []a2aproto.Part{a2aproto.NewTextPart("a"), &a2aproto.TextPart{Text: "b"}}}
	if got := GetInputTextFromMessage(mixed); got != "ab" {
		t.Errorf("text = %q, want ab", got)
	}
}

func TestRegressionGetInputTextFromNilMessage(t *testing.T) {
	noPanic(t, "GetInputTextFromMessage", func() {
		if got := GetInputTextFromMessage(nil); got != "" {
			t.Errorf("text = %q, want none", got)
		}
	})
}
