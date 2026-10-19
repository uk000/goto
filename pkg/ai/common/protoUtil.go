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
	"strings"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

// GetInputTextFromMessage joins the text of all text parts of the message. Parts that are built in the code are values
// of TextPart, and the ones that are decoded from JSON are pointers, so both are accepted.
func GetInputTextFromMessage(m *a2aproto.Message) string {
	if m == nil {
		return ""
	}
	b := strings.Builder{}
	for _, p := range m.Parts {
		switch tp := p.(type) {
		case *a2aproto.TextPart:
			if tp != nil {
				b.WriteString(tp.Text)
			}
		case a2aproto.TextPart:
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}
