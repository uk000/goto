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

package locker

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func roundTrip(t *testing.T, ll *LabeledLockers) *LabeledLockers {
	t.Helper()
	b, err := json.Marshal(ll.GetAllLockers(true, true, true, 0))
	if err != nil {
		t.Fatal(err)
	}
	loaded := CombiLockers{}
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatalf("unmarshal failed: %v\n%s", err, b)
	}
	restored := NewLabeledLockers()
	restored.ReplaceLockers(loaded)
	return restored
}

func TestLockers_JSONRoundTrip(t *testing.T) {
	ll := NewLabeledLockers()
	l1 := ll.GetOrCreateLocker("L1")
	l1.Store([]string{"a", "b"}, "hello", false)
	l1.Store([]string{"list"}, "x", true)
	l1.Store([]string{"list"}, "y", true)
	// Keys named like the reserved fields are told apart: "firstReported" is a timestamp only on a top-level entry.
	l1.Store([]string{"firstReported"}, "a top-level entry", false)
	l1.Store([]string{"a", "b", "firstReported"}, "a deep key", false)
	ll.OpenLocker("L1,sub")
	ll.GetOrCreateLocker("L1,sub").Store([]string{"x"}, "nested", false)
	before := l1.DataLocker.Locker["a"].FirstReported

	restored := roundTrip(t, ll)

	if got, _ := restored.Get("L1", []string{"a", "b"}, true, 0); got != "hello" {
		t.Errorf("a,b = %v, want hello", got)
	}
	if got, _ := restored.Get("L1", []string{"list"}, true, 0); !reflect.DeepEqual(got, []any{"x", "y"}) {
		t.Errorf("list = %#v, want [x y]", got)
	}
	if got, _ := restored.Get("L1,sub", []string{"x"}, true, 0); got != "nested" {
		t.Errorf("x in L1,sub = %v, want nested", got)
	}
	// Top-level entries get their timestamps back, and keys named like the timestamps are not mistaken for them.
	if got := restored.GetLocker("L1").DataLocker.Locker["a"]; !got.FirstReported.Equal(before) || got.FirstReported.IsZero() {
		t.Errorf("firstReported = %v, want %v", got.FirstReported, before)
	}
	if got, _ := restored.Get("L1", []string{"firstReported"}, true, 0); got != "a top-level entry" {
		t.Errorf("top-level entry named firstReported = %v", got)
	}
	if got, _ := restored.Get("L1", []string{"a", "b", "firstReported"}, true, 0); got != "a deep key" {
		t.Errorf("deep key named firstReported = %v", got)
	}
}

func TestReplaceLockers_RebuildsLinksAndCurrent(t *testing.T) {
	ll := NewLabeledLockers()
	ll.OpenLocker("A,B") // flags both A and A,B as current, A,B being the last opened
	ll.GetOrCreateLocker("A,B").Store([]string{"k"}, "v", false)

	restored := roundTrip(t, ll)

	sub := restored.GetLocker("A,B")
	if sub == nil || sub.Label != "B" || sub.PathLabel != "A,B" || sub.parent == nil || sub.parent.Label != "A" {
		t.Fatalf("sub-locker not linked to its parent: %+v", sub)
	}
	if current := restored.GetLocker("current"); current != sub {
		t.Errorf("current locker = %v, want the deepest flagged one (A,B)", current)
	}
	// Closing the sub-locker has to work through the restored parent link, and leave the parent.
	restored.ClearLocker("A,B", true)
	if restored.GetLocker("A,B") != nil || restored.GetLocker("A") == nil {
		t.Errorf("closing A,B should keep A only")
	}
	if restored.GetLocker("current").Label != "default" {
		t.Errorf("closing the current locker should make the default locker current")
	}
}

func TestReplaceLockers_AddsDefaultLocker(t *testing.T) {
	x := NewCombiLocker("X", nil)
	x.Current = false
	restored := NewLabeledLockers()
	restored.ReplaceLockers(CombiLockers{"X": x})
	if restored.GetLocker("default") == nil {
		t.Fatal("default locker missing")
	}
	if restored.GetLocker("current") != restored.GetLocker("default") || !restored.GetLocker("default").Current {
		t.Errorf("default should be current when no loaded locker is")
	}
}

func TestReplaceLockers_SkipsEntriesWithoutLabel(t *testing.T) {
	// How the lockers of a context show up in /registry/lockers: a map of lockers keyed by the context.
	b := []byte(`{"default":{"label":"default","current":true},"ctx1":{"C1":{"label":"C1","current":true}}}`)
	loaded := CombiLockers{}
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatal(err)
	}
	restored := NewLabeledLockers()
	restored.ReplaceLockers(loaded)
	if restored.GetLocker("ctx1") != nil || restored.GetLocker("") != nil {
		t.Errorf("the context entry was loaded as a locker")
	}
	if restored.GetLocker("default") == nil || restored.GetLocker("current") != restored.GetLocker("default") {
		t.Errorf("default locker should be there and current")
	}
}

// Clearing a locker keeps its data but not its current flag, which used to be turned on by the re-initialization.
func TestClearLocker_KeepsTheCurrentFlag(t *testing.T) {
	ll := NewLabeledLockers()
	l1 := ll.GetOrCreateLocker("L1")
	l1.Store([]string{"a"}, "x", false)
	l1.Current = false
	ll.ClearLocker("L1", false)
	if ll.GetLocker("L1").Current {
		t.Errorf("clearing a non-current locker flagged it current")
	}
	if n := len(ll.GetLocker("L1").DataLocker.Locker); n != 0 {
		t.Errorf("data not cleared: %d keys left", n)
	}
	ll.ClearLocker("default", false)
	if !ll.GetLocker("default").Current {
		t.Errorf("clearing the current locker made it non-current")
	}
}

func TestHasData(t *testing.T) {
	var nilMap map[string]any
	for _, tc := range []struct {
		data any
		want bool
	}{
		{nil, false}, {"", false}, {"x", true}, {[]any{"x"}, true}, {nilMap, false}, {time.Time{}, true},
	} {
		if got := hasData(tc.data); got != tc.want {
			t.Errorf("hasData(%#v) = %v, want %v", tc.data, got, tc.want)
		}
	}
}

// The view for a locker without data reports "..." instead of the data.
func TestGetDataLockerView_MasksData(t *testing.T) {
	ll := NewLabeledLockers()
	ll.GetOrCreateLocker("L1").Store([]string{"a", "b"}, "hello", false)
	view := ll.GetDataLockersView("L1")["L1"]
	if got := view.Locker["a"].SubKeys["b"].Data; got != "..." {
		t.Errorf("view data = %v, want ...", got)
	}
	if got := ll.GetDataLockers("L1")["L1"].Locker["a"].SubKeys["b"].Data; got != "hello" {
		t.Errorf("data = %v, want hello", got)
	}
}
