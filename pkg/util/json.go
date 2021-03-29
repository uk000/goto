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

  "sigs.k8s.io/yaml"
)

type JSON struct {
  Value          interface{}
  jsonMap        map[string]interface{}
  jsonArr        []interface{}
  targetPatterns map[int][]string
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

func NewJSON() *JSON {
  return &JSON{
    targetPatterns: map[int][]string{},
  }
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

func FromJSONText(text string) *JSON {
  json := NewJSON()
  json.ParseJSON(text)
  return json
}

func FromJSON(j interface{}) *JSON {
  json := NewJSON()
  json.store(j)
  return json
}

func FromYAML(y string) *JSON {
  json := NewJSON()
  json.ParseYAML(y)
  return json
}

func (j *JSON) ParseJSON(text string) {
  var o interface{}
  if err := json.Unmarshal([]byte(text), &o); err == nil {
    j.store(o)
  } else {
    fmt.Printf("Failed to parse json with error: %s\n", err.Error())
  }
}

func (j *JSON) ParseYAML(y string) {
  if o, err := yaml.YAMLToJSON([]byte(y)); err == nil {
    j.ParseJSON(string(o))
  } else {
    fmt.Printf("Failed to parse yaml with error: %s\n", err.Error())
  }
}

func (j *JSON) ToJSON() string {
  if output, err := json.Marshal(j.Value); err == nil {
    return string(output)
  } else {
    fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
  }
  return ""
}

func (j *JSON) ToYAML() string {
  if b, err := yaml.Marshal(j.Value); err == nil {
    return string(b)
  } else {
    fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
  }
  return ""
}

func (j *JSON) store(i interface{}) {
  i = Clone(i)
  switch v := i.(type) {
  case map[string]interface{}:
    j.jsonMap = v
    j.Value = &j.jsonMap
  case []interface{}:
    j.jsonArr = v
    j.Value = &j.jsonArr
  }
}

func (j *JSON) IsEmpty() bool {
  return j.jsonArr == nil && j.jsonMap == nil
}

func (j *JSON) FindPath(path string, join, replace, push bool) JSONField {
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

func (j *JSON) Transform(ts []*JSONTransform, source *JSON) bool {
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
      if sourceField := source.FindPath(t.Source, t.join, t.replace, t.push); sourceField != nil && sourceField.Exists() {
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
        j.targetPatterns[i] = []string{filler, fmt.Sprint(sourceValue)}
      } else if targetField := j.FindPath(target, t.join, t.replace, t.push); targetField != nil {
        targetField.Update(sourceValue)
      }
      transformed = true
    }
  }
  return transformed
}

func (j *JSON) TransformPatterns(text string) string {
  for _, p := range j.targetPatterns {
    if len(p) < 2 {
      continue
    }
    text = strings.ReplaceAll(text, p[0], p[1])
  }
  return text
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
