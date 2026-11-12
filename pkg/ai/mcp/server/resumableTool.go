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

package mcpserver

import (
	registryclient "goto/pkg/registry/client"
	"sync"
)

var (
	MCPRegistryClient  *registryclient.RegistryClient
	registryClientLock sync.Mutex
)

// getRegistryClient gives the client of the registry, which is made when it is first needed, by whichever of the
// resumable calls that run at the same time gets here first.
func getRegistryClient() *registryclient.RegistryClient {
	registryClientLock.Lock()
	defer registryClientLock.Unlock()
	if MCPRegistryClient == nil {
		MCPRegistryClient = registryclient.NewRegistryClient()
	}
	return MCPRegistryClient
}

// saveState stores the state of the call in the registry, to let a resumable tool continue it later.
func (tctx *ToolContext) saveState(ts *ToolState) error {
	rc := getRegistryClient()
	if lc, err := rc.OpenLocker(tctx.sessionID, tctx.Kind); err != nil {
		return err
	} else {
		lc.Store([]string{tctx.Name}, ts)
	}
	return nil
}

// loadState loads the state that a resumable tool stored in the registry for the session.
func (tctx *ToolContext) loadState() (*ToolState, error) {
	rc := getRegistryClient()
	lc, err := rc.OpenLocker(tctx.sessionID, tctx.Kind)
	if err != nil {
		return nil, err
	}
	ts := &ToolState{}
	err = lc.LoadJSON([]string{tctx.Name}, ts)
	return ts, err
}
