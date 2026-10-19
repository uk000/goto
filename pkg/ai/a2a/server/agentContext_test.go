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

package a2aserver

import (
	"context"
	"testing"
	"time"

	"goto/pkg/types"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func textInput(text string, data map[string]any) *a2aproto.Message {
	parts := []a2aproto.Part{&a2aproto.TextPart{Text: text}}
	if data != nil {
		parts = append(parts, &a2aproto.DataPart{Data: data})
	}
	m := a2aproto.NewMessage(a2aproto.MessageRoleUser, parts)
	return &m
}

func TestLoadInputsText(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("inputs", false, nil, nil)
	ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
	ac.loadInputs(textInput("first part", nil))
	if ac.inputText != "first part" || len(ac.inputData) != 0 {
		t.Errorf("input = %q, data = %v", ac.inputText, ac.inputData)
	}
	// Parts that are not text or data, and data that is not a map, are not inputs.
	m := a2aproto.NewMessage(a2aproto.MessageRoleUser, []a2aproto.Part{
		&a2aproto.TextPart{Text: "one "}, &a2aproto.DataPart{Data: "not a map"}, &a2aproto.DataPart{}, &a2aproto.TextPart{Text: "two"},
	})
	ac.loadInputs(&m)
	if ac.inputText != "one two" || len(ac.inputData) != 0 {
		t.Errorf("input = %q, data = %v", ac.inputText, ac.inputData)
	}
}

func TestLoadInputsDataConfiguresFeatures(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("datainputs", false, nil, nil)
	ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
	ac.timeline.ResultOnly = false
	ac.loadInputs(textInput("go", map[string]any{
		"timeline": true,
		"count":    3,
		"options":  map[string]any{"noEvents": true, "resultOnly": true},
	}))
	if ac.inputData["count"] != 3 || ac.inputData["options"] == nil || ac.inputData["timeline"] != true {
		t.Errorf("input data = %v, want all of the keys of the data", ac.inputData)
	}
	if !ac.reportTimeline {
		t.Errorf("reportTimeline = false, want the timeline asked for by the data")
	}
	if !ac.timeline.NoEvents || !ac.timeline.ResultOnly {
		t.Errorf("noEvents = %v, resultOnly = %v, want the ones set in the nested data", ac.timeline.NoEvents, ac.timeline.ResultOnly)
	}
}

func TestDetectHints(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("hints", false, nil, nil)
	tests := []struct {
		name         string
		text         string
		status       int
		delay        time.Duration
		reportTL     bool
		noEvents     bool
		resultOnly   bool
		surfaceDatas bool
	}{
		{"nothing", "just text", 0, 0, false, false, false, true},
		{"status", "fail with status 503 please", 503, 0, false, false, false, true},
		{"delay", "wait 2s now", 0, 2 * time.Second, false, false, false, true},
		{"timeline", "run and show timeline", 0, 0, true, false, false, true},
		{"events off", "run with no events", 0, 0, false, true, false, true},
		{"input hint", "run timeline:[true]", 0, 0, true, false, false, true},
		{"hide data", "run hide data", 0, 0, false, false, false, false},
	}
	for _, tt := range tests {
		ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
		ac.inputText = tt.text
		ac.detectHints()
		if ac.forcedStatus != tt.status || ac.overrideDelay != tt.delay || ac.reportTimeline != tt.reportTL ||
			ac.timeline.NoEvents != tt.noEvents || ac.timeline.ResultOnly != tt.resultOnly || ac.surfaceData != tt.surfaceDatas {
			t.Errorf("%s: status %d, delay %s, timeline %v, noEvents %v, resultOnly %v, surfaceData %v, want %d, %s, %v, %v, %v, %v", tt.name,
				ac.forcedStatus, ac.overrideDelay, ac.reportTimeline, ac.timeline.NoEvents, ac.timeline.ResultOnly, ac.surfaceData,
				tt.status, tt.delay, tt.reportTL, tt.noEvents, tt.resultOnly, tt.surfaceDatas)
		}
	}
}

func TestSendDataWhetherSurfaced(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("senddata", false, nil, nil)
	handler := &stubTaskHandler{}
	ac := newUnitContext(ctxWithTimeout(t), agent, handler)
	if err := ac.sendData("Key", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if handler.artifacts.Load() != 1 || handler.updates.Load() != 0 {
		t.Errorf("artifacts = %d, updates = %d, want the data as an artifact", handler.artifacts.Load(), handler.updates.Load())
	}
	ac.surfaceData = false
	if err := ac.sendData("Key", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if handler.artifacts.Load() != 1 || handler.updates.Load() != 1 {
		t.Errorf("artifacts = %d, updates = %d, want the data as a status update once it is not surfaced", handler.artifacts.Load(), handler.updates.Load())
	}
}

func TestNotifyWithoutTask(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("notify", false, nil, nil)
	ac := newAgentContext(ts.port, "srv", "listener", agent, nil, nil)
	if err := ac.notifyUpdate("msg", nil, false); err == nil {
		t.Errorf("notifyUpdate without a task = nil, want an error")
	}
	ac.notifyEndSession("msg", nil, true)
	ac.task = &AgentTask{}
	if err := ac.notifyUpdate("msg", nil, false); err == nil {
		t.Errorf("notifyUpdate without a handler = nil, want an error")
	}
}

func TestNotifyUpdateText(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("notifytext", false, nil, nil)
	handler := &stubTaskHandler{}
	ac := newUnitContext(ctxWithTimeout(t), agent, handler)
	if err := ac.notifyUpdate("progress", nil, false); err != nil || handler.updates.Load() != 1 {
		t.Errorf("notifyUpdate = %v, updates = %d, want one update", err, handler.updates.Load())
	}
	if err := ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, ""); err != nil || handler.updates.Load() != 1 {
		t.Errorf("an update without a message = %v, updates = %d, want nothing sent", err, handler.updates.Load())
	}
}

func TestWaitBeforeNextStep(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("wait", false, nil, nil)

	handler := &stubTaskHandler{}
	ac := newUnitContext(ctxWithTimeout(t), agent, handler)
	ac.delay = types.NewDelay(5*time.Millisecond, 5*time.Millisecond, 0)
	delay, err := ac.waitBeforeNextStep()
	if err != nil || delay != 5*time.Millisecond {
		t.Errorf("waitBeforeNextStep = %s, %v, want the delay", delay, err)
	}

	handler = &stubTaskHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ac = newUnitContext(ctx, agent, handler)
	ac.delay = types.NewDelay(10*time.Second, 10*time.Second, 0)
	if _, err := ac.waitBeforeNextStep(); err != context.Canceled {
		t.Errorf("waitBeforeNextStep = %v, want the cancellation", err)
	}
	if handler.canceled.Load() != 1 {
		t.Errorf("canceled updates = %d, want the task to be reported as canceled", handler.canceled.Load())
	}
}

func TestRecordUpstreamIsAdded(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("record", false, nil, nil)
	ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
	ac.recordUpstream("A2A", "one", map[string]bool{"gotoA": true}, map[string]any{"H1": "v1"}, nil, nil, map[string]any{"s1": 200}, 200)
	ac.recordUpstream("MCP", "two", map[string]bool{"gotoB": true}, map[string]any{"H2": "v2"}, nil, nil, nil, 503)
	ac.recordUpstream("A2A", "three", nil, nil, nil, nil, nil, 0)

	if len(ac.remoteGotos) != 2 || !ac.remoteGotos["gotoA"] || !ac.remoteGotos["gotoB"] {
		t.Errorf("remote gotos = %v, want both", ac.remoteGotos)
	}
	if ac.upstreamHeaders["H1"] != "v1" || ac.upstreamHeaders["H2"] != "v2" {
		t.Errorf("upstream headers = %v, want the headers of both", ac.upstreamHeaders)
	}
	reported, _ := ac.upstreamHeaders["UpstreamHeaders"].(map[string]any)
	for _, key := range []string{"one (A2A)", "two (MCP)", "three (A2A)"} {
		if reported[key] == nil {
			t.Errorf("reported = %v, want [%s]", reported, key)
		}
	}
	if ac.upstreamStatuses["s1"] != 200 || ac.upstreamStatuses["two"] != 503 {
		t.Errorf("upstream statuses = %v, want the given ones and the status of the call that gave none", ac.upstreamStatuses)
	}
	if ac.forcedStatus != 503 {
		t.Errorf("forced status = %d, want the last status that was positive", ac.forcedStatus)
	}
}

func TestEndTaskReportsUpstreamStatuses(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("endtask", false, nil, nil)
	handler := &stubTaskHandler{}
	ac := newUnitContext(ctxWithTimeout(t), agent, handler)
	ac.task.subscriber = nil
	ac.upstreamStatuses["remote"] = 502
	ac.forcedStatus = 502
	ac.endTask(false, "done")
	if handler.cleaned.Load() != 1 {
		t.Errorf("cleaned tasks = %d, want the task to be cleaned up", handler.cleaned.Load())
	}
	if ac.rs.UpstreamStatuses["remote"] != 502 {
		t.Errorf("upstream statuses of the request = %v, want the ones of the delegates", ac.rs.UpstreamStatuses)
	}
	if ac.timeline.Status != 502 || ac.timeline.UpstreamStatuses["remote"] != 502 || ac.timeline.Success || !ac.timeline.Finished {
		t.Errorf("timeline = %+v, want it finished with the status", ac.timeline)
	}
}
