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
	"time"
)

type Step interface {
	GetCounter() int
}

type BaseStep struct {
	Counter   int    `json:"step"`
	Host      string `json:"host"`
	Operation string `json:"operation"`
}

type RemoteStep struct {
	BaseStep
	Data any `json:"remote"`
}

type HopStep struct {
	BaseStep
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

type Hops struct {
	currentIndex int
	HostLabel    string `json:"Label,omitempty"`
	Operation    string `json:"Operation,omitempty"`
	Steps        []Step `json:"hops"`
}

func NewHops(label, operation string) *Hops {
	return &Hops{HostLabel: label, Operation: operation, Steps: []Step{}}
}

func (hs *HopStep) GetCounter() int {
	return hs.Counter
}

func (rs *RemoteStep) GetCounter() int {
	return rs.Counter
}

func (h *Hops) AddAt(step int, msg string) *Hops {
	h.Steps = append(h.Steps, h.newStep(step, msg))
	h.currentIndex = step
	return h
}

func (h *Hops) Add(msg string) *Hops {
	h.currentIndex++
	h.Steps = append(h.Steps, h.newStep(h.currentIndex, msg))
	return h
}

func (h *Hops) newStep(step int, msg string) *HopStep {
	return &HopStep{
		BaseStep: BaseStep{
			Counter:   step,
			Host:      h.HostLabel,
			Operation: h.Operation,
		},
		Message: msg,
		At:      time.Now(),
	}
}

func (h *Hops) AddRemote(data any) *Hops {
	h.Steps = append(h.Steps, &RemoteStep{
		BaseStep: BaseStep{
			Counter:   h.currentIndex,
			Host:      h.HostLabel,
			Operation: h.Operation,
		},
		Data: data,
	})
	return h
}

func (h *Hops) MergeRemoteHops(output map[string]any) map[string]any {
	var remoteSteps []any
	if hops, ok := output["hops"].(map[string]any); ok {
		if hops2, ok := hops["hops"].([]any); ok {
			remoteSteps = hops2
		}
	} else if hopSteps, ok := output["hops"].([]any); ok {
		remoteSteps = hopSteps
	}
	h.currentIndex++
	for _, s := range remoteSteps {
		h.AddRemote(s)
	}
	delete(output, "hops")
	return output
}

func (h *Hops) AddToOutput(output map[string]any) {
	output["hops"] = h.Steps
}

func (h *Hops) AsOutput() map[string]any {
	output := map[string]any{}
	output["hops"] = h.Steps
	return output
}

func (h *Hops) AsJSONText() string {
	return ToJSONText(h.Steps)
}

// func (h *Hops) MergeClientServerHops(output map[string]any) map[string]any {
// 	if hops, ok := output["hops"].(map[string]any); ok {
// 		for k, v := range hops {
// 			prefix := ""
// 			if counterFrom > 0 {
// 				prefix = fmt.Sprintf("%s%d. ", prefix, counterFrom)
// 				counterFrom++
// 			}
// 			if label != "" {
// 				prefix = fmt.Sprintf("%s%s -> ", prefix, label)
// 			}
// 			k = fmt.Sprintf("%s%s", prefix, k)
// 			hops[k] = v
// 		}
// 		delete(output, "hops")
// 	}
// 	return output
// }
