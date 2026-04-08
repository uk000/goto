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
	"log"
)

func clearTLS(portTLS ctl.PortTLS) {
	for _, tls := range portTLS {
		listeners.RemoveListenerCert(tls.Port)
	}
}

func processTLS(portTLS ctl.PortTLS) {
	if len(portTLS) == 0 {
		log.Println("No TLS configs to configure")
		return
	}
	for _, tls := range portTLS {
		l := listeners.GetListenerForPort(tls.Port)
		if l == nil {
			log.Printf("No Listener on Port [%d]", tls.Port)
			continue
		}
		log.Printf("Loading TLS Cert from path [%s]", tls.Cert)
		cert, key, err := tls.Load()
		if err != nil {
			log.Printf("Failed to read TLS cert file [%s] with error: %s\n", tls.Cert, err.Error())
			continue
		}
		if err = listeners.AddListenerCert(tls.Port, key, cert, true); err != nil {
			log.Printf("Failed to add listener cert for port [%d] cert [%s] key [%s] with error: %s\n", tls.Port, tls.Cert, tls.Key, err.Error())
		}
		log.Println("============================================================")
		log.Printf("Loaded TLS cert for port [%d]", tls.Port)
		log.Println("============================================================")

	}
}
