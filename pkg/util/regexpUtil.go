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
	muxTemplate             = regexp.MustCompile(`\(\?P<(v\d+)>(?:\(\?i\))?\{([^:]+):([^\}]+)\}\)`)
)

// IsFiller reports whether key contains a {filler} placeholder.
func IsFiller(key string) bool {
	return fillerRegexp.MatchString(key)
}

// MarkFiller wraps key in braces to form a {filler} placeholder.
func MarkFiller(key string) string {
	return "{" + key + "}"
}

// UnmarkFiller strips the leading { and trailing } from a placeholder.
func UnmarkFiller(key string) string {
	key = strings.TrimPrefix(key, "{")
	key = strings.TrimSuffix(key, "}")
	return key
}

// UnmarkAllFillers removes all braces from text.
func UnmarkAllFillers(text string) string {
	text = strings.ReplaceAll(text, "{", "")
	text = strings.ReplaceAll(text, "}", "")
	return text
}

// ReplaceFillersWithRegex replaces each {filler} placeholder in text with the ".*" wildcard regex.
func ReplaceFillersWithRegex(text string) string {
	fillers := GetFillers(text)
	for _, filler := range fillers {
		text = strings.ReplaceAll(text, filler, ".*")
	}
	return text
}

// ReplaceFillersWithCaptureGroupRegex replaces each {filler} with a capture group and returns the placeholders in order along with the compiled regexp.
func ReplaceFillersWithCaptureGroupRegex(text string) ([]string, *regexp.Regexp) {
	fillers := GetFillers(text)
	captures := []string{}
	for _, filler := range fillers {
		text = strings.ReplaceAll(text, filler, "(.+)")
		captures = append(captures, filler)
	}
	return captures, regexp.MustCompile(text)
}

// GetCaptureGroupValues matches input against re and maps each capture name to its captured value, returning nil if there is no match.
func GetCaptureGroupValues(re *regexp.Regexp, captures []string, input string) map[string]string {
	matches := re.FindStringSubmatch(input)
	if len(matches) != len(captures)+1 {
		return nil
	}
	captureValues := map[string]string{}
	for i, c := range captures {
		captureValues[c] = matches[i+1]
	}
	return captureValues
}

// GetFillers returns all {filler} placeholders found in text.
func GetFillers(text string) []string {
	return fillerRegexp.FindAllString(text, -1)
}

// GetFiller returns the first {filler} placeholder found in text, if any.
func GetFiller(text string) (string, bool) {
	if filler := fillerRegexp.FindString(text); filler != "" {
		return filler, true
	}
	return "", false
}

// GetFillersUnmarked returns all placeholders found in text without their braces.
func GetFillersUnmarked(text string) []string {
	matches := GetFillers(text)
	for i, m := range matches {
		matches[i] = UnmarkFiller(m)
	}
	return matches
}

// GetFillerUnmarked returns the first placeholder found in text without its braces, if any.
func GetFillerUnmarked(text string) (string, bool) {
	fillers := GetFillersUnmarked(text)
	if len(fillers) > 0 {
		return fillers[0], true
	}
	return "", false
}

// Fill replaces every occurrence of filler in text with value.
func Fill(text, filler, value string) string {
	return strings.ReplaceAll(text, filler, value)
}

// FillFrom replaces filler in text with the value stored under its unmarked key, if present.
func FillFrom(text, filler string, store map[string]interface{}) string {
	key := UnmarkFiller(filler)
	if value := store[key]; value != nil {
		text = strings.ReplaceAll(text, filler, fmt.Sprint(value))
	}
	return text
}

// FillValues replaces each {key} placeholder in text with its non-empty value from values.
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

// SubstitutePayloadMarkers replaces the {key} markers of the given keys in payload with their non-empty values.
func SubstitutePayloadMarkers(payload string, keys []string, values map[string]string) string {
	for _, key := range keys {
		if values[key] != "" {
			payload = strings.Replace(payload, MarkFiller(key), values[key], -1)
		}
	}
	return payload
}

// Unglob strips the trailing glob asterisk from s (removing all asterisks) and reports whether there was one.
func Unglob(s string) (string, bool) {
	glob := false
	if strings.HasSuffix(s, "*") {
		s = strings.ReplaceAll(s, "*", "")
		glob = true
	}
	return s, glob
}

// DeMux rewrites gorilla/mux generated named groups of the form (?P<vN>{name:regex}) into plain (?P<vN>regex) groups.
func DeMux(muxRegex string) string {
	return muxTemplate.ReplaceAllString(muxRegex, "(?P<${1}>${3})")
}

// Prepare compiles the Regex or InRegex patterns for later matching.
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

// Match tests val against the first configured criterion, in order of Exact, In, Regex and InRegex.
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
