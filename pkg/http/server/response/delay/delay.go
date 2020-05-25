package delay

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Handler     util.ServerHandler       = util.ServerHandler{"delay", SetRoutes, Middleware}
	delayByPort map[string]time.Duration = map[string]time.Duration{}
	delayLock   sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router) {
	delayRouter := r.PathPrefix("/delay").Subrouter()
	util.AddRoute(delayRouter, "/set/{delay}", setDelay, "POST", "PUT")
	util.AddRoute(delayRouter, "/clear", setDelay, "POST", "PUT")
	util.AddRoute(delayRouter, "", getDelay, "GET")
}

func setDelay(w http.ResponseWriter, r *http.Request) {
	delayLock.Lock()
	defer delayLock.Unlock()
	msg := ""
	if delayParam, present := util.GetStringParam(r, "delay"); present {
		if delay, err := time.ParseDuration(delayParam); err == nil {
			delayByPort[util.GetListenerPort(r)] = delay
			msg = fmt.Sprintf("Will delay all requests by %s\n", delay)
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Invalid delay: %s\n", err.Error())
		}
	} else {
		delayByPort[util.GetListenerPort(r)] = 0
		msg = fmt.Sprintf("Delay cleared\n")
		w.WriteHeader(http.StatusAccepted)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getDelay(w http.ResponseWriter, r *http.Request) {
	delayLock.RLock()
	defer delayLock.RUnlock()
	delay := delayByPort[util.GetListenerPort(r)]
	msg := fmt.Sprintf("Current delay: %s\n", delay.String())
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delayLock.RLock()
		delay := delayByPort[util.GetListenerPort(r)]
		delayLock.RUnlock()
		if delay > 0 && !util.IsAdminRequest(r) {
			util.AddLogMessage(fmt.Sprintf("Delaying for = %s", delay.String()), r)
			time.Sleep(delay)
		}
		next.ServeHTTP(w, r)
	})
}
