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
	"fmt"
	"goto/pkg/global"
	"runtime"
	"strings"
)

func BuildListenerLabel(port int) string {
	return fmt.Sprintf("[%s:%d].[%s@%s]", global.Self.PodIP, port, global.Self.Namespace, global.Self.Cluster)
}

func PrintCallers(level int, callee string) {
	pc := make([]uintptr, 16)
	n := runtime.Callers(1, pc)
	frames := runtime.CallersFrames(pc[:n])
	var callers []string
	i := 0
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "util") &&
			strings.Contains(frame.Function, "goto") {
			callers = append(callers, frame.Function)
			i++
		}
		if !more || i >= level {
			break
		}
	}
	fmt.Println("-----------------------------------------------")
	fmt.Printf("Callers of [%s]: %+v\n", callee, callers)
	fmt.Println("-----------------------------------------------")
}
