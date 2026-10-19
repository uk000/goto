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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func agentTool(url string) string {
	return toolJSON("agent", `{"agents":true}`, fmt.Sprintf(`{"agent":{"name":"upstream-agent","agentURL":%q,"requestTimeout":"5s"}}`, url))
}

func TestAgentToolFailsWhenTheAgentIsDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	_, cs := newToolServer(t, agentTool(url))
	res, err := callToolRaw(t, cs, "agent", map[string]any{"remoteArgs": map[string]any{"agentMessage": "hello"}})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "Failed to load agent card", "upstream-agent")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "Failed to load agent card")
	default:
		t.Errorf("a call to an agent that is down is a success: %+v", res)
	}
}

func TestAgentToolFailsWhenTheAgentHasNoCard(t *testing.T) {
	// Answers everything with a 404, the card included.
	notAnAgent := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notAnAgent.Close)
	_, cs := newToolServer(t, agentTool(notAnAgent.URL))
	res, err := callToolRaw(t, cs, "agent", map[string]any{})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "Failed to load agent card")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "Failed to load agent card")
	default:
		t.Errorf("a call to something that is not an agent is a success: %+v", res)
	}
}
