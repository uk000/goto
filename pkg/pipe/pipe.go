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
  "bufio"
  "bytes"
  "fmt"
  "goto/pkg/util"
  "io"
  "strings"
  "sync"
  "text/template"
)

type PipeStage struct {
  Label     string
  Source    Source
  Transform Transform
}

type Pipe struct {
  Name      string
  Sources   map[string]Source
  Templates map[string]*Template
  lock      sync.RWMutex
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

func NewPipeline(name string) *Pipe {
  return &Pipe{
    Name:      name,
    Sources:   map[string]Source{},
    Templates: map[string]*Template{},
  }
}

func (pm *PipeManager) CreatePipe(name string) {
  pipeline := NewPipeline(name)
  pm.lock.Lock()
  pm.Pipes[name] = pipeline
  pm.lock.Unlock()
}

func (pm *PipeManager) DumpPipes() string {
  pm.lock.RLock()
  defer pm.lock.RUnlock()
  return util.ToJSON(pm.Pipes)
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

func (pm *PipeManager) AddSource(pipeName string, source *PipelineSource) error {
  pm.lock.RLock()
  pipe := pm.Pipes[pipeName]
  pm.lock.RUnlock()
  if pipe == nil {
    return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
  }
  pipe.AddSource(source)
  return nil
}

func (pm *PipeManager) AddTemplate(pipeName, name, text string) error {
  pm.lock.RLock()
  pipe := pm.Pipes[pipeName]
  pm.lock.RUnlock()
  if pipe == nil {
    return fmt.Errorf("Pipe [%s] doesn't exist.", pipeName)
  }
  return pipe.addTemplate(name, text)
}

func (pm *PipeManager) StoreTemplates(pipe string, content []byte) map[string]string {
  result := map[string]string{}
  pm.lock.Lock()
  pm.fileIndex++
  index := pm.fileIndex
  pm.lock.Unlock()
  fileName := fmt.Sprintf("template-%d.txt", index)
  if path, err := util.StoreFile("", fileName, content); err == nil {
    pm.lock.Lock()
    pm.Files = append(pm.Files, path)
    pm.lock.Unlock()
    result[fileName] = fmt.Sprintf("Templates stored at path [%s]", path)
  } else {
    result[fileName] = err.Error()
  }
  scanner := bufio.NewScanner(bytes.NewReader(content))
  for scanner.Scan() {
    line := scanner.Text()
    if pieces := strings.Split(line, "="); len(pieces) == 2 {
      templateName := pieces[0]
      if err := pm.AddTemplate(pipe, templateName, pieces[1]); err == nil {
        result[templateName] = "Added"
      } else {
        result[templateName] = err.Error()
      }
    }
  }
  return result
}

func (pm *PipeManager) RunPipe(name string, w io.Writer) error {
  pm.lock.RLock()
  pipe := pm.Pipes[name]
  pm.lock.RUnlock()
  if pipe == nil {
    return fmt.Errorf("Pipe [%s] doesn't exist.", name)
  }
  pipe.Run(w)
  return nil
}

func (pipe *Pipe) AddK8sSource(sourceName, resourceID string) {
  pipe.lock.Lock()
  pipe.Sources[sourceName] = NewSource(sourceName, SourceK8s, resourceID)
  pipe.lock.Unlock()
}

func (pipe *Pipe) AddSource(source *PipelineSource) {
  pipe.lock.Lock()
  pipe.Sources[source.Name] = source
  pipe.lock.Unlock()
}

func (pipe *Pipe) addTemplate(name, text string) error {
  if t, err := template.New(name).Parse(text); err == nil {
    pipe.lock.Lock()
    pipe.Templates[name] = &Template{Code: text, template: t}
    pipe.lock.Unlock()
    return nil
  } else {
    return err
  }
}

func (pipe *Pipe) Run(w io.Writer) {
  for name, source := range pipe.Sources {
    fmt.Printf("Running Source [%s]\n", name)
    out := source.Out()
    fmt.Printf("Source [%s] Output [%+v]\n", name, out)
    w.Write([]byte(fmt.Sprintf("%+v", out)))
  }
}
