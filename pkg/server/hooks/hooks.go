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
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

var (
	Middleware         = middleware.NewMiddleware("hooks", setRoutes, middlewareFunc)
	HeaderTrackingFunc func(port int, key, uri string, matchedHeaders [][2]string)
)

type HTTPListener func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool
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

type GRPCListener interface {
	clientStream() chan proto.Message
	serverStream() chan proto.Message
	onClientHeaders(metadata.MD)
	onServerHeaders(metadata.MD)
}

type Hook struct {
	Key           string
	ID            string
	IsJSONRPC     bool
	URI           string
	Match         *HookMatch
	httpListener  HTTPListener
	grpcListener  GRPCListener
	streamIndexes map[int]bool
}

type GRPCHooks struct {
	hooks                map[string]*Hook
	listeners            map[int]map[string]GRPCListener
	activeStreamsByURI   map[string]int
	activeStreamsByIndex map[int]string
	clientStreams        []reflect.SelectCase
	serverStreams        []reflect.SelectCase
	serverHeaders        []reflect.SelectCase
}

type Hooks struct {
	port      int
	httpHooks map[string]*Hook
	grpcHooks *GRPCHooks
	lock      sync.RWMutex
}

var (
	portHooks     = map[int]*Hooks{}
	hookIdCounter = atomic.Int32{}
	lock          = sync.RWMutex{}
)

func GetPortHooks(port int) *Hooks {
	if portHooks[port] == nil {
		lock.Lock()
		portHooks[port] = newHooks(port)
		lock.Unlock()
	}
	lock.RLock()
	defer lock.RUnlock()
	return portHooks[port]
}

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	hooksRouter := util.PathRouter(r, "/hooks")
	util.AddRoute(hooksRouter, "", getHooks, "GET")
}

func getHooks(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	defer lock.RUnlock()
	util.WriteYaml(w, portHooks)
}

func newHooks(port int) *Hooks {
	hooks := &Hooks{
		port:      port,
		httpHooks: map[string]*Hook{},
		grpcHooks: &GRPCHooks{
			hooks:                map[string]*Hook{},
			activeStreamsByURI:   map[string]int{},
			activeStreamsByIndex: map[int]string{},
			listeners:            map[int]map[string]GRPCListener{},
			clientStreams:        []reflect.SelectCase{},
			serverStreams:        []reflect.SelectCase{},
			serverHeaders:        []reflect.SelectCase{},
		},
		lock: sync.RWMutex{},
	}
	go hooks.monitorGRPCStreams(true)
	go hooks.monitorGRPCStreams(false)
	go hooks.monitorGRPCHeaders()
	return hooks
}

func (h *Hooks) monitorGRPCStreams(client bool) {
	for {
		var streams []reflect.SelectCase
		if client {
			streams = h.grpcHooks.clientStreams
		} else {
			streams = h.grpcHooks.serverStreams
		}
		if len(streams) == 0 {
			time.Sleep(5 * time.Second)
			continue
		}
		index, val, ok := reflect.Select(streams)
		if !ok {
			h.RemoveGRPCStream("", index)
			continue
		}
		if msg, ok := val.Interface().(proto.Message); ok {
			h.lock.RLock()
			listeners := h.grpcHooks.listeners[index]
			h.lock.RUnlock()
			for _, l := range listeners {
				if client {
					l.clientStream() <- msg
				} else {
					l.serverStream() <- msg
				}
			}
		}
	}
}

func (h *Hooks) monitorGRPCHeaders() {
	for {
		headersChans := h.grpcHooks.serverHeaders
		if len(headersChans) == 0 {
			time.Sleep(5 * time.Second)
			continue
		}
		index, val, ok := reflect.Select(headersChans)
		if !ok {
			h.RemoveGRPCStream("", index)
			continue
		}
		if md, ok := val.Interface().(metadata.MD); ok {
			h.lock.RLock()
			listeners := h.grpcHooks.listeners[index]
			h.lock.RUnlock()
			for _, l := range listeners {
				l.onServerHeaders(md)
			}
		}
	}
}

func (h *Hooks) AddGRPCHook(key, id, methodURI string, grpcListener GRPCListener) error {
	if methodURI == "" {
		return fmt.Errorf("URI needed")
	}
	uriPrefix, re, _, _, err := util.GetURIRegexpAndRoute(methodURI, util.RootRouter)
	if err != nil {
		return err
	}
	h.addGRPCHook(key, id, methodURI, uriPrefix, re, grpcListener)
	return nil
}

func (h *Hooks) AddHTTPHookWithHandler(key, id, uri string, headers Headers, isJSONRPC bool, httpHandler middleware.MiddlewareFunc) error {
	if uri == "" && headers == nil {
		return fmt.Errorf("One of URI and Headers needed")
	}
	uri, uriPrefix, re, err := h.registerURI(uri, httpHandler)
	if err != nil {
		return err
	}
	h.addHTTPHook(key, id, uri, uriPrefix, re, headers, isJSONRPC, nil)
	return nil
}

func (h *Hooks) AddHTTPHookWithListener(key, id, uri string, headers Headers, isJSONRPC bool, listener HTTPListener) error {
	if uri == "" && headers == nil {
		return fmt.Errorf("One of URI and Headers needed")
	}
	uri, uriPrefix, re, err := h.registerURI(uri, nil)
	if err != nil {
		return err
	}
	h.addHTTPHook(key, id, uri, uriPrefix, re, headers, isJSONRPC, listener)
	return nil
}

func (h *Hooks) registerURI(uri string, httpHandler middleware.MiddlewareFunc) (luri, uriPrefix string, re *regexp.Regexp, err error) {
	luri = strings.ToLower(uri)
	if httpHandler != nil {
		uriPrefix, re, _, err = util.BuildURIMatcher(luri, httpHandler)
	} else {
		uriPrefix, re, _, err = util.BuildURIMatcher(luri, func(w http.ResponseWriter, r *http.Request) {})
	}
	return
}

func (h *Hooks) addHTTPHook(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers, isJSONRPC bool, listener HTTPListener) *Hook {
	hook := newHook()
	hook.initHTTP(key, id, uri, uriPrefix, reURI, headers, isJSONRPC, listener)
	h.lock.Lock()
	defer h.lock.Unlock()
	h.httpHooks[hook.ID] = hook
	return hook
}

func (h *Hooks) addGRPCHook(key, id, uri, uriPrefix string, reURI *regexp.Regexp, grpcListener GRPCListener) *Hook {
	hook := newHook()
	hook.init(key, id, uri, uriPrefix, reURI, nil)
	h.lock.Lock()
	defer h.lock.Unlock()
	hook.grpcListener = grpcListener
	h.grpcHooks.hooks[hook.ID] = hook
	for methodURI, index := range h.grpcHooks.activeStreamsByURI {
		if hook.match(methodURI, nil, nil, nil) {
			hook.streamIndexes[index] = true
			if h.grpcHooks.listeners[index] == nil {
				h.grpcHooks.listeners[index] = map[string]GRPCListener{}
			}
			h.grpcHooks.listeners[index][hook.ID] = hook.grpcListener
		}
	}
	return hook
}

func (h *Hooks) AddGRPCStream(methodURI string, md metadata.MD, clientStream, serverStream chan proto.Message, serverHeaders chan metadata.MD) {
	h.lock.Lock()
	defer h.lock.Unlock()
	index := len(h.grpcHooks.clientStreams)
	if clientStream == nil {
		clientStream = make(chan proto.Message)
	}
	h.grpcHooks.clientStreams = append(h.grpcHooks.clientStreams, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(clientStream),
	})
	if serverStream == nil {
		serverStream = make(chan proto.Message)
	}
	h.grpcHooks.serverStreams = append(h.grpcHooks.serverStreams, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(serverStream),
	})
	if serverHeaders == nil {
		serverHeaders = make(chan metadata.MD)
	}
	h.grpcHooks.serverHeaders = append(h.grpcHooks.serverHeaders, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(serverHeaders),
	})
	h.grpcHooks.activeStreamsByURI[methodURI] = index
	h.grpcHooks.activeStreamsByIndex[index] = methodURI
	allMatches, matchedHeaders := h.MatchGRPCRequest(methodURI)
	if h.grpcHooks.listeners[index] == nil {
		h.grpcHooks.listeners[index] = map[string]GRPCListener{}
	}
	for _, hook := range allMatches {
		if md != nil {
			hook.grpcListener.onClientHeaders(md)
		}
		h.grpcHooks.listeners[index][hook.ID] = hook.grpcListener
	}
	for key, headers := range matchedHeaders {
		HeaderTrackingFunc(h.port, key, methodURI, headers)
	}
}

func (h *Hooks) RemoveGRPCStream(methodURI string, index int) {
	h.lock.Lock()
	defer h.lock.Unlock()
	if methodURI != "" {
		index = h.grpcHooks.activeStreamsByURI[methodURI]
	} else {
		methodURI = h.grpcHooks.activeStreamsByIndex[index]
	}
	delete(h.grpcHooks.activeStreamsByURI, methodURI)
	delete(h.grpcHooks.activeStreamsByIndex, index)
	h.grpcHooks.clientStreams = append(h.grpcHooks.clientStreams[:index], h.grpcHooks.clientStreams[index+1:]...)
	h.grpcHooks.serverStreams = append(h.grpcHooks.serverStreams[:index], h.grpcHooks.serverStreams[index+1:]...)
	h.grpcHooks.serverHeaders = append(h.grpcHooks.serverHeaders[:index], h.grpcHooks.serverHeaders[index+1:]...)
	for _, hook := range h.grpcHooks.hooks {
		delete(hook.streamIndexes, index)
	}
}

func (h *Hooks) RemoveHook(id string) {
	h.lock.Lock()
	defer h.lock.Unlock()
	hook := h.grpcHooks.hooks[id]
	if hook != nil {
		for i, _ := range hook.streamIndexes {
			delete(h.grpcHooks.listeners[i], id)
		}
		delete(h.grpcHooks.hooks, id)
	} else {
		delete(h.httpHooks, id)
	}
}

func (h *Hooks) MatchHTTPRequest(uri string, headers map[string][]string) (allMatches map[string]*Hook, matchedHeaders map[string][][2]string) {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return matchRequest(uri, headers, h.httpHooks)
}

func (h *Hooks) MatchGRPCRequest(methodURI string) (allMatches map[string]*Hook, matchedHeaders map[string][][2]string) {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return matchRequest(methodURI, nil, h.grpcHooks.hooks)
}

func newHook() *Hook {
	return &Hook{
		streamIndexes: map[int]bool{},
	}
}

func (h *Hook) initHTTP(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers, isJSONRPC bool, listener HTTPListener) {
	h.init(key, id, uri, uriPrefix, reURI, headers)
	h.IsJSONRPC = isJSONRPC
	h.httpListener = listener
}

func (h *Hook) init(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers) {
	if id == "" {
		id = string(hookIdCounter.Add(1))
	}
	h.Key = key
	h.ID = id
	h.URI = uri
	h.Match = &HookMatch{
		UriPrefix: uriPrefix,
		re:        reURI,
	}
	if len(headers) > 0 {
		h.Match.HeaderMatches = map[string]*HeaderMatch{}
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
}

func (h *Hook) matchURI(uri string) bool {
	if h.Match.re != nil {
		if h.Match.re.MatchString(uri) {
			return true
		}
	}
	return false
}

func (h *Hook) matchHeader(header string, values []string) (bool, string) {
	if h.Match.HeaderMatches == nil {
		return false, ""
	}
	header = strings.ToLower(header)
	for _, hm := range h.Match.HeaderMatches {
		if hm.re.MatchString(header) {
			if hm.Values == nil {
				return true, ""
			}
			for _, vre := range hm.Values {
				for _, hv := range values {
					if vre.MatchString(hv) {
						return true, hv
					}
				}
			}
		}
	}
	return false, ""
}

func (h *Hook) match(uri string, headers map[string][]string, allMatches map[string]*Hook, matchedHeaders map[string][][2]string) bool {
	if !h.matchURI(uri) {
		return false
	}
	if allMatches != nil {
		allMatches[h.ID] = h
	}
	for header, hvalues := range headers {
		if matched, value := h.matchHeader(header, hvalues); matched {
			if matchedHeaders != nil {
				if matchedHeaders[h.Key] == nil {
					matchedHeaders[h.Key] = [][2]string{}
				}
				matchedHeaders[h.Key] = append(matchedHeaders[h.Key], [2]string{header, value})
			}
		}
	}
	return true
}

func matchRequest(uri string, headers map[string][]string, hooks map[string]*Hook) (allMatches map[string]*Hook, matchedHeaders map[string][][2]string) {
	allMatches = map[string]*Hook{}
	matchedHeaders = map[string][][2]string{}
	for _, hook := range hooks {
		hook.match(uri, headers, allMatches, matchedHeaders)
	}
	return allMatches, matchedHeaders
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsKnownNonTraffic {
			if next != nil {
				next.ServeHTTP(w, r)
			}
			return
		}
		allMatches, matchedHeaders := GetPortHooks(util.GetRequestOrListenerPortNum(r)).MatchHTTPRequest(r.RequestURI, r.Header)
		callNext := false
		if len(allMatches) > 0 {
			body := util.Read(r.Body)
			for _, hook := range allMatches {
				util.SetIsJSONRPC(r, hook.IsJSONRPC)
				if hook.httpListener != nil {
					callNext = callNext || hook.httpListener(util.GetRequestOrListenerPortNum(r), r.RequestURI, r.Header, io.NopCloser(strings.NewReader(body)))
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
