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
  ReadinessProbe         string `json:"readinessProbe"`
  ReadinessStatus        int    `json:"readinessStatus"`
  ReadinessCount         uint64 `json:"readinessCount"`
  ReadinessOverflowCount uint64 `json:"readinessOverflowCount"`
  LivenessProbe          string `json:"livenessProbe"`
  LivenessStatus         int    `json:"livenessStatus"`
  LivenessCount          uint64 `json:"livenessCount"`
  LivenessOverflowCount  uint64 `json:"livenessOverflowCount"`
  lock                   sync.RWMutex
}

var (
  Handler      util.ServerHandler     = util.ServerHandler{"probes", SetRoutes, Middleware}
  probesByPort map[string]*PortProbes = map[string]*PortProbes{}
  lock         sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  probeRouter := r.PathPrefix("/probe").Subrouter()
  util.AddRouteQ(probeRouter, "/{type}/set", setProbe, "uri", "{uri}", "PUT", "POST")
  util.AddRoute(probeRouter, "/{type}/status/set/{status}", setProbeStatus, "PUT", "POST")
  util.AddRoute(probeRouter, "/counts/clear", clearProbeCounts, "POST")
  util.AddRoute(probeRouter, "", getProbes, "GET")
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
    probesByPort[port] = &PortProbes{ReadinessProbe: "/ready", ReadinessStatus: 200, LivenessProbe: "/live", LivenessStatus: 200}
  }
  return probesByPort[port]
}

func initPortProbes(r *http.Request) *PortProbes {
  return GetPortProbes(util.GetListenerPort(r))
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
    probeStatus := initPortProbes(r)
    uri = strings.ToLower(uri)
    probeStatus.lock.Lock()
    if isReadiness {
      probeStatus.ReadinessProbe = uri
      probeStatus.ReadinessCount = 0
    } else if isLiveness {
      probeStatus.LivenessProbe = uri
      probeStatus.LivenessCount = 0
    }
    probeStatus.lock.Unlock()
    msg = fmt.Sprintf("%s URI %s added, count reset", probeType, uri)
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
  } else if status, present := util.GetIntParam(r, "status", 200); !present {
    msg = "Cannot set. Invalid status code"
    w.WriteHeader(http.StatusBadRequest)
  } else {
    probeStatus := initPortProbes(r)
    if status <= 0 {
      status = 200
    }
    probeStatus.lock.Lock()
    if isReadiness {
      probeStatus.ReadinessStatus = status
    } else if isLiveness {
      probeStatus.LivenessStatus = status
    }
    probeStatus.lock.Unlock()
    msg = fmt.Sprintf("%s status %d set", probeType, status)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getProbes(w http.ResponseWriter, r *http.Request) {
  probeStatus := initPortProbes(r)
  probeStatus.lock.RLock()
  output := util.ToJSON(probeStatus)
  probeStatus.lock.RUnlock()
  util.AddLogMessage(fmt.Sprintf("Reporting probe counts: %s", output), r)
  fmt.Fprintln(w, output)
}

func clearProbeCounts(w http.ResponseWriter, r *http.Request) {
  probeStatus := initPortProbes(r)
  probeStatus.lock.Lock()
  probeStatus.ReadinessCount = 0
  probeStatus.LivenessCount = 0
  probeStatus.lock.Unlock()
  msg := "Probe counts cleared"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    probeStatus := initPortProbes(r)
    if IsReadinessProbe(r) {
      metrics.UpdateRequestCount("readinessProbe")
      probeStatus.lock.Lock()
      probeStatus.ReadinessCount++
      if probeStatus.ReadinessCount == 0 {
        probeStatus.ReadinessOverflowCount++
      }
      probeStatus.lock.Unlock()
      util.CopyHeaders("Readiness-Request", w, r.Header, r.Host, r.RequestURI)
      w.Header().Add("Readiness-Request-Count", fmt.Sprint(probeStatus.ReadinessCount))
      w.Header().Add("Readiness-Overflow-Count", fmt.Sprint(probeStatus.ReadinessOverflowCount))
      w.WriteHeader(probeStatus.ReadinessStatus)
      util.AddLogMessage(fmt.Sprintf("Serving Readiness Probe: [%s]", probeStatus.ReadinessProbe), r)
    } else if IsLivenessProbe(r) {
      metrics.UpdateRequestCount("livenessProbe")
      probeStatus.lock.Lock()
      probeStatus.LivenessCount++
      if probeStatus.LivenessCount == 0 {
        probeStatus.LivenessOverflowCount++
      }
      probeStatus.lock.Unlock()
      util.CopyHeaders("Liveness-Request", w, r.Header, r.Host, r.RequestURI)
      w.Header().Add("Liveness-Request-Count", fmt.Sprint(probeStatus.LivenessCount))
      w.Header().Add("Liveness-Overflow-Count", fmt.Sprint(probeStatus.LivenessOverflowCount))
      w.WriteHeader(probeStatus.LivenessStatus)
      util.AddLogMessage(fmt.Sprintf("Serving Liveness Probe: [%s]", probeStatus.LivenessProbe), r)
    } else if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
