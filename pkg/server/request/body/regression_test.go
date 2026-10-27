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

package body

import (
	"fmt"
	"strings"
	"testing"

	"goto/pkg/util"
)

// The size was of the body without its leading and trailing spaces, which is what the echo of the request, and the
// payload size header of it, then gave: less than what the client sent, and said it sent.
func TestPayloadSizeIsOfTheWholeBodyThatWasSent(t *testing.T) {
	setLogFlags(t, false, false, false)
	for _, body := range []string{"  padded  ", " x", "x ", "   ", strings.Repeat(" ", 10) + "{}" + strings.Repeat(" ", 10)} {
		req := newRequest("/some/path", body)
		seen, _ := serve(req)
		rs := util.GetRequestStore(req)
		if rs.RequestPayloadSize != len(body) {
			t.Errorf("body %q: size = %d, want the %d bytes that were sent", body, rs.RequestPayloadSize, len(body))
		}
		if want := fmt.Sprintf("Request Body Length: [%d]", len(body)); len(rs.LogMessages) == 0 || rs.LogMessages[0] != want {
			t.Errorf("body %q: log = %q, want %q first", body, rs.LogMessages, want)
		}
		if seen != body {
			t.Errorf("body %q: next saw %q, want it as it was sent", body, seen)
		}
	}
}

// The size of what was sent is longer than the payload when there are spaces around it. The end of the mini body is taken
// from the payload, so it must not be taken using that size, which is past the end of it: this guards the fix above.
func TestMiniBodyOfABodyWithSpacesAroundItIsNotSlicedOutOfRange(t *testing.T) {
	setLogFlags(t, true, false, false)
	payload := strings.Repeat("a", 60) + strings.Repeat("b", 60)
	body := strings.Repeat(" ", 40) + payload + strings.Repeat(" ", 40)
	req := newRequest("/some/path", body)
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("a mini body of a body with spaces around it panicked: %v", p)
			}
		}()
		serve(req)
	}()
	want := "Request Mini Body: [" + strings.Repeat("a", 50) + "..." + strings.Repeat("b", 50) + "]"
	logs := util.GetRequestStore(req).LogMessages
	if len(logs) != 2 || logs[1] != want {
		t.Errorf("log = %q, want the ends of the payload: %q", logs, want)
	}
}
