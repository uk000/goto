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
  "strconv"
  "strings"
  "sync"
  "time"

  "github.com/gorilla/mux"
)

type Endpoint struct {
  URL         string
  Address     string
  Host        string
  Port        int
  Transparent bool
  isTLS       bool
  client      *util.ClientTracker
}

type PortTunnel struct {
  Endpoints map[string]*Endpoint
  lock      sync.RWMutex
}

var (
  tunnels    = map[int]*PortTunnel{}
  tunnelLock sync.RWMutex
  Handler    = util.ServerHandler{"tunnel", SetRoutes, Middleware}
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
  pieces := strings.Split(address, ":")
  if len(pieces) > 1 {
    port, _ = strconv.Atoi(pieces[len(pieces)-1])
  }
  if strings.HasPrefix(address, "http") {
    proto = pieces[0] + "://"
    if len(pieces) > 1 {
      pieces = pieces[1:]
      address = strings.Join(pieces, ":")
    }
    tls = strings.HasPrefix(address, "https")
  } else {
    if tls {
      proto = "https://"
    } else {
      proto = "http://"
    }
  }
  return &Endpoint{URL: proto + address, Address: address, Host: pieces[0], Port: port, isTLS: tls, Transparent: transparent}
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

func (pt *PortTunnel) tunnel(address string, r *http.Request, w http.ResponseWriter) {
  metrics.UpdateRequestCount("tunnel")
  endpoints := pt.Endpoints
  uri := r.RequestURI
  if address != "" {
    endpoints = map[string]*Endpoint{"address": NewEndpoint(address, r.TLS != nil, false)}
    if pieces := strings.Split(uri, address); len(pieces) > 1 {
      uri = pieces[1]
    }
  }
  l := listeners.GetCurrentListener(r)
  tunnelCount := len(r.Header[HeaderGotoHostTunnel]) + 1
  tunnelHostLabel := fmt.Sprintf("%s[%d]", l.HostLabel, tunnelCount)
  viaTunnelLabel := fmt.Sprintf("%s[%d]", l.Label, tunnelCount)
  wg := &sync.WaitGroup{}
  wg.Add(len(endpoints))
  for _, ep := range endpoints {
    go ep.tunnel(uri, tunnelHostLabel, viaTunnelLabel, r, w, wg)
  }
  wg.Wait()
}

func (ep *Endpoint) sendTunnelResponse(uri string, r *http.Request, w http.ResponseWriter, resp *http.Response) {
  defer resp.Body.Close()
  rs := util.GetRequestStore(r)
  rs.TunnelLock.Lock()
  if !rs.IsTunnelResponseSent {
    io.Copy(w, resp.Body)
    util.CopyHeaders("", w, resp.Header, "", "", true)
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
  isH2 := util.IsH2(r)
  util.AddLogMessage(fmt.Sprintf("Tunneled to [%s]", url), r)
  if !ep.Transparent {
    r.Header[HeaderGotoHostTunnel] = append(r.Header[HeaderGotoHostTunnel], tunnelHostLabel)
    r.Header[HeaderViaGotoTunnel] = append(r.Header[HeaderViaGotoTunnel], viaTunnelLabel)
  }
  if req, err := util.CreateRequest(r.Method, url, r.Header, nil, r.Body); err == nil {
    req.Host = r.Host
    if ep.client == nil {
      ep.client = util.CreateHTTPClient(fmt.Sprintf("Tunnel[%s]", ep.Address), isH2, false, ep.isTLS,
        rs.ServerName, rs.TLSVersionNum, 30*time.Second, 30*time.Second, 3*time.Minute, metrics.ConnTracker)
    } else {
      ep.client.UpdateTLSConfig(rs.ServerName, rs.TLSVersionNum)
    }
    if resp, err := ep.client.Do(req); err == nil {
      fmt.Printf("Got response from tunnel [%s]: %s\n", url, resp.Status)
      ep.sendTunnelResponse(uri, r, w, resp)
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

func tunnel(w http.ResponseWriter, r *http.Request) {
  address := util.GetStringParamValue(r, "address")
  tunnel := GetOrCreatePortTunnel(util.GetListenerPortNum(r))
  tunnel.tunnel(address, r, w)
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
      for _, hv := range r.Header[HeaderGotoHostTunnel] {
        if strings.EqualFold(hv, l.HostLabel) {
          msg := fmt.Sprintf("Tunnel loop detected in header [%s] : %+v", HeaderGotoHostTunnel, r.Header[HeaderGotoHostTunnel])
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
