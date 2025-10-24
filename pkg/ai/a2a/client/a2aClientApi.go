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

package a2aclient

import (
	"encoding/json"
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"

	goa2aserver "trpc.group/trpc-go/trpc-a2a-go/server"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("a2a", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	a2aClientRouter := util.PathRouter(r, "/a2a/client")
	util.AddRouteMultiQ(a2aClientRouter, "/agent/card", fetchAgentCard, []string{"url", "authority"}, "GET")
	util.AddRoute(a2aClientRouter, "/agent/{agent}/call", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/call", callAgent, "POST")
	util.AddRoute(a2aClientRouter, "/push", pushReceiver, "POST")
}

func fetchAgentCard(w http.ResponseWriter, r *http.Request) {
	url := util.GetStringParamValue(r, "url")
	authority := util.GetStringParamValue(r, "authority")
	card, err := FetchAgentCard(r.Context(), url, authority, nil)
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
	msg := ""
	if err != nil {
		util.SendBadRequest(fmt.Sprintf("Failed to parse payload with error [%s]", err.Error()), w, r)
		return
	}
	var card *goa2aserver.AgentCard
	if name != "" {
		card = GetAgentCard(name)
	}
	if card == nil {
		if call.CardURL == "" {
			util.SendBadRequest(fmt.Sprintf("Agent card not loaded and missing agent card URL in the given call spec: %+v", call), w, r)
			return
		}
		card, err = FetchAgentCard(r.Context(), call.CardURL, call.Authority, call.Headers)
		if err != nil || card == nil {
			util.SendBadRequest(fmt.Sprintf("Error fetching agent card from url [%s], authority [%s]: %s", call.AgentURL, call.Authority, err.Error()), w, r)
			return
		}
	}
	var agentURL string
	if call.AgentURL != "" {
		agentURL = call.AgentURL
	} else {
		agentURL = card.URL
	}
	call.AgentURL = agentURL
	port := util.GetRequestOrListenerPortNum(r)
	session := NewA2ASession(r.Context(), port, card, call)
	err = session.Connect()
	if err != nil {
		msg = fmt.Sprintf("Failed to load agent card with error [%s]. Agent Call: %+v", err.Error(), call)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	err = session.CallAgent(streamAgentResponse(call.Name, w, r), nil, nil)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Error invoking agent [%s]: %s", call.Name, err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Invoked agent [%s] successfully on URL [%s] with input [%s], streamed result", call.Name, call.AgentURL, call.Message)
	}
	util.AddLogMessage(msg, r)
}

func streamAgentResponse(agent string, w http.ResponseWriter, r *http.Request) func(output string) {
	var fw http.Flusher
	return func(output string) {
		if fw == nil {
			if f, ok := w.(http.Flusher); ok {
				fw = f
			}
		}
		if fw != nil {
			w.Write([]byte(output))
			fw.Flush()
			msg := fmt.Sprintf("Received stream response from agent [%s], response: %s", agent, output)
			util.AddLogMessage(msg, r)
		} else {
			msg := fmt.Sprintf("Cannot get flush writer to send stream response from agent [%s]", agent)
			util.AddLogMessage(msg, r)
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
