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
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/job"
	"goto/pkg/scripts"
	"goto/pkg/server/response/trigger"
	"goto/pkg/tunnel"
	"goto/pkg/util"
)

var (
	nameSeq  atomic.Int32
	stubOnce sync.Once
)

// uniqueName is a name that no other test uses, as the pipes, the jobs and the scripts are kept by their names for as
// long as the tests run.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, nameSeq.Add(1))
}

// stubGlobalFuncs is for what the request store asks the server of, and there is no server here.
func stubGlobalFuncs() {
	stubOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	})
}

func addCommandJob(t *testing.T, name string, args ...string) {
	t.Helper()
	argsJSON, _ := json.Marshal(args)
	j, err := job.ParseJobFromPayload(fmt.Sprintf(`{"name":%q,"task":{"cmd":"echo","args":%s}}`, name, argsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := job.Manager.AddJob(j); err != nil {
		t.Fatal(err)
	}
}

func jobOutputs(name string) []string {
	var outputs []string
	for _, r := range job.GetLatestJobResults(name) {
		if s, ok := r.Data.(string); ok && s != "" {
			outputs = append(outputs, s)
		}
	}
	return outputs
}

// fakeSource is a source that generates what it is given, and that is initialised as the real ones are.
type fakeSource struct {
	AbstractSource
	out interface{}
}

func (f *fakeSource) generate() interface{} { return f.out }

func sourceGenerating(out interface{}, configure func(ps *PipelineSource)) *PipelineSource {
	ps := &PipelineSource{Name: "fake"}
	if configure != nil {
		configure(ps)
	}
	ps.Source = &fakeSource{AbstractSource: AbstractSource{PipelineSource: ps}, out: out}
	return ps
}

func TestNewSourceKeepsNameTypeAndSpec(t *testing.T) {
	pipe := NewPipe("owner")
	for _, st := range []SourceType{SourceK8s, SourceK8sPodExec, SourceJob, SourceScript, SourceHTTPRequest, SourceTunnel} {
		ps := NewSource("s-"+string(st), st, "spec-"+string(st), pipe)
		if ps.Name != "s-"+string(st) || ps.Type != st || ps.Spec != "spec-"+string(st) || ps.triggerPipe != pipe {
			t.Errorf("%s: source = %+v", st, ps)
		}
		if ps.Source == nil || ps.GetName() != ps.Name || ps.GetSpec() != ps.Spec {
			t.Errorf("%s: the source was not initialised", st)
		}
	}
}

func TestSourceInitMakesTheSourceOfTheType(t *testing.T) {
	for st, want := range map[SourceType]interface{}{
		SourceK8s:         &K8sSource{},
		SourceK8sPodExec:  &K8sPodExecSource{},
		SourceJob:         &JobSource{},
		SourceScript:      &ScriptSource{},
		SourceHTTPRequest: &HTTPRequestSource{},
		SourceTunnel:      &TunnelSource{},
	} {
		ps := &PipelineSource{Type: st}
		real := ps.Init("n", nil)
		if reflect.TypeOf(real) != reflect.TypeOf(want) {
			t.Errorf("%s: source is %T, want %T", st, real, want)
		}
		if ps.Name != "n" || ps.Source != real || real.pipelineSource() != ps {
			t.Errorf("%s: the source is not linked with its pipeline source", st)
		}
		if real.IsScript() != (st == SourceScript) {
			t.Errorf("%s: IsScript = %v", st, real.IsScript())
		}
	}
}

func TestSourceAccessors(t *testing.T) {
	ps := &PipelineSource{Type: SourceScript, Spec: "sp", Content: "ct", Input: "in", InputSource: "from"}
	real := ps.Init("n", nil)
	if real.GetName() != "n" || real.GetSpec() != "sp" || real.GetContent() != "ct" || real.GetInput() != "in" || real.GetInputSource() != "from" {
		t.Errorf("accessors = %q %q %q %v %q", real.GetName(), real.GetSpec(), real.GetContent(), real.GetInput(), real.GetInputSource())
	}
	real.SetInput(map[string]interface{}{"a": 1})
	if !reflect.DeepEqual(ps.Input, map[string]interface{}{"a": 1}) {
		t.Errorf("input = %v", ps.Input)
	}
	// the sources that have no code of their own generate nothing
	abstract := &AbstractSource{PipelineSource: ps}
	ws := map[string]interface{}{}
	abstract.Generate(ws)
	if abstract.generate() != nil || abstract.IsScript() || len(ws) != 0 {
		t.Errorf("an abstract source generated something")
	}
}

func TestSourceFillersAreTakenFromTheWorkspace(t *testing.T) {
	ps := &PipelineSource{Type: SourceScript, Spec: "run-{who}", Content: "echo {what} and {what} again, {unknown}", Input: "{who}:{n}"}
	ps.Init("s", nil)
	ps.processFillers(map[string]interface{}{"who": "me", "what": "this", "n": 7})
	if ps.finalSpec != "run-me" {
		t.Errorf("spec = %q", ps.finalSpec)
	}
	// a value is filled wherever it appears, and what the workspace doesn't have is left as it is
	if ps.finalContent != "echo this and this again, {unknown}" {
		t.Errorf("content = %q", ps.finalContent)
	}
	if ps.finalInput != "me:7" {
		t.Errorf("input = %v", ps.finalInput)
	}
	// the given texts are kept for the next run
	if ps.Spec != "run-{who}" || ps.Content != "echo {what} and {what} again, {unknown}" || ps.Input != "{who}:{n}" {
		t.Errorf("the source was changed: %+v", ps)
	}
	ps.processFillers(map[string]interface{}{"who": "you", "n": 8})
	if ps.finalSpec != "run-you" || ps.finalInput != "you:8" {
		t.Errorf("second run: spec = %q, input = %v", ps.finalSpec, ps.finalInput)
	}
}

func TestSourceInputThatIsNotTextIsKept(t *testing.T) {
	ps := &PipelineSource{Type: SourceScript, Input: map[string]interface{}{"k": "{v}"}}
	ps.Init("s", nil)
	ps.processFillers(map[string]interface{}{"v": "x"})
	if !reflect.DeepEqual(ps.finalInput, map[string]interface{}{"k": "{v}"}) {
		t.Errorf("input = %v", ps.finalInput)
	}
	none := &PipelineSource{Type: SourceScript}
	none.Init("s", nil)
	none.processFillers(map[string]interface{}{"v": "x"})
	if none.finalSpec != "" || none.finalContent != "" || none.finalInput != nil {
		t.Errorf("a source without a spec, a content and an input got them: %+v", none)
	}
}

func TestSourceGenerateStoresResultUnderName(t *testing.T) {
	ws := map[string]interface{}{}
	sourceGenerating("text", nil).Generate(ws)
	if !reflect.DeepEqual(ws, map[string]interface{}{"fake": "text"}) {
		t.Errorf("workspace = %v", ws)
	}
	// nothing is stored when nothing is generated, and what was there stays
	ws = map[string]interface{}{"fake": "old"}
	sourceGenerating(nil, nil).Generate(ws)
	if ws["fake"] != "old" {
		t.Errorf("workspace = %v", ws)
	}
}

func TestSourceGenerateUnwrapsSingleResult(t *testing.T) {
	ws := map[string]interface{}{}
	sourceGenerating([]interface{}{"only"}, nil).Generate(ws)
	if ws["fake"] != "only" {
		t.Errorf("single result = %v", ws["fake"])
	}
	ws = map[string]interface{}{}
	sourceGenerating([]interface{}{"a", "b"}, nil).Generate(ws)
	if !reflect.DeepEqual(ws["fake"], []interface{}{"a", "b"}) {
		t.Errorf("results = %v", ws["fake"])
	}
	ws = map[string]interface{}{}
	sourceGenerating([]interface{}{}, nil).Generate(ws)
	if !reflect.DeepEqual(ws["fake"], []interface{}{}) {
		t.Errorf("no results = %#v", ws["fake"])
	}
}

func TestSourceGenerateParsesJSONAndNumbers(t *testing.T) {
	ws := map[string]interface{}{}
	sourceGenerating(`{"a":[1,2],"b":"c"}`, func(ps *PipelineSource) { ps.ParseJSON = true }).Generate(ws)
	if got, _ := json.Marshal(ws["fake"]); string(got) != `{"a":[1,2],"b":"c"}` {
		t.Errorf("parsed JSON = %s", got)
	}
	if _, isText := ws["fake"].(string); isText {
		t.Errorf("the JSON was not parsed")
	}
	// what is not JSON stays as text
	ws = map[string]interface{}{}
	sourceGenerating("plain words", func(ps *PipelineSource) { ps.ParseJSON = true }).Generate(ws)
	if ws["fake"] != "plain words" {
		t.Errorf("plain text = %v", ws["fake"])
	}
	ws = map[string]interface{}{}
	sourceGenerating(" 42\n", func(ps *PipelineSource) { ps.ParseNumber = true }).Generate(ws)
	if ws["fake"] != 42 {
		t.Errorf("number = %#v", ws["fake"])
	}
	ws = map[string]interface{}{}
	sourceGenerating("4x2", func(ps *PipelineSource) { ps.ParseNumber = true }).Generate(ws)
	if ws["fake"] != "4x2" {
		t.Errorf("not a number = %#v", ws["fake"])
	}
	// the number is read only when the JSON is not
	ws = map[string]interface{}{}
	sourceGenerating("7", func(ps *PipelineSource) { ps.ParseJSON = true; ps.ParseNumber = true }).Generate(ws)
	if _, isInt := ws["fake"].(int); isInt {
		t.Errorf("ParseNumber was applied together with ParseJSON: %#v", ws["fake"])
	}
	// a result that is a single element of a list is parsed after the list is unwrapped
	ws = map[string]interface{}{}
	sourceGenerating([]interface{}{"15"}, func(ps *PipelineSource) { ps.ParseNumber = true }).Generate(ws)
	if ws["fake"] != 15 {
		t.Errorf("unwrapped number = %#v", ws["fake"])
	}
}

func TestScriptSourceRunsScriptWithInput(t *testing.T) {
	name := uniqueName("catscript")
	scripts.Scripts.AddScript(name, "cat", false)
	t.Cleanup(func() { scripts.Scripts.RemoveScript(name) })
	ps := NewSource("out", SourceScript, name, nil)
	ps.Input = "hello {who}"
	ps.Init("out", nil)
	ws := map[string]interface{}{"who": "world"}
	ps.Generate(ws)
	if ws["out"] != "hello world" {
		t.Errorf("output = %q", ws["out"])
	}
}

func TestScriptSourceFromContentOfThePipe(t *testing.T) {
	name := uniqueName("contentscript")
	pipe := NewPipe(uniqueName("scriptpipe"))
	ps := &PipelineSource{Type: SourceScript, Spec: name, Content: "echo from-pipe", Name: "out"}
	pipe.AddSource(ps)
	t.Cleanup(func() { scripts.Scripts.RemoveScript(name) })
	ws := map[string]interface{}{}
	pipe.Sources["out"].Generate(ws)
	if strings.TrimSpace(fmt.Sprint(ws["out"])) != "from-pipe" {
		t.Errorf("output = %q", ws["out"])
	}
}

func TestScriptSourceWithMissingScriptReportsIt(t *testing.T) {
	ps := NewSource("out", SourceScript, uniqueName("nosuchscript"), nil)
	ws := map[string]interface{}{}
	ps.Generate(ws)
	if !strings.Contains(fmt.Sprint(ws["out"]), "not found") {
		t.Errorf("output = %q", ws["out"])
	}
}

func TestJobSourceRunsJobAndCollectsResults(t *testing.T) {
	name := uniqueName("srcjob")
	addCommandJob(t, name, "job-says-hi")
	ps := NewSource("out", SourceJob, name, nil)
	ws := map[string]interface{}{}
	ps.Generate(ws)
	if got := fmt.Sprint(ws["out"]); !strings.Contains(got, "job-says-hi") {
		t.Errorf("results = %q", got)
	}
	// every generation runs the job again, unless the results of the earlier run are asked for
	ps.Generate(ws)
	if latest := job.GetLatestJobResults(name); len(latest) == 0 || !strings.HasPrefix(latest[0].ID, "2.") {
		t.Errorf("latest results = %+v, want the ones of run 2", latest)
	}
}

func TestJobSourceReusesExistingResults(t *testing.T) {
	name := uniqueName("reusejob")
	addCommandJob(t, name, "first-run")
	if !job.RunJobWithInputAndWait(name, nil, nil, 10*time.Second) {
		t.Fatal("job didn't run")
	}
	ps := NewSource("out", SourceJob, name, nil)
	ps.ReuseIfExists = true
	ws := map[string]interface{}{}
	ps.Generate(ws)
	if !strings.Contains(fmt.Sprint(ws["out"]), "first-run") {
		t.Errorf("results = %q", ws["out"])
	}
	if latest := job.GetLatestJobResults(name); len(latest) == 0 || !strings.HasPrefix(latest[0].ID, "1.") {
		t.Errorf("the job was run again: %+v", latest)
	}
	// with no results yet, the job is run
	fresh := uniqueName("freshjob")
	addCommandJob(t, fresh, "fresh-run")
	ps = NewSource("out", SourceJob, fresh, nil)
	ps.ReuseIfExists = true
	ws = map[string]interface{}{}
	ps.Generate(ws)
	if !strings.Contains(fmt.Sprint(ws["out"]), "fresh-run") {
		t.Errorf("results = %q", ws["out"])
	}
}

func TestJobSourceWithInputAndUnknownJob(t *testing.T) {
	name := uniqueName("inputjob")
	addCommandJob(t, name, "with-input")
	ps := NewSource("out", SourceJob, name, nil)
	ps.Input = "typed-{n}"
	ps.Init("out", nil)
	ws := map[string]interface{}{"n": 3}
	ps.Generate(ws)
	if !strings.Contains(fmt.Sprint(ws["out"]), "with-input") {
		t.Errorf("results = %q", ws["out"])
	}
	if got := ps.finalInput; got != "typed-3" {
		t.Errorf("the input of the job = %v", got)
	}
	missing := NewSource("out", SourceJob, uniqueName("nosuchjob"), nil)
	ws = map[string]interface{}{}
	missing.Generate(ws)
	if _, ok := ws["out"]; ok {
		t.Errorf("a job that doesn't exist gave %v", ws["out"])
	}
}

func TestJobSourceProcessJobResult(t *testing.T) {
	js := &JobSource{AbstractSource{PipelineSource: &PipelineSource{Name: "j"}}}
	if got := js.processJobResult(&job.JobResult{Data: "text"}); got != "text" {
		t.Errorf("text = %v", got)
	}
	if got := js.processJobResult(&job.JobResult{Data: 5}); got != 5 {
		t.Errorf("number = %v", got)
	}
	ir := &invocation.InvocationResult{TargetName: "tn", Retries: 2}
	got, ok := js.processJobResult(&job.JobResult{Data: ir}).(string)
	if !ok {
		t.Fatalf("an invocation result is given as JSON text")
	}
	var back invocation.InvocationResult
	if err := json.Unmarshal([]byte(got), &back); err != nil || back.TargetName != "tn" || back.Retries != 2 {
		t.Errorf("result = %s, %v", got, err)
	}
}

func TestJobSourceTriggersPipeWhenJobRuns(t *testing.T) {
	name := uniqueName("watchedjob")
	addCommandJob(t, name, "watched")
	pipe := newManagedPipe(t, fmt.Sprintf(`{"name":"$NAME","sources":{"out":{"type":"Job","spec":%q,"reuseIfExists":true}}}`, name))
	// the job is watched once the source generated, which happens when the pipe runs
	if err := Manager.RunPipe(pipe.Name, nil, false); err != nil {
		t.Fatal(err)
	}
	runsBefore := runCount(pipe.Name)
	if !job.RunJobWithInputAndWait(name, nil, nil, 10*time.Second) {
		t.Fatal("job didn't run")
	}
	waitForRuns(t, pipe.Name, runsBefore+1)
}

func TestK8sSourceWithInvalidResourceGeneratesNothing(t *testing.T) {
	ps := NewSource("out", SourceK8s, "not-a-resource-id", nil)
	ps.Watch = true
	ws := map[string]interface{}{}
	ps.Generate(ws)
	if _, ok := ws["out"]; ok {
		t.Errorf("an invalid resource gave %v", ws["out"])
	}
}

func TestK8sPodExecSourceCommand(t *testing.T) {
	ps := &PipelineSource{Type: SourceK8sPodExec}
	src := &K8sPodExecSource{AbstractSource{PipelineSource: ps}}
	ps.finalContent = "wc -c"
	if got := src.prepareCommand(); got != "wc -c" {
		t.Errorf("command = %q", got)
	}
	ps.finalInput = "some data"
	if got := src.prepareCommand(); got != "echo 'some data' | xargs wc -c" {
		t.Errorf("command with input = %q", got)
	}
}

func TestK8sPodExecSourceWithInvalidPodGeneratesNothing(t *testing.T) {
	for _, spec := range []string{"", "ns", "ns/label", "ns/label/container/extra"} {
		ps := NewSource("out", SourceK8sPodExec, spec, nil)
		ps.Content = "ls"
		ps.Init("out", nil)
		ws := map[string]interface{}{}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("spec %q: generating panicked: %v", spec, r)
				}
			}()
			ps.Generate(ws)
		}()
		if _, ok := ws["out"]; ok {
			t.Errorf("spec %q: gave %v", spec, ws["out"])
		}
	}
}

func newRequestWithBody(t *testing.T, method, uri, body string) *http.Request {
	t.Helper()
	stubGlobalFuncs()
	r := httptest.NewRequest(method, uri, strings.NewReader(body))
	r.Host = "caller.example"
	r.Header.Set("X-Caller", "yes")
	_, r, rs := util.WithRequestStore(r, nil)
	rs.RequestPayload = body
	return r
}

func TestHTTPRequestSourceRegistersCallbackAndReportsRequest(t *testing.T) {
	var callbacks sync.Map
	prev := registerTriggerPipeCallback
	registerTriggerPipeCallback = func(target, pipe string, cb trigger.PipeCallback) { callbacks.Store(target+"/"+pipe, cb) }
	t.Cleanup(func() { registerTriggerPipeCallback = prev })

	pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"req":{"type":"HTTPRequest","spec":"trig1"}}}`)
	cbAny, ok := callbacks.Load("trig1/req")
	if !ok {
		t.Fatalf("no callback was registered for the trigger target")
	}
	cb := cbAny.(trigger.PipeCallback)

	// nothing was received yet
	ws := map[string]interface{}{}
	pipe.Sources["req"].Generate(ws)
	if got := ws["req"].(map[string]interface{}); got["request"] != nil && len(got["request"].(map[string]interface{})) != 0 {
		t.Errorf("a request that was never received: %v", got)
	}

	r := newRequestWithBody(t, "POST", "/orders?id=7", "request-body")
	respHeaders := http.Header{"X-Resp": {"r1"}}
	cb("trig1", "req", 8080, r, 201, respHeaders)
	waitForRuns(t, pipe.Name, 1)

	runs := runsOf(pipe.Name)
	out := runs[len(runs)-1].Out["req"].(map[string]interface{})
	request := out["request"].(map[string]interface{})
	response := out["response"].(map[string]interface{})
	if request["trigger"] != "trig1" || request["host"] != "caller.example" || request["uri"] != "/orders?id=7" || request["body"] != "request-body" {
		t.Errorf("request = %v", request)
	}
	if h, _ := request["headers"].(http.Header); h.Get("X-Caller") != "yes" {
		t.Errorf("request headers = %v", request["headers"])
	}
	if response["status"] != 201 {
		t.Errorf("response = %v", response)
	}
	if h, _ := response["headers"].(http.Header); h.Get("X-Resp") != "r1" {
		t.Errorf("response headers = %v", response["headers"])
	}
}

func TestTunnelSourceRegistersCallbackAndReportsTraffic(t *testing.T) {
	var callbacks sync.Map
	prev := registerTunnelPipeCallback
	registerTunnelPipeCallback = func(tunnelName, pipe string, cb tunnel.PipeCallback) { callbacks.Store(tunnelName+"/"+pipe, cb) }
	t.Cleanup(func() { registerTunnelPipeCallback = prev })

	pipe := newManagedPipe(t, `{"name":"$NAME","sources":{"tun":{"type":"Tunnel","spec":"tunnel1"}}}`)
	cbAny, ok := callbacks.Load("tunnel1/tun")
	if !ok {
		t.Fatalf("no callback was registered for the tunnel")
	}
	cb := cbAny.(tunnel.PipeCallback)

	r := newRequestWithBody(t, "PUT", "/tunneled", "tunnel-request")
	body := newReadCloser("tunnel-response")
	cb("tunnel1", "tun", 8080, r, 202, http.Header{"X-T": {"t1"}}, body)
	waitForRuns(t, pipe.Name, 1)

	runs := runsOf(pipe.Name)
	out := runs[len(runs)-1].Out["tun"].(map[string]interface{})
	request := out["request"].(map[string]interface{})
	response := out["response"].(map[string]interface{})
	if request["host"] != "caller.example" || request["uri"] != "/tunneled" || request["body"] != "tunnel-request" {
		t.Errorf("request = %v", request)
	}
	if response["endpoint"] != "tunnel1" || response["body"] != "tunnel-response" || response["status"] != 202 {
		t.Errorf("response = %v", response)
	}
	if !body.closedOrDrained() {
		t.Errorf("the response body was not read")
	}
}
