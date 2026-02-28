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
