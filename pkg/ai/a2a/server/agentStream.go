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

package a2aserver

import (
	"fmt"
	"goto/pkg/types"
	"log"
	"strings"
	"time"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type AgentBehaviorStream struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorStream) DoStream(aCtx *AgentContext) error {
	if aCtx.delay == nil {
		aCtx.delay = types.NewDelay(10*time.Millisecond, 100*time.Millisecond, 0)
	}
	var delay time.Duration
	output := []string{}
	outputFrom := 1
	var streamCount int
	var err error
	if ab.agent.Config != nil && ab.agent.Config.ResponsePayload != nil {
		streamCount = ab.agent.Config.ResponsePayload.StreamCount
		streatStartAt := time.Now()
		if ab.agent.Config.ResponsePayload.Delay != nil && ab.agent.Config.ResponsePayload.Delay.IsLargerThan(aCtx.delay) {
			aCtx.delay = ab.agent.Config.ResponsePayload.Delay
		}
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("Will stream output count [%d] with delay range [%s-%s]", ab.agent.Config.ResponsePayload.StreamCount, aCtx.delay.Min, aCtx.delay.Max), nil)
		ab.agent.Config.ResponsePayload.RangeText(func(text string, count int, restarted bool) error {
			if restarted {
				if err = aCtx.sendTextArtifact(fmt.Sprintf("Recap of recently streamed output [%d-%d]", outputFrom, count-1), "", []string{strings.Join(output, "\n")}, false, false); err != nil {
					return err
				}
				outputFrom = count
				output = []string{}
			}
			delay = aCtx.delay.Compute()
			if err = aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("Sleeping for [%s] before sending next update", delay), nil); err != nil {
				log.Printf("Failed to send task update about sleeping with error: %s", err.Error())
				return err
			}
			if delay, err = aCtx.waitBeforeNextStep(); err != nil {
				log.Printf("Failed to wait before next step with error: %s", err.Error())
				return err
			}
			text = fmt.Sprintf("Result# %d: %s", count, text)
			output = append(output, text)
			if delay > 0 {
				text = fmt.Sprintf("%s, after delay %s", text, delay)
			}
			text = fmt.Sprintf("%s, total stream time: %s", text, time.Since(streatStartAt))
			if err = aCtx.sendTextArtifact("", "", []string{text}, count == streamCount, false); err != nil {
				log.Printf("Failed to send partial result with error: %s", err.Error())
				return err
			}
			return nil
		})
	} else {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "No Response Configured", nil)
	}
	if len(output) > 0 {
		if err = aCtx.sendTextArtifact(fmt.Sprintf("Recap of recently streamed output [%d-%d]", outputFrom, streamCount), "", []string{strings.Join(output, "\n")}, false, false); err != nil {
			return err
		}
	}
	return nil
}
