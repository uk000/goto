package mcpserver

import (
	"goto/pkg/util"
	"net/http"
)

func GetMCPSessionID(headers http.Header) string {
	sessionID := headers.Get(HeaderXMCPSessionID)
	if sessionID == "" {
		sessionID = headers.Get(HeaderMCPSessionID)
	}
	return sessionID
}

func (server *MCPServer) SetMCPSessionStore(r *http.Request) *util.MCPRequestStore {
	rs := util.GetRequestStore(r)
	sessionID := GetMCPSessionID(r.Header)
	ms := MCPRequestStoreBySession[sessionID]
	if ms == nil {
		ms = &util.MCPRequestStore{}
		MCPRequestStoreBySession[sessionID] = ms
	}
	ms.Ctx = r.Context()
	ms.RS = rs
	return ms
}

func (server *MCPServer) GetMCPSessionStore(sessionID string) *util.MCPRequestStore {
	return MCPRequestStoreBySession[sessionID]
}

func (server *MCPServer) DeleteMCPSessionStore(sessionID string) {
	delete(MCPRequestStoreBySession, sessionID)
}

func (m *MCPServer) getSessionContext(sessionID string) *SessionContext {
	m.lock.Lock()
	defer m.lock.Unlock()
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

func (m *MCPServer) getOrSetSessionContext(sessionID string) (session *SessionContext) {
	if sessionID != "" {
		session = m.getSessionContext(sessionID)
		if session == nil {
			session = &SessionContext{SessionID: sessionID, Server: m, finished: make(chan bool, 10)}
			m.setSessionContext(sessionID, session)
		} else {
			session.Server = m
		}
	}
	return
}
