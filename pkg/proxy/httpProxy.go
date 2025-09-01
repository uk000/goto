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
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/invocation"
	"goto/pkg/metrics"
	"goto/pkg/proxy/trackers"
	"goto/pkg/server/response/status"
	"goto/pkg/server/response/trigger"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"github.com/gorilla/reverse"
)

type HTTPProxy struct {
	Proxy
	HTTPTracker *trackers.HTTPProxyTracker `json:"tracker"`
	router      *mux.Router
}

type HTTPTarget struct {
	*ProxyTarget
	Routes         map[string]string `json:"routes"`
	SendID         bool              `json:"sendID"`
	AddHeaders     [][]string        `json:"addHeaders"`
	RemoveHeaders  []string          `json:"removeHeaders"`
	AddQuery       [][]string        `json:"addQuery"`
	RemoveQuery    []string          `json:"removeQuery"`
	StripURI       string            `json:"stripURI"`
	Host           string            `json:"host"`
	matchRootURI   bool
	stripURIRegExp *regexp.Regexp
	captureHeaders map[string]string
	captureQuery   map[string]string
	uriRouters     map[string]*mux.Router
}

var (
	httpProxyByPort = map[int]*HTTPProxy{}
	proxyLock       sync.RWMutex
)

func init() {
	util.WillProxyHTTP = WillProxyHTTP
}

func newHTTPProxy(port int) *HTTPProxy {
	p := &HTTPProxy{Proxy: *newProxy(port)}
	p.initTracker()
	p.router = rootRouter.NewRoute().Subrouter()
	return p
}

func getHTTPProxyForRequestPort(r *http.Request) *HTTPProxy {
	return getHTTPProxyForPort(util.GetRequestOrListenerPortNum(r))
}

func getHTTPProxyForPort(port int) *HTTPProxy {
	proxyLock.RLock()
	proxy := httpProxyByPort[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxyLock.Lock()
		defer proxyLock.Unlock()
		proxy = newHTTPProxy(port)
		httpProxyByPort[port] = proxy
	}
	return proxy
}

func newHTTPTarget(name, endpoint string) *HTTPTarget {
	return &HTTPTarget{
		ProxyTarget:    newProxyTarget(name, "HTTP/1.1", endpoint),
		Routes:         map[string]string{},
		AddHeaders:     [][]string{},
		RemoveHeaders:  []string{},
		AddQuery:       [][]string{},
		RemoveQuery:    []string{},
		captureHeaders: map[string]string{},
		captureQuery:   map[string]string{},
		uriRouters:     map[string]*mux.Router{},
	}
}

func (t *HTTPTarget) GetProxyTarget() *ProxyTarget {
	if t.parent != nil {
		return t.parent.GetProxyTarget()
	}
	return t.ProxyTarget
}

func (t *HTTPTarget) GetHTTPTarget() *HTTPTarget {
	if t.parent != nil {
		return t.parent.GetHTTPTarget()
	}
	return t
}

func (p *HTTPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.HTTPTracker = trackers.NewHTTPTracker()
}

func (p *HTTPProxy) setupHTTPTarget(t Target, proto, sni, uriFrom, uriTo string, headers [][]string) {
	target := t.GetHTTPTarget()
	if target == nil {
		return
	}
	if proto == "" {
		proto = "HTTP/1.1"
	}
	target.AddHeaders = headers
	target.Protocol = proto
	if sni != "" {
		snis := strings.Split(sni, ",")
		snisRegexp := "(" + strings.Join(snis, "|") + ")"
		target.MatchAny = &ProxyTargetMatch{
			SNI:       snis,
			sniRegexp: regexp.MustCompile(snisRegexp),
		}
	}
	p.deleteProxyTarget(target.Name)
	p.lock.Lock()
	p.Targets[target.Name] = t
	p.lock.Unlock()
	if uriFrom != "" {
		if uriTo == "" {
			uriTo = uriFrom
		}
		target.lock.Lock()
		target.Routes[uriFrom] = uriTo
		target.lock.Unlock()
	}
	p.addURIMatch(target, uriFrom)
}

func (p *HTTPProxy) addNewHTTPTarget(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "target")
	url := util.GetStringParamValue(r, "url")
	proto := util.GetStringParamValue(r, "proto")
	sni := util.GetStringParamValue(r, "sni")
	uriFrom := util.GetStringParamValue(r, "from")
	uriTo := util.GetStringParamValue(r, "to")
	target := newHTTPTarget(name, url)
	p.setupHTTPTarget(target, proto, sni, uriFrom, uriTo, nil)
	msg = fmt.Sprintf("Port [%d]: Added HTTP proxy target [%s] with upstream URL [%s] Protocol [%s]", p.Port, target.Name, target.Endpoint, target.Protocol)
	if sni != "" {
		msg += fmt.Sprintf(" SNI [%s]", sni)
	}
	if uriFrom != "" {
		msg += fmt.Sprintf(" With Route[from=%s, to=%s]", uriFrom, uriTo)
	}
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
	events.SendRequestEventJSON("Proxy Target Added", target.Name, target, r)
}

func (p *HTTPProxy) addTargetRoute(w http.ResponseWriter, r *http.Request) {
	t := p.checkAndGetTarget(w, r)
	if t == nil {
		return
	}
	target := t.GetHTTPTarget()
	from := util.GetStringParamValue(r, "from")
	to := util.GetStringParamValue(r, "to")
	if strings.Compare(from, "/") == 0 {
		from = "/*"
	}
	target.lock.Lock()
	target.Routes[from] = to
	target.lock.Unlock()
	p.addURIMatch(target, from)
	msg := fmt.Sprintf("Port [%d]: Added URI routing for Target [%s], URL [%s], From [%s] To [%s]", p.Port, target.Name, target.Endpoint, from, to)
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func (p *HTTPProxy) addHeaderOrQueryMatch(w http.ResponseWriter, r *http.Request, isHeader bool) {
	t := p.checkAndGetTarget(w, r)
	if t == nil {
		return
	}
	target := t.GetHTTPTarget()
	key := util.LowerAndTrim(util.GetStringParamValue(r, "key"))
	value := util.LowerAndTrim(util.GetStringParamValue(r, "value"))
	msg := ""
	target.lock.Lock()
	if target.MatchAny == nil {
		target.MatchAny = &ProxyTargetMatch{}
	}
	if isHeader {
		target.MatchAny.Headers = append(target.MatchAny.Headers, []string{key, value})
		p.addHeaderCaptures(target, key, value)
		msg = fmt.Sprintf("Port [%d]: Added header match criteria for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.Endpoint, key, value)
	} else {
		target.MatchAny.Query = append(target.MatchAny.Query, []string{key, value})
		p.addQueryCaptures(target, key, value)
		msg = fmt.Sprintf("Port [%d]: Added query match criteria for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.Endpoint, key, value)
	}
	target.lock.Unlock()
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func (p *HTTPProxy) addTargetHeaderOrQuery(w http.ResponseWriter, r *http.Request, isHeader bool) {
	t := p.checkAndGetTarget(w, r)
	if t == nil {
		return
	}
	target := t.GetHTTPTarget()
	key := util.GetStringParamValue(r, "key")
	value := util.GetStringParamValue(r, "value")
	msg := ""
	target.lock.Lock()
	if isHeader {
		target.AddHeaders = append(target.AddHeaders, []string{key, value})
		msg = fmt.Sprintf("Port [%d]: Recorded header to add for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.Endpoint, key, value)
	} else {
		target.AddQuery = append(target.AddQuery, []string{key, value})
		msg = fmt.Sprintf("Port [%d]: Recorded query to add for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.Endpoint, key, value)
	}
	target.lock.Unlock()
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func (p *HTTPProxy) removeTargetHeaderOrQuery(w http.ResponseWriter, r *http.Request, isHeader bool) {
	t := p.checkAndGetTarget(w, r)
	if t == nil {
		return
	}
	target := t.GetHTTPTarget()
	key := util.GetStringParamValue(r, "key")
	msg := ""
	target.lock.Lock()
	if isHeader {
		target.RemoveHeaders = append(target.RemoveHeaders, key)
		msg = fmt.Sprintf("Port [%d]: Recorded header to remove for Target [%s], URL [%s], Key [%s]", p.Port, target.Name, target.Endpoint, key)
	} else {
		target.RemoveQuery = append(target.RemoveQuery, key)
		msg = fmt.Sprintf("Port [%d]: Recorded query to remove for Target [%s], URL [%s], Key [%s]", p.Port, target.Name, target.Endpoint, key)
	}
	target.lock.Unlock()
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func (p *HTTPProxy) addProxyTarget(w http.ResponseWriter, r *http.Request) {
	target := newHTTPTarget("", "")
	payload := util.Read(r.Body)
	if err := util.ReadJson(payload, target); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
		events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": payload}, r)
		return
	}
	if target.MatchAll != nil && target.MatchAny != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := "Only one of matchAll and matchAny should be specified"
		fmt.Fprintln(w, msg)
		events.SendRequestEventJSON("Proxy Target Rejected", msg,
			map[string]interface{}{"error": msg, "payload": payload}, r)
		return
	}
	if target.Protocol == "" {
		target.Protocol = "HTTP/1.1"
	}
	if _, err := p.toInvocationSpec(target, "/", nil); err == nil {
		p.deleteProxyTarget(target.Name)
		p.lock.Lock()
		defer p.lock.Unlock()
		if target.StripURI != "" {
			target.stripURIRegExp = regexp.MustCompile("^(.*)(" + target.StripURI + ")(/.+).*$")
		}
		p.Targets[target.Name] = target
		p.addHeadersAndQueriesMatch(target)
		if err := p.addRoutes(target); err == nil {
			util.AddLogMessage(fmt.Sprintf("Added proxy target: %+v", target), r)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Port [%d]: Added proxy target: %s\n", p.Port, util.ToJSONText(target))
			events.SendRequestEventJSON("Proxy Target Added", target.Name, target, r)
		} else {
			w.WriteHeader(http.StatusBadRequest)
			events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
				map[string]interface{}{"error": err.Error(), "payload": payload}, r)
			fmt.Fprintf(w, "Failed to add URI Match with error: %s\n", err.Error())
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": payload}, r)
		fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
	}
}

func (p *HTTPProxy) addHeadersAndQueriesMatch(target *HTTPTarget) {
	headerMatches := [][]string{}
	queryMatches := [][]string{}
	if target.MatchAny != nil {
		headerMatches = append(headerMatches, target.MatchAny.Headers...)
		queryMatches = append(queryMatches, target.MatchAny.Query...)
	}
	if target.MatchAll != nil {
		headerMatches = append(headerMatches, target.MatchAll.Headers...)
		queryMatches = append(queryMatches, target.MatchAll.Query...)
	}
	extractKV := func(kv []string) (k string, v string) {
		if len(kv) > 0 {
			k = util.LowerAndTrim(kv[0])
		}
		if len(kv) > 1 {
			v = util.LowerAndTrim(kv[1])
		}
		return
	}
	for _, m := range headerMatches {
		key, value := extractKV(m)
		p.addHeaderCaptures(target, key, value)
	}
	for _, m := range queryMatches {
		key, value := extractKV(m)
		p.addQueryCaptures(target, key, value)
	}
}

func (p *HTTPProxy) addHeaderCaptures(target *HTTPTarget, header, value string) {
	if value != "" {
		if captureKey, found := util.GetFillerUnmarked(value); found {
			if target.captureHeaders == nil {
				target.captureHeaders = map[string]string{}
			}
			target.captureHeaders[header] = captureKey
			value = ""
		}
	}
}

func (p *HTTPProxy) addQueryCaptures(target *HTTPTarget, key, value string) {
	if value != "" {
		if filler, found := util.GetFillerUnmarked(value); found {
			if target.captureQuery == nil {
				target.captureQuery = map[string]string{}
			}
			target.captureQuery[key] = filler
			value = ""
		}
	}
}

func (p *HTTPProxy) addRoutes(target *HTTPTarget) error {
	for uri := range target.Routes {
		if strings.Compare(uri, "/") == 0 {
			to := target.Routes[uri]
			uri = "/*"
			delete(target.Routes, "/")
			target.Routes[uri] = to
		}
		if err := p.addURIMatch(target, uri); err != nil {
			return err
		}
	}
	return nil
}

func (p *HTTPProxy) addURIMatch(target *HTTPTarget, uri string) error {
	uri = strings.ToLower(uri)
	if uri == "" || uri == "/" {
		uri = "/*"
		target.matchRootURI = true
	}
	if _, re, router, err := util.BuildURIMatcher(uri, ProxyRequest); err == nil {
		target.uriRegexps[uri] = re
		target.uriRouters[uri] = router
	} else {
		log.Printf("Proxy: Failed to add URI match %s with error: %s\n", uri, err.Error())
		return err
	}
	return nil
}

func (p *HTTPProxy) prepareTargetHeaders(target *HTTPTarget, r *http.Request) [][]string {
	host := ""
	var headers [][]string = [][]string{}
	for k, values := range r.Header {
		for _, v := range values {
			if strings.EqualFold(k, "Host") {
				host = v
			}
			headers = append(headers, []string{k, v})
		}
	}
	for _, h := range target.AddHeaders {
		header := strings.Trim(h[0], " ")
		headerValue := ""
		if len(h) > 1 {
			headerValue = strings.Trim(h[1], " ")
		}
		if captureKey, found := util.GetFillerUnmarked(headerValue); found {
			for requestHeader, requestCaptureKey := range target.captureHeaders {
				if strings.EqualFold(captureKey, requestCaptureKey) &&
					r.Header.Get(requestHeader) != "" {
					headerValue = r.Header.Get(requestHeader)
				}
			}
		}
		if strings.EqualFold(header, "Host") {
			host = headerValue
		}
		headers = append(headers, []string{header, headerValue})
	}
	for _, header := range target.RemoveHeaders {
		header := strings.Trim(header, " ")
		for i, h := range headers {
			if strings.EqualFold(h[0], header) {
				headers = append(headers[:i], headers[i+1:]...)
			}
		}
	}
	if host != "" {
		target.Host = host
	}
	return headers
}

func (p *HTTPProxy) prepareTargetURL(target *HTTPTarget, uri string, r *http.Request) string {
	url := target.Endpoint
	path := r.URL.Path
	targetURI := path
	if len(target.Routes) > 0 && target.Routes[uri] != "" {
		targetURI = target.Routes[uri]
	}
	if targetURI != "" {
		if uri == "/" {
			uri = "/*"
		}
		forwardRoute := target.uriRouters[uri].NewRoute().BuildOnly().Path(targetURI)
		vars := mux.Vars(r)
		targetVars := []string{}
		if rep, err := reverse.NewGorillaPath(targetURI, false); err == nil {
			for _, k := range rep.Groups() {
				targetVars = append(targetVars, k, vars[k])
			}
			if netURL, err := forwardRoute.URLPath(targetVars...); err == nil {
				path = netURL.Path
			} else {
				log.Printf("Proxy: Failed to set vars on target URI %s with error: %s. Using target URI as is.", targetURI, err.Error())
				path = targetURI
			}
		} else {
			log.Printf("Proxy: Failed to parse path vars from target URI %s with error: %s. Using target URI as is.", targetURI, err.Error())
			path = targetURI
		}
	} else if len(target.StripURI) > 0 {
		path = target.stripURIRegExp.ReplaceAllString(path, "$1$3")
	}
	url += path
	url = p.prepareTargetQuery(url, target, r)
	return url
}

func (p *HTTPProxy) prepareTargetQuery(url string, target *HTTPTarget, r *http.Request) string {
	var params [][]string = [][]string{}
	for k, values := range r.URL.Query() {
		for _, v := range values {
			params = append(params, []string{k, v})
		}
	}
	for _, q := range target.AddQuery {
		addKey := strings.Trim(q[0], " ")
		addValue := ""
		if len(q) > 1 {
			addValue = strings.Trim(q[1], " ")
		}
		if captureKey, found := util.GetFillerUnmarked(addValue); found {
			for reqKey, requestCaptureKey := range target.captureQuery {
				if strings.EqualFold(captureKey, requestCaptureKey) && r.URL.Query().Get(reqKey) != "" {
					addValue = r.URL.Query().Get(reqKey)
				}
			}
		}
		params = append(params, []string{addKey, addValue})
	}
	for _, k := range target.RemoveQuery {
		key := strings.Trim(k, " ")
		for i, q := range params {
			if strings.EqualFold(q[0], key) {
				params = append(params[:i], params[i+1:]...)
			}
		}
	}
	if len(params) > 0 {
		url += "?"
		for _, q := range params {
			url += q[0] + "=" + q[1] + "&"
		}
		url = strings.TrimRight(url, "&")
	}
	return url
}

func (p *HTTPProxy) toInvocationSpec(target *HTTPTarget, uri string, r *http.Request) (*invocation.InvocationSpec, error) {
	is := &invocation.InvocationSpec{}
	is.Name = target.Name
	is.Method = "GET"
	is.Protocol = target.Protocol
	is.URL = target.Endpoint
	is.Replicas = target.Replicas
	is.SendID = target.SendID
	if r != nil {
		is.URL = p.prepareTargetURL(target, uri, r)
		is.Headers = p.prepareTargetHeaders(target, r)
		is.Host = target.Host
		is.Method = r.Method
		is.BodyReader = r.Body
	}
	is.CollectResponse = true
	is.TrackPayload = true
	return is, invocation.ValidateSpec(is)
}

func (p *HTTPProxy) invokeTargets(targetsMatches map[string]*TargetMatchInfo, w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPort(r)
	label := util.GetCurrentListenerLabel(r)
	if len(targetsMatches) > 0 {
		responses := []*invocation.InvocationResultResponse{}
		maxTargetDelay := 0 * time.Second
		var maxDelayTarget *ProxyTarget
		for _, m := range targetsMatches {
			dropTarget := p.shouldDrop(m.target)
			if dropTarget && util.Random(5) < 3 {
				p.HTTPTracker.IncrementTargetDropCount(m.target.Name, r.RequestURI, true)
				util.AddHeaderWithSuffix(constants.HeaderProxyRequestDropped, "|"+m.target.Name, "true", w.Header())
				log.Printf("HTTP Proxy[%d]: Request dropped for target [%s] endpoint [%s]\n", p.Port, m.target.Name, m.target.Endpoint)
				continue
			}
			if m.target.DelayMax > 0 && m.target.DelayMax > maxTargetDelay {
				maxTargetDelay = m.target.DelayMax
				maxDelayTarget = m.target
			}
			p.applyDelay(m.target, m.target.Endpoint, w)
			metrics.UpdateProxiedRequestCount(m.target.Name)
			var t Target = m.target
			is, _ := p.toInvocationSpec(t.GetHTTPTarget(), m.URI, r)
			if tracker, err := invocation.RegisterInvocation(is); err == nil {
				m.target.lock.Lock()
				m.target.callCount++
				tracker.CustomID = m.target.callCount
				m.target.lock.Unlock()
				invocationResponses := invocation.StartInvocation(tracker, true)
				events.SendRequestEventJSON("Proxy Target Invoked", m.target.Name, m.target, r)
				if dropTarget {
					p.HTTPTracker.IncrementTargetDropCount(m.target.Name, r.RequestURI, false)
					util.AddHeaderWithSuffix(constants.HeaderProxyResponseDropped, "|"+m.target.Name, "true", w.Header())
					log.Printf("HTTP Proxy[%d]: Response dropped for target [%s] endpoint [%s]\n", p.Port, m.target.Name, m.target.Endpoint)
				} else {
					if !util.IsBinaryContentHeader(invocationResponses[0].Response.Headers) {
						invocationResponses[0].Response.PayloadText = string(invocationResponses[0].Response.Payload)
					}
					responses = append(responses, invocationResponses[0].Response)
				}
				util.AddHeaderWithSuffix(constants.HeaderGotoProxyUpstreamStatus, "_"+m.target.Name,
					invocationResponses[0].Response.Status, w.Header())
				util.AddHeaderWithSuffix(constants.HeaderGotoProxyUpstreamTook, "_"+m.target.Name,
					invocationResponses[0].TookNanos.String(), w.Header())
			} else {
				log.Println(err.Error())
			}
		}
		for _, response := range responses {
			util.CopyHeaders("", r, w, response.Headers, false, false, false)
			w.Header().Add(constants.HeaderViaGoto, label)
			w.Header().Add(constants.HeaderGotoPort, port)
			if response.StatusCode == 0 {
				response.StatusCode = 503
			}
			status.IncrementStatusCount(response.StatusCode, r)
			trigger.RunTriggers(r, w, response.StatusCode)
		}
		if maxDelayTarget != nil {
			p.applyDelay(maxDelayTarget, r.RemoteAddr, w)
		}
		if len(responses) == 1 {
			if util.IsBinaryContentHeader(responses[0].Headers) {
				fmt.Fprintln(w, responses[0].Payload)
			} else {
				if hv := responses[0].Headers[constants.HeaderContentTypeLower]; len(hv) > 0 {
					w.Header().Add(constants.HeaderContentType, hv[0])
				}
				fmt.Fprintln(w, responses[0].PayloadText)
			}
			w.WriteHeader(responses[0].StatusCode)
		} else {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, util.ToJSONText(responses))
		}
	}
}

func (p *HTTPProxy) getMatchingTargetsForRequest(r *http.Request) map[string]*TargetMatchInfo {
	rs := util.GetRequestStore(r)
	if rs.ProxyTargets != nil {
		return rs.ProxyTargets.(map[string]*TargetMatchInfo)
	}
	targets := p.checkMatchingTargetsForRequest(r)
	rs.ProxyTargets = targets
	return targets
}

func ProxyRequest(w http.ResponseWriter, r *http.Request) {
	p := getHTTPProxyForRequestPort(r)
	targets := p.getMatchingTargetsForRequest(r)
	if len(targets) > 0 {
		util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.GetMapKeys(targets)), r)
		p.incrementMatchCounts(targets, r)
		p.invokeTargets(targets, w, r)
	}
}

func (p *HTTPProxy) incrementMatchCounts(matches map[string]*TargetMatchInfo, r *http.Request) {
	p.HTTPTracker.IncrementRequestCounts(r.RequestURI)
	for _, m := range matches {
		p.HTTPTracker.IncrementTargetRequestCounts(m.target.Name, r.RequestURI)
		if m.URI != "" {
			p.HTTPTracker.IncrementTargetMatchCounts(m.target.Name, m.URI, "", "", "", "")
		}
		for _, hv := range m.Headers {
			p.HTTPTracker.IncrementTargetMatchCounts(m.target.Name, "", hv[0], hv[1], "", "")
		}
		for _, qv := range m.Query {
			p.HTTPTracker.IncrementTargetMatchCounts(m.target.Name, "", "", "", qv[0], qv[1])
		}
	}
}

func (p *Proxy) checkMatchingTargetsForRequest(r *http.Request) map[string]*TargetMatchInfo {
	p.lock.RLock()
	defer p.lock.RUnlock()
	matchInfo := map[string]*TargetMatchInfo{}
	for name, t := range p.Targets {
		target := t.GetHTTPTarget()
		if target.Enabled {
			if target.matchRootURI {
				matchInfo[name] = &TargetMatchInfo{target: target.ProxyTarget, URI: "/"}
			}
			//Even if all URIs allowed, still look for a better match
			for uri, re := range target.uriRegexps {
				if uri != "/*" && re.MatchString(r.RequestURI) {
					matchInfo[name] = &TargetMatchInfo{target: target.ProxyTarget, URI: uri}
					break
				}
			}
		}
	}

	var headerValuesMap map[string]map[string]int
	var queryParamsMap map[string]map[string]int
	for _, m := range matchInfo {
		headerMatches := [][]string{}
		queryMatches := [][]string{}
		if m.target.MatchAny != nil {
			headerMatches = append(headerMatches, m.target.MatchAny.Headers...)
			queryMatches = append(queryMatches, m.target.MatchAny.Query...)
		}
		if m.target.MatchAll != nil {
			headerMatches = append(headerMatches, m.target.MatchAll.Headers...)
			queryMatches = append(queryMatches, m.target.MatchAll.Query...)
		}
		if len(headerMatches) > 0 {
			if headerValuesMap == nil {
				headerValuesMap = util.GetHeaderValues(r)
			}
			for _, hv := range headerMatches {
				if valueMap, present := headerValuesMap[hv[0]]; present {
					if len(hv) == 1 || hv[1] == "" || util.IsFiller(hv[1]) {
						m.Headers = append(m.Headers, []string{hv[0], ""})
					} else {
						if _, found := valueMap[hv[1]]; found {
							m.Headers = append(m.Headers, []string{hv[0], hv[1]})
						}
					}
				}
			}
		}
		if len(queryMatches) > 0 {
			if queryParamsMap == nil {
				queryParamsMap = util.GetQueryParams(r)
			}
			for _, kv := range queryMatches {
				if valueMap, present := queryParamsMap[kv[0]]; present {
					if len(kv) == 1 || kv[1] == "" {
						m.Query = append(m.Query, []string{kv[0], ""})
					} else {
						v, _ := util.GetFillerUnmarked(kv[1])
						if _, found := valueMap[v]; found {
							m.Query = append(m.Query, []string{kv[0], v})
						}
					}
				}
			}
		}
	}
	targetsToBeRemoved := []string{}
	for _, m := range matchInfo {
		if m.target.MatchAll != nil {
			if len(m.target.MatchAll.Headers) != len(m.Headers) ||
				len(m.target.MatchAll.Query) != len(m.Query) {
				targetsToBeRemoved = append(targetsToBeRemoved, m.target.Name)
			}
		} else if m.target.MatchAny != nil {
			if len(m.target.MatchAny.Headers)+len(m.target.MatchAny.Query) > 0 &&
				len(m.Headers)+len(m.Query) == 0 {
				targetsToBeRemoved = append(targetsToBeRemoved, m.target.Name)
			}
		}
	}
	for _, t := range targetsToBeRemoved {
		delete(matchInfo, t)
	}
	return matchInfo
}

func WillProxyHTTP(r *http.Request, rs *util.RequestStore) bool {
	p := getHTTPProxyForRequestPort(r)
	rs.WillProxy = false
	if p.Enabled && p.hasAnyTargets() && !status.IsForcedStatus(r) {
		matches := p.checkMatchingTargetsForRequest(r)
		rs.WillProxy = len(matches) > 0
		if rs.WillProxy {
			rs.ProxyTargets = matches
		}
	}
	return rs.WillProxy
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := getHTTPProxyForRequestPort(r)
		rs := util.GetRequestStore(r)
		if p.Enabled && rs.WillProxy {
			p.router.ServeHTTP(w, r)
		} else if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
