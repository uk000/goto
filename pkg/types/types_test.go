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

package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPairAndTripleText(t *testing.T) {
	p := NewPair("left", 42)
	if p.Left != "left" || p.Right != 42 {
		t.Errorf("NewPair() = %+v, want left, 42", p)
	}
	if got := p.String(); got != "left: 42" {
		t.Errorf("Pair.String() = %q, want %q", got, "left: 42")
	}
	if p.LeftS() != "left" || p.RightS() != "42" {
		t.Errorf("Pair.LeftS(), RightS() = %q, %q, want left, 42", p.LeftS(), p.RightS())
	}
	sp := NewStringPair("a", "b")
	if sp.Left != "a" || sp.Right != "b" {
		t.Errorf("NewStringPair() = %+v, want a, b", sp)
	}
	ap := NewStringAnyPair("k", []int{1, 2})
	if ap.Left != "k" || ap.RightS() != "[1 2]" {
		t.Errorf("NewStringAnyPair() = %+v, want k, [1 2]", ap)
	}
	tr := NewTriple("x", 2, true)
	if tr.First != "x" || tr.Second != 2 || tr.Third != true {
		t.Errorf("NewTriple() = %+v, want x, 2, true", tr)
	}
	if got := tr.String(); got != "x:2:true" {
		t.Errorf("Triple.String() = %q, want x:2:true", got)
	}
	if tr.FirstS() != "x" || tr.SecondS() != "2" || tr.ThirdS() != "true" {
		t.Errorf("Triple.FirstS(), SecondS(), ThirdS() = %q, %q, %q, want x, 2, true", tr.FirstS(), tr.SecondS(), tr.ThirdS())
	}
}

func TestPairMarshalJSON(t *testing.T) {
	// The format of the plain pairs is an object of one key, with a space after the colon.
	b, err := NewPair("a", "b").MarshalJSON()
	if err != nil || string(b) != `{"a": "b"}` {
		t.Errorf("MarshalJSON() = %s, %v, want {\"a\": \"b\"}", b, err)
	}
	b, err = json.Marshal(NewPair("k", 7))
	if err != nil || string(b) != `{"k":"7"}` {
		t.Errorf("json.Marshal() = %s, %v, want {\"k\":\"7\"}: the right side is written as its text", b, err)
	}
	b, err = json.Marshal(map[string]*Pair[string, string]{"p": NewPair("x", "y")})
	if err != nil || string(b) != `{"p":{"x":"y"}}` {
		t.Errorf("json.Marshal() of a pair in a map = %s, %v, want {\"p\":{\"x\":\"y\"}}", b, err)
	}
}

// The texts of a pair are written as JSON strings. They were quoted by strconv.Quote, which escapes control characters
// and invalid UTF-8 the Go way (\x01, \xff) that is not JSON, so that the pair could not be marshalled at all.
func TestPairMarshalJSONEscapesTheTextsAsJSON(t *testing.T) {
	cases := []struct{ left, right string }{
		{"a\x01b", "c"},
		{"tab\there", "line\nbreak"},
		{`quote"inside`, `back\slash`},
		{"bell\a", "esc\x1b[0m"},
		{"unicode ✓ é", "\U0001F600"},
		{"<tag>&", ">"},
		{"  ", "x"},
		{"bad\xffutf8", "also\xc3bad"},
		{"", ""},
	}
	for _, c := range cases {
		b, err := json.Marshal(NewPair(c.left, c.right))
		if err != nil {
			t.Errorf("Marshal(%q, %q) failed: %v", c.left, c.right, err)
			continue
		}
		if !json.Valid(b) {
			t.Errorf("Marshal(%q, %q) = %s, want valid JSON", c.left, c.right, b)
			continue
		}
		var back map[string]string
		if err := json.Unmarshal(b, &back); err != nil {
			t.Errorf("Unmarshal(%s) failed: %v", b, err)
			continue
		}
		// Invalid UTF-8 can't be in JSON, it comes back as the replacement character.
		left, right := strings.ToValidUTF8(c.left, "\uFFFD"), strings.ToValidUTF8(c.right, "\uFFFD")
		if len(back) != 1 || back[left] != right {
			t.Errorf("Marshal(%q, %q) = %s, which reads back as %q, want {%q: %q}", c.left, c.right, b, back, left, right)
		}
	}
}

func TestListArg(t *testing.T) {
	var l ListArg
	if got := l.String(); got != "" {
		t.Errorf("String() of an empty list = %q, want empty", got)
	}
	if err := l.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Set("b c"); err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 || l[0] != "a" || l[1] != "b c" {
		t.Errorf("list after Set = %q, want [a, b c]", []string(l))
	}
	if got := l.String(); got != "a b c" {
		t.Errorf("String() = %q, want %q", got, "a b c")
	}
}

func TestRawBytesKeepsTheTextOfStringsUnquoted(t *testing.T) {
	var r RawBytes
	if err := json.Unmarshal([]byte(`"plain text"`), &r); err != nil || string(r) != "plain text" {
		t.Errorf("RawBytes from a string = %q, %v, want the text without its quotes", r, err)
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &r); err != nil || string(r) != `{"a":1}` {
		t.Errorf("RawBytes from an object = %q, %v, want the object as it is", r, err)
	}
	b, err := json.Marshal(RawBytes("some text"))
	if err != nil || string(b) != `"some text"` {
		t.Errorf("RawBytes marshalled = %s, %v, want a JSON string", b, err)
	}
}
