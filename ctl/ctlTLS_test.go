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

package ctl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	gototls "goto/pkg/tls"
)

// certAndKey makes a self-signed certificate and its private key, as PEM text.
func certAndKey(t *testing.T, name string) (cert, key string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	key = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return
}

// forgetCert takes what the test added under the name out of the certs of the process.
func forgetCert(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		gototls.RemoveCert(name)
		gototls.RemoveKey(name)
	})
}

func hasCerts(name string) bool {
	certs, _ := gototls.GetCerts(name)
	return len(certs) > 0
}

func TestProcessTLSWithNothingToConfigure(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processTLS", func() {
		processTLS(nil)
		processTLS(&TLSConfigs{})
		processTLS(&TLSConfigs{Certs: []*CertConfig{}, CACerts: []*CACertConfig{}})
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestProcessTLSSendsTheCertAndKeyOfAListenerToTheRemote(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	cert, key := certAndKey(t, "remote.example")
	dir := t.TempDir()
	certPath := writeFile(t, dir, "tls.crt", cert)
	keyPath := writeFile(t, dir, "tls.key", key)
	within(t, "processTLS", func() {
		processTLS(&TLSConfigs{Certs: []*CertConfig{{Port: 8443, Cert: certPath, Key: keyPath}}})
	})
	got := r.got()
	if len(got) != 3 {
		t.Fatalf("requests = %+v, want the cert, the key, and the reopening of the listener", got)
	}
	for i, want := range []struct{ path, body string }{
		{"/server/listeners/8443/cert/add", cert},
		{"/server/listeners/8443/key/add", key},
		{"/server/listeners/8443/reopen", ""},
	} {
		if got[i].Method != "POST" || got[i].Path != want.path || got[i].Body != want.body {
			t.Errorf("request %d = %s %s with %d bytes, want a POST to %s with %d bytes", i, got[i].Method, got[i].Path, len(got[i].Body), want.path, len(want.body))
		}
	}
	if got[0].ContentType != "application/octet-stream" {
		t.Errorf("content type = %q", got[0].ContentType)
	}
}

func TestLoadCertsSkipsConfigsThatAreNotValid(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	cert, key := certAndKey(t, "x")
	dir := t.TempDir()
	certPath := writeFile(t, dir, "tls.crt", cert)
	keyPath := writeFile(t, dir, "tls.key", key)
	tls := &TLSConfigs{Certs: []*CertConfig{
		{Cert: certPath, Key: keyPath},                          // Neither a port nor a name.
		{Port: 9000},                                            // Nothing to load.
		{Port: 9001, Cert: certPath},                            // A cert without its key.
		{Name: "named-without-key", Key: keyPath},               // A key without its cert.
		{Port: 9002, Cert: certPath + ".missing", Key: keyPath}, // A cert file that is not there.
		{Port: 9003, Cert: certPath, Key: keyPath + ".missing"}, // A key file that is not there.
	}}
	within(t, "LoadCerts", func() { tls.LoadCerts(true) })
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %+v, want none for configs that are not valid", got)
	}
	within(t, "LoadCerts", func() { (&TLSConfigs{}).LoadCerts(true) })
}

func TestLoadFromPathStoresTheCertAndKeyOfAName(t *testing.T) {
	forgetCert(t, "ctl-named-cert")
	cert, key := certAndKey(t, "named.example")
	dir := t.TempDir()
	cc := &CertConfig{Name: "ctl-named-cert", Cert: writeFile(t, dir, "c.crt", cert), Key: writeFile(t, dir, "c.key", key)}
	if hasCerts("ctl-named-cert") {
		t.Fatal("the name has a cert before it was loaded")
	}
	within(t, "LoadFromPath", func() { cc.LoadFromPath(false) })
	certs, err := gototls.GetCerts("ctl-named-cert")
	if err != nil || len(certs) != 1 {
		t.Fatalf("certs = %v, %v, want the one that was loaded", certs, err)
	}
	// The same through the loading of all the certs.
	forgetCert(t, "ctl-named-cert-2")
	within(t, "LoadCerts", func() {
		(&TLSConfigs{Certs: []*CertConfig{{Name: "ctl-named-cert-2", Cert: cc.Cert, Key: cc.Key}}}).LoadCerts(false)
	})
	if !hasCerts("ctl-named-cert-2") {
		t.Error("the cert was not loaded by LoadCerts")
	}
}

func TestLoadFromPathOfFilesThatAreNotThereStoresNothing(t *testing.T) {
	forgetCert(t, "ctl-missing-files")
	dir := t.TempDir()
	within(t, "LoadFromPath", func() {
		(&CertConfig{Name: "ctl-missing-files", Cert: filepath.Join(dir, "no.crt"), Key: filepath.Join(dir, "no.key")}).LoadFromPath(false)
	})
	if hasCerts("ctl-missing-files") {
		t.Error("a cert was stored for files that are not there")
	}
}

func TestLoadFromPathForAPortWithoutAListenerStoresNothing(t *testing.T) {
	cert, key := certAndKey(t, "x")
	dir := t.TempDir()
	cc := &CertConfig{Port: 47399, Cert: writeFile(t, dir, "c.crt", cert), Key: writeFile(t, dir, "c.key", key)}
	within(t, "LoadFromPath", func() { cc.LoadFromPath(false) })
}

func TestSendCertOrKeyWhenTheRemoteFailsOrIsGone(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusInternalServerError)
	useRemote(t, r.URL)
	cc := &CertConfig{Port: 8443}
	within(t, "sendCertOrKey", func() {
		cc.sendCertOrKey("cert", []byte("data"))
		cc.sendCertOrKey("key", nil)
		reopenListener(8443)
	})
	if got := r.got(); len(got) != 3 {
		t.Errorf("requests = %d, want 3", len(got))
	}
	url := r.URL
	r.Close()
	useRemote(t, url)
	within(t, "sendCertOrKey", func() {
		cc.sendCertOrKey("cert", []byte("data"))
		reopenListener(8443)
	})
}

func TestSendCertOrKeyOfASpiffeIDGoesToTheAutoCertAPI(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	cc := &CertConfig{Port: 8443, Domain: "goto.goto", SpiffeID: "spiffe://goto/a"}
	within(t, "sendCertOrKey", func() { cc.sendCertOrKey("cert", []byte("data")) })
	got := r.got()
	if len(got) != 1 || got[0].Path != "/server/listeners/8443/cert/auto/goto.goto" || got[0].RawQuery != "spiffeID=spiffe://goto/a" {
		t.Errorf("requests = %+v", got)
	}
}

func TestLoadAutoCert(t *testing.T) {
	forgetCert(t, "ctl-auto-cert")
	cc := &CertConfig{Name: "ctl-auto-cert", Domain: "auto.example", SAN: []string{"alt.example"}}
	within(t, "LoadAutoCert", func() {
		cc.LoadAutoCert(true)
	})
	if hasCerts("ctl-auto-cert") {
		t.Fatal("a remote auto cert was made in this process")
	}
	within(t, "LoadAutoCert", func() { cc.LoadAutoCert(false) })
	if !hasCerts("ctl-auto-cert") {
		t.Error("the auto cert was not made")
	}
	// Without a listener there is nothing to apply it to.
	within(t, "LoadAutoCert", func() {
		(&CertConfig{Port: 47398, Domain: "x"}).LoadAutoCert(false)
		(&CertConfig{Port: 47398, Domain: "x"}).LoadAutoCert(true)
	})
}

func TestLoadCertsWithAutoCertGoesThroughLoadAutoCert(t *testing.T) {
	forgetCert(t, "ctl-auto-via-load")
	within(t, "LoadCerts", func() {
		(&TLSConfigs{Certs: []*CertConfig{{Name: "ctl-auto-via-load", Domain: "x.example", AutoCert: true}}}).LoadCerts(false)
	})
	if !hasCerts("ctl-auto-via-load") {
		t.Error("the auto cert was not made")
	}
}

func TestLoadSpiffeCertWithoutAListener(t *testing.T) {
	within(t, "LoadSpiffeCert", func() {
		(&CertConfig{Port: 47397, Domain: "x", SpiffeID: "spiffe://x"}).LoadSpiffeCert(false)
		(&CertConfig{Port: 47397, Domain: "x", SpiffeID: "spiffe://x"}).LoadSpiffeCert(true)
		(&CertConfig{Name: "ctl-no-spiffe-cert", Domain: "x", SpiffeID: "spiffe://x"}).LoadSpiffeCert(false)
	})
}

func TestLoadCACerts(t *testing.T) {
	t.Cleanup(func() {
		gototls.RemoveCACert("ctl-ca")
		gototls.RemoveCAKey("ctl-ca")
	})
	cert, key := certAndKey(t, "ca.example")
	tls := &TLSConfigs{CACerts: []*CACertConfig{
		{Name: "", Domain: "no-name", Key: key, Cert: cert},
		{Name: "no-domain", Domain: "", Key: key, Cert: cert},
		{Name: "ctl-ca", Domain: "ca.example", Key: key, Cert: cert},
	}}
	within(t, "LoadCACerts", func() { tls.LoadCACerts(true) })
	if gototls.CACerts["ctl-ca"] != nil {
		t.Fatal("a CA was stored in this process for a config that is for the remote")
	}
	within(t, "LoadCACerts", func() { tls.LoadCACerts(false) })
	if gototls.CACerts["ctl-ca"] == nil || !gototls.CACerts["ctl-ca"].Left["ca.example"] {
		t.Errorf("CA certs = %v, want the one that is valid", gototls.CACerts)
	}
	if gototls.CACerts["no-domain"] != nil || gototls.CAKeys["no-domain"] != nil {
		t.Error("a CA without a domain was stored")
	}
	within(t, "LoadCACerts", func() { (&TLSConfigs{}).LoadCACerts(false) })
}

func TestCACertConfigLoadSendsTheCertAndTheKey(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	cc := &CACertConfig{Name: "myca", Domain: "goto.goto", Cert: "CERT-DATA", Key: "KEY-DATA"}
	within(t, "load", cc.load)
	got := r.got()
	if len(got) != 2 {
		t.Fatalf("requests = %+v, want the cert and the key", got)
	}
	for _, req := range got {
		if req.Method != "POST" || req.ContentType != "application/octet-stream" {
			t.Errorf("request = %+v", req)
		}
	}
	if got[0].Body != "CERT-DATA" || got[1].Body != "KEY-DATA" {
		t.Errorf("bodies = %q, %q, want the cert and then the key", got[0].Body, got[1].Body)
	}
}

func TestCACertConfigLoadSkipsWhatIsNotComplete(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "load", func() {
		(&CACertConfig{Cert: "c", Key: "k"}).load()
		(&CACertConfig{Name: "n", Domain: "d", Cert: "c"}).load()
		(&CACertConfig{Name: "n", Domain: "d", Key: "k"}).load()
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %+v, want none", got)
	}
}
