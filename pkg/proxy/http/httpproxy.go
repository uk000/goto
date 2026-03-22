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
	"goto/pkg/constants"
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/metrics"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/status"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"

	"github.com/gorilla/mux"
)

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
}

var (
	portProxy = map[int]*Proxy{}
	proxyLock sync.RWMutex
)

func GetPortProxy(port int) *Proxy {
	proxyLock.RLock()
	proxy := portProxy[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxyLock.Lock()
		defer proxyLock.Unlock()
		proxy = newProxy(port)
		portProxy[port] = proxy
	}
	return proxy
}

func ClearAllProxies() {
	proxyLock.Lock()
	defer proxyLock.Unlock()
	portProxy = map[int]*Proxy{}
}

func ClearPortProxy(port int) {
	proxyLock.Lock()
	defer proxyLock.Unlock()
	portProxy[port] = newProxy(port)
}

func newProxy(port int) *Proxy {
	p := &Proxy{
		Port:        port,
		Targets:     map[string]*Target{},
		Enabled:     true,
		HTTPTracker: NewHTTPTracker(),
		Router:      mux.NewRouter().SkipClean(true),
	}
	p.Router.Use(setProxyFlag)
	middleware.UseCore(p.Router)
	p.Router.Use(intercept.IntereceptMiddleware(nil, nil))
	middleware.UseInterceptedCore(p.Router)
	return p
}

func setProxyFlag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		rs.ProxyRouter = true
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

func newHTTPTarget() *Target {
	return &Target{}
}

func parseTarget(r io.Reader) (*Target, error) {
	target := newHTTPTarget()
	if err := util.ReadJsonPayloadFromBody(r, target); err != nil {
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

func (p *Proxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.HTTPTracker = NewHTTPTracker()
}

func (p *Proxy) enable(enable bool) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Enabled = enable
}

func (p *Proxy) AddTarget(t *Target) error {
	for _, trigger := range t.Triggers {
		for _, match := range trigger.MatchAny {
			//Registering URI with mux, so that the URI's embedded vars are extracted by mux
			rootURI, suffix := util.GetRootURI(match.URIPrefix)
			if rootURI == "" {
				rootURI = "/"
				suffix = "*"
			}
			match.router = middleware.AddProxyPath(p.Router, p.Port, rootURI)
			if re, err := util.BuildURIMatcherForRouter(suffix, ProxyRequest, match.router); err == nil {
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

func (p *Proxy) incrementMatchCounts(matches map[string]*MatchedTarget, r *http.Request) {
	p.HTTPTracker.IncrementRequestCounts(r.RequestURI)
	for _, match := range matches {
		p.HTTPTracker.IncrementTargetRequestCounts(match.target.Name, r.RequestURI)
		if match.matchedURI != "" {
			p.HTTPTracker.IncrementTargetMatchCounts(match.target.Name, match.matchedURI, "", "", "", "")
		}
		for k, v := range match.matchedHeaders {
			p.HTTPTracker.IncrementTargetMatchCounts(match.target.Name, "", k, v, "", "")
		}
	}
}

func (p *Proxy) invokeTargets(targetsMatches map[string]*MatchedTarget, rc *RequestContext) {
	out := make(chan *TargetEndpointResponse, 10)
	wg := &sync.WaitGroup{}
	responses := map[string]map[string]*invocation.InvocationResultResponse{}
	responseStatuses := map[string]map[string]int{}
	var proxyResponseStatus int
	go p.asyncCollectResponses(out, wg, &proxyResponseStatus, responseStatuses, responses)
	go p.asyncStreamResponse(rc)
	clean := false
	for _, match := range targetsMatches {
		match.invoke(rc, out, wg)
		if match.trafficConfig != nil && match.trafficConfig.Clean {
			clean = true
		}
	}
	wg.Wait()
	close(rc.c)
	rc.w.Header().Add(constants.HeaderGotoProxyUpstreamStatus, util.ToJSONText(responseStatuses))
	rc.w.WriteHeader(proxyResponseStatus)
	if len(responses) > 0 {
		if clean {
			for _, m := range responses {
				for _, resp := range m {
					util.WriteJsonOrYAMLPayload(rc.w, resp.PayloadText, false)
					break
				}
				break
			}
		} else {
			util.WriteJsonOrYAMLPayload(rc.w, responses, true)
		}
	} else {
		fmt.Fprintln(rc.w, "No Response")
	}

}

func (t *MatchedTarget) invoke(rc *RequestContext, out chan *TargetEndpointResponse, wg *sync.WaitGroup) {
	for _, ep := range t.endpoints {
		t.target.lock.Lock()
		t.target.callCount++
		targetCounter := t.target.callCount
		t.target.lock.Unlock()
		t.trigger.lock.Lock()
		t.trigger.callCount++
		t.trigger.lock.Unlock()
		ep.ep.lock.Lock()
		ep.ep.callCount++
		epCounter := ep.ep.callCount
		ep.ep.lock.Unlock()
		metrics.UpdateProxiedRequestCount(ep.ep.name)
		err := ep.invoke(targetCounter, epCounter, t.target.Name, t.matchedURI, t.transform, rc, out)
		if err != nil {
			log.Println(err.Error())
		}
		wg.Add(1)
	}
}

func (ep *EndpointInvocation) invoke(targetCounter, epCounter int, target string, matchedURI string, tt *TrafficTransform, rc *RequestContext, out chan *TargetEndpointResponse) error {
	is := ep.toInvocationSpec(matchedURI, tt, rc)
	tracker, err := invocation.RegisterInvocation(is)
	if err != nil {
		return err
	}
	tracker.CustomID = fmt.Sprintf("%d.%d", targetCounter, epCounter)
	go ep.asyncInvoke(target, tracker, out)
	return nil
}

func (ep *EndpointInvocation) asyncInvoke(target string, tracker *invocation.InvocationTracker, out chan *TargetEndpointResponse) {
	responses := invocation.StartInvocation(tracker, true)
	for _, resp := range responses {
		if !util.IsBinaryContentHeader(resp.Response.Headers) {
			resp.Response.PayloadText = string(resp.Response.Payload)
		}
		out <- &TargetEndpointResponse{
			target:   target,
			endpoint: ep.ep.URL,
			response: resp.Response,
		}
	}
}

func (p *Proxy) asyncStreamResponse(rc *RequestContext) {
	for data := range rc.c {
		if len(data) == 0 {
			return
		}
		rc.fw.Write(data)
	}
}

func (p *Proxy) asyncCollectResponses(out chan *TargetEndpointResponse, wg *sync.WaitGroup, proxyResponseStatus *int, responseStatuses map[string]map[string]int, responses map[string]map[string]*invocation.InvocationResultResponse) {
	*proxyResponseStatus = http.StatusOK
	for resp := range out {
		if responseStatuses[resp.target] == nil {
			responseStatuses[resp.target] = map[string]int{}
		}
		if responses[resp.target] == nil {
			responses[resp.target] = map[string]*invocation.InvocationResultResponse{}
		}
		responseStatuses[resp.target][resp.endpoint] = resp.response.StatusCode
		responses[resp.target][resp.endpoint] = resp.response
		if p.ProxyResponses != nil {
			for _, pr := range p.ProxyResponses {
				if len(pr.UpResponseRange) < 2 {
					continue
				}
				if resp.response.StatusCode >= pr.UpResponseRange[0] && resp.response.StatusCode <= pr.UpResponseRange[1] {
					*proxyResponseStatus = pr.ProxyResponse
					break
				}
			}
		}
		wg.Done()
	}
}

func (ep *TargetEndpoint) prepareInvocationSpec(tc *TrafficConfig) (*invocation.InvocationSpec, error) {
	is := &invocation.InvocationSpec{}
	is.Name = ep.name
	is.Method = ep.Method
	is.Protocol = ep.Protocol
	is.URL = ep.URL
	is.Host = ep.Authority
	is.RequestCount = ep.RequestCount
	is.Replicas = ep.Concurrent
	is.TrackPayload = true
	if tc != nil {
		is.CollectResponse = tc.Payload
		is.Retries = tc.Retries
		if tc.Delay != nil {
			is.Delay = tc.Delay.Compute().String()
		} else {
			is.Delay = "0s"
		}
		if tc.RetryDelay != nil {
			is.RetryDelay = tc.RetryDelay.Compute().String()
		}
		is.RetriableStatusCodes = tc.RetryOn
	}
	if err := invocation.ValidateSpec(is); err != nil {
		return nil, err
	}
	return is, nil
}

func (tt *TrafficTransform) prepare() {
	if tt.StripURI != "" {
		tt.stripURIRegexp = regexp.MustCompile("^(.*)(" + tt.StripURI + ")(/.+).*$")
	}
}

func (ep *EndpointInvocation) toInvocationSpec(matchedURI string, tt *TrafficTransform, rc *RequestContext) *invocation.InvocationSpec {
	is := *ep.is
	is.URL = ep.prepareURL(matchedURI, tt, rc)
	is.Method = rc.method
	var add map[string]string
	var remove []string
	if tt != nil && tt.Headers != nil {
		add = tt.Headers.Add
		remove = tt.Headers.Remove
		is.RequestId = tt.RequestId
	}
	is.Headers, is.Host = util.TransformHeaders(rc.vars, rc.headers, add, remove)
	is.BodyReader = rc.body
	if ep.ep.Stream {
		if rc.cw == nil {
			rc.cw = intercept.NewChanWriter(rc.c)
		}
		if rc.fw == nil {
			rc.fw = intercept.CreateOrGetFlushWriter(rc.w)
		}
		is.ResponseWriter = rc.cw
	}
	is.SendID = false
	return &is
}

func (ep *EndpointInvocation) prepareURL(matchedURI string, tt *TrafficTransform, rc *RequestContext) string {
	targetURI := rc.path
	var add map[string]string
	var remove []string
	if tt != nil {
		if tt.stripURIRegexp != nil {
			targetURI = tt.stripURIRegexp.ReplaceAllString(targetURI, "$1$3")
		} else if len(tt.URIMap) > 0 {
			uri := tt.URIMap[matchedURI]
			if uri == "" {
				uri = tt.URIMap[matchedURI+"/*"]
			}
			if uri != "" {
				targetURI = uri
			}
		}
		if tt.Queries != nil {
			add = tt.Queries.Add
			remove = tt.Queries.Remove
		}
	}
	targetURI = util.TransposeURI(rc.path, matchedURI, targetURI, rc.vars, rc.headers, rc.queries, add, remove)
	url := ep.ep.URL
	url += targetURI
	return url
}

func (m *TargetMatch) matchURI(matchedTarget *MatchedTarget, r *http.Request) bool {
	if m.uriRegexp != nil && !m.uriRegexp.MatchString(r.RequestURI) {
		return false
	}
	keys := util.GetFillersUnmarked(m.URIPrefix)
	uriVarsMatched := true
	for _, key := range keys {
		if varMatch := m.Vars[key]; varMatch != nil {
			keyVal := util.GetStringParamValue(r, key)
			if keyVal == "" || !varMatch.Match(keyVal) {
				uriVarsMatched = false
				break
			}
		}
		matchedTarget.captureKeys[key] = ""
	}
	if !uriVarsMatched {
		return false
	}
	matchedTarget.matchedURI = m.URIPrefix
	return true
}

func (m *TargetMatch) matchHeaders(matchedTarget *MatchedTarget, r *http.Request) bool {
	headerValuesMap := util.GetHeaderValues(r)
	for h, v := range m.Headers {
		hv, present := headerValuesMap[h]
		if !present {
			return false
		}
		key, filler := util.GetFillerUnmarked(v)
		if !filler {
			if v != "" && hv != v {
				return false
			}
		} else {
			if varMatch := m.Vars[key]; varMatch != nil {
				if !varMatch.Match(hv) {
					return false
				}
			}
			matchedTarget.captureKeys[key] = ""
		}
	}
	matchedTarget.matchedHeaders = m.Headers
	for _, hv := range m.Headers {
		if k, present := util.GetFillerUnmarked(hv); present {
			matchedTarget.captureKeys[k] = ""
		}
	}
	return true
}

func (t *TargetTrigger) match(matchedTarget *MatchedTarget, r *http.Request) bool {
	if len(t.Endpoints) == 0 {
		return false
	}
	matched := false
	for _, match := range t.MatchAny {
		if !match.matchURI(matchedTarget, r) {
			continue
		}
		if !match.matchHeaders(matchedTarget, r) {
			continue
		}
		matched = true
		break
	}
	return matched
}

func (p *Proxy) getMatchingProxyTargets(r *http.Request) ProxyTargets {
	p.lock.RLock()
	defer p.lock.RUnlock()
	matchedTargets := map[string]*MatchedTarget{}
	for _, target := range p.Targets {
		if !target.Enabled {
			continue
		}
		if len(target.Triggers) == 0 {
			continue
		}
		for _, trigger := range target.Triggers {
			matchedTarget := &MatchedTarget{
				target:      target,
				trigger:     trigger,
				captureKeys: map[string]string{},
			}
			if trigger.match(matchedTarget, r) {
				matchedTarget.endpoints = trigger.epSpecs
				if trigger.Transform != nil {
					matchedTarget.transform = trigger.Transform
				} else {
					matchedTarget.transform = target.Transform
				}
				if trigger.TrafficConfig != nil {
					matchedTarget.trafficConfig = trigger.TrafficConfig
				} else {
					matchedTarget.trafficConfig = target.TrafficConfig
				}
				matchedTargets[target.Name] = matchedTarget
				continue
			}
		}
	}
	return matchedTargets
}

func ProxyRequest(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	rs := util.GetRequestStore(r)
	var targets ProxyTargets
	proxy := GetPortProxy(port)
	if rs.ProxyTargets == nil {
		targets = proxy.getMatchingProxyTargets(r)
	} else {
		targets = rs.ProxyTargets.(ProxyTargets)
	}
	if len(targets) > 0 {
		rs.ProxiedRequest = true
		util.AddHeaderWithPrefix("Proxy-", HeaderGotoHost, global.Self.HostLabel, w.Header())
		util.AddHeaderWithPrefix("Proxy-", HeaderGotoPort, port, w.Header())
		util.AddHeaderWithPrefix("Proxy-", HeaderGotoProtocol, rs.GotoProtocol, w.Header())
		util.AddHeaderWithPrefix("Proxy-", HeaderViaGoto, rs.ListenerLabel, w.Header())
		w.Header().Set("Trailer", constants.HeaderGotoProxyUpstreamStatus)

		util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.GetMapKeys(targets)), r)
		proxy.incrementMatchCounts(targets, r)
		rc := &RequestContext{
			path:    r.URL.Path,
			method:  r.Method,
			vars:    mux.Vars(r),
			headers: util.GetHeaderValues(r),
			queries: util.GetQueryParams(r),
			body:    r.Body,
			r:       r,
			w:       w,
			fw:      intercept.NewFlushWriter(w),
			c:       make(chan []byte, 2),
		}
		proxy.invokeTargets(targets, rc)
	}
}

func WillProxyHTTP(w http.ResponseWriter, r *http.Request) bool {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := GetPortProxy(port)
	rs := util.GetRequestStore(r)
	rs.ProxiedRequest = false
	if proxy.Enabled && proxy.hasAnyTargets() && !status.IsForcedStatus(r) {
		matches := proxy.getMatchingProxyTargets(r)
		rs.ProxiedRequest = len(matches) > 0
		if rs.ProxiedRequest {
			rs.ProxyTargets = matches
		}
	}
	return rs.ProxiedRequest
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if WillProxyHTTP(w, r) {
			util.MatchRouter.ServeHTTP(w, r)
		} else if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
