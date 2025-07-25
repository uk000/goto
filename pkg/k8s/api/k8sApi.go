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

package k8sApi

import (
	"fmt"
	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/store"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("k8s", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	k8sRouter := util.PathRouter(r, "/k8s")

	util.AddRoute(k8sRouter, "/config/{name}/{url}/{cadata}", apiConfigCluster, "POST")
	util.AddRoute(k8sRouter, "/context/{name}", apiSetContext, "POST")
	util.AddRouteQ(k8sRouter, "/resources/{kind}", apiGetResource, "jq", "GET")
	util.AddRouteQ(k8sRouter, "/resources/{kind}", apiGetResource, "jp", "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{name}", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/all", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/{name}", apiGetResource, "GET")
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
	if strings.Contains(r.RequestURI, "jp") {
		jp = util.ParseJSONPathsFromRequest("jp", r)
	} else if strings.Contains(r.RequestURI, "jq") {
		jq = util.ParseJQFromRequest("jq", r)
	}
	gvk, json, err := store.GetResource(kind, namespace, name, jp, jq, r)
	sendResourceResponse(gvk.Group, gvk.Version, gvk.Kind, namespace, name, json, err, w, r)
}

func sendResourceResponse(group, version, kind, namespace, name string, json util.JSON, err error, w http.ResponseWriter, r *http.Request) {
	if json != nil {
		if strings.Contains(r.Header.Get("Accept"), "json") {
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
