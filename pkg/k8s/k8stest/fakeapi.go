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
// discovery and the resources (list, get, apply and delete) of a few kinds, on a listener of the loopback interface,
// and makes the k8s client of the package the one that talks to it. It is not used by the code of the server.
package k8stest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

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

	mu       sync.Mutex
	objects  map[string]map[string]map[string]interface{}
	requests []Request
	failCode int
}

// New starts an API server that is stopped when the test is done.
func New(t *testing.T) *API {
	t.Helper()
	a := &API{objects: map[string]map[string]map[string]interface{}{}}
	a.Server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.Server.Close)
	return a
}

// Install starts an API server, and makes it the one that the default client of k8s talks to until the test is done.
func Install(t *testing.T) *API {
	t.Helper()
	a := New(t)
	c, err := k8sClient.NewK8sClientForConfig(a.URL(), "")
	if err != nil {
		t.Fatal(err)
	}
	old := k8sClient.Client
	k8sClient.Client = c
	t.Cleanup(func() { k8sClient.Client = old })
	return a
}

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

// ClearRequests forgets the requests that were made.
func (a *API) ClearRequests() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = nil
}

func find(apiVersion, kind string) *resource {
	for i := range resources {
		r := &resources[i]
		if r.kind == kind && r.gv() == apiVersion {
			return r
		}
	}
	return nil
}

func (r *resource) gv() string {
	if r.group == "" {
		return r.version
	}
	return r.group + "/" + r.version
}

func (r *resource) prefix() string {
	if r.group == "" {
		return "/api/" + r.version
	}
	return "/apis/" + r.group + "/" + r.version
}

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

func (a *API) store(r *resource, namespace, name string, obj map[string]interface{}) {
	key := r.collection(namespace)
	if a.objects[key] == nil {
		a.objects[key] = map[string]map[string]interface{}{}
	}
	a.objects[key][name] = obj
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeStatus(w http.ResponseWriter, code int, reason, message string) {
	status := "Failure"
	if code < 400 {
		status = "Success"
	}
	writeJSON(w, code, map[string]interface{}{
		"kind": "Status", "apiVersion": "v1", "status": status, "message": message, "reason": reason, "code": code,
	})
}

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
		writeJSON(w, 200, map[string]interface{}{"kind": res.kind + "List", "apiVersion": res.gv(), "metadata": map[string]interface{}{}, "items": items})
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
		delete(a.objects[key], name)
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
