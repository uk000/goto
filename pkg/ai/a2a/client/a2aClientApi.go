package a2aclient

import (
	"encoding/json"
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("a2a", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	a2aClientRouter := util.PathRouter(r, "/a2a/client")
	util.AddRouteMultiQWithPort(a2aClientRouter, "/agent/card", getAgentCard, []string{"url", "authority"}, "GET")
	util.AddRouteWithPort(a2aClientRouter, "/invoke", invokeAgent, "POST")
	util.AddRouteWithPort(a2aClientRouter, "/push", pushReceiver, "POST")
}

func getAgentCard(w http.ResponseWriter, r *http.Request) {
	url := util.GetStringParamValue(r, "url")
	authority := util.GetStringParamValue(r, "authority")
	msg := ""
	card, err := GetAgentCard(r.Context(), url, authority, nil)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Error fetching agent card from url [%s], authority [%s]: %s", url, authority, err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Fetched agent card successfully for agent [%s], from url [%s], authority [%s]", card.Name, url, authority)
		util.WriteJsonPayload(w, card)
	}
	util.AddLogMessage(msg, r)
}

func invokeAgent(w http.ResponseWriter, r *http.Request) {
	ac := &AgentCall{}
	err := util.ReadJsonPayload(r, &ac)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Failed to parse payload with error [%s]", err.Error())
	} else if ac.Name == "" || ac.URL == "" {
		msg = fmt.Sprintf("Missing agent name/URL: %+v", ac)
	}
	if msg != "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	port := util.GetRequestOrListenerPortNum(r)
	client := NewA2AClient(port)
	session, err := client.ConnectWithAgentCard(r.Context(), ac)
	if err != nil {
		msg = fmt.Sprintf("Failed to load agent card with error [%s]. Agent Call: %+v", err.Error(), ac)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	err = session.CallAgent(streamAgentResponse(ac.Name, w, r), nil, nil)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Error invoking agent [%s]: %s", ac.Name, err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Invoked agent [%s] successfully on URL [%s] with input [%s], streamed result", ac.Name, ac.URL, ac.Message)
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
