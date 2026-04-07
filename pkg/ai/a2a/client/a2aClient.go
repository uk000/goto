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

package a2aclient

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"net/http"
	"time"

	goa2aclient "trpc.group/trpc-go/trpc-a2a-go/client"
	goa2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

type AgentResultsCallback func(key, output string, data any)

type AgentCall struct {
	Name                 string           `json:"name,omitempty"`
	AgentURL             string           `json:"agentURL,omitempty"`
	CardURL              string           `json:"cardURL,omitempty"`
	Authority            string           `json:"authority,omitempty"`
	H2                   bool             `json:"h2,omitempty"`
	TLS                  bool             `json:"tls,omitempty"`
	DataOnly             bool             `json:"dataOnly,omitempty"`
	Delay                string           `json:"delay,omitempty"`
	Message              string           `json:"message,omitempty"`
	Data                 map[string]any   `json:"data,omitempty"`
	Headers              *types.Headers   `json:"headers,omitempty"`
	RequestCount         int              `json:"requestCount"`
	Concurrent           int              `json:"concurrent"`
	InitialDelay         string           `json:"initialDelay"`
	RetryDelay           string           `json:"retryDelay"`
	RetriableStatusCodes []int            `json:"retriableStatusCodes"`
	RequestId            *types.RequestId `json:"requestId"`
}

type A2AClient struct {
	ID         string
	port       int
	httpClient *http.Client
	ht         transport.IHTTPTransportIntercept
	client     *goa2aclient.A2AClient
}

func NewA2AClient(port int, clientId string, h2, tls bool, authority string) *A2AClient {
	c := transport.CreateHTTPClient(clientId, h2, true, tls, authority, 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	ac := &A2AClient{
		ID:         clientId,
		port:       port,
		httpClient: c.HTTP(),
	}
	if ht, ok := c.Transport().(*transport.HTTPTransportIntercept); ok {
		ac.ht = ht
	} else if ht, ok := c.Transport().(*transport.HTTP2TransportIntercept); ok {
		ac.ht = ht
	}
	return ac
}

func (ac *A2AClient) newSession(ctx context.Context, port int, callerId, authority string, card *goa2aserver.AgentCard, call *AgentCall, inHeaders http.Header, timeline *timeline.Timeline) *A2ASession {
	return newSession(ac, ctx, port, callerId, authority, card, call, inHeaders, timeline)
}

func FetchAgentCard(ctx context.Context, url, authority string, call *AgentCall, inHeaders http.Header) (card *goa2aserver.AgentCard, err error) {
	port := util.GetContextPort(ctx)
	client := NewA2AClient(port, "", call.H2, call.TLS, call.Authority)
	card, err = client.loadAgentCard(ctx, url, authority, call, inHeaders)
	return
}

func CallAgent(ctx context.Context, port int, call *AgentCall, callback AgentResultsCallback, inHeaders http.Header) (err error) {
	return CallAgentWithTimeline(ctx, port, call, callback, inHeaders, timeline.NewTimeline(port, call.Name, nil, nil, inHeaders, nil, nil, nil))
}

func CallAgentWithTimeline(ctx context.Context, port int, call *AgentCall, callback AgentResultsCallback, inHeaders http.Header, timeline *timeline.Timeline) (err error) {
	var card *goa2aserver.AgentCard
	if call == nil || call.CardURL == "" {
		return fmt.Errorf("Missing Agent Call spec/URL: %+v", call)
	}
	card, err = FetchAgentCard(ctx, call.CardURL, call.Authority, call, inHeaders)
	if err != nil || card == nil {
		return fmt.Errorf("Error fetching agent card from url [%s], authority [%s]: %s", call.AgentURL, call.Authority, err.Error())
	}
	var agentURL string
	if call.AgentURL != "" {
		agentURL = call.AgentURL
	} else {
		agentURL = card.URL
	}
	call.AgentURL = agentURL
	session := NewA2ASessionWithTimeline(ctx, port, card, call, inHeaders, timeline)
	err = session.Connect()
	if err != nil {
		return fmt.Errorf("Failed to load agent card with error [%s]. Agent Call: %+v", err.Error(), call)
	}
	return session.CallAgent(callback, nil, nil)
}

func (ac *A2AClient) loadAgentCard(ctx context.Context, url, authority string, call *AgentCall, inHeaders http.Header) (card *goa2aserver.AgentCard, err error) {
	if url == "" {
		url = call.CardURL
	}
	if url == "" {
		url = call.AgentURL
	}
	if authority == "" {
		authority = call.Authority
	}
	url = util.FixURL(url, ".well-known/agent.json", false)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request wtih error: %w", err)
	}
	if authority != "" {
		req.Host = authority
	}
	resp, err := ac.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent card: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Agent card request failed with status code: %d", resp.StatusCode)
	}
	card = &goa2aserver.AgentCard{}
	rr := util.CreateOrGetReReader(resp.Body)
	if err := json.NewDecoder(rr).Decode(card); err != nil {
		rr.Rewind()
		return nil, fmt.Errorf("failed to parse agent card: %s. Body: %s", err.Error(), string(rr.Content))
	}
	return card, nil
}

func (ac *A2AClient) ConnectWithAgentCard(ctx context.Context, call *AgentCall, cardURL, authority string, inHeaders http.Header, timeline *timeline.Timeline) (*A2ASession, error) {
	card, err := ac.loadAgentCard(ctx, cardURL, authority, call, inHeaders)
	if err != nil {
		return nil, err
	}
	session := ac.newSession(ctx, ac.port, ac.ID, authority, card, call, inHeaders, timeline)
	session.inHeaders = inHeaders
	err = session.Connect()
	return session, err
}

func (ac *AgentCall) CloneWithUpdate(name, url, authority, message string, data map[string]any) *AgentCall {
	clone := *ac
	if name != "" {
		clone.Name = name
	}
	if url != "" {
		clone.AgentURL = url
	}
	if authority != "" {
		clone.Authority = authority
	}
	if message != "" {
		clone.Message = message
	}
	if data != nil {
		clone.Data = data
	}
	return &clone
}

func (ac *AgentCall) NonNil() {
	if ac.Data == nil {
		ac.Data = map[string]any{}
	}
	if ac.Headers == nil {
		ac.Headers = types.NewHeaders()
	}
	ac.Headers.NonNil()
}
