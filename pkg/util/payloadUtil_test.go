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

package util

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func newTransform(payload any, mappings ...*JSONTransform) *Transform {
	for _, m := range mappings {
		m.Init()
	}
	return &Transform{Mappings: mappings, Payload: payload}
}

func TestTransformPayloadAppliesMappingsToATemplate(t *testing.T) {
	template := map[string]any{"b": "", "kept": "yes"}
	tr := newTransform(template, &JSONTransform{Source: "a", Target: "b", Mode: "replace"})
	tests := []struct{ source, want string }{
		{`{"a":"copied"}`, `{"b":"copied","kept":"yes"}`},
		{`{"a":"other"}`, `{"b":"other","kept":"yes"}`},
		{`{"a":"copied"}`, `{"b":"copied","kept":"yes"}`},
	}
	for _, tt := range tests {
		if got := TransformPayload(tt.source, []*Transform{tr}, false); got != tt.want {
			t.Errorf("TransformPayload(%s) = %s, want %s", tt.source, got, tt.want)
		}
	}
	// The template is what every request starts from, so applying mappings to it changes none of it.
	if want := map[string]any{"b": "", "kept": "yes"}; !reflect.DeepEqual(tr.Payload, want) {
		t.Errorf("template after the transformations = %v, want %v", tr.Payload, want)
	}
}

func TestTransformPayloadDoesNotCarryValuesBetweenRequests(t *testing.T) {
	tr := newTransform(map[string]any{"list": []any{}}, &JSONTransform{Source: "a", Target: "list"})
	for _, tt := range []struct{ source, want string }{
		{`{"a":"first"}`, `{"list":["first"]}`},
		{`{"a":"second"}`, `{"list":["second"]}`},
		{`{"a":"third"}`, `{"list":["third"]}`},
	} {
		if got := TransformPayload(tt.source, []*Transform{tr}, false); got != tt.want {
			t.Errorf("TransformPayload(%s) = %s, want %s", tt.source, got, tt.want)
		}
	}
	if want := map[string]any{"list": []any{}}; !reflect.DeepEqual(tr.Payload, want) {
		t.Errorf("template after the transformations = %v, want %v", tr.Payload, want)
	}
}

func TestTransformPayloadOfYAML(t *testing.T) {
	tr := newTransform(map[string]any{"b": "", "kept": "keepme"}, &JSONTransform{Source: "a", Target: "b", Mode: "replace"})
	if got, want := TransformPayload("a: copied\n", []*Transform{tr}, true), "b: copied\nkept: keepme\n"; got != want {
		t.Errorf("TransformPayload of YAML = %q, want %q", got, want)
	}
}

func TestTransformPayloadTransformsTheRequestPayloadWithoutATemplate(t *testing.T) {
	tr := newTransform(nil, &JSONTransform{Source: "a", Target: "c", Mode: "replace"})
	if got, want := TransformPayload(`{"a":"x","c":""}`, []*Transform{tr}, false), `{"a":"x","c":"x"}`; got != want {
		t.Errorf("TransformPayload = %s, want %s", got, want)
	}
	// A given value is what the target gets when the source is not in the payload.
	withValue := newTransform(nil, &JSONTransform{Source: "missing", Target: "c", Value: "dflt", Mode: "replace"})
	if got, want := TransformPayload(`{"a":"x","c":""}`, []*Transform{withValue}, false), `{"a":"x","c":"dflt"}`; got != want {
		t.Errorf("TransformPayload with a default value = %s, want %s", got, want)
	}
}

func TestTransformPayloadUsesTheFirstTransformThatApplies(t *testing.T) {
	skipped := newTransform(map[string]any{"b": "from-first"}, &JSONTransform{Source: "missing", Target: "b", Mode: "replace"})
	applies := newTransform(map[string]any{"b": "", "second": true}, &JSONTransform{Source: "a", Target: "b", Mode: "replace"})
	never := newTransform(map[string]any{"b": "", "third": true}, &JSONTransform{Source: "a", Target: "b", Mode: "replace"})
	if got, want := TransformPayload(`{"a":"v"}`, []*Transform{skipped, applies, never}, false), `{"b":"v","second":true}`; got != want {
		t.Errorf("TransformPayload = %s, want %s", got, want)
	}
}

func TestTransformPayloadLeavesPayloadsThatCannotBeTransformed(t *testing.T) {
	tr := newTransform(map[string]any{"b": ""}, &JSONTransform{Source: "a", Target: "b", Mode: "replace"})
	for _, source := range []string{"", "not json", `"text"`, `5`, `{}`, `{"x":1}`, "[: not yaml"} {
		if got := TransformPayload(source, []*Transform{tr}, false); got != source {
			t.Errorf("TransformPayload(%q) = %q, want it unchanged", source, got)
		}
	}
	if got := TransformPayload(`{"a":"v"}`, nil, false); got != `{"a":"v"}` {
		t.Errorf("TransformPayload without transforms = %q, want it unchanged", got)
	}
	if got := TransformPayload("a: v\n", []*Transform{newTransform(nil)}, true); got != "a: v\n" {
		t.Errorf("TransformPayload with a transform of no mappings = %q, want it unchanged", got)
	}
}

// Requests that match the same transform run it at the same time.
func TestTransformPayloadConcurrently(t *testing.T) {
	tr := newTransform(map[string]any{"b": "", "n": []any{}}, &JSONTransform{Source: "a", Target: "b", Mode: "replace"},
		&JSONTransform{Source: "a", Target: "n"})
	const workers, rounds = 8, 40
	failures := make([]string, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				value := fmt.Sprintf("w%d-r%d", w, i)
				want := fmt.Sprintf(`{"b":%q,"n":[%q]}`, value, value)
				if got := TransformPayload(fmt.Sprintf(`{"a":%q}`, value), []*Transform{tr}, false); got != want {
					failures[w] = fmt.Sprintf("round %d: got %s, want %s", i, got, want)
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for w, failure := range failures {
		if failure != "" {
			t.Errorf("worker %d: %s", w, failure)
		}
	}
}
