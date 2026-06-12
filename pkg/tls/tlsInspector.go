package tls

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"goto/pkg/util"
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
	CommonName     string   `json:"cn,omitempty"`
	DNSNames       []string `json:"alt,omitempty"`
	URIs           []string `json:"uris,omitempty"`
	SNI            string   `json:"sni,omitempty"`
	ALPN           []string `json:"alpn,omitempty"`
	NegotiatedALPN string   `json:"negotiated,omitempty"`
	Issuers        []string `json:"issuers,omitempty"`
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

func ExtractSNI(port int, label string, remoteAddr string, callback func(string, string, string)) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		fmt.Printf("VerifyConnection called on Port [%d] Label [%s]. SNI: '%s'\n", port, label, cs.ServerName)
		callback(remoteAddr, cs.ServerName, cs.NegotiatedProtocol)
		return nil
	}
}

func ExtractPeerCertInfo(port int, label, remoteAddr string, callback func(string, string, []string, []string, []string)) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("[%s]Port[%d]: VerifyPeerCertificate: No client certificate provided", label, port)
		}
		firstCert := rawCerts[0]
		clientCert, err := x509.ParseCertificate(firstCert)
		if err != nil {
			return fmt.Errorf("[%s]Port[%d]: VerifyPeerCertificate: Failed to parse client certificate: %s", label, port, err.Error())
		}
		uris := []string{}
		issuers := []string{}
		for _, url := range clientCert.URIs {
			uris = append(uris, url.String())
		}
		for _, chain := range verifiedChains {
			for _, c := range chain {
				issuers = append(issuers, c.Issuer.CommonName)
			}
		}
		callback(remoteAddr, clientCert.Subject.CommonName, clientCert.DNSNames, uris, issuers)
		fmt.Printf("[%s]Port[%d]: VerifyPeerCertificate: Certificate CN: [%s], SAN: %+v, URIs: %+v\n", label, port, clientCert.Subject.CommonName, clientCert.DNSNames, uris)
		return nil
	}
}

func GetConfigForClient(port int, label string, tlsConfig *tls.Config, storeALPN func(string, []string),
	storeCertInfo func(remoteAddr string, commonName string, dnsNames, uris, issuers []string),
	storeSNI func(remoteAddr string, sni, alpn string)) func(*tls.ClientHelloInfo) (*tls.Config, error) {
	return func(clientHello *tls.ClientHelloInfo) (*tls.Config, error) {
		if clientHello == nil {
			return nil, fmt.Errorf("[%s]Port[%d]: GetConfigForClient: No clientHello provided", label, port)
		}
		remoteAddr := util.GetRemoteAddr(clientHello.Context())
		storeALPN(remoteAddr, clientHello.SupportedProtos)
		tlsConfig.VerifyConnection = ExtractSNI(port, label, remoteAddr, storeSNI)
		tlsConfig.VerifyPeerCertificate = ExtractPeerCertInfo(port, label, remoteAddr, storeCertInfo)
		return tlsConfig, nil
	}
}
