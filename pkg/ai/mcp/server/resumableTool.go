package mcpserver

import (
	registryclient "goto/pkg/registry/client"
)

var (
	MCPRegistryClient *registryclient.RegistryClient
)

func getRegistryClient() *registryclient.RegistryClient {
	if MCPRegistryClient == nil {
		MCPRegistryClient = registryclient.NewRegistryClient()
	}
	return MCPRegistryClient
}

func (t *ToolCallContext) saveState(ts *ToolState) error {
	rc := getRegistryClient()
	if lc, err := rc.OpenLocker(t.sessionID, t.Kind); err != nil {
		return err
	} else {
		lc.Store([]string{t.Name}, ts)
	}
	return nil
}

func (t *ToolCallContext) loadState() (*ToolState, error) {
	rc := getRegistryClient()
	lc, err := rc.OpenLocker(t.sessionID, t.Kind)
	if err != nil {
		return nil, err
	}
	ts := &ToolState{}
	err = lc.LoadJSON([]string{t.Name}, ts)
	return ts, err
}
