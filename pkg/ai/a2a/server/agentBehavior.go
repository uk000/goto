package a2aserver

import (
	"context"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorImpl struct {
	self  model.IAgentBehavior
	agent *model.Agent
	delay *types.Delay
}

type AgentBehaviorEcho struct {
	*AgentBehaviorImpl
}

type AgentBehaviorStream struct {
	*AgentBehaviorImpl
}

type AgentBehaviorDelegate struct {
	*AgentBehaviorImpl
}

type AgentBehaviorHttpProxy struct {
	*AgentBehaviorImpl
}

type AgentTask struct {
	Agent      *model.Agent
	Behavior   model.IAgentBehavior
	TaskID     string
	Ctx        context.Context
	Input      a2aproto.Message
	Options    taskmanager.ProcessOptions
	Handler    taskmanager.TaskHandler
	Subscriber taskmanager.TaskSubscriber
	Delay      *types.Delay
}

func PrepareAgentBehavior(agent *model.Agent) {
	if agent.Behavior == nil {
		agent.Behavior = &model.AgentBehavior{}
	}
	impl := &AgentBehaviorImpl{agent: agent}
	if agent.Config != nil {
		if agent.Config.Delay != nil {
			agent.Config.Delay.Prepare()
			impl.delay = agent.Config.Delay
		}
	}
	if agent.Behavior.Echo {
		agent.Behavior.Impl = &AgentBehaviorEcho{AgentBehaviorImpl: impl}
	} else if agent.Behavior.Stream {
		agent.Behavior.Impl = &AgentBehaviorStream{AgentBehaviorImpl: impl}
	} else if agent.Behavior.Delegate {
		agent.Behavior.Impl = &AgentBehaviorDelegate{AgentBehaviorImpl: impl}
	} else if agent.Behavior.HTTPProxy {
		agent.Behavior.Impl = &AgentBehaviorHttpProxy{AgentBehaviorImpl: impl}
	}
	impl.self = agent.Behavior.Impl
}

func (b *AgentBehaviorImpl) newAgentTask(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*AgentTask, error) {
	taskID, err := handler.BuildTask(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build task: %w", err)
	}
	subscriber, err := handler.SubscribeTask(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to task: %w", err)
	}
	return &AgentTask{
		Agent:      b.agent,
		Behavior:   b.self,
		TaskID:     taskID,
		Ctx:        ctx,
		Input:      input,
		Options:    options,
		Handler:    handler,
		Subscriber: subscriber,
		Delay:      b.delay,
	}, nil
}

func (ab *AgentBehaviorEcho) DoUnary(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	_, msg := ab.getEchoMessage(input)
	return &taskmanager.MessageProcessingResult{
		Result: &msg,
	}, nil
}

func (ab *AgentBehaviorDelegate) DoUnary(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	return nil, nil
}

func (ab *AgentBehaviorHttpProxy) DoUnary(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	return nil, nil
}

func (ab *AgentBehaviorEcho) DoStream(t model.IAgentTask) error {
	task := t.(*AgentTask)
	output, _ := ab.getEchoMessage(task.Input)
	task.sendTaskStatusUpdate(a2aproto.TaskStateWorking, output)
	return nil
}

func (ab *AgentBehaviorStream) DoStream(t model.IAgentTask) error {
	task := t.(*AgentTask)
	if task.Delay == nil {
		task.Delay = &types.Delay{
			Min: &types.Duration{10 * time.Millisecond},
			Max: &types.Duration{100 * time.Millisecond},
		}
	}
	if ab.agent.Config != nil && ab.agent.Config.Response != nil {
		ab.agent.Config.Response.RangeText(func(text string) {
			task.sendTaskStatusUpdate(a2aproto.TaskStateWorking, text)
			select {
			case <-task.Ctx.Done():
				log.Printf("Task %s cancelled during delay: %v", task.TaskID, task.Ctx.Err())
				if err := task.sendTaskStatusUpdate(a2aproto.TaskStateCanceled, ""); err != nil {
					log.Printf("Failed to update task state with error: %v", err)
				}
				return
			case delay := <-task.Delay.Block():
				log.Printf("Continuing after Delay.Block [%s]", delay)
			}
		})
	}
	return nil
}

func (ab *AgentBehaviorDelegate) DoStream(task model.IAgentTask) error {
	return nil
}

func (ab *AgentBehaviorHttpProxy) DoStream(task model.IAgentTask) error {
	return nil
}

func (b *AgentBehaviorImpl) ProcessMessage(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	if options.Streaming {
		return b.handleStream(ctx, input, options, handler)
	} else {
		return b.self.DoUnary(ctx, input, options, handler)
	}
}

func (b *AgentBehaviorImpl) handleStream(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	task, err := b.newAgentTask(ctx, input, options, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to build task: %w", err)
	}
	go task.stream(ctx, input)
	return &taskmanager.MessageProcessingResult{
		StreamingEvents: task.Subscriber,
	}, nil
}

func (ab *AgentBehaviorImpl) DoUnary(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	return nil, nil
}

func (ab *AgentBehaviorImpl) DoStream(task model.IAgentTask) error {
	return nil
}

func (t *AgentTask) stream(ctx context.Context, input a2aproto.Message) (err error) {
	defer t.endTask()
	if err = t.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Started..."); err != nil {
		return
	}
	return t.Behavior.DoStream(t)
}

func (t *AgentTask) endTask() {
	t.sendTaskStatusUpdate(a2aproto.TaskStateCompleted, "Completed.")
	if t.Subscriber != nil {
		t.Subscriber.Close()
	}
	t.Handler.CleanTask(&t.TaskID)
}

func (t *AgentTask) sendTaskStatusUpdate(state a2aproto.TaskState, msg string) (err error) {
	message := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]protocol.Part{protocol.NewTextPart(msg)},
	)
	// event := a2aproto.TaskStatusUpdateEvent{
	// 	TaskID:    t.TaskID,
	// 	ContextID: contextID,
	// 	Kind:      a2aproto.KindTaskStatusUpdate,
	// 	Final:     isFinal,
	// 	Status: a2aproto.TaskStatus{
	// 		State:     state,
	// 		Message:   &message,
	// 		Timestamp: time.Now().Format(time.RFC3339Nano),
	// 	},
	// }
	// err = t.Subscriber.Send(a2aproto.StreamingMessageEvent{Result: &event})
	// if err != nil {
	// 	return
	// }
	err = t.Handler.UpdateTaskState(&t.TaskID, state, &message)
	return
}

func (t *AgentTask) sendTextArtifact(title, description, text string, isFinal, isQuestion bool) (err error) {
	artifact := protocol.Artifact{
		ArtifactID:  uuid.New().String(),
		Name:        util.Ptr(title),
		Description: util.Ptr(description),
		Parts:       []protocol.Part{protocol.NewTextPart(text)},
	}
	artifactEvent := a2aproto.StreamingMessageEvent{
		Result: &a2aproto.TaskArtifactUpdateEvent{
			TaskID:    t.TaskID,
			Kind:      a2aproto.KindTaskArtifactUpdate,
			Artifact:  artifact,
			LastChunk: util.Ptr(true),
		},
	}
	err = t.Subscriber.Send(artifactEvent)
	if err != nil {
		return
	}
	return t.Handler.AddArtifact(&t.TaskID, artifact, isFinal, isQuestion)
}

func (t *AgentTask) waitBeforeNextStep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		log.Printf("Task %s cancelled during delay: %v", t.TaskID, ctx.Err())
		return t.Handler.UpdateTaskState(&t.TaskID, protocol.TaskStateCanceled, nil)
	case <-t.Delay.Block():
	}
	return nil
}

func (ab *AgentBehaviorEcho) getEchoMessage(input a2aproto.Message) (output string, message a2aproto.Message) {
	output = util.ToJSONText(input)
	message = a2aproto.NewMessage(
		a2aproto.MessageRoleAgent,
		[]a2aproto.Part{a2aproto.NewTextPart(output)},
	)
	return
}
