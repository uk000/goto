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
	"fmt"
	aicommon "goto/pkg/ai/common"
	"goto/pkg/types"
	"maps"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func (tctx *ToolCallContext) processResults(name, url string, progressChan chan string, resultsChan chan *types.Pair[string, any], result *gomcp.CallToolResult, wg *sync.WaitGroup) {
	structuredContent := map[string]any{}
	structuredCount := 1
outer:
	for {
		select {
		case <-tctx.ctx.Done():
			tctx.notifyClient("Stream was cancelled", 0)
			break outer
		case update, ok := <-progressChan:
			if !ok {
				break outer
			}
			if update != "" {
				tctx.notifyClient(fmt.Sprintf("Upstream[%s] update: %s", name, update), 0)
			}
		case pair, ok := <-resultsChan:
			if !ok {
				break outer
			}
			if pair != nil && pair.Right != nil {
				tctx.notifyClient(fmt.Sprintf("Upstream[%s] Result for %s: %s", name, pair.Left, pair.RightS()), 0)
				// content, data := createContent(name, pair.Right)
				// result.Content = append(result.Content, content)
				if a, ok := pair.Right.(protocol.Artifact); ok {
					for _, part := range a.Parts {
						if t, ok := part.(*protocol.TextPart); ok {
							tctx.notifyClient(t.Text, 0)
						} else if d, ok := part.(*protocol.DataPart); ok {
							structuredContent[fmt.Sprintf("%s(%d)", url, structuredCount)] = d.Data
							structuredCount++
						}
					}
				} else {
					structuredContent[fmt.Sprintf("%s(%d)", url, structuredCount)] = pair.Right
					structuredCount++
				}
			}
		}
	}
	result.StructuredContent = structuredContent
	wg.Done()
}

func (tctx *ToolCallContext) addForwardHeaders(headers types.SimpleHTTPHeaders, forwardHeaders []string, args *aicommon.ToolCallArgs) {
	finalForwardHeaders := map[string]bool{}
	if tctx.args != nil && tctx.args.Remote != nil {
		if tctx.args.Remote.ForwardHeaders != nil {
			for _, h := range tctx.args.Remote.ForwardHeaders {
				finalForwardHeaders[h] = true
			}
		}
		if tctx.args.Remote.Headers != nil && tctx.args.Remote.Headers.HasForwardHeaders() {
			forwardHeaders = append(forwardHeaders, tctx.args.Remote.Headers.Request.Forward...)
		}
		for _, h := range forwardHeaders {
			finalForwardHeaders[h] = true
		}
		if tctx.requestHeaders != nil {
			types.ForwardHeaders(tctx.requestHeaders, headers, maps.Keys(finalForwardHeaders), tctx.Label)
		}
	}
	toolForwardHeaders := []string{}
	for h := range finalForwardHeaders {
		toolForwardHeaders = append(toolForwardHeaders, h)
	}
	tctx.args.Remote.ForwardHeaders = toolForwardHeaders
	if args.Remote == nil {
		args.Remote = aicommon.NewRemoteCallArgs(true)
	}
	args.Remote.ForwardHeaders = toolForwardHeaders
}
