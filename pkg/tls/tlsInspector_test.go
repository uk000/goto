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

package tls

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// serverConfig returns a TLS config for a server that has a cert for the name, and the pool that trusts it.
func serverConfig(t *testing.T, name string) (*tls.Config, *x509.CertPool) {
	t.Helper()
	c, err := CreateCertificate([]string{name}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{*c}}, poolOf(t, mustEncodeCert(t, c))
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

type accepted struct {
	conn net.Conn
	err  error
}

// acceptN accepts n connections from the listener one after the other and delivers the outcome of each.
func acceptN(l net.Listener, n int) <-chan accepted {
	out := make(chan accepted, n)
	go func() {
		for i := 0; i < n; i++ {
			c, err := l.Accept()
			out <- accepted{c, err}
			if err != nil {
				return
			}
		}
	}()
	return out
}

func nextAccepted(t *testing.T, ch <-chan accepted) accepted {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(ioTimeout):
		t.Fatal("no connection was accepted")
		return accepted{}
	}
}

func dial(t *testing.T, l net.Listener) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", l.Addr().String(), ioTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(ioTimeout))
	return c
}

func TestTLSInspectorServesPlainConnections(t *testing.T) {
	isolate(t)
	cfg, _ := serverConfig(t, "srv.test")
	l := NewTLSInspector(1, "plain", listen(t), cfg)
	ch := acceptN(l, 1)
	c := dial(t, l.(*TLSInspector).Listener)
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	a := nextAccepted(t, ch)
	if a.err != nil {
		t.Fatal(a.err)
	}
	defer a.conn.Close()
	pc, ok := a.conn.(*PeekedConn)
	if !ok {
		t.Fatalf("a plain connection should be a *PeekedConn, got %T", a.conn)
	}
	_ = pc.SetReadDeadline(time.Now().Add(ioTimeout))
	b := make([]byte, 16)
	if _, err := io.ReadFull(pc, b); err != nil || string(b) != "GET / HTTP/1.1\r\n" {
		t.Errorf("the byte that was peeked must still be read: %q, %v", b, err)
	}
}

func TestTLSInspectorServesTLSConnections(t *testing.T) {
	isolate(t)
	cfg, roots := serverConfig(t, "srv.test")
	l := NewTLSInspector(1, "tls", listen(t), cfg)
	ch := acceptN(l, 1)
	errs := &errList{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: ioTimeout}, "tcp", l.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "srv.test"})
		if err != nil {
			errs.addf("TLS dial: %v", err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(ioTimeout))
		if _, err := conn.Write([]byte("ping")); err != nil {
			errs.addf("TLS write: %v", err)
		}
	}()
	a := nextAccepted(t, ch)
	if a.err != nil {
		t.Fatal(a.err)
	}
	defer a.conn.Close()
	tc, ok := a.conn.(*tls.Conn)
	if !ok {
		t.Fatalf("a TLS connection should be a *tls.Conn, got %T", a.conn)
	}
	_ = tc.SetDeadline(time.Now().Add(ioTimeout))
	b := make([]byte, 4)
	if _, err := io.ReadFull(tc, b); err != nil || string(b) != "ping" {
		t.Errorf("read %q, %v", b, err)
	}
	wg.Wait()
	errs.report(t)
}

func TestTLSInspectorAcceptFailsWhenListenerCloses(t *testing.T) {
	isolate(t)
	cfg, _ := serverConfig(t, "srv.test")
	inner := listen(t)
	l := NewTLSInspector(1, "closing", inner, cfg)
	ch := acceptN(l, 1)
	time.Sleep(20 * time.Millisecond)
	inner.Close()
	if a := nextAccepted(t, ch); a.err == nil {
		t.Error("Accept must fail when the listener is closed")
	}
}

func TestPeekedConn(t *testing.T) {
	c, peer := pipe(t)
	pc := newPeekedConn(c)
	go func() { peer.Write([]byte("abc")) }()
	b, err := pc.Peek(2)
	if err != nil || string(b) != "ab" {
		t.Fatalf("Peek = %q, %v", b, err)
	}
	all := make([]byte, 3)
	if _, err := io.ReadFull(pc, all); err != nil || string(all) != "abc" {
		t.Errorf("Read after Peek = %q, %v", all, err)
	}
}

type statusCall struct {
	success bool
	msg     string
}

type recorder struct {
	mu       sync.Mutex
	alpns    map[string][]string
	snis     [][2]string
	subjects []string
	dns      [][]string
	uris     [][]string
	issuers  []string
	statuses []statusCall
}

func (r *recorder) storeALPN(remote string, protos []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.alpns == nil {
		r.alpns = map[string][]string{}
	}
	r.alpns[remote] = protos
}

func (r *recorder) storeSNI(remote, sni, alpn string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snis = append(r.snis, [2]string{sni, alpn})
}

func (r *recorder) storeCertInfo(remote, subject string, dns, uris []string, issuer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subjects = append(r.subjects, subject)
	r.dns = append(r.dns, dns)
	r.uris = append(r.uris, uris)
	r.issuers = append(r.issuers, issuer)
}

func (r *recorder) updateStatus(remote string, success bool, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, statusCall{success, msg})
}

func (r *recorder) hello(cfg *tls.Config) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return HandleClientHello("8443", "mtls", cfg, r.storeALPN, r.storeCertInfo, r.storeSNI, r.updateStatus)
}

// handshake runs a TLS handshake between a client and a server that uses the config for the client hello.
func handshake(t *testing.T, serverCfg *tls.Config, clientCfg *tls.Config) (serverErr, clientErr error) {
	t.Helper()
	l := listen(t)
	done := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(ioTimeout))
		tc := tls.Server(c, serverCfg)
		err = tc.Handshake()
		if err == nil {
			_, _ = tc.Read(make([]byte, 1))
		}
		done <- err
	}()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: ioTimeout}, "tcp", l.Addr().String(), clientCfg)
	if err == nil {
		_ = conn.SetDeadline(time.Now().Add(ioTimeout))
		_, err = conn.Write([]byte("x"))
		conn.Close()
	}
	select {
	case serverErr = <-done:
	case <-time.After(ioTimeout):
		t.Fatal("the server side of the handshake didn't finish")
	}
	return serverErr, err
}

func TestHandleClientHelloRecordsPeer(t *testing.T) {
	isolate(t)
	srvCert, err := CreateCertificate([]string{"srv.test"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := CreateCertificate([]string{"client.test"}, "spiffe://cluster.local/ns/goto/sa/client", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{*srvCert},
		ClientAuth:   tls.RequireAnyClientCert,
		NextProtos:   []string{"h2"},
	}
	rec := &recorder{}
	serverCfg := &tls.Config{GetConfigForClient: rec.hello(cfg)}
	clientCfg := &tls.Config{
		RootCAs:      poolOf(t, mustEncodeCert(t, srvCert)),
		ServerName:   "srv.test",
		NextProtos:   []string{"h2", "other"},
		Certificates: []tls.Certificate{*clientCert},
	}
	if serverErr, clientErr := handshake(t, serverCfg, clientCfg); serverErr != nil || clientErr != nil {
		t.Fatalf("handshake failed: server %v, client %v", serverErr, clientErr)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.alpns) != 1 {
		t.Fatalf("ALPNs by peer = %v", rec.alpns)
	}
	for _, protos := range rec.alpns {
		if strings.Join(protos, ",") != "h2,other" {
			t.Errorf("stored client ALPNs = %v", protos)
		}
	}
	if len(rec.snis) < 2 || rec.snis[0] != [2]string{"srv.test", ""} || rec.snis[len(rec.snis)-1] != [2]string{"srv.test", "h2"} {
		t.Errorf("SNI was stored with the client hello and again with the negotiated protocol: %v", rec.snis)
	}
	if len(rec.subjects) != 1 || !strings.Contains(rec.subjects[0], "cn=[client.test]") {
		t.Errorf("client cert subject = %v", rec.subjects)
	}
	if len(rec.uris) != 1 || len(rec.uris[0]) != 1 || rec.uris[0][0] != "spiffe://cluster.local/ns/goto/sa/client" {
		t.Errorf("client cert URIs = %v", rec.uris)
	}
	if len(rec.dns) != 1 || len(rec.dns[0]) != 1 || rec.dns[0][0] != "client.test" {
		t.Errorf("client cert DNS names = %v", rec.dns)
	}
	if len(rec.issuers) != 1 || !strings.HasPrefix(rec.issuers[0], "org=[client.test]") {
		t.Errorf("client cert issuer = %v", rec.issuers)
	}
	if len(rec.statuses) < 2 {
		t.Errorf("peer status should be updated with the hello and the cert: %v", rec.statuses)
	}
}

func TestHandleClientHelloWithoutClientCert(t *testing.T) {
	isolate(t)
	srvCert, _ := CreateCertificate([]string{"srv.test"}, "", "")
	cfg := &tls.Config{Certificates: []tls.Certificate{*srvCert}, ClientAuth: tls.RequestClientCert}
	rec := &recorder{}
	serverCfg := &tls.Config{GetConfigForClient: rec.hello(cfg)}
	clientCfg := &tls.Config{RootCAs: poolOf(t, mustEncodeCert(t, srvCert)), ServerName: "srv.test"}
	serverErr, _ := handshake(t, serverCfg, clientCfg)
	if serverErr == nil || !strings.Contains(serverErr.Error(), "No client certificate provided") {
		t.Errorf("a client that has no cert is rejected by the peer cert check: %v", serverErr)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	failed := false
	for _, s := range rec.statuses {
		failed = failed || (s.success && strings.Contains(s.msg, "No client certificate"))
	}
	if !failed {
		t.Errorf("the failure should be reported: %v", rec.statuses)
	}
}

func TestExtractSNI(t *testing.T) {
	rec := &recorder{}
	verify := ExtractSNI("8443", "lbl", "1.2.3.4:5", rec.storeSNI, rec.storeCertInfo, rec.updateStatus)
	if err := verify(tls.ConnectionState{ServerName: "a.test", NegotiatedProtocol: "h2"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.snis) != 1 || rec.snis[0] != [2]string{"a.test", "h2"} || len(rec.subjects) != 0 {
		t.Errorf("without a peer cert only the SNI is stored: %v %v", rec.snis, rec.subjects)
	}
	if len(rec.statuses) != 1 || rec.statuses[0].success || !strings.Contains(rec.statuses[0].msg, "No client certificate") {
		t.Errorf("statuses = %v", rec.statuses)
	}

	c, _ := CreateCertificate([]string{"p.test"}, "spiffe://a/b", "")
	if err := verify(tls.ConnectionState{ServerName: "a.test", PeerCertificates: []*x509.Certificate{parseLeaf(t, c)}}); err != nil {
		t.Fatal(err)
	}
	if len(rec.subjects) != 1 || len(rec.uris[0]) != 1 || rec.uris[0][0] != "spiffe://a/b" {
		t.Errorf("peer cert wasn't stored: %v %v", rec.subjects, rec.uris)
	}
}

func TestExtractPeerCertInfo(t *testing.T) {
	rec := &recorder{}
	verify := ExtractPeerCertInfo("8443", "lbl", "1.2.3.4:5", rec.storeCertInfo, rec.updateStatus)
	err := verify(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "No client certificate provided") {
		t.Errorf("err = %v", err)
	}
	if len(rec.statuses) != 1 || !rec.statuses[0].success {
		t.Errorf("statuses = %v", rec.statuses)
	}
	if err := verify([][]byte{{1}}, nil); err != nil {
		t.Errorf("a presented cert is accepted: %v", err)
	}
}

func TestIdentifyLeaf(t *testing.T) {
	c, _ := CreateCertificate([]string{"leaf.test"}, "", "")
	certs := []*tls.Certificate{c}
	leaf := IdentifyLeaf(certs)
	if leaf == nil || leaf.Subject.CommonName != "leaf.test" {
		t.Fatalf("leaf = %v", leaf)
	}
	if c.Leaf != leaf {
		t.Error("the parsed leaf should be kept in the certificate")
	}
	if again := IdentifyLeaf(certs); again != leaf {
		t.Error("a certificate that has its leaf returns it")
	}
	if got := IdentifyLeaf([]*tls.Certificate{{}}); got != nil {
		t.Errorf("a certificate without data has no leaf, got %v", got)
	}
}

func TestSubjectAndIssuerToString(t *testing.T) {
	cert := &x509.Certificate{
		Subject: pkix.Name{Organization: []string{"o1"}, Country: []string{"US"}, CommonName: "cn1"},
		Issuer:  pkix.Name{Organization: []string{"o2"}, Country: []string{"DE"}, SerialNumber: "42"},
	}
	if got := SubjectToString(cert); got != "org=[o1],co=[US],cn=[cn1]" {
		t.Errorf("SubjectToString = %q", got)
	}
	if got := IssuerToString(cert); got != "org=[o2],co=[DE],sn=[42]" {
		t.Errorf("IssuerToString = %q", got)
	}
}

func TestPeerCertInfo(t *testing.T) {
	before := time.Now()
	pci := NewPeerCertInfo("1.2.3.4:5")
	if pci.RemoteAddr != "1.2.3.4:5" || pci.StartAt.Before(before) || pci.Finished {
		t.Errorf("new PeerCertInfo = %+v", pci)
	}
	pci.Subject = "org=[o],co=[US],cn=[x]"
	pci.DNSNames = []string{"a.test"}
	pci.URIs = []string{"spiffe://a/b"}
	pci.SNI = "sni.test"
	pci.ALPN = []string{"h2"}
	pci.Issuer = "org=[i]"
	pci.NegotiatedALPN = "h2"
	got := pci.Summary()
	want := "RemoteAddr: 1.2.3.4:5;; Subject: org=[o],co=[US],cn=[x];; DNSNames: [a.test];; URIs: [spiffe://a/b];; SNI: sni.test;; ALPN: [h2];; Issuer: org=[i];; NegotiatedALPN: h2;; Finished: true;;"
	if got != want {
		t.Errorf("Summary =\n%s\nwant\n%s", got, want)
	}
	if !pci.Finished || len(pci.Status) != 1 || pci.Status[0] != "Peer Cert Reported" {
		t.Errorf("Summary should mark the info finished: %+v", pci)
	}
	if got := (&PeerCertInfo{}).Summary(); got != "Finished: true;;" {
		t.Errorf("Summary of an empty info = %q", got)
	}
}
