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
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"net/http"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *MCPTool) fetch(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	url := tctx.Config.RemoteTool.URL
	authority := tctx.Config.RemoteTool.Authority
	if tctx.args == nil {
		tctx.args = aicommon.NewCallArgs(false)
	}
	if tctx.args.Remote.URL != "" {
		url = tctx.args.Remote.URL
	}
	if tctx.args.Remote.Authority != "" {
		authority = tctx.args.Remote.Authority
	}
	count := tctx.args.Count
	if count == 0 {
		count = 1
	}
	finalHeaders := types.Union(tctx.Config.RemoteTool.Headers, tctx.args.Remote.Headers)
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if finalHeaders != nil && finalHeaders.Request != nil {
		for h, v := range finalHeaders.Request.Add {
			req.Header.Add(h, v)
		}
		if tctx.requestHeaders != nil {
			for _, h := range finalHeaders.Request.Forward {
				if tctx.requestHeaders[h] != nil {
					req.Header[h] = tctx.requestHeaders[h]
				}
			}
		}
	}
	req.Header["User-Agent"] = []string{tctx.Label}
	if authority != "" {
		req.Host = tctx.args.Remote.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	clientInfo := timeline.BuildGotoClientInfo(tctx.Server.Port, tctx.Label, req.Host, url, req.Host,
		tctx.requestHeaders, req.Header, tctx.args, nil, count, 1, nil)
	results := []any{}
	var anyError error
	for i := 1; i <= count; i++ {
		tctx.timeline.AddEventWithClient(tctx.Label, fmt.Sprintf("%s: Invoking HTTP URL [%s], Request %d/%d", t.Label, url, i, count), clientInfo)
		resp, err := tctx.client.HTTP().Do(req)
		msg := ""
		if err != nil {
			msg = fmt.Sprintf("Server [%s] Failed to invoke Remote URL [%s] with error: %s", tctx.Server.GetName(), url, err.Error())
			tctx.Log(msg)
			result.IsError = true
			result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
			anyError = err
		} else {
			tctx.Log(fmt.Sprintf("Server [%s] fetched response from remote URL [%s]", tctx.Server.GetName(), url))
			output := util.Read(resp.Body)
			result.Content = append(result.Content, &gomcp.TextContent{Text: output})
			results = append(results, output)
		}
		tctx.applyDelay()
	}
	result.StructuredContent = results
	return result, anyError
}
