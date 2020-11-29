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

type Listener struct {
  ListenerID         string        `json:"-"`
  Label              string        `json:"label"`
  Port               int           `json:"port"`
  Protocol           string        `json:"protocol"`
  Open               bool          `json:"open"`
  ReadTimeout        string        `json:"readTimeout"`
  WriteTimeout       string        `json:"writeTimeout"`
  ConnectTimeout     string        `json:"connectTimeout"`
  ConnIdleTimeout    string        `json:"connIdleTimeout"`
  ConnectionLife     string        `json:"connectionLife"`
  Echo               bool          `json:"echo"`
  EchoPacketSize     int           `json:"echoPacketSize"`
  ResponseDelay      string        `json:"responseDelay"`
  Stream             bool          `json:"stream"`
  StreamPayloadSize  string        `json:"streamPayloadSize"`
  StreamChunkSize    string        `json:"streamChunkSize"`
  StreamChunkCount   int           `json:"streamChunkCount"`
  StreamChunkDelay   string        `json:"streamChunkDelay"`
  StreamDuration     string        `json:"streamDuration"`
  TLS                bool          `json:"tls"`
  Cert               []byte        `json:"-"`
  Key                []byte        `json:"-"`
  isHTTP             bool          `json:"-"`
  isTCP              bool          `json:"-"`
  isUDP              bool          `json:"-"`
  StreamPayloadSizeV int           `json:"-"`
  StreamChunkSizeV   int           `json:"-"`
  StreamChunkDelayD  time.Duration `json:"-"`
  StreamDurationD    time.Duration `json:"-"`
  ReadTimeoutD       time.Duration `json:"-"`
  WriteTimeoutD      time.Duration `json:"-"`
  ConnectTimeoutD    time.Duration `json:"-"`
  ConnIdleTimeoutD   time.Duration `json:"-"`
  ResponseDelayD     time.Duration `json:"-"`
  ConnectionLifeD    time.Duration `json:"-"`
  Listener           net.Listener  `json:"-"`
  UDPConn            *net.UDPConn  `json:"-"`
  Restarted          bool          `json:"-"`
  Generation         int           `json:"-"`
  Lock               sync.RWMutex  `json:"-"`
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
  util.AddRoute(lRouter, "/{port}/cert/add", addListenerCert, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/key/add", addListenerKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/cert/remove", removeListenerCertAndKey, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/remove", removeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/open", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/reopen", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/close", closeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/response/delay/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/timeout/read/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/timeout/write/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/timeout/idle/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/connection/life/{duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/size/{payloadSize}/duration/{duration}/delay/{delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/chunk/{chunkSize}/duration/{duration}/delay/{delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/stream/chunk/{chunkSize}/count/{chunkCount}/delay/{delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/mode/stream/{enable}", setModes, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/mode/echo/{enable}", setModes, "PUT", "POST")
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
  return l.openListener()
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

func (l *Listener) configureStreamParams(sPayloadSize, sChunkSize, sDuration, sChunkDelay string, chunkCount int) {
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
  if l.Stream {
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
  l.Lock.Lock()
  l.StreamChunkSizeV = chunkSize
  l.StreamChunkSize = strconv.Itoa(chunkSize)
  l.StreamChunkCount = chunkCount
  l.StreamChunkDelayD = chunkDelay
  l.StreamChunkDelay = chunkDelay.String()
  l.StreamDurationD = streamDuration
  l.StreamDuration = streamDuration.String()
  l.StreamPayloadSizeV = payloadSize
  if math.Abs(float64(requestedPayloadSize-(chunkCount*chunkSize))) > 10 {
    l.StreamPayloadSize = strconv.Itoa(payloadSize)
  }
  l.Lock.Unlock()
}

func (l *Listener) configureStream() {
  l.configureStreamParams(l.StreamPayloadSize, l.StreamChunkSize, l.StreamDuration, l.StreamChunkDelay, l.StreamChunkCount)
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
  if l.ReadTimeout != "" {
    if l.ReadTimeoutD = util.ParseDuration(l.ReadTimeout); l.ReadTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid read timeout: %s]", l.ReadTimeout)
    }
  } else {
    l.ReadTimeoutD = 30 * time.Second
  }
  if l.WriteTimeout != "" {
    if l.WriteTimeoutD = util.ParseDuration(l.WriteTimeout); l.WriteTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", l.WriteTimeout)
    }
  } else {
    l.WriteTimeoutD = 30 * time.Second
  }
  if l.ConnectTimeout != "" {
    if l.ConnectTimeoutD = util.ParseDuration(l.ConnectTimeout); l.ConnectTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", l.ConnectTimeout)
    }
  } else {
    l.ConnectTimeoutD = 30 * time.Second
  }
  if l.ConnIdleTimeout != "" {
    if l.ConnIdleTimeoutD = util.ParseDuration(l.ConnIdleTimeout); l.ConnIdleTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", l.ConnIdleTimeout)
    }
  } else {
    l.ConnIdleTimeoutD = 30 * time.Second
  }
  if l.ResponseDelay != "" {
    if l.ResponseDelayD = util.ParseDuration(l.ResponseDelay); l.ResponseDelayD < 0 {
      msg += fmt.Sprintf("[Invalid response delay: %s]", l.ResponseDelay)
    }
  } else {
    l.ResponseDelayD = 0
  }
  if l.ConnectionLife != "" {
    if l.ConnectionLifeD = util.ParseDuration(l.ConnectionLife); l.ConnectionLifeD < 0 {
      msg += fmt.Sprintf("[Invalid connection life: %s]", l.ConnectionLife)
    }
  } else {
    l.ConnectionLifeD = 0
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
  if l.ConnectionLifeD < 0 {
    l.ConnectionLifeD = 0
  }
  if l.ResponseDelayD < 0 {
    l.ResponseDelayD = 0
  }
  if l.EchoPacketSize <= 0 {
    l.EchoPacketSize = 100
  }
  l.configureStream()

  listenersLock.RLock()
  _, exists := listeners[l.Port]
  listenersLock.RUnlock()
  if exists {
    if update {
      msg = fmt.Sprintf("Listener %d already present, restarting.", l.Port)
      fmt.Fprintln(w, msg)
      l.reopenListener()
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Listener %d already present, cannot add.", l.Port)
      fmt.Fprintln(w, msg)
    }
  } else {
    if l.Open {
      msg = fmt.Sprintf("Listener %d added and opened.", l.Port)
      fmt.Fprintln(w, msg)
      l.reopenListener()
    } else {
      msg = fmt.Sprintf("Listener %d added.", l.Port)
      fmt.Fprintln(w, msg)
    }
  }
  listenersLock.Lock()
  listeners[l.Port] = l
  listenersLock.Unlock()
  util.AddLogMessage(msg, r)
}

func addListenerCertOrKey(w http.ResponseWriter, r *http.Request, cert bool) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l != nil {
    data := util.ReadBytes(r.Body)
    l.Lock.Lock()
    defer l.Lock.Unlock()
    if cert {
      l.Cert = data
      fmt.Fprintf(w, "Cert added for listener %d\n", l.Port)
    } else {
      l.Key = data
      fmt.Fprintf(w, "Key added for listener %d\n", l.Port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "No listener present or invalid port %d\n", port)
  }
}

func addListenerCert(w http.ResponseWriter, r *http.Request) {
  addListenerCertOrKey(w, r, true)
}

func addListenerKey(w http.ResponseWriter, r *http.Request) {
  addListenerCertOrKey(w, r, false)
}

func removeListenerCertAndKey(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l != nil {
    l.Lock.Lock()
    l.Key = nil
    l.Cert = nil
    l.TLS = false
    l.Lock.Unlock()
    l.reopenListener()
    fmt.Fprintf(w, "Cert and Key removed for listener %d, and reopened\n", l.Port)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "No listener present or invalid port %d\n", port)
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
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "No listener present or invalid port %d\n", port)
  } else if l.Listener == nil {
    if l.openListener() {
      if l.TLS {
        fmt.Fprintf(w, "TLS Listener opened on port %d\n", l.Port)
      } else {
        fmt.Fprintf(w, "Listener opened on port %d\n", l.Port)
      }
    } else {
      w.WriteHeader(http.StatusInternalServerError)
      fmt.Fprintf(w, "Failed to listen on port %d\n", l.Port)
    }
  } else {
    l.reopenListener()
    if l.TLS {
      fmt.Fprintf(w, "TLS Listener reopened on port %d\n", l.Port)
    } else {
      fmt.Fprintf(w, "Listener reopened on port %d\n", l.Port)
    }
  }
}

func closeListener(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Port %d: no listener/invalid port/not closeable\n", port)
  } else if l.Listener == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Port %d not open\n", port)
  } else {
    l.closeListener()
    fmt.Fprintf(w, "Listener on port %d closed\n", l.Port)
  }
}

func removeListener(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Port %d: no listener/invalid port/not removable\n", port)
  } else {
    l.Lock.Lock()
    if l.Listener != nil {
      l.Listener.Close()
      l.Listener = nil
    }
    l.Lock.Unlock()
    listenersLock.Lock()
    delete(listeners, port)
    listenersLock.Unlock()
    fmt.Fprintf(w, "Listener on port %d removed\n", port)
  }
}

func setConnectionDurationConfig(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Port %d: no listener/invalid port/not removable\n", port)
  } else {
    dur := util.GetStringParamValue(r, "duration")
    setLife := strings.Contains(r.RequestURI, "connection/life")
    setReadTimeout := strings.Contains(r.RequestURI, "timeout/read")
    setWriteTimeout := strings.Contains(r.RequestURI, "timeout/write")
    setIdleTimeout := strings.Contains(r.RequestURI, "timeout/idle")
    setResponseDelay := strings.Contains(r.RequestURI, "response/delay")
    if d := util.ParseDuration(dur); d >= 0 {
      l.Lock.Lock()
      if setLife {
        l.ConnectionLifeD = d
        fmt.Fprintf(w, "Connection will close %s after creation for listener %d\n", dur, port)
      } else if setReadTimeout {
        l.ReadTimeoutD = d
        fmt.Fprintf(w, "Read timeout set to %s for listener %d\n", dur, port)
      } else if setWriteTimeout {
        l.WriteTimeoutD = d
        fmt.Fprintf(w, "Write timeout set to %s for listener %d\n", dur, port)
      } else if setIdleTimeout {
        l.ConnIdleTimeoutD = d
        fmt.Fprintf(w, "Connection idle timeout set to %s for listener %d\n", dur, port)
      } else if setResponseDelay {
        l.ResponseDelayD = d
        fmt.Fprintf(w, "Response will be sent %d after connection for listener %d\n", dur, port)
      }
      l.Lock.Unlock()
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintln(w, "Invalid duration")
    }
  }
}

func setStreamConfig(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Port %d: no listener/invalid port/not removable\n", port)
    return
  }
  payloadSize := util.GetStringParamValue(r, "payloadSize")
  chunkSize := util.GetStringParamValue(r, "chunkSize")
  chunkCount := util.GetIntParamValue(r, "chunkCount")
  duration := util.GetStringParamValue(r, "duration")
  delay := util.GetStringParamValue(r, "delay")
  if (chunkSize == "" && payloadSize == "") || (duration == "" && chunkCount == 0) {
    w.WriteHeader(http.StatusBadRequest)
    util.AddLogMessage("Invalid parameters for streaming", r)
    fmt.Fprintln(w, "{error: 'Invalid parameters for streaming'}")
    return
  }
  l.configureStreamParams(payloadSize, chunkSize, duration, delay, chunkCount)
  fmt.Fprintf(w, "Connection will stream [%d] chunks of size [%d] with delay [%s] for a duration of [%s] for listener %d\n", chunkCount, chunkSize, delay, duration, port)
}

func setModes(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  listenersLock.Lock()
  l := listeners[port]
  listenersLock.Unlock()
  if l == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "No listener present or invalid port %d\n", port)
  } else {
    enable := util.GetBoolParamValue(r, "enable")
    stream := strings.Contains(r.RequestURI, "stream")
    echo := strings.Contains(r.RequestURI, "echo")
    if stream {
      l.Stream = enable
      if l.Stream {
        l.configureStream()
      }
      fmt.Fprintf(w, "Streaming set to [%t] for listener %d\n", enable, port)
    } else if echo {
      l.Echo = enable
      fmt.Fprintf(w, "Echo set to [%t] for listener %d\n", enable, port)
    }
  }
}
