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

package httpproxy

import (
	"errors"
	"fmt"
	"goto/pkg/invocation"
	"goto/pkg/server/intercept"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sync"

	"github.com/gorilla/mux"
)

type ITarget interface {
	GetName() string
	Enable()
	Disable()
	IsRunning() bool
	Stop()
	Close()
}

type TargetEndpointResponse struct {
	target     string
	endpoint   string
	requestURI string
	url        string
	response   *invocation.InvocationResultResponse
}

type TargetMatch struct {
	URI       string                 `yaml:"uri" json:"uri"`
	URIPrefix string                 `yaml:"uriPrefix" json:"uriPrefix"`
	Headers   map[string]string      `yaml:"headers" json:"headers"`
	Vars      map[string]*util.Match `yaml:"vars" json:"vars"`
	uriRegexp *regexp.Regexp
	router    *mux.Router
}

type Keys struct {
	Add    map[string]string `yaml:"add" json:"add"`
	Remove []string          `yaml:"remove" json:"remove"`
	Lower  bool              `yaml:"lower" json:"lower"`
}

type TrafficConfig struct {
	Delay       *types.Delay `yaml:"delay" json:"delay"`
	Retries     int          `yaml:"retries" json:"retries"`
	RetryDelay  *types.Delay `yaml:"retryDelay" json:"retryDelay"`
	RetryOn     []int        `yaml:"retryOn" json:"retryOn"`
	Payload     bool         `yaml:"payload" json:"payload"`
	Transparent bool         `yaml:"transparent" json:"transparent"`
}

type TrafficTransform struct {
	URIMap         map[string]string `yaml:"uriMap" json:"uriMap"`
	Headers        *Keys             `yaml:"headers" json:"headers"`
	Queries        *Keys             `yaml:"queries" json:"queries"`
	Payload        string            `yaml:"payload" json:"payload"`
	StripURI       string            `yaml:"stripURI" json:"stripURI"`
	RequestId      *types.RequestId  `yaml:"requestId" json:"requestId"`
	stripURIRegexp *regexp.Regexp
}

type TargetEndpoint struct {
	URL          string `yaml:"url" json:"url"`
	Method       string `yaml:"method" json:"method"`
	Protocol     string `yaml:"protocol" json:"protocol"`
	Authority    string `yaml:"authority" json:"authority"`
	IsTLS        bool   `yaml:"tls" json:"tls"`
	RequestCount int    `yaml:"requestCount" json:"requestCount"`
	Concurrent   int    `yaml:"concurrent" json:"concurrent"`
	Stream       bool   `yaml:"stream" json:"stream"`
	CallCount    int    `yaml:"-" json:"callCount"`
	name         string
	target       *Target
	lock         sync.RWMutex
}

type EndpointInvocation struct {
	ep     *TargetEndpoint
	is     *invocation.InvocationSpec
	target *Target
}

type TargetTrigger struct {
	MatchAny      []*TargetMatch    `yaml:"matchAny" json:"matchAny"`
	Endpoints     []string          `yaml:"endpoints" json:"endpoints"`
	Transform     *TrafficTransform `yaml:"transform" json:"transform"`
	TrafficConfig *TrafficConfig    `yaml:"trafficConfig" json:"trafficConfig"`
	CallCount     int               `yaml:"-" json:"callCount"`
	name          string
	epSpecs       map[string]*EndpointInvocation
	exactMatches  []*TargetMatch
	prefixMatches []*TargetMatch
	lock          sync.RWMutex
}

type Target struct {
	Port          int                        `yaml:"-" json:"port"`
	Name          string                     `yaml:"name" json:"name"`
	Enabled       bool                       `yaml:"enabled" json:"enabled"`
	Endpoints     map[string]*TargetEndpoint `yaml:"endpoints" json:"endpoints"`
	Triggers      map[string]*TargetTrigger  `yaml:"triggers" json:"triggers"`
	Transform     *TrafficTransform          `yaml:"transform" json:"transform"`
	TrafficConfig *TrafficConfig             `yaml:"trafficConfig" json:"trafficConfig"`
	CallCount     int                        `yaml:"-" json:"callCount"`
	lock          sync.RWMutex
}

type MatchedTarget struct {
	target         *Target
	trigger        *TargetTrigger
	endpoints      map[string]*EndpointInvocation
	transform      *TrafficTransform
	trafficConfig  *TrafficConfig
	matchedURI     string
	matchedHeaders map[string]string
	captureKeys    map[string]string
}

type TargetTracker struct {
	callCount          int
	writeSinceLastDrop int
	isRunning          bool
	stopChan           chan bool
}

type UpstreamResults map[string]map[string][]*invocation.InvocationResultResponse

type ProxyResponse struct {
	UpResponseRange []int `yaml:"upResponseRange" json:"upResponseRange"`
	ProxyResponse   int   `yaml:"proxyResponse" json:"proxyResponse"`
}

type Proxy struct {
	Port           int                `yaml:"port" json:"port"`
	Targets        map[string]*Target `yaml:"targets" json:"targets"`
	Enabled        bool               `yaml:"enabled" json:"enabled"`
	ProxyResponses []*ProxyResponse   `yaml:"proxyResponses" json:"proxyResponses"`
	HTTPTracker    *HTTPProxyTracker  `yaml:"-" json:"tracker"`
	Router         *mux.Router
	lock           sync.RWMutex
}

type ProxyTargets map[string]*MatchedTarget

type RequestContext struct {
	path     string
	method   string
	vars     map[string]string
	headers  map[string]string
	queries  map[string]string
	body     io.Reader
	r        *http.Request
	respChan chan byte
	w        http.ResponseWriter
	fw       intercept.FlushWriter
	c        chan []byte
	cw       intercept.ChanWriter
	yaml     bool
	clean    bool
}

func newProxy(port int) *Proxy {
	p := &Proxy{
		Port:        port,
		Targets:     map[string]*Target{},
		Enabled:     true,
		HTTPTracker: NewHTTPTracker(),
		Router:      mux.NewRouter().SkipClean(true),
	}
	configureRouter(p.Router)
	return p
}

func newHTTPTarget() *Target {
	return &Target{}
}

func parseTarget(r io.Reader) (*Target, error) {
	target := newHTTPTarget()
	if err := util.ReadJsonOrYamlPayloadFromBody(r, target); err != nil {
		return nil, err
	}
	if target.Name == "" {
		return nil, errors.New("target name missing")
	}
	if target.Endpoints == nil {
		return nil, errors.New("target endpoints missing")
	}
	for name, ep := range target.Endpoints {
		if ep.URL == "" {
			return nil, fmt.Errorf("target endpoint [%s] missing url", name)
		}
	}
	if len(target.Triggers) == 0 {
		return nil, fmt.Errorf("At least one trigger is required")
	}
	for i, t := range target.Triggers {
		if len(t.MatchAny) == 0 {
			return nil, fmt.Errorf("target trigger [%s] must specify matchAny", i)
		}
		if len(t.Endpoints) == 0 {
			return nil, fmt.Errorf("target trigger [%s] must specify one endpoint", i)
		}
	}
	return target, nil
}

func (p *Proxy) enable(enable bool) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Enabled = enable
}

func (p *Proxy) addProxyPath(path string) *mux.Router {
	proxyLock.Lock()
	defer proxyLock.Unlock()
	if proxyRouters[p.Port] == nil {
		proxyRouters[p.Port] = map[string]*mux.Router{}
	}
	proxyRouters[p.Port][path] = p.Router.PathPrefix(path).Subrouter()
	configureRouter(proxyRouters[p.Port][path])
	return proxyRouters[p.Port][path]
}

func (p *Proxy) AddTarget(t *Target) error {
	for _, trigger := range t.Triggers {
		for _, match := range trigger.MatchAny {
			//Registering URI with mux, so that the URI's embedded vars are extracted by mux
			uri := match.URI
			prefix := false
			if uri != "" {
				trigger.exactMatches = append(trigger.exactMatches, match)
				prefix = false
			} else {
				uri = match.URIPrefix
				trigger.prefixMatches = append(trigger.prefixMatches, match)
				prefix = true
			}
			rootURI, suffix := util.GetRootURI(uri)
			if rootURI == "" {
				rootURI = "/"
				suffix = "*"
			} else if match.URIPrefix != "" {
				suffix += "*"
			}
			match.router = p.addProxyPath(rootURI)
			if re, err := util.BuildURIMatcherForRouter(suffix, prefix, ProxyRequest, match.router); err == nil {
				match.uriRegexp = re
			} else {
				return err
			}
		}
	}
	for epName, ep := range t.Endpoints {
		ep.name = epName
		ep.target = t
	}
	for triggerName, trigger := range t.Triggers {
		trigger.name = triggerName
		for _, m := range trigger.MatchAny {
			for _, v := range m.Vars {
				v.Prepare()
			}
		}
		if trigger.Transform != nil {
			trigger.Transform.prepare()
		}
		trigger.epSpecs = map[string]*EndpointInvocation{}
		for _, epName := range trigger.Endpoints {
			ep := t.Endpoints[epName]
			if ep == nil {
				return fmt.Errorf("Target [%s] Trigger [%s] refers to Endpoint [%s] but endpoint not defined under target", t.Name, triggerName, epName)
			}
			tc := trigger.TrafficConfig
			if tc == nil {
				tc = t.TrafficConfig
			}
			if is, err := ep.prepareInvocationSpec(tc); err != nil {
				return err
			} else {
				trigger.epSpecs[epName] = &EndpointInvocation{
					ep:     ep,
					is:     is,
					target: t,
				}
			}
			ep.target = t
		}
	}
	if t.Transform != nil {
		t.Transform.prepare()
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Targets[t.Name] = t
	return nil
}

func (p *Proxy) getTarget(name string) *Target {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.Targets[name]
}

func (p *Proxy) clearTargets() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Targets = map[string]*Target{}
}

func (p *Proxy) removeTarget(target string) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.Targets[target] != nil {
		delete(p.Targets, target)
		return true
	}
	return false
}

func (p *Proxy) enableTarget(target string, enable bool) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.Targets[target] != nil {
		p.Targets[target].Enabled = enable
		return true
	}
	return false
}

func (p *Proxy) hasAnyTargets() bool {
	return len(p.Targets) > 0
}

func (p *Proxy) addRemoveHeader(targetName, triggerName, header, value string, remove bool) bool {
	p.lock.Lock()
	target := p.Targets[targetName]
	p.lock.Unlock()
	if target == nil {
		return false
	}
	return target.addRemoveHeader(triggerName, header, value, remove)
}

func (t *Target) addRemoveHeader(triggerName, header, value string, remove bool) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if triggerName != "" {
		trigger := t.Triggers[triggerName]
		if trigger == nil {
			return false
		}
		return trigger.addRemoveHeader(header, value, remove)
	}
	if t.Transform == nil {
		t.Transform = &TrafficTransform{}
	}
	t.Transform.addRemoveHeader(header, value, remove)
	for _, tr := range t.Triggers {
		if tr.Transform == nil {
			tr.Transform = t.Transform
		} else {
			tr.Transform.addRemoveHeader(header, value, remove)
		}
	}
	return true
}

func (t *TargetTrigger) addRemoveHeader(header, value string, remove bool) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.Transform == nil {
		t.Transform = &TrafficTransform{}
	}
	return t.Transform.addRemoveHeader(header, value, remove)
}

func (t *TrafficTransform) addRemoveHeader(header, value string, remove bool) bool {
	if t.Headers == nil {
		t.Headers = &Keys{}
	}
	if remove {
		t.Headers.Remove = append(t.Headers.Remove, header)
	} else {
		if t.Headers.Add == nil {
			t.Headers.Add = map[string]string{}
		}
		t.Headers.Add[header] = value
	}
	return true
}

func (p *Proxy) clearHeaders(targetName, triggerName, header string) bool {
	p.lock.Lock()
	target := p.Targets[targetName]
	defer p.lock.Unlock()
	if target != nil {
		return target.clearHeaders(triggerName, header)
	}
	return false
}

func (t *Target) clearHeaders(triggerName, header string) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if triggerName != "" {
		trigger := t.Triggers[triggerName]
		if trigger == nil {
			return false
		}
		return trigger.clearHeaders(header)
	}
	if t.Transform != nil {
		t.Transform.clearHeaders(header)
	}
	for _, tr := range t.Triggers {
		if tr.Transform != nil {
			tr.Transform.clearHeaders(header)
		}
	}
	return true
}

func (t *TargetTrigger) clearHeaders(header string) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.Transform != nil {
		return t.Transform.clearHeaders(header)
	}
	return false
}

func (t *TrafficTransform) clearHeaders(header string) bool {
	if t.Headers != nil {
		if header != "" {
			delete(t.Headers.Add, header)
			if idx := slices.Index(t.Headers.Remove, header); idx != -1 {
				t.Headers.Remove = slices.Delete(t.Headers.Remove, idx, idx+1)
			}
		} else {
			t.Headers.Add = map[string]string{}
			t.Headers.Remove = []string{}
		}
	}
	return true
}
