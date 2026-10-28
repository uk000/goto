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
	"fmt"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Payload struct {
	IsStream    bool         `yaml:"isStream,omitempty" json:"isStream,omitempty"`
	StreamCount int          `yaml:"streamCount,omitempty" json:"streamCount,omitempty"`
	Delay       *types.Delay `yaml:"delay,omitempty" json:"delay,omitempty"`
	JSON        util.JSON    `yaml:"json,omitempty" json:"json,omitempty"`
	Text        string       `yaml:"text,omitempty" json:"text,omitempty"`
	Raw         any          `yaml:"raw,omitempty" json:"raw,omitempty"`
	JSONStream  []util.JSON  `yaml:"jsonStream,omitempty" json:"jsonStream,omitempty"`
	TextStream  []string     `yaml:"textStream,omitempty" json:"textStream,omitempty"`
	RawStream   []any        `yaml:"rawStream,omitempty" json:"rawStream,omitempty"`
}

type PayloadMarshal struct {
	IsStream    bool             `yaml:"isStream,omitempty" json:"isStream,omitempty"`
	StreamCount int              `yaml:"streamCount,omitempty" json:"streamCount,omitempty"`
	Delay       *types.Delay     `yaml:"delay,omitempty" json:"delay,omitempty"`
	JSON        map[string]any   `yaml:"json,omitempty" json:"json,omitempty"`
	Text        string           `yaml:"text,omitempty" json:"text,omitempty"`
	Raw         any              `yaml:"raw,omitempty" json:"raw,omitempty"`
	JSONStream  []map[string]any `yaml:"jsonStream,omitempty" json:"jsonStream,omitempty"`
	TextStream  []string         `yaml:"textStream,omitempty" json:"textStream,omitempty"`
	RawStream   []any            `yaml:"rawStream,omitempty" json:"rawStream,omitempty"`
}

type TextRangeFunc func(text string, count int, restarted bool) (bool, error)
type DataRangeFunc func(data any, count int, restarted bool) (bool, error)

func NewJSONPayload(json util.JSON, raw []byte, delayMin, delayMax time.Duration, delayCount int) *Payload {
	if json == nil && len(raw) > 0 {
		json = util.JSONFromBytes(raw)
	}
	return &Payload{
		Delay: types.NewDelay(delayMin, delayMax, delayCount),
		JSON:  json,
	}
}

func NewRawPayload(raw []byte, text string, delayMin, delayMax time.Duration, delayCount int) *Payload {
	return &Payload{
		Delay: types.NewDelay(delayMin, delayMax, delayCount),
		Text:  text,
		Raw:   raw,
	}
}

func NewStreamJSONPayload(jsonArr []util.JSON, raw []byte, streamCount int, delayMin, delayMax time.Duration, delayCount int) *Payload {
	payload := &Payload{
		IsStream:    true,
		StreamCount: streamCount,
		Delay:       types.NewDelay(delayMin, delayMax, delayCount),
	}
	payload.prepareJSONStream(jsonArr, nil, nil, raw)
	return payload
}

func NewStreamTextPayload(textArr []string, b []byte, streamCount int, delayMin, delayMax time.Duration, delayCount int) *Payload {
	payload := &Payload{
		IsStream:    true,
		StreamCount: streamCount,
		Delay:       types.NewDelay(delayMin, delayMax, delayCount),
	}
	payload.prepareTextStream(textArr, b)
	return payload
}

func NewRawStreamPayload(raw []any, strArr []string, byteArr [][]byte, streamCount int, delayMin, delayMax time.Duration, delayCount int) *Payload {
	payload := &Payload{
		IsStream:    true,
		StreamCount: streamCount,
		Delay:       types.NewDelay(delayMin, delayMax, delayCount),
	}
	payload.prepareRawStream(raw, strArr, byteArr)
	return payload
}

type payloadYAML Payload

// MarshalYAML marshals the payload as the structure that it is. It used to marshal the payload with yaml.Marshal, which
// calls this method again.
func (p *Payload) MarshalYAML() (any, error) {
	return (*payloadYAML)(p), nil
}

func (p *Payload) UnmarshalYAML(node *yaml.Node) error {
	pm := &PayloadMarshal{}
	if err := node.Decode(&pm); err != nil {
		return err
	}
	return p.Unmarshal(pm)
}

func (p *Payload) UnmarshalJSON(b []byte) error {
	pm := &PayloadMarshal{}
	if err := json.Unmarshal(b, pm); err != nil {
		return err
	}
	return p.Unmarshal(pm)
}

func (p *Payload) Unmarshal(pm *PayloadMarshal) error {
	if pm.StreamCount > 0 || len(pm.JSONStream) > 0 || len(pm.TextStream) > 0 || len(pm.RawStream) > 0 {
		p.StreamCount = pm.StreamCount
		if len(pm.JSONStream) > 0 {
			p.prepareJSONStream(nil, pm.JSONStream, pm.JSON, nil)
		} else if len(pm.TextStream) > 0 {
			p.prepareTextStream(pm.TextStream, nil)
		} else if len(pm.RawStream) > 0 {
			p.prepareRawStream(pm.RawStream, nil, nil)
		}
	} else if len(pm.JSON) > 0 {
		p.JSON = util.JSONFromMap(pm.JSON)
	} else if pm.Text != "" {
		p.Text = pm.Text
	} else {
		p.Raw = pm.Raw
	}
	p.Delay = pm.Delay
	if p.Delay != nil {
		p.Delay.Prepare()
	}
	return nil
}

func (p *Payload) prepareJSONStream(jsonArr []util.JSON, jsonMapArr []map[string]any, jsonMap map[string]any, raw []byte) {
	if len(jsonArr) == 0 && len(jsonMapArr) == 0 && len(raw) == 0 && jsonMap == nil {
		return
	}
	if len(jsonMapArr) > 0 {
		jsonArr = []util.JSON{}
		for _, j := range jsonMapArr {
			jsonArr = append(jsonArr, util.JSONFromMap(j))
		}
	} else if len(jsonArr) == 0 && len(raw) > 0 {
		jsonArr = util.JSONFromBytes(raw).ToJSONArray()
	} else if len(jsonArr) == 0 && len(jsonMap) > 0 {
		jsonArr = util.JSONFromMap(jsonMap).ToJSONArray()
	}
	if p.StreamCount <= 0 {
		p.StreamCount = len(jsonArr)
	}
	p.IsStream = true
	p.JSONStream = jsonArr
}

func (p *Payload) prepareTextStream(textArr []string, b []byte) {
	if textArr == nil {
		textArr = util.ReadStringArray(b)
	}
	if p.StreamCount <= 0 {
		p.StreamCount = len(textArr)
	}
	p.IsStream = true
	p.TextStream = textArr
}

func (p *Payload) prepareRawStream(raw []any, strArr []string, byteArr [][]byte) {
	payload := []any{}
	streamCount := 0
	if raw != nil {
		streamCount = len(raw)
		for _, r := range raw {
			payload = append(payload, r)
		}
	} else if strArr != nil {
		streamCount = len(strArr)
		for _, s := range strArr {
			payload = append(payload, s)
		}
	} else {
		streamCount = len(byteArr)
		for _, b := range byteArr {
			payload = append(payload, b)
		}
	}
	if p.StreamCount <= 0 {
		p.StreamCount = streamCount
	}
	p.IsStream = true
	p.RawStream = payload
}

func (p *Payload) Count() int {
	if p.JSONStream != nil {
		return len(p.JSONStream)
	} else if p.TextStream != nil {
		return len(p.TextStream)
	} else if p.RawStream != nil {
		return len(p.RawStream)
	} else if p.JSON != nil || p.Text != "" || p.Raw != nil {
		return 1
	}
	return 0
}

func (p *Payload) ToText() string {
	output := strings.Builder{}
	if p.Text != "" {
		output.WriteString(p.Text)
	} else if p.JSON != nil {
		output.WriteString(p.JSON.ToJSONText())
	} else if p.Raw != nil {
		if s, ok := p.Raw.(string); ok {
			output.WriteString(s)
		} else if b, ok := p.Raw.([]byte); ok {
			output.Write(b)
		} else if j, ok := p.Raw.(util.JSON); ok {
			output.WriteString(j.ToJSONText())
		} else {
			output.WriteString(fmt.Sprintf("%v", p.Raw))
		}
	} else {
		arr := []string{}
		if p.JSONStream != nil {
			for _, j := range p.JSONStream {
				arr = append(arr, j.ToJSONText())
			}
		} else if p.TextStream != nil {
			for _, t := range p.TextStream {
				arr = append(arr, t)
			}
		} else if p.RawStream != nil {
			for _, r := range p.RawStream {
				if s, ok := r.(string); ok {
					arr = append(arr, s)
				} else if b, ok := r.([]byte); ok {
					arr = append(arr, string(b))
				} else if j, ok := r.(util.JSON); ok {
					arr = append(arr, j.ToJSONText())
				} else {
					arr = append(arr, fmt.Sprintf("%v", r))
				}
			}
		}
		output.WriteString(strings.Join(arr, ", "))
	}
	return output.String()
}

func (p *Payload) sendJSONStream(from, to int, f TextRangeFunc, f2 DataRangeFunc) (err error) {
	if len(p.JSONStream) == 0 {
		// There is nothing to send, however many times it would be repeated.
		return nil
	}
	count := 0
	restarted := false
	cont := true
	if to <= 0 {
		to = p.StreamCount
	}
	for {
		for _, j := range p.JSONStream {
			count++
			if count < from {
				continue
			}
			if f != nil {
				if cont, err = f(j.ToJSONText(), count, restarted); err != nil {
					return err
				}
			} else if f2 != nil {
				if cont, err = f2(j, count, restarted); err != nil {
					return err
				}
			}
			restarted = false
			if !cont || count >= to {
				break
			}
		}
		if !cont || count >= to {
			break
		}
		restarted = true
	}
	return nil
}

func (p *Payload) sendTextStream(from, to int, f TextRangeFunc, f2 DataRangeFunc) (err error) {
	if len(p.TextStream) == 0 {
		// There is nothing to send, however many times it would be repeated.
		return nil
	}
	count := 0
	restarted := false
	cont := true
	if to <= 0 {
		to = p.StreamCount
	}
	for {
		for _, t := range p.TextStream {
			count++
			if count < from {
				continue
			}
			if f != nil {
				if cont, err = f(t, count, restarted); err != nil {
					return err
				}
			} else if f2 != nil {
				if cont, err = f2(t, count, restarted); err != nil {
					return err
				}
			}
			restarted = false
			if !cont || count >= to {
				break
			}
		}
		if !cont || count >= to {
			break
		}
		restarted = true
	}
	return nil
}

func (p *Payload) sendRawStream(from, to int, f TextRangeFunc, f2 DataRangeFunc) (err error) {
	if len(p.RawStream) == 0 {
		// There is nothing to send, however many times it would be repeated.
		return nil
	}
	count := 0
	restarted := false
	cont := true
	if to <= 0 {
		to = p.StreamCount
	}
	for {
		for _, r := range p.RawStream {
			count++
			if count < from {
				continue
			}
			text := ""
			if s, ok := r.(string); ok {
				text = s
			} else if b, ok := r.([]byte); ok {
				text = string(b)
			} else if j, ok := r.(util.JSON); ok {
				text = j.ToJSONText()
			} else {
				text = fmt.Sprintf("%v", r)
			}
			if text != "" {
				if f != nil {
					if cont, err = f(text, count, restarted); err != nil {
						return err
					}
				} else if f2 != nil {
					if cont, err = f2(text, count, restarted); err != nil {
						return err
					}
				}
			}
			restarted = false
			if !cont || count >= to {
				break
			}
		}
		if !cont || count >= to {
			break
		}
		restarted = true
	}
	return nil
}

func (p *Payload) sendRaw(f TextRangeFunc, f2 DataRangeFunc) (err error) {
	text := ""
	if s, ok := p.Raw.(string); ok {
		text = s
	} else if b, ok := p.Raw.([]byte); ok {
		text = string(b)
	} else if j, ok := p.Raw.(util.JSON); ok {
		text = j.ToJSONText()
	} else {
		text = fmt.Sprintf("%v", p.Raw)
	}
	if text != "" {
		if f != nil {
			_, err = f(text, 1, false)
			return
		} else if f2 != nil {
			_, err = f2(text, 1, false)
			return
		}
	}
	return nil
}

func (p *Payload) RangeText(to int, f TextRangeFunc) (err error) {
	return p.RangeTextFrom(0, to, f)
}

func (p *Payload) RangeTextWithDelay(to int, delay *types.Duration, f TextRangeFunc) (err error) {
	d := &types.Delay{Count: -1}
	if p.Delay != nil {
		d = p.Delay.Clone()
	}
	if delay != nil && delay.Abs() != 0 {
		d.Min = delay
		d.Max = delay
	}
	if d.IsNonZero() {
		originalF := f
		delayedF := func(text string, count int, restarted bool) (bool, error) {
			d.ComputeAndApply()
			return originalF(text, count, restarted)
		}
		f = delayedF
	}
	return p.RangeTextFrom(0, to, f)
}

func (p *Payload) RangeTextFrom(from, to int, f TextRangeFunc) (err error) {
	if p.JSONStream != nil {
		return p.sendJSONStream(from, to, f, nil)
	} else if p.TextStream != nil {
		return p.sendTextStream(from, to, f, nil)
	} else if p.RawStream != nil {
		return p.sendRawStream(from, to, f, nil)
	} else if p.JSON != nil {
		_, err = f(p.JSON.ToJSONText(), 1, false)
		return
	} else if p.Text != "" {
		_, err = f(p.Text, 1, false)
		return
	} else if p.Raw != nil {
		return p.sendRaw(f, nil)
	}
	return nil
}

func (p *Payload) RangeAny(to int, f DataRangeFunc) (err error) {
	return p.RangeAnyFrom(0, to, f)
}

func (p *Payload) RangeAnyFrom(from, to int, f DataRangeFunc) (err error) {
	if p.JSONStream != nil {
		return p.sendJSONStream(from, to, nil, f)
	} else if p.TextStream != nil {
		return p.sendTextStream(from, to, nil, f)
	} else if p.RawStream != nil {
		return p.sendRawStream(from, to, nil, f)
	} else if p.JSON != nil {
		_, err = f(p.JSON, 1, false)
		return
	} else if p.Text != "" {
		_, err = f(p.Text, 1, false)
		return
	} else if p.Raw != nil {
		return p.sendRaw(nil, f)
	}
	return nil
}

// getPayloadForBodyMatch reads the body and finds the payload that matches it. The reader that is returned gives the
// body again, whether or not a payload was found.
func getPayloadForBodyMatch(bodyReader io.ReadCloser, bodyMatchResponses map[string]*ResponsePayload) (newBodyReader io.ReadCloser, matchedResponsePayload *ResponsePayload, captures map[string]string, matched bool) {
	if len(bodyMatchResponses) == 0 || bodyReader == nil {
		return nil, nil, nil, false
	}
	body := util.Read(bodyReader)
	matchedResponsePayload, captures, matched = matchBody(body, bodyMatchResponses)
	return io.NopCloser(strings.NewReader(body)), matchedResponsePayload, captures, matched
}

// matchBody finds the payload that matches the body: a payload that is matched with keywords is preferred over one that
// is matched with JSON paths, and the payloads of a kind are tried in the order of their keys, so that the same body
// always gets the same payload. The body is not required to be JSON, unless a payload is matched with JSON paths.
func matchBody(body string, bodyMatchResponses map[string]*ResponsePayload) (*ResponsePayload, map[string]string, bool) {
	keys := make([]string, 0, len(bodyMatchResponses))
	for k := range bodyMatchResponses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lowerBody := strings.ToLower(body)
	for _, k := range keys {
		if rp := bodyMatchResponses[k]; rp != nil && rp.bodyMatchRegexp != nil && rp.bodyMatchRegexp.MatchString(lowerBody) {
			return rp, nil, true
		}
	}
	var data map[string]any
	parsed := false
	for _, k := range keys {
		rp := bodyMatchResponses[k]
		if rp == nil || rp.bodyJsonPath == nil || rp.bodyJsonPath.IsEmpty() {
			continue
		}
		if !parsed {
			if util.ReadJson(body, &data) != nil {
				// No JSON path can be found in a body that is not JSON.
				break
			}
			parsed = true
		}
		if captures, allMatched := rp.bodyJsonPath.FindResults(data); allMatched {
			return rp, captures, true
		}
	}
	return nil, nil, false
}

func getPayloadForKV(kvMap map[string][]string, payloadMap map[string]map[string]*ResponsePayload) (*ResponsePayload, bool) {
	if len(kvMap) == 0 || len(payloadMap) == 0 {
		return nil, false
	}
	// The keys are tried in order, so that a request that matches several payloads always gets the same one.
	keys := make([]string, 0, len(kvMap))
	for k := range kvMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		k := strings.ToLower(key)
		if payloadMap[k] != nil {
			for _, v := range kvMap[key] {
				v = strings.ToLower(v)
				if p, found := payloadMap[k][v]; found {
					return p, found
				}
			}
			if p, found := payloadMap[k][""]; found {
				return p, found
			}
		}
	}
	return nil, false
}

// FixPayload makes the payload as long as the given size, by trimming it, or by appending random characters to it. An
// empty payload is replaced with a random one. A size that is not positive leaves the payload as it is.
func FixPayload(payload []byte, size int) []byte {
	if size <= 0 {
		return payload
	}
	if len(payload) == 0 {
		payload = types.GenerateRandomPayload(size)
	} else if len(payload) > size {
		payload = payload[:size]
	} else if len(payload) < size {
		fixed := make([]byte, 0, size)
		fixed = append(fixed, payload...)
		payload = append(fixed, types.GenerateRandomPayload(size-len(payload))...)
	}
	return payload
}
