package a2aserver

import (
	"fmt"
	"goto/pkg/types"
	"log"
	"time"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type AgentBehaviorStream struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorStream) DoStream(aCtx *AgentContext) error {
	if aCtx.delay == nil {
		aCtx.delay = &types.Delay{
			Min: &types.Duration{10 * time.Millisecond},
			Max: &types.Duration{100 * time.Millisecond},
		}
	}
	var delay time.Duration
	if ab.agent.Config != nil && ab.agent.Config.Response != nil {
		ab.agent.Config.Response.RangeText(func(text string) {
			if delay > 0 {
				text = fmt.Sprintf("%s, after delay %s", text, delay)
			}
			aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, text, nil)
			select {
			case <-aCtx.ctx.Done():
				log.Printf("Task %s cancelled during delay: %v", aCtx.task.taskID, aCtx.ctx.Err())
				if err := aCtx.sendTaskStatusUpdate(a2aproto.TaskStateCanceled, "", nil); err != nil {
					log.Printf("Failed to update task state with error: %v", err)
				}
				return
			case delay = <-aCtx.delay.Block():
				log.Printf("Continuing after Delay.Block [%s]", delay)
			}
		})
	}
	return nil
}
