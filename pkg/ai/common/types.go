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
	"fmt"
	"maps"
	"slices"

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
	AgentArgs      map[string]any    `json:"agentArgs,omitempty"`
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
	if a.AgentArgs == nil {
		a.AgentArgs = map[string]any{}
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
	if args.AgentArgs != nil {
		a.AgentArgs = args.AgentArgs
	}
	if args.RemoteArgs != nil {
		if a.RemoteArgs == nil {
			a.RemoteArgs = NewCallArgs()
		}
		a.RemoteArgs.UpdateFrom(args.RemoteArgs)
	}
}

// inputString returns the value if it's text. Values of any other type are left out by the callers, instead of the
// process panicking on a type assertion.
func inputString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// inputStringMap accepts the maps of strings that the code builds (map[string]string) and the maps that come out of
// decoded JSON (map[string]any, whose non-text values are converted to text).
func inputStringMap(v any) (map[string]string, bool) {
	switch m := v.(type) {
	case map[string]string:
		return m, true
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, val := range m {
			if s, ok := val.(string); ok {
				out[k] = s
			} else if val != nil {
				out[k] = fmt.Sprint(val)
			}
		}
		return out, true
	}
	return nil, false
}

// inputStringSlice accepts []string and the []any that comes out of decoded JSON.
func inputStringSlice(v any) ([]string, bool) {
	switch l := v.(type) {
	case []string:
		return slices.Clone(l), true
	case []any:
		out := make([]string, 0, len(l))
		for _, val := range l {
			if s, ok := val.(string); ok {
				out = append(out, s)
			} else if val != nil {
				out = append(out, fmt.Sprint(val))
			}
		}
		return out, true
	}
	return nil, false
}

func (a *ToolCallArgs) UpdateFromInputArgs(args map[string]any) {
	if args == nil {
		return
	}
	if args["count"] != nil {
		a.Count = util.AnyToInt(args["count"])
	}
	if s, ok := inputString(args["delay"]); ok {
		a.DelayText = s
	}
	if s, ok := inputString(args["text"]); ok {
		a.Text = s
	}
	if args["status"] != nil {
		a.Status = util.AnyToInt(args["status"])
	}
	if s, ok := inputString(args["tool"]); ok {
		a.ToolName = s
	}
	if s, ok := inputString(args["agent"]); ok {
		a.AgentName = s
	}
	if s, ok := inputString(args["url"]); ok {
		a.URL = s
	}
	if s, ok := inputString(args["authority"]); ok {
		a.Authority = s
	}
	if args["sse"] != nil {
		a.SSE = util.AnyToBool(args["sse"])
	}
	if headers, ok := inputStringMap(args["headers"]); ok {
		a.requestHeaders().Add = mergeStrings(a.requestHeaders().Add, headers)
	}
	if forwardHeaders, ok := inputStringSlice(args["forwardHeaders"]); ok {
		a.requestHeaders().Forward = forwardHeaders
	}
	if s, ok := inputString(args["message"]); ok {
		a.AgentMessage = s
	}
	if s, ok := inputString(args["prompt"]); ok {
		a.AgentMessage = s
	}
	if s, ok := inputString(args["input"]); ok {
		a.AgentMessage = s
	}
	if args["agentArgs"] != nil {
		switch agentData := args["agentArgs"].(type) {
		case map[string]string:
			if a.AgentArgs == nil {
				a.AgentArgs = map[string]any{}
			}
			for k, v := range agentData {
				a.AgentArgs[k] = v
			}
		case map[string]any:
			if a.AgentArgs == nil {
				a.AgentArgs = map[string]any{}
			}
			maps.Copy(a.AgentArgs, agentData)
		}
	}
	ra := args["args"]
	if ra == nil {
		ra = args["remoteArgs"]
	}
	if ra != nil {
		if remoteArgs, ok := ra.(map[string]any); ok {
			if a.RemoteArgs == nil {
				a.RemoteArgs = NewCallArgs()
			}
			a.RemoteArgs.UpdateFromInputArgs(remoteArgs)
		}
	}
}

// requestHeaders returns the request headers config of the args, creating the headers and the config if they're missing.
func (a *ToolCallArgs) requestHeaders() *types.HeadersConfig {
	if a.Headers == nil {
		a.Headers = types.NewHeaders()
	}
	a.Headers.NonNil()
	return a.Headers.Request
}

func mergeStrings(to, from map[string]string) map[string]string {
	if to == nil {
		to = make(map[string]string, len(from))
	}
	maps.Copy(to, from)
	return to
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

// CloneHeaders makes a deep copy of the headers, which stay nil when they are. types.Headers.Clone is not used here,
// because it fails for headers without a request config, and leaves out the response config.
func CloneHeaders(h *types.Headers) *types.Headers {
	if h == nil {
		return nil
	}
	return &types.Headers{Request: cloneHeadersConfig(h.Request), Response: cloneHeadersConfig(h.Response)}
}

func cloneHeadersConfig(hc *types.HeadersConfig) *types.HeadersConfig {
	if hc == nil {
		return nil
	}
	return &types.HeadersConfig{Add: maps.Clone(hc.Add), Remove: slices.Clone(hc.Remove), Forward: slices.Clone(hc.Forward)}
}

// Clone makes a deep copy of the args, so that the clone can be updated without changing the original, and by
// concurrent requests. The Delay is the only part that is shared, because it's the one stateful piece of the args
// (the number of times it's applied is counted down).
func (a *ToolCallArgs) Clone() *ToolCallArgs {
	if a == nil {
		return nil
	}
	args := *a
	args.Metadata = maps.Clone(a.Metadata)
	args.ToolDef = maps.Clone(a.ToolDef)
	args.AgentArgs = maps.Clone(a.AgentArgs)
	args.ForwardHeaders = slices.Clone(a.ForwardHeaders)
	args.Headers = CloneHeaders(a.Headers)
	args.RemoteArgs = a.RemoteArgs.Clone()
	return &args
}

func (a *ToolCallArgs) IsEmpty() bool {
	return a.Text == "" && a.DelayText == "" && a.Count == 0 && a.Delay == nil &&
		a.ToolName == "" && a.AgentName == "" && a.URL == "" && a.Authority == "" &&
		a.Headers == nil && len(a.ForwardHeaders) == 0 && a.AgentMessage == "" &&
		len(a.Metadata) == 0 && len(a.AgentArgs) == 0 &&
		(a.RemoteArgs == nil || a.RemoteArgs.IsEmpty())
}
