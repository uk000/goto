/**
 * Copyright 2021 uk
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
  "fmt"
  "goto/pkg/job"
  "goto/pkg/script"
  "goto/pkg/util"
  "io"
  "log"
  "os"
  "sync"
  "time"
)

type PipeStage struct {
  Label      string        `json:"label"`
  Sources    []string      `json:"sources"`
  Transforms []string      `json:"transforms"`
  Delay      util.Duration `json:"delay"`
}

type Pipe struct {
  Name       string                        `json:"name"`
  Sources    map[string]*PipelineSource    `json:"sources"`
  Transforms map[string]*PipelineTransform `json:"transforms"`
  Stages     []*PipeStage                  `json:"stages"`
  Out        []string                      `json:"out"`
  Running    bool                          `json:"running"`
  lock       sync.RWMutex
}

type PipeManager struct {
  Pipes     map[string]*Pipe
  Files     []string
  fileIndex int
  lock      sync.RWMutex
}

var (
  Manager = &PipeManager{
    Pipes: map[string]*Pipe{},
  }
)

func NewPipe(name string) *Pipe {
  pipe := &Pipe{Name: name}
  pipe.Init()
  return pipe
}

func (pm *PipeManager) CreatePipe(name string) {
  pipeline := NewPipe(name)
  pm.lock.Lock()
  pm.Pipes[name] = pipeline
  pm.lock.Unlock()
}

func (pm *PipeManager) AddPipe(pipe *Pipe) {
  pipe.Running = false
  for _, s := range pipe.Sources {
    if s.Type == SourceJob {
      job.Manager.ClearJobWatchers(s.Spec)
    }
  }
  pm.lock.Lock()
  pm.Pipes[pipe.Name] = pipe
  pm.lock.Unlock()
  pipe.InitSources()
  pipe.InitTransforms()
}

func (pm *PipeManager) ClearPipe(name string) {
  pm.lock.RLock()
  pipe := pm.Pipes[name]
  pm.lock.RUnlock()
  if pipe != nil {
    pipe.Init()
  }
}

func (pm *PipeManager) RemovePipe(name string) {
  pm.lock.RLock()
  delete(pm.Pipes, name)
  pm.lock.RUnlock()
}

func (pm *PipeManager) DumpPipes() string {
  pm.lock.RLock()
  defer pm.lock.RUnlock()
  return util.ToJSONText(pm.Pipes)
}

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

func (pm *PipeManager) AddScriptSource(pipeName, sourceName, scriptContent string) error {
  pm.lock.RLock()
  pipe := pm.Pipes[pipeName]
  pm.lock.RUnlock()
  if pipe == nil {
    return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
  }
  if len(scriptContent) > 0 {
    script.Scripts.AddScript(sourceName, scriptContent)
  }
  pipe.AddScriptSource(sourceName, sourceName)
  return nil
}

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

func (pipe *Pipe) AddK8sSource(sourceName, resourceID string) {
  pipe.lock.Lock()
  pipe.Sources[sourceName] = NewSource(sourceName, SourceK8s, resourceID, pipe)
  pipe.lock.Unlock()
}

func (pipe *Pipe) AddScriptSource(sourceName, scriptName string) {
  pipe.lock.Lock()
  pipe.Sources[sourceName] = NewSource(sourceName, SourceScript, scriptName, pipe)
  pipe.lock.Unlock()
}

func (pipe *Pipe) AddSource(source *PipelineSource) {
  pipe.lock.Lock()
  pipe.Sources[source.Name] = source
  pipe.InitSources()
  pipe.InitTransforms()
  pipe.lock.Unlock()
}

func (pipe *Pipe) RemoveSource(sourceName string) {
  pipe.lock.Lock()
  delete(pipe.Sources, sourceName)
  pipe.lock.Unlock()
}

func (pipe *Pipe) Init() {
  pipe.lock.Lock()
  pipe.Sources = map[string]*PipelineSource{}
  pipe.lock.Unlock()
}

func (pipe *Pipe) InitSources() {
  for name, source := range pipe.Sources {
    pipe.Sources[name] = source.Init(name, pipe).pipelineSource()
    if source.IsScript() && len(source.GetContent()) > 0 {
      script.Scripts.AddScript(source.GetSpec(), source.GetContent())
    }
  }
}

func (pipe *Pipe) InitTransforms() {
  for name, transform := range pipe.Transforms {
    transform.Name = name
    transform.InitTransform()
  }
}

func (pipe *Pipe) Trigger() {
  pipe.lock.RLock()
  running := pipe.Running
  pipe.lock.RUnlock()
  if !running {
    pipe.Run(os.Stdout, true)
  }
}

func (pipe *Pipe) Run(w io.Writer, yaml bool) {
  pipe.lock.Lock()
  pipe.Running = true
  pipe.lock.Unlock()
  workspace := map[string]interface{}{}
  if len(pipe.Stages) > 0 {
    for _, stage := range pipe.Stages {
      if stage.Delay.Duration > 0 {
        log.Printf("Pipe: Delaying Stage [%s] by [%s]\n", stage.Label, stage.Delay)
        time.Sleep(stage.Delay.Duration)
      }
      log.Printf("Pipe: Running Stage [%s]\n", stage.Label)
      for _, s := range stage.Sources {
        if source := pipe.Sources[s]; source != nil {
          log.Printf("Pipe: Pulling from Source [%s]\n", s)
          if source.GetInputSource() != "" {
            source.SetInput(workspace[source.GetInputSource()])
          }
          source.Generate(workspace)
        }
      }
      if len(stage.Transforms) > 0 {
        for _, t := range stage.Transforms {
          if transform := pipe.Transforms[t]; transform != nil {
            log.Printf("Pipe: Applying Transform [%s]\n", t)
            json := transform.Map(workspace)
            for k, v := range json.Object() {
              workspace[k] = v
            }
          }
        }
      }
    }
  } else {
    for _, source := range pipe.Sources {
      log.Printf("Pipe: Pulling from Source [%s]\n", source.GetName())
      if source.GetInputSource() != "" {
        source.SetInput(workspace[source.GetInputSource()])
      }
      source.Generate(workspace)
    }
    if len(pipe.Transforms) > 0 {
      for _, transform := range pipe.Transforms {
        log.Printf("Pipe: Applying Transform [%s]\n", transform.Name)
        workspace[transform.Name] = transform.Map(workspace)
      }
    }
  }
  out := map[string]interface{}{}
  if len(pipe.Out) > 0 {
    for _, o := range pipe.Out {
      out[o] = workspace[o]
    }
  } else {
    out = workspace
  }

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
