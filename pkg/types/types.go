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

package types

import (
	"fmt"
	"net/http"
	"strings"
)

type Pair[L any, R any] struct {
	Left  L
	Right R
}

type Triple[F any, S any, T any] struct {
	First  F
	Second S
	Third  T
}

type Funcs struct {
	GetPeers                  func(string, *http.Request) map[string]string
	IsReadinessProbe          func(*http.Request) bool
	IsLivenessProbe           func(*http.Request) bool
	IsListenerPresent         func(int) bool
	IsListenerOpen            func(int) bool
	GetListenerID             func(int) string
	GetListenerLabel          func(*http.Request) string
	GetListenerLabelForPort   func(int) string
	GetHostLabelForPort       func(int) string
	CloseConnectionsForPort   func(int)
	StoreEventInCurrentLocker func(interface{})
}

type ListArg []string

func (l *ListArg) String() string {
	return strings.Join(*l, " ")
}

func (l *ListArg) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func (l *ListArg) SetAt(index int, value string) error {
	*l = append(*l, value)
	return nil
}

func NewPair[L any, R any](left L, right R) *Pair[L, R] {
	return &Pair[L, R]{
		Left:  left,
		Right: right,
	}
}

func (p *Pair[L, R]) String() string {
	return fmt.Sprintf("%+v: %+v", p.Left, p.Right)
}

func (p *Pair[L, R]) LeftS() string {
	return fmt.Sprintf("%+v", p.Left)
}

func (p *Pair[L, R]) RightS() string {
	return fmt.Sprintf("%+v", p.Right)
}

func NewTriple[F any, S any, T any](first F, second S, third T) *Triple[F, S, T] {
	return &Triple[F, S, T]{
		First:  first,
		Second: second,
		Third:  third,
	}
}

func (t *Triple[F, S, T]) String() string {
	return fmt.Sprintf("%+v:%+v:%+v", t.First, t.Second, t.Third)
}

func (t *Triple[F, S, T]) FirstS() string {
	return fmt.Sprintf("%+v", t.First)
}

func (t *Triple[F, S, T]) SecondS() string {
	return fmt.Sprintf("%+v", t.Second)
}

func (t *Triple[F, S, T]) ThirdS() string {
	return fmt.Sprintf("%+v", t.Third)
}
