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

package registry

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	a2amodel "goto/pkg/ai/a2a/model"

	trpcserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

func newAgent(name string) *a2amodel.Agent {
	return &a2amodel.Agent{ID: name + "-id", Card: &trpcserver.AgentCard{Name: name}}
}

func agentNames(r *AgentRegistry) []string {
	r.lock.RLock()
	defer r.lock.RUnlock()
	names := []string{}
	for name := range r.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestRegistriesAreInitialized(t *testing.T) {
	if TheAgentRegistry == nil || TheAgentRegistry.Agents == nil {
		t.Error("the agent registry is not initialized")
	}
	if TheMCPRegistry == nil || TheMCPRegistry.Servers == nil {
		t.Error("the MCP registry is not initialized")
	}
	if TheSkillsRegistry == nil || TheSkillsRegistry.AgentsBySkill == nil || TheSkillsRegistry.ToolsBySkill == nil {
		t.Error("the skills registry is not initialized")
	}
	r := newAgentRegistry()
	if r.Agents == nil || len(r.Agents) != 0 {
		t.Errorf("new agent registry = %+v", r.Agents)
	}
	if m := newMCPRegistry(); m.Servers == nil || len(m.Servers) != 0 {
		t.Errorf("new MCP registry = %+v", m.Servers)
	}
	if s := newSkillsRegistry(); s.AgentsBySkill == nil || s.ToolsBySkill == nil || s.Skill != nil {
		t.Errorf("new skills registry = %+v", s)
	}
	if newAgentRegistry() == r {
		t.Error("registries are shared")
	}
}

func TestAddAndGetAgents(t *testing.T) {
	r := newAgentRegistry()
	a1, a2, b := newAgent("alpha"), newAgent("alpha"), newAgent("beta")
	r.AddAgent(a1, 8080)
	r.AddAgent(a2, 9090)
	r.AddAgent(b, 8080)
	alpha := r.GetAgents("alpha")
	if len(alpha) != 2 || alpha[8080] != a1 || alpha[9090] != a2 {
		t.Errorf("agents of alpha = %+v", alpha)
	}
	if beta := r.GetAgents("beta"); len(beta) != 1 || beta[8080] != b {
		t.Errorf("agents of beta = %+v", beta)
	}
	if got := r.GetAgents("missing"); len(got) != 0 {
		t.Errorf("agents of missing = %+v, want none", got)
	}
	if got := agentNames(r); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("names = %v", got)
	}
}

func TestAddAgentReplacesTheAgentOfThePort(t *testing.T) {
	r := newAgentRegistry()
	old, replacement := newAgent("alpha"), newAgent("alpha")
	r.AddAgent(old, 8080)
	r.AddAgent(replacement, 8080)
	if got := r.GetAgents("alpha"); len(got) != 1 || got[8080] != replacement {
		t.Errorf("agents = %+v, want the replacement only", got)
	}
}

func TestAddAgents(t *testing.T) {
	r := newAgentRegistry()
	r.AddAgents([]*a2amodel.Agent{newAgent("one"), newAgent("two"), newAgent("three")}, 7000)
	if got := agentNames(r); len(got) != 3 {
		t.Errorf("names = %v", got)
	}
	for _, name := range []string{"one", "two", "three"} {
		if got := r.GetAgents(name); len(got) != 1 || got[7000] == nil || got[7000].Card.Name != name {
			t.Errorf("agents of %s = %+v", name, got)
		}
	}
	r.AddAgents(nil, 7000)
	if len(agentNames(r)) != 3 {
		t.Error("adding no agents changed the registry")
	}
}

func TestRemoveAgent(t *testing.T) {
	r := newAgentRegistry()
	r.AddAgent(newAgent("alpha"), 1)
	r.AddAgent(newAgent("alpha"), 2)
	r.AddAgent(newAgent("beta"), 1)
	r.RemoveAgent("alpha")
	if got := r.GetAgents("alpha"); len(got) != 0 {
		t.Errorf("agents of alpha = %+v, want them all removed", got)
	}
	if got := r.GetAgents("beta"); len(got) != 1 {
		t.Errorf("agents of beta = %+v, want it left alone", got)
	}
	r.RemoveAgent("missing")
	if got := agentNames(r); len(got) != 1 || got[0] != "beta" {
		t.Errorf("names = %v", got)
	}
}

func TestClearAgents(t *testing.T) {
	r := newAgentRegistry()
	r.AddAgents([]*a2amodel.Agent{newAgent("a"), newAgent("b")}, 1)
	r.Clear()
	if got := agentNames(r); len(got) != 0 || r.Agents == nil {
		t.Errorf("registry after Clear = %v / %+v", got, r.Agents)
	}
	r.AddAgent(newAgent("c"), 1)
	if got := agentNames(r); len(got) != 1 {
		t.Errorf("registry is not usable after Clear: %v", got)
	}
	r.init()
	if got := agentNames(r); len(got) != 0 {
		t.Errorf("registry after init = %v", got)
	}
}

func TestAgentRegistryConcurrentAdds(t *testing.T) {
	r := newAgentRegistry()
	const workers, perWorker = 8, 40
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				r.AddAgent(newAgent(fmt.Sprintf("agent-%d-%d", w, i)), 1000+w)
				r.AddAgent(newAgent("shared"), 1000+w)
			}
		}(w)
	}
	waitWithin(t, &wg, 30*time.Second)
	if got := len(agentNames(r)); got != workers*perWorker+1 {
		t.Errorf("agents = %d, want %d", got, workers*perWorker+1)
	}
	if got := r.GetAgents("shared"); len(got) != workers {
		t.Errorf("ports of the shared agent = %d, want %d", len(got), workers)
	}
	for w := 0; w < workers; w++ {
		if a := r.GetAgents(fmt.Sprintf("agent-%d-%d", w, perWorker-1)); a[1000+w] == nil {
			t.Errorf("agent of worker %d is missing: %+v", w, a)
		}
	}
}
