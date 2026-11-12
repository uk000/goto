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

package model

import (
	"context"
	"encoding/json"
	a2aclient "goto/pkg/ai/a2a/client"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/server/response/payload"
	"goto/pkg/types"
	"net/http"
	"sync"
	"sync/atomic"

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
	ID        string                `yaml:"-" json:"id"`
	Card      *trpcserver.AgentCard `yaml:"card" json:"card"`
	Behavior  *AgentBehavior        `yaml:"behavior,omitempty" json:"behavior,omitempty"`
	Config    *AgentConfig          `yaml:"config,omitempty" json:"config,omitempty"`
	Handler   http.Handler          `yaml:"-" json:"-"`
	URI       string                `yaml:"-" json:"-"`
	Server    *trpcserver.A2AServer `yaml:"-" json:"-"`
	Port      int                   `yaml:"-" json:"-"`
	Streaming bool                  `yaml:"-" json:"-"`

	// payloadLock guards Config.ResponsePayload, which the admin API replaces while the agent is serving.
	payloadLock sync.RWMutex
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

type DelegateConfig struct {
	Tools    map[string]*DelegateToolCall  `yaml:"tools,omitempty" json:"tools,omitempty"`
	Agents   map[string]*DelegateAgentCall `yaml:"agents,omitempty" json:"agents,omitempty"`
	HTTP     map[string]*DelegateHTTPCall  `yaml:"http,omitempty" json:"http,omitempty"`
	MaxCalls int                           `yaml:"maxCalls,omitempty" json:"maxCalls,omitempty"`
	Parallel bool                          `yaml:"parallel,omitempty" json:"parallel,omitempty"`
}

// DelegateTracker counts the calls made to a delegate and the responses received from it. It is updated by the
// goroutines that run the delegates, and reported by the admin API at the same time.
type DelegateTracker struct {
	CallCount     atomic.Int32
	ResponseCount atomic.Int32
	done          atomic.Bool
}

type delegateTrackerJSON struct {
	CallCount     int32 `json:"callCount"`
	ResponseCount int32 `json:"responseCount"`
	Done          bool  `json:"done"`
}

type DelegateToolCall struct {
	Disabled    bool                       `json:"disabled"`
	GivenName   string                     `json:"givenName"`
	Triggers    []string                   `yaml:"triggers" json:"triggers"`
	ToolCall    *mcpclient.ToolCall        `yaml:"toolCall,omitempty" json:"toolCall,omitempty"`
	Substitutes map[string]*DelegateServer `yaml:"substitutes,omitempty" json:"substitutes,omitempty"`
	Tracker     *DelegateTracker           `json:"tracker"`
}

type DelegateAgentCall struct {
	Disabled    bool                       `json:"disabled"`
	GivenName   string                     `json:"givenName"`
	Triggers    []string                   `yaml:"triggers" json:"triggers"`
	AgentCall   *a2aclient.AgentCall       `yaml:"agentCall,omitempty" json:"agentCall,omitempty"`
	Substitutes map[string]*DelegateServer `yaml:"substitutes,omitempty" json:"substitutes,omitempty"`
	Tracker     *DelegateTracker           `json:"tracker"`
}

type DelegateHTTPCall struct {
	Disabled    bool                       `json:"disabled"`
	GivenName   string                     `json:"givenName"`
	Triggers    []string                   `yaml:"triggers" json:"triggers"`
	HTTPCall    *HTTPCall                  `yaml:"httpCall,omitempty" json:"httpCall,omitempty"`
	Substitutes map[string]*DelegateServer `yaml:"substitutes,omitempty" json:"substitutes,omitempty"`
	Tracker     *DelegateTracker           `json:"tracker"`
}

type DelegateServer struct {
	URL       string `yaml:"url" json:"url"`
	Authority string `yaml:"authority,omitempty" json:"authority,omitempty"`
}

type HTTPCall struct {
	URL       string         `yaml:"url" json:"url"`
	Authority string         `yaml:"authority" json:"authority"`
	Delay     *types.Delay   `yaml:"delay,omitempty" json:"delay,omitempty"`
	Headers   *types.Headers `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// NewDelegateTracker creates a tracker with no calls or responses counted.
func NewDelegateTracker() *DelegateTracker {
	return &DelegateTracker{}
}

// MarshalJSON reports the counts, as the atomics that hold them have no JSON form of their own.
func (t *DelegateTracker) MarshalJSON() ([]byte, error) {
	return json.Marshal(&delegateTrackerJSON{
		CallCount:     t.CallCount.Load(),
		ResponseCount: t.ResponseCount.Load(),
		Done:          t.done.Load(),
	})
}

// UnmarshalJSON restores the counts and the done state from their JSON form.
func (t *DelegateTracker) UnmarshalJSON(b []byte) error {
	v := &delegateTrackerJSON{}
	if err := json.Unmarshal(b, v); err != nil {
		return err
	}
	t.CallCount.Store(v.CallCount)
	t.ResponseCount.Store(v.ResponseCount)
	t.done.Store(v.Done)
	return nil
}

// IncrementCall counts a call made to the delegate.
func (t *DelegateTracker) IncrementCall() {
	t.CallCount.Add(1)
}

// IncrementResponse counts a response received from the delegate.
func (t *DelegateTracker) IncrementResponse() {
	t.ResponseCount.Add(1)
}

// Finish marks the delegate as done.
func (t *DelegateTracker) Finish() {
	t.done.Store(true)
}

// IsDone tells whether the delegate is done.
func (t *DelegateTracker) IsDone() bool {
	return t.done.Load()
}

// GetCard returns the card of the agent.
func (a *Agent) GetCard() *trpcserver.AgentCard {
	return a.Card
}

// SetPayload parses the JSON as a payload and sets it as the response payload of the agent, with the lock held.
func (a *Agent) SetPayload(b []byte) error {
	p := &payload.Payload{}
	err := json.Unmarshal(b, p)
	if err != nil {
		return err
	}
	a.payloadLock.Lock()
	defer a.payloadLock.Unlock()
	if a.Config == nil {
		a.Config = &AgentConfig{}
	}
	a.Config.ResponsePayload = p
	return nil
}

// GetPayload returns the response payload of the agent, which is nil if there is none.
func (a *Agent) GetPayload() *payload.Payload {
	a.payloadLock.RLock()
	defer a.payloadLock.RUnlock()
	if a.Config == nil {
		return nil
	}
	return a.Config.ResponsePayload
}

type agentReport struct {
	ID       string                `json:"id"`
	Card     *trpcserver.AgentCard `json:"card"`
	Behavior *AgentBehavior        `json:"behavior,omitempty"`
	Config   *AgentConfig          `json:"config,omitempty"`
}

// MarshalJSON reports the agent. The payload of an agent is replaced by the admin API while the agent serves and is
// reported, so the config is copied with the lock held.
func (a *Agent) MarshalJSON() ([]byte, error) {
	a.payloadLock.RLock()
	report := agentReport{ID: a.ID, Card: a.Card, Behavior: a.Behavior}
	if a.Config != nil {
		config := *a.Config
		report.Config = &config
	}
	a.payloadLock.RUnlock()
	return json.Marshal(report)
}

// Serve hands the request to the agent's handler, or responds with 503 if the agent is not ready.
func (a *Agent) Serve(w http.ResponseWriter, r *http.Request) {
	if a.Handler == nil {
		http.Error(w, "agent is not ready to serve", http.StatusServiceUnavailable)
		return
	}
	a.Handler.ServeHTTP(w, r)
}
