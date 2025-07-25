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

package hooks

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/mux"
)

var (
	Middleware         = middleware.NewMiddleware("hooks", setRoutes, middlewareFunc)
	HeaderTrackingFunc func(port int, key, uri string, matchedHeaders [][2]string)
)

type HookCallback func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool
type Headers [][2]string

type HeaderMatch struct {
	Header string
	re     *regexp.Regexp
	Values map[string]*regexp.Regexp
}

type HookMatch struct {
	UriPrefix     string
	re            *regexp.Regexp
	HeaderMatches map[string]*HeaderMatch
}

type Hook struct {
	Key       string
	ID        string
	IsGRPC    bool
	IsJSONRPC bool
	URI       string
	Match     *HookMatch
	callback  func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool
}

type Hooks struct {
	Hooks map[string]*Hook
	lock  sync.RWMutex
}

var (
	portHooks     = map[int]*Hooks{}
	hookIdCounter = atomic.Int32{}
	lock          = sync.RWMutex{}
)

func GetPortHooks(port int) *Hooks {
	if portHooks[port] == nil {
		portHooks[port] = &Hooks{
			Hooks: map[string]*Hook{},
			lock:  sync.RWMutex{},
		}
	}
	return portHooks[port]
}

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	hooksRouter := util.PathRouter(r, "/hooks")
	util.AddRoute(hooksRouter, "", getHooks, "GET")
}

func getHooks(w http.ResponseWriter, r *http.Request) {
	util.WriteYaml(w, portHooks)
}

func (h *Hooks) AddURIHookWithHandler(key, id, uri string, headers Headers, isGRPC, isJSONRPC bool, httpHandler middleware.MiddlewareFunc) error {
	if uri == "" && headers == nil {
		return fmt.Errorf("One of URI and Headers needed")
	}
	uri, uriPrefix, re, err := h.registerURI(uri, httpHandler)
	if err != nil {
		return err
	}
	h.addHook(key, id, uri, uriPrefix, re, headers, isGRPC, isJSONRPC, nil)
	return nil
}

func (h *Hooks) AddHookWithCallback(key, id, uri string, headers Headers, isGRPC, isJSONRPC bool, callback HookCallback) error {
	if uri == "" && headers == nil {
		return fmt.Errorf("One of URI and Headers needed")
	}
	uri, uriPrefix, re, err := h.registerURI(uri, nil)
	if err != nil {
		return err
	}
	h.addHook(key, id, uri, uriPrefix, re, headers, isGRPC, isJSONRPC, callback)
	return nil
}

func (h *Hooks) registerURI(uri string, httpHandler middleware.MiddlewareFunc) (luri, uriPrefix string, re *regexp.Regexp, err error) {
	luri = strings.ToLower(uri)
	if httpHandler != nil {
		uriPrefix, re, _, err = util.RegisterURIRouteAndGetRegex(luri, util.RootRouter, httpHandler)
	} else {
		uriPrefix, re, _, err = util.BuildURIMatcher(luri, func(w http.ResponseWriter, r *http.Request) {})
	}
	return
}

func (h *Hooks) addHook(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers, isGRPC, isJSONRPC bool, callback HookCallback) *Hook {
	hook := &Hook{}
	hook.init(key, id, uri, uriPrefix, reURI, headers, isGRPC, isJSONRPC, callback)
	h.lock.Lock()
	defer h.lock.Unlock()
	h.Hooks[hook.ID] = hook
	return hook
}

func (h *Hooks) RemoveHook(id string) {
	h.lock.Lock()
	defer h.lock.Unlock()
	if hook := h.Hooks[id]; hook != nil {
		delete(h.Hooks, id)
	}
}

func (h *Hooks) MatchRequest(r *http.Request) (allMatches map[string]*Hook, matchedHeaders map[string][][2]string) {
	h.lock.RLock()
	defer h.lock.RUnlock()
	allMatches = map[string]*Hook{}
	matchedHeaders = map[string][][2]string{}
	for id, hook := range h.Hooks {
		if hook.matchURI(r.RequestURI) {
			allMatches[id] = hook
		} else {
			continue
		}
		for header, hvalues := range r.Header {
			matches := hook.matchHeader(header, hvalues)
			if len(matches) > 0 {
				allMatches[id] = hook
				if matchedHeaders[hook.Key] == nil {
					matchedHeaders[hook.Key] = [][2]string{}
				}
				matchedHeaders[hook.Key] = append(matchedHeaders[hook.Key], matches...)
			}
		}
	}
	return allMatches, matchedHeaders
}

func (h *Hook) init(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers, isGRPC, isJSONRPC bool, callback HookCallback) {
	if id == "" {
		id = string(hookIdCounter.Add(1))
	}
	h.Key = key
	h.ID = id
	h.IsGRPC = isGRPC
	h.IsJSONRPC = isJSONRPC
	h.URI = uri
	h.Match = &HookMatch{
		UriPrefix: uriPrefix,
		re:        reURI,
	}
	h.callback = callback
	if len(headers) > 0 {
		h.Match.HeaderMatches = map[string]*HeaderMatch{}
	}
	for _, hv := range headers {
		header := strings.ToLower(hv[0])
		hm := h.Match.HeaderMatches[header]
		if hm == nil {
			hmatch, glob := util.Unglob(header)
			if glob {
				hmatch += "(.*)?"
			}
			hm = &HeaderMatch{
				Header: header,
				re:     regexp.MustCompile(hmatch),
				Values: map[string]*regexp.Regexp{},
			}
		}
		if hv[1] != "" {
			value := strings.ToLower(hv[1])
			vmatch, glob := util.Unglob(value)
			if glob {
				vmatch += "(.*)?"
			}
			hm.Values[value] = regexp.MustCompile(vmatch)
		}
		h.Match.HeaderMatches[header] = hm
	}
}

func (h *Hook) matchURI(uri string) bool {
	if h.Match.re != nil {
		if h.Match.re.MatchString(uri) {
			return true
		}
	}
	return false
}

func (h *Hook) matchHeader(header string, values []string) [][2]string {
	if h.Match.HeaderMatches == nil {
		return nil
	}
	matches := [][2]string{}
	header = strings.ToLower(header)
	for _, hm := range h.Match.HeaderMatches {
		if hm.re.MatchString(header) {
			if hm.Values == nil {
				matches = append(matches, [2]string{header, ""})
				continue
			}
		nextheader:
			for _, vre := range hm.Values {
				for _, hv := range values {
					if vre.MatchString(hv) {
						matches = append(matches, [2]string{header, hv})
						break nextheader
					}
				}
			}
		}
	}
	return matches
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if util.IsKnownNonTraffic(r) {
			if next != nil {
				next.ServeHTTP(w, r)
			}
			return
		}
		allMatches, matchedHeaders := GetPortHooks(util.GetRequestOrListenerPortNum(r)).MatchRequest(r)
		callNext := false
		if len(allMatches) > 0 {
			body := util.Read(r.Body)
			for _, hook := range allMatches {
				util.SetIsJSONRPC(r, hook.IsJSONRPC)
				if hook.IsGRPC {
					util.SetIsGRPC(r, true)
				}
				if hook.callback != nil {
					callNext = callNext || hook.callback(util.GetRequestOrListenerPortNum(r), r.RequestURI, r.Header, io.NopCloser(strings.NewReader(body)))
				}
			}
			r.Body = io.NopCloser(strings.NewReader(body))
			for key, headers := range matchedHeaders {
				HeaderTrackingFunc(util.GetRequestOrListenerPortNum(r), key, r.RequestURI, headers)
			}
		} else {
			callNext = true
		}
		if callNext && next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
