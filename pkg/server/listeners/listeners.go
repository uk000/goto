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

package listeners

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/server/tcp"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
)

type Listener struct {
	ListenerID    string                      `json:"listenerID"`
	Label         string                      `json:"label"`
	HostLabel     string                      `json:"hostLabel"`
	Port          int                         `json:"port"`
	Protocol      string                      `json:"protocol"`
	L8Proto       string                      `json:"l8Proto"`
	Open          bool                        `json:"open"`
	AutoCert      bool                        `json:"autoCert"`
	AutoSNI       bool                        `json:"autoSNI"`
	CommonName    string                      `json:"commonName"`
	MutualTLS     bool                        `json:"mutualTLS"`
	TLS           bool                        `json:"tls"`
	TCP           *tcp.TCPConfig              `json:"tcp,omitempty"`
	IsHTTP        bool                        `json:"isHTTP"`
	IsHTTP2       bool                        `json:"isH2"`
	IsGRPC        bool                        `json:"isGRPC"`
	IsJSONRPC     bool                        `json:"isJSONRPC"`
	IsMCP         bool                        `json:"isMCP"`
	IsTCP         bool                        `json:"isTCP"`
	IsUDP         bool                        `json:"isUDP"`
	IsXDS         bool                        `json:"isXDS"`
	ProtoAssigned bool                        `json:"-"`
	Generation    int                         `json:"generation"`
	Cert          *tls.Certificate            `json:"-"`
	CACerts       *x509.CertPool              `json:"-"`
	RawCert       []byte                      `json:"-"`
	RawKey        []byte                      `json:"-"`
	CertsCache    map[string]*tls.Certificate `json:"-"`
	Listener      net.Listener                `json:"-"`
	UDPConn       *net.UDPConn                `json:"-"`
	Restarted     bool                        `json:"-"`
	lock          sync.RWMutex                `json:"-"`
}

const (
	PROTO_HTTP     = "HTTP"
	PROTOL_HTTP    = "http"
	PROTOL_HTTPS   = "https"
	PROTOL_H2      = "h2"
	PROTOL_H2C     = "h2c"
	PROTO_GRPC     = "GRPC"
	PROTOL_GRPC    = "grpc"
	PROTO_MCP      = "MCP"
	PROTOL_MCP     = "mcp"
	PROTO_UDP      = "UDP"
	PROTOL_UDP     = "udp"
	PROTO_JSONRPC  = "JSONRPC"
	PROTOL_JSONRPC = "jsonrpc"
	PROTO_XDS      = "XDS"
	PROTOL_XDS     = "xds"
	PROTO_TCP      = "TCP"
	PROTOL_TCP     = "tcp"
	PROTO_TLS      = "TLS"
	PROTOL_TLS     = "tls"
)

var (
	DefaultListener     = newListener(global.Self.ServerPort, PROTOL_HTTP, constants.DefaultCommonName, true)
	DefaultGRPCListener = newListener(global.Self.GRPCPort, PROTOL_GRPC, constants.DefaultCommonName, false)
	listeners           = map[int]*Listener{}
	grpcListeners       = map[int]*Listener{}
	udpListeners        = map[int]*Listener{}
	listenerGenerations = map[int]int{}
	initialListeners    = []*Listener{}
	initialHTTPStarted  = false
	initialMCPStarted   = false
	grpcStarted         = false
	httpStarted         = false
	mcpStarted          = false
	httpServer          *http.Server
	mcpServer           *http.Server
	serveTCP            func(string, int, net.Listener) error
	serveXDS            func(*grpc.Server)
	DefaultLabel        string
	listenersLock       sync.RWMutex
)

func Init() {
	global.Funcs.IsListenerPresent = IsListenerPresent
	global.Funcs.IsListenerOpen = IsListenerOpen
	global.Funcs.GetListenerID = GetListenerID
	global.Funcs.GetListenerLabel = GetListenerLabel
	global.Funcs.GetListenerLabelForPort = GetListenerLabelForPort
	global.Funcs.GetHostLabelForPort = GetHostLabelForPort

	if DefaultLabel == "" {
		DefaultLabel = util.GetHostLabel()
	}
	DefaultListener.Label = DefaultLabel
	DefaultListener.HostLabel = util.GetHostLabel()
	DefaultListener.IsHTTP = true
	DefaultListener.TLS = false
	global.AddGRPCStartWatcher(OnGRPCStart)
	global.AddGRPCStopWatcher(OnGRPCStop)
	global.AddHTTPStartWatcher(OnHTTPStart)
	global.AddHTTPStopWatcher(OnHTTPStop)
	global.AddMCPStartWatcher(OnMCPStart)
	global.AddMCPStopWatcher(OnMCPStop)
	global.AddTCPServeWatcher(ConfigureTCPServer)
}

func OnGRPCStart() {
	grpcStarted = true
	if httpStarted {
		AddInitialHTTPListeners(false)
	}
}

func OnGRPCStop() {
	grpcStarted = false
	for _, l := range grpcListeners {
		l.closeListener()
	}
}

func OnHTTPStart(s *http.Server) {
	httpStarted = true
	httpServer = s
	if grpcStarted {
		AddInitialHTTPListeners(false)
	}
}

func OnHTTPStop() {
	httpStarted = false
	httpServer = nil
}

func OnMCPStart(s *http.Server) {
	mcpStarted = true
	mcpServer = s
	AddInitialHTTPListeners(true)
}

func OnMCPStop() {
	mcpStarted = false
	mcpServer = nil
}

func ConfigureTCPServer(serve func(listenerID string, port int, listener net.Listener) error) {
	serveTCP = serve
}

func ConfigureXDSServer(serve func(*grpc.Server)) {
	serveXDS = serve
}
func newListener(port int, protocol string, cn string, open bool) *Listener {
	return &Listener{Port: port, Protocol: protocol, CommonName: cn, Open: open, CertsCache: map[string]*tls.Certificate{}}
}

func InitDefaultGRPCListener() {
	DefaultGRPCListener.HostLabel = util.GetHostLabel()
	DefaultGRPCListener.Port = global.Self.GRPCPort
	addOrUpdateListener(DefaultGRPCListener)
}

func AddInitialGRPCListeners() {
	for _, l := range initialListeners {
		if l.IsGRPC {
			addOrUpdateListener(l)
		}
	}
}

func AddInitialHTTPListeners(mcp bool) {
	if mcp && !initialMCPStarted || !mcp && !initialHTTPStarted {
		time.Sleep(1 * time.Second)
		for _, l := range initialListeners {
			if (l.IsMCP && mcp) || (!l.IsMCP && l.IsHTTP && !mcp) {
				addOrUpdateListener(l)
			}
		}
		if mcp {
			initialMCPStarted = true
		} else {
			initialHTTPStarted = true
		}
	}
}

func (l *Listener) assignProtocol() {
	isHTTP := strings.EqualFold(l.Protocol, PROTOL_HTTP)
	isHTTPS := strings.EqualFold(l.Protocol, PROTOL_HTTPS)
	isHTTP1 := strings.EqualFold(l.Protocol, "http1")
	isHTTPS1 := strings.EqualFold(l.Protocol, "https1")
	isGRPC := strings.EqualFold(l.Protocol, PROTOL_GRPC)
	isGRPCS := strings.EqualFold(l.Protocol, "grpcs")
	isXDS := strings.EqualFold(l.Protocol, PROTOL_XDS)
	isJSONRPC := strings.EqualFold(l.Protocol, PROTOL_JSONRPC)
	isJSONRPCS := strings.EqualFold(l.Protocol, "jsonrpcs")
	isMCP := strings.EqualFold(l.Protocol, PROTOL_MCP)
	isMCPS := strings.EqualFold(l.Protocol, "mcps")
	isUDP := strings.EqualFold(l.Protocol, PROTOL_UDP)
	isTCPS := strings.EqualFold(l.Protocol, PROTOL_TLS)

	if isHTTP1 || isHTTPS1 {
		if isHTTPS1 {
			l.L8Proto = PROTOL_HTTPS
			l.Protocol = PROTOL_HTTPS
			l.TLS = true
		} else {
			l.L8Proto = PROTOL_HTTP
			l.Protocol = PROTOL_HTTP
		}
		l.IsHTTP = true
		l.IsHTTP2 = false
	} else if isHTTP || isHTTPS {
		l.TLS = isHTTPS
		l.IsHTTP = true
		l.IsHTTP2 = true
		if isHTTPS {
			l.L8Proto = PROTOL_H2
		} else {
			l.L8Proto = PROTOL_H2C
		}
	} else if isGRPC || isGRPCS {
		if isGRPCS {
			l.TLS = true
		}
		l.L8Proto = PROTOL_GRPC
		l.Protocol = PROTOL_GRPC
		l.IsGRPC = true
	} else if isXDS {
		l.L8Proto = PROTOL_XDS
		l.Protocol = PROTOL_GRPC
		l.IsGRPC = true
		l.IsXDS = true
	} else if isJSONRPC || isJSONRPCS || isMCP || isMCPS {
		if isJSONRPCS || isMCPS {
			l.Protocol = PROTOL_HTTPS
			l.TLS = true
		} else {
			l.Protocol = PROTOL_HTTP
		}
		if isMCP || isMCPS {
			l.L8Proto = PROTOL_MCP
		} else {
			l.L8Proto = PROTOL_JSONRPC
		}
		l.IsHTTP = true
		l.IsHTTP2 = true
		l.IsJSONRPC = true
		l.IsMCP = isMCP || isMCPS
	} else if isUDP {
		l.L8Proto = PROTOL_UDP
		l.IsUDP = true
	} else {
		l.L8Proto = PROTOL_TCP
		l.Protocol = PROTOL_TCP
		l.IsTCP = true
		l.TLS = isTCPS
	}
	if l.TLS {
		if l.Cert == nil && l.RawCert == nil {
			l.AutoCert = true
		}
	}
	l.ProtoAssigned = true
}

func AddInitialListeners(portList []string) {
	existing := map[int]bool{}
	l := createPortListener(global.Self.MCPPort, PROTOL_MCP, constants.DefaultCommonName, existing)
	if l != nil {
		listenersLock.Lock()
		listeners[l.Port] = l
		listenersLock.Unlock()
	}
	for i, p := range portList {
		portInfo := strings.Split(p, "/")
		if port, err := strconv.Atoi(portInfo[0]); err == nil && port > 0 && port <= 65535 {
			if i == 0 {
				global.Self.ServerPort = port
				DefaultListener.Port = port
			} else {
				protocol := ""
				if len(portInfo) > 1 && portInfo[1] != "" {
					protocol = strings.ToLower(portInfo[1])
				}
				cn := constants.DefaultCommonName
				if len(portInfo) > 2 && portInfo[2] != "" {
					cn = strings.ToLower(portInfo[2])
				}
				l := createPortListener(port, protocol, cn, existing)
				if l != nil {
					listenersLock.Lock()
					initialListeners = append(initialListeners, l)
					listenersLock.Unlock()
				}
			}
		} else {
			log.Fatalf("Error: Invalid port [%d]\n", port)
		}
	}
}

func createPortListener(port int, protocol, cn string, existing map[int]bool) *Listener {
	if !existing[port] {
		existing[port] = true
		l := newListener(port, protocol, cn, true)
		if protocol == "" {
			protocol = "tcp"
		}
		l.Protocol = protocol
		l.assignProtocol()
		l.Label = util.BuildListenerLabel(l.Port)
		l.HostLabel = util.GetHostLabel()
		return l
	} else {
		log.Fatalf("Error: Duplicate port [%d]\n", port)
	}
	return nil
}

func (l *Listener) serveMCP() {
	go func() {
		msg := fmt.Sprintf("Starting MCP Listener [%s]", l.ListenerID)
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
		}
		log.Println(msg)
		if err := mcpServer.Serve(l.Listener); err != nil {
			log.Printf("MCP Listener [%d]: %s", l.Port, err.Error())
		}
	}()
}

func (l *Listener) serveHTTP() {
	go func() {
		msg := fmt.Sprintf("Starting HTTP Listener [%s]", l.ListenerID)
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
		}
		log.Println(msg)
		if err := httpServer.Serve(l.Listener); err != nil {
			log.Printf("HTTP Listener [%d]: %s", l.Port, err.Error())
		}
	}()
}

func (l *Listener) serveGRPC() {
	if grpcStarted {
		go func() {
			msg := fmt.Sprintf("Starting GRPC Listener %s", l.ListenerID)
			log.Println(msg)
			global.GRPCServer.ServeListener(l)
			events.SendEventForPort(l.Port, "GRPC Listener Started", msg)
		}()
	} else {
		global.GRPCServer.AddListener(l)
	}

}

func (l *Listener) serveTCP() {
	l.TCP.ListenerID = l.ListenerID
	go func() {
		msg := fmt.Sprintf("Starting TCP Listener [%s]", l.ListenerID)
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
		}
		log.Println(msg)
		if err := serveTCP(l.ListenerID, l.Port, l.Listener); err != nil {
			log.Printf("Listener [%d]: %s", l.Port, err.Error())
		}
	}()
}

func (l *Listener) InitListener() bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	var tlsConfig *tls.Config
	if l.AutoCert {
		if l.CommonName == "" {
			l.CommonName = constants.DefaultCommonName
		}
		if cert, err := gototls.CreateCertificate(l.CommonName, fmt.Sprintf("%s-%d", l.Label, l.Port)); err == nil {
			l.Cert = cert
		}
	}
	if l.AutoSNI {
		tlsConfig = &tls.Config{
			GetCertificate: func(chi *tls.ClientHelloInfo) (cert *tls.Certificate, err error) {
				if cert = l.CertsCache[chi.ServerName]; cert != nil {
					return
				}
				if cert, err = gototls.CreateCertificate(chi.ServerName, ""); err == nil {
					l.CertsCache[chi.ServerName] = cert
				}
				return
			},
		}
	} else if l.Cert != nil {
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{*l.Cert},
		}
	} else if len(l.RawCert) > 0 && len(l.RawKey) > 0 {
		if x509Cert, err := tls.X509KeyPair(l.RawCert, l.RawKey); err == nil {
			tlsConfig = &tls.Config{
				Certificates: []tls.Certificate{x509Cert},
			}
		} else {
			log.Printf("Failed to parse certificate with error: %s\n", err.Error())
			return false
		}
	}
	address := fmt.Sprintf("0.0.0.0:%d", l.Port)
	if l.IsUDP {
		if udpAddr, err := net.ResolveUDPAddr("udp4", address); err == nil {
			if udpConn, err := net.ListenUDP("udp", udpAddr); err == nil {
				l.UDPConn = udpConn
				return true
			} else {
				log.Printf("Failed to open UDP listener with error: %s\n", err.Error())
				return false
			}
		} else {
			log.Printf("Failed to resolve UDP address with error: %s\n", err.Error())
			return false
		}
	} else {
		if listener, err := net.Listen("tcp", address); err == nil {
			if tlsConfig != nil {
				if l.MutualTLS {
					tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
					tlsConfig.ClientCAs = l.CACerts
				} else {
					tlsConfig.ClientAuth = tls.NoClientCert
				}
				if l.IsHTTP2 {
					tlsConfig.NextProtos = []string{"h2"}
				}
				listener = tls.NewListener(listener, tlsConfig)
			}
			l.Listener = listener
			return true
		} else {
			log.Printf("Failed to open listener with error: %s\n", err.Error())
			return false
		}
	}
}

func (l *Listener) openListener(serve bool) bool {
	if l.InitListener() {
		l.lock.Lock()
		defer l.lock.Unlock()
		listenerGenerations[l.Port] = listenerGenerations[l.Port] + 1
		l.Generation = listenerGenerations[l.Port]
		l.ListenerID = fmt.Sprintf("%d-%d", l.Port, l.Generation)
		log.Printf("Opening [%s] listener [%s] on port [%d].", l.L8Proto, l.ListenerID, l.Port)
		if serve {
			if l.IsMCP {
				l.serveMCP()
			} else if l.IsHTTP {
				l.serveHTTP()
			} else if l.IsGRPC {
				l.serveGRPC()
			} else if l.IsTCP {
				l.serveTCP()
			}
		}
		l.Open = true
		l.TLS = l.AutoCert || l.Cert != nil || len(l.RawCert) > 0 && len(l.RawKey) > 0
		return true
	}
	return false
}

func (l *Listener) closeListener() {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.Listener != nil {
		l.Listener.Close()
		global.Funcs.CloseConnectionsForPort(l.Port)
		l.Listener = nil
	}
	if l.UDPConn != nil {
		l.UDPConn.Close()
		l.UDPConn = nil
	}
	l.Open = false
}

func (l *Listener) reopenListener() bool {
	listenersLock.RLock()
	old := listeners[l.Port]
	listenersLock.RUnlock()
	if old != nil {
		log.Printf("Closing old listener %s before reopening.", old.ListenerID)
		old.lock.Lock()
		old.Restarted = true
		old.lock.Unlock()
		old.closeListener()
	}
	for i := 0; i < 5; i++ {
		if l.openListener(true) {
			log.Printf("Reopened listener %s on port %d.", l.ListenerID, l.Port)
			return true
		} else {
			log.Printf("Couldn't reopen listener %s on port %d since previous listener is still running. Retrying...", l.ListenerID, l.Port)
			time.Sleep(5 * time.Second)
		}
	}
	return false
}

func validateListener(w http.ResponseWriter, r *http.Request) *Listener {
	port := util.GetIntParamValue(r, "port")
	listenersLock.Lock()
	l := listeners[port]
	listenersLock.Unlock()
	if l == nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Port %d: No listener on the port, or listener not closeable\n", port)
		return nil
	}
	return l
}

func ValidateUDPListener(w http.ResponseWriter, r *http.Request) bool {
	if ok, msg := util.ValidateListener(w, r); !ok {
		events.SendRequestEvent("UDP Configuration Rejected", msg, r)
		return false
	}
	port := util.GetIntParamValue(r, "port")
	l := GetListenerForPort(port)
	if l == nil || !l.IsUDP {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Port %d is not a UDP listener", port)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		events.SendRequestEvent("UDP Configuration Rejected", msg, r)
		return false
	}
	return true
}

func AddGRPCListener(port int, serve bool) (*Listener, error) {
	l := GetListenerForPort(port)
	if l != nil {
		if l.IsGRPC {
			if !l.Open {
				l.openListener(serve)
			}
			return l, nil
		}
		l.closeListener()
	}
	l = newListener(port, "grpc", "", false)
	if err, msg := addOrUpdateListener(l); err > 0 {
		return nil, fmt.Errorf(msg)
	}
	if !l.openListener(serve) {
		return nil, fmt.Errorf("failed to open GRPC listener on port [%d]", port)
	}
	return l, nil
}

func AddListener(port int, isTCP, isUDP bool, sni string) error {
	l := GetListenerForPort(port)
	if l != nil {
		if (isTCP && l.IsTCP) || (isUDP && l.IsUDP) {
			if !l.Open {
				l.openListener(true)
			}
			return nil
		}
		l.closeListener()
	}
	protocol := "tcp"
	if isUDP {
		protocol = "udp"
	}
	l = newListener(port, protocol, sni, false)
	if err, msg := addOrUpdateListener(l); err > 0 {
		return errors.New(msg)
	}
	if !l.openListener(true) {
		return fmt.Errorf("failed to open %s listener on port [%d]", protocol, port)
	}
	return nil
}

func addOrUpdateListenerAndRespond(w http.ResponseWriter, r *http.Request) {
	msg := ""
	l := newListener(0, "", "", false)
	body := util.Read(r.Body)
	if err := util.ReadJson(body, l); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
		events.SendRequestEventJSON("Listener Rejected", err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": body}, r)
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	errorCode := 0
	if errorCode, msg = addOrUpdateListener(l); errorCode > 0 {
		w.WriteHeader(errorCode)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addOrUpdateListener(l *Listener) (int, string) {
	msg := ""
	errorCode := 0
	if l.Label == "" {
		if global.Self.GivenName {
			l.Label = global.Self.Name
		} else {
			l.Label = util.BuildListenerLabel(l.Port)
		}
	}
	l.HostLabel = util.BuildHostLabel(l.Port)
	l.Protocol = strings.ToLower(l.Protocol)
	if l.Port <= 0 || l.Port > 65535 {
		msg = fmt.Sprintf("[Invalid port number: %d]", l.Port)
	}
	if !l.ProtoAssigned {
		l.assignProtocol()
	}
	if l.IsTCP && l.TCP == nil {
		l.TCP, msg = tcp.InitTCPConfig(l.Port, l.TCP)
	}
	if msg != "" {
		events.SendEventJSON("Listener Rejected", msg, l)
		return http.StatusBadRequest, msg
	}

	listenersLock.RLock()
	_, exists := listeners[l.Port]
	listenersLock.RUnlock()
	if exists {
		if l.reopenListener() {
			l.store()
			msg = fmt.Sprintf("Listener %d already present, restarted.", l.Port)
			events.SendEventJSON("Listener Updated", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
		} else {
			errorCode = http.StatusInternalServerError
			msg = fmt.Sprintf("Listener %d already present, failed to restart.", l.Port)
			events.SendEventJSON("Listener Updated", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
		}
	} else {
		if l.Open {
			if l.openListener(true) {
				l.store()
				msg = fmt.Sprintf("Listener %d added and opened.", l.Port)
				events.SendEventJSON("Listener Added", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
			} else {
				errorCode = http.StatusInternalServerError
				msg = fmt.Sprintf("Listener %d added but failed to open.", l.Port)
				events.SendEventJSON("Listener Added", l.HostLabel, map[string]interface{}{"listener": l, "status": msg})
			}
		} else {
			l.store()
			msg = fmt.Sprintf("Listener %d added.", l.Port)
			events.SendEventJSON("Listener Added", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
		}
	}
	if l.IsGRPC {
		grpcListeners[l.Port] = l
	} else if l.IsUDP {
		udpListeners[l.Port] = l
	}
	return errorCode, msg
}

func (l *Listener) store() {
	listenersLock.Lock()
	defer listenersLock.Unlock()
	listeners[l.Port] = l
}

func addListenerCertOrKey(w http.ResponseWriter, r *http.Request, cert bool) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		data := util.ReadBytes(r.Body)
		if len(data) > 0 {
			l.lock.Lock()
			defer l.lock.Unlock()
			l.AutoCert = false
			l.CommonName = ""
			l.Cert = nil
			if cert {
				l.RawCert = data
				msg = fmt.Sprintf("Cert added for listener %d\n", l.Port)
				events.SendRequestEvent("Listener Cert Added", msg, r)
			} else {
				l.RawKey = data
				msg = fmt.Sprintf("Key added for listener %d\n", l.Port)
				events.SendRequestEvent("Listener Key Added", msg, r)
			}
		} else {
			w.WriteHeader(http.StatusBadRequest)
			msg = "No payload"
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func GetListeners() map[int]*Listener {
	listenersView := map[int]*Listener{}
	listenersView[DefaultListener.Port] = DefaultListener
	for port, l := range listeners {
		listenersView[port] = l
	}
	return listenersView
}

func GetListenerPorts() map[int]string {
	listenersView := map[int]string{}
	listenersView[DefaultListener.Port] = DefaultListener.Protocol
	for port, l := range listeners {
		listenersView[port] = l.L8Proto
	}
	return listenersView
}

func GetListenerForPort(port int) *Listener {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	if port == DefaultListener.Port {
		return DefaultListener
	}
	return listeners[port]
}

func GetRequestedListener(r *http.Request) *Listener {
	return GetListenerForPort(util.GetRequestOrListenerPortNum(r))
}

func GetCurrentListener(r *http.Request) *Listener {
	l := GetListenerForPort(util.GetListenerPortNum(r))
	if l == nil {
		l = DefaultListener
	}
	return l
}

func IsListenerPresent(port int) bool {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	return listeners[port] != nil
}

func IsListenerOpen(port int) bool {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	l := listeners[port]
	return l != nil && l.Open
}

func GetListenerID(port int) string {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	if l := listeners[port]; l != nil {
		return l.ListenerID
	}
	return ""
}

func GetListenerLabel(r *http.Request) string {
	return GetListenerLabelForPort(util.GetRequestOrListenerPortNum(r))
}

func GetListenerLabelForPort(port int) string {
	listenersLock.RLock()
	l := listeners[port]
	listenersLock.RUnlock()
	if l != nil {
		return l.Label
	} else if port == global.Self.ServerPort {
		if DefaultListener.Label != "" {
			return DefaultListener.Label
		} else {
			return util.GetHostLabel()
		}
	}
	return util.BuildListenerLabel(port)
}

func GetHostLabelForPort(port int) string {
	listenersLock.RLock()
	l := listeners[port]
	listenersLock.RUnlock()
	if l != nil {
		return l.HostLabel
	} else if port == global.Self.ServerPort {
		return util.GetHostLabel()
	}
	return util.BuildHostLabel(port)
}

func SetListenerLabel(r *http.Request) string {
	port := util.GetRequestOrListenerPortNum(r)
	label := util.GetStringParamValue(r, "label")
	listenersLock.Lock()
	l := listeners[port]
	listenersLock.Unlock()
	if l != nil {
		l.lock.Lock()
		l.Label = label
		l.lock.Unlock()
	} else if label != "" {
		DefaultLabel = label
		DefaultListener.Label = label
	}
	events.SendRequestEvent("Listener Label Updated", label, r)
	return label
}
