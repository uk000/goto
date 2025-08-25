package mcpserver

import "time"

type TrackerByPortServer[T any] map[int]map[string]map[string]T

var (
	InitCallsByPortServer        = TrackerByPortServer[map[string]int]{}
	ToolCallsByPortServer        = TrackerByPortServer[int]{}
	ToolCallsByPortServerSession = TrackerByPortServer[int]{}
	ToolCallSuccessByPortServer  = TrackerByPortServer[int]{}
	ToolCallFailureByPortServer  = TrackerByPortServer[int]{}
	ToolCallDurationByPortServer = TrackerByPortServer[int64]{}
)

func trackPortServer[T any](port int, server string, m TrackerByPortServer[T]) map[string]T {
	if m[port] == nil {
		m[port] = map[string]map[string]T{}
	}
	if m[port][server] == nil {
		m[port][server] = map[string]T{}
	}
	return m[port][server]
}

func TrackInitCall(port int, server, client, protocol string) {
	trackPortServer(port, server, InitCallsByPortServer)
}

func TrackToolCall(port int, server, sessionID, tool string) {
	trackPortServer(port, server, ToolCallsByPortServer)[tool]++
	trackPortServer(port, server, ToolCallsByPortServerSession)[sessionID]++
}

func TrackToolCallResult(port int, server, tool string, duration time.Duration, success bool) {
	smap := trackPortServer(port, server, ToolCallSuccessByPortServer)
	fmap := trackPortServer(port, server, ToolCallFailureByPortServer)
	if success {
		smap[tool]++
	} else {
		fmap[tool]++
	}
	dmap := trackPortServer(port, server, ToolCallDurationByPortServer)
	total := smap[tool] + fmap[tool]
	dmap[tool] = (dmap[tool]*int64(total-1) + duration.Milliseconds()) / int64(total)
}
