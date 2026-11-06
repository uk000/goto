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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"goto/pkg/invocation"
	"goto/pkg/job"
	k8sExec "goto/pkg/k8s/exec"
	k8sStore "goto/pkg/k8s/store"
	"goto/pkg/scripts"
	"goto/pkg/server/response/trigger"
	"goto/pkg/tunnel"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SourceType string

// The callbacks of the sources that are triggered by the traffic are registered through these, which the tests replace.
var (
	registerTriggerPipeCallback = trigger.RegisterPipeCallback
	registerTunnelPipeCallback  = tunnel.RegisterPipeCallback
)

const (
	SourceK8s         SourceType = "K8s"
	SourceK8sPodExec  SourceType = "K8sPodExec"
	SourceJob         SourceType = "Job"
	SourceScript      SourceType = "Script"
	SourceHTTPRequest SourceType = "HTTPRequest"
	SourceTunnel      SourceType = "Tunnel"
)

type Source interface {
	Init(pipe *Pipe)
	Generate(map[string]interface{})
	IsScript() bool
	GetName() string
	GetSpec() string
	GetContent() string
	GetInput() interface{}
	GetInputSource() string
	SetInput(interface{})
	generate() interface{}
	init()
	pipelineSource() *PipelineSource
}

type PipelineSource struct {
	Source         `json:"-"`
	Name           string      `json:"name"`
	Type           SourceType  `json:"type"`
	Spec           string      `json:"spec,omitempty"`
	Content        string      `json:"content,omitempty"`
	Input          interface{} `json:"input,omitempty"`
	InputSource    string      `json:"inputSource,omitempty"`
	ParseJSON      bool        `json:"parseJSON,omitempty"`
	ParseNumber    bool        `json:"parseNumber,omitempty"`
	ReuseIfExists  bool        `json:"reuseIfExists,omitempty"`
	Watch          bool        `json:"watch"`
	specFillers    []string
	contentFillers []string
	inputFillers   []string
	finalSpec      string
	finalContent   string
	finalInput     interface{}
	watching       bool
	triggerPipe    *Pipe
}

type AbstractSource struct {
	*PipelineSource `json:"-"`
}

type K8sSource struct {
	AbstractSource
}

type K8sPodExecSource struct {
	AbstractSource
}

type JobSource struct {
	AbstractSource
}

type ScriptSource struct {
	AbstractSource
}

// HTTPRequestSource keeps the last request that the trigger reported. It's written by the goroutine of the request that
// is reported and read by the run of the pipe, which are not the same goroutine when the pipe is running already.
type HTTPRequestSource struct {
	AbstractSource
	request  map[string]interface{}
	response map[string]interface{}
	lock     sync.Mutex
}

// TunnelSource keeps the last request and response that the tunnel reported, under the same circumstances as the
// HTTPRequestSource does.
type TunnelSource struct {
	AbstractSource
	request  map[string]interface{}
	response map[string]interface{}
	lock     sync.Mutex
}

// Init is a no-op default, as the real initialisation is done by PipelineSource.Init.
func (s *AbstractSource) Init(*Pipe) {}

// init is a no-op default for the sources that need no setup.
func (s *AbstractSource) init() {}

// Generate is a no-op default, as the work is done by PipelineSource.Generate.
func (s *AbstractSource) Generate(workspace map[string]interface{}) {
}

// IsScript reports false, as only ScriptSource overrides it.
func (s *AbstractSource) IsScript() bool {
	return false
}

// GetName returns the name of the source.
func (s *AbstractSource) GetName() string {
	return s.Name
}

// GetSpec returns the spec (the resource or script identifier) of the source.
func (s *AbstractSource) GetSpec() string {
	return s.Spec
}

// GetContent returns the content of the source, e.g. script text or a command.
func (s *AbstractSource) GetContent() string {
	return s.Content
}

// GetInput returns the input that is given to the source.
func (s *AbstractSource) GetInput() interface{} {
	return s.Input
}

// GetInputSource returns the name of the workspace entry that is fed to the source as its input.
func (s *AbstractSource) GetInputSource() string {
	return s.InputSource
}

// SetInput sets the input that is given to the source.
func (s *AbstractSource) SetInput(input interface{}) {
	s.Input = input
}

// generate is a default that produces nothing, which each source type overrides.
func (s *AbstractSource) generate() interface{} {
	return nil
}

// pipelineSource returns the PipelineSource that this source wraps.
func (s *AbstractSource) pipelineSource() *PipelineSource {
	return s.PipelineSource
}

// NewSource creates and initialises a PipelineSource of the given type and spec for the pipe.
func NewSource(name string, sourceType SourceType, spec string, pipe *Pipe) *PipelineSource {
	ps := &PipelineSource{Type: sourceType, Spec: spec}
	return ps.Init(name, pipe).pipelineSource()
}

// Init creates the concrete source for the source type (an unknown type gets a no-op one), finds the fillers in its spec, content and input, and sets up its trigger.
func (ps *PipelineSource) Init(name string, pipe *Pipe) Source {
	ps.Name = name
	var realSource Source
	switch ps.Type {
	case SourceK8s:
		realSource = &K8sSource{AbstractSource{PipelineSource: ps}}
	case SourceK8sPodExec:
		realSource = &K8sPodExecSource{AbstractSource{PipelineSource: ps}}
	case SourceJob:
		realSource = &JobSource{AbstractSource{PipelineSource: ps}}
	case SourceScript:
		realSource = &ScriptSource{AbstractSource{PipelineSource: ps}}
	case SourceHTTPRequest:
		realSource = &HTTPRequestSource{AbstractSource: AbstractSource{PipelineSource: ps}}
	case SourceTunnel:
		realSource = &TunnelSource{AbstractSource: AbstractSource{PipelineSource: ps}}
	default:
		// a source of an unknown type has nothing to generate, but it can still be a part of the pipe
		log.Printf("Pipe: Source [%s] has unknown type [%s]\n", name, ps.Type)
		realSource = &AbstractSource{PipelineSource: ps}
	}
	// the fillers are found again whenever the source is initialised, so they are not to be added to the earlier ones
	ps.specFillers, ps.contentFillers, ps.inputFillers = nil, nil, nil
	if ps.Spec != "" {
		for _, filler := range util.GetFillers(ps.Spec) {
			ps.specFillers = append(ps.specFillers, filler)
		}
	}
	if ps.Content != "" {
		for _, filler := range util.GetFillers(ps.Content) {
			ps.contentFillers = append(ps.contentFillers, filler)
		}
	}
	if ps.Input != nil {
		if input, ok := ps.Input.(string); ok {
			for _, filler := range util.GetFillers(input) {
				ps.inputFillers = append(ps.inputFillers, filler)
			}
		}
	}
	ps.triggerPipe = pipe
	ps.Source = realSource
	realSource.init()
	return realSource
}

// func (ps *PipelineSource) MarshalJSON() ([]byte, error) {
//   data := map[string]interface{}{}
//   data["type"] = ps.Type
//   if ps.source != nil {
//     data["spec"] = ps.source.GetSpec()
//   }
//   return json.Marshal(data)
// }

// processFillers resolves the fillers in the spec, content and input from the workspace into the final values used by this run.
func (ps *PipelineSource) processFillers(workspace map[string]interface{}) {
	if ps.Spec != "" {
		ps.finalSpec = ps.Spec
		for _, filler := range ps.specFillers {
			ps.finalSpec = util.FillFrom(ps.finalSpec, filler, workspace)
		}
	}
	if ps.Content != "" {
		ps.finalContent = ps.Content
		for _, filler := range ps.contentFillers {
			ps.finalContent = util.FillFrom(ps.finalContent, filler, workspace)
		}
	}
	if ps.Input != nil {
		ps.finalInput = ps.Input
		if input, ok := ps.finalInput.(string); ok {
			for _, filler := range ps.inputFillers {
				input = util.FillFrom(input, filler, workspace)
			}
			ps.finalInput = input
		}
	}
}

// Generate resolves the fillers, runs the concrete source, optionally parses the result as JSON or a number, and stores it in the workspace under the source name.
func (ps *PipelineSource) Generate(workspace map[string]interface{}) {
	ps.processFillers(workspace)
	result := ps.generate()
	if result == nil {
		return
	}
	switch val := result.(type) {
	case []interface{}:
		if len(val) == 1 {
			result = val[0]
		}
	}
	if ps.ParseJSON {
		j, ok := util.JSONFromJSONText(fmt.Sprint(result))
		if ok && !j.IsEmpty() {
			result = j.Value()
		}
	} else if ps.ParseNumber {
		if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(result))); err == nil {
			result = n
		}
	}
	workspace[ps.Name] = result
}

// watch registers a K8s resource watch that triggers the pipe whenever the resource is added, updated or deleted.
func (k *K8sSource) watch() {
	if k.triggerPipe != nil && k.Watch && !k.watching {
		resourceID := k.finalSpec
		if resourceID == "" {
			resourceID = k.Spec
		}
		kw := &k8sStore.K8sResourceWatchCallback{
			Name: k.triggerPipe.Name,
			OnAdd: func(namespace, name string, obj interface{}) {
				log.Printf("K8s Resource [%s/%s] Add triggered pipe [%s]\n", namespace, name, k.triggerPipe.Name)
				k.triggerPipe.Trigger()
			},
			OnUpdate: func(namespace, name string, obj interface{}) {
				log.Printf("K8s Resource [%s/%s] Update triggered pipe [%s]\n", namespace, name, k.triggerPipe.Name)
				k.triggerPipe.Trigger()
			},
			OnDelete: func(namespace, name string, obj interface{}) {
				log.Printf("K8s Resource [%s/%s] Delete triggered pipe [%s]\n", namespace, name, k.triggerPipe.Name)
				k.triggerPipe.Trigger()
			},
		}
		k8sStore.WatchResourceById(resourceID, kw)
	}
}

// init starts watching the K8s resource.
func (k *K8sSource) init() {
	k.watch()
}

// generate returns the current value of the K8s resource, and starts watching it if it isn't yet.
func (k *K8sSource) generate() interface{} {
	k.watch()
	k.watching = true
	if j, _ := k8sStore.GetResourceByID(k.finalSpec); j != nil {
		return j.Value()
	}
	return nil
}

// prepareCommand builds the command to run, piping the input (if any) through xargs ahead of the content.
func (k *K8sPodExecSource) prepareCommand() string {
	cmd := ""
	if k.finalInput != nil {
		cmd = "echo '" + fmt.Sprint(k.finalInput) + "' | xargs "
	}
	cmd += k.finalContent
	return cmd
}

// generate executes the command in the container of the pod (namespace/label/container) and returns its output.
func (k *K8sPodExecSource) generate() interface{} {
	podID := strings.Split(k.finalSpec, "/")
	if len(podID) != 3 {
		log.Printf("Pipe: Invalid pod [%s] for source [%s], want namespace/label/container\n", k.finalSpec, k.Name)
		return nil
	}
	command := k.prepareCommand()
	if out, err := k8sExec.PodExec(podID[0], podID[1], podID[2], command); err == nil {
		return out
	} else {
		log.Printf("Pipe: Error executing command [%+v] on pod [%+v]\n", command, k.finalSpec)
	}
	return nil
}

// watch registers a job watcher that triggers the pipe whenever the job reports results.
func (j *JobSource) watch() {
	if j.triggerPipe != nil && !j.watching {
		j.watching = true
		job.Manager.AddJobWatcher(j.finalSpec, j.Name, func(job string, runId int, results []*job.JobResult) {
			log.Printf("Job Source [%s] triggered pipe [%s]\n", j.Name, j.triggerPipe.Name)
			j.triggerPipe.Trigger()
		})
	}
}

// processJobResult returns the data of a job result, JSON-encoding it if it is an invocation result.
func (j *JobSource) processJobResult(r *job.JobResult) interface{} {
	var result interface{}
	switch data := r.Data.(type) {
	case *invocation.InvocationResult:
		if r, err := json.Marshal(data); err == nil {
			result = string(r)
		} else {
			log.Printf("Job Source [%s] failed to process result with error: %s\n", j.Name, err.Error())
		}
	default:
		result = r.Data
	}
	return result
}

// generate returns the latest results of the job if reuse is set and there are any, else runs the job with the input and waits up to 10s for its results.
func (j *JobSource) generate() interface{} {
	var result interface{}
	if j.ReuseIfExists {
		if latestResults := job.GetLatestJobResults(j.finalSpec); len(latestResults) > 0 {
			var data []interface{}
			for _, r := range latestResults {
				data = append(data, j.processJobResult(r))
			}
			result = data
		}
	}
	if result == nil {
		var rawInput []byte
		if j.finalInput != nil {
			rawInput = []byte(fmt.Sprint(j.finalInput))
		}
		if job.RunJobWithInputAndWait(j.finalSpec, nil, rawInput, 10*time.Second) {
			jobResults := job.Manager.GetLatestJobResults(j.finalSpec)
			var results []interface{}
			for _, jr := range jobResults {
				results = append(results, j.processJobResult(jr))
			}
			result = results
		} else {
			log.Printf("Failed to run job [%s] and get results\n", j.finalSpec)
		}
	}
	j.watch()
	return result
}

// generate runs the script with the input as its stdin and returns what it wrote.
func (s *ScriptSource) generate() interface{} {
	input := ""
	if s.finalInput != nil {
		input = fmt.Sprint(s.finalInput)
	}
	r := strings.NewReader(input)
	var buff bytes.Buffer
	w := bufio.NewWriter(&buff)
	scripts.Scripts.RunScript(s.finalSpec, nil, r, w)
	w.Flush()
	return buff.String()
}

// IsScript reports true, which makes the pipe register the script content as a script.
func (s *ScriptSource) IsScript() bool {
	return true
}

// watch registers a trigger callback that records the request and response that the trigger reported, and triggers the pipe.
func (h *HTTPRequestSource) watch() {
	if h.triggerPipe != nil && !h.watching {
		registerTriggerPipeCallback(h.Spec, h.Name, func(trigger, source string, port int, r *http.Request, statusCode int, responseHeaders http.Header) {
			log.Printf("HTTP Request Trigger [%s] Source [%s] triggered pipe [%s]\n", trigger, source, h.triggerPipe.Name)
			rs := util.GetRequestStore(r)
			request := map[string]interface{}{}
			request["trigger"] = trigger
			request["host"] = r.Host
			request["uri"] = r.RequestURI
			request["headers"] = r.Header.Clone()
			request["body"] = rs.RequestPayload
			response := map[string]interface{}{}
			response["status"] = statusCode
			response["headers"] = responseHeaders.Clone()
			h.lock.Lock()
			h.request, h.response = request, response
			h.lock.Unlock()
			h.triggerPipe.Trigger()
		})
	}
}

// init starts watching the HTTP request trigger.
func (h *HTTPRequestSource) init() {
	h.watch()
}

// generate returns the last request and response that the trigger reported, and starts watching it if it isn't yet.
func (h *HTTPRequestSource) generate() interface{} {
	h.watch()
	h.watching = true
	h.lock.Lock()
	defer h.lock.Unlock()
	return map[string]interface{}{"request": h.request, "response": h.response}
}

// watch registers a tunnel callback that records the request and response that the tunnel reported, and triggers the pipe.
func (t *TunnelSource) watch() {
	if t.triggerPipe != nil && !t.watching {
		registerTunnelPipeCallback(t.Spec, t.Name, func(endpoint, source string, port int, r *http.Request, statusCode int, responseHeaders http.Header, responseBody io.ReadCloser) {
			log.Printf("Tunnel Endpoint [%s] Source [%s] triggered pipe [%s]\n", endpoint, source, t.triggerPipe.Name)
			request := map[string]interface{}{}
			request["host"] = r.Host
			request["uri"] = r.RequestURI
			request["headers"] = r.Header.Clone()
			request["body"] = util.Read(r.Body)
			response := map[string]interface{}{}
			response["endpoint"] = endpoint
			response["headers"] = responseHeaders.Clone()
			response["body"] = util.Read(responseBody)
			response["status"] = statusCode
			t.lock.Lock()
			t.request, t.response = request, response
			t.lock.Unlock()
			t.triggerPipe.Trigger()
		})
	}
}

// init starts watching the tunnel.
func (t *TunnelSource) init() {
	t.watch()
}

// generate returns the last request and response that the tunnel reported, and starts watching it if it isn't yet.
func (t *TunnelSource) generate() interface{} {
	t.watch()
	t.watching = true
	t.lock.Lock()
	defer t.lock.Unlock()
	return map[string]interface{}{"request": t.request, "response": t.response}
}
