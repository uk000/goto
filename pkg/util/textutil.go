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
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	BeforeRegex        = `(.*[\s\(\)\[\]\{\}])?`
	AfterRegex         = `([\s\(\)\[\]\{\}].*)?`
	EmbeddedJSON       = regexp.MustCompile(`\{[^}]*\}`)
	PortHint           = regexp.MustCompile(`(?i)\bport\s*(\d+)`)
	StatusHint         = regexp.MustCompile(`(?i)\bstatus\s*(\d+)`)
	TargetHint         = regexp.MustCompile(`(?i)\b(to|on|with)\s+(\w+)(\s*)`)
	InputHint          = regexp.MustCompile(`(?i)\b(\S*):\[(.*)\]`)
	FeatureHint        = regexp.MustCompile(`(?i)\b(show|with|use)\s+(\w+)(\s*)`)
	NotFeatureHint     = regexp.MustCompile(`(?i)\b(hide|without|not|no)\s+(\w+)(\s*)`)
	DigitRegexp        = regexp.MustCompile(`(\d+)`)
	DurationRegexp     = regexp.MustCompile(`(?i)(\d+)\s*(s|sec|m|min|h|hour)s?\b`)
	normalizedDuration = map[string]string{"sec": "s", "min": "s", "hour": "s"}
)

func ExtractEmbeddedJSONs(text string) (input string, jsons []map[string]any) {
	matches := EmbeddedJSON.FindAll([]byte(text), -1)
	for _, b := range matches {
		jsons = append(jsons, JSONFromBytes(b).Object())
	}
	input = string(EmbeddedJSON.ReplaceAll([]byte(text), []byte("")))
	return
}

func ExtractPortHint(text string) (string, string) {
	matches := PortHint.FindStringSubmatch(text)
	if len(matches) > 1 {
		if _, err := strconv.Atoi(matches[1]); err == nil {
			return text, matches[1]
		}
	}
	return text, ""
}

func ExtractStatusHint(text string) (string, int) {
	matches := StatusHint.FindStringSubmatch(text)
	if len(matches) > 1 {
		if status, err := strconv.Atoi(matches[1]); err == nil {
			return text, status
		}
	}
	return text, 0
}

func ExtractTargetHint(text string) (string, string) {
	matches := TargetHint.FindStringSubmatch(text)
	if len(matches) > 2 {
		text = string(TargetHint.ReplaceAll([]byte(text), []byte("")))
		return text, matches[2]
	}
	return text, ""
}

func ExtractInputHint(text string) (string, map[string]string) {
	inputs := map[string]string{}
	matches := InputHint.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		inputs[m[1]] = m[2]
		pattern := fmt.Sprintf("%s:[%s]", m[1], m[2])
		text = strings.Replace(text, pattern, m[1], 1)
	}
	return text, inputs
}

func ExtractNumberHint(text string) (string, int) {
	match := DigitRegexp.FindString(text)
	if match != "" {
		val, _ := strconv.Atoi(match)
		return text, val
	}
	return text, 0
}

func ExtractDurationHint(text string) (string, time.Duration) {
	matches := DurationRegexp.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 || len(matches[0]) == 0 {
		return text, 0
	}
	text = string(DurationRegexp.ReplaceAll([]byte(text), []byte("")))
	val := matches[0][1]
	unit := strings.ToLower(matches[0][2])
	if nu, ok := normalizedDuration[unit]; ok {
		unit = nu
	}
	return text, ParseDuration(val + unit)
}

func ExtractFeatureHint(text string) (string, string, bool) {
	notMatches := NotFeatureHint.FindStringSubmatch(text)
	if len(notMatches) > 2 {
		text = strings.ReplaceAll(text, notMatches[0], "")
		return text, notMatches[2], false
	}
	matches := FeatureHint.FindStringSubmatch(text)
	if len(matches) > 2 {
		text = strings.ReplaceAll(text, matches[0], "")
		return text, matches[2], true
	}
	return text, "", false
}

func SplitTextIntoChunks(text string, chunkSize int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	chunks := []string{}
	currentChunk := ""
	for _, word := range words {
		if len(currentChunk) > 0 && len(currentChunk)+len(word)+1 > chunkSize {
			chunks = append(chunks, currentChunk)
			currentChunk = word
		} else {
			if len(currentChunk) > 0 {
				currentChunk += " "
			}
			currentChunk += word
		}
	}
	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}
	if len(chunks) < 3 && len(text) > 15 {
		return SplitAtSentenceBoundaries(text, 3)
	}
	return chunks
}
func SplitAtSentenceBoundaries(text string, targetChunks int) []string {
	delimiters := []string{". ", "! ", "? ", "\n\n", "; "}
	if len(text) < 30 {
		return []string{text}
	}
	var splitPoints []int
	for _, delimiter := range delimiters {
		idx := 0
		for {
			found := strings.Index(text[idx:], delimiter)
			if found == -1 {
				break
			}
			splitPoint := idx + found + len(delimiter)
			splitPoints = append(splitPoints, splitPoint)
			idx = splitPoint
		}
	}
	sort.Ints(splitPoints)
	if len(splitPoints) < targetChunks-1 {
		chunkSize := len(text) / targetChunks
		chunks := make([]string, targetChunks)
		for i := 0; i < targetChunks-1; i++ {
			chunks[i] = text[i*chunkSize : (i+1)*chunkSize]
		}
		chunks[targetChunks-1] = text[(targetChunks-1)*chunkSize:]
		return chunks
	}
	selectedPoints := make([]int, targetChunks-1)
	step := len(splitPoints) / targetChunks
	for i := 0; i < targetChunks-1; i++ {
		index := min((i+1)*step, len(splitPoints)-1)
		selectedPoints[i] = splitPoints[index]
	}
	sort.Ints(selectedPoints)
	chunks := make([]string, targetChunks)
	startIdx := 0
	for i, point := range selectedPoints {
		chunks[i] = text[startIdx:point]
		startIdx = point
	}
	chunks[targetChunks-1] = text[startIdx:]
	return chunks
}

func ReverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func AnyToBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int:
		return v != 0
	case string:
		b, _ := strconv.ParseBool(v)
		return b
	case nil:
		return false
	default:
		return false
	}
}

func AnyToInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case string:
		i, _ := strconv.Atoi(v)
		return i
	case nil:
		return 0
	default:
		return 0
	}
}
