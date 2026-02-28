/**
 * Copyright 2025 uk
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

package model

import (
	"context"
	"encoding/json"
	a2aclient "goto/pkg/ai/a2a/client"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/server/response/payload"
	"goto/pkg/types"
	"net/http"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	trpcserver "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentSkill trpcserver.AgentSkill

type IAgentBehavior interface {
	ProcessMessage(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions, handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error)
	// DoUnary(aCtx IAgentCallContext) (*taskmanager.MessageProcessingResult, error)
	// DoStream(aCtx IAgentCallContext) error
}

type IAgentCallContext interface {
}

type Agent struct {
	ID       string                `yaml:"-" json:"id"`
	Card     *trpcserver.AgentCard `yaml:"card" json:"card"`
	Behavior *AgentBehavior        `yaml:"behavior,omitempty" json:"behavior,omitempty"`
	Config   *AgentConfig          `yaml:"config,omitempty" json:"config,omitempty"`
	Server   *trpcserver.A2AServer `yaml:"-" json:"-"`
	Port     int                   `yaml:"-" json:"-"`
}

type AgentBehavior struct {
	Echo      bool           `yaml:"echo,omitempty" json:"echo,omitempty"`
	Stream    bool           `yaml:"stream,omitempty" json:"stream,omitempty"`
	Federate  bool           `yaml:"federate,omitempty" json:"federate,omitempty"`
	HTTPProxy bool           `yaml:"httpProxy,omitempty" json:"httpProxy,omitempty"`
	Impl      IAgentBehavior `yaml:"-" json:"-"`
}

type AgentConfig struct {
	Delay           *types.Delay     `yaml:"delay,omitempty" json:"delay,omitempty"`
	ResponsePayload *payload.Payload `yaml:"response,omitempty" json:"response,omitempty"`
	Delegates       *DelegateConfig  `yaml:"delegates,omitempty" json:"delegates,omitempty"`
}

type DelegateServer struct {
	URL       string `yaml:"url" json:"url"`
	Authority string `yaml:"authority,omitempty" json:"authority,omitempty"`
}

type DelegateToolCall struct {
	Triggers []string                   `yaml:"triggers" json:"triggers"`
	ToolCall mcpclient.ToolCall         `yaml:"toolCall,omitempty" json:"toolCall,omitempty"`
	Servers  map[string]*DelegateServer `yaml:"servers" json:"servers"`
}

type DelegateAgentCall struct {
	Triggers  []string                   `yaml:"triggers" json:"triggers"`
	AgentCall a2aclient.AgentCall        `yaml:"agentCall,omitempty" json:"agentCall,omitempty"`
	Servers   map[string]*DelegateServer `yaml:"servers" json:"servers"`
}

type DelegateConfig struct {
	Tools    map[string]*DelegateToolCall  `yaml:"tools,omitempty" json:"tools,omitempty"`
	Agents   map[string]*DelegateAgentCall `yaml:"agents,omitempty" json:"agents,omitempty"`
	MaxCalls int                           `yaml:"maxCalls,omitempty" json:"maxCalls,omitempty"`
	Parallel bool                          `yaml:"parallel,omitempty" json:"parallel,omitempty"`
}

func (a *Agent) GetCard() *trpcserver.AgentCard {
	return a.Card
}

func (a *Agent) SetPayload(b []byte) error {
	p := &payload.Payload{}
	err := json.Unmarshal(b, p)
	if err != nil {
		return err
	}
	a.Config.ResponsePayload = p
	return nil
}

func (a *Agent) Serve(w http.ResponseWriter, r *http.Request) {
	a.Server.Handler().ServeHTTP(w, r)
}
