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
	"reflect"

	"goto/ctl"
	"goto/pkg/server/listeners"
	"goto/pkg/tls"
)

// clearTLS removes everything that a TLS config added: the certs of its listeners and names, and its CAs.
func clearTLS(tlsConfigs *ctl.TLSConfigs) {
	if tlsConfigs == nil {
		return
	}
	clearCerts(tlsConfigs.Certs)
	clearCAs(tlsConfigs.CACerts)
}

func clearCerts(certs []*ctl.CertConfig) {
	for _, cert := range certs {
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
}

func clearCAs(cas []*ctl.CACertConfig) {
	for _, ca := range cas {
		if ca != nil {
			tls.RemoveCACert(ca.Name)
			// The key goes with the cert: a key that stays is the one that a CA of the same name gets when it is loaded again
			// with another cert, as the key that is there is not replaced.
			tls.RemoveCAKey(ca.Name)
		}
	}
}

// certKey tells what an entry of the certs of a TLS config is for: the listener of its port, or its name.
type certKey struct {
	port int
	name string
}

func indexCerts(tlsConfigs *ctl.TLSConfigs) map[certKey]*ctl.CertConfig {
	index := map[certKey]*ctl.CertConfig{}
	if tlsConfigs != nil {
		for _, cert := range tlsConfigs.Certs {
			if cert != nil {
				index[certKey{cert.Port, cert.Name}] = cert
			}
		}
	}
	return index
}

// certKind tells where the cert of an entry comes from. Entries of the same kind can replace each other in place.
func certKind(cert *ctl.CertConfig) string {
	switch {
	case cert.Cert != "" && cert.Key != "":
		return "path"
	case cert.SpiffeID != "":
		return "spiffe"
	case cert.AutoCert:
		return "auto"
	}
	return ""
}

func sameCAs(a, b []*ctl.CACertConfig) bool {
	return (len(a) == 0 && len(b) == 0) || reflect.DeepEqual(a, b)
}

// diffCerts tells what has to be done to the certs when the TLS config of a file is replaced: the entries of the old
// config that are gone have to be cleared, and the entries of the new one that are new or changed have to be loaded.
//
//   - An entry that is the same in both has been loaded already, and is left alone: clearing it and loading it again
//     restarts its listener (and makes it serve without its cert for a moment) for nothing.
//   - An entry that changed and is of the same kind (SPIFFE, or auto cert) is loaded over the old one, and not cleared
//     first: clearing takes the TLS off the listener and restarts it, and loading restarts it again with the new cert.
//   - Nothing is the same when the CAs or the socket of the SPIFFE certs changed, as the certs of the entries are made
//     from them. Those entries are loaded again.
//   - The entries that have the cert and key in files are always cleared and loaded again, as the files can change
//     under a config that did not.
func diffCerts(old, new *ctl.TLSConfigs) (toClear, toLoad []*ctl.CertConfig) {
	if new == nil {
		return nil, nil
	}
	oldIndex, newIndex := indexCerts(old), indexCerts(new)
	sameBase := old != nil && old.SpiffeSocket == new.SpiffeSocket && sameCAs(old.CACerts, new.CACerts)
	same := func(a, b *ctl.CertConfig) bool {
		return a != nil && b != nil && sameBase && certKind(b) != "path" && reflect.DeepEqual(a, b)
	}
	inPlace := func(a, b *ctl.CertConfig) bool {
		if a == nil || b == nil {
			return false
		}
		kind := certKind(b)
		return kind == certKind(a) && (kind == "spiffe" || kind == "auto")
	}
	for _, cert := range new.Certs {
		if cert != nil && !same(oldIndex[certKey{cert.Port, cert.Name}], cert) {
			toLoad = append(toLoad, cert)
		}
	}
	if old != nil {
		for _, cert := range old.Certs {
			if cert == nil {
				continue
			}
			replacement := newIndex[certKey{cert.Port, cert.Name}]
			if !same(cert, replacement) && !inPlace(cert, replacement) {
				toClear = append(toClear, cert)
			}
		}
	}
	return toClear, toLoad
}

func processTLS(tlsConfigs *ctl.TLSConfigs) {
	if tlsConfigs == nil {
		return
	}
	processTLSCerts(tlsConfigs, tlsConfigs.Certs)
}

// processTLSCerts loads the CAs of a TLS config, and the given entries of its certs.
func processTLSCerts(tlsConfigs *ctl.TLSConfigs, certs []*ctl.CertConfig) {
	if tlsConfigs == nil {
		return
	}
	// An entry of a list in a file can be empty, and the loading of the certs expects every entry to have something in it.
	loadable := &ctl.TLSConfigs{SpiffeSocket: tlsConfigs.SpiffeSocket}
	for _, ca := range tlsConfigs.CACerts {
		if ca != nil {
			loadable.CACerts = append(loadable.CACerts, ca)
		}
	}
	for _, cert := range certs {
		if cert != nil {
			loadable.Certs = append(loadable.Certs, cert)
		}
	}
	loadable.LoadCACerts(false)
	loadable.LoadCerts(false)
}
