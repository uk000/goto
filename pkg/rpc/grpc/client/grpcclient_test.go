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

package grpcclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/util"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The client tests talk to a real gRPC server on 127.0.0.1:0. A TCP listener is used over bufconn, because the client
// dials through its own context dialer, and the generic server code expects TCP addresses.

var stubOnce sync.Once

func stubGlobals(t *testing.T) {
	t.Helper()
	stubOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		global.Funcs.IsListenerTLS = func(int) bool { return false }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	})
	// The service registry builds the handlers of the services that it learns from reflection with the hooks that the
	// server package sets. The client package doesn't import it, so the hooks are set here.
	oldUnary, oldStream := gotogrpc.GRPCUnaryHandler, gotogrpc.GRPCStreamHandler
	gotogrpc.GRPCUnaryHandler = func(*gotogrpc.GRPCServiceMethod) grpc.MethodHandler { return nil }
	gotogrpc.GRPCStreamHandler = func(any, grpc.ServerStream) error { return nil }
	gotogrpc.ServiceRegistry.Init()
	t.Cleanup(func() {
		gotogrpc.GRPCUnaryHandler, gotogrpc.GRPCStreamHandler = oldUnary, oldStream
		gotogrpc.ServiceRegistry.Init()
	})
}

// eventually polls until the condition holds, and fails the test when it does not within the time.
func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s: %s", within, what)
	}
}

// trackingListener counts the connections that the server accepted, and the ones that were closed since.
type trackingListener struct {
	net.Listener
	accepted atomic.Int32
	closed   atomic.Int32
}

type trackedConn struct {
	net.Conn
	l    *trackingListener
	once sync.Once
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Add(1)
	return &trackedConn{Conn: c, l: l}, nil
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.l.closed.Add(1) })
	return c.Conn.Close()
}

func (l *trackingListener) open() int { return int(l.accepted.Load() - l.closed.Load()) }

type callRecord struct {
	method  string
	content string
	md      metadata.MD
}

// sampleServer implements the sample service of the pb package. The content of a request drives what is done:
// "fail" fails the call, "block" holds a unary call until the server is stopped.
type sampleServer struct {
	pb.UnimplementedSampleServiceServer
	lock    sync.Mutex
	calls   []callRecord
	release chan struct{}
}

func (s *sampleServer) record(ctx context.Context, method, content string) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.lock.Lock()
	defer s.lock.Unlock()
	s.calls = append(s.calls, callRecord{method: method, content: content, md: md})
}

func (s *sampleServer) recorded(method string) []callRecord {
	s.lock.Lock()
	defer s.lock.Unlock()
	var out []callRecord
	for _, c := range s.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (s *sampleServer) OneOne(ctx context.Context, req *pb.SampleRequest) (*pb.SampleResponse, error) {
	s.record(ctx, "oneOne", req.Content)
	grpc.SetHeader(ctx, metadata.Pairs("x-reply-header", "h-"+req.Content))
	grpc.SetTrailer(ctx, metadata.Pairs("x-reply-trailer", "t-"+req.Content))
	switch req.Content {
	case "via":
		grpc.SetHeader(ctx, metadata.Pairs("via-goto", "remote-goto"))
	case "fail":
		return nil, status.Error(codes.NotFound, "no such thing")
	case "block":
		select {
		case <-s.release:
		case <-ctx.Done():
		}
		return nil, status.Error(codes.Aborted, "released")
	}
	return &pb.SampleResponse{Id: req.Id, Content: "re:" + req.Content}, nil
}

func (s *sampleServer) OneMany(req *pb.SampleRequest, stream pb.SampleService_OneManyServer) error {
	s.record(stream.Context(), "oneMany", req.Content)
	stream.SetHeader(metadata.Pairs("x-reply-header", "h-"+req.Content))
	stream.SetTrailer(metadata.Pairs("x-reply-trailer", "t-"+req.Content))
	if req.Content == "via" {
		stream.SetHeader(metadata.Pairs("via-goto", "remote-goto"))
	}
	for i := 1; i <= int(req.Id); i++ {
		if req.Content == "slow" {
			time.Sleep(150 * time.Millisecond)
		}
		if req.Content == "fail" && i == 2 {
			return status.Error(codes.Aborted, "stream failed")
		}
		if err := stream.Send(&pb.SampleResponse{Id: int32(i), Content: fmt.Sprintf("%s-%d", req.Content, i)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *sampleServer) ManyOne(stream pb.SampleService_ManyOneServer) error {
	s.record(stream.Context(), "manyOne", "")
	stream.SetHeader(metadata.Pairs("x-reply-header", "h-many"))
	stream.SetTrailer(metadata.Pairs("x-reply-trailer", "t-many"))
	var contents []string
	for {
		req, err := stream.Recv()
		if err != nil {
			break
		}
		contents = append(contents, req.Content)
	}
	return stream.SendAndClose(&pb.SampleResponse{Id: int32(len(contents)), Content: strings.Join(contents, ",")})
}

func (s *sampleServer) ManyMany(stream pb.SampleService_ManyManyServer) error {
	s.record(stream.Context(), "manyMany", "")
	stream.SetHeader(metadata.Pairs("x-reply-header", "h-bidi"))
	stream.SetTrailer(metadata.Pairs("x-reply-trailer", "t-bidi"))
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil
		}
		if req.Content == "fail" {
			return status.Error(codes.Aborted, "stream failed")
		}
		if err := stream.Send(&pb.SampleResponse{Id: req.Id, Content: "echo:" + req.Content}); err != nil {
			return err
		}
	}
}

type testEnv struct {
	addr   string
	port   int
	srv    *grpc.Server
	lis    *trackingListener
	sample *sampleServer
}

// newEnv starts a gRPC server with the sample service and reflection. The server is stopped with the test.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	stubGlobals(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	e := &testEnv{
		addr:   ln.Addr().String(),
		port:   ln.Addr().(*net.TCPAddr).Port,
		lis:    &trackingListener{Listener: ln},
		srv:    grpc.NewServer(),
		sample: &sampleServer{release: make(chan struct{})},
	}
	pb.RegisterSampleServiceServer(e.srv, e.sample)
	reflection.Register(e.srv)
	go e.srv.Serve(e.lis)
	t.Cleanup(func() {
		close(e.sample.release)
		e.srv.Stop()
	})
	return e
}

// newClient creates a client for the server of the env, with the sample service loaded through reflection.
func newClient(t *testing.T, e *testEnv, options *GRPCOptions) *GRPCClient {
	t.Helper()
	if options == nil {
		options = &GRPCOptions{}
	}
	c, err := NewGRPCClient("test", e.port, nil, e.addr, "", "", options)
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(c.Close)
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.LoadServiceMethodFromReflection("SampleService", ""); err != nil {
		t.Fatalf("LoadServiceMethodFromReflection: %v", err)
	}
	return c
}

// deadlineCtx is the context for the RPCs that the tests make directly.
func deadlineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// within runs f in a goroutine and fails the test if it does not return in time, or if it panics, so that a bug
// cannot hang or crash the run.
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		f()
	}()
	select {
	case r := <-done:
		if r != nil {
			t.Fatalf("%s panicked: %v", what, r)
		}
	case <-time.After(d):
		t.Fatalf("%s did not return within %s", what, d)
	}
}

func invoke(t *testing.T, c *GRPCClient, call *GRPCCall, cb func(proto.Message, metadata.MD)) *GRPCResult {
	t.Helper()
	var result *GRPCResult
	within(t, 15*time.Second, "Invoke", func() { result = c.Invoke(call, cb) })
	return result
}

func protoInt32(v int32) protoreflect.Value { return protoreflect.ValueOfInt32(v) }

func protoString(s string) protoreflect.Value { return protoreflect.ValueOfString(s) }

func samplePayloadDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	return (&pb.SampleResponse{}).ProtoReflect().Descriptor()
}

func join(values []string) string { return strings.Join(values, ",") }

var gotogrpcMethodForTest = gotogrpc.GRPCServiceMethod{Name: "m"}

func samplePayloads(contents ...string) *GRPCPayloads {
	p := &GRPCPayloads{}
	for i, c := range contents {
		p.Linear = append(p.Linear, &GRPCPayload{Payload: fmt.Sprintf(`{"id":%d,"content":%q}`, i+1, c)})
	}
	return p
}

func TestNewGRPCClientDefaults(t *testing.T) {
	c, err := NewGRPCClient("lbl", 8081, nil, "127.0.0.1:9999", "auth.example", "", &GRPCOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Label != "lbl" || c.ClientPort != 8081 || c.URL != "127.0.0.1:9999" {
		t.Errorf("unexpected identity: %q %d %q", c.Label, c.ClientPort, c.URL)
	}
	if c.Authority != "auth.example" {
		t.Errorf("Authority = %q, want auth.example", c.Authority)
	}
	if c.TLSServerName != "auth.example" {
		t.Errorf("TLSServerName = %q, want the authority when no server name is given", c.TLSServerName)
	}
	if c.Dialer.Timeout != 5*time.Second {
		t.Errorf("dialer timeout = %s, want 5s", c.Dialer.Timeout)
	}
	if c.tlsConfig != nil || c.tlsCredentials != nil || c.Options.IsTLS {
		t.Errorf("plain client must not have TLS configured")
	}
	if c.Options.IdleTimeout != 10*time.Minute {
		t.Errorf("IdleTimeout defaults to %s, want 10m", c.Options.IdleTimeout)
	}
	if len(c.Options.DialOptions) == 0 {
		t.Errorf("dial options not configured")
	}
	if !c.IsGRPC() || c.IsHTTP() || c.IsH2() || c.HTTP() != nil || c.GRPC() != nil {
		t.Errorf("transport identity flags are wrong for a gRPC client that is not connected")
	}
	if c.Transport() != c.Transport() || c.GetDialer() != &c.Dialer {
		t.Errorf("Transport/GetDialer must expose the client itself")
	}
	if c.GetOpenConnectionCount() != 0 {
		t.Errorf("open connections = %d, want 0", c.GetOpenConnectionCount())
	}
	// an explicit server name wins over the authority
	c2, _ := NewGRPCClient("l", 1, nil, "u", "auth", "sni.example", &GRPCOptions{})
	if c2.TLSServerName != "sni.example" || c2.Authority != "auth" {
		t.Errorf("server name %q authority %q", c2.TLSServerName, c2.Authority)
	}
	// the options given are kept as given: the idle timeout of the caller isn't overridden
	c3, _ := NewGRPCClient("l", 1, nil, "u", "", "", &GRPCOptions{IdleTimeout: time.Minute, KeepOpen: time.Second})
	if c3.Options.IdleTimeout != time.Minute || c3.Options.KeepOpen != time.Second {
		t.Errorf("options not kept: %+v", c3.Options)
	}
}

func TestNewGRPCClientTLS(t *testing.T) {
	c, err := NewGRPCClient("tlsc", 1, nil, "127.0.0.1:1", "", "sni.example", &GRPCOptions{IsTLS: true, VerifyTLS: false, TLSVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	if c.tlsConfig == nil || c.tlsCredentials == nil {
		t.Fatalf("TLS client must have a TLS config and credentials")
	}
	if !c.tlsConfig.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify must be set when VerifyTLS is off")
	}
	if c.tlsConfig.ServerName != "sni.example" {
		t.Errorf("ServerName = %q", c.tlsConfig.ServerName)
	}
	if c.tlsConfig.MinVersion != tls.VersionTLS12 || c.tlsConfig.MaxVersion != tls.VersionTLS12 {
		t.Errorf("TLS versions = %x/%x, want 1.2 for both", c.tlsConfig.MinVersion, c.tlsConfig.MaxVersion)
	}
	if c.Authority != "sni.example" {
		t.Errorf("authority = %q, want it to default to the TLS server name", c.Authority)
	}

	v, _ := NewGRPCClient("v", 1, nil, "127.0.0.1:1", "", "sni.example", &GRPCOptions{IsTLS: true, VerifyTLS: true, TLSVersion: tls.VersionTLS13})
	if v.tlsConfig.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify must be off when VerifyTLS is on")
	}
}

func TestSetTLSConfigEnablesTLS(t *testing.T) {
	c, _ := NewGRPCClient("c", 1, nil, "u", "", "name", &GRPCOptions{})
	if c.Options.IsTLS {
		t.Fatalf("precondition: plain client")
	}
	cfg := &tls.Config{ServerName: "name"}
	c.SetTLSConfig(cfg)
	if !c.Options.IsTLS || c.tlsConfig != cfg || c.tlsCredentials == nil {
		t.Errorf("SetTLSConfig must switch the client to TLS with the config given")
	}
	if c.Authority != "name" {
		t.Errorf("authority = %q, want the TLS server name", c.Authority)
	}
}

func TestUpdateTLSConfig(t *testing.T) {
	c, _ := NewGRPCClient("c", 1, nil, "u", "", "old", &GRPCOptions{IsTLS: true, TLSVersion: tls.VersionTLS13})
	c.UpdateTLSConfig("new.example", tls.VersionTLS12, false, nil)
	if c.TLSServerName != "new.example" || c.Authority != "new.example" {
		t.Errorf("name/authority = %q/%q, want new.example", c.TLSServerName, c.Authority)
	}
	if c.tlsConfig.ServerName != "new.example" || c.tlsConfig.MinVersion != tls.VersionTLS12 || c.tlsConfig.MaxVersion != tls.VersionTLS12 {
		t.Errorf("TLS config not updated: %q %x %x", c.tlsConfig.ServerName, c.tlsConfig.MinVersion, c.tlsConfig.MaxVersion)
	}
	if !c.tlsConfig.InsecureSkipVerify {
		t.Errorf("verify=false must skip verification")
	}
	// UpdateTLSCerts is a no-op that must not panic
	c.UpdateTLSCerts(nil, nil)

	// a plain client keeps being plain, and forgets TLS settings
	p, _ := NewGRPCClient("p", 1, nil, "u", "", "", &GRPCOptions{})
	p.UpdateTLSConfig("n", tls.VersionTLS12, true, nil)
	if p.Options.IsTLS || p.tlsConfig != nil || p.tlsCredentials != nil {
		t.Errorf("a plain client must stay plain")
	}
	if p.TLSServerName != "n" || p.Authority != "n" {
		t.Errorf("name/authority = %q/%q", p.TLSServerName, p.Authority)
	}
}

func TestPeerCertInfo(t *testing.T) {
	c, _ := NewGRPCClient("c", 1, nil, "u", "", "", &GRPCOptions{})
	if c.GetPeerCertInfo() != nil {
		t.Fatalf("no peer info before any handshake")
	}
	c.StoreSNI("1.2.3.4:5", "sni.example", "h2")
	info := c.GetPeerCertInfo()
	if info == nil || info.RemoteAddr != "1.2.3.4:5" || info.SNI != "sni.example" || info.NegotiatedALPN != "h2" {
		t.Fatalf("SNI info not stored: %+v", info)
	}
	c.StorePeerCertInfo("1.2.3.4:5", "cn", []string{"a.example"}, []string{"spiffe://x"}, "issuer")
	if info.Subject != "cn" || len(info.DNSNames) != 1 || info.DNSNames[0] != "a.example" || len(info.URIs) != 1 || info.Issuer != "issuer" {
		t.Errorf("peer cert info not stored: %+v", info)
	}
	if info.SNI != "sni.example" {
		t.Errorf("storing cert info must keep the SNI info")
	}
	c.StoreALPN("1.2.3.4:5", []string{"h2", "http/1.1"})
	if len(info.ALPN) != 2 {
		t.Errorf("ALPN = %v", info.ALPN)
	}
	c.UpdatePeerStatus("1.2.3.4:5", true, "one")
	c.UpdatePeerStatus("1.2.3.4:5", true, "two")
	if !info.Finished || len(info.Status) != 2 || info.Status[1] != "two" || info.EndAt.IsZero() {
		t.Errorf("status not tracked: %+v", info)
	}
	// each Store* creates the info when it is the first one called
	for i, f := range []func(*GRPCClient){
		func(c *GRPCClient) { c.StoreALPN("r", []string{"h2"}) },
		func(c *GRPCClient) { c.StorePeerCertInfo("r", "cn", nil, nil, "i") },
		func(c *GRPCClient) { c.UpdatePeerStatus("r", false, "s") },
	} {
		c2, _ := NewGRPCClient("c", 1, nil, "u", "", "", &GRPCOptions{})
		f(c2)
		if c2.GetPeerCertInfo() == nil || c2.GetPeerCertInfo().RemoteAddr != "r" {
			t.Errorf("case %d: peer info not created", i)
		}
	}
}

func TestConnectAndClose(t *testing.T) {
	e := newEnv(t)
	c, _ := NewGRPCClient("c", e.port, nil, e.addr, "", "", &GRPCOptions{})
	c.Close() // closing a client that never connected is a no-op
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	conn := c.GRPC()
	if conn == nil || c.stub == nil {
		t.Fatalf("Connect must create the connection and the stub")
	}
	if err := c.Connect(); err != nil || c.GRPC() != conn {
		t.Errorf("Connect on a live connection must keep it: err=%v same=%v", err, c.GRPC() == conn)
	}
	// use the connection, so that the server sees it
	ctx := deadlineCtx(t)
	resp, err := pb.NewSampleServiceClient(conn).OneOne(ctx, &pb.SampleRequest{Id: 3, Content: "x"})
	if err != nil || resp.Content != "re:x" {
		t.Fatalf("OneOne via the client connection: %v %v", resp, err)
	}
	c.Close()
	if st := conn.GetState(); st != connectivity.Shutdown {
		t.Errorf("state after Close = %s, want SHUTDOWN", st)
	}
	// a closed connection is replaced when connecting again
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if c.GRPC() == conn || c.GRPC() == nil {
		t.Errorf("Connect must replace a shut down connection")
	}
	resp, err = pb.NewSampleServiceClient(c.GRPC()).OneOne(ctx, &pb.SampleRequest{Id: 4, Content: "y"})
	if err != nil || resp.Content != "re:y" {
		t.Fatalf("OneOne after reconnect: %v %v", resp, err)
	}
	if c.GetOpenConnectionCount() < 1 {
		t.Errorf("connection tracking must count the connections dialed, got %d", c.GetOpenConnectionCount())
	}
	c.Close()
	eventually(t, 5*time.Second, "server connections closed", func() bool { return e.lis.open() == 0 })
}

func TestConnectWithHeadersOrMD(t *testing.T) {
	c, _ := NewGRPCClient("c", 1, nil, "127.0.0.1:1", "", "", &GRPCOptions{})
	t.Cleanup(c.Close)

	ctx, md, err := c.ConnectWithHeadersOrMD(nil, nil)
	if err != nil || ctx == nil {
		t.Fatalf("connect without headers: %v", err)
	}
	if md != nil {
		t.Errorf("no headers and no metadata gives no metadata, got %v", md)
	}
	if c.Options.RequestTimeout != 5*time.Minute {
		t.Errorf("request timeout defaults to %s, want 5m", c.Options.RequestTimeout)
	}

	ctx, md, _ = c.ConnectWithHeadersOrMD(map[string]string{"X-One": "1", "x-two": "2"}, nil)
	if got := md.Get("x-one"); len(got) != 1 || got[0] != "1" {
		t.Errorf("headers are converted to lower-case metadata: %v", md)
	}
	if out, ok := metadata.FromOutgoingContext(ctx); !ok || out.Get("x-two")[0] != "2" {
		t.Errorf("headers must be in the outgoing context: %v", out)
	}

	given := metadata.Pairs("x-given", "g")
	ctx, md, _ = c.ConnectWithHeadersOrMD(map[string]string{"x-ignored": "i"}, given)
	if len(md.Get("x-ignored")) != 0 || md.Get("x-given")[0] != "g" {
		t.Errorf("metadata given takes precedence over the headers: %v", md)
	}
	if out, _ := metadata.FromOutgoingContext(ctx); out.Get("x-given")[0] != "g" {
		t.Errorf("metadata given must be in the outgoing context: %v", out)
	}
	if c.GRPC() == nil {
		t.Errorf("connecting must create the connection")
	}
}

func TestLoadServiceMethodFromReflection(t *testing.T) {
	e := newEnv(t)
	c, _ := NewGRPCClient("c", e.port, nil, e.addr, "", "", &GRPCOptions{})
	t.Cleanup(c.Close)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadServiceMethodFromReflection("SampleService", "oneOne"); err != nil {
		t.Fatalf("load service: %v", err)
	}
	if c.Service == nil || c.Service.Name != "SampleService" || len(c.Service.Methods) != 4 {
		t.Fatalf("service not loaded: %+v", c.Service)
	}
	for name, want := range map[string][3]bool{
		"oneOne":   {true, false, false},
		"oneMany":  {false, false, true},
		"manyOne":  {false, true, false},
		"manyMany": {false, true, true},
	} {
		m := c.Service.Methods[name]
		if m == nil {
			t.Errorf("method %s missing", name)
			continue
		}
		if got := [3]bool{m.IsUnary, m.IsClientStream, m.IsServerStream}; got != want {
			t.Errorf("method %s: unary/client/server = %v, want %v", name, got, want)
		}
	}
	if err := c.LoadServiceMethodFromReflection("NoSuchService", ""); err == nil {
		t.Errorf("unknown service must fail")
	}
	if err := c.LoadServiceMethodFromReflection("SampleService", "noSuchMethod"); err == nil {
		t.Errorf("unknown method must fail")
	}
}

func TestLoadRemoteReflectedServices(t *testing.T) {
	e := newEnv(t)
	if err := LoadRemoteReflectedServices(e.addr); err != nil {
		t.Fatalf("LoadRemoteReflectedServices: %v", err)
	}
	if s := gotogrpc.ServiceRegistry.GetService("SampleService"); s == nil || len(s.Methods) != 4 {
		t.Errorf("SampleService must be in the registry after loading by reflection: %+v", s)
	}
	// a upstream that is not there is an error, not a panic or a hang
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	within(t, 15*time.Second, "reflect from dead upstream", func() {
		if err := LoadRemoteReflectedServices(dead); err == nil {
			t.Errorf("reflecting from a dead upstream must fail")
		}
	})
}

func TestDialerCountsAndLimitsFailures(t *testing.T) {
	e := newEnv(t)
	svc := &gotogrpc.GRPCService{Name: "dialsvc"}
	c, _ := NewGRPCClient("c", e.port, svc, e.addr, "", "", &GRPCOptions{})
	t.Cleanup(c.Close)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	ctx := deadlineCtx(t)
	if _, err := pb.NewSampleServiceClient(c.GRPC()).OneOne(ctx, &pb.SampleRequest{Content: "x"}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if c.GetOpenConnectionCount() != 1 {
		t.Errorf("open connections = %d, want 1", c.GetOpenConnectionCount())
	}

	// a client of a dead address gives up dialing after a couple of failures
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	d, _ := NewGRPCClient("d", 1, nil, dead, "", "", &GRPCOptions{})
	t.Cleanup(d.Close)
	if err := d.Connect(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := pb.NewSampleServiceClient(d.GRPC()).OneOne(cctx, &pb.SampleRequest{Content: "x"})
		cancel()
		if status.Code(err) != codes.Unavailable {
			t.Errorf("attempt %d against a dead address: code = %v (%v), want Unavailable", i, status.Code(err), err)
		}
	}
	if d.connErrorCount.Load() < 1 {
		t.Errorf("failed dials must be counted, got %d", d.connErrorCount.Load())
	}
	// connecting again gives the client a new start
	if err := d.Connect(); err != nil {
		t.Fatal(err)
	}
	if d.connErrorCount.Load() != 0 {
		t.Errorf("Connect must reset the failed dials, got %d", d.connErrorCount.Load())
	}
}

func TestPermanentDialError(t *testing.T) {
	err := permanentDialError{error: errors.New("max connection attempt reached")}
	if err.Temporary() || err.Timeout() {
		t.Errorf("a permanent dial error is neither temporary nor a timeout")
	}
	if err.Error() != "max connection attempt reached" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestUpdateTLSCertsKeepsTheClient(t *testing.T) {
	c := &GRPCClient{Label: "svc", Options: GRPCOptions{IsTLS: true, VerifyTLS: true}}
	c.UpdateTLSCerts(nil, nil)
	if c.Label != "svc" || !c.Options.IsTLS || !c.Options.VerifyTLS {
		t.Errorf("UpdateTLSCerts changed the client: %+v", c)
	}
}
