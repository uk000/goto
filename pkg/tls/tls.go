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
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	RootCAs   = x509.NewCertPool()
	CACerts   = map[string]*types.Pair[map[string]bool, []byte]{}
	CAKeys    = map[string]*types.Pair[map[string]bool, []byte]{}
	rawCerts  = map[string][]byte{}
	rawKeys   = map[string][]byte{}
	X509Certs = map[string]*tls.Certificate{}
	lock      = sync.RWMutex{}
)

func AddCert(name string, cert []byte) {
	lock.Lock()
	defer lock.Unlock()
	rawCerts[name] = cert
}

func RemoveCert(name string) {
	lock.Lock()
	defer lock.Unlock()
	delete(rawCerts, name)
}

func AddKey(name string, cert []byte) {
	lock.Lock()
	defer lock.Unlock()
	rawKeys[name] = cert
}

func RemoveKey(name string) {
	lock.Lock()
	defer lock.Unlock()
	delete(rawKeys, name)
}

func AddAutoCert(name string, commonName string, altNames []string, spiffeID string) error {
	domains := []string{commonName}
	domains = append(domains, altNames...)
	if cert, err := CreateCertificate(domains, spiffeID, name); err == nil {
		lock.Lock()
		defer lock.Unlock()
		X509Certs[name] = cert
	} else {
		return err
	}
	return nil
}

func GetCert(name string) (*tls.Certificate, error) {
	lock.Lock()
	defer lock.Unlock()
	cert := X509Certs[name]
	if cert == nil {
		rawCert := rawCerts[name]
		rawKey := rawKeys[name]
		if len(rawCert) > 0 && len(rawKey) > 0 {
			if c, err := tls.X509KeyPair(rawCert, rawKey); err == nil {
				cert = &c
				X509Certs[name] = cert
			} else {
				return nil, fmt.Errorf("Failed to parse certificate with error: %s", err.Error())
			}
		} else {
			return nil, fmt.Errorf("Cert/Key not uploaded yet for [%s]\n", name)
		}
	}
	return cert, nil
}

func AddCACert(name, domain string, cert []byte) {
	if len(cert) > 0 {
		if d, err := base64.StdEncoding.DecodeString(string(cert)); err == nil {
			cert = d
		}
	}
	lock.Lock()
	defer lock.Unlock()
	domainsCert := CACerts[name]
	if domainsCert == nil {
		domainsCert = types.NewPair[map[string]bool, []byte](map[string]bool{}, cert)
		CACerts[name] = domainsCert
	}
	domainsCert.Left[domain] = true
	if len(domainsCert.Right) == 0 {
		domainsCert.Right = cert
	}
	loadCerts()
}

func RemoveCACert(name string) {
	lock.Lock()
	defer lock.Unlock()
	delete(CACerts, name)
	loadCerts()
}

func AddCAKey(name, domain string, key []byte) {
	if len(key) > 0 {
		if d, err := base64.StdEncoding.DecodeString(string(key)); err == nil {
			key = d
		}
	}
	lock.Lock()
	defer lock.Unlock()
	domainsKey := CAKeys[name]
	if domainsKey == nil {
		domainsKey = types.NewPair(map[string]bool{}, key)
		CAKeys[name] = domainsKey
	}
	domainsKey.Left[domain] = true
	if len(domainsKey.Right) == 0 {
		domainsKey.Right = key
	}
}

func RemoveCAKey(name string) {
	lock.Lock()
	defer lock.Unlock()
	delete(CAKeys, name)
}

func loadCerts() {
	RootCAs = x509.NewCertPool()
	if certs, err := filepath.Glob(global.ServerConfig.CertPath + "/*.crt"); err == nil {
		for _, c := range certs {
			if cert, err := os.ReadFile(c); err == nil {
				RootCAs.AppendCertsFromPEM(cert)
			}
		}
	}
	if certs, err := filepath.Glob(global.ServerConfig.CertPath + "/*.pem"); err == nil {
		for _, c := range certs {
			if cert, err := os.ReadFile(c); err == nil {
				RootCAs.AppendCertsFromPEM(cert)
			}
		}
	}
	for _, domainsCert := range CACerts {
		RootCAs.AppendCertsFromPEM(domainsCert.Right)
	}
}

func CreateCertificate(domains []string, spiffeID, saveWithPrefix string) (outCert *tls.Certificate, err error) {
	if len(domains) == 0 {
		return nil, fmt.Errorf("no domains")
	}
	cn := domains[0]
	var rawCACert, rawCAKey []byte
	for _, domainsCert := range CACerts {
		if domainsCert.Left[cn] {
			rawCACert = domainsCert.Right
			break
		}
	}
	for _, domainsKey := range CAKeys {
		if domainsKey.Left[cn] {
			rawCAKey = domainsKey.Right
			break
		}
	}
	return CreateCertificateWithCA(rawCACert, rawCAKey, domains, spiffeID, saveWithPrefix)
}

func CreateCertificateWithCA(rawCACert, rawCAKey []byte, domains []string, spiffeID, saveWithPrefix string) (outCert *tls.Certificate, err error) {
	var spiffeURL *url.URL
	if spiffeID != "" {
		spiffeURL, err = url.Parse(spiffeID)
		if err != nil {
			return nil, err
		}
	}
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	// priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return &tls.Certificate{}, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		return &tls.Certificate{}, fmt.Errorf("failed to marshal public key: %w", err)
	}
	subjectKeyID := sha1.Sum(pubDER)

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.Unix()),
		Subject: pkix.Name{
			CommonName:         domains[0],
			Organization:       []string{domains[0]},
			OrganizationalUnit: []string{domains[0]},
		},
		URIs:                  []*url.URL{},
		DNSNames:              domains,
		NotBefore:             now,
		NotAfter:              now.AddDate(1, 0, 0),
		SubjectKeyId:          subjectKeyID[:],
		BasicConstraintsValid: true,
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature,
	}
	if spiffeURL != nil {
		template.URIs = append(template.URIs, spiffeURL)
	}
	var caCert *x509.Certificate
	var caKey any
	if rawCACert != nil && rawCAKey != nil {
		caCertPEM, _ := pem.Decode(rawCACert)
		caKeyPEM, _ := pem.Decode(rawCAKey)
		caCert, err = x509.ParseCertificate(caCertPEM.Bytes)
		if err != nil {
			fmt.Printf("Failed to parse CA certificate: %v\n", err)
			return
		}
		template.AuthorityKeyId = caCert.SubjectKeyId
		k, err := x509.ParsePKCS8PrivateKey(caKeyPEM.Bytes)
		if err != nil {
			k, err = x509.ParsePKCS1PrivateKey(caKeyPEM.Bytes)
		}
		if err != nil {
			fmt.Printf("Failed to parse CA private key: %v\n", err)
			return nil, err
		}
		caKey = k
	} else {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
		caCert = template
		caKey = priv
	}
	cert, err := x509.CreateCertificate(rand.Reader, template, caCert, priv.Public(), caKey)
	if err != nil {
		return &tls.Certificate{}, err
	}
	if saveWithPrefix != "" {
		folder := filepath.Join(global.ServerConfig.WorkDir)
		if _, err = os.Stat(folder); os.IsNotExist(err) {
			err = os.MkdirAll(folder, os.ModeTemporary)
		}
		if err != nil {
			fmt.Printf("Failed to create path [%s] for writing cert with error: %s\n", saveWithPrefix, err.Error())
		} else {
			saveWithPrefix = util.Sanitize(saveWithPrefix)
			saveWithPrefix = filepath.Join(folder, saveWithPrefix)
			certFile := saveWithPrefix + "-cert.pem"
			if certOut, err := os.Create(certFile); err != nil {
				fmt.Printf("Failed to open file [%s] for writing cert with error: %s\n", certFile, err.Error())
			} else if err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: cert}); err != nil {
				fmt.Printf("Failed to write data to file [%s] with error: %s\n", certFile, err.Error())
			} else if err := certOut.Close(); err != nil {
				fmt.Printf("Error closing file [%s]: %s\n", certFile, err.Error())
			} else {
				fmt.Printf("Saved certificate to file [%s]\n", certFile)
			}
			keyFile := saveWithPrefix + "-key.pem"
			if keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600); err != nil {
				fmt.Printf("Failed to open file [%s] for writing key with error: %s\n", keyFile, err.Error())
			} else if privBytes, err := x509.MarshalPKCS8PrivateKey(priv); err != nil {
				fmt.Printf("Failed to marshal key with error: %s\n", err.Error())
			} else if err = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
				fmt.Printf("Failed to write PRIVATE KEY to file [%s] with error: %s. Will try writing as EC key.\n", keyFile, err.Error())
				if ecBytes, err := x509.MarshalECPrivateKey(priv); err != nil {
					fmt.Printf("Failed to marshal EC key with error: %s\n", err.Error())
				} else if err = pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: ecBytes}); err != nil {
					fmt.Printf("Failed to write EC PRIVATE KEY to file [%s] with error: %s\n", keyFile, err.Error())
				} else if err := keyOut.Close(); err != nil {
					fmt.Printf("Error closing file [%s] with error: %s\n", keyFile, err.Error())
				} else {
					fmt.Printf("Saved EC PRIVATE KEY to file [%s]\n", keyFile)
				}
			} else if err := keyOut.Close(); err != nil {
				fmt.Printf("Error closing file [%s] with error: %s\n", keyFile, err.Error())
			} else {
				fmt.Printf("Saved PRIVATE KEY to file [%s]\n", keyFile)
			}
		}
	}
	outCert = &tls.Certificate{}
	outCert.Certificate = append(outCert.Certificate, cert)
	if caCert != nil && caCert != template {
		outCert.Certificate = append(outCert.Certificate, caCert.Raw)
	}
	outCert.PrivateKey = priv

	return outCert, nil
}

func EncodeX509Cert(cert *tls.Certificate) ([]byte, error) {
	if cert == nil {
		return nil, fmt.Errorf("No cert")
	}
	var buff bytes.Buffer
	w := bufio.NewWriter(&buff)
	for _, cert := range cert.Certificate {
		if err := pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: cert}); err != nil {
			return nil, fmt.Errorf("Failed to encode cert data with error: %s\n", err.Error())
		}
	}
	w.Flush()
	return buff.Bytes(), nil
}

func EncodeX509Key(cert *tls.Certificate) ([]byte, error) {
	if cert == nil {
		return nil, fmt.Errorf("No cert")
	}
	var buff bytes.Buffer
	w := bufio.NewWriter(&buff)
	if privBytes, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey); err != nil {
		return nil, fmt.Errorf("Failed to marshal key with error: %s\n", err.Error())
	} else if err = pem.Encode(w, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return nil, fmt.Errorf("Failed to encode key data with error: %s\n", err.Error())
	}
	w.Flush()
	return buff.Bytes(), nil
}

func pemBlockForKey(priv interface{}) *pem.Block {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			fmt.Printf("Unable to marshal ECDSA private key: %v", err)
			return nil
		}
		return &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	default:
		return nil
	}
}

func ReadTLSSNIFromConn(conn net.Conn) (sni string, buff bytes.Buffer, err error) {
	r := io.TeeReader(conn, &buff)
	tlsBuff, err := readTLSHandshake(r)
	if err != nil {
		return
	}
	tlsBuff, err = getClientHelloData(tlsBuff)
	if err != nil {
		return
	}
	cipherSuites, _ := getClientCipherSuites(tlsBuff)
	fmt.Println(cipherSuites)

	var extensions []byte
	extensions, err = getClientHelloExtensions(tlsBuff)
	if err != nil {
		return
	}
	algos, _ := getSignatureAlgorithms(extensions)
	fmt.Println(algos)
	sni, err = getSNIExtensionEntries(extensions)
	return
}

func readTLSHandshake(r io.Reader) (buff []byte, err error) {
	var hs struct {
		Type, VersionMajor, VersionMinor uint8
		Length                           uint16
	}
	if err = binary.Read(r, binary.BigEndian, &hs); err != nil {
		return
	}
	if hs.Type != 0x16 {
		err = errors.New("Not a TLS Handshake")
		return
	}
	buff = make([]byte, hs.Length)
	_, err = io.ReadFull(r, buff)
	return
}

func getClientHelloData(tlsBuff []byte) ([]byte, error) {
	if tlsBuff[0] != 0x01 {
		return nil, errors.New("Invalid ClientHello")
	}
	buff, _, err := parseByteRecordOfSize(tlsBuff[1:], 3, "Client Hello Header")
	if err != nil {
		return nil, err
	}
	if len(buff) < 34 /*32 client random + 1 session id + 1 cipher suites*/ {
		return nil, errors.New("Invalid ClientHello")
	}
	//skip client random
	buff = buff[34:]
	//skip session data
	_, buff, err = parseByteRecordOfSize(buff, 1, "Session Data")
	if err != nil {
		return nil, err
	}
	return buff, nil
}

func getClientCipherSuites(tlsBuff []byte) ([]string, error) {
	buff, _, err := parseByteRecordOfSize(tlsBuff, 2, "Cipher Suites")
	if err != nil {
		return nil, err
	}
	cipherSuites := []string{}
	for i := 0; i < len(buff); i += 2 {
		if len(buff[i:]) < 2 {
			break
		}
		if buff[i] == 0xcc && buff[i+1] == 0xa8 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256")
		} else if buff[i] == 0xcc && buff[i+1] == 0xa9 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256")
		} else if buff[i] == 0xcc && buff[i+1] == 0xaa {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_CHACHA20_POLY1305_SHA256")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x09 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x0a {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x12 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x13 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x14 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x23 {
			cipherSuites = append(cipherSuites, "ECDHE_ECDSA_WITH_AES_128_CBC_SHA256")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x24 {
			cipherSuites = append(cipherSuites, "ECDHE_ECDSA_WITH_AES_256_CBC_SHA384")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x27 {
			cipherSuites = append(cipherSuites, "ECDHE_RSA_WITH_AES_128_CBC_SHA256")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x28 {
			cipherSuites = append(cipherSuites, "ECDHE_RSA_WITH_AES_256_CBC_SHA384")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x2b {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x2c {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x2f {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256")
		} else if buff[i] == 0xc0 && buff[i+1] == 0x30 {
			cipherSuites = append(cipherSuites, "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384")
		} else if buff[i] == 0x00 && buff[i+1] == 0x0a {
			cipherSuites = append(cipherSuites, "TLS_RSA_WITH_3DES_EDE_CBC_SHA")
		} else if buff[i] == 0x00 && buff[i+1] == 0x2f {
			cipherSuites = append(cipherSuites, "TLS_RSA_WITH_AES_128_CBC_SHA")
		} else if buff[i] == 0x00 && buff[i+1] == 0x33 {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_AES_128_CBC_SHA")
		} else if buff[i] == 0x00 && buff[i+1] == 0x35 {
			cipherSuites = append(cipherSuites, "TLS_RSA_WITH_AES_256_CBC_SHA")
		} else if buff[i] == 0x00 && buff[i+1] == 0x39 {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_AES_256_CBC_SHA")
		} else if buff[i] == 0x00 && buff[i+1] == 0x3c {
			cipherSuites = append(cipherSuites, "RSA_WITH_AES_128_CBC_SHA256")
		} else if buff[i] == 0x00 && buff[i+1] == 0x3d {
			cipherSuites = append(cipherSuites, "RSA_WITH_AES_256_CBC_SHA256")
		} else if buff[i] == 0x00 && buff[i+1] == 0x6b {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_AES_256_CBC_SHA384")
		} else if buff[i] == 0x00 && buff[i+1] == 0x6c {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_AES_128_CBC_SHA256")
		} else if buff[i] == 0x00 && buff[i+1] == 0x9c {
			cipherSuites = append(cipherSuites, "TLS_RSA_WITH_AES_128_GCM_SHA256")
		} else if buff[i] == 0x00 && buff[i+1] == 0x9d {
			cipherSuites = append(cipherSuites, "TLS_RSA_WITH_AES_256_GCM_SHA384")
		} else if buff[i] == 0x00 && buff[i+1] == 0x9e {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_AES_128_GCM_SHA256")
		} else if buff[i] == 0x00 && buff[i+1] == 0x9f {
			cipherSuites = append(cipherSuites, "DHE_RSA_WITH_AES_256_GCM_SHA384")
		} else if buff[i] == 0x00 && buff[i+1] == 0xff {
			cipherSuites = append(cipherSuites, "psuedo-cipher-suite: renegotiation SCSV supported")
		}
	}
	return cipherSuites, nil
}

func getClientHelloExtensions(tlsBuff []byte) ([]byte, error) {
	//skip cipher suite
	_, buff, err := parseByteRecordOfSize(tlsBuff, 2, "Cipher Suites")
	if err != nil {
		return nil, err
	}
	//skip compression
	_, buff, err = parseByteRecordOfSize(buff, 1, "Compression")
	if err != nil {
		return nil, err
	}
	if len(buff) < 2 { //No extensions
		return nil, nil
	}
	buff, _, err = parseByteRecordOfSize(buff, 2, "Client Extensions")
	if err != nil {
		return nil, err
	}
	if len(buff) < 4 {
		return nil, errors.New("Invalid ClientHello Extensions")
	}
	return buff, nil
}

func getSNIExtensionEntries(extensions []byte) (sni string, err error) {
	found := false
	var extData []byte
	for len(extensions) > 0 {
		extType := binary.BigEndian.Uint16(extensions[:2])
		extData, extensions, err = parseByteRecordOfSize(extensions[2:], 2, "Extension Data")
		if extType == 0x0 { //SNI record
			found = true
			break
		}
	}
	if found {
		//While the extension data format is list type, a client can only send atmost 1 server name, so just read 1
		sniListEntry, _, err := parseByteRecordOfSize(extData, 2, "SNI Record")
		if err != nil {
			return "", err
		}
		if sniListEntry[0] == 0x0 { //type of list entry is DNS hostname
			sniEntry, _, err := parseByteRecordOfSize(sniListEntry[1:], 2, "SNI Hostname")
			if err != nil {
				return "", err
			}
			return string(sniEntry), nil
		}
	}
	return
}

func getSignatureAlgorithms(extensions []byte) (algos []string, err error) {
	found := false
	var extData []byte
	for len(extensions) > 0 {
		extType := binary.BigEndian.Uint16(extensions[:2])
		extData, extensions, err = parseByteRecordOfSize(extensions[2:], 2, "Extension Data")
		if extType == 0x0d { //Signature Algorithms
			found = true
			break
		}
	}
	if found {
		//read redundant length again
		length := int(binary.BigEndian.Uint16(extData[:2]))
		extData = extData[2:]
		for i := 0; i < length; i += 2 {
			algo := ""
			if extData[i] == 0x07 {
				switch extData[i+1] {
				case 0x00:
					algo = "RSA/PSS/SHA256"
				case 0x01:
					algo = "RSA/PSS/SHA384"
				case 0x02:
					algo = "RSA/PSS/SHA512"
				case 0x03:
					algo = "EdDSA/ED25519"
				case 0x04:
					algo = "EdDSA/ED448"
				default:
					algo = fmt.Sprintf("%d/%d", extData[i], extData[i+1])
				}
			} else if extData[i] == 0x08 {
				switch extData[i+1] {
				case 0x04:
					algo = "RSASSA-PSS/ED448"
				case 0x05:
					algo = "RSASSA-PSS/SHA384"
				case 0x06:
					algo = "RSASSA-PSS/SHA512"
				default:
					algo = fmt.Sprintf("%d/%d", extData[i], extData[i+1])
				}
			} else {
				switch extData[i+1] {
				case 0x01:
					algo = "RSA/"
				case 0x02:
					algo = "DSA/"
				case 0x03:
					algo = "ECDSA/"
				default:
					algo = "Unknown"
				}
				switch extData[i] {
				case 0x01:
					algo += "MD5"
				case 0x02:
					algo += "SHA1"
				case 0x03:
					algo += "SHA224"
				case 0x04:
					algo += "SHA256"
				case 0x05:
					algo += "SHA384"
				case 0x06:
					algo += "SHA512"
				default:
					algo = fmt.Sprintf("%d/%d", extData[i], extData[i+1])
				}
			}
			algos = append(algos, algo)
		}
	}
	return
}

func parseByteRecordOfSize(buff []byte, sizeLen int, what string) ([]byte, []byte, error) {
	if len(buff) < sizeLen {
		return nil, nil, fmt.Errorf("Buffer doesn't have enough bytes to read size for [%s]", what)
	}
	var size int
	for _, b := range buff[:sizeLen] {
		size = (size << 8) | int(b)
	}
	if len(buff) < size+sizeLen {
		return nil, nil, fmt.Errorf("Buffer doesn't have enough bytes given by the size for [%s]", what)
	}
	return buff[sizeLen : size+sizeLen], buff[size+sizeLen:], nil
}

func GetTLSVersion(tlsState *tls.ConnectionState) string {
	switch tlsState.Version {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS10:
		return "1.0"
	}
	return "???"
}
