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
	"bytes"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/util"
	"log"
	"net/http"
)

type A2AServer struct {
	Port     int                     `yaml:"port"`
	Agents   map[string]*model.Agent `yaml:"agents"`
	Response map[string]any          `yaml:"response"`
}

type A2A struct {
	Servers []*A2AServer `yaml:"servers"`
}

func processA2A(config *GotoConfig) {
	if config.A2A == nil {
		return
	}
	sendAgents(config.A2A)
}

func sendAgents(a2a *A2A) {
	if len(a2a.Servers) == 0 {
		log.Println("No A2A Agents to configure")
		return
	}
	agentPayload := []*model.Agent{}
	for _, pa := range a2a.Servers {
		url := fmt.Sprintf("%s/port=%d/a2a/agents/add", currentContext.RemoteGotoURL, pa.Port)
		for _, agent := range pa.Agents {
			agentPayload = append(agentPayload, agent)
		}
		json := util.ToJSONBytes(agentPayload)
		if json == nil {
			log.Printf("JSON marshalling error. Agents JSON: %+v", agentPayload)
			return
		}
		log.Printf("Sending Agents to URL [%s]\n", url)
		resp, err := http.Post(url, "application/json", bytes.NewReader(json))
		if err != nil {
			log.Printf("Failed to send Agents. Error [%s]n", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("Non-OK status for Agents: %s\n", resp.Status)
			log.Println(string(json))
		} else {
			log.Printf("Agents sent successfully. Response: [%s]\n", util.Read(resp.Body))
		}
	}
}
