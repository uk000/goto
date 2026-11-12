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

// Package k8stest has an in-memory API server of Kubernetes for the tests of the packages that talk to one. It serves
// discovery and the resources (list, get, watch, apply and delete) of a few kinds, on a listener of the loopback
// interface, and makes the k8s client of the package the one that talks to it. It is not used by the code of the server.
package k8stest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
)

// Request is a request that the API server got for a resource.
type Request struct {
	Method      string
	Path        string
	RawQuery    string
	ContentType string
	Body        []byte
}

type resource struct {
	group, version, plural, kind string
	namespaced                   bool
}

var resources = []resource{
	{"", "v1", "namespaces", "Namespace", false},
	{"", "v1", "configmaps", "ConfigMap", true},
	{"", "v1", "pods", "Pod", true},
	{"", "v1", "services", "Service", true},
	{"apps", "v1", "deployments", "Deployment", true},
}

// API is an API server of Kubernetes that keeps its resources in memory.
type API struct {
	Server *httptest.Server

	mu            sync.Mutex
	objects       map[string]map[string]map[string]interface{}
	requests      []Request
	watchRequests []Request
	failCode      int
	version       int64
	watchers      map[*watcher]struct{}
	done          chan struct{}
}

// watcher is a watch that is being served, which is told of the changes of what it watches.
type watcher struct {
	res       *resource
	namespace string
	name      string
	selector  string
	events    chan watchEvent
}

// watchEvent is an event of a watch, as the API server of Kubernetes writes it.
type watchEvent struct {
	Type   string                 `json:"type"`
	Object map[string]interface{} `json:"object"`
}

// New starts an API server that is stopped when the test is done.
func New(t *testing.T) *API {
	t.Helper()
	a := &API{
		objects:  map[string]map[string]map[string]interface{}{},
		watchers: map[*watcher]struct{}{},
		done:     make(chan struct{}),
	}
	a.Server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.Server.Close)
	// Closing the server waits for the requests that are being served, which the watches are until this ends them. This
	// is done before, as the cleanups are done in the reverse order.
	t.Cleanup(func() { close(a.done) })
	return a
}

// Install starts an API server, and makes it the one that the default client of k8s talks to until the test is done. The
// client that was switched to is put aside for that time, as it would be the one that is talked to otherwise.
func Install(t *testing.T) *API {
	t.Helper()
	a := New(t)
	c, err := k8sClient.NewK8sClientForConfig(a.URL(), "")
	if err != nil {
		t.Fatal(err)
	}
	old := k8sClient.Client
	oldCurrent := k8sClient.ReplaceCurrentClient(nil)
	k8sClient.Client = c
	t.Cleanup(func() {
		k8sClient.Client = old
		k8sClient.ReplaceCurrentClient(oldCurrent)
	})
	return a
}

// URL returns the base URL of the fake API server.
func (a *API) URL() string { return a.Server.URL }

// FailResources makes the API server answer every request for a resource with the status, until it is called with 0.
// Discovery is not affected.
func (a *API) FailResources(status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failCode = status
}

// Requests returns the requests for resources that were made, in order. Discovery is left out.
func (a *API) Requests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request{}, a.requests...)
}

// WatchRequests returns the requests for watches that were made, in order. They are not among Requests, so that the ones
// that informers make do not get in the way of what is looked for there.
func (a *API) WatchRequests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request{}, a.watchRequests...)
}

// ClearRequests forgets the requests that were made.
func (a *API) ClearRequests() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = nil
	a.watchRequests = nil
}

// Watchers tells how many watches are being served.
func (a *API) Watchers() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.watchers)
}

// find returns the served resource definition for the apiVersion and kind, or nil if it is not served.
func find(apiVersion, kind string) *resource {
	for i := range resources {
		r := &resources[i]
		if r.kind == kind && r.gv() == apiVersion {
			return r
		}
	}
	return nil
}

// gv returns the group/version string of the resource, which is just the version for the core group.
func (r *resource) gv() string {
	if r.group == "" {
		return r.version
	}
	return r.group + "/" + r.version
}

// prefix returns the URL path prefix of the resource's group, /api/{version} for core and /apis/{group}/{version} otherwise.
func (r *resource) prefix() string {
	if r.group == "" {
		return "/api/" + r.version
	}
	return "/apis/" + r.group + "/" + r.version
}

// collection returns the key under which the objects of the resource in the namespace are stored.
func (r *resource) collection(namespace string) string {
	return r.gv() + "|" + r.plural + "|" + namespace
}

// Put stores an object, as if it had been created. The kind of it has to be one of what the server serves.
func (a *API) Put(obj map[string]interface{}) {
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	r := find(apiVersion, kind)
	if r == nil {
		panic(fmt.Sprintf("k8stest: kind %s %s is not served", apiVersion, kind))
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	name, _ := meta["name"].(string)
	namespace, _ := meta["namespace"].(string)
	if !r.namespaced {
		namespace = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store(r, namespace, name, obj)
}

// Delete removes a stored object, as if it had been deleted, and tells the watches of it. It returns false when there
// was none.
func (a *API) Delete(apiVersion, kind, namespace, name string) bool {
	r := find(apiVersion, kind)
	if r == nil {
		return false
	}
	if !r.namespaced {
		namespace = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := r.collection(namespace)
	deleted := a.objects[key][name]
	if deleted == nil {
		return false
	}
	delete(a.objects[key], name)
	a.version++
	a.emit(r, namespace, name, "DELETED", deleted)
	return true
}

// Get returns a stored object, or nil.
func (a *API) Get(apiVersion, kind, namespace, name string) map[string]interface{} {
	r := find(apiVersion, kind)
	if r == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.objects[r.collection(namespace)][name]
}

// store saves the object, bumps the version and tells the watches that it was ADDED or MODIFIED; the caller must hold the lock.
func (a *API) store(r *resource, namespace, name string, obj map[string]interface{}) {
	key := r.collection(namespace)
	if a.objects[key] == nil {
		a.objects[key] = map[string]map[string]interface{}{}
	}
	_, existed := a.objects[key][name]
	a.objects[key][name] = obj
	a.version++
	if existed {
		a.emit(r, namespace, name, "MODIFIED", obj)
	} else {
		a.emit(r, namespace, name, "ADDED", obj)
	}
}

// emit tells the watches of the object what happened to it. A watch that does not keep up is not told, as the server does
// not wait for it.
func (a *API) emit(r *resource, namespace, name, eventType string, obj map[string]interface{}) {
	for w := range a.watchers {
		if w.accepts(r, namespace, name, obj) {
			select {
			case w.events <- watchEvent{Type: eventType, Object: obj}:
			default:
			}
		}
	}
}

// accepts tells if the watch is for the resource and covers the namespace, name and label selector of the object.
func (w *watcher) accepts(r *resource, namespace, name string, obj map[string]interface{}) bool {
	return w.res == r && (w.namespace == "" || w.namespace == namespace) && (w.name == "" || w.name == name) && matches(obj, w.selector)
}

// writeJSON writes v as a JSON response with the status code.
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeStatus writes a Kubernetes Status object, which is a Failure for codes of 400 and above.
func writeStatus(w http.ResponseWriter, code int, reason, message string) {
	status := "Failure"
	if code < 400 {
		status = "Success"
	}
	writeJSON(w, code, map[string]interface{}{
		"kind": "Status", "apiVersion": "v1", "status": status, "message": message, "reason": reason, "code": code,
	})
}

// serve is the HTTP handler of the fake server, which answers discovery, hands watches to serveWatch, and records and routes the requests for resources.
func (a *API) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch path {
	case "/api":
		writeJSON(w, 200, map[string]interface{}{"kind": "APIVersions", "versions": []string{"v1"}})
		return
	case "/apis":
		var groups []map[string]interface{}
		seen := map[string]bool{}
		for _, res := range resources {
			if res.group == "" || seen[res.group] {
				continue
			}
			seen[res.group] = true
			gv := map[string]string{"groupVersion": res.gv(), "version": res.version}
			groups = append(groups, map[string]interface{}{
				"name": res.group, "versions": []map[string]string{gv}, "preferredVersion": gv,
			})
		}
		writeJSON(w, 200, map[string]interface{}{"kind": "APIGroupList", "apiVersion": "v1", "groups": groups})
		return
	}
	for _, res := range resources {
		if path == res.prefix() {
			var list []map[string]interface{}
			for _, other := range resources {
				if other.gv() != res.gv() {
					continue
				}
				list = append(list, map[string]interface{}{
					"name": other.plural, "singularName": strings.TrimSuffix(other.plural, "s"), "namespaced": other.namespaced,
					"kind": other.kind, "verbs": []string{"create", "delete", "get", "list", "patch", "update", "watch"},
				})
			}
			writeJSON(w, 200, map[string]interface{}{"kind": "APIResourceList", "groupVersion": res.gv(), "resources": list})
			return
		}
	}
	if watch := r.URL.Query().Get("watch"); r.Method == "GET" && (watch == "true" || watch == "1") {
		a.serveWatch(w, r, path)
		return
	}
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, Request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), body})
	if a.failCode != 0 {
		writeStatus(w, a.failCode, "InternalError", "the server failed the request")
		return
	}
	for i := range resources {
		res := &resources[i]
		if !strings.HasPrefix(path, res.prefix()+"/") {
			continue
		}
		namespace, name, ok := parse(res, strings.TrimPrefix(path, res.prefix()+"/"))
		if ok {
			a.handle(w, r, res, namespace, name, body)
			return
		}
	}
	writeStatus(w, 404, "NotFound", "the server could not find the requested resource")
}

// parse splits what is after the prefix of the group, which is plural, namespaces/ns/plural, namespaces/name for the
// namespaces themselves, with the name of an object at the end.
func parse(res *resource, rest string) (namespace, name string, ok bool) {
	parts := strings.Split(rest, "/")
	if res.plural == "namespaces" {
		switch len(parts) {
		case 1:
			return "", "", parts[0] == "namespaces"
		case 2:
			return "", parts[1], parts[0] == "namespaces"
		}
		return "", "", false
	}
	switch {
	case len(parts) == 1 && parts[0] == res.plural:
		return "", "", true
	case len(parts) == 3 && parts[0] == "namespaces" && parts[2] == res.plural:
		return parts[1], "", true
	case len(parts) == 4 && parts[0] == "namespaces" && parts[2] == res.plural:
		return parts[1], parts[3], true
	}
	return "", "", false
}

// handle serves GET (get or list), PATCH/POST/PUT (store) and DELETE of a resource or collection from the in-memory objects.
func (a *API) handle(w http.ResponseWriter, r *http.Request, res *resource, namespace, name string, body []byte) {
	key := res.collection(namespace)
	switch r.Method {
	case "GET":
		if name != "" {
			if obj := a.objects[key][name]; obj != nil {
				writeJSON(w, 200, obj)
			} else {
				writeStatus(w, 404, "NotFound", fmt.Sprintf("%s %q not found", res.plural, name))
			}
			return
		}
		selector := r.URL.Query().Get("labelSelector")
		items := []interface{}{}
		var names []string
		for n := range a.objects[key] {
			names = append(names, n)
		}
		// A list without a namespace has the objects of all namespaces.
		if namespace == "" && res.namespaced {
			names = nil
			prefix := res.gv() + "|" + res.plural + "|"
			var keys []string
			for k := range a.objects {
				if strings.HasPrefix(k, prefix) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				var inner []string
				for n := range a.objects[k] {
					inner = append(inner, n)
				}
				sort.Strings(inner)
				for _, n := range inner {
					if matches(a.objects[k][n], selector) {
						items = append(items, a.objects[k][n])
					}
				}
			}
		} else {
			sort.Strings(names)
			for _, n := range names {
				if matches(a.objects[key][n], selector) {
					items = append(items, a.objects[key][n])
				}
			}
		}
		writeJSON(w, 200, map[string]interface{}{
			"kind": res.kind + "List", "apiVersion": res.gv(),
			"metadata": map[string]interface{}{"resourceVersion": strconv.FormatInt(a.version, 10)}, "items": items,
		})
	case "PATCH", "POST", "PUT":
		var obj map[string]interface{}
		if err := json.Unmarshal(body, &obj); err != nil {
			writeStatus(w, 400, "BadRequest", "the body is not an object: "+err.Error())
			return
		}
		if name == "" {
			meta, _ := obj["metadata"].(map[string]interface{})
			name, _ = meta["name"].(string)
		}
		a.store(res, namespace, name, obj)
		writeJSON(w, 200, obj)
	case "DELETE":
		if a.objects[key][name] == nil {
			writeStatus(w, 404, "NotFound", fmt.Sprintf("%s %q not found", res.plural, name))
			return
		}
		deleted := a.objects[key][name]
		delete(a.objects[key], name)
		a.version++
		a.emit(res, namespace, name, "DELETED", deleted)
		writeStatus(w, 200, "", "deleted")
	default:
		writeStatus(w, 405, "MethodNotAllowed", "method not allowed")
	}
}

// matches checks the labels of an object against a selector of the form k=v,k2=v2.
func matches(obj map[string]interface{}, selector string) bool {
	if selector == "" {
		return true
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	labels, _ := meta["labels"].(map[string]interface{})
	for _, term := range strings.Split(selector, ",") {
		kv := strings.SplitN(term, "=", 2)
		if len(kv) != 2 || labels[strings.TrimSuffix(kv[0], "=")] != kv[1] {
			return false
		}
	}
	return true
}

// serveWatch streams the events of the resources that the path is of, one JSON object on each line, until the client
// goes away, the time of the watch is over or the server is closed. The objects that exist are reported as added first,
// unless the watch is from a resource version, and the end of them is told with a bookmark when they are asked for.
func (a *API) serveWatch(w http.ResponseWriter, r *http.Request, path string) {
	var res *resource
	var namespace, name string
	for i := range resources {
		candidate := &resources[i]
		if !strings.HasPrefix(path, candidate.prefix()+"/") {
			continue
		}
		if ns, n, ok := parse(candidate, strings.TrimPrefix(path, candidate.prefix()+"/")); ok {
			res, namespace, name = candidate, ns, n
			break
		}
	}
	query := r.URL.Query()
	// A name can be given as a field selector, which is how the name of a resource is watched by clients.
	for _, term := range strings.Split(query.Get("fieldSelector"), ",") {
		if kv := strings.SplitN(term, "=", 2); len(kv) == 2 && kv[0] == "metadata.name" {
			name = kv[1]
		}
	}
	wt := &watcher{res: res, namespace: namespace, name: name, selector: query.Get("labelSelector"), events: make(chan watchEvent, 256)}
	sendInitial := query.Get("sendInitialEvents") == "true"
	resourceVersion := query.Get("resourceVersion")

	a.mu.Lock()
	a.watchRequests = append(a.watchRequests, Request{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery})
	if res == nil {
		a.mu.Unlock()
		writeStatus(w, 404, "NotFound", "the server could not find the requested resource")
		return
	}
	if a.failCode != 0 {
		code := a.failCode
		a.mu.Unlock()
		writeStatus(w, code, "InternalError", "the server failed the request")
		return
	}
	// The objects are taken and the watch is registered under the same lock, so that no change is missed in between.
	var initial []watchEvent
	if sendInitial || resourceVersion == "" || resourceVersion == "0" {
		initial = a.snapshot(wt)
	}
	version := a.version
	a.watchers[wt] = struct{}{}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.watchers, wt)
		a.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	enc := json.NewEncoder(w)
	send := func(event watchEvent) bool {
		if err := enc.Encode(event); err != nil {
			return false
		}
		flush()
		return true
	}
	flush()
	for _, event := range initial {
		if !send(event) {
			return
		}
	}
	if sendInitial {
		send(watchEvent{Type: "BOOKMARK", Object: map[string]interface{}{
			"kind": res.kind, "apiVersion": res.gv(),
			"metadata": map[string]interface{}{
				"resourceVersion": strconv.FormatInt(version, 10),
				"annotations":     map[string]interface{}{"k8s.io/initial-events-end": "true"},
			},
		}})
	}
	var timeout <-chan time.Time
	if seconds, err := strconv.Atoi(query.Get("timeoutSeconds")); err == nil && seconds > 0 {
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		defer timer.Stop()
		timeout = timer.C
	}
	for {
		select {
		case event := <-wt.events:
			if !send(event) {
				return
			}
		case <-timeout:
			return
		case <-r.Context().Done():
			return
		case <-a.done:
			return
		}
	}
}

// snapshot has the objects that a watch is of, as added events, in the order of their namespaces and names.
func (a *API) snapshot(wt *watcher) []watchEvent {
	prefix := wt.res.collection("")
	var keys []string
	for key := range a.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var events []watchEvent
	for _, key := range keys {
		namespace := strings.TrimPrefix(key, prefix)
		var names []string
		for name := range a.objects[key] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if obj := a.objects[key][name]; wt.accepts(wt.res, namespace, name, obj) {
				events = append(events, watchEvent{Type: "ADDED", Object: obj})
			}
		}
	}
	return events
}
