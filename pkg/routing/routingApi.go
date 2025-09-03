package routing

import (
	"fmt"
	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	"goto/pkg/transport"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/mux"
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
	Label      string    `json:"label"`
	From       RouteFrom `json:"from"`
	To         RouteTo   `json:"to"`
	rootMatch  bool
	basePrefix string
	re         *regexp.Regexp
	router     *mux.Router
	client     transport.ClientTransport
}

type PortRouter struct {
	Routes map[string]*Route `json:"routes"`
	lock   sync.RWMutex
}

var (
	Middleware           = middleware.NewMiddleware("routing", setRoutes, nil)
	PortRouters          = map[int]*PortRouter{}
	RequestCorrelationID atomic.Int64
	lock                 sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	routingRouter := util.PathRouter(r, "/routing")
	util.AddRouteWithPort(routingRouter, "/add", addRoute, "POST")
	util.AddRouteWithPort(routingRouter, "/clear", clearRoutes, "POST")
	util.AddRouteWithPort(routingRouter, "", getRoutes, "GET")
}

func addRoute(w http.ResponseWriter, r *http.Request) {
	route := &Route{}
	err := util.ReadJsonPayload(r, route)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Failed to parse routing payload with error [%s]", err.Error())
	} else if !route.IsValid() {
		msg = fmt.Sprintf("Invalid route: [%+v]", route)
	} else {
		pr := GetPortRouter(route.From.Port)
		pr.AddRoute(route)
		msg = fmt.Sprintf("Route added: [%+v]", route)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pr := GetPortRouter(port)
	pr.Clear()
	msg := fmt.Sprintf("Routes cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pr := GetPortRouter(port)
	util.WriteJsonPayload(w, pr)
	msg := fmt.Sprintf("Routes reported on port [%d]", port)
	util.AddLogMessage(msg, r)
}

func GetPortRouter(port int) *PortRouter {
	lock.Lock()
	defer lock.Unlock()
	if PortRouters[port] == nil {
		PortRouters[port] = &PortRouter{
			Routes: map[string]*Route{},
		}
	}
	return PortRouters[port]
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
	uri := strings.ToLower(r.From.URIPrefix)
	if uri == "" || uri == "/" {
		uri = "/*"
		r.rootMatch = true
	}
	if prefix, re, router, err := util.BuildURIAndPortMatcher(util.PortRouter, uri, r.From.Port, r.RouteRequest); err == nil {
		r.basePrefix = prefix
		r.re = re
		r.router = router
		r.client, _ = transport.CreateDefaultHTTPClient(r.Label, r.To.IsH2, r.To.IsTLS, metrics.ConnTracker)
	} else {
		log.Printf("Route: Failed to add URI match [%s] with error: %s\n", uri, err.Error())
		return err
	}
	return nil
}

func (r *Route) RouteRequest(w http.ResponseWriter, hr *http.Request) {
	//	uri := string(r.re.ReplaceAll([]byte(hr.RequestURI), []byte(r.To.URIPrefix)))
	uri := hr.RequestURI
	rr := util.NewReReader(hr.Body)
	req, err := r.prepareRequest(r.To.URL, uri, hr, rr)
	id := RequestCorrelationID.Add(1)
	msg := fmt.Sprintf("Routing ID [%d]: Request URI [%s], Routing to upstream [%s], URI [%s], Headers [%+v], Body [%s] ",
		id, hr.RequestURI, r.To.URL, uri, hr.Header, string(rr.Content))
	util.AddLogMessage(msg, hr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Routing ID [%d]: Failed to prepare upstream request with error [%s]", id, err.Error())
		fmt.Fprintln(w, msg)
	} else {
		resp, err := r.client.HTTP().Do(req)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			msg = fmt.Sprintf("Routing ID [%d]: Upstream request failed with error [%s]", id, err.Error())
			fmt.Fprintln(w, msg)
		} else {
			headers := r.prepareHeaders(resp.Header, r.To.ResponseHeaders)
			for k, v := range headers {
				for _, val := range v {
					w.Header().Add(k, val)
				}
			}
			rr = util.NewReReader(resp.Body)
			rr.Rewind()
			if len, err := io.Copy(w, rr); err != nil {
				msg = fmt.Sprintf("Routing ID [%d]: Downstream response failed with error [%s]", id, err.Error())
			} else if len != int64(rr.Length()) {
				msg = fmt.Sprintf("Routing ID [%d]: Downstream response length [%d] didn't match upstream response length [%d]", id, len, rr.Length())
			} else {
				msg = fmt.Sprintf("Routing ID [%d]: Request URI [%s], Routed successfully to upstream [%s]. Response Headers [%+v], Response Body [%s]",
					id, hr.RequestURI, r.To.URL, headers, string(rr.Content))
			}
		}
	}
	util.AddLogMessage(msg, hr)
}

func (r *Route) prepareRequest(url, uri string, hr *http.Request, rr *util.ReReader) (req *http.Request, err error) {
	if !strings.HasPrefix(url, "http") {
		if r.To.IsTLS {
			url = "https://" + url
		} else {
			url = "http://" + url
		}
	}
	rr.Rewind()
	req, err = http.NewRequest(hr.Method, url+uri, rr)
	if err != nil {
		return nil, err
	}
	req.Header = r.prepareHeaders(hr.Header, r.To.RequestHeaders)
	if r.To.Authority != "" {
		req.Host = r.To.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	req.ContentLength = hr.ContentLength
	return
}

func (r *Route) prepareHeaders(hh http.Header, h *Headers) map[string][]string {
	headers := map[string][]string{}
	for k, v := range hh {
		if h != nil {
			if _, present := h.Remove[k]; present {
				continue
			}
		}
		if len(v) > 0 {
			headers[k] = v
		}
	}
	if h != nil && len(h.Add) > 0 {
		for k, v := range h.Add {
			headers[k] = []string{v}
		}
	}
	return headers
}
