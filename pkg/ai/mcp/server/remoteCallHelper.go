/**
 * Copyright 2025 uk
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
	"goto/pkg/types"
	"strings"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *ToolCallContext) processResults(name string, progressChan chan string, resultsChan chan *types.Pair[string, any], result *gomcp.CallToolResult, wg *sync.WaitGroup) {
	structuredContent := map[string]any{}
	structuredCount := 1
outer:
	for {
		select {
		case <-t.ctx.Done():
			t.notifyClient("Stream was cancelled", 0)
			break outer
		case update, ok := <-progressChan:
			if !ok {
				break outer
			}
			if update != "" {
				t.notifyClient(fmt.Sprintf("Upstream[%s] update: %s", name, update), 0)
			}
		case pair, ok := <-resultsChan:
			if !ok {
				break outer
			}
			if pair != nil && pair.Right != nil {
				t.notifyClient(fmt.Sprintf("Upstream[%s] Result for %s: %s", name, pair.Left, pair.RightS()), 0)
				// content, data := createContent(name, pair.Right)
				// result.Content = append(result.Content, content)
				structuredContent[fmt.Sprintf("%s-%d", pair.Left, structuredCount)] = pair.Right
				structuredCount++
			}
		}
	}
	result.StructuredContent = structuredContent
	wg.Done()
}

func (t *ToolCallContext) addForwardHeaders(headers map[string][]string, forwardHeaders []string, args map[string]any) {
	finalForwardHeaders := map[string]bool{}
	if t.remoteArgs.ToolArgs == nil {
		t.remoteArgs.ToolArgs = map[string]any{}
	}
	if t.remoteArgs.ToolArgs["forwardHeaders"] != nil {
		if arr, ok := t.remoteArgs.ToolArgs["forwardHeaders"].([]string); ok {
			for _, h := range arr {
				finalForwardHeaders[h] = true
			}
		}
	}
	if t.remoteArgs.ForwardHeaders == nil {
		t.remoteArgs.ForwardHeaders = []string{}
	}
	t.remoteArgs.ForwardHeaders = append(t.remoteArgs.ForwardHeaders, forwardHeaders...)
	for _, h := range t.remoteArgs.ForwardHeaders {
		finalForwardHeaders[h] = true
	}
	toolForwardHeaders := []string{}
	for h := range finalForwardHeaders {
		toolForwardHeaders = append(toolForwardHeaders, h)
	}
	t.remoteArgs.ToolArgs["forwardHeaders"] = toolForwardHeaders
	args["forwardHeaders"] = toolForwardHeaders
	if t.requestHeaders != nil {
		for _, h := range t.remoteArgs.ForwardHeaders {
			for h2, v2 := range t.requestHeaders {
				if strings.EqualFold(h, h2) {
					headers[h] = v2
					break
				}
			}
		}
	}
}
