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
	DelayText string            `json:"delay,omitempty"`
	Count     int               `json:"count,omitempty"`
	Text      string            `json:"text,omitempty"`
	Remote    *RemoteCallArgs   `json:"remote,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Delay     *types.Delay      `json:"-"`
}

type RemoteCallArgs struct {
	ToolName       string         `json:"tool,omitempty"`
	AgentName      string         `json:"agent,omitempty"`
	URL            string         `json:"url,omitempty"`
	Authority      string         `json:"authority,omitempty"`
	SSE            bool           `json:"sse,omitempty"`
	Headers        *types.Headers `json:"headers,omitempty"`
	ForwardHeaders []string       `json:"forwardHeaders,omitempty"`
	AgentMessage   string         `json:"agentMessage,omitempty"`
	AgentData      map[string]any `json:"agentData,omitempty"`
	Args           *ToolCallArgs  `json:"args,omitempty"`
}

func NewCallArgs(remoteArgs ...bool) *ToolCallArgs {
	return &ToolCallArgs{
		Remote:   NewRemoteCallArgs(remoteArgs...),
		Metadata: map[string]string{},
	}
}

func NewRemoteCallArgs(remoteArgs ...bool) *RemoteCallArgs {
	remote := &RemoteCallArgs{}
	if len(remoteArgs) == 0 || remoteArgs[0] {
		remote.Args = NewCallArgs(false)
	}
	return remote
}

func (a *ToolCallArgs) UpdateFrom(argsList ...*ToolCallArgs) {
	for _, args := range argsList {
		if args.Count > 0 {
			a.Count = args.Count
		}
		if args.DelayText != "" {
			a.DelayText = args.DelayText
		}
		if args.Text != "" {
			a.Text = args.Text
		}
		if a.Metadata == nil {
			a.Metadata = map[string]string{}
		}
		for k, v := range args.Metadata {
			a.Metadata[k] = v
		}
		if args.Remote != nil {
			if a.Remote == nil {
				a.Remote = NewRemoteCallArgs(false)
			}
			a.Remote.UpdateFrom(args.Remote)
		}
	}
}

func (a *RemoteCallArgs) UpdateFrom(args *RemoteCallArgs) {
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
	if args.Args != nil {
		if a.Args == nil {
			a.Args = NewCallArgs(false)
		}
		a.Args.UpdateFrom(args.Args)
	}
}

func (a *ToolCallArgs) UpdateFromInputArgs(args map[string]any) {
	if args["count"] != nil {
		a.Count = util.AnyToInt(args["count"])
	}
	if args["delay"] != nil {
		a.DelayText = args["delay"].(string)
	}
	if args["text"] != nil {
		a.Text = args["text"].(string)
	}
	a.Remote.UpdateFromInputArgs(args)
}

func (a *RemoteCallArgs) UpdateFromInputArgs(args map[string]any) {
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
		if a.Args == nil {
			a.Args = NewCallArgs(true, true)
		}
		remoteArgs := args["args"].(map[string]any)
		a.Args.UpdateFromInputArgs(remoteArgs)
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
