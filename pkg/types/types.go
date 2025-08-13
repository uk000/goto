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
	"net/http"
	"strings"
)

type Pair struct {
	Left  any
	Right any
}

type Triple struct {
	First  any
	Second any
	Third  any
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
