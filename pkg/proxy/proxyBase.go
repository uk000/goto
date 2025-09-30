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

package proxy

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"
)

type Target interface {
	GetName() string
	SetDelay(delayMin, delayMax time.Duration, delayCount int)
	ClearDelay()
	SetDropPct(drop int)
	Enable()
	Disable()
	IsRunning() bool
	Stop()
	Close()
	GetProxyTarget() *ProxyTarget
	GetHTTPTarget() *HTTPTarget
}

type ProxyTarget struct {
	parent             Target
	Name               string            `json:"name"`
	Protocol           string            `json:"protocol"`
	Endpoint           string            `json:"endpoint"`
	Authority          string            `json:"authority"`
	MatchAny           *ProxyTargetMatch `json:"matchAny"`
	MatchAll           *ProxyTargetMatch `json:"matchAll"`
	Replicas           int               `json:"replicas"`
	DelayMin           time.Duration     `json:"delayMin"`
	DelayMax           time.Duration     `json:"delayMax"`
	DelayCount         int               `json:"delayCount"`
	DropPct            int               `json:"dropPct"`
	Retries            int               `json:"retries"`
	RetryDelay         time.Duration     `json:"retryDelay"`
	Enabled            bool              `json:"enabled"`
	uriRegexps         map[string]*regexp.Regexp
	isTCP              bool
	callCount          int
	writeSinceLastDrop int
	isRunning          bool
	stopChan           chan bool
	lock               sync.RWMutex
}

type Proxy struct {
	Port    int               `json:"port"`
	Targets map[string]Target `json:"targets"`
	Enabled bool              `json:"enabled"`
	lock    sync.RWMutex
}

type ProxyTargetMatch struct {
	Headers   [][]string
	Query     [][]string
	SNI       []string
	sniRegexp *regexp.Regexp
}

type TargetMatchInfo struct {
	Headers [][]string
	Query   [][]string
	URI     string
	SNI     string
	target  *ProxyTarget
}

func newProxy(port int) *Proxy {
	return &Proxy{
		Port:    port,
		Enabled: true,
		Targets: map[string]Target{},
	}
}

func newProxyTarget(name, protocol, endpoint string) *ProxyTarget {
	return &ProxyTarget{
		Name:       name,
		Protocol:   protocol,
		Endpoint:   endpoint,
		Replicas:   1,
		uriRegexps: map[string]*regexp.Regexp{},
		RetryDelay: 10 * time.Second,
		Enabled:    true,
	}
}

func (p *Proxy) init() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Targets = map[string]Target{}
}

func (p *Proxy) hasAnyTargets() bool {
	return len(p.Targets) > 0
}

func (p *Proxy) enable(enabled bool) {
	p.Enabled = enabled
}

func (p *Proxy) checkAndGetTarget(w http.ResponseWriter, r *http.Request) Target {
	name := util.GetStringParamValue(r, "target")
	p.lock.RLock()
	target := p.Targets[name]
	p.lock.RUnlock()
	if target == nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid target: %s\n", name)
	}
	return target
}

func (p *Proxy) setProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	msg := ""
	if delayMin, delayMax, delayCount, ok := util.GetDurationParam(r, "delay"); ok {
		if delayMin > 0 || delayMax > 0 {
			if delayCount == 0 {
				delayCount = -1 //forever
			}
		}
		target.SetDelay(delayMin, delayMax, delayCount)
		msg = fmt.Sprintf("Proxy[%d]: Target [%s] Delay set to [Min=%s, Max=%s, Count=%d]", p.Port, target.GetName(), delayMin, delayMax, delayCount)
		w.WriteHeader(http.StatusOK)
	} else {
		msg = fmt.Sprintf("Invalid delay param [%s]", util.GetStringParamValue(r, "delay"))
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func (p *Proxy) clearProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	msg := ""
	target.ClearDelay()
	msg = fmt.Sprintf("Proxy[%d]: Target [%s] Delay Cleared", p.Port, target.GetName())
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func (p *Proxy) setProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	msg := ""
	if drop := util.GetIntParamValue(r, "drop"); drop > 0 {
		target.SetDropPct(drop)
		msg = fmt.Sprintf("Proxy[%d]: Will drop [%d]%s packets for Target [%s] ", p.Port, drop, "%", target.GetName())
		w.WriteHeader(http.StatusOK)
	} else {
		msg = fmt.Sprintf("Invalid drops param [%s]", util.GetStringParamValue(r, "drops"))
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func (p *Proxy) clearProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	msg := ""
	target.SetDropPct(0)
	msg = fmt.Sprintf("Proxy[%d]: Target [%s] Drops Cleared", p.Port, target.GetName())
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func (p *Proxy) deleteProxyTarget(targetName string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.Targets, targetName)
}

func (p *Proxy) removeProxyTarget(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	p.deleteProxyTarget(target.GetName())
	util.AddLogMessage(fmt.Sprintf("Removed proxy target: %+v", target), r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Port [%d]: Removed proxy target: %s\n", p.Port, util.ToJSONText(target))
	events.SendRequestEventJSON("Proxy Target Removed", target.GetName(), target, r)
}

func (p *Proxy) enableProxyTarget(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	p.lock.Lock()
	target.Enable()
	p.lock.Unlock()
	msg := fmt.Sprintf("Port [%d]: Enabled proxy target: %s", p.Port, target.GetName())
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
	events.SendRequestEvent("Proxy Target Enabled", msg, r)
}

func (p *Proxy) disableProxyTarget(w http.ResponseWriter, r *http.Request) {
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	target.Disable()
	msg := fmt.Sprintf("Port [%d]: Disabled proxy target: %s", p.Port, target.GetName())
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("Proxy Target Disabled", msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func (p *Proxy) shouldDrop(target *ProxyTarget) bool {
	target.lock.Lock()
	defer target.lock.Unlock()
	if target.DropPct <= 0 {
		return false
	}
	target.writeSinceLastDrop++
	if target.writeSinceLastDrop >= (100 / target.DropPct) {
		target.writeSinceLastDrop = 0
		return true
	}
	return false
}

func (p *Proxy) applyDelay(target *ProxyTarget, who string, w http.ResponseWriter) bool {
	delay := target.ApplyDelay()
	if global.Flags.EnableProxyDebugLogs && delay != "" {
		log.Printf("[DEBUG] Proxy[%d]: Delayed [%s] for Target [%s] by [%s]\n", p.Port, who, target.Name, delay)
		if w != nil {
			w.Header().Add(constants.HeaderGotoProxyDelay, delay)
		}
		return true
	}
	return false
}

func (t *ProxyTarget) GetName() string {
	return t.Name
}

func (t *ProxyTarget) SetDelay(delayMin, delayMax time.Duration, delayCount int) {
	t.DelayMin = delayMin
	t.DelayMax = delayMax
	t.DelayCount = delayCount
}

func (t *ProxyTarget) ClearDelay() {
	t.DelayMin = 0
	t.DelayMax = 0
	t.DelayCount = -1
}

func (t *ProxyTarget) HasDelay() bool {
	return (t.DelayCount > 0 || t.DelayCount == -1) && (t.DelayMin > 0 || t.DelayMax > 0)
}

func (t *ProxyTarget) ApplyDelay() (delay string) {
	if t.DelayCount > 0 || t.DelayCount == -1 {
		d := types.RandomDuration(t.DelayMin, t.DelayMax)
		delay = d.String()
		if global.Flags.EnableProxyDebugLogs {
			log.Printf("[DEBUG] Target [%s]: Delaying Upstream by [%s]\n", t.Name, delay)
		}
		time.Sleep(d)
		if t.DelayCount > 0 {
			t.lock.Lock()
			t.DelayCount--
			t.lock.Unlock()
		}
	}
	return
}

func (t *ProxyTarget) SetDropPct(drop int) {
	t.DropPct = drop
}

func (t *ProxyTarget) Enable() {
	t.Enabled = true
}

func (t *ProxyTarget) Disable() {
	t.Enabled = false
}

func (t *ProxyTarget) IsRunning() bool {
	return t.isRunning
}

func (t *ProxyTarget) Stop() {
	t.stopChan <- true
}

func (t *ProxyTarget) Close() {
	close(t.stopChan)
}

func (t *ProxyTarget) GetProxyTarget() *ProxyTarget {
	if t.parent != nil {
		return t.parent.GetProxyTarget()
	}
	return t
}

func (t *ProxyTarget) GetHTTPTarget() *HTTPTarget {
	if t.parent != nil {
		return t.parent.GetHTTPTarget()
	}
	return nil
}
