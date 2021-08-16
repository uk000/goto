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

package util

import (
  "fmt"
  "net/http"
  "regexp"
  "strconv"
  "strings"
  "time"

  "github.com/gorilla/mux"
)

type ServerHandler struct {
  Name       string
  SetRoutes  func(r *mux.Router, parent *mux.Router, root *mux.Router)
  Middleware mux.MiddlewareFunc
}

var (
  portRouter            *mux.Router
  portTunnelRouters     = map[string]*mux.Router{}
  coRoutersMap          = map[*mux.Router][]*mux.Router{}
  fillerRegexp          = regexp.MustCompile("{({[^{}]+?})}|{([^{}]+?)}")
  optionalPathRegexp    = regexp.MustCompile("(\\/[^{}]+?\\?)")
  optionalPathKeyRegexp = regexp.MustCompile("(\\/(?:[^\\/{}]+=)?{[^{}]+?}\\?\\??)")
)

func GetSubPaths(path string, key bool) []string {
  var matches [][]int
  if key {
    matches = optionalPathKeyRegexp.FindAllStringIndex(path, -1)
  } else {
    matches = optionalPathRegexp.FindAllStringIndex(path, -1)
  }
  var paths []string
  addSubpath := func(subPath string) {
    canSkip := strings.HasSuffix(subPath, "?") && !strings.HasSuffix(subPath, "??")
    subPath = strings.ReplaceAll(subPath, "?", "")
    if len(paths) > 0 {
      for i, prefixPath := range paths {
        if canSkip {
          paths = append(paths, prefixPath)
        }
        paths[i] = prefixPath + subPath
      }
    } else {
      paths = append(paths, subPath)
    }
  }
  if len(matches) > 0 {
    start := 0
    end := 0
    for _, m := range matches {
      addSubpath(path[start:m[0]])
      addSubpath(path[m[0]:m[1]])
      start = m[1]
      end = m[1]
    }
    if end < len(path) {
      addSubpath(path[end:])
    }
    for _, prefixPath := range paths {
      paths = append(paths, prefixPath+"/")
    }
  } else {
    paths = []string{path}
  }
  return paths
}

func PathRouter(r *mux.Router, path string) *mux.Router {
  routerPath := path
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil {
    routerPath = lpath + path
  }
  portTunnelRouters[routerPath] = portRouter.PathPrefix(routerPath).Subrouter()
  pathRouter := PathPrefix(r, path)
  for _, coRouter := range coRoutersMap[r] {
    coRoutersMap[pathRouter] = append(coRoutersMap[pathRouter], PathPrefix(coRouter, path))
  }
  return pathRouter
}

func PathPrefix(r *mux.Router, path string) *mux.Router {
  var subRouter *mux.Router
  for _, p := range GetSubPaths(path, false) {
    if subRouter == nil {
      subRouter = r.PathPrefix(p).Subrouter()
      for _, coRouter := range coRoutersMap[r] {
        coRoutersMap[subRouter] = append(coRoutersMap[coRouter], coRouter.PathPrefix(p).Subrouter())
      }
    } else {
      coRoutersMap[subRouter] = append(coRoutersMap[subRouter], r.PathPrefix(p).Subrouter())
    }
  }
  return subRouter
}

func AddRoute(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
  for _, p := range GetSubPaths(path, true) {
    if len(methods) > 0 {
      r.HandleFunc(p, f).Methods(methods...)
      for _, coRouter := range coRoutersMap[r] {
        coRouter.HandleFunc(p, f).Methods(methods...)
      }
    } else {
      r.HandleFunc(p, f)
      for _, coRouter := range coRoutersMap[r] {
        coRouter.HandleFunc(p, f)
      }
    }
  }
}

func AddRouteWithPort(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
  AddRoute(r, path, f, methods...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && portTunnelRouters[lpath] != nil {
    AddRoute(portTunnelRouters[lpath], path, f, methods...)
  }
}

func AddRouteQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, methods ...string) {
  queryKey := fmt.Sprintf("{%s}", queryParam)
  for _, p := range GetSubPaths(path, true) {
    r.HandleFunc(p, f).Queries(queryParam, queryKey).Methods(methods...)
    for _, coRouter := range coRoutersMap[r] {
      coRouter.HandleFunc(p, f).Queries(queryParam, queryKey).Methods(methods...)
    }
  }
}

func AddRouteQWithPort(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, methods ...string) {
  AddRouteQ(r, path, f, queryParam, methods...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && portTunnelRouters[lpath] != nil {
    AddRouteQ(portTunnelRouters[lpath], path, f, queryParam, methods...)
  }
}

func AddRouteMultiQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  queryParamPairs := []string{}
  for _, q := range queryParams {
    queryParamPairs = append(queryParamPairs, q, fmt.Sprintf("{%s}", q))
  }
  for _, p := range GetSubPaths(path, true) {
    r.HandleFunc(p, f).Queries(queryParamPairs...).Methods(method)
    for _, coRouter := range coRoutersMap[r] {
      coRouter.HandleFunc(p, f).Queries(queryParamPairs...).Methods(method)
    }
    for i := 0; i < len(queryParamPairs); i += 2 {
      for j := i + 2; j < len(queryParamPairs); j += 2 {
        r.HandleFunc(p, f).Queries(queryParamPairs[i], queryParamPairs[i+1], queryParamPairs[j], queryParamPairs[j+1]).Methods(method)
        for _, coRouter := range coRoutersMap[r] {
          coRouter.HandleFunc(p, f).Queries(queryParamPairs[i], queryParamPairs[i+1], queryParamPairs[j], queryParamPairs[j+1]).Methods(method)
        }
      }
    }
    for i := 0; i < len(queryParamPairs); i += 2 {
      r.HandleFunc(p, f).Queries(queryParamPairs[i], queryParamPairs[i+1]).Methods(method)
      for _, coRouter := range coRoutersMap[r] {
        coRouter.HandleFunc(p, f).Queries(queryParamPairs[i], queryParamPairs[i+1]).Methods(method)
      }
    }
    r.HandleFunc(p, f).Methods(method)
    for _, coRouter := range coRoutersMap[r] {
      coRouter.HandleFunc(p, f).Methods(method)
    }
  }
}

func AddRouteMultiQWithPort(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  AddRouteMultiQ(r, path, f, method, queryParams...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && portTunnelRouters[lpath] != nil {
    AddRouteMultiQ(portTunnelRouters[lpath], path, f, method, queryParams...)
  }
}

func AddRoutes(r *mux.Router, parent *mux.Router, root *mux.Router, handlers ...ServerHandler) {
  for _, h := range handlers {
    if h.SetRoutes != nil {
      h.SetRoutes(r, parent, root)
    }
  }
}

func AddMiddlewares(next http.Handler, handlers ...ServerHandler) http.Handler {
  handler := next
  for i := len(handlers) - 1; i >= 0; i-- {
    if handlers[i].Middleware != nil {
      handler = handlers[i].Middleware(handler)
    }
  }
  return handler
}

func IsFiller(key string) bool {
  return fillerRegexp.MatchString(key)
}

func GetFillerMarked(key string) string {
  return "{" + key + "}"
}

func GetFillers(text string) []string {
  return fillerRegexp.FindAllString(text, -1)
}

func GetFiller(text string) (string, bool) {
  fillers := GetFillers(text)
  if len(fillers) > 0 {
    return fillers[0], true
  }
  return "", false
}

func GetFillersUnmarked(text string) []string {
  matches := GetFillers(text)
  for i, m := range matches {
    m = strings.TrimPrefix(m, "{")
    matches[i] = strings.TrimSuffix(m, "}")
  }
  return matches
}

func GetFillerUnmarked(text string) (string, bool) {
  fillers := GetFillersUnmarked(text)
  if len(fillers) > 0 {
    return fillers[0], true
  }
  return "", false
}

func RegisterURIRouteAndGetRegex(uri string, glob bool, router *mux.Router, handler func(http.ResponseWriter, *http.Request)) (*mux.Router, *regexp.Regexp, error) {
  if uri != "" {
    vars := fillerRegexp.FindAllString(uri, -1)
    for _, v := range vars {
      v2, _ := GetFillerUnmarked(v)
      v2 = GetFillerMarked(v2 + ":[^/&\\?]*")
      uri = strings.ReplaceAll(uri, v, v2)
    }
    subRouter := router.NewRoute().Subrouter()
    route := subRouter.PathPrefix(uri)
    if path, err := route.GetPathRegexp(); err == nil {
      //path = strings.ReplaceAll(path, "$", "(/.*)?$")
      pattern := path
      if glob {
        pattern += "(.*)?"
      }
      pattern += "(\\?.*)?$"
      re := regexp.MustCompile(pattern)
      route = route.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
        return re.MatchString(r.URL.Path)
      }).HandlerFunc(handler)
      return subRouter, re, nil
    } else {
      return nil, nil, err
    }
  }
  return nil, nil, fmt.Errorf("Empty URI")
}

func GetIntParam(r *http.Request, param string, defaultVal ...int) (int, bool) {
  vars := mux.Vars(r)
  switch {
  case len(vars[param]) > 0:
    s, _ := strconv.ParseInt(vars[param], 10, 32)
    return int(s), true
  case len(defaultVal) > 0:
    return defaultVal[0], false
  default:
    return 0, false
  }
}

func GetIntParamValue(r *http.Request, param string, defaultVal ...int) int {
  val, _ := GetIntParam(r, param, defaultVal...)
  return val
}

func GetStringParam(r *http.Request, param string, defaultVal ...string) (string, bool) {
  vars := mux.Vars(r)
  switch {
  case len(vars[param]) > 0:
    return vars[param], true
  case len(defaultVal) > 0:
    return defaultVal[0], false
  default:
    return "", false
  }
}

func GetStringParamValue(r *http.Request, param string, defaultVal ...string) string {
  val, _ := GetStringParam(r, param, defaultVal...)
  return val
}

func GetBoolParamValue(r *http.Request, param string, defaultVal ...bool) bool {
  val, _ := GetStringParam(r, param)
  if val != "" {
    return IsYes(val)
  }
  if len(defaultVal) > 0 {
    return defaultVal[0]
  }
  return false
}

func GetListParam(r *http.Request, param string) ([]string, bool) {
  values := []string{}
  if v, present := GetStringParam(r, param); present {
    values = strings.Split(v, ",")
  }
  return values, len(values) > 0 && len(values[0]) > 0
}

func GetStatusParam(r *http.Request) (statusCodes []int, times int, present bool) {
  vars := mux.Vars(r)
  status := vars["status"]
  if len(status) == 0 {
    return nil, 0, false
  }
  pieces := strings.Split(status, ":")
  if len(pieces[0]) > 0 {
    for _, s := range strings.Split(pieces[0], ",") {
      if sc, err := strconv.ParseInt(s, 10, 32); err == nil {
        statusCodes = append(statusCodes, int(sc))
      }
    }
    if len(pieces) > 1 {
      s, _ := strconv.ParseInt(pieces[1], 10, 32)
      times = int(s)
    }
  }
  return statusCodes, times, true
}

func ParseSize(value string) int {
  size := 0
  multiplier := 1
  if len(value) == 0 {
    return 0
  }
  for k, v := range sizes {
    if strings.Contains(value, k) {
      multiplier = int(v)
      value = strings.Split(value, k)[0]
      break
    }
  }
  if len(value) > 0 {
    s, _ := strconv.ParseInt(value, 10, 32)
    size = int(s)
  } else {
    size = 1
  }
  size = size * multiplier
  return size
}

func GetSizeParam(r *http.Request, name string) int {
  return ParseSize(mux.Vars(r)[name])
}

func ParseDuration(value string) time.Duration {
  if d, err := time.ParseDuration(value); err == nil {
    return d
  }
  return 0
}

func GetDurationParam(r *http.Request, name string) (low, high time.Duration, count int, ok bool) {
  if val := mux.Vars(r)[name]; val != "" {
    dRangeAndCount := strings.Split(val, ":")
    dRange := strings.Split(dRangeAndCount[0], "-")
    if d, err := time.ParseDuration(dRange[0]); err != nil {
      return 0, 0, 0, false
    } else {
      low = d
    }
    if len(dRange) > 1 {
      if d, err := time.ParseDuration(dRange[1]); err == nil {
        if d < low {
          high = low
          low = d
        } else {
          high = d
        }
      }
    } else {
      high = low
    }
    if len(dRangeAndCount) > 1 {
      if c, err := strconv.ParseInt(dRangeAndCount[1], 10, 32); err == nil {
        if c > 0 {
          count = int(c)
        }
      }
    }
    return low, high, count, true
  }
  return 0, 0, 0, false
}

func ParseJSONPathsFromRequest(r *http.Request) *JSONPath {
  paths, ok := GetListParam(r, "paths")
  if !ok {
    paths = strings.Split(Read(r.Body), "\n")
  }
  if len(paths) > 0 {
    return NewJSONPaths().Parse(paths)
  }
  return nil
}

func ParseJQFromRequest(r *http.Request) *JQ {
  paths, ok := GetListParam(r, "paths")
  if !ok {
    paths = strings.Split(Read(r.Body), "\n")
  }
  if len(paths) > 0 {
    return NewJQ().Parse(paths)
  }
  return nil
}
