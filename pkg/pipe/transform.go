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
	"goto/pkg/util"
	"log"
	"regexp"
	"strings"
	"text/template"
)

type TransformType string

const (
	TransformJSONPath TransformType = "JSONPath"
	TransformJQ       TransformType = "JQ"
	TransformTemplate TransformType = "Template"
	TransformRegex    TransformType = "Regex"
)

type Transform interface {
	Out() util.JSON
	IsJSONPath() bool
	IsJQ() bool
	IsTemplate() bool
	IsRegex() bool
	GetSpec() string
	SetSpec(string)
	SetInput(interface{})
	Map(interface{}) util.JSON
}

type AbstractTransform struct {
	Name  string `json:"name"`
	Spec  string `json:"spec,omitempty"`
	input interface{}
}

type PipelineTransform struct {
	AbstractTransform `json:",inline"`
	Type              TransformType `json:"type"`
	transform         Transform
}

type JSONPathTransform struct {
	AbstractTransform
	jp *util.JSONPath
}

type JQTransform struct {
	AbstractTransform
	jq *util.JQ
}

type TemplateTransform struct {
	AbstractTransform
	template *template.Template
}

type RegexTransform struct {
	AbstractTransform
	regexp *regexp.Regexp
}

func (a *AbstractTransform) Out() util.JSON {
	return nil
}

func (a *AbstractTransform) IsJSONPath() bool {
	return false
}

func (a *AbstractTransform) IsJQ() bool {
	return false
}

func (a *AbstractTransform) IsTemplate() bool {
	return false
}

func (a *AbstractTransform) IsRegex() bool {
	return false
}

func (a *AbstractTransform) GetSpec() string {
	return a.Spec
}

func (a *AbstractTransform) SetSpec(spec string) {
	a.Spec = spec
}

func (a *AbstractTransform) SetInput(i interface{}) {
	a.input = i
}

func (a *AbstractTransform) Map(in interface{}) util.JSON {
	a.SetInput(in)
	return a.Out()
}

func NewTransform(name string, transformType TransformType, spec string) *PipelineTransform {
	pt := &PipelineTransform{AbstractTransform: AbstractTransform{Name: name, Spec: spec}, Type: transformType}
	pt.InitTransform()
	return pt
}

func (pt *PipelineTransform) InitTransform() {
	switch pt.Type {
	case TransformJSONPath:
		pt.transform = &JSONPathTransform{AbstractTransform: pt.AbstractTransform}
	case TransformJQ:
		pt.transform = &JQTransform{AbstractTransform: pt.AbstractTransform}
	case TransformTemplate:
		pt.transform = &TemplateTransform{AbstractTransform: pt.AbstractTransform}
	case TransformRegex:
		pt.transform = &RegexTransform{AbstractTransform: pt.AbstractTransform}
	default:
		// a transform of an unknown type does nothing
		pt.transform = nil
		return
	}
	pt.transform.SetSpec(pt.Spec)
}

func (pt *PipelineTransform) MarshalOut() ([]byte, error) {
	data := map[string]interface{}{}
	data["type"] = pt.Type
	if pt.transform != nil {
		data["spec"] = pt.transform.GetSpec()
	}
	return json.Marshal(data)
}

func (pt *PipelineTransform) Out() util.JSON {
	if pt.transform != nil {
		return pt.transform.Out()
	}
	return nil
}

func (pt *PipelineTransform) IsJSONPath() bool {
	if pt.transform != nil {
		return pt.transform.IsJSONPath()
	}
	return false
}

func (pt *PipelineTransform) IsJQ() bool {
	if pt.transform != nil {
		return pt.transform.IsJQ()
	}
	return false
}

func (pt *PipelineTransform) IsTemplate() bool {
	if pt.transform != nil {
		return pt.transform.IsTemplate()
	}
	return false
}

func (pt *PipelineTransform) IsRegex() bool {
	if pt.transform != nil {
		return pt.transform.IsRegex()
	}
	return false
}

func (pt *PipelineTransform) Map(in interface{}) util.JSON {
	if pt.transform != nil {
		pt.transform.SetInput(in)
		return pt.transform.Out()
	}
	return nil
}

func (j *JSONPathTransform) SetSpec(spec string) {
	spec = strings.Trim(spec, " \t\n")
	j.Spec = spec
	j.jp = util.NewJSONPath()
	if !strings.HasPrefix(spec, "{") {
		spec = "{" + spec + "}"
	}
	j.jp.Parse([]string{j.Name + "=" + spec})
}

func (j *JSONPathTransform) Out() util.JSON {
	return j.jp.Apply(util.JSONFromJSON(j.input))
}

func (j *JSONPathTransform) IsJSONPath() bool {
	return true
}

func (j *JQTransform) SetSpec(spec string) {
	j.Spec = spec
	j.jq = util.NewJQ()
	j.jq.Parse([]string{j.Name + "=" + spec})
}

func (j *JQTransform) Out() util.JSON {
	return j.jq.Apply(util.JSONFromJSON(j.input))
}

func (j *JQTransform) IsJQ() bool {
	return true
}

func (t *TemplateTransform) SetSpec(spec string) {
	t.Spec = spec
	if tpl, err := template.New(t.Name).Parse(spec); err == nil {
		t.template = tpl
	}
}

func (t *TemplateTransform) Out() util.JSON {
	// the template is missing when the spec could not be parsed
	if t.template == nil {
		return util.JSONFromJSON(map[string]interface{}{t.Name: ""})
	}
	return util.JSONFromJSON(map[string]interface{}{
		t.Name: util.JSONFromJSON(t.input).ExecuteTemplate(t.template),
	})
}

func (j *TemplateTransform) IsTemplate() bool {
	return true
}

func (r *RegexTransform) SetSpec(spec string) {
	r.Spec = spec
	re, err := regexp.Compile(spec)
	if err != nil {
		log.Printf("Pipe: Transform [%s] has an invalid regex [%s]: %s\n", r.Name, spec, err.Error())
		re = nil
	}
	r.regexp = re
}

func (r *RegexTransform) Out() util.JSON {
	var matches []string
	// a regex that couldn't be compiled finds nothing
	if r.regexp != nil {
		matches = r.regexp.FindAllString(fmt.Sprint(r.input), -1)
	}
	return util.JSONFromJSON(map[string]interface{}{
		r.Name: matches,
	})
}

func (r *RegexTransform) IsRegex() bool {
	return true
}
