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

package model

import (
	"encoding/json"
	"testing"

	goa2a "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func TestMessageText(t *testing.T) {
	tests := []struct {
		name  string
		parts []goa2a.Part
		want  string
	}{
		{"no parts", nil, ""},
		{"pointer text part", []goa2a.Part{&goa2a.TextPart{Kind: goa2a.KindText, Text: "hello"}}, "hello"},
		{"value text part", []goa2a.Part{goa2a.NewTextPart("hello value")}, "hello value"},
		{"data part only", []goa2a.Part{goa2a.NewDataPart(map[string]any{"a": 1})}, ""},
		{"text after data", []goa2a.Part{goa2a.NewDataPart("x"), goa2a.NewTextPart("after")}, "after"},
		{"first text wins", []goa2a.Part{goa2a.NewTextPart("one"), goa2a.NewTextPart("two")}, "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := goa2a.NewMessage(goa2a.MessageRoleUser, tt.parts)
			msg := &Message{Message: &m}
			if got := msg.Text(); got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A message that was received over the wire has its parts decoded by the protocol package.
func TestMessageTextOfDecodedMessage(t *testing.T) {
	var m goa2a.Message
	body := `{"kind":"message","messageId":"m1","role":"user","parts":[{"kind":"data","data":{"k":"v"}},{"kind":"text","text":"decoded hello"}]}`
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got := (&Message{Message: &m}).Text(); got != "decoded hello" {
		t.Errorf("Text() = %q, want %q", got, "decoded hello")
	}
}
