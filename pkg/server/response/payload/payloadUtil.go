/**
 * Copyright 2025 uk
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
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/util"
	"io"
	"regexp"
	"strings"
	"time"
)

type Payload struct {
	IsStream    bool        `json:"isStream,omitempty"`
	StreamCount int         `json:"streamCount,omitempty"`
	Delay       *util.Delay `json:"delay,omitempty"`
	JSON        util.JSON   `json:"json,omitempty"`
	Text        string      `json:"text,omitempty"`
	Raw         any         `json:"raw,omitempty"`
	JSONStream  []util.JSON `json:"jsonStream,omitempty"`
	TextStream  []string    `json:"textStream,omitempty"`
	RawStream   []any       `json:"rawStream,omitempty"`
}

func NewJSONPayload(json util.JSON, raw []byte, delayMin, delayMax time.Duration, delayCount int) *Payload {
	if json == nil && len(raw) > 0 {
		json = util.JSONFromBytes(raw)
	}
	return &Payload{
		Delay: util.NewDelay(delayMin, delayMax, delayCount),
		JSON:  json,
	}
}

func NewRawPayload(raw []byte, text string, delayMin, delayMax time.Duration, delayCount int) *Payload {
	return &Payload{
		Delay: util.NewDelay(delayMin, delayMax, delayCount),
		Text:  text,
		Raw:   raw,
	}
}

func NewStreamJSONPayload(jsonArr []util.JSON, raw []byte, streamCount int, delayMin, delayMax time.Duration, delayCount int) *Payload {
	if jsonArr == nil && len(raw) > 0 {
		jsonArr = util.JSONFromBytes(raw).ToJSONArray()
	}
	streamPayload := []util.JSON{}
	for i := 0; i < streamCount; {
		for _, v := range jsonArr {
			streamPayload = append(streamPayload, v)
			i++
			if i >= streamCount {
				break
			}
		}
	}
	return &Payload{
		IsStream:    true,
		StreamCount: streamCount,
		Delay:       util.NewDelay(delayMin, delayMax, delayCount),
		JSONStream:  streamPayload,
	}
}

func NewStreamTextPayload(textArr []string, b []byte, streamCount int, delayMin, delayMax time.Duration, delayCount int) *Payload {
	if textArr == nil {
		textArr = util.ReadStringArray(b)
	}
	streamPayload := []string{}
	if streamCount <= 0 {
		streamCount = len(textArr)
	}
	for i := 0; i < streamCount; {
		for _, v := range textArr {
			streamPayload = append(streamPayload, v)
			i++
			if i >= streamCount {
				break
			}
		}
	}
	return &Payload{
		IsStream:    true,
		StreamCount: streamCount,
		Delay:       util.NewDelay(delayMin, delayMax, delayCount),
		TextStream:  streamPayload,
	}
}

func NewRawStreamPayload(raw []any, strArr []string, byteArr [][]byte, streamCount int, delayMin, delayMax time.Duration, delayCount int) *Payload {
	streamPayload := []any{}
	payload := []any{}
	if raw != nil {
		for _, r := range raw {
			payload = append(payload, r)
		}
	} else if strArr != nil {
		for _, s := range strArr {
			payload = append(payload, s)
		}
	} else {
		for _, b := range byteArr {
			payload = append(payload, b)
		}
	}
	for i := 0; i < streamCount; {
		for _, v := range payload {
			streamPayload = append(streamPayload, v)
			i++
			if i >= streamCount {
				break
			}
		}
	}
	return &Payload{
		IsStream:    true,
		StreamCount: streamCount,
		Delay:       util.NewDelay(delayMin, delayMax, delayCount),
		RawStream:   streamPayload,
	}
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

func (p *Payload) RangeText(f func(text string)) {
	if p.JSONStream != nil {
		for _, j := range p.JSONStream {
			f(j.ToJSONText())
		}
	} else if p.TextStream != nil {
		for _, t := range p.TextStream {
			f(t)
		}
	} else if p.RawStream != nil {
		for _, r := range p.RawStream {
			if s, ok := r.(string); ok {
				f(s)
			} else if b, ok := r.([]byte); ok {
				f(string(b))
			} else if j, ok := r.(util.JSON); ok {
				f(j.ToJSONText())
			} else {
				f(fmt.Sprintf("%v", r))
			}
		}
	} else if p.JSON != nil {
		f(p.JSON.ToJSONText())
	} else if p.Text != "" {
		f(p.Text)
	} else if p.Raw != nil {
		if s, ok := p.Raw.(string); ok {
			f(s)
		} else if b, ok := p.Raw.([]byte); ok {
			f(string(b))
		} else if j, ok := p.Raw.(util.JSON); ok {
			f(j.ToJSONText())
		} else {
			f(fmt.Sprintf("%v", p.Raw))
		}
	}
}

func (p *Payload) RangeAny(f func(data any)) {
	if p.JSONStream != nil {
		for _, j := range p.JSONStream {
			f(j)
		}
	} else if p.TextStream != nil {
		for _, t := range p.TextStream {
			f(t)
		}
	} else if p.RawStream != nil {
		for _, r := range p.RawStream {
			f(r)
		}
	} else if p.JSON != nil {
		f(p.JSON)
	} else if p.Text != "" {
		f(p.Text)
	} else if p.Raw != nil {
		f(p.Raw)
	}
}

func newResponsePayload(payload []byte, stream, binary bool, contentType, uri, header, query, value string,
	bodyRegexes []string, paths []string, transforms []*util.Transform) (*ResponsePayload, error) {
	if contentType == "" {
		contentType = ContentTypeJSON
	}
	_, uriRegExp, responseRouter, err := util.BuildURIMatcher(uri, handleURI)
	if err != nil {
		return nil, fmt.Errorf("failed to add URI match %s with error: %s\n", uri, err.Error())
	}
	headerValueMatch := ""
	headerCaptureKey := ""
	queryValueMatch := ""
	queryCaptureKey := ""
	if util.IsFiller(value) {
		if header != "" {
			headerCaptureKey = value
		} else if query != "" {
			queryCaptureKey = value
		}
	} else if header != "" {
		headerValueMatch = value
	} else if query != "" {
		queryValueMatch = value
	}

	jsonPaths := util.NewJSONPath().Parse(paths)

	var bodyMatchRegexp *regexp.Regexp
	if len(bodyRegexes) > 0 {
		bodyMatchRegexp = regexp.MustCompile("(?i)" + strings.Join(bodyRegexes, ".*") + ".*")
	}

	var fillers []string
	if !binary {
		fillers = util.GetFillersUnmarked(string(payload))
	}
	for _, t := range transforms {
		for _, m := range t.Mappings {
			m.Init()
		}
	}
	return &ResponsePayload{
		Payload:          payload,
		ContentType:      contentType,
		IsStream:         stream,
		IsBinary:         util.IsBinaryContentType(contentType),
		URIMatch:         uri,
		HeaderMatch:      header,
		HeaderValueMatch: headerValueMatch,
		QueryMatch:       query,
		QueryValueMatch:  queryValueMatch,
		BodyMatch:        bodyRegexes,
		BodyPaths:        jsonPaths.TextPaths,
		uriRegexp:        uriRegExp,
		queryMatchRegexp: regexp.MustCompile("(?i)" + query),
		bodyMatchRegexp:  bodyMatchRegexp,
		bodyJsonPaths:    jsonPaths.Paths,
		URICaptureKeys:   util.GetFillersUnmarked(uri),
		HeaderCaptureKey: headerCaptureKey,
		QueryCaptureKey:  queryCaptureKey,
		Transforms:       transforms,
		fillers:          fillers,
		router:           responseRouter,
	}, nil
}

func (rp *ResponsePayload) PrepareStreamPayload(count int, delayMin, delayMax time.Duration) {
	rp.StreamCount = count
	rp.StreamDelayMin = delayMin
	rp.StreamDelayMax = delayMax
	json := util.JSONFromJSONText(string(rp.Payload))
	jsonArray := json.ToJSONArray()
	b := [][]byte{}
	if len(jsonArray) > 0 {
		for i := 0; i < count; {
			for _, v := range jsonArray {
				b = append(b, util.ToBytes(v))
				i++
				if i >= count {
					break
				}
			}
		}
	}
	rp.StreamPayload = b
}

func getPayloadForBodyMatch(bodyReader io.ReadCloser, bodyMatchResponses map[string]*ResponsePayload) (newBodyReader io.ReadCloser, matchedResponsePayload *ResponsePayload, captures map[string]string, matched bool) {
	if len(bodyMatchResponses) == 0 {
		return nil, nil, nil, false
	}
	body := util.Read(bodyReader)
	lowerBody := strings.ToLower(body)
	for _, rp := range bodyMatchResponses {
		if rp.bodyMatchRegexp != nil && rp.bodyMatchRegexp.MatchString(lowerBody) {
			matchedResponsePayload = rp
			break
		} else if len(rp.bodyJsonPaths) > 0 {
			allMatched := true
			captures = map[string]string{}
			var data map[string]interface{}
			if err := util.ReadJson(body, &data); err == nil {
				for key, jp := range rp.bodyJsonPaths {
					if matches, err := jp.FindResults(data); err == nil && len(matches) > 0 && len(matches[0]) > 0 {
						if key != "" {
							captures[key] = fmt.Sprintf("%v", matches[0][0].Interface())
						}
					} else {
						allMatched = false
						break
					}
				}
			} else {
				allMatched = false
				break
			}
			if allMatched {
				matchedResponsePayload = rp
				break
			}
		}
	}
	newBodyReader = io.NopCloser(strings.NewReader(body))
	if matchedResponsePayload != nil {
		return newBodyReader, matchedResponsePayload, captures, true
	}
	return nil, nil, nil, false
}

func getPayloadForKV(kvMap map[string][]string, payloadMap map[string]map[string]*ResponsePayload) (*ResponsePayload, bool) {
	if len(kvMap) == 0 || len(payloadMap) == 0 {
		return nil, false
	}
	for k, kv := range kvMap {
		k = strings.ToLower(k)
		if payloadMap[k] != nil {
			for _, v := range kv {
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

func fixPayload(payload []byte, size int) []byte {
	if len(payload) == 0 && size > 0 {
		payload = util.GenerateRandomPayload(size)
	} else if len(payload) > size {
		payload = payload[:size]
	}
	return payload
}
