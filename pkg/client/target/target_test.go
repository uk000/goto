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

package target

import (
	"testing"

	"goto/pkg/constants"
	"goto/pkg/invocation"
	"goto/pkg/util"
)

// A target pushed by the registry usually has no headers, and AddTarget adds the Goto headers to it.
func TestAddTargetWithoutHeaders(t *testing.T) {
	tc := NewTargetClient(0)
	payload := `{"name":"t1","method":"GET","url":"http://localhost:1/status/200","replicas":1,"requestCount":1,"autoInvoke":false}`
	target := &Target{InvocationSpec: &invocation.InvocationSpec{}}
	if err := util.ReadJson(payload, target); err != nil {
		t.Fatalf("failed to read target: %s", err.Error())
	}
	if target.Headers != nil {
		t.Fatalf("expected the target to have no headers, got %v", target.Headers)
	}
	if err := tc.AddTarget(target); err != nil {
		t.Fatalf("AddTarget failed: %s", err.Error())
	}
	if _, present := target.Headers[constants.HeaderFromGoto]; !present {
		t.Errorf("expected header %s on the target, got %v", constants.HeaderFromGoto, target.Headers)
	}
	if tc.PrepareTarget("t1") == nil {
		t.Errorf("target t1 was not added")
	}
}
