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
	"net/http"
	"reflect"
	"slices"
	"testing"
)

func sampleHeaders() *Headers {
	return &Headers{
		Request: &HeadersConfig{
			Add:     map[string]string{"x-add": "1", "x-other": "2"},
			Remove:  []string{"x-remove"},
			Forward: []string{"x-forward", "x-forward2"},
		},
		Response: &HeadersConfig{
			Add:     map[string]string{"x-resp": "r"},
			Remove:  []string{"x-resp-remove"},
			Forward: []string{"x-resp-forward"},
		},
	}
}

func TestHeadersCloneIsADeepCopy(t *testing.T) {
	orig := sampleHeaders()
	clone := orig.Clone()
	if !reflect.DeepEqual(clone, orig) {
		t.Fatalf("Clone() = %+v %+v, want a copy equal to %+v %+v", clone.Request, clone.Response, orig.Request, orig.Response)
	}
	if clone == orig || clone.Request == orig.Request || clone.Response == orig.Response {
		t.Fatalf("Clone() shares the structs of the original")
	}

	clone.Request.Add["x-add"] = "changed"
	clone.Request.Add["x-new"] = "new"
	clone.Request.Remove[0] = "changed"
	clone.Request.Forward[0] = "changed"
	clone.Response.Add["x-resp"] = "changed"
	clone.Response.Remove[0] = "changed"
	clone.Response.Forward[0] = "changed"
	if !reflect.DeepEqual(orig, sampleHeaders()) {
		t.Errorf("changing the clone changed the original: %+v %+v", orig.Request, orig.Response)
	}

	orig2 := sampleHeaders()
	clone2 := orig2.Clone()
	orig2.Request.Add["x-add"] = "changed"
	orig2.Request.Remove[0] = "changed"
	orig2.Response.Forward[0] = "changed"
	if !reflect.DeepEqual(clone2, sampleHeaders()) {
		t.Errorf("changing the original changed the clone: %+v %+v", clone2.Request, clone2.Response)
	}
}

// The response headers used to be dropped by the clone.
func TestHeadersCloneKeepsTheResponseHeaders(t *testing.T) {
	clone := sampleHeaders().Clone()
	want := sampleHeaders().Response
	if clone.Response == nil || !reflect.DeepEqual(clone.Response, want) {
		t.Errorf("Clone().Response = %+v, want %+v", clone.Response, want)
	}
}

// The slices were shared, so that appending to the clone, or to the original, wrote into the array of the other.
func TestHeadersCloneDoesNotShareSliceStorage(t *testing.T) {
	remove := make([]string, 1, 4)
	remove[0] = "x-remove"
	orig := &Headers{Request: &HeadersConfig{Add: map[string]string{}, Remove: remove, Forward: make([]string, 0, 4)}, Response: NewHeadersConfig()}
	clone := orig.Clone()
	clone.Request.Remove = append(clone.Request.Remove, "x-from-clone")
	clone.Request.Forward = append(clone.Request.Forward, "x-from-clone")
	if got := orig.Request.Remove[:2][1]; got != "" {
		t.Errorf("appending to the clone wrote %q into the Remove array of the original", got)
	}
	if got := orig.Request.Forward[:1][0]; got != "" {
		t.Errorf("appending to the clone wrote %q into the Forward array of the original", got)
	}
	if len(orig.Request.Remove) != 1 || len(orig.Request.Forward) != 0 {
		t.Errorf("the original changed: %+v", orig.Request)
	}
}

// A Headers that has no Request or no Response, as it is when it comes from a config that doesn't have them or from
// &Headers{}, used to make the clone panic.
func TestHeadersCloneOfMissingConfigs(t *testing.T) {
	cases := map[string]*Headers{
		"empty":         {},
		"request only":  {Request: &HeadersConfig{Add: map[string]string{"a": "b"}}},
		"response only": {Response: &HeadersConfig{Remove: []string{"c"}}},
		"nil fields":    {Request: &HeadersConfig{}, Response: &HeadersConfig{}},
	}
	for name, orig := range cases {
		t.Run(name, func(t *testing.T) {
			var clone *Headers
			expectNoPanic(t, "Clone", func() { clone = orig.Clone() })
			if clone == nil {
				t.Fatalf("Clone() = nil")
			}
			if clone.Request == nil || clone.Response == nil || clone.Request.Add == nil || clone.Response.Add == nil {
				t.Errorf("Clone() = %+v %+v, want the configs and their maps to be there, to use", clone.Request, clone.Response)
			}
			if orig.Request != nil && len(orig.Request.Add) > 0 && !reflect.DeepEqual(clone.Request.Add, orig.Request.Add) {
				t.Errorf("Clone().Request.Add = %v, want %v", clone.Request.Add, orig.Request.Add)
			}
			if orig.Response != nil && !slices.Equal(clone.Response.Remove, orig.Response.Remove) {
				t.Errorf("Clone().Response.Remove = %v, want %v", clone.Response.Remove, orig.Response.Remove)
			}
			// The clone is usable, e.g. to merge into.
			expectNoPanic(t, "Merge into the clone", func() { clone.Merge(sampleHeaders()) })
		})
	}
	var nilHeaders *Headers
	expectNoPanic(t, "Clone of nil", func() {
		if clone := nilHeaders.Clone(); clone != nil {
			t.Errorf("Clone() of nil = %+v, want nil", clone)
		}
	})
}

// The headers that are not set up, &Headers{} like the ones of a timeline, can be merged into.
func TestHeadersMergeIntoEmptyHeaders(t *testing.T) {
	h := &Headers{}
	expectNoPanic(t, "Merge into &Headers{}", func() { h.Merge(sampleHeaders()) })
	if !reflect.DeepEqual(h, sampleHeaders()) {
		t.Errorf("merged = %+v %+v, want %+v %+v", h.Request, h.Response, sampleHeaders().Request, sampleHeaders().Response)
	}
	hc := &HeadersConfig{}
	expectNoPanic(t, "Merge into &HeadersConfig{}", func() { hc.Merge(sampleHeaders().Request) })
	if !reflect.DeepEqual(hc, sampleHeaders().Request) {
		t.Errorf("merged = %+v, want %+v", hc, sampleHeaders().Request)
	}
	h = &Headers{Request: &HeadersConfig{Add: nil}}
	expectNoPanic(t, "Merge into headers without maps", func() { h.Merge(sampleHeaders()) })
	if h.Request.Add["x-add"] != "1" || h.Response == nil || h.Response.Add["x-resp"] != "r" {
		t.Errorf("merged = %+v %+v, want the headers merged in", h.Request, h.Response)
	}
	var nilHeaders *Headers
	expectNoPanic(t, "Merge into nil", func() { nilHeaders.Merge(sampleHeaders()) })
}

func TestHeadersMergeAndUnion(t *testing.T) {
	h1 := &Headers{
		Request:  &HeadersConfig{Add: map[string]string{"a": "1", "b": "1"}, Remove: []string{"r1"}, Forward: []string{"f1"}},
		Response: &HeadersConfig{Add: map[string]string{"ra": "1"}},
	}
	h2 := &Headers{
		Request:  &HeadersConfig{Add: map[string]string{"b": "2", "c": "2"}, Remove: []string{"r2"}, Forward: []string{"f2"}},
		Response: &HeadersConfig{Remove: []string{"rr"}},
	}
	u := Union(h1, h2)
	want := &Headers{
		Request:  &HeadersConfig{Add: map[string]string{"a": "1", "b": "2", "c": "2"}, Remove: []string{"r1", "r2"}, Forward: []string{"f1", "f2"}},
		Response: &HeadersConfig{Add: map[string]string{"ra": "1"}, Remove: []string{"rr"}, Forward: []string{}},
	}
	if !reflect.DeepEqual(u, want) {
		t.Errorf("Union() = %+v %+v, want %+v %+v", u.Request, u.Response, want.Request, want.Response)
	}
	if h1.Request.Add["b"] != "1" || len(h1.Request.Remove) != 1 || len(h2.Request.Add) != 2 {
		t.Errorf("Union() changed its arguments: %+v %+v", h1.Request, h2.Request)
	}
	if u0 := Union(nil, nil); !reflect.DeepEqual(u0, NewHeaders()) {
		t.Errorf("Union(nil, nil) = %+v, want empty headers", u0)
	}
	if u1 := Union(nil, h2); !reflect.DeepEqual(u1.Request.Add, h2.Request.Add) {
		t.Errorf("Union(nil, h2).Request.Add = %v, want %v", u1.Request.Add, h2.Request.Add)
	}
	hc := h1.Request.Union(h2.Request)
	if !reflect.DeepEqual(hc, want.Request) {
		t.Errorf("HeadersConfig.Union() = %+v, want %+v", hc, want.Request)
	}
	h := NewHeaders()
	h.Merge(nil)
	h.Request.Merge(nil)
	if !reflect.DeepEqual(h, NewHeaders()) {
		t.Errorf("merging nil changed the headers: %+v", h)
	}
}

func TestHeadersNonNilAndHasForwardHeaders(t *testing.T) {
	h := &Headers{}
	h.NonNil()
	if h.Request == nil || h.Response == nil || h.Request.Add == nil || h.Response.Add == nil {
		t.Errorf("NonNil() = %+v %+v, want all the configs and maps to be there", h.Request, h.Response)
	}
	if h.HasForwardHeaders() {
		t.Errorf("HasForwardHeaders() = true for headers that forward nothing")
	}
	h.Request.Forward = []string{"x-a"}
	if !h.HasForwardHeaders() {
		t.Errorf("HasForwardHeaders() = false for headers that forward x-a")
	}
	h.Response.Forward = []string{"x-b"}
	h.Request.Forward = nil
	if h.HasForwardHeaders() {
		t.Errorf("HasForwardHeaders() = true, want it to be about the request headers only")
	}
	if (&Headers{}).HasForwardHeaders() {
		t.Errorf("HasForwardHeaders() = true for headers that have no request")
	}
	var nilHeaders *Headers
	expectNoPanic(t, "HasForwardHeaders of nil", func() {
		if nilHeaders.HasForwardHeaders() {
			t.Errorf("HasForwardHeaders() = true for nil")
		}
	})
}

func TestHeadersConfigUpdateHeaders(t *testing.T) {
	hc := &HeadersConfig{
		Add:    map[string]string{"X-Add": "added", "X-Both": "added"},
		Remove: []string{"X-Remove", "X-Both"},
	}
	target := http.Header{}
	target.Set("X-Remove", "old")
	target.Set("X-Both", "old")
	target.Set("X-Keep", "kept")
	hc.UpdateHeaders(target)
	want := http.Header{"X-Add": {"added"}, "X-Both": {"added"}, "X-Keep": {"kept"}}
	if !reflect.DeepEqual(target, want) {
		t.Errorf("UpdateHeaders() = %v, want %v: the headers are removed, then added", target, want)
	}
	var nilConfig *HeadersConfig
	expectNoPanic(t, "UpdateHeaders of nil", func() { nilConfig.UpdateHeaders(target) })
	if !reflect.DeepEqual(target, want) {
		t.Errorf("UpdateHeaders() of nil changed the headers to %v", target)
	}
}

func TestForwardHeaders(t *testing.T) {
	source := http.Header{}
	source.Set("X-A", "1")
	source.Set("X-B", "")
	source.Set("X-D", "4")
	target := http.Header{}
	ForwardHeaders(source, target, slices.Values([]string{"X-A", "X-B", "X-C"}))
	want := http.Header{"X-A": {"1"}}
	if !reflect.DeepEqual(target, want) {
		t.Errorf("ForwardHeaders() = %v, want %v: only the headers that the source has, with a value", target, want)
	}
	target = http.Header{}
	ForwardHeaders(source, target, nil)
	ForwardHeaders(nil, target, slices.Values([]string{"X-A"}))
	if len(target) != 0 {
		t.Errorf("ForwardHeaders() without headers to forward, or a source, = %v, want nothing", target)
	}
	md := MD{}
	ForwardHeaders(source, md, slices.Values([]string{"X-D"}))
	if got := md["X-D"]; !slices.Equal(got, []string{"4"}) {
		t.Errorf("ForwardHeaders() to metadata = %v, want X-D: [4]", md)
	}
}

func TestSimpleHTTPHeadersAndMD(t *testing.T) {
	sh := SimpleHTTPHeaders{}
	sh.Add("a", "1")
	sh.Add("a", "2")
	if sh.Get("a") != "2" || sh.Get("missing") != "" {
		t.Errorf("SimpleHTTPHeaders = %v, want a: 2 (Add replaces)", sh)
	}
	sh.Del("a")
	sh.Del("missing")
	if len(sh) != 0 {
		t.Errorf("SimpleHTTPHeaders after Del = %v, want none", sh)
	}
	md := MD{}
	md.Add("k", "v1")
	md.Add("k", "v2")
	if !slices.Equal(md["k"], []string{"v1", "v2"}) {
		t.Errorf("MD = %v, want k: [v1 v2] (Add appends)", md)
	}
	md.Del("k")
	md.Del("missing")
	if len(md) != 0 {
		t.Errorf("MD after Del = %v, want none", md)
	}
}
