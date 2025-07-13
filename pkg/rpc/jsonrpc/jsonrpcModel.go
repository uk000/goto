package jsonrpc

import "goto/pkg/util"

type JSONRPCMessage struct {
	ID      interface{} `json:"id"`
	JSONRPC string      `json:"jsonrpc,omitempty"`
}

type JSONRPCRequest struct {
	JSONRPCMessage
	Method string `json:"method"`
	Params []byte `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPCMessage
	Result any           `json:"result,omitempty"`
	Error  *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    util.JSON `json:"data,omitempty"`
}
