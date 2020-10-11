package probe

import (
	"fmt"
	"goto/pkg/global"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{"probe", SetRoutes, Middleware}

  ReadinessStatus        int = 200
  ReadinessCount         uint64
  ReadinessOverflowCount uint64
  LivenessStatus         int = 200
  LivenessCount          uint64
  LivenessOverflowCount  uint64
  lock                   sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  probeRouter := r.PathPrefix("/probe").Subrouter()
  util.AddRouteQ(probeRouter, "/{type}/set", setProbe, "uri", "{uri}", "PUT", "POST")
  util.AddRoute(probeRouter, "/{type}/status/set/{status}", setProbeStatus, "PUT", "POST")
  util.AddRoute(probeRouter, "/counts/clear", clearProbeCounts, "POST")
  util.AddRoute(probeRouter, "", getProbes, "GET")
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
    lock.Lock()
    defer lock.Unlock()
    uri = strings.ToLower(uri)
    if isReadiness {
      global.ReadinessProbe = uri
      ReadinessCount = 0
    } else if isLiveness {
      global.LivenessProbe = uri
      LivenessCount = 0
    }
    msg = fmt.Sprintf("%s URI %s added, count reset", probeType, uri)
    w.WriteHeader(http.StatusAccepted)
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
    lock.Lock()
    defer lock.Unlock()
    if status <= 0 {
      status = 200
    }
    if isReadiness {
      ReadinessStatus = status
    } else if isLiveness {
      LivenessStatus = status
    }
    msg = fmt.Sprintf("%s status %d set", probeType, status)
    w.WriteHeader(http.StatusAccepted)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getProbes(w http.ResponseWriter, r *http.Request) {
  lock.RLock()
  lock.RUnlock()
  output := fmt.Sprintf("{\"readiness\": {\"probe\": \"%s\", \"status\": %d, \"count\": %d}, \"liveness\": {\"probe\": \"%s\", \"status\": %d, \"count\": %d}}",
    global.ReadinessProbe, ReadinessStatus, ReadinessCount, global.LivenessProbe, LivenessStatus, LivenessCount)
  util.AddLogMessage(fmt.Sprintf("Reporting probe counts: %s", output), r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, output)
}

func clearProbeCounts(w http.ResponseWriter, r *http.Request) {
  lock.Lock()
  defer lock.Unlock()
  ReadinessCount = 0
  LivenessCount = 0
  msg := "Probe counts cleared"
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if util.IsReadinessProbe(r) {
      lock.Lock()
      ReadinessCount++
      if ReadinessCount == 0 {
        ReadinessOverflowCount++
      }
      lock.Unlock()
      util.CopyHeaders("Readiness-Request", w, r.Header, r.Host)
      w.Header().Add("Readiness-Request-Count", fmt.Sprint(ReadinessCount))
      w.Header().Add("Readiness-Overflow-Count", fmt.Sprint(ReadinessOverflowCount))
      w.WriteHeader(ReadinessStatus)
    } else if util.IsLivenessProbe(r) {
      lock.Lock()
      LivenessCount++
      if LivenessCount == 0 {
        LivenessOverflowCount++
      }
      lock.Unlock()
      util.CopyHeaders("Liveness-Request", w, r.Header, r.Host)
      w.Header().Add("Liveness-Request-Count", fmt.Sprint(LivenessCount))
      w.Header().Add("Liveness-Overflow-Count", fmt.Sprint(LivenessOverflowCount))
      w.WriteHeader(LivenessStatus)
    } else {
      next.ServeHTTP(w, r)
    }
  })
}
