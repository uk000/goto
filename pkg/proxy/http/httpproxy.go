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
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/invocation"
	"goto/pkg/metrics"
	"goto/pkg/server/catchall"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/status"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

var (
	proxyRouters             = map[int]map[string]*mux.Router{}
	portProxy                = map[int]*Proxy{}
	interceptMiddleware      = intercept.IntereceptMiddleware(nil, nil)
	notFoundHandler          = catchall.Middleware.MiddlewareHandler(nil)
	lowerProxyUpstreamStatus = strings.ToLower(constants.HeaderGotoProxyUpstreamStatus)
	lowerGotoUpstreamStatus  = strings.ToLower(constants.HeaderGotoUpstreamStatus)
	proxyLock                sync.RWMutex
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

func configureRouter(r *mux.Router) {
	r.Use(setProxyFlag)
	middleware.UseCore(r)
	r.Use(interceptMiddleware)
	middleware.UseInterceptedCore(r)
	r.NotFoundHandler = notFoundHandler
}

func setProxyFlag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		rs.ProxyRouter = true
		rs.InterceptChunked = !rs.ProxyStreaming
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

func (p *Proxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.HTTPTracker = NewHTTPTracker()
}

func (p *Proxy) incrementMatchCounts(matches map[string]*MatchedTarget, r *http.Request) {
	p.HTTPTracker.IncrementDownstreamRequestCounts(r.RequestURI)
	for _, match := range matches {
		p.HTTPTracker.IncrementTargetDownstreamCounts(match.target.Name, r.RequestURI)
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
	responses := UpstreamResults{}
	responseStatuses := map[string]map[string]int{}
	var proxyResponseStatus int
	go p.asyncCollectResponses(out, wg, &proxyResponseStatus, responseStatuses, responses)
	go p.asyncStreamResponse(rc)
	for _, match := range targetsMatches {
		match.invoke(rc, out, wg, p.HTTPTracker)
		if match.trafficConfig != nil {
			if match.trafficConfig.Transparent {
				rc.sendHeaders = true
				rc.sendPayload = true
			}
			if match.trafficConfig.JsonPayload || match.trafficConfig.YamlPayload {
				rc.sendPayload = true
				rc.parseJson = match.trafficConfig.JsonPayload
				rc.parseYaml = match.trafficConfig.YamlPayload
			}
		}
	}
	wg.Wait()
	close(rc.c)
	p.processResponses(rc, responses, responseStatuses)
	rc.rs.StatusCode = proxyResponseStatus
	rc.w.WriteHeader(proxyResponseStatus)
	p.processPayload(rc, responses)
}

func (p *Proxy) processResponses(rc *RequestContext, responses UpstreamResults, responseStatuses map[string]map[string]int) {
	upstreamStatuses := []string{}
	upstreamViaGoto := []string{}
	upstreamProxyStatuses := []map[string]map[string][]string{}
	peerCertInfos := []string{}
	for target, targetResponses := range responses {
		for ep, epResponses := range targetResponses {
			for _, resp := range epResponses {
				rc.processResponseHeaders(target, ep, resp.Headers, resp.PeerCertInfo, &peerCertInfos, &upstreamStatuses, &upstreamViaGoto, &upstreamProxyStatuses)
			}
		}
	}
	rc.sendProxyStatuses(upstreamViaGoto, upstreamProxyStatuses, responseStatuses, peerCertInfos)
	if rc.sendHeaders {
		for _, m := range responses {
			for _, responses := range m {
				for _, resp := range responses {
					util.CopyHeadersWithIgnore("", resp.Headers, rc.w.Header(), nil, false, false, false)
					break
				}
				break
			}
			break
		}
	} else {
		rc.w.Header()[constants.HeaderGotoUpstreamStatus] = upstreamStatuses
	}
}

func (rc *RequestContext) sendProxyStatuses(upstreamViaGoto []string, upstreamProxyStatuses []map[string]map[string][]string, responseStatuses map[string]map[string]int, peerCertInfos []string) {
	upstreamProxyStatusHeaders := []string{}
	for _, v := range upstreamProxyStatuses {
		upstreamProxyStatusHeaders = append(upstreamProxyStatusHeaders, util.ToJSONText(v))
	}
	if len(responseStatuses) > 0 {
		upstreamProxyStatusHeaders = append(upstreamProxyStatusHeaders, util.ToJSONText(responseStatuses))
	}
	rc.w.Header()[constants.HeaderGotoProxyUpstreamStatus] = upstreamProxyStatusHeaders
	rc.w.Header()[constants.HeaderViaGoto] = append(rc.w.Header()[constants.HeaderViaGoto], upstreamViaGoto...)
	rc.w.Header()[constants.HeaderGotoClientCert] = peerCertInfos
}

func (rc *RequestContext) processResponseHeaders(target, ep string, headers http.Header, pci *gototls.PeerCertInfo, peerCertInfos *[]string, upstreamStatuses *[]string, upstreamViaGoto *[]string, upstreamProxyStatuses *[]map[string]map[string][]string) {
	v := headers[constants.HeaderViaGoto]
	if len(v) == 0 {
		v = headers[util.LowerViaGoto]
	}
	if len(v) > 0 {
		*upstreamViaGoto = append(*upstreamViaGoto, v...)
	}
	v = headers[constants.HeaderGotoProxyUpstreamStatus]
	if len(v) == 0 {
		v = headers[lowerProxyUpstreamStatus]
	}
	if len(v) > 0 {
		*upstreamProxyStatuses = append(*upstreamProxyStatuses, map[string]map[string][]string{target: {ep: v}})
	}
	if pci != nil {
		*peerCertInfos = append(*peerCertInfos, util.ToJSONText(pci))
	}
	v = headers[constants.HeaderGotoUpstreamStatus]
	if len(v) == 0 {
		v = headers[lowerGotoUpstreamStatus]
	}
	if len(v) > 0 {
		*upstreamStatuses = append(*upstreamStatuses, v...)
	}
}

func (p *Proxy) processPayload(rc *RequestContext, responses UpstreamResults) {
	if len(responses) > 0 {
		if rc.sendPayload {
			for _, m := range responses {
				for _, responses := range m {
					for _, resp := range responses {
						if rc.parseJson {
							if json, ok := util.JSONFromJSONText(resp.PayloadText); ok {
								util.WriteJsonOrYAMLPayload(rc.w, json, rc.yaml)
								break
							}
						} else if rc.parseYaml {
							if yaml, err := util.JsonOrYamlFromText(resp.PayloadText); err == nil {
								util.WriteJsonOrYAMLPayload(rc.w, yaml, true)
								break
							}
						}
						util.WriteJsonOrYAMLPayload(rc.w, resp.PayloadText, false)
						break
					}
					break
				}
				break
			}
		} else {
			util.WriteJsonOrYAMLPayload(rc.w, responses, rc.yaml)
		}
	} else {
		fmt.Fprintln(rc.w, "No Response")
	}
}

func (t *MatchedTarget) invoke(rc *RequestContext, out chan *TargetEndpointResponse, wg *sync.WaitGroup, pt *HTTPProxyTracker) {
	for _, ep := range t.endpoints {
		t.target.lock.Lock()
		t.target.CallCount++
		targetCounter := t.target.CallCount
		t.target.lock.Unlock()
		t.trigger.lock.Lock()
		t.trigger.CallCount++
		t.trigger.lock.Unlock()
		ep.ep.lock.Lock()
		ep.ep.CallCount++
		epCounter := ep.ep.CallCount
		ep.ep.lock.Unlock()
		metrics.UpdateProxiedRequestCount(ep.ep.name)
		err := ep.invoke(targetCounter, epCounter, t.target.Name, t.matchedURI, t.transform, rc, out, pt)
		if err != nil {
			log.Println(err.Error())
		}
		wg.Add(ep.ep.RequestCount * ep.ep.Concurrent)
	}
}

func (ep *EndpointInvocation) invoke(targetCounter, epCounter int, target string, matchedURI string, tt *TrafficTransform, rc *RequestContext, out chan *TargetEndpointResponse, pt *HTTPProxyTracker) error {
	is := ep.toInvocationSpec(matchedURI, tt, rc, pt)
	tracker, err := invocation.RegisterInvocation(ep.proxyPort, is)
	if err != nil {
		return err
	}
	tracker.CustomID = fmt.Sprintf("%d.%d", targetCounter, epCounter)
	tracker.OnHeaders = ep.onHeaders(rc)
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
			target:     target,
			endpoint:   ep.ep.name,
			requestURI: resp.Request.URI,
			url:        ep.ep.URL,
			response:   resp.Response,
		}
	}
}

func (ep *EndpointInvocation) onHeaders(rc *RequestContext) func(http.Header, int, *gototls.PeerCertInfo) {
	return func(headers http.Header, status int, peerCertInfo *gototls.PeerCertInfo) {
		upstreamViaGoto := []string{}
		upstreamStatuses := []string{}
		upstreamProxyStatuses := []map[string]map[string][]string{}
		responseStatuses := map[string]map[string]int{ep.target.Name: {ep.ep.name: status}}
		peerCertInfos := []string{}
		rc.processResponseHeaders(ep.target.Name, ep.ep.name, headers, peerCertInfo, &peerCertInfos, &upstreamStatuses, &upstreamViaGoto, &upstreamProxyStatuses)
		rc.sendProxyStatuses(upstreamViaGoto, upstreamProxyStatuses, responseStatuses, peerCertInfos)
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

func (p *Proxy) asyncCollectResponses(out chan *TargetEndpointResponse, wg *sync.WaitGroup, proxyResponseStatus *int, responseStatuses map[string]map[string]int, responses UpstreamResults) {
	*proxyResponseStatus = 0
	for resp := range out {
		if responseStatuses[resp.target] == nil {
			responseStatuses[resp.target] = map[string]int{}
		}
		if responses[resp.target] == nil {
			responses[resp.target] = map[string][]*invocation.InvocationResultResponse{}
		}
		responseStatuses[resp.target][resp.endpoint] = resp.response.StatusCode
		responses[resp.target][resp.endpoint] = append(responses[resp.target][resp.endpoint], resp.response)
		p.HTTPTracker.IncrementTargetUpstreamStatusCounts(resp.target, resp.endpoint, resp.requestURI, resp.response.StatusCode)
		remapped := false
		if p.ProxyResponses != nil {
			for _, pr := range p.ProxyResponses {
				if len(pr.UpResponseRange) == 0 {
					continue
				}
				responseFrom := pr.UpResponseRange[0]
				responseTo := responseFrom
				if len(pr.UpResponseRange) >= 2 {
					responseTo = pr.UpResponseRange[1]
				}
				if resp.response.StatusCode >= responseFrom && resp.response.StatusCode <= responseTo {
					*proxyResponseStatus = pr.ProxyResponse
					remapped = true
					break
				}
			}
		}
		if !remapped {
			*proxyResponseStatus = resp.response.StatusCode
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
	is.TLS = ep.IsTLS
	is.ClientCert = ep.ClientCert
	is.ALPN = ep.ALPN
	is.RequestCount = ep.RequestCount
	is.Replicas = ep.Concurrent
	is.TrackPayload = true
	if tc != nil {
		is.CollectResponse = tc.Payload || tc.JsonPayload || tc.YamlPayload
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
	is.LongRunning = true
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

func (ep *EndpointInvocation) toInvocationSpec(matchedURI string, tt *TrafficTransform, rc *RequestContext, pt *HTTPProxyTracker) *invocation.InvocationSpec {
	is := ep.is.Clone()
	is.URL = ep.prepareURL(matchedURI, tt, rc, pt)
	if is.Method == "" {
		is.Method = rc.method
	}
	var addHeaders, mapHeaders map[string]string
	var removeHeaders []string
	if tt != nil {
		is.RequestId = tt.RequestId
		if tt.Headers != nil {
			addHeaders = tt.Headers.Add
			mapHeaders = tt.Headers.Map
			removeHeaders = tt.Headers.Remove
			is.LowerHeaders = tt.Headers.Lower
		}
		if tt.Payload != "" {
			is.Body = util.GetFilledText(tt.Payload, rc.vars)
			removeHeaders = append(removeHeaders, constants.HeaderContentLength)
		} else {
			is.BodyReader = rc.body
		}
	}
	is.Headers, is.Host = util.TransformHeaders(rc.vars, rc.headers, addHeaders, mapHeaders, removeHeaders)
	if ep.ep.Stream {
		if rc.cw == nil {
			rc.cw = intercept.NewChanWriter(rc.c, rc.w)
		}
		if rc.fw == nil {
			rc.fw = intercept.CreateOrGetFlushWriter(rc.w)
		}
		is.ResponseWriter = rc.cw
	}
	is.SendID = false
	return is
}

func (ep *EndpointInvocation) prepareURL(matchedURI string, tt *TrafficTransform, rc *RequestContext, pt *HTTPProxyTracker) string {
	targetURI := rc.path
	var add map[string]string
	var remove []string
	if tt != nil {
		if tt.stripURIRegexp != nil {
			targetURI = tt.stripURIRegexp.ReplaceAllString(targetURI, "$1$3")
		} else if len(tt.URIMap) > 0 {
			uri := tt.URIMap[rc.r.RequestURI]
			if uri == "" {
				uri = tt.URIMap[matchedURI]
			}
			if uri == "" {
				uri = tt.URIMap[matchedURI+"*"]
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
	pt.IncrementUpstreamRequestCounts(targetURI)
	pt.IncrementTargetUpstreamCounts(ep.ep.target.Name, ep.ep.name, targetURI)
	url := ep.ep.URL
	url += targetURI
	return url
}

func (m *TargetMatch) matchURI(matchedTarget *MatchedTarget, r *http.Request) bool {
	if m.uriRegexp != nil && !m.uriRegexp.MatchString(r.RequestURI) {
		return false
	}
	uri := m.URI
	if uri == "" {
		uri = m.URIPrefix
	}
	keys := util.GetFillersUnmarked(uri)
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
	matchedTarget.matchedURI = uri
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
	matchFunc := func(match *TargetMatch) bool {
		if !match.matchURI(matchedTarget, r) {
			return false
		}
		if !match.matchHeaders(matchedTarget, r) {
			return false
		}
		return true
	}
	for _, match := range t.exactMatches {
		if !matchFunc(match) {
			continue
		}
		matched = true
		break
	}
	if !matched {
		for _, match := range t.prefixMatches {
			if !matchFunc(match) {
				continue
			}
			matched = true
			break
		}
	}
	return matched
}

func (p *Proxy) getMatchingProxyTargets(r *http.Request) (ProxyTargets, bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	matchedTargets := map[string]*MatchedTarget{}
	streaming := false
	matchTargetFunc := func(target *Target, trigger *TargetTrigger) bool {
		if !target.Enabled {
			return false
		}
		if len(target.Triggers) == 0 {
			return false
		}
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
			streaming = streaming || matchedTarget.target.streaming
			return true
		}
		return false
	}
	for _, target := range p.Targets {
		matched := false
		for _, trigger := range target.Triggers {
			if len(trigger.exactMatches) > 0 && matchTargetFunc(target, trigger) {
				matched = true
				break
			}
		}
		if !matched {
			for _, trigger := range target.Triggers {
				if len(trigger.prefixMatches) > 0 && matchTargetFunc(target, trigger) {
					matched = true
					break
				}
			}
		}
	}
	return matchedTargets, streaming
}

func ProxyRequest(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	rs := util.GetRequestStore(r)
	var targets ProxyTargets
	streaming := false
	proxy := GetPortProxy(port)
	if rs.ProxyTargets == nil {
		targets, streaming = proxy.getMatchingProxyTargets(r)
		rs.ProxyStreaming = streaming
	} else {
		targets = rs.ProxyTargets.(ProxyTargets)
	}
	if len(targets) > 0 {
		rs.ProxiedRequest = true
		util.SendGotoHeaders(w, r)
		// w.Header()["Trailer"] = []string{constants.HeaderGotoProxyUpstreamStatus, fmt.Sprintf("Proxy-%s", constants.HeaderGotoInAt), fmt.Sprintf("Proxy-%s", constants.HeaderGotoOutAt), fmt.Sprintf("Proxy-%s", constants.HeaderGotoTook)}
		util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.GetMapKeys(targets)), r)
		proxy.incrementMatchCounts(targets, r)
		rc := &RequestContext{
			path:    r.URL.Path,
			method:  r.Method,
			vars:    mux.Vars(r),
			headers: util.GetHeaderValues(r),
			queries: util.GetQueryParams(r),
			body:    r.Body,
			rs:      rs,
			r:       r,
			w:       w,
			fw:      intercept.NewFlushWriter(w),
			c:       make(chan []byte, 2),
			yaml:    rs.YAMLResponse,
		}
		proxy.invokeTargets(targets, rc)
		util.SendGotoTrailers(w, r)
	}
}

func WillProxyHTTP(r *http.Request) (bool, *mux.Router) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := GetPortProxy(port)
	rs := util.GetRequestStore(r)
	rs.ProxiedRequest = false
	if proxy.Enabled && proxy.hasAnyTargets() && !status.IsForcedStatus(r) {
		matches, streaming := proxy.getMatchingProxyTargets(r)
		rs.ProxiedRequest = len(matches) > 0
		if rs.ProxiedRequest {
			rs.ProxyTargets = matches
			rs.ProxyStreaming = streaming
			rootURI, _ := util.GetRootURI(r.RequestURI)
			router := proxyRouters[port][rootURI]
			if router == nil {
				for _, m := range matches {
					router = proxyRouters[port][m.matchedURI]
					if router != nil {
						break
					}
				}
			}
			if router == nil {
				router = proxyRouters[port]["/"]
			}
			return true, router
		}
	}
	return false, nil
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, router := WillProxyHTTP(r); ok {
			router.ServeHTTP(w, r)
		} else if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
