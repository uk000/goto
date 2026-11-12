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

package tunnel

import (
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/transport"
	"goto/pkg/util"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Endpoint struct {
	ID              string `json:"id"`
	URL             string `json:"url"`
	Address         string `json:"address"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Transparent     bool   `json:"transparent"`
	IsTLS           bool   `json:"isTLS"`
	IsH2            bool   `json:"isH2"`
	UseRequestProto bool   `json:"useRequestProto"`
	client          transport.ClientTransport
	clientLock      sync.Mutex
}

type TunnelTrafficLog struct {
	RequestURI             string        `json:"requestURI"`
	RequestHeaders         http.Header   `json:"requestHeaders"`
	RequestProtocol        string        `json:"requestProtocol"`
	RequestPayloadLength   int           `json:"requestPayloadLength"`
	ResponseHeaders        []http.Header `json:"responseHeaders"`
	ResponsePayloadLengths []int         `json:"responsePayloadLength"`
	ResponseProtocol       string        `json:"responseProtocol"`
}

type TunnelTraffic struct {
	Logs                      []*TunnelTrafficLog       `json:"logs"`
	ByEndpoints               map[string]int            `json:"byEndpoints"`
	ByURIs                    map[string]int            `json:"byURIs"`
	ByURIEndpoints            map[string]map[string]int `json:"byURIEndpoints"`
	ByRequestHeaders          map[string]int            `json:"byRequestHeaders"`
	ByRequestHeaderValues     map[string]map[string]int `json:"byRequestHeaderValues"`
	ByRequestHeaderEndpoints  map[string]map[string]int `json:"byRequestHeaderEndpoints"`
	ByURIRequestHeaders       map[string]map[string]int `json:"byURIRequestHeaders"`
	ByRequestQueries          map[string]int            `json:"byRequestQueries"`
	ByRequestQueryValues      map[string]map[string]int `json:"byRequestQueryValues"`
	ByRequestQueryEndpoints   map[string]map[string]int `json:"byRequestQueryEndpoints"`
	ByURIRequestQueries       map[string]map[string]int `json:"byURIRequestQueries"`
	ByResponseHeaders         map[string]int            `json:"byResponseHeaders"`
	ByResponseHeaderValues    map[string]map[string]int `json:"byResponseHeaderValues"`
	ByResponseHeaderEndpoints map[string]map[string]int `json:"byResponseHeaderEndpoints"`
	ByURIResponseHeaders      map[string]map[string]int `json:"byURIResponseHeaders"`
	ByResponseStatus          map[int]int               `json:"byResponseStatus"`
	ByURIResponseStatus       map[string]map[int]int    `json:"byURIResponseStatus"`
}

type ProxyTunnel struct {
	FromAddress string
	ToAddress   string
	IsH2        bool
	IsH2C       bool
	IsTLS       bool
}

type PortTunnel struct {
	Port                  int                                       `json:"port"`
	Tunnels               map[string]*Endpoint                      `json:"tunnels"`
	ProxyTunnels          map[string]*ProxyTunnel                   `json:"proxyTunnels"`
	BroadTunnels          []string                                  `json:"broadTunnels"`
	URITunnels            map[string][]string                       `json:"uriTunnels"`
	HeaderTunnels         map[string]map[string][]string            `json:"headerTunnels"`
	URIHeaderTunnels      map[string]map[string]map[string][]string `json:"uriHeaderTunnels"`
	TunnelTrackingHeaders []string                                  `json:"tunnelTrackingHeaders"`
	TunnelTrackingQueries []string                                  `json:"tunnelTrackingQueries"`
	Traffic               *TunnelTraffic                            `json:"traffic"`
	CaptureTrafficLog     bool                                      `json:"captureTrafficLog"`
	lock                  sync.RWMutex
}

type PipeCallback func(epID, source string, port int, r *http.Request, statusCode int, responseHeaders http.Header, responseBody io.ReadCloser)

var (
	TunnelCountMiddleware  = middleware.NewMiddleware("tunnelCount", nil, TunnelCountHandler)
	tunnels                = map[int]*PortTunnel{}
	tunnelRegexp           = regexp.MustCompile("(?i)tunnel")
	pipeCallbacksByTunnels = map[string]map[string]PipeCallback{}
	tunnelLock             sync.RWMutex
)

// init registers WillTunnel with util so the request pipeline can ask whether a request will be tunneled.
func init() {
	util.WillTunnel = WillTunnel
}

// newPortTunnel creates an initialized PortTunnel for the port.
func newPortTunnel(port int) *PortTunnel {
	return (&PortTunnel{Port: port}).init()
}

// hasScheme tells whether an endpoint address (scheme:host:port) starts with a scheme. A host that just starts with
// the letters of a scheme (e.g. httpbin.org, h2o.example.com) doesn't.
func hasScheme(pieces []string) bool {
	if len(pieces) < 2 {
		return false
	}
	switch pieces[0] {
	case "http", "https", "h2", "h2c":
		return true
	}
	return false
}

// newEndpoint parses an endpoint address (optionally prefixed with http, https, h2 or h2c) into an Endpoint, defaulting to the request's protocol when no scheme is given.
func newEndpoint(address string, tls, transparent bool) *Endpoint {
	id := address
	proto := ""
	port := 80
	h2 := false
	useRequestProto := true
	pieces := strings.Split(address, ":")
	if len(pieces) > 1 {
		port, _ = strconv.Atoi(pieces[len(pieces)-1])
	}
	if hasScheme(pieces) {
		useRequestProto = false
		if pieces[0] == "h2" {
			proto = "https://"
			h2 = true
			tls = true
		} else if pieces[0] == "h2c" {
			proto = "http://"
			h2 = true
			tls = false
		} else {
			proto = pieces[0] + "://"
			tls = pieces[0] == "https"
		}
		if len(pieces) > 1 {
			pieces = pieces[1:]
			address = strings.Join(pieces, ":")
		}
	} else {
		useRequestProto = true
		if tls {
			proto = "https://"
			id = "https:" + address
		} else {
			proto = "http://"
			id = "http:" + address
		}
	}
	return &Endpoint{ID: id, URL: proto + address, Address: address, Host: pieces[0], Port: port, IsTLS: tls, IsH2: h2, UseRequestProto: useRequestProto, Transparent: transparent}
}

// GetOrCreatePortTunnel returns the tunnel config of the port, creating it on first use.
func GetOrCreatePortTunnel(port int) *PortTunnel {
	tunnelLock.Lock()
	defer tunnelLock.Unlock()
	if tunnels[port] == nil {
		tunnels[port] = newPortTunnel(port)
	}
	return tunnels[port]
}

// GetTunnelID builds the endpoint ID of the form protocol:host, where the protocol is h2, h2c, https or http.
func GetTunnelID(isH2, isH2C, isTLS bool, host string) string {
	protocol := "http"
	if isH2 {
		protocol = "h2"
	} else if isH2C {
		protocol = "h2c"
	} else if isTLS {
		protocol = "https"
	}
	return fmt.Sprintf("%s:%s", protocol, host)
}

// CheckTunnelRequest flags the request as a tunnel request, and records the endpoints it asks for, when it is a CONNECT, comes over a proxy-connect tunnel, or carries the goto tunnel or proxy connection header.
func CheckTunnelRequest(r *http.Request) {
	rs := util.GetRequestStore(r)
	if r.Method == http.MethodConnect {
		rs.IsTunnelConnectRequest = true
		return
	}
	pt := GetOrCreatePortTunnel(util.GetRequestOrListenerPortNum(r))
	pt.lock.RLock()
	proxyTunnel, isProxyConnectedTunnel := pt.ProxyTunnels[r.RemoteAddr]
	pt.lock.RUnlock()
	isProxyConnection := len(r.Header[HeaderProxyConnection]) > 0
	if isProxyConnectedTunnel {
		rs.IsTunnelRequest = true
		rs.RequestedTunnels = []string{GetTunnelID(rs.IsH2 || proxyTunnel.IsH2, rs.IsH2C || proxyTunnel.IsH2C, rs.IsTLS || proxyTunnel.IsTLS, proxyTunnel.ToAddress)}
	} else {
		isGotoTunnel := len(r.Header[HeaderGotoTunnel]) > 0
		tunnelID := GetTunnelID(rs.IsH2, rs.IsH2C, rs.IsTLS, r.Host)
		if isGotoTunnel || isProxyConnection {
			rs.IsTunnelRequest = true
		}
		if isProxyConnection {
			rs.RequestedTunnels = []string{tunnelID}
		} else {
			rs.RequestedTunnels = r.Header[HeaderGotoTunnel]
		}
	}
	if isProxyConnection || isProxyConnectedTunnel {
		if r.RequestURI == "*" {
			r.RequestURI = ""
		}
		if u, err := url.Parse(r.RequestURI); err == nil {
			// The URI is what's sent to the endpoint, so the query comes along with the path
			r.RequestURI = u.Path
			if u.RawQuery != "" {
				r.RequestURI += "?" + u.RawQuery
			}
		}
	}
	return
}

// WillTunnel tells whether the request matches a tunnel of its port, and records the matched endpoints in the request store.
func WillTunnel(r *http.Request, rs *util.RequestStore) bool {
	port := util.GetRequestOrListenerPortNum(r)
	tunnelLock.RLock()
	pt := tunnels[port]
	tunnelLock.RUnlock()
	if pt != nil {
		if willTunnel, endpoints := pt.checkTunnelsForRequest(r); willTunnel {
			if willTunnel && len(endpoints) > 0 {
				rs.TunnelEndpoints = endpoints
			}
			return willTunnel
		}
	}
	return false
}

// HijackConnect takes over the client connection of a CONNECT request and pipes it to the server itself as a proxy tunnel, closing it if that fails.
func HijackConnect(r *http.Request, w http.ResponseWriter) bool {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if hijacker, ok := w.(http.Hijacker); ok {
		if clientConn, _, err := hijacker.Hijack(); err == nil {
			rs := util.GetRequestStore(r)
			if GetOrCreatePortTunnel(util.GetRequestOrListenerPortNum(r)).
				openProxyTunnel(r.RemoteAddr, r.Host, rs.IsH2, rs.IsH2C, rs.IsTLS, clientConn) {
				return true
			}
			// The connection has been taken over from the server, so no one else is going to close it.
			clientConn.Close()
		}
	}
	return false
}

// pipeData copies the data from the source to the destination until either of them ends, and closes both. The caller
// adds to the wait group before starting this: adding from here could be after a Wait that is meant to wait for it.
func pipeData(dest io.WriteCloser, source io.ReadCloser, wg *sync.WaitGroup) {
	defer wg.Done()
	defer dest.Close()
	defer source.Close()
	io.Copy(dest, source)
}

// RegisterPipeCallback registers a callback, by name, that is invoked with the response of each request tunneled to the given tunnel endpoint.
func RegisterPipeCallback(tunnel, pipe string, callback PipeCallback) {
	tunnelLock.Lock()
	defer tunnelLock.Unlock()
	if pipeCallbacksByTunnels[tunnel] == nil {
		pipeCallbacksByTunnels[tunnel] = map[string]PipeCallback{}
	}
	pipeCallbacksByTunnels[tunnel][pipe] = callback
}

// init resets all the tunnels, proxy tunnels and tracking state of the port tunnel.
func (pt *PortTunnel) init() *PortTunnel {
	pt.Tunnels = map[string]*Endpoint{}
	pt.ProxyTunnels = map[string]*ProxyTunnel{}
	pt.BroadTunnels = []string{}
	pt.URITunnels = map[string][]string{}
	pt.HeaderTunnels = map[string]map[string][]string{}
	pt.URIHeaderTunnels = map[string]map[string]map[string][]string{}
	pt.initTrackingLocked()
	return pt
}

// initTracking resets the tracking headers, queries and traffic counts of the port tunnel under its lock.
func (pt *PortTunnel) initTracking() {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.initTrackingLocked()
}

// initTrackingLocked is for the callers that have the lock of the tunnel, or that don't share it yet.
func (pt *PortTunnel) initTrackingLocked() {
	pt.TunnelTrackingHeaders = []string{}
	pt.TunnelTrackingQueries = []string{}
	pt.Traffic = &TunnelTraffic{
		Logs:                      []*TunnelTrafficLog{},
		ByEndpoints:               map[string]int{},
		ByURIs:                    map[string]int{},
		ByURIEndpoints:            map[string]map[string]int{},
		ByRequestHeaders:          map[string]int{},
		ByRequestHeaderValues:     map[string]map[string]int{},
		ByRequestHeaderEndpoints:  map[string]map[string]int{},
		ByURIRequestHeaders:       map[string]map[string]int{},
		ByRequestQueries:          map[string]int{},
		ByRequestQueryValues:      map[string]map[string]int{},
		ByRequestQueryEndpoints:   map[string]map[string]int{},
		ByURIRequestQueries:       map[string]map[string]int{},
		ByResponseHeaders:         map[string]int{},
		ByResponseHeaderValues:    map[string]map[string]int{},
		ByResponseHeaderEndpoints: map[string]map[string]int{},
		ByURIResponseHeaders:      map[string]map[string]int{},
		ByResponseStatus:          map[int]int{},
		ByURIResponseStatus:       map[string]map[int]int{},
	}
}

// checkTunnelsForRequest finds the endpoints the request is tunneled to, trying URI+header, URI, header and then broad tunnels in that order; goto tunnel and proxy connection requests always tunnel.
func (pt *PortTunnel) checkTunnelsForRequest(r *http.Request) (willTunnel bool, endpoints []string) {
	uri := r.RequestURI
	if strings.HasPrefix(r.RequestURI, "/tunnel=") || r.Header.Get(HeaderGotoTunnel) != "" || r.Header.Get(HeaderProxyConnection) != "" {
		return true, nil
	}

	pt.lock.RLock()
	defer pt.lock.RUnlock()
	if len(pt.URIHeaderTunnels) == 0 && len(pt.URITunnels) == 0 &&
		len(pt.HeaderTunnels) == 0 && len(pt.BroadTunnels) == 0 {
		return false, nil
	}

	checkHeaders := func(headersEndpointMap map[string]map[string][]string, r *http.Request) {
		for h, hMap := range headersEndpointMap {
			if len(r.Header.Values(h)) == 0 {
				// A tunnel for a header is for the requests that carry the header
				continue
			}
			if hv := r.Header.Get(h); hv != "" {
				endpoints = hMap[hv]
			}
			if len(endpoints) == 0 {
				// A tunnel that was added without a value is for any value of the header
				endpoints = hMap[""]
			}
			if len(endpoints) > 0 {
				break
			}
		}
	}

	if pt.URIHeaderTunnels[uri] != nil {
		checkHeaders(pt.URIHeaderTunnels[uri], r)
	}
	if len(endpoints) == 0 && pt.URITunnels[uri] != nil {
		endpoints = pt.URITunnels[uri]
	}
	if len(endpoints) == 0 && len(pt.HeaderTunnels) > 0 {
		checkHeaders(pt.HeaderTunnels, r)
	}
	if len(endpoints) == 0 && len(pt.BroadTunnels) > 0 {
		endpoints = pt.BroadTunnels
	}
	// The request keeps these, while the tunnels can change
	endpoints = append([]string(nil), endpoints...)
	return len(endpoints) > 0, endpoints
}

// openProxyTunnel dials the server's own port and pipes the client connection to it in both directions, registering the connection as a proxy tunnel until both pipes finish.
func (pt *PortTunnel) openProxyTunnel(fromAddress, toAddress string, isH2, isH2C, isTLS bool, clientConn net.Conn) bool {
	selfAddress := net.JoinHostPort(global.Self.PodIP, strconv.Itoa(pt.Port))
	if selfConn, err := net.DialTimeout("tcp", selfAddress, 10*time.Second); err == nil {
		selfAddress = selfConn.LocalAddr().String()
		pt.lock.Lock()
		pt.ProxyTunnels[selfAddress] = &ProxyTunnel{FromAddress: fromAddress, ToAddress: toAddress, IsH2: isH2, IsH2C: isH2C, IsTLS: isTLS}
		pt.lock.Unlock()
		wg := &sync.WaitGroup{}
		wg.Add(2)
		go pipeData(selfConn, clientConn, wg)
		go pipeData(clientConn, selfConn, wg)
		go func() {
			wg.Wait()
			log.Printf("Tunnel finished from client [%s] via self [%s] to [%s]\n", fromAddress, selfAddress, toAddress)
			pt.lock.Lock()
			delete(pt.ProxyTunnels, selfAddress)
			pt.lock.Unlock()
		}()
		return true
	} else {
		log.Printf("Failed to open a connection to self [%s] in order to support connect tunnel, with error: %s\n", selfAddress, err.Error())
	}
	return false
}

// addTunnel registers an endpoint as a tunnel for all requests, or only those with the given URI, header and value.
func (pt *PortTunnel) addTunnel(endpoint string, tls, transparent bool, uri, header, value string) {
	ep := newEndpoint(endpoint, tls, transparent)
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.Tunnels[ep.ID] = ep
	if uri != "" && header != "" {
		if pt.URIHeaderTunnels[uri] == nil {
			pt.URIHeaderTunnels[uri] = map[string]map[string][]string{}
		}
		if pt.URIHeaderTunnels[uri][header] == nil {
			pt.URIHeaderTunnels[uri][header] = map[string][]string{}
		}
		pt.URIHeaderTunnels[uri][header][value] = appendID(pt.URIHeaderTunnels[uri][header][value], ep.ID)
	} else if uri != "" {
		pt.URITunnels[uri] = appendID(pt.URITunnels[uri], ep.ID)
	} else if header != "" {
		if pt.HeaderTunnels[header] == nil {
			pt.HeaderTunnels[header] = map[string][]string{}
		}
		pt.HeaderTunnels[header][value] = appendID(pt.HeaderTunnels[header][value], ep.ID)
	} else {
		pt.BroadTunnels = appendID(pt.BroadTunnels, ep.ID)
	}
}

// appendID adds the endpoint ID to the list unless it's there already, so that removing it once removes it for good.
func appendID(ids []string, id string) []string {
	for _, v := range ids {
		if v == id {
			return ids
		}
	}
	return append(ids, id)
}

// removeID returns the IDs without the given one, as a new list: the requests that are being served keep the lists
// that they were given.
func removeID(ids []string, id string) (kept []string, removed bool) {
	for _, v := range ids {
		if v == id {
			removed = true
		} else {
			kept = append(kept, v)
		}
	}
	return kept, removed
}

// removeTunnel removes an endpoint (or all endpoints when it is empty) from the tunnel registered for the given URI, header and value, and reports whether something was removed.
func (pt *PortTunnel) removeTunnel(endpoint, uri, header, value string) (exists bool) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if uri != "" && header != "" {
		if pt.URIHeaderTunnels[uri] != nil {
			if pt.URIHeaderTunnels[uri][header] != nil {
				hvEndpoints := pt.URIHeaderTunnels[uri][header][value]
				if len(hvEndpoints) > 0 {
					if endpoint != "" {
						hvEndpoints, exists = removeID(hvEndpoints, endpoint)
						if len(hvEndpoints) == 0 {
							delete(pt.URIHeaderTunnels[uri][header], value)
						} else {
							pt.URIHeaderTunnels[uri][header][value] = hvEndpoints
						}
					} else {
						delete(pt.URIHeaderTunnels[uri][header], value)
						exists = true
					}
				}
			}
			if len(pt.URIHeaderTunnels[uri][header]) == 0 {
				delete(pt.URIHeaderTunnels[uri], header)
			}
			if len(pt.URIHeaderTunnels[uri]) == 0 {
				delete(pt.URIHeaderTunnels, uri)
			}
		}
	} else if uri != "" {
		if pt.URITunnels[uri] != nil {
			if endpoint != "" {
				pt.URITunnels[uri], exists = removeID(pt.URITunnels[uri], endpoint)
				if len(pt.URITunnels[uri]) == 0 {
					delete(pt.URITunnels, uri)
				}
			} else {
				delete(pt.URITunnels, uri)
				exists = true
			}
		}
	} else if header != "" {
		if pt.HeaderTunnels[header] != nil {
			if pt.HeaderTunnels[header][value] != nil {
				hvEndpoints := pt.HeaderTunnels[header][value]
				if len(hvEndpoints) > 0 {
					if endpoint != "" {
						hvEndpoints, exists = removeID(hvEndpoints, endpoint)
						if len(hvEndpoints) == 0 {
							delete(pt.HeaderTunnels[header], value)
						} else {
							pt.HeaderTunnels[header][value] = hvEndpoints
						}
					} else {
						delete(pt.HeaderTunnels[header], value)
						exists = true
					}
				}
			}
			if len(pt.HeaderTunnels[header]) == 0 {
				delete(pt.HeaderTunnels, header)
			}
		}
	} else {
		if endpoint != "" {
			pt.BroadTunnels, exists = removeID(pt.BroadTunnels, endpoint)
			if pt.BroadTunnels == nil {
				pt.BroadTunnels = []string{}
			}
		} else {
			pt.BroadTunnels = []string{}
			exists = true
		}
	}
	return
}

// addTracking adds the headers and query params whose values are counted in the tunnel traffic.
func (pt *PortTunnel) addTracking(headers, queryParams []string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	for _, h := range headers {
		pt.TunnelTrackingHeaders = append(pt.TunnelTrackingHeaders, h)
	}
	for _, q := range queryParams {
		pt.TunnelTrackingQueries = append(pt.TunnelTrackingQueries, q)
	}
}

// clear resets all the tunnels and tracking of the port tunnel.
func (pt *PortTunnel) clear() {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.init()
}

// captureTunnelTraffic turns the capture of per-request tunnel traffic logs on or off.
func (pt *PortTunnel) captureTunnelTraffic(flag bool) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.CaptureTrafficLog = flag
}

// trackRequest counts the request in the tunnel traffic by endpoint, URI, and the tracked headers and query params.
func (pt *PortTunnel) trackRequest(r *http.Request, ep *Endpoint) {
	uri := strings.ToLower(r.RequestURI)
	pt.lock.Lock()
	defer pt.lock.Unlock()
	c := pt.Traffic
	util.UpdateTrackingCountsByURIAndID(ep.ID, uri, c.ByEndpoints, c.ByURIs, c.ByURIEndpoints)
	util.UpdateTrackingCountsByURIKeyValuesID(ep.ID, uri, pt.TunnelTrackingHeaders, r.Header,
		c.ByRequestHeaders, c.ByRequestHeaderValues, c.ByURIRequestHeaders, c.ByRequestHeaderEndpoints)
	util.UpdateTrackingCountsByURIKeyValuesID(ep.ID, uri, pt.TunnelTrackingQueries, r.URL.Query(),
		c.ByRequestQueries, c.ByRequestQueryValues, c.ByURIRequestQueries, c.ByRequestQueryEndpoints)
}

// trackResponse counts the response in the tunnel traffic by status, and by the tracked response headers.
func (pt *PortTunnel) trackResponse(uri string, r *http.Response, ep *Endpoint) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	c := pt.Traffic

	c.ByResponseStatus[r.StatusCode]++
	if c.ByURIResponseStatus[uri] == nil {
		c.ByURIResponseStatus[uri] = map[int]int{}
	}
	c.ByURIResponseStatus[uri][r.StatusCode]++

	util.UpdateTrackingCountsByURIKeyValuesID(ep.ID, uri, pt.TunnelTrackingHeaders, r.Header,
		c.ByResponseHeaders, c.ByResponseHeaderValues, c.ByURIResponseHeaders, c.ByResponseHeaderEndpoints)
}

// sendTunnelResponse writes the first response received from the tunneled endpoints (body, headers, status) to the client and runs the pipe callbacks, discarding the responses of any other endpoints.
func (ep *Endpoint) sendTunnelResponse(viaTunnelLabel, uri string, r *http.Request, w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	rs := util.GetRequestStore(r)
	port := util.GetRequestOrListenerPortNum(r)
	if global.Debug {
		log.Printf("Tunneling URI [%s] to endpoint [%s] - Getting lock\n", uri, ep.ID)
	}
	rs.TunnelLock.Lock()
	defer rs.TunnelLock.Unlock()
	if !rs.IsTunnelResponseSent {
		if global.Debug {
			log.Printf("Tunneling URI [%s] to endpoint [%s] - Writing body\n", uri, ep.ID)
		}
		io.Copy(w, resp.Body)
		resp.Body.Close()
		if global.Debug {
			log.Printf("Tunneling URI [%s] to endpoint [%s] - Copying headers\n", uri, ep.ID)
		}
		util.CopyHeaders(viaTunnelLabel, r, w, r.Header, true, true, true)
		util.CopyHeaders("", r, w, resp.Header, false, false, true)
		w.WriteHeader(resp.StatusCode)
		rs.IsTunnelResponseSent = true
		p := pipeCallbacksByTunnels
		for source, callback := range p[ep.ID] {
			if global.Debug {
				log.Printf("Tunneling URI [%s] to endpoint [%s] - Invoking callback [%s]\n", uri, ep.ID, source)
			}
			callback(ep.ID, source, port, r, resp.StatusCode, w.Header(), resp.Body)
			if global.Debug {
				log.Printf("Tunneling URI [%s] to endpoint [%s] - Finished callback [%s]\n", uri, ep.ID, source)
			}
		}
	} else {
		if global.Debug {
			log.Printf("Tunneling URI [%s] to endpoint [%s] - Discarding body as response already sent\n", uri, ep.ID)
		}
		io.Copy(ioutil.Discard, resp.Body)
	}
	if global.Debug {
		log.Printf("Tunneling URI [%s] to endpoint [%s] - Lock released\n", uri, ep.ID)
	}
}

// getClient returns the client of the endpoint, which is created for the first request that is tunneled to it. The
// requests to an endpoint can be tunneled at the same time.
func (ep *Endpoint) getClient(port int, isH2, isTLS bool) transport.ClientTransport {
	ep.clientLock.Lock()
	defer ep.clientLock.Unlock()
	if ep.client == nil {
		if global.Debug {
			log.Printf("Tunneling to endpoint [%s] - Creating HTTP Client\n", ep.ID)
		}
		ep.client = transport.CreateHTTPClient(port, fmt.Sprintf("Tunnel[%s]", ep.Address), isH2, false, isTLS, false,
			ep.Host, 30*time.Second, 30*time.Second, 3*time.Minute, metrics.ConnTracker)
	}
	return ep.client
}

// addLogMessage adds the message to the log of the request. The endpoints of a request are tunneled to at the same
// time, and the log of a request can't take messages being added from several goroutines.
func addLogMessage(msg string, r *http.Request) {
	rs := util.GetRequestStore(r)
	rs.TunnelLock.Lock()
	defer rs.TunnelLock.Unlock()
	util.AddLogMessage(msg, r)
}

// tunnelToEndpoint sends a copy of the request to one endpoint with the tunnel headers, tracks the response, and writes it to the client; it runs as one goroutine per endpoint.
func (pt *PortTunnel) tunnelToEndpoint(ep *Endpoint, uri, tunnelHostLabel, viaTunnelLabel string, r *http.Request, w http.ResponseWriter, wg *sync.WaitGroup, trafficLog *TunnelTrafficLog) {
	defer wg.Done()
	url := ep.URL + uri
	rs := util.GetRequestStore(r)
	isH2 := ep.IsH2
	isTLS := ep.IsTLS
	if ep.UseRequestProto {
		isH2 = util.IsH2(r)
		isTLS = r.TLS != nil
	}
	msg := fmt.Sprintf("Tunnel Count [%d]. Tunneling to [%s]", rs.TunnelCount, url)
	if global.Debug {
		log.Println(msg)
	}
	addLogMessage(msg, r)
	reqHeaders := http.Header{}
	reqHeaders[HeaderGotoViaTunnelCount] = []string{strconv.Itoa(rs.TunnelCount)}
	if !ep.Transparent {
		// The tunnels of a request run together, so they must not append to the slices of the request's headers.
		reqHeaders[HeaderGotoTunnelHost] = append(append([]string(nil), r.Header[HeaderGotoTunnelHost]...), tunnelHostLabel)
		reqHeaders[HeaderViaGotoTunnel] = append(append([]string(nil), r.Header[HeaderViaGotoTunnel]...), viaTunnelLabel)
	}
	for h, hv := range r.Header {
		if strings.EqualFold(h, HeaderProxyConnection) {
			continue
		}
		if ep.Transparent && tunnelRegexp.MatchString(h) && !strings.EqualFold(h, HeaderGotoTunnel) {
			continue
		}
		reqHeaders[h] = hv
	}
	if global.Debug {
		log.Printf("Tunneling URI [%s] to endpoint [%s] - Creating Request\n", uri, ep.ID)
	}
	rr := util.AsReReader(r.Body)
	method := r.Method
	if method == http.MethodConnect || method == "PRI" {
		if len(rr.Content) > 0 {
			method = "POST"
		} else {
			method = "GET"
		}
	}
	// Each endpoint gets its own reader of the payload, as the endpoints are tunneled to at the same time
	if req, err := transport.CreateRequest(method, url, reqHeaders, rr.Content, nil); err == nil {
		req.Host = ep.Host
		client := ep.getClient(pt.Port, isH2, isTLS)
		client.UpdateTLSConfig(rs.ServerName, rs.TLSVersionNum, false, nil)
		if global.Debug {
			log.Printf("Tunneling URI [%s] to endpoint [%s] - Sending Request\n", uri, ep.ID)
		}
		if resp, err := client.HTTP().Do(req); err == nil {
			rr := util.CreateOrGetReReader(resp.Body)
			resp.Body = rr
			msg = fmt.Sprintf("Got response from tunnel [%s]: %s", url, resp.Status)
			if global.Debug {
				log.Println(msg)
			}
			addLogMessage(msg, r)
			pt.trackResponse(uri, resp, ep)
			ep.sendTunnelResponse(viaTunnelLabel, uri, r, w, resp)
			if trafficLog != nil {
				pt.lock.Lock()
				trafficLog.ResponseHeaders = append(trafficLog.ResponseHeaders, resp.Header)
				trafficLog.ResponsePayloadLengths = append(trafficLog.ResponsePayloadLengths, len(rr.Content))
				trafficLog.ResponseProtocol = util.GotoProtocol(ep.IsH2, ep.IsTLS, rs.IsGRPC)
				pt.lock.Unlock()
			}
		} else {
			msg := fmt.Sprintf("Error invoking tunnel to [%s]: %s", url, err.Error())
			if global.Debug {
				log.Println(msg)
			}
			rs.TunnelLock.Lock()
			fmt.Fprintln(w, msg)
			rs.TunnelLock.Unlock()
			addLogMessage(msg, r)
		}
	} else {
		msg := fmt.Sprintf("Error creating tunnel request for [%s]: %s", url, err.Error())
		rs.TunnelLock.Lock()
		fmt.Fprintln(w, msg)
		rs.TunnelLock.Unlock()
		addLogMessage(msg, r)
	}
}

// tunnel resolves the endpoints (given addresses or the ones matched for the request), tunnels the request to all of them concurrently and waits, logging the traffic if capture is on.
func (pt *PortTunnel) tunnel(addresses []string, uri string, r *http.Request, w http.ResponseWriter) {
	if global.Debug {
		log.Printf("Tunnel: Enter [%s] - %+v\n", uri, addresses)
	}
	metrics.UpdateRequestCount("tunnel")
	endpoints := map[string]*Endpoint{}
	if len(addresses) > 0 {
		for _, a := range addresses {
			endpoints[a] = newEndpoint(a, r.TLS != nil, true)
		}
	} else {
		rs := util.GetRequestStore(r)
		if rs.TunnelEndpoints != nil {
			if eps, ok := rs.TunnelEndpoints.([]string); ok {
				pt.lock.RLock()
				for _, ep := range eps {
					// A tunnel that was removed since the request was matched to it is not there anymore
					if pt.Tunnels[ep] != nil {
						endpoints[ep] = pt.Tunnels[ep]
					}
				}
				pt.lock.RUnlock()
			}
		}
	}
	if len(endpoints) == 0 {
		if global.Debug {
			log.Printf("Tunnel request without any endpoints: [%s] - %+v\n", uri, addresses)
		}
		return
	}
	l := listeners.GetCurrentListener(r)
	rs := util.GetRequestStore(r)
	tunnelCount := util.GetTunnelCount(r)
	tunnelHostLabel := fmt.Sprintf("%s|%d", l.HostLabel, tunnelCount)
	viaTunnelLabel := fmt.Sprintf("%s|%d", l.Label, tunnelCount)
	rr := util.AsReReader(r.Body)
	var trafficLog *TunnelTrafficLog
	pt.lock.RLock()
	captureTraffic := pt.CaptureTrafficLog
	pt.lock.RUnlock()
	if captureTraffic {
		trafficLog = &TunnelTrafficLog{
			RequestURI:           r.RequestURI,
			RequestHeaders:       r.Header,
			RequestPayloadLength: len(rr.Content),
			RequestProtocol:      rs.GotoProtocol,
		}
	}
	wg := &sync.WaitGroup{}
	wg.Add(len(endpoints))
	for _, ep := range endpoints {
		pt.trackRequest(r, ep)
		if global.Debug {
			log.Printf("Tunneling URI [%s] to endpoint [%s]\n", uri, ep.ID)
		}
		go pt.tunnelToEndpoint(ep, uri, tunnelHostLabel, viaTunnelLabel, r, w, wg, trafficLog)
	}
	wg.Wait()
	// The endpoints were sent copies of the payload, and the payload of the request is left to be read again
	rr.Rewind()
	if trafficLog != nil {
		pt.lock.Lock()
		pt.Traffic.Logs = append(pt.Traffic.Logs, trafficLog)
		pt.lock.Unlock()
	}
	if global.Debug {
		log.Printf("Tunnel: Exit [%s] - %+v\n", uri, addresses)
	}
}

// tunnel is the handler of tunnel requests, which works out the addresses to tunnel to from the URL or the requested tunnels, and hands the request to the tunnel of the port.
func tunnel(w http.ResponseWriter, r *http.Request) {
	addressPath := util.GetStringParamValue(r, "address")
	addresses, present := util.GetListParam(r, "address")
	uri := r.RequestURI
	if present {
		if pieces := strings.Split(uri, addressPath); len(pieces) > 1 {
			uri = pieces[1]
		}
	} else {
		rs := util.GetRequestStore(r)
		r.Header[HeaderGotoRequestedTunnel] = rs.RequestedTunnels
		addresses = rs.RequestedTunnels
		if len(addresses) == 1 {
			addresses = strings.Split(addresses[0], ",")
			if len(addresses) > 1 {
				r.Header[HeaderGotoTunnel] = []string{strings.Join(addresses[1:], ",")}
			} else {
				delete(r.Header, HeaderGotoTunnel)
			}
			addresses = addresses[0:1]
		} else {
			delete(r.Header, HeaderGotoTunnel)
		}
	}
	tunnel := GetOrCreatePortTunnel(util.GetRequestOrListenerPortNum(r))
	tunnel.tunnel(addresses, uri, r, w)
}

// tunnelHostName is the host that a value of the tunnel host header is for. The value is the label of the host
// followed by the tunnel count at the host, as tunnelHostLabel has it.
func tunnelHostName(headerValue string) string {
	if i := strings.LastIndex(headerValue, "|"); i >= 0 {
		return headerValue[:i]
	}
	return headerValue
}

// middlewareFunc tunnels the tunnel requests, stopping with a message if a loop is detected through the tunnel host header, and passes the rest on.
func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if util.IsTunnelRequest(r) {
			l := listeners.GetCurrentListener(r)
			for _, hv := range r.Header[HeaderGotoTunnelHost] {
				if strings.EqualFold(tunnelHostName(hv), l.HostLabel) {
					msg := fmt.Sprintf("Tunnel loop detected in header [%s] : %+v", HeaderGotoTunnelHost, r.Header[HeaderGotoTunnelHost])
					util.AddLogMessage(msg, r)
					fmt.Fprintln(w, msg)
					w.Header().Add(HeaderViaGoto, util.GetViaGotoValue(l.Port)+"[tunnel loop]")
					util.UnsetTunnelRequest(r)
					return
				}
			}
			tunnel(w, r)
		} else if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

// TunnelCountHandler sets the tunnel count of a tunnel request to one more than the count in the via-tunnel-count header.
func TunnelCountHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if util.IsTunnelRequest(r) {
			tunnelCount, _ := strconv.Atoi(r.Header.Get(HeaderGotoViaTunnelCount))
			tunnelCount++
			util.SetTunnelCount(r, tunnelCount)
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
