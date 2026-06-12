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

package ctl

import (
	"bytes"
	"fmt"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"log"
	"net/http"
	"os"
)

type CertConfig struct {
	Port       int      `yaml:"port"`
	Name       string   `yaml:"name"`
	AutoCert   bool     `yaml:"autoCert"`
	CommonName string   `yaml:"commonName"`
	AltNames   []string `yaml:"altNames"`
	SpiffeID   string   `yaml:"spiffeID"`
	Key        string   `yaml:"key"`
	Cert       string   `yaml:"cert"`
	cert, key  []byte
}

type CACertConfig struct {
	Name   string `yaml:"name"`
	Domain string `yaml:"domain"`
	Key    string `yaml:"key"`
	Cert   string `yaml:"cert"`
}

type TLSConfigs struct {
	Certs   []*CertConfig   `yaml:"certs"`
	CACerts []*CACertConfig `yaml:"caCerts"`
}

func processTLS(config *GotoConfig) {
	if config.TLS == nil || (len(config.TLS.Certs) == 0) {
		log.Println("No TLS configs to configure")
		return
	}
	for _, tlsConfig := range config.TLS.Certs {
		tlsConfig.load()
	}
}

func (tls *CertConfig) load() {
	if tls.Port == 0 && tls.Name == "" {
		log.Println("OneOf Port and Name are required")
		return
	}
	if tls.Cert == "" || tls.Key == "" {
		log.Printf("Cert and Key are required for Name [%s] Port [%d]\n", tls.Name, tls.Port)
		return
	}
	tls.Load()
	if tls.Port > 0 {
		tls.sendCertOrKey("cert")
		tls.sendCertOrKey("key")
		reopenListener(tls.Port)
	} else if tls.Name != "" {
		gototls.AddCert(tls.Name, tls.cert)
		gototls.AddKey(tls.Name, tls.key)
		log.Printf("TLS Cert and Key loaded successfully for [%s]\n", tls.Name)
	}
}

func (tls *CertConfig) Load() (cert, key []byte, err error) {
	if tls.cert, err = os.ReadFile(tls.Cert); err != nil {
		return nil, nil, err
	}
	if tls.key, err = os.ReadFile(tls.Key); err != nil {
		return nil, nil, err
	}
	return tls.cert, tls.key, nil
}

func (tls *CertConfig) sendCertOrKey(certOrKey string) {
	url := fmt.Sprintf("%s/server/listeners/%d", currentContext.RemoteGotoURL, tls.Port)
	if tls.SpiffeID != "" {
		url = fmt.Sprintf("%s/cert/auto/%s?spiffeID=%s", url, tls.CommonName, tls.SpiffeID)
	} else {
		url = fmt.Sprintf("%s/%s/add", url, certOrKey)
	}
	log.Printf("Sending TLS %s to URL [%s]\n", certOrKey, url)
	var data []byte
	if certOrKey == "key" {
		data = tls.key
	} else {
		data = tls.cert
	}
	if len(data) == 0 {
		log.Printf("No %s to send\n", certOrKey)
	}
	resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		log.Printf("Failed to send TLS %s to URL [%s]. Error [%s]n", certOrKey, url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Non-OK status for TLS %s from URL [%s]: %s\n", certOrKey, url, resp.Status)
	} else {
		log.Printf("TLS %s sent successfully to URL [%s]. Response: [%s]\n", certOrKey, url, util.Read(resp.Body))
	}
}

func reopenListener(port int) {
	url := fmt.Sprintf("%s/server/listeners/%d/reopen", currentContext.RemoteGotoURL, port)
	log.Printf("Sending request to reopen listener [%d] to URL [%s]\n", port, url)
	resp, err := http.Post(url, "application/octet-stream", http.NoBody)
	if err != nil {
		log.Printf("Failed to reopen listener [%d] on URL [%s]. Error [%s]n", port, url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Non-OK status for Listener [%d] from URL [%s]: %s\n", port, url, resp.Status)
	} else {
		log.Printf("Listener [%d] reopened successfully. Response: [%s]\n", port, util.Read(resp.Body))
	}
}

func (c *CACertConfig) load() {
	if c.Name == "" && c.Domain == "" {
		log.Println("Name and Domain are required")
		return
	}
	if c.Cert == "" || c.Key == "" {
		log.Printf("Cert and Key are required for Name [%s] Domain [%d]\n", c.Name, c.Domain)
		return
	}
	c.sendCACertOrKey("cert")
	c.sendCACertOrKey("key")
	log.Printf("CA Cert and Key loaded successfully for Name [%s] Domain [%s]\n", c.Name, c.Domain)
}

func (c *CACertConfig) sendCACertOrKey(certOrKey string) {
	url := fmt.Sprintf("%s/tls/ca/cert/add/%s/%s", currentContext.RemoteGotoURL, c.Name, c.Domain)
	log.Printf("Sending CA %s to URL [%s]\n", certOrKey, url)
	var data []byte
	if certOrKey == "key" {
		data = []byte(c.Key)
	} else {
		data = []byte(c.Cert)
	}
	if len(data) == 0 {
		log.Printf("No %s to send\n", certOrKey)
	}
	resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		log.Printf("Failed to send TLS %s to URL [%s]. Error [%s]n", certOrKey, url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Non-OK status for TLS %s from URL [%s]: %s\n", certOrKey, url, resp.Status)
	} else {
		log.Printf("TLS %s sent successfully to URL [%s]. Response: [%s]\n", certOrKey, url, util.Read(resp.Body))
	}
}
