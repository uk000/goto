package client

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goto/pkg/constants"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/types"
)

// invokeForHeaders makes a call through the API of the client, and gives the headers that it answered with.
func invokeForHeaders(t *testing.T, spec map[string]any) http.Header {
	t.Helper()
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", string(body), "Accept", "application/json")
	rr := serve(t, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	return rr.Header()
}

// phaseValues reads a header that has a time for each call, which is "-" for the calls that didn't go through the phase.
// It gives none if the header isn't there.
func phaseValues(t *testing.T, h http.Header, name string) []string {
	t.Helper()
	v := h.Get(name)
	if v == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(v), &values); err != nil {
		t.Fatalf("%s = %q is not a JSON array of times: %v", name, v, err)
	}
	return values
}

// phaseTime is the time that a call had in a phase, and whether it went through it.
func phaseTime(t *testing.T, name, value string) (time.Duration, bool) {
	t.Helper()
	if value == "-" {
		return 0, false
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		t.Fatalf("%s: %q is not a time (err %v)", name, value, err)
	}
	return d, true
}

func localhostOf(url string) string {
	return strings.Replace(url, "127.0.0.1", "localhost", 1)
}

// expectedPhases tell which of the headers that the first call has, which depends on how it goes to its target.
type expectedPhases struct {
	dns, tcp, proxyTLS, tunnel, tls bool
}

func checkPhases(t *testing.T, h http.Header, want expectedPhases, calls int) {
	t.Helper()
	present := map[string]bool{
		constants.HeaderGotoClientDNSTook:      want.dns,
		constants.HeaderGotoClientTCPTook:      want.tcp,
		constants.HeaderGotoClientProxyTLSTook: want.proxyTLS,
		constants.HeaderGotoClientTunnelTook:   want.tunnel,
		constants.HeaderGotoClientTLSTook:      want.tls,
		// the ones that every call has
		constants.HeaderGotoClientResponseHeadTook: true,
		constants.HeaderGotoClientBodyTook:         true,
		constants.HeaderGotoClientTotalTook:        true,
		// what the proxy says of the CONNECT request is what it was asked for
		constants.HeaderGotoConnectProxyTook:     want.tunnel,
		constants.HeaderGotoConnectProxyDNSTook:  want.tunnel,
		constants.HeaderGotoConnectProxyDialTook: want.tunnel,
	}
	for name, expected := range present {
		values := phaseValues(t, h, name)
		switch {
		case expected && len(values) != calls:
			t.Errorf("%s = %q, want a time for each of the %d calls", name, h.Get(name), calls)
		case !expected && values != nil:
			t.Errorf("%s = %q, want none: the calls didn't go through the phase", name, h.Get(name))
		}
	}
	if got := h.Get(constants.HeaderGotoConnectProxyDelay); got != "" {
		t.Errorf("%s = %q, want none as the proxy added no delay", constants.HeaderGotoConnectProxyDelay, got)
	}
}

// checkTimesAddUp tells that what the phases of a call took is not more than what the call took, as they follow each
// other, and that the proxy took part of the time that the exchange with it took.
func checkTimesAddUp(t *testing.T, h http.Header, call int) {
	t.Helper()
	get := func(name string) time.Duration {
		values := phaseValues(t, h, name)
		if call >= len(values) {
			return 0
		}
		d, _ := phaseTime(t, name, values[call])
		return d
	}
	total := get(constants.HeaderGotoClientTotalTook)
	if total <= 0 {
		t.Fatalf("call %d: total = %s", call, total)
	}
	var sum time.Duration
	for _, name := range []string{constants.HeaderGotoClientDNSTook, constants.HeaderGotoClientTCPTook, constants.HeaderGotoClientProxyTLSTook,
		constants.HeaderGotoClientTunnelTook, constants.HeaderGotoClientTLSTook, constants.HeaderGotoClientResponseHeadTook, constants.HeaderGotoClientBodyTook} {
		sum += get(name)
	}
	if sum > total {
		t.Errorf("call %d: the phases took %s, more than the %s of the call", call, sum, total)
	}
	proxyTook := get(constants.HeaderGotoConnectProxyTook)
	if lookups := get(constants.HeaderGotoConnectProxyDNSTook) + get(constants.HeaderGotoConnectProxyDialTook); lookups > proxyTook {
		t.Errorf("call %d: the proxy took %s to resolve and connect, more than the %s of its answer", call, lookups, proxyTook)
	}
	if tunnel := get(constants.HeaderGotoClientTunnelTook); proxyTook > tunnel {
		t.Errorf("call %d: the proxy took %s to answer, more than the %s that the client waited for the answer", call, proxyTook, tunnel)
	}
}

func TestClientReportsTimesOfCallsToATarget(t *testing.T) {
	plain := httptest.NewServer(echoHandler(&recorder{}))
	t.Cleanup(plain.Close)
	secure, _ := newTLSEchoServer(t)
	plainProxy := startConnectProxy(t, nil)
	secureProxy := startConnectProxy(t, proxyServerTLSConfig())

	tests := []struct {
		name   string
		url    string
		proxy  string
		phases expectedPhases
	}{
		{"http target", localhostOf(plain.URL), "", expectedPhases{dns: true, tcp: true}},
		{"http target by IP", plain.URL, "", expectedPhases{tcp: true}},
		{"https target", localhostOf(secure.URL), "", expectedPhases{dns: true, tcp: true, tls: true}},
		{"http target via http proxy", localhostOf(plain.URL), plainProxy.url("http", "localhost"),
			expectedPhases{dns: true, tcp: true, tunnel: true}},
		{"https target via http proxy", localhostOf(secure.URL), plainProxy.url("http", "localhost"),
			expectedPhases{dns: true, tcp: true, tunnel: true, tls: true}},
		{"http target via https proxy", localhostOf(plain.URL), secureProxy.url("https", "localhost"),
			expectedPhases{dns: true, tcp: true, proxyTLS: true, tunnel: true}},
		{"https target via https proxy", localhostOf(secure.URL), secureProxy.url("https", "localhost"),
			expectedPhases{dns: true, tcp: true, proxyTLS: true, tunnel: true, tls: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := map[string]any{"url": tt.url + "/timed"}
			if tt.proxy != "" {
				spec["proxy"] = tt.proxy
			}
			h := invokeForHeaders(t, spec)
			checkPhases(t, h, tt.phases, 1)
			checkTimesAddUp(t, h, 0)
			// the phases of the first hop are the ones of the proxy if there is one
			if tt.phases.tls {
				if d, _ := phaseTime(t, "tls", phaseValues(t, h, constants.HeaderGotoClientTLSTook)[0]); d <= 0 {
					t.Errorf("the TLS handshake with the target took %s, want the time that it took", d)
				}
			}
		})
	}
}

// A call that gets the connection that an earlier call opened has nothing to resolve or connect to, and has no time for it.
func TestClientReportsNoTimeForConnectionsThatAreReused(t *testing.T) {
	secure, _ := newTLSEchoServer(t)
	proxy := startConnectProxy(t, nil)
	h := invokeForHeaders(t, map[string]any{"url": localhostOf(secure.URL) + "/timed", "count": 3, "proxy": proxy.url("http", "localhost")})
	checkPhases(t, h, expectedPhases{dns: true, tcp: true, tunnel: true, tls: true}, 3)
	for _, name := range []string{constants.HeaderGotoClientDNSTook, constants.HeaderGotoClientTCPTook, constants.HeaderGotoClientTunnelTook,
		constants.HeaderGotoClientTLSTook, constants.HeaderGotoConnectProxyTook} {
		values := phaseValues(t, h, name)
		if _, ok := phaseTime(t, name, values[0]); !ok {
			t.Errorf("%s = %q, want the first call to have a time", name, h.Get(name))
		}
		for i := 1; i < 3; i++ {
			if values[i] != "-" {
				t.Errorf("%s = %q, want call %d that reused the connection to have none", name, h.Get(name), i+1)
			}
		}
	}
	for i := 0; i < 3; i++ {
		checkTimesAddUp(t, h, i)
	}
}

// The delay of the proxy is reported by the proxy, and the client passes it on as it is told of in the answer.
func TestClientReportsDelayOfTheProxy(t *testing.T) {
	plain := httptest.NewServer(echoHandler(&recorder{}))
	t.Cleanup(plain.Close)
	proxy := startConnectProxy(t, nil)
	tcpproxy.SetConnectDelay(proxy.port, types.NewDelay(60*time.Millisecond, 60*time.Millisecond, 0))
	h := invokeForHeaders(t, map[string]any{"url": localhostOf(plain.URL) + "/timed", "proxy": proxy.url("http", "localhost")})
	values := phaseValues(t, h, constants.HeaderGotoConnectProxyDelay)
	if len(values) != 1 {
		t.Fatalf("%s = %q, want the delay for the call", constants.HeaderGotoConnectProxyDelay, h.Get(constants.HeaderGotoConnectProxyDelay))
	}
	if d, ok := phaseTime(t, "delay", values[0]); !ok || d < 60*time.Millisecond {
		t.Errorf("delay = %q, want at least the 60ms of the proxy", values[0])
	}
	// the delay is part of the time that the proxy took to answer, which is part of the time that the client waited
	proxyTook, _ := phaseTime(t, "took", phaseValues(t, h, constants.HeaderGotoConnectProxyTook)[0])
	tunnel, _ := phaseTime(t, "tunnel", phaseValues(t, h, constants.HeaderGotoClientTunnelTook)[0])
	if proxyTook < 60*time.Millisecond || tunnel < proxyTook {
		t.Errorf("the proxy took %s, and the client waited %s for it, want the delay (60ms) in both", proxyTook, tunnel)
	}
}

// The time of the TLS handshake with the target is the time that the handshake took, however the client gets to the
// target: the transport of http does the handshake for a target that it is tunneled to, and the dials of the client do it
// for the one that is connected to directly.
func TestClientTimesTheTLSHandshakeOfTheTarget(t *testing.T) {
	const handshake = 40 * time.Millisecond
	slow := httptest.NewUnstartedServer(echoHandler(&recorder{}))
	slow.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		time.Sleep(handshake)
		return nil, nil
	}}
	slow.StartTLS()
	t.Cleanup(slow.Close)
	proxy := startConnectProxy(t, nil)
	for name, proxyURL := range map[string]string{"directly": "", "via a proxy": proxy.url("http", "localhost")} {
		t.Run(name, func(t *testing.T) {
			spec := map[string]any{"url": localhostOf(slow.URL) + "/slow-tls"}
			if proxyURL != "" {
				spec["proxy"] = proxyURL
			}
			h := invokeForHeaders(t, spec)
			values := phaseValues(t, h, constants.HeaderGotoClientTLSTook)
			if len(values) != 1 {
				t.Fatalf("%s = %q", constants.HeaderGotoClientTLSTook, h.Get(constants.HeaderGotoClientTLSTook))
			}
			if d, _ := phaseTime(t, "tls", values[0]); d < handshake || d > handshake+2*time.Second {
				t.Errorf("the TLS handshake took %s, want the %s that the server took for it", d, handshake)
			}
			checkTimesAddUp(t, h, 0)
		})
	}
}
