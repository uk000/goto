package ctl

import (
	"bytes"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/util"
	"log"
	"net/http"
)

type A2AAgent struct {
	Port     int            `yaml:"port"`
	Agent    *model.Agent   `yaml:"agent"`
	Response map[string]any `yaml:"response"`
}

type A2A struct {
	Agents []*A2AAgent `yaml:"agents"`
}

func processA2A(config *GotoConfig) {
	if config.A2A == nil || len(config.A2A.Agents) == 0 {
		log.Println("No A2A Agents to configure")
		return
	}
	sendAgents(config)
}

func sendAgents(config *GotoConfig) {
	url := fmt.Sprintf("%s/a2a/agents/add", currentContext.RemoteGotoURL)
	agentPayload := []*model.Agent{}
	for _, agent := range config.A2A.Agents {
		agentPayload = append(agentPayload, agent.Agent)
	}
	json := util.ToJSONBytes(agentPayload)
	if json == nil {
		log.Fatalf("JSON marshalling error. Agents JSON: %+v", config.A2A.Agents)
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

	for _, agent := range config.A2A.Agents {
		if agent.Response != nil {
			url = fmt.Sprintf("%s/a2a/agent/%s/payload", currentContext.RemoteGotoURL, agent.Agent.Card.Name)
			json := util.ToJSONBytes(agent.Response)
			if json == nil {
				log.Fatalf("JSON marshalling error. Agent Payload: %+v", agent.Response)
			}
			log.Printf("Sending Agent Payload to URL [%s]\n", url)
			resp, err := http.Post(url, "application/json", bytes.NewReader(json))
			if err != nil {
				log.Printf("Failed to send Agent payload. Error [%s]n", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				log.Printf("Non-OK status for Agent [%s] payload: status [%s]\n", resp.Status)
				log.Println(string(json))
			} else {
				log.Printf("Agent [%s] payload sent successfully. Response: [%s]\n", agent.Agent.Card.Name, util.Read(resp.Body))
			}

		}
	}
}
