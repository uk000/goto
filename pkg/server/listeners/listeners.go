package listeners

import (
  "crypto/tls"
  "fmt"
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
  ListenerID string         `json:"listenerID"`
  Label      string         `json:"label"`
  Port       int            `json:"port"`
  Protocol   string         `json:"protocol"`
  Open       bool           `json:"open"`
  TLS        bool           `json:"tls"`
  TCP        *tcp.TCPConfig `json:"tcp,omitempty"`
  Cert       []byte         `json:"-"`
  Key        []byte         `json:"-"`
  isHTTP     bool           `json:"-"`
  isTCP      bool           `json:"-"`
  isUDP      bool           `json:"-"`
  Listener   net.Listener   `json:"-"`
  UDPConn    *net.UDPConn   `json:"-"`
  Restarted  bool           `json:"-"`
  Generation int            `json:"-"`
  lock       sync.RWMutex   `json:"-"`
}

var (
  listeners           = map[int]*Listener{}
  listenerGenerations = map[int]int{}
  initialListeners    = []*Listener{}
  httpServer          func(*Listener)
  tcpServer           func(string, int, net.Listener)
  DefaultLabel        string
  serverStarted       bool
  listenersLock       sync.RWMutex
  Handler             util.ServerHandler = util.ServerHandler{Name: "listeners", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  lRouter := r.PathPrefix("/listeners").Subrouter()
  util.AddRoute(lRouter, "/add", addListener, "POST")
  util.AddRoute(lRouter, "/update", updateListener, "POST")
  util.AddRoute(lRouter, "/{port}/cert/add", addListenerCert, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/key/add", addListenerKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/cert/remove", removeListenerCertAndKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/remove", removeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/open", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/reopen", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/close", closeListener, "PUT", "POST")
  util.AddRoute(lRouter, "", getListeners, "GET")
  global.IsListenerPresent = IsListenerPresent
  global.IsListenerOpen = IsListenerOpen
  global.GetListenerID = GetListenerID
}

func SetHTTPServer(server func(*Listener)) {
  httpServer = server
}

func SetTCPServer(server func(string, int, net.Listener)) {
  tcpServer = server
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
        if len(portInfo) > 1 && portInfo[1] != "" {
          portInfo[1] = strings.ToLower(portInfo[1])
          if strings.EqualFold(portInfo[1], "http") || strings.EqualFold(portInfo[1], "tcp") || strings.EqualFold(portInfo[1], "udp") {
            protocol = portInfo[1]
          } else {
            log.Fatalf("Error: Invalid protocol [%s]\n", portInfo[1])
          }
        }
        ports[port] = true
        if i == 0 {
          global.ServerPort = port
        } else {
          listenersLock.Lock()
          initialListeners = append(initialListeners, &Listener{Port: port, Protocol: protocol, Open: true})
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

func (l *Listener) initListener() bool {
  l.lock.Lock()
  defer l.lock.Unlock()
  var tlsConfig *tls.Config
  if len(l.Cert) > 0 && len(l.Key) > 0 {
    if x509Cert, err := tls.X509KeyPair(l.Cert, l.Key); err == nil {
      tlsConfig = &tls.Config{
        Certificates: []tls.Certificate{x509Cert},
        NextProtos:   []string{"http/1.1"},
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
    log.Printf("Opening listener [%s] on port [%d].", l.ListenerID, l.Port)
    if l.isHTTP {
      httpServer(l)
    } else if l.isTCP {
      l.TCP.ListenerID = l.ListenerID
      tcpServer(l.ListenerID, l.Port, l.Listener)
    }
    l.Open = true
    l.TLS = len(l.Cert) > 0 && len(l.Key) > 0
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
    fmt.Fprintf(w, "Port %d: no listener/invalid port/not removable\n", port)
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
  if err := util.ReadJsonPayload(r, l); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
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
    l.Label = strconv.Itoa(l.Port)
  }
  l.Protocol = strings.ToLower(l.Protocol)
  if l.Port <= 0 || l.Port > 65535 {
    msg = fmt.Sprintf("[Invalid port number: %d]", l.Port)
  } else if !strings.EqualFold(l.Protocol, "http") && !strings.EqualFold(l.Protocol, "tcp") && !strings.EqualFold(l.Protocol, "udp") {
    msg = fmt.Sprintf("[Invalid protocol: %s]", l.Protocol)
  } else if strings.EqualFold(l.Protocol, "udp") {
    l.isUDP = true
  } else if strings.EqualFold(l.Protocol, "http") {
    l.isHTTP = true
  } else {
    l.isTCP = true
    l.TCP, msg = tcp.InitTCPConfig(l.Port, l.TCP)
  }
  if msg != "" {
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
      } else {
        errorCode = http.StatusInternalServerError
        msg = fmt.Sprintf("Listener %d already present, failed to restart.", l.Port)
      }
    } else {
      errorCode = http.StatusBadRequest
      msg = fmt.Sprintf("Listener %d already present, cannot add.", l.Port)
    }
  } else {
    if l.Open {
      if l.openListener() {
        listenersLock.Lock()
        listeners[l.Port] = l
        listenersLock.Unlock()
        msg = fmt.Sprintf("Listener %d added and opened.", l.Port)
      } else {
        errorCode = http.StatusInternalServerError
        msg = fmt.Sprintf("Listener %d added but failed to open.", l.Port)
      }
    } else {
      listenersLock.Lock()
      listeners[l.Port] = l
      listenersLock.Unlock()
      msg = fmt.Sprintf("Listener %d added.", l.Port)
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
        l.Cert = data
        msg = fmt.Sprintf("Cert added for listener %d\n", l.Port)
      } else {
        l.Key = data
        msg = fmt.Sprintf("Key added for listener %d\n", l.Port)
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
    l.Key = nil
    l.Cert = nil
    l.TLS = false
    l.lock.Unlock()
    if l.reopenListener() {
      msg = fmt.Sprintf("Cert and Key removed for listener %d, and reopened\n", l.Port)
    } else {
      w.WriteHeader(http.StatusInternalServerError)
      msg = fmt.Sprintf("Cert and Key removed for listener %d but failed to reopen\n", l.Port)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func getListeners(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  util.WriteJsonPayload(w, listeners)
}

func GetListener(r *http.Request) *Listener {
  port := util.GetListenerPortNum(r)
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  return listeners[port]
}

func GetListenerLabel(r *http.Request) string {
  port := util.GetListenerPortNum(r)
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if l := listeners[port]; l != nil {
    return l.Label
  } else if DefaultLabel != "" {
    return DefaultLabel
  }
  return strconv.Itoa(port)
}

func SetListenerLabel(r *http.Request) string {
  port := util.GetListenerPortNum(r)
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
  }
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
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}
