package a2aserver

import (
	"context"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentTask struct {
	agent      *model.Agent
	behavior   model.IAgentBehavior
	taskID     string
	input      a2aproto.Message
	options    taskmanager.ProcessOptions
	handler    taskmanager.TaskHandler
	subscriber taskmanager.TaskSubscriber
}

type AgentCallContext struct {
	serverID string
	agent    *model.Agent
	behavior model.IAgentBehavior
	ctx      context.Context
	rs       *util.RequestStore
	headers  http.Header
	delay    *types.Delay
	triggers DelegateTriggers
	tools    map[string]*model.DelegateToolCall
	agents   map[string]*model.DelegateAgentCall
	input    a2aproto.Message
	options  taskmanager.ProcessOptions
	handler  taskmanager.TaskHandler
	task     *AgentTask
	hops     *util.Hops
	logs     []string
}

type toolOverrides struct {
	tool        string
	agent       string
	url         string
	remoteInput string
	args        map[string]any
}

func newAgentCallContext(serverID string, agent *model.Agent, headers http.Header, rs *util.RequestStore) *AgentCallContext {
	return &AgentCallContext{
		serverID: serverID,
		agent:    agent,
		hops:     util.NewHops(serverID, agent.ID),
		headers:  headers,
		rs:       rs,
	}
}

func (ac *AgentCallContext) setContext(ctx context.Context, b *AgentBehaviorImpl, task *AgentTask, input a2aproto.Message, options taskmanager.ProcessOptions, handler taskmanager.TaskHandler) {
	ac.ctx = ctx
	ac.behavior = b
	ac.task = task
	ac.input = input
	ac.options = options
	ac.handler = handler
	ac.delay = b.delay
	if abd, ok := ac.behavior.(*AgentBehaviorFederate); ok {
		ac.triggers = abd.triggers
	}
}

func (ac *AgentCallContext) detectRemoteCalls() {
	text := getMessageText(ac.input)
	inputText, jsons := util.ExtractEmbeddedJSON(text)
	inputText, portHint := util.ExtractPortHint(text)
	ac.matchDelegates(inputText, portHint)
	ac.sendDelegatesMatchUpdate()
	ac.setOverrideParamsFromInput(jsons)
}

func (ac *AgentCallContext) sendDelegatesMatchUpdate() {
	toolNames := []string{}
	agentNames := []string{}
	for name := range ac.tools {
		toolNames = append(toolNames, name)
	}
	for name := range ac.agents {
		agentNames = append(agentNames, name)
	}
	msg := fmt.Sprintf("Matched Tools: %+v, Agents: %+v", toolNames, agentNames)
	log.Println(msg)
	ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg, nil)
}

func (ac *AgentCallContext) matchDelegates(input string, portHint string) {
	ac.tools = map[string]*model.DelegateToolCall{}
	ac.agents = map[string]*model.DelegateAgentCall{}
	for _, triple := range ac.triggers {
		re := triple.First
		if re.MatchString(input) {
			if triple.Second != nil {
				tool := *triple.Second
				toolName := tool.ToolCall.Tool
				if ac.tools[toolName] != nil && portHint != "" {
					if strings.Contains(tool.ToolCall.URL, portHint) ||
						!strings.Contains(ac.tools[toolName].ToolCall.URL, portHint) {
						ac.tools[toolName] = &tool
					}
				} else {
					ac.tools[toolName] = &tool
				}
			} else if triple.Third != nil {
				agent := *triple.Third
				agentName := agent.AgentCall.Name
				if ac.agents[agentName] != nil && portHint != "" {
					if strings.Contains(agent.AgentCall.URL, portHint) ||
						!strings.Contains(ac.agents[agentName].AgentCall.URL, portHint) {
						ac.agents[agentName] = &agent
					}
				} else {
					ac.agents[agentName] = &agent
				}
			}
			if len(ac.tools)+len(ac.agents) >= ac.agent.Config.Delegates.MaxCalls {
				break
			}
		}
	}
}

func (ac *AgentCallContext) setOverrideParamsFromInput(jsons []map[string]any) {
	overrides := extractJSONValues(jsons)
	for name, override := range overrides {
		if t := ac.tools[name]; t != nil {
			if override.url != "" {
				msg := fmt.Sprintf("Will use URL [%s] instead of [%s] for Tool [%s]", override.url, t.ToolCall.URL, t.ToolCall.Tool)
				log.Println(msg)
				ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg, nil)
				t.ToolCall.URL = override.url
			}
			if override.args != nil {
				msg := fmt.Sprintf("Will use Args %+v instead of %+v for Tool [%s]", override.args, t.ToolCall.Args, t.ToolCall.Tool)
				log.Println(msg)
				ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg, nil)
				t.ToolCall.Args = override.args
			}
		} else if a := ac.agents[name]; a != nil {
			if override.url != "" {
				msg := fmt.Sprintf("Will use URL [%s] instead of [%s] for Agent [%s]", override.url, a.AgentCall.URL, a.AgentCall.Name)
				log.Println(msg)
				ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg, nil)
				a.AgentCall.URL = override.url
			}
			if override.args != nil {
				msg := fmt.Sprintf("Will use Data %+v instead of %+v for Agent [%s]", override.args, a.AgentCall.Data, a.AgentCall.Name)
				log.Println(msg)
				ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg, nil)
				a.AgentCall.Data = override.args
			}
			if override.remoteInput != "" {
				msg := fmt.Sprintf("Will use Message %s instead of %s for Agent [%s]", override.remoteInput, a.AgentCall.Message, a.AgentCall.Name)
				log.Println(msg)
				ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg, nil)
				a.AgentCall.Message = override.remoteInput
			}
		}
	}
}

func extractJSONValues(jsons []map[string]any) map[string]*toolOverrides {
	overrides := map[string]*toolOverrides{}
	for _, json := range jsons {
		override := &toolOverrides{}
		if json["tool"] != nil {
			override.tool = json["tool"].(string)
			overrides[override.tool] = override
		}
		if json["agent"] != nil {
			override.agent = json["agent"].(string)
			overrides[override.agent] = override
		}
		if json["url"] != nil {
			override.url = json["url"].(string)
		}
		if json["input"] != nil {
			if s, ok := json["input"].(string); ok {
				override.remoteInput = s
			}
		}
		if json["args"] != nil {
			if m, ok := json["args"].(map[string]any); ok {
				override.args = m
			}
		}
	}
	return overrides
}

func (ac *AgentCallContext) sendTaskStatusUpdate(state a2aproto.TaskState, msg string, parts []a2aproto.Part) (err error) {
	var message a2aproto.Message
	if parts == nil {
		parts = []a2aproto.Part{}
	}
	if len(parts) == 0 {
		msg = fmt.Sprintf("[%s]: Agent [%s]: %s", time.Now().Format(time.RFC3339Nano), ac.agent.ID, msg)
		parts = append(parts, a2aproto.NewTextPart(msg))
	}
	message = a2aproto.NewMessage(a2aproto.MessageRoleAgent, parts)
	err = ac.task.handler.UpdateTaskState(&ac.task.taskID, state, &message)
	return
}

func (ac *AgentCallContext) sendTextArtifact(title, description, text string, isFinal, isQuestion bool) (err error) {
	artifact := a2aproto.Artifact{
		ArtifactID:  uuid.New().String(),
		Name:        util.Ptr(title),
		Description: util.Ptr(description),
		Parts:       []a2aproto.Part{a2aproto.NewTextPart(text)},
	}
	artifactEvent := a2aproto.StreamingMessageEvent{
		Result: &a2aproto.TaskArtifactUpdateEvent{
			TaskID:    ac.task.taskID,
			Kind:      a2aproto.KindTaskArtifactUpdate,
			Artifact:  artifact,
			LastChunk: util.Ptr(true),
		},
	}
	err = ac.task.subscriber.Send(artifactEvent)
	if err != nil {
		return
	}
	return ac.task.handler.AddArtifact(&ac.task.taskID, artifact, isFinal, isQuestion)
}

func (ac *AgentCallContext) endTask() {
	ac.sendTaskStatusUpdate(a2aproto.TaskStateCompleted, "Agent update: Completed.", nil)
	if ac.task.subscriber != nil {
		ac.task.subscriber.Close()
	}
	ac.task.handler.CleanTask(&ac.task.taskID)
}

func (ac *AgentCallContext) waitBeforeNextStep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		log.Printf("Task %s cancelled during delay: %v", ac.task.taskID, ctx.Err())
		return ac.task.handler.UpdateTaskState(&ac.task.taskID, a2aproto.TaskStateCanceled, nil)
	case <-ac.delay.Block():
	}
	return nil
}

func (ac *AgentCallContext) Log(msg string, args ...any) string {
	msg = fmt.Sprintf(msg, args...)
	ac.logs = append(ac.logs, msg)
	return msg
}

func (ac *AgentCallContext) Flush(print bool) string {
	msg := strings.Join(ac.logs, " --> ")
	ac.logs = []string{}
	if print {
		log.Println(msg)
	}
	return msg
}

func (ac *AgentCallContext) Hop(msg string) {
	if msg != "" {
		ac.hops.Add(msg)
	}
}
