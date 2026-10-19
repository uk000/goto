package mcpserver

import (
	"context"
	"fmt"
	"goto/pkg/util"
	"net/http"
	"sync"
	"sync/atomic"
)

// sessionStoreLock guards the request stores of the sessions, and what the requests and the tools put in them.
var sessionStoreLock sync.RWMutex

func GetMCPSessionID(headers http.Header) string {
	sessionID := headers.Get(HeaderXMCPSessionID)
	if sessionID == "" {
		sessionID = headers.Get(HeaderMCPSessionID)
	}
	return sessionID
}

// headerRequestKey is the header that Serve puts on a request to tell it from the other requests of its session, or from
// the ones that have no session at all (all requests of a stateless server). It is what the tools of the request use to
// find the store of it, since they are only given the headers of the request.
const headerRequestKey = "Goto-Mcp-Request-Key"

var requestKeySeq atomic.Uint64

func newRequestKey() string {
	return fmt.Sprintf("req-%d", requestKeySeq.Add(1))
}

// storeKeyOf gives the key of the request store that the headers are for: the one of the request, if it has been given
// one, or else the one of its session.
func storeKeyOf(headers http.Header) string {
	if key := headers.Get(headerRequestKey); key != "" {
		return key
	}
	return GetMCPSessionID(headers)
}

func (server *MCPServer) SetMCPSessionStore(r *http.Request) (*util.MCPRequestStore, *util.RequestStore) {
	rs := util.GetRequestStore(r)
	key := storeKeyOf(r.Header)
	sessionStoreLock.Lock()
	defer sessionStoreLock.Unlock()
	ms := MCPRequestStoreBySession[key]
	if ms == nil {
		ms = &util.MCPRequestStore{}
		MCPRequestStoreBySession[key] = ms
	}
	ms.Ctx = r.Context()
	ms.RS = rs
	return ms, rs
}

func (server *MCPServer) GetMCPSessionStore(sessionID string) *util.MCPRequestStore {
	sessionStoreLock.RLock()
	defer sessionStoreLock.RUnlock()
	return MCPRequestStoreBySession[sessionID]
}

func (server *MCPServer) DeleteMCPSessionStore(sessionID string) {
	sessionStoreLock.Lock()
	defer sessionStoreLock.Unlock()
	delete(MCPRequestStoreBySession, sessionID)
}

// callStoreOf gives the store for a tool call. The store of a session is changed by the requests of the session, so the
// call gets a copy to work with, and puts the status that it forces back when it is done (see setForcedStatus). A call
// that has no session store, like the one of the admin API, gets one that is made from the context of the call.
func (server *MCPServer) callStoreOf(sessionID string, ctx context.Context) (shared, call *util.MCPRequestStore) {
	shared = server.GetMCPSessionStore(sessionID)
	if shared == nil {
		_, rs := util.GetRequestStoreFromContext(ctx)
		return nil, &util.MCPRequestStore{Ctx: ctx, RS: rs}
	}
	sessionStoreLock.RLock()
	defer sessionStoreLock.RUnlock()
	return shared, &util.MCPRequestStore{Ctx: shared.Ctx, RS: shared.RS}
}

func getForcedStatus(ms *util.MCPRequestStore) int {
	sessionStoreLock.RLock()
	defer sessionStoreLock.RUnlock()
	return ms.ForcedStatus
}

func setForcedStatus(ms *util.MCPRequestStore, status int) {
	sessionStoreLock.Lock()
	defer sessionStoreLock.Unlock()
	ms.ForcedStatus = status
}

func (m *MCPServer) getSessionContext(sessionID string) *SessionContext {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.sessionContexts[sessionID]
}

func (m *MCPServer) removeSessionContext(sessionID string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.sessionContexts, sessionID)
}

func (m *MCPServer) setSessionContext(sessionID string, ctx *SessionContext) {
	m.lock.Lock()
	m.sessionContexts[sessionID] = ctx
	m.lock.Unlock()
}

// getOrSetSessionContext gives the context of the session, which is made if the server doesn't have it yet.
func (m *MCPServer) getOrSetSessionContext(sessionID string) (session *SessionContext) {
	if sessionID == "" {
		return nil
	}
	m.lock.Lock()
	defer m.lock.Unlock()
	session = m.sessionContexts[sessionID]
	if session == nil {
		session = &SessionContext{SessionID: sessionID, Server: m, finished: make(chan bool, 10)}
		m.sessionContexts[sessionID] = session
	} else {
		session.Server = m
	}
	return session
}
