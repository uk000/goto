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
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

type TLSInspector struct {
	net.Listener
	tlsConfig *tls.Config
	port      int
	label     string
}

type PeerCertInfo struct {
	StartAt        time.Time `json:"startAt,omitempty"`
	EndAt          time.Time `json:"endAt,omitempty"`
	Finished       bool      `json:"finished"`
	Status         []string  `json:"status,omitempty"`
	RemoteAddr     string    `json:"remoteAddr"`
	Subject        string    `json:"subject,omitempty"`
	DNSNames       []string  `json:"dnsNames,omitempty"`
	URIs           []string  `json:"uris,omitempty"`
	SNI            string    `json:"sni,omitempty"`
	ALPN           []string  `json:"alpn,omitempty"`
	NegotiatedALPN string    `json:"negotiated,omitempty"`
	Issuer         string    `json:"issuer,omitempty"`
}

func NewTLSInspector(port int, label string, l net.Listener, tlsConfig *tls.Config) net.Listener {
	return &TLSInspector{
		Listener:  l,
		tlsConfig: tlsConfig,
		port:      port,
		label:     label,
	}
}

func (t *TLSInspector) Accept() (net.Conn, error) {
	c, err := t.Listener.Accept()
	if err != nil {
		return nil, err
	}
	buffered := newPeekedConn(c)

	// 0x16 (22 in decimal) is the standard TLS Handshake record type
	b, err := buffered.Peek(1)
	if err != nil {
		c.Close()
		return nil, err
	}
	if b[0] == 0x16 {
		return tls.Server(buffered, t.tlsConfig), nil
	}
	return buffered, nil
}

type PeekedConn struct {
	net.Conn
	r *bufio.Reader
}

func newPeekedConn(c net.Conn) *PeekedConn {
	return &PeekedConn{
		Conn: c,
		r:    bufio.NewReader(c),
	}
}

func (b *PeekedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

func (b *PeekedConn) Peek(n int) ([]byte, error) {
	return b.r.Peek(n)
}

func ExtractSNI(port, label string, remoteAddr string, storeSNI func(string, string, string),
	storeCertInfo func(remoteAddr string, commonName string, dnsNames, uris []string, issuer string),
	updatePeerStatus func(remoteAddr string, success bool, e string)) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		log.Printf("VerifyConnection: called on Port [%s] Label [%s]. SNI: '%s'\n", port, label, cs.ServerName)
		storeSNI(remoteAddr, cs.ServerName, cs.NegotiatedProtocol)
		msg := ""
		if len(cs.PeerCertificates) == 0 {
			msg = fmt.Sprintf("VerifyConnection: No client certificate provided on Port [%s] Label [%s] RemoteAddr [%s]. Stored SNI: [%s], NegotiatedProtocol: [%s]", port, label, remoteAddr, cs.ServerName, cs.NegotiatedProtocol)
		} else {
			peerCert := cs.PeerCertificates[0]
			uris := []string{}
			for _, url := range peerCert.URIs {
				uris = append(uris, url.String())
			}
			issuer := IssuerToString(peerCert)
			subject := SubjectToString(peerCert)
			storeCertInfo(remoteAddr, subject, peerCert.DNSNames, uris, issuer)
			msg = fmt.Sprintf("VerifyConnection: Stored Certificate Info on Port [%s] Label [%s] RemoteAddr [%s] - CN: [%s], SAN: %+v, URIs: %+v, Issuers: %+v, SNI: [%s], NegotiatedProtocol: [%s]",
				port, label, remoteAddr, peerCert.Subject.CommonName, peerCert.DNSNames, uris, issuer, cs.ServerName, cs.NegotiatedProtocol)
		}
		updatePeerStatus(remoteAddr, false, msg)
		log.Println(msg)
		return nil
	}
}

func ExtractPeerCertInfo(port, label string, remoteAddr string,
	storeCertInfo func(remoteAddr string, commonName string, dnsNames, uris []string, issuer string),
	updatePeerStatus func(remoteAddr string, success bool, e string)) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		log.Printf("VerifyPeerCertificate: called on Port [%s] Label [%s] RemoteAddr [%s]\n", port, label, remoteAddr)
		if len(rawCerts) == 0 {
			msg := fmt.Sprintf("VerifyPeerCertificate: No client certificate provided on Port [%s] Label [%s] RemoteAddr [%s]", port, label, remoteAddr)
			updatePeerStatus(remoteAddr, true, msg)
			return errors.New(msg)
		}
		// firstCert := rawCerts[0]
		// peerCert, err := x509.ParseCertificate(firstCert)
		// if err != nil {
		// 	msg := fmt.Sprintf("VerifyPeerCertificate: Failed to parse client certificate on Port [%s] Label [%s] RemoteAddr [%s]: Error: %s", port, label, remoteAddr, err.Error())
		// 	updatePeerStatus(remoteAddr, true, msg)
		// 	return errors.New(msg)
		// }
		// uris := []string{}
		// for _, url := range peerCert.URIs {
		// 	uris = append(uris, url.String())
		// }
		// issuer := IssuerToString(peerCert)
		// subject := SubjectToString(peerCert)
		// storeCertInfo(remoteAddr, subject, peerCert.DNSNames, uris, issuer)
		// msg := fmt.Sprintf("VerifyPeerCertificate: Stored Certificate Info on Port [%s] Label [%s] RemoteAddr [%s] - CN: [%s], SAN: %+v, URIs: %+v, Issuers: %+v",
		// 	port, label, remoteAddr, peerCert.Subject.CommonName, peerCert.DNSNames, uris, issuer)
		// updatePeerStatus(remoteAddr, false, msg)
		// log.Println(msg)
		return nil
	}
}

func HandleClientHello(port, label string, tlsConfig *tls.Config,
	storeALPN func(string, []string),
	storeCertInfo func(remoteAddr string, commonName string, dnsNames, uris []string, issuer string),
	storeSNI func(remoteAddr string, sni, alpn string),
	updatePeerStatus func(remoteAddr string, success bool, e string)) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(clientHello *tls.ClientHelloInfo) (*tls.Config, error) {
		log.Printf("HandleClientHello called on Port [%s] Label [%s] - SNI: [%s], SupportedProtos: %+v\n", port, label, clientHello.ServerName, clientHello.SupportedProtos)
		if clientHello == nil {
			msg := fmt.Sprintf("HandleClientHello on Port [%s] Label [%s] - No clientHello provided", port, label)
			return nil, errors.New(msg)
		}
		remoteAddr := clientHello.Conn.RemoteAddr().String()
		storeALPN(remoteAddr, clientHello.SupportedProtos)
		storeSNI(remoteAddr, clientHello.ServerName, "")
		msg := fmt.Sprintf("HandleClientHello: Server Reporting ALPNs on Port [%s] Label [%s] RemoteAddr [%s] - ALPNs: %+v", port, label, remoteAddr, tlsConfig.NextProtos)
		updatePeerStatus(remoteAddr, false, msg)
		log.Println(msg)
		t2 := tlsConfig.Clone()
		t2.VerifyConnection = ExtractSNI(port, label, remoteAddr, storeSNI, storeCertInfo, updatePeerStatus)
		t2.VerifyPeerCertificate = ExtractPeerCertInfo(port, label, remoteAddr, storeCertInfo, updatePeerStatus)
		return t2, nil
	}
}

func SubjectToString(cert *x509.Certificate) string {
	return fmt.Sprintf("org=%s,co=%s,cn=[%s]", cert.Subject.Organization, cert.Subject.Country, cert.Subject.CommonName)
}

func IssuerToString(cert *x509.Certificate) string {
	return fmt.Sprintf("org=%s,co=%s,sn=[%s]", cert.Issuer.Organization, cert.Issuer.Country, cert.Issuer.SerialNumber)
}

func IdentifyLeaf(certs []*tls.Certificate) *x509.Certificate {
	var leaf *x509.Certificate
	var err error
	if certs[0].Leaf == nil && len(certs[0].Certificate) > 0 {
		if leaf, err = x509.ParseCertificate(certs[0].Certificate[0]); err == nil {
			certs[0].Leaf = leaf
		}
	} else {
		leaf = certs[0].Leaf
	}
	return leaf
}

func NewPeerCertInfo(remoteAddr string) *PeerCertInfo {
	return &PeerCertInfo{
		StartAt:    time.Now(),
		RemoteAddr: remoteAddr,
	}
}

func (pci *PeerCertInfo) Summary() string {
	pci.Finished = true
	pci.Status = append(pci.Status, "Peer Cert Reported")
	b := &strings.Builder{}
	b.Grow(300)
	if pci.RemoteAddr != "" {
		b.WriteString(fmt.Sprintf("RemoteAddr: %s;; ", pci.RemoteAddr))
	}
	if pci.Subject != "" {
		b.WriteString(fmt.Sprintf("Subject: %s;; ", pci.Subject))
	}
	if len(pci.DNSNames) > 0 {
		b.WriteString(fmt.Sprintf("DNSNames: %+v;; ", pci.DNSNames))
	}
	if len(pci.URIs) > 0 {
		b.WriteString(fmt.Sprintf("URIs: %+v;; ", pci.URIs))
	}
	if pci.SNI != "" {
		b.WriteString(fmt.Sprintf("SNI: %s;; ", pci.SNI))
	}
	if len(pci.ALPN) > 0 {
		b.WriteString(fmt.Sprintf("ALPN: %+v;; ", pci.ALPN))
	}
	if pci.Issuer != "" {
		b.WriteString(fmt.Sprintf("Issuer: %s;; ", pci.Issuer))
	}
	if pci.NegotiatedALPN != "" {
		b.WriteString(fmt.Sprintf("NegotiatedALPN: %s;; ", pci.NegotiatedALPN))
	}
	b.WriteString(fmt.Sprintf("Finished: %t;;", pci.Finished))
	return b.String()
}
