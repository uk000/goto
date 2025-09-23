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
	ID       string                `json:"id"`
	Card     *trpcserver.AgentCard `json:"card"`
	Behavior *AgentBehavior        `json:"behavior,omitempty"`
	Config   *AgentConfig          `json:"config,omitempty"`
	Server   *trpcserver.A2AServer `json:"-"`
	Port     int                   `json:"-"`
}

type AgentBehavior struct {
	Echo      bool           `json:"echo,omitempty"`
	Stream    bool           `json:"stream,omitempty"`
	Federate  bool           `json:"federate,omitempty"`
	HTTPProxy bool           `json:"httpProxy,omitempty"`
	Impl      IAgentBehavior `json:"-"`
}

type AgentConfig struct {
	Delay     *types.Delay     `json:"delay,omitempty"`
	Response  *payload.Payload `json:"response,omitempty"`
	Delegates *DelegateConfig  `json:"delegates,omitempty"`
}

type DelegateToolCall struct {
	Triggers []string           `json:"triggers"`
	ToolCall mcpclient.ToolCall `json:"toolCall,omitempty"`
}

type DelegateAgentCall struct {
	Triggers  []string            `json:"triggers"`
	AgentCall a2aclient.AgentCall `json:"agentCall,omitempty"`
}

type DelegateConfig struct {
	Tools    map[string]*DelegateToolCall  `json:"tools,omitempty"`
	Agents   map[string]*DelegateAgentCall `json:"agents,omitempty"`
	MaxCalls int                           `json:"maxCalls,omitempty"`
	Parallel bool                          `json:"parallel,omitempty"`
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
	a.Config.Response = p
	return nil
}

func (a *Agent) Serve(w http.ResponseWriter, r *http.Request) {
	a.Server.Handler().ServeHTTP(w, r)
}
