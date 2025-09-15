package a2aserver

import (
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/util"
	"log"
	"net/http"
	"sync"

	trpcserver "trpc.group/trpc-go/trpc-a2a-go/server"
	trpctask "trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type A2AServer struct {
	Agents  map[string]*model.Agent `json:"agents"`
	Enabled bool                    `json:"enabled"`
	lock    sync.RWMutex
}

var (
	PortServers = map[int]*A2AServer{}
	lock        sync.RWMutex
)

func GetOrAddServer(port int) *A2AServer {
	lock.Lock()
	defer lock.Unlock()
	s := PortServers[port]
	if s == nil {
		s = newA2AServer()
		PortServers[port] = s
	}
	return s
}

func GetAgent(port int, name string) *model.Agent {
	s := GetOrAddServer(port)
	return s.GetAgent(name)
}

func ClearServer(port int) {
	lock.Lock()
	defer lock.Unlock()
	PortServers[port] = newA2AServer()
}

func newA2AServer() *A2AServer {
	return &A2AServer{
		Agents:  map[string]*model.Agent{},
		Enabled: true,
	}
}

func (a *A2AServer) Enable() {
	a.Enabled = true
}

func (a *A2AServer) Disable() {
	a.Enabled = false
}

func (a *A2AServer) AddAgent(agent *model.Agent) error {
	err := a.PrepareAgent(agent)
	if err == nil {
		a.lock.Lock()
		a.Agents[agent.Card.Name] = agent
		a.lock.Unlock()
	}
	return err
}

func (a *A2AServer) PrepareAgent(agent *model.Agent) error {
	PrepareAgentBehavior(agent)
	tm, err := trpctask.NewMemoryTaskManager(agent.Behavior.Impl)
	if err != nil {
		return err
	}
	srv, err := trpcserver.NewA2AServer(*agent.Card, tm)
	if err == nil {
		agent.Server = srv
	}
	return err
}

func (a *A2AServer) GetAgent(name string) *model.Agent {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return a.Agents[name]
}

func (a *A2AServer) Serve(name string, w http.ResponseWriter, r *http.Request) error {
	log.Println("-------- A2A Request Details --------")
	util.PrintRequest(r)
	agent := a.GetAgent(name)
	if agent == nil {
		return fmt.Errorf("agent [%s] not found", name)
	}
	agent.Serve(w, r)
	return nil
}
