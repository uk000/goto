package label

import (
  "fmt"
  "net/http"

  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/server/listeners"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{"label", SetRoutes, Middleware}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  labelRouter := util.PathRouter(r, "/server/label")
  util.AddRouteWithPort(labelRouter, "/set/{label}", setLabel, "PUT", "POST")
  util.AddRouteWithPort(labelRouter, "/clear", setLabel, "POST")
  util.AddRouteWithPort(labelRouter, "", getLabel)
}

func setLabel(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if label := listeners.SetListenerLabel(r); label == "" {
    msg := fmt.Sprintf("Port [%s] Label Cleared", util.GetRequestOrListenerPort(r))
    events.SendRequestEvent("Label Cleared", msg, r)
  } else {
    msg = fmt.Sprintf("Will use label %s for all responses on port %s", label, util.GetRequestOrListenerPort(r))
    events.SendRequestEvent("Label Set", msg, r)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getLabel(w http.ResponseWriter, r *http.Request) {
  label := listeners.GetListenerLabel(r)
  msg := fmt.Sprintf("Port [%s] Label [%s]", util.GetRequestOrListenerPort(r), label)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, "Server Label: "+label)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    l := listeners.GetCurrentListener(r)
    hostLabel := util.GetHostLabel()
    util.AddLogMessage(fmt.Sprintf("[%s]", l.Label), r)
    protocol := "HTTP"
    if l.TLS {
      if r.ProtoMajor == 2 {
        protocol = "HTTP/2"
      } else {
        protocol = "HTTPS"
      }
    } else if r.ProtoMajor == 2 {
      protocol = "H2C"
    }
    w.Header().Add("Via-Goto", l.Label)
    if !util.IsTunnelRequest(r) {
      port := util.GetListenerPort(r)
      w.Header().Add("Goto-Host", hostLabel)
      w.Header().Add("Goto-Port", port)
      w.Header().Add("Goto-Protocol", protocol)
      if !util.IsAdminRequest(r) {
        metrics.UpdateProtocolRequestCount(protocol, r.RequestURI)
      }
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
