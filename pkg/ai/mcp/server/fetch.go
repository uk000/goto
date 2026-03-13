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
	"goto/pkg/util"
	"net/http"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *ToolCallContext) fetch() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	url := t.Config.RemoteTool.URL
	authority := t.Config.RemoteTool.Authority
	if t.remoteArgs == nil {
		t.remoteArgs = &RemoteCallArgs{}
	}
	if t.remoteArgs.URL != "" {
		url = t.remoteArgs.URL
	}
	if t.remoteArgs.Authority != "" {
		authority = t.remoteArgs.Authority
	}
	forwardHeaders := map[string]bool{}
	for _, h := range t.remoteArgs.ForwardHeaders {
		forwardHeaders[h] = true
	}
	for _, h := range t.Config.RemoteTool.ForwardHeaders {
		forwardHeaders[h] = true
	}
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for h, v := range t.remoteArgs.Headers {
		req.Header.Add(h, v)
	}
	if t.requestHeaders != nil {
		for h := range forwardHeaders {
			if t.requestHeaders[h] != nil {
				req.Header[h] = t.requestHeaders[h]
			}
		}
	}
	req.Header["User-Agent"] = []string{t.Label}
	if authority != "" {
		req.Host = t.remoteArgs.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	util.PrintRequest("Tool Remote HTTP Call Request Details", req)
	resp, err := t.client.HTTP().Do(req)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Server [%s] Failed to invoke Remote URL [%s] with error: %s", t.Server.GetName(), url, err.Error())
		t.Log(msg)
		result.IsError = true
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		t.notifyClient(t.Log(fmt.Sprintf("Server [%s] fetched response from remote URL [%s]", t.Server.GetName(), url)), 0)
		output := util.Read(resp.Body)
		result.Content = append(result.Content, &gomcp.TextContent{Text: output})
		result.StructuredContent = util.BuildGotoClientInfo(nil, t.Server.Port, t.Label, t.Name, req.Host, url, req.Host, t.remoteArgs, nil, t.requestHeaders, req.Header,
			map[string]any{"ForwardHeaders": forwardHeaders})
	}
	t.applyDelay()
	return result, err
}
