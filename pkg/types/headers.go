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

func NewHeaders() *Headers {
	return &Headers{
		Request:  NewHeadersConfig(),
		Response: NewHeadersConfig(),
	}
}

func NewHeadersConfig() *HeadersConfig {
	return &HeadersConfig{
		Add:     map[string]string{},
		Remove:  []string{},
		Forward: []string{},
	}
}

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

func (hc *HeadersConfig) clone() *HeadersConfig {
	c := NewHeadersConfig()
	if hc != nil {
		maps.Copy(c.Add, hc.Add)
		c.Remove = append(c.Remove, hc.Remove...)
		c.Forward = append(c.Forward, hc.Forward...)
	}
	return c
}

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

func (hc *HeadersConfig) NonNil() {
	if hc != nil && hc.Add == nil {
		hc.Add = map[string]string{}
	}
}

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

func (hc *HeadersConfig) Union(hc2 *HeadersConfig) *HeadersConfig {
	hcNew := NewHeadersConfig()
	hcNew.Merge(hc)
	hcNew.Merge(hc2)
	return hcNew
}

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

func (sh SimpleHTTPHeaders) Get(k string) string {
	return sh[k]
}

func (sh SimpleHTTPHeaders) Add(k, v string) {
	sh[k] = v
}

func (sh SimpleHTTPHeaders) Del(k string) {
	delete(sh, k)
}

func (md MD) Add(k, v string) {
	md[k] = append(md[k], v)
}

func (md MD) Del(k string) {
	delete(md, k)
}
