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

// Package apiserver is a mock Kubernetes REST API server that is served by the goto HTTP server, for clients that only
// read and write resources, which can be pointed at the port of goto as they are at a cluster.
//
// It is in the list of middlewares of goto (pkg/server/server.go), so its routes take over what the listener does with
// /api and /apis, which otherwise are echoed like any other path, but only for the requests whose host starts
// with the label HostKeyword ("k8s", e.g. k8s.example.com). The others are handled as if the mock was not there. Only /api and /apis
// are served; the version, health and probe URIs (/version, /healthz, /readyz, /livez) are not, as the mock is for
// testing the calls of a client that reads and writes resources.
//
// Resources are kept in memory, and the first list or get of a kind that has a sample responds with it. The requests are
// logged (method, URI, headers and body) with util.PrintRequest. The verbs are get, list, create, update, patch, delete
// and deletecollection; watches are not served.
//
// Routes (all of them for the group of the resource that is in the path, "" for the core resources of /api/v1):
//
//	Discovery: /api, /api/v1, /apis, /apis/{group}, /apis/{group}/{version}
//	Namespaced: /api/v1/namespaces/{ns}/{resource}[/{name}], /apis/{group}/{version}/namespaces/{ns}/{resource}[/{name}]
//	Cluster wide: /api/v1/{resource}[/{name}], /apis/{group}/{version}/{resource}[/{name}]
package apiserver

import (
	"net"
	"net/http"
	"strings"

	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	// HostKeyword is what the first label of the host of a request has to be (in any case, without the port) for the mock
	// to serve it, so that goto can be reached over many FQDNs while the mock is exposed only over those like
	// k8s.example.com. An empty keyword leaves the mock open to all the hosts.
	HostKeyword = "k8s"

	// Middleware has the routes of the mock K8s REST API (/api and /apis), and is in the list of middlewares of goto.
	Middleware = middleware.NewMiddleware("k8smock", setRoutes, nil)

	// globalStore is the in-memory resource store shared by all handlers.
	globalStore = NewResourceStore()
)

// isK8sHost tells if the first label of the host of the request is the keyword, i.e. if the host is k8s.xxx. The host is
// the Host header, or the host of the URL of a request that is in the absolute form, and not X-Forwarded-Host.
func isK8sHost(r *http.Request) bool {
	if HostKeyword == "" {
		return true
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	label, _, _ := strings.Cut(strings.ToLower(host), ".")
	return label == strings.ToLower(HostKeyword)
}

func setRoutes(r *mux.Router) {
	// ── Discovery endpoints ────────────────────────────────────────────────────
	// Each root segment (/api, /apis) is a separate RootRouter entry matched by httpserver.go via
	// middleware.RootRouterFor, which checks the guard, so a request for a host that is not a k8s one is not served by the
	// mock and goes the way it would without it.

	apiRouter := middleware.RootPathIf("/api", isK8sHost)
	util.AddRoute(apiRouter, "", handleAPIVersions, "GET")
	util.AddRoute(apiRouter, "/v1", handleCoreV1Resources, "GET")

	apisRouter := middleware.RootPathIf("/apis", isK8sHost)
	util.AddRoute(apisRouter, "", handleAPIGroupList, "GET")
	util.AddRoute(apisRouter, "/{group}", handleAPIGroupVersions, "GET")
	util.AddRoute(apisRouter, "/{group}/{version}", handleAPIGroupResources, "GET")

	// ── Core v1 — namespaced resources ────────────────────────────────────────
	// Must be registered before the cluster-scoped subrouter so that gorilla/mux
	// evaluates the more-specific prefix first.
	//
	// GET    /api/v1/namespaces/{namespace}/{resource}          → list
	// POST   /api/v1/namespaces/{namespace}/{resource}          → create
	// DELETE /api/v1/namespaces/{namespace}/{resource}          → deleteCollection
	// GET    /api/v1/namespaces/{namespace}/{resource}/{name}   → get
	// PUT    /api/v1/namespaces/{namespace}/{resource}/{name}   → update
	// PATCH  /api/v1/namespaces/{namespace}/{resource}/{name}   → patch
	// DELETE /api/v1/namespaces/{namespace}/{resource}/{name}   → delete
	coreV1NsRouter := util.PathRouter(apiRouter, "/v1/namespaces/{namespace}")
	util.AddRoute(coreV1NsRouter, "/{resource}", handleCollection, "GET", "POST", "DELETE")
	util.AddRoute(coreV1NsRouter, "/{resource}/{name}", handleItem, "GET", "PUT", "PATCH", "DELETE")

	// ── Core v1 — cluster-scoped / cross-namespace listing ────────────────────
	// GET    /api/v1/{resource}          → list (all namespaces for namespaced resources)
	// POST   /api/v1/{resource}          → create
	// DELETE /api/v1/{resource}          → deleteCollection
	// GET    /api/v1/{resource}/{name}   → get
	// PUT    /api/v1/{resource}/{name}   → update
	// PATCH  /api/v1/{resource}/{name}   → patch
	// DELETE /api/v1/{resource}/{name}   → delete
	coreV1Router := util.PathRouter(apiRouter, "/v1")
	util.AddRoute(coreV1Router, "/{resource}", handleCollection, "GET", "POST", "DELETE")
	util.AddRoute(coreV1Router, "/{resource}/{name}", handleItem, "GET", "PUT", "PATCH", "DELETE")

	// ── API groups — namespaced resources ─────────────────────────────────────
	// More-specific prefix registered first (see reasoning above).
	apisGroupNsRouter := util.PathRouter(apisRouter, "/{group}/{version}/namespaces/{namespace}")
	util.AddRoute(apisGroupNsRouter, "/{resource}", handleCollection, "GET", "POST", "DELETE")
	util.AddRoute(apisGroupNsRouter, "/{resource}/{name}", handleItem, "GET", "PUT", "PATCH", "DELETE")

	// ── API groups — cluster-scoped / cross-namespace listing ─────────────────
	apisGroupRouter := util.PathRouter(apisRouter, "/{group}/{version}")
	util.AddRoute(apisGroupRouter, "/{resource}", handleCollection, "GET", "POST", "DELETE")
	util.AddRoute(apisGroupRouter, "/{resource}/{name}", handleItem, "GET", "PUT", "PATCH", "DELETE")
}
