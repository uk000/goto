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

package startup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"goto/ctl"
	gototls "goto/pkg/tls"
)

// selfSigned makes a certificate and its key, as the PEM that is read from files and configs.
func selfSigned(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{cn},
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return
}

// tlsConfigOf is the config of a file that has the given TLS configs.
func tlsConfigOf(name string, tls *ctl.TLSConfigs) *ctl.GotoConfig {
	return &ctl.GotoConfig{Filepath: "/configs/" + name, TLS: tls}
}

// forgetCA and forgetCert take the CAs and the named certs of a test out of the TLS store when the test ends.
func forgetCA(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, n := range names {
			gototls.RemoveCACert(n)
			gototls.RemoveCAKey(n)
			gototls.RemoveCert(n)
			gototls.RemoveKey(n)
		}
	})
}

func caKey(name string) string {
	if p := gototls.CAKeys[name]; p != nil {
		return string(p.Right)
	}
	return ""
}

func caCert(name string) string {
	if p := gototls.CACerts[name]; p != nil {
		return string(p.Right)
	}
	return ""
}

func namedCerts(name string) int {
	certs, _ := gototls.GetCerts(name)
	return len(certs)
}

func TestProcessTLSNothingToLoad(t *testing.T) {
	resetState(t)
	processTLS(nil)
	processTLS(&ctl.TLSConfigs{})
	clearTLS(nil)
	clearTLS(&ctl.TLSConfigs{})
	clearTLS(&ctl.TLSConfigs{Certs: []*ctl.CertConfig{nil}, CACerts: []*ctl.CACertConfig{nil}})
}

func TestProcessTLSLoadsCACerts(t *testing.T) {
	resetState(t)
	name := "startup-ca-load"
	forgetCA(t, name)
	cert, key := selfSigned(t, "ca.startup.test")
	loadConfig(tlsConfigOf("ca.yaml", &ctl.TLSConfigs{CACerts: []*ctl.CACertConfig{{Name: name, Domain: "startup.test", Cert: cert, Key: key}}}))

	if tlsConfigs["ca.yaml"] == nil {
		t.Error("the TLS config of the file is not kept")
	}
	if caCert(name) != cert || caKey(name) != key {
		t.Errorf("CA of the config: cert matches %t, key matches %t", caCert(name) == cert, caKey(name) == key)
	}
	if !gototls.CACerts[name].Left["startup.test"] {
		t.Errorf("CA is not for the domain of the config: %v", gototls.CACerts[name].Left)
	}
}

func TestProcessTLSSkipsCAWithoutNameOrDomain(t *testing.T) {
	resetState(t)
	forgetCA(t, "startup-ca-nodomain", "")
	cert, key := selfSigned(t, "x")
	before := len(gototls.CACerts)
	processTLS(&ctl.TLSConfigs{CACerts: []*ctl.CACertConfig{
		{Name: "startup-ca-nodomain", Cert: cert, Key: key},
		{Domain: "startup.test", Cert: cert, Key: key},
	}})
	if len(gototls.CACerts) != before || len(gototls.CAKeys) != 0 && gototls.CAKeys["startup-ca-nodomain"] != nil {
		t.Errorf("a CA without a name or a domain was stored: %v", keys(gototls.CACerts))
	}
}

func TestClearTLSRemovesCACertAndKey(t *testing.T) {
	resetState(t)
	name := "startup-ca-clear"
	forgetCA(t, name)
	cert, key := selfSigned(t, "ca.clear.test")
	tls := &ctl.TLSConfigs{CACerts: []*ctl.CACertConfig{{Name: name, Domain: "clear.test", Cert: cert, Key: key}}}
	processTLS(tls)
	clearTLS(tls)
	if gototls.CACerts[name] != nil || gototls.CAKeys[name] != nil {
		t.Errorf("CA is still stored after it was cleared: cert %t, key %t", gototls.CACerts[name] != nil, gototls.CAKeys[name] != nil)
	}
}

func TestProcessTLSLoadsNamedCertFromFiles(t *testing.T) {
	resetState(t)
	name := "startup-named-cert"
	forgetCA(t, name)
	dir := t.TempDir()
	cert, key := selfSigned(t, "named.test")
	cc := &ctl.CertConfig{Name: name, Cert: writeFile(t, dir, "tls.crt", cert), Key: writeFile(t, dir, "tls.key", key)}
	tls := &ctl.TLSConfigs{Certs: []*ctl.CertConfig{cc}}
	processTLS(tls)
	if namedCerts(name) != 1 {
		t.Fatalf("the cert of the files is not loaded by its name")
	}
	clearTLS(tls)
	if namedCerts(name) != 0 {
		t.Error("the named cert is still there after the config that loaded it was cleared")
	}
}

func TestProcessTLSSkipsCertsThatAreNotComplete(t *testing.T) {
	resetState(t)
	forgetCA(t, "startup-no-key", "startup-missing-files")
	dir := t.TempDir()
	cert, _ := selfSigned(t, "incomplete.test")
	processTLS(&ctl.TLSConfigs{Certs: []*ctl.CertConfig{
		{}, // no port and no name
		{Name: "startup-no-key", Cert: writeFile(t, dir, "only.crt", cert)}, // a cert without its key
		{Name: "startup-missing-files", Cert: dir + "/missing.crt", Key: dir + "/missing.key"},
		nil,
	}})
	if namedCerts("startup-no-key") != 0 || namedCerts("startup-missing-files") != 0 {
		t.Error("a cert that is not complete was loaded")
	}
}

func TestProcessTLSLoadsListenerCertAndClearRemovesIt(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	loadConfig(parse(t, "l.yaml", fmt.Sprintf("listeners:\n  %d:\n    protocol: tcp\n", port)))
	l := mustListener(t, port)
	dir := t.TempDir()
	cert, key := selfSigned(t, "listener.test")
	loadConfig(tlsConfigOf("t.yaml", &ctl.TLSConfigs{Certs: []*ctl.CertConfig{
		{Port: port, Cert: writeFile(t, dir, "l.crt", cert), Key: writeFile(t, dir, "l.key", key)},
	}}))
	if string(l.RawCert) != cert || string(l.RawKey) != key {
		t.Fatalf("the listener did not get the cert and key of the config")
	}
	removeConfigs("/configs/t.yaml")
	if len(l.RawCert) != 0 || len(l.RawKey) != 0 {
		t.Error("the listener still has the cert of the config that was removed")
	}
}

func TestLoadConfigTLSReloadReplacesCAWithItsKey(t *testing.T) {
	resetState(t)
	name := "startup-ca-reload"
	forgetCA(t, name)
	cert1, key1 := selfSigned(t, "first.test")
	cert2, key2 := selfSigned(t, "second.test")
	ca := func(cert, key string) *ctl.TLSConfigs {
		return &ctl.TLSConfigs{CACerts: []*ctl.CACertConfig{{Name: name, Domain: "reload.test", Cert: cert, Key: key}}}
	}
	loadConfig(tlsConfigOf("reload.yaml", ca(cert1, key1)))
	loadConfig(tlsConfigOf("reload.yaml", ca(cert2, key2)))
	if caCert(name) != cert2 {
		t.Error("the cert of the CA is not the one of the file as it is now")
	}
	if caKey(name) != key2 {
		t.Error("the key of the CA is not the one of the file as it is now: it is the key of the cert that was replaced")
	}
}

func TestLoadConfigTLSSectionOfWrongKindOfPEMDoesNotCrash(t *testing.T) {
	resetState(t)
	forgetCA(t, "startup-ca-garbage")
	loadConfig(tlsConfigOf("garbage.yaml", &ctl.TLSConfigs{CACerts: []*ctl.CACertConfig{{Name: "startup-ca-garbage", Domain: "d", Cert: "not a cert", Key: "not a key"}}}))
	if caCert("startup-ca-garbage") != "not a cert" {
		t.Error("the CA of the config is not kept as it was given")
	}
}
