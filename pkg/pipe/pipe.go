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
	"goto/pkg/job"
	"goto/pkg/scripts"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

type PipeStage struct {
	Label      string         `json:"label"`
	Sources    []string       `json:"sources"`
	Transforms []string       `json:"transforms"`
	Delay      types.Duration `json:"delay"`
}

type Pipe struct {
	Name       string                        `json:"name"`
	Sources    map[string]*PipelineSource    `json:"sources"`
	Transforms map[string]*PipelineTransform `json:"transforms"`
	Stages     []*PipeStage                  `json:"stages"`
	Outs       []string                      `json:"outs"`
	Running    bool                          `json:"running"`
	lock       sync.RWMutex
	// runLock is held by the run of the pipe, as the sources and the transforms keep the state of the run in them
	runLock sync.Mutex
}

type PipeRun struct {
	ID   int                    `json:"id"`
	Name string                 `json:"name"`
	Out  map[string]interface{} `json:"out"`
}

type PipeManager struct {
	Pipes          map[string]*Pipe
	Files          []string
	PipeRuns       map[string][]*PipeRun
	fileIndex      int
	pipeRunCounter int
	lock           sync.RWMutex
}

var (
	Manager = (&PipeManager{}).init()
)

// init resets the manager to an empty state, with no pipes, runs or files.
func (pm *PipeManager) init() *PipeManager {
	pm.Pipes = map[string]*Pipe{}
	pm.PipeRuns = map[string][]*PipeRun{}
	pm.Files = nil
	pm.fileIndex = 0
	pm.pipeRunCounter = 0
	return pm
}

// sortedKeys returns the keys of the map in sorted order, so that iterating over them is deterministic.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// NewPipe creates and initialises a pipe with the given name.
func NewPipe(name string) *Pipe {
	pipe := &Pipe{Name: name}
	pipe.Init()
	return pipe
}

// CreatePipe creates an empty pipe with the given name and registers it, replacing any pipe of that name.
func (pm *PipeManager) CreatePipe(name string) {
	pipeline := NewPipe(name)
	pm.lock.Lock()
	pm.Pipes[name] = pipeline
	pm.lock.Unlock()
}

// AddPipe registers a posted pipe, defaulting to one stage of all sources and transforms (sorted by name), and initialises them before the pipe becomes visible.
func (pm *PipeManager) AddPipe(pipe *Pipe) {
	if pipe == nil {
		return
	}
	pipe.Running = false
	// entries without details, and the maps that were not given, are what a pipe that was posted may have
	if pipe.Sources == nil {
		pipe.Sources = map[string]*PipelineSource{}
	}
	for name, s := range pipe.Sources {
		if s == nil {
			delete(pipe.Sources, name)
		}
	}
	for name, t := range pipe.Transforms {
		if t == nil {
			delete(pipe.Transforms, name)
		}
	}
	for _, s := range pipe.Sources {
		if s.Type == SourceJob {
			job.Manager.ClearJobWatchers(s.Spec)
		}
	}
	if len(pipe.Stages) == 0 {
		// the order of the sources and the transforms is the order that they are run in, so it must not depend on the
		// order that the maps give them in
		pipe.Stages = []*PipeStage{{Label: "default"}}
		// the sources and the transforms are known by the keys of their maps, which are their names even when they were
		// given without names
		pipe.Stages[0].Sources = sortedKeys(pipe.Sources)
		pipe.Stages[0].Transforms = sortedKeys(pipe.Transforms)
	}
	// the pipe is made ready before it's visible to anyone else
	pipe.InitSources()
	pipe.InitTransforms()
	pm.lock.Lock()
	pm.Pipes[pipe.Name] = pipe
	pm.lock.Unlock()
}

// ClearPipe clears the sources of the named pipe, or resets the whole manager (all pipes and runs) if no name is given.
func (pm *PipeManager) ClearPipe(name string) {
	if name != "" {
		pm.lock.RLock()
		pipe := pm.Pipes[name]
		pm.lock.RUnlock()
		if pipe != nil {
			pipe.Init()
		}
	} else {
		pm.lock.Lock()
		pm.init()
		pm.lock.Unlock()
	}
}

// RemovePipe deletes the named pipe from the manager.
func (pm *PipeManager) RemovePipe(name string) {
	pm.lock.Lock()
	delete(pm.Pipes, name)
	pm.lock.Unlock()
}

// DumpPipes returns all pipes as JSON text.
func (pm *PipeManager) DumpPipes() string {
	pm.lock.RLock()
	defer pm.lock.RUnlock()
	return util.ToJSONText(pm.Pipes)
}

// AddRun records the output of a run of the pipe under a new run ID.
func (pm *PipeManager) AddRun(pipe *Pipe, out map[string]interface{}) {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	pm.pipeRunCounter++
	pm.PipeRuns[pipe.Name] = append(pm.PipeRuns[pipe.Name], &PipeRun{
		ID:   pm.pipeRunCounter,
		Name: pipe.Name,
		Out:  out,
	})
}

// GetRuns returns the named pipe together with the runs that were recorded for it.
func (pm *PipeManager) GetRuns(pipe string) interface{} {
	pm.lock.RLock()
	defer pm.lock.RUnlock()
	return map[string]interface{}{"pipe": pm.Pipes[pipe], "runs": pm.PipeRuns[pipe]}
}

// AddK8sSource adds a K8s resource source to the named pipe, failing if the pipe doesn't exist.
func (pm *PipeManager) AddK8sSource(pipeName, sourceName, resourceID string) error {
	pm.lock.RLock()
	pipe := pm.Pipes[pipeName]
	pm.lock.RUnlock()
	if pipe == nil {
		return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
	}
	pipe.AddK8sSource(sourceName, resourceID)
	return nil
}

// AddScriptSource adds a script source to the named pipe, registering the script content first if given, and fails if the pipe doesn't exist.
func (pm *PipeManager) AddScriptSource(pipeName, sourceName, scriptContent string) error {
	pm.lock.RLock()
	pipe := pm.Pipes[pipeName]
	pm.lock.RUnlock()
	if pipe == nil {
		return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
	}
	if len(scriptContent) > 0 {
		scripts.Scripts.AddScript(sourceName, scriptContent, false)
	}
	pipe.AddScriptSource(sourceName, sourceName)
	return nil
}

// AddSource adds the source to the named pipe, failing if the source is nil or the pipe doesn't exist.
func (pm *PipeManager) AddSource(pipeName string, source *PipelineSource) error {
	if source == nil {
		return fmt.Errorf("No source")
	}
	pm.lock.RLock()
	pipe := pm.Pipes[pipeName]
	pm.lock.RUnlock()
	if pipe == nil {
		return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
	}
	pipe.AddSource(source)
	return nil
}

// RemoveSource removes the named source from the named pipe, failing if the pipe doesn't exist.
func (pm *PipeManager) RemoveSource(pipeName, sourceName string) error {
	pm.lock.RLock()
	pipe := pm.Pipes[pipeName]
	pm.lock.RUnlock()
	if pipe == nil {
		return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
	}
	pipe.RemoveSource(sourceName)
	return nil
}

// RunPipe runs the named pipe and writes its output to w as YAML or JSON, failing if the pipe doesn't exist.
func (pm *PipeManager) RunPipe(name string, w io.Writer, yaml bool) error {
	pm.lock.RLock()
	pipe := pm.Pipes[name]
	pm.lock.RUnlock()
	if pipe == nil {
		return fmt.Errorf("Pipe [%s] doesn't exist.", name)
	}
	pipe.Run(w, yaml)
	return nil
}

// AddK8sSource adds a K8s resource source with the given name and resource ID to the pipe.
func (pipe *Pipe) AddK8sSource(sourceName, resourceID string) {
	pipe.lock.Lock()
	pipe.initSources()
	pipe.Sources[sourceName] = NewSource(sourceName, SourceK8s, resourceID, pipe)
	pipe.lock.Unlock()
}

// AddScriptSource adds a source with the given name that runs the named script.
func (pipe *Pipe) AddScriptSource(sourceName, scriptName string) {
	pipe.lock.Lock()
	pipe.initSources()
	pipe.Sources[sourceName] = NewSource(sourceName, SourceScript, scriptName, pipe)
	pipe.lock.Unlock()
}

// AddSource adds the source to the pipe, and initialises that source. The other sources and the transforms of the pipe
// are left as they are, as they have the state of what was reported to them in them, and a run may be using them.
func (pipe *Pipe) AddSource(source *PipelineSource) {
	if source == nil {
		return
	}
	pipe.lock.Lock()
	defer pipe.lock.Unlock()
	pipe.initSources()
	pipe.initSource(source.Name, source)
}

// RemoveSource removes the named source from the pipe.
func (pipe *Pipe) RemoveSource(sourceName string) {
	pipe.lock.Lock()
	delete(pipe.Sources, sourceName)
	pipe.lock.Unlock()
}

// Init resets the sources of the pipe to an empty set.
func (pipe *Pipe) Init() {
	pipe.lock.Lock()
	pipe.Sources = map[string]*PipelineSource{}
	pipe.lock.Unlock()
}

// initSources makes the map of the sources, if the pipe doesn't have it, as it doesn't when it was posted without any
// sources. It's called with the lock of the pipe held.
func (pipe *Pipe) initSources() {
	if pipe.Sources == nil {
		pipe.Sources = map[string]*PipelineSource{}
	}
}

// InitSources initialises every source of the pipe under its map key, dropping nil ones.
func (pipe *Pipe) InitSources() {
	for name, source := range pipe.Sources {
		if source == nil {
			delete(pipe.Sources, name)
			continue
		}
		pipe.initSource(name, source)
	}
}

// initSource makes the source ready to be run as a part of the pipe, under the name that the pipe knows it by.
func (pipe *Pipe) initSource(name string, source *PipelineSource) {
	pipe.Sources[name] = source.Init(name, pipe).pipelineSource()
	if source.IsScript() && len(source.GetContent()) > 0 {
		scripts.Scripts.AddScript(source.GetSpec(), source.GetContent(), false)
	}
}

// MarshalJSON reports the pipe as it is at a moment, which is not when the pipe is being changed.
func (pipe *Pipe) MarshalJSON() ([]byte, error) {
	pipe.lock.RLock()
	defer pipe.lock.RUnlock()
	return json.Marshal(struct {
		Name       string                        `json:"name"`
		Sources    map[string]*PipelineSource    `json:"sources"`
		Transforms map[string]*PipelineTransform `json:"transforms"`
		Stages     []*PipeStage                  `json:"stages"`
		Outs       []string                      `json:"outs"`
		Running    bool                          `json:"running"`
	}{pipe.Name, pipe.Sources, pipe.Transforms, pipe.Stages, pipe.Outs, pipe.Running})
}

// InitTransforms names every transform after its map key and initialises it, dropping nil ones.
func (pipe *Pipe) InitTransforms() {
	for name, transform := range pipe.Transforms {
		if transform == nil {
			delete(pipe.Transforms, name)
			continue
		}
		transform.Name = name
		transform.InitTransform()
	}
}

// Trigger runs the pipe, unless it is already running, as it is when the run itself causes the trigger.
func (pipe *Pipe) Trigger() {
	if pipe.runLock.TryLock() {
		defer pipe.runLock.Unlock()
		pipe.run(os.Stdout, true)
	}
}

// Run runs the pipe, after the run that is on, if there is one.
func (pipe *Pipe) Run(w io.Writer, yaml bool) {
	pipe.runLock.Lock()
	defer pipe.runLock.Unlock()
	pipe.run(w, yaml)
}

// run executes the stages in order (delay, sources, then transforms) over a shared workspace, records the selected outs as a run, and writes them to w.
func (pipe *Pipe) run(w io.Writer, yaml bool) {
	pipe.lock.Lock()
	pipe.Running = true
	stages := pipe.Stages
	outs := pipe.Outs
	pipe.lock.Unlock()
	workspace := map[string]interface{}{}
	for _, stage := range stages {
		if stage.Delay.Duration > 0 {
			log.Printf("Pipe: Delaying Stage [%s] by [%s]\n", stage.Label, stage.Delay)
			time.Sleep(stage.Delay.Duration)
		}
		log.Printf("Pipe: Running Stage [%s]\n", stage.Label)
		for _, s := range stage.Sources {
			pipe.lock.Lock()
			source := pipe.Sources[s]
			pipe.lock.Unlock()
			if source != nil {
				log.Printf("Pipe: Fetching Source [%s]\n", s)
				if inputSource := source.GetInputSource(); inputSource != "" {
					// the input is a part of what is reported of the pipe
					pipe.lock.Lock()
					source.SetInput(workspace[inputSource])
					pipe.lock.Unlock()
				}
				source.Generate(workspace)
			}
		}
		if len(stage.Transforms) > 0 {
			for _, t := range stage.Transforms {
				pipe.lock.Lock()
				transform := pipe.Transforms[t]
				pipe.lock.Unlock()
				if transform != nil {
					log.Printf("Pipe: Applying Transform [%s]\n", t)
					// a transform that has no type gives nothing
					if json := transform.Map(workspace); json != nil {
						for k, v := range json.Object() {
							workspace[k] = v
						}
					}
				}
			}
		}
	}
	out := map[string]interface{}{}
	if len(outs) > 0 {
		for _, o := range outs {
			out[o] = workspace[o]
		}
	} else {
		out = workspace
	}

	Manager.AddRun(pipe, out)
	pipe.lock.Lock()
	pipe.Running = false
	pipe.lock.Unlock()

	if w != nil {
		if yaml {
			util.WriteYaml(w, out)
		} else {
			util.WriteJson(w, out)
		}
	}
}
