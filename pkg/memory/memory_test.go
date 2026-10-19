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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"
)

var (
	stubOnce sync.Once
	// Memory is kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// newManager is a memory manager that the test owns alone, which the package-wide registry of managers doesn't know.
func newManager(t *testing.T) *MemoryManager {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	return newMemoryManager(int(46000 + portSeq.Add(1)))
}

// registeredManager is the manager of a port of its own in the package-wide registry, which is what the middleware
// and the APIs use. It's taken out of the registry when the test ends.
func registeredManager(t *testing.T) (*MemoryManager, int) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	port := int(46000 + portSeq.Add(1))
	t.Cleanup(func() {
		lock.Lock()
		delete(Managers, port)
		lock.Unlock()
	})
	return GetMemoryManager(port), port
}

// within runs fn and fails the test when it hasn't returned by the deadline. A function that deadlocks is left behind
// by the test, which is why the callers use a manager of their own.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s panicked: %v", what, p)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return in time (deadlock)", what)
	}
}

func body(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

func mapping(source, target, mode string) *util.JSONTransform {
	m := &util.JSONTransform{Source: source, Target: target, Mode: mode}
	m.Init()
	return m
}

func TestMemoryItems(t *testing.T) {
	m := newMemory()
	if got := m.Get("missing"); got != "" {
		t.Errorf("Get of a missing key = %q, want empty", got)
	}
	m.Add("k1", "v1")
	m.Add("k2", "v2")
	m.Add("k1", "v1b")
	if got := m.Get("k1"); got != "v1b" {
		t.Errorf("Get(k1) = %q, want the latest value v1b", got)
	}
	if got := m.Get("k2"); got != "v2" {
		t.Errorf("Get(k2) = %q, want v2", got)
	}
	m.Clear()
	if m.Get("k1") != "" || m.Get("k2") != "" || len(m.Items) != 0 {
		t.Errorf("items after Clear = %v, want none", m.Items)
	}
	m.Add("k3", "v3")
	if got := m.Get("k3"); got != "v3" {
		t.Errorf("a memory that was cleared takes items again, Get(k3) = %q", got)
	}
}

func TestGetMemoryManagerPerPort(t *testing.T) {
	m1, port1 := registeredManager(t)
	m2, port2 := registeredManager(t)
	if m1 == m2 {
		t.Fatal("different ports must not share a manager")
	}
	if m1.Port != port1 || m2.Port != port2 {
		t.Errorf("manager ports = %d, %d, want %d, %d", m1.Port, m2.Port, port1, port2)
	}
	if got := GetMemoryManager(port1); got != m1 {
		t.Error("the manager of a port must be the same one every time")
	}
	lock.RLock()
	registered := Managers[port1] == m1 && Managers[port2] == m2
	lock.RUnlock()
	if !registered {
		t.Error("managers are expected in the Managers registry")
	}
	m1.GetOrAddContext("c").Add("k", "v")
	if m2.GetOrAddContext("c").Get("k") != "" {
		t.Error("the memory of one port must not show on another port")
	}
}

func TestGetMemoryManagerConcurrently(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(46000 + portSeq.Add(1))
	t.Cleanup(func() {
		lock.Lock()
		delete(Managers, port)
		lock.Unlock()
	})
	const workers = 16
	got := make([]*MemoryManager, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = GetMemoryManager(port)
		}()
	}
	wg.Wait()
	for i := 1; i < workers; i++ {
		if got[i] != got[0] {
			t.Fatalf("concurrent callers got different managers for one port")
		}
	}
}

func TestManagerInitClearsEverything(t *testing.T) {
	m := newManager(t)
	m.GetOrAddContext("c").Add("k", "v")
	within(t, "AddHeaderMatch", func() { m.AddHeaderMatch("key", "X-Header") })
	m.Init()
	if len(m.ContextMemory) != 0 || len(m.MemoryExtractors) != 0 {
		t.Errorf("after Init: contexts %v extractors %v, want none", m.ContextMemory, m.MemoryExtractors)
	}
	if m.HasAnyMatches() {
		t.Error("a manager without extractors has no matches")
	}
}

func TestAddContextStartsOverGetOrAddContextKeeps(t *testing.T) {
	m := newManager(t)
	first := m.AddContext("c")
	first.Add("k", "v")
	if got := m.GetOrAddContext("c"); got != first || got.Get("k") != "v" {
		t.Error("GetOrAddContext must return the existing memory with its items")
	}
	second := m.AddContext("c")
	if second == first || second.Get("k") != "" {
		t.Error("AddContext starts the context over with an empty memory")
	}
	if got := m.GetOrAddContext("c"); got != second {
		t.Error("the context must hold the latest memory")
	}
}

func TestMatchContext(t *testing.T) {
	m := newManager(t)
	h := http.Header{}
	h.Set("X-Session", "s-1")
	if found, _, _ := m.MatchContext(h); found {
		t.Error("a manager without extractors matches nothing")
	}
	within(t, "AddContextMatch", func() { m.AddContextMatch("session", "X-Session") })
	found, key, ctx := m.MatchContext(h)
	if !found || key != "session" || ctx != "s-1" {
		t.Errorf("MatchContext = %v %q %q, want true session s-1", found, key, ctx)
	}
	if found, _, _ := m.MatchContext(http.Header{"X-Other": {"v"}}); found {
		t.Error("a request without the header must not match")
	}
	// an extractor that has no context header cannot match, whatever the request has
	m2 := newManager(t)
	within(t, "AddHeaderMatch", func() { m2.AddHeaderMatch("noctx", "X-Session") })
	if found, _, _ := m2.MatchContext(h); found {
		t.Error("an extractor without a context header must not match")
	}
}

func TestExtractFromHeadersAndQuery(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddHeaderMatch("k", "X-User")
		m.AddHeaderMatch("k", "X-Tenant")
		m.AddQueryMatch("k", "page")
	})
	within(t, "ExtractFromHeaders", func() {
		m.ExtractFromHeaders("k", "ctx1", http.Header{"X-User": {"alice"}, "X-Tenant": {"t1"}, "X-Ignored": {"x"}})
	})
	within(t, "ExtractFromQuery", func() {
		m.ExtractFromQuery("k", "ctx1", url.Values{"page": {"3"}, "other": {"o"}})
	})
	mem := m.GetOrAddContext("ctx1")
	if mem.Get("X-User") != "alice" || mem.Get("X-Tenant") != "t1" || mem.Get("page") != "3" {
		t.Errorf("memory = %v, want X-User, X-Tenant and page", mem.Items)
	}
	if len(mem.Items) != 3 {
		t.Errorf("memory = %v, want only the 3 extracted items", mem.Items)
	}
}

func TestExtractSkipsMissingValues(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddHeaderMatch("k", "X-User")
		m.AddQueryMatch("k", "page")
	})
	within(t, "extract", func() {
		m.ExtractFromHeaders("k", "ctx", http.Header{})
		m.ExtractFromQuery("k", "ctx", url.Values{})
	})
	if mem := m.GetOrAddContext("ctx"); len(mem.Items) != 0 {
		t.Errorf("memory = %v, want nothing for absent values", mem.Items)
	}
}

func TestExtractForUnknownExtractorDoesNothing(t *testing.T) {
	m := newManager(t)
	within(t, "extract for an unknown key", func() {
		m.ExtractFromHeaders("nope", "ctx", http.Header{"X-User": {"alice"}})
		m.ExtractFromQuery("nope", "ctx", url.Values{"page": {"3"}})
		m.ExtractFromBody("nope", "ctx", body(`{"a":1}`), false)
	})
	if len(m.ContextMemory) != 0 {
		t.Errorf("contexts = %v, want none to be created for an unknown extractor", m.ContextMemory)
	}
}

func TestExtractFromBodyRegex(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddBodyRegexMatch("k", "order", `order-\d+`)
		m.AddBodyRegexMatch("k", "who", `user=[a-z]+`)
	})
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body("Hello ORDER-12 and order-34 for user=bob"), false)
	})
	mem := m.GetOrAddContext("ctx")
	// the regexes ignore the case, and the last of several matches is what stays
	if got := mem.Get("order"); got != "order-34" {
		t.Errorf("order = %q, want order-34", got)
	}
	if got := mem.Get("who"); got != "user=bob" {
		t.Errorf("who = %q, want user=bob", got)
	}
	if mx := m.GetContextExtractors("k").(*MemoryExtractor); mx.BodyRegexMatches["order"] != `order-\d+` {
		t.Errorf("extractor regexes = %v", mx.BodyRegexMatches)
	}
}

func TestAddBodyRegexMatchRefusesRegexesThatDontCompile(t *testing.T) {
	m := newManager(t)
	if err := m.AddBodyRegexMatch("k", "bad", "[a-"); err == nil {
		t.Error("AddBodyRegexMatch of a regex that doesn't compile must fail")
	}
	if m.HasAnyMatches() {
		t.Error("a regex that was refused must not count as an extraction")
	}
	if err := m.AddBodyRegexMatch("k", "good", "a+"); err != nil || !m.HasAnyMatches() {
		t.Errorf("AddBodyRegexMatch of a good regex = %v, matches %v", err, m.HasAnyMatches())
	}
}

func TestSetBodyTransformationRefusesWhatCannotBeCompiled(t *testing.T) {
	m := newManager(t)
	bad := []*util.Transform{{Mappings: []*util.JSONTransform{{Source: "a", Target: "b", IfContains: "(unclosed"}}}}
	if err := m.SetBodyTransformation("k", bad); err == nil {
		t.Error("a mapping with a condition that isn't a regex must be refused")
	}
	for _, trs := range [][]*util.Transform{{nil}, {{Mappings: []*util.JSONTransform{nil}}}} {
		if err := m.SetBodyTransformation("k", trs); err == nil {
			t.Errorf("null transforms %v must be refused", trs)
		}
	}
	if m.HasAnyMatches() {
		t.Error("transforms that were refused must not count as an extraction")
	}
}

func TestExtractFromBodyJSONPaths(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() { m.AddBodyJsonPathMatch("k", "name={.user.name},id={.user.id}") })
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body(`{"user":{"name":"alice","id":7}}`), false)
	})
	mem := m.GetOrAddContext("ctx")
	if mem.Get("name") != "alice" || mem.Get("id") != "7" {
		t.Errorf("memory = %v, want name=alice id=7", mem.Items)
	}
	// a body that isn't JSON gives the JSON paths nothing to read, and takes nothing out of memory
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx2", body("not json"), false)
	})
	if got := m.GetOrAddContext("ctx2"); len(got.Items) != 0 {
		t.Errorf("memory of a body that isn't JSON = %v, want none", got.Items)
	}
}

func TestExtractFromBodyRegexWorksOnTextThatIsNotJSON(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddBodyRegexMatch("k", "tok", `tok-[0-9a-f]+`)
		m.AddBodyJsonPathMatch("k", "a={.a}")
	})
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body("plain text with tok-9f2 inside"), false)
	})
	mem := m.GetOrAddContext("ctx")
	if mem.Get("tok") != "tok-9f2" || mem.Get("a") != "" {
		t.Errorf("memory = %v, want only tok", mem.Items)
	}
}

func TestExtractFromBodyAppliesTheTransformsFirst(t *testing.T) {
	m := newManager(t)
	// the template is what the extraction reads, with the values of the body mapped into it
	tr := &util.Transform{
		Payload:  map[string]any{"customer": "", "kind": "order"},
		Mappings: []*util.JSONTransform{mapping("user.name", "customer", "replace")},
	}
	within(t, "setup", func() {
		m.SetBodyTransformation("k", []*util.Transform{tr})
		m.AddBodyJsonPathMatch("k", "cust={.customer},kind={.kind}")
	})
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body(`{"user":{"name":"alice"}}`), false)
	})
	mem := m.GetOrAddContext("ctx")
	if mem.Get("cust") != "alice" || mem.Get("kind") != "order" {
		t.Errorf("memory = %v, want cust=alice kind=order from the transformed body", mem.Items)
	}
	// without the transform the body has no customer to read
	plain := newManager(t)
	within(t, "setup", func() {
		plain.AddBodyJsonPathMatch("k", "cust={.customer}")
		plain.ExtractFromBody("k", "ctx", body(`{"user":{"name":"alice"}}`), false)
	})
	if got := plain.GetOrAddContext("ctx").Get("cust"); got != "" {
		t.Errorf("cust without the transform = %q, want none", got)
	}
}

func TestExtractFromBodyTransformsYAML(t *testing.T) {
	m := newManager(t)
	tr := &util.Transform{
		Payload:  map[string]any{"customer": ""},
		Mappings: []*util.JSONTransform{mapping("user.name", "customer", "replace")},
	}
	within(t, "setup", func() {
		m.SetBodyTransformation("k", []*util.Transform{tr})
		m.AddBodyRegexMatch("k", "cust", `customer: \w+`)
	})
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body("user:\n  name: bob\n"), true)
	})
	if got := m.GetOrAddContext("ctx").Get("cust"); got != "customer: bob" {
		t.Errorf("cust = %q, want the customer of the transformed YAML", got)
	}
}

func TestExtractFromBodyKeepsABodyThatTheTransformsCannotRead(t *testing.T) {
	m := newManager(t)
	tr := &util.Transform{
		Payload:  map[string]any{"customer": ""},
		Mappings: []*util.JSONTransform{mapping("user.name", "customer", "replace")},
	}
	within(t, "setup", func() {
		m.SetBodyTransformation("k", []*util.Transform{tr})
		m.AddBodyRegexMatch("k", "word", `hello`)
	})
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body("hello, not json"), false)
	})
	if got := m.GetOrAddContext("ctx").Get("word"); got != "hello" {
		t.Errorf("word = %q, want the untransformed body to be read", got)
	}
}

func TestExtractorsAreReported(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddContextMatch("a", "X-A")
		m.AddHeaderMatch("a", "h1")
		m.AddQueryMatch("a", "q1")
		m.AddBodyRegexMatch("a", "r", "x+")
		m.AddBodyJsonPathMatch("a", "p={.p}")
		m.AddHeaderMatch("b", "h2")
	})
	one, ok := m.GetContextExtractors("a").(*MemoryExtractor)
	if !ok || one == nil {
		t.Fatalf("extractor a = %#v", m.GetContextExtractors("a"))
	}
	if one.ContextKey != "a" || one.ContextHeaderMatch != "X-A" || len(one.HeaderMatches) != 1 || one.HeaderMatches[0] != "h1" ||
		len(one.QueryMatches) != 1 || one.QueryMatches[0] != "q1" || one.BodyRegexMatches["r"] != "x+" || len(one.BodyJsonPathMatches) != 1 {
		t.Errorf("extractor a = %+v", one)
	}
	all, ok := m.GetContextExtractors("").(map[string]*MemoryExtractor)
	if !ok || len(all) != 2 || all["a"] == nil || all["b"] == nil {
		t.Errorf("all extractors = %#v, want a and b", m.GetContextExtractors(""))
	}
	if mx, _ := m.GetContextExtractors("missing").(*MemoryExtractor); mx != nil {
		t.Errorf("an unknown extractor = %+v, want none", mx)
	}
}

func TestHasAnyMatches(t *testing.T) {
	for name, add := range map[string]func(*MemoryManager){
		"header":    func(m *MemoryManager) { m.AddHeaderMatch("k", "h") },
		"query":     func(m *MemoryManager) { m.AddQueryMatch("k", "q") },
		"regex":     func(m *MemoryManager) { m.AddBodyRegexMatch("k", "r", "x") },
		"jsonpath":  func(m *MemoryManager) { m.AddBodyJsonPathMatch("k", "a={.a}") },
		"transform": func(m *MemoryManager) { m.SetBodyTransformation("k", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			m := newManager(t)
			if m.HasAnyMatches() {
				t.Fatal("a new manager has no matches")
			}
			within(t, "add", func() { add(m) })
			if !m.HasAnyMatches() {
				t.Errorf("a manager with a %s extraction has matches", name)
			}
		})
	}
	// a context match alone doesn't extract anything
	m := newManager(t)
	within(t, "AddContextMatch", func() { m.AddContextMatch("k", "X-Ctx") })
	if m.HasAnyMatches() {
		t.Error("a context match alone must not count as an extraction")
	}
}

func serveThroughMiddleware(t *testing.T, port int, rs func(*util.RequestStore), req *http.Request, next http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, store := util.WithRequestStore(req, nil)
	if rs != nil {
		rs(store)
	}
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(next).ServeHTTP(rr, req)
	return rr
}

func TestMiddlewareExtractsIntoMemory(t *testing.T) {
	m, port := registeredManager(t)
	within(t, "setup", func() {
		m.AddContextMatch("session", "X-Session")
		m.AddHeaderMatch("session", "X-User")
		m.AddQueryMatch("session", "page")
		m.AddBodyRegexMatch("session", "order", `order-\d+`)
		m.AddBodyJsonPathMatch("session", "name={.user.name}")
	})
	var nextBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		nextBody = string(b)
		w.WriteHeader(http.StatusAccepted)
	})
	payload := `{"user":{"name":"alice"},"ref":"order-77"}`
	req := httptest.NewRequest("POST", "/some/traffic?page=4", strings.NewReader(payload))
	req.Header.Set("X-Session", "sess-1")
	req.Header.Set("X-User", "alice")
	var rr *httptest.ResponseRecorder
	within(t, "the middleware", func() { rr = serveThroughMiddleware(t, port, nil, req, next) })
	if rr.Code != http.StatusAccepted {
		t.Errorf("status = %d, want the 202 of the next handler", rr.Code)
	}
	if nextBody != payload {
		t.Errorf("body seen by the next handler = %q, want it intact after the extraction", nextBody)
	}
	mem := m.GetOrAddContext("sess-1")
	want := map[string]string{"X-User": "alice", "page": "4", "order": "order-77", "name": "alice"}
	for k, v := range want {
		if mem.Get(k) != v {
			t.Errorf("memory[%s] = %q, want %q (memory %v)", k, mem.Get(k), v, mem.Items)
		}
	}
}

func TestMiddlewareLeavesRequestsAloneThatDontMatch(t *testing.T) {
	m, port := registeredManager(t)
	within(t, "setup", func() {
		m.AddContextMatch("session", "X-Session")
		m.AddHeaderMatch("session", "X-User")
	})
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ })

	// no context header
	req := httptest.NewRequest("GET", "/t", nil)
	req.Header.Set("X-User", "alice")
	within(t, "the middleware", func() { serveThroughMiddleware(t, port, nil, req, next) })
	// known non-traffic requests are not looked at
	req2 := httptest.NewRequest("GET", "/t", nil)
	req2.Header.Set("X-Session", "sess-2")
	req2.Header.Set("X-User", "alice")
	within(t, "the middleware", func() {
		serveThroughMiddleware(t, port, func(rs *util.RequestStore) { rs.IsKnownNonTraffic = true }, req2, next)
	})
	if calls != 2 {
		t.Errorf("next handler calls = %d, want 2", calls)
	}
	if len(m.ContextMemory) != 0 {
		t.Errorf("contexts = %v, want none", m.ContextMemory)
	}
}

func TestMiddlewareWithoutExtractionsOrNextHandler(t *testing.T) {
	_, port := registeredManager(t)
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ })
	var rr *httptest.ResponseRecorder
	within(t, "the middleware", func() {
		serveThroughMiddleware(t, port, nil, httptest.NewRequest("GET", "/t", nil), next)
		// a middleware that has nothing behind it still serves
		rr = serveThroughMiddleware(t, port, nil, httptest.NewRequest("GET", "/t", nil), nil)
	})
	if rr.Code != http.StatusOK {
		t.Errorf("status without a next handler = %d", rr.Code)
	}
	if calls != 1 {
		t.Errorf("next handler calls = %d, want 1", calls)
	}
}

// Requests extract into memory while the APIs change what is extracted, and read it.
func TestMemoryConcurrently(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddContextMatch("k", "X-Ctx")
		m.AddHeaderMatch("k", "X-Seed")
	})
	const workers, rounds = 8, 60
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := http.Header{"X-Ctx": {fmt.Sprintf("c%d", w%3)}, "X-Seed": {"s"}}
			for i := 0; i < rounds; i++ {
				switch (w + i) % 8 {
				case 0:
					m.AddHeaderMatch("k", fmt.Sprintf("X-H%d", i%4))
				case 1:
					m.AddQueryMatch("k", "q")
				case 2:
					m.AddBodyRegexMatch("k", "r", `\d+`)
				case 3:
					m.AddBodyJsonPathMatch("k", "a={.a}")
				case 4:
					if found, key, ctx := m.MatchContext(h); found {
						m.ExtractFromHeaders(key, ctx, h)
						m.ExtractFromQuery(key, ctx, url.Values{"q": {"1"}})
						m.ExtractFromBody(key, ctx, body(`{"a":"x","n":12}`), false)
					}
				case 5:
					_ = util.ToJSONText(m.GetContextExtractors("k"))
					_ = util.ToJSONText(m.GetContextExtractors(""))
				case 6:
					mem := m.GetOrAddContext(fmt.Sprintf("c%d", w%3))
					mem.Add("x", "y")
					_ = mem.Get("x")
				case 7:
					_ = m.HasAnyMatches()
					_ = util.ToJSONText(m.GetOrAddContext("c1"))
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("concurrent use of the memory did not finish in time (deadlock)")
	}
	if !m.HasAnyMatches() {
		t.Error("extractions were added, so the manager must report matches")
	}
}
