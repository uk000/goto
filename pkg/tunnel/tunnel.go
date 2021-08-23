/**
 * Copyright 2021 uk
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
  "goto/pkg/metrics"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "io"
  "io/ioutil"
  "net/http"
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
  client          *util.ClientTracker
}

type TunnelCounts struct {
  ByEndpoints               map[string]int            `json:"ByEndpoints"`
  ByURIs                    map[string]int            `json:"ByURIs"`
  ByURIEndpoints            map[string]map[string]int `json:"ByURIEndpoints"`
  ByRequestHeaders          map[string]int            `json:"ByRequestHeaders"`
  ByRequestHeaderValues     map[string]map[string]int `json:"ByRequestHeaderValues"`
  ByRequestHeaderEndpoints  map[string]map[string]int `json:"ByRequestHeaderEndpoints"`
  ByURIRequestHeaders       map[string]map[string]int `json:"ByURIRequestHeaders"`
  ByResponseHeaders         map[string]int            `json:"ByResponseHeaders"`
  ByResponseHeaderValues    map[string]map[string]int `json:"ByResponseHeaderValues"`
  ByResponseHeaderEndpoints map[string]map[string]int `json:"ByResponseHeaderEndpoints"`
  ByURIResponseHeaders      map[string]map[string]int `json:"ByURIResponseHeaders"`
}

type PortTunnel struct {
  Port                  int
  BroadTunnels          map[string]*Endpoint
  URITunnels            map[string]map[string]*Endpoint
  HeaderTunnels         map[string]map[string]map[string]*Endpoint
  URIHeaderTunnels      map[string]map[string]map[string]map[string]*Endpoint
  TunnelTrackingHeaders map[string][]string
  TunnelTrackingQueries map[string][]string
  Counts                *TunnelCounts
  lock                  sync.RWMutex
}

var (
  tunnels            = map[int]*PortTunnel{}
  tunnelLock         sync.RWMutex
  tunnelRegexp       = regexp.MustCompile("(?i)tunnel")
  TunnelCountHandler = util.ServerHandler{Name: "tunnelCount", Middleware: TunnelCountMiddleware}
)

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

func WillTunnel(r *http.Request, rs *util.RequestStore) bool {
  tunnelLock.Lock()
  defer tunnelLock.Unlock()
  port := util.GetListenerPortNum(r)
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

func (pt *PortTunnel) init() *PortTunnel {
  pt.BroadTunnels = map[string]*Endpoint{}
  pt.URITunnels = map[string]map[string]*Endpoint{}
  pt.HeaderTunnels = map[string]map[string]map[string]*Endpoint{}
  pt.URIHeaderTunnels = map[string]map[string]map[string]map[string]*Endpoint{}
  pt.TunnelTrackingHeaders = map[string][]string{}
  pt.TunnelTrackingQueries = map[string][]string{}
  pt.Counts = &TunnelCounts{
    ByEndpoints:               map[string]int{},
    ByURIs:                    map[string]int{},
    ByURIEndpoints:            map[string]map[string]int{},
    ByRequestHeaders:          map[string]int{},
    ByRequestHeaderValues:     map[string]map[string]int{},
    ByRequestHeaderEndpoints:  map[string]map[string]int{},
    ByURIRequestHeaders:       map[string]map[string]int{},
    ByResponseHeaders:         map[string]int{},
    ByResponseHeaderValues:    map[string]map[string]int{},
    ByResponseHeaderEndpoints: map[string]map[string]int{},
    ByURIResponseHeaders:      map[string]map[string]int{},
  }
  return pt
}

func (pt *PortTunnel) checkTunnelsForRequest(r *http.Request) (willTunnel bool, endpoints map[string]*Endpoint) {
  uri := r.RequestURI

  if strings.HasPrefix(r.RequestURI, "/tunnel=") || r.Header.Get(HeaderGotoTunnel) != "" {
    return true, nil
  }

  if len(pt.URIHeaderTunnels) == 0 && len(pt.URITunnels) == 0 &&
    len(pt.HeaderTunnels) == 0 && len(pt.BroadTunnels) == 0 {
    return false, nil
  }

  checkHeaders := func(headersEndpointMap map[string]map[string]map[string]*Endpoint, r *http.Request) {
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

func (pt *PortTunnel) addTunnel(endpoint string, tls, transparent bool, uri, header, value string) {
  ep := newEndpoint(endpoint, tls, transparent)
  pt.lock.Lock()
  defer pt.lock.Unlock()
  if uri != "" && header != "" {
    if pt.URIHeaderTunnels[uri] == nil {
      pt.URIHeaderTunnels[uri] = map[string]map[string]map[string]*Endpoint{}
    }
    if pt.URIHeaderTunnels[uri][header] == nil {
      pt.URIHeaderTunnels[uri][header] = map[string]map[string]*Endpoint{}
    }
    if pt.URIHeaderTunnels[uri][header][value] == nil {
      pt.URIHeaderTunnels[uri][header][value] = map[string]*Endpoint{}
    }
    pt.URIHeaderTunnels[uri][header][value][endpoint] = ep
  } else if uri != "" {
    if pt.URITunnels[uri] == nil {
      pt.URITunnels[uri] = map[string]*Endpoint{}
    }
    pt.URITunnels[uri][endpoint] = ep
  } else if header != "" {
    if pt.HeaderTunnels[header] == nil {
      pt.HeaderTunnels[header] = map[string]map[string]*Endpoint{}
    }
    if pt.HeaderTunnels[header][value] == nil {
      pt.HeaderTunnels[header][value] = map[string]*Endpoint{}
    }
    pt.HeaderTunnels[header][value][endpoint] = ep
  } else {
    pt.BroadTunnels[endpoint] = ep
  }
}

func (pt *PortTunnel) removeTunnel(endpoint, uri, header, value string) (exists bool) {
  pt.lock.Lock()
  defer pt.lock.Unlock()
  if uri != "" && header != "" {
    if pt.URIHeaderTunnels[uri] != nil {
      if pt.URIHeaderTunnels[uri][header] != nil {
        if pt.URIHeaderTunnels[uri][header][value] != nil {
          if endpoint != "" {
            if pt.URIHeaderTunnels[uri][header][value][endpoint] != nil {
              delete(pt.URIHeaderTunnels[uri][header][value], endpoint)
              exists = true
            }
            if len(pt.URIHeaderTunnels[uri][header][value]) == 0 {
              delete(pt.URIHeaderTunnels[uri][header], value)
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
        if pt.URITunnels[uri][endpoint] != nil {
          delete(pt.URITunnels[uri], endpoint)
          exists = true
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
        if endpoint != "" {
          if pt.HeaderTunnels[header][value][endpoint] != nil {
            delete(pt.HeaderTunnels[header][value], endpoint)
            exists = true
          }
          if len(pt.HeaderTunnels[header][value]) == 0 {
            delete(pt.HeaderTunnels[header], value)
          }
        } else {
          delete(pt.HeaderTunnels[header], value)
          exists = true
        }
      }
      if len(pt.HeaderTunnels[header]) == 0 {
        delete(pt.HeaderTunnels, header)
      }
    }
  } else {
    if endpoint != "" {
      if pt.BroadTunnels[endpoint] != nil {
        delete(pt.BroadTunnels, endpoint)
        exists = true
      }
    } else {
      pt.BroadTunnels = map[string]*Endpoint{}
      exists = true
    }
  }
  return
}

func (pt *PortTunnel) addTracking(endpoint string, headers, queryParams []string) {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  for _, h := range headers {
    pt.TunnelTrackingHeaders[endpoint] = append(pt.TunnelTrackingHeaders[endpoint], h)
  }
  for _, q := range queryParams {
    pt.TunnelTrackingQueries[endpoint] = append(pt.TunnelTrackingQueries[endpoint], q)
  }
}

func (pt *PortTunnel) clearTracking(endpoint string) {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  delete(pt.TunnelTrackingHeaders, endpoint)
  delete(pt.TunnelTrackingQueries, endpoint)
}

func (pt *PortTunnel) clear() {
  pt.lock.Lock()
  defer pt.lock.Unlock()
  pt.init()
}

func (pt *PortTunnel) trackRequest(r *http.Request, ep *Endpoint) {
  c := pt.Counts
  c.ByEndpoints[ep.ID]++

  uri := strings.ToLower(r.RequestURI)
  c.ByURIs[uri]++

  if c.ByURIEndpoints[uri] == nil {
    c.ByURIEndpoints[uri] = map[string]int{}
  }
  c.ByURIEndpoints[uri][ep.ID]++

  if trackingHeaders := pt.TunnelTrackingHeaders[ep.ID]; trackingHeaders != nil {
    for _, h := range trackingHeaders {
      if hv := r.Header.Get(h); hv != "" {
        c.ByRequestHeaders[h]++
        if c.ByRequestHeaderValues[h] == nil {
          c.ByRequestHeaderValues[h] = map[string]int{}
        }
        c.ByRequestHeaderValues[h][hv]++
        if c.ByRequestHeaderEndpoints[h] == nil {
          c.ByRequestHeaderEndpoints[h] = map[string]int{}
        }
        c.ByRequestHeaderEndpoints[h][ep.ID]++
        if c.ByURIRequestHeaders[uri] == nil {
          c.ByURIRequestHeaders[uri] = map[string]int{}
        }
        c.ByURIRequestHeaders[uri][h]++
      }
    }
  }
}

func (pt *PortTunnel) trackResponse(uri string, r *http.Response, ep *Endpoint) {
  c := pt.Counts
  if trackingHeaders := pt.TunnelTrackingHeaders[ep.ID]; trackingHeaders != nil {
    for _, h := range trackingHeaders {
      if hv := r.Header.Get(h); hv != "" {
        c.ByResponseHeaders[h]++
        if c.ByResponseHeaderValues[h] == nil {
          c.ByResponseHeaderValues[h] = map[string]int{}
        }
        c.ByResponseHeaderValues[h][hv]++
        if c.ByResponseHeaderEndpoints[h] == nil {
          c.ByResponseHeaderEndpoints[h] = map[string]int{}
        }
        c.ByResponseHeaderEndpoints[h][ep.ID]++
        if c.ByURIResponseHeaders[uri] == nil {
          c.ByURIResponseHeaders[uri] = map[string]int{}
        }
        c.ByURIResponseHeaders[uri][h]++
      }
    }
  }
}

func (ep *Endpoint) sendTunnelResponse(viaTunnelLabel, uri string, r *http.Request, w http.ResponseWriter, resp *http.Response) {
  defer resp.Body.Close()
  rs := util.GetRequestStore(r)
  rs.TunnelLock.Lock()
  if !rs.IsTunnelResponseSent {
    io.Copy(w, resp.Body)
    util.CopyHeaders(viaTunnelLabel, r, w, r.Header, true, true, true)
    util.CopyHeaders("", r, w, resp.Header, false, false, true)
    w.WriteHeader(resp.StatusCode)
    rs.IsTunnelResponseSent = true
  } else {
    io.Copy(ioutil.Discard, resp.Body)
  }
  rs.TunnelLock.Unlock()
}

func (pt *PortTunnel) tunnelToEndpoint(ep *Endpoint, uri, tunnelHostLabel, viaTunnelLabel string, r *http.Request, w http.ResponseWriter, wg *sync.WaitGroup) {
  url := ep.URL + uri
  rs := util.GetRequestStore(r)
  isH2 := ep.IsH2
  isTLS := ep.IsTLS
  if ep.UseRequestProto {
    isH2 = util.IsH2(r)
    isTLS = r.TLS != nil
  }
  msg := fmt.Sprintf("Tunnel Count [%d]. Tunneling to [%s]", rs.TunnelCount, url)
  util.AddLogMessage(msg, r)
  reqHeaders := http.Header{}
  reqHeaders[HeaderGotoViaTunnelCount] = []string{strconv.Itoa(rs.TunnelCount)}
  if !ep.Transparent {
    reqHeaders[HeaderGotoTunnelHost] = append(r.Header[HeaderGotoTunnelHost], tunnelHostLabel)
    reqHeaders[HeaderViaGotoTunnel] = append(r.Header[HeaderViaGotoTunnel], viaTunnelLabel)
  }
  for h, hv := range r.Header {
    if ep.Transparent && tunnelRegexp.MatchString(h) && !strings.EqualFold(h, HeaderGotoTunnel) {
      continue
    }
    reqHeaders[h] = hv
  }
  if req, err := util.CreateRequest(r.Method, url, reqHeaders, nil, r.Body); err == nil {
    req.Host = ep.Host
    if ep.client == nil {
      ep.client = util.CreateHTTPClient(fmt.Sprintf("Tunnel[%s]", ep.Address), isH2, false, isTLS,
        ep.Host, rs.TLSVersionNum, 30*time.Second, 30*time.Second, 3*time.Minute, metrics.ConnTracker)
    } else {
      ep.client.UpdateTLSConfig(rs.ServerName, rs.TLSVersionNum)
    }
    if resp, err := ep.client.Do(req); err == nil {
      msg = fmt.Sprintf("Got response from tunnel [%s]: %s", url, resp.Status)
      util.AddLogMessage(msg, r)
      pt.trackResponse(uri, resp, ep)
      ep.sendTunnelResponse(viaTunnelLabel, uri, r, w, resp)
    } else {
      msg := fmt.Sprintf("Error invoking tunnel to [%s]: %s", url, err.Error())
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
  metrics.UpdateRequestCount("tunnel")
  var endpoints map[string]*Endpoint
  if len(addresses) > 0 {
    endpoints = map[string]*Endpoint{}
    for _, a := range addresses {
      endpoints[a] = newEndpoint(a, r.TLS != nil, true)
    }
  } else {
    if e := util.GetTunnelEndpoints(r); e != nil {
      if eps, ok := e.(map[string]*Endpoint); ok {
        endpoints = eps
      }
    }
  }
  if len(endpoints) == 0 {
    return
  }
  l := listeners.GetCurrentListener(r)
  tunnelCount := util.GetTunnelCount(r)
  tunnelHostLabel := fmt.Sprintf("%s|%d", l.HostLabel, tunnelCount)
  viaTunnelLabel := fmt.Sprintf("%s|%d", l.Label, tunnelCount)
  wg := &sync.WaitGroup{}
  wg.Add(len(endpoints))
  for _, ep := range endpoints {
    pt.trackRequest(r, ep)
    go pt.tunnelToEndpoint(ep, uri, tunnelHostLabel, viaTunnelLabel, r, w, wg)
  }
  wg.Wait()
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
    r.Header[HeaderGotoRequestedTunnel] = r.Header[HeaderGotoTunnel]
    addresses = r.Header[HeaderGotoTunnel]
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
  tunnel := GetOrCreatePortTunnel(util.GetListenerPortNum(r))
  tunnel.tunnel(addresses, uri, r, w)
}

func Middleware(next http.Handler) http.Handler {
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

func TunnelCountMiddleware(next http.Handler) http.Handler {
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
