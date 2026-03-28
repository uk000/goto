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
	"errors"
	"fmt"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	goa2aclient "trpc.group/trpc-go/trpc-a2a-go/client"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	goa2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

type AgentResultsCallback func(key, output string)

type AgentCall struct {
	Name                 string           `json:"name,omitempty"`
	AgentURL             string           `json:"agentURL,omitempty"`
	CardURL              string           `json:"cardURL,omitempty"`
	Authority            string           `json:"authority,omitempty"`
	H2                   bool             `json:"h2,omitempty"`
	TLS                  bool             `json:"tls,omitempty"`
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

type A2AClientSession struct {
	ctx              context.Context
	port             int
	callerId         string
	client           *A2AClient
	Card             *goa2aserver.AgentCard
	url              string
	authority        string
	call             *AgentCall
	inInput          string
	outInput         string
	inHeaders        http.Header
	outHeaders       *types.Headers
	ResponseHeaders  http.Header
	callback         AgentResultsCallback
	localProgress    chan *types.Pair[string, any]
	upstreamProgress chan string
	resultChan       chan *types.Pair[string, any]
	inputParts       []a2aproto.Part
	err              error
}

var (
	AgentCards = map[string]*goa2aserver.AgentCard{}
	lock       = sync.RWMutex{}
)

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

func NewA2ASession(ctx context.Context, port int, card *goa2aserver.AgentCard, call *AgentCall, requestHeaders http.Header) *A2AClientSession {
	client := NewA2AClient(port, card.Name, call.H2, call.TLS, call.Authority)
	return client.NewSession(ctx, card, call, requestHeaders)
}

func GetAgentCard(name string) *goa2aserver.AgentCard {
	lock.RLock()
	defer lock.RUnlock()
	return AgentCards[name]
}

func FetchAgentCard(ctx context.Context, url, authority string, call *AgentCall, requestHeaders http.Header) (card *goa2aserver.AgentCard, err error) {
	port := util.GetContextPort(ctx)
	client := NewA2AClient(port, "", call.H2, call.TLS, call.Authority)
	session, err := client.loadAgentCard(ctx, url, authority, call, requestHeaders)
	if err != nil {
		return nil, err
	}
	return session.Card, nil
}

func CallAgent(ctx context.Context, port int, call *AgentCall, callback AgentResultsCallback, requestHeaders http.Header) (err error) {
	var card *goa2aserver.AgentCard
	if call.Name != "" {
		card = GetAgentCard(call.Name)
	}
	if card == nil {
		if call.CardURL == "" {
			return fmt.Errorf("Agent card not loaded and missing agent card URL in the given call spec: %+v", call)
		}
		card, err = FetchAgentCard(ctx, call.CardURL, call.Authority, call, requestHeaders)
		if err != nil || card == nil {
			return fmt.Errorf("Error fetching agent card from url [%s], authority [%s]: %s", call.AgentURL, call.Authority, err.Error())
		}
	}
	var agentURL string
	if call.AgentURL != "" {
		agentURL = call.AgentURL
	} else {
		agentURL = card.URL
	}
	call.AgentURL = agentURL
	session := NewA2ASession(ctx, port, card, call, requestHeaders)
	err = session.Connect()
	if err != nil {
		return fmt.Errorf("Failed to load agent card with error [%s]. Agent Call: %+v", err.Error(), call)
	}
	return session.CallAgent(callback, nil, nil, nil)
}

func (ac *A2AClient) loadAgentCard(ctx context.Context, url, authority string, call *AgentCall, requestHeaders http.Header) (session *A2AClientSession, err error) {
	if url == "" {
		url = call.CardURL
	}
	if url == "" {
		url = call.AgentURL
	}
	if authority == "" {
		authority = call.Authority
	}
	if !strings.HasSuffix(url, ".well-known/agent.json") {
		if !strings.HasSuffix(url, "/") {
			url += "/"
		}
		url += ".well-known/agent.json"
	}
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
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
	card := &goa2aserver.AgentCard{}
	rr := util.CreateOrGetReReader(resp.Body)
	if err := json.NewDecoder(rr).Decode(card); err != nil {
		rr.Rewind()
		return nil, fmt.Errorf("failed to parse agent card: %s. Body: %s", err.Error(), string(rr.Content))
	}
	lock.Lock()
	AgentCards[card.Name] = card
	lock.Unlock()
	session = ac.newSession(ctx, ac.port, ac.ID, authority, card, call, requestHeaders)
	return
}

func (ac *A2AClient) NewSession(ctx context.Context, card *goa2aserver.AgentCard, call *AgentCall, requestHeaders http.Header) *A2AClientSession {
	return ac.newSession(ctx, ac.port, ac.ID, call.Authority, card, call, requestHeaders)
}

func (ac *A2AClient) ConnectWithAgentCard(ctx context.Context, call *AgentCall, cardURL, authority string, requestHeaders http.Header) (*A2AClientSession, error) {
	session, err := ac.loadAgentCard(ctx, cardURL, authority, call, requestHeaders)
	if err != nil {
		return nil, err
	}
	session.inHeaders = requestHeaders
	err = session.Connect()
	return session, err
}

func (ac *A2AClient) newSession(ctx context.Context, port int, callerId, authority string, card *goa2aserver.AgentCard, call *AgentCall, requestHeaders http.Header) *A2AClientSession {
	call.NonNil()
	return &A2AClientSession{
		ctx:        ctx,
		port:       port,
		callerId:   callerId,
		authority:  authority,
		client:     ac,
		Card:       card,
		call:       call,
		inHeaders:  requestHeaders,
		outHeaders: call.Headers,
	}
}

func (acs *A2AClientSession) Connect() error {
	acs.url = acs.call.AgentURL
	if acs.url == "" {
		acs.url = acs.Card.URL
	}
	c, err := goa2aclient.NewA2AClient(acs.url, goa2aclient.WithHTTPClient(acs.client.httpClient),
		goa2aclient.WithHTTPReqHandler(acs), goa2aclient.WithUserAgent(acs.callerId))
	if err != nil {
		return err
	}
	acs.client.client = c
	return nil
}

func (acs *A2AClientSession) CallAgent(callback AgentResultsCallback, resultChan, localProgress chan *types.Pair[string, any], upstreamProgress chan string) (err error) {
	return acs.invokeAgent(acs.call.Message, acs.call.Data, callback, resultChan, localProgress, upstreamProgress)
}

func (acs *A2AClientSession) invokeAgent(input string, data map[string]any, callback AgentResultsCallback, resultChan, localProgress chan *types.Pair[string, any], upstreamProgress chan string) (err error) {
	if input == "" {
		input = acs.call.Message
	}
	if data == nil {
		data = acs.call.Data
	}
	inputParts := buildInputParts(input, data)
	acs.update(callback, resultChan, localProgress, upstreamProgress, inputParts)
	if acs.Card.Capabilities.Streaming != nil && *acs.Card.Capabilities.Streaming {
		err = acs.InvokeStream()
	} else {
		err = acs.InvokeUnary()
	}
	return
}

func (acs *A2AClientSession) update(callback AgentResultsCallback, resultChan, localProgress chan *types.Pair[string, any], upstreamProgress chan string, inputParts []a2aproto.Part) {
	acs.callback = callback
	acs.resultChan = resultChan
	acs.localProgress = localProgress
	acs.upstreamProgress = upstreamProgress
	acs.inputParts = inputParts
}

func (acs *A2AClientSession) InvokeUnary() error {
	results, err := acs.SendMessage()
	if err != nil {
		return err
	}
	for requestID, result := range results {
		acs.processResponse(requestID, result)
	}
	return nil
}

func (acs *A2AClientSession) SendMessage() (map[string]*a2aproto.MessageResult, error) {
	requestCount := acs.call.RequestCount
	if requestCount == 0 {
		requestCount = 1
	}
	concurrent := acs.call.Concurrent
	if concurrent == 0 {
		concurrent = 1
	}
	rounds := requestCount / concurrent
	acs.sendLocalProgress(acs.callerId, fmt.Sprintf("[%s] Will send %d requests in %d rounds with concurrency %d to agent %s\n", acs.callerId, requestCount, rounds, concurrent, acs.call.AgentURL))
	results := map[string]*a2aproto.MessageResult{}
	for i := 0; i < rounds; i++ {
		requestID := fmt.Sprintf("[%s] Request#[%d]", acs.call.Name, i)
		result, err := acs.client.client.SendMessage(acs.ctx, a2aproto.SendMessageParams{
			Message: a2aproto.NewMessage(a2aproto.MessageRoleUser, acs.inputParts),
		})
		if err != nil {
			return nil, err
		}
		results[requestID] = result
	}
	return results, nil
}

func buildInputParts(text string, data any) []a2aproto.Part {
	parts := []a2aproto.Part{}
	if text != "" {
		parts = append(parts, a2aproto.NewTextPart(text))
	}
	if data != nil {
		parts = append(parts, a2aproto.NewDataPart(data))
	}
	return parts
}

func (acs *A2AClientSession) InvokeStream() error {
	//** set push config, by getting task id from somewhere
	return acs.Stream()
}

func (acs *A2AClientSession) Stream() error {
	requestCount := acs.call.RequestCount
	if requestCount == 0 {
		requestCount = 1
	}
	concurrent := acs.call.Concurrent
	if concurrent == 0 {
		concurrent = 1
	}
	rounds := requestCount / concurrent
	acs.sendLocalProgress(acs.callerId, fmt.Sprintf("[%s] Will send %d requests in %d rounds with concurrency %d to agent %s\n", acs.callerId, requestCount, rounds, concurrent, acs.call.AgentURL))
	for i := 0; i < rounds; i++ {
		requestID := fmt.Sprintf("[%s] Request#[%d]", acs.call.Name, i)
		eventChan, err := acs.client.client.StreamMessage(acs.ctx, a2aproto.SendMessageParams{
			Message: a2aproto.NewMessage(a2aproto.MessageRoleUser, acs.inputParts),
		})
		if err != nil {
			return err
		}
		acs.processStreamResponse(requestID, eventChan)
	}
	return acs.err
}

func (acs *A2AClientSession) setPushConfig(taskID, url string) error {
	pushConfig := a2aproto.PushNotificationConfig{
		URL: url,
	}
	taskPushConfig := a2aproto.TaskPushNotificationConfig{
		TaskID:                 taskID,
		PushNotificationConfig: pushConfig,
	}
	result, err := acs.client.client.SetPushNotification(acs.ctx, taskPushConfig)
	if err != nil {
		return err
	}
	log.Printf("Push notification config set successfully: %+v\n", result)
	return nil
}

func (acs *A2AClientSession) processStreamResponse(id string, eventChan <-chan a2aproto.StreamingMessageEvent) {
	for {
		select {
		case <-acs.ctx.Done():
			log.Printf("ERROR: Context timeout or cancellation while waiting for stream events: %v", acs.ctx.Err())
			return
		case event, ok := <-eventChan:
			if !ok {
				log.Println("Stream channel closed.")
				if acs.ctx.Err() != nil {
					log.Printf("Context error after stream close: %v", acs.ctx.Err())
				}
				return
			}
			acs.processEventResult(id, &event)
		}
	}
}

func (acs *A2AClientSession) processResponse(id string, result *a2aproto.MessageResult) {
	switch r := result.Result.(type) {
	case *a2aproto.Message:
		acs.processParts(id, r.Parts)
	case *a2aproto.Task:
		acs.sendResponse(id, fmt.Sprintf("Task %s State: %s @ %s\n", r.ID, r.Status.State, r.Status.Timestamp), nil)
		if r.Status.Message != nil {
			acs.processParts(id, r.Status.Message.Parts)
		}
	default:
		acs.sendResponse(id, fmt.Sprintf("Task %s Received unknown message type: %T\n", r.GetKind(), r), r)
	}
}

func (acs *A2AClientSession) processEventResult(id string, event *a2aproto.StreamingMessageEvent) {
	switch e := event.Result.(type) {
	case *a2aproto.Message:
		acs.processParts(id, e.Parts)
	case *a2aproto.Task:
		acs.sendResponse(id, fmt.Sprintf("Task %s State: %s @ %s\n", e.ID, e.Status.State, e.Status.Timestamp), nil)
		if e.Status.Message != nil {
			acs.processParts(id, e.Status.Message.Parts)
		}
	case *a2aproto.TaskStatusUpdateEvent:
		if e.Status.Message != nil {
			acs.processParts(id, e.Status.Message.Parts)
		}
		text := []string{}
		for _, p := range e.Status.Message.Parts {
			if t, ok := p.(*a2aproto.TextPart); ok {
				text = append(text, t.Text)
			}
		}
		msg := fmt.Sprintf("Agent: %s, Timestamp: %s\n", acs.callerId, e.Status.Timestamp)
		msg2 := ""
		if e.Status.State == a2aproto.TaskStateInputRequired {
			msg2 = ", [Additional input required]"
		} else if e.Final {
			msg2 = fmt.Sprintf(", Final status received: %s", e.Status.State)
			switch e.Status.State {
			case a2aproto.TaskStateCompleted:
				msg2 = " [Task completed successfully] \U0001F4AF"
			case a2aproto.TaskStateFailed:
				msg2 = " [Task failed] \u274C"
				acs.err = errors.New(msg + msg2)
			case a2aproto.TaskStateCanceled:
				msg2 = " [Task was canceled] \u2716"
			}
		}
		if msg2 != "" {
			acs.sendResponse(id, msg+msg2, nil)
		}
	case *a2aproto.TaskArtifactUpdateEvent:
		acs.processParts(id, e.Artifact.Parts)
		if e.LastChunk != nil && *e.LastChunk {
			acs.sendResponse(id, fmt.Sprintf("Task %s Final Artifact Received: ID [%s], Name: [%s], \n", e.TaskID, e.Artifact.ArtifactID, *e.Artifact.Name), e.Artifact)
		} else {
			acs.sendResponse(id, fmt.Sprintf("Task %s Artifact Update: ID [%s], Name: [%s], \n", e.TaskID, e.Artifact.ArtifactID, *e.Artifact.Name), e.Artifact)
		}
	default:
		acs.sendResponse(id, fmt.Sprintf("Task %s Received unknown event type: %T\n", e.GetKind(), event.Result), event.Result)
	}
}

func (acs *A2AClientSession) processParts(id string, parts []a2aproto.Part) {
	for _, p := range parts {
		var part any = p
		switch p := part.(type) {
		case *a2aproto.TextPart:
			acs.sendResponse(id, p.Text, nil)
		case a2aproto.TextPart:
			acs.sendResponse(id, p.Text, nil)
		case *a2aproto.DataPart:
			acs.sendResponse(id, "", p.Data)
		case map[string]interface{}:
			textHandled := false
			if typeStr, ok := p["type"].(string); ok && typeStr == "text" {
				if text, ok := p["text"].(string); ok {
					acs.sendResponse(id, text, nil)
					textHandled = true
				}
			}
			if !textHandled {
				acs.sendResponse(id, "", p)
			}
		default:
			acs.sendResponse(id, "", p)
		}
	}
}

func (acs *A2AClientSession) sendLocalProgress(id, text string) {
	if text != "" && acs.localProgress != nil {
		acs.localProgress <- types.NewPair[string, any](id, text)
	}
}

func (acs *A2AClientSession) sendResponse(id, text string, data any) {
	if acs.callback != nil {
		acs.callback(id, text)
		if data != nil {
			acs.callback(id, util.ToJSONText(data))
		}
	}
	if text != "" && data == nil && acs.upstreamProgress != nil {
		acs.upstreamProgress <- text
	} else if data != nil && acs.resultChan != nil {
		key := fmt.Sprintf("%s:%s", id, text)
		if acs.Card != nil {
			key = fmt.Sprintf("%s/%s", acs.Card.Name, key)
		} else if acs.call != nil {
			key = fmt.Sprintf("%s/%s", acs.call.Name, key)
		} else {
			key = fmt.Sprintf("%s/%s", acs.client.ID, key)
		}
		acs.resultChan <- types.NewPair(key, data)
	}
}

func (acs *A2AClientSession) Handle(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var err error
	var resp *http.Response
	defer func() {
		if err != nil && resp != nil {
			resp.Body.Close()
		}
	}()

	if client == nil {
		return nil, fmt.Errorf("a2aClient.httpRequestHandler: http client is nil")
	}
	acs.updateRequestHeaders(req)
	resp, err = client.Do(req)
	acs.updateResponseHeaders(resp)
	if err != nil {
		return nil, fmt.Errorf("a2aClient.httpRequestHandler: http request failed: %w", err)
	}

	return resp, nil
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

func (acs *A2AClientSession) updateRequestHeaders(r *http.Request) {
	if acs.outHeaders.Request != nil {
		acs.outHeaders.Request.UpdateHeaders(r.Header, fmt.Sprintf("A2A client request for caller %s", acs.callerId))
	}
	if len(r.Header["Host"]) > 0 {
		r.Host = r.Header["Host"][0]
	}
	log.Printf("---------- A2A client request headers for %s ------------\n", acs.callerId)
	log.Println(util.ToJSONText(r.Header))
	clientInfo := util.BuildGotoClientInfo(nil, acs.port, acs.callerId, acs.callerId, acs.call.Name, acs.url, acs.authority,
		acs.inInput, acs.outInput, acs.inHeaders, r.Header, acs.call.Headers.Request.Forward, acs.call.Headers.Request.Add, acs.call.Headers.Request.Remove, nil)
	acs.sendResponse("", "", clientInfo)
}

func (acs *A2AClientSession) updateResponseHeaders(r *http.Response) {
	if r != nil {
		if acs.outHeaders.Response != nil {
			acs.outHeaders.Response.UpdateHeaders(r.Header, fmt.Sprintf("A2A client response for caller %s", acs.callerId))
		}
		log.Printf("---------- A2A client response headers for %s ------------\n", acs.callerId)
		log.Println(util.ToJSONText(r.Header))
		acs.ResponseHeaders = r.Header
	}
}
