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

package grpcserver

import (
	"strings"
	"sync"
)

// ExtAuthzRule configures how the ext authz service responds to requests
// carrying a given header. A rule with only a header matches on presence of
// the header; a rule with both header and value additionally requires an
// exact value match. The special header name "*" matches any request.
type ExtAuthzRule struct {
	Header string `json:"header"`
	Value  string `json:"value,omitempty"`
	Action string `json:"action"` // "allow" or "deny"
}

func (r ExtAuthzRule) matches(headers map[string]string) bool {
	if r.Header == "*" {
		return true
	}
	value, ok := headers[r.Header]
	if !ok {
		return false
	}
	return r.Value == "" || value == r.Value
}

var (
	extAuthzRules    = []ExtAuthzRule{}
	extAuthzRuleLock = sync.RWMutex{}
)

func StoreExtAuthzRule(rule *ExtAuthzRule) bool {
	if rule.Header == "" || (rule.Action != "allow" && rule.Action != "deny") {
		return false
	}
	rule.Header = strings.ToLower(rule.Header)
	extAuthzRuleLock.Lock()
	defer extAuthzRuleLock.Unlock()
	// replace any existing rule for the same header+value
	for i, existing := range extAuthzRules {
		if existing.Header == rule.Header && existing.Value == rule.Value {
			extAuthzRules[i] = *rule
			return true
		}
	}
	extAuthzRules = append(extAuthzRules, *rule)
	return true
}

func GetExtAuthzRules() []ExtAuthzRule {
	extAuthzRuleLock.RLock()
	defer extAuthzRuleLock.RUnlock()
	rules := make([]ExtAuthzRule, len(extAuthzRules))
	copy(rules, extAuthzRules)
	return rules
}

func RemoveExtAuthzRule(header, value string) bool {
	header = strings.ToLower(header)
	extAuthzRuleLock.Lock()
	defer extAuthzRuleLock.Unlock()
	for i, existing := range extAuthzRules {
		if existing.Header == header && existing.Value == value {
			extAuthzRules = append(extAuthzRules[:i], extAuthzRules[i+1:]...)
			return true
		}
	}
	return false
}

func ClearExtAuthzRules() {
	extAuthzRuleLock.Lock()
	defer extAuthzRuleLock.Unlock()
	extAuthzRules = []ExtAuthzRule{}
}

// matchExtAuthzRule returns the action of the first rule matching the given
// request headers, or "allow" when no rule matches
func matchExtAuthzRule(headers map[string]string) string {
	extAuthzRuleLock.RLock()
	defer extAuthzRuleLock.RUnlock()
	for _, rule := range extAuthzRules {
		if rule.matches(headers) {
			return rule.Action
		}
	}
	return "allow"
}
