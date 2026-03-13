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
	"fmt"
	"regexp"
	"strings"
)

type Match struct {
	Exact   string   `json:"exact"`
	In      []string `json:"in"`
	Regex   string   `json:"regex"`
	InRegex []string `json:"inRegex"`
	re      *regexp.Regexp
	inRe    []*regexp.Regexp
}

var (
	URIPrefixRegexParts     = []string{`^(\/port=.*)?`, `((\/)(.*))?(\?.*)?$`}
	QueryParamRegex         = `(\?.*)?$`
	GlobRegex               = `(.*)?`
	fillerRegexp            = regexp.MustCompile("{({[^{}]+?})}|{([^{}]+?)}")
	contentRegexp           = regexp.MustCompile("(?i)content")
	hostRegexp              = regexp.MustCompile("(?i)^host$")
	tunnelRegexp            = regexp.MustCompile("(?i)tunnel")
	utf8Regexp              = regexp.MustCompile("(?i)utf-8")
	knownTextMimeTypeRegexp = regexp.MustCompile(".*(text|html|json|yaml|form).*")
	upgradeRegexp           = regexp.MustCompile("(?i)upgrade")
)

func IsFiller(key string) bool {
	return fillerRegexp.MatchString(key)
}

func MarkFiller(key string) string {
	return "{" + key + "}"
}

func UnmarkFiller(key string) string {
	key = strings.TrimPrefix(key, "{")
	key = strings.TrimSuffix(key, "}")
	return key
}

func GetFillers(text string) []string {
	return fillerRegexp.FindAllString(text, -1)
}

func GetFiller(text string) (string, bool) {
	if filler := fillerRegexp.FindString(text); filler != "" {
		return filler, true
	}
	return "", false
}

func GetFillersUnmarked(text string) []string {
	matches := GetFillers(text)
	for i, m := range matches {
		matches[i] = UnmarkFiller(m)
	}
	return matches
}

func GetFillerUnmarked(text string) (string, bool) {
	fillers := GetFillersUnmarked(text)
	if len(fillers) > 0 {
		return fillers[0], true
	}
	return "", false
}

func Fill(text, filler, value string) string {
	return strings.ReplaceAll(text, filler, value)
}

func FillFrom(text, filler string, store map[string]interface{}) string {
	key := UnmarkFiller(filler)
	if value := store[key]; value != nil {
		text = strings.ReplaceAll(text, filler, fmt.Sprint(value))
	}
	return text
}

func FillValues(text string, values map[string]string) string {
	fillers := GetFillers(text)
	for _, filler := range fillers {
		key := UnmarkFiller(filler)
		value := values[key]
		if value != "" {
			text = strings.ReplaceAll(text, filler, value)
		}
	}
	return text
}

func SubstitutePayloadMarkers(payload string, keys []string, values map[string]string) string {
	for _, key := range keys {
		if values[key] != "" {
			payload = strings.Replace(payload, MarkFiller(key), values[key], -1)
		}
	}
	return payload
}

func Unglob(s string) (string, bool) {
	glob := false
	if strings.HasSuffix(s, "*") {
		s = strings.ReplaceAll(s, "*", "")
		glob = true
	}
	return s, glob
}

func (m *Match) Prepare() {
	if m.Regex != "" {
		m.re = regexp.MustCompile(m.Regex)
	} else if len(m.InRegex) > 0 {
		m.inRe = make([]*regexp.Regexp, len(m.InRegex))
		for i, r := range m.InRegex {
			m.inRe[i] = regexp.MustCompile(r)
		}
	}
}

func (m *Match) Match(val string) bool {
	if m.Exact != "" {
		return m.Exact == val
	} else if len(m.In) > 0 {
		for _, in := range m.In {
			if in == val {
				return true
			}
		}
	} else if m.re != nil {
		return m.re.MatchString(val)
	} else if len(m.inRe) > 0 {
		for _, re := range m.inRe {
			if re.MatchString(val) {
				return true
			}
		}
	}
	return false
}
