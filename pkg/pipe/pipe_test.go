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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/scripts"
)

// readCloser is a body that tells whether it was read to the end.
type readCloser struct {
	io.Reader
	eof    atomic.Bool
	closed atomic.Bool
}

func newReadCloser(s string) *readCloser { return &readCloser{Reader: strings.NewReader(s)} }

func (r *readCloser) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.eof.Store(true)
	}
	return n, err
}

func (r *readCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func (r *readCloser) closedOrDrained() bool { return r.eof.Load() || r.closed.Load() }

func runsOf(pipe string) []*PipeRun {
	Manager.lock.RLock()
	defer Manager.lock.RUnlock()
	return append([]*PipeRun(nil), Manager.PipeRuns[pipe]...)
}

func runCount(pipe string) int { return len(runsOf(pipe)) }

func waitForRuns(t *testing.T, pipe string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runCount(pipe) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pipe %s has %d runs, want at least %d", pipe, runCount(pipe), n)
}

// removePipeForTest takes everything of the pipe out of the manager.
func removePipeForTest(name string) {
	Manager.lock.Lock()
	delete(Manager.Pipes, name)
	delete(Manager.PipeRuns, name)
	Manager.lock.Unlock()
}

func newManagedPipe(t *testing.T, payload string) *Pipe {
	t.Helper()
	name := uniqueName("pipe")
	payload = strings.Replace(payload, "$NAME", name, 1)
	pipe := &Pipe{}
	if err := json.Unmarshal([]byte(payload), pipe); err != nil {
		t.Fatalf("pipe payload: %v", err)
	}
	Manager.AddPipe(pipe)
	t.Cleanup(func() { removePipeForTest(name) })
	return pipe
}

func runPipe(t *testing.T, pipe *Pipe) map[string]interface{} {
	t.Helper()
	var buf bytes.Buffer
	if err := Manager.RunPipe(pipe.Name, &buf, false); err != nil {
		t.Fatalf("RunPipe: %v", err)
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("pipe output %q: %v", buf.String(), err)
	}
	return out
}

func TestNewPipeAndManagerBasics(t *testing.T) {
	pipe := NewPipe("basic")
	if pipe.Name != "basic" || pipe.Sources == nil || pipe.Running {
		t.Errorf("pipe = %+v", pipe)
	}
	name := uniqueName("basicpipe")
	Manager.CreatePipe(name)
	t.Cleanup(func() { removePipeForTest(name) })
	if err := Manager.AddSource(name, nil); err == nil {
		t.Errorf("adding no source must be an error")
	}
	var dump map[string]*Pipe
	if err := json.Unmarshal([]byte(Manager.DumpPipes()), &dump); err != nil || dump[name] == nil || dump[name].Name != name {
		t.Errorf("dump = %s, %v", Manager.DumpPipes(), err)
	}
	Manager.RemovePipe(name)
	if err := json.Unmarshal([]byte(Manager.DumpPipes()), &dump); err != nil {
		t.Fatal(err)
	}
	// a pipe that was removed is not reported any more
	var after map[string]*Pipe
	_ = json.Unmarshal([]byte(Manager.DumpPipes()), &after)
	if after[name] != nil {
		t.Errorf("the pipe is still there after it was removed")
	}
}

func TestManagerReportsMissingPipes(t *testing.T) {
	missing := uniqueName("nosuchpipe")
	if err := Manager.AddK8sSource(missing, "s", "id"); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("AddK8sSource error = %v", err)
	}
	if err := Manager.AddScriptSource(missing, "s", "echo"); err == nil {
		t.Errorf("AddScriptSource must fail")
	}
	if err := Manager.AddSource(missing, &PipelineSource{Name: "s"}); err == nil {
		t.Errorf("AddSource must fail")
	}
	if err := Manager.RemoveSource(missing, "s"); err == nil {
		t.Errorf("RemoveSource must fail")
	}
	if err := Manager.RunPipe(missing, nil, false); err == nil {
		t.Errorf("RunPipe must fail")
	}
	runs := Manager.GetRuns(missing).(map[string]interface{})
	if b, _ := json.Marshal(runs); string(b) != `{"pipe":null,"runs":null}` {
		t.Errorf("runs of a missing pipe = %s", b)
	}
}

func TestAddPipeBuildsDefaultStageInOrderOfNames(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME",
		"sources":{"s_c":{"type":"HTTPRequest","spec":"x"},"s_a":{"type":"HTTPRequest","spec":"x"},"s_b":{"type":"HTTPRequest","spec":"x"}},
		"transforms":{"t_b":{"type":"JQ","spec":".a"},"t_a":{"type":"JQ","spec":".b"}}}`)
	if len(pipe.Stages) != 1 || pipe.Stages[0].Label != "default" {
		t.Fatalf("stages = %+v", pipe.Stages)
	}
	if got := pipe.Stages[0].Sources; !reflect.DeepEqual(got, []string{"s_a", "s_b", "s_c"}) {
		t.Errorf("default stage sources = %v", got)
	}
	if got := pipe.Stages[0].Transforms; !reflect.DeepEqual(got, []string{"t_a", "t_b"}) {
		t.Errorf("default stage transforms = %v", got)
	}
	// the sources and the transforms are named after their keys
	for key, s := range pipe.Sources {
		if s.Name != key {
			t.Errorf("source %s is named %s", key, s.Name)
		}
	}
	for key, tr := range pipe.Transforms {
		if tr.Name != key || !tr.IsJQ() {
			t.Errorf("transform %s = %+v", key, tr)
		}
	}
}

func TestAddPipeKeepsGivenStagesAndResetsRunning(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","running":true,
		"sources":{"s1":{"type":"HTTPRequest","spec":"x"}},
		"stages":[{"label":"only","sources":["s1"],"delay":"5ms"}]}`)
	if pipe.Running {
		t.Errorf("a pipe is not running when it is added")
	}
	if len(pipe.Stages) != 1 || pipe.Stages[0].Label != "only" || pipe.Stages[0].Delay.Duration != 5*time.Millisecond {
		t.Errorf("stages = %+v", pipe.Stages)
	}
}

func TestAddPipeIgnoresNil(t *testing.T) {
	Manager.AddPipe(nil)
	pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"nil":null,"s":{"type":"HTTPRequest"}},"transforms":{"nil":null,"t":{"type":"JQ","spec":".x"}}}`)
	if len(pipe.Sources) != 1 || pipe.Sources["s"] == nil || len(pipe.Transforms) != 1 || pipe.Transforms["t"] == nil {
		t.Errorf("sources = %v, transforms = %v", pipe.Sources, pipe.Transforms)
	}
}

func TestClearAndRemovePipe(t *testing.T) {
	a := newManagedPipe(t, `{"name":"$NAME","sources":{"s":{"type":"HTTPRequest"}}}`)
	b := newManagedPipe(t, `{"name":"$NAME","sources":{"s":{"type":"HTTPRequest"}}}`)
	Manager.ClearPipe(a.Name)
	if len(a.Sources) != 0 || len(b.Sources) != 1 {
		t.Errorf("clearing a pipe cleared sources: a=%d b=%d", len(a.Sources), len(b.Sources))
	}
	Manager.ClearPipe(uniqueName("nosuchpipe")) // nothing to clear is fine
	Manager.RemovePipe(b.Name)
	if err := Manager.RunPipe(b.Name, nil, false); err == nil {
		t.Errorf("a removed pipe must not run")
	}
	// clearing without a name takes all the pipes away, which is what the tests of the other pipes can't have, so
	// it's done on a manager of its own
	pm := (&PipeManager{}).init()
	pm.CreatePipe("x")
	pm.CreatePipe("y")
	pm.ClearPipe("")
	if len(pm.Pipes) != 0 || len(pm.PipeRuns) != 0 {
		t.Errorf("pipes = %v", pm.Pipes)
	}
}

func TestAddRunAndGetRuns(t *testing.T) {
	pipe := NewPipe(uniqueName("runspipe"))
	Manager.AddPipe(pipe)
	t.Cleanup(func() { removePipeForTest(pipe.Name) })
	Manager.AddRun(pipe, map[string]interface{}{"a": 1})
	Manager.AddRun(pipe, map[string]interface{}{"a": 2})
	runs := runsOf(pipe.Name)
	if len(runs) != 2 || runs[0].Name != pipe.Name || runs[1].ID <= runs[0].ID {
		t.Fatalf("runs = %+v", runs)
	}
	reported := Manager.GetRuns(pipe.Name).(map[string]interface{})
	if reported["pipe"] != pipe {
		t.Errorf("pipe = %v", reported["pipe"])
	}
	if got := reported["runs"].([]*PipeRun); len(got) != 2 || got[1].Out["a"] != 2 {
		t.Errorf("runs = %v", got)
	}
}

func TestRunPipeThroughSourcesAndTransforms(t *testing.T) {
	scriptName := uniqueName("greeter")
	pipe := newManagedPipe(t, fmt.Sprintf(`{"name":"$NAME",
		"sources":{
			"first":{"type":"Script","spec":%q,"content":"printf Name-1"},
			"second":{"type":"Script","spec":%q,"content":"cat","inputSource":"first"}
		},
		"transforms":{
			"upper":{"type":"Template","spec":"[{{.second}}]"},
			"count":{"type":"JQ","spec":".second | length"}
		},
		"stages":[
			{"label":"one","sources":["first"]},
			{"label":"two","sources":["second"],"transforms":["upper","count"]}
		]}`, scriptName, scriptName+"2"))
	t.Cleanup(func() { scripts.Scripts.RemoveScript(scriptName); scripts.Scripts.RemoveScript(scriptName + "2") })
	out := runPipe(t, pipe)
	// the second source gets what the first one gave as its input
	if out["first"] != "Name-1" || out["second"] != "Name-1" {
		t.Errorf("sources gave %v", out)
	}
	if out["upper"] != "[Name-1]" {
		t.Errorf("template gave %v", out["upper"])
	}
	if out["count"] != float64(len("Name-1")) {
		t.Errorf("jq gave %v", out["count"])
	}
	if pipe.Running {
		t.Errorf("the pipe is running after the run")
	}
	runs := runsOf(pipe.Name)
	if len(runs) != 1 || runs[0].Out["upper"] != "[Name-1]" {
		t.Errorf("runs = %+v", runs)
	}
	// the input that was handed to the second source is what the pipe reports of it
	if in := pipe.Sources["second"].GetInput(); in != "Name-1" {
		t.Errorf("input of the second source = %v", in)
	}
}

func TestRunPipeOutsAndEmptyWriter(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME",
		"transforms":{"keep":{"type":"Template","spec":"kept"},"drop":{"type":"Template","spec":"dropped"}},
		"outs":["keep","absent"]}`)
	out := runPipe(t, pipe)
	if !reflect.DeepEqual(out, map[string]interface{}{"keep": "kept", "absent": nil}) {
		t.Errorf("out = %v", out)
	}
	// a pipe that is run without anywhere to write to still records the run
	if err := Manager.RunPipe(pipe.Name, nil, true); err != nil {
		t.Fatal(err)
	}
	if runCount(pipe.Name) != 2 {
		t.Errorf("runs = %d, want 2", runCount(pipe.Name))
	}
}

func TestRunPipeWritesYAML(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"value"}}}`)
	var buf bytes.Buffer
	if err := Manager.RunPipe(pipe.Name, &buf, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "k: value" {
		t.Errorf("yaml = %q", got)
	}
}

func TestRunPipeDelaysStages(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"v"}},
		"stages":[{"label":"slow","transforms":["k"],"delay":"60ms"}]}`)
	start := time.Now()
	runPipe(t, pipe)
	if took := time.Since(start); took < 60*time.Millisecond {
		t.Errorf("the run took %v, which is less than the delay of the stage", took)
	}
}

func TestRunPipeSkipsUnknownSourcesAndTransforms(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"v"}},
		"stages":[{"label":"s","sources":["nope"],"transforms":["nope","k"]}]}`)
	out := runPipe(t, pipe)
	if !reflect.DeepEqual(out, map[string]interface{}{"k": "v"}) {
		t.Errorf("out = %v", out)
	}
}

func TestRunPipeWithTransformOfUnknownType(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"bogus":{"type":"Nonsense","spec":"x"},"k":{"type":"Template","spec":"v"}}}`)
	out := runPipe(t, pipe)
	if !reflect.DeepEqual(out, map[string]interface{}{"k": "v"}) {
		t.Errorf("out = %v", out)
	}
}

func TestRunPipeTransformsSeeWhatEarlierOnesGave(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME",
		"transforms":{"a_first":{"type":"JQ","spec":"{n: 5}"},"b_second":{"type":"JQ","spec":".a_first.n * 2"}},
		"stages":[{"label":"s","transforms":["a_first","b_second"]}]}`)
	out := runPipe(t, pipe)
	if !sameJSON(out["a_first"], map[string]interface{}{"n": float64(5)}) || out["b_second"] != float64(10) {
		t.Errorf("out = %v", out)
	}
}

func TestPipeAddAndRemoveSources(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME"}`)
	if err := Manager.AddK8sSource(pipe.Name, "k8s", "/v1/pods"); err != nil {
		t.Fatal(err)
	}
	if err := Manager.AddScriptSource(pipe.Name, "scr", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scripts.Scripts.RemoveScript("withcontent") })
	if err := Manager.AddScriptSource(pipe.Name, "withcontent", "echo hi"); err != nil {
		t.Fatal(err)
	}
	if err := Manager.AddSource(pipe.Name, &PipelineSource{Name: "req", Type: SourceHTTPRequest, Spec: "t"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct {
		typ  SourceType
		spec string
	}{
		"k8s":         {SourceK8s, "/v1/pods"},
		"scr":         {SourceScript, "scr"},
		"withcontent": {SourceScript, "withcontent"},
		"req":         {SourceHTTPRequest, "t"},
	} {
		s := pipe.Sources[name]
		if s == nil || s.Type != want.typ || s.Spec != want.spec || s.Name != name {
			t.Errorf("source %s = %+v, want %v with spec %q", name, s, want.typ, want.spec)
		}
	}
	if err := Manager.RemoveSource(pipe.Name, "k8s"); err != nil {
		t.Fatal(err)
	}
	if pipe.Sources["k8s"] != nil || len(pipe.Sources) != 3 {
		t.Errorf("sources = %v", pipe.Sources)
	}
	Manager.RemoveSource(pipe.Name, "never-added")
}

func TestPipeAddSourceInitialisesOnlyWhatItAdds(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"t":{"type":"Regex","spec":"a+"}}}`)
	before := pipe.Transforms["t"]
	pipe.AddSource(&PipelineSource{Name: "s", Type: SourceScript, Spec: "sp-{x}", Content: "echo {y}"})
	t.Cleanup(func() { scripts.Scripts.RemoveScript("sp-{x}") })
	if pipe.Transforms["t"] != before || !before.IsRegex() {
		t.Errorf("the transform was replaced")
	}
	if got := pipe.Sources["s"].specFillers; !reflect.DeepEqual(got, []string{"{x}"}) {
		t.Errorf("spec fillers = %v", got)
	}
	// adding another source initialises the earlier one again, which must not double its fillers
	pipe.AddSource(&PipelineSource{Name: "s2", Type: SourceScript, Spec: "other"})
	t.Cleanup(func() { scripts.Scripts.RemoveScript("other") })
	if got := pipe.Sources["s"].specFillers; !reflect.DeepEqual(got, []string{"{x}"}) {
		t.Errorf("spec fillers after adding another source = %v", got)
	}
	if got := pipe.Sources["s"].contentFillers; !reflect.DeepEqual(got, []string{"{y}"}) {
		t.Errorf("content fillers after adding another source = %v", got)
	}
}

func TestPipeTriggerRunsOnlyWhenNotRunning(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"v"}}}`)
	pipe.Trigger()
	if runCount(pipe.Name) != 1 {
		t.Fatalf("runs = %d, want 1", runCount(pipe.Name))
	}
	// a pipe that is in the middle of a run is not run again by the trigger
	pipe.runLock.Lock()
	pipe.Trigger()
	pipe.runLock.Unlock()
	if runCount(pipe.Name) != 1 {
		t.Errorf("the pipe was triggered while it was running")
	}
	pipe.Trigger()
	if runCount(pipe.Name) != 2 {
		t.Errorf("runs = %d, want 2", runCount(pipe.Name))
	}
}

func TestPipeRunsDoNotOverlap(t *testing.T) {
	pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"v"}},
		"stages":[{"label":"s","transforms":["k"],"delay":"5ms"}]}`)
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Manager.RunPipe(pipe.Name, nil, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if runCount(pipe.Name) != n {
		t.Errorf("runs = %d, want %d", runCount(pipe.Name), n)
	}
	ids := map[int]bool{}
	for _, r := range runsOf(pipe.Name) {
		if ids[r.ID] {
			t.Errorf("run id %d was used twice", r.ID)
		}
		ids[r.ID] = true
	}
}

func TestManagerConcurrentUse(t *testing.T) {
	const pipes, workers = 4, 4
	names := make([]string, pipes)
	for i := range names {
		pipe := newManagedPipe(t, `{"name":"$NAME","transforms":{"k":{"type":"Template","spec":"v"}}}`)
		names[i] = pipe.Name
	}
	scriptNames := make([]string, 0, pipes*workers)
	var scriptLock sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		for p, name := range names {
			wg.Add(1)
			go func(w, p int, name string) {
				defer wg.Done()
				for i := 0; i < 5; i++ {
					sname := fmt.Sprintf("%s-w%d-%d", name, w, i)
					scriptLock.Lock()
					scriptNames = append(scriptNames, sname)
					scriptLock.Unlock()
					if err := Manager.AddScriptSource(name, sname, "echo "+sname); err != nil {
						t.Error(err)
					}
					if err := Manager.AddSource(name, &PipelineSource{Name: sname + "-req", Type: SourceHTTPRequest, Spec: "t"}); err != nil {
						t.Error(err)
					}
					Manager.DumpPipes()
					Manager.GetRuns(name)
					if err := Manager.RunPipe(name, nil, false); err != nil {
						t.Error(err)
					}
					Manager.RemoveSource(name, sname)
				}
			}(w, p, name)
		}
	}
	wg.Wait()
	for _, s := range scriptNames {
		scripts.Scripts.RemoveScript(s)
	}
	for _, name := range names {
		if n := runCount(name); n != workers*5 {
			t.Errorf("pipe %s has %d runs, want %d", name, n, workers*5)
		}
	}
}
