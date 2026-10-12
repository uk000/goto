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

package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// markdownTrimKey is the key the locker uses to mark trimmed (not shown) sub-keys.
const markdownTrimKey = "..."

// markdownDataKey is the key under which a locker entry holds its own data.
const markdownDataKey = "data"

// renderLockersMarkdown renders a map of label -> locker as markdown. Each locker gets a
// heading, each top-level key in the locker gets a paragraph, lists are rendered as bulleted
// lists and nested content as indented sub-bullets.
func renderLockersMarkdown(lockers any) string {
	data := toGeneric(lockers)
	sb := &strings.Builder{}
	byLabel, _ := data.(map[string]any)
	for i, label := range sortedKeys(byLabel) {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(sb, "## %s\n", label)
		locker, _ := byLabel[label].(map[string]any)
		if len(locker) == 0 {
			sb.WriteString("_empty_\n")
			continue
		}
		for j, key := range sortedKeys(locker) {
			if j > 0 {
				// blank line only between paragraphs, so they don't merge when rendered
				sb.WriteString("\n")
			}
			writeMarkdownParagraph(sb, key, normalizeMarkdownValue(locker[key]))
		}
	}
	return sb.String()
}

func writeMarkdownParagraph(sb *strings.Builder, key string, v any) {
	if key == markdownTrimKey {
		fmt.Fprintf(sb, "**%s**\n", key)
		return
	}
	switch val := v.(type) {
	case map[string]any:
		fmt.Fprintf(sb, "**%s**\n", key)
		writeMarkdownMap(sb, val, 0)
	case []any:
		fmt.Fprintf(sb, "**%s**\n", key)
		writeMarkdownList(sb, val, 0)
	case nil:
		fmt.Fprintf(sb, "**%s**\n", key)
	default:
		writeMarkdownKeyedScalar(sb, fmt.Sprintf("**%s**", key), "", val)
	}
}

func writeMarkdownMap(sb *strings.Builder, m map[string]any, depth int) {
	indent := strings.Repeat("  ", depth)
	if d, found := m[markdownDataKey]; found {
		// a locker's own data is printed first, as just its value without the "data" key
		writeMarkdownData(sb, normalizeMarkdownValue(d), depth)
	}
	for _, key := range sortedKeys(m) {
		if key == markdownDataKey {
			continue
		}
		if key == markdownTrimKey {
			// "..." marks trimmed content, print just the marker
			fmt.Fprintf(sb, "%s- %s\n", indent, key)
			continue
		}
		switch val := normalizeMarkdownValue(m[key]).(type) {
		case map[string]any:
			fmt.Fprintf(sb, "%s- %s\n", indent, key)
			writeMarkdownMap(sb, val, depth+1)
		case []any:
			fmt.Fprintf(sb, "%s- %s\n", indent, key)
			writeMarkdownList(sb, val, depth+1)
		case nil:
			fmt.Fprintf(sb, "%s- %s\n", indent, key)
		default:
			writeMarkdownKeyedScalar(sb, fmt.Sprintf("%s- %s", indent, key), indent+"  ", val)
		}
	}
}

// writeMarkdownData prints the value of a "data" key at the given depth, without the key itself.
func writeMarkdownData(sb *strings.Builder, v any, depth int) {
	indent := strings.Repeat("  ", depth)
	switch val := v.(type) {
	case map[string]any:
		writeMarkdownMap(sb, val, depth)
	case []any:
		writeMarkdownList(sb, val, depth)
	case nil:
	default:
		if text := markdownScalar(val); text != "" {
			writeMarkdownLines(sb, indent+"- ", indent+"  ", text)
		}
	}
}

func writeMarkdownList(sb *strings.Builder, list []any, depth int) {
	indent := strings.Repeat("  ", depth)
	for i, item := range list {
		switch val := normalizeMarkdownValue(item).(type) {
		case map[string]any:
			fmt.Fprintf(sb, "%s- item %d\n", indent, i+1)
			writeMarkdownMap(sb, val, depth+1)
		case []any:
			fmt.Fprintf(sb, "%s- item %d\n", indent, i+1)
			writeMarkdownList(sb, val, depth+1)
		case nil:
			fmt.Fprintf(sb, "%s- \n", indent)
		default:
			writeMarkdownLines(sb, indent+"- ", indent+"  ", markdownScalar(val))
		}
	}
}

// writeMarkdownKeyedScalar prints "<header>:" followed by the value on the following line(s),
// each indented with valueIndent. Newlines in the value are retained.
func writeMarkdownKeyedScalar(sb *strings.Builder, header, valueIndent string, v any) {
	text := markdownScalar(v)
	if text == "" {
		fmt.Fprintf(sb, "%s\n", header)
		return
	}
	// trailing double space is a markdown hard line break, so the value starts on a new line
	// even when the output is rendered, and is invisible when read as plain text
	fmt.Fprintf(sb, "%s:  \n", header)
	writeMarkdownLines(sb, valueIndent, valueIndent, text)
}

// writeMarkdownLines writes each line of text on its own line, the first prefixed with
// firstPrefix and the rest with restPrefix. Lines are ended with a markdown hard line break
// so retained newlines also show up as line breaks when rendered.
func writeMarkdownLines(sb *strings.Builder, firstPrefix, restPrefix, text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			sb.WriteString(firstPrefix)
		} else {
			sb.WriteString(restPrefix)
		}
		sb.WriteString(line)
		if i < len(lines)-1 && line != "" && lines[i+1] != "" {
			sb.WriteString("  ")
		}
		sb.WriteString("\n")
	}
}

// normalizeMarkdownValue unwraps locker entries that carry only a "data" field, and expands
// string values holding a JSON object/array (locker data is stored as raw strings).
func normalizeMarkdownValue(v any) any {
	if m, ok := v.(map[string]any); ok {
		_, hasFirst := m["firstReported"].(string)
		_, hasLast := m["lastReported"].(string)
		if hasFirst || hasLast {
			// drop locker timestamps, they are metadata rather than content
			stripped := make(map[string]any, len(m))
			for k, val := range m {
				if k != "firstReported" && k != "lastReported" {
					stripped[k] = val
				}
			}
			m = stripped
			v = m
		}
		if d, found := m["data"]; found && d == nil {
			// a nil "data" has nothing to print
			trimmed := make(map[string]any, len(m))
			for k, val := range m {
				if k != "data" {
					trimmed[k] = val
				}
			}
			m = trimmed
			v = m
		}
		if d, found := m["data"]; found && len(m) == 1 {
			v = d
		}
	}
	if s, ok := v.(string); ok {
		if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			var parsed any
			dec := json.NewDecoder(strings.NewReader(t))
			dec.UseNumber()
			if err := dec.Decode(&parsed); err == nil {
				return parsed
			}
		}
	}
	return v
}

func markdownScalar(v any) string {
	s := fmt.Sprint(v)
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

func toGeneric(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
