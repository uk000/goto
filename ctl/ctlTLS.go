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
	"goto/pkg/util"
	"log"
	"net/http"
	"os"
)

type TLS struct {
	Port      int    `yaml:"port"`
	Key       string `yaml:"key"`
	Cert      string `yaml:"cert"`
	cert, key []byte
}

type PortTLS []*TLS

func processTLS(config *GotoConfig) {
	if len(config.TLS) == 0 {
		log.Println("No TLS configs to configure")
		return
	}
	for _, tlsConfig := range config.TLS {
		tlsConfig.send()
	}
}

func (tls *TLS) send() {
	tls.Load()
	tls.sendCertOrKey("cert")
	tls.sendCertOrKey("key")
	reopenListener(tls.Port)
}

func (tls *TLS) Load() (cert, key []byte, err error) {
	if tls.cert, err = os.ReadFile(tls.Cert); err != nil {
		return nil, nil, err
	}
	if tls.key, err = os.ReadFile(tls.Key); err != nil {
		return nil, nil, err
	}
	return tls.cert, tls.key, nil
}

func (tls *TLS) sendCertOrKey(certOrKey string) {
	url := fmt.Sprintf("%s/server/listeners/%d/%s/add", currentContext.RemoteGotoURL, tls.Port, certOrKey)
	log.Printf("Sending TLS %s to URL [%s]\n", certOrKey, url)
	var data []byte
	if certOrKey == "key" {
		data = tls.key
	} else {
		data = tls.cert
	}
	if len(data) == 0 {
		log.Printf("No %s to send\n", certOrKey)
		return
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
