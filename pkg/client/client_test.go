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

package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/client/results"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	gototls "goto/pkg/tls"
	"goto/pkg/types"
	"goto/pkg/util"
)

var stubOnce sync.Once

// stubGlobalFuncs is for what the request store asks the server of, and there is no server here.
func stubGlobalFuncs() {
	stubOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	})
}

// useHostLabel is the label that the requests of the client carry as their user agent.
func useHostLabel(t *testing.T, label string) {
	t.Helper()
	prev := global.Self.HostLabel
	global.Self.HostLabel = label
	t.Cleanup(func() { global.Self.HostLabel = prev })
}

// newCallRequest is the request that asks the client to make calls, the way the server has it.
func newCallRequest(t *testing.T, method, target, body string, headers ...string) (*http.Request, *util.RequestStore) {
	t.Helper()
	stubGlobalFuncs()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, 8080))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	_, r, rs := util.WithRequestStore(r, nil)
	return r, rs
}

// seen is what a request that reached the server looked like.
type seen struct {
	Method   string
	Path     string
	RawQuery string
	Host     string
	Proto    string
	Body     string
	Header   http.Header
	// what the server found out about the TLS connection, when there was one
	TLSVersion uint16
	ALPN       string
	PeerCN     string
}

// recorder keeps the requests that reached a server.
type recorder struct {
	lock sync.Mutex
	reqs []seen
}

func (rc *recorder) add(r *http.Request, body string) {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	g := seen{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Host: r.Host, Proto: r.Proto, Body: body, Header: r.Header.Clone()}
	if r.TLS != nil {
		g.TLSVersion = r.TLS.Version
		g.ALPN = r.TLS.NegotiatedProtocol
		if len(r.TLS.PeerCertificates) > 0 {
			g.PeerCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}
	}
	rc.reqs = append(rc.reqs, g)
}

func (rc *recorder) all() []seen {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	return append([]seen(nil), rc.reqs...)
}

// echoHandler records what it gets and answers with it. The path decides on the status, which is 200 unless the path
// is /status/<code>.
func echoHandler(rc *recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.add(r, string(b))
		w.Header().Set("X-Echo", "yes")
		w.Header().Set(constants.HeaderViaGoto, "upstream-goto")
		status := 200
		if strings.HasPrefix(r.URL.Path, "/status/") {
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/status/"), "%d", &status)
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, "echo:%s", b)
	})
}

func newEchoServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rc := &recorder{}
	s := httptest.NewServer(echoHandler(rc))
	t.Cleanup(s.Close)
	return s, rc
}

// closedAddress is an address that nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestPrepareAuthority(t *testing.T) {
	for _, tc := range []struct {
		name          string
		spec          CallSpec
		hostHeader    string
		wantURL       string
		wantAuthority string
		wantTLS       bool
	}{
		{name: "no scheme", spec: CallSpec{URL: "example.com:8080/path"}, wantURL: "http://example.com:8080/path", wantAuthority: "example.com:8080"},
		{name: "no scheme with tls", spec: CallSpec{URL: "example.com/path", TLS: true}, wantURL: "https://example.com/path", wantAuthority: "example.com", wantTLS: true},
		{name: "http", spec: CallSpec{URL: "http://example.com/x"}, wantURL: "http://example.com/x", wantAuthority: "example.com"},
		{name: "https turns tls on", spec: CallSpec{URL: "https://example.com:9443"}, wantURL: "https://example.com:9443", wantAuthority: "example.com:9443", wantTLS: true},
		{name: "authority is kept", spec: CallSpec{URL: "http://10.0.0.1:80", Authority: "virtual.host"}, wantURL: "http://10.0.0.1:80", wantAuthority: "virtual.host"},
		{name: "authority from the request", spec: CallSpec{URL: "http://"}, hostHeader: "caller.host:7000", wantURL: "http://", wantAuthority: "caller.host:7000"},
		{name: "nothing to take the authority from", spec: CallSpec{URL: ""}, wantURL: "http://", wantAuthority: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tc.hostHeader != "" {
				r.Header.Set("Host", tc.hostHeader)
			}
			spec := tc.spec
			spec.PrepareAuthority(r)
			if spec.URL != tc.wantURL || spec.Authority != tc.wantAuthority || spec.TLS != tc.wantTLS {
				t.Errorf("url = %q, authority = %q, tls = %v; want %q, %q, %v", spec.URL, spec.Authority, spec.TLS, tc.wantURL, tc.wantAuthority, tc.wantTLS)
			}
		})
	}
}

func TestPrepareRequestBuildsTheRequest(t *testing.T) {
	useHostLabel(t, "client-host-1")
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "", "X-Forward-Me", "fwd-value", "X-Not-Forwarded", "nope", "Accept", "application/json")
	spec := &CallSpec{
		URL:     "http://127.0.0.1:9/some/path?a=b",
		Method:  "PUT",
		Payload: []string{"the-body"},
		Headers: &types.HeadersConfig{Forward: []string{"X-Forward-Me"}, Add: map[string]string{"X-Added": "added-value"}},
	}
	spec.PrepareAuthority(r)
	if err := spec.PrepareRequest(r); err != nil {
		t.Fatal(err)
	}
	req := spec.request
	if req.Method != "PUT" || req.URL.String() != "http://127.0.0.1:9/some/path?a=b" {
		t.Errorf("request = %s %s", req.Method, req.URL)
	}
	if b, _ := io.ReadAll(req.Body); string(b) != "the-body" {
		t.Errorf("body = %q", b)
	}
	for h, want := range map[string]string{
		"X-Forward-Me":                "fwd-value",
		"X-Added":                     "added-value",
		"Accept":                      "application/json",
		"User-Agent":                  "client-host-1",
		constants.HeaderGotoClientTLS: "false",
	} {
		if got := req.Header.Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
	if req.Header.Get("X-Not-Forwarded") != "" {
		t.Errorf("a header that was not asked for was forwarded")
	}
	if req.Header.Get(constants.HeaderGotoClientMTLS) != "" {
		t.Errorf("the mTLS header is there for a call that is not TLS")
	}
	if req.Host != "127.0.0.1:9" || spec.Authority != "127.0.0.1:9" {
		t.Errorf("host = %q, authority = %q", req.Host, spec.Authority)
	}
	if spec.streamWriter != nil {
		t.Errorf("a single payload is not streamed")
	}
}

func TestPrepareRequestAuthorityBecomesHost(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: "http://127.0.0.1:9", Authority: "virtual.example"}
	spec.PrepareAuthority(r)
	if err := spec.PrepareRequest(r); err != nil {
		t.Fatal(err)
	}
	if spec.request.Host != "virtual.example" {
		t.Errorf("host = %q", spec.request.Host)
	}
	// the host of the URL stays what the connection is made to
	if spec.request.URL.Host != "127.0.0.1:9" {
		t.Errorf("url host = %q", spec.request.URL.Host)
	}
}

func TestPrepareRequestTLSHeaders(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	for _, tc := range []struct {
		name       string
		spec       CallSpec
		tls, mtls  string
		wantNoMTLS bool
	}{
		{name: "tls", spec: CallSpec{URL: "https://127.0.0.1:9"}, tls: "true", mtls: "false"},
		{name: "mtls", spec: CallSpec{URL: "https://127.0.0.1:9", ClientCert: "my-cert"}, tls: "true", mtls: "true"},
		{name: "plain", spec: CallSpec{URL: "http://127.0.0.1:9", ClientCert: "ignored-without-tls"}, tls: "false", wantNoMTLS: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.PrepareAuthority(r)
			if err := spec.PrepareRequest(r); err != nil {
				t.Fatal(err)
			}
			h := spec.request.Header
			if h.Get(constants.HeaderGotoClientTLS) != tc.tls {
				t.Errorf("tls header = %q, want %q", h.Get(constants.HeaderGotoClientTLS), tc.tls)
			}
			if tc.wantNoMTLS {
				if _, found := h[http.CanonicalHeaderKey(constants.HeaderGotoClientMTLS)]; found {
					t.Errorf("mtls header = %v for a call that is not TLS", h.Values(constants.HeaderGotoClientMTLS))
				}
			} else if h.Get(constants.HeaderGotoClientMTLS) != tc.mtls {
				t.Errorf("mtls header = %q, want %q", h.Get(constants.HeaderGotoClientMTLS), tc.mtls)
			}
		})
	}
}

func TestPrepareRequestRequestID(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		spec := &CallSpec{URL: "http://127.0.0.1:9/p", RequestID: true}
		spec.PrepareAuthority(r)
		if err := spec.PrepareRequest(r); err != nil {
			t.Fatal(err)
		}
		id := spec.request.Header.Get("x-request-id")
		if len(id) != 36 || spec.request.URL.Query().Get("x-request-id") != id {
			t.Errorf("header id = %q, url = %s", id, spec.request.URL)
		}
		ids[id] = true
	}
	if len(ids) != 3 {
		t.Errorf("the ids are not different: %v", ids)
	}
	spec := &CallSpec{URL: "http://127.0.0.1:9/p"}
	spec.PrepareAuthority(r)
	spec.PrepareRequest(r)
	if spec.request.Header.Get("x-request-id") != "" || spec.request.URL.RawQuery != "" {
		t.Errorf("a request id was added to a call that didn't ask for it")
	}
}

func TestPrepareRequestStreamsMoreThanOnePayload(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: "http://127.0.0.1:9", Payload: []string{"one", "two"}, StreamDelay: "25ms"}
	spec.PrepareAuthority(r)
	if err := spec.PrepareRequest(r); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { spec.streamWriter.Close() })
	if spec.streamWriter == nil || spec.request.Body == nil {
		t.Fatalf("writer = %v, body = %v", spec.streamWriter, spec.request.Body)
	}
	if spec.streamDelayD != 25*time.Millisecond {
		t.Errorf("stream delay = %v", spec.streamDelayD)
	}
	go streamPayloads(spec.streamWriter, spec.Payload, 0)
	if b, _ := io.ReadAll(spec.request.Body); string(b) != "onetwo" {
		t.Errorf("streamed = %q", b)
	}
}

func TestPrepareRequestInvalidMethod(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: "http://127.0.0.1:9", Method: "NOT A METHOD"}
	spec.PrepareAuthority(r)
	if err := spec.PrepareRequest(r); err == nil {
		t.Errorf("a method that is not valid must be an error")
	}
	if spec.request != nil {
		t.Errorf("a request was made for it")
	}
}

func TestStreamPayloadsWritesChunksAndClosesTheWriter(t *testing.T) {
	pr, pw := io.Pipe()
	start := time.Now()
	go streamPayloads(pw, []string{"a", "b", "c"}, 20*time.Millisecond)
	b, err := io.ReadAll(pr)
	if err != nil || string(b) != "abc" {
		t.Errorf("read %q, %v", b, err)
	}
	if took := time.Since(start); took < 60*time.Millisecond {
		t.Errorf("the chunks arrived after %v, which is less than the delays before them", took)
	}
	// a writer that fails ends the streaming, and is closed
	pr2, pw2 := io.Pipe()
	pr2.Close()
	done := make(chan struct{})
	go func() { streamPayloads(pw2, []string{"x", "y"}, 0); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streaming to a closed pipe didn't end")
	}
}

func TestInvokeCallsTheServer(t *testing.T) {
	useHostLabel(t, "client-host-2")
	s, rc := newEchoServer(t)
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "", "X-Fwd", "fv")
	spec := &CallSpec{
		URL:     s.URL + "/hello?x=1",
		Method:  "POST",
		Payload: []string{"ping"},
		Headers: &types.HeadersConfig{Forward: []string{"X-Fwd"}, Add: map[string]string{"X-Add": "av"}},
	}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) != 1 || len(results.Statuses) != 1 || results.Statuses[0] != 200 || results.ClientCert != "" {
		t.Fatalf("results = %+v", results)
	}
	key := s.URL + "/hello?x=1[1]"
	res := results.Results[key]
	if res == nil || res.Status != 200 || res.Payload != "echo:ping" || res.Headers.Get("X-Echo") != "yes" {
		t.Fatalf("result for %q = %+v, all: %+v", key, res, results.Results)
	}
	if results.URL != spec.URL {
		t.Errorf("url = %q", results.URL)
	}
	got := rc.all()
	if len(got) != 1 {
		t.Fatalf("server saw %d requests", len(got))
	}
	if g := got[0]; g.Method != "POST" || g.Path != "/hello" || g.RawQuery != "x=1" || g.Body != "ping" ||
		g.Header.Get("X-Fwd") != "fv" || g.Header.Get("X-Add") != "av" || g.Header.Get("User-Agent") != "client-host-2" ||
		g.Header.Get(constants.HeaderGotoClientTLS) != "false" {
		t.Errorf("server saw %+v", g)
	}
}

func TestInvokeMakesAsManyCallsAsCounted(t *testing.T) {
	s, rc := newEchoServer(t)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	// a count that is not given is one call
	spec := &CallSpec{URL: s.URL}
	results, err := spec.Invoke(r)
	if err != nil || len(results.Results) != 1 || spec.Count != 1 {
		t.Fatalf("results = %+v, count = %d, err = %v", results, spec.Count, err)
	}
	spec = &CallSpec{URL: s.URL + "/many", Count: 4, Authority: "virtual.example"}
	results, err = spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) != 4 || len(results.Statuses) != 4 {
		t.Fatalf("results = %d, statuses = %d", len(results.Results), len(results.Statuses))
	}
	for i := 1; i <= 4; i++ {
		if res := results.Results[fmt.Sprintf("%s/many[%d]", s.URL, i)]; res == nil || res.Status != 200 {
			t.Errorf("result %d = %+v", i, res)
		}
	}
	reqs := rc.all()
	if len(reqs) != 5 {
		t.Fatalf("server saw %d requests, want 5", len(reqs))
	}
	for _, g := range reqs[1:] {
		if g.Host != "virtual.example" {
			t.Errorf("host = %q", g.Host)
		}
	}
}

func TestInvokeReportsStatusesThatAreNotSuccess(t *testing.T) {
	s, _ := newEchoServer(t)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	for _, code := range []int{201, 404, 503} {
		spec := &CallSpec{URL: fmt.Sprintf("%s/status/%d", s.URL, code), Count: 2}
		results, err := spec.Invoke(r)
		if err != nil {
			t.Fatalf("%d: %v", code, err)
		}
		if len(results.Statuses) != 2 || results.Statuses[0] != code || results.Statuses[1] != code {
			t.Errorf("%d: statuses = %v", code, results.Statuses)
		}
	}
}

func TestInvokeStreamsPayloads(t *testing.T) {
	s, rc := newEchoServer(t)
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL, Method: "POST", Payload: []string{"one-", "two-", "three"}, StreamDelay: "10ms"}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if res := results.Results[s.URL+"[1]"]; res == nil || res.Payload != "echo:one-two-three" {
		t.Errorf("result = %+v", res)
	}
	if got := rc.all(); len(got) != 1 || got[0].Body != "one-two-three" {
		t.Errorf("server saw %+v", got)
	}
}

func TestInvokeWithRequestID(t *testing.T) {
	s, rc := newEchoServer(t)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/id", Count: 2, RequestID: true}
	if _, err := spec.Invoke(r); err != nil {
		t.Fatal(err)
	}
	reqs := rc.all()
	if len(reqs) != 2 {
		t.Fatalf("server saw %d requests", len(reqs))
	}
	first, second := reqs[0].Header.Get("x-request-id"), reqs[1].Header.Get("x-request-id")
	if first == "" || first == second || reqs[0].RawQuery != "x-request-id="+first || reqs[1].RawQuery != "x-request-id="+second {
		t.Errorf("ids = %q, %q, queries = %q, %q", first, second, reqs[0].RawQuery, reqs[1].RawQuery)
	}
}

func TestInvokeFailures(t *testing.T) {
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	// nothing listens at the address
	spec := &CallSpec{URL: "http://" + closedAddress(t)}
	if results, err := spec.Invoke(r); err == nil || results != nil {
		t.Errorf("a call to an address that nothing listens at gave %+v, %v", results, err)
	}
	s, _ := newEchoServer(t)
	spec = &CallSpec{URL: s.URL, Method: "NOT A METHOD"}
	if results, err := spec.Invoke(r); err == nil || results != nil {
		t.Errorf("a method that is not valid gave %+v, %v", results, err)
	}
}

func TestInvokeOverTLS(t *testing.T) {
	rc := &recorder{}
	s := httptest.NewTLSServer(echoHandler(rc))
	t.Cleanup(s.Close)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	for _, noSNI := range []bool{false, true} {
		spec := &CallSpec{URL: s.URL + "/secure", NoSNI: noSNI}
		results, err := spec.Invoke(r)
		if err != nil {
			t.Fatalf("noSNI=%v: %v", noSNI, err)
		}
		if !spec.TLS {
			t.Errorf("a call to an https URL is a TLS call")
		}
		if len(results.Statuses) != 1 || results.Statuses[0] != 200 || len(results.ServerCerts) != 1 || results.ServerCerts[0] == "" {
			t.Errorf("noSNI=%v: results = %+v", noSNI, results)
		}
		res := results.Results[s.URL+"/secure[1]"]
		if res == nil || res.ServerCert == "" || res.ServerCert != results.ServerCerts[0] {
			t.Errorf("noSNI=%v: result = %+v", noSNI, res)
		}
	}
	for _, g := range rc.all() {
		if g.Header.Get(constants.HeaderGotoClientTLS) != "true" || g.Header.Get(constants.HeaderGotoClientMTLS) != "false" {
			t.Errorf("headers = %v", g.Header)
		}
	}
}

// selfSignedPEM is a certificate that identifies the given name, and its key.
func selfSignedPEM(t *testing.T, commonName string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse("spiffe://goto.test/" + commonName)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName + ".goto.test"},
		URIs:         []*url.URL{uri},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func newTLSServer(t *testing.T, rc *recorder, clientAuth tls.ClientAuthType) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(echoHandler(rc))
	s.TLS = &tls.Config{ClientAuth: clientAuth}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestInvokeWithClientCertificate(t *testing.T) {
	name := fmt.Sprintf("client-cert-%d", time.Now().UnixNano())
	certPEM, keyPEM := selfSignedPEM(t, "goto-client")
	gototls.AddCert(name, certPEM)
	gototls.AddKey(name, keyPEM)
	t.Cleanup(func() { gototls.RemoveCert(name); gototls.RemoveKey(name) })
	rc := &recorder{}
	s := newTLSServer(t, rc, tls.RequestClientCert)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/mtls", ClientCert: name}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if results.ClientCert == "" || !strings.Contains(results.ClientCert, "goto-client") {
		t.Errorf("the client certificate that was used is reported as %q", results.ClientCert)
	}
	got := rc.all()
	if len(got) != 1 || got[0].PeerCN != "goto-client" || got[0].Header.Get(constants.HeaderGotoClientMTLS) != "true" {
		t.Errorf("server saw %+v", got)
	}
	// without it the server has no client certificate to see
	spec = &CallSpec{URL: s.URL + "/no-mtls"}
	if _, err := spec.Invoke(r); err != nil {
		t.Fatal(err)
	}
	if got = rc.all(); len(got) != 2 || got[1].PeerCN != "" {
		t.Errorf("server saw %+v", got)
	}
}

func TestInvokeTLSSettings(t *testing.T) {
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	rc := &recorder{}
	s := newTLSServer(t, rc, tls.NoClientCert)

	// a server that is not trusted is not called when it's to be verified
	spec := &CallSpec{URL: s.URL + "/verify", VerifyTLS: true}
	if results, err := spec.Invoke(r); err == nil || results != nil {
		t.Errorf("a server that is not trusted gave %+v, %v", results, err)
	}
	if len(rc.all()) != 0 {
		t.Errorf("the server was called without being trusted")
	}
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		spec := &CallSpec{URL: s.URL + "/version", TLSVersion: version}
		if _, err := spec.Invoke(r); err != nil {
			t.Fatalf("version %x: %v", version, err)
		}
		got := rc.all()
		if g := got[len(got)-1]; g.TLSVersion != version {
			t.Errorf("asked for version %x, the server saw %x", version, g.TLSVersion)
		}
	}
	spec = &CallSpec{URL: s.URL + "/alpn", ALPN: &gototls.ALPN{Protos: []string{"http/1.1"}}}
	if _, err := spec.Invoke(r); err != nil {
		t.Fatal(err)
	}
	got := rc.all()
	if g := got[len(got)-1]; g.ALPN != "http/1.1" {
		t.Errorf("the server saw ALPN %q", g.ALPN)
	}
}

func TestInvokeOverHTTP2(t *testing.T) {
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	t.Run("tls", func(t *testing.T) {
		rc := &recorder{}
		s := httptest.NewUnstartedServer(echoHandler(rc))
		s.EnableHTTP2 = true
		s.StartTLS()
		t.Cleanup(s.Close)
		spec := &CallSpec{URL: s.URL + "/h2", IsH2: true, Count: 2}
		results, err := spec.Invoke(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(results.Statuses) != 2 || results.Statuses[0] != 200 {
			t.Errorf("results = %+v", results)
		}
		for _, g := range rc.all() {
			if g.Proto != "HTTP/2.0" {
				t.Errorf("protocol = %s", g.Proto)
			}
		}
		if len(rc.all()) != 2 {
			t.Errorf("server saw %d requests", len(rc.all()))
		}
	})
	t.Run("cleartext", func(t *testing.T) {
		rc := &recorder{}
		s := httptest.NewUnstartedServer(echoHandler(rc))
		s.Config.Protocols = new(http.Protocols)
		s.Config.Protocols.SetHTTP1(true)
		s.Config.Protocols.SetUnencryptedHTTP2(true)
		s.Start()
		t.Cleanup(s.Close)
		spec := &CallSpec{URL: s.URL + "/h2c", IsH2: true}
		results, err := spec.Invoke(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(results.Statuses) != 1 || results.Statuses[0] != 200 {
			t.Errorf("results = %+v", results)
		}
		if got := rc.all(); len(got) != 1 || got[0].Proto != "HTTP/2.0" {
			t.Errorf("server saw %+v", got)
		}
	})
}

var routesOnce sync.Once

// serve makes the request to the router that the middleware package keeps for the /client path.
func serve(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	routesOnce.Do(func() {
		stubGlobalFuncs()
		Middleware.SetRoutes(nil)
	})
	h := middleware.RootRouters["/client"]
	rr := httptest.NewRecorder()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		h.ServeHTTP(rr, r)
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s %s panicked: %v", r.Method, r.URL, p)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s %s did not return in time", r.Method, r.URL)
	}
	return rr
}

func TestInvokeHTTPAPI(t *testing.T) {
	s, rc := newEchoServer(t)
	for _, path := range []string{"/client/http/invoke", "/client/http/call"} {
		spec := fmt.Sprintf(`{"url":%q,"method":"POST","count":2,"payload":["api-body"],"headers":{"add":{"X-Api":"1"}}}`, s.URL+"/api")
		r, rs := newCallRequest(t, "POST", path, spec, "Accept", "application/json")
		rr := serve(t, r)
		if rr.Code != 200 {
			t.Fatalf("%s: status = %d, body: %s", path, rr.Code, rr.Body.String())
		}
		var out struct {
			URL      string
			Results  map[string]*CallResult
			Statuses []int
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: body %q: %v", path, rr.Body.String(), err)
		}
		if len(out.Results) != 2 || len(out.Statuses) != 2 || out.Results[s.URL+"/api[1]"].Payload != "echo:api-body" {
			t.Errorf("%s: out = %+v", path, out)
		}
		if got := rr.Header().Get(constants.HeaderGotoUpstreamStatus); got != "[200,200]" {
			t.Errorf("%s: upstream status header = %q", path, got)
		}
		if _, found := rr.Header()[constants.HeaderGotoClientCert]; !found {
			t.Errorf("%s: the client cert header is missing", path)
		}
		if _, found := rr.Header()[constants.HeaderGotoServerCert]; found {
			t.Errorf("%s: server certs reported for a call that is not TLS", path)
		}
		// what the upstream says about the gotos that the call went through is passed on
		if len(rs.ViaGotos) != 2 || rs.ViaGotos[0] != "upstream-goto" {
			t.Errorf("%s: via gotos = %v", path, rs.ViaGotos)
		}
	}
	if got := rc.all(); len(got) != 4 || got[0].Header.Get("X-Api") != "1" || got[0].Body != "api-body" {
		t.Errorf("server saw %d requests, first: %+v", len(got), got)
	}
}

func TestInvokeHTTPAPIFormats(t *testing.T) {
	s, _ := newEchoServer(t)
	// the spec may be YAML, and the answer is YAML unless JSON is asked for
	r, _ := newCallRequest(t, "PUT", "/client/http/invoke", fmt.Sprintf("url: %s/yaml\nmethod: GET\n", s.URL))
	rr := serve(t, r)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if json.Valid(rr.Body.Bytes()) || !strings.Contains(body, "echo:") || !strings.Contains(body, "/yaml[1]") {
		t.Errorf("body = %q", body)
	}
}

func TestInvokeHTTPAPIMethods(t *testing.T) {
	s, _ := newEchoServer(t)
	spec := fmt.Sprintf(`{"url":%q}`, s.URL)
	for _, method := range []string{"GET", "POST", "PUT", "OPTIONS"} {
		r, _ := newCallRequest(t, method, "/client/http/invoke", spec)
		if rr := serve(t, r); rr.Code != 200 {
			t.Errorf("%s: status = %d, body: %s", method, rr.Code, rr.Body.String())
		}
	}
	r, _ := newCallRequest(t, "DELETE", "/client/http/invoke", spec)
	if rr := serve(t, r); rr.Code == 200 {
		t.Errorf("DELETE: status = %d, want it to be refused", rr.Code)
	}
	r, _ = newCallRequest(t, "POST", "/client/http/other", spec)
	if rr := serve(t, r); rr.Code != http.StatusNotFound {
		t.Errorf("other: status = %d, want 404", rr.Code)
	}
}

func TestInvokeHTTPAPIErrors(t *testing.T) {
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", `{"url":`)
	rr := serve(t, r)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Failed to parse call spec") {
		t.Errorf("bad spec: status = %d, body: %s", rr.Code, rr.Body.String())
	}
	addr := closedAddress(t)
	r, _ = newCallRequest(t, "POST", "/client/http/invoke", fmt.Sprintf(`{"url":"http://%s/gone"}`, addr))
	rr = serve(t, r)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Failed to invoke URL [http://"+addr+"/gone]") {
		t.Errorf("unreachable: status = %d, body: %s", rr.Code, rr.Body.String())
	}
}

func TestInvokeHTTPAPIOverTLS(t *testing.T) {
	rc := &recorder{}
	s := httptest.NewTLSServer(echoHandler(rc))
	t.Cleanup(s.Close)
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", fmt.Sprintf(`{"url":%q}`, s.URL), "Accept", "application/json")
	rr := serve(t, r)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	var certs []string
	if err := json.Unmarshal([]byte(rr.Header().Get(constants.HeaderGotoServerCert)), &certs); err != nil || len(certs) != 1 || certs[0] == "" {
		t.Errorf("server cert header = %q, %v", rr.Header().Get(constants.HeaderGotoServerCert), err)
	}
}

func TestMiddlewareMountsTargetRoutes(t *testing.T) {
	r, _ := newCallRequest(t, "GET", "/client/targets", "")
	if rr := serve(t, r); rr.Code != 200 {
		t.Errorf("targets: status = %d, body: %s", rr.Code, rr.Body.String())
	}
	r, _ = newCallRequest(t, "GET", "/client/results", "")
	if rr := serve(t, r); rr.Code != 200 {
		t.Errorf("results: status = %d, body: %s", rr.Code, rr.Body.String())
	}
}

func TestInvokeConcurrently(t *testing.T) {
	// the specs are used by the goroutine that invokes them, but the transports and the trackers that they share are not
	s, rc := newEchoServer(t)
	const workers, calls = 6, 3
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	statuses := make(chan int, workers*calls)
	for w := 0; w < workers; w++ {
		r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
		wg.Add(1)
		go func() {
			defer wg.Done()
			spec := &CallSpec{URL: fmt.Sprintf("%s/worker%d", s.URL, w), Count: calls, Method: "POST", Payload: []string{"a", "b"}, RequestID: true}
			results, err := spec.Invoke(r)
			if err != nil {
				errs <- err
				return
			}
			for _, st := range results.Statuses {
				statuses <- st
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(statuses)
	for err := range errs {
		t.Error(err)
	}
	n := 0
	for st := range statuses {
		if st != 200 {
			t.Errorf("status = %d", st)
		}
		n++
	}
	if n != workers*calls || len(rc.all()) != workers*calls {
		t.Errorf("results = %d, server saw %d, want %d", n, len(rc.all()), workers*calls)
	}
}

// useClientConfig makes the config that Run takes from the command line, and puts back what it found when the test
// is over. What Run leaves in the targets and in the results is taken away then, too.
func useClientConfig(t *testing.T, cfg types.CmdClientConfig) {
	t.Helper()
	prev := global.CmdClientConfig
	global.CmdClientConfig = cfg
	t.Cleanup(func() {
		global.CmdClientConfig = prev
		results.EnableInvocationResults(false)
		r, _ := newCallRequest(t, "POST", "/client/targets/clear", "")
		serve(t, r)
		results.ClearResults()
	})
}

func targetsOf(t *testing.T) map[string]map[string]interface{} {
	t.Helper()
	r, _ := newCallRequest(t, "GET", "/client/targets", "")
	rr := serve(t, r)
	if rr.Code != 200 {
		t.Fatalf("targets: status = %d, body: %s", rr.Code, rr.Body.String())
	}
	targets := map[string]map[string]interface{}{}
	if err := json.Unmarshal(rr.Body.Bytes(), &targets); err != nil {
		t.Fatalf("targets %q: %v", rr.Body.String(), err)
	}
	return targets
}

// summaryOf is what the results say of each target that was invoked, which is the invocation counts by the names.
func summaryOf(t *testing.T) map[string]struct {
	InvocationCount         int
	RetriedInvocationCounts int
	CountsByStatus          map[string]int
} {
	t.Helper()
	summary := map[string]struct {
		InvocationCount         int
		RetriedInvocationCounts int
		CountsByStatus          map[string]int
	}{}
	if err := json.Unmarshal([]byte(results.GetTargetsResultsJSON(false)), &summary); err != nil {
		t.Fatal(err)
	}
	return summary
}

func TestRunInvokesEveryURL(t *testing.T) {
	s, rc := newEchoServer(t)
	useClientConfig(t, types.CmdClientConfig{
		URLs:         []string{s.URL + "/run-a", s.URL + "/run-b"},
		Method:       "POST",
		Payload:      "run-body",
		RequestCount: 2,
		Parallel:     1,
		Headers:      map[string]string{"X-Run": "1"},
	})
	Run()
	perPath := map[string]int{}
	for _, g := range rc.all() {
		perPath[g.Path]++
		if g.Method != "POST" || g.Body != "run-body" || g.Header.Get("X-Run") != "1" || g.Header.Get("From-Goto-Host") == "" {
			t.Errorf("server saw %+v", g)
		}
	}
	if len(perPath) != 2 || perPath["/run-a"] != 2 || perPath["/run-b"] != 2 {
		t.Errorf("calls per path = %v", perPath)
	}
	summary := summaryOf(t)
	for _, name := range []string{s.URL + "/run-a", s.URL + "/run-b"} {
		if got := summary[name]; got.InvocationCount != 2 || got.CountsByStatus["200 OK"] != 2 {
			t.Errorf("results of %s = %+v", name, got)
		}
	}
	if targets := targetsOf(t); len(targets) != 2 || targets[s.URL+"/run-a"] == nil || targets[s.URL+"/run-b"] == nil {
		t.Errorf("targets = %v", targets)
	}
}

func TestRunMakesTheTargetsOfTheConfig(t *testing.T) {
	s, rc := newEchoServer(t)
	useClientConfig(t, types.CmdClientConfig{
		URLs:         []string{s.URL + "/mapped"},
		Protocol:     "HTTP/1.1",
		Method:       "PUT",
		Payload:      "mapped-body",
		Headers:      map[string]string{"X-A": "a"},
		RequestCount: 3,
		Parallel:     2,
		Delay:        "15ms",
		Retries:      1,
		RetryDelay:   "20ms",
		RetryOn:      []int{502, 503},
	})
	Run()
	target := targetsOf(t)[s.URL+"/mapped"]
	if target == nil {
		t.Fatalf("targets = %v", targetsOf(t))
	}
	for field, want := range map[string]interface{}{
		"name": s.URL + "/mapped", "url": s.URL + "/mapped", "protocol": "HTTP/1.1", "method": "PUT", "body": "mapped-body",
		"replicas": float64(2), "requestCount": float64(3), "delay": "15ms", "retries": float64(1), "retryDelay": "20ms",
		"lowerHeaders": true, "autoInvoke": false,
	} {
		if target[field] != want {
			t.Errorf("%s = %v, want %v", field, target[field], want)
		}
	}
	if codes, _ := target["retriableStatusCodes"].([]interface{}); len(codes) != 2 || codes[0] != float64(502) || codes[1] != float64(503) {
		t.Errorf("retriable status codes = %v", target["retriableStatusCodes"])
	}
	if headers, _ := target["headers"].(map[string]interface{}); headers["X-A"] != "a" {
		t.Errorf("headers = %v", target["headers"])
	}
	// the replicas each make the requests that are counted
	if got := len(rc.all()); got != 6 {
		t.Errorf("server saw %d requests, want 6", got)
	}
	if got := summaryOf(t)[s.URL+"/mapped"].InvocationCount; got != 6 {
		t.Errorf("invocation count = %d, want 6", got)
	}
}

func TestRunRetriesOnTheStatusesGiven(t *testing.T) {
	rc := &recorder{}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.add(r, "")
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(s.Close)
	useClientConfig(t, types.CmdClientConfig{URLs: []string{s.URL + "/flaky"}, RequestCount: 1, Parallel: 1, Retries: 2, RetryDelay: "10ms", RetryOn: []int{503}})
	Run()
	reqs := rc.all()
	if len(reqs) != 2 || reqs[0].Header.Get("Goto-Retry-Count") != "" || reqs[1].Header.Get("Goto-Retry-Count") != "1" {
		t.Fatalf("server saw %+v", reqs)
	}
	got := summaryOf(t)[s.URL+"/flaky"]
	if got.InvocationCount != 1 || got.RetriedInvocationCounts != 1 || got.CountsByStatus["200 OK"] != 1 {
		t.Errorf("results = %+v", got)
	}
}

func TestRunWithoutURLs(t *testing.T) {
	useClientConfig(t, types.CmdClientConfig{RequestCount: 2})
	Run()
	if targets := targetsOf(t); len(targets) != 0 {
		t.Errorf("targets = %v", targets)
	}
	for name, got := range summaryOf(t) {
		if name != "" || got.InvocationCount != 0 {
			t.Errorf("results of %q = %+v", name, got)
		}
	}
}

func TestRunSkipsTargetsThatAreNotValid(t *testing.T) {
	s, rc := newEchoServer(t)
	useClientConfig(t, types.CmdClientConfig{URLs: []string{"", s.URL + "/valid"}, RequestCount: 1, Parallel: 1})
	Run()
	if got := rc.all(); len(got) != 1 || got[0].Path != "/valid" {
		t.Errorf("server saw %+v", got)
	}
	if targets := targetsOf(t); len(targets) != 1 || targets[s.URL+"/valid"] == nil {
		t.Errorf("targets = %v", targets)
	}
}

func TestRunCollectsInvocationResultsWhenVerbose(t *testing.T) {
	s, _ := newEchoServer(t)
	detailed := func() map[string]struct {
		Target  struct{ Name string }
		Results []json.RawMessage
	} {
		out := map[string]struct {
			Target  struct{ Name string }
			Results []json.RawMessage
		}{}
		if err := json.Unmarshal([]byte(results.GetInvocationResultsJSON(false)), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	useClientConfig(t, types.CmdClientConfig{URLs: []string{s.URL + "/quiet"}, RequestCount: 2, Parallel: 1})
	Run()
	// the invocation is reported, but the results of its requests are kept only when that is asked for
	for _, inv := range detailed() {
		if len(inv.Results) != 0 {
			t.Errorf("results of the requests were collected without being asked for: %v", inv.Results)
		}
	}
	useClientConfig(t, types.CmdClientConfig{URLs: []string{s.URL + "/verbose"}, RequestCount: 2, Parallel: 1, Verbose: true})
	Run()
	found := false
	for _, inv := range detailed() {
		if inv.Target.Name == s.URL+"/verbose" {
			found = true
			if len(inv.Results) != 2 {
				t.Errorf("results of the invocation = %d, want 2", len(inv.Results))
			}
		}
	}
	if !found {
		t.Errorf("invocation results = %s", results.GetInvocationResultsJSON(false))
	}
}
