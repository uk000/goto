/**
 * Copyright 2021 uk
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
  util.AddRoute(k8sRouter, "/{resource}", getUngroupedResource, "GET")
  util.AddRoute(k8sRouter, "/{resource}/{j}", getUngroupedResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}", getUngroupedResource, "GET")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}/{j}", getUngroupedResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}/{name}", getUngroupedResource, "GET")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}/{name}/{j}", getUngroupedResource, "POST", "PUT")

  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}", getResource, "GET")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{j}", getResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}", getResource, "GET")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}/{j}", getResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}/{name}", getResource, "GET")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}/{name}/{j}", getResource, "POST", "PUT")
}

func getUngroupedResource(w http.ResponseWriter, r *http.Request) {
  resource := util.GetStringParamValue(r, "resource")
  ns := resource == "ns" || resource == "namespace" || resource == "namespaces"
  pod := resource == "pod" || resource == "pods"
  svc := resource == "svc" || resource == "service" || resource == "services"
  namespace := util.GetStringParamValue(r, "namespace")
  name := util.GetStringParamValue(r, "name")
  j := util.GetStringParamValue(r, "j")
  var jp *util.JSONPath
  var jq *util.JQ
  if j == "jp" {
    jp = util.ParseJSONPathsFromRequest(r)
  } else if j == "jq" {
    jq = util.ParseJQFromRequest(r)
  }
  kind := ""
  if ns {
    kind = "namespace"
  } else if pod {
    kind = "pod"
  } else if svc {
    kind = "service"
  }
  if resource := GetResource("", "v1", kind, namespace, name, jp, jq, r); resource != nil {
    if strings.Contains(r.Header.Get("Accept"), "json") {
      fmt.Fprintln(w, resource.ToJSON())
    } else {
      fmt.Fprintln(w, resource.ToYAML())
    }
  } else {
    util.AddLogMessage(fmt.Sprintf("Failed to serve Resource [pods/%s/%s]", namespace, name), r)
    w.WriteHeader(http.StatusInternalServerError)
  }
}

func getResource(w http.ResponseWriter, r *http.Request) {
  group := util.GetStringParamValue(r, "group")
  version := util.GetStringParamValue(r, "version")
  kind := util.GetStringParamValue(r, "kind")
  namespace := util.GetStringParamValue(r, "namespace")
  name := util.GetStringParamValue(r, "name")
  j := util.GetStringParamValue(r, "j")
  var jp *util.JSONPath
  var jq *util.JQ
  if j == "jp" {
    jp = util.ParseJSONPathsFromRequest(r)
  } else if j == "jq" {
    jq = util.ParseJQFromRequest(r)
  }
  if resource := GetResource(group, version, kind, namespace, name, jp, jq, r); resource != nil {
    if strings.Contains(r.Header.Get("Accept"), "json") {
      fmt.Fprintln(w, resource.ToJSON())
    } else {
      fmt.Fprintln(w, resource.ToYAML())
    }
  } else {
    util.AddLogMessage(fmt.Sprintf("Failed to serve Resource [%s/%s/%s/%s/%s]", group, version, kind, namespace, name), r)
    w.WriteHeader(http.StatusInternalServerError)
  }
}
