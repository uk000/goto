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
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"log"
)

func clearListeners(ll ctl.Listeners) {
	if ll == nil {
		return
	}
	for _, l := range ll {
		log.Printf("----------------- Removing Listener [%d]. --------------", l.Port)
		l.Close()
		listeners.RemoveListener(l)
	}
}

func clearRemovedListeners(old, new ctl.Listeners) {
	if old == nil || new == nil {
		return
	}
	for port, ol := range old {
		if new[port] == nil {
			log.Printf("----------------- Removing Listener [%d]. --------------", port)
			ol.Close()
			listeners.RemoveListener(ol)
		}
	}
}

func processListeners(ll ctl.Listeners) {
	if ll == nil {
		return
	}
	for port, nl := range ll {
		switch port {
		case global.Self.JSONRPCPort:
			log.Printf("[ERROR]: Default JSONRPC Port [%d] cannot be reconfigured", port)
		case global.Self.GRPCPort:
			log.Printf("[ERROR]: Default gRPC Port [%d] cannot be reconfigured", port)
		case global.Self.ServerPort:
			log.Printf("[ERROR]: Default HTTP Port [%d] cannot be reconfigured", port)
		default:
			nl.Port = port
			if nl.CommonName == "" {
				nl.CommonName = global.ServerConfig.CommonName
			}
			existing := listeners.GetListenerForPort(port)
			if listeners.Diff(existing, nl) {
				if existing != nil {
					log.Printf("----------------- Listener [%d] changed. Reloading. --------------", nl.Port)
				} else {
					log.Printf("----------------- Adding Listener [%d]. --------------", nl.Port)
				}
				listeners.AddOrUpdateListener(nl)
				var tlsConfig *ctl.TLSConfigs
				for _, tls := range tlsConfigs {
					for _, c := range tls.Certs {
						if c.Port == nl.Port {
							tlsConfig = tls
						}
					}
				}
				if tlsConfig != nil {
					for _, cc := range tlsConfig.Certs {
						cc.LoadSpiffeCert(false)
					}
				}
				log.Printf("----------------- Processed Listener [%d] Successfully. --------------", nl.Port)
			}
		}
	}
	listeners.LinkForwardListeners()
}
