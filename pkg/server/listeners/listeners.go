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

package listeners

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/tcp"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
)

const (
	PROTO_HTTP          = "HTTP"
	PROTOL_HTTP         = "http"
	PROTOL_HTTPS        = "https"
	PROTOL_STRICT_HTTPS = "httpss"
	PROTOL_H2           = "h2"
	PROTOL_H2C          = "h2c"
	PROTO_GRPC          = "GRPC"
	PROTOL_GRPC         = "grpc"
	PROTOL_GRPC_SECURE  = "grpcs"
	PROTOL_GRPC_STRICT  = "grpcss"
	PROTO_UDP           = "UDP"
	PROTOL_UDP          = "udp"
	PROTO_JSONRPC       = "JSONRPC"
	PROTOL_JSONRPC      = "jsonrpc"
	PROTO_RPC           = "RPC"
	PROTOL_RPC          = "rpc"
	PROTO_XDS           = "XDS"
	PROTOL_XDS          = "xds"
	PROTO_TCP           = "TCP"
	PROTOL_TCP          = "tcp"
	PROTO_TLS           = "TLS"
	PROTOL_TLS          = "tls"
)

type Listener struct {
	ListenerID       string                           `json:"listenerID"`
	Label            string                           `json:"label"`
	HostLabel        string                           `json:"hostLabel"`
	Port             int                              `json:"port"`
	ForwardPort      int                              `json:"forward"`
	Protocol         string                           `json:"protocol"`
	UDS              string                           `json:"uds,omitempty"`
	L8Proto          string                           `json:"l8Proto"`
	ALPN             *gototls.ALPN                    `json:"alpn"`
	Open             bool                             `json:"open"`
	AutoCert         bool                             `json:"autoCert"`
	AutoSNI          bool                             `json:"autoSNI"`
	CommonName       string                           `json:"commonName"`
	SpiffeID         string                           `json:"spiffeID"`
	AltNames         []string                         `json:"altNames"`
	TLS              bool                             `json:"tls"`
	MTLS             bool                             `json:"mTLS"`
	VerifyClientCert bool                             `json:"verifyClientCert"`
	TCP              *tcp.TCPConfig                   `json:"tcp,omitempty"`
	IsHTTP           bool                             `json:"isHTTP"`
	IsHTTP2          bool                             `json:"isH2"`
	IsGRPC           bool                             `json:"isGRPC"`
	IsJSONRPC        bool                             `json:"isJSONRPC"`
	IsTCP            bool                             `json:"isTCP"`
	IsUDP            bool                             `json:"isUDP"`
	IsXDS            bool                             `json:"isXDS"`
	ProtoAssigned    bool                             `json:"-"`
	Generation       int                              `json:"generation"`
	ServerCert       *gototls.PeerCertInfo            `json:"serverCert"`
	Certificates     []*tls.Certificate               `json:"-"`
	CACerts          *x509.CertPool                   `json:"-"`
	RawCert          []byte                           `json:"-"`
	RawKey           []byte                           `json:"-"`
	CertsCache       map[string]*tls.Certificate      `json:"-"`
	TLSConfig        *tls.Config                      `json:"-"`
	ForwardListener  *Listener                        `json:"-"`
	Listener         net.Listener                     `json:"-"`
	UDPConn          *net.UDPConn                     `json:"-"`
	Restarted        bool                             `json:"-"`
	peerCertInfos    map[string]*gototls.PeerCertInfo `json:"-"`
	lock             sync.RWMutex                     `json:"-"`
}

// listenerJSON has the fields of a listener, and none of its methods, so that it is marshaled as the struct that it is.
type listenerJSON Listener

// MarshalJSON gives the listener as it is at one moment: a listener is reported while it opens, closes, and is
// handshaken with, which change what is reported.
func (l *Listener) MarshalJSON() ([]byte, error) {
	l.lock.RLock()
	defer l.lock.RUnlock()
	return json.Marshal((*listenerJSON)(l))
}

var (
	DefaultListener       = newListener(global.Self.ServerPort, PROTOL_HTTP, global.ServerConfig.CommonName, true)
	DefaultGRPCListener   = newListener(global.Self.GRPCPort, PROTOL_GRPC, global.ServerConfig.CommonName, false)
	listeners             = map[int]*Listener{}
	grpcListeners         = map[int]*Listener{}
	udpListeners          = map[int]*Listener{}
	listenerGenerations   = map[int]int{}
	initialListeners      = []*Listener{}
	initialHTTPStarted    = false
	initialJSONRPCStarted = false
	initialTCPStarted     = false
	// The flags of the servers that are started are set by the watchers of the servers, and read as listeners open.
	grpcStarted     atomic.Bool
	httpStarted     atomic.Bool
	jsonRPCStarted  atomic.Bool
	httpServer      *http.Server
	h2Server        *http2.Server
	jsonRPCServer   *http.Server
	serveTCP        func(string, int, net.Listener) error
	serveXDS        func(*grpc.Server)
	DefaultLabel    string
	listenersLock   sync.RWMutex
	generationsLock sync.Mutex
	// reopenRetryDelay is how long ReopenListener waits before it tries again to open a port that is still in use, and
	// initialListenersDelay is how long the initial HTTP/JSONRPC listeners wait for the servers to be ready to serve.
	reopenRetryDelay      = 5 * time.Second
	initialListenersDelay = 1 * time.Second
)

// initialDefs are what the startup arguments define for the ports of the initial listeners, by port (guarded by
// listenersLock). The listeners themselves change as they are opened, closed and reconfigured, and can't tell what they
// were defined as.
var initialDefs = map[int]*PortProtoConfig{}

func Init() {
	global.Funcs.IsListenerPresent = IsListenerPresent
	global.Funcs.IsListenerOpen = IsListenerOpen
	global.Funcs.GetListenerID = GetListenerID
	global.Funcs.GetListenerLabel = GetListenerLabel
	global.Funcs.GetListenerLabelForPort = GetListenerLabelForPort
	global.Funcs.IsListenerTLS = IsListenerTLS
	global.Funcs.GetCertInfo = GetPeerCert

	if DefaultLabel == "" {
		DefaultLabel = global.Self.HostLabel
	}
	DefaultListener.Label = DefaultLabel
	DefaultListener.HostLabel = global.Self.HostLabel
	DefaultListener.IsHTTP = true
	DefaultListener.TLS = false
	global.AddGRPCStartWatcher(OnGRPCStart)
	global.AddGRPCStopWatcher(OnGRPCStop)
	global.AddHTTPStartWatcher(OnHTTPStart)
	global.AddHTTPStopWatcher(OnHTTPStop)
	global.AddJSONRPCStartWatcher(OnJSONRPCStart)
	global.AddJSONRPCStopWatcher(OnJSONRPCStop)
	global.AddTCPServeWatcher(ConfigureTCPServer)
}

func OnGRPCStart() {
	grpcStarted.Store(true)
	if httpStarted.Load() {
		startInitialHTTPListeners(false)
	}
}

func OnGRPCStop() {
	grpcStarted.Store(false)
	// The listeners are closed outside of the lock: closing takes the lock of the listener, and closing the connections
	// of the port takes other locks.
	listenersLock.RLock()
	toClose := make([]*Listener, 0, len(grpcListeners))
	for _, l := range grpcListeners {
		toClose = append(toClose, l)
	}
	listenersLock.RUnlock()
	for _, l := range toClose {
		l.Close()
	}
}

func OnHTTPStart(s *http.Server, h2s *http2.Server) {
	httpStarted.Store(true)
	httpServer = s
	h2Server = h2s
	if grpcStarted.Load() {
		startInitialHTTPListeners(false)
	}
}

func OnHTTPStop() {
	httpStarted.Store(false)
	httpServer = nil
}

func OnJSONRPCStart(s *http.Server, h2s *http2.Server) {
	jsonRPCStarted.Store(true)
	jsonRPCServer = s
	startInitialHTTPListeners(true)
}

func OnJSONRPCStop() {
	jsonRPCStarted.Store(false)
	jsonRPCServer = nil
}

func ConfigureTCPServer(serve func(string, int, net.Listener) error) {
	serveTCP = serve
	startInitialTCPListeners()
}

func ConfigureXDSServer(serve func(*grpc.Server)) {
	serveXDS = serve
}

func newListener(port int, protocol string, cn string, open bool) *Listener {
	l := &Listener{
		Port:       port,
		Protocol:   protocol,
		CommonName: cn,
		Open:       open,
	}
	l.Init()
	return l
}

func (l *Listener) Init() {
	l.CertsCache = map[string]*tls.Certificate{}
	l.peerCertInfos = map[string]*gototls.PeerCertInfo{}
}

func InitDefaultGRPCListener() {
	DefaultGRPCListener.HostLabel = global.Self.HostLabel
	DefaultGRPCListener.Port = global.Self.GRPCPort
	AddOrUpdateListener(DefaultGRPCListener)
}

func AddInitialGRPCListeners() {
	for _, l := range initialListeners {
		if l.IsGRPC {
			if err, _ := AddOrUpdateListener(l); err != nil {
				panic(err)
			}
		}
	}
}

func startInitialHTTPListeners(jsonRPC bool) {
	if jsonRPC && !initialJSONRPCStarted || !jsonRPC && !initialHTTPStarted {
		time.Sleep(initialListenersDelay)
		for _, l := range initialListeners {
			if (l.IsJSONRPC && jsonRPC) || (!l.IsJSONRPC && l.IsHTTP && !jsonRPC) {
				if err, _ := AddOrUpdateListener(l); err != nil {
					panic(err)
				}
			}
		}
		if jsonRPC {
			initialJSONRPCStarted = true
		} else {
			initialHTTPStarted = true
		}
	}
	if initialHTTPStarted && initialJSONRPCStarted && initialTCPStarted {
		allInitialListenersStarted()
	}
}

func startInitialTCPListeners() {
	for _, l := range initialListeners {
		if l.IsTCP {
			if err, _ := AddOrUpdateListener(l); err != nil {
				panic(err)
			}
		}
	}
	initialTCPStarted = true
	if initialHTTPStarted && initialJSONRPCStarted && initialTCPStarted {
		allInitialListenersStarted()
	}
}

func allInitialListenersStarted() {
	LinkForwardListeners()
	global.OnListenersStarted()
}

func (l *Listener) assignProtocol() {
	isHTTP := strings.EqualFold(l.Protocol, PROTOL_HTTP)
	isHTTPSS := strings.EqualFold(l.Protocol, PROTOL_STRICT_HTTPS)
	isHTTPS := isHTTPSS || strings.EqualFold(l.Protocol, PROTOL_HTTPS)
	isHTTP1 := strings.EqualFold(l.Protocol, "http1")
	isHTTPS1 := strings.EqualFold(l.Protocol, "https1")
	isGRPCSS := strings.EqualFold(l.Protocol, PROTOL_GRPC_STRICT)
	isGRPCS := isGRPCSS || strings.EqualFold(l.Protocol, PROTOL_GRPC_SECURE)
	isGRPC := isGRPCS || strings.EqualFold(l.Protocol, PROTOL_GRPC)
	isXDS := strings.EqualFold(l.Protocol, PROTOL_XDS)
	isJSONRPC := strings.EqualFold(l.Protocol, PROTOL_JSONRPC) || strings.EqualFold(l.Protocol, PROTOL_RPC)
	isJSONRPCS := strings.EqualFold(l.Protocol, "jsonrpcs") || strings.EqualFold(l.Protocol, "rpcs")
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
		l.VerifyClientCert = isHTTPSS
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
			l.VerifyClientCert = isGRPCSS
		}
		l.L8Proto = PROTOL_GRPC
		l.Protocol = PROTOL_GRPC
		l.IsGRPC = true
	} else if isXDS {
		l.L8Proto = PROTOL_XDS
		l.Protocol = PROTOL_GRPC
		l.IsGRPC = true
		l.IsXDS = true
	} else if isJSONRPC || isJSONRPCS {
		if isJSONRPCS {
			l.Protocol = PROTOL_HTTPS
			l.TLS = true
		} else {
			l.Protocol = PROTOL_HTTP
		}
		l.L8Proto = PROTOL_JSONRPC
		l.IsHTTP = true
		l.IsHTTP2 = true
		l.IsJSONRPC = true
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
		if l.Certificates == nil && l.RawCert == nil {
			l.AutoCert = true
		}
	}
	l.ProtoAssigned = true
}

func parseListenerPorts(portList []string) []*PortProtoConfig {
	portProtos := []*PortProtoConfig{}
	for i, p := range portList {
		pp := strings.Split(p, "/")
		port, err := strconv.Atoi(pp[0])
		if i == 0 && port <= 0 {
			port = global.Self.ServerPort
			err = nil
		}
		if err == nil && port > 0 && port <= 65535 {
			portProto := &PortProtoConfig{
				Port: port,
			}
			if i == 0 {
				global.Self.ServerPort = port
				portProto.Proto = PROTOL_HTTP
			} else {
				if len(pp) > 1 && pp[1] != "" {
					portProto.Proto = strings.ToLower(pp[1])
				}
				if len(pp) > 2 && pp[2] != "" {
					portProto.Verify = strings.EqualFold(pp[2], "mtlss")
					portProto.MTLS = portProto.Verify || strings.EqualFold(pp[2], "mtls")
					if strings.HasPrefix(pp[2], "->") {
						port2, err2 := strconv.Atoi(strings.TrimPrefix(pp[2], "->"))
						if err2 == nil && port2 > 0 && port2 <= 65535 {
							portProto.Forward = port2
						}
					}
				}
			}
			portProtos = append(portProtos, portProto)
		} else {
			log.Fatalf("Error: Invalid port [%d]\n", port)
		}
	}
	return portProtos
}

type PortProtoConfig struct {
	Port    int
	Forward int
	Proto   string
	MTLS    bool
	Verify  bool
}

func AddInitialListeners(portList []string) {
	portProtos := parseListenerPorts(portList)
	if len(portProtos) == 0 {
		log.Fatalf("Error: Empty port list")
		return
	}
	existing := map[int]bool{}
	l := createPortListener(global.Self.JSONRPCPort, 0, PROTOL_JSONRPC, false, false, existing)
	if l != nil {
		listenersLock.Lock()
		listeners[l.Port] = l
		listenersLock.Unlock()
	}
	DefaultListener.Port = portProtos[0].Port
	DefaultListener.Protocol = portProtos[0].Proto
	DefaultListener.assignProtocol()
	DefaultListener.Label = util.BuildListenerLabel(portProtos[0].Port)
	DefaultListener.HostLabel = global.Self.HostLabel
	for i := 1; i < len(portProtos); i++ {
		addInitialListener(portProtos[i], existing)
	}
}

func addInitialListener(pp *PortProtoConfig, existing map[int]bool) *Listener {
	l := createPortListener(pp.Port, pp.Forward, pp.Proto, pp.MTLS, pp.Verify, existing)
	if l != nil {
		listenersLock.Lock()
		initialListeners = append(initialListeners, l)
		initialDefs[pp.Port] = pp
		listenersLock.Unlock()
	}
	return l
}

// AddInitialListener adds one listener the way AddInitialListeners adds the ones of the startup arguments: it is started
// with the process, and it is what the port goes back to when a config that took the port over goes away. It gives the
// listener, or nil if the port is already one of the initial listeners.
func AddInitialListener(pp *PortProtoConfig) *Listener {
	listenersLock.RLock()
	_, duplicate := initialDefs[pp.Port]
	listenersLock.RUnlock()
	return addInitialListener(pp, map[int]bool{pp.Port: duplicate})
}

// RemoveInitialListener makes the port not one of the initial listeners anymore, which leaves a listener on the port
// that is already open as it is.
func RemoveInitialListener(port int) {
	listenersLock.Lock()
	defer listenersLock.Unlock()
	delete(initialDefs, port)
	kept := make([]*Listener, 0, len(initialListeners))
	for _, l := range initialListeners {
		if l.Port != port {
			kept = append(kept, l)
		}
	}
	initialListeners = kept
}

// RestoreInitialListener gives a port that is one of the initial listeners back to what the startup arguments define for
// it, after the config that took it over is gone. It tells if the port is one of them: a port that is not has nothing
// to go back to, and it is for the caller to close and remove its listener. A listener that is already as the
// arguments define it is left alone, as it is not worth a restart.
func RestoreInitialListener(port int) bool {
	listenersLock.RLock()
	pp := initialDefs[port]
	listenersLock.RUnlock()
	if pp == nil {
		return false
	}
	l := createPortListener(pp.Port, pp.Forward, pp.Proto, pp.MTLS, pp.Verify, map[int]bool{})
	if live := GetListenerForPort(port); live != nil && live.isOpen() && !Diff(live, l) {
		return true
	}
	if err, msg := AddOrUpdateListener(l); err != nil {
		log.Printf("[ERROR]: Failed to restore the listener of the startup arguments on port [%d]: %s", port, msg)
	}
	return true
}

func createPortListener(port, forward int, protocol string, mtls, verify bool, existing map[int]bool) *Listener {
	if !existing[port] {
		existing[port] = true
		l := newListener(port, protocol, global.ServerConfig.CommonName, true)
		l.ForwardPort = forward
		if protocol == "" {
			protocol = "http"
		}
		l.Protocol = protocol
		l.assignProtocol()
		l.Label = util.BuildListenerLabel(l.Port)
		l.HostLabel = global.Self.HostLabel
		l.MTLS = mtls
		if verify {
			l.VerifyClientCert = verify
		}
		return l
	} else {
		log.Printf("Error: Duplicate port [%d]\n", port)
	}
	return nil
}

func LinkForwardListeners() {
	listenersLock.Lock()
	defer listenersLock.Unlock()
	for _, l := range listeners {
		if l.ForwardPort > 0 {
			if l2 := listeners[l.ForwardPort]; l2 != nil {
				l.ForwardListener = l2
			} else if l.ForwardListener != nil {
				l.ForwardListener = nil
			}
		}
	}
}

// describeTLS describes the TLS config of the listener for the log. The listeners are served by goroutines, which must
// not read the listener, as it keeps changing after it is opened: what they need is read before they start.
func (l *Listener) describeTLS() string {
	if !l.TLS {
		return ""
	}
	msg := fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
	if l.MTLS {
		msg += " With mTLS"
	}
	if l.IsHTTP {
		msg += fmt.Sprintf(" [Verify: %t]", l.VerifyClientCert)
	}
	return msg
}

func (l *Listener) serveJSONRPC() {
	// The server is the one that is there when the listener opens, and not the one that is there when the goroutine runs.
	server := jsonRPCServer
	if server == nil {
		log.Printf("JSONRPC Listener [%s] is open, but the JSONRPC server is not running to serve it", l.ListenerID)
		return
	}
	ln, port := l.Listener, l.Port
	msg := fmt.Sprintf("Starting JSONRPC Listener [%s]", l.ListenerID) + l.describeTLS()
	go func() {
		log.Println(msg)
		if err := server.Serve(ln); err != nil {
			log.Printf("JSONRPC Listener [%d]: %s", port, err.Error())
		}
	}()
}

func (l *Listener) serveHTTP() {
	server := httpServer
	if server == nil {
		log.Printf("HTTP Listener [%s] is open, but the HTTP server is not running to serve it", l.ListenerID)
		return
	}
	ln, port := l.Listener, l.Port
	msg := fmt.Sprintf("Starting HTTP Listener [%s]", l.ListenerID) + l.describeTLS()
	go func() {
		log.Println(msg)
		if err := server.Serve(ln); err != nil {
			log.Printf("HTTP Listener [%d]: %s", port, err.Error())
		}
	}()
}

func (l *Listener) serveGRPC() {
	server := global.GRPCServer
	if server == nil {
		log.Printf("GRPC Listener [%s] is open, but there is no GRPC server to serve it", l.ListenerID)
		return
	}
	if grpcStarted.Load() {
		msg := fmt.Sprintf("Starting GRPC Listener %s", l.ListenerID)
		go func() {
			log.Println(msg)
			server.ServeListener(l)
		}()
	} else {
		server.AddListener(l)
	}
}

func (l *Listener) serveTCP() {
	serve := serveTCP
	if serve == nil {
		log.Printf("TCP Listener [%s] is open, but there is no TCP server to serve it", l.ListenerID)
		return
	}
	l.TCP.ListenerID = l.ListenerID
	id, ln, port := l.ListenerID, l.Listener, l.Port
	log.Println(fmt.Sprintf("Starting TCP Listener [%s]", id) + l.describeTLS())
	go func() {
		log.Printf("TCP Listener [%s] Listening\n", id)
		if err := serve(id, port, ln); err != nil {
			log.Printf("Listener [%d]: %s", port, err.Error())
		}
	}()
}

func (l *Listener) SetCertificates(certs []*tls.Certificate) {
	if len(certs) > 0 {
		l.Certificates = certs
		uris := []string{}
		leaf := gototls.IdentifyLeaf(certs)
		if leaf != nil {
			for _, uri := range leaf.URIs {
				uris = append(uris, uri.String())
			}
			protos := []string{}
			if l.ALPN != nil {
				protos = l.ALPN.Protos
			}
			l.ServerCert = &gototls.PeerCertInfo{
				StartAt:  time.Now(),
				Subject:  gototls.SubjectToString(leaf),
				DNSNames: leaf.DNSNames,
				URIs:     uris,
				Issuer:   gototls.IssuerToString(leaf),
				ALPN:     protos,
			}
		}
	}
}

func (l *Listener) GetCertificate(chi *tls.ClientHelloInfo) (cert *tls.Certificate, err error) {
	// Handshakes run concurrently, so the cache is accessed with the lock of the listener.
	l.lock.RLock()
	cert = l.CertsCache[chi.ServerName]
	domains := []string{chi.ServerName, l.CommonName}
	domains = append(domains, l.AltNames...)
	spiffeID := l.SpiffeID
	l.lock.RUnlock()
	if cert != nil {
		return
	}
	if cert, err = gototls.CreateCertificate(domains, spiffeID, ""); err == nil {
		l.lock.Lock()
		l.CertsCache[chi.ServerName] = cert
		l.lock.Unlock()
	}
	return
}

func (l *Listener) alpnHandler(alpn string) func(s *http.Server, conn *tls.Conn, h http.Handler) {
	h2s := h2Server
	return func(s *http.Server, conn *tls.Conn, h http.Handler) {
		if h2s == nil {
			log.Printf("Listener %s: No HTTP2 server to serve negotiated ALPN: %s", l.Label, alpn)
			conn.Close()
			return
		}
		log.Printf("Listener %s: Serving negotiated ALPN: %s ---", l.Label, alpn)
		var ctx context.Context
		if s.BaseContext != nil {
			ctx = s.BaseContext(l.Listener)
		} else {
			ctx = context.Background()
		}
		h2s.ServeConn(conn, &http2.ServeConnOpts{
			BaseConfig: s,
			Context:    util.WithConn(ctx, conn),
			Handler:    h,
		})
	}
}

func (l *Listener) prepareMTLS(tlsConfig *tls.Config) {
	if l.VerifyClientCert {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	} else {
		tlsConfig.ClientAuth = tls.RequireAnyClientCert
	}
	tlsConfig.ClientCAs = l.CACerts
	server := httpServer
	if server == nil {
		return
	}
	if server.TLSNextProto == nil {
		server.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	}
	if l.ALPN != nil {
		for _, alpn := range l.ALPN.Protos {
			server.TLSNextProto[alpn] = l.alpnHandler(alpn)
		}
	}
	for _, alpn := range tlsConfig.NextProtos {
		server.TLSNextProto[alpn] = l.alpnHandler(alpn)
	}
}

func (l *Listener) prepareTLS() *tls.Config {
	tlsConfig := &tls.Config{}
	if l.AutoSNI {
		tlsConfig.GetCertificate = l.GetCertificate
	} else if l.Certificates != nil {
		for _, c := range l.Certificates {
			tlsConfig.Certificates = append(tlsConfig.Certificates, *c)
		}
	} else if len(l.RawCert) > 0 && len(l.RawKey) > 0 {
		if x509Cert, err := tls.X509KeyPair(l.RawCert, l.RawKey); err == nil {
			tlsConfig.Certificates = []tls.Certificate{x509Cert}
		} else {
			log.Printf("Failed to parse certificate with error: %s\n", err.Error())
			return nil
		}
	}
	tlsConfig.GetConfigForClient = gototls.HandleClientHello(strconv.Itoa(l.Port), "Server-"+l.Label, tlsConfig, l.StoreALPN,
		l.StorePeerCertInfo, l.StoreSNI, l.UpdatePeerStatus)
	if l.IsHTTP2 {
		tlsConfig.NextProtos = []string{"h2"}
	}
	if l.ALPN != nil && len(l.ALPN.Protos) > 0 {
		tlsConfig.NextProtos = append(tlsConfig.NextProtos, l.ALPN.Protos...)
	}
	if l.MTLS {
		l.prepareMTLS(tlsConfig)
	} else if l.VerifyClientCert {
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	} else {
		tlsConfig.ClientAuth = tls.NoClientCert
	}
	tlsConfig.InsecureSkipVerify = true
	return tlsConfig
}

// InitListener binds the port of the listener, which makes it open if it works out, and not open if it doesn't.
func (l *Listener) InitListener() (opened bool) {
	l.lock.Lock()
	defer l.lock.Unlock()
	defer func() { l.Open = opened }()
	if l.AutoCert && l.SpiffeID == "" {
		if l.CommonName == "" {
			l.CommonName = global.ServerConfig.CommonName
		}
		domains := []string{l.CommonName}
		domains = append(domains, l.AltNames...)
		if cert, err := gototls.CreateCertificate(domains, l.SpiffeID, fmt.Sprintf("%s-%d", l.Label, l.Port)); err == nil {
			l.SetCertificates([]*tls.Certificate{cert})
		} else {
			log.Printf("Failed to create certificate for Listener [%s], Domains %+v, SpiffeID [%s], with error: %s\n", l.Label, domains, l.SpiffeID, err.Error())
		}
	}
	address := fmt.Sprintf("0.0.0.0:%d", l.Port)
	if l.UDS != "" {
		if listener, err := util.ListenUDS(l.UDS); err == nil {
			if l.TLS {
				if tlsConfig := l.prepareTLS(); tlsConfig == nil {
					listener.Close()
					return false
				} else {
					l.TLSConfig = tlsConfig
					listener = gototls.NewTLSInspector(l.Port, l.Label, listener, tlsConfig)
				}
			}
			util.RegisterUDSPort(l.UDS, l.Port)
			l.Listener = listener
			return true
		} else {
			log.Printf("Failed to open UDS listener [%s] with error: %s\n", l.UDS, err.Error())
			return false
		}
	}
	if l.IsUDP {
		if udpAddr, err := net.ResolveUDPAddr("udp4", address); err == nil {
			if udpConn, err := net.ListenUDP("udp", udpAddr); err == nil {
				l.UDPConn = udpConn
				l.Open = true
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
			if l.TLS {
				if tlsConfig := l.prepareTLS(); tlsConfig == nil {
					// The port was taken, and must not stay taken by a listener that is not used.
					listener.Close()
					return false
				} else {
					l.TLSConfig = tlsConfig
					listener = gototls.NewTLSInspector(l.Port, l.Label, listener, tlsConfig)
				}
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
		generationsLock.Lock()
		listenerGenerations[l.Port]++
		l.Generation = listenerGenerations[l.Port]
		generationsLock.Unlock()
		l.ListenerID = fmt.Sprintf("%d-%d", l.Port, l.Generation)
		if l.UDS != "" {
			log.Printf("Opening [%s] listener [%s] on port [%d] via UDS [%s].", l.L8Proto, l.ListenerID, l.Port, l.UDS)
		} else {
			log.Printf("Opening [%s] listener [%s] on port [%d].", l.L8Proto, l.ListenerID, l.Port)
		}
		if serve {
			if l.IsJSONRPC {
				l.serveJSONRPC()
			} else if l.IsHTTP {
				l.serveHTTP()
			} else if l.IsGRPC {
				l.serveGRPC()
			} else if l.IsTCP {
				l.serveTCP()
			}
		}
		l.Open = true
		l.TLS = l.AutoCert || l.Certificates != nil || len(l.RawCert) > 0 && len(l.RawKey) > 0
		return true
	}
	return false
}

func (l *Listener) isOpen() bool {
	l.lock.RLock()
	defer l.lock.RUnlock()
	return l.Open
}

func (l *Listener) Close() {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.UDS != "" {
		log.Printf("Closing %s listener: %d (UDS: %s)\n", l.Protocol, l.Port, l.UDS)
	} else {
		log.Printf("Closing %s listener: %d\n", l.Protocol, l.Port)
	}
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

func (l *Listener) ReopenListener() bool {
	listenersLock.RLock()
	old := listeners[l.Port]
	listenersLock.RUnlock()
	if old != nil {
		log.Printf("Closing old listener %s before reopening.", old.ListenerID)
		old.lock.Lock()
		old.Restarted = true
		old.lock.Unlock()
		old.Close()
	}
	for i := 0; i < 5; i++ {
		if l.openListener(true) {
			log.Printf("Reopened listener %s on port %d.", l.ListenerID, l.Port)
			return true
		} else {
			log.Printf("Couldn't reopen listener %s on port %d since previous listener is still running. Retrying...", l.ListenerID, l.Port)
			time.Sleep(reopenRetryDelay)
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
	if ok, _ := util.ValidateListener(w, r); !ok {
		return false
	}
	port := util.GetIntParamValue(r, "port")
	l := GetListenerForPort(port)
	if l == nil || !l.IsUDP {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Port %d is not a UDP listener", port)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
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
		l.Close()
	}
	l = newListener(port, "grpc", "", false)
	if err, _ := AddOrUpdateListener(l); err != nil {
		return nil, err
	}
	// A listener that took over a port that had another one is opened by AddOrUpdateListener, and must not be opened twice.
	if !l.isOpen() && !l.openListener(serve) {
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
		l.Close()
	}
	protocol := "tcp"
	if isUDP {
		protocol = "udp"
	}
	l = newListener(port, protocol, sni, false)
	if err, _ := AddOrUpdateListener(l); err != nil {
		return err
	}
	if !l.isOpen() && !l.openListener(true) {
		return fmt.Errorf("failed to open %s listener on port [%d]", protocol, port)
	}
	return nil
}

func addOrUpdateListenerAndRespond(w http.ResponseWriter, r *http.Request) {
	msg := ""
	var err error
	l := newListener(0, "", "", false)
	body := util.Read(r.Body)
	if err = util.ReadJson(body, l); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	if err, msg = AddOrUpdateListener(l); err != nil {
		w.WriteHeader(http.StatusBadRequest)
	}
	LinkForwardListeners()
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func AddOrUpdateListener(l *Listener) (error, string) {
	msg := ""
	l.Init()
	if l.Label == "" {
		l.Label = util.BuildListenerLabel(l.Port)
	}
	l.HostLabel = global.BuildHostLabel()
	l.Protocol = strings.ToLower(l.Protocol)
	if l.Port <= 0 || l.Port > 65535 {
		msg = fmt.Sprintf("[Invalid port number: %d]", l.Port)
	}
	if !l.ProtoAssigned {
		l.assignProtocol()
	}
	if l.IsTCP && l.TCP == nil && msg == "" {
		// The config of the listener is not made when the port is not valid, and the message of that is not replaced.
		l.TCP, msg = tcp.InitTCPConfig(l.Port, l.TCP)
	}
	if msg == "" && l.UDS != "" {
		msg = validateUDS(l)
	}
	if msg != "" {
		return errors.New(msg), msg
	}

	listenersLock.RLock()
	_, exists := listeners[l.Port]
	listenersLock.RUnlock()
	if exists {
		if l.ReopenListener() {
			l.store()
			msg = fmt.Sprintf("Listener %d already present, restarted.", l.Port)
		} else {
			msg = fmt.Sprintf("Listener %d already present, failed to restart.", l.Port)
		}
	} else {
		if l.Open {
			if l.openListener(true) {
				l.store()
				msg = fmt.Sprintf("Listener %d added and opened.", l.Port)
			} else {
				msg = fmt.Sprintf("Listener %d added but failed to open.", l.Port)
			}
		} else {
			l.store()
			msg = fmt.Sprintf("Listener %d added.", l.Port)
		}
	}
	listenersLock.Lock()
	if l.IsGRPC {
		grpcListeners[l.Port] = l
	} else if l.IsUDP {
		udpListeners[l.Port] = l
	}
	listenersLock.Unlock()
	return nil, msg
}

// validateUDS tells why the listener can't use the unix domain socket path that it has, or gives "" if it can.
func validateUDS(l *Listener) string {
	if l.IsUDP {
		return "[UDS is not supported for UDP listeners]"
	}
	if l.UDS == global.ServerConfig.UDSPath {
		return fmt.Sprintf("[UDS path %s is already used by the server]", l.UDS)
	}
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	for port, other := range listeners {
		if port != l.Port && other.UDS == l.UDS {
			return fmt.Sprintf("[UDS path %s is already used by listener %d]", l.UDS, port)
		}
	}
	return ""
}

// RemoveListener takes the listener out of the ones that the process has. A listener that is not the one registered for
// its port is left out of it: it only has the port in common with the registered one, which keeps the port bound and
// would be unreachable (and so never closed) if it were dropped.
func RemoveListener(l *Listener) {
	listenersLock.Lock()
	defer listenersLock.Unlock()
	if l.IsGRPC {
		if grpcListeners[l.Port] == l {
			delete(grpcListeners, l.Port)
		}
	} else if l.IsUDP {
		if udpListeners[l.Port] == l {
			delete(udpListeners, l.Port)
		}
	}
	if listeners[l.Port] == l {
		delete(listeners, l.Port)
	}
}

func (l *Listener) store() {
	listenersLock.Lock()
	defer listenersLock.Unlock()
	listeners[l.Port] = l
}

func addListenerCertOrKey(w http.ResponseWriter, r *http.Request, isKey bool) {
	if l := validateListener(w, r); l != nil {
		msg := ""
		data := util.ReadBytes(r.Body)
		var key, cert []byte
		if isKey {
			key = data
		} else {
			cert = data
		}
		if err := AddListenerCert(l.Port, key, cert, false); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = err.Error()
		} else if isKey {
			msg = fmt.Sprintf("Key added for listener %d\n", l.Port)
		} else {
			msg = fmt.Sprintf("Cert added for listener %d\n", l.Port)
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	}
}

func AddListenerCert(port int, key []byte, cert []byte, reopen bool) error {
	l := GetListenerForPort(port)
	if l == nil {
		return fmt.Errorf("No listener on port [%d]", port)
	}
	if len(key) == 0 && len(cert) == 0 {
		return fmt.Errorf("No payload")
	}
	l.lock.Lock()
	l.AutoCert = false
	l.CommonName = "<from cert>"
	l.Certificates = nil
	if len(key) > 0 {
		l.RawKey = key
	}
	if len(cert) > 0 {
		l.RawCert = cert
	}
	l.lock.Unlock()
	if reopen {
		if !l.ReopenListener() {
			return fmt.Errorf("Failed to reopen listener on port [%d]", port)
		}
	}
	return nil
}

func RemoveListenerCert(port int) error {
	l := GetListenerForPort(port)
	if l == nil {
		return fmt.Errorf("No listener on port [%d]", port)
	}
	l.lock.Lock()
	l.RawKey = nil
	l.RawCert = nil
	l.Certificates = nil
	l.TLS = false
	l.AutoCert = false
	l.CommonName = ""
	l.lock.Unlock()
	if !l.ReopenListener() {
		return fmt.Errorf("Failed to reopen listener after removing cert on port [%d]", port)
	}
	return nil
}

func GetListeners() map[int]*Listener {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	listenersView := map[int]*Listener{}
	listenersView[DefaultListener.Port] = DefaultListener
	for port, l := range listeners {
		listenersView[port] = l
	}
	return listenersView
}

func GetListenerPorts() map[int]string {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
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

func IsListenerTLS(port int) bool {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	l := listeners[port]
	return l != nil && l.TLS
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
			return global.Self.HostLabel
		}
	}
	return util.BuildListenerLabel(port)
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
	return label
}

// getPeerCertInfo gives the info of the peer, which is made when the peer is new. It must be called with the lock of the
// listener held, as it is written to as the handshake progresses, and read from by whoever reports the peers.
func (l *Listener) getPeerCertInfo(remoteAddr string) *gototls.PeerCertInfo {
	pci := l.peerCertInfos[remoteAddr]
	if pci == nil {
		pci = gototls.NewPeerCertInfo(remoteAddr)
		l.peerCertInfos[remoteAddr] = pci
	}
	return pci
}

func (l *Listener) StorePeerCertInfo(remoteAddr string, subject string, dnsNames, uris []string, issuer string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	pci := l.getPeerCertInfo(remoteAddr)
	pci.Subject = subject
	pci.DNSNames = dnsNames
	pci.URIs = uris
	pci.Issuer = issuer
}

func (l *Listener) StoreSNI(remoteAddr string, sni, alpn string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	pci := l.getPeerCertInfo(remoteAddr)
	pci.SNI = sni
	pci.NegotiatedALPN = alpn
}

func (l *Listener) StoreALPN(remoteAddr string, alpn []string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	pci := l.getPeerCertInfo(remoteAddr)
	pci.ALPN = alpn
}

func (l *Listener) UpdatePeerStatus(remoteAddr string, finished bool, status string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	pci := l.getPeerCertInfo(remoteAddr)
	pci.Finished = finished
	pci.Status = append(pci.Status, status)
	pci.EndAt = time.Now()
}

// GetPeerCert gives the summaries of the cert of the server and of the cert of the peer, and nothing for the one that
// is not known: a port without a listener, a listener that has no cert info of its own (its cert was uploaded, and not
// generated), or a peer that didn't present a cert.
func GetPeerCert(port int, remoteAddr string) (string, string) {
	l := GetListenerForPort(port)
	if l == nil {
		return "", ""
	}
	var serverCert, clientCert string
	// A summary marks the info as reported, so it is not only a read.
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.ServerCert != nil {
		serverCert = l.ServerCert.Summary()
	}
	if pci := l.peerCertInfos[remoteAddr]; pci != nil {
		clientCert = pci.Summary()
	}
	return serverCert, clientCert
}

func ClearPeerCerts(port int) {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	for p, l := range listeners {
		if port == 0 || p == port {
			l.lock.Lock()
			l.peerCertInfos = map[string]*gototls.PeerCertInfo{}
			l.lock.Unlock()
		}
	}
}

// GetPeerCerts gives a copy of the peers of the listeners, since the ones of a listener keep changing, and the caller
// reports them after the lock is released.
func GetPeerCerts(port int) map[int]map[string]*gototls.PeerCertInfo {
	output := map[int]map[string]*gototls.PeerCertInfo{}
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	for p, l := range listeners {
		if port == 0 || p == port {
			l.lock.RLock()
			peers := make(map[string]*gototls.PeerCertInfo, len(l.peerCertInfos))
			for addr, pci := range l.peerCertInfos {
				// The slices are copied too: the copy is appended to when it is summarized.
				c := *pci
				c.Status, c.DNSNames, c.URIs, c.ALPN = slices.Clone(pci.Status), slices.Clone(pci.DNSNames), slices.Clone(pci.URIs), slices.Clone(pci.ALPN)
				peers[addr] = &c
			}
			l.lock.RUnlock()
			output[p] = peers
		}
	}
	return output
}

func Diff(l1, l2 *Listener) bool {
	if l1 == nil || l2 == nil {
		return l1 != l2
	}
	alpnChanged := false
	if l1.ALPN == nil && l2.ALPN != nil || l1.ALPN != nil && l2.ALPN == nil {
		alpnChanged = true
	} else if l1.ALPN != nil && l2.ALPN != nil {
		if !util.CompareStringSlices(l1.ALPN.Protos, l2.ALPN.Protos) {
			alpnChanged = true
		}
		if l1.ALPN.Handshake == nil && l2.ALPN.Handshake != nil || l1.ALPN.Handshake != nil && l2.ALPN.Handshake == nil {
			alpnChanged = true
		} else if l1.ALPN.Handshake != nil || l2.ALPN.Handshake != nil {
			if !util.CompareStringSlices(l1.ALPN.Handshake.Protos, l2.ALPN.Handshake.Protos) {
				alpnChanged = true
			}
			if len(l1.ALPN.Handshake.Seq) > 0 || len(l2.ALPN.Handshake.Seq) > 0 {
				alpnChanged = true
			}
		}
	}
	return l1.Port != l2.Port ||
		l1.ForwardPort != l2.ForwardPort ||
		l1.Protocol != l2.Protocol ||
		l1.UDS != l2.UDS ||
		alpnChanged ||
		l1.Open != l2.Open ||
		l1.AutoSNI != l2.AutoSNI ||
		l1.CommonName != l2.CommonName ||
		l1.TLS != l2.TLS ||
		l1.MTLS != l2.MTLS ||
		l1.VerifyClientCert != l2.VerifyClientCert
}
