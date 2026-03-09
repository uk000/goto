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

package util

import (
	"goto/pkg/types"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

var (
	sizes = map[string]uint64{
		"K":  1000,
		"KB": 1000,
		"M":  1000000,
		"MB": 1000000,
	}
)

func GetIntParam(r *http.Request, param string, defaultVal ...int) (int, bool) {
	vars := mux.Vars(r)
	switch {
	case len(vars[param]) > 0:
		s, _ := strconv.ParseInt(vars[param], 10, 32)
		return int(s), true
	case len(defaultVal) > 0:
		return defaultVal[0], false
	default:
		return 0, false
	}
}

func GetIntParamValue(r *http.Request, param string, defaultVal ...int) int {
	val, _ := GetIntParam(r, param, defaultVal...)
	return val
}

func GetStringParam(r *http.Request, param string, defaultVal ...string) (string, bool) {
	vars := mux.Vars(r)
	switch {
	case len(vars[param]) > 0:
		return vars[param], true
	case len(defaultVal) > 0:
		return defaultVal[0], false
	default:
		return "", false
	}
}

func GetStringParamValue(r *http.Request, param string, defaultVal ...string) string {
	val, _ := GetStringParam(r, param, defaultVal...)
	return val
}

func GetBoolParamValue(r *http.Request, param string, defaultVal ...bool) bool {
	val, _ := GetStringParam(r, param)
	if val != "" {
		return IsYes(val)
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return false
}

func GetListParam(r *http.Request, param string) ([]string, bool) {
	values := r.URL.Query()[param]
	if len(values) == 1 {
		values = strings.Split(values[0], ",")
	}
	return values, len(values) > 0 && len(values[0]) > 0
}

func GetStatusParam(r *http.Request) (statusCodes []int, times int, present bool) {
	vars := mux.Vars(r)
	status := vars["status"]
	if len(status) == 0 {
		return nil, 0, false
	}
	pieces := strings.Split(status, ":")
	if len(pieces[0]) > 0 {
		for _, s := range strings.Split(pieces[0], ",") {
			if sc, err := strconv.ParseInt(s, 10, 32); err == nil {
				statusCodes = append(statusCodes, int(sc))
			}
		}
		if len(pieces) > 1 {
			s, _ := strconv.ParseInt(pieces[1], 10, 32)
			times = int(s)
		}
	}
	if times == 0 {
		times = -1
	}
	return statusCodes, times, true
}

func ParseSize(value string) int {
	size := 0
	multiplier := 1
	if len(value) == 0 {
		return 0
	}
	for k, v := range sizes {
		if strings.Contains(value, k) {
			multiplier = int(v)
			value = strings.Split(value, k)[0]
			break
		}
	}
	if len(value) > 0 {
		s, _ := strconv.ParseInt(value, 10, 32)
		size = int(s)
	} else {
		size = 1
	}
	size = size * multiplier
	return size
}

func GetSizeParam(r *http.Request, name string) int {
	return ParseSize(mux.Vars(r)[name])
}

func ParseDuration(value string) time.Duration {
	if d, err := time.ParseDuration(value); err == nil {
		return d
	}
	return 0
}

func GetDurationParam(r *http.Request, name string) (low, high time.Duration, count int, ok bool) {
	if val := mux.Vars(r)[name]; val != "" {
		return types.ParseDurationRange(val)
	}
	return 0, 0, 0, false
}

func RequestHasParam(r *http.Request, param string) bool {
	if vars := mux.Vars(r); vars != nil {
		if _, ok := vars[param]; ok {
			return true
		}
	}
	return false
}

func ParseJSONPathsFromRequest(paramName string, r *http.Request) *JSONPath {
	paths, ok := GetListParam(r, paramName)
	if !ok {
		paths = strings.Split(Read(r.Body), "\n")
	}
	if len(paths) > 0 {
		return NewJSONPath().Parse(paths)
	}
	return nil
}

func ParseJQFromRequest(paramName string, r *http.Request) *JQ {
	paths, ok := GetListParam(r, paramName)
	if !ok {
		paths = strings.Split(Read(r.Body), "\n")
	}
	if len(paths) > 0 {
		return NewJQ().Parse(paths)
	}
	return nil
}
