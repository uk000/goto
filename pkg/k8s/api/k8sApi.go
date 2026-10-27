/**
 * Copyright 2026 uk
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

package k8sApi

import (
	"context"
	"encoding/json"
	"fmt"
	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/store"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/watch"
)

var (
	Middleware = middleware.NewMiddleware("k8s", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	k8sRouter := middleware.RootPath("/k8s")

	// The url has the scheme, and so the slashes, of the url of the server (https://host:port).
	util.AddRoute(k8sRouter, "/config/{name}/{url:.+}/{cadata}", apiConfigCluster, "POST")
	util.AddRoute(k8sRouter, "/context/{name}", apiSetContext, "POST")
	util.AddRouteWithMultiQ(k8sRouter, "/resources/{kind}", apiGetResource, [][]string{{}, {"jq", "jp"}}, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{name}", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/all", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/{name}", apiGetResource, "GET")

	// A watch is of the resources that the same URIs under /resources are of. It responds with a stream of events.
	util.AddRoute(k8sRouter, "/watch/{kind}", apiWatchResource, "GET")
	util.AddRoute(k8sRouter, "/watch/{kind}/{name}", apiWatchResource, "GET")
	util.AddRoute(k8sRouter, "/watch/{kind}/{namespace}/all", apiWatchResource, "GET")
	util.AddRoute(k8sRouter, "/watch/{kind}/{namespace}/{name}", apiWatchResource, "GET")
	util.AddRoute(k8sRouter, "/clear", apiClearK8sCache, "POST")
}

func apiClearK8sCache(w http.ResponseWriter, r *http.Request) {
	store.Cache.Clear()
	fmt.Fprintln(w, "K8s Cache Cleared")
}

func apiConfigCluster(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	url := util.GetStringParamValue(r, "url")
	caData := util.GetStringParamValue(r, "cadata")
	err := k8sClient.CreateK8sClientForConfig(name, url, caData)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Failed to create client for K8s config [%s, %s, %s]", name, url, caData)
		w.WriteHeader(http.StatusBadRequest)
	} else {
		msg = fmt.Sprintf("Client configured successfully for K8s config [%s, %s, %s]", name, url, caData)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func apiSetContext(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	msg := ""
	if k8sClient.SetCurrentK8sClient(name) {
		msg = fmt.Sprintf("Current Context swtiched to [%s]", name)
	} else {
		msg = fmt.Sprintf("Context [%s] not found.", name)
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func apiGetResource(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	namespace := util.GetStringParamValue(r, "namespace")
	name := util.GetStringParamValue(r, "name")
	//all := strings.Contains(r.RequestURI, "/all")
	var jp *util.JSONPath
	var jq *util.JQ
	// The queries are looked for as the params that they are: "jp" and "jq" are in the name of any resource that has them.
	if query := r.URL.Query(); query.Has("jp") {
		jp = util.ParseJSONPathsFromRequest("jp", r)
	} else if query.Has("jq") {
		jq = util.ParseJQFromRequest("jq", r)
	}
	gvk, json, err := store.GetResource(kind, namespace, name, jp, jq, r)
	// There is no GVK for a kind that is not known.
	group, version, gvkKind := "", "", kind
	if gvk != nil {
		group, version, gvkKind = gvk.Group, gvk.Version, gvk.Kind
	}
	sendResourceResponse(group, version, gvkKind, namespace, name, json, err, w, r)
}

func sendResourceResponse(group, version, kind, namespace, name string, json util.JSON, err error, w http.ResponseWriter, r *http.Request) {
	if json != nil {
		if util.IsAcceptJSON(r) {
			fmt.Fprintln(w, json.ToJSONText())
		} else {
			fmt.Fprintln(w, json.ToYAML())
		}
	} else if err != nil {
		util.AddLogMessage(fmt.Sprintf("Failed to serve Resource [%s/%s/%s/%s/%s]", group, version, kind, namespace, name), r)
		w.WriteHeader(http.StatusBadRequest)
	} else {
		util.AddLogMessage(fmt.Sprintf("Not Found: Resource [%s/%s/%s/%s/%s]", group, version, kind, namespace, name), r)
		w.WriteHeader(http.StatusNotFound)
	}
}

// watchEvent is an event of a watch, as the API server of k8s sends it.
type watchEvent struct {
	Type   string      `json:"type"`
	Object interface{} `json:"object"`
}

// parseTimeout reads the time that a watch lasts, as a duration (30s, 2m) or a number of seconds.
func parseTimeout(text string) (time.Duration, error) {
	if d, err := time.ParseDuration(text); err == nil {
		return d, nil
	}
	seconds, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("timeout [%s] is neither a duration nor a number of seconds", text)
	}
	return time.Duration(seconds) * time.Second, nil
}

// apiWatchResource responds with the events of the resources as they happen, one JSON object for each, of the form
// {"type":"ADDED|MODIFIED|DELETED|BOOKMARK|ERROR","object":{...}}, until the client goes away or the timeout is over. The
// resources that exist are reported first as added, unless the watch is from a resourceVersion.
// Query params: timeout (a duration or seconds), labelSelector, fieldSelector, resourceVersion.
func apiWatchResource(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	namespace := util.GetStringParamValue(r, "namespace")
	name := util.GetStringParamValue(r, "name")
	query := r.URL.Query()
	opts := store.WatchOptions{
		LabelSelector:   query.Get("labelSelector"),
		FieldSelector:   query.Get("fieldSelector"),
		ResourceVersion: query.Get("resourceVersion"),
	}
	if text := query.Get("timeout"); text != "" {
		timeout, err := parseTimeout(text)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintln(w, err.Error())
			util.AddLogMessage(err.Error(), r)
			return
		}
		opts.Timeout = timeout
	}
	ctx := r.Context()
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	gvk, watcher, err := store.OpenWatch(ctx, kind, namespace, name, opts)
	if err != nil {
		status := http.StatusBadRequest
		if se, ok := err.(*apierrors.StatusError); ok && se.Status().Code != 0 {
			status = int(se.Status().Code)
		}
		w.WriteHeader(status)
		fmt.Fprintln(w, err.Error())
		util.AddLogMessage(fmt.Sprintf("Failed to watch Resource [%s/%s/%s]: %s", kind, namespace, name, err.Error()), r)
		return
	}
	defer watcher.Stop()
	fw := intercept.NewFlushWriter(w)
	if fw == nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "Cannot stream")
		return
	}
	// The response lasts as long as the watch does, which is longer than a write deadline of the connection allows.
	if c := util.GetConn(r); c != nil {
		c.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	fw.Flush()
	util.AddLogMessage(fmt.Sprintf("Watching Resource [%s/%s/%s/%s/%s]", gvk.Group, gvk.Version, gvk.Kind, namespace, name), r)
	enc := json.NewEncoder(fw)
	events := 0
	for {
		select {
		case <-ctx.Done():
			util.AddLogMessage(fmt.Sprintf("Watch of Resource [%s/%s] ended after [%d] events: %v", namespace, name, events, ctx.Err()), r)
			return
		case event, open := <-watcher.ResultChan():
			if !open {
				util.AddLogMessage(fmt.Sprintf("Watch of Resource [%s/%s] closed by the server after [%d] events", namespace, name, events), r)
				return
			}
			// One write for each event, which the flush writer sends at once.
			if err := enc.Encode(watchEvent{Type: string(event.Type), Object: event.Object}); err != nil {
				util.AddLogMessage(fmt.Sprintf("Watch of Resource [%s/%s] ended by a failed write: %s", namespace, name, err.Error()), r)
				return
			}
			events++
			if event.Type == watch.Error {
				return
			}
		}
	}
}
