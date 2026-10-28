package tcpproxy

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// answerDuration reads a duration that the answer to a CONNECT request has in a header, and tells if the header is there.
func answerDuration(t *testing.T, answer *http.Response, name string) (time.Duration, bool) {
	t.Helper()
	v := answer.Header.Get(name)
	if v == "" {
		return 0, false
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		t.Fatalf("%s = %q, want a duration (err %v)", name, v, err)
	}
	return d, true
}

// The time that the proxy takes to answer is told apart: what it took to resolve the destination, and what it took to
// connect to it, are the parts of it that are the time of the network, and not the proxy.
func TestConnectAnswerTellsLookupAndConnectTimes(t *testing.T) {
	setViaLabels(t)
	dest := newStatusDestination(t)
	_, port, _ := net.SplitHostPort(dest)
	h := newConnectHarness(t, false)

	_, byName := sendConnect(t, h.dial(), connectRequest(net.JoinHostPort("localhost", port)))
	if byName.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT answered %q", byName.Status)
	}
	took, ok := answerDuration(t, byName, "Goto-Connect-Proxy-Took")
	if !ok {
		t.Fatal("the answer has no Goto-Connect-Proxy-Took")
	}
	dns, hasDNS := answerDuration(t, byName, "Goto-Connect-Proxy-DNS-Took")
	dial, hasDial := answerDuration(t, byName, "Goto-Connect-Proxy-Dial-Took")
	if !hasDNS || !hasDial {
		t.Fatalf("the answer for a name has DNS %v and Dial %v, want both: %v", hasDNS, hasDial, byName.Header)
	}
	if dns+dial > took {
		t.Errorf("lookup (%s) and connect (%s) are part of the answer, that took %s", dns, dial, took)
	}

	// an IP is not looked up, and is connected to
	_, byIP := sendConnect(t, h.dial(), connectRequest(dest))
	if _, ok := answerDuration(t, byIP, "Goto-Connect-Proxy-DNS-Took"); ok {
		t.Errorf("the answer for an IP has Goto-Connect-Proxy-DNS-Took = %q, want none", byIP.Header.Get("Goto-Connect-Proxy-DNS-Took"))
	}
	if _, ok := answerDuration(t, byIP, "Goto-Connect-Proxy-Dial-Took"); !ok {
		t.Error("the answer for an IP has no Goto-Connect-Proxy-Dial-Took")
	}
}

// What went into a connection that failed is told too, as it is what the time is about when a destination is slow to
// refuse. A destination that can't be connected to has no time of the tunnel, and none of the headers of the exchanges.
func TestConnectRefusedAnswerTellsLookupAndConnectTimes(t *testing.T) {
	setViaLabels(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	h := newConnectHarness(t, false)
	_, answer := sendConnect(t, h.dial(), connectRequest(net.JoinHostPort("localhost", port)))
	if answer.StatusCode != http.StatusBadGateway {
		t.Skipf("CONNECT answered %q, the port that was just closed is in use", answer.Status)
	}
	if _, ok := answerDuration(t, answer, "Goto-Connect-Proxy-Took"); !ok {
		t.Error("the refusal has no Goto-Connect-Proxy-Took")
	}
	if _, ok := answerDuration(t, answer, "Goto-Connect-Proxy-DNS-Took"); !ok {
		t.Error("the refusal has no Goto-Connect-Proxy-DNS-Took, for the lookup of localhost that was done")
	}
	if _, ok := answerDuration(t, answer, "Goto-Connect-Proxy-Dial-Took"); !ok {
		t.Error("the refusal has no Goto-Connect-Proxy-Dial-Took, for the attempt that was refused")
	}
}

// The headers of the time of the lookup and the connecting belong to the answer to the request for the tunnel, and are
// not in the responses that go through it, which don't have a lookup or a connecting of their own.
func TestConnectExchangesDoNotHaveLookupAndConnectTimes(t *testing.T) {
	setViaLabels(t)
	dest := newStatusDestination(t)
	_, port, _ := net.SplitHostPort(dest)
	h := newConnectHarness(t, false)
	tc, answer := sendConnect(t, h.dial(), connectRequest(net.JoinHostPort("localhost", port)))
	if _, ok := answerDuration(t, answer, "Goto-Connect-Proxy-DNS-Took"); !ok {
		t.Fatal("the answer has no Goto-Connect-Proxy-DNS-Took")
	}
	c := &httpTunnel{t: t, tc: tc, br: bufio.NewReader(tc)}
	c.send(get("/status/200"))
	resp := c.readHead("GET")
	for _, name := range []string{"Goto-Connect-Proxy-DNS-Took", "Goto-Connect-Proxy-Dial-Took"} {
		if got := resp.Header.Values(name); len(got) != 0 {
			t.Errorf("the response has %s = %q, want none", name, strings.Join(got, ","))
		}
	}
	resp.Body.Close()
}
