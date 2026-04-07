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
	"net/http"
)

type SimpleHTTPHeaders map[string]string

type HeadersConfig struct {
	Add     map[string]string `yaml:"add,omitempty" json:"add,omitempty"`
	Remove  []string          `yaml:"remove,omitempty" json:"remove,omitempty"`
	Forward []string          `yaml:"forward,omitempty" json:"forward,omitempty"`
}

type Headers struct {
	Request  *HeadersConfig `yaml:"request,omitempty" json:"request,omitempty"`
	Response *HeadersConfig `yaml:"response,omitempty" json:"response,omitempty"`
}

type AddableHeaders interface {
	Add(string, string)
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

func (h *Headers) Clone() *Headers {
	h2 := NewHeaders()
	h2.Request.Add = maps.Clone(h.Request.Add)
	h2.Request.Remove = h.Request.Remove
	h2.Request.Forward = h.Request.Forward
	return h2
}

func (h *Headers) NonNil() {
	if h.Request == nil {
		h.Request = NewHeadersConfig()
	}
	if h.Response == nil {
		h.Response = NewHeadersConfig()
	}
}

func (hc *Headers) HasForwardHeaders() bool {
	return hc.Request != nil && len(hc.Request.Forward) > 0
}

func (h *Headers) Merge(h2 *Headers) {
	if h2 == nil {
		return
	}
	h.Request.Merge(h2.Request)
	h.Response.Merge(h2.Response)
}

func (hc *HeadersConfig) Merge(hc2 *HeadersConfig) {
	if hc2 == nil {
		return
	}
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

func (hc *HeadersConfig) UpdateHeaders(headers http.Header, info string) {
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

func ForwardHeaders(sourceHeaders ReadableHeaders, targetHeaders AddableHeaders, forwardHeaders iter.Seq[string], info string) {
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
