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

package util

import (
	"bytes"
	"fmt"
	"goto/pkg/types"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"

	"github.com/itchyny/gojq"
	"k8s.io/client-go/util/jsonpath"
)

type JSONPath struct {
	Paths     map[string]*jsonpath.JSONPath `json:"-"`
	TextPaths map[string]string             `json:"paths"`
	counter   uint64
	lock      sync.Mutex
}

type JQ struct {
	Queries     map[string]*gojq.Code
	TextQueries map[string]string
	counter     uint64
	lock        sync.Mutex
}

type JSONPathWriter struct {
	results    map[string][]interface{}
	currentKey string
}

type JSONPathPair types.Pair[*JSONPath, *JSONPath]

// NewJQ creates an empty JQ query set.
func NewJQ() *JQ {
	return &JQ{Queries: map[string]*gojq.Code{}, TextQueries: map[string]string{}}
}

// Parse parses and compiles jq queries given as "key=query" or just "query" (auto-numbered key), logging and skipping invalid ones.
func (jq *JQ) Parse(queries []string) *JQ {
	if len(queries) == 0 || queries[0] == "" {
		return jq
	}
	for _, query := range queries {
		pieces := strings.Split(query, "=")
		key := ""
		val := ""
		if len(pieces) == 2 {
			key = pieces[0]
			val = pieces[1]
		} else {
			atomic.AddUint64(&jq.counter, 1)
			key = fmt.Sprint(jq.counter)
			val = pieces[0]
		}
		if q, err := gojq.Parse(val); err == nil {
			if code, err := gojq.Compile(q); err == nil {
				jq.lock.Lock()
				jq.Queries[key] = code
				jq.TextQueries[key] = val
				jq.lock.Unlock()
			} else {
				fmt.Printf("Failed to compile jq query [%s] with error: %s\n", query, err.Error())
			}
		} else {
			fmt.Printf("Failed to parse jq query [%s] with error: %s\n", query, err.Error())
		}
	}
	return jq
}

// Apply runs all queries against the JSON value and returns the results keyed by query id, with single results unwrapped.
func (jq *JQ) Apply(j JSON) JSON {
	if j == nil {
		return nil
	}
	val := j.Value()
	results := map[string][]interface{}{}
	for id, q := range jq.Queries {
		iter := q.Run(val)
		for {
			if value, ok := iter.Next(); ok {
				if err, ok := value.(error); ok {
					fmt.Printf("JQ: Failed to apply query [%s] with error: %s\n", id, err.Error())
				} else {
					switch v := value.(type) {
					case JSONValue:
						results[id] = append(results[id], v.Value())
					case *JSONValue:
						results[id] = append(results[id], v.Value())
					default:
						results[id] = append(results[id], v)
					}
				}
			} else {
				break
			}
		}
	}
	out := map[string]interface{}{}
	for k, v := range results {
		if len(v) == 1 {
			out[k] = v[0]
		} else {
			out[k] = v
		}
	}
	return JSONFromJSON(out)
}

// IsEmpty reports whether no queries are registered.
func (jq *JQ) IsEmpty() bool {
	return len(jq.Queries) == 0
}

// NewJSONPath creates an empty JSONPath set.
func NewJSONPath() *JSONPath {
	return &JSONPath{TextPaths: map[string]string{}, Paths: map[string]*jsonpath.JSONPath{}}
}

// Parse2 registers one JSONPath expression given as "key=path" or just "path" (auto-numbered key).
func (jp *JSONPath) Parse2(path string) {
	pieces := strings.Split(path, "=")
	key := ""
	val := ""
	if len(pieces) == 2 {
		key = pieces[0]
		val = pieces[1]
	} else {
		atomic.AddUint64(&jp.counter, 1)
		key = fmt.Sprint(jp.counter)
		val = pieces[0]
	}
	j := jsonpath.New(key)
	j.Parse(val)
	jp.TextPaths[key] = val
	jp.Paths[key] = j
}

// Parse registers each of the given JSONPath expressions and returns the receiver.
func (jp *JSONPath) Parse(paths []string) *JSONPath {
	if len(paths) == 0 || paths[0] == "" {
		return jp
	}
	for _, path := range paths {
		jp.Parse2(path)
	}
	return jp
}

// Apply runs all JSONPath expressions against the JSON value and returns the results keyed by path id, with single results unwrapped.
func (jp *JSONPath) Apply(j JSON) JSON {
	if j == nil {
		return nil
	}
	resultsWriter := &JSONPathWriter{results: map[string][]interface{}{}}

	data := j.Value()
	for k, jsonPath := range jp.Paths {
		resultsWriter.currentKey = k
		jsonPath.EnableJSONOutput(true)
		if err := jsonPath.Execute(resultsWriter, data); err != nil {
			fmt.Printf("Failed to find results for path [%s] with error: %s\n", jp.TextPaths[k], err.Error())
		}
	}
	out := map[string]interface{}{}
	for k, v := range resultsWriter.results {
		if len(v) == 1 {
			out[k] = v[0]
		} else {
			out[k] = v
		}
	}
	return JSONFromJSON(out)
}

// FindResults evaluates the paths against json, returning the first match of each as a string capture and whether every path matched.
func (jp *JSONPath) FindResults(json map[string]any) (captures map[string]string, allMatched bool) {
	captures = map[string]string{}
	allMatched = true
	for key, p := range jp.Paths {
		if matches, err := p.FindResults(json); err == nil && len(matches) > 0 {
			if key != "" && len(matches[0]) > 0 {
				captures[key] = fmt.Sprintf("%v", matches[0][0].Interface())
			}
		} else {
			allMatched = false
		}
	}
	return
}

// IsEmpty reports whether no paths are registered.
func (jp *JSONPath) IsEmpty() bool {
	return len(jp.Paths) == 0
}

// Write records each written chunk as a string result under the current path key.
func (jpw *JSONPathWriter) Write(p []byte) (n int, err error) {
	jpw.results[jpw.currentKey] = append(jpw.results[jpw.currentKey], string(p))
	return len(p), nil
}

// ExecuteTemplates executes each template against the value and returns the outputs as a JSON array.
func (j *JSONValue) ExecuteTemplates(templates []*template.Template) JSON {
	data := []interface{}{}
	for _, t := range templates {
		data = append(data, j.ExecuteTemplate(t))
	}
	return JSONFromJSON(data)
}

// ExecuteTemplate executes the template with the value as its data and returns the output, or empty on error.
func (j *JSONValue) ExecuteTemplate(t *template.Template) string {
	buf := &bytes.Buffer{}
	if err := t.Execute(buf, j.Value()); err == nil {
		return buf.String()
	} else {
		fmt.Printf("Failed to execute Template [%s] with error: %s\n", t.Name(), err.Error())
	}
	return ""
}
