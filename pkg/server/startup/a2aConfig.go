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
	"goto/ctl"
	a2aserver "goto/pkg/ai/a2a/server"
	"goto/pkg/ai/registry"
	"log"
)

func clearA2A(a2a *ctl.A2A) {
	for _, pa := range a2a.Servers {
		for _, a2aAgent := range pa.Agents {
			if a2aAgent != nil && a2aAgent.Card != nil {
				a2aserver.RemoveAgent(a2aAgent.Port, a2aAgent.Card.Name)
				registry.TheAgentRegistry.RemoveAgent(a2aAgent.Card.Name)
			}
		}
	}
}

func loadA2A(a2a *ctl.A2A) {
	names := []string{}
	clearA2A(a2a)
	for _, pa := range a2a.Servers {
		for aname, agent := range pa.Agents {
			if agent == nil || agent.Card == nil || agent.Config == nil {
				log.Printf("Skipping agent [%s] due to missing Card/Config\n", aname)
				continue
			}
			agent.Port = pa.Port
			name := agent.Card.Name
			server := a2aserver.GetOrAddServer(agent.Port)
			if err := server.AddAgent(agent); err == nil {
				registry.TheAgentRegistry.AddAgent(agent, agent.Port)
				names = append(names, fmt.Sprintf("%s(%d)", name, agent.Port))
			} else {
				log.Printf("Failed to load agent [%s]: %s\n", aname, err.Error())
			}
		}
	}
	log.Println("============================================================")
	log.Printf("Added Agents: %+v\n", names)
	log.Println("============================================================")
}
