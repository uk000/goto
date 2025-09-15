package model

import (
	"context"
	"encoding/json"
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
	DoUnary(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions, handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error)
	DoStream(task IAgentTask) error
}

type IAgentTask interface {
}

type Agent struct {
	Card     *trpcserver.AgentCard `json:"card"`
	Behavior *AgentBehavior        `json:"behavior,omitempty"`
	Config   *AgentConfig          `json:"config,omitempty"`
	Server   *trpcserver.A2AServer `json:"-"`
}

type AgentBehavior struct {
	Echo      bool           `json:"echo,omitempty"`
	Stream    bool           `json:"stream,omitempty"`
	Delegate  bool           `json:"delegate,omitempty"`
	HTTPProxy bool           `json:"httpProxy,omitempty"`
	Impl      IAgentBehavior `json:"-"`
}

type AgentConfig struct {
	Delay    *types.Delay     `json:"delay,omitempty"`
	Response *payload.Payload `json:"response,omitempty"`
	Delegate *DelegateConfig  `json:"delegate,omitempty"`
}

type DelegateConfig struct {
	Neat           bool                `json:"neat,omitempty"`
	URL            string              `json:"url"`
	Authority      string              `json:"authority,omitempty"`
	Args           map[string]any      `json:"args,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	ForwardHeaders []string            `json:"forwardHeaders,omitempty"`
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
