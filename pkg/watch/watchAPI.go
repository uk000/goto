/**
 * Copyright 2025 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package watch

import (
	"fmt"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type Watcher struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	FilterURIs    map[string]bool   `json:"filterURIs"`
	FilterHeaders map[string]string `json:"filterHeaders"`
}

type Watch struct {
	Watchers map[string]*Watcher `json:"watchers"`
	lock     sync.RWMutex
}

var (
	Middleware = middleware.NewMiddleware("watch", setRoutes, middlewareFunc)
	watch      = &Watch{
		Watchers: map[string]*Watcher{},
	}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	watchRouter := util.PathRouter(r, "/watch")
	util.AddRouteQ(watchRouter, "/add/{name}", addWebhookWatcher, "url", "POST")
	// util.AddRouteQ(watchRouter, "/{name}/filter/add", addWatchFilter, "uri", "POST")
	// util.AddRouteMultiQ(watchRouter, "/{name}/filter/add", addWatchFilter, []string{"k", "v"}, "POST")
	util.AddRoute(watchRouter, "/ws", handleWebsocket, "GET")
}

func (w *Watch) addWebhookWatcher(name, url string) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.Watchers[name] = &Watcher{
		Name: name,
		URL:  url,
	}
}

func (w *Watch) addWatcherFilter(name, uri, header, value string) bool {
	w.lock.Lock()
	defer w.lock.Unlock()
	watcher := w.Watchers[name]
	if watcher == nil {
		return false
	}
	watched := false
	if uri != "" {
		uri = strings.ToLower(uri)
		watcher.FilterURIs[uri] = true
		watched = true
	}
	if header != "" {
		watcher.FilterHeaders[header] = value
		watched = true
	}
	return watched
}

func (w *Watch) removeWatcher(name string) *Watcher {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.Watchers[name]
}

func (w *Watch) getWatcher(name string) {
	w.lock.Lock()
	defer w.lock.Unlock()
	delete(w.Watchers, name)
}

func addWebhookWatcher(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	url := util.GetStringParamValue(r, "url")
	watch.addWebhookWatcher(name, url)
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Registered Webhook Watch: Name [%s], URL [%s]", name, url)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// func addWatchFilter(w http.ResponseWriter, r *http.Request) {
// 	name := util.GetStringParamValue(r, "name")
// 	uri := util.GetStringParamValue(r, "uri")
// 	header := util.GetStringParamValue(r, "k")
// 	value := util.GetStringParamValue(r, "v")
// 	watch.addWebhookWatcher(name, url)
// 	w.WriteHeader(http.StatusOK)
// 	msg := fmt.Sprintf("Registered Webhook Watch: Name [%s], URL [%s]", name, url)
// 	fmt.Fprintln(w, msg)
// 	util.AddLogMessage(msg, r)
// }

func handleWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	WebSockets.AddSocket(r.RemoteAddr, conn)
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var irw *intercept.InterceptResponseWriter
		if WebSockets.HasOpenSockets() {
			w, irw = intercept.WithIntercept(r, w)
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
		if irw != nil {
			WebSockets.Broadcast(r.RequestURI, r.Header, irw.StatusCode, irw.Header(), irw.Data)
			irw.Proceed()
		}
	})
}
