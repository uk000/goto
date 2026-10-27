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
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
)

// A listener's ALPN is used by all the connections that the listener serves, and the ALPN of a client for all the
// requests that it sends, at the same time. Running a handshake used to change the ALPN (the direction of the
// handshake, the index of a step, the decoded preamble), which was a data race between the connections.
func TestRegressionALPNHandshakeDoesNotMutateSharedConfig(t *testing.T) {
	a := steps(write("6161"), write("6262"))
	errs := &errList{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		for _, server := range []bool{true, false} {
			wg.Add(2)
			c, peer := pipe(t)
			go func() {
				defer wg.Done()
				var err error
				if server {
					_, err = a.HandleServer(c, &tls.ConnectionState{NegotiatedProtocol: "istio"})
				} else {
					_, err = a.Handle(c)
				}
				if err != nil {
					errs.addf("handshake (server: %v): %v", server, err)
				}
			}()
			go func() {
				defer wg.Done()
				b := make([]byte, 4)
				if _, err := io.ReadFull(peer, b); err != nil || string(b) != "aabb" {
					errs.addf("peer (server: %v) received %q, %v", server, b, err)
				}
			}()
		}
	}
	wg.Wait()
	errs.report(t)
}

// The size that a frame announces for the next one is read from 4 bytes. A read that can't deliver 4 bytes used to
// panic.
func TestRegressionALPNSizeFrameShorterThanFourBytes(t *testing.T) {
	c, peer := pipe(t)
	done := make(chan handshakeResult, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- handshakeResult{err: fmt.Errorf("panic: %v", p)}
			}
		}()
		data, err := steps(read(2, true, false)).Handle(c)
		done <- handshakeResult{data, err}
	}()
	go func() { peer.Write([]byte{0, 1}) }()
	if r := wait(t, done); r.err == nil || strings.HasPrefix(r.err.Error(), "panic") {
		t.Errorf("err = %v, want an error without a panic", r.err)
	}
}

// Certs issued by one CA used to get the time in seconds as serial number, so the certs created within a second
// had the same one.
func TestRegressionCertsOfOneCAHaveDistinctSerials(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	serials := map[string]bool{}
	for i := 0; i < 4; i++ {
		c, err := CreateCertificateWithCA(ca.cert, ca.key, []string{"leaf.test"}, "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		serials[parseLeaf(t, c).SerialNumber.String()] = true
	}
	if len(serials) != 4 {
		t.Errorf("certs issued by one CA must have distinct serial numbers, got %v", serials)
	}
}

// The work dir was created with a mode that no one can use, so the cert and key of a new dir couldn't be saved.
func TestRegressionSavedCertsCreateWorkDir(t *testing.T) {
	isolate(t)
	dir := filepath.Join(global.ServerConfig.WorkDir, "certs", "nested")
	global.ServerConfig.WorkDir = dir
	t.Cleanup(func() {
		// Leave dirs that an old version of the code created unusable removable
		_ = os.Chmod(filepath.Dir(dir), 0700)
		_ = os.Chmod(dir, 0700)
	})
	c, err := CreateCertificate([]string{"saved.test"}, "", "my prefix")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("work dir wasn't created: %v", err)
	}
	var certFile, keyFile string
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), "-cert.pem"):
			certFile = filepath.Join(dir, e.Name())
		case strings.HasSuffix(e.Name(), "-key.pem"):
			keyFile = filepath.Join(dir, e.Name())
		}
	}
	if certFile == "" || keyFile == "" {
		t.Fatalf("expected a cert file and a key file, found %v", entries)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("saved files don't form a key pair: %v", err)
	}
	if !bytes.Equal(pair.Certificate[0], c.Certificate[0]) {
		t.Error("saved cert differs from the returned cert")
	}
	if fi, err := os.Stat(keyFile); err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("key file mode = %v, %v, want 0600", fi.Mode().Perm(), err)
	}
}

// The parsed cert of a name was kept after the raw cert or key of the name was removed or uploaded again, so the
// removed cert was still served and the new one never used.
func TestRegressionChangingRawCertOrKeyDropsParsedCert(t *testing.T) {
	isolate(t)
	first, _ := CreateCertificate([]string{"first.test"}, "", "")
	second, _ := CreateCertificate([]string{"second.test"}, "", "")
	p1, p2 := encodePair(t, first), encodePair(t, second)

	AddCert("n", p1.cert)
	AddKey("n", p1.key)
	if certs, _ := GetCerts("n"); len(certs) != 1 || !bytes.Equal(certs[0].Certificate[0], first.Certificate[0]) {
		t.Fatalf("first cert not served: %v", certs)
	}
	AddCert("n", p2.cert)
	AddKey("n", p2.key)
	if certs, _ := GetCerts("n"); len(certs) != 1 || !bytes.Equal(certs[0].Certificate[0], second.Certificate[0]) {
		t.Errorf("a cert uploaded again must replace the one used before: %v", certs)
	}
	RemoveKey("n")
	if certs, _ := GetCerts("n"); len(certs) != 0 {
		t.Errorf("a removed key must make the cert unusable: %d certs", len(certs))
	}
	AddKey("n", p2.key)
	if certs, _ := GetCerts("n"); len(certs) != 1 {
		t.Fatalf("cert with its key uploaded again: %d certs", len(certs))
	}
	RemoveCert("n")
	if certs, _ := GetCerts("n"); len(certs) != 0 {
		t.Errorf("a removed cert must not be served: %d certs", len(certs))
	}
}

// The API told a key from a cert by looking for "key" anywhere in the request URI, so a name or a domain such as
// "monkey" or "keycloak.example.com" made certs be stored as keys.
func TestRegressionNamesWithKeyAreNotTakenForKeys(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	mustOK(t, call(t, "POST", "/tls/cert/add/monkey", string(ca.cert)))
	mustOK(t, call(t, "POST", "/tls/key/add/keyed", string(ca.key)))
	mustOK(t, call(t, "POST", "/tls/ca/cert/add/keyring/keycloak.test", string(ca.cert)))
	mustOK(t, call(t, "POST", "/tls/ca/key/add/keyring/keycloak.test", string(ca.key)))
	lock.RLock()
	if !bytes.Equal(rawCerts["monkey"], ca.cert) || rawKeys["monkey"] != nil {
		t.Errorf("cert named monkey was taken for a key: certs %v keys %v", rawCerts["monkey"] != nil, rawKeys["monkey"] != nil)
	}
	if !bytes.Equal(rawKeys["keyed"], ca.key) || rawCerts["keyed"] != nil {
		t.Errorf("key named keyed was taken for a cert: certs %v keys %v", rawCerts["keyed"] != nil, rawKeys["keyed"] != nil)
	}
	caCert, caKey := CACerts["keyring"], CAKeys["keyring"]
	lock.RUnlock()
	if caCert == nil || !bytes.Equal(caCert.Right, ca.cert) || caKey == nil || !bytes.Equal(caKey.Right, ca.key) {
		t.Errorf("CA cert and key of a domain with key in it: cert %+v key %+v", caCert, caKey)
	}
	mustOK(t, call(t, "POST", "/tls/cert/remove/monkey", ""))
	mustOK(t, call(t, "POST", "/tls/key/remove/keyed", ""))
	lock.RLock()
	defer lock.RUnlock()
	if len(rawCerts) != 0 || len(rawKeys) != 0 {
		t.Errorf("removals went to the wrong place: certs %d keys %d", len(rawCerts), len(rawKeys))
	}
}

func TestRegressionHandleClientHelloWithoutClientHello(t *testing.T) {
	rec := &recorder{}
	hello := rec.hello(&tls.Config{})
	var cfg *tls.Config
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v", p)
			}
		}()
		cfg, err = hello(nil)
	}()
	if cfg != nil || err == nil || strings.HasPrefix(err.Error(), "panic") {
		t.Errorf("= %v, %v, want an error that isn't a panic", cfg, err)
	}
}

func TestRegressionIdentifyLeafWithoutCerts(t *testing.T) {
	// The invocation of a client whose cert name has no cert uploaded gets no certs, and asks for the leaf of those
	for _, certs := range [][]*tls.Certificate{nil, {}, {nil}} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("IdentifyLeaf(%v) panicked: %v", certs, p)
				}
			}()
			if leaf := IdentifyLeaf(certs); leaf != nil {
				t.Errorf("IdentifyLeaf(%v) = %v, want nil", certs, leaf)
			}
		}()
	}
}

// A client that connects and goes away without sending anything (a port probe or a health check) used to make Accept
// fail, which stops an http server from accepting any more connections.
func TestRegressionTLSInspectorSurvivesIdleClients(t *testing.T) {
	isolate(t)
	cfg, _ := serverConfig(t, "srv.test")
	inner := listen(t)
	l := NewTLSInspector(1, "idle", inner, cfg)
	ch := acceptN(l, 1)
	for i := 0; i < 3; i++ {
		c, err := net.DialTimeout("tcp", inner.Addr().String(), ioTimeout)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	c := dial(t, inner)
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	a := nextAccepted(t, ch)
	if a.err != nil {
		t.Fatalf("Accept failed because of a client that sent nothing: %v", a.err)
	}
	defer a.conn.Close()
	_ = a.conn.SetReadDeadline(time.Now().Add(ioTimeout))
	b := make([]byte, 5)
	if _, err := io.ReadFull(a.conn, b); err != nil || string(b) != "hello" {
		t.Errorf("read %q, %v", b, err)
	}
}

// reframe wraps the handshake message in a TLS record of the right size.
func reframe(body []byte) []byte {
	return append([]byte{0x16, 0x03, 0x01, byte(len(body) >> 8), byte(len(body))}, body...)
}

// parseHello runs a ClientHello through the parsing that ReadTLSSNIFromConn does, without the output that it prints.
func parseHello(record []byte) (sni string, err error) {
	tlsBuff, err := readTLSHandshake(bytes.NewReader(record))
	if err != nil {
		return
	}
	if tlsBuff, err = getClientHelloData(tlsBuff); err != nil {
		return
	}
	getClientCipherSuites(tlsBuff)
	var extensions []byte
	if extensions, err = getClientHelloExtensions(tlsBuff); err != nil {
		return
	}
	getSignatureAlgorithms(extensions)
	return getSNIExtensionEntries(extensions)
}

func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("%s panicked: %v", what, p)
		}
	}()
	f()
}

// The ClientHello comes from whoever connects, and the parsing of it (that the TCP proxy does to route by SNI) used
// to index the buffer without checking the sizes that the message claims.
func TestRegressionClientHelloParsingOfMalformedMessages(t *testing.T) {
	hello := clientHello(t, "sni.example.test", "h2", "http/1.1")
	body := hello[5:]
	for cut := 0; cut <= len(body); cut++ {
		mustNotPanic(t, fmt.Sprintf("a ClientHello cut at %d of %d bytes", cut, len(body)), func() { parseHello(reframe(body[:cut])) })
	}
	for i := range body {
		for _, v := range []byte{0x00, 0x01, 0x7f, 0xff} {
			mutated := append([]byte(nil), body...)
			mutated[i] = v
			mustNotPanic(t, fmt.Sprintf("a ClientHello with byte %d set to %#x", i, v), func() { parseHello(reframe(mutated)) })
		}
	}
	if sni, err := parseHello(reframe(body)); err != nil || sni != "sni.example.test" {
		t.Errorf("the intact ClientHello gives SNI %q, %v", sni, err)
	}
}

func TestRegressionReadTLSSNIFromConnWithEmptyAndOddRecords(t *testing.T) {
	cases := map[string][]byte{
		"empty record":                 {0x16, 0x03, 0x01, 0x00, 0x00},
		"record of one byte":           {0x16, 0x03, 0x01, 0x00, 0x01, 0x01},
		"hello type only":              reframe([]byte{0x01}),
		"hello that claims more bytes": reframe([]byte{0x01, 0xff, 0xff, 0xff, 0x03, 0x03}),
	}
	for name, record := range cases {
		mustNotPanic(t, name, func() {
			if sni, _, err := ReadTLSSNIFromConn(newReaderConn(record)); err == nil && sni != "" {
				t.Errorf("%s gave SNI %q", name, sni)
			}
		})
	}
	// An extensions block with a single byte left over after the last extension
	mustNotPanic(t, "odd extension remainder", func() { getSNIExtensionEntries([]byte{0x00, 0x10, 0x00, 0x00, 0x01}) })
	mustNotPanic(t, "empty SNI list", func() { getSNIExtensionEntries([]byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00}) })
	mustNotPanic(t, "signature algorithms that claim more than they carry", func() {
		getSignatureAlgorithms([]byte{0x00, 0x0d, 0x00, 0x04, 0x00, 0x40, 0x04, 0x01})
	})
}

// A cert registered concurrently with the creation and listing of certs used to be a data race on the maps.
func TestRegressionConcurrentCAChangesAndAdminReads(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	errs := &errList{}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				AddCAKey("c", fmt.Sprintf("d%d.test", j), ca.key)
				AddCACert("c", fmt.Sprintf("d%d.test", j), ca.cert)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := CreateCertificate([]string{"d1.test"}, "", ""); err != nil {
					errs.addf("CreateCertificate: %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if rr := call(t, "GET", "/tls/ca/certs", ""); rr.Code != 200 {
					errs.addf("/tls/ca/certs answered %d", rr.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
	errs.report(t)
}

func TestRegressionConcurrentSpiffeRegistrations(t *testing.T) {
	isolate(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				Spiffe.Add("n", "d", fmt.Sprintf("spiffe://a/%d", j))
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				Spiffe.IsEmpty()
			}
		}()
	}
	wg.Wait()
	if Spiffe.IsEmpty() {
		t.Error("registrations were lost")
	}
}
