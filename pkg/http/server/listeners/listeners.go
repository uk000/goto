package listeners

import (
  "crypto/tls"
  "fmt"
  "goto/pkg/util"
  "log"
  "net"
  "net/http"
  "strconv"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type Listener struct {
  Label    string `json:"label"`
  Port     int    `json:"port"`
  Protocol string `json:"protocol"`
  Open     bool   `json:"open"`
  cert     []byte
  key      []byte
  listener net.Listener
}

var (
  listeners      map[int]*Listener = map[int]*Listener{}
  listenerServer func(net.Listener)
  DefaultLabel   string
  listenersLock  sync.RWMutex
  Handler        util.ServerHandler = util.ServerHandler{Name: "listeners", SetRoutes: SetRoutes}
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
}

func newListener(l *Listener) bool {
  var tlsConfig *tls.Config
  if len(l.cert) > 0 && len(l.key) > 0 {
    if x509Cert, err := tls.X509KeyPair(l.cert, l.key); err == nil {
      tlsConfig = &tls.Config{
        Certificates: []tls.Certificate{x509Cert},
        NextProtos:   []string{"http/1.1"},
      }
    } else {
      log.Printf("Failed to parse certificate with error: %s\n", err.Error())
      return false
    }
  }
  if listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", l.Port)); err == nil {
    if tlsConfig != nil {
      listener = tls.NewListener(listener, tlsConfig)
    }
    l.listener = listener
    return true
  } else {
    log.Printf("Failed to open listener with error: %s\n", err.Error())
  }
  return false
}

func unsafeOpenListener(l *Listener) bool {
  if newListener(l) {
    l.Open = true
    listenerServer(l.listener)
    return true
  }
  return false
}

func unsafeCloseListener(l *Listener) {
  l.listener.Close()
  l.Open = false
  l.listener = nil
}

func unsafeReopenListener(l *Listener) bool {
  unsafeCloseListener(l)
  return unsafeOpenListener(l)
}

func addListener(w http.ResponseWriter, r *http.Request) {
  addOrUpdateListener(w, r, false)
}

func updateListener(w http.ResponseWriter, r *http.Request) {
  addOrUpdateListener(w, r, true)
}

func addOrUpdateListener(w http.ResponseWriter, r *http.Request, update bool) {
  msg := ""
  listenersLock.Lock()
  defer listenersLock.Unlock()
  l := &Listener{}
  if err := util.ReadJsonPayload(r, l); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    msg := fmt.Sprintf("Failed to parse json with error: %s", err.Error())
    util.AddLogMessage(msg, r)
    fmt.Fprintln(w, msg)
    return
  }
  if l.Port <= 0 || l.Port > 65535 {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Invalid port number: %d", l.Port)
    util.AddLogMessage(msg, r)
    fmt.Fprintln(w, msg)
    return
  }
  if !strings.EqualFold(l.Protocol, "http") && !strings.EqualFold(l.Protocol, "tcp") {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Invalid protocol: %s", l.Protocol)
    util.AddLogMessage(msg, r)
    fmt.Fprintln(w, msg)
    return
  }
  if l.Label == "" {
    l.Label = strconv.Itoa(l.Port)
  }
  if listeners[l.Port] != nil {
    if update {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Listener %d already present, restarting.", l.Port)
      fmt.Fprintln(w, msg)
      unsafeReopenListener(l)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Listener %d already present, cannot add.", l.Port)
      fmt.Fprintln(w, msg)
    }
  } else {
    if update {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Listener %d not present, starting.", l.Port)
      fmt.Fprintln(w, msg)
      unsafeReopenListener(l)
    } else {
      l.Open = false
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Listener %d added.", l.Port)
      fmt.Fprintln(w, msg)
    }
  }
  listeners[l.Port] = l
  util.AddLogMessage(msg, r)
}

func addListenerCert(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port, present := util.GetIntParam(r, "port"); present {
    if l, present := listeners[port]; present {
      l.cert = util.ReadBytes(r.Body)
      fmt.Fprintf(w, "Cert added for listener %d\n", l.Port)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "No listener added for port %d\n", port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No port given")
  }
}

func addListenerKey(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port, present := util.GetIntParam(r, "port"); present {
    if l, present := listeners[port]; present {
      l.key = util.ReadBytes(r.Body)
      fmt.Fprintf(w, "Key added for listener %d\n", l.Port)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "No listener present for port %d\n", port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No port given")
  }
}

func removeListenerCertAndKey(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port, present := util.GetIntParam(r, "port"); present {
    if l, present := listeners[port]; present {
      l.key = nil
      l.cert = nil
      unsafeReopenListener(l)
      fmt.Fprintf(w, "Cert and Key removed for listener %d, and reopened\n", l.Port)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "No listener present for port %d\n", port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No port given")
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
  if label, present := util.GetStringParam(r, "label"); present {
    port := util.GetListenerPortNum(r)
    listenersLock.Lock()
    defer listenersLock.Unlock()
    if l, present := listeners[port]; present {
      l.Label = label
    } else {
      DefaultLabel = label
    }
    return label
  }
  return ""
}
func SetListenerServer(server func(net.Listener)) {
  listenerServer = server
}

func openListener(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port, present := util.GetIntParam(r, "port"); present {
    if l, present := listeners[port]; !present || l.listener == nil {
      if unsafeOpenListener(l) {
        w.WriteHeader(http.StatusOK)
        if len(l.cert) > 0 && len(l.key) > 0 {
          fmt.Fprintf(w, "TLS Listener opened on port %d\n", l.Port)
        } else {
          fmt.Fprintf(w, "Listener opened on port %d\n", l.Port)
        }
      } else {
        w.WriteHeader(http.StatusInternalServerError)
        fmt.Fprintf(w, "Failed to listen on port %d\n", l.Port)
      }
    } else {
      unsafeReopenListener(l)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Listener on port %d reopened\n", l.Port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No port given")
  }
}

func closeListener(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port, present := util.GetIntParam(r, "port"); present {
    if l, present := listeners[port]; present {
      if l.listener != nil {
        unsafeCloseListener(l)
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Listener on port %d closed\n", l.Port)
      } else {
        w.WriteHeader(http.StatusBadRequest)
        fmt.Fprintf(w, "Port %d not open\n", port)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Port %d not a closeable listener\n", port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No port given")
  }
}

func removeListener(w http.ResponseWriter, r *http.Request) {
  listenersLock.RLock()
  defer listenersLock.RUnlock()
  if port, present := util.GetIntParam(r, "port"); present {
    if l, present := listeners[port]; present {
      if l.listener != nil {
        l.listener.Close()
      }
      delete(listeners, port)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Listener on port %d removed\n", port)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Port %d not a removable listener\n", port)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No port given")
  }
}
