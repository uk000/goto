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

  "github.com/itchyny/gojq"
  "sigs.k8s.io/yaml"
)

type JSON interface {
  Value() interface{}
  Object() map[string]interface{}
  Array() []interface{}

  ParseJSON(text string)
  ParseYAML(y string)
  Store(i interface{})
  ToJSON() string
  ToYAML() string

  IsEmpty() bool
  IsObject() bool
  IsArray() bool

  AddQueries(id int, queries []string)
  ExecuteQueries() []interface{}

  FindPath(path string) *Value
  FindPaths(paths []string) map[string]*Value
  FindTransformPath(path string, join, replace, push bool) JSONField
  Transform(ts []*JSONTransform, source JSON) bool
  TransformPatterns(text string) string

  View(fields ...string) map[string]interface{}
}

type JSONValue struct {
  jsonMap map[string]interface{}
  jsonArr []interface{}
  index   *JSONIndex
}

type JSONIndex struct {
  queries  map[int]*gojq.Code
  patterns map[int][]string
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
    index: &JSONIndex{
      patterns: map[int][]string{},
      queries:  map[int]*gojq.Code{},
    },
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

func (j *JSONValue) Value() interface{} {
  if j.jsonMap != nil {
    return j.jsonMap
  }
  return j.jsonArr
}

func (j *JSONValue) Object() map[string]interface{} {
  return j.jsonMap
}

func (j *JSONValue) Array() []interface{} {
  return j.jsonArr
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
  if output, err := json.Marshal(j.Value); err == nil {
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
  case []interface{}:
    j.jsonArr = v
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

func (j *JSONValue) AddQueries(id int, queries []string) {
  for _, query := range queries {
    if q, err := gojq.Parse(query); err == nil {
      if code, err := gojq.Compile(q); err == nil {
        j.index.queries[id] = code
      }
    }
  }
}

func (j *JSONValue) ExecuteQueries() []interface{} {
  data := []interface{}{}
  for _, q := range j.index.queries {
    iter := q.Run(j.Value())
    for {
      if value, ok := iter.Next(); ok {
        if err, ok := value.(error); ok {
          fmt.Println(err)
        } else {
          data = append(data, value)
        }
      } else {
        break
      }
    }
  }
  return data
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
  for _, p := range j.index.patterns {
    if len(p) < 2 {
      continue
    }
    text = strings.ReplaceAll(text, p[0], p[1])
  }
  return text
}

func (j *JSONValue) addPatterns(id int, patterns ...string) {
  j.index.patterns[id] = patterns
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
  gob.Register([]interface{}{})
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
    case []interface{}:
      copy := []interface{}{}
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
