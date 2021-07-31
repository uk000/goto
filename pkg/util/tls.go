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
  "encoding/pem"
  "fmt"
  "math/big"
  "os"
  "time"
)

func CreateCertificate(domain string, saveWithPrefix string) (*tls.Certificate, error) {
  now := time.Now()
  template := &x509.Certificate{
    SerialNumber: big.NewInt(now.Unix()),
    Subject: pkix.Name{
      CommonName:         domain,
      Organization:       []string{domain},
      OrganizationalUnit: []string{domain},
    },
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
