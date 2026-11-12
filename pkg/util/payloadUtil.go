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

	"sigs.k8s.io/yaml"
)

// ReadJsonPayload decodes the request body as JSON into t.
func ReadJsonPayload(r *http.Request, t interface{}) error {
	return ReadJsonPayloadFromBody(r.Body, t)
}

// ReadJsonPayloadFromBody reads the body fully and decodes it as JSON into t.
func ReadJsonPayloadFromBody(body io.Reader, t interface{}) error {
	if body, err := io.ReadAll(body); err == nil {
		return json.Unmarshal(body, t)
	} else {
		return err
	}
}

// ReadJsonOrYamlPayloadFromBody reads the body fully and decodes it into t as JSON, falling back to YAML.
func ReadJsonOrYamlPayloadFromBody(body io.Reader, t interface{}) error {
	if b, err := io.ReadAll(body); err == nil {
		return ReadJsonOrYamlPayloadFromBytes(b, t)
	} else {
		return err
	}
}

// ReadJsonOrYamlPayloadFromBytes decodes b into t as JSON, falling back to YAML.
func ReadJsonOrYamlPayloadFromBytes(b []byte, t interface{}) error {
	err := json.Unmarshal(b, t)
	if err != nil {
		err = yaml.Unmarshal(b, t)
	}
	return err
}

// JsonOrYamlFromText parses text as JSON, falling back to YAML, into a generic value.
func JsonOrYamlFromText(text string) (any, error) {
	var obj any
	err := json.Unmarshal([]byte(text), &obj)
	if err != nil {
		err = yaml.Unmarshal([]byte(text), &obj)
	}
	return obj, err
}

// WriteJsonOrYAMLPayload sets the matching Content-Type header and writes t to the response as YAML or JSON.
func WriteJsonOrYAMLPayload(w http.ResponseWriter, t interface{}, yaml bool) error {
	if yaml {
		w.Header().Add(constants.HeaderContentType, constants.ContentTypeYAML)
		return WriteYaml(w, t)
	} else {
		w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
		return WriteJson(w, t)
	}
}

// WriteJsonPayload sets the JSON Content-Type header and writes t to the response as indented JSON.
func WriteJsonPayload(w http.ResponseWriter, t interface{}) error {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	return WriteJson(w, t)
}

// WriteStringJsonPayload sets the JSON Content-Type header and writes the given string to the response as is.
func WriteStringJsonPayload(w http.ResponseWriter, json string) {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	fmt.Fprintln(w, json)
}

// WriteJson writes j followed by a newline as indented JSON, with strings written as is and nil written as an empty line.
func WriteJson(w io.Writer, j interface{}) error {
	if s, ok := j.(string); ok {
		fmt.Fprintln(w, s)
	} else if IsNil(j) {
		fmt.Fprintln(w, "")
	} else {
		if bytes, err := json.MarshalIndent(j, "", "  "); err == nil {
			data := string(bytes)
			if _, err := fmt.Fprintln(w, data); err != nil {
				return err
			}
		} else {
			fmt.Printf("Failed to write json payload: %s\n", err.Error())
			return err
		}
	}
	return nil
}

// WriteYaml writes t as YAML to w.
func WriteYaml(w io.Writer, t interface{}) error {
	data := ToYaml(t)
	if w != nil {
		if _, err := fmt.Fprintln(w, data); err != nil {
			return err
		}
	}
	return nil
}

// ToYaml marshals t to YAML text, returning an empty string for nil or on error.
func ToYaml(t interface{}) string {
	data := ""
	if !IsNil(t) {
		if b, err := yaml.Marshal(t); err == nil {
			data = string(b)
		} else {
			fmt.Printf("Failed to marshal yaml with error: %s\n", err.Error())
		}
	}
	return data
}

// WriteErrorJson writes a {"error": ...} JSON body with the given message, without escaping it.
func WriteErrorJson(w http.ResponseWriter, error string) {
	fmt.Fprintf(w, "{\"error\":\"%s\"}", error)
}

// ToJSONBytes marshals v to JSON bytes, returning nil on error.
func ToJSONBytes(v any) []byte {
	if b, err := json.Marshal(v); err == nil {
		return b
	} else {
		fmt.Printf("Failed to marshal value to bytes: %s\n", err.Error())
	}
	return nil
}

// DiscardRequestBody drains and closes the request body, returning the number of bytes discarded.
func DiscardRequestBody(r *http.Request) int {
	defer r.Body.Close()
	len, _ := io.Copy(io.Discard, r.Body)
	return int(len)
}

// DiscardResponseBody drains and closes the response body, returning the number of bytes discarded.
func DiscardResponseBody(r *http.Response) int {
	defer r.Body.Close()
	len, _ := io.Copy(io.Discard, r.Body)
	return int(len)
}

// CloseResponse drains and closes the response body so the connection can be reused.
func CloseResponse(r *http.Response) {
	defer r.Body.Close()
	io.Copy(io.Discard, r.Body)
}

// TransformPayload applies the transforms to a JSON or YAML source payload and returns the first resulting payload, or the source payload unchanged if it can't be parsed or no transform produces output.
func TransformPayload(sourcePayload string, transforms []*Transform, isYaml bool) string {
	var sourceJSON JSON
	var ok bool
	isYAML := false
	if isYaml {
		sourceJSON, ok = JSONFromYAML(sourcePayload)
		isYAML = true
	} else {
		sourceJSON, ok = JSONFromJSONText(sourcePayload)
	}
	// A payload that isn't JSON (or YAML), or has nothing in it to read values from, is served as it is.
	if !ok || sourceJSON.IsEmpty() {
		return sourcePayload
	}
	targetPayload := ""
	for _, t := range transforms {
		var targetJSON JSON
		if t.Payload != nil {
			// Applying the mappings changes the template, which is the same one for every request, so they are applied
			// to a copy of it that has nothing shared with it.
			targetJSON = JSONFromJSON(Clone(t.Payload))
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
