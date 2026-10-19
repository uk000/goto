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

package listeners

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Each test in this file failed on the code before it was fixed, and uses nothing that the fixes added.

// guard turns a panic into a failure of the test, so that the other tests still run.
func guard(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// A listener is open when its port is bound, and not open when it isn't. The GRPC server closes its listeners when it
// stops, and gets them bound again with InitListener when it starts: they are open then, and must not be opened again
// by whoever asks for the listener of the port.
func TestInitListenerSetsOpenToWhatItDid(t *testing.T) {
	setup(t)
	t.Run("restart of the GRPC server", func(t *testing.T) {
		port := freePort(t)
		forget(t, port)
		l, err := AddGRPCListener(port, false)
		if err != nil || !l.Open {
			t.Fatalf("AddGRPCListener = %v, %v, want an open listener", l, err)
		}
		generation := l.Generation
		OnGRPCStop()
		if l.Open || l.Listener != nil {
			t.Fatalf("after OnGRPCStop: open = %t, listener = %v, want the listener closed", l.Open, l.Listener)
		}
		if !l.InitListener() {
			t.Fatal("InitListener failed on a port that is free")
		}
		if !l.Open {
			t.Error("a listener that is bound by InitListener is not open")
		}
		again, err := AddGRPCListener(port, false)
		if err != nil || again != l || l.Generation != generation {
			t.Errorf("AddGRPCListener = %v, %v, generation %d -> %d, want the listener as it is: it is open", again, err, generation, l.Generation)
		}
	})
	t.Run("port that is taken", func(t *testing.T) {
		port := takenPort(t)
		l := newListener(port, "http", "", true)
		l.assignProtocol()
		if l.InitListener() {
			l.Close()
			t.Fatal("InitListener succeeded on a port that is taken")
		}
		if l.Open {
			t.Error("a listener that couldn't be bound is open")
		}
	})
}

func takenPort(t *testing.T) int {
	t.Helper()
	taken, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { taken.Close() })
	return taken.Addr().(*net.TCPAddr).Port
}

// The port that a listener binds is given back when its TLS config can't be made, which is what happens with a cert and
// key that don't match. The listener is retried, and the port can't be taken by the retries when it isn't free.
func TestFailedTLSConfigDoesNotKeepThePort(t *testing.T) {
	setup(t)
	port := freePort(t)
	l := newListener(port, "tcp", "", false)
	l.Label = "bad-cert"
	l.TLS = true
	l.RawCert, l.RawKey = []byte("not a cert"), []byte("not a key")
	if l.InitListener() {
		l.Close()
		t.Fatal("InitListener succeeded with a cert that can't be parsed")
	}
	if isListening(port) {
		t.Error("the port is still bound by a listener that failed to open")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Errorf("the port can't be bound again after the failure: %v", err)
	} else {
		ln.Close()
	}
}

func TestGetPeerCertHandlesWhatIsNotThere(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "http")
	guard(t, "GetPeerCert of a port with no listener", func() {
		if server, client := GetPeerCert(1, "1.1.1.1:1"); server != "" || client != "" {
			t.Errorf("GetPeerCert of an unknown port = %q, %q, want nothing", server, client)
		}
	})
	// A listener that has no server cert info, which is the case of the ones that serve a cert that was uploaded.
	guard(t, "GetPeerCert of a listener without a server cert", func() {
		l.StoreSNI("2.2.2.2:2", "x.test", "")
		server, client := GetPeerCert(l.Port, "2.2.2.2:2")
		if server != "" || !strings.Contains(client, "SNI: x.test") {
			t.Errorf("GetPeerCert = %q, %q, want no server cert and the client one", server, client)
		}
	})
	guard(t, "GetPeerCert of the default listener", func() {
		GetPeerCert(DefaultListener.Port, "3.3.3.3:3")
	})
}

// What is reported of the peers must not change after it is given out, since it is written to a response while the
// handshakes go on.
func TestGetPeerCertsGivesACopy(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "http")
	l.StoreSNI("1.1.1.1:1", "one.test", "")
	got := GetPeerCerts(l.Port)[l.Port]
	if len(got) != 1 || got["1.1.1.1:1"].SNI != "one.test" {
		t.Fatalf("peers = %v, want one", got)
	}
	l.StoreSNI("2.2.2.2:2", "two.test", "")
	l.StoreSNI("1.1.1.1:1", "changed.test", "")
	if len(got) != 1 || got["1.1.1.1:1"].SNI != "one.test" {
		t.Errorf("peers that were given out changed to %d peers, first with SNI %q, want what they were", len(got), got["1.1.1.1:1"].SNI)
	}
}

// The cert that is added with the API for certs is the cert of the listener, and the key that is added with the API for
// keys is its key.
func TestCertAndKeyAPIsStoreTheirOwn(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "http")
	base := fmt.Sprintf("/server/listeners/%d", l.Port)
	cert, key := "this is the cert", "this is the key"
	expectStatus(t, call(t, "PUT", base+"/cert/add", cert), 200, "Cert added")
	expectStatus(t, call(t, "PUT", base+"/key/add", key), 200, "Key added")
	if string(l.RawCert) != cert || string(l.RawKey) != key {
		t.Errorf("raw cert = %q, raw key = %q, want %q and %q", l.RawCert, l.RawKey, cert, key)
	}
}

// A port that is not valid is refused for every kind of listener. TCP is the kind that a listener is when it has no
// protocol, and its config used to take the place of the refusal.
func TestAddListenerRefusesInvalidPortOfTCPListener(t *testing.T) {
	setup(t)
	for _, body := range []string{`{"port":70000}`, `{"port":70000,"protocol":"tcp"}`, `{"port":0}`, `{"port":-5,"protocol":"tcp"}`} {
		forget(t, 70000)
		rr := call(t, "POST", "/server/listeners/add", body)
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), "Invalid port number") {
			t.Errorf("add %s = %d %q, want 400 with the invalid port", body, rr.Code, rr.Body.String())
		}
		for _, port := range []int{70000, 0, -5} {
			if IsListenerPresent(port) {
				t.Errorf("add %s stored a listener on port %d", body, port)
			}
		}
	}
}

// A listener that takes over the port of another kind of listener is opened by the one that stores it, and so it is
// open when the call that added it checks it.
func TestAddListenerOverAnotherKindOpensItOnce(t *testing.T) {
	setup(t)
	port := freePort(t)
	forget(t, port)
	if err := AddListener(port, true, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := AddListener(port, false, true, ""); err != nil {
		t.Errorf("replacing a TCP listener with a UDP one failed: %v", err)
	}
	if l := GetListenerForPort(port); l == nil || !l.IsUDP || !l.Open || l.UDPConn == nil {
		t.Errorf("listener = %+v, want an open UDP listener", l)
	}
	grpcPort := freePort(t)
	forget(t, grpcPort)
	if err := AddListener(grpcPort, true, false, ""); err != nil {
		t.Fatal(err)
	}
	g, err := AddGRPCListener(grpcPort, false)
	if err != nil || g == nil || !g.IsGRPC || !g.Open {
		t.Errorf("replacing a TCP listener with a GRPC one = %v, %v, want an open GRPC listener", g, err)
	}
}

// The listeners are looked at and changed while requests for them come in, and while the servers start and stop.
func TestListenersAreSafeForConcurrentUse(t *testing.T) {
	setup(t)
	api(t)
	base := 42000
	var wg sync.WaitGroup
	done := make(chan struct{})
	const rounds = 40
	// Writers add listeners that are not open, of the kinds that are tracked in lists of their own.
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				port := base + w*rounds + i
				protocol := []string{"grpc", "udp", "tcp"}[i%3]
				l := newListener(port, protocol, "", false)
				AddOrUpdateListener(l)
				if i%2 == 0 {
					RemoveListener(l)
				}
			}
		}()
	}
	t.Cleanup(func() {
		for p := base; p < base+3*rounds; p++ {
			if l := GetListenerForPort(p); l != nil {
				RemoveListener(l)
			}
		}
	})
	// Readers list them every way that they are listed.
	var readers sync.WaitGroup
	for r := 0; r < 3; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				GetListeners()
				GetListenerPorts()
				GetPeerCerts(0)
				ClearPeerCerts(0)
				call(t, "GET", "/server/listeners", "")
				call(t, "GET", fmt.Sprintf("/server/listeners/%d", base), "")
				call(t, "GET", "/server/listeners/ports", "")
				OnGRPCStop()
			}
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Error("the listeners could not be added in time: a lock is held by someone who waits for it")
	}
	close(done)
	readers.Wait()
}

// The peers that are seen by handshakes are stored while they are reported.
func TestPeerCertsAreSafeForConcurrentUse(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "http")
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				addr := fmt.Sprintf("10.0.0.%d:%d", g, i%5)
				l.StoreALPN(addr, []string{"h2"})
				l.StoreSNI(addr, "sni.test", "h2")
				l.StorePeerCertInfo(addr, "subject", nil, nil, "issuer")
				l.UpdatePeerStatus(addr, i%2 == 0, "status")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				addr := fmt.Sprintf("10.0.0.%d:%d", g, i%5)
				GetPeerCert(l.Port, addr)
				for _, peers := range GetPeerCerts(l.Port) {
					for _, p := range peers {
						_ = fmt.Sprint(p.Summary())
					}
				}
			}
		}()
	}
	wg.Wait()
}

// Opening a listener starts the goroutine that serves it, which must leave the listener to the one that opens it.
func TestOpeningListenersDoesNotRaceWithTheirServers(t *testing.T) {
	setup(t)
	for i := 0; i < 5; i++ {
		l := addAndOpen(t, []string{"http", "jsonrpc", "tcp", "https1", "tls"}[i])
		if body := l.Protocol; body == "" {
			t.Error("the listener has no protocol")
		}
		l.Close()
	}
}
