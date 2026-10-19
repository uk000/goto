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
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// resetExtAuthz gives a test rules that are empty, and leaves some behind: the rules are kept for the whole process.
func resetExtAuthz(t *testing.T) {
	t.Helper()
	ClearExtAuthzRules()
	t.Cleanup(ClearExtAuthzRules)
}

func store(t *testing.T, header, value, action string) {
	t.Helper()
	if !StoreExtAuthzRule(&ExtAuthzRule{Header: header, Value: value, Action: action}) {
		t.Fatalf("rule %s=%s -> %s was not stored", header, value, action)
	}
}

func TestStoreExtAuthzRuleValidation(t *testing.T) {
	resetExtAuthz(t)
	invalid := []ExtAuthzRule{
		{Header: "", Action: "allow"},
		{Header: "", Value: "v", Action: "deny"},
		{Header: "x-a", Action: ""},
		{Header: "x-a", Action: "block"},
		{Header: "x-a", Action: "ALLOW"},
		{Header: "x-a", Action: " deny"},
	}
	for _, rule := range invalid {
		if StoreExtAuthzRule(&rule) {
			t.Errorf("rule %+v must not be stored", rule)
		}
	}
	if rules := GetExtAuthzRules(); len(rules) != 0 {
		t.Errorf("rules after invalid ones = %+v, want none", rules)
	}
	for _, action := range []string{"allow", "deny"} {
		if !StoreExtAuthzRule(&ExtAuthzRule{Header: "x-a", Value: action, Action: action}) {
			t.Errorf("a rule that %ss must be stored", action)
		}
	}
}

func TestStoreExtAuthzRuleLowercasesHeader(t *testing.T) {
	resetExtAuthz(t)
	rule := &ExtAuthzRule{Header: "X-Mixed-Case", Value: "Value", Action: "deny"}
	if !StoreExtAuthzRule(rule) {
		t.Fatalf("not stored")
	}
	rules := GetExtAuthzRules()
	if len(rules) != 1 || rules[0].Header != "x-mixed-case" {
		t.Errorf("rules = %+v, want the header in lower case", rules)
	}
	if rules[0].Value != "Value" {
		t.Errorf("the value must keep its case: %q", rules[0].Value)
	}
}

func TestStoreExtAuthzRuleReplacesSameHeaderAndValue(t *testing.T) {
	resetExtAuthz(t)
	store(t, "x-a", "1", "allow")
	store(t, "x-b", "", "deny")
	store(t, "X-A", "1", "deny") // the same rule as the first, in other case
	store(t, "x-a", "2", "allow")
	want := []ExtAuthzRule{
		{Header: "x-a", Value: "1", Action: "deny"},
		{Header: "x-b", Action: "deny"},
		{Header: "x-a", Value: "2", Action: "allow"},
	}
	got := GetExtAuthzRules()
	if len(got) != len(want) {
		t.Fatalf("rules = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rule %d = %+v, want %+v (a replaced rule keeps its place)", i, got[i], want[i])
		}
	}
}

func TestExtAuthzRuleMatches(t *testing.T) {
	headers := map[string]string{"x-a": "1", "x-empty": "", "x-b": "two"}
	tests := []struct {
		name string
		rule ExtAuthzRule
		in   map[string]string
		want bool
	}{
		{"any request", ExtAuthzRule{Header: "*"}, headers, true},
		{"any request, no headers", ExtAuthzRule{Header: "*"}, nil, true},
		{"present header", ExtAuthzRule{Header: "x-a"}, headers, true},
		{"present header, empty value", ExtAuthzRule{Header: "x-empty"}, headers, true},
		{"absent header", ExtAuthzRule{Header: "x-none"}, headers, false},
		{"absent header, no headers", ExtAuthzRule{Header: "x-a"}, nil, false},
		{"exact value", ExtAuthzRule{Header: "x-b", Value: "two"}, headers, true},
		{"other value", ExtAuthzRule{Header: "x-b", Value: "three"}, headers, false},
		{"value is exact, not a prefix", ExtAuthzRule{Header: "x-b", Value: "tw"}, headers, false},
		{"value is case sensitive", ExtAuthzRule{Header: "x-b", Value: "TWO"}, headers, false},
		{"value of a header that is absent", ExtAuthzRule{Header: "x-none", Value: "1"}, headers, false},
		{"header names of the request are lower case", ExtAuthzRule{Header: "x-a"}, map[string]string{"X-A": "1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rule.matches(tc.in); got != tc.want {
				t.Errorf("%+v matches %v = %v, want %v", tc.rule, tc.in, got, tc.want)
			}
		})
	}
}

func TestGetExtAuthzRulesGivesACopy(t *testing.T) {
	resetExtAuthz(t)
	if rules := GetExtAuthzRules(); rules == nil || len(rules) != 0 {
		t.Errorf("no rules = %#v, want an empty list", rules)
	}
	store(t, "x-a", "", "deny")
	rules := GetExtAuthzRules()
	rules[0].Action = "allow"
	rules = append(rules, ExtAuthzRule{Header: "x-extra", Action: "deny"})
	got := GetExtAuthzRules()
	if len(got) != 1 || got[0].Action != "deny" {
		t.Errorf("changing the rules that were given changed the stored ones: %+v", got)
	}
}

func TestRemoveExtAuthzRule(t *testing.T) {
	resetExtAuthz(t)
	store(t, "x-a", "", "deny")
	store(t, "x-a", "1", "allow")
	store(t, "x-b", "", "deny")
	if RemoveExtAuthzRule("x-none", "") {
		t.Errorf("a rule that doesn't exist can't be removed")
	}
	if RemoveExtAuthzRule("x-a", "other") {
		t.Errorf("the value must match to remove a rule")
	}
	if !RemoveExtAuthzRule("X-A", "1") {
		t.Errorf("the rule for x-a=1 must be removed, whatever the case of the header is")
	}
	got := GetExtAuthzRules()
	if len(got) != 2 || got[0].Header != "x-a" || got[0].Value != "" || got[1].Header != "x-b" {
		t.Errorf("rules after the removal = %+v, want x-a and x-b, in order", got)
	}
	if !RemoveExtAuthzRule("x-a", "") || len(GetExtAuthzRules()) != 1 {
		t.Errorf("the rule for x-a without a value must be removed")
	}
	if RemoveExtAuthzRule("x-a", "") {
		t.Errorf("a rule can't be removed twice")
	}
}

func TestClearExtAuthzRules(t *testing.T) {
	resetExtAuthz(t)
	store(t, "x-a", "", "deny")
	store(t, "x-b", "", "allow")
	ClearExtAuthzRules()
	if rules := GetExtAuthzRules(); len(rules) != 0 {
		t.Errorf("rules after clear = %+v", rules)
	}
	if got := matchExtAuthzRule(map[string]string{"x-a": "1"}); got != "allow" {
		t.Errorf("after the rules are cleared, requests are allowed: %s", got)
	}
	ClearExtAuthzRules() // nothing to clear
}

func TestMatchExtAuthzRule(t *testing.T) {
	resetExtAuthz(t)
	if got := matchExtAuthzRule(nil); got != "allow" {
		t.Errorf("no rules = %s, want allow", got)
	}
	store(t, "x-a", "1", "deny")
	store(t, "x-a", "", "allow")
	store(t, "*", "", "deny")
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"first rule", map[string]string{"x-a": "1"}, "deny"},
		{"second rule, since the first has another value", map[string]string{"x-a": "2"}, "allow"},
		{"the rule for any request is last", map[string]string{"x-other": "1"}, "deny"},
		{"no headers", nil, "deny"},
	}
	for _, tc := range tests {
		if got := matchExtAuthzRule(tc.headers); got != tc.want {
			t.Errorf("%s: %v = %s, want %s", tc.name, tc.headers, got, tc.want)
		}
	}
	// the first match wins, so a rule for any request that is first decides all
	ClearExtAuthzRules()
	store(t, "*", "", "allow")
	store(t, "x-a", "", "deny")
	if got := matchExtAuthzRule(map[string]string{"x-a": "1"}); got != "allow" {
		t.Errorf("rule for any request first = %s, want allow", got)
	}
}

func TestExtAuthzRulesConcurrentUse(t *testing.T) {
	resetExtAuthz(t)
	const workers = 24
	var wg sync.WaitGroup
	var mismatches atomic.Int32
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			header := fmt.Sprintf("x-worker-%d", i)
			for round := 0; round < 20; round++ {
				StoreExtAuthzRule(&ExtAuthzRule{Header: header, Value: "v", Action: "deny"})
				GetExtAuthzRules()
				if matchExtAuthzRule(map[string]string{header: "v"}) != "deny" {
					// the rules of the other workers are for other headers, so the rule of this one must match
					mismatches.Add(1)
				}
				if round%2 == 1 {
					RemoveExtAuthzRule(header, "v")
				}
			}
		}()
	}
	wg.Wait()
	if n := mismatches.Load(); n != 0 {
		t.Errorf("%d times the rule of a worker did not match its own request", n)
	}
	// each worker did 20 rounds, and removed its rule in the odd ones: the last round is odd, so none is left
	if rules := GetExtAuthzRules(); len(rules) != 0 {
		t.Errorf("rules left = %d, want 0", len(rules))
	}
	for i := 0; i < workers; i++ {
		store(t, fmt.Sprintf("x-final-%d", i), "", "allow")
	}
	if rules := GetExtAuthzRules(); len(rules) != workers {
		t.Errorf("rules = %d, want %d", len(rules), workers)
	}
}
