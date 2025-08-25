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

package a2a

import (
	"encoding/json"
	"goto/pkg/constants"
	"goto/pkg/rpc/jsonrpc"
	"net/http"
)

const (
	// A2AProtocol is the name of the A2A protocol.
	SessionID = "sessionId"
	Query     = "query"
	Response  = "response"
)

// TextInputParams represents the params for a text input interaction.
func TextInputParams(sessionID, query string) map[string]any {
	return map[string]any{
		SessionID: sessionID,
		Query:     query,
	}
}

// TextInputResult represents the result for a text input interaction.
func TextInputResult(sessionID, response string) map[string]any {
	return map[string]any{
		SessionID: sessionID,
		Response:  response,
	}
}

// handleA2A handles incoming A2A protocol requests using JSON-RPC 2.0.
func handleA2A(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req jsonrpc.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, "", -32700, "Parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, -32600, "Invalid Request: jsonrpc must be '2.0'")
		return
	}

	switch req.Method {
	case "text.input":
		result := TextInputResult(req.Params["sessionID"].(string), "Your Query: "+req.Params["query"].(string))
		resp := jsonrpc.NewJSONRPCResponse(req.ID, result)
		writeJSON(w, resp)
	default:
		writeJSONRPCError(w, req.ID, -32601, "Method not found")
	}
}

func writeJSONRPCError(w http.ResponseWriter, id string, code int, message string) {
	resp := jsonrpc.NewJSONRPCError(id, code, message, nil)
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set(constants.HeaderContentType, "application/json")
	json.NewEncoder(w).Encode(v)
}

// RegisterHandlers registers the REST API endpoints.
func RegisterHandlers() {
	http.HandleFunc("/a2a", handleA2A)
}
