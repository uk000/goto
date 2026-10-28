package util

import (
	"net/http/httptrace"
	"sync"
	"time"
)

// DialTrace times the two things that a dial of the net package does: it resolves the name of the address, and connects
// to what the name resolved to. It is a trace of the context that the dial is given, so it sees the dials that are made
// with that context, e.g. the ones of an http client that the context is on the request of. It is safe to use while
// the dial is still going on, which a dial of an http client can be after the request that it was for is done.
//
// An address that is an IP has nothing to resolve, and a connection that is reused has nothing to dial, and the times
// of these are not there, which is not the same as being 0.
type DialTrace struct {
	lock         sync.Mutex
	dnsStart     time.Time
	dnsDone      time.Time
	connectStart time.Time
	connectEnd   time.Time
	connected    bool
}

// ClientTrace is what to put in the context of the dial: httptrace.WithClientTrace(ctx, trace.ClientTrace()). The hooks
// that it has add up with the ones that the context already has.
func (d *DialTrace) ClientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			d.lock.Lock()
			defer d.lock.Unlock()
			if d.dnsStart.IsZero() {
				d.dnsStart = time.Now()
			}
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			d.lock.Lock()
			defer d.lock.Unlock()
			d.dnsDone = time.Now()
		},
		ConnectStart: func(_, _ string) {
			d.lock.Lock()
			defer d.lock.Unlock()
			if d.connectStart.IsZero() {
				d.connectStart = time.Now()
			}
		},
		ConnectDone: func(_, _ string, err error) {
			d.lock.Lock()
			defer d.lock.Unlock()
			// The attempts that lose to the one that got connected end after it, and are not what the dial took.
			if err == nil {
				d.connected = true
				d.connectEnd = time.Now()
			} else if !d.connected {
				d.connectEnd = time.Now()
			}
		},
	}
}

// DNS is the time that it took to resolve the name, and whether there was a name to resolve. It is the time that went
// into the lookup that failed, if it did.
func (d *DialTrace) DNS() (time.Duration, bool) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.dnsStart.IsZero() || d.dnsDone.IsZero() {
		return 0, false
	}
	return d.dnsDone.Sub(d.dnsStart), true
}

// Dial is the time that it took to connect to the address once the name was resolved, which has in it the attempts that
// failed, and the wait for the other family of addresses to be tried if the first one is slow. It tells if the dial got
// as far as connecting.
func (d *DialTrace) Dial() (time.Duration, bool) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.connectStart.IsZero() || d.connectEnd.IsZero() {
		return 0, false
	}
	from := d.connectStart
	if !d.dnsDone.IsZero() && d.dnsDone.Before(from) {
		from = d.dnsDone
	}
	return d.connectEnd.Sub(from), true
}

// ConnectedAt is when the connection was made, which is zero if it was not.
func (d *DialTrace) ConnectedAt() time.Time {
	d.lock.Lock()
	defer d.lock.Unlock()
	if !d.connected {
		return time.Time{}
	}
	return d.connectEnd
}
