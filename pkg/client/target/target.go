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

package target

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"goto/pkg/client/results"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"
)

type Target struct {
	*invocation.InvocationSpec
}

type TargetClient struct {
	targets            map[string]*Target
	activeTargetsCount int
	targetsLock        sync.RWMutex
	trackHeaders       []string
	crossTrackHeaders  map[string][]string
	trackTimeBuckets   [][]int
}

var (
	Client                     = NewTargetClient()
	portClientsLock            sync.RWMutex
	InvocationResultsRetention int = 100
)

func NewTargetClient() *TargetClient {
	c := &TargetClient{}
	c.init()
	return c
}

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

func (tc *TargetClient) AddTarget(t *Target, r ...*http.Request) error {
	invocationSpec := t.InvocationSpec
	if err := invocation.ValidateSpec(invocationSpec); err == nil {
		tc.targetsLock.Lock()
		tc.targets[t.Name] = t
		tc.targetsLock.Unlock()
		invocation.RemoveHttpClientForTarget(t.Name)
		t.Headers = append(t.Headers, []string{constants.HeaderFromGoto, global.Self.Name},
			[]string{constants.HeaderFromGotoHost, global.Self.HostLabel})
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
		for _, target := range tc.targets {
			targetsToInvoke = append(targetsToInvoke, target.InvocationSpec)
		}
	}
	return targetsToInvoke
}

func (tc *TargetClient) AddTrackingHeaders(headers string) {
	tc.targetsLock.Lock()
	defer tc.targetsLock.Unlock()
	tc.trackHeaders, tc.crossTrackHeaders = util.ParseTrackingHeaders(headers)
}

func (tc *TargetClient) clearTrackingHeaders() {
	tc.targetsLock.Lock()
	tc.trackHeaders = []string{}
	tc.crossTrackHeaders = map[string][]string{}
	tc.targetsLock.Unlock()
}

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

func (tc *TargetClient) AddTrackingTimeBuckets(b string) bool {
	tc.targetsLock.Lock()
	defer tc.targetsLock.Unlock()
	buckets, ok := util.ParseTimeBuckets(b)
	if ok {
		tc.trackTimeBuckets = buckets
	}
	return ok
}

func (tc *TargetClient) clearTrackingTimeBuckets() {
	tc.targetsLock.Lock()
	tc.trackTimeBuckets = [][]int{}
	tc.targetsLock.Unlock()
}

func (tc *TargetClient) stopTargets(targetNames []string) (bool, bool) {
	tc.targetsLock.RLock()
	defer tc.targetsLock.RUnlock()
	stoppingTargets := []string{}
	if len(targetNames) > 0 {
		for _, tname := range targetNames {
			if len(tname) > 0 {
				if target, found := tc.targets[tname]; found {
					go invocation.StopTarget(target.Name)
					stoppingTargets = append(stoppingTargets, target.Name)
				}
			}
		}
	} else {
		if len(tc.targets) > 0 {
			for _, target := range tc.targets {
				go invocation.StopTarget(target.Name)
				stoppingTargets = append(stoppingTargets, target.Name)
			}
		}
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

func (tc *TargetClient) invokeTarget(target *invocation.InvocationSpec) {
	if tracker, err := invocation.RegisterInvocation(target, results.ResultChannelSinkFactory(target, tc.trackHeaders, tc.crossTrackHeaders, tc.trackTimeBuckets)); err == nil {
		tc.targetsLock.Lock()
		tc.activeTargetsCount++
		tc.targetsLock.Unlock()
		events.SendEventJSON(events.Client_TargetInvoked, target.Name, tracker)
		invocation.StartInvocation(tracker)
		tc.targetsLock.Lock()
		tc.activeTargetsCount--
		tc.targetsLock.Unlock()
	} else {
		log.Println(err.Error())
	}
}

func (tc *TargetClient) InvokeAll() {
	wg := &sync.WaitGroup{}
	for _, t := range tc.targets {
		wg.Add(1)
		go func() {
			tc.invokeTarget(t.InvocationSpec)
			wg.Done()
		}()
	}
	wg.Wait()
}
