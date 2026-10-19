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
	"testing"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func TestGetInputTextFromMessage(t *testing.T) {
	t.Run("text parts are joined", func(t *testing.T) {
		m := &a2aproto.Message{Parts: []a2aproto.Part{
			&a2aproto.TextPart{Kind: a2aproto.KindText, Text: "hello "},
			&a2aproto.TextPart{Kind: a2aproto.KindText, Text: "world"},
		}}
		if got := GetInputTextFromMessage(m); got != "hello world" {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("other parts are skipped", func(t *testing.T) {
		m := &a2aproto.Message{Parts: []a2aproto.Part{
			&a2aproto.TextPart{Text: "a"},
			&a2aproto.DataPart{Kind: a2aproto.KindData, Data: map[string]any{"k": "v"}},
			&a2aproto.TextPart{Text: "b"},
		}}
		if got := GetInputTextFromMessage(m); got != "ab" {
			t.Errorf("text = %q, want ab", got)
		}
	})
	t.Run("no parts", func(t *testing.T) {
		if got := GetInputTextFromMessage(&a2aproto.Message{}); got != "" {
			t.Errorf("text = %q, want none", got)
		}
	})
	t.Run("message from JSON", func(t *testing.T) {
		m := &a2aproto.Message{}
		payload := `{"kind":"message","messageId":"m1","role":"user","parts":[{"kind":"text","text":"from the wire"},{"kind":"data","data":{"a":1}}]}`
		if err := json.Unmarshal([]byte(payload), m); err != nil {
			t.Fatal(err)
		}
		if got := GetInputTextFromMessage(m); got != "from the wire" {
			t.Errorf("text = %q", got)
		}
	})
}
