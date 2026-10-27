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
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

var routesOnce sync.Once

// call sends a request to the admin API of the package, which lives on the router of /tls.
func call(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	routesOnce.Do(func() { Middleware.SetRoutes(mux.NewRouter()) })
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	middleware.RootRouters["/tls"].ServeHTTP(rr, req)
	return rr
}

func mustOK(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func TestAPIAddAndRemoveCertAndKey(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	if out := mustOK(t, call(t, "POST", "/tls/cert/add/web", string(ca.cert))); !strings.Contains(out, "Cert stored for name [web]") {
		t.Errorf("cert add answered %q", out)
	}
	if out := mustOK(t, call(t, "PUT", "/tls/key/add/web", string(ca.key))); !strings.Contains(out, "Key stored for name [web]") {
		t.Errorf("key add answered %q", out)
	}
	if certs, _ := GetCerts("web"); len(certs) != 1 {
		t.Fatalf("uploaded cert and key should be usable, got %d certs", len(certs))
	}
	if out := mustOK(t, call(t, "POST", "/tls/key/remove/web", "")); !strings.Contains(out, "Key Removed for name [web]") {
		t.Errorf("key remove answered %q", out)
	}
	if out := mustOK(t, call(t, "POST", "/tls/cert/remove/web", "")); !strings.Contains(out, "Cert Removed for name [web]") {
		t.Errorf("cert remove answered %q", out)
	}
	lock.RLock()
	defer lock.RUnlock()
	if len(rawCerts) != 0 || len(rawKeys) != 0 {
		t.Errorf("raw certs %d and keys %d should be gone", len(rawCerts), len(rawKeys))
	}
}

func TestAPIAddCertDecodesURLSafeBase64(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	encoded := base64.RawURLEncoding.EncodeToString(ca.cert)
	mustOK(t, call(t, "POST", "/tls/cert/add/enc", encoded))
	lock.RLock()
	defer lock.RUnlock()
	if !bytes.Equal(rawCerts["enc"], ca.cert) {
		t.Errorf("stored cert = %q", rawCerts["enc"])
	}
}

func TestAPIAddCertWithoutPayload(t *testing.T) {
	isolate(t)
	for _, target := range []string{"/tls/cert/add/empty", "/tls/key/add/empty"} {
		rr := call(t, "POST", target, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No Payload") {
			t.Errorf("%s answered %d %q, want 400 No Payload", target, rr.Code, rr.Body.String())
		}
	}
	lock.RLock()
	defer lock.RUnlock()
	if len(rawCerts) != 0 || len(rawKeys) != 0 {
		t.Error("nothing should have been stored")
	}
}

func TestAPIMethodsAndUnknownPaths(t *testing.T) {
	isolate(t)
	if rr := call(t, "GET", "/tls/cert/add/x", ""); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
		t.Errorf("GET on an upload API = %d, want it to be refused", rr.Code)
	}
	if rr := call(t, "POST", "/tls/ca/jwks", ""); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
		t.Errorf("POST on the JWKS API = %d, want it to be refused", rr.Code)
	}
	if rr := call(t, "GET", "/tls/nosuch", ""); rr.Code != http.StatusNotFound {
		t.Errorf("unknown path = %d, want 404", rr.Code)
	}
}

func TestAPICAUploadAndList(t *testing.T) {
	isolate(t)
	ca := newCA(t, "ca.test", false)
	if out := mustOK(t, call(t, "PUT", "/tls/ca/cert/add/myca/d1.test", string(ca.cert))); !strings.Contains(out, "CA Cert stored for name [myca]") {
		t.Errorf("answered %q", out)
	}
	mustOK(t, call(t, "POST", "/tls/ca/cert/add/myca/d2.test", string(ca.cert)))
	if out := mustOK(t, call(t, "POST", "/tls/ca/key/add/myca/d1.test", string(ca.key))); !strings.Contains(out, "CA Key stored for name [myca]") {
		t.Errorf("answered %q", out)
	}
	listing := mustOK(t, call(t, "GET", "/tls/ca/certs", ""))
	for _, want := range []string{"myca", "d1.test", "d2.test", "BEGIN CERTIFICATE"} {
		if !strings.Contains(listing, want) {
			t.Errorf("CA listing lacks %q: %s", want, listing)
		}
	}
	if strings.Contains(listing, "PRIVATE KEY") {
		t.Error("the CA listing must not reveal keys")
	}
	if out := mustOK(t, call(t, "POST", "/tls/ca/key/remove/myca", "")); !strings.Contains(out, "CA Key Removed for name [myca]") {
		t.Errorf("answered %q", out)
	}
	if out := mustOK(t, call(t, "POST", "/tls/ca/cert/remove/myca", "")); !strings.Contains(out, "CA Cert Removed for name [myca]") {
		t.Errorf("answered %q", out)
	}
	lock.RLock()
	defer lock.RUnlock()
	if len(CACerts) != 0 || len(CAKeys) != 0 {
		t.Errorf("CAs should be gone: %d certs, %d keys", len(CACerts), len(CAKeys))
	}
}

func TestAPISetDefaultCA(t *testing.T) {
	isolate(t)
	ca := sharedRSACA(t)
	AddCAKey("rsa", "d.test", ca.key)
	AddCACert("rsa", "d.test", ca.cert)
	mustOK(t, call(t, "POST", "/tls/ca/set/default/rsa", ""))

	var jwks JWKS
	if err := json.Unmarshal([]byte(mustOK(t, call(t, "GET", "/tls/ca/jwks", ""))), &jwks); err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("JWKS = %+v", jwks)
	}
	want, _ := parsePrivateKeyPEM(ca.key)
	pub, err := jwkToRSAPublicKey(jwks.Keys[0])
	if err != nil || !pub.Equal(&want.(*rsa.PrivateKey).PublicKey) {
		t.Errorf("served JWKS isn't of the default CA: %v", err)
	}

	pubPEM := mustOK(t, call(t, "GET", "/tls/ca/pubkey", ""))
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil || block.Type != "PUBLIC KEY" {
		t.Fatalf("public key PEM = %q", pubPEM)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil || !parsed.(*rsa.PublicKey).Equal(pub) {
		t.Errorf("served public key differs from the default CA's: %v", err)
	}

	certPEM := mustOK(t, call(t, "GET", "/tls/ca/cert", ""))
	block, _ = pem.Decode([]byte(certPEM))
	caBlock, _ := pem.Decode(ca.cert)
	if block == nil || block.Type != "CERTIFICATE" || !bytes.Equal(block.Bytes, caBlock.Bytes) {
		t.Errorf("served CA cert differs from the one uploaded: %q", certPEM)
	}
}

func TestAPISetDefaultCAUnknownName(t *testing.T) {
	isolate(t)
	lock.RLock()
	before := DefaultCAKey
	lock.RUnlock()
	mustOK(t, call(t, "POST", "/tls/ca/set/default/nosuch", ""))
	lock.RLock()
	defer lock.RUnlock()
	if DefaultCAKey != before {
		t.Error("default CA must not change for an unknown CA")
	}
}

func TestAPIDefaultCAWhenNotConfigured(t *testing.T) {
	isolate(t)
	lock.Lock()
	DefaultCAJWKS = nil
	lock.Unlock()
	for _, target := range []string{"/tls/ca/pubkey", "/tls/ca/cert"} {
		if rr := call(t, "GET", target, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("%s without a CA = %d, want 400", target, rr.Code)
		}
	}
	if rr := call(t, "POST", "/tls/ca/verify", "a.b.c"); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No CA JWKS configured") {
		t.Errorf("verify without a CA = %d %q", rr.Code, rr.Body.String())
	}
	lock.Lock()
	DefaultCAJWKS = &JWKS{Keys: []JWK{{Kid: "k"}}}
	lock.Unlock()
	if rr := call(t, "GET", "/tls/ca/cert", ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No CA certificate available") {
		t.Errorf("CA cert without x5c = %d %q", rr.Code, rr.Body.String())
	}
}

func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	var signer any = key
	if method == jwt.SigningMethodHS256 {
		signer = []byte("secret")
	}
	s, err := token.SignedString(signer)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAPIVerifyJWT(t *testing.T) {
	isolate(t)
	lock.RLock()
	key, kid := DefaultCAKey, DefaultCAJWKS.Keys[0].Kid
	lock.RUnlock()
	other, _ := parsePrivateKeyPEM(sharedRSACA(t).key)
	claims := jwt.MapClaims{"sub": "goto", "exp": time.Now().Add(-time.Hour).Unix()}

	cases := []struct {
		name   string
		token  string
		status int
	}{
		{"valid", signJWT(t, key, kid, jwt.SigningMethodRS256, claims), http.StatusOK},
		{"valid without kid", signJWT(t, key, "", jwt.SigningMethodRS256, claims), http.StatusOK},
		{"valid with spaces around", " " + signJWT(t, key, kid, jwt.SigningMethodRS256, claims) + "\n", http.StatusOK},
		{"signed by another key", signJWT(t, other.(*rsa.PrivateKey), kid, jwt.SigningMethodRS256, claims), http.StatusBadRequest},
		{"unknown kid", signJWT(t, key, "other-kid", jwt.SigningMethodRS256, claims), http.StatusBadRequest},
		{"wrong algorithm", signJWT(t, key, kid, jwt.SigningMethodHS256, claims), http.StatusBadRequest},
	}
	for _, c := range cases {
		rr := call(t, "POST", "/tls/ca/verify", c.token)
		if rr.Code != c.status {
			t.Errorf("%s: status = %d, want %d. Body: %q", c.name, rr.Code, c.status, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"sub": "goto"`) {
			t.Errorf("%s: the payload should be reported either way. Body: %q", c.name, rr.Body.String())
		}
	}
}

func TestAPIVerifyJWTBadInput(t *testing.T) {
	isolate(t)
	if rr := call(t, "PUT", "/tls/ca/verify", ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No JWT payload") {
		t.Errorf("empty payload = %d %q", rr.Code, rr.Body.String())
	}
	if rr := call(t, "POST", "/tls/ca/verify", "not-a-jwt"); rr.Code != http.StatusBadRequest {
		t.Errorf("garbage = %d, want 400", rr.Code)
	}
	if rr := call(t, "POST", "/tls/ca/verify", "a.!!!.c"); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "base64 decode error") {
		t.Errorf("bad payload encoding = %d %q", rr.Code, rr.Body.String())
	}
	text := "a." + base64.RawURLEncoding.EncodeToString([]byte("plain text")) + ".c"
	if rr := call(t, "POST", "/tls/ca/verify", text); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "plain text") {
		t.Errorf("payload that isn't JSON = %d %q", rr.Code, rr.Body.String())
	}
}

func TestAPIGetCerts(t *testing.T) {
	isolate(t)
	if err := AddAutoCert("auto", "auto.test", []string{"alt.test"}, "spiffe://cluster.local/ns/goto/sa/auto"); err != nil {
		t.Fatal(err)
	}
	certs, _ := GetCerts("auto")
	sum := sha256.Sum256(certs[0].Certificate[0])
	for _, target := range []string{"/tls/certs", "/tls/certs/raw"} {
		out := mustOK(t, call(t, "GET", target, ""))
		for _, want := range []string{
			"Certificate [auto]:",
			"Subject: CN=auto.test",
			"Basic Constraints: CA:true",
			"Key Usage: Digital Signature, Certificate Sign, CRL Sign",
			"Extended Key Usage: TLS Web Server Authentication, TLS Web Client Authentication",
			"Subject Alternative Name: DNS:auto.test, DNS:alt.test, URI:spiffe://cluster.local/ns/goto/sa/auto",
			"SHA-256 Fingerprint: " + certHexBytes(sum[:]),
			"Chain Length: 1",
			"Signature Algorithm: ECDSA-SHA384",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q:\n%s", target, want, out)
			}
		}
	}
}

func TestAPIGetCertsWithNoneAndWithBrokenOnes(t *testing.T) {
	isolate(t)
	if out := mustOK(t, call(t, "GET", "/tls/certs", "")); out != "" {
		t.Errorf("no certs should give an empty answer, got %q", out)
	}
	lock.Lock()
	X509Certs["empty"] = nil
	X509Certs["no-chain"] = nil
	lock.Unlock()
	if out := mustOK(t, call(t, "GET", "/tls/certs", "")); out != "" {
		t.Errorf("got %q", out)
	}
}

func TestAPISetWorkDir(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	out := mustOK(t, call(t, "POST", "/tls/workdir/set?dir="+dir, ""))
	if !strings.Contains(out, "Working directory set to ["+dir+"]") || global.ServerConfig.WorkDir != dir {
		t.Errorf("answered %q, work dir %q", out, global.ServerConfig.WorkDir)
	}
	before := global.ServerConfig.WorkDir
	if rr := call(t, "POST", "/tls/workdir/set", ""); rr.Code == http.StatusOK || global.ServerConfig.WorkDir != before {
		t.Errorf("without the dir the API must not apply, answered %d", rr.Code)
	}
}

func TestCertDescriptionHelpers(t *testing.T) {
	if got := certHexBytes([]byte{0x0a, 0xff, 0x00}); got != "0A:FF:00" {
		t.Errorf("certHexBytes = %q", got)
	}
	if got := certHexBytes(nil); got != "" {
		t.Errorf("certHexBytes(nil) = %q", got)
	}
	all := x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment | x509.KeyUsageKeyEncipherment |
		x509.KeyUsageDataEncipherment | x509.KeyUsageKeyAgreement | x509.KeyUsageCertSign | x509.KeyUsageCRLSign |
		x509.KeyUsageEncipherOnly | x509.KeyUsageDecipherOnly
	want := "Digital Signature, Content Commitment, Key Encipherment, Data Encipherment, Key Agreement, Certificate Sign, CRL Sign, Encipher Only, Decipher Only"
	if got := certKeyUsageString(all); got != want {
		t.Errorf("certKeyUsageString = %q", got)
	}
	if got := certKeyUsageString(0); got != "" {
		t.Errorf("certKeyUsageString(0) = %q", got)
	}
	ekus := []x509.ExtKeyUsage{x509.ExtKeyUsageAny, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageCodeSigning,
		x509.ExtKeyUsageEmailProtection, x509.ExtKeyUsageTimeStamping, x509.ExtKeyUsageOCSPSigning, x509.ExtKeyUsageIPSECEndSystem,
		x509.ExtKeyUsageIPSECTunnel, x509.ExtKeyUsageIPSECUser, x509.ExtKeyUsageMicrosoftServerGatedCrypto}
	want = "Any, TLS Web Server Authentication, TLS Web Client Authentication, Code Signing, Email Protection, Time Stamping, OCSP Signing, IPSEC End System, IPSEC Tunnel, IPSEC User, Unknown(" +
		strconv.Itoa(int(x509.ExtKeyUsageMicrosoftServerGatedCrypto)) + ")"
	if got := certExtKeyUsageString(ekus); got != want {
		t.Errorf("certExtKeyUsageString = %q, want %q", got, want)
	}
}

func TestJWKToRSAPublicKeyBadInput(t *testing.T) {
	if _, err := jwkToRSAPublicKey(JWK{N: "!!!", E: "AQAB"}); err == nil {
		t.Error("expected an error for a bad modulus")
	}
	if _, err := jwkToRSAPublicKey(JWK{N: "AQAB", E: "!!!"}); err == nil {
		t.Error("expected an error for a bad exponent")
	}
	k, err := jwkToRSAPublicKey(JWK{N: "AQAB", E: "AQAB"})
	if err != nil || k.E != 65537 || k.N.Int64() != 65537 {
		t.Errorf("= %+v, %v", k, err)
	}
}

func TestConcurrentAdminRequestsAndCertChanges(t *testing.T) {
	isolate(t)
	ca := sharedRSACA(t)
	errs := &errList{}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				AddCAKey("c", "d.test", ca.key)
				AddCACert("c", "d.test", ca.cert)
				SetDefaultCA("c")
				if err := AddAutoCert("auto", "auto.test", nil, ""); err != nil {
					errs.addf("AddAutoCert: %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				for _, target := range []string{"/tls/ca/certs", "/tls/certs", "/tls/ca/jwks", "/tls/ca/cert", "/tls/ca/pubkey"} {
					if rr := call(t, "GET", target, ""); rr.Code != http.StatusOK && rr.Code != http.StatusBadRequest {
						errs.addf("%s answered %d", target, rr.Code)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	errs.report(t)
}
