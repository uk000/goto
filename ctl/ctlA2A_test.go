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

package ctl

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
)

// agentNames are the names of the agents in the body of a request to add agents, in order.
func agentNames(t *testing.T, body string) []string {
	t.Helper()
	var agents []struct {
		Card struct {
			Name string `json:"name"`
		} `json:"card"`
	}
	if err := json.Unmarshal([]byte(body), &agents); err != nil {
		t.Fatalf("body is not a list of agents: %v\n%s", err, body)
	}
	var names []string
	for _, a := range agents {
		names = append(names, a.Card.Name)
	}
	sort.Strings(names)
	return names
}

func TestProcessA2ASendsTheAgentsOfAServer(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("a2a.yaml", []byte(`
a2a:
  servers:
  - port: 9001
    agents:
      first:
        card:
          name: agent-one
      second:
        card:
          name: agent-two
`))
	if config == nil {
		t.Fatal("config not parsed")
	}
	within(t, "processA2A", func() { processA2A(config) })
	got := r.got()
	if len(got) != 1 {
		t.Fatalf("requests = %+v, want 1", got)
	}
	if got[0].Method != "POST" || got[0].Path != "/port=9001/a2a/agents/add" || got[0].ContentType != "application/json" {
		t.Errorf("request = %+v", got[0])
	}
	if names := agentNames(t, got[0].Body); len(names) != 2 || names[0] != "agent-one" || names[1] != "agent-two" {
		t.Errorf("agents = %v", names)
	}
}

func TestProcessA2AWithNothingSendsNothing(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processA2A", func() {
		processA2A(&GotoConfig{})
		processA2A(&GotoConfig{A2A: &A2A{}})
		processA2A(&GotoConfig{A2A: &A2A{Servers: []*A2AServer{}}})
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestProcessA2AWhenTheRemoteFails(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusInternalServerError)
	useRemote(t, r.URL)
	config := ParseConfig("a2a.yaml", []byte("a2a:\n  servers:\n  - port: 1\n    agents:\n      a:\n        card:\n          name: x\n"))
	within(t, "processA2A", func() { processA2A(config) })
	if got := r.got(); len(got) != 1 {
		t.Errorf("requests = %v, want 1", got)
	}
}

func TestProcessA2AWhenTheRemoteIsGone(t *testing.T) {
	r := newRemote(t)
	url := r.URL
	r.Close()
	useRemote(t, url)
	config := ParseConfig("a2a.yaml", []byte("a2a:\n  servers:\n  - port: 1\n    agents:\n      a:\n        card:\n          name: x\n"))
	within(t, "processA2A", func() { processA2A(config) })
}
