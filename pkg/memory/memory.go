package memory

import (
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

func newMemoryManager(port int) *MemoryManager {
	m := &MemoryManager{Port: port}
	m.Init()
	return m
}

func newMemory() *Memory {
	return &Memory{
		Items: map[string]string{},
	}
}

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

func (m *MemoryManager) Init() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.ContextMemory = map[string]*Memory{}
	m.MemoryExtractors = map[string]*MemoryExtractor{}
	m.allHeaderMatches = []string{}
}

func (m *MemoryManager) HasAnyMatches() bool {
	return m.hasMatches
}

func (m *MemoryManager) GetContextExtractors(ctxKey string) any {
	m.lock.RLock()
	defer m.lock.RUnlock()
	if ctxKey != "" {
		return m.MemoryExtractors[ctxKey]
	} else {
		return m.MemoryExtractors
	}
}

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

func (m *MemoryManager) ExtractFromHeaders(ctxKey, ctx string, headers http.Header) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	mx := m.MemoryExtractors[ctxKey]
	if mx != nil {
		for _, k := range mx.HeaderMatches {
			v := headers.Get(k)
			if v != "" {
				m.GetOrAddContext(ctx).Add(k, v)
			}
		}
	}
}

func (m *MemoryManager) ExtractFromQuery(ctxKey, ctx string, query url.Values) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	mx := m.MemoryExtractors[ctxKey]
	if mx != nil {
		for _, k := range mx.QueryMatches {
			v := query.Get(k)
			if v != "" {
				m.GetOrAddContext(ctx).Add(k, v)
			}
		}
	}
}

func (m *MemoryManager) ExtractFromBody(ctxKey, ctx string, body io.ReadCloser, isYAML bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	mx := m.MemoryExtractors[ctxKey]
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

func (m *MemoryManager) AddContext(ctx string) *Memory {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.ContextMemory[ctx] = newMemory()
	return m.ContextMemory[ctx]
}

func (m *MemoryManager) GetOrAddContext(ctx string) *Memory {
	m.lock.RLock()
	memory := m.ContextMemory[ctx]
	m.lock.RUnlock()
	if memory == nil {
		return m.AddContext(ctx)
	}
	return memory
}

func (m *MemoryManager) GetOrAddContextExtractor(ctxKey string) *MemoryExtractor {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.MemoryExtractors[ctxKey] == nil {
		m.MemoryExtractors[ctxKey] = newMemoryExtractor(ctxKey)
	}
	return m.MemoryExtractors[ctxKey]
}

func (m *MemoryManager) AddContextMatch(ctxKey, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.GetOrAddContextExtractor(ctxKey).ContextHeaderMatch = match
}

func (m *MemoryManager) AddHeaderMatch(ctxKey, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.GetOrAddContextExtractor(ctxKey)
	mx.HeaderMatches = append(mx.HeaderMatches, match)
	mx.hasMatches = true
	m.hasMatches = true
}

func (m *MemoryManager) AddQueryMatch(ctxKey, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.GetOrAddContextExtractor(ctxKey)
	mx.QueryMatches = append(mx.QueryMatches, match)
	mx.hasMatches = true
	m.hasMatches = true
}

func (m *MemoryManager) AddBodyRegexMatch(ctxKey, key, match string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.GetOrAddContextExtractor(ctxKey)
	mx.BodyRegexMatches[key] = match
	mx.bodyMatchRegexps[key] = regexp.MustCompile("(?i)" + match)
	mx.hasMatches = true
	m.hasMatches = true
}

func (m *MemoryManager) AddBodyJsonPathMatch(ctxKey, match string) {
	paths := strings.Split(match, ",")
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.GetOrAddContextExtractor(ctxKey)
	mx.BodyJsonPathMatches = append(mx.BodyJsonPathMatches, util.NewJSONPath().Parse(paths))
	mx.hasMatches = true
	m.hasMatches = true
}

func (m *MemoryManager) SetBodyTransformation(ctxKey string, transforms []*util.Transform) {
	for _, t := range transforms {
		for _, m := range t.Mappings {
			m.Init()
		}
	}
	m.lock.Lock()
	defer m.lock.Unlock()
	mx := m.GetOrAddContextExtractor(ctxKey)
	mx.BodyTransforms = transforms
	mx.hasMatches = true
	m.hasMatches = true
}

func (m *Memory) Add(key string, value string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.Items[key] = value
}

func (m *Memory) Get(key string) string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.Items[key]
}

func (m *Memory) Clear() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.Items = map[string]string{}
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if !rs.IsKnownNonTraffic {
			port := util.GetRequestOrListenerPortNum(r)
			m := GetMemoryManager(port)
			if m.hasMatches {
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
