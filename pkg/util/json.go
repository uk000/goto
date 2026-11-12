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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type JSON interface {
	Value() interface{}
	Object() map[string]interface{}
	Array() []interface{}
	JSONObject() *JSONObject
	JSONArray() []JSON
	ToJSONArray() []JSON

	ParseJSON(text string, noerror bool) bool
	ParseYAML(y string) bool
	Store(i interface{})
	Clone() JSON
	ToJSONText() string
	ToYAML() string
	ToBytes() []byte

	IsEmpty() bool
	IsObject() bool
	IsArray() bool

	Get(path string) *Value
	GetText(path string) string
	GetRaw() any
	FindPath(path string) *Value
	FindPaths(paths []string) map[string]*Value
	FindTransformPath(path string, join, replace, push bool) JSONField
	Transform(ts []*JSONTransform, source JSON) bool
	TransformPatterns(text string) string
	ExecuteTemplates(templates []*template.Template) JSON
	ExecuteTemplate(t *template.Template) string

	View(fields ...string) map[string]interface{}

	At() *time.Time
}

type JSONPatterns struct {
	patterns map[int][]string
	lock     sync.Mutex
}

type JSONObject struct {
	JSONMap map[string]JSON
}

type JSONValue struct {
	JsonMap map[string]interface{} `json:",inline"`
	JsonArr []interface{}          `json:",inline"`
	Raw     any
	// scalar is what a value that is neither an object nor an array is (a string, number or boolean of an array or an
	// object). It's not what the JSON is parsed or stored into, which is only ever an object or an array.
	scalar       interface{}
	at           time.Time
	jsonPatterns *JSONPatterns
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

type Transform struct {
	Mappings []*JSONTransform `json:"mappings"`
	Payload  interface{}      `json:"payload"`
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

// NewJSON creates an empty JSONValue stamped with the current time.
func NewJSON() JSON {
	return &JSONValue{
		at: time.Now(),
	}
}

// NewJSONWithRaw creates an empty JSONValue that carries raw as the original object returned by GetRaw.
func NewJSONWithRaw(raw any) JSON {
	return &JSONValue{
		at:  time.Now(),
		Raw: raw,
	}
}

// JSONFromJSONText parses JSON text into a JSON value and reports whether parsing succeeded.
func JSONFromJSONText(text string) (JSON, bool) {
	json := NewJSON()
	ok := json.ParseJSON(text, true)
	return json, ok
}

// JSONFromJSON wraps an already decoded JSON map or array (see Store for supported types) in a JSON value.
func JSONFromJSON(j any) JSON {
	json := NewJSON()
	json.Store(j)
	return json
}

// JSONFromYAML parses YAML text into a JSON value and reports whether parsing succeeded.
func JSONFromYAML(y string) (JSON, bool) {
	json := NewJSON()
	ok := json.ParseYAML(y)
	return json, ok
}

// JSONFromObject converts any Go value to a JSON value by marshaling it to text and parsing it back.
func JSONFromObject(o any) JSON {
	j, _ := JSONFromJSONText(ToJSONText(o))
	return j
}

// JSONFromMap wraps the given map, without copying it, as a JSON object value.
func JSONFromMap(o map[string]any) JSON {
	j := NewJSON().(*JSONValue)
	j.JsonMap = o
	return j
}

// JSONFromStringArray wraps a string slice as a JSON value whose map holds the slice under the empty key.
func JSONFromStringArray(a []string) JSON {
	return JSONFromMap(map[string]any{"": a})
}

// JSONFromReader reads r fully and parses the content as JSON, returning nil if the read fails.
func JSONFromReader(r io.Reader) JSON {
	if b, err := io.ReadAll(r); err == nil {
		return JSONFromBytes(b)
	}
	return nil
}

// JSONFromBytes decodes JSON bytes into a JSON value, ignoring decode errors.
func JSONFromBytes(b []byte) JSON {
	var j interface{}
	json.Unmarshal(b, &j)
	return JSONFromJSON(j)
}

// ToJSONArray splits JSON array bytes into one JSON byte slice per element, or returns the input as the only element if it isn't an array.
func ToJSONArray(b []byte) [][]byte {
	j := JSONFromBytes(b)
	out := [][]byte{}
	if j.IsArray() {
		for _, a := range j.Array() {
			b2, _ := json.Marshal(a)
			if b2 != nil {
				out = append(out, b2)
			}
		}
	} else {
		out = append(out, b)
	}
	return out
}

// ToJSONValue wraps a decoded value as a JSONValue holding it as a map, array or scalar depending on its type.
func ToJSONValue(v interface{}) *JSONValue {
	jsonValue := &JSONValue{}
	if vv, ok := v.(map[string]interface{}); ok {
		jsonValue.JsonMap = vv
	} else if vv, ok := v.([]interface{}); ok {
		jsonValue.JsonArr = vv
	} else {
		jsonValue.scalar = v
	}
	return jsonValue
}

// Clone returns a new JSONValue over the same underlying value (shallow copy), keeping the recorded patterns.
func (j *JSONValue) Clone() JSON {
	clone := ToJSONValue(j.Value())
	clone.jsonPatterns = j.jsonPatterns
	return clone
}

// Store stores a decoded map, array or Kubernetes unstructured object into the value; other types are ignored.
func (j *JSONValue) Store(i interface{}) {
	switch v := i.(type) {
	case map[string]interface{}:
		j.JsonMap = v
	case map[string][]interface{}:
		j.JsonMap = map[string]interface{}{}
		for key, list := range v {
			j.JsonMap[key] = list
		}
	case []interface{}:
		j.JsonArr = v
	case *unstructured.UnstructuredList:
		j.ParseJSON(ToJSONText(v), false)
	case *unstructured.Unstructured:
		j.JsonMap = v.Object
	}
}

// Value returns the underlying map of JSON values.
func (j JSONObject) Value() interface{} {
	return j.JSONMap
}

// Object returns the object contents as a plain map, wrapping a non-map value under the empty key.
func (j JSONObject) Object() (obj map[string]interface{}) {
	if m, ok := j.Value().(map[string]interface{}); ok {
		obj = m
		return
	}
	m, ok := j.Value().(map[string]JSON)
	if ok {
		obj = map[string]interface{}{}
		for k, v := range m {
			obj[k] = v
		}
		return
	}
	data := map[string]any{
		"": j.Value(),
	}
	return data
}

// Array always returns nil since a JSONObject is not an array.
func (j JSONObject) Array() []interface{} {
	return nil
}

// JSONObject returns the receiver itself.
func (j JSONObject) JSONObject() JSONObject {
	return j
}

// JSONArray always returns nil since a JSONObject is not an array.
func (j *JSONObject) JSONArray() []JSON {
	return nil
}

// At returns the time at which the value was created.
func (j *JSONValue) At() *time.Time {
	return &j.at
}

// Value returns the underlying object, scalar or array held by the value, in that order of preference.
func (j *JSONValue) Value() interface{} {
	if j.JsonMap != nil {
		return j.JsonMap
	}
	if j.JsonArr == nil && j.scalar != nil {
		return j.scalar
	}
	return j.JsonArr
}

// Object returns the underlying object map, which is nil if the value is not an object.
func (j *JSONValue) Object() map[string]interface{} {
	return j.JsonMap
}

// JSONObject wraps each entry of the object map as a JSONValue inside a JSONObject.
func (j *JSONValue) JSONObject() *JSONObject {
	jsonObject := map[string]JSON{}
	for k, v := range j.JsonMap {
		jsonObject[k] = ToJSONValue(v)
	}
	return &JSONObject{JSONMap: jsonObject}
}

// Array returns the underlying array, which is nil if the value is not an array.
func (j *JSONValue) Array() []interface{} {
	return j.JsonArr
}

// JSONArray wraps each array element as a JSONValue.
func (j *JSONValue) JSONArray() []JSON {
	jsonArr := []JSON{}
	for _, v := range j.JsonArr {
		jsonArr = append(jsonArr, ToJSONValue(v))
	}
	return jsonArr
}

// ToJSONArray returns the array elements as JSON values, or the value itself as a single element if it is not an array.
func (j *JSONValue) ToJSONArray() []JSON {
	if j.JsonArr != nil {
		return j.JSONArray()
	}
	return []JSON{j}
}

// ParseJSON parses JSON text into the value and reports success, printing the parse error unless noerror is set.
func (j *JSONValue) ParseJSON(text string, noerror bool) bool {
	var o interface{}
	if err := json.Unmarshal([]byte(text), &o); err == nil {
		j.Store(o)
		return true
	} else if !noerror {
		fmt.Printf("Failed to parse json with error: %s\n", err.Error())
	}
	return false
}

// ParseYAML converts YAML text to JSON and parses it into the value, reporting success.
func (j *JSONValue) ParseYAML(y string) bool {
	if o, err := yaml.YAMLToJSON([]byte(y)); err == nil {
		return j.ParseJSON(string(o), false)
	} else {
		fmt.Printf("Failed to parse yaml with error: %s\n", err.Error())
	}
	return false
}

// ToJSONText marshals the value to compact JSON text, returning an empty string on error.
func (j *JSONValue) ToJSONText() string {
	if output, err := json.Marshal(j.Value()); err == nil {
		return string(output)
	} else {
		fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
	}
	return ""
}

// ToYAML marshals the value to YAML text, returning an empty string on error.
func (j *JSONValue) ToYAML() string {
	if b, err := yaml.Marshal(j.Value()); err == nil {
		return string(b)
	} else {
		fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
	}
	return ""
}

// ToBytes marshals the value to JSON bytes, returning nil on error.
func (j *JSONValue) ToBytes() []byte {
	if output, err := json.Marshal(j.Value()); err == nil {
		return output
	} else {
		fmt.Printf("Failed to marshal json with error: %s\n", err.Error())
	}
	return nil
}

// IsEmpty reports whether the value holds neither an object nor an array.
func (j *JSONValue) IsEmpty() bool {
	return j.JsonArr == nil && j.JsonMap == nil
}

// IsObject reports whether the value holds a JSON object.
func (j *JSONValue) IsObject() bool {
	return j.JsonMap != nil
}

// IsArray reports whether the value holds a JSON array.
func (j *JSONValue) IsArray() bool {
	return j.JsonArr != nil
}

// View returns a map of the given dot-separated paths to the values found at them.
func (j *JSONValue) View(paths ...string) map[string]interface{} {
	view := map[string]interface{}{}
	values := j.FindPaths(paths)
	for k, v := range values {
		view[k] = v.Value
	}
	return view
}

// Get looks up a dot-separated path in the value (same as FindPath).
func (j *JSONValue) Get(path string) *Value {
	return j.FindPath(path)
}

// GetText returns the value at a dot-separated path as text (JSON text for objects and arrays), or empty if not found.
func (j *JSONValue) GetText(path string) string {
	if value := j.FindPath(path); value != nil {
		if value.IsJSON {
			return value.JSON().ToJSONText()
		} else if s, ok := value.Value.(string); ok {
			return s
		} else {
			return fmt.Sprint(value.Value)
		}
	}
	return ""
}

// GetRaw returns the raw object supplied at creation if there is one, otherwise the JSONValue itself.
func (j *JSONValue) GetRaw() any {
	if j.Raw != nil {
		return j.Raw
	}
	return j
}

// JSON wraps the held value as a JSON value.
func (v *Value) JSON() JSON {
	return JSONFromJSON(v.Value)
}

// Get looks up a dot-separated path within the held value.
func (v *Value) Get(path string) *Value {
	return v.JSON().Get(path)
}

// GetText returns the text of the value found at a dot-separated path within the held value.
func (v *Value) GetText(path string) string {
	return v.JSON().GetText(path)
}

// GetRaw returns the underlying value held by the Value.
func (v *Value) GetRaw() any {
	return v.Value
}

// String formats the held value with fmt.Sprint.
func (v *Value) String() string {
	return fmt.Sprint(v.Value)
}

// FindPaths resolves each dot-separated path and returns the results keyed by path, with nil for paths not found.
func (j *JSONValue) FindPaths(paths []string) map[string]*Value {
	data := map[string]*Value{}
	for _, path := range paths {
		data[path] = j.FindPath(path)
	}
	return data
}

// FindPath walks a dot-separated path through nested objects and numeric array indexes, returning nil if any segment is missing.
func (j *JSONValue) FindPath(path string) *Value {
	value := &Value{Value: j.Value()}
	currMap := j.JsonMap
	currArr := j.JsonArr
	pathKeys := RemoveEmpty(strings.Split(path, "."))
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
		} else {
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

// FindTransformPath walks a dot-separated path and returns an updatable field (map key or array slot) for it, or nil if an intermediate segment is missing.
func (j *JSONValue) FindTransformPath(path string, join, replace, push bool) JSONField {
	currMap := j.JsonMap
	currArr := j.JsonArr
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
	pathKeys := RemoveEmpty(strings.Split(path, "."))
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

// Transform applies the mappings to copy values from source into this JSON (or into filler patterns), honoring the contains filters, and reports whether anything was transformed.
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

// TransformPatterns replaces the filler patterns recorded by Transform in text.
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

// addPatterns records filler replacement patterns under a mapping id for later use by TransformPatterns.
func (j *JSONValue) addPatterns(id int, patterns ...string) {
	if j.jsonPatterns == nil {
		j.jsonPatterns = &JSONPatterns{patterns: map[int][]string{}}
	}
	j.jsonPatterns.patterns[id] = patterns
}

// Init derives the join, replace or push mode flags from Mode and compiles the case-insensitive contains and not-contains regexps.
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

// Exists reports whether the path resolved to an existing array element.
func (j *JSONArrayField) Exists() bool {
	return j.exists
}

// Exists reports whether the path resolved to an existing map key.
func (j *JSONMapField) Exists() bool {
	return j.exists
}

// Read returns the array element at the field's index.
func (j *JSONArrayField) Read() interface{} {
	return j.arrayField[j.index]
}

// Read returns the map value at the field's key.
func (j *JSONMapField) Read() interface{} {
	return j.mapField[j.key]
}

// Update sets or joins the element in replace/join mode, otherwise inserts value next to it (before it if push) and propagates the new array to the grandparent field.
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

// Update sets or joins the key in replace/join mode, otherwise appends value to the existing value as an array (prepending if push).
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

// AddToArray returns a new array with value inserted after index at (before it if push), where a negative at means the last element.
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

// GetEmptyCopy returns an empty map or slice of the same kind as v, or nil for other types.
func GetEmptyCopy(v interface{}) interface{} {
	switch v.(type) {
	case map[string]interface{}:
		return map[string]interface{}{}
	case []interface{}:
		return []interface{}{}
	}
	return nil
}

// Clone returns a deep copy of v made by a JSON marshal and unmarshal round trip.
func Clone(v interface{}) interface{} {
	var copy interface{}
	ReadJson(ToJSONText(v), &copy)
	return copy
}

// CleanJSONBytes strips newlines and escaped newlines, unescapes quotes and trims NUL bytes so the content can be parsed as JSON.
func CleanJSONBytes(json []byte) []byte {
	s := string(json)
	s = strings.ReplaceAll(s, "\\\n", "")
	s = strings.ReplaceAll(s, "\\n", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\\\"", "\"")
	// s = strings.ReplaceAll(s, `\"`, `"`)
	json = []byte(s)
	json = bytes.Trim(json, "\x00")
	return json
}

// CleanJSON recursively replaces string values holding embedded JSON objects or arrays with their parsed form.
func CleanJSON(data any) any {
	switch v := data.(type) {
	case map[string]any:
		for key, value := range v {
			v[key] = CleanJSON(value)
		}
	case []any:
		for i, value := range v {
			v[i] = CleanJSON(value)
		}
	case string:
		// Check if string looks like JSON (starts with { or [)
		trimmed := strings.TrimSpace(v)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			var nested any
			if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
				// If unmarshal succeeds, recursively clean the nested data
				return CleanJSON(nested)
			}
		}
	}
	return data
}

// ReadJson unmarshals JSON text into t.
func ReadJson(s string, t interface{}) error {
	return json.Unmarshal([]byte(s), t)
}

// ReadJsonFromBytes unmarshals JSON bytes into t.
func ReadJsonFromBytes(b []byte, t interface{}) error {
	return json.Unmarshal(b, t)
}

// ReadJsonFromAny converts any value into t by marshaling it to JSON and unmarshaling the result.
func ReadJsonFromAny(a any, t interface{}) error {
	b, err := json.Marshal(a)
	if err == nil {
		return json.Unmarshal(b, t)
	}
	return err
}

// ReadStringArray parses JSON bytes as a string array, or treats the whole content as a single-element array if it isn't one.
func ReadStringArray(b []byte) []string {
	arr := []string{}
	if err := json.Unmarshal(b, &arr); err != nil {
		arr = []string{string(b)}
	}
	return arr
}

// ToJSONText marshals o to JSON text, returning an empty string for nil or on panic and a %+v rendering if marshaling fails.
func ToJSONText(o interface{}) (result string) {
	if IsNil(o) {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			result = ""
		}
	}()
	if output, err := json.Marshal(o); err == nil {
		result = string(output)
	} else {
		log.Println(err.Error())
		result = fmt.Sprintf("%+v", o)
	}
	return
}

// ToPrettyJSONText marshals o to JSON text indented with two spaces, falling back to a %+v rendering on error.
func ToPrettyJSONText(o interface{}) string {
	if output, err := json.MarshalIndent(o, "", "  "); err == nil {
		return string(output)
	}
	return fmt.Sprintf("%+v", o)
}

// RemoveEmpty returns arr without its empty strings.
func RemoveEmpty(arr []string) []string {
	var values []string
	for _, item := range arr {
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}

// ProtoToJSON2 decodes protobuf bytes as a structpb.Struct and returns it as a JSON value.
func ProtoToJSON2(b []byte) (JSON, error) {
	pbStruct := &structpb.Struct{}
	err := proto.Unmarshal(b, pbStruct)
	if err != nil {
		fmt.Printf("Failed to unmarshal protobuf data: %v\n", err)
		return nil, err
	}
	goMap := pbStruct.AsMap()
	jsonBytes, err := json.Marshal(goMap)
	if err != nil {
		fmt.Printf("Failed to marshal JSON: %v\n", err)
		return nil, err
	}
	return JSONFromBytes(jsonBytes), nil
}

// ProtoToJSON decodes protobuf bytes without a schema into a JSON map, reading each length-delimited field as a key (field 1) and value (field 2) string pair.
func ProtoToJSON(b []byte) JSON {
	resultMap := map[string]any{}

	// Step 1: Walk the outer payload buffer
	outerBytes := b
	for len(outerBytes) > 0 {
		_, outerWireType, n := protowire.ConsumeTag(outerBytes)
		if n < 0 {
			break
		}
		outerBytes = outerBytes[n:]

		if outerWireType == protowire.BytesType {
			// This extracts the nested kv-pair packet block
			kvPairBlock, n := protowire.ConsumeBytes(outerBytes)
			if n < 0 {
				break
			}
			outerBytes = outerBytes[n:]

			// Step 2: Parse inside the kv-pair packet block
			var currentKey string
			var currentValue string

			innerBytes := kvPairBlock
			for len(innerBytes) > 0 {
				fieldNum, innerWireType, innerN := protowire.ConsumeTag(innerBytes)
				if innerN < 0 {
					break
				}
				innerBytes = innerBytes[innerN:]

				if innerWireType == protowire.BytesType {
					stringBytes, innerN := protowire.ConsumeBytes(innerBytes)
					if innerN < 0 {
						break
					}
					innerBytes = innerBytes[innerN:]
					cleanStr := cleanPrintableString(stringBytes)
					if fieldNum == 1 {
						currentKey = cleanStr
					} else if fieldNum == 2 {
						currentValue = cleanStr
					}
				} else {
					innerN := protowire.ConsumeFieldValue(fieldNum, innerWireType, innerBytes)
					if innerN < 0 {
						break
					}
					innerBytes = innerBytes[innerN:]
				}
			}
			if currentKey != "" && currentValue != "" {
				resultMap[currentKey] = currentValue
			}
		} else {
			n := protowire.ConsumeFieldValue(1, outerWireType, outerBytes)
			if n < 0 {
				break
			}
			outerBytes = outerBytes[n:]
		}
	}
	return JSONFromMap(resultMap)
}

// parseNestedBytes recursively walks protobuf bytes and collects the printable strings found in length-delimited fields.
func parseNestedBytes(data []byte, stringsFound *[]string) {
	b := data
	for len(b) > 0 {
		_, wireType, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]

		if wireType == protowire.BytesType {
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]

			// Check if this block contains nested protobuf fields
			if isProtobufStructure(v) {
				parseNestedBytes(v, stringsFound)
			} else {
				// If it is just clean text, save it
				cleanText := cleanPrintableString(v)
				if len(cleanText) > 0 {
					*stringsFound = append(*stringsFound, cleanText)
				}
			}
		} else {
			// Skip primitive field types (varints, fixed32, etc.)
			n := protowire.ConsumeFieldValue(1, wireType, b)
			if n < 0 {
				break
			}
			b = b[n:]
		}
	}
}

// isProtobufStructure checks if a sub-buffer looks like a nested message
func isProtobufStructure(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// A simple heuristic: if it can be parsed as a valid tag, it's likely a nested struct
	_, _, n := protowire.ConsumeTag(data)
	return n > 0
}

// cleanPrintableString returns data with everything but printable ASCII characters removed.
func cleanPrintableString(data []byte) string {
	var sb strings.Builder
	for _, b := range data {
		if unicode.IsPrint(rune(b)) && b >= 32 && b <= 126 {
			sb.WriteByte(b)
		}
	}
	return sb.String()
}
