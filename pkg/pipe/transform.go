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
  "goto/pkg/util"
  "regexp"
  "text/template"
)

const (
  TransformJSONPath TransformType = iota
  TransformJQ       TransformType = iota
  TransformTemplate TransformType = iota
  TransformRegex    TransformType = iota
)

type TransformType int

type Transform interface {
  ID() string
  Map(interface{}) interface{}
}

type Template struct {
  Name     string `json:"name"`
  Code     string `json:"code"`
  template *template.Template
}

type PipelineTransform struct {
  Name     string        `json:"name"`
  Type     TransformType `json:"type"`
  Spec     string        `json:"spec"`
  jp       *util.JSONPath
  jq       *util.JQ
  template *template.Template
  regex    *regexp.Regexp
}

func (p *PipeManager) addTransform(name, source, query string) {
  // if query == "" {
  //   query = "."
  // }
  // var jq *gojq.Code
  // if q, err := gojq.Parse(query); err == nil {
  //   if code, err := gojq.Compile(q); err == nil {
  //     jq = code
  //   }
  // }
  // p.lock.Lock()
  // defer p.lock.Unlock()
}
