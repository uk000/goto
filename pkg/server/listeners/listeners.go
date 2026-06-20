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
	"errors"
	"fmt"
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
	grpcStarted           = false
	httpStarted           = false
	jsonRPCStarted        = false
	httpServer            *http.Server
	h2Server              *http2.Server
	jsonRPCServer         *http.Server
	serveTCP              func(string, int, net.Listener) error
	serveXDS              func(*grpc.Server)
	DefaultLabel          string
	listenersLock         sync.RWMutex
)

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
	grpcStarted = true
	if httpStarted {
		startInitialHTTPListeners(false)
	}
}

func OnGRPCStop() {
	grpcStarted = false
	for _, l := range grpcListeners {
		l.Close()
	}
}

func OnHTTPStart(s *http.Server, h2s *http2.Server) {
	httpStarted = true
	httpServer = s
	h2Server = h2s
	if grpcStarted {
		startInitialHTTPListeners(false)
	}
}

func OnHTTPStop() {
	httpStarted = false
	httpServer = nil
}

func OnJSONRPCStart(s *http.Server, h2s *http2.Server) {
	jsonRPCStarted = true
	jsonRPCServer = s
	startInitialHTTPListeners(true)
}

func OnJSONRPCStop() {
	jsonRPCStarted = false
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
		time.Sleep(1 * time.Second)
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
		pp := portProtos[i]
		l := createPortListener(pp.Port, pp.Forward, pp.Proto, pp.MTLS, pp.Verify, existing)
		if l != nil {
			listenersLock.Lock()
			initialListeners = append(initialListeners, l)
			listenersLock.Unlock()
		}
	}
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

func (l *Listener) serveJSONRPC() {
	go func() {
		msg := fmt.Sprintf("Starting JSONRPC Listener [%s]", l.ListenerID)
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
			if l.MTLS {
				msg += " With mTLS"
			}
			msg += fmt.Sprintf(" [Verify: %t]", l.VerifyClientCert)
		}
		log.Println(msg)
		if err := jsonRPCServer.Serve(l.Listener); err != nil {
			log.Printf("JSONRPC Listener [%d]: %s", l.Port, err.Error())
		}
	}()
}

func (l *Listener) serveHTTP() {
	go func() {
		msg := fmt.Sprintf("Starting HTTP Listener [%s]", l.ListenerID)
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
			if l.MTLS {
				msg += " With mTLS"
			}
			msg += fmt.Sprintf(" [Verify: %t]", l.VerifyClientCert)
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
	msg := fmt.Sprintf("Starting TCP Listener [%s]", l.ListenerID)
	if l.TLS {
		msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
	}
	log.Println(msg)
	go func() {
		log.Printf("TCP Listener [%s] Listening\n", l.ListenerID)
		if err := serveTCP(l.ListenerID, l.Port, l.Listener); err != nil {
			log.Printf("Listener [%d]: %s", l.Port, err.Error())
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
	if cert = l.CertsCache[chi.ServerName]; cert != nil {
		return
	}
	domains := []string{chi.ServerName, l.CommonName}
	domains = append(domains, l.AltNames...)
	if cert, err = gototls.CreateCertificate(domains, l.SpiffeID, ""); err == nil {
		l.CertsCache[chi.ServerName] = cert
	}
	return
}

func (l *Listener) alpnHandler(alpn string) func(s *http.Server, conn *tls.Conn, h http.Handler) {
	return func(s *http.Server, conn *tls.Conn, h http.Handler) {
		log.Printf("Listener %s: Serving negotiated ALPN: %s ---", l.Label, alpn)
		var ctx context.Context
		if s.BaseContext != nil {
			ctx = s.BaseContext(l.Listener)
		} else {
			ctx = context.Background()
		}
		h2Server.ServeConn(conn, &http2.ServeConnOpts{
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
	if httpServer.TLSNextProto == nil {
		httpServer.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	}
	if l.ALPN != nil {
		for _, alpn := range l.ALPN.Protos {
			httpServer.TLSNextProto[alpn] = l.alpnHandler(alpn)
		}
	}
	for _, alpn := range tlsConfig.NextProtos {
		httpServer.TLSNextProto[alpn] = l.alpnHandler(alpn)
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

func (l *Listener) InitListener() bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.AutoCert && l.SpiffeID == "" {
		if l.CommonName == "" {
			l.CommonName = global.ServerConfig.CommonName
		}
		domains := []string{l.CommonName}
		domains = append(domains, l.AltNames...)
		if cert, err := gototls.CreateCertificate(domains, l.SpiffeID, fmt.Sprintf("%s-%d", l.Label, l.Port)); err == nil {
			l.SetCertificates([]*tls.Certificate{cert})
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
			if l.TLS {
				if tlsConfig := l.prepareTLS(); tlsConfig == nil {
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
		listenerGenerations[l.Port] = listenerGenerations[l.Port] + 1
		l.Generation = listenerGenerations[l.Port]
		l.ListenerID = fmt.Sprintf("%d-%d", l.Port, l.Generation)
		log.Printf("Opening [%s] listener [%s] on port [%d].", l.L8Proto, l.ListenerID, l.Port)
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

func (l *Listener) Close() {
	l.lock.Lock()
	defer l.lock.Unlock()
	log.Printf("Closing %s listener: %d\n", l.Protocol, l.Port)
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
		l.Close()
	}
	l = newListener(port, "grpc", "", false)
	if err, _ := AddOrUpdateListener(l); err != nil {
		return nil, err
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
	if !l.openListener(true) {
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
		events.SendRequestEventJSON("Listener Rejected", err.Error(),
			map[string]interface{}{"error": err.Error(), "payload": body}, r)
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
	if l.IsTCP && l.TCP == nil {
		l.TCP, msg = tcp.InitTCPConfig(l.Port, l.TCP)
	}
	if msg != "" {
		events.SendEventJSON("Listener Rejected", msg, l)
		return errors.New(msg), msg
	}

	listenersLock.RLock()
	_, exists := listeners[l.Port]
	listenersLock.RUnlock()
	if exists {
		if l.ReopenListener() {
			l.store()
			msg = fmt.Sprintf("Listener %d already present, restarted.", l.Port)
			events.SendEventJSON("Listener Updated", l.ListenerID, map[string]interface{}{"listener": l, "status": msg})
		} else {
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
	return nil, msg
}

func RemoveListener(l *Listener) {
	listenersLock.Lock()
	defer listenersLock.Unlock()
	if l.IsGRPC {
		delete(grpcListeners, l.Port)
	} else if l.IsUDP {
		delete(udpListeners, l.Port)
	}
	delete(listeners, l.Port)
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
			events.SendRequestEvent("Listener Key Added", msg, r)
		} else {
			msg = fmt.Sprintf("Cert added for listener %d\n", l.Port)
			events.SendRequestEvent("Listener Cert Added", msg, r)
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
	events.SendRequestEvent("Listener Label Updated", label, r)
	return label
}

func (l *Listener) getPeerCertInfo(remoteAddr string) *gototls.PeerCertInfo {
	l.lock.Lock()
	defer l.lock.Unlock()
	pci := l.peerCertInfos[remoteAddr]
	if pci == nil {
		pci = gototls.NewPeerCertInfo(remoteAddr)
		l.peerCertInfos[remoteAddr] = pci
	}
	return pci
}

func (l *Listener) StorePeerCertInfo(remoteAddr string, subject string, dnsNames, uris []string, issuer string) {
	pci := l.getPeerCertInfo(remoteAddr)
	pci.Subject = subject
	pci.DNSNames = dnsNames
	pci.URIs = uris
	pci.Issuer = issuer
}

func (l *Listener) StoreSNI(remoteAddr string, sni, alpn string) {
	pci := l.getPeerCertInfo(remoteAddr)
	pci.SNI = sni
	pci.NegotiatedALPN = alpn
}

func (l *Listener) StoreALPN(remoteAddr string, alpn []string) {
	pci := l.getPeerCertInfo(remoteAddr)
	pci.ALPN = alpn
}

func (l *Listener) UpdatePeerStatus(remoteAddr string, finished bool, status string) {
	pci := l.getPeerCertInfo(remoteAddr)
	pci.Finished = finished
	pci.Status = append(pci.Status, status)
	pci.EndAt = time.Now()
}

func GetPeerCert(port int, remoteAddr string) (string, string) {
	listenersLock.RLock()
	l := listeners[port]
	listenersLock.RUnlock()
	var clientCert string
	l.lock.RLock()
	serverCert := l.ServerCert.Summary()
	if l.peerCertInfos[remoteAddr] != nil {
		clientCert = l.peerCertInfos[remoteAddr].Summary()
	}
	l.lock.RUnlock()
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

func GetPeerCerts(port int) map[int]map[string]*gototls.PeerCertInfo {
	output := map[int]map[string]*gototls.PeerCertInfo{}
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	for p, l := range listeners {
		if port == 0 || p == port {
			output[p] = map[string]*gototls.PeerCertInfo{}
			l.lock.RLock()
			output[p] = l.peerCertInfos
			l.lock.RUnlock()
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
		alpnChanged ||
		l1.Open != l2.Open ||
		l1.AutoSNI != l2.AutoSNI ||
		l1.CommonName != l2.CommonName ||
		l1.TLS != l2.TLS ||
		l1.MTLS != l2.MTLS ||
		l1.VerifyClientCert != l2.VerifyClientCert
}
