/**
 * Copyright 2022 uk
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
  util.AddRoute(k8sRouter, "/clear", clearK8sCache, "POST")

  util.AddRoute(k8sRouter, "/{resource}", getUngroupedResource, "GET")
  util.AddRoute(k8sRouter, "/{resource}/{j:jq|jp}", getUngroupedResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}", getUngroupedResource, "GET")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}/{j:jq|jp}", getUngroupedResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}/{name:}", getUngroupedResource, "GET")
  util.AddRoute(k8sRouter, "/{resource}/{namespace}/{name}/{j:jq|jp}", getUngroupedResource, "POST", "PUT")

  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}", getResource, "GET")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{j:jq|jp}", getResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}", getResource, "GET")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}/{j:jq|jp}", getResource, "POST", "PUT")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}/{name}", getResource, "GET")
  util.AddRoute(k8sRouter, "/{group}/{version}/{kind}/{namespace}/{name}/{j:jq|jp}", getResource, "POST", "PUT")
}

func clearK8sCache(w http.ResponseWriter, r *http.Request) {
  ClearCache()
  fmt.Fprintln(w, "K8s Cache Cleared")
}

func getUngroupedResource(w http.ResponseWriter, r *http.Request) {
  resource := util.GetStringParamValue(r, "resource")
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
  if resource := GetResource("", "v1", resource, namespace, name, jp, jq, r); resource != nil {
    if strings.Contains(r.Header.Get("Accept"), "json") {
      fmt.Fprintln(w, resource.ToJSONText())
    } else {
      fmt.Fprintln(w, resource.ToYAML())
    }
  } else {
    util.AddLogMessage(fmt.Sprintf("Failed to serve Resource [%s/%s/%s]", resource, namespace, name), r)
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
      fmt.Fprintln(w, resource.ToJSONText())
    } else {
      fmt.Fprintln(w, resource.ToYAML())
    }
  } else {
    util.AddLogMessage(fmt.Sprintf("Failed to serve Resource [%s/%s/%s/%s/%s]", group, version, kind, namespace, name), r)
    w.WriteHeader(http.StatusInternalServerError)
  }
}
