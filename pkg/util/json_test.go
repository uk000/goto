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
	"testing"
)

// The items of a JSON array that is streamed are the items as they are in the array, whatever they are.
func TestJSONArrayItemsKeepTheirValues(t *testing.T) {
	j, ok := JSONFromJSONText(`["a", 1, 2.5, true, false, null, {"k":"v"}, [1,2], "with \"quotes\""]`)
	if !ok {
		t.Fatal("JSON was not parsed")
	}
	want := []string{`"a"`, `1`, `2.5`, `true`, `false`, `null`, `{"k":"v"}`, `[1,2]`, `"with \"quotes\""`}
	items := j.ToJSONArray()
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d", len(items), len(want))
	}
	for i, item := range items {
		if got := item.ToJSONText(); got != want[i] {
			t.Errorf("item %d: ToJSONText() = %s, want %s", i, got, want[i])
		}
		if got := string(item.ToBytes()); got != want[i] {
			t.Errorf("item %d: ToBytes() = %s, want %s", i, got, want[i])
		}
		if b, err := json.Marshal(item); err != nil || string(b) != want[i] {
			t.Errorf("item %d: json.Marshal() = %s, %v, want %s", i, b, err, want[i])
		}
		if got := string(ToJSONBytes(item)); got != want[i] {
			t.Errorf("item %d: ToJSONBytes() = %s, want %s", i, got, want[i])
		}
	}
}

func TestJSONArrayItemsOfAnArrayOfObjectsAreUnchanged(t *testing.T) {
	items := JSONFromBytes([]byte(`[{"a":1},{"a":2},[3]]`)).ToJSONArray()
	want := []string{`{"a":1}`, `{"a":2}`, `[3]`}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d", len(items), len(want))
	}
	for i, item := range items {
		if got := item.ToJSONText(); got != want[i] {
			t.Errorf("item %d = %s, want %s", i, got, want[i])
		}
	}
}

func TestJSONObjectValuesKeepTheirValues(t *testing.T) {
	j, _ := JSONFromJSONText(`{"s":"v","n":3,"b":true,"z":null,"o":{"k":1},"a":[1]}`)
	want := map[string]string{"s": `"v"`, "n": `3`, "b": `true`, "z": `null`, "o": `{"k":1}`, "a": `[1]`}
	values := j.JSONObject().JSONMap
	if len(values) != len(want) {
		t.Fatalf("values = %d, want %d", len(values), len(want))
	}
	for k, w := range want {
		if got := values[k].ToJSONText(); got != w {
			t.Errorf("value of %s = %s, want %s", k, got, w)
		}
	}
}

func TestCloneOfAJSONValueThatIsNotAnObjectOrArray(t *testing.T) {
	item := JSONFromBytes([]byte(`["x"]`)).ToJSONArray()[0]
	if got := item.Clone().ToJSONText(); got != `"x"` {
		t.Errorf("clone of a string = %s, want %q", got, `"x"`)
	}
	if got := NewJSON().Clone().ToJSONText(); got != `null` {
		t.Errorf("clone of an empty JSON = %s, want null", got)
	}
}

func TestJSONValueThatIsNotAnObjectOrArrayStaysEmpty(t *testing.T) {
	item := JSONFromBytes([]byte(`["x"]`)).ToJSONArray()[0]
	if !item.IsEmpty() || item.IsObject() || item.IsArray() {
		t.Errorf("a string is empty/object/array = %v/%v/%v, want true/false/false", item.IsEmpty(), item.IsObject(), item.IsArray())
	}
	if got := item.ToJSONArray(); len(got) != 1 || got[0] != item {
		t.Errorf("ToJSONArray of a string = %v, want the string only", got)
	}
}
