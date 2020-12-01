package listeners

import (
  "crypto/tls"
  "fmt"
  "goto/pkg/util"
  "log"
  "math"
  "net"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "github.com/gorilla/mux"
)

type TCPConfig struct {
  ReadTimeout            string        `json:"readTimeout"`
  WriteTimeout           string        `json:"writeTimeout"`
  ConnectTimeout         string        `json:"connectTimeout"`
  ConnIdleTimeout        string        `json:"connIdleTimeout"`
  ConnectionLife         string        `json:"connectionLife"`
  Stream                 bool          `json:"stream"`
  Echo                   bool          `json:"echo"`
  Conversation           bool          `json:"conversation"`
  ValidatePayloadLength  bool          `json:"validatePayloadLength"`
  ValidatePayloadContent bool          `json:"validatePayloadContent"`
  ExpectedPayloadLength  int           `json:"expectedPayloadLength"`
  EchoResponseSize       int           `json:"echoResponseSize"`
  EchoResponseDelay      string        `json:"echoResponseDelay"`
  StreamPayloadSize      string        `json:"streamPayloadSize"`
  StreamChunkSize        string        `json:"streamChunkSize"`
  StreamChunkCount       int           `json:"streamChunkCount"`
  StreamChunkDelay       string        `json:"streamChunkDelay"`
  StreamDuration         string        `json:"streamDuration"`
  StreamPayloadSizeV     int           `json:"-"`
  StreamChunkSizeV       int           `json:"-"`
  StreamChunkDelayD      time.Duration `json:"-"`
  StreamDurationD        time.Duration `json:"-"`
  ExpectedPayload        []byte        `json:"-"`
  ReadTimeoutD           time.Duration `json:"-"`
  WriteTimeoutD          time.Duration `json:"-"`
  ConnectTimeoutD        time.Duration `json:"-"`
  ConnIdleTimeoutD       time.Duration `json:"-"`
  EchoResponseDelayD     time.Duration `json:"-"`
  ConnectionLifeD        time.Duration `json:"-"`
}

type Listener struct {
  ListenerID string       `json:"listenerID"`
  Label      string       `json:"label"`
  Port       int          `json:"port"`
  Protocol   string       `json:"protocol"`
  Open       bool         `json:"open"`
  TLS        bool         `json:"tls"`
  TCP        *TCPConfig   `json:"tcp,omitempty"`
  Cert       []byte       `json:"-"`
  Key        []byte       `json:"-"`
  isHTTP     bool         `json:"-"`
  isTCP      bool         `json:"-"`
  isUDP      bool         `json:"-"`
  Listener   net.Listener `json:"-"`
  UDPConn    *net.UDPConn `json:"-"`
  Restarted  bool         `json:"-"`
  Generation int          `json:"-"`
  Lock       sync.RWMutex `json:"-"`
}

var (
  listeners           map[int]*Listener = map[int]*Listener{}
  listenerGenerations map[int]int       = map[int]int{}
  httpServer          func(*Listener)
  tcpServer           func(*Listener)
  DefaultLabel        string
  listenersLock       sync.RWMutex
  Handler             util.ServerHandler = util.ServerHandler{Name: "listeners", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  lRouter := r.PathPrefix("/listeners").Subrouter()
  util.AddRoute(lRouter, "/add", addListener, "POST")
  util.AddRoute(lRouter, "/update", updateListener, "POST")
  util.AddRoute(lRouter, "/{port}/configure/tcp", configureTCP, "POST")
  util.AddRoute(lRouter, "/{port}/cert/add", addListenerCert, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/key/add", addListenerKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/cert/remove", removeListenerCertAndKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/remove", removeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/open", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/reopen", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/close", closeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/timeout/read/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/timeout/write/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/timeout/idle/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/connection/life/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/size/{payloadSize}/duration/{duration}/delay/{delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/chunk/{chunkSize}/duration/{duration}/delay/{delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/chunk/{chunkSize}/count/{chunkCount}/delay/{delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/expect/payload/{length}", setExpectedEchoPayloadLength, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/expect/payload", setExpectedEchoPayload, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/echo/response/delay/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/{enable}", setModes, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/echo/{enable}", setModes, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/conversation/{enable}", setModes, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/validate/payload/{enable}", configurePayloadValidation, "PUT", "POST")
  util.AddRoute(lRouter, "", getListeners, "GET")
}

func SetHTTPServer(server func(*Listener)) {
  httpServer = server
}

func SetTCPServer(server func(*Listener)) {
  tcpServer = server
}

func (l *Listener) initListener() bool {
  l.Lock.Lock()
  defer l.Lock.Unlock()
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
  if strings.EqualFold(l.Protocol, "udp") {
    l.isUDP = true
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
    if strings.EqualFold(l.Protocol, "http") {
      l.isHTTP = true
    } else {
      l.isTCP = true
      if l.TCP == nil {
        l.TCP = &TCPConfig{}
      }
    }
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
    l.Lock.Lock()
    defer l.Lock.Unlock()
    listenerGenerations[l.Port] = listenerGenerations[l.Port] + 1
    l.Generation = listenerGenerations[l.Port]
    l.ListenerID = fmt.Sprintf("%d-%d", l.Port, l.Generation)
    log.Printf("Opening listener %s.", l.ListenerID)
    if l.isHTTP {
      httpServer(l)
    } else if l.isTCP {
      tcpServer(l)
    }
    l.Open = true
    l.TLS = len(l.Cert) > 0 && len(l.Key) > 0
    return true
  }
  return false
}

func (l *Listener) closeListener() {
  l.Lock.Lock()
  defer l.Lock.Unlock()
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
    old.Lock.Lock()
    old.Restarted = true
    old.Lock.Unlock()
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

func (tcp *TCPConfig) configure() string {
  msg := ""
  if tcp.ReadTimeout != "" {
    if tcp.ReadTimeoutD = util.ParseDuration(tcp.ReadTimeout); tcp.ReadTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid read timeout: %s]", tcp.ReadTimeout)
    }
  } else {
    tcp.ReadTimeoutD = 30 * time.Second
  }
  if tcp.WriteTimeout != "" {
    if tcp.WriteTimeoutD = util.ParseDuration(tcp.WriteTimeout); tcp.WriteTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", tcp.WriteTimeout)
    }
  } else {
    tcp.WriteTimeoutD = 30 * time.Second
  }
  if tcp.ConnectTimeout != "" {
    if tcp.ConnectTimeoutD = util.ParseDuration(tcp.ConnectTimeout); tcp.ConnectTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", tcp.ConnectTimeout)
    }
  } else {
    tcp.ConnectTimeoutD = 30 * time.Second
  }
  if tcp.ConnIdleTimeout != "" {
    if tcp.ConnIdleTimeoutD = util.ParseDuration(tcp.ConnIdleTimeout); tcp.ConnIdleTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", tcp.ConnIdleTimeout)
    }
  } else {
    tcp.ConnIdleTimeoutD = 30 * time.Second
  }
  if tcp.EchoResponseDelay != "" {
    if tcp.EchoResponseDelayD = util.ParseDuration(tcp.EchoResponseDelay); tcp.EchoResponseDelayD < 0 {
      msg += fmt.Sprintf("[Invalid echo response delay: %s]", tcp.EchoResponseDelay)
    }
  } else {
    tcp.EchoResponseDelayD = 0
  }
  if tcp.ConnectionLife != "" {
    if tcp.ConnectionLifeD = util.ParseDuration(tcp.ConnectionLife); tcp.ConnectionLifeD < 0 {
      msg += fmt.Sprintf("[Invalid connection life: %s]", tcp.ConnectionLife)
    }
  } else {
    tcp.ConnectionLifeD = 0
  }
  if tcp.EchoResponseSize <= 0 {
    tcp.EchoResponseSize = 100
  }
  tcp.configureStream()
  return msg
}

func computeChunkCount(payloadSize, chunkSize int, chunkDelay, streamDuration time.Duration) int {
  if payloadSize > 0 && chunkSize > 0 {
    return payloadSize / chunkSize
  } else if streamDuration > 0 && chunkDelay > 0 {
    return int((streamDuration.Milliseconds() / chunkDelay.Milliseconds()))
  }
  return 0
}

func computeChunkDelay(payloadSize, chunkSize, chunkCount int, streamDuration time.Duration) time.Duration {
  if streamDuration > 0 {
    if chunkCount > 0 {
      return streamDuration / time.Duration(chunkCount)
    } else if payloadSize > 0 && chunkSize > 0 {
      return streamDuration / time.Duration(payloadSize/chunkSize)
    }
  }
  return 0
}

func computeChunkSize(payloadSize, chunkCount int, chunkDelay, streamDuration time.Duration) int {
  if payloadSize > 0 {
    if chunkCount > 0 {
      return payloadSize / chunkCount
    } else if streamDuration > 0 && chunkDelay > 0 {
      return payloadSize / int((streamDuration.Milliseconds() / chunkDelay.Milliseconds()))
    }
  }
  return 0
}

func computePayloadSize(chunkSize, chunkCount int, chunkDelay, streamDuration time.Duration) int {
  if chunkSize > 0 {
    if chunkCount > 0 {
      return chunkSize * chunkCount
    } else if streamDuration > 0 && chunkDelay > 0 {
      return chunkSize * int((streamDuration.Milliseconds() / chunkDelay.Milliseconds()))
    }
  }
  return 0
}

func computeStreamDuration(payloadSize, chunkSize, chunkCount int, chunkDelay time.Duration) time.Duration {
  if chunkDelay > 0 {
    if chunkCount > 0 {
      return chunkDelay * time.Duration(chunkCount)
    } else if payloadSize > 0 && chunkSize > 0 {
      return chunkDelay * time.Duration(payloadSize/chunkSize)
    }
  }
  return 0
}

func (tcp *TCPConfig) configureStreamParams(sPayloadSize, sChunkSize, sDuration, sChunkDelay string, chunkCount int) {
  requestedPayloadSize := util.ParseSize(sPayloadSize)
  payloadSize := requestedPayloadSize
  chunkSize := util.ParseSize(sChunkSize)
  streamDuration := util.ParseDuration(sDuration)
  chunkDelay := util.ParseDuration(sChunkDelay)

  if payloadSize > 0 && chunkSize > 0 && chunkCount > 0 {
    chunkCount = 0
  } else if streamDuration > 0 && chunkDelay > 0 && chunkCount > 0 {
    chunkCount = 0
  }
  if streamDuration > 0 && chunkDelay > 0 && payloadSize > 0 && chunkSize > 0 {
    chunkSize = 0
  }

  for i := 0; i < 1; i++ {
    if payloadSize == 0 {
      payloadSize = computePayloadSize(chunkSize, chunkCount, chunkDelay, streamDuration)
    }
    if streamDuration == 0 {
      streamDuration = computeStreamDuration(payloadSize, chunkSize, chunkCount, chunkDelay)
    }
    if chunkCount == 0 {
      chunkCount = computeChunkCount(payloadSize, chunkSize, chunkDelay, streamDuration)
    }
    if chunkSize == 0 {
      chunkSize = computeChunkSize(payloadSize, chunkCount, chunkDelay, streamDuration)
    }
    if chunkDelay == 0 {
      chunkDelay = computeChunkDelay(payloadSize, chunkSize, chunkCount, streamDuration)
    }
  }
  if tcp.Stream {
    if chunkDelay == 0 {
      chunkDelay = 100 * time.Millisecond
    }
    if chunkSize == 0 {
      chunkSize = computeChunkSize(payloadSize, chunkCount, chunkDelay, streamDuration)
      if chunkSize == 0 {
        chunkSize = 100
      }
    }
    if chunkCount == 0 {
      chunkCount = computeChunkCount(payloadSize, chunkSize, chunkDelay, streamDuration)
      if chunkCount == 0 {
        chunkCount = 10
      }
    }
    if payloadSize == 0 {
      payloadSize = computePayloadSize(chunkSize, chunkCount, chunkDelay, streamDuration)
      if payloadSize == 0 {
        payloadSize = 1000
      }
    }
    if streamDuration == 0 {
      streamDuration = computeStreamDuration(payloadSize, chunkSize, chunkCount, chunkDelay)
      if streamDuration == 0 {
        streamDuration = 1 * time.Second
      }
    }
  }
  tcp.StreamChunkSizeV = chunkSize
  tcp.StreamChunkSize = strconv.Itoa(chunkSize)
  tcp.StreamChunkCount = chunkCount
  tcp.StreamChunkDelayD = chunkDelay
  tcp.StreamChunkDelay = chunkDelay.String()
  tcp.StreamDurationD = streamDuration
  tcp.StreamDuration = streamDuration.String()
  tcp.StreamPayloadSizeV = payloadSize
  if math.Abs(float64(requestedPayloadSize-(chunkCount*chunkSize))) > 10 {
    tcp.StreamPayloadSize = strconv.Itoa(payloadSize)
  }
}

func (tcp *TCPConfig) configureStream() {
  tcp.configureStreamParams(tcp.StreamPayloadSize, tcp.StreamChunkSize, tcp.StreamDuration, tcp.StreamChunkDelay, tcp.StreamChunkCount)
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

func validateTCPListener(w http.ResponseWriter, r *http.Request) *Listener {
  l := validateListener(w, r)
  if l != nil {
    l.Lock.Lock()
    defer l.Lock.Unlock()
    if !l.isTCP {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Listener %d: Not TCP\n", l.Port)
      return nil
    }
    if l.TCP == nil {
      l.TCP = &TCPConfig{}
    }
  }
  return l
}

func addListener(w http.ResponseWriter, r *http.Request) {
  addOrUpdateListener(w, r, false)
}

func updateListener(w http.ResponseWriter, r *http.Request) {
  addOrUpdateListener(w, r, true)
}

func addOrUpdateListener(w http.ResponseWriter, r *http.Request, update bool) {
  msg := ""
  l := &Listener{}
  if err := util.ReadJsonPayload(r, l); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
    util.AddLogMessage(msg, r)
    fmt.Fprintln(w, msg)
    return
  }
  if l.Port <= 0 || l.Port > 65535 {
    msg += fmt.Sprintf("[Invalid port number: %d]", l.Port)
  }
  l.Protocol = strings.ToLower(l.Protocol)
  if !strings.EqualFold(l.Protocol, "http") && !strings.EqualFold(l.Protocol, "tcp") && !strings.EqualFold(l.Protocol, "udp") {
    msg += fmt.Sprintf("[Invalid protocol: %s]", l.Protocol)
  }
  if l.TCP != nil {
    msg += l.TCP.configure()
  }
  if msg != "" {
    w.WriteHeader(http.StatusBadRequest)
    util.AddLogMessage(msg, r)
    fmt.Fprintln(w, msg)
    return
  }

  if l.Label == "" {
    l.Label = strconv.Itoa(l.Port)
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
        w.WriteHeader(http.StatusInternalServerError)
        msg = fmt.Sprintf("Listener %d already present, failed to restart.", l.Port)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
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
        w.WriteHeader(http.StatusInternalServerError)
        msg = fmt.Sprintf("Listener %d added but failed to open.", l.Port)
      }
    } else {
      listenersLock.Lock()
      listeners[l.Port] = l
      listenersLock.Unlock()
      msg = fmt.Sprintf("Listener %d added.", l.Port)
    }
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func configureTCP(w http.ResponseWriter, r *http.Request) {
  if l := validateTCPListener(w, r); l != nil {
    msg := ""
    tcp := &TCPConfig{}
    if err := util.ReadJsonPayload(r, tcp); err == nil {
      if msg = tcp.configure(); msg == "" {
        l.TCP = tcp
        msg = fmt.Sprintf("TCP configuration applied to port %d", l.Port)
      } else {
        w.WriteHeader(http.StatusBadRequest)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func addListenerCertOrKey(w http.ResponseWriter, r *http.Request, cert bool) {
  if l := validateListener(w, r); l != nil {
    msg := ""
    data := util.ReadBytes(r.Body)
    if len(data) > 0 {
      l.Lock.Lock()
      defer l.Lock.Unlock()
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
    l.Lock.Lock()
    l.Key = nil
    l.Cert = nil
    l.TLS = false
    l.Lock.Unlock()
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
    l.Lock.Lock()
    l.Label = label
    l.Lock.Unlock()
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
    l.Lock.Lock()
    if l.Listener != nil {
      l.Listener.Close()
      l.Listener = nil
    }
    l.Lock.Unlock()
    listenersLock.Lock()
    delete(listeners, l.Port)
    listenersLock.Unlock()
    msg := fmt.Sprintf("Listener on port %d removed", l.Port)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setConnectionDurationConfig(w http.ResponseWriter, r *http.Request) {
  if l := validateTCPListener(w, r); l != nil {
    msg := ""
    dur := util.GetStringParamValue(r, "duration")
    setLife := strings.Contains(r.RequestURI, "connection/life")
    setReadTimeout := strings.Contains(r.RequestURI, "timeout/read")
    setWriteTimeout := strings.Contains(r.RequestURI, "timeout/write")
    setIdleTimeout := strings.Contains(r.RequestURI, "timeout/idle")
    setEchoResponseDelay := strings.Contains(r.RequestURI, "echo/response/delay")
    if d := util.ParseDuration(dur); d >= 0 {
      l.Lock.Lock()
      if setLife {
        l.TCP.ConnectionLifeD = d
        msg = fmt.Sprintf("Connection will close %s after creation for listener %d", dur, l.Port)
      } else if setReadTimeout {
        l.TCP.ReadTimeoutD = d
        msg = fmt.Sprintf("Read timeout set to %s for listener %d", dur, l.Port)
      } else if setWriteTimeout {
        l.TCP.WriteTimeoutD = d
        msg = fmt.Sprintf("Write timeout set to %s for listener %d", dur, l.Port)
      } else if setIdleTimeout {
        l.TCP.ConnIdleTimeoutD = d
        msg = fmt.Sprintf("Connection idle timeout set to %s for listener %d", dur, l.Port)
      } else if setEchoResponseDelay {
        l.TCP.EchoResponseDelayD = d
        msg = fmt.Sprintf("Response will be sent %d after connection for listener %d", dur, l.Port)
      }
      l.Lock.Unlock()
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Invalid duration: %s", dur)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setStreamConfig(w http.ResponseWriter, r *http.Request) {
  if l := validateTCPListener(w, r); l != nil {
    msg := ""
    payloadSize := util.GetStringParamValue(r, "payloadSize")
    chunkSize := util.GetStringParamValue(r, "chunkSize")
    chunkCount := util.GetIntParamValue(r, "chunkCount")
    duration := util.GetStringParamValue(r, "duration")
    delay := util.GetStringParamValue(r, "delay")
    if (chunkSize == "" && payloadSize == "") || (duration == "" && chunkCount == 0) {
      w.WriteHeader(http.StatusBadRequest)
      msg = "Invalid parameters for streaming"
    } else {
      l.TCP.configureStreamParams(payloadSize, chunkSize, duration, delay, chunkCount)
      msg = fmt.Sprintf("Connection will stream [%d] chunks of size [%d] with delay [%s] for a duration of [%s] for listener %d",
        chunkCount, chunkSize, delay, duration, l.Port)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setModes(w http.ResponseWriter, r *http.Request) {
  if l := validateTCPListener(w, r); l != nil {
    msg := ""
    enable := util.GetBoolParamValue(r, "enable")
    echo := strings.Contains(r.RequestURI, "echo")
    conversation := strings.Contains(r.RequestURI, "conversation")
    stream := strings.Contains(r.RequestURI, "stream")
    if stream {
      l.TCP.Stream = enable
      if l.TCP.Stream {
        l.TCP.configureStream()
      }
      msg = fmt.Sprintf("Streaming mode set to [%t] for listener %d", enable, l.Port)
    } else if echo {
      l.TCP.Echo = enable
      if l.TCP.Echo {
        l.TCP.configure()
      }
      msg = fmt.Sprintf("Echo mode set to [%t] for listener %d", enable, l.Port)
    } else if conversation {
      l.TCP.Conversation = enable
      msg = fmt.Sprintf("Conversation mode set to [%t] for listener %d", enable, l.Port)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setExpectedEchoPayloadLength(w http.ResponseWriter, r *http.Request) {
  if l := validateTCPListener(w, r); l != nil {
    l.TCP.ExpectedPayloadLength = util.GetIntParamValue(r, "length")
    l.TCP.ValidatePayloadLength = true
    l.TCP.ValidatePayloadContent = false
    l.TCP.ExpectedPayload = nil
    msg := fmt.Sprintf("Stored expected echo payload length [%d]", l.TCP.ExpectedPayloadLength)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setExpectedEchoPayload(w http.ResponseWriter, r *http.Request) {
  if l := validateTCPListener(w, r); l != nil {
    l.TCP.ExpectedPayload = util.ReadBytes(r.Body)
    l.TCP.ExpectedPayloadLength = len(l.TCP.ExpectedPayload)
    l.TCP.ValidatePayloadLength = true
    l.TCP.ValidatePayloadContent = true
    msg := fmt.Sprintf("Stored expected echo payload of length [%d]", l.TCP.ExpectedPayloadLength)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func configurePayloadValidation(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if l := validateTCPListener(w, r); l != nil {
    enable := util.GetBoolParamValue(r, "enable")
    if enable {
      l.TCP.ValidatePayloadLength = true
      if len(l.TCP.ExpectedPayload) > 0 {
        l.TCP.ValidatePayloadContent = true
        msg = fmt.Sprintf("Will validate payload content of size [%d]", l.TCP.ExpectedPayloadLength)
      } else {
        msg = fmt.Sprintf("Will validate payload length [%d]", l.TCP.ExpectedPayloadLength)
      }
    } else {
      l.TCP.ValidatePayloadLength = false
      l.TCP.ValidatePayloadContent = false
      msg = "Payload validation turned off"
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}
