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
	"encoding/json"
	"fmt"
	"goto/pkg/util"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"k8s.io/client-go/util/jsonpath"
)

type ResponsePayload struct {
	Payload          []byte            `json:"payload"`
	StreamPayload    [][]byte          `json:"streamPayload"`
	ContentType      string            `json:"contentType"`
	IsStream         bool              `json:"isStream"`
	IsBinary         bool              `json:"isBinary"`
	URIMatch         string            `json:"uriMatch"`
	HeaderMatch      string            `json:"headerMatch"`
	HeaderValueMatch string            `json:"headerValueMatch"`
	QueryMatch       string            `json:"queryMatch"`
	QueryValueMatch  string            `json:"queryValueMatch"`
	BodyMatch        []string          `json:"bodyMatch"`
	BodyPaths        map[string]string `json:"bodyPaths"`
	URICaptureKeys   []string          `json:"uriCaptureKeys"`
	HeaderCaptureKey string            `json:"headerCaptureKey"`
	QueryCaptureKey  string            `json:"queryCaptureKey"`
	Transforms       []*util.Transform `json:"transforms"`
	StreamCount      int               `json:"streamCount"`
	StreamDelayMin   time.Duration     `json:"streamDelayMin"`
	StreamDelayMax   time.Duration     `json:"streamDelayMax"`
	uriRegexp        *regexp.Regexp
	queryMatchRegexp *regexp.Regexp
	bodyMatchRegexp  *regexp.Regexp
	bodyJsonPaths    map[string]*jsonpath.JSONPath
	fillers          []string
	router           *mux.Router
}

type ProtoPayloads struct {
	DefaultPayload         *ResponsePayload                                  `json:"defaultPayload"`
	PayloadByURIs          map[string]*ResponsePayload                       `json:"payloadByURIs"`
	PayloadByHeaders       map[string]map[string]*ResponsePayload            `json:"responsePayloadByHeaders"`
	PayloadByURIAndHeaders map[string]map[string]map[string]*ResponsePayload `json:"responsePayloadByURIAndHeaders"`
	PayloadByQuery         map[string]map[string]*ResponsePayload            `json:"responsePayloadByQuery"`
	PayloadByURIAndQuery   map[string]map[string]map[string]*ResponsePayload `json:"responsePayloadByURIAndQuery"`
	PayloadByURIAndBody    map[string]map[string]*ResponsePayload            `json:"responsePayloadByURIAndBody"`
	allURIResponsePayloads map[string]*ResponsePayload
	lock                   sync.RWMutex
}

func (rp *ResponsePayload) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{
		"contentType":      rp.ContentType,
		"uriMatch":         rp.URIMatch,
		"headerMatch":      rp.HeaderMatch,
		"headerValueMatch": rp.HeaderValueMatch,
		"queryMatch":       rp.QueryMatch,
		"queryValueMatch":  rp.QueryValueMatch,
		"bodyMatch":        rp.BodyMatch,
		"uriCaptureKeys":   rp.URICaptureKeys,
		"headerCaptureKey": rp.HeaderCaptureKey,
		"queryCaptureKey":  rp.QueryCaptureKey,
		"transforms":       rp.Transforms,
		"binary":           rp.IsBinary,
	}
	if rp.IsBinary || len(rp.Payload) > 10000 {
		data["payload"] = fmt.Sprintf("...(%d bytes)", len(rp.Payload))
	} else {
		data["payload"] = string(rp.Payload)
	}
	return json.Marshal(data)
}

func (pp *ProtoPayloads) init() {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	pp.DefaultPayload = nil
	pp.PayloadByURIs = map[string]*ResponsePayload{}
	pp.PayloadByHeaders = map[string]map[string]*ResponsePayload{}
	pp.PayloadByURIAndHeaders = map[string]map[string]map[string]*ResponsePayload{}
	pp.PayloadByQuery = map[string]map[string]*ResponsePayload{}
	pp.PayloadByURIAndQuery = map[string]map[string]map[string]*ResponsePayload{}
	pp.PayloadByURIAndBody = map[string]map[string]*ResponsePayload{}
	pp.allURIResponsePayloads = map[string]*ResponsePayload{}
}

func (pp *ProtoPayloads) setDefaultResponsePayload(rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	pp.DefaultPayload = rp
}

func (pp *ProtoPayloads) setURIResponsePayload(uri string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		pp.PayloadByURIs[uri] = rp
		pp.allURIResponsePayloads[uri] = rp
	}
}

func (pp *ProtoPayloads) removeURIResponsePayload(uri string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	delete(pp.PayloadByURIs, uri)
	pp.unsafeRemoveUntrackedURI(uri)
}

func (pp *ProtoPayloads) unsafeRemoveUntrackedURI(uri string) {
	if !(pp.PayloadByURIs[uri] != nil || pp.PayloadByURIAndHeaders[uri] != nil ||
		pp.PayloadByURIAndQuery[uri] != nil || pp.PayloadByURIAndBody[uri] != nil) {
		delete(pp.allURIResponsePayloads, uri)
	}
}

func (pp *ProtoPayloads) setHeaderResponsePayload(header string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		if pp.PayloadByHeaders[header] == nil {
			pp.PayloadByHeaders[header] = map[string]*ResponsePayload{}
		}
		pp.PayloadByHeaders[header][rp.HeaderValueMatch] = rp
	}
}

func (pp *ProtoPayloads) removeHeaderResponsePayload(header, value string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if util.IsFiller(value) {
		value = ""
	}
	delete(pp.PayloadByHeaders[header], value)
	if len(pp.PayloadByHeaders[header]) == 0 {
		delete(pp.PayloadByHeaders, header)
	}
}

func (pp *ProtoPayloads) setURIWithHeaderResponsePayload(uri, header string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		if pp.PayloadByURIAndHeaders[uri] == nil {
			pp.PayloadByURIAndHeaders[uri] = map[string]map[string]*ResponsePayload{}
		}
		if pp.PayloadByURIAndHeaders[uri][header] == nil {
			pp.PayloadByURIAndHeaders[uri][header] = map[string]*ResponsePayload{}
		}
		pp.PayloadByURIAndHeaders[uri][header][rp.HeaderValueMatch] = rp
		pp.allURIResponsePayloads[uri] = rp
	}
}

func (pp *ProtoPayloads) removeURIWithHeaderResponsePayload(uri, header, value string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if pp.PayloadByURIAndHeaders[uri] != nil {
		if pp.PayloadByURIAndHeaders[uri][header] != nil {
			if _, present := util.GetFillerUnmarked(value); present {
				value = ""
			}
			delete(pp.PayloadByURIAndHeaders[uri][header], value)
			if len(pp.PayloadByURIAndHeaders[uri][header]) == 0 {
				delete(pp.PayloadByURIAndHeaders[uri], header)
			}
		}
		if len(pp.PayloadByURIAndHeaders[uri]) == 0 {
			delete(pp.PayloadByURIAndHeaders, uri)
			pp.unsafeRemoveUntrackedURI(uri)
		}
	}
}

func (pp *ProtoPayloads) setQueryResponsePayload(query string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		if pp.PayloadByQuery[query] == nil {
			pp.PayloadByQuery[query] = map[string]*ResponsePayload{}
		}
		pp.PayloadByQuery[query][rp.QueryValueMatch] = rp
	}
}

func (pp *ProtoPayloads) removeQueryResponsePayload(query, value string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if util.IsFiller(value) {
		value = ""
	}
	delete(pp.PayloadByQuery[query], value)
	if len(pp.PayloadByQuery[query]) == 0 {
		delete(pp.PayloadByQuery, query)
	}
}

func (pp *ProtoPayloads) setURIWithQueryResponsePayload(uri, query string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		if pp.PayloadByURIAndQuery[uri] == nil {
			pp.PayloadByURIAndQuery[uri] = map[string]map[string]*ResponsePayload{}
		}
		if pp.PayloadByURIAndQuery[uri][query] == nil {
			pp.PayloadByURIAndQuery[uri][query] = map[string]*ResponsePayload{}
		}
		pp.PayloadByURIAndQuery[uri][query][rp.QueryValueMatch] = rp
		pp.allURIResponsePayloads[uri] = rp
	}
}

func (pp *ProtoPayloads) removeURIWithQueryResponsePayload(uri, query, value string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if pp.PayloadByURIAndQuery[uri] != nil {
		if pp.PayloadByURIAndQuery[uri][query] != nil {
			if _, present := util.GetFillerUnmarked(value); present {
				value = ""
			}
			delete(pp.PayloadByURIAndQuery[uri][query], value)
			if len(pp.PayloadByURIAndQuery[uri][query]) == 0 {
				delete(pp.PayloadByURIAndQuery[uri], query)
			}
		}
		if len(pp.PayloadByURIAndQuery[uri]) == 0 {
			delete(pp.PayloadByURIAndQuery, uri)
			pp.unsafeRemoveUntrackedURI(uri)
		}
	}
}

func (pp *ProtoPayloads) setURIWithBodyMatchResponsePayload(uri, match string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		if pp.PayloadByURIAndBody[uri] == nil {
			pp.PayloadByURIAndBody[uri] = map[string]*ResponsePayload{}
		}
		pp.PayloadByURIAndBody[uri][match] = rp
		pp.allURIResponsePayloads[uri] = rp
	}
}

func (pp *ProtoPayloads) removeURIWithBodyMatchResponsePayload(uri, match string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if pp.PayloadByURIAndBody[uri] != nil {
		delete(pp.PayloadByURIAndBody[uri], match)
		if len(pp.PayloadByURIAndBody[uri]) == 0 {
			delete(pp.PayloadByURIAndBody, uri)
			pp.unsafeRemoveUntrackedURI(uri)
		}
	}
}

func (pp *ProtoPayloads) HasAnyPayload() bool {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	return len(pp.allURIResponsePayloads) > 0 || len(pp.PayloadByHeaders) > 0 ||
		len(pp.PayloadByQuery) > 0 || pp.DefaultPayload != nil
}

func (pp *ProtoPayloads) GetResponsePayload(requestURI string, header map[string][]string, query map[string][]string, body io.ReadCloser) (newBodyReader io.ReadCloser, responsePayload *ResponsePayload, captures map[string]string, found bool) {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	for uri, rp := range pp.allURIResponsePayloads {
		if rp.uriRegexp.MatchString(requestURI) {
			if !found && pp.PayloadByURIAndHeaders[uri] != nil {
				responsePayload, found = getPayloadForKV(header, pp.PayloadByURIAndHeaders[uri])
			}
			if !found && pp.PayloadByURIAndQuery[uri] != nil {
				responsePayload, found = getPayloadForKV(query, pp.PayloadByURIAndQuery[uri])
			}
			if !found && pp.PayloadByURIAndBody[uri] != nil {
				newBodyReader, responsePayload, captures, found = getPayloadForBodyMatch(body, pp.PayloadByURIAndBody[uri])
			}
			if !found && pp.PayloadByURIs[uri] != nil {
				responsePayload = pp.PayloadByURIs[uri]
				found = true
			}
			if found {
				break
			}
		}
	}
	if !found {
		responsePayload, found = getPayloadForKV(header, pp.PayloadByHeaders)
	}
	if !found {
		responsePayload, found = getPayloadForKV(query, pp.PayloadByQuery)
	}
	if !found && pp.DefaultPayload != nil {
		responsePayload = pp.DefaultPayload
		found = true
	}
	return
}
