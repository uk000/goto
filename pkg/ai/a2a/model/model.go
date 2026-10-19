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
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	goa2a "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type Message struct {
	*goa2a.Message
}

// Text returns the text of the first text part of the message. The parts are values when an agent builds a
// message, and pointers when a message is decoded, so both are read.
func (m *Message) Text() string {
	if m == nil || m.Message == nil {
		return ""
	}
	for _, part := range m.Parts {
		switch textPart := part.(type) {
		case *protocol.TextPart:
			if textPart != nil {
				return textPart.Text
			}
		case protocol.TextPart:
			return textPart.Text
		}
	}
	return ""
}
