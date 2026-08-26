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
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"math/big"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

const (
	DefaultSocket = "unix:///tmp/spire-agent/public/api.sock"
)

var (
	Middleware = middleware.NewMiddleware("tls", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	tlsRouter := middleware.RootPath("/tls")
	util.AddRoute(tlsRouter, "/ca/cert/add/{name}/{domain}", addCACertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/ca/cert/remove/{name}", removeCACertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/ca/key/add/{name}/{domain}", addCACertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/ca/key/remove/{name}", removeCACertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/ca/set/default/{name}", setDefaultCA, "PUT", "POST")
	util.AddRoute(tlsRouter, "/ca/jwks", getCAJWKS, "GET")
	util.AddRoute(tlsRouter, "/ca/pubkey", getDefaultCACert, "GET")
	util.AddRoute(tlsRouter, "/ca/cert", getDefaultCACertPEM, "GET")
	util.AddRoute(tlsRouter, "/ca/verify", verifyCAJWT, "POST", "PUT")
	util.AddRoute(tlsRouter, "/ca/certs", getCACerts, "GET")

	util.AddRoute(tlsRouter, "/cert/add/{name}", addCertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/cert/remove/{name}", removeCertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/key/add/{name}", addCertOrKey, "PUT", "POST")
	util.AddRoute(tlsRouter, "/key/remove/{name}", removeCertOrKey, "PUT", "POST")

	util.AddRoute(tlsRouter, "/certs", getCerts, "GET")
	util.AddRoute(tlsRouter, "/certs/raw", getCerts, "GET")

	util.AddRouteQ(tlsRouter, "/workdir/set", setWorkDir, "dir", "POST", "PUT")
}

func addCertOrKey(w http.ResponseWriter, r *http.Request) {
	msg := ""
	isKey := strings.Contains(r.RequestURI, "key")
	name := util.GetStringParamValue(r, "name")
	data := util.ReadBytes(r.Body)
	if len(data) > 0 {
		if d, err := base64.RawURLEncoding.DecodeString(string(data)); err == nil {
			data = d
		}
		if isKey {
			AddKey(name, data)
			msg = fmt.Sprintf("Key stored for name [%s]", name)
		} else {
			AddCert(name, data)
			msg = fmt.Sprintf("Cert stored for name [%s]", name)
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No Payload"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeCertOrKey(w http.ResponseWriter, r *http.Request) {
	isKey := strings.Contains(r.RequestURI, "key")
	name := util.GetStringParamValue(r, "name")
	msg := ""
	if isKey {
		RemoveKey(name)
		msg = "Key Removed"
	} else {
		RemoveCert(name)
		msg = "Cert Removed"
	}
	msg = fmt.Sprintf("%s for name [%s]", msg, name)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addCACertOrKey(w http.ResponseWriter, r *http.Request) {
	msg := ""
	isKey := strings.Contains(r.RequestURI, "key")
	name := util.GetStringParamValue(r, "name")
	domain := util.GetStringParamValue(r, "domain")
	data := util.ReadBytes(r.Body)
	if isKey {
		AddCAKey(name, domain, data)
		msg = fmt.Sprintf("CA Key stored for name [%s]", name)
	} else {
		AddCACert(name, domain, data)
		msg = fmt.Sprintf("CA Cert stored for name [%s]", name)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeCACertOrKey(w http.ResponseWriter, r *http.Request) {
	isKey := strings.Contains(r.RequestURI, "key")
	name := util.GetStringParamValue(r, "name")
	msg := ""
	if isKey {
		RemoveCAKey(name)
		msg = "CA Key Removed"
	} else {
		RemoveCACert(name)
		msg = "CA Cert Removed"
	}
	msg = fmt.Sprintf("%s for name [%s]", msg, name)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setDefaultCA(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "name")
	if name != "" {
		SetDefaultCA(name)
		msg = fmt.Sprintf("CA [%s] set as default signing authority", name)
	} else {
		msg = "No CA name given"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getCAJWKS(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage("Sent CA JWKS", r)
	util.WriteJsonPayload(w, DefaultCAJWKS)
}

func getDefaultCACert(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	jwks := DefaultCAJWKS
	lock.RUnlock()
	if jwks == nil || len(jwks.Keys) == 0 {
		util.SendBadRequest(w, r, "No CA JWKS configured")
		return
	}
	pubKey, err := jwkToRSAPublicKey(jwks.Keys[0])
	if err != nil {
		util.SendBadRequest(w, r, fmt.Sprintf("Failed to reconstruct public key: %s", err.Error()))
		return
	}
	der, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		util.SendBadRequest(w, r, fmt.Sprintf("Failed to marshal public key: %s", err.Error()))
		return
	}
	if err = pem.Encode(w, &pem.Block{Type: "PUBLIC KEY", Bytes: der}); err != nil {
		util.SendBadRequest(w, r, fmt.Sprintf("Failed to encode public key as PEM: %s", err.Error()))
		return
	}
	util.AddLogMessage("Sent CA public key", r)
}

func getDefaultCACertPEM(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	jwks := DefaultCAJWKS
	lock.RUnlock()
	if jwks == nil || len(jwks.Keys) == 0 || len(jwks.Keys[0].X5c) == 0 {
		util.SendBadRequest(w, r, "No CA certificate available")
		return
	}
	x5c := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, jwks.Keys[0].X5c[0])
	der, err := base64.StdEncoding.DecodeString(x5c)
	if err != nil {
		// Fall back to no-padding variant in case the encoder omitted '='.
		der, err = base64.RawStdEncoding.DecodeString(x5c)
	}
	if err != nil {
		util.SendBadRequest(w, r, fmt.Sprintf("Failed to decode certificate: %s", err.Error()))
		return
	}
	if err = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		util.SendBadRequest(w, r, fmt.Sprintf("Failed to encode certificate as PEM: %s", err.Error()))
		return
	}
	util.AddLogMessage("Sent CA certificate", r)
}

func jwkToRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("invalid JWK N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("invalid JWK E: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func verifyCAJWT(w http.ResponseWriter, r *http.Request) {
	tokenBytes := util.ReadBytes(r.Body)
	if len(tokenBytes) == 0 {
		util.SendBadRequest(w, r, "No JWT payload")
		return
	}
	// rawParts := strings.Split(strings.TrimSpace(string(tokenBytes)), ".")
	// for i, p := range rawParts {
	// 	rawParts[i] = strings.TrimRight(p, "=")
	// }
	// tokenString := strings.Join(rawParts, ".")
	tokenString := strings.TrimSpace(string(tokenBytes))
	lock.RLock()
	jwks := DefaultCAJWKS
	lock.RUnlock()

	if jwks == nil || len(jwks.Keys) == 0 {
		util.SendBadRequest(w, r, "No CA JWKS configured")
		return
	}

	_, jwtErr := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		for _, key := range jwks.Keys {
			if kid == "" || key.Kid == kid {
				return jwkToRSAPublicKey(key)
			}
		}
		return nil, fmt.Errorf("no matching key for kid %q", kid)
	}, jwt.WithoutClaimsValidation())

	var payloadOutput string
	parts := strings.Split(tokenString, ".")
	if len(parts) >= 2 {
		payloadBytes, decErr := base64.RawURLEncoding.DecodeString(parts[1])
		if decErr != nil {
			payloadOutput = fmt.Sprintf("Payload (base64 decode error): %s", decErr.Error())
		} else {
			var buf bytes.Buffer
			if jsonErr := json.Indent(&buf, payloadBytes, "", "  "); jsonErr != nil {
				payloadOutput = fmt.Sprintf("Payload (plain text — JSON parse error: %s):\n%s", jsonErr.Error(), string(payloadBytes))
			} else {
				payloadOutput = buf.String()
			}
		}
	}

	// HTTP status reflects JWT validity only; body carries payload content.
	if jwtErr != nil {
		w.WriteHeader(http.StatusBadRequest)
	}
	fmt.Fprintln(w, payloadOutput)
	util.AddLogMessage(fmt.Sprintf("JWT verify: jwtErr=%v", jwtErr), r)
}

func getCACerts(w http.ResponseWriter, r *http.Request) {
	caCerts := map[string]*types.Pair[map[string]bool, string]{}
	for name, pair := range CACerts {
		cert := string(pair.Right)
		caCerts[name] = types.NewPair(pair.Left, cert)
	}
	util.AddLogMessage("Sent CA certs", r)
	fmt.Fprintln(w, util.ToYaml(caCerts))
}

func getCerts(w http.ResponseWriter, r *http.Request) {
	for name, certs := range X509Certs {
		for _, tlsCert := range certs {
			cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
			if err != nil {
				util.SendBadRequest(w, r, err.Error())
				return
			}
			b := &strings.Builder{}
			b.WriteString(fmt.Sprintf("Certificate [%s]:\n", name))
			b.WriteString(fmt.Sprintf("  Version: %d\n", cert.Version))
			b.WriteString(fmt.Sprintf("  Serial Number: %s\n", cert.SerialNumber.String()))
			b.WriteString(fmt.Sprintf("  Signature Algorithm: %s\n", cert.SignatureAlgorithm.String()))
			b.WriteString(fmt.Sprintf("  Issuer: %s\n", cert.Issuer.String()))
			b.WriteString(fmt.Sprintln("  Validity:"))
			b.WriteString(fmt.Sprintf("    Not Before: %s\n", cert.NotBefore.UTC().Format("Jan  2 15:04:05 2006 GMT")))
			b.WriteString(fmt.Sprintf("    Not After : %s\n", cert.NotAfter.UTC().Format("Jan  2 15:04:05 2006 GMT")))
			b.WriteString(fmt.Sprintf("  Subject: %s\n", cert.Subject.String()))
			b.WriteString(fmt.Sprintln("  Subject Public Key Info:"))
			b.WriteString(fmt.Sprintf("    Public Key Algorithm: %s\n", cert.PublicKeyAlgorithm.String()))
			b.WriteString(fmt.Sprintln("  X509v3 Extensions:"))
			b.WriteString(fmt.Sprintf("    Basic Constraints: CA:%v\n", cert.IsCA))
			if cert.KeyUsage != 0 {
				b.WriteString(fmt.Sprintf("    Key Usage: %s\n", certKeyUsageString(cert.KeyUsage)))
			}
			if len(cert.ExtKeyUsage) > 0 {
				b.WriteString(fmt.Sprintf("    Extended Key Usage: %s\n", certExtKeyUsageString(cert.ExtKeyUsage)))
			}
			if len(cert.SubjectKeyId) > 0 {
				b.WriteString(fmt.Sprintf("    Subject Key Identifier: %s\n", certHexBytes(cert.SubjectKeyId)))
			}
			if len(cert.AuthorityKeyId) > 0 {
				b.WriteString(fmt.Sprintf("    Authority Key Identifier: %s\n", certHexBytes(cert.AuthorityKeyId)))
			}
			if len(cert.DNSNames) > 0 || len(cert.IPAddresses) > 0 || len(cert.URIs) > 0 {
				var sans []string
				for _, dns := range cert.DNSNames {
					sans = append(sans, "DNS:"+dns)
				}
				for _, ip := range cert.IPAddresses {
					sans = append(sans, "IP:"+ip.String())
				}
				for _, uri := range cert.URIs {
					sans = append(sans, "URI:"+uri.String())
				}
				b.WriteString(fmt.Sprintf("    Subject Alternative Name: %s\n", strings.Join(sans, ", ")))
			}
			fpBytes := sha256.Sum256(tlsCert.Certificate[0])
			b.WriteString(fmt.Sprintf("  SHA-256 Fingerprint: %s\n", certHexBytes(fpBytes[:])))
			b.WriteString(fmt.Sprintf("  Chain Length: %d\n", len(tlsCert.Certificate)))
			b.WriteString(fmt.Sprintln())
			fmt.Fprintln(w, b.String())
		}
	}
}

func certHexBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	return strings.Join(parts, ":")
}

func certKeyUsageString(ku x509.KeyUsage) string {
	var names []string
	if ku&x509.KeyUsageDigitalSignature != 0 {
		names = append(names, "Digital Signature")
	}
	if ku&x509.KeyUsageContentCommitment != 0 {
		names = append(names, "Content Commitment")
	}
	if ku&x509.KeyUsageKeyEncipherment != 0 {
		names = append(names, "Key Encipherment")
	}
	if ku&x509.KeyUsageDataEncipherment != 0 {
		names = append(names, "Data Encipherment")
	}
	if ku&x509.KeyUsageKeyAgreement != 0 {
		names = append(names, "Key Agreement")
	}
	if ku&x509.KeyUsageCertSign != 0 {
		names = append(names, "Certificate Sign")
	}
	if ku&x509.KeyUsageCRLSign != 0 {
		names = append(names, "CRL Sign")
	}
	if ku&x509.KeyUsageEncipherOnly != 0 {
		names = append(names, "Encipher Only")
	}
	if ku&x509.KeyUsageDecipherOnly != 0 {
		names = append(names, "Decipher Only")
	}
	return strings.Join(names, ", ")
}

func certExtKeyUsageString(ekus []x509.ExtKeyUsage) string {
	var names []string
	for _, eku := range ekus {
		switch eku {
		case x509.ExtKeyUsageAny:
			names = append(names, "Any")
		case x509.ExtKeyUsageServerAuth:
			names = append(names, "TLS Web Server Authentication")
		case x509.ExtKeyUsageClientAuth:
			names = append(names, "TLS Web Client Authentication")
		case x509.ExtKeyUsageCodeSigning:
			names = append(names, "Code Signing")
		case x509.ExtKeyUsageEmailProtection:
			names = append(names, "Email Protection")
		case x509.ExtKeyUsageTimeStamping:
			names = append(names, "Time Stamping")
		case x509.ExtKeyUsageOCSPSigning:
			names = append(names, "OCSP Signing")
		case x509.ExtKeyUsageIPSECEndSystem:
			names = append(names, "IPSEC End System")
		case x509.ExtKeyUsageIPSECTunnel:
			names = append(names, "IPSEC Tunnel")
		case x509.ExtKeyUsageIPSECUser:
			names = append(names, "IPSEC User")
		default:
			names = append(names, fmt.Sprintf("Unknown(%d)", eku))
		}
	}
	return strings.Join(names, ", ")
}

func setWorkDir(w http.ResponseWriter, r *http.Request) {
	msg := ""
	dir := util.GetStringParamValue(r, "dir")
	if dir != "" {
		global.ServerConfig.WorkDir = dir
		msg = fmt.Sprintf("Working directory set to [%s]", dir)
	} else {
		msg = "Missing directory path"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
