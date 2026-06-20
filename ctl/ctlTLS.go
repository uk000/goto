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
	"goto/pkg/server/listeners"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"log"
	"net/http"
	"os"
)

type CertConfig struct {
	Port     int      `yaml:"port"`
	Name     string   `yaml:"name"`
	AutoCert bool     `yaml:"autoCert"`
	Domain   string   `yaml:"domain"`
	SAN      []string `yaml:"san"`
	SpiffeID string   `yaml:"spiffeID"`
	Key      string   `yaml:"key"`
	Cert     string   `yaml:"cert"`
}

type CACertConfig struct {
	Name   string `yaml:"name"`
	Domain string `yaml:"domain"`
	Key    string `yaml:"key"`
	Cert   string `yaml:"cert"`
}

type TLSConfigs struct {
	Certs        []*CertConfig   `yaml:"certs"`
	CACerts      []*CACertConfig `yaml:"caCerts"`
	SpiffeSocket string          `yaml:"spiffeSocket"`
}

func processTLS(tls *TLSConfigs) {
	if tls == nil || ((len(tls.Certs) == 0) && (len(tls.CACerts) == 0)) {
		log.Println("No TLS configs to configure")
		return
	}
	tls.LoadCerts(true)
}

func (tls *TLSConfigs) LoadCACerts(remote bool) {
	log.Println("-------- CA Certs --------")
	if len(tls.CACerts) == 0 {
		log.Println("No Cert configs to configure")
		return
	}
	for _, caCert := range tls.CACerts {
		if caCert.Name == "" || caCert.Domain == "" {
			log.Println("Name and Domain are required")
			continue
		}
		if remote {

		} else {
			gototls.AddCAKey(caCert.Name, caCert.Domain, []byte(caCert.Key))
			gototls.AddCACert(caCert.Name, caCert.Domain, []byte(caCert.Cert))
			log.Printf("Stored CA cert for Name [%s] Domain [%s]", caCert.Name, caCert.Domain)
		}
	}
	log.Println("------------------")
}

func (tls *TLSConfigs) LoadCerts(remote bool) {
	log.Println("------------------ Certs ------------------")
	if len(tls.Certs) == 0 {
		log.Println("No Certs to configure")
		return
	}
	for _, cc := range tls.Certs {
		if cc.Port == 0 && cc.Name == "" {
			log.Println("OneOf Port and Name are required")
			continue
		}
		if cc.SpiffeID == "" && !cc.AutoCert && (cc.Cert == "" || cc.Key == "") {
			log.Printf("Cert and Key are required without Spiffe/AutoCert for Name [%s] Port [%d]\n", cc.Name, cc.Port)
			continue
		}
		if cc.Cert != "" && cc.Key != "" {
			cc.LoadFromPath(remote)
		} else if cc.SpiffeID != "" {
			gototls.Spiffe.Add(cc.Name, cc.Domain, cc.SpiffeID)
		} else if cc.AutoCert {
			cc.LoadAutoCert(remote)
		}
	}
	if !gototls.Spiffe.IsEmpty() {
		if err := gototls.Spiffe.LoadSpiffeCerts(tls.SpiffeSocket); err != nil {
			log.Printf("Failed to load Spiffe Cert. Socket Address [%s]. Requested Spiffe IDs: %+v\n", tls.SpiffeSocket, gototls.Spiffe.ByIDNameDomain)
			return
		}
		for _, cc := range tls.Certs {
			cc.LoadSpiffeCert(remote)
		}
	}
	log.Println("------------------------------------")
}

func (cc *CertConfig) LoadSpiffeCert(remote bool) {
	if cc.Port > 0 {
		if remote {
			//No remote loading of spiffe certs yet
		} else {
			l := listeners.GetListenerForPort(cc.Port)
			if l == nil {
				log.Printf("No Listener present to load cert for port [%d]", cc.Port)
				return
			}
			if certs, err := gototls.GetCerts(cc.Domain); err == nil {
				l.SpiffeID = cc.SpiffeID
				l.CommonName = cc.Domain
				l.AltNames = cc.SAN
				l.SetCertificates(certs)
			}
			if !l.ReopenListener() {
				log.Printf("Failed to reopen listener on port [%d]\n", l.Port)
			} else {
				log.Printf("Listener reopened with Spiffe Cert (CN: [%s], SPIFFE: [%s], SAN: %s) on port [%d]", cc.Domain, cc.SpiffeID, cc.SAN, cc.Port)
			}
		}
	} else {
		if _, err := gototls.GetCerts(cc.Name); err == nil {
			log.Printf("Loaded Client Spiffe Cert (CN: [%s], SPIFFE: [%s], SAN: %s) on port [%d]", cc.Domain, cc.SpiffeID, cc.SAN, cc.Port)
		}
	}
}

func (cc *CertConfig) LoadAutoCert(remote bool) {
	if cc.Port > 0 {
		if remote {
			//No remote loading of auto certs yet
		} else {
			l := listeners.GetListenerForPort(cc.Port)
			if l == nil {
				log.Printf("[*** ERROR ***] No Listener on Port [%d]\n", cc.Port)
				return
			}
			l.CommonName = cc.Domain
			l.AltNames = cc.SAN
			l.AutoCert = true
			if !l.ReopenListener() {
				log.Printf("Failed to reopen listener on port [%d]\n", l.Port)
			} else {
				log.Printf("Listener reopened with AutoCert (CN: [%s], SPIFFE: [%s], SAN: %s) on port [%d]", cc.Domain, cc.SpiffeID, cc.SAN, cc.Port)
			}
		}
	} else {
		if remote {
			//No remote loading of client auto certs yet
		} else {
			if err := gototls.AddAutoCert(cc.Name, cc.Domain, cc.SAN, cc.SpiffeID); err != nil {
				log.Printf("Failed to store Auto Cert for name [%s] with error: %s", cc.Name, err.Error())
			}
		}
	}
}

func (cc *CertConfig) LoadFromPath(remote bool) {
	var err error
	var cert, key []byte
	log.Printf("Loading TLS Cert from path [%s][%s]\n", cc.Cert, cc.Key)
	if cert, err = os.ReadFile(cc.Cert); err != nil {
		log.Printf("[*** ERROR ***] Failed to read TLS Cert file [%s] with error: %s\n", cc.Cert, err.Error())
		return
	}
	if key, err = os.ReadFile(cc.Key); err != nil {
		log.Printf("[*** ERROR ***] Failed to read TLS Key file [%s] with error: %s\n", cc.Key, err.Error())
		return
	}
	if cc.Port > 0 {
		if remote {
			cc.sendCertOrKey("cert", cert)
			cc.sendCertOrKey("key", key)
			reopenListener(cc.Port)
		} else if err = listeners.AddListenerCert(cc.Port, key, cert, false); err != nil {
			log.Printf("[*** ERROR ***] Failed to add listener cert for port [%d] cert [%s] key [%s] with error: %s\n", cc.Port, cc.Cert, cc.Key, err.Error())
			return
		}
		log.Printf("Loaded TLS cert for port [%d]", cc.Port)
	} else {
		gototls.AddKey(cc.Name, key)
		gototls.AddCert(cc.Name, cert)
		log.Printf("TLS Cert and Key loaded successfully for [%s]\n", cc.Name)
	}
}

func (cc *CertConfig) sendCertOrKey(certOrKey string, data []byte) {
	url := fmt.Sprintf("%s/server/listeners/%d", currentContext.RemoteGotoURL, cc.Port)
	if cc.SpiffeID != "" {
		url = fmt.Sprintf("%s/cert/auto/%s?spiffeID=%s", url, cc.Domain, cc.SpiffeID)
	} else {
		url = fmt.Sprintf("%s/%s/add", url, certOrKey)
	}
	log.Printf("Sending TLS %s to URL [%s]\n", certOrKey, url)
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
