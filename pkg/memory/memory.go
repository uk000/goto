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

package memory

import (
	"encoding/json"
	"fmt"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type Memory struct {
	Context string            `json:"context"`
	Items   map[string]string `json:"items"`
	lock    sync.RWMutex
}

type MemoryExtractor struct {
	ContextKey          string            `json:"contextKey"`
	ContextHeaderMatch  string            `json:"contextHeaderMatch"`
	HeaderMatches       []string          `json:"headerMatches"`
	QueryMatches        []string          `json:"queryMatches"`
	BodyRegexMatches    map[string]string `json:"bodyRegexMatches"`
	BodyJsonPathMatches []*util.JSONPath  `json:"bodyJSONPathMatches"`
	BodyTransforms      []*util.Transform `json:"bodyTransformMatches"`
	bodyMatchRegexps    map[string]*regexp.Regexp
	hasMatches          bool
}

type JSONPathRule types.Triple[*util.JSONPath, *util.JSONPath, string]

type MemoryApplicator struct {
	ContextKey       string                      `json:"contextKey"`
	AddHeaders       map[string]types.StringPair `json:"addHeaders"`
	ReplaceHeaders   map[string]types.StringPair `json:"replaceHeaders"`
	SetHeaders       map[string]string           `json:"setHeaders"`
	AddQueries       map[string]types.StringPair `json:"addQueries"`
	ReplaceQueries   map[string]types.StringPair `json:"replaceQueries"`
	SetQueries       map[string]string           `json:"setQueries"`
	AddBodyRegexes   map[string]string           `json:"replaceRegexes"`
	AddBodyJsonPaths []*JSONPathRule             `json:"replaceJSONPaths"`
	BodyTransforms   []*util.Transform           `json:"bodyTransforms"`
	addBodyRegexps   map[string]*regexp.Regexp
	hasMatches       bool
}

type MemoryManager struct {
	Port             int                         `json:"port"`
	ContextMemory    map[string]*Memory          `json:"contextMemory"`
	MemoryExtractors map[string]*MemoryExtractor `json:"memoryExtractors"`
	allHeaderMatches []string
	hasMatches       bool
	lock             sync.RWMutex
}

var (
	Managers = map[int]*MemoryManager{}
	lock     sync.RWMutex
)

// GetMemoryManager returns the memory manager of the port, creating it on first use.
func GetMemoryManager(port int) *MemoryManager {
	lock.Lock()
	defer lock.Unlock()
	m := Managers[port]
	if m == nil {
		m = newMemoryManager(port)
		Managers[port] = m
	}
	return m
}

// newMemoryManager creates and initializes a memory manager for the port.
func newMemoryManager(port int) *MemoryManager {
	m := &MemoryManager{Port: port}
	m.Init()
	return m
}

// newMemory creates an empty Memory.
func newMemory() *Memory {
	return &Memory{
		Items: map[string]string{},
	}
}

// newMemoryExtractor creates an empty extractor for the context key.
func newMemoryExtractor(ctxKey string) *MemoryExtractor {
	return &MemoryExtractor{
		ContextKey:          ctxKey,
		hasMatches:          false,
		HeaderMatches:       []string{},
		QueryMatches:        []string{},
		BodyRegexMatches:    map[string]string{},
		BodyJsonPathMatches: []*util.JSONPath{},
		BodyTransforms:      []*util.Transform{},
		bodyMatchRegexps:    map[string]*regexp.Regexp{},
	}
}

// Init resets the manager's contexts, extractors and match state.
func (m *MemoryManager) Init() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.ContextMemory = map[string]*Memory{}
	m.MemoryExtractors = map[string]*MemoryExtractor{}
	m.allHeaderMatches = []string{}
	m.hasMatches = false
}

// HasAnyMatches reports whether any extraction match has been configured.
func (m *MemoryManager) HasAnyMatches() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.hasMatches
}

// clone is a copy of the extractor that doesn't change when the extractor is extended. The compiled expressions, the
// JSON paths and the transforms are not changed after they are added, so the copy shares them.
func (mx *MemoryExtractor) clone() *MemoryExtractor {
	if mx == nil {
		return nil
	}
	c := *mx
	c.HeaderMatches = append([]string{}, mx.HeaderMatches...)
	c.QueryMatches = append([]string{}, mx.QueryMatches...)
	c.BodyJsonPathMatches = append([]*util.JSONPath{}, mx.BodyJsonPathMatches...)
	c.BodyTransforms = append([]*util.Transform{}, mx.BodyTransforms...)
	c.BodyRegexMatches = make(map[string]string, len(mx.BodyRegexMatches))
	for k, v := range mx.BodyRegexMatches {
		c.BodyRegexMatches[k] = v
	}
	c.bodyMatchRegexps = make(map[string]*regexp.Regexp, len(mx.bodyMatchRegexps))
	for k, v := range mx.bodyMatchRegexps {
		c.bodyMatchRegexps[k] = v
	}
	return &c
}

// getExtractor is a copy of an extractor, which can be used without holding the lock of the manager.
func (m *MemoryManager) getExtractor(ctxKey string) *MemoryExtractor {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.MemoryExtractors[ctxKey].clone()
}

// GetContextExtractors returns a copy of the extractor of the context key, or the copies of all the extractors when no
// key is given, which can be read while the extractors are being added to.
func (m *MemoryManager) GetContextExtractors(ctxKey string) any {
	m.lock.RLock()
	defer m.lock.RUnlock()
	if ctxKey != "" {
		return m.MemoryExtractors[ctxKey].clone()
	}
	all := make(map[string]*MemoryExtractor, len(m.MemoryExtractors))
	for k, mx := range m.MemoryExtractors {
		all[k] = mx.clone()
	}
	return all
}

// GetContexts returns the memory of all the contexts, which can be read while contexts are being added.
func (m *MemoryManager) GetContexts() map[string]*Memory {
	m.lock.RLock()
	defer m.lock.RUnlock()
	all := make(map[string]*Memory, len(m.ContextMemory))
	for k, v := range m.ContextMemory {
		all[k] = v
	}
	return all
}

// MatchContext finds the first extractor whose context header is present in the headers, returning its context key and the header value as the context.
func (m *MemoryManager) MatchContext(headers http.Header) (bool, string, string) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	for ctxKey, mx := range m.MemoryExtractors {
		v := headers.Get(mx.ContextHeaderMatch)
		if v != "" {
			return true, ctxKey, v
		}
	}
	return false, "", ""
}

// ExtractFromHeaders stores into the context's memory the headers that the context key's extractor matches.
func (m *MemoryManager) ExtractFromHeaders(ctxKey, ctx string, headers http.Header) {
	mx := m.getExtractor(ctxKey)
	if mx != nil {
		for _, k := range mx.HeaderMatches {
			v := headers.Get(k)
			if v != "" {
				m.GetOrAddContext(ctx).Add(k, v)
			}
		}
	}
}

// ExtractFromQuery stores into the context's memory the query params that the context key's extractor matches.
func (m *MemoryManager) ExtractFromQuery(ctxKey, ctx string, query url.Values) {
	mx := m.getExtractor(ctxKey)
	if mx != nil {
		for _, k := range mx.QueryMatches {
			v := query.Get(k)
			if v != "" {
				m.GetOrAddContext(ctx).Add(k, v)
			}
		}
	}
}

// ExtractFromBody applies the extractor's body transforms, regexes and JSON paths to the body and stores the captures in the context's memory.
func (m *MemoryManager) ExtractFromBody(ctxKey, ctx string, body io.ReadCloser, isYAML bool) {
	mx := m.getExtractor(ctxKey)
	if mx == nil {
		return
	}
	rr := util.CreateOrGetReReader(body)
	memory := m.GetOrAddContext(ctx)
	bodyText := rr.Text()
	if len(mx.BodyTransforms) > 0 {
		bodyText = util.TransformPayload(bodyText, mx.BodyTransforms, isYAML)
	}
	if len(mx.bodyMatchRegexps) > 0 {
		for k, re := range mx.bodyMatchRegexps {
			matches := re.FindAllString(bodyText, -1)
			for _, val := range matches {
				memory.Add(k, val)
			}
		}
	}
	if len(mx.BodyJsonPathMatches) > 0 {
		var data map[string]interface{}
		if err := util.ReadJson(bodyText, &data); err == nil {
			for _, jp := range mx.BodyJsonPathMatches {
				captures, _ := jp.FindResults(data)
				for k, v := range captures {
					memory.Add(k, v)
				}
			}
		}
	}
}

// AddContext creates a fresh, empty memory for the context, replacing any existing one.
func (m *MemoryManager) AddContext(ctx string) *Memory {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.ContextMemory[ctx] = newMemory()
	return m.ContextMemory[ctx]
}

// GetOrAddContext returns the memory of the context, creating it if it doesn't exist.
func (m *MemoryManager) GetOrAddContext(ctx string) *Memory {
	m.lock.RLock()
	memory := m.ContextMemory[ctx]
	m.lock.RUnlock()
	if memory == nil {
		m.lock.Lock()
		defer m.lock.Unlock()
		// another caller may have added it while the lock was not held
		if memory = m.ContextMemory[ctx]; memory == nil {
			memory = newMemory()
			m.ContextMemory[ctx] = memory
		}
	}
	return memory
}

// unsafeGetOrAddContextExtractor expects the caller to hold the lock of the manager.
func (m *MemoryManager) unsafeGetOrAddContextExtractor(ctxKey string) *MemoryExtractor {
	if m.MemoryExtractors[ctxKey] == nil {
		m.MemoryExtractors[ctxKey] = newMemoryExtractor(ctxKey)
	}
	return m.MemoryExtractors[ctxKey]
}

// GetOrAddContextExtractor returns the extractor that is in use, and not a copy of it, so it must not be read while
// extractors are being added. Use GetContextExtractors for that.
func (m *MemoryManager) GetOrAddContextExtractor(ctxKey string) *MemoryExtractor {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.unsafeGetOrAddContextExtractor(ctxKey)
}

// AddContextMatch sets the header whose value identifies the context for the context key.
func (m *MemoryManager) AddContextMatch(ctxKey, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.unsafeGetOrAddContextExtractor(ctxKey).ContextHeaderMatch = match
}

// AddHeaderMatch adds a header to extract into memory for the context key.
func (m *MemoryManager) AddHeaderMatch(ctxKey, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.unsafeGetOrAddContextExtractor(ctxKey)
	mx.HeaderMatches = append(mx.HeaderMatches, match)
	mx.hasMatches = true
	m.hasMatches = true
}

// AddQueryMatch adds a query param to extract into memory for the context key.
func (m *MemoryManager) AddQueryMatch(ctxKey, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.unsafeGetOrAddContextExtractor(ctxKey)
	mx.QueryMatches = append(mx.QueryMatches, match)
	mx.hasMatches = true
	m.hasMatches = true
}

// AddBodyRegexMatch fails, and adds nothing, when the regex isn't valid.
func (m *MemoryManager) AddBodyRegexMatch(ctxKey, key, match string) error {
	re, err := regexp.Compile("(?i)" + match)
	if err != nil {
		return fmt.Errorf("invalid regex [%s]: %w", match, err)
	}
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.unsafeGetOrAddContextExtractor(ctxKey)
	mx.BodyRegexMatches[key] = match
	mx.bodyMatchRegexps[key] = re
	mx.hasMatches = true
	m.hasMatches = true
	return nil
}

// AddBodyJsonPathMatch adds comma-separated JSON paths to extract from the body into memory for the context key.
func (m *MemoryManager) AddBodyJsonPathMatch(ctxKey, match string) {
	paths := strings.Split(match, ",")
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.unsafeGetOrAddContextExtractor(ctxKey)
	mx.BodyJsonPathMatches = append(mx.BodyJsonPathMatches, util.NewJSONPath().Parse(paths))
	mx.hasMatches = true
	m.hasMatches = true
}

// SetBodyTransformation fails, and changes nothing, when a mapping has an invalid condition.
func (m *MemoryManager) SetBodyTransformation(ctxKey string, transforms []*util.Transform) error {
	for _, t := range transforms {
		if t == nil {
			return fmt.Errorf("invalid transform: null")
		}
		for _, jm := range t.Mappings {
			if jm == nil {
				return fmt.Errorf("invalid transform: null mapping")
			}
			// the conditions of the mappings are compiled by Init, which panics for the ones that aren't valid
			for _, cond := range []string{jm.IfContains, jm.IfNotContains} {
				if cond != "" {
					if _, err := regexp.Compile("(?i)" + cond); err != nil {
						return fmt.Errorf("invalid condition [%s]: %w", cond, err)
					}
				}
			}
		}
	}
	for _, t := range transforms {
		for _, jm := range t.Mappings {
			jm.Init()
		}
	}
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.unsafeGetOrAddContextExtractor(ctxKey)
	mx.BodyTransforms = transforms
	mx.hasMatches = true
	m.hasMatches = true
	return nil
}

// Add stores a key and value in the memory.
func (m *Memory) Add(key string, value string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.Items[key] = value
}

// Get returns the value stored for the key.
func (m *Memory) Get(key string) string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.Items[key]
}

// Clear removes all items from the memory.
func (m *Memory) Clear() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.Items = map[string]string{}
}

// MarshalJSON reports the items while they can be added to.
func (m *Memory) MarshalJSON() ([]byte, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return json.Marshal(struct {
		Context string            `json:"context"`
		Items   map[string]string `json:"items"`
	}{m.Context, m.Items})
}

// middlewareFunc extracts configured headers, query params and body values of matching requests into memory, then calls the next handler.
func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if !rs.IsKnownNonTraffic {
			port := util.GetRequestOrListenerPortNum(r)
			m := GetMemoryManager(port)
			if m.HasAnyMatches() {
				found, ctxKey, ctx := m.MatchContext(r.Header)
				if found {
					m.ExtractFromHeaders(ctxKey, ctx, r.Header)
					m.ExtractFromQuery(ctxKey, ctx, r.URL.Query())
					m.ExtractFromBody(ctxKey, ctx, r.Body, util.IsYAMLContentType(r.Header))
				}
			}
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
