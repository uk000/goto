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

package transport

import (
	"crypto/tls"
	"fmt"
	gototls "goto/pkg/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// newTLSServer starts a TLS server that answers with the protocol of the request, and reports the SNI of every
// ClientHello it receives to onHello.
func newTLSServer(t *testing.T, enableH2 bool, onHello func(sni string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Proto)
	}))
	srv.EnableHTTP2 = enableH2
	srv.TLS = &tls.Config{GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
		if onHello != nil {
			onHello(chi.ServerName)
		}
		return nil, nil
	}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func newClient(h2, autoUpgrade bool) ClientTransport {
	return CreateHTTPClient(0, "test", h2, autoUpgrade, true, false, "", 10*time.Second, 5*time.Second, 5*time.Second, nil)
}

type reply struct {
	body  string
	major int
}

func doGet(ct ClientTransport, url string) (reply, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return reply{}, err
	}
	resp, err := ct.HTTP().Do(req)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return reply{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return reply{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	return reply{body: string(body), major: resp.ProtoMajor}, nil
}

// hammer sends requests from concurrent workers, each of them updating the TLS config of the shared client with an SNI and
// a version of its own (as every request of a target does), and returns the first error of every worker. The workers do not
// verify the certificate of the server, which is not trusted.
func hammer(ct ClientTransport, url string, workers, rounds int) []error {
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				version := uint16(tls.VersionTLS12)
				if w%2 == 1 {
					version = tls.VersionTLS13
				}
				ct.UpdateTLSConfig(fmt.Sprintf("worker%d.example", w), version, false, nil)
				ct.UpdateTLSCerts(nil, nil)
				if _, err := doGet(ct, url); err != nil {
					errs[w] = fmt.Errorf("worker %d round %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	return errs
}

func expectNoErrors(t *testing.T, errs []error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Errorf("request failed: %v", err)
		}
	}
}

// Concurrent requests of one client update its TLS config, which has to be safe to do from many goroutines. These tests are
// meant for the race detector.

func TestConcurrentTLSConfigUpdatesHTTP1(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	ct := newClient(false, false)
	defer ct.Close()
	expectNoErrors(t, hammer(ct, srv.URL, 8, 6))
}

func TestConcurrentTLSConfigUpdatesHTTP2(t *testing.T) {
	srv := newTLSServer(t, true, nil)
	ct := newClient(true, false)
	defer ct.Close()
	expectNoErrors(t, hammer(ct, srv.URL, 8, 6))
}

func TestConcurrentTLSConfigUpdatesPlainHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Proto) }))
	defer srv.Close()
	ct := CreateHTTPClient(0, "plain", false, false, false, false, "", 10*time.Second, 5*time.Second, 5*time.Second, nil)
	defer ct.Close()
	expectNoErrors(t, hammer(ct, srv.URL, 8, 6))
}

// Updating the config needs no request to be running for it to be called concurrently, which is what makes the race
// detector see it for sure, as requests synchronize the workers that make them.
func TestConcurrentUpdatesOfTheTLSConfig(t *testing.T) {
	ct := newClient(false, false)
	defer ct.Close()
	dct := ct.(*DefaultClientTransport)
	cert := tls.Certificate{Certificate: [][]byte{{1}}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				ct.UpdateTLSConfig(fmt.Sprintf("w%d.example", w), tls.VersionTLS12, false, nil)
				ct.UpdateTLSCerts(nil, []*tls.Certificate{&cert})
				dct.GetTLSConfig()
			}
		}(w)
	}
	close(start)
	wg.Wait()
	if got := len(dct.GetTLSConfig().Certificates); got != 1 {
		t.Errorf("certificates after the same certificate was applied 400 times = %d, want 1", got)
	}
}

// Whatever a request sets as SNI has to be what its handshake sends, which is checked here one request after the other,
// so that it holds with the dialer not using the config that the transport was created with.
func TestUpdateTLSConfigSNIReachesTheHandshake(t *testing.T) {
	var lock sync.Mutex
	var seen []string
	srv := newTLSServer(t, false, func(sni string) {
		lock.Lock()
		defer lock.Unlock()
		seen = append(seen, sni)
	})
	ct := newClient(false, false)
	defer ct.Close()
	want := []string{"a.example", "b.example", "a.example"}
	for _, sni := range want {
		ct.UpdateTLSConfig(sni, tls.VersionTLS12, false, nil)
		ct.HTTP().CloseIdleConnections()
		if _, err := doGet(ct, srv.URL); err != nil {
			t.Fatalf("request with sni %q: %v", sni, err)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("SNI of the handshakes = %v, want %v", seen, want)
	}
}

func TestUpdateTLSConfigVerifyAndVersionReachTheHandshake(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	ct := newClient(false, false)
	defer ct.Close()
	// The certificate of the server is not trusted, so a request that verifies it fails, and one that doesn't succeeds.
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, true, nil)
	if _, err := doGet(ct, srv.URL); err == nil {
		t.Errorf("request with verification of an untrusted certificate succeeded")
	}
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, false, nil)
	ct.HTTP().CloseIdleConnections()
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Errorf("request without verification failed: %v", err)
	}
	cfg := ct.(*DefaultClientTransport).GetTLSConfig()
	if !cfg.InsecureSkipVerify || cfg.ServerName != "a.example" || cfg.MinVersion != tls.VersionTLS12 || cfg.MaxVersion != tls.VersionTLS12 {
		t.Errorf("TLS config = {skipVerify:%v serverName:%q min:%x max:%x}, want {true a.example 303 303}",
			cfg.InsecureSkipVerify, cfg.ServerName, cfg.MinVersion, cfg.MaxVersion)
	}
}

// A client that is allowed to upgrade to HTTP/2 offers h2 in its handshakes, which net/http arranges lazily for a transport
// that has a TLS config.
func TestAutoUpgradeNegotiatesHTTP2(t *testing.T) {
	srv := newTLSServer(t, true, nil)
	ct := newClient(false, true)
	defer ct.Close()
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, false, nil)
	got, err := doGet(ct, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.major != 2 || got.body != "HTTP/2.0" {
		t.Errorf("response = %+v, want HTTP/2", got)
	}
}

func TestHTTP1ClientStaysOnHTTP1(t *testing.T) {
	srv := newTLSServer(t, true, nil)
	ct := newClient(false, false)
	defer ct.Close()
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, false, nil)
	got, err := doGet(ct, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.major != 1 || got.body != "HTTP/1.1" {
		t.Errorf("response = %+v, want HTTP/1.1", got)
	}
}

func TestHTTP2ClientNegotiatesHTTP2(t *testing.T) {
	srv := newTLSServer(t, true, nil)
	ct := newClient(true, false)
	defer ct.Close()
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, false, nil)
	got, err := doGet(ct, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.major != 2 {
		t.Errorf("response = %+v, want HTTP/2", got)
	}
}

// The certificates of the client are the same ones for every request of a target, so applying them again and again must not
// make the config grow.
func TestUpdateTLSCertsDoesNotAccumulateCertificates(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	cert := srv.TLS.Certificates[0]
	ct := newClient(false, false)
	defer ct.Close()
	for i := 0; i < 5; i++ {
		ct.UpdateTLSCerts(nil, []*tls.Certificate{&cert})
	}
	cfg := ct.(*DefaultClientTransport).GetTLSConfig()
	if len(cfg.Certificates) != 1 {
		t.Fatalf("certificates after applying one certificate 5 times = %d, want 1", len(cfg.Certificates))
	}
	got, err := cfg.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil || got == nil || !reflect.DeepEqual(got.Certificate, cert.Certificate) {
		t.Errorf("client certificate = %v, %v, want the applied one", got, err)
	}
}

func TestUpdateTLSCertsKeepsTheFirstCertificate(t *testing.T) {
	first := tls.Certificate{Certificate: [][]byte{{1}}}
	second := tls.Certificate{Certificate: [][]byte{{2}}}
	ct := newClient(false, false)
	defer ct.Close()
	ct.UpdateTLSCerts(nil, []*tls.Certificate{&first})
	ct.UpdateTLSCerts(nil, []*tls.Certificate{&second, &first})
	cfg := ct.(*DefaultClientTransport).GetTLSConfig()
	if len(cfg.Certificates) != 2 || cfg.Certificates[0].Certificate[0][0] != 1 || cfg.Certificates[1].Certificate[0][0] != 2 {
		t.Errorf("certificates = %v, want the first one then the second one", cfg.Certificates)
	}
	if got, _ := cfg.GetClientCertificate(nil); got.Certificate[0][0] != 1 {
		t.Errorf("client certificate = %v, want the first one", got.Certificate)
	}
}

// The config that a request updates must not be the one that the dialers of other requests are using.
func TestGetTLSConfigReturnsACopy(t *testing.T) {
	ct := newClient(false, false)
	defer ct.Close()
	dct := ct.(*DefaultClientTransport)
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, false, nil)
	cfg := dct.GetTLSConfig()
	cfg.ServerName = "changed.example"
	if got := dct.GetTLSConfig().ServerName; got != "a.example" {
		t.Errorf("server name after changing the returned config = %q, want a.example", got)
	}
}

func TestSetTLSConfigIsUsedForDialing(t *testing.T) {
	var lock sync.Mutex
	var seen []string
	srv := newTLSServer(t, false, func(sni string) {
		lock.Lock()
		defer lock.Unlock()
		seen = append(seen, sni)
	})
	ct := newClient(false, false)
	defer ct.Close()
	ct.SetTLSConfig(&tls.Config{ServerName: "set.example", InsecureSkipVerify: true})
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Fatal(err)
	}
	lock.Lock()
	defer lock.Unlock()
	if !reflect.DeepEqual(seen, []string{"set.example"}) {
		t.Errorf("SNI of the handshakes = %v, want [set.example]", seen)
	}
}

func TestPeerCertInfoIsRecordedForTLSRequests(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	ct := newClient(false, false)
	defer ct.Close()
	if ct.GetPeerCertInfo() != nil {
		t.Errorf("peer cert info before any request = %+v, want none", ct.GetPeerCertInfo())
	}
	ct.UpdateTLSConfig("a.example", tls.VersionTLS12, false, nil)
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Fatal(err)
	}
	pci := ct.GetPeerCertInfo()
	if pci == nil || pci.SNI != "a.example" {
		t.Fatalf("peer cert info = %+v, want the one of a handshake with SNI a.example", pci)
	}
	// What was handed out is a snapshot, which later handshakes do not change.
	ct.UpdateTLSConfig("b.example", tls.VersionTLS12, false, nil)
	ct.HTTP().CloseIdleConnections()
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Fatal(err)
	}
	if pci.SNI != "a.example" {
		t.Errorf("snapshot of the peer cert info changed to SNI %q", pci.SNI)
	}
	if now := ct.GetPeerCertInfo(); now == nil || now.SNI != "b.example" {
		t.Errorf("peer cert info after the second handshake = %+v, want SNI b.example", now)
	}
}

func TestUpdateTLSConfigALPN(t *testing.T) {
	alpn := &gototls.ALPN{Protos: []string{"x-proto"}, KeepDefault: true}
	h1 := newClient(false, false).(*DefaultClientTransport)
	defer h1.Close()
	h1.UpdateTLSConfig("a.example", tls.VersionTLS12, false, alpn)
	if got := h1.GetTLSConfig().NextProtos; !reflect.DeepEqual(got, []string{"x-proto", "http/1.1"}) {
		t.Errorf("protocols of an HTTP/1 client = %v, want [x-proto http/1.1]", got)
	}
	handlers := h1.h1Transport.TLSNextProto
	if len(handlers) != 2 || handlers["x-proto"] == nil || handlers["http/1.1"] == nil {
		t.Errorf("protocol handlers of an HTTP/1 client = %v, want the ones of x-proto and http/1.1", handlers)
	}
	// The same ALPN that every request of a target applies leaves the handlers that the transport is using in place.
	h1.UpdateTLSConfig("a.example", tls.VersionTLS12, false, alpn)
	if reflect.ValueOf(h1.h1Transport.TLSNextProto).Pointer() != reflect.ValueOf(handlers).Pointer() {
		t.Errorf("protocol handlers were replaced by an ALPN that is the same")
	}
	if !reflect.DeepEqual(alpn.Protos, []string{"x-proto"}) {
		t.Errorf("protocols of the given ALPN = %v, want them left as they were", alpn.Protos)
	}

	h2 := newClient(true, false).(*DefaultClientTransport)
	defer h2.Close()
	for i := 0; i < 3; i++ {
		h2.UpdateTLSConfig("a.example", tls.VersionTLS12, false, alpn)
	}
	if got := h2.GetTLSConfig().NextProtos; !reflect.DeepEqual(got, []string{"h2", "x-proto"}) {
		t.Errorf("protocols of an HTTP/2 client after applying the ALPN 3 times = %v, want [h2 x-proto]", got)
	}

	replaced := newClient(false, false).(*DefaultClientTransport)
	defer replaced.Close()
	replaced.UpdateTLSConfig("a.example", tls.VersionTLS12, false, &gototls.ALPN{Protos: []string{"only"}})
	if got := replaced.GetTLSConfig().NextProtos; !reflect.DeepEqual(got, []string{"only"}) {
		t.Errorf("protocols of a client that doesn't keep the defaults = %v, want [only]", got)
	}
}
