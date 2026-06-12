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

package startup

import (
	"goto/ctl"
	"goto/pkg/server/listeners"
	"goto/pkg/tls"
	gototls "goto/pkg/tls"
	"log"
)

func clearTLS(tlsConfigs *ctl.TLSConfigs) {
	for _, tls := range tlsConfigs.Certs {
		listeners.RemoveListenerCert(tls.Port)
	}
	for _, ca := range tlsConfigs.CACerts {
		tls.RemoveCACert(ca.Name)
	}
}

func processTLS(tls *ctl.TLSConfigs) {
	processCACerts(tls.CACerts)
	processCerts(tls.Certs)
}

func processCerts(certConfs []*ctl.CertConfig) {
	log.Println("============================ TLS ================================")
	if len(certConfs) == 0 {
		log.Println("No Cert configs to configure")
		return
	}
	for _, certConf := range certConfs {
		if certConf.Port == 0 && certConf.Name == "" {
			log.Println("OneOf Port and Name are required")
			continue
		}
		if !certConf.AutoCert && (certConf.Cert == "" || certConf.Key == "") {
			log.Printf("Cert and Key are required without AutoCert for Name [%s] Port [%d]\n", certConf.Name, certConf.Port)
			continue
		}
		var cert, key []byte
		var err error
		if !certConf.AutoCert {
			log.Printf("Loading TLS Cert from path [%s][%s]\n", certConf.Cert, certConf.Key)
			cert, key, err = certConf.Load()
			if err != nil {
				log.Printf("[*** ERROR ***] Failed to read TLS cert files [%s][%s] with error: %s\n", certConf.Cert, certConf.Key, err.Error())
				continue
			}
		}
		if certConf.Port > 0 {
			l := listeners.GetListenerForPort(certConf.Port)
			if l == nil {
				log.Printf("[*** ERROR ***] No Listener on Port [%d]\n", certConf.Port)
				continue
			}
			if certConf.AutoCert {
				l.CommonName = certConf.CommonName
				l.SpiffeID = certConf.SpiffeID
				l.AltNames = certConf.AltNames
				l.AutoCert = true
				if !l.ReopenListener() {
					log.Printf("Failed to reopen listener on port [%d]\n", l.Port)
				} else {
					log.Printf("Listener reopened with AutoCert (CN: [%s], SPIFFE: [%s], SAN: %s) on port [%d]", certConf.CommonName, certConf.SpiffeID, certConf.AltNames, certConf.Port)
				}
			} else {
				if err = listeners.AddListenerCert(certConf.Port, key, cert, true); err != nil {
					log.Printf("[*** ERROR ***] Failed to add listener cert for port [%d] cert [%s] key [%s] with error: %s\n", certConf.Port, certConf.Cert, certConf.Key, err.Error())
				} else {
					log.Printf("Loaded TLS cert for port [%d]", certConf.Port)
				}
			}
		} else {
			if certConf.AutoCert {
				if err := gototls.AddAutoCert(certConf.Name, certConf.CommonName, certConf.AltNames, certConf.SpiffeID); err != nil {
					log.Printf("Failed to store Auto Cert for name [%s] with error: %s", certConf.Name, err.Error())
					continue
				}
			} else {
				gototls.AddKey(certConf.Name, key)
				gototls.AddCert(certConf.Name, cert)
			}
			log.Printf("Stored TLS cert for name [%s]", certConf.Name)
		}
		log.Println("============================================================")
	}
}

func processCACerts(caCerts []*ctl.CACertConfig) {
	log.Println("============================ CA Certs ================================")
	if len(caCerts) == 0 {
		log.Println("No Cert configs to configure")
		return
	}
	for _, caCert := range caCerts {
		if caCert.Name == "" || caCert.Domain == "" {
			log.Println("Name and Domain are required")
			continue
		}
		gototls.AddCAKey(caCert.Name, caCert.Domain, []byte(caCert.Key))
		gototls.AddCACert(caCert.Name, caCert.Domain, []byte(caCert.Cert))
		log.Printf("Stored CA cert for Name [%s] Domain [%s]", caCert.Name, caCert.Domain)
	}
	log.Println("============================================================")
}
