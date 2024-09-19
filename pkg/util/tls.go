/**
 * Copyright 2024 uk
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

package util

import (
  "bufio"
  "bytes"
  "crypto/ecdsa"
  "crypto/rand"
  "crypto/rsa"
  "crypto/tls"
  "crypto/x509"
  "crypto/x509/pkix"
  "encoding/binary"
  "encoding/pem"
  "errors"
  "fmt"
  "goto/pkg/global"
  "io"
  "io/ioutil"
  "math/big"
  "net"
  "net/http"
  "os"
  "path/filepath"
  "time"

  "github.com/gorilla/mux"
)

var (
  Handler = ServerHandler{Name: "tls", SetRoutes: SetRoutes}
  RootCAs = x509.NewCertPool()
  CACert  []byte
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  tlsRouter := r.PathPrefix("/tls").Subrouter()
  AddRoute(tlsRouter, "/cacert/add", addCACert, "PUT", "POST")
  AddRoute(tlsRouter, "/cacert/remove", removeCACert, "PUT", "POST")
  AddRouteQ(tlsRouter, "/workdir/set", setWorkDir, "dir", "{dir}", "POST", "PUT")
}

func addCACert(w http.ResponseWriter, r *http.Request) {
  msg := ""
  data := ReadBytes(r.Body)
  if len(data) > 0 {
    StoreCACert(data)
    msg = "CA Cert Stored"
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No Cert Payload"
  }
  fmt.Fprintln(w, msg)
  AddLogMessage(msg, r)
}

func removeCACert(w http.ResponseWriter, r *http.Request) {
  RemoveCACert()
  msg := "CA Cert Removed"
  fmt.Fprintln(w, msg)
  AddLogMessage(msg, r)
}

func setWorkDir(w http.ResponseWriter, r *http.Request) {
  msg := ""
  dir := GetStringParamValue(r, "dir")
  if dir != "" {
    global.WorkDir = dir
    msg = fmt.Sprintf("Working directory set to [%s]", dir)
  } else {
    msg = "Missing directory path"
  }
  fmt.Fprintln(w, msg)
  AddLogMessage(msg, r)
}

func StoreCACert(cert []byte) {
  CACert = cert
  loadCerts()
  RootCAs.AppendCertsFromPEM(cert)
}

func RemoveCACert() {
  CACert = nil
  loadCerts()
}

func loadCerts() {
  RootCAs = x509.NewCertPool()
  found := false
  if certs, err := filepath.Glob(global.CertPath + "/*.crt"); err == nil {
    for _, c := range certs {
      if cert, err := ioutil.ReadFile(c); err == nil {
        RootCAs.AppendCertsFromPEM(cert)
        found = true
      }
    }
  }
  if certs, err := filepath.Glob(global.CertPath + "/*.pem"); err == nil {
    for _, c := range certs {
      if cert, err := ioutil.ReadFile(c); err == nil {
        RootCAs.AppendCertsFromPEM(cert)
        found = true
      }
    }
  }
  if !found {
    RootCAs = nil
  }
}

func CreateCertificate(domain string, saveWithPrefix string) (*tls.Certificate, error) {
  now := time.Now()
  template := &x509.Certificate{
    SerialNumber: big.NewInt(now.Unix()),
    Subject: pkix.Name{
      CommonName:         domain,
      Organization:       []string{domain},
      OrganizationalUnit: []string{domain},
    },
    DNSNames:              []string{domain},
    NotBefore:             now,
    NotAfter:              now.AddDate(1, 0, 0),
    SubjectKeyId:          []byte{113, 117, 105, 99, 107, 115, 101, 114, 118, 101},
    BasicConstraintsValid: true,
    IsCA:                  true,
    ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
    KeyUsage: x509.KeyUsageKeyEncipherment |
      x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
  }

  priv, err := rsa.GenerateKey(rand.Reader, 2048)
  if err != nil {
    return &tls.Certificate{}, err
  }

  cert, err := x509.CreateCertificate(rand.Reader, template, template, priv.Public(), priv)
  if err != nil {
    return &tls.Certificate{}, err
  }
  if saveWithPrefix != "" {
    saveWithPrefix = filepath.Join(global.WorkDir, saveWithPrefix)
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
      fmt.Printf("Failed to write PRIVATE KEY to file [%s] with error: %s. Will try writing as RSA key.\n", keyFile, err.Error())
      if err = pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
        fmt.Printf("Failed to write RSA PRIVATE KEY to file [%s] with error: %s\n", keyFile, err.Error())
      } else if err := keyOut.Close(); err != nil {
        fmt.Printf("Error closing file [%s] with error: %s\n", keyFile, err.Error())
      } else {
        fmt.Printf("Saved RSA PRIVATE KEY to file [%s]\n", keyFile)
      }
    } else if err := keyOut.Close(); err != nil {
      fmt.Printf("Error closing file [%s] with error: %s\n", keyFile, err.Error())
    } else {
      fmt.Printf("Saved PRIVATE KEY to file [%s]\n", keyFile)
    }
  }
  var outCert tls.Certificate
  outCert.Certificate = append(outCert.Certificate, cert)
  outCert.PrivateKey = priv

  return &outCert, nil
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
