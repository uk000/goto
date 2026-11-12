package transport

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"goto/pkg/constants"
	"goto/pkg/util"
)

// CallTimings times what a call of the client goes through: resolving the name of the first hop, connecting to it, the
// TLS handshake with the proxy, the CONNECT exchange that gets the tunnel to the target, the TLS handshake with the
// target, getting the head of the response, and reading its body. The first hop is the proxy if the call goes through one,
// and the target if it doesn't.
//
// It is put in the context of the request of the call (see Context), where the dials and the transport report to it. A
// phase that the call didn't go through, e.g. dns for an IP, or all but the last two of a call that got a connection that
// was open already, has no time, which is not the same as a time of 0.
type CallTimings struct {
	lock     sync.Mutex
	start    time.Time
	dial     util.DialTrace
	proxyTLS phaseTime
	tunnel   phaseTime
	tls      phaseTime
	head     phaseTime
	body     phaseTime
	total    phaseTime
	// when the connection to the first hop was ready for the first thing to be sent on it
	readyAt  time.Time
	wroteAt  time.Time
	headAt   time.Time
	tlsStart time.Time
	// the headers that the proxy answered the CONNECT request with
	answer http.Header
}

type phaseTime struct {
	d   time.Duration
	set bool
}

// add accumulates the duration into the phase and marks the phase as one that the call went through.
func (p *phaseTime) add(d time.Duration) {
	p.d += d
	p.set = true
}

// String returns the duration of the phase, or "-" if the call didn't go through it.
func (p phaseTime) String() string {
	if !p.set {
		return "-"
	}
	return p.d.String()
}

type callTimingsKey struct{}

// NewCallTimings starts timing a call.
func NewCallTimings() *CallTimings {
	return &CallTimings{start: time.Now()}
}

// Context gives the context for the request of the call, that has the timings in it.
func (t *CallTimings) Context(ctx context.Context) context.Context {
	trace := t.dial.ClientTrace()
	trace.WroteRequest = func(httptrace.WroteRequestInfo) {
		t.lock.Lock()
		defer t.lock.Unlock()
		t.wroteAt = time.Now()
	}
	trace.TLSHandshakeStart = func() {
		t.lock.Lock()
		defer t.lock.Unlock()
		t.tlsStart = time.Now()
	}
	trace.TLSHandshakeDone = func(tls.ConnectionState, error) {
		t.lock.Lock()
		defer t.lock.Unlock()
		if !t.tlsStart.IsZero() {
			t.tls.add(time.Since(t.tlsStart))
			t.tlsStart = time.Time{}
		}
	}
	return httptrace.WithClientTrace(context.WithValue(ctx, callTimingsKey{}, t), trace)
}

// callTimingsOf gives the timings that the context has, or none.
func callTimingsOf(ctx context.Context) *CallTimings {
	t, _ := ctx.Value(callTimingsKey{}).(*CallTimings)
	return t
}

// proxyTLSDone notes that the handshake with the proxy that was started at the time given is done.
func (t *CallTimings) proxyTLSDone(start time.Time) {
	if t == nil {
		return
	}
	t.lock.Lock()
	defer t.lock.Unlock()
	t.proxyTLS.add(time.Since(start))
	t.readyAt = time.Now()
}

// targetTLSDone notes the time of a handshake with the target that was done by a dial of the client, which the transport
// of http can't tell of, as it is done before the connection is given to it.
func (t *CallTimings) targetTLSDone(start time.Time) {
	if t == nil {
		return
	}
	t.lock.Lock()
	defer t.lock.Unlock()
	t.tls.add(time.Since(start))
}

// tunnelAnswered notes that the proxy answered the CONNECT request, with the headers given. The exchange started when
// the connection to the proxy was ready for it.
func (t *CallTimings) tunnelAnswered(header http.Header) {
	if t == nil {
		return
	}
	t.lock.Lock()
	defer t.lock.Unlock()
	from := t.readyAt
	if from.IsZero() {
		from = t.dial.ConnectedAt()
	}
	if !from.IsZero() {
		t.tunnel.add(time.Since(from))
	}
	t.answer = header.Clone()
}

// HeadReceived notes that the head of the response of the call was received. The time that it took is counted from the
// moment that the request was written, which is when the wait for it started.
func (t *CallTimings) HeadReceived() {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.headAt = time.Now()
	if !t.wroteAt.IsZero() {
		t.head = phaseTime{d: t.headAt.Sub(t.wroteAt), set: true}
	}
}

// BodyRead notes that the body of the response was read, which is the end of the call.
func (t *CallTimings) BodyRead() {
	t.lock.Lock()
	defer t.lock.Unlock()
	now := time.Now()
	if !t.headAt.IsZero() {
		t.body = phaseTime{d: now.Sub(t.headAt), set: true}
	}
	t.total = phaseTime{d: now.Sub(t.start), set: true}
}

// Headers gives the value of each header that reports a phase of the call, which is "-" for a phase that it didn't go
// through. The headers are the ones that are named in the constants of the client timings, and the ones that carry the
// timings that the proxy reported in its answer to the CONNECT request.
func (t *CallTimings) Headers() map[string]string {
	t.lock.Lock()
	defer t.lock.Unlock()
	dns := phaseTime{}
	if d, ok := t.dial.DNS(); ok {
		dns.add(d)
	}
	tcp := phaseTime{}
	if d, ok := t.dial.Dial(); ok {
		tcp.add(d)
	}
	answered := func(name string) string {
		if v := t.answer.Get(name); v != "" {
			return v
		}
		return "-"
	}
	return map[string]string{
		constants.HeaderGotoClientDNSTook:          dns.String(),
		constants.HeaderGotoClientTCPTook:          tcp.String(),
		constants.HeaderGotoClientProxyTLSTook:     t.proxyTLS.String(),
		constants.HeaderGotoClientTunnelTook:       t.tunnel.String(),
		constants.HeaderGotoClientTLSTook:          t.tls.String(),
		constants.HeaderGotoClientResponseHeadTook: t.head.String(),
		constants.HeaderGotoClientBodyTook:         t.body.String(),
		constants.HeaderGotoClientTotalTook:        t.total.String(),

		constants.HeaderGotoConnectProxyTook:     answered(constants.HeaderGotoConnectProxyTook),
		constants.HeaderGotoConnectProxyDNSTook:  answered(constants.HeaderGotoConnectProxyDNSTook),
		constants.HeaderGotoConnectProxyDialTook: answered(constants.HeaderGotoConnectProxyDialTook),
		constants.HeaderGotoConnectProxyDelay:    answered(constants.HeaderGotoConnectProxyDelay),
	}
}
