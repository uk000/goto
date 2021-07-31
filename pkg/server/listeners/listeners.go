package listeners

import (
  "crypto/tls"
  "crypto/x509"
  "fmt"
  "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/server/tcp"
  "goto/pkg/util"
  "log"
  "net"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "github.com/gorilla/mux"
)

type Listener struct {
  ListenerID string           `json:"listenerID"`
  Label      string           `json:"label"`
  HostLabel  string           `json:"hostLabel"`
  Port       int              `json:"port"`
  Protocol   string           `json:"protocol"`
  Open       bool             `json:"open"`
  AutoCert   bool             `json:"autoCert"`
  CommonName string           `json:"commonName"`
  MutualTLS  bool             `json:"mutualTLS"`
  TLS        bool             `json:"tls"`
  TCP        *tcp.TCPConfig   `json:"tcp,omitempty"`
  Cert       *tls.Certificate `json:"-"`
  CACerts    *x509.CertPool   `json:"-"`
  RawCert    []byte           `json:"-"`
  RawKey     []byte           `json:"-"`
  isHTTP     bool             `json:"-"`
  isGRPC     bool             `json:"-"`
  isTCP      bool             `json:"-"`
  isUDP      bool             `json:"-"`
  Listener   net.Listener     `json:"-"`
  UDPConn    *net.UDPConn     `json:"-"`
  Restarted  bool             `json:"-"`
  Generation int              `json:"-"`
  lock       sync.RWMutex     `json:"-"`
}

var (
  DefaultListener     = &Listener{}
  listeners           = map[int]*Listener{}
  listenerGenerations = map[int]int{}
  initialListeners    = []*Listener{}
  ServeHTTPListener   func(*Listener)
  ServeGRPCListener   func(*Listener)
  StartTCPServer      func(string, int, net.Listener)
  DefaultLabel        string
  serverStarted       bool
  listenersLock       sync.RWMutex
  Handler             util.ServerHandler = util.ServerHandler{Name: "listeners", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  lRouter := r.PathPrefix("/listeners").Subrouter()
  util.AddRoute(lRouter, "/add", addListener, "POST", "PUT")
  util.AddRoute(lRouter, "/update", updateListener, "POST", "PUT")
  util.AddRoute(lRouter, "/{port}/cert/auto/{domain}", autoCert, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/cert/add", addListenerCert, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/key/add", addListenerKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/cert/remove", removeListenerCertAndKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/cert", getListenerCertOrKey, "GET")
  util.AddRoute(lRouter, "/{port}/ca/add", addListenerCACert, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/ca/clear", clearListenerCACerts, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/key", getListenerCertOrKey, "GET")
  util.AddRoute(lRouter, "/{port}/remove", removeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/open", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/reopen", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/close", closeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}", getListeners, "GET")
  util.AddRoute(lRouter, "", getListeners, "GET")
  global.IsListenerPresent = IsListenerPresent
  global.IsListenerOpen = IsListenerOpen
  global.GetListenerID = GetListenerID
  global.GetListenerLabel = GetListenerLabel
  global.GetListenerLabelForPort = GetListenerLabelForPort
  global.GetHostLabelForPort = GetHostLabelForPort
}

func Configure(hs func(*Listener), gs func(*Listener), ts func(string, int, net.Listener)) {
  if DefaultLabel == "" {
    DefaultLabel = util.GetHostLabel()
  }
  DefaultListener.Label = DefaultLabel
  DefaultListener.HostLabel = util.GetHostLabel()
  DefaultListener.Port = global.ServerPort
  DefaultListener.Protocol = "HTTP"
  DefaultListener.isHTTP = true
  DefaultListener.TLS = false
  DefaultListener.Open = true
  ServeHTTPListener = hs
  ServeGRPCListener = gs
  StartTCPServer = ts
}

func StartInitialListeners() {
  serverStarted = true
  time.Sleep(1 * time.Second)
  for _, l := range initialListeners {
    addOrUpdateListener(l, false)
  }
}

func AddInitialListeners(portList []string) {
  ports := map[int]bool{}
  for i, p := range portList {
    portInfo := strings.Split(p, "/")
    if port, err := strconv.Atoi(portInfo[0]); err == nil && port > 0 && port <= 65535 {
      if !ports[port] {
        protocol := "http"
        cn := constants.DefaultCommonName
        if len(portInfo) > 1 && portInfo[1] != "" {
          protocol = strings.ToLower(portInfo[1])
          if !strings.EqualFold(protocol, "http") && !strings.EqualFold(protocol, "https") &&
            !strings.EqualFold(protocol, "grpc") && !strings.EqualFold(protocol, "udp") && !strings.EqualFold(protocol, "tls") {
            protocol = "tcp"
          }
        }
        if len(portInfo) > 2 && portInfo[2] != "" {
          cn = strings.ToLower(portInfo[2])
        }
        ports[port] = true
        if i == 0 {
          global.ServerPort = port
        } else {
          listenersLock.Lock()
          l := &Listener{Port: port, Protocol: protocol, CommonName: cn, Open: true}
          initialListeners = append(initialListeners, l)
          listenersLock.Unlock()
        }
      } else {
        log.Fatalf("Error: Duplicate port [%d]\n", port)
      }
    } else {
      log.Fatalf("Error: Invalid port [%d]\n", port)
    }
  }
}

func (l *Listener) initListener() bool {
  l.lock.Lock()
  defer l.lock.Unlock()
  var tlsConfig *tls.Config
  if l.AutoCert {
    if l.CommonName == "" {
      l.CommonName = constants.DefaultCommonName
    }
    if cert, err := util.CreateCertificate(l.CommonName, fmt.Sprintf("%s-%d", l.Label, l.Port)); err == nil {
      l.Cert = cert
    }
  }
  if l.Cert != nil {
    tlsConfig = &tls.Config{
      Certificates: []tls.Certificate{*l.Cert},
    }
  } else if len(l.RawCert) > 0 && len(l.RawKey) > 0 {
    if x509Cert, err := tls.X509KeyPair(l.RawCert, l.RawKey); err == nil {
      tlsConfig = &tls.Config{
        Certificates: []tls.Certificate{x509Cert},
      }
    } else {
      log.Printf("Failed to parse certificate with error: %s\n", err.Error())
      return false
    }
  }
  address := fmt.Sprintf("0.0.0.0:%d", l.Port)
  if l.isUDP {
    if udpAddr, err := net.ResolveUDPAddr("udp4", address); err == nil {
      if udpConn, err := net.ListenUDP("udp", udpAddr); err == nil {
        l.UDPConn = udpConn
      } else {
        log.Printf("Failed to open UDP listener with error: %s\n", err.Error())
        return false
      }
    } else {
      log.Printf("Failed to resolve UDP address with error: %s\n", err.Error())
      return false
    }
  } else {
    if listener, err := net.Listen("tcp", address); err == nil {
      if tlsConfig != nil {
        if l.MutualTLS {
          tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
          tlsConfig.ClientCAs = l.CACerts
        } else {
          tlsConfig.ClientAuth = tls.NoClientCert
        }
        listener = tls.NewListener(listener, tlsConfig)
      }
      l.Listener = listener
      return true
    } else {
      log.Printf("Failed to open listener with error: %s\n", err.Error())
      return false
    }
  }
  log.Println("Failed to open listener with no error")
  return false
}

func (l *Listener) openListener() bool {
  if l.initListener() {
    l.lock.Lock()
    defer l.lock.Unlock()
    listenerGenerations[l.Port] = listenerGenerations[l.Port] + 1
    l.Generation = listenerGenerations[l.Port]
    l.ListenerID = fmt.Sprintf("%d-%d", l.Port, l.Generation)
    log.Printf("Opening [%s] listener [%s] on port [%d].", l.Protocol, l.ListenerID, l.Port)
    if l.isHTTP {
      ServeHTTPListener(l)
    } else if l.isGRPC {
      ServeGRPCListener(l)
    } else if l.isTCP {
      l.TCP.ListenerID = l.ListenerID
      StartTCPServer(l.ListenerID, l.Port, l.Listener)
    }
    l.Open = true
    l.TLS = l.isHTTP && (l.AutoCert || l.Cert != nil || len(l.RawCert) > 0 && len(l.RawKey) > 0)
    return true
  }
  return false
}

func (l *Listener) closeListener() {
  l.lock.Lock()
  defer l.lock.Unlock()
  if l.Listener != nil {
    l.Listener.Close()
    l.Listener = nil
  }
  l.Open = false
}

func (l *Listener) reopenListener() bool {
  listenersLock.RLock()
  old := listeners[l.Port]
  listenersLock.RUnlock()
  if old != nil {
    log.Printf("Closing old listener %s before reopening.", old.ListenerID)
    old.lock.Lock()
    old.Restarted = true
    old.lock.Unlock()
    old.closeListener()
  }
  for i := 0; i < 5; i++ {
    if l.openListener() {
      log.Printf("Reopened listener %s on port %d.", l.ListenerID, l.Port)
      return true
    } else {
      log.Printf("Couldn't reopen listener %s on port %d since previous listener is still running. Retrying...", l.ListenerID, l.Port)
      time.Sleep(5 * time.Second)
    }
  }
  return false
}

func validateListener(w http.ResponseWriter, r *http.Request) *Listener {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Port %d: No listener on the port, or listener not closeable\n", port)
    return nil
  }
  return l
}

func addListener(w http.ResponseWriter, r *http.Request) {
  addOrUpdateListenerAndRespond(w, r, false)
}

func updateListener(w http.ResponseWriter, r *http.Request) {
  addOrUpdateListenerAndRespond(w, r, true)
}

func addOrUpdateListenerAndRespond(w http.ResponseWriter, r *http.Request, update bool) {
  msg := ""
  l := &Listener{}
  body := util.Read(r.Body)
  if err := util.ReadJson(body, l); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
    events.SendRequestEventJSON("Listener Rejected", err.Error(),
      map[string]interface{}{"error": err.Error(), "payload": body}, r)
    util.AddLogMessage(msg, r)
    fmt.Fprintln(w, msg)
    return
  }
  errorCode := 0
  if errorCode, msg = addOrUpdateListener(l, update); errorCode > 0 {
    w.WriteHeader(errorCode)
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func addOrUpdateListener(l *Listener, update bool) (int, string) {
  msg := ""
  errorCode := 0
  if l.Label == "" {
    if global.PeerName != "" {
      l.Label = global.PeerName
    } else {
      l.Label = util.BuildListenerLabel(l.Port)
    }
  }
  l.HostLabel = util.BuildHostLabel(l.Port)
  l.Protocol = strings.ToLower(l.Protocol)
  if l.Port <= 0 || l.Port > 65535 {
    msg = fmt.Sprintf("[Invalid port number: %d]", l.Port)
  }
  isHTTPS := strings.EqualFold(l.Protocol, "https")
  if isHTTPS || strings.EqualFold(l.Protocol, "http") {
    l.isHTTP = true
    if isHTTPS {
      l.TLS = true
      if l.Cert == nil && l.RawCert == nil {
        l.AutoCert = true
      }
    }
  } else if strings.EqualFold(l.Protocol, "grpc") {
    l.isGRPC = true
  } else if strings.EqualFold(l.Protocol, "udp") {
    l.isUDP = true
  } else {
    l.isTCP = true
    l.TCP, msg = tcp.InitTCPConfig(l.Port, l.TCP)
  }
  if msg != "" {
    events.SendEventJSON("Listener Rejected", msg, l)
    return http.StatusBadRequest, msg
  }

  listenersLock.RLock()
  _, exists := listeners[l.Port]
  listenersLock.RUnlock()
  if exists {
    if update {
      if l.reopenListener() {
        listenersLock.Lock()
        listeners[l.Port] = l
        listenersLock.Unlock()
        msg = fmt.Sprintf("Listener %d already present, restarted.", l.Port)
        events.SendEventJSON("Listener Updated", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
      } else {
        errorCode = http.StatusInternalServerError
        msg = fmt.Sprintf("Listener %d already present, failed to restart.", l.Port)
        events.SendEventJSON("Listener Updated", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
      }
    } else {
      errorCode = http.StatusBadRequest
      msg = fmt.Sprintf("Listener %d already present, cannot add.", l.Port)
      events.SendEventJSON("Listener Rejected", msg, l)
    }
  } else {
    if l.Open {
      if l.openListener() {
        listenersLock.Lock()
        listeners[l.Port] = l
        listenersLock.Unlock()
        msg = fmt.Sprintf("Listener %d added and opened.", l.Port)
        events.SendEventJSON("Listener Added", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
      } else {
        errorCode = http.StatusInternalServerError
        msg = fmt.Sprintf("Listener %d added but failed to open.", l.Port)
        events.SendEventJSON("Listener Added", l.HostLabel, map[string]interface{}{"listener": l, "status": msg})
      }
    } else {
      listenersLock.Lock()
      listeners[l.Port] = l
      listenersLock.Unlock()
      msg = fmt.Sprintf("Listener %d added.", l.Port)
      events.SendEventJSON("Listener Added", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
    }
  }
  return errorCode, msg
}

func addListenerCertOrKey(w http.ResponseWriter, r *http.Request, cert bool) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    data := util.ReadBytes(r.Body)
    if len(data) > 0 {
      l.lock.Lock()
      defer l.lock.Unlock()
      if cert {
        l.RawCert = data
        msg = fmt.Sprintf("Cert added for listener %d\n", l.Port)
        events.SendRequestEvent("Listener Cert Added", msg, r)
      } else {
        l.RawKey = data
        msg = fmt.Sprintf("Key added for listener %d\n", l.Port)
        events.SendRequestEvent("Listener Key Added", msg, r)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "No payload"
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func addListenerCert(w http.ResponseWriter, r *http.Request) {
  addListenerCertOrKey(w, r, true)
}

func addListenerKey(w http.ResponseWriter, r *http.Request) {
  addListenerCertOrKey(w, r, false)
}

func removeListenerCertAndKey(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    l.lock.Lock()
    l.RawKey = nil
    l.RawCert = nil
    l.Cert = nil
    l.TLS = false
    l.lock.Unlock()
    if l.reopenListener() {
      msg = fmt.Sprintf("Cert and Key removed for listener %d, and reopened\n", l.Port)
    } else {
      w.WriteHeader(http.StatusInternalServerError)
      msg = fmt.Sprintf("Cert and Key removed for listener %d but failed to reopen\n", l.Port)
    }
    events.SendRequestEvent("Listener Cert Removed", msg, r)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func addListenerCACert(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    data := util.ReadBytes(r.Body)
    if len(data) > 0 {
      l.lock.Lock()
      if l.CACerts == nil {
        l.CACerts = x509.NewCertPool()
      }
      l.CACerts.AppendCertsFromPEM(data)
      l.lock.Unlock()
      events.SendRequestEvent("Listener CA Cert Added", msg, r)
      if l.reopenListener() {
        msg = fmt.Sprintf("CA Cert added for listener %d, and reopened\n", l.Port)
      } else {
        w.WriteHeader(http.StatusInternalServerError)
        msg = fmt.Sprintf("CA Cert added for listener %d but failed to reopen\n", l.Port)
      }

    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "No payload"
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func clearListenerCACerts(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    l.lock.Lock()
    l.CACerts = x509.NewCertPool()
    l.lock.Unlock()
    if l.reopenListener() {
      msg = fmt.Sprintf("CA Certs cleared for listener %d, and reopened\n", l.Port)
    } else {
      w.WriteHeader(http.StatusInternalServerError)
      msg = fmt.Sprintf("CA Certs cleared for listener %d but failed to reopen\n", l.Port)
    }
    events.SendRequestEvent("Listener CA Certs Cleared", msg, r)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func getListenerCertOrKey(w http.ResponseWriter, r *http.Request) {
  cert := strings.Contains(r.RequestURI, "cert")
  key := strings.Contains(r.RequestURI, "key")
  if l := validateListener(w, r); l != nil {
    msg := ""
    var err error
    if cert {
      raw := l.RawCert
      if raw == nil {
        raw, err = util.EncodeX509Cert(l.Cert)
      }
      if raw != nil {
        w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
        w.Header().Set("Content-Type", "application/octet-stream")
        w.Write(raw)
        msg = "Listener TLS cert served"
      } else if err != nil {
        msg = fmt.Sprintf("Failed to serve listener tls cert with error: %s", err.Error())
      } else {
        msg = "Failed to serve listener tls cert"
      }
    } else if key {
      raw := l.RawKey
      if raw == nil {
        raw, err = util.EncodeX509Key(l.Cert)
      }
      if raw != nil {
        w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
        w.Header().Set("Content-Type", "application/octet-stream")
        w.Write(raw)
        msg = "Listener TLS key served"
      } else if err != nil {
        msg = fmt.Sprintf("Failed to serve listener tls key with error: %s", err.Error())
      } else {
        msg = "Failed to serve listener tls key"
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = "Neither cert nor key requested"
      fmt.Fprintln(w, msg)
    }
    util.AddLogMessage(msg, r)
  }
}

func autoCert(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    if domain := util.GetStringParamValue(r, "domain"); domain != "" {
      if cert, err := util.CreateCertificate(domain, l.Label); err == nil {
        l.Cert = cert
        if l.reopenListener() {
          msg = fmt.Sprintf("Cert auto-generated for listener %d\n", l.Port)
          events.SendRequestEvent("Listener Cert Generated", msg, r)
        } else {
          msg = fmt.Sprintf("Failed to reopen listener %d for auto-generate cert\n", l.Port)
        }
      } else {
        msg = fmt.Sprintf("Failed to auto-generate cert for listener %d\n", l.Port)
        w.WriteHeader(http.StatusInternalServerError)
      }
    } else {
      msg = fmt.Sprintf("Missing domain for cert auto-generation for listener %d\n", l.Port)
      w.WriteHeader(http.StatusBadRequest)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func GetListeners() map[int]*Listener {
  listenersView := map[int]*Listener{}
  listenersView[DefaultListener.Port] = DefaultListener
  for port, l := range listeners {
    listenersView[port] = l
  }
  return listenersView
}

func getListeners(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port > 0 {
    util.WriteJsonPayload(w, GetListenerForPort(port))
  } else {
    util.WriteJsonPayload(w, GetListeners())
  }
}

func GetListenerForPort(port int) *Listener {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port == DefaultListener.Port {
    return DefaultListener
  }
  return listeners[port]
}

func GetListener(r *http.Request) *Listener {
  return GetListenerForPort(util.GetListenerPortNum(r))
}

func GetCurrentListener(r *http.Request) *Listener {
  l := GetListener(r)
  if l == nil {
    l = DefaultListener
  }
  return l
}

func IsListenerPresent(port int) bool {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  return listeners[port] != nil
}

func IsListenerOpen(port int) bool {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  l := listeners[port]
  return l != nil && l.Open
}

func GetListenerID(port int) string {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if l := listeners[port]; l != nil {
    return l.ListenerID
  }
  return ""
}

func GetListenerLabel(r *http.Request) string {
  return GetListenerLabelForPort(util.GetRequestOrListenerPortNum(r))
}

func GetListenerLabelForPort(port int) string {
  listenersLock.RLock()
  l := listeners[port]
  listenersLock.RUnlock()
  if l != nil {
    return l.Label
  } else if port == global.ServerPort {
    if DefaultListener.Label != "" {
      return DefaultListener.Label
    } else {
      return util.GetHostLabel()
    }
  }
  return util.BuildListenerLabel(port)
}

func GetHostLabelForPort(port int) string {
  listenersLock.RLock()
  l := listeners[port]
  listenersLock.RUnlock()
  if l != nil {
    return l.HostLabel
  } else if port == global.ServerPort {
    return util.GetHostLabel()
  }
  return util.BuildHostLabel(port)
}

func SetListenerLabel(r *http.Request) string {
  port := util.GetRequestOrListenerPortNum(r)
  label := util.GetStringParamValue(r, "label")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l != nil {
    l.lock.Lock()
    l.Label = label
    l.lock.Unlock()
  } else if label != "" {
    DefaultLabel = label
    DefaultListener.Label = label
  }
  events.SendRequestEvent("Listener Label Updated", label, r)
  return label
}

func openListener(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    if l.Listener == nil {
      if l.openListener() {
        if l.TLS {
          msg = fmt.Sprintf("TLS Listener opened on port %d\n", l.Port)
        } else {
          msg = fmt.Sprintf("Listener opened on port %d\n", l.Port)
        }
        events.SendRequestEventJSON("Listener Opened", l.ListenerID,
          map[string]interface{}{"listener": l, "status": msg}, r)
      } else {
        w.WriteHeader(http.StatusInternalServerError)
        msg = fmt.Sprintf("Failed to listen on port %d\n", l.Port)
      }
    } else {
      l.reopenListener()
      if l.TLS {
        msg = fmt.Sprintf("TLS Listener reopened on port %d\n", l.Port)
      } else {
        msg = fmt.Sprintf("Listener reopened on port %d\n", l.Port)
      }
      events.SendRequestEventJSON("Listener Reopened", l.ListenerID,
        map[string]interface{}{"listener": l, "status": msg}, r)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func closeListener(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    if l.Listener == nil {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Port %d not open\n", l.Port)
    } else {
      l.closeListener()
      msg = fmt.Sprintf("Listener on port %d closed\n", l.Port)
      events.SendRequestEvent("Listener Closed", msg, r)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func removeListener(w http.ResponseWriter, r *http.Request) {
  if l := validateListener(w, r); l != nil {
    l.lock.Lock()
    if l.Listener != nil {
      l.Listener.Close()
      l.Listener = nil
    }
    l.lock.Unlock()
    listenersLock.Lock()
    delete(listeners, l.Port)
    listenersLock.Unlock()
    msg := fmt.Sprintf("Listener on port %d removed", l.Port)
    events.SendRequestEvent("Listener Removed", msg, r)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}
