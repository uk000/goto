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

import "time"

type AbstractJSON struct {}

func (j AbstractJSON) Value() interface{} {
	return j
}

func (j AbstractJSON) Object() map[string]interface{} {
	return nil
}

func (j AbstractJSON) Array() []interface{} {
	return nil
}

func (j AbstractJSON) JSONObject() *JSONObject {
	return nil
}

func (j AbstractJSON) JSONArray() []JSON {
	return nil
}

func (j AbstractJSON) ParseJSON(text string) {
}

func (j AbstractJSON) ParseYAML(text string) {
}

func (j AbstractJSON) Store(i interface{}) {
}

func (j AbstractJSON) ToJSON() string {
	return ""
}

func (j AbstractJSON) ToYAML() string {
	return ""
}

func (j AbstractJSON) IsEmpty() bool {
	return true
}

func (j AbstractJSON) IsObject() bool {
	return false
}

func (j AbstractJSON) IsArray() bool {
	return false
}

func (j AbstractJSON) FindPath(path string) *Value {
	return nil
}

func (j AbstractJSON) FindPaths(paths []string) map[string]*Value {
	return nil
}

func (j AbstractJSON) FindTransformPath(path string, join, replace, push bool) JSONField {
	return nil
}

func (j AbstractJSON) Transform(ts []*JSONTransform, source JSON) bool {
	return false
}

func (j AbstractJSON) TransformPatterns(text string) string {
	return ""
}

func (j AbstractJSON) View(fields ...string) map[string]interface{} {
	return nil
}

func (j AbstractJSON) At() *time.Time {
	return nil
}
