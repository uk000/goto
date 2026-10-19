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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/server/middleware"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

var routesOnce sync.Once

// api serves the requests to the routes that the listeners register, which are the ones of the server.
func api(t *testing.T) http.Handler {
	t.Helper()
	setup(t)
	routesOnce.Do(func() { Middleware.SetRoutes(mux.NewRouter()) })
	return middleware.RootRouters["/server"]
}

func call(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	handler := api(t)
	req := newRequestWithBody(method, target, body, apiPort, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func expectStatus(t *testing.T, rr *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if rr.Code != code || !strings.Contains(rr.Body.String(), body) {
		t.Errorf("response = %d %q, want %d containing %q", rr.Code, rr.Body.String(), code, body)
	}
}

func addJSON(port int, protocol string, open bool, extra string) string {
	return fmt.Sprintf(`{"port":%d,"protocol":%q,"open":%t%s}`, port, protocol, open, extra)
}

func TestAPIAddAndGetListeners(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	rr := call(t, "POST", "/server/listeners/add", addJSON(port, "http", true, ""))
	expectStatus(t, rr, 200, fmt.Sprintf("Listener %d added and opened.", port))
	if body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", port), nil); body != "served" {
		t.Errorf("the added listener serves %q, want served", body)
	}

	one := call(t, "GET", fmt.Sprintf("/server/listeners/%d", port), "")
	var l map[string]any
	if one.Code != 200 || json.Unmarshal(one.Body.Bytes(), &l) != nil {
		t.Fatalf("GET listener = %d %q", one.Code, one.Body.String())
	}
	if l["port"] != float64(port) || l["open"] != true || l["protocol"] != "http" || l["l8Proto"] != "h2c" || l["label"] == "" {
		t.Errorf("listener = %v, want an open h2c http listener on %d with a label", l, port)
	}

	all := call(t, "GET", "/server/listeners", "")
	var m map[string]map[string]any
	if all.Code != 200 || json.Unmarshal(all.Body.Bytes(), &m) != nil {
		t.Fatalf("GET listeners = %d %q", all.Code, all.Body.String())
	}
	if m[fmt.Sprint(port)] == nil || m[fmt.Sprint(DefaultListener.Port)] == nil {
		t.Errorf("listeners = %d entries, want the added one and the default one", len(m))
	}
	// The path also answers to the singular, and to a trailing slash.
	for _, path := range []string{"/server/listener", "/server/listeners/"} {
		if rr := call(t, "GET", path, ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), fmt.Sprint(port)) {
			t.Errorf("GET %s = %d %q, want the listeners", path, rr.Code, rr.Body.String())
		}
	}

	ports := call(t, "GET", "/server/listeners/ports", "")
	var pm map[string]string
	if ports.Code != 200 || json.Unmarshal(ports.Body.Bytes(), &pm) != nil || pm[fmt.Sprint(port)] != "h2c" {
		t.Errorf("GET ports = %d %q, want %d -> h2c", ports.Code, ports.Body.String(), port)
	}
}

func TestAPIAddListenerRejectsBadInput(t *testing.T) {
	expectStatus(t, call(t, "POST", "/server/listeners/add", "{not json"), 400, "Failed to parse json")
	expectStatus(t, call(t, "POST", "/server/listeners/add", `{"port":70000,"protocol":"http"}`), 400, "Invalid port number: 70000")
	expectStatus(t, call(t, "PUT", "/server/listeners/update", `{"protocol":"http"}`), 400, "Invalid port number: 0")
	if IsListenerPresent(70000) {
		t.Error("a listener with an invalid port was stored")
	}
	// Adding is not done with GET: "add" is taken for the port of a listener, and lists the listeners instead.
	if rr := call(t, "GET", "/server/listeners/add", ""); IsListenerPresent(0) || rr.Code != 200 {
		t.Errorf("GET add = %d, present = %t, want nothing to be added", rr.Code, IsListenerPresent(0))
	}
}

func TestAPIUpdateListener(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	expectStatus(t, call(t, "POST", "/server/listeners/add", addJSON(port, "http", false, "")), 200, fmt.Sprintf("Listener %d added.", port))
	if isListening(port) {
		t.Fatal("a listener that was added closed is open")
	}
	expectStatus(t, call(t, "PUT", "/server/listeners/update", addJSON(port, "http", true, "")), 200, fmt.Sprintf("Listener %d already present, restarted.", port))
	if !isListening(port) {
		t.Error("the updated listener is not open")
	}
}

func TestAPIOpenCloseRemove(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	base := fmt.Sprintf("/server/listeners/%d", port)
	call(t, "POST", "/server/listeners/add", addJSON(port, "http", false, ""))

	expectStatus(t, call(t, "POST", base+"/open", ""), 200, fmt.Sprintf("Listener opened on port %d", port))
	if !isListening(port) {
		t.Error("the listener is not open after open")
	}
	expectStatus(t, call(t, "POST", base+"/open", ""), 200, fmt.Sprintf("Listener reopened on port %d", port))
	if !isListening(port) {
		t.Error("the listener is not open after a reopen")
	}
	expectStatus(t, call(t, "POST", base+"/close", ""), 200, fmt.Sprintf("Listener on port %d closed", port))
	if isListening(port) {
		t.Error("the listener is open after close")
	}
	expectStatus(t, call(t, "POST", base+"/close", ""), 400, fmt.Sprintf("Port %d not open", port))
	expectStatus(t, call(t, "PUT", base+"/reopen", ""), 200, fmt.Sprintf("Listener opened on port %d", port))
	if !isListening(port) {
		t.Error("the listener is not open after reopen")
	}
	expectStatus(t, call(t, "POST", base+"/remove", ""), 200, fmt.Sprintf("Listener on port %d removed", port))
	if isListening(port) || IsListenerPresent(port) {
		t.Errorf("the listener is still there after remove: listening = %t, present = %t", isListening(port), IsListenerPresent(port))
	}
	for _, op := range []string{"open", "close", "remove", "reopen", "cert/autosni", "ca/clear"} {
		expectStatus(t, call(t, "POST", base+"/"+op, ""), 400, "No listener on the port")
	}
	if rr := call(t, "GET", base, ""); rr.Code != 200 || strings.Contains(rr.Body.String(), "listenerID") {
		t.Errorf("GET removed listener = %d %q, want no listener", rr.Code, rr.Body.String())
	}
}

func TestAPIListenerCertsAndKeys(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	base := fmt.Sprintf("/server/listeners/%d", port)
	expectStatus(t, call(t, "POST", "/server/listeners/add", addJSON(port, "https1", true, `,"commonName":"api.test"`)), 200, "added and opened")
	l := GetListenerForPort(port)

	// The cert and the key that the listener generated are served.
	cert := call(t, "GET", base+"/cert", "")
	if cert.Code != 200 || !strings.HasPrefix(cert.Body.String(), "-----BEGIN CERTIFICATE-----") || cert.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("GET cert = %d %q (%s), want a PEM cert", cert.Code, cert.Body.String(), cert.Header().Get("Content-Type"))
	}
	if key := call(t, "GET", base+"/key", ""); key.Code != 200 || !strings.Contains(key.Body.String(), "PRIVATE KEY") {
		t.Errorf("GET key = %d %q, want a PEM key", key.Code, key.Body.String())
	}

	// The cert that is uploaded is the one that the listener serves from then on, and what is served is what was uploaded.
	pemCert, pemKey := certAndKey(t, "uploaded.test")
	expectStatus(t, call(t, "PUT", base+"/cert/add", string(pemCert)), 200, fmt.Sprintf("Cert added for listener %d", port))
	expectStatus(t, call(t, "PUT", base+"/key/add", string(pemKey)), 200, fmt.Sprintf("Key added for listener %d", port))
	if string(l.RawCert) != string(pemCert) || string(l.RawKey) != string(pemKey) {
		t.Errorf("raw cert holds the cert = %t, raw key holds the key = %t, want both", string(l.RawCert) == string(pemCert), string(l.RawKey) == string(pemKey))
	}
	if got := call(t, "GET", base+"/cert", "").Body.String(); got != string(pemCert) {
		t.Errorf("GET cert after upload = %q, want the uploaded cert", got)
	}
	if got := call(t, "GET", base+"/key", "").Body.String(); got != string(pemKey) {
		t.Errorf("GET key after upload = %q, want the uploaded key", got)
	}
	expectStatus(t, call(t, "PUT", base+"/cert/add", ""), 400, "No payload")
	expectStatus(t, call(t, "PUT", base+"/key/add", ""), 400, "No payload")

	expectStatus(t, call(t, "POST", base+"/reopen", ""), 200, "reopened")
	if got := servedCertNames(t, port); !contains(got, "uploaded.test") {
		t.Errorf("the listener serves a cert for %v after the reopen, want the uploaded one for uploaded.test", got)
	}

	// Removing the certs reopens the listener without TLS.
	expectStatus(t, call(t, "POST", base+"/cert/remove", ""), 200, fmt.Sprintf("Cert and Key removed for listener %d, and reopened", port))
	cur := GetListenerForPort(port)
	if cur.TLS || cur.RawCert != nil || cur.RawKey != nil {
		t.Errorf("after removal: tls = %t, raw cert = %d bytes, raw key = %d bytes, want none", cur.TLS, len(cur.RawCert), len(cur.RawKey))
	}
	if body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", port), nil); body != "served" {
		t.Errorf("the listener serves %q over plain HTTP after the removal, want served", body)
	}
	// With nothing to serve, the response is empty.
	if rr := call(t, "GET", base+"/cert", ""); rr.Code != 200 || rr.Body.Len() != 0 {
		t.Errorf("GET cert with no cert = %d %q, want an empty 200", rr.Code, rr.Body.String())
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// servedCertNames is what the listener presents to a client that doesn't ask for a name.
func servedCertNames(t *testing.T, port int) []string {
	return servedCertNamesFor(t, port, "")
}

func servedCertNamesFor(t *testing.T, port int, sni string) []string {
	t.Helper()
	client := &http.Client{Timeout: ioTimeout, Transport: &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, ServerName: sni},
	}}
	resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET https on port %d: %v", port, err)
	}
	defer resp.Body.Close()
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		t.Fatalf("no peer certs from port %d", port)
	}
	return resp.TLS.PeerCertificates[0].DNSNames
}

func TestAPIAutoCert(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	base := fmt.Sprintf("/server/listeners/%d", port)
	call(t, "POST", "/server/listeners/add", addJSON(port, "https1", true, `,"commonName":"api.test"`))
	rr := call(t, "POST", base+"/cert/auto/auto.test?spiffeID=spiffe://goto/auto", "")
	expectStatus(t, rr, 200, fmt.Sprintf("Cert auto-generated for listener %d", port))
	if got := servedCertNames(t, port); len(got) == 0 {
		t.Error("the listener serves no cert after the auto-cert")
	}
	// The spiffe ID is required by the route.
	if rr := call(t, "POST", base+"/cert/auto/auto.test", ""); rr.Code == 200 {
		t.Errorf("cert/auto without a spiffeID = %d, want the route not to match", rr.Code)
	}
}

func TestAPIAutoSNI(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	base := fmt.Sprintf("/server/listeners/%d", port)
	call(t, "POST", "/server/listeners/add", addJSON(port, "https1", true, `,"commonName":"api.test"`))
	expectStatus(t, call(t, "POST", base+"/cert/autosni", ""), 200, fmt.Sprintf("Listener [%d] configured to auto-generate cert for any SNI", port))
	for _, sni := range []string{"one.test", "two.test"} {
		if got := servedCertNamesFor(t, port, sni); !contains(got, sni) {
			t.Errorf("cert for SNI %s has names %v, want one for the SNI", sni, got)
		}
	}
}

func TestAPIListenerCACerts(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	base := fmt.Sprintf("/server/listeners/%d", port)
	call(t, "POST", "/server/listeners/add", addJSON(port, "https1", true, `,"commonName":"api.test"`))
	ca, _ := certAndKey(t, "ca.test")
	expectStatus(t, call(t, "POST", base+"/ca/add", string(ca)), 200, fmt.Sprintf("CA Cert added for listener %d, and reopened", port))
	if l := GetListenerForPort(port); l.CACerts == nil || len(l.CACerts.Subjects()) != 1 { //nolint:staticcheck
		t.Errorf("CA certs = %v, want a pool with the CA that was added", l.CACerts)
	}
	expectStatus(t, call(t, "POST", base+"/ca/add", ""), 400, "No payload")
	expectStatus(t, call(t, "POST", base+"/ca/clear", ""), 200, fmt.Sprintf("CA Certs cleared for listener %d, and reopened", port))
	if l := GetListenerForPort(port); l.CACerts == nil || len(l.CACerts.Subjects()) != 0 { //nolint:staticcheck
		t.Errorf("CA certs after clear = %v, want an empty pool", l.CACerts)
	}
	if !isListening(port) {
		t.Error("the listener is not open after the CA certs changed")
	}
}

func TestAPIClientCerts(t *testing.T) {
	l := addAndOpen(t, "tls")
	base := fmt.Sprintf("/server/listeners/%d/client/certs", l.Port)
	l.StoreSNI("9.9.9.9:9", "seen.test", "")

	one := call(t, "GET", base, "")
	var certs map[string]map[string]map[string]any
	if one.Code != 200 || yaml.Unmarshal(one.Body.Bytes(), &certs) != nil || certs[fmt.Sprint(l.Port)]["9.9.9.9:9"]["sni"] != "seen.test" {
		t.Errorf("GET client certs of the port = %d %q, want the peer that was seen", one.Code, one.Body.String())
	}
	if len(certs) != 1 {
		t.Errorf("client certs of the port cover %d ports, want just this one", len(certs))
	}
	all := call(t, "GET", "/server/listeners/client/certs", "")
	if all.Code != 200 || !strings.Contains(all.Body.String(), "seen.test") {
		t.Errorf("GET client certs = %d %q, want the peer that was seen", all.Code, all.Body.String())
	}

	if rr := call(t, "POST", base+"/clear", ""); rr.Code != 200 {
		t.Errorf("clear = %d", rr.Code)
	}
	if got := GetPeerCerts(l.Port)[l.Port]; len(got) != 0 {
		t.Errorf("peers after clear = %v, want none", got)
	}
	l.StoreSNI("9.9.9.9:9", "seen.test", "")
	if rr := call(t, "POST", "/server/listeners/client/certs/clear", ""); rr.Code != 200 {
		t.Errorf("clear all = %d", rr.Code)
	}
	if got := GetPeerCerts(l.Port)[l.Port]; len(got) != 0 {
		t.Errorf("peers after clearing all = %v, want none", got)
	}
}

// A listener that is added through the API can be a TCP one that is not given a protocol, and echoes.
func TestAPIAddTCPListenerByDefault(t *testing.T) {
	port := freePort(t)
	forget(t, port)
	expectStatus(t, call(t, "POST", "/server/listeners/add", fmt.Sprintf(`{"port":%d,"open":true}`, port)), 200, "added and opened")
	l := GetListenerForPort(port)
	if l == nil || !l.IsTCP || l.TCP == nil {
		t.Fatalf("listener = %+v, want a TCP listener with a TCP config", l)
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), dialTimeout)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// Closing is seen by the clients.
	call(t, "POST", fmt.Sprintf("/server/listeners/%d/close", port), "")
	deadline := time.Now().Add(ioTimeout)
	for isListening(port) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if isListening(port) {
		t.Error("the port still accepts connections after the listener was closed")
	}
}
