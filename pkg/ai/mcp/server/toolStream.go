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
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *MCPTool) stream(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	var delay time.Duration
	d := tctx.args.Delay
	if tctx.Response != nil && tctx.Response.Delay != nil {
		if tctx.Response.Delay.IsLargerThan(tctx.args.Delay) {
			d = tctx.Response.Delay
		}
	}
	if tctx.Response != nil {
		responseCount := 0
		oldResponseCount := 0
		keepSending := true
		if tctx.Config.StreamCount > tctx.Response.StreamCount {
			tctx.Response.StreamCount = tctx.Config.StreamCount
		}
		total := tctx.Response.StreamCount
		if tctx.Behavior.Resumable {
			state, err := tctx.loadState()
			if err == nil && state != nil {
				oldResponseCount = state.ResponseCount
			}
		}
		tctx.Response.RangeTextFrom(oldResponseCount+1, total, func(text string, count int, restarted bool) (bool, error) {
			if !keepSending {
				return false, nil
			}
			responseCount = count
			if oldResponseCount > 0 && count <= oldResponseCount {
				msg := fmt.Sprintf("%s Skipping previously sent result [%d]", tctx.Label, count)
				tctx.notifyClient(msg, 0)
				return true, nil
			}
			progress := float64(count) / float64(total)
			if tctx.Behavior.Stream {
				msg := fmt.Sprintf("%s Progress: [%d] done, only [%d] more to go. Current stream output: %s", tctx.Label, count, total-count, text)
				tctx.notifyClient(msg, progress)
			}
			if d != nil && total-count > 0 {
				delay = d.Compute()
				msg := fmt.Sprintf("%s Progress: \U0001F634\U0001F4A4 Sleeping for [%s] before sending next update. [%d] done, [%d] more to go.", tctx.Label, delay, count, total-count)
				tctx.notifyClient(msg, progress)
				d.Apply()
			}
			result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("[%d] %s", count, text)})
			if tctx.Behavior.Resumable && count >= oldResponseCount+2 {
				keepSending = false
				err := tctx.saveState(&ToolState{
					RequestHeaders: tctx.requestHeaders,
					Args:           tctx.args,
					Delay:          d,
					ResponseCount:  count,
				})
				if err != nil {
					return false, err
				}
			}
			return true, nil
		})
		if keepSending {
			tctx.Log(fmt.Sprintf("%s Server [%s] sent response: count [%d] after delay [%s]", tctx.Label, tctx.Server.GetName(), responseCount, delay))
		} else {
			tctx.Log(fmt.Sprintf("%s Server [%s] sent partial response: count [%d] after delay [%s], kept the rest for resumable operation", tctx.Label, tctx.Server.GetName(), responseCount, delay))
		}
	} else {
		result.Content = append(result.Content, &gomcp.TextContent{Text: "<No payload>"})
		tctx.Log(fmt.Sprintf("%s Server [%s] sent default response after delay [%s]", tctx.Label, tctx.Server.GetName(), delay))
	}
	msg := fmt.Sprintf("%s Stream finished \U000026F3", tctx.Label)
	tctx.notifyClient(msg, 100)
	return result, nil
}
