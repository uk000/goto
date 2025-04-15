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

package k8s

import (
	"fmt"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Handler = util.ServerHandler{Name: "k8s", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	k8sRouter := util.PathRouter(r, "/k8s")

	util.AddRoute(k8sRouter, "/ctl", apiGetResources, "GET")
	util.AddRoute(k8sRouter, "/ctl/{unit}", apiGetResources, "GET")
	util.AddRoute(k8sRouter, "/ctl/{unit}/{namespace}", apiGetResources, "GET")
	util.AddRoute(k8sRouter, "/ctl/{unit}/{namespace}/{kind}", apiGetResources, "GET")

	util.AddRoute(k8sRouter, "/ctl/clear", apiClearResources, "POST")
	util.AddRoute(k8sRouter, "/ctl/clear/{unit}", apiClearResources, "POST")
	util.AddRoute(k8sRouter, "/ctl/clear/{unit}/{namespace}", apiClearResources, "POST")

	util.AddRoute(k8sRouter, "/ctl/store/{unit}", apiStoreYaml, "POST")

	util.AddRoute(k8sRouter, "/ctl/{a:apply|delete}/{unit}", apiApplyYaml, "POST")
	util.AddRoute(k8sRouter, "/ctl/{a:apply|delete}/{unit}/{namespace}", apiApplyYaml, "POST")
	util.AddRoute(k8sRouter, "/ctl/{a:apply|delete}/{unit}/{namespace}/{kind}", apiApplyYaml, "POST")
	util.AddRoute(k8sRouter, "/ctl/{a:apply|delete}/{unit}/{namespace}/{kind}/{name}", apiApplyYaml, "POST")

	util.AddRoute(k8sRouter, "/clear", apiClearK8sCache, "POST")
	util.AddRoute(k8sRouter, "/resources/{kind}", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{j:jq|jp}", apiGetResource, "POST")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/{j:jq|jp}", apiGetResource, "POST")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/{name}", apiGetResource, "GET")
	util.AddRoute(k8sRouter, "/resources/{kind}/{namespace}/{name}/{j:jq|jp}", apiGetResource, "POST")
}

func apiGetResources(w http.ResponseWriter, r *http.Request) {
	unit := util.GetStringParamValue(r, "unit")
	namespace := util.GetStringParamValue(r, "namespace")
	kind := util.GetStringParamValue(r, "kind")
	if result := kstore.get(unit, namespace, kind); result != nil {
		if strings.Contains(r.Header.Get("Accept"), "json") {
			util.WriteJson(w, result)
		} else {
			util.WriteYaml(w, result)
		}
	} else {
		fmt.Fprintf(w, "No resources found for unit [%s] and namespace [%s]\n", unit, namespace)
		w.WriteHeader(http.StatusNotFound)
	}
}

func apiClearResources(w http.ResponseWriter, r *http.Request) {
	unit := util.GetStringParamValue(r, "unit")
	namespace := util.GetStringParamValue(r, "namespace")
	kstore.clear(unit, namespace)
	fmt.Fprintln(w, "Cleared resources")
}

func apiStoreYaml(w http.ResponseWriter, r *http.Request) {
	unit := util.GetStringParamValue(r, "unit")
	if list, errs := kstore.storeYaml(unit, r.Body); len(errs) == 0 {
		fmt.Fprintf(w, "Stored %d resources from Yaml\n", len(list))
		util.WriteYaml(w, list)
	} else {
		fmt.Fprintf(w, "Failed to store resources from Yaml with errors: %v\n", errs)
	}
}

func apiApplyYaml(w http.ResponseWriter, r *http.Request) {
	action := util.GetStringParamValue(r, "a")
	unit := util.GetStringParamValue(r, "unit")
	namespace := util.GetStringParamValue(r, "namespace")
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
	var list []string
	var errs []error
	isDelete := action == "delete"
	list, errs = kstore.applyYaml(unit, namespace, kind, name, isDelete)
	if len(list) > 0 {
		if isDelete {
			fmt.Fprintf(w, "Successfully deleted %d resources: %v\n", len(list), list)
		} else {
			fmt.Fprintf(w, "Successfully applied %d resources: %v\n", len(list), list)
		}
	}
	if len(errs) > 0 {
		fmt.Fprintf(w, "Failed to apply %d resources. Error %v\n", len(errs), errs)
	}
}

func apiClearK8sCache(w http.ResponseWriter, r *http.Request) {
	k8sCache.clear()
	fmt.Fprintln(w, "K8s Cache Cleared")
}

func apiGetResource(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	namespace := util.GetStringParamValue(r, "namespace")
	name := util.GetStringParamValue(r, "name")
	j := util.GetStringParamValue(r, "j")
	var jp *util.JSONPath
	var jq *util.JQ
	if j == "jp" || util.RequestHasParam(r, "jp") {
		jp = util.ParseJSONPathsFromRequest("jp", r)
	} else if j == "jq" || util.RequestHasParam(r, "jq") {
		jq = util.ParseJQFromRequest("jq", r)
	}
	gvk, json, err := GetResource(kind, namespace, name, jp, jq, r)
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
