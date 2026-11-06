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

package mcpclient

import (
	"context"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"log"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// payloadDelayLock guards the state of the delays of client payloads. A payload is shared by all the sessions that use
// it, and its delay counts how many times it's been applied.
var payloadDelayLock sync.Mutex

// pick returns a random index below n, which must be positive. It's used instead of types.Random, which uses a random
// source that can't be shared by goroutines, as the callbacks of concurrent requests do.
func pick(n int) int {
	return rand.IntN(n)
}

// applyPayloadDelay waits for the delay of a client payload, and returns how long it waited. The state of the delay is
// updated under a lock, and the wait happens outside of it, so that concurrent requests wait together.
func applyPayloadDelay(d *types.Delay) time.Duration {
	if d == nil || d.Min == nil || d.Max == nil {
		return 0
	}
	payloadDelayLock.Lock()
	var delay time.Duration
	if d.Count > 0 || d.Count == -1 {
		delay = d.Min.Duration
		if span := (d.Max.Duration - d.Min.Duration).Milliseconds(); span > 0 {
			delay += time.Duration(rand.Int64N(span)) * time.Millisecond
		}
		if delay > 0 && d.Count > 0 {
			d.Count--
		}
	}
	payloadDelayLock.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return delay
}

// ElicitationHandler answers an elicitation request of the server with an action and content picked from the client's elicit payload, applying the payload's delay.
func (s *MCPSession) ElicitationHandler(ctx context.Context, req *gomcp.ElicitRequest) (result *gomcp.ElicitResult, err error) {
	if s.Stop {
		log.Println("Received elicitation after client stopped. Ignoring.")
		return
	}
	label := fmt.Sprintf("%s[Elicitation]", s.CallerId)
	msg := ""
	output := map[string]any{}
	if req != nil && req.Params != nil {
		output["requestParams"] = req.Params
	}
	action := "approve"
	var elicitPayload *MCPClientPayload
	if s.clientPayload != nil {
		elicitPayload = s.clientPayload.ElicitPayload
	}
	if elicitPayload != nil && len(elicitPayload.Contents) > 0 {
		msg = fmt.Sprintf("%s --> %s", label, elicitPayload.Contents[pick(len(elicitPayload.Contents))])
	} else {
		msg = fmt.Sprintf("%s --> No Elicitation Content", label)
	}
	if elicitPayload != nil {
		if elicitPayload.Delay != nil {
			msg = fmt.Sprintf("%s --> Will delay", msg)
		}
		if len(elicitPayload.Actions) > 0 {
			action = elicitPayload.Actions[pick(len(elicitPayload.Actions))]
		}
	}
	if elicitPayload != nil && elicitPayload.Delay != nil {
		delay := applyPayloadDelay(elicitPayload.Delay)
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
	}
	log.Println(msg)
	s.Timeline.AddEvent(label, msg)
	t := timeline.NewTimeline(s.mcpClient.Port, label, nil, nil, nil, s.mcpClient.stream, s.mcpClient.updateCallback, s.mcpClient.endCallback)
	t.AddEvent(label, msg)
	output["Timeline"] = t
	result = &gomcp.ElicitResult{
		Action:  action,
		Content: output,
	}
	return
}

// CreateMessageHandler answers a sampling request of the server with a model, role and content picked from the client's sample (or elicit, for elicit prompts) payload, applying its delay.
func (s *MCPSession) CreateMessageHandler(ctx context.Context, req *gomcp.CreateMessageRequest) (*gomcp.CreateMessageResult, error) {
	if s.Stop {
		log.Println("Received message after client stopped. Ignoring.")
		return nil, nil
	}
	var params *gomcp.CreateMessageParams
	if req != nil {
		params = req.Params
	}
	systemPrompt := ""
	if params != nil {
		systemPrompt = params.SystemPrompt
	}
	isElicit := strings.Contains(systemPrompt, "elicit")
	task := "Sampling/Message"
	var payload *MCPClientPayload
	if s.clientPayload != nil {
		payload = s.clientPayload.SamplePayload
	}
	if isElicit {
		task = "Elicitation"
		if s.clientPayload != nil {
			payload = s.clientPayload.ElicitPayload
		}
	}
	label := fmt.Sprintf("%s[%s]", s.CallerId, task)
	msg := ""
	result := &gomcp.CreateMessageResult{}
	var content, model, role string
	if payload != nil {
		if payload.Delay != nil {
			msg = fmt.Sprintf("%s --> Will delay", msg)
		}
		if len(payload.Models) > 0 {
			model = payload.Models[pick(len(payload.Models))]
		}
		if len(payload.Roles) > 0 {
			role = payload.Roles[pick(len(payload.Roles))]
		}
		if len(payload.Contents) > 0 {
			content = payload.Contents[pick(len(payload.Contents))]
		}
	}
	if model == "" {
		model = "GotoModel"
	}
	if role == "" {
		role = "none"
	}
	if content == "" {
		msg = fmt.Sprintf("%s %s --> Responding to [%s] request with no defined payload", label, msg, task)
	}
	if payload != nil && payload.Delay != nil {
		delay := applyPayloadDelay(payload.Delay)
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
	}
	log.Println(msg)
	s.Timeline.AddEvent(label, msg)
	t := timeline.NewTimeline(s.mcpClient.Port, label, nil, nil, nil, s.mcpClient.stream, s.mcpClient.updateCallback, s.mcpClient.endCallback)
	t.AddEvent(label, msg)
	output := map[string]any{}
	output["Content"] = content
	output["Timeline"] = t
	if params != nil {
		output["requestParams"] = params
	}
	result.Model = model
	result.Role = gomcp.Role(role)
	result.Content = &gomcp.TextContent{Text: util.ToJSONText(output)}
	result.StopReason = systemPrompt
	return result, nil
}

// ToolListChangedHandler ignores the notification that the tool list changed.
func (s *MCPSession) ToolListChangedHandler(ctx context.Context, req *gomcp.ToolListChangedRequest) {

}

// PromptListChangedHandler ignores the notification that the prompt list changed.
func (s *MCPSession) PromptListChangedHandler(ctx context.Context, req *gomcp.PromptListChangedRequest) {

}

// ResourceListChangedHandler ignores the notification that the resource list changed.
func (s *MCPSession) ResourceListChangedHandler(ctx context.Context, req *gomcp.ResourceListChangedRequest) {

}

// ResourceUpdatedHandler ignores the notification that a resource was updated.
func (s *MCPSession) ResourceUpdatedHandler(ctx context.Context, req *gomcp.ResourceUpdatedNotificationRequest) {

}

// LoggingMessageHandler ignores the logging messages of the server.
func (s *MCPSession) LoggingMessageHandler(ctx context.Context, req *gomcp.LoggingMessageRequest) {

}

// ProgressNotificationHandler adds the progress notification of the server to the timeline, recognizing the client info, server info, timeline or result that it carries as JSON.
func (s *MCPSession) ProgressNotificationHandler(ctx context.Context, req *gomcp.ProgressNotificationClientRequest) {
	if s.Stop {
		log.Println("Received progress notification after client stopped. Ignoring.")
		return
	}
	if req == nil || req.Params == nil {
		return
	}
	msg := fmt.Sprintf("%s: [ProgressNotification]. Received Upstream Message", s.Operation)
	if req.Params.Progress > 0 {
		msg = fmt.Sprintf("%s --> [Total: %f][Progress: %f]", msg, req.Params.Total, req.Params.Progress)
	}
	var remoteData any
	var remoteClient *timeline.GotoClientInfo
	var remoteServer *timeline.GotoServerInfo
	isjson := false
	if req.Params.Meta != nil {
		if req.Params.Meta["json"] != nil {
			if json, ok := util.JSONFromJSONText(req.Params.Message); ok {
				remoteData = json.Object()
				req.Params.Message = ""
				isjson = true
				if remoteClient, ok = timeline.CheckAndGetClientInfo(remoteData); ok {
					req.Params.Message = "ClientInfo"
				} else if remoteServer, ok = timeline.CheckAndGetServerInfo(remoteData); ok {
					req.Params.Message = "ServerInfo"
				} else if timeline.IsTimeline(remoteData) {
					req.Params.Message = "Timeline"
				} else if timeline.IsResult(remoteData) {
					req.Params.Message = "Result"
				}
			}
		}
	}
	s.Timeline.AddEventWithRemote(s.CallerId, msg, req.Params.Message, remoteServer, remoteClient, remoteData, isjson)
	if global.Flags.VerboseMCP {
		log.Println(msg)
	}
}
