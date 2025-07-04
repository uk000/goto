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

package ctl

import (
	"fmt"
	"goto/pkg/k8s/ctl"
	"goto/pkg/k8s/store"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("k8s", SetRoutes, nil)
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	k8sYamlRouter := util.PathRouter(r, "/k8s/yaml")

	util.AddRoute(k8sYamlRouter, "/store/{yamlName}", apiStoreYaml, "POST")

	util.AddRoute(k8sYamlRouter, "/{a:apply|delete}/{yamlName}", apiApplyYaml, "POST")
	util.AddRouteQ(k8sYamlRouter, "/{a:apply|delete}/{yamlName}/to", apiApplyYaml, "ns", "POST")
	util.AddRoute(k8sYamlRouter, "/{a:apply|delete}/{yamlName}/{namespace}", apiApplyYaml, "POST")
	util.AddRoute(k8sYamlRouter, "/{a:apply|delete}/{yamlName}/{namespace}/{kind}", apiApplyYaml, "POST")
	util.AddRoute(k8sYamlRouter, "/{a:apply|delete}/{yamlName}/{namespace}/{kind}/{name}", apiApplyYaml, "POST")

	util.AddRoute(k8sYamlRouter, "", apiGetResources, "GET")
	util.AddRoute(k8sYamlRouter, "/{yamlName}", apiGetResources, "GET")
	util.AddRoute(k8sYamlRouter, "/{yamlName}/{namespace}", apiGetResources, "GET")
	util.AddRoute(k8sYamlRouter, "/{yamlName}/{namespace}/{kind}", apiGetResources, "GET")

	util.AddRoute(k8sYamlRouter, "/clear", apiClearResources, "POST")
	util.AddRoute(k8sYamlRouter, "/clear/{yamlName}", apiClearResources, "POST")
	util.AddRoute(k8sYamlRouter, "/clear/{yamlName}/{namespace}", apiClearResources, "POST")
}

func apiStoreYaml(w http.ResponseWriter, r *http.Request) {
	yamlName := util.GetStringParamValue(r, "yamlName")
	if list, errs := store.Kstore.StoreYaml(yamlName, r.Body); len(errs) == 0 {
		fmt.Fprintf(w, "Stored %d resources from Yaml\n", len(list))
		util.WriteYaml(w, list)
	} else {
		fmt.Fprintf(w, "Failed to store resources from Yaml with errors: %v\n", errs)
	}
}

func apiApplyYaml(w http.ResponseWriter, r *http.Request) {
	action := util.GetStringParamValue(r, "a")
	yamlName := util.GetStringParamValue(r, "yamlName")
	namespace := util.GetStringParamValue(r, "namespace")
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
	to := util.GetStringParamValue(r, "ns")
	if to == "" {
		to = namespace
	}
	var list []string
	var errs []error
	isDelete := action == "delete"
	list, errs = ctl.ApplyYaml(yamlName, namespace, kind, name, to, isDelete)
	if len(list) > 0 {
		if isDelete {
			fmt.Fprintf(w, "Successfully deleted %d resources: [%v] from namespace: [%s]\n", len(list), list, to)
		} else {
			fmt.Fprintf(w, "Successfully applied %d resources: [%v] to namespace: [%s]\n", len(list), list, to)
		}
	}
	if len(errs) > 0 {
		fmt.Fprintf(w, "Failed to apply %d resources. Error %v\n", len(errs), errs)
	}
}

func apiGetResources(w http.ResponseWriter, r *http.Request) {
	yamlName := util.GetStringParamValue(r, "yamlName")
	namespace := util.GetStringParamValue(r, "namespace")
	kind := util.GetStringParamValue(r, "kind")
	if result := store.Kstore.Get(yamlName, namespace, kind); result != nil {
		if strings.Contains(r.Header.Get("Accept"), "json") {
			util.WriteJson(w, result)
		} else {
			util.WriteYaml(w, result)
		}
	} else {
		fmt.Fprintf(w, "No resources found for yamlName [%s] and namespace [%s]\n", yamlName, namespace)
		w.WriteHeader(http.StatusNotFound)
	}
}

func apiClearResources(w http.ResponseWriter, r *http.Request) {
	yamlName := util.GetStringParamValue(r, "yamlName")
	namespace := util.GetStringParamValue(r, "namespace")
	store.Kstore.Clear(yamlName, namespace)
	fmt.Fprintln(w, "Cleared resources")
}
