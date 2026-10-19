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
	"testing"

	"goto/pkg/ai/a2a/model"
)

// The behavior for a remote HTTP call is declared, but does not do anything yet.
func TestHTTPProxyBehaviorIsPrepared(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("httpproxy", false, &model.AgentBehavior{HTTPProxy: true}, nil)
	behavior, ok := agent.Behavior.Impl.(*AgentBehaviorRemoteHttp)
	if !ok {
		t.Fatalf("behavior = %T, want the one for remote HTTP calls", agent.Behavior.Impl)
	}
	result, err := behavior.DoUnary(nil)
	if result != nil || err != nil {
		t.Errorf("DoUnary = %v, %v, want nothing", result, err)
	}
	status, err := behavior.DoStream(nil)
	if status != "" || err != nil {
		t.Errorf("DoStream = %q, %v, want nothing", status, err)
	}
	if agent.Handler == nil || agent.URI == "" {
		t.Errorf("agent = %+v, want it prepared to serve", agent)
	}
}
