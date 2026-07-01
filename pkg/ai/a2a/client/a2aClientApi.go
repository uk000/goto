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

package a2aclient

import (
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strings"

	"github.com/gorilla/mux"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

var (
	Middleware = middleware.NewMiddleware("a2a", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	a2a := middleware.RootPath("/a2a")
	a2aClientRouter := util.PathRouter(a2a, "/client")
	util.AddRouteWithMultiQ(a2aClientRouter, "/agent/card", fetchAgentCard, [][]string{{"url"}, {"authority"}}, "GET")
	util.AddRoute(a2aClientRouter, "/agent/{agent}/call", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/call/stream", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/call/result", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/call", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/push", pushReceiver, "POST")
}

func fetchAgentCard(w http.ResponseWriter, r *http.Request) {
	url := util.GetStringParamValue(r, "url")
	authority := util.GetStringParamValue(r, "authority")
	card, err := FetchAgentCard(r.Context(), url, authority, nil, r.Header)
	if err != nil {
		util.SendBadRequest(w, r, "Error fetching agent card from url [%s], authority [%s]: %s", url, authority, err.Error())
		return
	}
	msg := fmt.Sprintf("Fetched agent card successfully for agent [%s], from url [%s], authority [%s]", card.Name, url, authority)
	util.WriteJsonPayload(w, card)
	util.AddLogMessage(msg, r)
}

func callAgent(w http.ResponseWriter, r *http.Request) {
	call := &AgentCall{}
	name := util.GetStringParamValue(r, "agent")
	stream := strings.Contains(r.RequestURI, "stream")
	port := util.GetRequestOrListenerPortNum(r)
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, &call)
	if err != nil {
		util.SendBadRequest(w, r, "Failed to parse payload with error [%s]", err.Error())
		return
	}
	if name != "" {
		call.Name = name
	}
	output := map[string]map[string]any{}
	msg := ""
	result, err := CallAgent(r.Context(), port, call, streamAgentResponse(call.Name, stream, output, w, r), r.Header)
	var headers map[string]any
	var viaGotos map[string]bool
	status := http.StatusBadGateway
	if result != nil {
		status = result.LastResponseStatus
		if len(result.UpstreamStatuses) > 0 {
			w.Header().Add(constants.HeaderGotoUpstreamStatus, util.ToJSONText(result.UpstreamStatuses))
		}
		headers = map[string]any{call.Name: map[string]any{
			"RequestHeaders":  result.LastRequestHeaders,
			"ResponseHeaders": result.LastResponseHeaders,
		}}
		viaGotos = util.GetViaGotosFromUpstreamHeaders(headers)
		for v := range result.RemoteGotos {
			viaGotos[v] = true
		}
		for v := range viaGotos {
			w.Header().Add(constants.HeaderViaGoto, v)
		}
	}
	if !util.IsNil(err) {
		if e, ok := err.(*AgentError); ok {
			status = e.StatusCode
		}
		msg = fmt.Sprintf("Error invoking agent [%s] on URL [%s] with input [%s]: Status [%d] - %s", call.Name, call.AgentURL, call.Message, status, err.Error())
	} else {
		msg = fmt.Sprintf("Invoked agent [%s] successfully on URL [%s] with input [%s]", call.Name, call.AgentURL, call.Message)
	}
	w.WriteHeader(status)
	util.AddLogMessage(msg, r)
	if stream {
		util.WriteJsonPayload(w, headers)
		util.AddLogMessage(msg, r)
	} else {
		for _, upOut := range output {
			for k, v := range upOut {
				if strings.Contains(k, "headers") || strings.Contains(k, "Headers") && v != nil && !reflect.ValueOf(v).IsNil() {
					if upstreamHeaders, ok := v.(map[string]any); ok {
						maps.Copy(headers, upstreamHeaders)
					}
				}
			}
		}
		output["headers"] = headers
		util.WriteJsonPayload(w, output)
	}
}

func streamAgentResponse(agent string, stream bool, output map[string]map[string]any, w http.ResponseWriter, r *http.Request) AgentResultsCallback {
	var fw http.Flusher
	if stream {
		if f, ok := w.(http.Flusher); ok {
			fw = f
		}
	}
	messages := []string{}
	send := func(id, msg string, data map[string]any, other any) {
		if stream && fw != nil {
			if msg != "" {
				fmt.Fprintln(w, msg)
			}
			if data != nil {
				util.WriteJsonPayload(w, data)
			}
			fw.Flush()
			msg := fmt.Sprintf("Received stream response from agent [%s][%s], response: %s", agent, id, msg)
			util.AddLogMessage(msg, r)
		} else {
			if output[id] == nil {
				output[id] = map[string]any{}
			}
			if data != nil {
				if len(data) == 1 {
					for k, v := range data {
						output[id][k] = v
					}
				} else {
					output[id][msg] = data
				}
			} else if other != nil && !reflect.ValueOf(other).IsNil() {
				output[id][msg] = other
			} else if msg != "" {
				messages = append(messages, msg)
				output[id]["messages"] = messages
			}
		}
	}
	return func(id, msg string, data any) {
		isArtifact := false
		if data != nil {
			_, isArtifact = data.(a2aproto.Artifact)
		}
		if !isArtifact {
			if !stream {
				if m, ok := data.(map[string]any); ok {
					send(id, msg, m, nil)
				} else {
					send(id, msg, nil, nil)
				}
			} else {
				send(id, msg, nil, data)
			}
		}
	}
}

func pushReceiver(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	msg := ""
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Error reading push notification body: %s", err.Error())
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	var notification map[string]interface{}
	if err := json.Unmarshal(body, &notification); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Error parsing notification JSON: %v", err)
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	// taskID, _ := notification["id"].(string)
}
