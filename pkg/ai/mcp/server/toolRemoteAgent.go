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

package mcpserver

import (
	"errors"
	"fmt"
	a2aclient "goto/pkg/ai/a2a/client"
	aicommon "goto/pkg/ai/common"
	"goto/pkg/types"
	"goto/pkg/util"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *MCPTool) callRemoteAgent(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{},
	}
	if tctx.args == nil {
		tctx.args = aicommon.NewCallArgs()
	}
	tctx.args.NonNil()
	tctx.Config.Agent.NonNil()
	ac := tctx.Config.Agent.CloneWithUpdate(tctx.args.RemoteArgs.AgentName, tctx.args.RemoteArgs.URL, tctx.args.RemoteArgs.Authority, tctx.args.RemoteArgs.AgentMessage, tctx.args.RemoteArgs.AgentData)
	if tctx.timeline.ResultOnly {
		ac.ResultOnly = true
	}
	if tctx.timeline.NoEvents {
		ac.NoEvents = true
	}
	finalHeaders := types.Union(ac.Headers, tctx.args.RemoteArgs.Headers)
	tctx.addForwardHeaders(finalHeaders.Request.Add, finalHeaders.Request.Forward, tctx.args.RemoteArgs)
	msg := fmt.Sprintf("Invoking Agent [%s] at URL [%s]", ac.Name, ac.AgentURL)
	tctx.AddEvent(msg)
	client := a2aclient.NewA2AClient(tctx.Server.Port, tctx.Name, ac.H2, ac.TLS, ac.Authority)
	if client == nil {
		return nil, errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(tctx.ctx, ac, ac.CardURL, ac.Authority, tctx.requestHeaders, tctx.timeline)
	if err != nil {
		return nil, fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", ac.Name, ac.AgentURL, err.Error())
	} else {
		msg = fmt.Sprintf("Loaded agent card for Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.AgentURL, session.Card.Capabilities.Streaming)
		tctx.AddEvent(msg)
	}
	localProgress := make(chan *types.Pair[string, any], 10)
	upstreamProgress := make(chan *types.Pair[string, any], 10)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go util.LinkChannels(localProgress, upstreamProgress)
	go tctx.processResults(ac.Name, ac.AgentURL, upstreamProgress, result, &wg)
	// callback := func(key, output string, data any) {
	// 	tctx.notifyClient(fmt.Sprintf("%s: %s", key, output), data, true)
	// }
	err = session.CallAgent(nil, localProgress, upstreamProgress)
	wg.Wait()
	close(localProgress)
	if !util.IsNil(err) {
		return nil, fmt.Errorf("Failed to call Agent [%s] URL [%s] with error: %s", ac.Name, ac.AgentURL, err.Error())
	}
	tctx.timeline.RemoteGotos = session.Result.RemoteGotos
	data := result.StructuredContent
	result = session.Result.ToMCP(data.(map[string]any))
	return result, nil
}
