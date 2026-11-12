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

package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type PortResponsePayloads struct {
	Port         int
	HTTPPayloads *ProtoPayloads `json:"http"`
	RPCPayloads  *ProtoPayloads `json:"rpc"`
	lock         sync.RWMutex
}

type ResponsePayloadManager struct {
	payloads map[int]*PortResponsePayloads
	lock     sync.RWMutex
}

var (
	PayloadManager = (&ResponsePayloadManager{}).init()
	payloadKey     = &util.ContextKey{Key: "payloadKey"}
	captureKey     = &util.ContextKey{Key: "captureKey"}
)

// MarshalJSON reports the payloads of the port under its lock, and the payloads of each protocol under theirs.
func (pr *PortResponsePayloads) MarshalJSON() ([]byte, error) {
	pr.lock.RLock()
	port, httpPayloads, rpcPayloads := pr.Port, pr.HTTPPayloads, pr.RPCPayloads
	pr.lock.RUnlock()
	return json.Marshal(struct {
		Port         int
		HTTPPayloads *ProtoPayloads `json:"http"`
		RPCPayloads  *ProtoPayloads `json:"rpc"`
	}{port, httpPayloads, rpcPayloads})
}

// init allocates the per-port payload map and returns the manager.
func (pm *ResponsePayloadManager) init() *ResponsePayloadManager {
	pm.payloads = map[int]*PortResponsePayloads{}
	return pm
}

// ClearRPCResponsePayloads resets all the payloads (HTTP and RPC) configured for the port.
func (pm *ResponsePayloadManager) ClearRPCResponsePayloads(port int) {
	pm.GetPortResponse(port).init()
}

// SetRPCResponsePayload sets an RPC response payload for the port, keyed by stream, default, URI, header or URI with header/body match, whichever of those the arguments give; errors if the payload is empty.
func (pm *ResponsePayloadManager) SetRPCResponsePayload(port int, isStream bool, payload []byte, contentType, uri, header, value, regexes, paths string, count int, delayMin, delayMax time.Duration) (err error) {
	if len(payload) == 0 {
		return fmt.Errorf("No payload")
	}
	pr := pm.GetPortResponse(port)
	isDefault := uri == "" && header == ""
	if isStream {
		err = pr.setStreamResponsePayload(true, payload, contentType, uri, header, value, count, delayMin, delayMax)
	} else if isDefault {
		err = pr.setDefaultResponsePayload(true, payload, contentType, len(payload), delayMin, delayMax)
	} else if uri != "" {
		if header != "" {
			err = pr.setResponsePayloadForURIWithHeader(true, payload, false, uri, header, value, contentType, delayMin, delayMax)
		} else if regexes != "" || paths != "" {
			match := regexes
			if match == "" {
				match = paths
			}
			err = pr.setResponsePayloadForURIWithBodyMatch(true, payload, false, uri, match, contentType, paths != "", delayMin, delayMax)
		} else {
			err = pr.setURIResponsePayload(true, isStream, payload, false, uri, contentType, nil, delayMin, delayMax)
		}
	} else if header != "" {
		err = pr.setHeaderResponsePayload(true, payload, false, header, value, contentType, delayMin, delayMax)
	}
	return
}

// SetRPCResponsePayloadTransform sets RPC response payload transforms for a URI on the port.
func (pm *ResponsePayloadManager) SetRPCResponsePayloadTransform(port int, isStream bool, contentType, uri string, transforms []*util.Transform, delayMin, delayMax time.Duration) error {
	return pm.GetPortResponse(port).setURIResponsePayload(true, isStream, nil, false, uri, contentType, transforms, delayMin, delayMax)
}

// SetURIResponsePayloadWithMatches validates a response payload and registers it for the URI prefix of each of its request matches on the port.
func (pm *ResponsePayloadManager) SetURIResponsePayloadWithMatches(port int, rp *ResponsePayload, isGRPC bool) error {
	if rp == nil {
		return errors.New("missing payload")
	}
	if err := rp.Process(); err != nil {
		return err
	}
	if rp.StreamCount > 0 {
		rp.IsStream = true
		rp.PrepareJSONStreamPayload(rp.StreamCount)
	}
	pr := pm.GetPortResponse(port)
	pp := pr.protoPayload(isGRPC)
	for _, m := range rp.RequestMatches {
		pp.setURIResponsePayloadWithMatches(strings.ToLower(m.URIPrefix), rp)

	}
	return nil
}

// GetResponsePayload finds the response payload configured for the port that matches the request URI, headers, query and body.
func (pm *ResponsePayloadManager) GetResponsePayload(port int, isGRPC bool, requestURI string, header http.Header, query url.Values, body io.ReadCloser) (newBodyReader io.ReadCloser, responsePayload *ResponsePayload, captures map[string]string, found bool) {
	pr := pm.GetPortResponse(port)
	return pr.protoPayload(isGRPC).GetMatchingResponsePayload(requestURI, header, query, body)
}

// getPortResponse returns the payloads of the request's port.
func (pm *ResponsePayloadManager) getPortResponse(r *http.Request) *PortResponsePayloads {
	return pm.GetPortResponse(util.GetRequestOrListenerPortNum(r))
}

// hasPayloadsFor tells whether handleURI has anything to do for the request: its port has a payload that can answer it, and
// the request is not one that asks for a payload of its own. It is the condition of the routes of the payload URIs (a route
// stays for good once it is registered, while the payloads of its URI don't), and it creates nothing for a port that has no payloads.
func (pm *ResponsePayloadManager) hasPayloadsFor(r *http.Request) bool {
	pm.lock.RLock()
	pr := pm.payloads[util.GetRequestOrListenerPortNum(r)]
	pm.lock.RUnlock()
	if pr == nil || util.IsPayloadRequest(r) {
		return false
	}
	return pr.protoPayload(util.IsGRPC(r) || util.IsJSONRPC(r)).HasPayloadFor(r.RequestURI, r.Header, r.URL.Query())
}

// payloadsApplyTo is the condition of the routes of the payload URIs. It looks the manager up as it is called.
func payloadsApplyTo(r *http.Request) bool {
	return PayloadManager.hasPayloadsFor(r)
}

// GetPortResponse returns the payloads of the port, creating them if missing.
func (pm *ResponsePayloadManager) GetPortResponse(port int) *PortResponsePayloads {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	pr := pm.payloads[port]
	if pr == nil {
		pr = &PortResponsePayloads{Port: port}
		pr.init()
		pm.payloads[port] = pr
	}
	return pr
}

// init resets the HTTP and RPC payloads of the port to empty.
func (pr *PortResponsePayloads) init() {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	pr.HTTPPayloads = &ProtoPayloads{}
	pr.HTTPPayloads.init()
	pr.RPCPayloads = &ProtoPayloads{}
	pr.RPCPayloads.init()
}

// GetGRPCPayloads returns the RPC payloads of the port.
func (pr *PortResponsePayloads) GetGRPCPayloads() *ProtoPayloads {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	return pr.RPCPayloads
}

// protoPayload returns the RPC payloads if isGRPC, else the HTTP payloads.
func (pr *PortResponsePayloads) protoPayload(isGRPC bool) *ProtoPayloads {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	if isGRPC {
		return pr.RPCPayloads
	}
	return pr.HTTPPayloads
}

// setStreamResponsePayload sets a stream payload (split into count parts) for the default, URI, header or URI with header scope; an empty payload removes the URI's.
func (pr *PortResponsePayloads) setStreamResponsePayload(isGRPC bool, payload []byte, contentType, uri, header, value string, count int, delayMin, delayMax time.Duration) error {
	uri = strings.ToLower(uri)
	header = strings.ToLower(header)
	value = strings.ToLower(value)
	rp, err := newResponsePayload(payload, true, true, contentType, uri, header, "", value, nil, nil, nil, delayMin, delayMax)
	if err != nil {
		return err
	}
	rp.PrepareJSONStreamPayload(count)
	rp.Delay = types.NewDelay(delayMin, delayMax, -1)
	pp := pr.protoPayload(isGRPC)
	if len(payload) > 0 {
		if uri != "" && header != "" {
			pp.setURIWithHeaderResponsePayload(uri, header, rp)
		} else if uri != "" {
			pp.setURIResponsePayload(uri, rp)
		} else if header != "" {
			pp.setHeaderResponsePayload(header, rp)
		} else {
			pp.setDefaultResponsePayload(rp)
		}
	} else if uri != "" {
		pp.removeURIResponsePayload(uri)
	}
	return nil
}

// setDefaultResponsePayload sets the payload used when nothing more specific matches, padded or trimmed to size if given.
func (pr *PortResponsePayloads) setDefaultResponsePayload(isGRPC bool, payload []byte, contentType string, size int, delayMin, delayMax time.Duration) error {
	if size > 0 {
		payload = FixPayload(payload, size)
	}
	if rp, err := newResponsePayload(payload, false, true, contentType, "", "", "", "", nil, nil, nil, delayMin, delayMax); err == nil {
		pr.protoPayload(isGRPC).setDefaultResponsePayload(rp)
		return nil
	} else {
		return err
	}
}

// setURIResponsePayload sets the payload or transforms for a URI; an empty payload with no transforms removes the URI's payload.
func (pr *PortResponsePayloads) setURIResponsePayload(isGRPC, isStream bool, payload []byte, binary bool, uri, contentType string, transforms []*util.Transform, delayMin, delayMax time.Duration) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	if len(payload) > 0 || len(transforms) > 0 {
		if rp, err := newResponsePayload(payload, isStream, binary, contentType, uri, "", "", "", nil, nil, transforms, delayMin, delayMax); err == nil {
			pp.setURIResponsePayload(uri, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIResponsePayload(uri)
	}
	return nil
}

// setHeaderResponsePayload sets the payload for a request header (and value); an empty payload removes it.
func (pr *PortResponsePayloads) setHeaderResponsePayload(isGRPC bool, payload []byte, binary bool, header, value, contentType string, delayMin, delayMax time.Duration) error {
	pp := pr.protoPayload(isGRPC)
	header = strings.ToLower(header)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, "", header, "", value, nil, nil, nil, delayMin, delayMax); err == nil {
			pp.setHeaderResponsePayload(header, rp)
		} else {
			return err
		}
	} else {
		pp.removeHeaderResponsePayload(header, value)
	}
	return nil
}

// setQueryResponsePayload sets the payload for a request query param (and value); an empty payload removes it.
func (pr *PortResponsePayloads) setQueryResponsePayload(isGRPC bool, payload []byte, binary bool, query, value, contentType string, delayMin, delayMax time.Duration) error {
	pp := pr.protoPayload(isGRPC)
	query = strings.ToLower(query)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, "", "", query, value, nil, nil, nil, delayMin, delayMax); err == nil {
			pp.setQueryResponsePayload(query, rp)
		} else {
			return err
		}
	} else {
		pp.removeQueryResponsePayload(query, value)
	}
	return nil
}

// setResponsePayloadForURIWithHeader sets the payload for a URI combined with a request header (and value); an empty payload removes it.
func (pr *PortResponsePayloads) setResponsePayloadForURIWithHeader(isGRPC bool, payload []byte, binary bool, uri, header, value, contentType string, delayMin, delayMax time.Duration) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	header = strings.ToLower(header)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, uri, header, "", value, nil, nil, nil, delayMin, delayMax); err == nil {
			pp.setURIWithHeaderResponsePayload(uri, header, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIWithHeaderResponsePayload(uri, header, value)
	}
	return nil
}

// setResponsePayloadForURIWithQuery sets the payload for a URI combined with a request query param (and value); an empty payload removes it.
func (pr *PortResponsePayloads) setResponsePayloadForURIWithQuery(isGRPC bool, payload []byte, binary bool, uri, query, value, contentType string, delayMin, delayMax time.Duration) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	query = strings.ToLower(query)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, uri, "", query, value, nil, nil, nil, delayMin, delayMax); err == nil {
			pp.setURIWithQueryResponsePayload(uri, query, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIWithQueryResponsePayload(uri, query, value)
	}
	return nil
}

// setResponsePayloadForURIWithBodyMatch sets the payload for a URI combined with comma-separated body regexes or JSON paths; an empty payload removes it.
func (pr *PortResponsePayloads) setResponsePayloadForURIWithBodyMatch(isGRPC bool, payload []byte, binary bool, uri, match, contentType string, isPaths bool, delayMin, delayMax time.Duration) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	if !isPaths {
		match = strings.ToLower(match)
	}
	if len(payload) > 0 {
		var rp *ResponsePayload
		var err error
		bodyMatch := strings.Split(match, ",")
		if isPaths {
			rp, err = newResponsePayload(payload, false, binary, contentType, uri, "", "", "", nil, bodyMatch, nil, delayMin, delayMax)
		} else {
			rp, err = newResponsePayload(payload, false, binary, contentType, uri, "", "", "", bodyMatch, nil, nil, delayMin, delayMax)
		}
		if err == nil {
			pp.setURIWithBodyMatchResponsePayload(uri, match, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIWithBodyMatchResponsePayload(uri, match)
	}
	return nil
}
