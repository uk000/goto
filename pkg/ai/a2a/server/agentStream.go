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

package a2aserver

import (
	"errors"
	"fmt"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"strings"
	"time"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type AgentBehaviorStream struct {
	*AgentBehaviorImpl
	stopReqested bool
	withError    bool
}

func (ab *AgentBehaviorStream) DoStream(aCtx *AgentContext) (string, error) {
	if ab.processStop(aCtx) {
		return "Stream Stop Requested", nil
	}
	return ab.sendStream(aCtx)
}

func (ab *AgentBehaviorStream) sendStream(aCtx *AgentContext) (string, error) {
	count, text := util.ExtractNumberHint(aCtx.inputText)
	overrideDelay, text := util.ExtractDurationHint(text)
	if aCtx.delay == nil {
		aCtx.delay = types.NewDelay(10*time.Millisecond, 100*time.Millisecond, 0)
	}
	var delay time.Duration
	output := []string{}
	outputFrom := 1
	var streamCount, sentCount int
	var err error
	if ab.agent.Config != nil && ab.agent.Config.ResponsePayload != nil {
		streamCount = ab.agent.Config.ResponsePayload.StreamCount
		if count > 0 {
			streamCount = count
		}
		streatStartAt := time.Now()
		if overrideDelay != 0 {
			aCtx.delay = types.NewDelay(overrideDelay, overrideDelay, 0)
		} else if ab.agent.Config.ResponsePayload.Delay != nil && ab.agent.Config.ResponsePayload.Delay.IsLargerThan(aCtx.delay) {
			aCtx.delay = ab.agent.Config.ResponsePayload.Delay
		}
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("Will stream output count [%d] with delay range [%s-%s]", streamCount, aCtx.delay.Min, aCtx.delay.Max), nil)
		ab.agent.Config.ResponsePayload.RangeText(count, func(text string, count int, restarted bool) (bool, error) {
			if ab.stopReqested || count > streamCount {
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateCompleted, fmt.Sprintf("\U0001F6D1 Stop requested, stopping after %d stream messages, %d remaining", count-1, (streamCount-count+1)), nil)
				return false, nil
			}
			if restarted {
				if err = aCtx.sendTextArtifact(fmt.Sprintf("\u2705 Recap of recently streamed output [%d-%d]", outputFrom, count-1), "", output, false, false); err != nil {
					return false, err
				}
				outputFrom = count
				output = []string{}
			}
			delay = aCtx.delay.Compute()
			if err = aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("\U0001F634\U0001F4A4 Sleeping for [%s] before sending next update", delay), nil); err != nil {
				log.Printf("Failed to send task update about sleeping with error: %s", err.Error())
				return false, err
			}
			if delay, err = aCtx.waitBeforeNextStep(); err != nil {
				log.Printf("Failed to wait before next step with error: %s", err.Error())
				return false, err
			}
			text = fmt.Sprintf("Result# %d (of %d): %s", count, streamCount, text)
			output = append(output, text)
			if delay > 0 {
				text = fmt.Sprintf("%s, after delay %s", text, delay)
			}
			text = fmt.Sprintf("%s, total stream time: %s", text, time.Since(streatStartAt))
			if err = aCtx.sendTextArtifact("", "", []string{text}, count == streamCount, false); err != nil {
				log.Printf("Failed to send partial result with error: %s", err.Error())
				return false, err
			}
			sentCount++
			return true, nil
		})
	} else {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "No Response Configured", nil)
	}
	if len(output) > 0 {
		if err = aCtx.sendTextArtifact(fmt.Sprintf("\u2705 Recap of recently streamed output [%d-%d] \U000026F3", outputFrom, sentCount), "", output, false, false); err != nil {
			return "Stream finished with error \u274C", err
		}
	}
	if ab.withError {
		ab.withError = false
		ab.stopReqested = false
		return "Stream finished with requested error \u274C", errors.New("Error Requested")
	} else if ab.stopReqested {
		ab.stopReqested = false
		return "Stream finished due to stop requested \U0001F6D1", nil
	}
	return "Stream finished \U000026F3", nil
}

func (ab *AgentBehaviorStream) processStop(aCtx *AgentContext) bool {
	if strings.Contains(aCtx.inputText, "stop") {
		ab.stopReqested = true
		if strings.Contains(aCtx.inputText, "error") {
			ab.withError = true
		}
		return true
	}
	return false
}
