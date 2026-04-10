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
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"log"
	"net/http"
	"slices"
	"strings"

	goa2aclient "trpc.group/trpc-go/trpc-a2a-go/client"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	goa2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

type A2ASession struct {
	ctx              context.Context
	port             int
	callerId         string
	client           *A2AClient
	clientInfo       *timeline.GotoClientInfo
	Card             *goa2aserver.AgentCard
	url              string
	authority        string
	call             *AgentCall
	inInput          string
	outInput         string
	inHeaders        http.Header
	outHeaders       *types.Headers
	ResponseHeaders  http.Header
	Result           *A2AResult
	callback         AgentResultsCallback
	localProgress    chan *types.Pair[string, any]
	upstreamProgress chan *types.Pair[string, any]
	inputParts       []a2aproto.Part
	timeline         *timeline.Timeline
	err              error
}

func NewA2ASession(ctx context.Context, port int, card *goa2aserver.AgentCard, call *AgentCall, inHeaders http.Header) *A2ASession {
	client := NewA2AClient(port, call.Name, call.H2, call.TLS, call.Authority)
	return newSession(client, ctx, client.port, client.ID, call.Authority, card, call, inHeaders, timeline.NewTimeline(port, call.Name, nil, nil, inHeaders, nil, nil, nil))
}

func NewA2ASessionWithTimeline(ctx context.Context, port int, card *goa2aserver.AgentCard, call *AgentCall, inHeaders http.Header, timeline *timeline.Timeline) *A2ASession {
	client := NewA2AClient(port, card.Name, call.H2, call.TLS, call.Authority)
	return newSession(client, ctx, client.port, client.ID, call.Authority, card, call, inHeaders, timeline)
}

func newSession(ac *A2AClient, ctx context.Context, port int, callerId, authority string, card *goa2aserver.AgentCard, call *AgentCall, inHeaders http.Header, timeline *timeline.Timeline) *A2ASession {
	call.NonNil()
	return &A2ASession{
		client:     ac,
		ctx:        ctx,
		port:       port,
		callerId:   callerId,
		authority:  authority,
		Card:       card,
		call:       call,
		inHeaders:  inHeaders,
		outHeaders: call.Headers,
		timeline:   timeline,
		Result:     NewA2AResult(call.AgentURL, call, timeline),
	}
}

func (acs *A2ASession) Connect() error {
	acs.url = acs.call.AgentURL
	if acs.url == "" {
		acs.url = acs.Card.URL
	}
	acs.url = util.FixURL(acs.url, "/", false)
	c, err := goa2aclient.NewA2AClient(acs.url, goa2aclient.WithHTTPClient(acs.client.httpClient),
		goa2aclient.WithHTTPReqHandler(acs), goa2aclient.WithUserAgent(acs.callerId))
	if err != nil {
		return err
	}
	acs.client.client = c
	return nil
}

func (acs *A2ASession) CallAgent(callback AgentResultsCallback, localProgress, upstreamProgress chan *types.Pair[string, any]) (err error) {
	input := acs.call.Message
	data := acs.call.Data
	data["resultOnly"] = acs.call.ResultOnly
	if input == "" {
		input = acs.call.Message
	}
	inputParts := buildInputParts(input, data)
	acs.configure(callback, localProgress, upstreamProgress, inputParts)
	if acs.Card.Capabilities.Streaming != nil && *acs.Card.Capabilities.Streaming {
		err = acs.InvokeStream()
	} else {
		err = acs.InvokeUnary()
	}
	return
}

func (acs *A2ASession) configure(callback AgentResultsCallback, localProgress, upstreamProgress chan *types.Pair[string, any], inputParts []a2aproto.Part) {
	acs.callback = callback
	acs.localProgress = localProgress
	acs.upstreamProgress = upstreamProgress
	acs.inputParts = inputParts
	acs.timeline.SetStreamPreferred(localProgress)
}

func (acs *A2ASession) InvokeUnary() error {
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
	errors := map[string]error{}
	acs.reportInitiateCall()
	for i := 1; i <= rounds; i++ {
		requestID := fmt.Sprintf("[%s] Request#[%d]", acs.call.AgentURL, i)
		cr := acs.Result.getOrAddCall(requestID)
		cr.ClientInfo = acs.clientInfo
		result, err := acs.client.client.SendMessage(acs.ctx, a2aproto.SendMessageParams{
			Message: a2aproto.NewMessage(a2aproto.MessageRoleUser, acs.inputParts),
		})
		if err != nil {
			errors[requestID] = err
		} else {
			results[requestID] = result
			acs.processResponse(requestID, result, cr)
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("%+v", errors)
	}
	return nil
}

func (acs *A2ASession) InvokeStream() error {
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
	acs.reportInitiateCall()
	for i := 0; i < rounds; i++ {
		requestID := fmt.Sprintf("[%s @ %s] [Request# %d]", acs.call.Name, acs.url, i)
		cr := acs.Result.getOrAddCall(requestID)
		cr.ClientInfo = acs.clientInfo
		ctx := context.WithValue(acs.ctx, constants.HeaderGotoA2ARequestID, requestID)
		eventChan, err := acs.client.client.StreamMessage(ctx, a2aproto.SendMessageParams{
			Message: a2aproto.NewMessage(a2aproto.MessageRoleUser, acs.inputParts),
		})
		if err != nil {
			return err
		}
		acs.processStreamResponse(requestID, eventChan, cr)
	}
	return acs.err
}

func (acs *A2ASession) Handle(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
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
	requestID := ""
	if v := req.Context().Value(constants.HeaderGotoA2ARequestID); v != nil {
		if s, ok := v.(string); ok {
			requestID = s
			req.Header.Add(constants.HeaderGotoA2ARequestID, requestID)
			req.Header.Add(constants.HeaderGotoA2AServer, acs.url)
			req.Header.Add(constants.HeaderGotoA2AAgent, acs.Card.Name)
		}
	}
	acs.Result.storeHeaders(requestID, req.Header, nil, 0)
	acs.clientInfo.StoreHeaders(req.Header)
	resp, err = client.Do(req)
	if resp != nil {
		acs.updateResponseHeaders(resp)
		acs.Result.storeHeaders(requestID, req.Header, resp.Header, resp.StatusCode)
	}
	if err != nil {
		return nil, fmt.Errorf("a2aClient.httpRequestHandler: http request failed: %w", err)
	}

	return resp, nil
}

func (acs *A2ASession) updateRequestHeaders(r *http.Request) {
	if acs.outHeaders.Request != nil {
		acs.outHeaders.Request.UpdateHeaders(r.Header, fmt.Sprintf("A2A client request for caller %s", acs.callerId))
		types.ForwardHeaders(acs.inHeaders, r.Header, slices.Values(acs.outHeaders.Request.Forward), acs.callerId)
	}
	if len(r.Header["Host"]) > 0 {
		r.Host = r.Header["Host"][0]
	}
	log.Printf("---------- Outbound request headers from A2A client [%s] to %s ------------\n", acs.callerId, acs.url)
	log.Println(util.ToJSONText(r.Header))
}

func (acs *A2ASession) updateResponseHeaders(r *http.Response) {
	if r != nil {
		if acs.outHeaders.Response != nil {
			acs.outHeaders.Response.UpdateHeaders(r.Header, fmt.Sprintf("A2A client response for caller %s", acs.callerId))
		}
		log.Printf("---------- Response headers received by A2A client [%s] from [%s] ------------\n", acs.callerId, acs.url)
		log.Println(util.ToJSONText(r.Header))
		acs.ResponseHeaders = r.Header
	}
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

func (acs *A2ASession) setPushConfig(taskID, url string) error {
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

func (acs *A2ASession) processStreamResponse(id string, eventChan <-chan a2aproto.StreamingMessageEvent, cr *A2ACallResult) {
	for {
		select {
		case <-acs.ctx.Done():
			msg := fmt.Sprintf("ERROR: Context timeout or cancellation while waiting for stream events: %v", acs.ctx.Err())
			acs.err = acs.ctx.Err()
			if acs.err == nil {
				acs.err = errors.New(msg)
			}
			log.Println(msg)
			return
		case event, ok := <-eventChan:
			if !ok {
				log.Println("Stream channel closed.")
				if acs.ctx.Err() != nil {
					log.Printf("Context error after stream close: %v", acs.ctx.Err())
					acs.err = acs.ctx.Err()
				}
				return
			}
			acs.processEventResult(id, &event, cr)
		}
	}
}

func (acs *A2ASession) processResponse(id string, result *a2aproto.MessageResult, cr *A2ACallResult) {
	switch r := result.Result.(type) {
	case *a2aproto.Message:
		acs.processParts(id, r.Parts, cr)
	case *a2aproto.Task:
		acs.sendResponse(id, fmt.Sprintf("Task %s State: %s @ %s\n", r.ID, r.Status.State, r.Status.Timestamp), nil, cr)
		if r.Status.Message != nil {
			acs.processParts(id, r.Status.Message.Parts, cr)
		}
	default:
		acs.sendResponse(id, fmt.Sprintf("Task %s Received unknown message type: %T\n", r.GetKind(), r), r, cr)
	}
}

func (acs *A2ASession) processEventResult(id string, event *a2aproto.StreamingMessageEvent, cr *A2ACallResult) {
	switch e := event.Result.(type) {
	case *a2aproto.Message:
		acs.processParts(id, e.Parts, cr)
	case *a2aproto.Task:
		acs.sendResponse(id, fmt.Sprintf("Task %s State: %s @ %s\n", e.ID, e.Status.State, e.Status.Timestamp), nil, cr)
		if e.Status.Message != nil {
			acs.processParts(id, e.Status.Message.Parts, cr)
		}
	case *a2aproto.TaskStatusUpdateEvent:
		if e.Status.Message != nil {
			acs.processParts(id, e.Status.Message.Parts, cr)
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
			acs.sendResponse(id, msg+msg2, nil, cr)
		}
	case *a2aproto.TaskArtifactUpdateEvent:
		if e.Artifact.Name != nil && strings.EqualFold(*e.Artifact.Name, "Timeline") && len(e.Artifact.Parts) > 0 {
			if dp, ok := e.Artifact.Parts[0].(*a2aproto.DataPart); ok {
				cr.storeRemoteTimeline(dp.Data)
			}
		}
		acs.processParts(id, e.Artifact.Parts, cr)
	default:
		acs.sendResponse(id, fmt.Sprintf("Task %s Received unknown event type: %T\n", e.GetKind(), event.Result), event.Result, cr)
	}
}

func (acs *A2ASession) processParts(id string, parts []a2aproto.Part, cr *A2ACallResult) {
	for _, p := range parts {
		var part any = p
		switch p := part.(type) {
		case *a2aproto.TextPart:
			acs.sendResponse(id, p.Text, nil, cr)
		case a2aproto.TextPart:
			acs.sendResponse(id, p.Text, nil, cr)
		case *a2aproto.DataPart:
			acs.sendResponse(id, "", p.Data, cr)
		case map[string]interface{}:
			textHandled := false
			if typeStr, ok := p["type"].(string); ok && typeStr == "text" {
				if text, ok := p["text"].(string); ok {
					acs.sendResponse(id, text, nil, cr)
					textHandled = true
				}
			}
			if !textHandled {
				acs.sendResponse(id, "", p, cr)
			}
		default:
			acs.sendResponse(id, "", p, cr)
		}
	}
}

func (acs *A2ASession) sendLocalProgress(id, text string) {
	if text != "" && acs.localProgress != nil {
		acs.localProgress <- types.NewPair[string, any](id, text)
	}
}

func (acs *A2ASession) sendResponse(id, text string, data any, cr *A2ACallResult) {
	dataKey := text
	if dataKey == "" {
		if m, ok := data.(map[string]any); ok {
			for k := range m {
				dataKey = fmt.Sprintf("%s: %s", id, k)
				break
			}
		}
	}
	if data != nil {
		result := timeline.CheckAndGetResult(data)
		if strings.Contains(dataKey, "Result") || result != nil {
			cr.RemoteResult = data
		} else {
			t := timeline.CheckAndGetTimeline(data)
			if strings.Contains(dataKey, "Timeline") || t != nil {
				cr.RemoteTimeline = t
			} else {
				cr.Data[dataKey] = data
			}
		}
	} else if text != "" {
		cr.Content = append(cr.Content, text)
	}
	if acs.callback != nil {
		if data != nil {
			acs.callback(id, dataKey, data)
		} else {
			acs.callback(id, text, nil)
		}
	}
	if acs.upstreamProgress != nil {
		if text != "" {
			acs.upstreamProgress <- types.NewPair[string, any](fmt.Sprintf("%s[%s]", id, text), data)
		} else if data != nil {
			acs.upstreamProgress <- types.NewPair(dataKey, data)
		}
	}
}

func (as *A2ASession) reportInitiateCall() {
	msg := fmt.Sprintf("%s [%s] Initiating Agent Call [%s] on URL [%s], Request Count [%d], Concurrent [%d]",
		as.callerId, as.client.ID, as.call.Name, as.call.AgentURL, as.call.RequestCount, as.call.Concurrent)
	as.clientInfo = timeline.BuildGotoClientInfo(as.port, as.callerId, as.call.Name, as.call.AgentURL, as.call.CardURL, as.inHeaders, nil,
		nil, nil, as.call.RequestCount, as.call.Concurrent, map[string]any{
			"Agent-Call": as.call,
		})
	as.timeline.AddEventWithClient(as.callerId, msg, as.clientInfo)
}
