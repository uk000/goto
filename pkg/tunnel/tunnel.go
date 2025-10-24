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

func init() {
	util.WillTunnel = WillTunnel
}

func newPortTunnel(port int) *PortTunnel {
	return (&PortTunnel{Port: port}).init()
}

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
	if strings.HasPrefix(address, "http") || strings.HasPrefix(address, "h2") {
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

func GetOrCreatePortTunnel(port int) *PortTunnel {
	tunnelLock.Lock()
	defer tunnelLock.Unlock()
	if tunnels[port] == nil {
		tunnels[port] = newPortTunnel(port)
	}
	return tunnels[port]
}

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
			r.RequestURI = u.Path
		}
	}
	return
}

func WillTunnel(r *http.Request, rs *util.RequestStore) bool {
	tunnelLock.Lock()
	defer tunnelLock.Unlock()
	port := util.GetRequestOrListenerPortNum(r)
	if tunnels[port] != nil {
		if willTunnel, endpoints := tunnels[port].checkTunnelsForRequest(r); willTunnel {
			if willTunnel && len(endpoints) > 0 {
				rs.TunnelEndpoints = endpoints
			}
			return willTunnel
		}
	}
	return false
}

func HijackConnect(r *http.Request, w http.ResponseWriter) bool {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if hijacker, ok := w.(http.Hijacker); ok {
		if clientConn, _, err := hijacker.Hijack(); err == nil {
			rs := util.GetRequestStore(r)
			return GetOrCreatePortTunnel(util.GetRequestOrListenerPortNum(r)).
				openProxyTunnel(r.RemoteAddr, r.Host, rs.IsH2, rs.IsH2C, rs.IsTLS, clientConn)
		}
	}
	return false
}

func copy(dest io.WriteCloser, source io.ReadCloser, wg *sync.WaitGroup) {
	wg.Add(1)
	defer dest.Close()
	defer source.Close()
	io.Copy(dest, source)
	wg.Done()
}

func RegisterPipeCallback(tunnel, pipe string, callback PipeCallback) {
	tunnelLock.Lock()
	defer tunnelLock.Unlock()
	if pipeCallbacksByTunnels[tunnel] == nil {
		pipeCallbacksByTunnels[tunnel] = map[string]PipeCallback{}
	}
	pipeCallbacksByTunnels[tunnel][pipe] = callback
}

func (pt *PortTunnel) init() *PortTunnel {
	pt.Tunnels = map[string]*Endpoint{}
	pt.ProxyTunnels = map[string]*ProxyTunnel{}
	pt.BroadTunnels = []string{}
	pt.URITunnels = map[string][]string{}
	pt.HeaderTunnels = map[string]map[string][]string{}
	pt.URIHeaderTunnels = map[string]map[string]map[string][]string{}
	pt.initTracking()
	return pt
}

func (pt *PortTunnel) initTracking() {
	pt.lock.Lock()
	defer pt.lock.Unlock()
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

func (pt *PortTunnel) checkTunnelsForRequest(r *http.Request) (willTunnel bool, endpoints []string) {
	uri := r.RequestURI
	if strings.HasPrefix(r.RequestURI, "/tunnel=") || r.Header.Get(HeaderGotoTunnel) != "" || r.Header.Get(HeaderProxyConnection) != "" {
		return true, nil
	}

	if len(pt.URIHeaderTunnels) == 0 && len(pt.URITunnels) == 0 &&
		len(pt.HeaderTunnels) == 0 && len(pt.BroadTunnels) == 0 {
		return false, nil
	}

	checkHeaders := func(headersEndpointMap map[string]map[string][]string, r *http.Request) {
		for h, hMap := range headersEndpointMap {
			if hv := r.Header.Get(h); hv != "" {
				endpoints = hMap[hv]
				if len(endpoints) > 0 {
					break
				}
			}
			if len(endpoints) == 0 {
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
	return len(endpoints) > 0, endpoints
}

func (pt *PortTunnel) openProxyTunnel(fromAddress, toAddress string, isH2, isH2C, isTLS bool, clientConn net.Conn) bool {
	selfAddress := fmt.Sprintf("%s:%d", global.Self.PodIP, pt.Port)
	if selfConn, err := net.DialTimeout("tcp", selfAddress, 10*time.Second); err == nil {
		selfAddress = selfConn.LocalAddr().String()
		pt.lock.Lock()
		pt.ProxyTunnels[selfAddress] = &ProxyTunnel{FromAddress: fromAddress, ToAddress: toAddress, IsH2: isH2, IsH2C: isH2C, IsTLS: isTLS}
		pt.lock.Unlock()
		wg := &sync.WaitGroup{}
		go copy(selfConn, clientConn, wg)
		go copy(clientConn, selfConn, wg)
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
		pt.URIHeaderTunnels[uri][header][value] = append(pt.URIHeaderTunnels[uri][header][value], ep.ID)
	} else if uri != "" {
		pt.URITunnels[uri] = append(pt.URITunnels[uri], ep.ID)
	} else if header != "" {
		if pt.HeaderTunnels[header] == nil {
			pt.HeaderTunnels[header] = map[string][]string{}
		}
		pt.HeaderTunnels[header][value] = append(pt.HeaderTunnels[header][value], ep.ID)
	} else {
		pt.BroadTunnels = append(pt.BroadTunnels, ep.ID)
	}
}

func (pt *PortTunnel) removeTunnel(endpoint, uri, header, value string) (exists bool) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if uri != "" && header != "" {
		if pt.URIHeaderTunnels[uri] != nil {
			if pt.URIHeaderTunnels[uri][header] != nil {
				hvEndpoints := pt.URIHeaderTunnels[uri][header][value]
				if len(hvEndpoints) > 0 {
					if endpoint != "" {
						for i, ep := range hvEndpoints {
							if ep == endpoint {
								exists = true
								hvEndpoints = append(hvEndpoints[:i], hvEndpoints[i+1:]...)
							}
						}
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
				for i, ep := range pt.URITunnels[uri] {
					if ep == endpoint {
						exists = true
						pt.URITunnels[uri] = append(pt.URITunnels[uri][:i], pt.URITunnels[uri][i+1:]...)
					}
				}
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
						for i, ep := range hvEndpoints {
							if ep == endpoint {
								exists = true
								hvEndpoints = append(hvEndpoints[:i], hvEndpoints[i+1:]...)
							}
						}
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
			for i, ep := range pt.BroadTunnels {
				if ep == endpoint {
					exists = true
					pt.BroadTunnels = append(pt.BroadTunnels[:i], pt.BroadTunnels[i+1:]...)
				}
			}
		} else {
			pt.BroadTunnels = []string{}
			exists = true
		}
	}
	return
}

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

func (pt *PortTunnel) clear() {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.init()
}

func (pt *PortTunnel) captureTunnelTraffic(flag bool) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.CaptureTrafficLog = flag
}

func (pt *PortTunnel) trackRequest(r *http.Request, ep *Endpoint) {
	uri := strings.ToLower(r.RequestURI)
	c := pt.Traffic
	util.UpdateTrackingCountsByURIAndID(ep.ID, uri, c.ByEndpoints, c.ByURIs, c.ByURIEndpoints)
	util.UpdateTrackingCountsByURIKeyValuesID(ep.ID, uri, pt.TunnelTrackingHeaders, r.Header,
		c.ByRequestHeaders, c.ByRequestHeaderValues, c.ByURIRequestHeaders, c.ByRequestHeaderEndpoints)
	util.UpdateTrackingCountsByURIKeyValuesID(ep.ID, uri, pt.TunnelTrackingQueries, r.URL.Query(),
		c.ByRequestQueries, c.ByRequestQueryValues, c.ByURIRequestQueries, c.ByRequestQueryEndpoints)
}

func (pt *PortTunnel) trackResponse(uri string, r *http.Response, ep *Endpoint) {
	c := pt.Traffic

	c.ByResponseStatus[r.StatusCode]++
	if c.ByURIResponseStatus[uri] == nil {
		c.ByURIResponseStatus[uri] = map[int]int{}
	}
	c.ByURIResponseStatus[uri][r.StatusCode]++

	util.UpdateTrackingCountsByURIKeyValuesID(ep.ID, uri, pt.TunnelTrackingHeaders, r.Header,
		c.ByResponseHeaders, c.ByResponseHeaderValues, c.ByURIResponseHeaders, c.ByResponseHeaderEndpoints)
}

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

func (pt *PortTunnel) tunnelToEndpoint(ep *Endpoint, uri, tunnelHostLabel, viaTunnelLabel string, r *http.Request, w http.ResponseWriter, wg *sync.WaitGroup, trafficLog *TunnelTrafficLog) {
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
	util.AddLogMessage(msg, r)
	reqHeaders := http.Header{}
	reqHeaders[HeaderGotoViaTunnelCount] = []string{strconv.Itoa(rs.TunnelCount)}
	if !ep.Transparent {
		reqHeaders[HeaderGotoTunnelHost] = append(r.Header[HeaderGotoTunnelHost], tunnelHostLabel)
		reqHeaders[HeaderViaGotoTunnel] = append(r.Header[HeaderViaGotoTunnel], viaTunnelLabel)
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
	if req, err := transport.CreateRequest(method, url, reqHeaders, nil, r.Body); err == nil {
		req.Host = ep.Host
		if ep.client == nil {
			if global.Debug {
				log.Printf("Tunneling URI [%s] to endpoint [%s] - Creating HTTP Client\n", uri, ep.ID)
			}
			ep.client = transport.CreateHTTPClient(fmt.Sprintf("Tunnel[%s]", ep.Address), isH2, false, isTLS,
				ep.Host, rs.TLSVersionNum, 30*time.Second, 30*time.Second, 3*time.Minute, metrics.ConnTracker)
		} else {
			ep.client.UpdateTLSConfig(rs.ServerName, rs.TLSVersionNum)
		}
		if global.Debug {
			log.Printf("Tunneling URI [%s] to endpoint [%s] - Sending Request\n", uri, ep.ID)
		}
		if resp, err := ep.client.HTTP().Do(req); err == nil {
			r.Body.Close()
			rr := util.CreateOrGetReReader(resp.Body)
			resp.Body = rr
			msg = fmt.Sprintf("Got response from tunnel [%s]: %s", url, resp.Status)
			if global.Debug {
				log.Println(msg)
			}
			util.AddLogMessage(msg, r)
			pt.trackResponse(uri, resp, ep)
			ep.sendTunnelResponse(viaTunnelLabel, uri, r, w, resp)
			if trafficLog != nil {
				trafficLog.ResponseHeaders = append(trafficLog.ResponseHeaders, resp.Header)
				trafficLog.ResponsePayloadLengths = append(trafficLog.ResponsePayloadLengths, len(rr.Content))
				trafficLog.ResponseProtocol = util.GotoProtocol(ep.IsH2, ep.IsTLS)
			}
		} else {
			msg := fmt.Sprintf("Error invoking tunnel to [%s]: %s", url, err.Error())
			if global.Debug {
				log.Println(msg)
			}
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	} else {
		msg := fmt.Sprintf("Error creating tunnel request for [%s]: %s", url, err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
	wg.Done()
}

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
				for _, ep := range eps {
					endpoints[ep] = pt.Tunnels[ep]
				}
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
	if pt.CaptureTrafficLog {
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
	if trafficLog != nil {
		pt.Traffic.Logs = append(pt.Traffic.Logs, trafficLog)
	}
	if global.Debug {
		log.Printf("Tunnel: Exit [%s] - %+v\n", uri, addresses)
	}
}

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

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if util.IsTunnelRequest(r) {
			l := listeners.GetCurrentListener(r)
			for _, hv := range r.Header[HeaderGotoTunnelHost] {
				if strings.EqualFold(hv, l.HostLabel) {
					msg := fmt.Sprintf("Tunnel loop detected in header [%s] : %+v", HeaderGotoTunnelHost, r.Header[HeaderGotoTunnelHost])
					util.AddLogMessage(msg, r)
					fmt.Fprintln(w, msg)
					w.Header().Add(HeaderViaGoto, l.Label+"[tunnel loop]")
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
