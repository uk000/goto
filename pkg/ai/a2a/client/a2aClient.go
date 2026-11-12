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
	timelinepkg "goto/pkg/util/timeline"
	"log"
	"net/http"
	"slices"
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
	Delay                string           `json:"delay,omitempty"`
	Message              string           `json:"message,omitempty"`
	Args                 map[string]any   `json:"args,omitempty"`
	Headers              *types.Headers   `json:"headers,omitempty"`
	RequestCount         int              `json:"requestCount"`
	Concurrent           int              `json:"concurrent"`
	RequestDelay         string           `json:"requestDelay"`
	InitialDelay         string           `json:"initialDelay"`
	RetryDelay           string           `json:"retryDelay"`
	RequestTimeout       string           `json:"requestTimeout"`
	RetriableStatusCodes []int            `json:"retriableStatusCodes"`
	RequestId            *types.RequestId `json:"requestId"`
	ForcedStatus         int              `json:"forcedStatus"`
	ResultOnly           bool             `json:"resultOnly,omitempty"`
	NoEvents             bool             `json:"noEvents,omitempty"`
	NoCallDetails        bool             `json:"noCallDetails,omitempty"`
	Stream               bool             `json:"stream,omitempty"`
	RequestTimeoutD      time.Duration    `json:"-"`
}

type AgentError struct {
	error
	StatusCode int
}

type A2AClient struct {
	ID         string
	port       int
	httpClient *http.Client
	ht         transport.IHTTPTransportIntercept
	client     *goa2aclient.A2AClient
}

// NewA2AClient creates an A2AClient with an HTTP client for the given port, protocol and authority, defaulting the request timeout to 1 hour.
func NewA2AClient(port int, clientId string, h2, tls bool, authority string, requestTimeout time.Duration) *A2AClient {
	if requestTimeout == 0 {
		requestTimeout = 1 * time.Hour
	}
	c := transport.CreateHTTPClient(port, clientId, h2, true, tls, false, authority,
		requestTimeout, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	ac := &A2AClient{
		ID:         clientId,
		port:       port,
		httpClient: c.HTTP(),
	}
	if ht, ok := c.Transport().(*transport.HTTPTransportIntercept); ok {
		ac.ht = ht
	}
	return ac
}

// newSession creates an A2ASession bound to this client for the given agent card and call.
func (ac *A2AClient) newSession(ctx context.Context, port int, callerId, authority string, card *goa2aserver.AgentCard, call *AgentCall, inHeaders http.Header, timeline *timeline.Timeline) *A2ASession {
	return newSession(ac, ctx, port, callerId, authority, card, call, inHeaders, timeline)
}

// FetchAgentCard fetches the card of the agent at the URL. The call is optional, it tells how to reach the agent.
func FetchAgentCard(ctx context.Context, url, authority string, call *AgentCall, inHeaders http.Header) (card *goa2aserver.AgentCard, err error) {
	port := util.GetContextPort(ctx)
	var h2, tls bool
	var callAuthority string
	var requestTimeout time.Duration
	if call != nil {
		h2, tls, callAuthority, requestTimeout = call.H2, call.TLS, call.Authority, call.RequestTimeoutD
	}
	client := NewA2AClient(port, "", h2, tls, callAuthority, requestTimeout)
	card, err = client.loadAgentCard(ctx, url, authority, call, inHeaders)
	return
}

// CallAgent calls the agent described by the call using a new timeline, and fails if the call is missing.
func CallAgent(ctx context.Context, port int, call *AgentCall, callback AgentResultsCallback, inHeaders http.Header) (*A2AResult, error) {
	if call == nil {
		return nil, fmt.Errorf("Missing Agent Call spec/URL: %+v", call)
	}
	return CallAgentWithTimeline(ctx, port, call, callback, inHeaders, timeline.NewTimeline(port, call.Name, nil, nil, inHeaders, nil, nil, nil))
}

// CallAgentWithTimeline fetches the agent card from the call's card URL, connects a session, sends the call's message and returns the result along with any *AgentError.
func CallAgentWithTimeline(ctx context.Context, port int, call *AgentCall, callback AgentResultsCallback, inHeaders http.Header, timeline *timeline.Timeline) (result *A2AResult, err error) {
	var card *goa2aserver.AgentCard
	if call == nil || call.CardURL == "" {
		return nil, fmt.Errorf("Missing Agent Call spec/URL: %+v", call)
	}
	if timeline == nil {
		timeline = timelinepkg.NewTimeline(port, call.Name, nil, nil, inHeaders, nil, nil, nil)
	}
	card, err = FetchAgentCard(ctx, call.CardURL, call.Authority, call, inHeaders)
	if !util.IsNil(err) || card == nil {
		reason := "no card"
		if !util.IsNil(err) {
			reason = err.Error()
		}
		return nil, fmt.Errorf("Error fetching agent card from url [%s], authority [%s]: %s", call.CardURL, call.Authority, reason)
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
	if !util.IsNil(err) {
		return nil, fmt.Errorf("Failed to load agent card with error [%s]. Agent Call: %+v", err.Error(), call)
	}
	result = session.Result
	// The error of the call is a *AgentError, which must not make a nil error of the interface when there is none.
	if agentErr := session.CallAgent(call.Message, call.Args, callback, nil, nil); agentErr != nil {
		return result, agentErr
	}
	return result, nil
}

// loadAgentCard fetches and decodes the agent's .well-known/agent.json card, falling back to the call's URL and authority and forwarding the configured headers.
func (ac *A2AClient) loadAgentCard(ctx context.Context, url, authority string, call *AgentCall, inHeaders http.Header) (card *goa2aserver.AgentCard, err error) {
	if call != nil {
		if url == "" {
			url = call.CardURL
		}
		if url == "" {
			url = call.AgentURL
		}
		if authority == "" {
			authority = call.Authority
		}
	}
	url = util.FixURL(url, ".well-known/agent.json", false)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if !util.IsNil(err) {
		return nil, fmt.Errorf("failed to create request wtih error: %w", err)
	}
	if authority != "" {
		req.Host = authority
	}
	if call != nil && call.Headers != nil && call.Headers.HasForwardHeaders() {
		types.ForwardHeaders(inHeaders, req.Header, slices.Values(call.Headers.Request.Forward))
	}
	log.Printf("---------- Outbound request headers from A2A client to load Agent Card from %s ------------\n", url)
	log.Println(util.ToJSONText(req.Header))
	resp, err := ac.httpClient.Do(req)
	if !util.IsNil(err) {
		return nil, fmt.Errorf("failed to fetch agent card: %w", err)
	}
	log.Printf("---------- Response headers received by A2A client for Agent Card from %s ------------\n", url)
	log.Println(util.ToJSONText(resp.Header))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Agent card request failed with status code: %d", resp.StatusCode)
	}
	card = &goa2aserver.AgentCard{}
	rr := util.CreateOrGetReReader(resp.Body)
	if err := json.NewDecoder(rr).Decode(card); !util.IsNil(err) {
		rr.Rewind()
		return nil, fmt.Errorf("failed to parse agent card: %s. Body: %s", err.Error(), string(rr.Content))
	}
	return card, nil
}

// ConnectWithAgentCard loads the agent card from the given URL and returns a connected session for it.
func (ac *A2AClient) ConnectWithAgentCard(ctx context.Context, call *AgentCall, cardURL, authority string, inHeaders http.Header, timeline *timeline.Timeline) (*A2ASession, error) {
	card, err := ac.loadAgentCard(ctx, cardURL, authority, call, inHeaders)
	if !util.IsNil(err) {
		return nil, err
	}
	session := ac.newSession(ctx, ac.port, ac.ID, authority, card, call, inHeaders, timeline)
	session.inHeaders = inHeaders
	err = session.Connect()
	return session, err
}

// Prepare parses RequestTimeout into RequestTimeoutD, defaulting to 10 hours when it is not set.
func (ac *AgentCall) Prepare() {
	if ac.RequestTimeout != "" {
		ac.RequestTimeoutD = util.ParseDuration(ac.RequestTimeout)
	} else {
		ac.RequestTimeoutD = 10 * time.Hour
	}
}

// CloneWithUpdate returns a copy of the call with the given non-empty name, URL, authority, message and args overriding the original.
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
		clone.Args = data
	}
	return &clone
}

// NonNil initializes the nil Args and Headers of the call so that they are safe to use.
func (ac *AgentCall) NonNil() {
	if ac.Args == nil {
		ac.Args = map[string]any{}
	}
	if ac.Headers == nil {
		ac.Headers = types.NewHeaders()
	}
	ac.Headers.NonNil()
}
