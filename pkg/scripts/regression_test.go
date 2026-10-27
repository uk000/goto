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

package scripts

import (
	"strings"
	"testing"
)

// Running a script that was never added (or that was removed, or that could not be stored) used to call a method of a nil
// script, which is a panic. The one who asked for it is told that there is no such script instead.
func TestRunScriptThatIsNotThereIsReported(t *testing.T) {
	sm := newManager()
	out := run(t, sm, "never-added", []string{"a"}, "")
	if !strings.Contains(out, "[never-added]") || !strings.Contains(out, "not found") {
		t.Errorf("output = %q, want the name of the script that is not there", out)
	}
}

func TestRunScriptThatWasRemovedIsReported(t *testing.T) {
	sm := newManager()
	sm.AddScript("short-lived", "echo hi", false)
	sm.RemoveScript("short-lived")
	if out := run(t, sm, "short-lived", nil, ""); !strings.Contains(out, "not found") {
		t.Errorf("output = %q, want a report that the script is gone", out)
	}
}

func TestRunScriptThatCouldNotBeStoredIsReported(t *testing.T) {
	useScriptsDir(t)
	sm := newManager()
	sm.AddScript("sub/missing", "echo no", true)
	if out := run(t, sm, "sub/missing", nil, ""); !strings.Contains(out, "not found") {
		t.Errorf("output = %q, want a report that the script is not there", out)
	}
}

// The API answers that a script is not there, as it does not with what the script would have written.
func TestAPIRunScriptThatIsNotThereDoesNotPanic(t *testing.T) {
	useAPI(t)
	rr := call(t, "POST", "/scripts/never-added/run?args=a", "")
	wantStatus(t, rr, 404)
	if !strings.Contains(rr.Body.String(), "not found") {
		t.Errorf("body = %q", rr.Body.String())
	}
}
