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

package router

import (
	"bytes"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/server/echo"
	"goto/pkg/server/intercept"
	"goto/pkg/server/listeners"
	"goto/pkg/server/request"
	"goto/pkg/server/response"
	"goto/pkg/transport"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	LogHops     bool      `json:"logHops"`
	LogBody     bool      `json:"logBody"`
	ProcessReq  bool      `json:"processReq"`
	ProcessResp bool      `json:"processResp"`
	rootMatch   bool
	basePrefix  string
	re          *regexp.Regexp
	client      transport.ClientTransport
	handler     http.Handler
}

type RouteTraffic struct {
	Route
	Listener            string              `json:"listener"`
	GotoHost            string              `json:"gotoHost"`
	RequestAt           time.Time           `json:"requestAt"`
	ResponseAt          time.Time           `json:"responseAt"`
	Took                time.Duration       `json:"took"`
	RemoteAddr          string              `json:"remoteAddr"`
	RequestHost         string              `json:"requestHost"`
	RequestURI          string              `json:"requestURI"`
	RequestQuery        string              `json:"requestQuery"`
	RequestHeaders      map[string][]string `json:"requestHeaders"`
	ResponseHeaders     map[string][]string `json:"responseHeaders"`
	RequestMethod       string              `json:"requestMethod"`
	RequestProto        string              `json:"requestProto"`
	RequestPayloadSize  int                 `json:"requestPayloadSize"`
	ResponsePayloadSize int                 `json:"responsePayloadSize"`
}

type PortRouter struct {
	ID                     string                   `json:"id"`
	Port                   int                      `json:"port"`
	Routes                 map[string]*Route        `json:"routes"`
	RouteTraffic           map[string]*RouteTraffic `json:"traffic"`
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

func Init() {
	PortRouters = map[int]*PortRouter{}
	RequestCorrelationID = atomic.Int64{}
}

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
		RouteTraffic: map[string]*RouteTraffic{},
	}
	client := transport.CreateDefaultHTTPClient(pr.ID, false, false, "", metrics.ConnTracker)
	pr.intercept = client.Transport().AsHTTP()
	//pr.intercept.SetResponseIntercept(pr)
	pr.proxy = &httputil.ReverseProxy{
		Rewrite:        pr.GetRewriter(),
		Transport:      pr.intercept,
		ModifyResponse: pr.Intercept,
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
	pr.RouteTraffic = map[string]*RouteTraffic{}
}

func (pr *PortRouter) AddRoute(r *Route) {
	r.Setup()
	pr.lock.Lock()
	defer pr.lock.Unlock()
	pr.Routes[r.Label] = r
	if r.To.RequestHeaders != nil {
		r.To.RequestHeaders.Remove = util.ToLowerHeader(r.To.RequestHeaders.Remove)
	}
	if r.To.ResponseHeaders != nil {
		r.To.ResponseHeaders.Remove = util.ToLowerHeader(r.To.ResponseHeaders.Remove)
	}
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

func (pr *PortRouter) AddRouteTraffic(id string, r *Route, req *http.Request) *RouteTraffic {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	a := &RouteTraffic{
		Route:          *r,
		Listener:       listeners.GetListenerLabelForPort(r.From.Port),
		GotoHost:       global.Self.HostLabel,
		RequestAt:      time.Now(),
		RemoteAddr:     req.RemoteAddr,
		RequestURI:     req.RequestURI,
		RequestQuery:   req.URL.Query().Encode(),
		RequestHeaders: req.Header,
		RequestMethod:  req.Method,
		RequestProto:   req.Proto,
	}
	pr.RouteTraffic[id] = a
	return a
}

func (pr *PortRouter) GetRouteTraffic(id string) *RouteTraffic {
	pr.lock.RLock()
	defer pr.lock.RUnlock()
	return pr.RouteTraffic[id]
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
		ar := pr.AddRouteTraffic(id, route, proxyReq.In)
		body := ""
		if route.LogBody {
			body = string(rs.ReReader.Content)
			ar.RequestPayloadSize = len(body)
		}
		log.Printf("Routing ID [%s]: Request URI [%s]: Routing to upstream [%s], URI [%s], Headers [%+v], Body [%s]\n",
			id, proxyReq.In.RequestURI, route.To.URL, proxyReq.Out.RequestURI, proxyReq.Out.Header, body)
	}
}

func (pr *PortRouter) Intercept(resp *http.Response) error {
	req := resp.Request
	rs := util.GetRequestStore(req)
	rr := util.CreateOrGetReReader(resp.Body)
	id := req.Header.Get("X-Correlation-ID")
	var rt *RouteTraffic
	var route *Route
	if id != "" {
		rt = pr.GetRouteTraffic(id)
		if rt != nil {
			route = &rt.Route
		}
	}
	if route == nil {
		route = pr.GetMatchingRoute(req)
		if route != nil {
			rt = pr.AddRouteTraffic(id, route, req)
		}
	}
	msg := ""
	if route != nil {
		body := ""
		rt.ResponseHeaders = map[string][]string{}
		util.CopyHeadersWithPrefix("", resp.Header, rt.ResponseHeaders)
		prepareHeaders(resp.Header, resp.Header, route.To.ResponseHeaders)
		if route.LogHops || route.LogBody {
			body = string(rr.Content)
			rt.ResponsePayloadSize = len(body)
		}
		msg = fmt.Sprintf("Routing ID [%s]: Request URI [%s], Response from upstream URL [%s] sent to downstream. Response Headers [%+v], Response Body [%s]",
			id, req.RequestURI, route.To.URL, resp.Header, body)
		rt.ResponseAt = time.Now()
		if !rt.RequestAt.IsZero() {
			rt.Took = rt.ResponseAt.Sub(rt.RequestAt)
		}
		if route.LogHops {
			var content any
			if err := util.ReadJson(body, &content); err != nil {
				content = body
			}
			selfHeaders := echo.GetEchoResponse(rt.Listener, rt.RemoteAddr, rt.RequestHost, rt.RequestURI, rt.RequestMethod, rt.RequestProto,
				rt.RequestQuery, rt.Route.From.Port, rt.RequestPayloadSize, rt.ResponsePayloadSize, rt.RequestHeaders, rs.IsTLS)
			routeRequestAt := fmt.Sprintf("%s/%s", rt.Listener, rt.RequestAt.Format(time.RFC3339Nano))
			routeResponseAt := fmt.Sprintf("%s/%s", rt.Listener, rt.ResponseAt.Format(time.RFC3339Nano))
			routeTook := fmt.Sprintf("%s/%s", rt.Listener, rt.Took.String())
			selfHeaders["Response-Headers"] = rt.ResponseHeaders
			selfHeaders["Route-Request-At"] = routeRequestAt
			selfHeaders["Route-Response-At"] = routeResponseAt
			selfHeaders["Route-Took"] = routeTook
			resp.Header.Add("Route-Request-At", routeRequestAt)
			resp.Header.Add("Route-Response-At", routeResponseAt)
			resp.Header.Add("Route-Took", routeTook)
			hops := map[string]map[string]map[string]any{
				rt.Listener: {
					"self": selfHeaders,
					"upstream": {
						"headers": resp.Header,
						"body":    content,
					},
				},
			}
			data := util.ToJSONBytes(hops)
			resp.ContentLength = int64(len(data))
			resp.Header.Set("Content-Length", fmt.Sprint(len(data)))
			resp.Body = io.NopCloser(bytes.NewReader(data))
		} else {
			resp.Body = rr
		}
	} else {
		msg = fmt.Sprintf("Failed to find matching route (ID [%s]) for request URI [%s]. Response from upstream URL [%s] sent to downstream with original headers. Response Headers [%+v]",
			id, req.RequestURI, resp.Request.URL.String(), resp.Header)

	}
	log.Println(msg)
	util.AddLogMessage(msg, req)
	return nil
}

func (r *Route) WillReroute(req *http.Request) bool {
	return strings.HasPrefix(req.RequestURI, r.basePrefix)
}

func (r *Route) IsValid() bool {
	if r.From.Port <= 0 || r.To.URL == "" {
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
		r.client = transport.CreateDefaultHTTPClient(r.Label, r.To.IsH2, r.To.IsTLS, r.To.Authority, metrics.ConnTracker)
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
	req.Header.Set("X-Correlation-ID", id)
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
