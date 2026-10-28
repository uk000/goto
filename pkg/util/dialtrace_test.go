package util

import (
	"context"
	"errors"
	"net"
	"net/http/httptrace"
	"testing"
	"time"
)

func listenForDials(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln
}

func tracedContext(d *DialTrace) context.Context {
	return httptrace.WithClientTrace(context.Background(), d.ClientTrace())
}

func TestDialTraceTimesLookupAndConnect(t *testing.T) {
	ln := listenForDials(t)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	d := &DialTrace{}
	c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(tracedContext(d), "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if dns, ok := d.DNS(); !ok || dns < 0 {
		t.Errorf("DNS() = %s, %v, want the time of the lookup of localhost", dns, ok)
	}
	if dial, ok := d.Dial(); !ok || dial < 0 {
		t.Errorf("Dial() = %s, %v, want the time of the connecting", dial, ok)
	}
	if d.ConnectedAt().IsZero() {
		t.Error("ConnectedAt() is zero for a dial that connected")
	}
}

func TestDialTraceHasNoDNSForAnIP(t *testing.T) {
	ln := listenForDials(t)
	d := &DialTrace{}
	c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(tracedContext(d), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if dns, ok := d.DNS(); ok {
		t.Errorf("DNS() = %s, true, want none for an IP, which there is nothing to look up for", dns)
	}
	if _, ok := d.Dial(); !ok {
		t.Error("Dial() is not there for a dial that connected")
	}
}

func TestDialTraceOfALookupThatFails(t *testing.T) {
	failing := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("no resolver")
	}}
	d := &DialTrace{}
	_, err := (&net.Dialer{Timeout: 5 * time.Second, Resolver: failing}).DialContext(tracedContext(d), "tcp", "no-such-host.invalid:80")
	if err == nil {
		t.Fatal("the dial of a name that can't be resolved worked")
	}
	if _, ok := d.DNS(); !ok {
		t.Error("DNS() is not there for the lookup that failed, which is the time that went into it")
	}
	if _, ok := d.Dial(); ok {
		t.Error("Dial() is there for a dial that never got to connect")
	}
	if !d.ConnectedAt().IsZero() {
		t.Error("ConnectedAt() is not zero for a dial that didn't connect")
	}
}

func TestDialTraceOfAConnectThatIsRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	d := &DialTrace{}
	if c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(tracedContext(d), "tcp", addr); err == nil {
		c.Close()
		t.Skip("something is listening on the port that was just closed")
	}
	if _, ok := d.Dial(); !ok {
		t.Error("Dial() is not there for the attempt that failed, which is the time that went into it")
	}
	if !d.ConnectedAt().IsZero() {
		t.Error("ConnectedAt() is not zero for a dial that didn't connect")
	}
}

// The attempts to the other addresses of a name that the connected one beat end after it, and are not what the dial took.
func TestDialTraceDoesNotCountTheAttemptsThatLose(t *testing.T) {
	d := &DialTrace{}
	trace := d.ClientTrace()
	trace.DNSStart(httptrace.DNSStartInfo{})
	trace.DNSDone(httptrace.DNSDoneInfo{})
	trace.ConnectStart("tcp", "[::1]:80")
	trace.ConnectStart("tcp", "127.0.0.1:80")
	time.Sleep(10 * time.Millisecond)
	trace.ConnectDone("tcp", "127.0.0.1:80", nil)
	connectedAt := d.ConnectedAt()
	took, _ := d.Dial()
	time.Sleep(30 * time.Millisecond)
	trace.ConnectDone("tcp", "[::1]:80", errors.New("operation was canceled"))
	if got, _ := d.Dial(); got != took {
		t.Errorf("Dial() = %s after a losing attempt ended, was %s", got, took)
	}
	if !d.ConnectedAt().Equal(connectedAt) {
		t.Error("ConnectedAt() moved with a losing attempt")
	}
	if took < 10*time.Millisecond || took > 25*time.Millisecond {
		t.Errorf("Dial() = %s, want what the connected attempt took (10ms)", took)
	}
}
