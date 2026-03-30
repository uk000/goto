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
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"
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
	util.AddRoute(a2aClientRouter, "/call", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/push", pushReceiver, "POST")
}

func fetchAgentCard(w http.ResponseWriter, r *http.Request) {
	url := util.GetStringParamValue(r, "url")
	authority := util.GetStringParamValue(r, "authority")
	card, err := FetchAgentCard(r.Context(), url, authority, nil, r.Header)
	if err != nil {
		util.SendBadRequest(fmt.Sprintf("Error fetching agent card from url [%s], authority [%s]: %s", url, authority, err.Error()), w, r)
		return
	}
	msg := fmt.Sprintf("Fetched agent card successfully for agent [%s], from url [%s], authority [%s]", card.Name, url, authority)
	util.WriteJsonPayload(w, card)
	util.AddLogMessage(msg, r)
}

func callAgent(w http.ResponseWriter, r *http.Request) {
	call := &AgentCall{}
	name := util.GetStringParamValue(r, "agent")
	err := util.ReadJsonPayload(r, &call)
	if err != nil {
		util.SendBadRequest(fmt.Sprintf("Failed to parse payload with error [%s]", err.Error()), w, r)
		return
	}
	if name != "" {
		call.Name = name
	}
	port := util.GetRequestOrListenerPortNum(r)
	stream := strings.Contains(r.RequestURI, "stream")
	output := map[string][]any{}
	err = CallAgent(r.Context(), port, call, streamAgentResponse(call.Name, stream, output, w, r), r.Header)
	if err != nil {
		msg := fmt.Sprintf("Error invoking agent [%s]: %s", call.Name, err.Error())
		util.SendBadRequest(msg, w, r)
		return
	} else {
		if stream {
			msg := fmt.Sprintf("Invoked agent [%s] successfully on URL [%s] with input [%s], streamed result", call.Name, call.AgentURL, call.Message)
			util.AddLogMessage(msg, r)
		} else {
			msg := fmt.Sprintf("Invoked agent [%s] successfully on URL [%s] with input [%s], JSON result", call.Name, call.AgentURL, call.Message)
			util.AddLogMessage(msg, r)
			util.WriteJsonPayload(w, output)
		}
	}
}

func streamAgentResponse(agent string, stream bool, output map[string][]any, w http.ResponseWriter, r *http.Request) AgentResultsCallback {
	var fw http.Flusher
	if stream {
		if f, ok := w.(http.Flusher); ok {
			fw = f
		}
	}
	send := func(id, msg string, data any) {
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
			if msg != "" {
				output[id] = append(output[id], msg)
			}
			if data != nil {
				output[id] = append(output[id], data)
			}

		}
	}
	return func(id, msg string, data any) {
		isArtifact := false
		if data != nil {
			_, isArtifact = data.(a2aproto.Artifact)
		}
		if !isArtifact {
			send(id, msg, data)
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
