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
	"goto/pkg/global"
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

type AgentCall struct {
	Name      string         `json:"name,omitempty"`
	AgentURL  string         `json:"agentURL,omitempty"`
	CardURL   string         `json:"cardURL,omitempty"`
	Authority string         `json:"authority,omitempty"`
	TLS       bool           `json:"tls,omitempty"`
	Delay     string         `json:"delay,omitempty"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Headers   *types.Headers `json:"headers,omitempty"`
}

type A2AClient struct {
	ID         string
	port       int
	httpClient *http.Client
	ht         transport.ClientTransport
	client     *goa2aclient.A2AClient
}

type A2AClientSession struct {
	ctx          context.Context
	port         int
	callerId     string
	client       *A2AClient
	Card         *goa2aserver.AgentCard
	url          string
	authority    string
	call         *AgentCall
	inInput      string
	outInput     string
	inHeaders    http.Header
	outHeaders   *types.Headers
	callback     func(output string)
	progressChan chan string
	resultChan   chan *types.Pair[string, any]
	inputParts   []a2aproto.Part
	err          error
}

var (
	AgentCards = map[string]*goa2aserver.AgentCard{}
	lock       = sync.RWMutex{}
)

func NewA2AClient(port int, clientId string, isTLS bool, authority string) *A2AClient {
	id := fmt.Sprintf("%s[%s]", clientId, global.Funcs.GetListenerLabelForPort(port))
	ht := transport.CreateHTTPClient(id, true, true, isTLS, authority, 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	return &A2AClient{
		ID:         id,
		port:       port,
		httpClient: ht.HTTP(),
		ht:         ht,
	}
}

func NewA2ASession(ctx context.Context, port int, card *goa2aserver.AgentCard, call *AgentCall) *A2AClientSession {
	client := NewA2AClient(port, card.Name, call.TLS, call.Authority)
	return client.NewSession(ctx, card, call)
}

func GetAgentCard(name string) *goa2aserver.AgentCard {
	lock.RLock()
	defer lock.RUnlock()
	return AgentCards[name]
}

func FetchAgentCard(ctx context.Context, url, authority string, call *AgentCall) (card *goa2aserver.AgentCard, err error) {
	port := util.GetContextPort(ctx)
	client := NewA2AClient(port, "", call.TLS, call.Authority)
	session, err := client.loadAgentCard(ctx, url, authority, call)
	if err != nil {
		return nil, err
	}
	return session.Card, nil
}

func CallAgent(ctx context.Context, port int, call *AgentCall, callback func(output string)) (err error) {
	var card *goa2aserver.AgentCard
	if call.Name != "" {
		card = GetAgentCard(call.Name)
	}
	if card == nil {
		if call.CardURL == "" {
			return fmt.Errorf("Agent card not loaded and missing agent card URL in the given call spec: %+v", call)
		}
		card, err = FetchAgentCard(ctx, call.CardURL, call.Authority, call)
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
	session := NewA2ASession(ctx, port, card, call)
	err = session.Connect()
	if err != nil {
		return fmt.Errorf("Failed to load agent card with error [%s]. Agent Call: %+v", err.Error(), call)
	}
	return session.CallAgent(callback, nil, nil)
}

func (ac *A2AClient) LoadAgentCard(ctx context.Context, call *AgentCall) (session *A2AClientSession, err error) {
	return ac.loadAgentCard(ctx, "", "", call)
}

func (ac *A2AClient) loadAgentCard(ctx context.Context, url, authority string, call *AgentCall) (session *A2AClientSession, err error) {
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
	session = ac.newSession(ctx, ac.port, ac.ID, authority, card, call)
	return
}

func (ac *A2AClient) NewSession(ctx context.Context, card *goa2aserver.AgentCard, call *AgentCall) *A2AClientSession {
	return ac.newSession(ctx, ac.port, ac.ID, call.Authority, card, call)
}

func (ac *A2AClient) ConnectWithAgentCard(ctx context.Context, call *AgentCall, agentURL string) (*A2AClientSession, error) {
	session, err := ac.LoadAgentCard(ctx, call)
	if err != nil {
		return nil, err
	}
	if agentURL != "" {
		call.AgentURL = agentURL
	}
	err = session.Connect()
	return session, err
}

func (ac *A2AClient) newSession(ctx context.Context, port int, callerId, authority string, card *goa2aserver.AgentCard, call *AgentCall) *A2AClientSession {
	call.NonNil()
	return &A2AClientSession{
		ctx:        ctx,
		port:       port,
		callerId:   callerId,
		authority:  authority,
		client:     ac,
		Card:       card,
		call:       call,
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

// func (ac *A2AClient) CallAgent(ctx context.Context, port int, callerId, name, input string, data map[string]any, callback func(output string), call *AgentCall, resultChan chan *types.Pair[string, any], progressChan chan string) error {
// 	if call != nil {
// 		name = call.Name
// 		input = call.Message
// 		data = call.Data
// 	}
// 	lock.RLock()
// 	card := AgentCards[name]
// 	lock.RUnlock()
// 	if card == nil {
// 		return errors.New("agent not found")
// 	}
// 	session, err := ac.Connect(ctx, port, callerId, card)
// 	if err != nil {
// 		return err
// 	}
// 	if call != nil && call.Delay != "" {
// 		delay := types.ParseDelay(call.Delay)
// 		if delay != nil {
// 			d := delay.Compute()
// 			if progressChan != nil {
// 				progressChan <- fmt.Sprintf("Agent Call [%s]: Delaying call by %s", name, d)
// 			}
// 			delay.Apply()
// 		}
// 	}
// 	return session.invokeAgent(input, data, callback, resultChan, progressChan)
// }

func (acs *A2AClientSession) CallAgent(callback func(output string), resultChan chan *types.Pair[string, any], progressChan chan string) (err error) {
	return acs.invokeAgent(acs.call.Message, acs.call.Data, callback, resultChan, progressChan)
}

func (acs *A2AClientSession) invokeAgent(input string, data map[string]any, callback func(output string), resultChan chan *types.Pair[string, any], progressChan chan string) (err error) {
	if input == "" {
		input = acs.call.Message
	}
	if data == nil {
		data = acs.call.Data
	}
	inputParts := buildInputParts(input, data)
	acs.update(callback, resultChan, progressChan, inputParts)
	clientInfo := util.BuildGotoClientInfo(nil, acs.port, acs.callerId, acs.callerId, acs.call.Name, acs.url, acs.authority,
		acs.inInput, acs.outInput, acs.inHeaders, acs.call.Headers.Request.Add, acs.call.Headers.Request.Forward, nil)
	acs.sendResponse("", clientInfo)
	if acs.Card.Capabilities.Streaming != nil && *acs.Card.Capabilities.Streaming {
		err = acs.InvokeStream()
	} else {
		err = acs.InvokeUnary()
	}
	return
}

func (acs *A2AClientSession) update(callback func(output string), resultChan chan *types.Pair[string, any], progressChan chan string, inputParts []a2aproto.Part) {
	acs.callback = callback
	acs.resultChan = resultChan
	acs.progressChan = progressChan
	acs.inputParts = inputParts
}

func (acs *A2AClientSession) InvokeUnary() error {
	result, err := acs.SendParts()
	if err != nil {
		return err
	}
	acs.processMessageResult(result)
	return nil
}

func (acs *A2AClientSession) InvokeStream() error {
	//** set push config, by getting task id from somewhere
	return acs.Stream()
}

func (acs *A2AClientSession) SendParts() (*a2aproto.MessageResult, error) {
	return acs.client.client.SendMessage(acs.ctx, a2aproto.SendMessageParams{
		Message: a2aproto.NewMessage(a2aproto.MessageRoleUser, acs.inputParts),
	})
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

func (acs *A2AClientSession) Stream() error {
	eventChan, err := acs.client.client.StreamMessage(acs.ctx, a2aproto.SendMessageParams{
		Message: a2aproto.NewMessage(a2aproto.MessageRoleUser, acs.inputParts),
	})
	if err != nil {
		return err
	}
	acs.processStreamResponse(eventChan)
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

func (acs *A2AClientSession) processStreamResponse(eventChan <-chan a2aproto.StreamingMessageEvent) {
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
			acs.processEventResult(&event)
		}
	}
}

func (acs *A2AClientSession) processMessageResult(result *a2aproto.MessageResult) {
	switch r := result.Result.(type) {
	case *a2aproto.Message:
		acs.processParts(r.Parts)
	case *a2aproto.Task:
		acs.sendResponse(fmt.Sprintf("Task %s State: %s @ %s\n", r.ID, r.Status.State, r.Status.Timestamp), nil)
		if r.Status.Message != nil {
			acs.processParts(r.Status.Message.Parts)
		}
	default:
		acs.sendResponse(fmt.Sprintf("Task %s Received unknown message type: %T\n", r.GetKind(), r), r)
	}
}

func (acs *A2AClientSession) processEventResult(event *a2aproto.StreamingMessageEvent) {
	switch e := event.Result.(type) {
	case *a2aproto.Message:
		acs.processParts(e.Parts)
	case *a2aproto.Task:
		acs.sendResponse(fmt.Sprintf("Task %s State: %s @ %s\n", e.ID, e.Status.State, e.Status.Timestamp), nil)
		if e.Status.Message != nil {
			acs.processParts(e.Status.Message.Parts)
		}
	case *a2aproto.TaskStatusUpdateEvent:
		if e.Status.Message != nil {
			acs.processParts(e.Status.Message.Parts)
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
				msg2 = " [Task completed successfully]"
			case a2aproto.TaskStateFailed:
				msg2 = " [Task failed]"
				acs.err = errors.New(msg + msg2)
			case a2aproto.TaskStateCanceled:
				msg2 = " [Task was canceled]"
			}
		}
		if msg2 != "" {
			acs.sendResponse(msg+msg2, nil)
		}
	case *a2aproto.TaskArtifactUpdateEvent:
		acs.processParts(e.Artifact.Parts)
		if e.LastChunk != nil && *e.LastChunk {
			acs.sendResponse(fmt.Sprintf("Task %s Final Artifact Received: ID [%s], Name: [%s], \n", e.TaskID, e.Artifact.ArtifactID, *e.Artifact.Name), e.Artifact)
		} else {
			acs.sendResponse(fmt.Sprintf("Task %s Artifact Update: ID [%s], Name: [%s], \n", e.TaskID, e.Artifact.ArtifactID, *e.Artifact.Name), e.Artifact)
		}
	default:
		acs.sendResponse(fmt.Sprintf("Task %s Received unknown event type: %T\n", e.GetKind(), event.Result), event.Result)
	}
}

func (acs *A2AClientSession) processParts(parts []a2aproto.Part) {
	for _, p := range parts {
		var part any = p
		switch p := part.(type) {
		case *a2aproto.TextPart:
			acs.sendResponse(p.Text, nil)
		case a2aproto.TextPart:
			acs.sendResponse(p.Text, nil)
		case *a2aproto.DataPart:
			acs.sendResponse("", p.Data)
		case map[string]interface{}:
			textHandled := false
			if typeStr, ok := p["type"].(string); ok && typeStr == "text" {
				if text, ok := p["text"].(string); ok {
					acs.sendResponse(text, nil)
					textHandled = true
				}
			}
			if !textHandled {
				acs.sendResponse("", p)
			}
		default:
			acs.sendResponse("", p)
		}
	}
}

func (acs *A2AClientSession) sendResponse(text string, data any) {
	if acs.callback != nil {
		acs.callback(text)
		if data != nil {
			acs.callback(util.ToJSONText(data))
		}
	}
	if text != "" && data == nil && acs.progressChan != nil {
		acs.progressChan <- text
	}
	if data != nil && acs.resultChan != nil {
		key := ""
		if text != "" {
			key = text
		}
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
	ac.Data = map[string]any{}
	ac.Headers = types.NewHeaders()
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
}

func (acs *A2AClientSession) updateResponseHeaders(r *http.Response) {
	if acs.outHeaders.Response != nil {
		acs.outHeaders.Response.UpdateHeaders(r.Header, fmt.Sprintf("A2A client response for caller %s", acs.callerId))
	}
	log.Printf("---------- A2A client response headers for %s ------------\n", acs.callerId)
	log.Println(util.ToJSONText(r.Header))
}
