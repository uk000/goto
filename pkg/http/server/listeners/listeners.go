package listeners

import (
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
  Label    string
  Port     int
  Protocol string
  Open     bool
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
  util.AddRoute(lRouter, "/{port}/remove", removeListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/open", openListener, "PUT", "POST")
  util.AddRoute(lRouter, "/{port}/close", closeListener, "PUT", "POST")
  util.AddRoute(lRouter, "", getListeners, "GET")
}

func addListener(w http.ResponseWriter, r *http.Request) {
  listenersLock.Lock()
  defer listenersLock.Unlock()
  var l Listener
  if err := util.ReadJsonPayload(r, &l); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Failed to parse json")
    return
  }
  if l.Port <= 0 || l.Port > 65535 {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Invalid port number")
    return
  }
  if !strings.EqualFold(l.Protocol, "http") && !strings.EqualFold(l.Protocol, "tcp") {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Invalid protocol")
    return
  }
  if listeners[l.Port] != nil {
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintln(w, "Listener already present")
    return
  }
  if l.Label == "" {
    l.Label = strconv.Itoa(l.Port)
  }
  l.Open = false
  listeners[l.Port] = &l
  w.WriteHeader(http.StatusAccepted)
  fmt.Fprintln(w, "Listener added")
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
    if l, present := listeners[port]; present {
      if l.listener == nil {
        if listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", l.Port)); err == nil {
          listenerServer(listener)
          l.listener = listener
          l.Open = true
          w.WriteHeader(http.StatusOK)
          fmt.Fprintf(w, "Listener on port %d opened\n", l.Port)
        } else {
          w.WriteHeader(http.StatusInternalServerError)
          fmt.Fprintf(w, "Failed to listen on port %d\n", l.Port)
          log.Println(err)
        }
      } else {
        w.WriteHeader(http.StatusBadRequest)
        fmt.Fprintf(w, "Already listening on port %d\n", port)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Port %d cannot be opened\n", port)
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
        l.listener.Close()
        l.Open = false
        l.listener = nil
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
