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
)

func clearTLS(tlsConfigs *ctl.TLSConfigs) {
	if tlsConfigs == nil {
		return
	}
	for _, cert := range tlsConfigs.Certs {
		if cert == nil {
			continue
		}
		if cert.Port > 0 {
			listeners.RemoveListenerCert(cert.Port)
		} else if cert.Name != "" {
			// A cert that is not of a listener is kept by its name, and it is gone with the config that brought it.
			tls.RemoveCert(cert.Name)
			tls.RemoveKey(cert.Name)
		}
	}
	for _, ca := range tlsConfigs.CACerts {
		if ca != nil {
			tls.RemoveCACert(ca.Name)
			// The key goes with the cert: a key that stays is the one that a CA of the same name gets when it is loaded again
			// with another cert, as the key that is there is not replaced.
			tls.RemoveCAKey(ca.Name)
		}
	}
}

func processTLS(tls *ctl.TLSConfigs) {
	if tls == nil {
		return
	}
	// An entry of a list in a file can be empty, and the loading of the certs expects every entry to have something in it.
	loadable := &ctl.TLSConfigs{SpiffeSocket: tls.SpiffeSocket}
	for _, ca := range tls.CACerts {
		if ca != nil {
			loadable.CACerts = append(loadable.CACerts, ca)
		}
	}
	for _, cert := range tls.Certs {
		if cert != nil {
			loadable.Certs = append(loadable.Certs, cert)
		}
	}
	loadable.LoadCACerts(false)
	loadable.LoadCerts(false)
}
