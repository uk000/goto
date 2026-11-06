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
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/tls"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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

type PayloadPartProcess struct {
	SplitWith    string `json:"splitWith,omitempty"`
	JoinWith     string `json:"joinWith,omitempty"`
	Base64Encode []bool `json:"base64Encode,omitempty"`
	Base64Decode []bool `json:"base64Decode,omitempty"`
	Keep         []bool `json:"keep,omitempty"`
	Sign         []bool `json:"sign,omitempty"`
}

type PayloadParts struct {
	Pre  *PayloadPartProcess `json:"pre,omitempty"`
	Post *PayloadPartProcess `json:"post,omitempty"`
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
	Replace          map[string]string `json:"replace,omitempty"`
	Parts            *PayloadParts     `json:"parts,omitempty"`
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

type protoPayloadsJSON ProtoPayloads

// MarshalJSON reports the payloads while holding the lock that protects them, so that listing the payloads is safe while
// they are being changed.
func (pp *ProtoPayloads) MarshalJSON() ([]byte, error) {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	return json.Marshal((*protoPayloadsJSON)(pp))
}

// NewRequestMatch creates a request match for the given URI prefix.
func NewRequestMatch(uriPrefix string) *RequestMatch {
	return &RequestMatch{
		URIPrefix: uriPrefix,
	}
}

// newResponsePayload builds a payload for the given URI, header, query and body (regex or JSON path) match scope, compiling and validating the matchers and registering the URI route before anything is stored.
func newResponsePayload(payload []byte, stream, binary bool, contentType, uri, header, query, value string,
	bodyRegexes []string, paths []string, transforms []*util.Transform, delayMin, delayMax time.Duration) (*ResponsePayload, error) {
	if contentType == "" {
		contentType = constants.ContentTypeJSON
	}
	// The matches are validated before the URI is registered, so that a payload that is refused leaves no route behind.
	queryMatchRegexp, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return nil, fmt.Errorf("invalid query match [%s]: %s", query, err.Error())
	}
	var bodyMatchRegexp *regexp.Regexp
	if len(bodyRegexes) > 0 {
		if bodyMatchRegexp, err = compileBodyMatch(bodyRegexes); err != nil {
			return nil, err
		}
	}
	_, uriRegExp, responseRouter, err := util.BuildURIMatcher(uri, true, handleURI)
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

	jsonPaths := util.NewJSONPath().Parse(normalizeBodyPaths(paths))

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
		queryMatchRegexp: queryMatchRegexp,
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

// normalizeBodyPaths lets the JSON paths of a body match be written the way the documentation writes them, the path
// first and then the name that its value is captured into (.foo.bar={a}). JSON paths are parsed from the other order,
// the name first and then the path (a={.foo.bar}), under which the documented form is a path that never matches.
func normalizeBodyPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, p := range paths {
		if pieces := strings.Split(p, "="); len(pieces) == 2 && strings.HasPrefix(pieces[0], ".") {
			name := pieces[1]
			if strings.HasPrefix(name, "{") && strings.HasSuffix(name, "}") && !strings.HasPrefix(name, "{.") &&
				!strings.ContainsAny(name[1:len(name)-1], "{}") {
				p = name[1:len(name)-1] + "={" + pieces[0] + "}"
			}
		}
		normalized = append(normalized, p)
	}
	return normalized
}

// compileBodyMatch builds the regular expression that a body is matched with: the keywords have to appear in the body
// in the given order.
func compileBodyMatch(keywords []string) (*regexp.Regexp, error) {
	re, err := regexp.Compile("(?i)" + strings.Join(keywords, ".*") + ".*")
	if err != nil {
		return nil, fmt.Errorf("invalid body match %v: %s", keywords, err.Error())
	}
	return re, nil
}

// NewResponsePayload creates a payload with the given request matches, captures and encoding options.
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

// Process validates the payload and prepares it for use: compiles the body match, URI matchers and header/query captures of its request matches; errors if matches or payload are missing.
func (rp *ResponsePayload) Process() error {
	if len(rp.RequestMatches) == 0 {
		return fmt.Errorf("Matches required")
	}
	if len(rp.Payload) == 0 && len(rp.StreamPayload) == 0 {
		return fmt.Errorf("Payload required")
	}
	// The body match is validated first, so that a payload that is refused leaves no route behind.
	var bodyMatchRegexp *regexp.Regexp
	if len(rp.BodyMatch) > 0 {
		var err error
		if bodyMatchRegexp, err = compileBodyMatch(rp.BodyMatch); err != nil {
			return err
		}
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
		match.URIPrefix = strings.ToLower(match.URIPrefix)
		_, uriRE, rr, err := util.BuildURIMatcher(match.URIPrefix, true, handleURI)
		if err != nil {
			return fmt.Errorf("failed to add URI match %s with error: %s\n", match.URIPrefix, err.Error())
		}
		match.uriRegexp = uriRE
		match.uriCaptureKeys = util.GetFillersUnmarked(match.URIPrefix)
		match.router = rr

		for h, v := range match.Headers {
			if h != "" {
				h = strings.ToLower(h)
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
				q = strings.ToLower(q)
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
	rp.bodyMatchRegexp = bodyMatchRegexp
	return nil
}

// PrepareJSONStreamPayload builds a stream of count items from the JSON array in the payload, repeating its elements as needed.
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

// init resets all the payload registries.
func (pp *ProtoPayloads) init() {
	pp.lock.Lock()
	defer pp.lock.Unlock()
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

// setDefaultResponsePayload sets the payload used when nothing else matches.
func (pp *ProtoPayloads) setDefaultResponsePayload(rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	pp.DefaultPayload = rp
}

// setURIResponsePayload registers the payload for a URI.
func (pp *ProtoPayloads) setURIResponsePayload(uri string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		pp.PayloadByURIs[uri] = rp
		pp.allURIResponsePayloads[uri] = rp
	}
}

// setURIResponsePayloadWithMatches adds a payload that has its own request matches under a URI prefix.
func (pp *ProtoPayloads) setURIResponsePayloadWithMatches(uri string, rp *ResponsePayload) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	if rp != nil {
		pp.PayloadByURIWithMatches[uri] = append(pp.PayloadByURIWithMatches[uri], rp)
	}
}

// removeURIResponsePayload removes the payloads registered for a URI, and tracks it no further if nothing else uses it.
func (pp *ProtoPayloads) removeURIResponsePayload(uri string) {
	pp.lock.Lock()
	defer pp.lock.Unlock()
	delete(pp.PayloadByURIs, uri)
	delete(pp.PayloadByURIWithMatches, uri)
	pp.unsafeRemoveUntrackedURI(uri)
}

// unsafeRemoveUntrackedURI stops tracking a URI once no payload registry has an entry for it; the caller must hold the lock.
func (pp *ProtoPayloads) unsafeRemoveUntrackedURI(uri string) {
	if !(pp.PayloadByURIs[uri] != nil || pp.PayloadByURIAndHeaders[uri] != nil ||
		pp.PayloadByURIAndQuery[uri] != nil || pp.PayloadByURIAndBody[uri] != nil) {
		delete(pp.allURIResponsePayloads, uri)
	}
}

// setHeaderResponsePayload registers the payload for a request header and its value match.
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

// removeHeaderResponsePayload removes the payload of a request header and value (a filler value means any value).
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

// setURIWithHeaderResponsePayload registers the payload for a URI combined with a request header and its value match.
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

// removeURIWithHeaderResponsePayload removes the payload of a URI with header and value, pruning emptied entries.
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

// setQueryResponsePayload registers the payload for a request query param and its value match.
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

// removeQueryResponsePayload removes the payload of a query param and value (a filler value means any value).
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

// setURIWithQueryResponsePayload registers the payload for a URI combined with a request query param and its value match.
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

// removeURIWithQueryResponsePayload removes the payload of a URI with query and value, pruning emptied entries.
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

// setURIWithBodyMatchResponsePayload registers the payload for a URI combined with a body match.
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

// removeURIWithBodyMatchResponsePayload removes the payload of a URI with body match, pruning emptied entries.
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

// HasAnyPayload tells if any payload (URI, matches, header, query or default) is configured.
func (pp *ProtoPayloads) HasAnyPayload() bool {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	return len(pp.allURIResponsePayloads) > 0 || len(pp.PayloadByURIWithMatches) > 0 ||
		len(pp.PayloadByHeaders) > 0 || len(pp.PayloadByQuery) > 0 || pp.DefaultPayload != nil
}

// sortedURIs orders URIs so that the more specific one (the longer one) comes first, and the order of URIs of the same
// length is the same every time.
func sortedURIs(uris []string) []string {
	sort.Slice(uris, func(i, j int) bool {
		if len(uris[i]) != len(uris[j]) {
			return len(uris[i]) > len(uris[j])
		}
		return uris[i] < uris[j]
	})
	return uris
}

// GetMatchingResponsePayload finds the payload for a request. The candidates are tried in this order, and the first one
// that matches wins: payloads with request matches, URI with header, URI with query, URI with body keywords, URI with
// body JSON paths, URI alone, header, query, and the default payload. A candidate category is tried against all the
// URIs that match the request before the next category is, so that the choice doesn't depend on the number or the names
// of the URIs that match. If the body was read to look for a match, the reader that is returned gives it again.
func (pp *ProtoPayloads) GetMatchingResponsePayload(requestURI string, header http.Header, query url.Values, body io.ReadCloser) (
	newBodyReader io.ReadCloser, responsePayload *ResponsePayload, captures map[string]string, found bool) {
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	withMatches := make([]string, 0, len(pp.PayloadByURIWithMatches))
	for uri := range pp.PayloadByURIWithMatches {
		withMatches = append(withMatches, uri)
	}
	for _, uri := range sortedURIs(withMatches) {
		for _, rp := range pp.PayloadByURIWithMatches[uri] {
			for _, m := range rp.RequestMatches {
				if m.match(requestURI, header, query) {
					return nil, rp, nil, true
				}
			}
		}
	}
	matchingURIs := []string{}
	for uri, rp := range pp.allURIResponsePayloads {
		if rp.uriRegexp != nil && rp.uriRegexp.MatchString(requestURI) {
			matchingURIs = append(matchingURIs, uri)
		}
	}
	sortedURIs(matchingURIs)
	for _, uri := range matchingURIs {
		if pp.PayloadByURIAndHeaders[uri] != nil {
			if responsePayload, found = getPayloadForKV(header, pp.PayloadByURIAndHeaders[uri]); found {
				return
			}
		}
	}
	for _, uri := range matchingURIs {
		if pp.PayloadByURIAndQuery[uri] != nil {
			if responsePayload, found = getPayloadForKV(query, pp.PayloadByURIAndQuery[uri]); found {
				return
			}
		}
	}
	if body != nil {
		bodyRead := false
		bodyText := ""
		for _, uri := range matchingURIs {
			if len(pp.PayloadByURIAndBody[uri]) == 0 {
				continue
			}
			if !bodyRead {
				bodyText = util.Read(body)
				bodyRead = true
				newBodyReader = io.NopCloser(strings.NewReader(bodyText))
			}
			if responsePayload, captures, found = matchBody(bodyText, pp.PayloadByURIAndBody[uri]); found {
				return
			}
		}
	}
	for _, uri := range matchingURIs {
		if pp.PayloadByURIs[uri] != nil {
			return newBodyReader, pp.PayloadByURIs[uri], nil, true
		}
	}
	if responsePayload, found = getPayloadForKV(header, pp.PayloadByHeaders); found {
		return
	}
	if responsePayload, found = getPayloadForKV(query, pp.PayloadByQuery); found {
		return
	}
	if pp.DefaultPayload != nil {
		return newBodyReader, pp.DefaultPayload, nil, true
	}
	return newBodyReader, nil, nil, false
}

// match tells if the request URI, headers and queries satisfy the match; headers and queries must be present, and equal their configured values where one is set.
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

// matchKV tells if a header or query value is present and equals the configured value for that name, if any (case-insensitively).
func matchKV(k string, valueMatches map[string]string, v string) bool {
	if len(v) == 0 {
		return false
	}
	// The value matches are kept under the lowercase name of the header or the query.
	if v2 := valueMatches[strings.ToLower(k)]; v2 != "" {
		if !strings.EqualFold(v, v2) {
			return false
		}
	}
	return true
}

// newRequestCapture creates a request capture with empty capture maps.
func newRequestCapture() *RequestCapture {
	return &RequestCapture{
		uriCaptureKeys:    []string{},
		headerCaptureKeys: map[string]*types.Pair[*regexp.Regexp, []string]{},
		queryCaptureKeys:  map[string]*types.Pair[*regexp.Regexp, []string]{},
	}
}

// NonNil ensures the header and query capture maps are allocated.
func (rc *RequestCapture) NonNil() {
	if rc.headerCaptureKeys == nil {
		rc.headerCaptureKeys = map[string]*types.Pair[*regexp.Regexp, []string]{}
	}
	if rc.queryCaptureKeys == nil {
		rc.queryCaptureKeys = map[string]*types.Pair[*regexp.Regexp, []string]{}
	}
}

// encodePayload returns the payload base64 (raw URL) encoded.
func (rp *ResponsePayload) encodePayload(payload []byte) []byte {
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(payload)))
	base64.RawURLEncoding.Encode(encoded, []byte(payload))
	return encoded
}

// decodePayload returns the base64 (raw URL) decoded payload, cleaned and re-marshaled if the payload is JSON; logs and returns it unchanged if decoding fails.
func (rp *ResponsePayload) decodePayload(payload []byte) []byte {
	decoded := make([]byte, base64.RawURLEncoding.DecodedLen(len(payload)))
	if _, err := base64.RawURLEncoding.Decode(decoded, payload); err != nil {
		log.Printf("Failed to decode payload with error: %s\n", err.Error())
	} else {
		decoded = util.CleanJSONBytes(decoded)
		if rp.IsJSON {
			var jsonPayload any
			var err error
			if err = json.Unmarshal(decoded, &jsonPayload); err == nil {
				jsonPayload = util.CleanJSON(jsonPayload)
				payload, _ = json.Marshal(jsonPayload)
			} else {
				payload = decoded
			}
		} else {
			payload = decoded
		}
	}
	return payload
}

// encodeDecodeParts base64 encodes or decodes each part according to the per-part encode and decode flags.
func (rp *ResponsePayload) encodeDecodeParts(parts [][]byte, encode []bool, decode []bool) [][]byte {
	processedParts := [][]byte{}
	for i, part := range parts {
		if len(encode) > i && encode[i] {
			processedParts = append(processedParts, rp.encodePayload(part))
		} else if len(decode) > i && decode[i] {
			processedParts = append(processedParts, rp.decodePayload(part))
		} else {
			processedParts = append(processedParts, part)
		}
	}
	return processedParts
}

// preProcess splits the payload into parts and applies the pre-processing base64 encode/decode, if parts are configured.
func (rp *ResponsePayload) preProcess() [][]byte {
	var rawParts [][]byte
	if rp.Parts != nil && rp.Parts.Pre != nil {
		var payloadParts [][]byte
		if rp.Parts.Pre.SplitWith != "" {
			payloadParts = bytes.Split(rp.Payload, []byte(rp.Parts.Pre.SplitWith))
		} else {
			payloadParts = [][]byte{rp.Payload}
		}
		rawParts = rp.encodeDecodeParts(payloadParts, rp.Parts.Pre.Base64Encode, rp.Parts.Pre.Base64Decode)
	} else {
		rawParts = [][]byte{rp.Payload}
	}
	return rawParts
}

// joinSplit concatenates the parts and splits the result again on the separator.
func joinSplit(parts [][]byte, sep []byte) [][]byte {
	joined := []byte{}
	for _, b := range parts {
		joined = append(joined, b...)
	}
	return bytes.Split(joined, sep)
}

// signParts signs the parts flagged for signing with the CA key (RSA SHA-256) and substitutes the base64 signature for the [SIGNATURE] marker.
func (rp *ResponsePayload) signParts(parts [][]byte) {
	signableParts := [][]byte{}
	for i, p := range parts {
		if len(rp.Parts.Post.Sign) > i && rp.Parts.Post.Sign[i] {
			signableParts = append(signableParts, p)
		}
	}
	signatureInput := bytes.Join(signableParts, []byte(rp.Parts.Post.JoinWith))
	hasher := sha256.New()
	hasher.Write(signatureInput)
	hash := hasher.Sum(nil)
	signatureBytes, err := rsa.SignPKCS1v15(rand.Reader, tls.DefaultCAKey, crypto.SHA256, hash)
	if err != nil {
		fmt.Printf("Signing failed: %v\n", err)
		return
	}
	for i, part := range parts {
		if bytes.Contains(part, []byte("[SIGNATURE]")) {
			parts[i] = bytes.ReplaceAll(part, []byte("[SIGNATURE]"),
				[]byte(base64.RawURLEncoding.EncodeToString(signatureBytes)))
		}
	}
}

// postProcess applies the post-processing of parts (re-split, keep, base64 encode/decode, sign, join) and returns the final payload bytes.
func (rp *ResponsePayload) postProcess(parts [][]byte) []byte {
	var payload []byte
	if rp.Parts != nil && rp.Parts.Post != nil {
		if rp.Parts.Post.SplitWith != "" {
			parts = joinSplit(parts, []byte(rp.Parts.Post.SplitWith))
		}
		keptParts := [][]byte{}
		for i, part := range parts {
			if len(rp.Parts.Post.Keep) > i {
				if rp.Parts.Post.Keep[i] {
					keptParts = append(keptParts, part)
				}
			} else {
				keptParts = append(keptParts, part)
			}
		}
		parts = keptParts
		parts = rp.encodeDecodeParts(parts, rp.Parts.Post.Base64Encode, rp.Parts.Post.Base64Decode)
		if len(rp.Parts.Post.Sign) > 0 {
			rp.signParts(parts)
		}
		if rp.Parts.Post.JoinWith != "" {
			payload = bytes.Join(parts, []byte(rp.Parts.Post.JoinWith))
		}
	} else {
		// Without post-processing of parts, the base64Encode and base64Decode of the payload apply to the payload. They
		// used to apply only to a payload that had parts (of which none had post-processing).
		parts = rp.encodeDecodeParts(parts, []bool{rp.Base64Encode}, []bool{rp.Base64Decode})
	}
	if len(payload) == 0 {
		for _, b := range parts {
			payload = append(payload, b...)
		}
	}
	return payload
}

// getFilledPayload returns the payload with its markers filled from the request URI vars, headers, queries, body transforms and body-match captures, then post-processed and with the configured replacements applied.
func (rp *ResponsePayload) getFilledPayload(r *http.Request, captures map[string]string) []byte {
	vars := mux.Vars(r)
	query := r.URL.Query()
	payloadParts := rp.preProcess()
	rawParts := [][]byte{}
	for _, b := range payloadParts {
		part := string(b)
		part = util.SubstitutePayloadMarkers(part, rp.URICaptureKeys, vars)
		if rp.HeaderCaptureKey != "" {
			if value := r.Header.Get(rp.HeaderMatch); value != "" {
				part = strings.Replace(part, rp.HeaderCaptureKey, value, -1)
			}
		}
		if rp.RequestCapture != nil {
			for k, pair := range rp.RequestCapture.headerCaptureKeys {
				part = processCaptures(part, r.Header.Get(k), pair, rp.DetectJSON, rp.EscapeJSON)
			}
			for k, pair := range rp.RequestCapture.queryCaptureKeys {
				part = processCaptures(part, query.Get(k), pair, rp.DetectJSON, rp.EscapeJSON)
			}
		}
		if rp.QueryCaptureKey != "" {
			for k, values := range query {
				if rp.queryMatchRegexp.MatchString(k) && len(values) > 0 {
					part = strings.Replace(part, rp.QueryCaptureKey, values[0], -1)
				}
			}
		}
		if len(rp.Transforms) > 0 {
			part = util.TransformPayload(util.Read(r.Body), rp.Transforms, util.IsYAMLContentType(r.Header))
		}
		for k, v := range captures {
			part = strings.Replace(part, util.MarkFiller(k), v, -1)
		}
		rawParts = append(rawParts, []byte(part))
	}
	rawPayload := rp.postProcess(rawParts)
	for from, to := range rp.Replace {
		rawPayload = bytes.ReplaceAll(rawPayload, []byte(from), []byte(to))
	}
	return rawPayload
}

// processCaptures replaces the capture keys in the payload with the values captured from the given header or query value, quoting JSON-looking values as configured.
func processCaptures(payload string, value string, pair *types.Pair[*regexp.Regexp, []string], detectJSON, escapteJSON bool) string {
	if value != "" && pair.Left != nil {
		captures := util.GetCaptureGroupValues(pair.Left, pair.Right, value)
		for k, v := range captures {
			isJSONValue := strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") || strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}")
			if isJSONValue {
				if detectJSON {
					k = "\"" + k + "\""
				} else if escapteJSON {
					if v2, err := json.Marshal(v); err == nil {
						v = string(v2)
					}
					k = "\"" + k + "\""
				}
			}
			payload = strings.Replace(payload, k, v, -1)
		}
	}
	return payload
}
