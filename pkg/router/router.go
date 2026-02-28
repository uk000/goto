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

package router

import (
	"fmt"
	"goto/pkg/metrics"
	"goto/pkg/server/intercept"
	"goto/pkg/server/request"
	"goto/pkg/server/response"
	"goto/pkg/transport"
	"goto/pkg/util"
	"log"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type RouteFrom struct {
	Port      int    `json:"port"`
	URIPrefix string `json:"uriPrefix"`
}

type Headers struct {
	Add    map[string]string `json:"add"`
	Remove map[string]string `json:"remove"`
}

type RouteTo struct {
	URL             string   `json:"url"`
	URIPrefix       string   `json:"uriPrefix"`
	Authority       string   `json:"authority"`
	RequestHeaders  *Headers `json:"requestHeaders"`
	ResponseHeaders *Headers `json:"responseHeaders"`
	IsH2            bool     `json:"http2"`
	IsTLS           bool     `json:"tls"`
}

type Route struct {
	Label       string    `json:"label"`
	From        RouteFrom `json:"from"`
	To          RouteTo   `json:"to"`
	LogBody     bool      `json:"logBody"`
	ProcessReq  bool      `json:"processReq"`
	ProcessResp bool      `json:"processResp"`
	rootMatch   bool
	basePrefix  string
	re          *regexp.Regexp
	client      transport.ClientTransport
	handler     http.Handler
}

type PortRouter struct {
	ID                     string            `json:"id"`
	Port                   int               `json:"port"`
	Routes                 map[string]*Route `json:"routes"`
	ActiveRoutes           map[string]*Route
	proxy                  *httputil.ReverseProxy
	intercept              transport.IHTTPTransportIntercept
	requestHandler         http.Handler
	responseHandler        http.Handler
	requestResponseHandler http.Handler
	lock                   sync.RWMutex
}

var (
	PortRouters          = map[int]*PortRouter{}
	RequestCorrelationID atomic.Int64
	lock                 sync.RWMutex
)

func WillRoute(port int, r *http.Request) http.Handler {
	route := GetPortRouter(port).GetMatchingRoute(r)
	if route != nil {
		if route.WillReroute(r) {
			return route.handler
		}
	}
	return nil
}

func GetPortRouter(port int) *PortRouter {
	lock.Lock()
	defer lock.Unlock()
	if PortRouters[port] == nil {
		PortRouters[port] = newPortRouter(port)
	}
	return PortRouters[port]
}

func newPortRouter(port int) *PortRouter {
	pr := &PortRouter{
		ID:           fmt.Sprintf("PortRouter-%d", port),
		Port:         port,
		Routes:       map[string]*Route{},
		ActiveRoutes: map[string]*Route{},
	}
	client := transport.CreateDefaultHTTPClient(pr.ID, false, false, metrics.ConnTracker)
	pr.intercept = client.Transport().AsHTTP()
	pr.intercept.SetResponseIntercept(pr)
	pr.proxy = &httputil.ReverseProxy{
		Rewrite:   pr.GetRewriter(),
		Transport: pr.intercept,
	}

	pr.requestHandler = intercept.IntereceptMiddleware(request.Middleware.MiddlewareHandler.Middleware(nil), nil)(pr.proxy)
	pr.responseHandler = intercept.IntereceptMiddleware(nil, response.Middleware.MiddlewareHandler.Middleware(nil))(pr.proxy)
	pr.requestResponseHandler = intercept.IntereceptMiddleware(request.Middleware.MiddlewareHandler.Middleware(nil), response.Middleware.MiddlewareHandler.Middleware(nil))(pr.proxy)
	return pr
}

func (pr *PortRouter) Clear() {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	pr.Routes = map[string]*Route{}
}

func (pr *PortRouter) AddRoute(r *Route) {
	r.Setup()
	pr.lock.Lock()
	defer pr.lock.Unlock()
	pr.Routes[r.Label] = r
	r.To.RequestHeaders.Remove = util.ToLowerHeader(r.To.RequestHeaders.Remove)
	r.To.ResponseHeaders.Remove = util.ToLowerHeader(r.To.ResponseHeaders.Remove)
	if r.ProcessReq && r.ProcessResp {
		r.handler = pr.requestResponseHandler
	} else if r.ProcessReq {
		r.handler = pr.requestHandler
	} else if r.ProcessResp {
		r.handler = pr.responseHandler
	} else {
		r.handler = pr.proxy
	}
}

func (pr *PortRouter) AddActiveRoute(id string, r *Route) {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	pr.ActiveRoutes[id] = r
}

func (pr *PortRouter) GetActiveRoute(id string) *Route {
	pr.lock.RLock()
	defer pr.lock.RUnlock()
	return pr.ActiveRoutes[id]
}

func (pr *PortRouter) GetMatchingRoute(r *http.Request) *Route {
	for _, route := range pr.Routes {
		if route.re.MatchString(r.RequestURI) {
			return route
		}
	}
	return nil
}

func (pr *PortRouter) GetRewriter() func(*httputil.ProxyRequest) {
	return func(proxyReq *httputil.ProxyRequest) {
		route := pr.GetMatchingRoute(proxyReq.In)
		if route == nil {
			return
		}
		if !route.WillReroute(proxyReq.In) {
			return
		}
		id := strconv.Itoa(int(RequestCorrelationID.Add(1)))
		req, rs, err := route.prepareRequest(proxyReq.In, id)
		if err != nil {
			log.Printf("Routing ID [%s]: Request URI [%s]: Failed to prepare upstream request with error [%s]\n", id, proxyReq.In.RequestURI, err.Error())
			return
		}
		proxyReq.Out = req
		pr.AddActiveRoute(id, route)
		body := ""
		if route.LogBody {
			body = string(rs.ReReader.Content)
		}
		log.Printf("Routing ID [%s]: Request URI [%s]: Routing to upstream [%s], URI [%s], Headers [%+v], Body [%s]\n",
			id, proxyReq.In.RequestURI, route.To.URL, proxyReq.Out.RequestURI, proxyReq.Out.Header, body)
	}
}

func (pr *PortRouter) Intercept(resp *http.Response) {
	req := resp.Request
	util.GetRequestStore(req)
	rr := util.CreateOrGetReReader(resp.Body)
	resp.Body = rr
	id := req.Header.Get("X-Correlation-ID")
	var route *Route
	if id != "" {
		route = pr.GetActiveRoute(id)
	}
	if route == nil {
		route = pr.GetMatchingRoute(req)
	}
	msg := ""
	if route != nil {
		body := ""
		if route.LogBody {
			body = string(rr.Content)
		}
		prepareHeaders(resp.Header, resp.Header, route.To.ResponseHeaders)
		msg = fmt.Sprintf("Routing ID [%s]: Request URI [%s], Response from upstream URL [%s] sent to downstream. Response Headers [%+v], Response Body [%s]",
			id, req.RequestURI, route.To.URL, resp.Header, body)
	} else {
		msg = fmt.Sprintf("Failed to find matching route (ID [%d]) for request URI [%s]. Response from upstream URL [%s] sent to downstream with original headers. Response Headers [%+v]",
			id, req.RequestURI, req.RequestURI, resp.Request.URL.String(), resp.Header)

	}
	log.Println(msg)
	util.AddLogMessage(msg, req)
}

func (r *Route) WillReroute(req *http.Request) bool {
	return strings.HasPrefix(req.RequestURI, r.basePrefix)
}

func (r *Route) IsValid() bool {
	if r.From.Port <= 0 || r.From.URIPrefix == "" || r.To.URL == "" {
		return false
	}
	if r.To.URIPrefix == "" {
		r.To.URIPrefix = r.From.URIPrefix
	}
	return true
}

func (r *Route) Setup() error {
	if !strings.HasPrefix(r.To.URL, "http") {
		if r.To.IsTLS {
			r.To.URL = "https://" + r.To.URL
		} else {
			r.To.URL = "http://" + r.To.URL
		}
	}
	uri := strings.ToLower(r.From.URIPrefix)
	if uri == "" || uri == "/" {
		uri = "/*"
		r.rootMatch = true
	}
	if prefix, re, err := util.GetURIRegexp(uri); err == nil {
		r.basePrefix = prefix
		r.re = re
		r.client = transport.CreateDefaultHTTPClient(r.Label, r.To.IsH2, r.To.IsTLS, metrics.ConnTracker)
	} else {
		log.Printf("Route: Failed to add URI match [%s] with error: %s\n", uri, err.Error())
		return err
	}
	return nil
}

// func (r *Route) RouteRequest(w http.ResponseWriter, hr *http.Request) {
// 	//	uri := string(r.re.ReplaceAll([]byte(hr.RequestURI), []byte(r.To.URIPrefix)))
// 	uri := hr.RequestURI
// 	rr := util.CreateOrGetReReader(hr.Body)
// 	id := strconv.Itoa(int(RequestCorrelationID.Add(1)))
// 	req, err := r.prepareRequest(hr, rr, id)
// 	msg := fmt.Sprintf("Routing ID [%s]: Request URI [%s], Routing to upstream [%s], URI [%s], Headers [%+v], Body [%s] ",
// 		id, hr.RequestURI, r.To.URL, uri, hr.Header, string(rr.Content))
// 	util.AddLogMessage(msg, hr)
// 	if err != nil {
// 		w.WriteHeader(http.StatusBadRequest)
// 		msg = fmt.Sprintf("Routing ID [%s]: Failed to prepare upstream request with error [%s]", id, err.Error())
// 		fmt.Fprintln(w, msg)
// 	} else {
// 		resp, err := r.client.HTTP().Do(req)
// 		if err != nil {
// 			w.WriteHeader(http.StatusServiceUnavailable)
// 			msg = fmt.Sprintf("Routing ID [%s]: Upstream request failed with error [%s]", id, err.Error())
// 			fmt.Fprintln(w, msg)
// 		} else {
// 			prepareHeaders(resp.Header, w.Header(), r.To.ResponseHeaders)
// 			rr = util.CreateOrGetReReader(resp.Body)
// 			rr.Rewind()
// 			if len, err := io.Copy(w, rr); err != nil {
// 				msg = fmt.Sprintf("Routing ID [%s]: Downstream response failed with error [%s]", id, err.Error())
// 			} else if len != int64(rr.Length()) {
// 				msg = fmt.Sprintf("Routing ID [%s]: Downstream response length [%d] didn't match upstream response length [%d]", id, len, rr.Length())
// 			} else {
// 				msg = fmt.Sprintf("Routing ID [%s]: Request URI [%s], Routed successfully to upstream [%s]. Response Headers [%+v], Response Body [%s]",
// 					id, hr.RequestURI, r.To.URL, w.Header(), string(rr.Content))
// 			}
// 		}
// 	}
// 	util.AddLogMessage(msg, hr)
// }

func (r *Route) prepareRequest(inReq *http.Request, id string) (req *http.Request, rs *util.RequestStore, err error) {
	rs = util.GetRequestStore(inReq)
	uri := inReq.RequestURI
	if r.To.URIPrefix != "" {
		uri = strings.Replace(uri, r.From.URIPrefix, r.To.URIPrefix, 1)
	}
	req, err = http.NewRequest(inReq.Method, r.To.URL+uri, rs.ReReader)
	if err != nil {
		return nil, nil, err
	}
	prepareHeaders(inReq.Header, req.Header, r.To.RequestHeaders)
	if r.To.Authority != "" {
		req.Host = r.To.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	req.ContentLength = inReq.ContentLength
	req.Header.Add("X-Correlation-ID", id)
	return
}

func prepareHeaders(inHeaders, outHeaders http.Header, overrides *Headers) {
	for k, v := range inHeaders {
		if overrides != nil {
			if v2, present := overrides.Remove[strings.ToLower(k)]; present {
				if v2 == "" || len(v) == 0 || strings.EqualFold(v[0], v2) {
					delete(outHeaders, k)
					continue
				}
			}
		}
		if _, present := outHeaders[k]; !present {
			outHeaders[k] = v
		}
	}
	if overrides != nil && len(overrides.Add) > 0 {
		for k, v := range overrides.Add {
			outHeaders[k] = []string{v}
		}
	}
}
