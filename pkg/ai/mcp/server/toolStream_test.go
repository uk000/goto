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
	"strings"
	"testing"
	"time"
)

func TestStreamToolSendsTheTextAsManyTimesAsCounted(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("streamer", `{"stream":true}`, ""))
	res := invokeTool(t, cs, "streamer", map[string]any{"text": "chunk", "count": 3, "delay": "1ms"})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	lines := strings.Split(strings.TrimSpace(resultText(res)), "\n")
	if len(lines) != 3 {
		t.Fatalf("content = %q, want 3 chunks", lines)
	}
	for i, line := range lines {
		if want := "[" + string(rune('1'+i)) + "] chunk"; line != want {
			t.Errorf("chunk %d = %q, want %q", i, line, want)
		}
	}
	timeline := compactJSON(t, toJSON(t, res.StructuredContent))
	expectContains(t, timeline, "Will stream [3] responses", "Stream finished", "Progress: [1] done, only [2] more to go")
}

func TestStreamToolGeneratesTextOfTheSizeWhenThereIsNone(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("streamer", `{"stream":true}`, ""))
	res := invokeTool(t, cs, "streamer", map[string]any{"size": 7, "count": 2, "delay": "1ms"})
	lines := strings.Split(strings.TrimSpace(resultText(res)), "\n")
	if len(lines) != 2 {
		t.Fatalf("content = %q, want 2 chunks", lines)
	}
	for i, line := range lines {
		prefix := "[" + string(rune('1'+i)) + "] "
		if !strings.HasPrefix(line, prefix) || len(line) != len(prefix)+7 {
			t.Errorf("chunk %d = %q, want text of 7 characters after %q", i, line, prefix)
		}
	}
}

func TestStreamToolTakesTheCountOfTheToolConfig(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("streamer", `{"stream":true}`, `{"streamCount":2}`))
	res := invokeTool(t, cs, "streamer", map[string]any{"text": "cfg", "delay": "1ms"})
	if lines := strings.Split(strings.TrimSpace(resultText(res)), "\n"); len(lines) != 2 {
		t.Errorf("content = %q, want 2 chunks", lines)
	}
}

func TestToolWithoutABehaviorStreams(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("plain", `{}`, ""))
	res := invokeTool(t, cs, "plain", map[string]any{"text": "plain text", "count": 1, "delay": "1ms"})
	expectContains(t, resultText(res), "[1] plain text")
}

func TestStreamToolDelaysBetweenTheChunks(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("streamer", `{"stream":true}`, ""))
	start := time.Now()
	invokeTool(t, cs, "streamer", map[string]any{"text": "slow", "count": 3, "delay": "100ms"})
	// There is a delay before each of the chunks that is followed by another one.
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("stream took %s, want it to be delayed between the chunks", elapsed)
	}
}

// A resumable stream keeps its state in the registry after its second chunk, and stops there. Without a registry the
// state cannot be kept, so the call fails with the reason, instead of going on to send what it can't resume.
func TestResumableStreamFailsWhenItsStateCannotBeKept(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("resumer", `{"stream":true,"resumable":true}`, ""))
	res, err := callToolRaw(t, cs, "resumer", map[string]any{"text": "part", "count": 5, "delay": "1ms"})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "Registry not configured")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "Registry not configured")
	default:
		t.Errorf("a stream that could not keep its state is a success: %q", resultText(res))
	}
}
