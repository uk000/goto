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
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"io"
	"net/http"
	"reflect"

	"sigs.k8s.io/yaml"
)

func ReadJsonPayload(r *http.Request, t interface{}) error {
	return ReadJsonPayloadFromBody(r.Body, t)
}

func ReadJsonPayloadFromBody(body io.Reader, t interface{}) error {
	if body, err := io.ReadAll(body); err == nil {
		return json.Unmarshal(body, t)
	} else {
		return err
	}
}

func WriteJsonOrYAMLPayload(w http.ResponseWriter, t interface{}, yaml bool) string {
	if yaml {
		w.Header().Add(constants.HeaderContentType, constants.ContentTypeYAML)
		return WriteYaml(w, t)
	} else {
		w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
		return WriteJson(w, t)
	}
}

func WriteJsonPayload(w http.ResponseWriter, t interface{}) string {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	return WriteJson(w, t)
}

func WriteStringJsonPayload(w http.ResponseWriter, json string) {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	fmt.Fprintln(w, json)
}

func WriteJson(w io.Writer, j interface{}) string {
	if s, ok := j.(string); ok {
		fmt.Fprintln(w, s)
	} else if reflect.ValueOf(j).IsNil() {
		fmt.Fprintln(w, "")
	} else {
		if bytes, err := json.MarshalIndent(j, "", "  "); err == nil {
			data := string(bytes)
			fmt.Fprintln(w, data)
			return data
		} else {
			fmt.Printf("Failed to write json payload: %s\n", err.Error())
		}
	}
	return ""
}

func WriteYaml(w io.Writer, t interface{}) string {
	data := ""
	if !reflect.ValueOf(t).IsNil() {
		if b, err := yaml.Marshal(t); err == nil {
			data = string(b)
		} else {
			fmt.Printf("Failed to marshal yaml with error: %s\n", err.Error())
		}
	}
	if w != nil {
		fmt.Fprintln(w, data)
	}
	return data
}

func WriteErrorJson(w http.ResponseWriter, error string) {
	fmt.Fprintf(w, "{\"error\":\"%s\"}", error)
}

func ToJSONBytes(v any) []byte {
	if b, err := json.Marshal(v); err == nil {
		return b
	} else {
		fmt.Printf("Failed to marshal value to bytes: %s\n", err.Error())
	}
	return nil
}

func DiscardRequestBody(r *http.Request) int {
	defer r.Body.Close()
	len, _ := io.Copy(io.Discard, r.Body)
	return int(len)
}

func DiscardResponseBody(r *http.Response) int {
	defer r.Body.Close()
	len, _ := io.Copy(io.Discard, r.Body)
	return int(len)
}

func CloseResponse(r *http.Response) {
	defer r.Body.Close()
	io.Copy(io.Discard, r.Body)
}

func TransformPayload(sourcePayload string, transforms []*Transform, isYaml bool) string {
	var sourceJSON JSON
	isYAML := false
	if isYaml {
		sourceJSON = JSONFromYAML(sourcePayload)
		isYAML = true
	} else {
		sourceJSON = JSONFromJSONText(sourcePayload)
	}
	if sourceJSON.IsEmpty() {
		return sourcePayload
	}
	targetPayload := ""
	for _, t := range transforms {
		var targetJSON JSON
		if t.Payload != nil {
			targetJSON = JSONFromJSON(t.Payload).Clone()
		} else {
			targetJSON = sourceJSON
		}
		if targetJSON != nil && !targetJSON.IsEmpty() {
			if targetJSON.Transform(t.Mappings, sourceJSON) {
				if isYAML {
					targetPayload = targetJSON.ToYAML()
				} else {
					targetPayload = targetJSON.ToJSONText()
				}
			}
			targetPayload = targetJSON.TransformPatterns(targetPayload)
		}
		if targetPayload != "" {
			break
		}
	}
	if targetPayload == "" {
		targetPayload = sourcePayload
	}
	return targetPayload
}
