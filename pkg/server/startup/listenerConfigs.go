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

// isDefaultPort tells if the port is one of the ports of the server itself, which configs can't reconfigure or remove.
func isDefaultPort(port int) bool {
	return port == global.Self.JSONRPCPort || port == global.Self.GRPCPort || port == global.Self.ServerPort
}

// removeLiveListener takes a config's hold off the port: a port that is one of the startup arguments (--ports) goes back
// to the listener that they define, and any other port has its live listener closed and removed.
//
// The live listener is not necessarily the listener of the config: a config whose listener did not change is not
// applied, so the live one is whichever was opened before (at startup, by an earlier config, or through the API), and
// the one of the config was never opened. Closing that one and removing the port would leave the live one open and
// unreachable, and the port taken for good.
func removeLiveListener(port int) {
	if isDefaultPort(port) {
		log.Printf("[ERROR]: Default Port [%d] cannot be removed", port)
		return
	}
	if listeners.GetListenerForPort(port) == nil {
		return
	}
	if listeners.RestoreInitialListener(port) {
		log.Printf("----------------- Listener [%d] is back to the startup arguments. --------------", port)
		return
	}
	log.Printf("----------------- Removing Listener [%d]. --------------", port)
	if l := listeners.GetListenerForPort(port); l != nil {
		l.Close()
		listeners.RemoveListener(l)
	}
}

func clearListeners(ll ctl.Listeners) {
	if ll == nil {
		return
	}
	for port, l := range ll {
		if l == nil {
			continue
		}
		removeLiveListener(port)
	}
	// the listeners that forward to the ones that were removed or restored have to follow them
	listeners.LinkForwardListeners()
}

func clearRemovedListeners(old, new ctl.Listeners) {
	if old == nil || new == nil {
		return
	}
	for port, ol := range old {
		if ol != nil && new[port] == nil {
			removeLiveListener(port)
		}
	}
	listeners.LinkForwardListeners()
}

// certOfPort gives the entry of the certs of the TLS configs that is for the port, and the TLS config that it is in.
func certOfPort(port int) (tlsConfig *ctl.TLSConfigs, cert *ctl.CertConfig) {
	for _, tc := range tlsConfigs {
		for _, c := range tc.Certs {
			if c != nil && c.Port == port {
				tlsConfig, cert = tc, c
			}
		}
	}
	return tlsConfig, cert
}

// applyCert gives the config of a listener what its entry of the certs makes of the listener once it is loaded (see
// CertConfig.LoadSpiffeCert), so that the config is compared with the live listener as it is made, and not as it was
// before the cert was applied to it: the common name that the entry gives is not the one that the config has, and a
// listener that did not change would otherwise differ from its config every time that it is loaded, and be restarted.
func applyCert(l *listeners.Listener, cert *ctl.CertConfig) {
	if cert == nil {
		return
	}
	l.SpiffeID = cert.SpiffeID
	if cert.Domain != "" {
		l.CommonName = cert.Domain
	}
	l.AltNames = cert.SAN
}

func processListeners(ll ctl.Listeners) {
	if ll == nil {
		return
	}
	for port, nl := range ll {
		if nl == nil {
			log.Printf("[ERROR]: Listener [%d] has no config", port)
			continue
		}
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
			tlsConfig, lc := certOfPort(nl.Port)
			applyCert(nl, lc)
			existing := listeners.GetListenerForPort(port)
			if listeners.Diff(existing, nl) {
				if existing != nil {
					log.Printf("----------------- Listener [%d] changed. Reloading. --------------", nl.Port)
				} else {
					log.Printf("----------------- Adding Listener [%d]. --------------", nl.Port)
				}
				listeners.AddOrUpdateListener(nl)
				if tlsConfig != nil {
					// the certs of this listener only: the ones of the other listeners are on listeners that did not change
					for _, cc := range tlsConfig.Certs {
						if cc != nil && cc.Port == nl.Port {
							cc.LoadSpiffeCert(false)
						}
					}
				}
				log.Printf("----------------- Processed Listener [%d] Successfully. --------------", nl.Port)
			} else {
				// The listener is already live as the config wants it, and the config of it is not applied. The config
				// is kept by the file (this is the map of the file), so it must keep the live listener, and not the one
				// that was never opened: the live one is what the file's removal has to act on.
				ll[port] = existing
			}
		}
	}
	listeners.LinkForwardListeners()
}
