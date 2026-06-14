package tls

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"goto/pkg/util"
	"log"
	"net"
	"sync"
)

type TLSInspector struct {
	net.Listener
	tlsConfig *tls.Config
	port      int
	label     string
}

type PeerCertInfo struct {
	RemoteAddr     string   `json:"remoteAddr,omitempty"`
	Subject        string   `json:"subject,omitempty"`
	DNSNames       []string `json:"dnsNames,omitempty"`
	URIs           []string `json:"uris,omitempty"`
	SNI            string   `json:"sni,omitempty"`
	ALPN           []string `json:"alpn,omitempty"`
	NegotiatedALPN string   `json:"negotiated,omitempty"`
	Issuer         string   `json:"issuer,omitempty"`
}

var (
	PeerCertInfos = map[string]*PeerCertInfo{}
	peerCertLock  = sync.RWMutex{}
)

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
	buffered := newBufferedConn(c)

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

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func newBufferedConn(c net.Conn) *bufferedConn {
	return &bufferedConn{
		Conn: c,
		r:    bufio.NewReader(c),
	}
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

func (b *bufferedConn) Peek(n int) ([]byte, error) {
	return b.r.Peek(n)
}

func ExtractSNI(port, label string, getRemoteAddr func() string, callback func(string, string, string), origVerifyConn func(cs tls.ConnectionState) error) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		log.Printf("VerifyConnection: called on Port [%s] Label [%s]. SNI: '%s'\n", port, label, cs.ServerName)
		callback(getRemoteAddr(), cs.ServerName, cs.NegotiatedProtocol)
		if origVerifyConn != nil {
			return origVerifyConn(cs)
		}
		log.Printf("VerifyConnection: stored on Port [%s] Label [%s] - SNI: [%s], NegotiatedProtocol: [%s].\n", port, label, cs.ServerName, cs.NegotiatedProtocol)
		return nil
	}
}

func ExtractPeerCertInfo(port, label string, getRemoteAddr func() string, callback func(string, string, []string, []string, string),
	origVeryPeerCert func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		remoteAddr := getRemoteAddr()
		log.Printf("VerifyPeerCertificate: called on Port [%s] Label [%s] RemoteAddr [%s]\n", port, label, remoteAddr)
		if len(rawCerts) == 0 {
			return fmt.Errorf("VerifyPeerCertificate: No client certificate provided on Port [%s] Label [%s] RemoteAddr [%s]", port, label, remoteAddr)
		}
		firstCert := rawCerts[0]
		clientCert, err := x509.ParseCertificate(firstCert)
		if err != nil {
			return fmt.Errorf("VerifyPeerCertificate: Failed to parse client certificate on Port [%s] Label [%s] RemoteAddr [%s]: Error: %s", port, label, remoteAddr, err.Error())
		}
		uris := []string{}
		for _, url := range clientCert.URIs {
			uris = append(uris, url.String())
		}
		issuer := IssuerToString(clientCert)
		subject := SubjectToString(clientCert)
		callback(remoteAddr, subject, clientCert.DNSNames, uris, issuer)
		log.Printf("VerifyPeerCertificate: Stored Certificate Info on Port [%s] Label [%s] RemoteAddr [%s] - CN: [%s], SAN: %+v, URIs: %+v, Issuers: %+v\n",
			port, label, remoteAddr, clientCert.Subject.CommonName, clientCert.DNSNames, uris, issuer)
		if origVeryPeerCert != nil {
			return origVeryPeerCert(rawCerts, verifiedChains)
		}
		return nil
	}
}

func GetConfigForClient(port, label string, tlsConfig *tls.Config, storeALPN func(string, []string),
	storeCertInfo func(remoteAddr string, commonName string, dnsNames, uris []string, issuer string),
	storeSNI func(remoteAddr string, sni, alpn string),
	origVerifyConn func(cs tls.ConnectionState) error,
	origVeryPeerCert func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	remoteAddr := ""
	getRemoteAddr := func() string { return remoteAddr }
	tlsConfig.VerifyConnection = ExtractSNI(port, label, getRemoteAddr, storeSNI, origVerifyConn)
	tlsConfig.VerifyPeerCertificate = ExtractPeerCertInfo(port, label, getRemoteAddr, storeCertInfo, origVeryPeerCert)
	return func(clientHello *tls.ClientHelloInfo) (*tls.Config, error) {
		log.Printf("GetConfigForClient called on Port [%s] Label [%s] - SNI: [%s], SupportedProtos: %+v\n", port, label, clientHello.ServerName, clientHello.SupportedProtos)
		if clientHello == nil {
			return nil, fmt.Errorf("[%s]Port[%s]: GetConfigForClient: No clientHello provided", label, port)
		}
		remoteAddr = util.GetRemoteAddr(clientHello.Context())
		storeALPN(remoteAddr, clientHello.SupportedProtos)
		storeSNI(remoteAddr, clientHello.ServerName, "")
		log.Printf("GetConfigForClient: Server Reporting ALPNs on Port [%s] Label [%s] RemoteAddr [%s] - ALPNs: %+v\n", port, label, remoteAddr, tlsConfig.NextProtos)
		return tlsConfig, nil
	}
}

func SubjectToString(cert *x509.Certificate) string {
	return fmt.Sprintf("org=%s;co=%s,cn=[%s]", cert.Subject.Organization, cert.Subject.Country, cert.Subject.CommonName)
}

func IssuerToString(cert *x509.Certificate) string {
	return fmt.Sprintf("org=%s;co=%s,sn=[%s]", cert.Issuer.Organization, cert.Issuer.Country, cert.Issuer.SerialNumber)
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
