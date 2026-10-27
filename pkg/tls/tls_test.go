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
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
)

var stubOnce sync.Once

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// isolate gives the test a private copy of all the package level state, and the cert path and work dir of the server
// config that point to temp dirs. Everything is restored when the test ends.
func isolate(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	lock.Lock()
	oldCACerts, oldCAKeys, oldRawCerts, oldRawKeys, oldX509 := CACerts, CAKeys, rawCerts, rawKeys, X509Certs
	oldRoots, oldSpiffe, oldKey, oldJWKS := RootCAs, Spiffe, DefaultCAKey, DefaultCAJWKS
	CACerts = map[string]*types.Pair[map[string]bool, []byte]{}
	CAKeys = map[string]*types.Pair[map[string]bool, []byte]{}
	rawCerts = map[string][]byte{}
	rawKeys = map[string][]byte{}
	X509Certs = map[string][]*tls.Certificate{}
	RootCAs = x509.NewCertPool()
	Spiffe = &SpiffeCerts{}
	lock.Unlock()
	oldCertPath, oldWorkDir := global.ServerConfig.CertPath, global.ServerConfig.WorkDir
	global.ServerConfig.CertPath = t.TempDir()
	global.ServerConfig.WorkDir = t.TempDir()
	t.Cleanup(func() {
		global.ServerConfig.CertPath, global.ServerConfig.WorkDir = oldCertPath, oldWorkDir
		lock.Lock()
		defer lock.Unlock()
		CACerts, CAKeys, rawCerts, rawKeys, X509Certs = oldCACerts, oldCAKeys, oldRawCerts, oldRawKeys, oldX509
		RootCAs, Spiffe, DefaultCAKey, DefaultCAJWKS = oldRoots, oldSpiffe, oldKey, oldJWKS
	})
}

// errList collects the failures of the goroutines of a test, which report them from the goroutine of the test.
type errList struct {
	mu   sync.Mutex
	msgs []string
}

func (e *errList) addf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.msgs = append(e.msgs, fmt.Sprintf(format, args...))
}

func (e *errList) report(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.msgs {
		t.Error(m)
	}
}

type pemPair struct {
	cert, key []byte
}

var (
	rsaCAOnce sync.Once
	rsaCA     pemPair
)

// newCA creates a self signed CA certificate for the domain, and returns it with its key, both PEM encoded.
func newCA(t *testing.T, domain string, useRSA bool) pemPair {
	t.Helper()
	c, err := CreateCertificateWithCA(nil, nil, []string{domain}, "", "", useRSA)
	if err != nil {
		t.Fatalf("creating the CA: %v", err)
	}
	return encodePair(t, c)
}

// sharedRSACA is an RSA CA that is expensive to create, so it's created once. It is only ever read.
func sharedRSACA(t *testing.T) pemPair {
	t.Helper()
	rsaCAOnce.Do(func() { rsaCA = newCA(t, "rsa.ca.test", true) })
	if rsaCA.cert == nil {
		t.Fatal("shared RSA CA is not available")
	}
	return rsaCA
}

func encodePair(t *testing.T, c *tls.Certificate) pemPair {
	t.Helper()
	certPEM, err := EncodeX509Cert(c)
	if err != nil {
		t.Fatalf("encoding cert: %v", err)
	}
	keyPEM, err := EncodeX509Key(c)
	if err != nil {
		t.Fatalf("encoding key: %v", err)
	}
	return pemPair{cert: certPEM, key: keyPEM}
}

func parseLeaf(t *testing.T, c *tls.Certificate) *x509.Certificate {
	t.Helper()
	if c == nil || len(c.Certificate) == 0 {
		t.Fatalf("certificate has no data: %+v", c)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatalf("parsing the certificate: %v", err)
	}
	return leaf
}

func poolOf(t *testing.T, certPEM []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("no certificate in PEM")
	}
	return pool
}

func TestCreateCertificateSelfSigned(t *testing.T) {
	isolate(t)
	c, err := CreateCertificate([]string{"a.test", "b.test"}, "spiffe://cluster.local/ns/goto/sa/a", "")
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseLeaf(t, c)
	if len(c.Certificate) != 1 {
		t.Errorf("chain length = %d, want 1 for a self signed cert", len(c.Certificate))
	}
	if !leaf.IsCA || leaf.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("self signed cert should be a CA: IsCA=%v KeyUsage=%v", leaf.IsCA, leaf.KeyUsage)
	}
	if leaf.Subject.CommonName != "a.test" {
		t.Errorf("CN = %q, want a.test", leaf.Subject.CommonName)
	}
	if got := strings.Join(leaf.DNSNames, ","); got != "a.test,b.test" {
		t.Errorf("DNS names = %q", got)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != "spiffe://cluster.local/ns/goto/sa/a" {
		t.Errorf("URIs = %v", leaf.URIs)
	}
	if _, ok := c.PrivateKey.(*ecdsa.PrivateKey); !ok {
		t.Errorf("default key type = %T, want ECDSA", c.PrivateKey)
	}
	if !leaf.NotAfter.After(time.Now().Add(300 * 24 * time.Hour)) {
		t.Errorf("cert valid until %v, want about a year", leaf.NotAfter)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: poolOf(t, mustEncodeCert(t, c)), DNSName: "b.test"}); err != nil {
		t.Errorf("self signed cert doesn't verify against itself: %v", err)
	}
}

func mustEncodeCert(t *testing.T, c *tls.Certificate) []byte {
	t.Helper()
	b, err := EncodeX509Cert(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateCertificateRSA(t *testing.T) {
	isolate(t)
	ca := sharedRSACA(t)
	block, _ := pem.Decode(ca.cert)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.PublicKeyAlgorithm != x509.RSA {
		t.Errorf("public key algorithm = %v, want RSA", leaf.PublicKeyAlgorithm)
	}
	k, err := parsePrivateKeyPEM(ca.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := k.(*rsa.PrivateKey); !ok {
		t.Errorf("key = %T, want RSA", k)
	}
}

func TestCreateCertificateWithoutDomains(t *testing.T) {
	isolate(t)
	if c, err := CreateCertificate(nil, "", ""); err == nil || c != nil {
		t.Errorf("CreateCertificate(nil) = %v, %v, want an error", c, err)
	}
}

func TestCreateCertificateInvalidSpiffeID(t *testing.T) {
	isolate(t)
	if c, err := CreateCertificate([]string{"a.test"}, "%zz", ""); err == nil || c != nil {
		t.Errorf("CreateCertificate with a bad URL = %v, %v, want an error", c, err)
	}
}

func TestCreateCertificateSignedByCA(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	c, err := CreateCertificateWithCA(ca.cert, ca.key, []string{"leaf.test"}, "spiffe://x/y", "", false)
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseLeaf(t, c)
	if leaf.IsCA {
		t.Error("a cert signed by a CA must not be a CA itself")
	}
	if len(c.Certificate) != 2 {
		t.Errorf("chain length = %d, want leaf + CA", len(c.Certificate))
	}
	caBlock, _ := pem.Decode(ca.cert)
	caCert, _ := x509.ParseCertificate(caBlock.Bytes)
	if !bytes.Equal(leaf.AuthorityKeyId, caCert.SubjectKeyId) {
		t.Errorf("authority key id = %x, want the CA subject key id %x", leaf.AuthorityKeyId, caCert.SubjectKeyId)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: poolOf(t, ca.cert), DNSName: "leaf.test"}); err != nil {
		t.Errorf("leaf doesn't verify against its CA: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: x509.NewCertPool(), DNSName: "leaf.test"}); err == nil {
		t.Error("leaf must not verify without its CA")
	}
}

func TestCreateCertificateWithCABadInputs(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	if _, err := CreateCertificateWithCA(ca.cert, []byte("not a key"), []string{"x.test"}, "", "", false); err == nil {
		t.Error("expected an error for a CA key that isn't PEM")
	}
	if _, err := CreateCertificateWithCA([]byte("not a cert"), ca.key, []string{"x.test"}, "", "", false); err == nil {
		t.Error("expected an error for a CA cert that isn't PEM")
	}
	junk := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("junk")})
	if _, err := CreateCertificateWithCA(junk, ca.key, []string{"x.test"}, "", "", false); err == nil {
		t.Error("expected an error for a CA cert that isn't DER")
	}
}

func TestCreateCertificateChainOfCAs(t *testing.T) {
	isolate(t)
	root := newCA(t, "root.test", false)
	inter, err := CreateCertificateWithCA(root.cert, root.key, []string{"inter.test"}, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	interPair := encodePair(t, inter)
	leaf, err := CreateCertificateWithCA(interPair.cert, interPair.key, []string{"leaf.test"}, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.Certificate) != 3 {
		t.Errorf("chain length = %d, want leaf + intermediate + root", len(leaf.Certificate))
	}
}

func TestCreateCertificatePicksCAByDomain(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	AddCAKey("myca", "served.test", ca.key)
	AddCACert("myca", "served.test", ca.cert)

	signed, err := CreateCertificate([]string{"served.test", "alt.test"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(signed.Certificate) != 2 || parseLeaf(t, signed).IsCA {
		t.Errorf("a cert for a domain of the CA should be signed by it. chain: %d", len(signed.Certificate))
	}
	if _, err := parseLeaf(t, signed).Verify(x509.VerifyOptions{Roots: poolOf(t, ca.cert), DNSName: "alt.test"}); err != nil {
		t.Errorf("signed cert doesn't verify: %v", err)
	}

	self, err := CreateCertificate([]string{"other.test"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(self.Certificate) != 1 || !parseLeaf(t, self).IsCA {
		t.Errorf("a cert for a domain without a CA should be self signed. chain: %d", len(self.Certificate))
	}
}

func TestAddGetRemoveCerts(t *testing.T) {
	isolate(t)
	c, err := CreateCertificate([]string{"named.test"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p := encodePair(t, c)

	if certs, _ := GetCerts("named"); len(certs) != 0 {
		t.Fatalf("GetCerts before the upload = %d certs, want 0", len(certs))
	}
	AddCert("named", p.cert)
	if certs, _ := GetCerts("named"); len(certs) != 0 {
		t.Fatalf("GetCerts with a cert but no key = %d certs, want 0", len(certs))
	}
	AddKey("named", p.key)
	certs, _ := GetCerts("named")
	if len(certs) != 1 || !bytes.Equal(certs[0].Certificate[0], c.Certificate[0]) {
		t.Fatalf("GetCerts = %v", certs)
	}
	again, _ := GetCerts("named")
	if len(again) != 1 || again[0] != certs[0] {
		t.Error("a parsed cert should be cached and returned again")
	}
	RemoveCert("named")
	if cached, _ := GetCerts("named"); len(cached) != 0 {
		t.Errorf("a removed cert must not be served: %d certs", len(cached))
	}
	AddCert("named", p.cert)
	if cached, _ := GetCerts("named"); len(cached) != 1 {
		t.Errorf("cert uploaded again should be usable: %d certs", len(cached))
	}
	RemoveKey("named")
	if cached, _ := GetCerts("named"); len(cached) != 0 {
		t.Errorf("a cert whose key was removed must not be served: %d certs", len(cached))
	}
}

func TestGetCertsWithMismatchedKey(t *testing.T) {
	isolate(t)
	a, _ := CreateCertificate([]string{"a.test"}, "", "")
	b, _ := CreateCertificate([]string{"b.test"}, "", "")
	AddCert("mixed", encodePair(t, a).cert)
	AddKey("mixed", encodePair(t, b).key)
	if certs, _ := GetCerts("mixed"); len(certs) != 0 {
		t.Errorf("a cert with another cert's key must not be returned: %v", certs)
	}
}

func TestAddCACertAndKey(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	AddCACert("ca1", "d1.test", ca.cert)
	AddCACert("ca1", "d2.test", ca.cert)
	AddCAKey("ca1", "d1.test", ca.key)

	lock.RLock()
	certPair, keyPair := CACerts["ca1"], CAKeys["ca1"]
	lock.RUnlock()
	if certPair == nil || !certPair.Left["d1.test"] || !certPair.Left["d2.test"] || !bytes.Equal(certPair.Right, ca.cert) {
		t.Errorf("CA cert registry = %+v", certPair)
	}
	if keyPair == nil || !keyPair.Left["d1.test"] || keyPair.Left["d2.test"] || !bytes.Equal(keyPair.Right, ca.key) {
		t.Errorf("CA key registry = %+v", keyPair)
	}

	lock.RLock()
	roots := RootCAs
	lock.RUnlock()
	leaf, _ := CreateCertificateWithCA(ca.cert, ca.key, []string{"x.test"}, "", "", false)
	if _, err := parseLeaf(t, leaf).Verify(x509.VerifyOptions{Roots: roots, DNSName: "x.test"}); err != nil {
		t.Errorf("a CA cert added must become a root CA: %v", err)
	}

	RemoveCACert("ca1")
	RemoveCAKey("ca1")
	lock.RLock()
	defer lock.RUnlock()
	if CACerts["ca1"] != nil || CAKeys["ca1"] != nil {
		t.Error("CA cert and key should have been removed")
	}
	if _, err := parseLeaf(t, leaf).Verify(x509.VerifyOptions{Roots: RootCAs, DNSName: "x.test"}); err == nil {
		t.Error("a removed CA cert must no longer be a root CA")
	}
}

func TestAddCACertAcceptsBase64(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	AddCACert("b64", "d.test", []byte(base64.StdEncoding.EncodeToString(ca.cert)))
	AddCAKey("b64", "d.test", []byte(base64.StdEncoding.EncodeToString(ca.key)))
	lock.RLock()
	defer lock.RUnlock()
	if !bytes.Equal(CACerts["b64"].Right, ca.cert) {
		t.Errorf("base64 cert wasn't decoded: %q", CACerts["b64"].Right)
	}
	if !bytes.Equal(CAKeys["b64"].Right, ca.key) {
		t.Errorf("base64 key wasn't decoded: %q", CAKeys["b64"].Right)
	}
}

func TestLoadCertsReadsCertPath(t *testing.T) {
	isolate(t)
	ca := newCA(t, "file.ca.test", false)
	if err := os.WriteFile(filepath.Join(global.ServerConfig.CertPath, "a.crt"), ca.cert, 0600); err != nil {
		t.Fatal(err)
	}
	other := newCA(t, "pem.ca.test", false)
	if err := os.WriteFile(filepath.Join(global.ServerConfig.CertPath, "b.pem"), other.cert, 0600); err != nil {
		t.Fatal(err)
	}
	AddCACert("unrelated", "x.test", newCA(t, "x.test", false).cert)
	lock.RLock()
	roots := RootCAs
	lock.RUnlock()
	for _, p := range []pemPair{ca, other} {
		leaf, _ := CreateCertificateWithCA(p.cert, p.key, []string{"l.test"}, "", "", false)
		if _, err := parseLeaf(t, leaf).Verify(x509.VerifyOptions{Roots: roots, DNSName: "l.test"}); err != nil {
			t.Errorf("CA from the cert path should be a root CA: %v", err)
		}
	}
}

func TestSetDefaultCA(t *testing.T) {
	isolate(t)
	ca := sharedRSACA(t)
	AddCAKey("rsa", "rsa.ca.test", ca.key)
	AddCACert("rsa", "rsa.ca.test", ca.cert)

	lock.RLock()
	before := DefaultCAKey
	lock.RUnlock()
	SetDefaultCA("nosuch")
	lock.RLock()
	if DefaultCAKey != before {
		t.Error("an unknown CA name must not change the default CA")
	}
	lock.RUnlock()

	SetDefaultCA("rsa")
	lock.RLock()
	defer lock.RUnlock()
	want, _ := parsePrivateKeyPEM(ca.key)
	if DefaultCAKey == nil || !DefaultCAKey.Equal(want) {
		t.Fatal("default CA key wasn't set")
	}
	if DefaultCAJWKS == nil || len(DefaultCAJWKS.Keys) != 1 {
		t.Fatalf("default JWKS = %+v", DefaultCAJWKS)
	}
	k := DefaultCAJWKS.Keys[0]
	pub, err := jwkToRSAPublicKey(k)
	if err != nil || !pub.Equal(&DefaultCAKey.PublicKey) {
		t.Errorf("JWK doesn't describe the default CA key: %v", err)
	}
	if len(k.X5c) != 1 || k.X5t == "" || k.Kid != k.X5t {
		t.Errorf("JWK should carry the CA cert: %+v", k)
	}
}

func TestSetDefaultCAIgnoresNonRSAKeys(t *testing.T) {
	isolate(t)
	ec := newCA(t, "ec.test", false)
	AddCAKey("ec", "ec.test", ec.key)
	AddCAKey("junk", "junk.test", []byte("not a key"))
	lock.RLock()
	before, beforeJWKS := DefaultCAKey, DefaultCAJWKS
	lock.RUnlock()
	SetDefaultCA("ec")
	SetDefaultCA("junk")
	lock.RLock()
	defer lock.RUnlock()
	if DefaultCAKey != before || DefaultCAJWKS != beforeJWKS {
		t.Error("only a parseable RSA key can become the default CA")
	}
}

func TestDefaultCAKeysAtStartup(t *testing.T) {
	jwks := DefaultCAJWKS
	if DefaultCAKey == nil || jwks == nil || len(jwks.Keys) != 1 {
		t.Fatalf("default CA wasn't initialized: %v %+v", DefaultCAKey, jwks)
	}
	k := jwks.Keys[0]
	if k.Kty != "RSA" || k.Use != "sig" || k.Alg != "RS256" || len(k.X5c) != 1 {
		t.Errorf("JWK = %+v", k)
	}
	pub, err := jwkToRSAPublicKey(k)
	if err != nil || !pub.Equal(&DefaultCAKey.PublicKey) {
		t.Errorf("JWK public key doesn't match the default key: %v", err)
	}
}

func TestBuildRSAJWKSWithoutCert(t *testing.T) {
	ca := sharedRSACA(t)
	k, _ := parsePrivateKeyPEM(ca.key)
	jwks := buildRSAJWKS(k.(*rsa.PrivateKey), nil)
	if len(jwks.Keys) != 1 || len(jwks.Keys[0].X5c) != 0 || jwks.Keys[0].X5t != "" || jwks.Keys[0].Kid == "" {
		t.Errorf("JWKS without a cert = %+v", jwks)
	}
	if jwks.Keys[0].E != "AQAB" {
		t.Errorf("E = %q, want AQAB for 65537", jwks.Keys[0].E)
	}
}

func TestAddAutoCert(t *testing.T) {
	isolate(t)
	if err := AddAutoCert("auto", "auto.test", []string{"alt.test"}, "spiffe://a/b"); err != nil {
		t.Fatal(err)
	}
	certs, _ := GetCerts("auto")
	if len(certs) != 1 {
		t.Fatalf("GetCerts = %d certs", len(certs))
	}
	leaf := parseLeaf(t, certs[0])
	if strings.Join(leaf.DNSNames, ",") != "auto.test,alt.test" {
		t.Errorf("DNS names = %v", leaf.DNSNames)
	}
	if err := AddAutoCert("bad", "bad.test", nil, "%zz"); err == nil {
		t.Error("expected an error for a bad spiffe ID")
	}
	if certs, _ := GetCerts("bad"); len(certs) != 0 {
		t.Error("a failed auto cert must not be stored")
	}
}

func TestSpiffeCerts(t *testing.T) {
	s := &SpiffeCerts{}
	if !s.IsEmpty() {
		t.Error("new SpiffeCerts should be empty")
	}
	s.Add("n", "d", "spiffe://a/b")
	s.Add("n2", "d2", "spiffe://a/c")
	s.Add("n3", "d3", "spiffe://a/b")
	if s.IsEmpty() || len(s.ByIDNameDomain) != 2 {
		t.Fatalf("registry = %+v", s.ByIDNameDomain)
	}
	if p := s.ByIDNameDomain["spiffe://a/b"]; p.Left != "n3" || p.Right != "d3" {
		t.Errorf("the last registration of an ID wins, got %+v", p)
	}
}

func TestParsePrivateKeyPEM(t *testing.T) {
	ec := newCA(t, "ec.test", false)
	if k, err := parsePrivateKeyPEM(ec.key); err != nil {
		t.Errorf("PKCS8 EC key: %v", err)
	} else if _, ok := k.(*ecdsa.PrivateKey); !ok {
		t.Errorf("PKCS8 EC key parsed as %T", k)
	}
	rsaKey, _ := parsePrivateKeyPEM(sharedRSACA(t).key)
	pkcs1 := pem.EncodeToMemory(pemBlockForKey(rsaKey))
	if k, err := parsePrivateKeyPEM(pkcs1); err != nil {
		t.Errorf("PKCS1 RSA key: %v", err)
	} else if _, ok := k.(*rsa.PrivateKey); !ok {
		t.Errorf("PKCS1 key parsed as %T", k)
	}
	ecKey, _ := parsePrivateKeyPEM(ec.key)
	sec1 := pem.EncodeToMemory(pemBlockForKey(ecKey))
	if k, err := parsePrivateKeyPEM(sec1); err != nil {
		t.Errorf("SEC1 EC key: %v", err)
	} else if _, ok := k.(*ecdsa.PrivateKey); !ok {
		t.Errorf("SEC1 key parsed as %T", k)
	}
	if _, err := parsePrivateKeyPEM([]byte("garbage")); err == nil {
		t.Error("expected an error for non PEM data")
	}
	junk := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")})
	if _, err := parsePrivateKeyPEM(junk); err == nil {
		t.Error("expected an error for a PEM block that isn't a key")
	}
}

func TestPemBlockForKey(t *testing.T) {
	rsaKey, _ := parsePrivateKeyPEM(sharedRSACA(t).key)
	if b := pemBlockForKey(rsaKey); b == nil || b.Type != "RSA PRIVATE KEY" {
		t.Errorf("RSA block = %+v", b)
	}
	ecKey, _ := parsePrivateKeyPEM(newCA(t, "ec.test", false).key)
	if b := pemBlockForKey(ecKey); b == nil || b.Type != "EC PRIVATE KEY" {
		t.Errorf("EC block = %+v", b)
	}
	if b := pemBlockForKey("neither"); b != nil {
		t.Errorf("unsupported key type should give no block, got %+v", b)
	}
}

func TestEncodeX509(t *testing.T) {
	if b, err := EncodeX509Cert(nil); err == nil || b != nil {
		t.Errorf("EncodeX509Cert(nil) = %q, %v", b, err)
	}
	if b, err := EncodeX509Key(nil); err == nil || b != nil {
		t.Errorf("EncodeX509Key(nil) = %q, %v", b, err)
	}
	root := newCA(t, "root.test", false)
	c, _ := CreateCertificateWithCA(root.cert, root.key, []string{"leaf.test"}, "", "", false)
	certPEM, err := EncodeX509Cert(c)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(certPEM), "BEGIN CERTIFICATE"); n != 2 {
		t.Errorf("encoded %d certs, want the whole chain of 2", n)
	}
	keyPEM, err := EncodeX509Key(c)
	if err != nil || !strings.Contains(string(keyPEM), "BEGIN PRIVATE KEY") {
		t.Errorf("key PEM = %q, %v", keyPEM, err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Errorf("encoded cert and key don't form a key pair: %v", err)
	}
	if _, err := EncodeX509Key(&tls.Certificate{PrivateKey: "not a key"}); err == nil {
		t.Error("expected an error for a key that can't be marshalled")
	}
}

func TestGetTLSVersion(t *testing.T) {
	for v, want := range map[uint16]string{
		tls.VersionTLS13: "1.3", tls.VersionTLS12: "1.2", tls.VersionTLS11: "1.1", tls.VersionTLS10: "1.0", 0: "???",
	} {
		if got := GetTLSVersion(&tls.ConnectionState{Version: v}); got != want {
			t.Errorf("GetTLSVersion(%#x) = %q, want %q", v, got, want)
		}
	}
}

// clientHello captures the ClientHello that a real TLS client sends for the given server name and ALPN protocols.
func clientHello(t *testing.T, serverName string, protos ...string) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer server.Close()
	go func() {
		defer client.Close()
		tls.Client(client, &tls.Config{ServerName: serverName, InsecureSkipVerify: true, NextProtos: protos}).Handshake()
	}()
	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	head := make([]byte, 5)
	if _, err := readFull(server, head); err != nil {
		t.Fatalf("reading the record header: %v", err)
	}
	body := make([]byte, int(head[3])<<8|int(head[4]))
	if _, err := readFull(server, body); err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	return append(head, body...)
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// readerConn serves canned bytes through the net.Conn interface.
type readerConn struct {
	net.Conn
	r *bytes.Reader
}

func newReaderConn(b []byte) *readerConn { return &readerConn{r: bytes.NewReader(b)} }

func (c *readerConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func TestReadTLSSNIFromConn(t *testing.T) {
	hello := clientHello(t, "sni.example.test", "h2", "http/1.1")
	sni, buff, err := ReadTLSSNIFromConn(newReaderConn(hello))
	if err != nil {
		t.Fatal(err)
	}
	if sni != "sni.example.test" {
		t.Errorf("SNI = %q", sni)
	}
	if !bytes.Equal(buff.Bytes(), hello) {
		t.Errorf("the bytes consumed from the connection must be kept for replay: got %d bytes, want %d", buff.Len(), len(hello))
	}
}

func TestReadTLSSNIFromConnWithoutSNI(t *testing.T) {
	// A client doesn't send SNI for an IP address
	hello := clientHello(t, "127.0.0.1")
	sni, _, err := ReadTLSSNIFromConn(newReaderConn(hello))
	if err != nil || sni != "" {
		t.Errorf("SNI = %q, %v, want an empty SNI without an error", sni, err)
	}
}

func TestReadTLSSNIFromConnNotTLS(t *testing.T) {
	_, buff, err := ReadTLSSNIFromConn(newReaderConn([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")))
	if err == nil || !strings.Contains(err.Error(), "Not a TLS Handshake") {
		t.Errorf("err = %v, want 'Not a TLS Handshake'", err)
	}
	if buff.Len() == 0 {
		t.Error("the bytes that were read should be available to replay to the upstream")
	}
	if _, _, err := ReadTLSSNIFromConn(newReaderConn(nil)); err == nil {
		t.Error("expected an error for an empty connection")
	}
}

// Only a handshake record whose content is not a parsable hello is a malformed hello: what is not a TLS handshake, and
// what is cut short on the connection, are the errors of those, so that the callers can tell them apart.
func TestReadTLSSNIFromConnMalformedClientHello(t *testing.T) {
	hello := clientHello(t, "sni.example.test")
	for name, record := range map[string][]byte{
		"empty record":     {0x16, 0x03, 0x01, 0x00, 0x00},
		"not a hello":      reframe([]byte{0x02, 0x00, 0x00, 0x00}),
		"hello type only":  reframe([]byte{0x01}),
		"hello header cut": reframe([]byte{0x01, 0x00, 0x00}),
		"hello too short":  reframe([]byte{0x01, 0x00, 0x00, 0x04, 0x03, 0x03, 0x00, 0x00}),
	} {
		if _, _, err := ReadTLSSNIFromConn(newReaderConn(record)); !errors.Is(err, ErrMalformedClientHello) {
			t.Errorf("%s: err = %v, want a malformed client hello", name, err)
		}
	}
	for name, record := range map[string][]byte{
		"not TLS":                []byte("GET / HTTP/1.1\r\n\r\n"),
		"no data":                nil,
		"record header cut":      hello[:3],
		"record cut on the wire": hello[:len(hello)/2],
	} {
		if _, _, err := ReadTLSSNIFromConn(newReaderConn(record)); err == nil || errors.Is(err, ErrMalformedClientHello) {
			t.Errorf("%s: err = %v, want an error that is not a malformed client hello", name, err)
		}
	}
	for _, record := range [][]byte{hello, clientHello(t, "127.0.0.1")} {
		if _, _, err := ReadTLSSNIFromConn(newReaderConn(record)); err != nil {
			t.Errorf("a valid client hello gave %v", err)
		}
	}
}

func TestGetClientCipherSuites(t *testing.T) {
	// length 2 * 4 suites
	buff := []byte{0, 8, 0xc0, 0x2f, 0xcc, 0xa9, 0x13, 0x01, 0x00, 0xff}
	got, err := getClientCipherSuites(buff)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256", "psuedo-cipher-suite: renegotiation SCSV supported"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cipher suites = %v, want %v (TLS 1.3 suites aren't named)", got, want)
	}
	if _, err := getClientCipherSuites([]byte{0, 8, 1}); err == nil {
		t.Error("expected an error for truncated cipher suites")
	}
}

func TestGetSignatureAlgorithms(t *testing.T) {
	hello := clientHello(t, "sni.test")
	data := mustHelloExtensions(t, hello)
	algos, _ := getSignatureAlgorithms(data)
	if len(algos) == 0 {
		t.Error("a Go client always offers signature algorithms")
	}
	sig := []byte{0x00, 0x0d, 0x00, 0x08, 0x00, 0x06, 0x04, 0x01, 0x04, 0x03, 0x02, 0x01}
	algos, _ = getSignatureAlgorithms(sig)
	if strings.Join(algos, "|") != "RSA/SHA256|ECDSA/SHA256|RSA/SHA1" {
		t.Errorf("algorithms = %v", algos)
	}
}

func mustHelloExtensions(t *testing.T, hello []byte) []byte {
	t.Helper()
	body, err := getClientHelloData(hello[5:])
	if err != nil {
		t.Fatal(err)
	}
	ext, err := getClientHelloExtensions(body)
	if err != nil {
		t.Fatal(err)
	}
	return ext
}

func TestParseByteRecordOfSize(t *testing.T) {
	rec, rest, err := parseByteRecordOfSize([]byte{0, 3, 'a', 'b', 'c', 'x'}, 2, "t")
	if err != nil || string(rec) != "abc" || string(rest) != "x" {
		t.Errorf("= %q %q %v", rec, rest, err)
	}
	rec, rest, err = parseByteRecordOfSize([]byte{0, 0, 0}, 3, "t")
	if err != nil || len(rec) != 0 || len(rest) != 0 {
		t.Errorf("3 byte size = %q %q %v", rec, rest, err)
	}
	if _, _, err := parseByteRecordOfSize([]byte{0}, 2, "t"); err == nil {
		t.Error("expected an error when the size itself is cut")
	}
	if _, _, err := parseByteRecordOfSize([]byte{0, 9, 'a'}, 2, "t"); err == nil {
		t.Error("expected an error when the record is shorter than its size")
	}
}

func TestConcurrentCertState(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	errs := &errList{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				AddCAKey("c", "d.test", ca.key)
				AddCACert("c", "d.test", ca.cert)
				AddCert("n", ca.cert)
				AddKey("n", ca.key)
				RemoveCert("n")
				RemoveKey("n")
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := CreateCertificate([]string{"d.test"}, "", ""); err != nil {
					errs.addf("CreateCertificate: %v", err)
					return
				}
				GetCerts("n")
			}
		}()
	}
	wg.Wait()
	errs.report(t)
}
