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
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
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
	util.AddRoute(tlsRouter, "/ca/certs", getCACerts, "GET")
	util.AddRouteQ(tlsRouter, "/cert/add", addCertOrKey, "name", "PUT", "POST")
	util.AddRouteQ(tlsRouter, "/cert/remove", removeCertOrKey, "name", "PUT", "POST")
	util.AddRouteQ(tlsRouter, "/key/add", addCertOrKey, "name", "PUT", "POST")
	util.AddRouteQ(tlsRouter, "/key/remove", removeCertOrKey, "name", "PUT", "POST")
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
		if d, err := base64.StdEncoding.DecodeString(string(data)); err == nil {
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
	for name, tlsCert := range X509Certs {
		cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
		if err != nil {
			util.SendBadRequest(err.Error(), w, r)
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
