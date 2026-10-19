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

package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"goto/pkg/types"
	"goto/pkg/util"

	"gopkg.in/yaml.v3"
)

type rangeItem struct {
	text      string
	count     int
	restarted bool
}

func (r rangeItem) String() string { return fmt.Sprintf("%s#%d/%v", r.text, r.count, r.restarted) }

func collectText(t *testing.T, call func(f TextRangeFunc) error) []rangeItem {
	t.Helper()
	items := []rangeItem{}
	err := call(func(text string, count int, restarted bool) (bool, error) {
		items = append(items, rangeItem{text, count, restarted})
		return true, nil
	})
	if err != nil {
		t.Fatalf("range failed: %v", err)
	}
	return items
}

func itemsString(items []rangeItem) string {
	s := []string{}
	for _, i := range items {
		s = append(s, i.String())
	}
	return strings.Join(s, " ")
}

func TestNewJSONPayload(t *testing.T) {
	p := NewJSONPayload(util.JSONFromMap(map[string]any{"a": 1}), nil, 0, 0, 0)
	if p.JSON == nil || p.Delay != nil || p.IsStream {
		t.Errorf("payload = %+v", p)
	}
	p = NewJSONPayload(nil, []byte(`{"b":2}`), time.Millisecond, 2*time.Millisecond, 3)
	if p.JSON == nil || p.JSON.ToJSONText() != `{"b":2}` {
		t.Errorf("JSON of the payload = %v", p.JSON)
	}
	if p.Delay == nil || p.Delay.Count != 3 || p.Delay.Max.Duration != 2*time.Millisecond {
		t.Errorf("delay = %+v", p.Delay)
	}
	if p = NewJSONPayload(nil, nil, 0, 0, 0); p.JSON != nil || p.Count() != 0 {
		t.Errorf("payload without content = %+v", p)
	}
}

func TestNewRawPayload(t *testing.T) {
	p := NewRawPayload([]byte("raw"), "text", 0, 0, 0)
	if p.Text != "text" || string(p.Raw.([]byte)) != "raw" || p.Delay != nil {
		t.Errorf("payload = %+v", p)
	}
}

func TestNewStreamPayloads(t *testing.T) {
	jsonStream := NewStreamJSONPayload([]util.JSON{util.JSONFromMap(map[string]any{"a": 1}), util.JSONFromMap(map[string]any{"a": 2})}, nil, 0, 0, 0, 0)
	if !jsonStream.IsStream || len(jsonStream.JSONStream) != 2 || jsonStream.StreamCount != 2 {
		t.Errorf("JSON stream = %+v", jsonStream)
	}
	fromRaw := NewStreamJSONPayload(nil, []byte(`[{"a":1},{"a":2},{"a":3}]`), 5, time.Millisecond, time.Millisecond, 1)
	if len(fromRaw.JSONStream) != 3 || fromRaw.StreamCount != 5 || fromRaw.Delay == nil {
		t.Errorf("JSON stream from bytes = %+v", fromRaw)
	}
	if empty := NewStreamJSONPayload(nil, nil, 0, 0, 0, 0); empty.JSONStream != nil {
		t.Errorf("JSON stream of nothing = %+v", empty)
	}
	textStream := NewStreamTextPayload([]string{"a", "b"}, nil, 0, 0, 0, 0)
	if !textStream.IsStream || strings.Join(textStream.TextStream, ",") != "a,b" || textStream.StreamCount != 2 {
		t.Errorf("text stream = %+v", textStream)
	}
	fromBytes := NewStreamTextPayload(nil, []byte(`["x","y","z"]`), 0, 0, 0, 0)
	if strings.Join(fromBytes.TextStream, ",") != "x,y,z" {
		t.Errorf("text stream from bytes = %+v", fromBytes.TextStream)
	}
	rawFromAny := NewRawStreamPayload([]any{1, "two"}, nil, nil, 0, 0, 0, 0)
	if len(rawFromAny.RawStream) != 2 || rawFromAny.StreamCount != 2 {
		t.Errorf("raw stream of values = %+v", rawFromAny)
	}
	rawFromStrings := NewRawStreamPayload(nil, []string{"a", "b", "c"}, nil, 2, 0, 0, 0)
	if len(rawFromStrings.RawStream) != 3 || rawFromStrings.StreamCount != 2 {
		t.Errorf("raw stream of strings = %+v", rawFromStrings)
	}
	rawFromBytes := NewRawStreamPayload(nil, nil, [][]byte{[]byte("a")}, 0, 0, 0, 0)
	if len(rawFromBytes.RawStream) != 1 || string(rawFromBytes.RawStream[0].([]byte)) != "a" {
		t.Errorf("raw stream of bytes = %+v", rawFromBytes)
	}
}

func TestPayloadCount(t *testing.T) {
	tests := []struct {
		name string
		p    *Payload
		want int
	}{
		{"nothing", &Payload{}, 0},
		{"JSON", &Payload{JSON: util.JSONFromMap(map[string]any{"a": 1})}, 1},
		{"text", &Payload{Text: "t"}, 1},
		{"raw", &Payload{Raw: []byte("r")}, 1},
		{"JSON stream", &Payload{JSONStream: []util.JSON{util.JSONFromMap(map[string]any{"a": 1}), util.JSONFromMap(map[string]any{"a": 2})}}, 2},
		{"text stream", &Payload{TextStream: []string{"a", "b", "c"}}, 3},
		{"raw stream", &Payload{RawStream: []any{1}}, 1},
	}
	for _, tt := range tests {
		if got := tt.p.Count(); got != tt.want {
			t.Errorf("%s: Count() = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestPayloadToText(t *testing.T) {
	tests := []struct {
		name string
		p    *Payload
		want string
	}{
		{"nothing", &Payload{}, ""},
		{"text", &Payload{Text: "hello"}, "hello"},
		{"JSON", &Payload{JSON: util.JSONFromMap(map[string]any{"a": 1})}, `{"a":1}`},
		{"raw string", &Payload{Raw: "raw"}, "raw"},
		{"raw bytes", &Payload{Raw: []byte("bytes")}, "bytes"},
		{"raw JSON", &Payload{Raw: util.JSONFromMap(map[string]any{"b": 2})}, `{"b":2}`},
		{"raw other", &Payload{Raw: 42}, "42"},
		{"text wins over JSON", &Payload{Text: "t", JSON: util.JSONFromMap(map[string]any{"a": 1})}, "t"},
		{"JSON stream", &Payload{JSONStream: []util.JSON{util.JSONFromMap(map[string]any{"a": 1}), util.JSONFromMap(map[string]any{"a": 2})}}, `{"a":1}, {"a":2}`},
		{"text stream", &Payload{TextStream: []string{"a", "b"}}, "a, b"},
		{"raw stream", &Payload{RawStream: []any{"s", []byte("b"), util.JSONFromMap(map[string]any{"c": 3}), 4}}, `s, b, {"c":3}, 4`},
	}
	for _, tt := range tests {
		if got := tt.p.ToText(); got != tt.want {
			t.Errorf("%s: ToText() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestPayloadUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, p *Payload)
	}{
		{"JSON", `{"json":{"a":1}}`, func(t *testing.T, p *Payload) {
			if p.JSON == nil || p.JSON.ToJSONText() != `{"a":1}` || p.IsStream {
				t.Errorf("payload = %+v", p)
			}
		}},
		{"text", `{"text":"hello"}`, func(t *testing.T, p *Payload) {
			if p.Text != "hello" {
				t.Errorf("payload = %+v", p)
			}
		}},
		{"raw", `{"raw":"some raw"}`, func(t *testing.T, p *Payload) {
			if p.Raw != "some raw" {
				t.Errorf("payload = %+v", p)
			}
		}},
		{"JSON stream", `{"jsonStream":[{"a":1},{"a":2}],"streamCount":5}`, func(t *testing.T, p *Payload) {
			if !p.IsStream || len(p.JSONStream) != 2 || p.StreamCount != 5 {
				t.Errorf("payload = %+v", p)
			}
		}},
		{"text stream", `{"textStream":["a","b","c"]}`, func(t *testing.T, p *Payload) {
			if !p.IsStream || len(p.TextStream) != 3 || p.StreamCount != 3 {
				t.Errorf("payload = %+v", p)
			}
		}},
		{"raw stream", `{"rawStream":["a",2],"streamCount":4}`, func(t *testing.T, p *Payload) {
			if !p.IsStream || len(p.RawStream) != 2 || p.StreamCount != 4 {
				t.Errorf("payload = %+v", p)
			}
		}},
		{"delay", `{"text":"t","delay":{"min":"1ms","max":"3ms","count":2}}`, func(t *testing.T, p *Payload) {
			if p.Delay == nil || p.Delay.Min.Duration != time.Millisecond || p.Delay.Max.Duration != 3*time.Millisecond || p.Delay.Count != 2 {
				t.Errorf("delay = %+v", p.Delay)
			}
		}},
		{"empty", `{}`, func(t *testing.T, p *Payload) {
			if p.Count() != 0 || p.Delay != nil {
				t.Errorf("payload = %+v", p)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Payload{}
			if err := json.Unmarshal([]byte(tt.input), p); err != nil {
				t.Fatal(err)
			}
			tt.check(t, p)
		})
	}
}

func TestPayloadUnmarshalJSONRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{`not json`, `{"jsonStream":"not an array"}`, `{"streamCount":"x"}`} {
		if err := json.Unmarshal([]byte(input), &Payload{}); err == nil {
			t.Errorf("%q was accepted", input)
		}
	}
}

func TestPayloadUnmarshalYAML(t *testing.T) {
	p := &Payload{}
	input := "textStream:\n  - one\n  - two\nstreamCount: 3\ndelay:\n  min: 1ms\n  max: 2ms\n"
	if err := yaml.Unmarshal([]byte(input), p); err != nil {
		t.Fatal(err)
	}
	if !p.IsStream || strings.Join(p.TextStream, ",") != "one,two" || p.StreamCount != 3 {
		t.Errorf("payload = %+v", p)
	}
	if p.Delay == nil || p.Delay.Max.Duration != 2*time.Millisecond {
		t.Errorf("delay = %+v", p.Delay)
	}
	if err := yaml.Unmarshal([]byte("streamCount: [not, a, number]"), &Payload{}); err == nil {
		t.Errorf("invalid YAML was accepted")
	}
}

func TestPayloadMarshalYAML(t *testing.T) {
	out, err := yaml.Marshal(&Payload{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "text: hello") {
		t.Errorf("YAML = %q, want the text of the payload", string(out))
	}
	out, err = yaml.Marshal(&Payload{IsStream: true, StreamCount: 2, TextStream: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	var back Payload
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("YAML %q can't be read back: %v", string(out), err)
	}
	if !back.IsStream || strings.Join(back.TextStream, ",") != "a,b" || back.StreamCount != 2 {
		t.Errorf("payload read back = %+v from %q", back, string(out))
	}
}

func TestRangeTextOfSingleValues(t *testing.T) {
	tests := []struct {
		name string
		p    *Payload
		want string
	}{
		{"JSON", &Payload{JSON: util.JSONFromMap(map[string]any{"a": 1})}, `{"a":1}#1/false`},
		{"text", &Payload{Text: "hello"}, `hello#1/false`},
		{"raw string", &Payload{Raw: "raw"}, `raw#1/false`},
		{"raw bytes", &Payload{Raw: []byte("bytes")}, `bytes#1/false`},
		{"raw JSON", &Payload{Raw: util.JSONFromMap(map[string]any{"b": 2})}, `{"b":2}#1/false`},
		{"raw other", &Payload{Raw: 7}, `7#1/false`},
		{"nothing", &Payload{}, ``},
	}
	for _, tt := range tests {
		if got := itemsString(collectText(t, func(f TextRangeFunc) error { return tt.p.RangeText(0, f) })); got != tt.want {
			t.Errorf("%s: RangeText = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRangeTextOfStreamsRepeatsToTheCount(t *testing.T) {
	text := &Payload{TextStream: []string{"a", "b"}, StreamCount: 5}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return text.RangeText(0, f) })); got != "a#1/false b#2/false a#3/true b#4/false a#5/true" {
		t.Errorf("text stream: %s", got)
	}
	jsonStream := &Payload{JSONStream: []util.JSON{util.JSONFromMap(map[string]any{"n": 1}), util.JSONFromMap(map[string]any{"n": 2})}, StreamCount: 3}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return jsonStream.RangeText(0, f) })); got != `{"n":1}#1/false {"n":2}#2/false {"n":1}#3/true` {
		t.Errorf("JSON stream: %s", got)
	}
	raw := &Payload{RawStream: []any{"s", []byte("b"), util.JSONFromMap(map[string]any{"c": 3}), 4}, StreamCount: 4}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return raw.RangeText(0, f) })); got != `s#1/false b#2/false {"c":3}#3/false 4#4/false` {
		t.Errorf("raw stream: %s", got)
	}
}

func TestRangeTextStopsAtTheGivenCount(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b", "c"}, StreamCount: 9}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return p.RangeText(2, f) })); got != "a#1/false b#2/false" {
		t.Errorf("RangeText(2) = %s", got)
	}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return p.RangeText(4, f) })); got != "a#1/false b#2/false c#3/false a#4/true" {
		t.Errorf("RangeText(4) = %s", got)
	}
}

func TestRangeTextFromSkipsTheItemsBefore(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b", "c"}, StreamCount: 3}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return p.RangeTextFrom(2, 0, f) })); got != "b#2/false c#3/false" {
		t.Errorf("RangeTextFrom(2) = %s", got)
	}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return p.RangeTextFrom(5, 6, f) })); got != "b#5/true c#6/false" {
		t.Errorf("RangeTextFrom(5, 6) = %s", got)
	}
}

func TestRangeTextStopsWhenTheCallbackStops(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b", "c"}, StreamCount: 3}
	seen := []string{}
	err := p.RangeText(0, func(text string, count int, restarted bool) (bool, error) {
		seen = append(seen, text)
		return text != "b", nil
	})
	if err != nil || strings.Join(seen, ",") != "a,b" {
		t.Errorf("seen %v, err %v, want the range to stop after b", seen, err)
	}
}

func TestRangeTextReturnsTheErrorOfTheCallback(t *testing.T) {
	boom := errors.New("boom")
	payloads := map[string]*Payload{
		"JSON stream": {JSONStream: []util.JSON{util.JSONFromMap(map[string]any{"a": 1})}, StreamCount: 3},
		"text stream": {TextStream: []string{"a"}, StreamCount: 3},
		"raw stream":  {RawStream: []any{"a"}, StreamCount: 3},
		"JSON":        {JSON: util.JSONFromMap(map[string]any{"a": 1})},
		"text":        {Text: "a"},
		"raw":         {Raw: "a"},
	}
	for name, p := range payloads {
		calls := 0
		err := p.RangeText(0, func(string, int, bool) (bool, error) { calls++; return true, boom })
		if err != boom || calls != 1 {
			t.Errorf("%s: err = %v after %d calls, want the error after the first call", name, err, calls)
		}
		calls = 0
		err = p.RangeAny(0, func(any, int, bool) (bool, error) { calls++; return true, boom })
		if err != boom || calls != 1 {
			t.Errorf("%s: RangeAny err = %v after %d calls, want the error after the first call", name, err, calls)
		}
	}
}

func TestRangeAnyGivesTheValuesAsTheyAre(t *testing.T) {
	jsonValue := util.JSONFromMap(map[string]any{"a": 1})
	type seen struct {
		value any
		count int
	}
	collect := func(p *Payload) []seen {
		out := []seen{}
		if err := p.RangeAny(0, func(data any, count int, restarted bool) (bool, error) {
			out = append(out, seen{data, count})
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := collect(&Payload{JSON: jsonValue}); len(got) != 1 || got[0].value != jsonValue {
		t.Errorf("JSON: %+v", got)
	}
	if got := collect(&Payload{Text: "t"}); len(got) != 1 || got[0].value != "t" {
		t.Errorf("text: %+v", got)
	}
	if got := collect(&Payload{Raw: []byte("r")}); len(got) != 1 || got[0].value != "r" {
		t.Errorf("raw: %+v", got)
	}
	got := collect(&Payload{JSONStream: []util.JSON{jsonValue}, StreamCount: 2})
	if len(got) != 2 || got[0].value != jsonValue || got[1].count != 2 {
		t.Errorf("JSON stream: %+v", got)
	}
	if got := collect(&Payload{TextStream: []string{"x", "y"}, StreamCount: 2}); len(got) != 2 || got[1].value != "y" {
		t.Errorf("text stream: %+v", got)
	}
	if got := collect(&Payload{RawStream: []any{"x", 2}, StreamCount: 2}); len(got) != 2 || got[1].value != "2" {
		t.Errorf("raw stream: %+v", got)
	}
	if got := collect(&Payload{}); len(got) != 0 {
		t.Errorf("nothing: %+v", got)
	}
	items := []any{}
	p := &Payload{TextStream: []string{"a", "b", "c"}, StreamCount: 3}
	if err := p.RangeAnyFrom(2, 0, func(data any, count int, restarted bool) (bool, error) {
		items = append(items, data)
		return true, nil
	}); err != nil || fmt.Sprint(items) != "[b c]" {
		t.Errorf("RangeAnyFrom(2) = %v, err %v", items, err)
	}
}

func TestRangeSkipsRawStreamItemsThatHaveNoText(t *testing.T) {
	p := &Payload{RawStream: []any{"a", "", "c"}, StreamCount: 3}
	if got := itemsString(collectText(t, func(f TextRangeFunc) error { return p.RangeText(0, f) })); got != "a#1/false c#3/false" {
		t.Errorf("RangeText = %s, want the empty item to be skipped but counted", got)
	}
}

func TestRangeTextWithDelayDelaysEachItem(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b", "c"}, StreamCount: 3, Delay: types.NewDelay(5*time.Millisecond, 5*time.Millisecond, -1)}
	start := time.Now()
	got := collectText(t, func(f TextRangeFunc) error { return p.RangeTextWithDelay(0, nil, f) })
	if len(got) != 3 {
		t.Fatalf("items = %v", got)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("3 items of a 5ms delay took %v, want at least 15ms", elapsed)
	}
}

func TestRangeTextWithDelayCanBeOverridden(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b"}, StreamCount: 2, Delay: types.NewDelay(time.Millisecond, time.Millisecond, -1)}
	override := &types.Duration{Duration: 10 * time.Millisecond}
	start := time.Now()
	collectText(t, func(f TextRangeFunc) error { return p.RangeTextWithDelay(0, override, f) })
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("2 items of a 10ms delay took %v, want at least 20ms", elapsed)
	}
}

func TestRangeTextWithDelayWithoutADelay(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b"}, StreamCount: 2}
	if got := collectText(t, func(f TextRangeFunc) error { return p.RangeTextWithDelay(0, nil, f) }); len(got) != 2 {
		t.Errorf("items = %v, want both of them with no delay", got)
	}
	override := &types.Duration{Duration: 3 * time.Millisecond}
	start := time.Now()
	if got := collectText(t, func(f TextRangeFunc) error { return p.RangeTextWithDelay(0, override, f) }); len(got) != 2 {
		t.Errorf("items = %v, want both of them", got)
	}
	if elapsed := time.Since(start); elapsed < 6*time.Millisecond {
		t.Errorf("2 items of a 3ms delay took %v, want the delay that was asked for even though the payload has none", elapsed)
	}
}

func TestRangeTextWithDelayKeepsTheDelayOfThePayload(t *testing.T) {
	p := &Payload{TextStream: []string{"a"}, StreamCount: 3, Delay: types.NewDelay(time.Millisecond, time.Millisecond, 2)}
	collectText(t, func(f TextRangeFunc) error { return p.RangeTextWithDelay(0, nil, f) })
	collectText(t, func(f TextRangeFunc) error { return p.RangeTextWithDelay(0, nil, f) })
	// The delay of a payload is a template: ranging works on a copy, so the count of the payload is not used up.
	if p.Delay.Count != 2 {
		t.Errorf("delay count = %d, want the payload to keep its delay", p.Delay.Count)
	}
}

func TestStreamsOfNothingEndInsteadOfRepeatingForever(t *testing.T) {
	payloads := map[string]*Payload{
		"JSON stream": {JSONStream: []util.JSON{}, StreamCount: 3},
		"text stream": {TextStream: []string{}, StreamCount: 3},
		"raw stream":  {RawStream: []any{}, StreamCount: 3},
	}
	for name, p := range payloads {
		done := make(chan int, 1)
		go func() {
			calls := 0
			p.RangeText(0, func(string, int, bool) (bool, error) { calls++; return true, nil })
			p.RangeAny(0, func(any, int, bool) (bool, error) { calls++; return true, nil })
			done <- calls
		}()
		select {
		case calls := <-done:
			if calls != 0 {
				t.Errorf("%s: %d items from a stream of nothing", name, calls)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: ranging over a stream of nothing does not end", name)
		}
	}
}

func TestFixPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		size    int
		want    string
		wantLen int
	}{
		{"trimmed", "abcdef", 3, "abc", 3},
		{"as long as the size", "abc", 3, "abc", 3},
		{"extended", "abc", 8, "abc", 8},
		{"generated", "", 6, "", 6},
		{"no size", "abc", 0, "abc", 3},
		{"negative size", "abc", -1, "abc", 3},
		{"no size and no payload", "", 0, "", 0},
	}
	for _, tt := range tests {
		got := FixPayload([]byte(tt.payload), tt.size)
		if len(got) != tt.wantLen || !strings.HasPrefix(string(got), tt.want) {
			t.Errorf("%s: FixPayload(%q, %d) = %q, want %d bytes starting with %q", tt.name, tt.payload, tt.size, got, tt.wantLen, tt.want)
		}
	}
	if FixPayload(nil, 4) == nil {
		t.Errorf("FixPayload(nil, 4) = nil, want a generated payload")
	}
}

func TestFixPayloadDoesNotChangeTheGivenPayload(t *testing.T) {
	original := []byte("abc")
	extended := FixPayload(original, 10)
	if string(original) != "abc" || len(extended) != 10 {
		t.Errorf("original = %q, extended = %q", original, extended)
	}
	extended[0] = 'X'
	if original[0] != 'a' {
		t.Errorf("the extended payload shares memory with the given one")
	}
}

func rp(bodyRegexes, paths []string) *ResponsePayload {
	setupRoutes()
	r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "", "", bodyRegexes, paths, nil, 0, 0)
	if err != nil {
		panic(err)
	}
	return r
}

func TestBodyMatch(t *testing.T) {
	byKeywords := rp([]string{"alpha", "beta"}, nil)
	byPath := rp(nil, []string{"a={.id}"})
	byOtherPath := rp(nil, []string{"b={.name}"})
	payloads := map[string]*ResponsePayload{"alpha,beta": byKeywords, "a={.id}": byPath, "b={.name}": byOtherPath}
	tests := []struct {
		name     string
		body     string
		want     *ResponsePayload
		captures map[string]string
	}{
		{"keywords", "ALPHA then beta", byKeywords, nil},
		{"path", `{"id":7}`, byPath, map[string]string{"a": "7"}},
		{"other path", `{"name":"n"}`, byOtherPath, map[string]string{"b": "n"}},
		{"keywords before paths", `{"id":7,"text":"alpha beta"}`, byKeywords, nil},
		{"nothing matches", `{"other":1}`, nil, nil},
		{"not JSON and no keywords", `plain text`, nil, nil},
		{"empty", ``, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 32; i++ {
				_, got, captures, matched := getPayloadForBodyMatch(nopBody(tt.body), payloads)
				if got != tt.want || matched != (tt.want != nil) {
					t.Fatalf("round %d: matched %v with %p, want %p", i, matched, got, tt.want)
				}
				for k, v := range tt.captures {
					if captures[k] != v {
						t.Fatalf("round %d: captures = %v, want %v", i, captures, tt.captures)
					}
				}
			}
		})
	}
}

func TestBodyMatchTriesEveryPayload(t *testing.T) {
	// A body that is not JSON can't match paths, but a keyword payload next to them still can.
	byKeywords := rp([]string{"needle"}, nil)
	byPath := rp(nil, []string{"{.id}"})
	payloads := map[string]*ResponsePayload{"a": byPath, "z": byKeywords}
	for i := 0; i < 32; i++ {
		if _, got, _, matched := getPayloadForBodyMatch(nopBody("a needle in plain text"), payloads); !matched || got != byKeywords {
			t.Fatalf("round %d: matched %v with %p, want the keywords payload", i, matched, got)
		}
	}
}

func TestGetPayloadForBodyMatchGivesTheBodyBack(t *testing.T) {
	byKeywords := rp([]string{"needle"}, nil)
	payloads := map[string]*ResponsePayload{"needle": byKeywords}
	tests := []struct {
		body        string
		wantMatched bool
	}{
		{"has a needle", true},
		{"has nothing", false},
	}
	for _, tt := range tests {
		reader, got, _, matched := getPayloadForBodyMatch(io.NopCloser(strings.NewReader(tt.body)), payloads)
		if matched != tt.wantMatched || (got != nil) != tt.wantMatched {
			t.Errorf("body %q: matched %v with %v", tt.body, matched, got)
		}
		if reader == nil {
			t.Fatalf("body %q: no reader was returned, so the body that was read is lost", tt.body)
		}
		if again, _ := io.ReadAll(reader); string(again) != tt.body {
			t.Errorf("body %q: the reader gives %q", tt.body, again)
		}
	}
	if reader, got, _, matched := getPayloadForBodyMatch(io.NopCloser(strings.NewReader("x")), nil); reader != nil || got != nil || matched {
		t.Errorf("without payloads: %v %v %v, want nothing", reader, got, matched)
	}
	if reader, got, _, matched := getPayloadForBodyMatch(nil, payloads); reader != nil || got != nil || matched {
		t.Errorf("without a body: %v %v %v, want nothing", reader, got, matched)
	}
}

func TestGetPayloadForKV(t *testing.T) {
	anyValue := rp(nil, nil)
	exact := rp(nil, nil)
	other := rp(nil, nil)
	payloads := map[string]map[string]*ResponsePayload{
		"x-env":   {"": anyValue, "prod": exact},
		"x-other": {"v": other},
	}
	tests := []struct {
		name string
		kv   map[string][]string
		want *ResponsePayload
	}{
		{"exact value", map[string][]string{"X-Env": {"prod"}}, exact},
		{"exact value in another case", map[string][]string{"x-ENV": {"PROD"}}, exact},
		{"any value", map[string][]string{"X-Env": {"dev"}}, anyValue},
		{"one of several values", map[string][]string{"X-Env": {"dev", "prod"}}, exact},
		{"another key", map[string][]string{"X-Other": {"v"}}, other},
		{"another key with another value", map[string][]string{"X-Other": {"w"}}, nil},
		{"unknown key", map[string][]string{"X-Unknown": {"v"}}, nil},
		{"key with no values", map[string][]string{"X-Env": nil}, anyValue},
		{"nothing", nil, nil},
	}
	for _, tt := range tests {
		got, found := getPayloadForKV(tt.kv, payloads)
		if got != tt.want || found != (tt.want != nil) {
			t.Errorf("%s: got %p (found %v), want %p", tt.name, got, found, tt.want)
		}
	}
	if got, found := getPayloadForKV(map[string][]string{"X-Env": {"prod"}}, nil); got != nil || found {
		t.Errorf("without payloads: %v %v", got, found)
	}
}
