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

package types

import (
	"iter"
	"maps"

	"google.golang.org/grpc/metadata"
)

type SimpleHTTPHeaders map[string]string
type MD metadata.MD

type HeadersConfig struct {
	Add     map[string]string `yaml:"add,omitempty" json:"add,omitempty"`
	Remove  []string          `yaml:"remove,omitempty" json:"remove,omitempty"`
	Forward []string          `yaml:"forward,omitempty" json:"forward,omitempty"`
}

type Headers struct {
	Request  *HeadersConfig `yaml:"request,omitempty" json:"request,omitempty"`
	Response *HeadersConfig `yaml:"response,omitempty" json:"response,omitempty"`
}

type MutatingHeaders interface {
	Add(string, string)
	Del(string)
}

type ReadableHeaders interface {
	Get(string) string
}

// NewHeaders creates headers with empty request and response configs.
func NewHeaders() *Headers {
	return &Headers{
		Request:  NewHeadersConfig(),
		Response: NewHeadersConfig(),
	}
}

// NewHeadersConfig creates a headers config with empty add, remove and forward entries.
func NewHeadersConfig() *HeadersConfig {
	return &HeadersConfig{
		Add:     map[string]string{},
		Remove:  []string{},
		Forward: []string{},
	}
}

// Union returns new headers that merge h1 and then h2, either of which can be nil.
func Union(h1, h2 *Headers) *Headers {
	hNew := NewHeaders()
	if h1 != nil {
		hNew.Merge(h1)
	}
	if h2 != nil {
		hNew.Merge(h2)
	}
	return hNew
}

// Clone returns a deep copy of the headers, that has nothing in common with them. The copy is always set up (NonNil):
// what is missing in the headers (the request, the response, their maps) is empty in the copy.
func (h *Headers) Clone() *Headers {
	if h == nil {
		return nil
	}
	return &Headers{Request: h.Request.clone(), Response: h.Response.clone()}
}

// clone returns a deep copy of the config, which is an empty one if the config is nil.
func (hc *HeadersConfig) clone() *HeadersConfig {
	c := NewHeadersConfig()
	if hc != nil {
		maps.Copy(c.Add, hc.Add)
		c.Remove = append(c.Remove, hc.Remove...)
		c.Forward = append(c.Forward, hc.Forward...)
	}
	return c
}

// NonNil makes sure that the request and response configs, and their maps, exist.
func (h *Headers) NonNil() {
	if h == nil {
		return
	}
	if h.Request == nil {
		h.Request = NewHeadersConfig()
	}
	h.Request.NonNil()
	if h.Response == nil {
		h.Response = NewHeadersConfig()
	}
	h.Response.NonNil()
}

// NonNil makes sure that the Add map exists.
func (hc *HeadersConfig) NonNil() {
	if hc != nil && hc.Add == nil {
		hc.Add = map[string]string{}
	}
}

// HasForwardHeaders tells if the request config lists any headers to forward.
func (hc *Headers) HasForwardHeaders() bool {
	return hc != nil && hc.Request != nil && len(hc.Request.Forward) > 0
}

// Merge adds the headers of h2 to these headers, which are set up if they are not (a Headers{} has no request or response).
func (h *Headers) Merge(h2 *Headers) {
	if h == nil || h2 == nil {
		return
	}
	h.NonNil()
	h.Request.Merge(h2.Request)
	h.Response.Merge(h2.Response)
}

// Merge adds the entries of hc2 to this config: Add entries override, and Remove and Forward are appended.
func (hc *HeadersConfig) Merge(hc2 *HeadersConfig) {
	if hc == nil || hc2 == nil {
		return
	}
	hc.NonNil()
	for k, v := range hc2.Add {
		hc.Add[k] = v
	}
	hc.Remove = append(hc.Remove, hc2.Remove...)
	hc.Forward = append(hc.Forward, hc2.Forward...)
}

// Union returns a new config that merges hc and then hc2.
func (hc *HeadersConfig) Union(hc2 *HeadersConfig) *HeadersConfig {
	hcNew := NewHeadersConfig()
	hcNew.Merge(hc)
	hcNew.Merge(hc2)
	return hcNew
}

// UpdateHeaders applies the config to the headers by removing the Remove ones first and then adding the Add ones.
func (hc *HeadersConfig) UpdateHeaders(headers MutatingHeaders) {
	if hc == nil {
		return
	}
	if len(hc.Remove) > 0 {
		for _, h := range hc.Remove {
			headers.Del(h)
		}
	}
	if len(hc.Add) > 0 {
		for h, v := range hc.Add {
			headers.Add(h, v)
		}
	}
}

// ForwardHeaders copies those of the named headers that have a value from the source headers to the target headers.
func ForwardHeaders(sourceHeaders ReadableHeaders, targetHeaders MutatingHeaders, forwardHeaders iter.Seq[string]) {
	forwardedHeaders := map[string]string{}
	if forwardHeaders != nil && sourceHeaders != nil {
		for h := range forwardHeaders {
			v := sourceHeaders.Get(h)
			if len(v) > 0 {
				forwardedHeaders[h] = v
				targetHeaders.Add(h, v)
			}
		}
	}
}

// Get returns the value of the header.
func (sh SimpleHTTPHeaders) Get(k string) string {
	return sh[k]
}

// Add sets the header, replacing any earlier value.
func (sh SimpleHTTPHeaders) Add(k, v string) {
	sh[k] = v
}

// Del removes the header.
func (sh SimpleHTTPHeaders) Del(k string) {
	delete(sh, k)
}

// Add appends the value to the metadata key.
func (md MD) Add(k, v string) {
	md[k] = append(md[k], v)
}

// Del removes the metadata key.
func (md MD) Del(k string) {
	delete(md, k)
}
