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
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type RequestMatch struct {
	URIPrefix          string            `yaml:"uriPrefix" json:"uriPrefix,omitempty"`
	Headers            map[string]string `yaml:"headers" json:"headers,omitempty"`
	Queries            map[string]string `yaml:"queries" json:"queries,omitempty"`
	BodyRegexes        []string          `yaml:"bodyRegexes" json:"bodyRegexes,omitempty"`
	uriCaptureKeys     []string
	uriRegexp          *regexp.Regexp
	headerValueMatches map[string]string
	queryValueMatches  map[string]string
	router             *mux.Router
}

type RequestCapture struct {
	Headers           map[string]string `yaml:"headers" json:"headers,omitempty"`
	Queries           map[string]string `yaml:"queries" json:"queries,omitempty"`
	uriCaptureKeys    []string
	headerCaptureKeys map[string]*types.Pair[*regexp.Regexp, []string]
	queryCaptureKeys  map[string]*types.Pair[*regexp.Regexp, []string]
}

type ResponsePayload struct {
	Payload          types.RawBytes    `json:"payload,omitempty"`
	StreamPayload    []types.RawBytes  `json:"streamPayload,omitempty"`
	ContentType      string            `json:"contentType,omitempty"`
	IsStream         bool              `json:"isStream,omitempty"`
	IsBinary         bool              `json:"isBinary,omitempty"`
	IsJSON           bool              `json:"isJSON,omitempty"`
	URIMatch         string            `json:"uriMatch,omitempty"`
	HeaderMatch      string            `json:"headerMatch,omitempty"`
	HeaderValueMatch string            `json:"headerValueMatch,omitempty"`
	RequestMatches   []*RequestMatch   `json:"matches,omitempty"`
	RequestCapture   *RequestCapture   `json:"capture,omitempty"`
	QueryMatch       string            `json:"queryMatch,omitempty"`
	QueryValueMatch  string            `json:"queryValueMatch,omitempty"`
	BodyMatch        []string          `json:"bodyMatch,omitempty"`
	BodyPaths        map[string]string `json:"bodyPaths,omitempty"`
	URICaptureKeys   []string          `json:"uriCaptureKeys,omitempty"`
	HeaderCaptureKey string            `json:"headerCaptureKey,omitempty"`
	QueryCaptureKey  string            `json:"queryCaptureKey,omitempty"`
	Transforms       []*util.Transform `json:"transforms,omitempty"`
	Delay            *types.Delay      `json:"delay,omitempty"`
	StreamCount      int               `json:"streamCount,omitempty"`
	Base64Encode     bool              `json:"base64Encode,omitempty"`
	Base64Decode     bool              `json:"base64Decode,omitempty"`
	DetectJSON       bool              `json:"detectJSON,omitempty"`
	EscapeJSON       bool              `json:"escapeJSON,omitempty"`
	uriRegexp        *regexp.Regexp
	queryMatchRegexp *regexp.Regexp
	bodyMatchRegexp  *regexp.Regexp
	bodyJsonPath     *util.JSONPath
	fillers          []string
	router           *mux.Router
}

type ProtoPayloads struct {
	DefaultPayload          *ResponsePayload                                  `json:"defaultPayload"`
	PayloadByURIWithMatches map[string][]*ResponsePayload                     `json:"payloadByURIWithMatches"`
	PayloadByURIs           map[string]*ResponsePayload                       `json:"payloadByURIs"`
	PayloadByHeaders        map[string]map[string]*ResponsePayload            `json:"responsePayloadByHeaders"`
	PayloadByURIAndHeaders  map[string]map[string]map[string]*ResponsePayload `json:"responsePayloadByURIAndHeaders"`
	PayloadByQuery          map[string]map[string]*ResponsePayload            `json:"responsePayloadByQuery"`
	PayloadByURIAndQuery    map[string]map[string]map[string]*ResponsePayload `json:"responsePayloadByURIAndQuery"`
	PayloadByURIAndBody     map[string]map[string]*ResponsePayload            `json:"responsePayloadByURIAndBody"`
	allURIResponsePayloads  map[string]*ResponsePayload
	lock                    sync.RWMutex
}

func NewRequestMatch(uriPrefix string) *RequestMatch {
	return &RequestMatch{
		URIPrefix: uriPrefix,
	}
}

func newResponsePayload(payload []byte, stream, binary bool, contentType, uri, header, query, value string,
	bodyRegexes []string, paths []string, transforms []*util.Transform, delayMin, delayMax time.Duration) (*ResponsePayload, error) {
	if contentType == "" {
		contentType = constants.ContentTypeJSON
	}
	_, uriRegExp, responseRouter, err := util.BuildURIMatcher(uri, handleURI)
	if err != nil {
		return nil, fmt.Errorf("failed to add URI match %s with error: %s\n", uri, err.Error())
	}
	headerValueMatch := ""
	headerCaptureKey := ""
	queryValueMatch := ""
	queryCaptureKey := ""
	if util.IsFiller(value) {
		if header != "" {
			headerCaptureKey = value
		} else if query != "" {
			queryCaptureKey = value
		}
	} else if header != "" {
		headerValueMatch = value
	} else if query != "" {
		queryValueMatch = value
	}

	jsonPaths := util.NewJSONPath().Parse(paths)

	var bodyMatchRegexp *regexp.Regexp
	if len(bodyRegexes) > 0 {
		bodyMatchRegexp = regexp.MustCompile("(?i)" + strings.Join(bodyRegexes, ".*") + ".*")
	}

	var fillers []string
	if !binary {
		fillers = util.GetFillersUnmarked(string(payload))
	}
	for _, t := range transforms {
		for _, m := range t.Mappings {
			m.Init()
		}
	}
	return &ResponsePayload{
		Payload:          payload,
		ContentType:      contentType,
		IsStream:         stream,
		IsBinary:         util.IsBinaryContentType(contentType),
		URIMatch:         uri,
		HeaderMatch:      header,
		HeaderValueMatch: headerValueMatch,
		QueryMatch:       query,
		QueryValueMatch:  queryValueMatch,
		BodyMatch:        bodyRegexes,
		BodyPaths:        jsonPaths.TextPaths,
		uriRegexp:        uriRegExp,
		queryMatchRegexp: regexp.MustCompile("(?i)" + query),
		bodyMatchRegexp:  bodyMatchRegexp,
		bodyJsonPath:     jsonPaths,
		URICaptureKeys:   util.GetFillersUnmarked(uri),
		HeaderCaptureKey: headerCaptureKey,
		QueryCaptureKey:  queryCaptureKey,
		Transforms:       transforms,
		Delay:            types.NewDelay(delayMin, delayMax, -1),
		fillers:          fillers,
		router:           responseRouter,
	}, nil
}

func NewResponsePayload(payload []byte, matches []*RequestMatch, capture *RequestCapture, contentType string, base64Encode, base64Decode, detectJSON, escapeJSON bool) *ResponsePayload {
	return &ResponsePayload{
		RequestMatches: matches,
		RequestCapture: capture,
		Payload:        payload,
		ContentType:    contentType,
		Base64Encode:   base64Encode,
		Base64Decode:   base64Decode,
		DetectJSON:     detectJSON,
		EscapeJSON:     escapeJSON,
	}
}

func (rp *ResponsePayload) Process() error {
	if len(rp.RequestMatches) == 0 {
		return fmt.Errorf("Matches required")
	}
	if len(rp.Payload) == 0 && len(rp.StreamPayload) == 0 {
		return fmt.Errorf("Payload required")
	}
	if rp.ContentType == "" {
		rp.ContentType = constants.ContentTypeJSON
	}
	rp.IsBinary = util.IsBinaryContentType(rp.ContentType)
	rp.IsStream = (len(rp.Payload) == 0 && len(rp.StreamPayload) > 0)
	rp.IsJSON = strings.EqualFold(rp.ContentType, constants.ContentTypeJSON)
	if rp.Payload != nil {
		rp.Payload = types.RawBytes(util.CleanJSONBytes(rp.Payload))
	} else if len(rp.StreamPayload) > 0 {
		cleanPayload := []types.RawBytes{}
		for _, sp := range rp.StreamPayload {
			cleanPayload = append(cleanPayload, types.RawBytes(util.CleanJSONBytes(sp)))
		}
		rp.StreamPayload = cleanPayload
	}

	if rp.RequestCapture == nil {
		rp.RequestCapture = newRequestCapture()
	} else {
		rp.RequestCapture.NonNil()
	}
	for k, v := range rp.RequestCapture.Headers {
		captures, regexp := util.ReplaceFillersWithCaptureGroupRegex(v)
		rp.RequestCapture.headerCaptureKeys[k] = types.NewPair(regexp, captures)
	}
	for k, v := range rp.RequestCapture.Queries {
		captures, regexp := util.ReplaceFillersWithCaptureGroupRegex(v)
		rp.RequestCapture.queryCaptureKeys[k] = types.NewPair(regexp, captures)
	}

	for _, match := range rp.RequestMatches {
		if match.URIPrefix == "" {
			return fmt.Errorf("URI match is required")
		}
		_, uriRE, rr, err := util.BuildURIMatcher(match.URIPrefix, handleURI)
		if err != nil {
			return fmt.Errorf("failed to add URI match %s with error: %s\n", match.URIPrefix, err.Error())
		}
		match.uriRegexp = uriRE
		match.uriCaptureKeys = util.GetFillersUnmarked(match.URIPrefix)
		match.router = rr

		for h, v := range match.Headers {
			if h != "" {
				if util.IsFiller(v) {
					if rp.RequestCapture.headerCaptureKeys[h] == nil {
						captures, regexp := util.ReplaceFillersWithCaptureGroupRegex(v)
						rp.RequestCapture.headerCaptureKeys[h] = types.NewPair(regexp, captures)
					}
				} else if v != "{}" {
					if match.headerValueMatches == nil {
						match.headerValueMatches = map[string]string{}
					}
					match.headerValueMatches[h] = v
				}
			}
		}
		for q, v := range match.Queries {
			if q != "" {
				if util.IsFiller(v) {
					if rp.RequestCapture.queryCaptureKeys[q] == nil {
						captures, regexp := util.ReplaceFillersWithCaptureGroupRegex(v)
						rp.RequestCapture.queryCaptureKeys[q] = types.NewPair(regexp, captures)
					}
				} else if v != "{}" {
					if match.queryValueMatches == nil {
						match.queryValueMatches = map[string]string{}
					}
					match.queryValueMatches[q] = v
				}
			}
		}
	}
	rp.fillers = util.GetFillersUnmarked(string(rp.Payload))
	if len(rp.BodyMatch) > 0 {
		rp.bodyMatchRegexp = regexp.MustCompile("(?i)" + strings.Join(rp.BodyMatch, ".*") + ".*")
	}
	return nil
}

func (rp *ResponsePayload) PrepareJSONStreamPayload(count int) {
	rp.StreamCount = count
	j, ok := util.JSONFromJSONText(string(rp.Payload))
	if ok && !j.IsEmpty() {
		jsonArray := j.ToJSONArray()
		b := []types.RawBytes{}
		if len(jsonArray) > 0 {
			for i := 0; i < count; {
				for _, v := range jsonArray {
					b = append(b, util.ToJSONBytes(v))
					i++
					if i >= count {
						break
					}
				}
			}
		}
		rp.StreamPayload = b
	}
}

func (pp *ProtoPayloads) init() {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	pp.DefaultPayload = nil
	pp.PayloadByURIWithMatches = map[string][]*ResponsePayload{}
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

func (pp *ProtoPayloads) setURIResponsePayloadWithMatches(uri string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		pp.PayloadByURIWithMatches[uri] = append(pp.PayloadByURIWithMatches[uri], rp)
	}
}

func (pp *ProtoPayloads) removeURIResponsePayload(uri string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	delete(pp.PayloadByURIs, uri)
	delete(pp.PayloadByURIWithMatches, uri)
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
	return len(pp.allURIResponsePayloads) > 0 || len(pp.PayloadByURIWithMatches) > 0 ||
		len(pp.PayloadByHeaders) > 0 || len(pp.PayloadByQuery) > 0 || pp.DefaultPayload != nil
}

func (pp *ProtoPayloads) GetMatchingResponsePayload(requestURI string, header http.Header, query url.Values, body io.ReadCloser) (
	newBodyReader io.ReadCloser, responsePayload *ResponsePayload, captures map[string]string, found bool) {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
outer:
	for _, payloads := range pp.PayloadByURIWithMatches {
		for _, rp := range payloads {
			for _, m := range rp.RequestMatches {
				if m.match(requestURI, header, query) {
					responsePayload = rp
					found = true
					break outer
				}
			}
		}
	}
	if !found {
		for uri, rp := range pp.allURIResponsePayloads {
			uriMatched := false
			if rp.uriRegexp != nil && rp.uriRegexp.MatchString(requestURI) {
				uriMatched = true
			}
			if uriMatched {
				if !found && pp.PayloadByURIAndHeaders[uri] != nil {
					responsePayload, found = getPayloadForKV(header, pp.PayloadByURIAndHeaders[uri])
				}
				if !found && pp.PayloadByURIAndQuery[uri] != nil {
					responsePayload, found = getPayloadForKV(query, pp.PayloadByURIAndQuery[uri])
				}
				if !found && pp.PayloadByURIAndBody[uri] != nil && body != nil {
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

func (m *RequestMatch) match(requestURI string, headers http.Header, query url.Values) bool {
	if m.uriRegexp != nil && !m.uriRegexp.MatchString(requestURI) {
		return false
	}
	matched := true
	for k := range m.Headers {
		if !matchKV(k, m.headerValueMatches, headers.Get(k)) {
			matched = false
			break
		}
	}
	if !matched {
		return false
	}
	for k := range m.Queries {
		if !matchKV(k, m.queryValueMatches, query.Get(k)) {
			matched = false
			break
		}
	}
	return matched
}

func matchKV(k string, valueMatches map[string]string, v string) bool {
	if len(v) == 0 {
		return false
	}
	if v2 := valueMatches[k]; v2 != "" {
		if !strings.EqualFold(v, v2) {
			return false
		}
	}
	return true
}

func newRequestCapture() *RequestCapture {
	return &RequestCapture{
		uriCaptureKeys:    []string{},
		headerCaptureKeys: map[string]*types.Pair[*regexp.Regexp, []string]{},
		queryCaptureKeys:  map[string]*types.Pair[*regexp.Regexp, []string]{},
	}
}

func (rc *RequestCapture) NonNil() {
	if rc.headerCaptureKeys == nil {
		rc.headerCaptureKeys = map[string]*types.Pair[*regexp.Regexp, []string]{}
	}
	if rc.queryCaptureKeys == nil {
		rc.queryCaptureKeys = map[string]*types.Pair[*regexp.Regexp, []string]{}
	}
}
