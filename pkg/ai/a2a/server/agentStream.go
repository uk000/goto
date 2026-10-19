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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type AgentBehaviorStream struct {
	*AgentBehaviorImpl
	// The streams of the agent are sent by the goroutines of the requests, and a stop is asked for by another one.
	stopReqested atomic.Bool
	withError    atomic.Bool
	streaming    atomic.Int32
}

func (ab *AgentBehaviorStream) DoStream(aCtx *AgentContext) (string, error) {
	if ab.processStop(aCtx) {
		return "Stream Stop Requested", nil
	}
	return ab.sendStream(aCtx)
}

func (ab *AgentBehaviorStream) sendStream(aCtx *AgentContext) (string, error) {
	if ab.streaming.Add(1) == 1 {
		// A stop that was asked for while no stream was sent is not for this one.
		ab.stopReqested.Store(false)
		ab.withError.Store(false)
	}
	defer ab.streaming.Add(-1)
	text, count := util.ExtractNumberHint(aCtx.inputText)
	text, overrideDelay := util.ExtractDurationHint(text)
	if aCtx.delay == nil {
		aCtx.delay = types.NewDelay(10*time.Millisecond, 100*time.Millisecond, 0)
	}
	var delay time.Duration
	var streamCount, sentCount int
	var err error
	// The payload is replaced by the admin API. The stream is of the one that was there when it began.
	if p := ab.agent.GetPayload(); p != nil {
		streamCount = p.StreamCount
		if count > 0 {
			streamCount = count
		}
		streatStartAt := time.Now()
		if overrideDelay != 0 {
			aCtx.delay = types.NewDelay(overrideDelay, overrideDelay, 0)
		} else if p.Delay != nil && p.Delay.IsLargerThan(aCtx.delay) {
			aCtx.delay = p.Delay
		}
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("Will stream output count [%d] with delay range [%s-%s]", streamCount, aCtx.delay.Min, aCtx.delay.Max))
		p.RangeText(count, func(_ string, count int, restarted bool) (bool, error) {
			if ab.stopReqested.Load() || count > streamCount {
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateCompleted, fmt.Sprintf("\U0001F6D1 Stop requested, stopping after %d stream messages, %d remaining", count-1, (streamCount-count+1)))
				return false, nil
			}
			delay = aCtx.delay.Compute()
			if err = aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("\U0001F634\U0001F4A4 Sleeping for [%s] before sending next update", delay)); err != nil {
				log.Printf("Failed to send task update about sleeping with error: %s", err.Error())
				return false, err
			}
			if delay, err = aCtx.waitBeforeNextStep(); err != nil {
				log.Printf("Failed to wait before next step with error: %s", err.Error())
				return false, err
			}
			// Every message is made of the text of the request, not of the message before it.
			msg := strings.ReplaceAll(text, "{current}", strconv.Itoa(count))
			msg = strings.ReplaceAll(msg, "{total}", strconv.Itoa(streamCount))
			msg = fmt.Sprintf("Result# %d (of %d): %s", count, streamCount, msg)
			if delay > 0 {
				msg = fmt.Sprintf("%s, after delay %s", msg, delay)
			}
			msg = fmt.Sprintf("%s, total stream time: %s", msg, time.Since(streatStartAt))
			if err = aCtx.notifyUpdate(msg, nil, false); err != nil {
				log.Printf("Failed to send partial result with error: %s", err.Error())
				return false, err
			}
			sentCount++
			return true, nil
		})
	} else {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "No Response Configured")
	}
	if ab.withError.Swap(false) {
		ab.stopReqested.Store(false)
		return "Stream finished with requested error \u274C", errors.New("Error Requested")
	} else if ab.stopReqested.Swap(false) {
		return "Stream finished due to stop requested \U0001F6D1", nil
	}
	return "Stream finished \U000026F3", nil
}

func (ab *AgentBehaviorStream) processStop(aCtx *AgentContext) bool {
	if strings.Contains(aCtx.inputText, "stop") {
		ab.stopReqested.Store(true)
		if strings.Contains(aCtx.inputText, "error") {
			ab.withError.Store(true)
		}
		return true
	}
	return false
}
