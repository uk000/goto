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
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goto/ctl"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	gototls "goto/pkg/tls"
)

// ---- the listeners of a file that has certs for them ----
//
// A file with the entries of the certs of its listeners is loaded again every time that the files of its config map are
// updated. The listeners that the update did not change must not be restarted by it: restarting one makes it serve
// without its cert for a moment, and drops its connections.

const (
	certsDomain8443 = "goto.uk3"
	certsDomain9443 = "goto.uk3"
)

// certsConfigYAML is a config the shape of the one of a deployment: an mTLS listener with ALPN, an https one, the CA, and
// the entries of the certs of a client and of the two listeners.
func certsConfigYAML(t *testing.T, ca certsCA, mtlsPort, httpsPort int, mtlsDomain string, extraListeners string, withHTTPSCert bool) string {
	t.Helper()
	caCert, caKey := ca.cert, ca.key
	httpsEntry := ""
	if withHTTPSCert {
		httpsEntry = fmt.Sprintf("  - port: %d\n    autoCert: true\n    domain: %q\n    san: [\"goto.uk3.9443\"]\n", httpsPort, certsDomain9443)
	}
	return fmt.Sprintf(`
listeners:
  %d:
    label: listener-mtls
    protocol: https
    open: true
    tls: true
    mtls: true
    verifyClientCert: false
    alpn:
      protos: ["istio", "istio-h2", "istio-peer-exchange"]
      keepDefault: true
  %d:
    label: listener-https
    protocol: https
    open: true
    tls: true
%stls:
  caCerts:
  - name: certs-ca
    domain: goto.goto
    cert: %q
    key: %q
  certs:
  - name: clientCert
    domain: goto.uk3
    san: ["goto.uk3.client"]
    autoCert: true
  - port: %d
    autoCert: true
    domain: %q
    san: ["goto.uk3.8443"]
%s`, mtlsPort, httpsPort, extraListeners, caCert, caKey, mtlsPort, mtlsDomain, httpsEntry)
}

// certsCA is the CA of a test. The configs of a test have to have the same one: a change of the CAs of a file loads all of
// its certs again.
type certsCA struct{ cert, key string }

func newCertsCA(t *testing.T) certsCA {
	t.Helper()
	cert, key := selfSigned(t, "ca.certs.test")
	forgetCA(t, "certs-ca")
	return certsCA{cert, key}
}

type liveState struct {
	l          *listeners.Listener
	generation int
	commonName string
}

func stateOf(t *testing.T, port int) liveState {
	t.Helper()
	l := mustListener(t, port)
	return liveState{l, l.Generation, l.CommonName}
}

// requireUntouched fails the test if the listener of the port is not the one that it was, restarted or reconfigured.
func requireUntouched(t *testing.T, port int, before liveState) {
	t.Helper()
	l := mustListener(t, port)
	if l != before.l || l.Generation != before.generation || l.CommonName != before.commonName {
		t.Errorf("listener of port %d was touched: now %p generation %d common name %q, was %p generation %d common name %q",
			port, l, l.Generation, l.CommonName, before.l, before.generation, before.commonName)
	}
	if !l.TLS || !isAccepting(port) {
		t.Errorf("listener of port %d: tls %t, accepting %t, want both", port, l.TLS, isAccepting(port))
	}
}

func TestReloadOfAFileWithCertsLeavesUnchangedListenersAlone(t *testing.T) {
	resetState(t)
	ca := newCertsCA(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	yamlText := certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true)
	loadConfig(parse(t, "tls.yaml", yamlText))
	mtls, https := stateOf(t, mtlsPort), stateOf(t, httpsPort)
	if mtls.commonName != certsDomain8443 || https.commonName != certsDomain9443 || !mtls.l.TLS || !https.l.TLS {
		t.Fatalf("the certs were not applied to the listeners: common names %q, %q", mtls.commonName, https.commonName)
	}

	logged := captureLog(t)
	loadConfig(parse(t, "tls.yaml", yamlText))
	requireUntouched(t, mtlsPort, mtls)
	requireUntouched(t, httpsPort, https)
	for _, unwanted := range []string{"Closing", "Reopened", "changed. Reloading", "Adding Listener"} {
		if strings.Contains(logged.String(), unwanted) {
			t.Errorf("loading a file that did not change was logged as %q:\n%s", unwanted, logged.String())
		}
	}
}

func TestReloadOfAFileWithACertChangedRestartsOnlyItsListener(t *testing.T) {
	resetState(t)
	ca := newCertsCA(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true)))
	mtls, https := stateOf(t, mtlsPort), stateOf(t, httpsPort)

	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, "changed.uk3", "", true)))
	requireUntouched(t, httpsPort, https)
	l := mustListener(t, mtlsPort)
	if l.CommonName != "changed.uk3" || !l.TLS || !l.MTLS {
		t.Errorf("listener of the changed cert: common name %q, tls %t, mtls %t, want the changed domain with its TLS and mTLS kept", l.CommonName, l.TLS, l.MTLS)
	}
	if l == mtls.l && l.Generation == mtls.generation {
		t.Error("the listener of the cert that changed was not restarted to use it")
	}
	if !isAccepting(mtlsPort) {
		t.Errorf("port %d is not open", mtlsPort)
	}
}

func TestReloadOfAFileThatDropsACertGivesItsListenerBackItsDefaults(t *testing.T) {
	resetState(t)
	ca := newCertsCA(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true)))
	mtls := stateOf(t, mtlsPort)

	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", false)))
	requireUntouched(t, mtlsPort, mtls)
	l := mustListener(t, httpsPort)
	if l.CommonName != global.ServerConfig.CommonName || !l.TLS {
		t.Errorf("listener of the cert that was dropped: common name %q, tls %t, want the default common name, and TLS from its own config", l.CommonName, l.TLS)
	}
	if !isAccepting(httpsPort) {
		t.Errorf("port %d is not open", httpsPort)
	}
}

func TestReloadThatChangesAListenerLeavesTheOtherListenersWithCertsAlone(t *testing.T) {
	resetState(t)
	ca := newCertsCA(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true)))
	mtls, https := stateOf(t, mtlsPort), stateOf(t, httpsPort)

	// the https listener forwards to another port now, which changes it, and nothing else
	changed := strings.Replace(certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true),
		"    label: listener-https\n", fmt.Sprintf("    label: listener-https\n    forward: %d\n", newPort()), 1)
	loadConfig(parse(t, "tls.yaml", changed))
	requireUntouched(t, mtlsPort, mtls)
	l := mustListener(t, httpsPort)
	if l == https.l || l.ForwardPort == 0 {
		t.Errorf("the listener that changed was not reloaded: %p (was %p), forward %d", l, https.l, l.ForwardPort)
	}
	if l.CommonName != certsDomain9443 || !l.TLS {
		t.Errorf("listener that changed: common name %q, tls %t, want the cert of its entry to be applied to it", l.CommonName, l.TLS)
	}
}

func TestWatchedUpdateOfAVolumeLeavesListenersWithCertsAlone(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	ca := newCertsCA(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	yamlText := certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true)
	v := newVolume(t, map[string]string{"goto-tls-config.yaml": yamlText})
	global.ServerConfig.ConfigPaths = []string{v.dir}
	reconcileConfigs()
	mtls, https := stateOf(t, mtlsPort), stateOf(t, httpsPort)
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	// another config map of the volume is updated, which updates all of its files
	other := newPort()
	newDir, oldDir := v.update(map[string]string{
		"goto-tls-config.yaml": yamlText,
		"goto-other.yaml":      httpYAML(other, "/other", "o"),
	})
	v.events(fw, newDir, oldDir)
	waitFor(t, "the new file to be loaded", func() bool { return hasHTTPConfig("goto-other.yaml") })
	time.Sleep(3 * configDebounce)
	requireUntouched(t, mtlsPort, mtls)
	requireUntouched(t, httpsPort, https)
}

func TestDiffCerts(t *testing.T) {
	c := func(port int, name, domain string) *ctl.CertConfig {
		return &ctl.CertConfig{Port: port, Name: name, Domain: domain, SAN: []string{domain + ".san"}}
	}
	names := func(certs []*ctl.CertConfig) string {
		out := make([]string, 0, len(certs))
		for _, cert := range certs {
			out = append(out, fmt.Sprintf("%d/%s/%s", cert.Port, cert.Name, cert.Domain))
		}
		return strings.Join(out, ",")
	}
	old := &ctl.TLSConfigs{SpiffeSocket: "unix:///a", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "a"), c(9443, "", "b"), nil}}
	for name, tc := range map[string]struct {
		old, new          *ctl.TLSConfigs
		wantClear, wantLd string
	}{
		"first load loads everything": {
			nil, old, "", "0/client/c,8443//a,9443//b",
		},
		"nothing changed": {
			old, &ctl.TLSConfigs{SpiffeSocket: "unix:///a", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "a"), c(9443, "", "b")}}, "", "",
		},
		"an entry changed": {
			old, &ctl.TLSConfigs{SpiffeSocket: "unix:///a", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "changed"), c(9443, "", "b")}},
			"8443//a", "8443//changed",
		},
		"an entry was removed": {
			old, &ctl.TLSConfigs{SpiffeSocket: "unix:///a", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "a")}}, "9443//b", "",
		},
		"an entry was added": {
			old, &ctl.TLSConfigs{SpiffeSocket: "unix:///a", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "a"), c(9443, "", "b"), c(7443, "", "n")}}, "", "7443//n",
		},
		"an entry of the same name for another port is another entry": {
			old, &ctl.TLSConfigs{SpiffeSocket: "unix:///a", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "a"), c(9443, "", "b"), c(0, "client2", "c")}}, "", "0/client2/c",
		},
		"another socket changes everything": {
			old, &ctl.TLSConfigs{SpiffeSocket: "unix:///b", Certs: []*ctl.CertConfig{c(0, "client", "c"), c(8443, "", "a"), c(9443, "", "b")}},
			"0/client/c,8443//a,9443//b", "0/client/c,8443//a,9443//b",
		},
		"empty entries are not entries": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{nil}}, &ctl.TLSConfigs{Certs: []*ctl.CertConfig{nil, nil}}, "", "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			gotClear, gotLoad := diffCerts(tc.old, tc.new)
			if names(gotClear) != tc.wantClear || names(gotLoad) != tc.wantLd {
				t.Errorf("clear [%s], load [%s], want clear [%s], load [%s]", names(gotClear), names(gotLoad), tc.wantClear, tc.wantLd)
			}
		})
	}
	if clear, load := diffCerts(old, nil); clear != nil || load != nil {
		t.Errorf("a config that is gone has nothing to clear or load here: %v %v", clear, load)
	}
}

func TestApplyCertGivesTheListenerWhatItsEntryMakesOfIt(t *testing.T) {
	l := &listeners.Listener{CommonName: "default.test"}
	applyCert(l, nil)
	if l.CommonName != "default.test" || l.SpiffeID != "" || l.AltNames != nil {
		t.Errorf("a listener without an entry was changed: %+v", l)
	}
	applyCert(l, &ctl.CertConfig{Domain: "entry.test", SAN: []string{"a.entry.test"}, SpiffeID: "spiffe://x"})
	if l.CommonName != "entry.test" || l.SpiffeID != "spiffe://x" || len(l.AltNames) != 1 || l.AltNames[0] != "a.entry.test" {
		t.Errorf("listener = common name %q, spiffe %q, alt names %v, want what the entry gives", l.CommonName, l.SpiffeID, l.AltNames)
	}
	l = &listeners.Listener{CommonName: "default.test"}
	applyCert(l, &ctl.CertConfig{})
	if l.CommonName != "default.test" {
		t.Errorf("an entry without a domain changed the common name to %q", l.CommonName)
	}
}

func TestReloadOfAFileThatChangesACertRestartsItsListenerOnce(t *testing.T) {
	resetState(t)
	ca := newCertsCA(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, certsDomain8443, "", true)))
	mtls, https := stateOf(t, mtlsPort), stateOf(t, httpsPort)

	// The changed cert is loaded over the old one: removing the old one first would take the TLS off the listener, and
	// restart it, to restart it again with the new cert.
	logged := captureLog(t)
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, ca, mtlsPort, httpsPort, "changed.uk3", "", true)))
	requireUntouched(t, httpsPort, https)
	l := mustListener(t, mtlsPort)
	if l != mtls.l || l.Generation != mtls.generation+1 {
		t.Errorf("listener of the changed cert: %p generation %d, want the same listener (%p) restarted once (generation %d)", l, l.Generation, mtls.l, mtls.generation+1)
	}
	if got := strings.Count(logged.String(), "Reopened listener"); got != 1 {
		t.Errorf("listener was reopened %d times, want 1:\n%s", got, logged.String())
	}
	if l.CommonName != "changed.uk3" || !l.TLS || !l.MTLS || !isAccepting(mtlsPort) {
		t.Errorf("listener of the changed cert: common name %q, tls %t, mtls %t, accepting %t", l.CommonName, l.TLS, l.MTLS, isAccepting(mtlsPort))
	}
}

func TestReloadOfAFileThatChangesItsCAsLoadsTheCertsAgain(t *testing.T) {
	resetState(t)
	mtlsPort, httpsPort := newPort(), newPort()
	removeListenersAfter(t, mtlsPort, httpsPort)
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, newCertsCA(t), mtlsPort, httpsPort, certsDomain8443, "", true)))
	mtls, https := stateOf(t, mtlsPort), stateOf(t, httpsPort)

	// the certs are made from the CAs: the listeners have to use the certs that the new CAs make
	loadConfig(parse(t, "tls.yaml", certsConfigYAML(t, newCertsCA(t), mtlsPort, httpsPort, certsDomain8443, "", true)))
	for port, before := range map[int]liveState{mtlsPort: mtls, httpsPort: https} {
		l := mustListener(t, port)
		if l.Generation <= before.generation || !l.TLS || !isAccepting(port) {
			t.Errorf("listener of port %d: generation %d (was %d), tls %t, accepting %t, want it restarted with its TLS", port, l.Generation, before.generation, l.TLS, isAccepting(port))
		}
	}
}

func TestReloadOfAFileLoadsTheCertsFromFilesAgain(t *testing.T) {
	resetState(t)
	forgetCA(t, "fileCert")
	dir := t.TempDir()
	write := func(cn string) {
		t.Helper()
		cert, key := selfSigned(t, cn)
		for name, content := range map[string]string{"cert.pem": cert, "key.pem": key} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	commonName := func() string {
		t.Helper()
		certs, err := gototls.GetCerts("fileCert")
		if err != nil || len(certs) == 0 {
			t.Fatalf("no cert of the file: %v", err)
		}
		leaf, err := x509.ParseCertificate(certs[0].Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		return leaf.Subject.CommonName
	}
	yamlText := fmt.Sprintf("tls:\n  certs:\n  - name: fileCert\n    cert: %q\n    key: %q\n",
		filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))

	write("first.test")
	loadConfig(parse(t, "tls.yaml", yamlText))
	if got := commonName(); got != "first.test" {
		t.Fatalf("cert of the file: %q, want first.test", got)
	}
	// The files change under a config that does not, which is what the rotation of a mounted secret does
	write("second.test")
	loadConfig(parse(t, "tls.yaml", yamlText))
	if got := commonName(); got != "second.test" {
		t.Errorf("cert of the file after it was rotated: %q, want second.test", got)
	}
}

func TestDiffCertsOfEntriesThatReplaceEachOther(t *testing.T) {
	spiffe := func(port int, id string) *ctl.CertConfig {
		return &ctl.CertConfig{Port: port, SpiffeID: id, Domain: "d"}
	}
	auto := func(port int, domain string) *ctl.CertConfig {
		return &ctl.CertConfig{Port: port, AutoCert: true, Domain: domain}
	}
	file := func(port int, cert string) *ctl.CertConfig {
		return &ctl.CertConfig{Port: port, Cert: cert, Key: "key"}
	}
	ca := func(name string) []*ctl.CACertConfig {
		return []*ctl.CACertConfig{{Name: name, Domain: "d", Cert: "c", Key: "k"}}
	}
	describe := func(certs []*ctl.CertConfig) string {
		out := []string{}
		for _, c := range certs {
			out = append(out, fmt.Sprintf("%d:%s%s", c.Port, c.SpiffeID, c.Domain))
		}
		return strings.Join(out, ",")
	}
	for name, tc := range map[string]struct {
		old, new          *ctl.TLSConfigs
		wantClear, wantLd string
	}{
		"a changed SPIFFE entry is loaded over the old one": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{spiffe(1, "a")}}, &ctl.TLSConfigs{Certs: []*ctl.CertConfig{spiffe(1, "b")}}, "", "1:bd",
		},
		"a changed auto cert entry is loaded over the old one": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{auto(1, "a")}}, &ctl.TLSConfigs{Certs: []*ctl.CertConfig{auto(1, "b")}}, "", "1:b",
		},
		"an entry that is of another kind now is cleared first": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{spiffe(1, "a")}}, &ctl.TLSConfigs{Certs: []*ctl.CertConfig{auto(1, "a")}}, "1:ad", "1:a",
		},
		"an entry that is gone is cleared": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{spiffe(1, "a"), auto(2, "b")}}, &ctl.TLSConfigs{Certs: []*ctl.CertConfig{spiffe(1, "a")}}, "2:b", "",
		},
		"entries with the cert in files are always loaded again": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{file(1, "c")}}, &ctl.TLSConfigs{Certs: []*ctl.CertConfig{file(1, "c")}}, "1:", "1:",
		},
		"the CAs changed, so the entries are loaded again, over the old ones": {
			&ctl.TLSConfigs{CACerts: ca("x"), Certs: []*ctl.CertConfig{auto(1, "a"), spiffe(2, "s")}},
			&ctl.TLSConfigs{CACerts: ca("y"), Certs: []*ctl.CertConfig{auto(1, "a"), spiffe(2, "s")}}, "", "1:a,2:sd",
		},
		"the CAs did not change": {
			&ctl.TLSConfigs{CACerts: ca("x"), Certs: []*ctl.CertConfig{auto(1, "a")}},
			&ctl.TLSConfigs{CACerts: ca("x"), Certs: []*ctl.CertConfig{auto(1, "a")}}, "", "",
		},
		"no CAs in the old config and an empty list in the new one are the same": {
			&ctl.TLSConfigs{Certs: []*ctl.CertConfig{auto(1, "a")}},
			&ctl.TLSConfigs{CACerts: []*ctl.CACertConfig{}, Certs: []*ctl.CertConfig{auto(1, "a")}}, "", "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			gotClear, gotLoad := diffCerts(tc.old, tc.new)
			if describe(gotClear) != tc.wantClear || describe(gotLoad) != tc.wantLd {
				t.Errorf("clear [%s], load [%s], want clear [%s], load [%s]", describe(gotClear), describe(gotLoad), tc.wantClear, tc.wantLd)
			}
		})
	}
}
