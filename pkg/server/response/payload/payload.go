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

package payload

import (
	"fmt"
	"goto/pkg/util"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
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
	rootRouter     *mux.Router
	matchRouter    *mux.Router
	payloadKey     = &util.ContextKey{Key: "payloadKey"}
	captureKey     = &util.ContextKey{Key: "captureKey"}
)

func (pm *ResponsePayloadManager) init() *ResponsePayloadManager {
	pm.payloads = map[int]*PortResponsePayloads{}
	return pm
}

func (pm *ResponsePayloadManager) ClearRPCResponsePayloads(port int) {
	pm.GetPortResponse(port).protoPayload(true).init()
}

func (pm *ResponsePayloadManager) SetRPCResponsePayload(port int, isStream bool, payload []byte, contentType, uri, header, value, regexes, paths string, count int, delayMin, delayMax time.Duration) (err error) {
	if len(payload) == 0 {
		return fmt.Errorf("No payload")
	}
	pr := pm.GetPortResponse(port)
	isDefault := uri == "" && header == ""
	if isStream {
		err = pr.setStreamResponsePayload(true, payload, contentType, uri, header, value, count, delayMin, delayMax)
	} else if isDefault {
		err = pr.setDefaultResponsePayload(true, payload, contentType, len(payload))
	} else if uri != "" {
		if header != "" {
			err = pr.setResponsePayloadForURIWithHeader(true, payload, false, uri, header, value, contentType)
		} else if regexes != "" || paths != "" {
			match := regexes
			if match == "" {
				match = paths
			}
			err = pr.setResponsePayloadForURIWithBodyMatch(true, payload, false, uri, match, contentType, paths != "")
		} else {
			err = pr.setURIResponsePayload(true, isStream, payload, false, uri, contentType, nil)
		}
	} else if header != "" {
		err = pr.setHeaderResponsePayload(true, payload, false, header, value, contentType)
	}
	return
}

func (pm *ResponsePayloadManager) SetRPCResponsePayloadTransform(port int, isStream bool, contentType, uri string, transforms []*util.Transform) error {
	return pm.GetPortResponse(port).setURIResponsePayload(true, isStream, nil, false, uri, contentType, transforms)
}

func (pm *ResponsePayloadManager) GetResponsePayload(port int, isGRPC bool, requestURI string, header map[string][]string, query map[string][]string, body io.ReadCloser) (newBodyReader io.ReadCloser, responsePayload *ResponsePayload, captures map[string]string, found bool) {
	pr := pm.GetPortResponse(port)
	return pr.protoPayload(isGRPC).GetResponsePayload(requestURI, header, query, body)
}

func (pm *ResponsePayloadManager) getPortResponse(r *http.Request) *PortResponsePayloads {
	return pm.GetPortResponse(util.GetRequestOrListenerPortNum(r))
}

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

func (pr *PortResponsePayloads) init() {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	pr.HTTPPayloads = &ProtoPayloads{}
	pr.HTTPPayloads.init()
	pr.RPCPayloads = &ProtoPayloads{}
	pr.RPCPayloads.init()
}

func (pr *PortResponsePayloads) GetGRPCPayloads() *ProtoPayloads {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	return pr.RPCPayloads
}

func (pr *PortResponsePayloads) protoPayload(isGRPC bool) *ProtoPayloads {
	pr.lock.Lock()
	defer pr.lock.Unlock()
	if isGRPC {
		return pr.RPCPayloads
	}
	return pr.HTTPPayloads
}

func (pr *PortResponsePayloads) setStreamResponsePayload(isGRPC bool, payload []byte, contentType, uri, header, value string, count int, delayMin, delayMax time.Duration) error {
	uri = strings.ToLower(uri)
	header = strings.ToLower(header)
	value = strings.ToLower(value)
	rp, err := newResponsePayload(payload, true, true, contentType, uri, header, "", value, nil, nil, nil)
	if err != nil {
		return err
	}
	rp.PrepareStreamPayload(count, delayMin, delayMax)
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

func (pr *PortResponsePayloads) setDefaultResponsePayload(isGRPC bool, payload []byte, contentType string, size int) error {
	if size > 0 {
		payload = fixPayload(payload, size)
	}
	if rp, err := newResponsePayload(payload, false, true, contentType, "", "", "", "", nil, nil, nil); err == nil {
		pr.protoPayload(isGRPC).setDefaultResponsePayload(rp)
		return nil
	} else {
		return err
	}
}

func (pr *PortResponsePayloads) setURIResponsePayload(isGRPC, isStream bool, payload []byte, binary bool, uri, contentType string, transforms []*util.Transform) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	if len(payload) > 0 || len(transforms) > 0 {
		if rp, err := newResponsePayload(payload, isStream, binary, contentType, uri, "", "", "", nil, nil, transforms); err == nil {
			pp.setURIResponsePayload(uri, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIResponsePayload(uri)
	}
	return nil
}

func (pr *PortResponsePayloads) setHeaderResponsePayload(isGRPC bool, payload []byte, binary bool, header, value, contentType string) error {
	pp := pr.protoPayload(isGRPC)
	header = strings.ToLower(header)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, "", header, "", value, nil, nil, nil); err == nil {
			pp.setHeaderResponsePayload(header, rp)
		} else {
			return err
		}
	} else {
		pp.removeHeaderResponsePayload(header, value)
	}
	return nil
}

func (pr *PortResponsePayloads) setQueryResponsePayload(isGRPC bool, payload []byte, binary bool, query, value, contentType string) error {
	pp := pr.protoPayload(isGRPC)
	query = strings.ToLower(query)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, "", "", query, value, nil, nil, nil); err == nil {
			pp.setQueryResponsePayload(query, rp)
		} else {
			return err
		}
	} else {
		pp.removeQueryResponsePayload(query, value)
	}
	return nil
}

func (pr *PortResponsePayloads) setResponsePayloadForURIWithHeader(isGRPC bool, payload []byte, binary bool, uri, header, value, contentType string) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	header = strings.ToLower(header)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, uri, header, "", value, nil, nil, nil); err == nil {
			pp.setURIWithHeaderResponsePayload(uri, header, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIWithHeaderResponsePayload(uri, header, value)
	}
	return nil
}

func (pr *PortResponsePayloads) setResponsePayloadForURIWithQuery(isGRPC bool, payload []byte, binary bool, uri, query, value, contentType string) error {
	pp := pr.protoPayload(isGRPC)
	uri = strings.ToLower(uri)
	query = strings.ToLower(query)
	value = strings.ToLower(value)
	if len(payload) > 0 {
		if rp, err := newResponsePayload(payload, false, binary, contentType, uri, "", query, value, nil, nil, nil); err == nil {
			pp.setURIWithHeaderResponsePayload(uri, query, rp)
		} else {
			return err
		}
	} else {
		pp.removeURIWithQueryResponsePayload(uri, query, value)
	}
	return nil
}

func (pr *PortResponsePayloads) setResponsePayloadForURIWithBodyMatch(isGRPC bool, payload []byte, binary bool, uri, match, contentType string, isPaths bool) error {
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
			rp, err = newResponsePayload(payload, false, binary, contentType, uri, "", "", "", nil, bodyMatch, nil)
		} else {
			rp, err = newResponsePayload(payload, false, binary, contentType, uri, "", "", "", bodyMatch, nil, nil)
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
