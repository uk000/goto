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

package pipe

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func sampleInput() map[string]interface{} {
	return map[string]interface{}{
		"a":     map[string]interface{}{"b": "x", "n": 5},
		"items": []interface{}{"p", "q"},
		"text":  "a1b22",
	}
}

// transformValue is what a transform gives for the input, as the plain data that the JSON of it has.
func transformValue(t *testing.T, pt *PipelineTransform, input interface{}) interface{} {
	t.Helper()
	out := pt.Map(input)
	if out == nil {
		t.Fatalf("transform %s gave nothing", pt.Name)
	}
	return out.Value()
}

// sameJSON tells whether the values have the same JSON, which is what a transform is compared by.
func sameJSON(a, b interface{}) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

func TestNewTransformTypes(t *testing.T) {
	for _, tc := range []struct {
		typ                           TransformType
		jsonPath, jq, template, regex bool
	}{
		{typ: TransformJSONPath, jsonPath: true},
		{typ: TransformJQ, jq: true},
		{typ: TransformTemplate, template: true},
		{typ: TransformRegex, regex: true},
	} {
		spec := ".a"
		switch tc.typ {
		case TransformTemplate:
			spec = "{{.a}}"
		case TransformRegex:
			spec = "a+"
		}
		pt := NewTransform("t", tc.typ, spec)
		if pt.Name != "t" || pt.Type != tc.typ || pt.Spec != spec {
			t.Errorf("%s: transform = %+v", tc.typ, pt)
		}
		if pt.IsJSONPath() != tc.jsonPath || pt.IsJQ() != tc.jq || pt.IsTemplate() != tc.template || pt.IsRegex() != tc.regex {
			t.Errorf("%s: flags = %v %v %v %v", tc.typ, pt.IsJSONPath(), pt.IsJQ(), pt.IsTemplate(), pt.IsRegex())
		}
	}
}

func TestAbstractTransform(t *testing.T) {
	a := &AbstractTransform{Name: "n"}
	if a.Out() != nil || a.IsJSONPath() || a.IsJQ() || a.IsTemplate() || a.IsRegex() {
		t.Errorf("an abstract transform is of no type and gives nothing")
	}
	a.SetSpec("spec")
	if a.GetSpec() != "spec" || a.Spec != "spec" {
		t.Errorf("spec = %q", a.GetSpec())
	}
	a.SetInput(5)
	if a.input != 5 {
		t.Errorf("input = %v", a.input)
	}
	if a.Map("in") != nil || a.input != "in" {
		t.Errorf("Map must set the input and give what Out gives")
	}
}

func TestTransformsOfEachType(t *testing.T) {
	in := sampleInput()
	for _, tc := range []struct {
		name string
		typ  TransformType
		spec string
		want interface{}
	}{
		// the JSON path gives its matches as JSON text
		{"jsonpath field", TransformJSONPath, ".a.b", "[\n    \"x\"\n]\n"},
		{"jsonpath braces", TransformJSONPath, "{.a.b}", "[\n    \"x\"\n]\n"},
		{"jsonpath padded", TransformJSONPath, " \t{.a.n}\n", "[\n    5\n]\n"},
		{"jsonpath all", TransformJSONPath, ".items[*]", "[\n    \"p\",\n    \"q\"\n]\n"},
		{"jq field", TransformJQ, ".a.b", "x"},
		{"jq number", TransformJQ, ".a.n", 5.0},
		{"jq many", TransformJQ, ".items[]", []interface{}{"p", "q"}},
		{"jq no result", TransformJQ, ".missing", nil},
		{"template", TransformTemplate, "{{.a.b}}", "x"},
		{"template range", TransformTemplate, "n={{.a.n}} {{range .items}}[{{.}}]{{end}}", "n=5 [p][q]"},
		{"regex no match", TransformRegex, "zzz", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pt := NewTransform("out", tc.typ, tc.spec)
			got := transformValue(t, pt, in)
			// the values come back as they are in the JSON, so a number is a float
			var want interface{} = map[string]interface{}{"out": tc.want}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("%s %q = %s, want %s", tc.typ, tc.spec, gotJSON, wantJSON)
			}
		})
	}
}

// The regex works on the text of the whole input, which is the workspace of the pipe, so it finds what is in the text
// of every value.
func TestRegexTransformMatchesTheTextOfTheInput(t *testing.T) {
	pt := NewTransform("nums", TransformRegex, "[0-9]+")
	if got := transformValue(t, pt, "a1b22c333"); !sameJSON(got, map[string]interface{}{"nums": []interface{}{"1", "22", "333"}}) {
		t.Errorf("matches in text = %v", got)
	}
	if got := transformValue(t, pt, 4096); !sameJSON(got, map[string]interface{}{"nums": []interface{}{"4096"}}) {
		t.Errorf("matches in number = %v", got)
	}
}

func TestTransformOfInputFromJSON(t *testing.T) {
	var in interface{}
	if err := json.Unmarshal([]byte(`{"items":[{"name":"one","n":1},{"name":"two","n":2}]}`), &in); err != nil {
		t.Fatal(err)
	}
	pt := NewTransform("names", TransformJQ, "[.items[].name]")
	if got := transformValue(t, pt, in); !reflect.DeepEqual(got, map[string]interface{}{"names": []interface{}{"one", "two"}}) {
		t.Errorf("names = %v", got)
	}
	pt = NewTransform("total", TransformTemplate, `{{range .items}}{{.n}},{{end}}`)
	if got := transformValue(t, pt, in); !reflect.DeepEqual(got, map[string]interface{}{"total": "1,2,"}) {
		t.Errorf("total = %v", got)
	}
}

// A JQ query or a JSON path that has an equals sign in it is split at it by the parser of util, which takes what is
// before the first equals sign for the name of the query. This is the way it is for now, and not something to rely on.
func TestTransformsWithEqualsSignInSpecDoNotWork(t *testing.T) {
	pt := NewTransform("out", TransformJQ, `.items | map(select(. == "p"))`)
	if got := transformValue(t, pt, sampleInput()); !reflect.DeepEqual(got, map[string]interface{}{}) {
		t.Errorf("jq with == gave %v", got)
	}
	pt = NewTransform("out", TransformJSONPath, `.items[?(@=="p")]`)
	got := transformValue(t, pt, sampleInput()).(map[string]interface{})
	if _, ok := got["out"]; ok {
		t.Errorf("jsonpath with == gave the result under the name of the transform: %v", got)
	}
}

func TestTransformsWithInvalidSpecs(t *testing.T) {
	in := sampleInput()
	if got := transformValue(t, NewTransform("out", TransformJQ, "bad(("), in); !reflect.DeepEqual(got, map[string]interface{}{}) {
		t.Errorf("invalid jq gave %v", got)
	}
	if got := transformValue(t, NewTransform("out", TransformTemplate, "{{bad"), in); !reflect.DeepEqual(got, map[string]interface{}{"out": ""}) {
		t.Errorf("invalid template gave %v", got)
	}
	if got := transformValue(t, NewTransform("out", TransformRegex, "("), in); !sameJSON(got, map[string]interface{}{"out": nil}) {
		t.Errorf("invalid regex gave %v", got)
	}
	// a path that doesn't parse gives no results either
	if got := transformValue(t, NewTransform("out", TransformJSONPath, ".a.b.c.d"), in); got == nil {
		t.Errorf("jsonpath with nothing to find gave nothing at all")
	}
}

func TestTransformWithUnknownTypeGivesNothing(t *testing.T) {
	pt := NewTransform("out", TransformType("Bogus"), "spec")
	if pt.IsJSONPath() || pt.IsJQ() || pt.IsTemplate() || pt.IsRegex() {
		t.Errorf("a transform of an unknown type has a type")
	}
	if pt.Out() != nil || pt.Map(sampleInput()) != nil {
		t.Errorf("a transform of an unknown type gave something")
	}
	if b, err := pt.MarshalOut(); err != nil || string(b) != `{"type":"Bogus"}` {
		t.Errorf("MarshalOut = %s, %v", b, err)
	}
	empty := &PipelineTransform{}
	if empty.Out() != nil || empty.Map(1) != nil || empty.IsRegex() {
		t.Errorf("a transform that wasn't initialised gave something")
	}
}

func TestPipelineTransformMarshalOut(t *testing.T) {
	pt := NewTransform("out", TransformJQ, ".a.b")
	b, err := pt.MarshalOut()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil || m["type"] != "JQ" || m["spec"] != ".a.b" {
		t.Errorf("MarshalOut = %s, %v", b, err)
	}
	// the JSON path keeps the spec without the white space around it
	pt = NewTransform("out", TransformJSONPath, "  {.a}  ")
	b, _ = pt.MarshalOut()
	if !strings.Contains(string(b), `"spec":"{.a}"`) {
		t.Errorf("MarshalOut = %s", b)
	}
}

func TestPipelineTransformJSON(t *testing.T) {
	pt := NewTransform("out", TransformRegex, "a+")
	b, err := json.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "out" || m["spec"] != "a+" || m["type"] != "Regex" || len(m) != 3 {
		t.Errorf("json = %s", b)
	}
	back := &PipelineTransform{}
	if err := json.Unmarshal([]byte(`{"type":"Template","spec":"{{.x}}"}`), back); err != nil {
		t.Fatal(err)
	}
	if back.Type != TransformTemplate || back.Spec != "{{.x}}" || back.Name != "" {
		t.Errorf("unmarshalled = %+v", back)
	}
	// the transform is made of what was read, and takes its name from the pipe
	back.Name = "named"
	back.InitTransform()
	if got := transformValue(t, back, map[string]interface{}{"x": "v"}); !reflect.DeepEqual(got, map[string]interface{}{"named": "v"}) {
		t.Errorf("transform of the unmarshalled = %v", got)
	}
}

func TestTransformsAreUsableFromManyGoroutinesWithTheirOwnInstances(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for _, pt := range []*PipelineTransform{
				NewTransform("j", TransformJQ, ".a.b"),
				NewTransform("t", TransformTemplate, "{{.a.b}}"),
				NewTransform("r", TransformRegex, "x"),
				NewTransform("p", TransformJSONPath, ".a.b"),
			} {
				for n := 0; n < 20; n++ {
					if pt.Map(sampleInput()) == nil {
						errs <- pt.Name
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for name := range errs {
		t.Errorf("transform %s gave nothing", name)
	}
}
