/**
 * Copyright 2022 uk
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
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/util"
)

type Target struct {
  invocation.InvocationSpec
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

func (pc *TargetClient) init() bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.activeTargetsCount > 0 {
    return false
  }
  pc.targets = map[string]*Target{}
  pc.trackHeaders = []string{}
  pc.crossTrackHeaders = map[string][]string{}
  pc.trackTimeBuckets = [][]int{}
  return true
}

func (pc *TargetClient) AddTarget(t *Target, r ...*http.Request) error {
  invocationSpec := &t.InvocationSpec
  if err := invocation.ValidateSpec(invocationSpec); err == nil {
    pc.targetsLock.Lock()
    pc.targets[t.Name] = t
    pc.targetsLock.Unlock()
    invocation.RemoveHttpClientForTarget(t.Name)
    t.Headers = append(t.Headers, []string{constants.HeaderFromGoto, global.PeerName},
      []string{constants.HeaderFromGotoHost, util.GetHostLabel()})
    if t.AutoInvoke {
      go func() {
        if global.EnableClientLogs {
          log.Printf("Auto-invoking target: %s\n", t.Name)
        }
        if len(r) > 0 {
          invocationSpec = pc.prepareTargetForPeer(invocationSpec, r[0])
        }
        pc.invokeTarget(invocationSpec)
      }()
    }
    return nil
  } else {
    return err
  }
}

func (pc *TargetClient) removeTargets(targets []string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.activeTargetsCount > 0 {
    return false
  }
  for _, t := range targets {
    delete(pc.targets, t)
  }
  return true
}

func (pc *TargetClient) prepareTargetForPeer(target *invocation.InvocationSpec, r *http.Request) *invocation.InvocationSpec {
  if target == nil || r == nil {
    return target
  }
  peerName, _ := util.GetFillerUnmarked(target.Name)
  if peerName == "" {
    return target
  }
  peers := global.GetPeers(peerName, r)
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

func (pc *TargetClient) PrepareTarget(name string) *invocation.InvocationSpec {
  var targetToInvoke *invocation.InvocationSpec
  if name != "" {
    pc.targetsLock.RLock()
    target, found := pc.targets[name]
    if !found {
      target, found = pc.targets["{"+name+"}"]
    }
    pc.targetsLock.RUnlock()
    if found {
      targetToInvoke = &target.InvocationSpec
    }
  }
  return targetToInvoke
}

func (pc *TargetClient) getTargetsToInvoke(r *http.Request) []*invocation.InvocationSpec {
  var names []string
  if r != nil {
    names, _ = util.GetListParam(r, "targets")
  }
  var targetsToInvoke []*invocation.InvocationSpec
  if len(names) > 0 {
    for _, name := range names {
      if t := pc.prepareTargetForPeer(pc.PrepareTarget(name), r); t != nil {
        targetsToInvoke = append(targetsToInvoke, t)
      }
    }
  } else {
    for _, target := range pc.targets {
      targetsToInvoke = append(targetsToInvoke, &target.InvocationSpec)
    }
  }
  return targetsToInvoke
}

func (pc *TargetClient) AddTrackingHeaders(headers string) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.trackHeaders, pc.crossTrackHeaders = util.ParseTrackingHeaders(headers)
}

func (pc *TargetClient) clearTrackingHeaders() {
  pc.targetsLock.Lock()
  pc.trackHeaders = []string{}
  pc.crossTrackHeaders = map[string][]string{}
  pc.targetsLock.Unlock()
}

func (pc *TargetClient) getTrackingHeaders() []string {
  headers := []string{}
  pc.targetsLock.RLock()
  for _, h := range pc.trackHeaders {
    if crossHeaders := pc.crossTrackHeaders[h]; crossHeaders != nil {
      headers = append(headers, strings.Join([]string{h, strings.Join(crossHeaders, "|")}, "|"))
    }
    headers = append(headers, h)
  }
  pc.targetsLock.RUnlock()
  return headers
}

func (pc *TargetClient) AddTrackingTimeBuckets(b string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  buckets, ok := util.ParseTimeBuckets(b)
  if ok {
    pc.trackTimeBuckets = buckets
  }
  return ok
}

func (pc *TargetClient) clearTrackingTimeBuckets() {
  pc.targetsLock.Lock()
  pc.trackTimeBuckets = [][]int{}
  pc.targetsLock.Unlock()
}

func (pc *TargetClient) stopTargets(targetNames []string) (bool, bool) {
  pc.targetsLock.RLock()
  defer pc.targetsLock.RUnlock()
  stoppingTargets := []string{}
  if len(targetNames) > 0 {
    for _, tname := range targetNames {
      if len(tname) > 0 {
        if target, found := pc.targets[tname]; found {
          go invocation.StopTarget(target.Name)
          stoppingTargets = append(stoppingTargets, target.Name)
        }
      }
    }
  } else {
    if len(pc.targets) > 0 {
      for _, target := range pc.targets {
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

func (pc *TargetClient) invokeTarget(target *invocation.InvocationSpec) {
  if tracker, err := invocation.RegisterInvocation(target, results.ResultChannelSinkFactory(target, pc.trackHeaders, pc.crossTrackHeaders, pc.trackTimeBuckets)); err == nil {
    pc.targetsLock.Lock()
    pc.activeTargetsCount++
    pc.targetsLock.Unlock()
    events.SendEventJSON(Client_TargetInvoked, target.Name, tracker)
    invocation.StartInvocation(tracker)
    pc.targetsLock.Lock()
    pc.activeTargetsCount--
    pc.targetsLock.Unlock()
  } else {
    log.Println(err.Error())
  }
}
