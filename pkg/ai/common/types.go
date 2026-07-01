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

package aicommon

import (
	"goto/pkg/types"
	"goto/pkg/util"
)

type ToolCallArgs struct {
	DelayText      string            `json:"delay,omitempty"`
	Count          int               `json:"count,omitempty"`
	Size           int               `json:"size,omitempty"`
	Text           string            `json:"text,omitempty"`
	Status         int               `json:"status,omitempty"`
	ResultOnly     bool              `json:"resultOnly,omitempty"`
	NoEvents       bool              `json:"noEvents,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ToolDef        map[string]any    `json:"toolDef,omitempty"`
	Delay          *types.Delay      `json:"-"`
	ToolName       string            `json:"tool,omitempty"`
	AgentName      string            `json:"agent,omitempty"`
	URL            string            `json:"url,omitempty"`
	Authority      string            `json:"authority,omitempty"`
	SSE            bool              `json:"sse,omitempty"`
	Headers        *types.Headers    `json:"headers,omitempty"`
	ForwardHeaders []string          `json:"forwardHeaders,omitempty"`
	AgentMessage   string            `json:"agentMessage,omitempty"`
	AgentData      map[string]any    `json:"agentData,omitempty"`
	RemoteArgs     *ToolCallArgs     `json:"remoteArgs,omitempty"`
}

func NewCallArgs() *ToolCallArgs {
	return &ToolCallArgs{
		Metadata:   map[string]string{},
		Delay:      types.NewDelay(0, 0, 0),
		RemoteArgs: &ToolCallArgs{},
	}
}

func (a *ToolCallArgs) NonNil() {
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	if a.ToolDef == nil {
		a.ToolDef = map[string]any{}
	}
	if a.Delay == nil {
		a.Delay = types.NewDelay(0, 0, 0)
	}
	if a.Headers == nil {
		a.Headers = types.NewHeaders()
	}
	if a.ForwardHeaders == nil {
		a.ForwardHeaders = []string{}
	}
	if a.AgentData == nil {
		a.AgentData = map[string]any{}
	}
	if a.RemoteArgs == nil {
		a.RemoteArgs = NewCallArgs()
	}
}

func (a *ToolCallArgs) UpdateFrom(args *ToolCallArgs) {
	if args == nil {
		return
	}
	if args.Count > 0 {
		a.Count = args.Count
	}
	if args.Size > 0 {
		a.Size = args.Size
	}
	if args.DelayText != "" {
		a.DelayText = args.DelayText
	}
	if args.Text != "" {
		a.Text = args.Text
	}
	if args.Status > 0 {
		a.Status = args.Status
	}
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	for k, v := range args.Metadata {
		a.Metadata[k] = v
	}
	if args.ToolName != "" {
		a.ToolName = args.ToolName
	}
	if args.AgentName != "" {
		a.AgentName = args.AgentName
	}
	if args.URL != "" {
		a.URL = args.URL
	}
	if args.Authority != "" {
		a.Authority = args.Authority
	}
	if args.SSE {
		a.SSE = true
	}
	if args.Headers != nil {
		a.Headers = args.Headers
	}
	if args.ForwardHeaders != nil {
		a.ForwardHeaders = args.ForwardHeaders
	}
	if args.AgentMessage != "" {
		a.AgentMessage = args.AgentMessage
	}
	if args.AgentData != nil {
		a.AgentData = args.AgentData
	}
	if args.RemoteArgs != nil {
		if a.RemoteArgs == nil {
			a.RemoteArgs = NewCallArgs()
		}
		a.RemoteArgs.UpdateFrom(args.RemoteArgs)
	}
}

func (a *ToolCallArgs) UpdateFromInputArgs(args map[string]any) {
	if args == nil {
		return
	}
	if args["count"] != nil {
		a.Count = util.AnyToInt(args["count"])
	}
	if args["delay"] != nil {
		a.DelayText = args["delay"].(string)
	}
	if args["text"] != nil {
		a.Text = args["text"].(string)
	}
	if args["status"] != nil {
		a.Status = util.AnyToInt(args["status"])
	}
	if args["tool"] != nil {
		a.ToolName = args["tool"].(string)
	}
	if args["agent"] != nil {
		a.AgentName = args["agent"].(string)
	}
	if args["url"] != nil {
		a.URL = args["url"].(string)
	}
	if args["authority"] != nil {
		a.Authority = args["authority"].(string)
	}
	if args["sse"] != nil {
		a.SSE = util.AnyToBool(args["sse"])
	}
	if args["headers"] != nil {
		if a.Headers == nil {
			a.Headers = types.NewHeaders()
		}
		headers := args["headers"].(map[string]string)
		for h, v := range headers {
			a.Headers.Request.Add[h] = v
		}
	}
	if args["forwardHeaders"] != nil {
		if a.Headers == nil {
			a.Headers = types.NewHeaders()
		}
		forwardHeaders := args["forwardHeaders"].([]string)
		a.Headers.Request.Forward = forwardHeaders
	}
	if args["message"] != nil {
		a.AgentMessage = args["message"].(string)
	}
	if args["prompt"] != nil {
		a.AgentMessage = args["prompt"].(string)
	}
	if args["input"] != nil {
		a.AgentMessage = args["input"].(string)
	}
	if args["agentData"] != nil {
		if a.AgentData == nil {
			a.AgentData = map[string]any{}
		}
		agentData := args["agentData"].(map[string]string)
		for k, v := range agentData {
			a.AgentData[k] = v
		}
	}
	if args["args"] != nil {
		if remoteArgs, ok := args["args"].(map[string]any); ok {
			if a.RemoteArgs == nil {
				a.RemoteArgs = NewCallArgs()
			}
			a.RemoteArgs.UpdateFromInputArgs(remoteArgs)
		}
	}
}

func (a *ToolCallArgs) UpdateDelay(delay *types.Delay) {
	if a.DelayText != "" {
		if delay2 := types.ParseDelay(a.DelayText); delay2 != nil {
			delay = delay2
		}
	}
	a.Delay = delay
}

func (a *ToolCallArgs) AddMetadata(k, v string) {
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	a.Metadata[k] = v
}

func (a *ToolCallArgs) Clone() *ToolCallArgs {
	args := *a
	return &args
}

func (a *ToolCallArgs) IsEmpty() bool {
	return a.Text == "" && a.DelayText == "" && a.Count == 0 && a.Delay == nil &&
		a.ToolName == "" && a.AgentName == "" && a.URL == "" && a.Authority == "" &&
		a.Headers == nil && len(a.ForwardHeaders) == 0 && a.AgentMessage == "" &&
		len(a.Metadata) == 0 && len(a.AgentData) == 0 &&
		(a.RemoteArgs == nil || a.RemoteArgs.IsEmpty())
}
