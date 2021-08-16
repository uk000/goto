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
  "goto/pkg/job"
  "goto/pkg/k8s"
  "goto/pkg/script"
  "goto/pkg/util"
)

type SourceType string

const (
  SourceK8s        SourceType = "K8s"
  SourceK8sPodExec SourceType = "K8sPodExec"
  SourceJob        SourceType = "Job"
  SourceScript     SourceType = "Script"
)

type Source interface {
  Out() string
  Value() interface{}
  JSON() util.JSON
  Text() string
  GetSpec() string
  SetSpec(string)
}

type AbstractSource struct {
  Spec string `json:"spec"`
}

type PipelineSource struct {
  AbstractSource `json:",inline"`
  Name           string     `json:"name"`
  Type           SourceType `json:"type"`
  source         Source
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

func (s *AbstractSource) Out() string {
  return ""
}

func (s *AbstractSource) Value() interface{} {
  return nil
}

func (s *AbstractSource) JSON() util.JSON {
  return nil
}

func (s *AbstractSource) Text() string {
  return ""
}

func (s *AbstractSource) GetSpec() string {
  return s.Spec
}

func (s *AbstractSource) SetSpec(spec string) {
  s.Spec = spec
}

func NewSource(name string, sourceType SourceType, spec string) Source {
  ps := &PipelineSource{Name: name, Type: sourceType}
  switch sourceType {
  case SourceK8s:
    ps.source = &K8sSource{}
  case SourceK8sPodExec:
    ps.source = &K8sPodExecSource{}
  case SourceJob:
    ps.source = &JobSource{}
  case SourceScript:
    ps.source = &ScriptSource{}
  }
  ps.source.SetSpec(spec)
  return ps
}

func (s *PipelineSource) MarshalJSON() ([]byte, error) {
  data := map[string]interface{}{}
  data["type"] = s.Type
  if s.source != nil {
    data["spec"] = s.source.GetSpec()
  }
  return json.Marshal(data)
}

func (s *PipelineSource) Out() string {
  out := s.JSON().ToJSON()
  if out == "" {
    out = s.Text()
  }
  return out
}

func (s *PipelineSource) Value() interface{} {
  var out interface{} = s.JSON()
  if out == nil {
    out = s.Text()
  }
  return out
}

func (s *PipelineSource) JSON() util.JSON {
  if s.source != nil {
    return s.source.JSON()
  }
  return nil
}

func (s *PipelineSource) Text() string {
  if s.source != nil {
    return s.source.Text()
  }
  return ""
}

func (k *K8sSource) JSON() util.JSON {
  return k8s.GetResourceByID(k.Spec)
}

func (j *JobSource) JSON() util.JSON {
  return util.FromJSON(job.Jobs.GetJobLatestResults(j.Spec))
}

func (s *ScriptSource) Text() string {
  var buff bytes.Buffer
  w := bufio.NewWriter(&buff)
  script.Scripts.RunScript(s.Spec, w)
  w.Flush()
  return buff.String()
}
