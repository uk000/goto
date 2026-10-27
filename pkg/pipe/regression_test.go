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
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/scripts"
	"goto/pkg/server/response/trigger"
)

// mustNotPanic reports what panicked, instead of letting the test binary go down with it.
func mustNotPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	fn()
}

// captureTriggerCallbacks takes over the registration of the callbacks of the HTTP request sources, and keeps them
// under the name of the target and the source.
func captureTriggerCallbacks(t *testing.T) *sync.Map {
	t.Helper()
	var callbacks sync.Map
	prev := registerTriggerPipeCallback
	registerTriggerPipeCallback = func(target, source string, cb trigger.PipeCallback) { callbacks.Store(target+"/"+source, cb) }
	t.Cleanup(func() { registerTriggerPipeCallback = prev })
	return &callbacks
}

func TestRegressionNewSourceKeepsSpec(t *testing.T) {
	// the spec that a source is made with used to be dropped, which left a source that had nothing to run
	ps := NewSource("s", SourceScript, "the-script", nil)
	if ps.Spec != "the-script" || ps.GetSpec() != "the-script" {
		t.Errorf("spec = %q", ps.Spec)
	}
}

func TestRegressionSourceOfUnknownTypeDoesNotPanic(t *testing.T) {
	mustNotPanic(t, "initialising a source of an unknown type", func() {
		ps := NewSource("s", SourceType("Bogus"), "spec", nil)
		ws := map[string]interface{}{}
		ps.Generate(ws)
		if _, ok := ws["s"]; ok {
			t.Errorf("a source of an unknown type gave %v", ws["s"])
		}
	})
	mustNotPanic(t, "adding a pipe with a source of an unknown type", func() {
		pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"s":{"name":"s","type":"Bogus"}},"transforms":{"k":{"type":"Template","spec":"v"}}}`)
		if out := runPipe(t, pipe); out["k"] != "v" {
			t.Errorf("out = %v", out)
		}
	})
}

func TestRegressionTransformOfUnknownTypeDoesNotPanic(t *testing.T) {
	mustNotPanic(t, "initialising a transform of an unknown type", func() {
		pt := &PipelineTransform{AbstractTransform: AbstractTransform{Name: "t", Spec: "x"}, Type: TransformType("Bogus")}
		pt.InitTransform()
		if j := pt.Map(map[string]interface{}{"a": 1}); j != nil && !j.IsEmpty() {
			t.Errorf("a transform of an unknown type gave %v", j.Value())
		}
	})
	mustNotPanic(t, "running a pipe with a transform of an unknown type", func() {
		pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"bogus":{"type":"Nonsense"},"k":{"type":"Template","spec":"v"}}}`)
		if out := runPipe(t, pipe); !reflect.DeepEqual(out, map[string]interface{}{"k": "v"}) {
			t.Errorf("out = %v", out)
		}
	})
}

func TestRegressionInvalidRegexDoesNotPanic(t *testing.T) {
	mustNotPanic(t, "a transform with a regex that doesn't compile", func() {
		pt := &PipelineTransform{AbstractTransform: AbstractTransform{Name: "r", Spec: "(unclosed"}, Type: TransformRegex}
		pt.InitTransform()
		pt.Map(map[string]interface{}{"a": "text"})
	})
}

func TestRegressionInvalidTemplateDoesNotPanic(t *testing.T) {
	mustNotPanic(t, "a transform with a template that doesn't parse", func() {
		pt := &PipelineTransform{AbstractTransform: AbstractTransform{Name: "tpl", Spec: "{{ .a "}, Type: TransformTemplate}
		pt.InitTransform()
		pt.Map(map[string]interface{}{"a": "text"})
	})
}

func TestRegressionPipeWithoutSourcesCanTakeSources(t *testing.T) {
	// a pipe that is posted without sources has no map for them, and used to have it assigned to when one was added
	for name, add := range map[string]func(pipe string) error{
		"k8s":    func(pipe string) error { return Manager.AddK8sSource(pipe, "s", "/v1/pods") },
		"script": func(pipe string) error { return Manager.AddScriptSource(pipe, "s", "") },
		"source": func(pipe string) error {
			return Manager.AddSource(pipe, &PipelineSource{Name: "s", Type: SourceHTTPRequest, Spec: "t"})
		},
	} {
		mustNotPanic(t, "adding a "+name+" source", func() {
			pipe := newManagedPipe(t, `{"name":"$NAME"}`)
			if err := add(pipe.Name); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			if pipe.Sources["s"] == nil {
				t.Errorf("%s: the source was not added", name)
			}
		})
	}
}

func TestRegressionPipeWithNilEntriesCanBeAdded(t *testing.T) {
	mustNotPanic(t, "adding a pipe with entries that have no details", func() {
		pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"a":null},"transforms":{"t":null}}`)
		if len(pipe.Sources) != 0 || len(pipe.Transforms) != 0 {
			t.Errorf("sources = %v, transforms = %v", pipe.Sources, pipe.Transforms)
		}
		runPipe(t, pipe)
	})
}

func TestRegressionPodExecWithInvalidSpecDoesNotPanic(t *testing.T) {
	for _, spec := range []string{"", "ns", "ns/label", "ns/label/container/extra"} {
		mustNotPanic(t, fmt.Sprintf("generating with spec %q", spec), func() {
			ps := NewSource("out", SourceK8sPodExec, spec, nil)
			ps.Content = "ls"
			ws := map[string]interface{}{}
			ps.Generate(ws)
			if _, ok := ws["out"]; ok {
				t.Errorf("spec %q: gave %v", spec, ws["out"])
			}
		})
	}
}

func TestRegressionScriptSourceWithoutInputGetsNoStdin(t *testing.T) {
	// a source that had no input used to give "<nil>" to the script
	name := uniqueName("catnothing")
	scripts.Scripts.AddScript(name, "cat", false)
	t.Cleanup(func() { scripts.Scripts.RemoveScript(name) })
	ws := map[string]interface{}{}
	ps := &PipelineSource{Type: SourceScript, Spec: name}
	ps.Init("out", nil)
	ps.Generate(ws)
	if ws["out"] != "" {
		t.Errorf("output = %q", ws["out"])
	}
}

func TestRegressionDefaultStageRunsSourcesInOrderOfTheirNames(t *testing.T) {
	// the sources of a pipe are known by the keys of its map, and not necessarily by names of their own. The default
	// stage used to list the names, which were not set yet, and so it ran nothing.
	first, second := uniqueName("firstscript"), uniqueName("secondscript")
	t.Cleanup(func() { scripts.Scripts.RemoveScript(first); scripts.Scripts.RemoveScript(second) })
	pipe := newManagedPipe(t, fmt.Sprintf(`{"name":"$NAME","sources":{
		"b_src":{"type":"Script","spec":%q,"content":"cat","inputSource":"a_src"},
		"a_src":{"type":"Script","spec":%q,"content":"printf ready"}}}`, second, first))
	if got := pipe.Stages[0].Sources; !reflect.DeepEqual(got, []string{"a_src", "b_src"}) {
		t.Errorf("sources of the default stage = %v", got)
	}
	// b_src is fed by a_src, which is only possible when a_src is run first
	if out := runPipe(t, pipe); out["a_src"] != "ready" || out["b_src"] != "ready" {
		t.Errorf("out = %v", out)
	}
}

func TestRegressionFillersAreNotRepeatedByInit(t *testing.T) {
	ps := &PipelineSource{Type: SourceScript, Spec: "spec-{a}", Content: "content-{b}", Input: "input-{c}"}
	ps.Init("s", nil)
	ps.Init("s", nil)
	ps.Init("s", nil)
	if !reflect.DeepEqual(ps.specFillers, []string{"{a}"}) || !reflect.DeepEqual(ps.contentFillers, []string{"{b}"}) ||
		!reflect.DeepEqual(ps.inputFillers, []string{"{c}"}) {
		t.Errorf("fillers = %v, %v, %v", ps.specFillers, ps.contentFillers, ps.inputFillers)
	}
}

func TestRegressionManagerChangesWhilePipesAreUsed(t *testing.T) {
	// removing a pipe and clearing them all used to change the maps of the manager under the lock for reading
	const rounds = 40
	var wg sync.WaitGroup
	prefix := uniqueName("churn")
	worker := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				fn(i)
			}
		}()
	}
	t.Cleanup(func() {
		for i := 0; i < rounds; i++ {
			removePipeForTest(fmt.Sprintf("%s-%d", prefix, i))
		}
	})
	worker(func(i int) { Manager.CreatePipe(fmt.Sprintf("%s-%d", prefix, i)) })
	worker(func(i int) { Manager.RemovePipe(fmt.Sprintf("%s-%d", prefix, i)) })
	worker(func(i int) { Manager.DumpPipes() })
	worker(func(i int) { Manager.GetRuns(fmt.Sprintf("%s-%d", prefix, i)) })
	worker(func(i int) { Manager.AddRun(NewPipe(fmt.Sprintf("%s-%d", prefix, i)), nil) })
	// a pipe that is cleared and removed all at once is not what the others of the tests may have in the manager, so
	// this is on a manager of its own
	other := (&PipeManager{}).init()
	worker(func(i int) { other.CreatePipe(fmt.Sprintf("x%d", i)) })
	worker(func(i int) { other.ClearPipe("") })
	worker(func(i int) { other.DumpPipes() })
	wg.Wait()
}

func TestRegressionPipesAreReportedWhileTheyChange(t *testing.T) {
	// reporting the pipes, and running them, used to be done while the sources and the transforms of the pipe were
	// being changed
	const rounds = 40
	captureTriggerCallbacks(t)
	pipe := newManagedPipe(t, `{"name":"$NAME",
		"sources":{"in":{"name":"in","type":"HTTPRequest","spec":"t","inputSource":"tmpl"}},
		"transforms":{"tmpl":{"type":"Template","spec":"templated"}},
		"stages":[{"label":"one","transforms":["tmpl"]},{"label":"two","sources":["in"]}]}`)
	var wg sync.WaitGroup
	worker := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				fn(i)
			}
		}()
	}
	worker(func(i int) {
		name := fmt.Sprintf("extra%d", i)
		if err := Manager.AddSource(pipe.Name, &PipelineSource{Name: name, Type: SourceHTTPRequest, Spec: "t" + name}); err != nil {
			t.Error(err)
		}
		Manager.RemoveSource(pipe.Name, name)
	})
	worker(func(i int) {
		if !strings.Contains(Manager.DumpPipes(), pipe.Name) {
			t.Error("the pipe is not reported")
		}
		Manager.GetRuns(pipe.Name)
	})
	worker(func(i int) {
		if err := Manager.RunPipe(pipe.Name, nil, false); err != nil {
			t.Error(err)
		}
	})
	churn := uniqueName("churnpipe")
	t.Cleanup(func() { removePipeForTest(churn) })
	worker(func(i int) {
		// a pipe that is added is ready before it's there to be reported or run
		p := &Pipe{}
		if err := json.Unmarshal([]byte(fmt.Sprintf(`{"name":%q,"sources":{"s":{"name":"s","type":"HTTPRequest","spec":"c"}},"transforms":{"k":{"type":"Template","spec":"v"}}}`, churn)), p); err != nil {
			t.Error(err)
			return
		}
		Manager.AddPipe(p)
		Manager.RunPipe(churn, nil, false)
	})
	wg.Wait()
	if got := runCount(pipe.Name); got != rounds {
		t.Errorf("runs = %d, want %d", got, rounds)
	}
	var reported map[string]*Pipe
	if err := json.Unmarshal([]byte(Manager.DumpPipes()), &reported); err != nil || reported[pipe.Name] == nil || reported[pipe.Name].Sources["in"] == nil {
		t.Errorf("reported = %s, %v", Manager.DumpPipes(), err)
	}
}

func TestRegressionRunsOfAPipeDoNotOverlap(t *testing.T) {
	// the sources and the transforms of a pipe keep the state of the run that is going, so the runs of a pipe can't be
	// going at the same time, but they used to be
	const delay = 150 * time.Millisecond
	pipe := newManagedPipe(t, fmt.Sprintf(`{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"v"}},
		"stages":[{"label":"slow","transforms":["k"],"delay":"%dms"}]}`, delay.Milliseconds()))
	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Manager.RunPipe(pipe.Name, nil, false)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	// the delays are waited out one after the other, as nothing sleeps for less than it is told to
	if took := time.Since(start); took < 2*delay {
		t.Errorf("two runs took %v, which is less than the delays that they have to wait out one after the other", took)
	}
	if got := runCount(pipe.Name); got != 2 {
		t.Errorf("runs = %d, want 2", got)
	}
}

func TestRegressionAddingASourceKeepsWhatTheOthersReported(t *testing.T) {
	// a source that is added made the earlier ones start again, which dropped the request that the HTTP request source
	// had been told of
	callbacks := captureTriggerCallbacks(t)
	pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"req":{"name":"req","type":"HTTPRequest","spec":"trig"}}}`)
	cbAny, ok := callbacks.Load("trig/req")
	if !ok {
		t.Fatal("no callback was registered")
	}
	cb := cbAny.(trigger.PipeCallback)
	cb("trig", "req", 8080, newRequestWithBody(t, "POST", "/first?x=1", "first-body"), 200, http.Header{})
	waitForRuns(t, pipe.Name, 1)

	if err := Manager.AddSource(pipe.Name, &PipelineSource{Name: "other", Type: SourceHTTPRequest, Spec: "another"}); err != nil {
		t.Fatal(err)
	}
	if err := Manager.RunPipe(pipe.Name, nil, false); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(pipe.Name)
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	reported, _ := runs[1].Out["req"].(map[string]interface{})
	request, _ := reported["request"].(map[string]interface{})
	if request["uri"] != "/first?x=1" || request["body"] != "first-body" {
		t.Errorf("the request that the source was told of is gone: %v", reported)
	}
}

func TestRegressionAddNilSourceDoesNotPanic(t *testing.T) {
	pipe := NewPipe(uniqueName("nilsource"))
	mustNotPanic(t, "adding a source that is nil", func() { pipe.AddSource(nil) })
	if len(pipe.Sources) != 0 {
		t.Errorf("sources = %v", pipe.Sources)
	}
}

func TestRegressionReportedRequestIsNotChangedByTheCaller(t *testing.T) {
	// the headers that the source reports used to be the ones of the request itself, which go on being used
	callbacks := captureTriggerCallbacks(t)
	pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"req":{"name":"req","type":"HTTPRequest","spec":"hdrs"}}}`)
	cbAny, ok := callbacks.Load("hdrs/req")
	if !ok {
		t.Fatal("no callback was registered")
	}
	r := newRequestWithBody(t, "GET", "/h", "")
	respHeaders := http.Header{"X-Resp": {"before"}}
	cbAny.(trigger.PipeCallback)("hdrs", "req", 8080, r, 200, respHeaders)
	waitForRuns(t, pipe.Name, 1)
	r.Header.Set("X-Caller", "after")
	respHeaders.Set("X-Resp", "after")

	if err := Manager.RunPipe(pipe.Name, nil, false); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(pipe.Name)
	reported := runs[len(runs)-1].Out["req"].(map[string]interface{})
	request := reported["request"].(map[string]interface{})
	response := reported["response"].(map[string]interface{})
	if h, _ := request["headers"].(http.Header); h.Get("X-Caller") != "yes" {
		t.Errorf("request headers = %v", request["headers"])
	}
	if h, _ := response["headers"].(http.Header); h.Get("X-Resp") != "before" {
		t.Errorf("response headers = %v", response["headers"])
	}
}

func TestRegressionHTTPSourceToldOfRequestsWhilePipeRuns(t *testing.T) {
	// the request that the source is told of used to be written while a run of the pipe was reading it
	const rounds = 30
	callbacks := captureTriggerCallbacks(t)
	pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"req":{"name":"req","type":"HTTPRequest","spec":"busy"}}}`)
	cbAny, ok := callbacks.Load("busy/req")
	if !ok {
		t.Fatal("no callback was registered")
	}
	cb := cbAny.(trigger.PipeCallback)
	requests := make([]*http.Request, rounds)
	for i := range requests {
		requests[i] = newRequestWithBody(t, "POST", fmt.Sprintf("/busy/%d", i), "body")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, r := range requests {
			cb("busy", "req", 8080, r, 200, http.Header{"X-N": {"1"}})
		}
	}()
	runErrs := make(chan error, rounds)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			runErrs <- Manager.RunPipe(pipe.Name, nil, false)
		}
	}()
	wg.Wait()
	close(runErrs)
	for err := range runErrs {
		if err != nil {
			t.Error(err)
		}
	}
	// a request that arrives while the pipe runs doesn't trigger another run, but the last one is what is reported
	if err := Manager.RunPipe(pipe.Name, nil, false); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(pipe.Name)
	reported := runs[len(runs)-1].Out["req"].(map[string]interface{})
	request, _ := reported["request"].(map[string]interface{})
	if request["uri"] != fmt.Sprintf("/busy/%d", rounds-1) {
		t.Errorf("the last request that was reported is %v", request["uri"])
	}
}
