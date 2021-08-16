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

package util

import (
  "bytes"
  "encoding/gob"
  "encoding/json"
  "fmt"
  "reflect"
  "regexp"
  "strconv"
  "strings"
  "text/template"
  "time"

  "github.com/itchyny/gojq"
  "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
  "k8s.io/client-go/util/jsonpath"
  "sigs.k8s.io/yaml"
)

type JSONPath struct {
  Paths     map[string]*jsonpath.JSONPath
  TextPaths map[string]string
}

type JQ struct {
  Queries map[string]*gojq.Code
  TextQueries map[string]string
}

type JSONPatterns struct {
  patterns map[int][]string
}

type JSONMap map[string]JSON

type JSONObject struct {
  AbstractJSON
  JSONMap
}

type JSON interface {
  Value() interface{}
  Object() map[string]interface{}
  Array() []interface{}
  JSONObject() *JSONObject
  JSONArray() []JSON

  ParseJSON(text string)
  ParseYAML(y string)
  Store(i interface{})
  ToJSON() string
  ToYAML() string

  IsEmpty() bool
  IsObject() bool
  IsArray() bool

  FindPath(path string) *Value
  FindPaths(paths []string) map[string]*Value
  FindTransformPath(path string, join, replace, push bool) JSONField
  Transform(ts []*JSONTransform, source JSON) bool
  TransformPatterns(text string) string

  View(fields ...string) map[string]interface{}

  At() *time.Time
}

type JSONValue struct {
  jsonMap map[string]interface{}
  jsonArr []interface{}
  at      time.Time
  jsonPatterns   *JSONPatterns
}

type JSONTransform struct {
  Source           string      `json:"source"`
  Target           string      `json:"target"`
  IfContains       string      `json:"ifContains"`
  IfNotContains    string      `json:"ifNotContains"`
  Mode             string      `json:"mode"`
  Value            interface{} `json:"value"`
  replace          bool
  join             bool
  push             bool
  cotainsRegexp    *regexp.Regexp
  notCotainsRegexp *regexp.Regexp
}

type Value struct {
  Value  interface{}
  IsJSON bool
}

type JSONField interface {
  Read() interface{}
  Update(value interface{})
  Exists() bool
}

type JSONArrayField struct {
  path        string
  arrayField  []interface{}
  grandParent JSONField
  index       int
  join        bool
  replace     bool
  push        bool
  exists      bool
}

type JSONMapField struct {
  path     string
  mapField map[string]interface{}
  key      string
  join     bool
  replace  bool
  push     bool
  exists   bool
}

func NewJSON() JSON {
  return &JSONValue{
    at: time.Now(),
  }
}

func FromJSONText(text string) JSON {
  json := NewJSON()
  json.ParseJSON(text)
  return json
}

func FromJSON(j interface{}) JSON {
  json := NewJSON()
  json.Store(j)
  return json
}

func FromYAML(y string) JSON {
  json := NewJSON()
  json.ParseYAML(y)
  return json
}

func FromObject(o interface{}) JSON {
  return FromJSONText(ToJSON(o))
}

func ToJSONValue(v interface{}) *JSONValue {
  jsonValue := &JSONValue{}
  if vv, ok := v.(map[string]interface{}); ok {
    jsonValue.jsonMap = vv
  } else if vv, ok := v.([]interface{}); ok {
    jsonValue.jsonArr = vv
  }
  return jsonValue
}

func (j JSONObject) Value() interface{} {
  return j
}

func (j JSONObject) Object() map[string]interface{} {
  return j.Value().(map[string]interface{})
}

func (j JSONObject) Array() []interface{} {
  return nil
}

func (j JSONObject) JSONObject() JSONObject {
  return j
}

func (j *JSONObject) JSONArray() []JSON {
  return nil
}

func (j *JSONValue) At() *time.Time {
  return &j.at
}

func (j *JSONValue) Value() interface{} {
  if j.jsonMap != nil {
    return j.jsonMap
  }
  return j.jsonArr
}

func (j *JSONValue) Object() map[string]interface{} {
  return j.jsonMap
}

func (j *JSONValue) JSONObject() *JSONObject {
  jsonObject := JSONMap{}
  for k, v := range j.jsonMap {
    jsonObject[k] = ToJSONValue(v)
  }
  return &JSONObject{JSONMap: jsonObject}
}

func (j *JSONValue) Array() []interface{} {
  return j.jsonArr
}

func (j *JSONValue) JSONArray() []JSON {
  jsonArr := []JSON{}
  for _, v := range j.jsonArr {
    jsonArr = append(jsonArr, ToJSONValue(v))
  }
  return jsonArr
}

func (j *JSONValue) ParseJSON(text string) {
  var o interface{}
  if err := json.Unmarshal([]byte(text), &o); err == nil {
    j.Store(o)
  } else {
    fmt.Printf("Failed to parse json with error: %s\n", err.Error())
  }
}

func (j *JSONValue) ParseYAML(y string) {
  if o, err := yaml.YAMLToJSON([]byte(y)); err == nil {
    j.ParseJSON(string(o))
  } else {
    fmt.Printf("Failed to parse yaml with error: %s\n", err.Error())
  }
}

func (j *JSONValue) ToJSON() string {
  if output, err := json.Marshal(j.Value()); err == nil {
    return string(output)
  } else {
    fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
  }
  return ""
}

func (j *JSONValue) ToYAML() string {
  if b, err := yaml.Marshal(j.Value()); err == nil {
    return string(b)
  } else {
    fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
  }
  return ""
}

func (j *JSONValue) Store(i interface{}) {
  i = Clone(i)
  switch v := i.(type) {
  case map[string]interface{}:
    j.jsonMap = v
  case map[string][]interface{}:
    j.jsonMap = map[string]interface{}{}
    for key, list := range v {
      j.jsonMap[key] = list
    }
  case []interface{}:
    j.jsonArr = v
  case *unstructured.UnstructuredList:
    j.ParseJSON(ToJSON(v))
  case *unstructured.Unstructured:
    j.jsonMap = v.Object
  }
}

func (j *JSONValue) IsEmpty() bool {
  return j.jsonArr == nil && j.jsonMap == nil
}

func (j *JSONValue) IsObject() bool {
  return j.jsonMap != nil
}

func (j *JSONValue) IsArray() bool {
  return j.jsonArr != nil
}

func (j *JSONValue) ExecuteTemplates(templates []*template.Template) []interface{} {
  data := []interface{}{}
  for _, t := range templates {
    data = append(data, j.ExecuteTemplate(t))
  }
  return data
}

func (j *JSONValue) ExecuteTemplate(t *template.Template) interface{} {
  buf := &bytes.Buffer{}
  if err := t.Execute(buf, j.Value()); err == nil {
    return buf
  } else {
    fmt.Println(err.Error())
  }
  return nil
}

func (j *JSONValue) View(paths ...string) map[string]interface{} {
  view := map[string]interface{}{}
  values := j.FindPaths(paths)
  for k, v := range values {
    view[k] = v.Value
  }
  return view
}

func (j *JSONValue) FindPaths(paths []string) map[string]*Value {
  data := map[string]*Value{}
  for _, path := range paths {
    data[path] = j.FindPath(path)
  }
  return data
}

func (j *JSONValue) FindPath(path string) *Value {
  value := &Value{Value: j.Value()}
  currMap := j.jsonMap
  currArr := j.jsonArr
  pathKeys := strings.Split(path, ".")
  for _, key := range pathKeys {
    var next interface{}
    if currArr != nil {
      if i, err := strconv.Atoi(key); err == nil && i < len(currArr) {
        next = currArr[i]
      }
    } else if currMap != nil {
      next = currMap[key]
    }
    if next != nil {
      value.Value = next
    } else if next == nil {
      return nil
    }
    switch v := next.(type) {
    case map[string]interface{}:
      currMap = v
      currArr = nil
      value.IsJSON = true
    case []interface{}:
      currArr = v
      currMap = nil
      value.IsJSON = true
    default:
      value.IsJSON = false
    }
  }
  return value
}

func (j *JSONValue) FindTransformPath(path string, join, replace, push bool) JSONField {
  currMap := j.jsonMap
  currArr := j.jsonArr
  var parentMap map[string]interface{}
  var parentArr []interface{}
  var grandParentMap map[string]interface{}
  var grandParentArr []interface{}
  var jsonField JSONField
  var grandParentKey string
  var grandParentIndex int
  lastIndex := 0
  lastKey := ""
  lastKeyExists := false
  pathKeys := strings.Split(path, ".")
  for i, key := range pathKeys {
    grandParentMap = parentMap
    grandParentArr = parentArr
    grandParentKey = lastKey
    grandParentIndex = lastIndex
    parentMap = currMap
    parentArr = currArr
    var next interface{}
    if currArr != nil {
      if i, err := strconv.Atoi(key); err == nil && i < len(currArr) {
        next = currArr[i]
        lastIndex = i
        lastKey = ""
      }
    } else if currMap != nil {
      next = currMap[key]
      lastKey = key
      lastIndex = -1
    }
    if next == nil {
      lastKeyExists = false
      if i < len(pathKeys)-1 || parentArr != nil && lastIndex == -1 ||
        parentMap != nil && lastKey == "" {
        return nil
      } else {
        break
      }
    }
    lastKeyExists = true
    switch v := next.(type) {
    case map[string]interface{}:
      currMap = v
      currArr = nil
    case []interface{}:
      currArr = v
      currMap = nil
    }
  }
  if parentArr != nil {
    var grandParent JSONField
    if grandParentMap != nil {
      grandParent = &JSONMapField{path: path, mapField: grandParentMap, key: grandParentKey, replace: true}
    } else if grandParentArr != nil {
      grandParent = &JSONArrayField{path: path, arrayField: grandParentArr, index: grandParentIndex, replace: true}
    }
    jsonField = &JSONArrayField{path: path, arrayField: parentArr, index: lastIndex, exists: lastKeyExists, join: join, replace: replace, push: push, grandParent: grandParent}
  } else if parentMap != nil {
    jsonField = &JSONMapField{path: path, mapField: parentMap, key: lastKey, exists: lastKeyExists, join: join, replace: replace, push: push}
  }
  return jsonField
}

func (j *JSONValue) Transform(ts []*JSONTransform, source JSON) bool {
  transformed := false
  for i, t := range ts {
    var sourceValue interface{}
    target := t.Target
    if target == "" {
      target = t.Source
      //if source and target are same, prefer given value
      sourceValue = t.Value
    }
    if target == "" {
      continue
    }
    //either source/target are different, or the given value is missing when source and target are same
    if sourceValue == nil {
      if sourceField := source.FindTransformPath(t.Source, t.join, t.replace, t.push); sourceField != nil && sourceField.Exists() {
        sourceValue = sourceField.Read()
      }
    }
    //if source value is missing for different source/target, given value is read with lower preference.
    //for same source/target, it's already handled previously and this assignment is redundant
    if sourceValue == nil {
      sourceValue = t.Value
    }
    if sourceValue != nil {
      if t.cotainsRegexp != nil && !t.cotainsRegexp.MatchString(fmt.Sprint(sourceValue)) {
        continue
      }
      if t.notCotainsRegexp != nil && t.notCotainsRegexp.MatchString(fmt.Sprint(sourceValue)) {
        continue
      }
      if IsFiller(target) {
        filler, _ := GetFillerUnmarked(target)
        j.addPatterns(i, filler, fmt.Sprint(sourceValue))
      } else if targetField := j.FindTransformPath(target, t.join, t.replace, t.push); targetField != nil {
        targetField.Update(sourceValue)
      }
      transformed = true
    }
  }
  return transformed
}

func (j *JSONValue) TransformPatterns(text string) string {
  if j.jsonPatterns == nil {
    return text
  }
  for _, p := range j.jsonPatterns.patterns {
    if len(p) < 2 {
      continue
    }
    text = strings.ReplaceAll(text, p[0], p[1])
  }
  return text
}

func (j *JSONValue) addPatterns(id int, patterns ...string) {
  if j.jsonPatterns == nil {
    j.jsonPatterns = &JSONPatterns{patterns: map[int][]string{}}
  }
  j.jsonPatterns.patterns[id] = patterns
}

func (j *JSONTransform) Init() {
  if strings.EqualFold(j.Mode, "join") {
    j.join = true
  } else if strings.EqualFold(j.Mode, "replace") {
    j.replace = true
  } else if strings.EqualFold(j.Mode, "push") {
    j.push = true
  }
  if j.IfContains != "" {
    j.cotainsRegexp = regexp.MustCompile("(?i)" + j.IfContains)
  }
  if j.IfNotContains != "" {
    j.notCotainsRegexp = regexp.MustCompile("(?i)" + j.IfNotContains)
  }
}

func (j *JSONArrayField) Exists() bool {
  return j.exists
}

func (j *JSONMapField) Exists() bool {
  return j.exists
}

func (j *JSONArrayField) Read() interface{} {
  return j.arrayField[j.index]
}

func (j *JSONMapField) Read() interface{} {
  return j.mapField[j.key]
}

func (j *JSONArrayField) Update(value interface{}) {
  if j.replace || j.join {
    if len(j.arrayField) > j.index {
      if j.join {
        j.arrayField[j.index] = fmt.Sprint(j.arrayField[j.index]) + fmt.Sprint(value)
      } else {
        j.arrayField[j.index] = value
      }
    }
  } else {
    if len(j.arrayField) > j.index {
      j.arrayField = AddToArray(j.arrayField, value, j.index, j.push)
    } else {
      j.arrayField = append(j.arrayField, value)
    }
    if j.grandParent != nil {
      j.grandParent.Update(j.arrayField)
    }
  }
}

func (j *JSONMapField) Update(value interface{}) {
  if j.replace || j.join {
    if _, present := j.mapField[j.key]; present {
      if j.join {
        j.mapField[j.key] = fmt.Sprint(j.mapField[j.key]) + fmt.Sprint(value)
      } else {
        j.mapField[j.key] = value
      }
    }
  } else {
    currVal := j.mapField[j.key]
    kind := reflect.ValueOf(currVal).Kind()
    if kind == reflect.Array || kind == reflect.Slice {
      if currVal != nil {
        j.mapField[j.key] = AddToArray(currVal.([]interface{}), value, -1, j.push)
      } else {
        j.mapField[j.key] = []interface{}{value}
      }
    } else if currVal != nil {
      if j.push {
        j.mapField[j.key] = []interface{}{value, currVal}
      } else {
        j.mapField[j.key] = []interface{}{currVal, value}
      }
    } else {
      j.mapField[j.key] = []interface{}{value}
    }
  }
}

func AddToArray(arr []interface{}, value interface{}, at int, push bool) []interface{} {
  newArr := []interface{}{}
  if at < 0 {
    at = len(arr) - 1
  }
  if push {
    newArr = append(newArr, arr[:at]...)
  } else {
    newArr = append(newArr, arr[:at+1]...)
  }
  newArr = append(newArr, value)
  if push {
    newArr = append(newArr, arr[at:]...)
  } else if len(arr) > at+1 {
    newArr = append(newArr, arr[at+1:]...)
  }
  return newArr
}

func GetEmptyCopy(v interface{}) interface{} {
  switch v.(type) {
  case map[string]interface{}:
    return map[string]interface{}{}
  case []interface{}:
    return []interface{}{}
  }
  return nil
}

func Clone(v interface{}) interface{} {
  gob.Register(map[string]interface{}{})
  gob.Register(map[string][]interface{}{})
  gob.Register([]interface{}{})
  gob.Register(unstructured.Unstructured{})
  gob.Register(unstructured.UnstructuredList{})
  buff := &bytes.Buffer{}
  enc := gob.NewEncoder(buff)
  dec := gob.NewDecoder(buff)
  var err error
  if err = enc.Encode(v); err == nil {
    switch v.(type) {
    case map[string]interface{}:
      copy := map[string]interface{}{}
      if err = dec.Decode(&copy); err == nil {
        return copy
      }
    case map[string][]interface{}:
      copy := map[string][]interface{}{}
      if err = dec.Decode(&copy); err == nil {
        return copy
      }
    case []interface{}:
      copy := []interface{}{}
      if err = dec.Decode(&copy); err == nil {
        return copy
      }
    case *unstructured.UnstructuredList:
      copy := &unstructured.UnstructuredList{}
      if err = dec.Decode(&copy); err == nil {
        return copy
      }
    case *unstructured.Unstructured:
      copy := &unstructured.Unstructured{}
      if err = dec.Decode(&copy); err == nil {
        return copy
      }
    }
  }
  if err != nil {
    fmt.Printf("Failed to clone [%+v] with error: %s\n", v, err.Error())
  }
  return nil
}

func ReadJson(s string, t interface{}) error {
  return json.Unmarshal([]byte(s), t)
}

func ToJSON(o interface{}) string {
  if output, err := json.Marshal(o); err == nil {
    return string(output)
  }
  return fmt.Sprintf("%+v", o)
}

func NewJSONPaths() *JSONPath {
  return &JSONPath{TextPaths: map[string]string{}, Paths: map[string]*jsonpath.JSONPath{}}
}

func (jp *JSONPath) Parse(paths []string) *JSONPath {
  if len(paths) == 0 || paths[0] == "" {
    return jp
  }
  for _, path := range paths {
    pathKV := strings.Split(path, ":")
    if len(pathKV) < 2 {
      fmt.Printf("Invalid JSONPath [%s]\n", path)
      continue
    }
    key := pathKV[0]
    path = pathKV[1]
    j := jsonpath.New(path)
    j.Parse(path)
    jp.TextPaths[key] = path
    jp.Paths[key] = j
  }
  return jp
}

func (jp *JSONPath) Apply(j JSON) JSON {
  if j == nil {
    return nil
  }
  out := map[string][]interface{}{}
  data := j.Value()
  for k, jsonPath := range jp.Paths {
    if matches, err := jsonPath.FindResults(data); err == nil && len(matches) > 0 && len(matches[0]) > 0 {
      for _, v1 := range matches {
        for _, v2 := range v1 {
          out[k] = append(out[k], v2.Interface())
        }
      }
    } else if err != nil {
      fmt.Printf("Failed to find results for path [%s] with error: %s\n", jp.TextPaths[k], err.Error())
    }
  }
  return FromJSON(out)
}

func (jp *JSONPath) IsEmpty() bool {
  return len(jp.Paths) == 0
}

func NewJQ() *JQ {
  return &JQ{Queries: map[string]*gojq.Code{}, TextQueries: map[string]string{}}
}

func (jq *JQ) Parse(queries []string) *JQ {
  if len(queries) == 0 || queries[0] == "" {
    return jq
  }
  for _, query := range queries {
    pieces := strings.Split(query, ":")
    if len(pieces) < 2 {
      fmt.Printf("Invalid jq id+query pair: %s\n", query)
      continue
    }
    if q, err := gojq.Parse(pieces[1]); err == nil {
      if code, err := gojq.Compile(q); err == nil {
        jq.Queries[pieces[0]] = code
        jq.TextQueries[pieces[0]] = pieces[1]
      } else {
        fmt.Printf("Failed to compile jq query [%s] with error: %s\n", query, err.Error())
      }
    } else {
      fmt.Printf("Failed to parse jq query [%s] with error: %s\n", query, err.Error())
    }
  }
  return jq
}

func (jq *JQ) Apply(j JSON) JSON {
  if j == nil {
    return nil
  }
  out := map[string][]interface{}{}
  for id, q := range jq.Queries {
    iter := q.Run(j.Value())
    for {
      if value, ok := iter.Next(); ok {
        if err, ok := value.(error); ok {
          fmt.Println(err)
        } else {
          out[id] = append(out[id], value)
        }
      } else {
        break
      }
    }
  }
  return FromJSON(out)
}

func (jq *JQ) IsEmpty() bool {
  return len(jq.Queries) == 0
}
