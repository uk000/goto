package proxy

import (
	"errors"
	"goto/pkg/proxy/trackers"
	"goto/pkg/server/response/status"
	"goto/pkg/util"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

type MCPSessionLog struct {
	logCounter       atomic.Int32
	ClientMessageLog map[int]any `json:"clientMessageLog"`
	ServerMessageLog map[int]any `json:"serverMessageLog"`
	err              string
}

type MCPSession struct {
	ID             string         `json:"id"`
	DownstreamAddr string         `json:"downstreamAddr"`
	Log            *MCPSessionLog `json:"log"`
	target         *MCPTarget
	tracker        *trackers.MCPProxyTracker
}

type MCPTarget struct {
	*HTTPTarget
	Tools          map[string]string      `json:"tools"`
	ActiveSessions map[string]*MCPSession `json:"activeSessions"`
	PastSessions   map[string]*MCPSession `json:"pastSessions"`
}

type MCPProxy struct {
	*HTTPProxy
	MCPTracker *trackers.MCPProxyTracker `json:"tracker"`
}

var (
	mcpProxyByPort = map[int]*MCPProxy{}
	mcpProxyLock   sync.RWMutex
)

func WillProxyMCP(port int, r *http.Request) (willProxy bool) {
	rs := util.GetRequestStore(r)
	if rs.IsAdminRequest || !rs.IsMCP {
		return false
	}
	p := GetMCPProxyForPort(port)
	if !p.Enabled || !p.hasAnyTargets() || status.IsForcedStatus(r) {
		return false
	}
	tool := ""
	if reReader, ok := r.Body.(*util.ReReader); ok {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return false
		}
		bodyText := string(b)
		if !strings.Contains(bodyText, "tools/call") {
			return false
		}
		json := util.JSONFromReader(reReader)
		reReader.Rewind()
		tool = json.GetText("params.name")
	}
	if tool == "" {
		return false
	}
	target := p.Targets[tool]
	if target == nil {
		target = p.Targets["*"]
	}
	if target != nil {
		matches := map[string]*TargetMatchInfo{tool: &TargetMatchInfo{target: target.GetHTTPTarget().ProxyTarget, URI: ""}}
		rs.WillProxy = true
		rs.ProxyTargets = matches
		return true
	}
	return
}

func (p *MCPProxy) SetupMCPProxy(server, endpoint, sni, fromTool, toTool string, headers [][]string) error {
	_, err := p.addMCPTarget(server, endpoint, sni, fromTool, toTool, headers)
	if err != nil {
		return err
	}
	return nil
}

func GetMCPProxyForPort(port int) *MCPProxy {
	proxyLock.RLock()
	proxy := mcpProxyByPort[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxy = newMCPProxy(port)
		proxyLock.Lock()
		mcpProxyByPort[port] = proxy
		proxyLock.Unlock()
	}
	return proxy
}

func newMCPProxy(port int) *MCPProxy {
	p := &MCPProxy{
		HTTPProxy:  getHTTPProxyForPort(port),
		MCPTracker: &trackers.MCPProxyTracker{},
	}
	p.initTracker()
	return p
}

func (p *MCPProxy) Init() {
	p.Proxy.init()
	p.initTracker()
}

func (p *MCPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.MCPTracker = trackers.NewMCPProxyTracker()
}

func (p *MCPProxy) RemoveProxy(server string) {
	p.Proxy.deleteProxyTarget(server)
}

func (p *MCPProxy) addMCPTarget(server, endpoint, sni, fromTool, toTool string, headers [][]string) (*MCPTarget, error) {
	if server == "" || endpoint == "" {
		return nil, errors.New("no endpoint given")
	}
	target := &MCPTarget{
		HTTPTarget:     newHTTPTarget(fromTool, endpoint),
		ActiveSessions: map[string]*MCPSession{},
		PastSessions:   map[string]*MCPSession{},
	}
	target.Tools[fromTool] = toTool
	target.parent = target
	p.setupHTTPTarget(target, "", sni, "", "", headers)
	return target, nil
}

func (t *MCPTarget) GetProxyTarget() *ProxyTarget {
	return t.ProxyTarget
}

func (t *MCPTarget) GetHTTPTarget() *HTTPTarget {
	return t.HTTPTarget
}
