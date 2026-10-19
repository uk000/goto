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

package startup

import (
	"fmt"
	"strings"
	"testing"

	"goto/ctl"
	"goto/pkg/ai/a2a/model"
	a2aserver "goto/pkg/ai/a2a/server"
	"goto/pkg/ai/registry"
)

func a2aYAML(port int, agents ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "a2a:\n  servers:\n  - port: %d\n    agents:\n", port)
	for _, name := range agents {
		fmt.Fprintf(&b, `      %[1]s:
        card:
          name: %[1]s
          description: an agent of a test
          version: "1"
          url: http://localhost:%[2]d/a2a/%[1]s
          capabilities: {}
          defaultInputModes: [text]
          defaultOutputModes: [text]
          skills:
          - id: talk
            name: talk
            description: talks
        behavior:
          echo: true
        config: {}
`, name, port)
	}
	return b.String()
}

// a2aPort is a port for the agents of a test, which are removed when the test ends.
func a2aPort(t *testing.T, agents ...string) int {
	t.Helper()
	port := newPort()
	t.Cleanup(func() {
		for _, name := range agents {
			a2aserver.RemoveAgent(port, name)
			registry.TheAgentRegistry.RemoveAgent(name)
		}
	})
	return port
}

func hasAgent(port int, name string) bool {
	return a2aserver.GetOrAddServer(port).GetAgent(name) != nil
}

func TestLoadA2ANothingToLoad(t *testing.T) {
	resetState(t)
	loadA2A(nil)
	clearA2A(nil)
	logged := captureLog(t)
	loadA2A(&ctl.A2A{Servers: []*ctl.A2AServer{nil, {Port: newPort()}}})
	clearA2A(&ctl.A2A{Servers: []*ctl.A2AServer{nil, {Agents: map[string]*model.Agent{"nothing": nil}}}})
	if strings.Contains(logged.String(), "panic") {
		t.Errorf("unexpected log: %s", logged.String())
	}
}

func TestLoadA2AAddsAgentsToTheServerOfTheirPort(t *testing.T) {
	resetState(t)
	port := a2aPort(t, "startup-agent-one", "startup-agent-two")
	loadConfig(parse(t, "agents.yaml", a2aYAML(port, "startup-agent-one", "startup-agent-two")))
	for _, name := range []string{"startup-agent-one", "startup-agent-two"} {
		if !hasAgent(port, name) {
			t.Errorf("agent %s is not on the server of port %d", name, port)
		}
		if agents := registry.TheAgentRegistry.GetAgents(name); agents[port] == nil {
			t.Errorf("agent %s is not in the registry for port %d: %v", name, port, agents)
		}
	}
	if a2aConfigs["agents.yaml"] == nil {
		t.Error("the config of the file is not kept")
	}
	if agent := a2aserver.GetOrAddServer(port).GetAgent("startup-agent-one"); agent == nil || agent.Port != port {
		t.Errorf("agent = %+v, want one that has the port of its server", agent)
	}
}

func TestLoadA2ASkipsAgentsWithoutCardOrConfig(t *testing.T) {
	resetState(t)
	port := a2aPort(t, "startup-agent-valid")
	logged := captureLog(t)
	loadConfig(parse(t, "partial.yaml", a2aYAML(port, "startup-agent-valid")+`
      no-card:
        config: {}
      no-config:
        card:
          name: startup-agent-no-config
      empty:
`))
	if !hasAgent(port, "startup-agent-valid") {
		t.Error("the agent that is valid was not added")
	}
	if hasAgent(port, "startup-agent-no-config") {
		t.Error("an agent without a config was added")
	}
	if n := strings.Count(logged.String(), "due to missing Card/Config"); n != 3 {
		t.Errorf("agents skipped = %d, want 3: %s", n, logged.String())
	}
}

func TestClearA2ARemovesOnlyTheAgentsOfTheFile(t *testing.T) {
	resetState(t)
	port := a2aPort(t, "startup-agent-gone", "startup-agent-stays")
	loadConfig(parse(t, "gone.yaml", a2aYAML(port, "startup-agent-gone")))
	loadConfig(parse(t, "stays.yaml", a2aYAML(port, "startup-agent-stays")))
	removeConfigs("/configs/gone.yaml")
	if hasAgent(port, "startup-agent-gone") || len(registry.TheAgentRegistry.GetAgents("startup-agent-gone")) != 0 {
		t.Error("the agent of the removed file is still there")
	}
	if !hasAgent(port, "startup-agent-stays") {
		t.Error("the agent of the other file was removed")
	}
}

func TestLoadConfigA2AReloadReplacesTheAgentsOfTheFile(t *testing.T) {
	resetState(t)
	port := a2aPort(t, "startup-agent-before", "startup-agent-after")
	loadConfig(parse(t, "reload.yaml", a2aYAML(port, "startup-agent-before")))
	loadConfig(parse(t, "reload.yaml", a2aYAML(port, "startup-agent-after")))
	if hasAgent(port, "startup-agent-before") {
		t.Error("the agent of the file as it was is still there")
	}
	if !hasAgent(port, "startup-agent-after") {
		t.Error("the agent of the file as it is now is not there")
	}
}
