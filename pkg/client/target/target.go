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

package target

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"goto/pkg/client/results"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"
)

type Target struct {
	*invocation.InvocationSpec
}

type TargetClient struct {
	clientPort         int
	targets            map[string]*Target
	activeTargetsCount int
	targetsLock        sync.RWMutex
	trackHeaders       []string
	crossTrackHeaders  map[string][]string
	trackTimeBuckets   [][]int
}

var (
	Client                     = NewTargetClient(global.Self.ServerPort)
	portClientsLock            sync.RWMutex
	InvocationResultsRetention int = 100
)

// NewTargetClient creates a target client for the client port, with no targets and no tracking config.
func NewTargetClient(clientPort int) *TargetClient {
	c := &TargetClient{clientPort: clientPort}
	c.init()
	return c
}

// init clears the targets and tracking config, returning false and changing nothing if a target is being invoked.
func (tc *TargetClient) init() bool {
	tc.targetsLock.Lock()
	defer tc.targetsLock.Unlock()
	if tc.activeTargetsCount > 0 {
		return false
	}
	tc.targets = map[string]*Target{}
	tc.trackHeaders = []string{}
	tc.crossTrackHeaders = map[string][]string{}
	tc.trackTimeBuckets = [][]int{}
	return true
}

// AddTarget validates and registers a target, tagging its headers with this goto, and auto-invokes it if asked.
func (tc *TargetClient) AddTarget(t *Target, r ...*http.Request) error {
	invocationSpec := t.InvocationSpec
	if err := invocation.ValidateSpec(invocationSpec); err == nil {
		// The headers are set before the target is published: once it is in the map it is read (and encoded) by
		// other requests, which don't expect its headers map to change.
		t.Headers[constants.HeaderFromGoto] = global.Self.Name
		t.Headers[constants.HeaderFromGotoHost] = global.Self.HostLabel
		tc.targetsLock.Lock()
		tc.targets[t.Name] = t
		tc.targetsLock.Unlock()
		invocation.RemoveHttpClientForTarget(t.Name)
		if t.AutoInvoke {
			go func() {
				if global.Flags.EnableClientLogs {
					log.Printf("Auto-invoking target: %s\n", t.Name)
				}
				if len(r) > 0 {
					invocationSpec = tc.prepareTargetForPeer(invocationSpec, r[0])
				}
				tc.invokeTarget(invocationSpec)
			}()
		}
		return nil
	} else {
		return err
	}
}

// removeTargets deletes the named targets, refusing (false) while any target is active.
func (tc *TargetClient) removeTargets(targets []string) bool {
	tc.targetsLock.Lock()
	defer tc.targetsLock.Unlock()
	if tc.activeTargetsCount > 0 {
		return false
	}
	for _, t := range targets {
		delete(tc.targets, t)
	}
	return true
}

// prepareTargetForPeer resolves a peer placeholder in the target's URL to the peer's address, returning a modified copy of the target.
func (tc *TargetClient) prepareTargetForPeer(target *invocation.InvocationSpec, r *http.Request) *invocation.InvocationSpec {
	if target == nil || r == nil {
		return target
	}
	peerName, _ := util.GetFillerUnmarked(target.Name)
	if peerName == "" {
		return target
	}
	peers := global.Funcs.GetPeers(peerName, r)
	if peers == nil || len(peers) == 0 {
		return target
	}
	if strings.Contains(target.URL, "{") && strings.Contains(target.URL, "}") {
		urlPre := strings.Split(target.URL, "{")[0]
		urlPost := strings.Split(target.URL, "}")[1]
		address := peers[peerName]
		var newTarget = *target
		newTarget.Name = peerName
		newTarget.URL = urlPre + address + urlPost
		target = &newTarget
	}
	return target
}

// PrepareTarget returns the invocation spec of the named target, also trying its "{name}" placeholder form, or nil if not found.
func (tc *TargetClient) PrepareTarget(name string) *invocation.InvocationSpec {
	var targetToInvoke *invocation.InvocationSpec
	if name != "" {
		tc.targetsLock.RLock()
		target, found := tc.targets[name]
		if !found {
			target, found = tc.targets["{"+name+"}"]
		}
		tc.targetsLock.RUnlock()
		if found {
			targetToInvoke = target.InvocationSpec
		}
	}
	return targetToInvoke
}

// getTargetsToInvoke returns the targets named in the request's targets param, resolved for peers, or all targets if none are named.
func (tc *TargetClient) getTargetsToInvoke(r *http.Request) []*invocation.InvocationSpec {
	var names []string
	if r != nil {
		names, _ = util.GetListParam(r, "targets")
	}
	var targetsToInvoke []*invocation.InvocationSpec
	if len(names) > 0 {
		for _, name := range names {
			if t := tc.prepareTargetForPeer(tc.PrepareTarget(name), r); t != nil {
				targetsToInvoke = append(targetsToInvoke, t)
			}
		}
	} else {
		tc.targetsLock.RLock()
		for _, target := range tc.targets {
			targetsToInvoke = append(targetsToInvoke, target.InvocationSpec)
		}
		tc.targetsLock.RUnlock()
	}
	return targetsToInvoke
}

// getTargetsPayload returns the named target, or all targets if the name is empty, as indented JSON, and whether the
// named target exists. The targets are encoded while holding the targets lock, since the targets map is updated under
// that lock, and encoding a map while it is being updated is a fatal error that can't be recovered.
func (tc *TargetClient) getTargetsPayload(name string) (string, bool) {
	tc.targetsLock.RLock()
	defer tc.targetsLock.RUnlock()
	var v any = tc.targets
	if name != "" {
		t := tc.targets[name]
		if t == nil {
			return "", false
		}
		v = t
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("Failed to encode targets: %s\n", err.Error())
		return "", true
	}
	return string(b), true
}

// getTrackingTimeBuckets returns the tracked time buckets.
func (tc *TargetClient) getTrackingTimeBuckets() [][]int {
	tc.targetsLock.RLock()
	defer tc.targetsLock.RUnlock()
	return tc.trackTimeBuckets
}

// getTrackingConfig returns the tracked headers, cross headers and time buckets. They are replaced as a whole when
// they change, never updated in place, so they can be used after the lock is released.
func (tc *TargetClient) getTrackingConfig() ([]string, map[string][]string, [][]int) {
	tc.targetsLock.RLock()
	defer tc.targetsLock.RUnlock()
	return tc.trackHeaders, tc.crossTrackHeaders, tc.trackTimeBuckets
}

// AddTrackingHeaders parses and sets the headers, and cross headers, whose values are tracked in the results.
func (tc *TargetClient) AddTrackingHeaders(headers string) {
	tc.targetsLock.Lock()
	defer tc.targetsLock.Unlock()
	tc.trackHeaders, tc.crossTrackHeaders = util.ParseTrackingHeaders(headers)
}

// clearTrackingHeaders removes all tracked headers and cross headers.
func (tc *TargetClient) clearTrackingHeaders() {
	tc.targetsLock.Lock()
	tc.trackHeaders = []string{}
	tc.crossTrackHeaders = map[string][]string{}
	tc.targetsLock.Unlock()
}

// getTrackingHeaders returns the tracked headers, each header that has cross headers preceded by its "header|cross1|cross2" form.
func (tc *TargetClient) getTrackingHeaders() []string {
	headers := []string{}
	tc.targetsLock.RLock()
	for _, h := range tc.trackHeaders {
		if crossHeaders := tc.crossTrackHeaders[h]; crossHeaders != nil {
			headers = append(headers, strings.Join([]string{h, strings.Join(crossHeaders, "|")}, "|"))
		}
		headers = append(headers, h)
	}
	tc.targetsLock.RUnlock()
	return headers
}

// AddTrackingTimeBuckets parses and sets the time buckets for tracking response times, returning false if the spec is invalid.
func (tc *TargetClient) AddTrackingTimeBuckets(b string) bool {
	tc.targetsLock.Lock()
	defer tc.targetsLock.Unlock()
	buckets, ok := util.ParseTimeBuckets(b)
	if ok {
		tc.trackTimeBuckets = buckets
	}
	return ok
}

// clearTrackingTimeBuckets removes all tracked time buckets.
func (tc *TargetClient) clearTrackingTimeBuckets() {
	tc.targetsLock.Lock()
	tc.trackTimeBuckets = [][]int{}
	tc.targetsLock.Unlock()
}

// stopTargets stops the named targets (all if none named), waits up to 10s for them to go idle, and returns whether any were stopping and whether they stopped.
func (tc *TargetClient) stopTargets(targetNames []string) (bool, bool) {
	stoppingTargets := []string{}
	tc.targetsLock.RLock()
	if len(targetNames) > 0 {
		for _, tname := range targetNames {
			if len(tname) > 0 {
				if target, found := tc.targets[tname]; found {
					stoppingTargets = append(stoppingTargets, target.Name)
				}
			}
		}
	} else {
		for _, target := range tc.targets {
			stoppingTargets = append(stoppingTargets, target.Name)
		}
	}
	tc.targetsLock.RUnlock()
	// The lock isn't held while waiting for the targets to stop: that can take 10s, and a writer waiting for the lock
	// would hold up all readers meanwhile.
	for _, name := range stoppingTargets {
		go invocation.StopTarget(name)
	}
	stopped := len(stoppingTargets) == 0
	if !stopped {
		for i := 0; i < 10; i++ {
			if !invocation.IsAnyTargetActive(stoppingTargets) {
				stopped = true
				break
			}
			time.Sleep(time.Second * 1)
		}
	}
	if len(targetNames) == 0 { //Reset active invocations if all targets were stopped
		invocation.ResetActiveInvocations()
	}
	return len(stoppingTargets) > 0, stopped
}

// invokeTarget registers and runs an invocation of the target, counting it as active until it finishes.
func (tc *TargetClient) invokeTarget(target *invocation.InvocationSpec) {
	trackHeaders, crossTrackHeaders, trackTimeBuckets := tc.getTrackingConfig()
	if tracker, err := invocation.RegisterInvocation(tc.clientPort, target, results.ResultChannelSinkFactory(target, trackHeaders, crossTrackHeaders, trackTimeBuckets)); err == nil {
		tc.targetsLock.Lock()
		tc.activeTargetsCount++
		tc.targetsLock.Unlock()
		invocation.StartInvocation(tracker)
		tc.targetsLock.Lock()
		tc.activeTargetsCount--
		tc.targetsLock.Unlock()
	} else {
		log.Println(err.Error())
	}
}

// InvokeAll invokes all targets concurrently and waits for them to finish.
func (tc *TargetClient) InvokeAll() {
	tc.targetsLock.RLock()
	specs := make([]*invocation.InvocationSpec, 0, len(tc.targets))
	for _, t := range tc.targets {
		specs = append(specs, t.InvocationSpec)
	}
	tc.targetsLock.RUnlock()
	wg := &sync.WaitGroup{}
	for _, spec := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tc.invokeTarget(spec)
		}()
	}
	wg.Wait()
}
