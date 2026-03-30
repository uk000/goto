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
	tctx.Config.Agent.NonNil()
	ac := tctx.Config.Agent.CloneWithUpdate(tctx.args.Remote.AgentName, tctx.args.Remote.URL, tctx.args.Remote.Authority, tctx.args.Remote.AgentMessage, tctx.args.Remote.AgentData)
	finalHeaders := types.Union(ac.Headers, tctx.args.Remote.Headers)
	tctx.addForwardHeaders(finalHeaders.Request.Add, finalHeaders.Request.Forward, tctx.args.Remote.Args)
	msg := fmt.Sprintf("Invoking Agent [%s] at URL [%s]", ac.Name, ac.AgentURL)
	tctx.notifyClient(msg, 0)
	client := a2aclient.NewA2AClient(tctx.Server.Port, tctx.Name, ac.H2, ac.TLS, ac.Authority)
	if client == nil {
		return nil, errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(tctx.ctx, ac, ac.CardURL, ac.Authority, tctx.requestHeaders)
	if err != nil {
		return nil, fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", ac.Name, ac.AgentURL, err.Error())
	} else {
		msg = fmt.Sprintf("Loaded agent card for Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.AgentURL, session.Card.Capabilities.Streaming)
		tctx.notifyClient(msg, 0)
	}
	resultsChan := make(chan *types.Pair[string, any], 10)
	progressChan := make(chan string, 10)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go tctx.processResults(ac.Name, ac.AgentURL, progressChan, resultsChan, result, &wg)
	err = session.CallAgent(nil, resultsChan, resultsChan, progressChan)
	close(resultsChan)
	close(progressChan)
	wg.Wait()
	if err != nil {
		return nil, fmt.Errorf("Failed to call Agent [%s] URL [%s] with error: %s", ac.Name, ac.AgentURL, err.Error())
	} else {
		msg = fmt.Sprintf("Finished Call to Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.AgentURL, session.Card.Capabilities.Streaming)
		tctx.notifyClient(msg, 0)
	}
	return result, nil
}
