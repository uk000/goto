package probes

import (
  "fmt"
  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "net/http"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type PortProbes struct {
  Port                          string `json:"port"`
  ReadinessProbe                string `json:"readinessProbe"`
  ReadinessStatus               int    `json:"readinessStatus"`
  ReadinessStatusRemainingCount int    `json:"readinessStatusRemainingCount"`
  ReadinessCount                uint64 `json:"readinessCount"`
  ReadinessOverflowCount        uint64 `json:"readinessOverflowCount"`
  LivenessProbe                 string `json:"livenessProbe"`
  LivenessStatus                int    `json:"livenessStatus"`
  LivenessStatusRemainingCount  int    `json:"livenessStatusRemainingCount"`
  LivenessCount                 uint64 `json:"livenessCount"`
  LivenessOverflowCount         uint64 `json:"livenessOverflowCount"`
  lock                          sync.RWMutex
}

var (
  Handler      util.ServerHandler     = util.ServerHandler{"probes", SetRoutes, Middleware}
  probesByPort map[string]*PortProbes = map[string]*PortProbes{}
  lock         sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  probeRouter := util.PathRouter(r, "/probes")
  util.AddRouteQWithPort(probeRouter, "/{type}/set", setProbe, "uri", "{uri}", "PUT", "POST")
  util.AddRouteWithPort(probeRouter, "/{type}/set/status={status}", setProbeStatus, "PUT", "POST")
  util.AddRouteWithPort(probeRouter, "/counts/clear", clearProbeCounts, "POST")
  util.AddRouteWithPort(probeRouter, "", getProbes, "GET")
  global.IsLivenessProbe = IsLivenessProbe
  global.IsReadinessProbe = IsReadinessProbe
}

func IsLivenessProbe(r *http.Request) bool {
  return strings.EqualFold(r.RequestURI, initPortProbes(r).LivenessProbe)
}
func IsReadinessProbe(r *http.Request) bool {
  return strings.EqualFold(r.RequestURI, initPortProbes(r).ReadinessProbe)
}

func GetPortProbes(port string) *PortProbes {
  lock.Lock()
  defer lock.Unlock()
  if probesByPort[port] == nil {
    probesByPort[port] = &PortProbes{Port: port, ReadinessProbe: "/ready", ReadinessStatus: 200, LivenessProbe: "/live", LivenessStatus: 200}
  }
  return probesByPort[port]
}

func initPortProbes(r *http.Request) *PortProbes {
  return GetPortProbes(util.GetRequestOrListenerPort(r))
}

func setProbe(w http.ResponseWriter, r *http.Request) {
  msg := ""
  probeType := util.GetStringParamValue(r, "type")
  isReadiness := strings.EqualFold(probeType, "readiness")
  isLiveness := strings.EqualFold(probeType, "liveness")
  if !isReadiness && !isLiveness {
    msg = "Cannot add. Invalid probe type"
    w.WriteHeader(http.StatusBadRequest)
  } else if uri, present := util.GetStringParam(r, "uri"); !present {
    msg = "Cannot add. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    pp := initPortProbes(r)
    uri = strings.ToLower(uri)
    pp.lock.Lock()
    if isReadiness {
      pp.ReadinessProbe = uri
      pp.ReadinessCount = 0
    } else if isLiveness {
      pp.LivenessProbe = uri
      pp.LivenessCount = 0
    }
    pp.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] Probe [%s] URI [%s] added, count reset", pp.Port, probeType, uri)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func setProbeStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  probeType := util.GetStringParamValue(r, "type")
  isReadiness := strings.EqualFold(probeType, "readiness")
  isLiveness := strings.EqualFold(probeType, "liveness")
  if !isReadiness && !isLiveness {
    msg = "Cannot add. Invalid probe type"
    w.WriteHeader(http.StatusBadRequest)
  } else if status, count, present := util.GetStatusParam(r); !present {
    msg = "Cannot set. Invalid status code"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    pp := initPortProbes(r)
    if status <= 0 {
      status = 200
    }
    if count <= 0 {
      count = -1
    }
    pp.lock.Lock()
    if isReadiness {
      pp.ReadinessStatus = status
      pp.ReadinessStatusRemainingCount = count
    } else if isLiveness {
      pp.LivenessStatus = status
      pp.LivenessStatusRemainingCount = count
    }
    pp.lock.Unlock()
    if count > 0 {
      msg = fmt.Sprintf("Port [%s] Probe [%s] Status [%d] set with remaining count [%d]", pp.Port, probeType, status, count)
    } else {
      msg = fmt.Sprintf("Port [%s] Probe [%s] Status [%d] set", pp.Port, probeType, status)
    }
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getProbes(w http.ResponseWriter, r *http.Request) {
  pp := initPortProbes(r)
  pp.lock.RLock()
  output := util.ToJSON(pp)
  pp.lock.RUnlock()
  util.AddLogMessage(fmt.Sprintf("Reporting probe counts: %s", output), r)
  util.WriteJsonPayload(w, output)
}

func clearProbeCounts(w http.ResponseWriter, r *http.Request) {
  pp := initPortProbes(r)
  pp.lock.Lock()
  pp.ReadinessCount = 0
  pp.LivenessCount = 0
  pp.lock.Unlock()
  msg := fmt.Sprintf("Port [%s] Counts Cleared", pp.Port)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    pp := initPortProbes(r)
    if IsReadinessProbe(r) {
      metrics.UpdateRequestCount("readinessProbe")
      pp.lock.Lock()
      status := pp.ReadinessStatus
      if pp.ReadinessStatusRemainingCount > 0 {
        pp.ReadinessStatusRemainingCount--
        if pp.ReadinessStatusRemainingCount == 0 {
          pp.ReadinessStatusRemainingCount = -1
          pp.ReadinessStatus = 200
        }
      }
      pp.ReadinessCount++
      if pp.ReadinessCount == 0 {
        pp.ReadinessOverflowCount++
      }
      pp.lock.Unlock()
      util.CopyHeaders("Readiness-Request", w, r.Header, r.Host, r.RequestURI)
      w.Header().Add("Readiness-Request-Count", fmt.Sprint(pp.ReadinessCount))
      w.Header().Add("Readiness-Overflow-Count", fmt.Sprint(pp.ReadinessOverflowCount))
      w.WriteHeader(status)
      util.SetHeadersSent(r, true)
      util.AddLogMessage(fmt.Sprintf("Serving Readiness Probe: [%s]", pp.ReadinessProbe), r)
    } else if IsLivenessProbe(r) {
      metrics.UpdateRequestCount("livenessProbe")
      pp.lock.Lock()
      status := pp.LivenessStatus
      if pp.LivenessStatusRemainingCount > 0 {
        pp.LivenessStatusRemainingCount--
        if pp.LivenessStatusRemainingCount == 0 {
          pp.LivenessStatusRemainingCount = -1
          pp.LivenessStatus = 200
        }
      }
      pp.LivenessCount++
      if pp.LivenessCount == 0 {
        pp.LivenessOverflowCount++
      }
      pp.lock.Unlock()
      util.CopyHeaders("Liveness-Request", w, r.Header, r.Host, r.RequestURI)
      w.Header().Add("Liveness-Request-Count", fmt.Sprint(pp.LivenessCount))
      w.Header().Add("Liveness-Overflow-Count", fmt.Sprint(pp.LivenessOverflowCount))
      w.WriteHeader(status)
      util.SetHeadersSent(r, true)
      util.AddLogMessage(fmt.Sprintf("Serving Liveness Probe: [%s]", pp.LivenessProbe), r)
    } else if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
