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

  "github.com/gorilla/mux"
)

type Endpoint struct {
  URL             string
  Address         string
  Host            string
  Port            int
  Transparent     bool
  isTLS           bool
  isH2            bool
  useRequestProto bool
  client          *util.ClientTracker
}

type PortTunnel struct {
  Endpoints map[string]*Endpoint
  lock      sync.RWMutex
}

var (
  tunnels            = map[int]*PortTunnel{}
  tunnelLock         sync.RWMutex
  tunnelRegexp       = regexp.MustCompile("(?i)tunnel")
  Handler            = util.ServerHandler{"tunnel", SetRoutes, Middleware}
  TunnelCountHandler = util.ServerHandler{Name: "tunnelCount", Middleware: TunnelCountMiddleware}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  root.PathPrefix("/tunnel={address}").Subrouter().MatcherFunc(func(*http.Request, *mux.RouteMatch) bool { return true }).HandlerFunc(tunnel)
  tunnelRouter := util.PathRouter(r, "/tunnels")
  util.AddRouteWithPort(tunnelRouter, "/add/{address}/transparent", addTunnel, "POST", "PUT")
  util.AddRouteWithPort(tunnelRouter, "/add/{address}", addTunnel, "POST", "PUT")
  util.AddRouteWithPort(tunnelRouter, "/remove/{address}", clearTunnel, "POST", "PUT")
  util.AddRouteWithPort(tunnelRouter, "/clear", clearTunnel, "POST", "PUT")
  util.AddRouteWithPort(tunnelRouter, "", getTunnels, "GET")
}

func NewEndpoint(address string, tls, transparent bool) *Endpoint {
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
    } else {
      proto = "http://"
    }
  }
  return &Endpoint{URL: proto + address, Address: address, Host: pieces[0], Port: port, isTLS: tls, isH2: h2, useRequestProto: useRequestProto, Transparent: transparent}
}

func HasTunnel(r *http.Request, rm *mux.RouteMatch) bool {
  tunnelLock.Lock()
  defer tunnelLock.Unlock()
  port := util.GetListenerPortNum(r)
  return tunnels[port] != nil && !tunnels[port].IsEmpty()
}

func GetOrCreatePortTunnel(port int) *PortTunnel {
  tunnelLock.Lock()
  defer tunnelLock.Unlock()
  if tunnels[port] == nil {
    tunnels[port] = &PortTunnel{Endpoints: map[string]*Endpoint{}}
  }
  return tunnels[port]
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

func (ep *Endpoint) tunnel(uri, tunnelHostLabel, viaTunnelLabel string, r *http.Request, w http.ResponseWriter, wg *sync.WaitGroup) {
  url := ep.URL + uri
  rs := util.GetRequestStore(r)
  isH2 := ep.isH2
  isTLS := ep.isTLS
  if ep.useRequestProto {
    isH2 = util.IsH2(r)
    isTLS = r.TLS != nil
  }
  msg := fmt.Sprintf("Tunnel Count [%d]. Tunneling to [%s]", rs.TunnelCount, url)
  // fmt.Println(msg)
  util.AddLogMessage(msg, r)
  reqHeaders := http.Header{}
  hasMoreTunnels := len(r.Header[HeaderGotoTunnel]) > 0
  if hasMoreTunnels {
    reqHeaders[HeaderGotoViaTunnelCount] = []string{strconv.Itoa(rs.TunnelCount)}
  }
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
      // fmt.Println(msg)
      util.AddLogMessage(msg, r)
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

func (pt *PortTunnel) IsEmpty() bool {
  return len(pt.Endpoints) == 0
}

func (pt *PortTunnel) addEndpoint(address string, tls, transparent bool) {
  ep := NewEndpoint(address, tls, transparent)
  pt.lock.Lock()
  defer pt.lock.Unlock()
  pt.Endpoints[ep.Address] = ep
}

func (pt *PortTunnel) removeEndpoint(address string) {
  if strings.HasPrefix(address, "http") {
    address = strings.Join(strings.Split(address, ":")[1:], ":")
  }
  pt.lock.Lock()
  defer pt.lock.Unlock()
  if pt.Endpoints[address] != nil {
    if pt.Endpoints[address].client != nil {
      pt.Endpoints[address].client.CloseIdleConnections()
    }
    delete(pt.Endpoints, address)
  }
}

func (pt *PortTunnel) clear() {
  pt.lock.Lock()
  defer pt.lock.Unlock()
  pt.Endpoints = map[string]*Endpoint{}
}

func (pt *PortTunnel) tunnel(addresses []string, uri string, r *http.Request, w http.ResponseWriter) {
  metrics.UpdateRequestCount("tunnel")
  endpoints := pt.Endpoints
  if len(addresses) > 0 {
    endpoints = map[string]*Endpoint{}
    for _, a := range addresses {
      endpoints[a] = NewEndpoint(a, r.TLS != nil, true)
    }
  }
  l := listeners.GetCurrentListener(r)
  tunnelCount := len(r.Header[HeaderGotoTunnelHost]) + 1
  tunnelHostLabel := fmt.Sprintf("%s|%d", l.HostLabel, tunnelCount)
  viaTunnelLabel := fmt.Sprintf("%s|%d", l.Label, tunnelCount)
  wg := &sync.WaitGroup{}
  wg.Add(len(endpoints))
  for _, ep := range endpoints {
    go ep.tunnel(uri, tunnelHostLabel, viaTunnelLabel, r, w, wg)
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

func addTunnel(w http.ResponseWriter, r *http.Request) {
  address := util.GetStringParamValue(r, "address")
  transparent := strings.HasSuffix(r.RequestURI, "transparent")
  port := util.GetRequestOrListenerPortNum(r)
  GetOrCreatePortTunnel(port).addEndpoint(address, r.TLS != nil, transparent)
  msg := fmt.Sprintf("Tunnel added on port [%d] to [%s]", port, address)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func clearTunnel(w http.ResponseWriter, r *http.Request) {
  address := util.GetStringParamValue(r, "address")
  port := util.GetRequestOrListenerPortNum(r)
  pt := GetOrCreatePortTunnel(port)
  msg := ""
  if address != "" {
    pt.removeEndpoint(address)
    msg = fmt.Sprintf("Tunnel removed on port [%d] to [%s]", port, address)
  } else {
    pt.clear()
    msg = fmt.Sprintf("Tunnels cleared on port [%d]", port)
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func getTunnels(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, tunnels)
  util.AddLogMessage("Tunnels reported", r)
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
