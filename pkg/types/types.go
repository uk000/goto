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
	"fmt"
	"net/http"
	"strings"
)

type RequestId struct {
	Send   bool   `json:"send"`
	UUID   bool   `json:"uuid"`
	Header string `json:"header"`
	Query  string `json:"query"`
}

type Pair[L any, R any] struct {
	Left  L
	Right R
}
type StringPair Pair[string, string]

type Triple[F any, S any, T any] struct {
	First  F
	Second S
	Third  T
}
type StringTriple Triple[string, string, string]

type Funcs struct {
	GetPeers                func(string, *http.Request) map[string]string
	IsReadinessProbe        func(*http.Request) bool
	IsLivenessProbe         func(*http.Request) bool
	IsListenerPresent       func(int) bool
	IsListenerOpen          func(int) bool
	GetListenerID           func(int) string
	GetListenerLabel        func(*http.Request) string
	GetListenerLabelForPort func(int) string
	IsListenerTLS           func(int) bool
	GetCertInfo             func(int, string) (string, string)
	CloseConnectionsForPort func(int)
}

type ListArg []string

// String returns the values joined by spaces.
func (l *ListArg) String() string {
	return strings.Join(*l, " ")
}

// Set appends the value, so that a flag given repeatedly collects all its values.
func (l *ListArg) Set(value string) error {
	*l = append(*l, value)
	return nil
}

// SetAt appends the value, whatever the index is.
func (l *ListArg) SetAt(index int, value string) error {
	*l = append(*l, value)
	return nil
}

// NewPair creates a pair of the left and right values.
func NewPair[L any, R any](left L, right R) *Pair[L, R] {
	return &Pair[L, R]{
		Left:  left,
		Right: right,
	}
}

// NewStringAnyPair creates a pair of a string and any value.
func NewStringAnyPair(left string, right any) *Pair[string, any] {
	return &Pair[string, any]{
		Left:  left,
		Right: right,
	}
}

// NewStringPair creates a pair of two strings.
func NewStringPair(left, right string) *StringPair {
	return &StringPair{
		Left:  left,
		Right: right,
	}
}

// String formats the pair as "left: right".
func (p *Pair[L, R]) String() string {
	return fmt.Sprintf("%+v: %+v", p.Left, p.Right)
}

// LeftS returns the left value as text.
func (p *Pair[L, R]) LeftS() string {
	return fmt.Sprintf("%+v", p.Left)
}

// RightS returns the right value as text.
func (p *Pair[L, R]) RightS() string {
	return fmt.Sprintf("%+v", p.Right)
}

// MarshalJSON writes the pair as an object of one key, with the text of the left as the key and the one of the right as
// the value. The texts are quoted as JSON strings (strconv.Quote makes Go escapes, like \x01, that are not JSON).
func (p *Pair[L, R]) MarshalJSON() ([]byte, error) {
	left, err := json.Marshal(fmt.Sprint(p.Left))
	if err != nil {
		return nil, err
	}
	right, err := json.Marshal(fmt.Sprint(p.Right))
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("{%s: %s}", left, right)), nil
}

// NewTriple creates a triple of the first, second and third values.
func NewTriple[F any, S any, T any](first F, second S, third T) *Triple[F, S, T] {
	return &Triple[F, S, T]{
		First:  first,
		Second: second,
		Third:  third,
	}
}

// String formats the triple as "first:second:third".
func (t *Triple[F, S, T]) String() string {
	return fmt.Sprintf("%+v:%+v:%+v", t.First, t.Second, t.Third)
}

// FirstS returns the first value as text.
func (t *Triple[F, S, T]) FirstS() string {
	return fmt.Sprintf("%+v", t.First)
}

// SecondS returns the second value as text.
func (t *Triple[F, S, T]) SecondS() string {
	return fmt.Sprintf("%+v", t.Second)
}

// ThirdS returns the third value as text.
func (t *Triple[F, S, T]) ThirdS() string {
	return fmt.Sprintf("%+v", t.Third)
}

type RawBytes []byte

// UnmarshalJSON keeps the raw bytes of the value, without the quotes if it is a JSON string.
func (r *RawBytes) UnmarshalJSON(data []byte) error {
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
		*r = data[1 : len(data)-1]
	} else {
		*r = data
	}
	return nil
}

// MarshalJSON writes the bytes as a JSON string.
func (r RawBytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(r))
}
