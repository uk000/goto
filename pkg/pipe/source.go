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
  "encoding/json"
  "fmt"
  "goto/pkg/invocation"
  "goto/pkg/job"
  "goto/pkg/k8s"
  "goto/pkg/script"
  "goto/pkg/util"
  "log"
  "strconv"
  "strings"
  "time"
)

type SourceType string

const (
  SourceK8s        SourceType = "K8s"
  SourceK8sPodExec SourceType = "K8sPodExec"
  SourceJob        SourceType = "Job"
  SourceScript     SourceType = "Script"
)

type Source interface {
  Init(pipe *Pipe)
  Generate(map[string]interface{})
  IsK8s() bool
  IsK8sPodExec() bool
  IsJob() bool
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
  Source
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
  *PipelineSource
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

func (s *AbstractSource) Init(*Pipe) {}

func (s *AbstractSource) init() {}

func (s *AbstractSource) Generate(workspace map[string]interface{}) {
}

func (s *AbstractSource) IsK8s() bool {
  return false
}

func (s *AbstractSource) IsK8sPodExec() bool {
  return false
}

func (s *AbstractSource) IsJob() bool {
  return false
}

func (s *AbstractSource) IsScript() bool {
  return false
}

func (s *AbstractSource) GetName() string {
  return s.Name
}

func (s *AbstractSource) GetSpec() string {
  return s.Spec
}

func (s *AbstractSource) GetContent() string {
  return s.Content
}

func (s *AbstractSource) GetInput() interface{} {
  return s.Input
}

func (s *AbstractSource) GetInputSource() string {
  return s.InputSource
}

func (s *AbstractSource) SetInput(input interface{}) {
  s.Input = input
}

func (s *AbstractSource) generate() interface{} {
  return nil
}

func (s *AbstractSource) pipelineSource() *PipelineSource {
  return s.PipelineSource
}

func NewSource(name string, sourceType SourceType, spec string, pipe *Pipe) *PipelineSource {
  ps := &PipelineSource{Type: sourceType}
  return ps.Init(name, pipe).pipelineSource()
}

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
  }
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
    if input, ok := ps.Input.(string); !ok {
      for _, filler := range util.GetFillers(input) {
        ps.inputFillers = append(ps.inputFillers, filler)
      }
    }
  }
  if ps.Watch && pipe != nil {
    ps.triggerPipe = pipe
  }
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
    if input, ok := ps.finalInput.(string); !ok {
      for _, filler := range ps.inputFillers {
        input = util.FillFrom(input, filler, workspace)
      }
      ps.finalInput = input
    }
  }
}

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
    if s, ok := result.(string); ok {
      result = util.FromJSONText(s).Value()
    } else {
      result = util.FromJSONText(fmt.Sprint(result)).Value()
    }
  } else if ps.ParseNumber {
    if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(result))); err == nil {
      result = n
    }
  }
  workspace[ps.Name] = result
}

func (k *K8sSource) watch() {
  if k.triggerPipe != nil && !k.watching {
    k.watching = true
    k8s.WatchResource(k.finalSpec, func(id string) {
      log.Printf("K8s Source [%s] triggered pipe [%s]\n", id, k.triggerPipe.Name)
      k.triggerPipe.Trigger()
    })
  }
}

func (k *K8sSource) generate() interface{} {
  k.watch()
  if j := k8s.GetResourceByID(k.finalSpec); j != nil {
    return j.Value()
  }
  return nil
}

func (k *K8sSource) IsK8s() bool {
  return true
}

func (k *K8sPodExecSource) prepareCommand() string {
  cmd := ""
  if k.finalInput != nil {
    cmd = "echo '" + fmt.Sprint(k.finalInput) + "' | xargs "
  }
  cmd += k.finalContent
  return cmd
}

func (k *K8sPodExecSource) generate() interface{} {
  podID := strings.Split(k.finalSpec, "/")
  command := k.prepareCommand()
  if out, err := k8s.PodExec(podID[0], podID[1], podID[2], command); err == nil {
    return out
  } else {
    log.Printf("Pipe: Error executing command [%+v] on pod [%+v]\n", command, k.finalSpec)
  }
  return nil
}

func (k *K8sPodExecSource) IsK8sPodExec() bool {
  return true
}

func (j *JobSource) watch() {
  if j.triggerPipe != nil && !j.watching {
    j.watching = true
    job.Manager.AddJobWatcher(j.finalSpec, j.Name, func(job string, runId int, results []*job.JobResult) {
      log.Printf("Job Source [%s] triggered pipe [%s]\n", j.Name, j.triggerPipe.Name)
      j.triggerPipe.Trigger()
    })
  }
}

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

func (j *JobSource) IsJob() bool {
  return true
}

func (s *ScriptSource) generate() interface{} {
  r := strings.NewReader(fmt.Sprint(s.finalInput))
  var buff bytes.Buffer
  w := bufio.NewWriter(&buff)
  script.Scripts.RunScript(s.finalSpec, r, w)
  w.Flush()
  return buff.String()
}

func (s *ScriptSource) IsScript() bool {
  return true
}
