package a2a

import (
	"encoding/json"
	"goto/pkg/rpc/jsonrpc"
	"net/http"
)

// TextInputParams represents the params for a text input interaction.
type TextInputParams struct {
	SessionID string `json:"sessionId"`
	Query     string `json:"query"`
}

// TextInputResult represents the result for a text input interaction.
type TextInputResult struct {
	SessionID string `json:"sessionId"`
	Response  string `json:"response"`
}

// handleA2A handles incoming A2A protocol requests using JSON-RPC 2.0.
func handleA2A(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req jsonrpc.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, nil, -32700, "Parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, -32600, "Invalid Request: jsonrpc must be '2.0'")
		return
	}

	switch req.Method {
	case "text.input":
		var params TextInputParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONRPCError(w, req.ID, -32602, "Invalid params")
			return
		}
		result := TextInputResult{
			SessionID: params.SessionID,
			Response:  "You said: " + params.Query,
		}
		resp := jsonrpc.JSONRPCResponse{
			JSONRPCMessage: jsonrpc.JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      req.ID,
			},
			Result: result,
		}
		writeJSON(w, resp)
	default:
		writeJSONRPCError(w, req.ID, -32601, "Method not found")
	}
}

func writeJSONRPCError(w http.ResponseWriter, id interface{}, code int, message string) {
	resp := jsonrpc.JSONRPCResponse{
		JSONRPCMessage: jsonrpc.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
		},
		Error: &jsonrpc.JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// RegisterHandlers registers the REST API endpoints.
func RegisterHandlers() {
	http.HandleFunc("/a2a", handleA2A)
}
