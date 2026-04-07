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
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"log"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *MCPSession) ElicitationHandler(ctx context.Context, req *gomcp.ElicitRequest) (result *gomcp.ElicitResult, err error) {
	if s.Stop {
		log.Println("Received elicitation after client stopped. Ignoring.")
		return
	}
	label := fmt.Sprintf("%s[Elicitation]", s.CallerId)
	msg := ""
	output := map[string]any{}
	if req.Params != nil {
		output["requestParams"] = req.Params
	}
	action := "approve"
	var elicitPayload *MCPClientPayload
	if s.clientPayload != nil {
		elicitPayload = s.clientPayload.ElicitPayload
	}
	if elicitPayload != nil {
		msg = fmt.Sprintf("%s --> %s", label, elicitPayload.Contents[types.Random(len(elicitPayload.Contents))])
		if elicitPayload.Delay != nil {
			msg = fmt.Sprintf("%s --> Will delay", msg)
		}
		action = elicitPayload.Actions[types.Random(len(elicitPayload.Actions))]
	} else {
		msg = fmt.Sprintf("%s --> No Elicitation Content", label)
	}
	if elicitPayload != nil && elicitPayload.Delay != nil {
		delay := elicitPayload.Delay.ComputeAndApply()
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
	}
	log.Println(msg)
	s.Timeline.AddEvent(label, msg, nil, nil, false)
	t := timeline.NewTimeline(s.mcpClient.Port, label, nil, nil, nil, s.mcpClient.stream, s.mcpClient.updateCallback, s.mcpClient.endCallback)
	t.AddEvent(label, msg, nil, nil, false)
	output["Timeline"] = t
	result = &gomcp.ElicitResult{
		Action:  action,
		Content: output,
	}
	return
}

func (s *MCPSession) CreateMessageHandler(ctx context.Context, req *gomcp.CreateMessageRequest) (*gomcp.CreateMessageResult, error) {
	if s.Stop {
		log.Println("Received message after client stopped. Ignoring.")
		return nil, nil
	}
	isElicit := strings.Contains(req.Params.SystemPrompt, "elicit")
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
			model = payload.Models[types.Random(len(payload.Models))]
		}
		if len(payload.Roles) > 0 {
			role = payload.Roles[types.Random(len(payload.Roles))]
		}
		if len(payload.Contents) > 0 {
			content = payload.Contents[types.Random(len(payload.Contents))]
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
	if payload.Delay != nil {
		delay := payload.Delay.ComputeAndApply()
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
	}
	log.Println(msg)
	s.Timeline.AddEvent(label, msg, nil, nil, false)
	t := timeline.NewTimeline(s.mcpClient.Port, label, nil, nil, nil, s.mcpClient.stream, s.mcpClient.updateCallback, s.mcpClient.endCallback)
	t.AddEvent(label, msg, nil, nil, false)
	output := map[string]any{}
	output["Content"] = content
	output["Timeline"] = t
	if req.Params != nil {
		output["requestParams"] = req.Params
	}
	result.Model = model
	result.Role = gomcp.Role(role)
	result.Content = &gomcp.TextContent{Text: util.ToJSONText(output)}
	result.StopReason = req.Params.SystemPrompt
	return result, nil
}

func (s *MCPSession) ToolListChangedHandler(ctx context.Context, req *gomcp.ToolListChangedRequest) {

}

func (s *MCPSession) PromptListChangedHandler(ctx context.Context, req *gomcp.PromptListChangedRequest) {

}

func (s *MCPSession) ResourceListChangedHandler(ctx context.Context, req *gomcp.ResourceListChangedRequest) {

}

func (s *MCPSession) ResourceUpdatedHandler(ctx context.Context, req *gomcp.ResourceUpdatedNotificationRequest) {

}

func (s *MCPSession) LoggingMessageHandler(ctx context.Context, req *gomcp.LoggingMessageRequest) {

}

func (s *MCPSession) ProgressNotificationHandler(ctx context.Context, req *gomcp.ProgressNotificationClientRequest) {
	if s.Stop {
		log.Println("Received progress notification after client stopped. Ignoring.")
		return
	}
	msg := fmt.Sprintf("%s: [ProgressNotification]. Received Upstream Message", s.Operation)
	if req.Params.Progress > 0 {
		msg = fmt.Sprintf("%s --> [Total: %f][Progress: %f]", msg, req.Params.Total, req.Params.Progress)
	}
	var remoteData any
	isjson := false
	if req.Params.Meta != nil {
		if req.Params.Meta["json"] != nil {
			if json, ok := util.JSONFromJSONText(req.Params.Message); ok {
				remoteData = json.Object()
				isjson = true
			}
		}
	}
	if remoteData == nil {
		remoteData = req.Params.Message
	}
	s.Timeline.AddEvent(s.CallerId, msg, nil, remoteData, isjson)
	log.Println(msg)
}
