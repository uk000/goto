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
	HeaderTrackingFunc func(port int, key, uri string, matchedHeaders map[string][]string)
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

// GetPortHooks gives the hooks of the port, which are made on first use. They are made once, whoever asks for them at the
// same time: the callers share the hooks, and the ones that are added to them.
func GetPortHooks(port int) *Hooks {
	lock.RLock()
	hooks := portHooks[port]
	lock.RUnlock()
	if hooks != nil {
		return hooks
	}
	lock.Lock()
	defer lock.Unlock()
	if hooks = portHooks[port]; hooks == nil {
		hooks = newHooks(port)
		portHooks[port] = hooks
	}
	return hooks
}

// setRoutes registers the GET /hooks route that lists the hooks of all ports.
func setRoutes(r *mux.Router) {
	hooksRouter := middleware.RootPath("/hooks")
	util.AddRoute(hooksRouter, "", getHooks, "GET")
}

// getHooks writes the hooks of all ports as YAML.
func getHooks(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	defer lock.RUnlock()
	util.WriteYaml(w, portHooks)
}

// newHooks creates the hooks of a port and starts the goroutines that monitor its gRPC client streams, server streams and server headers.
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

// monitorGRPCStreams forever selects on the gRPC client or server stream channels and forwards each message to the listeners of the matching hooks, removing closed streams.
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

// monitorGRPCHeaders forever selects on the gRPC server header channels and passes the headers to the listeners of the matching hooks, removing closed streams.
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

// AddGRPCHook registers a hook for a gRPC method URI whose listener receives the messages of the matching streams.
func (h *Hooks) AddGRPCHook(key, id, methodURI string, grpcListener GRPCListener) error {
	if methodURI == "" {
		return fmt.Errorf("URI needed")
	}
	uriPrefix, re, _, _, err := util.GetURIRegexpAndRoute(methodURI, true, util.RootRouter)
	if err != nil {
		return err
	}
	h.addGRPCHook(key, id, methodURI, uriPrefix, re, grpcListener)
	return nil
}

// AddHTTPHookWithHandler registers an HTTP hook for a URI and/or headers, with a route that serves the URI using the given handler.
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

// AddHTTPHookWithListener registers an HTTP hook for a URI and/or headers that observes the matching requests through the given listener.
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

// AddHTTPTracking registers an HTTP hook, without handler or listener, for a URI and/or headers to track the matched request headers.
func (h *Hooks) AddHTTPTracking(key, uri string, headers Headers, isJSONRPC bool) error {
	if uri == "" && headers == nil {
		return fmt.Errorf("One of URI and Headers needed")
	}
	uri, uriPrefix, re, err := h.registerURI(uri, nil)
	if err != nil {
		return err
	}
	h.addHTTPHook(key, "", uri, uriPrefix, re, headers, isJSONRPC, nil)
	return nil
}

// registerURI lower-cases the URI and builds its matcher: a route if a handler serves it, otherwise only a regexp to observe the matching requests.
func (h *Hooks) registerURI(uri string, httpHandler middleware.MiddlewareFunc) (luri, uriPrefix string, re *regexp.Regexp, err error) {
	luri = strings.ToLower(uri)
	if httpHandler != nil {
		// The handler serves the URI, so a route is needed.
		uriPrefix, re, _, err = util.BuildURIMatcher(luri, true, httpHandler)
	} else {
		// A hook without a handler only observes the requests that match, which the hooks middleware finds with the
		// regexp. A route would answer these requests itself, with an empty response, instead of the handlers
		// that serve them (and with a catch-all route for the empty URI of header-only hooks, all of them).
		uriPrefix, re, err = util.BuildURIRegexp(luri, true)
	}
	return
}

// addHTTPHook creates an HTTP hook and stores it by its ID.
func (h *Hooks) addHTTPHook(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers, isJSONRPC bool, listener HTTPListener) *Hook {
	hook := newHook()
	hook.initHTTP(key, id, uri, uriPrefix, reURI, headers, isJSONRPC, listener)
	h.lock.Lock()
	defer h.lock.Unlock()
	h.httpHooks[hook.ID] = hook
	return hook
}

// addGRPCHook creates and stores a gRPC hook and attaches its listener to the active streams whose method URI matches.
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

// AddGRPCStream registers the channels of a new gRPC stream and attaches the listeners of the hooks matching its method URI, tracking the matched headers.
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

// RemoveGRPCStream removes a gRPC stream, found by method URI or index, along with its channels and its indexes in the hooks.
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

// RemoveHook removes the gRPC or HTTP hook with the given ID, detaching a gRPC hook from the active streams.
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

// MatchHTTPRequest returns the HTTP hooks that match a URI and headers, and the request headers that matched.
func (h *Hooks) MatchHTTPRequest(uri string, headers map[string][]string) (allMatches map[string]*Hook, matchedHeaders map[string]map[string][]string) {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return matchRequest(uri, headers, h.httpHooks)
}

// MatchGRPCRequest returns the gRPC hooks that match a method URI.
func (h *Hooks) MatchGRPCRequest(methodURI string) (allMatches map[string]*Hook, matchedHeaders map[string]map[string][]string) {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return matchRequest(methodURI, nil, h.grpcHooks.hooks)
}

// newHook creates an empty hook.
func newHook() *Hook {
	return &Hook{
		streamIndexes: map[int]bool{},
	}
}

// initHTTP initializes the hook as an HTTP hook with its JSON-RPC flag and listener.
func (h *Hook) initHTTP(key, id, uri, uriPrefix string, reURI *regexp.Regexp, headers Headers, isJSONRPC bool, listener HTTPListener) {
	h.init(key, id, uri, uriPrefix, reURI, headers)
	h.IsJSONRPC = isJSONRPC
	h.httpListener = listener
}

// init sets the identity of the hook (generating an ID if empty), its URI matcher, and its case-insensitive header and value regexp matchers, which support globs.
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

// matchURI reports whether the URI matches the URI regexp of the hook.
func (h *Hook) matchURI(uri string) bool {
	if h.Match.re != nil {
		if h.Match.re.MatchString(uri) {
			return true
		}
	}
	return false
}

// matchHeader reports whether a header, and one of its values if the hook has value matchers, matches the hook, returning the matched value.
func (h *Hook) matchHeader(header string, values []string) (bool, string) {
	if h.Match.HeaderMatches == nil {
		return false, ""
	}
	header = strings.ToLower(header)
	for _, hm := range h.Match.HeaderMatches {
		if hm.re.MatchString(header) {
			if len(hm.Values) == 0 {
				// The header is given without values: it matches by its presence, reported with the value that it has first.
				if len(values) > 0 {
					return true, values[0]
				}
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

// match reports whether the URI matches the hook, recording the hook in allMatches and the matching headers in matchedHeaders.
func (h *Hook) match(uri string, headers map[string][]string, allMatches map[string]*Hook, matchedHeaders map[string]map[string][]string) bool {
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
					matchedHeaders[h.Key] = map[string][]string{}
				}
				matchedHeaders[h.Key][header] = []string{value}
			}
		}
	}
	return true
}

// matchRequest collects the hooks that match a URI, and the headers that they matched.
func matchRequest(uri string, headers map[string][]string, hooks map[string]*Hook) (allMatches map[string]*Hook, matchedHeaders map[string]map[string][]string) {
	allMatches = map[string]*Hook{}
	matchedHeaders = map[string]map[string][]string{}
	for _, hook := range hooks {
		hook.match(uri, headers, allMatches, matchedHeaders)
	}
	return allMatches, matchedHeaders
}

// middlewareFunc matches the request against the HTTP hooks of its port, calls their listeners with the body, and tracks the matched headers; it calls next if no hook matched, no matched hook has a listener, or a listener returns true.
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
			hasListener := false
			for _, hook := range allMatches {
				util.SetIsJSONRPC(r, hook.IsJSONRPC)
				if hook.httpListener != nil {
					hasListener = true
					// Every listener is called, whatever the others returned.
					if hook.httpListener(util.GetRequestOrListenerPortNum(r), r.RequestURI, r.Header, io.NopCloser(strings.NewReader(body))) {
						callNext = true
					}
				}
			}
			r.Body = io.NopCloser(strings.NewReader(body))
			for key, headers := range matchedHeaders {
				HeaderTrackingFunc(util.GetRequestOrListenerPortNum(r), key, r.RequestURI, headers)
			}
			if !hasListener {
				callNext = true
			}
		} else {
			callNext = true
		}
		if callNext && next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
