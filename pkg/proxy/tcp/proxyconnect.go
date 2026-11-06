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

package tcpproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/listeners"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// An HTTP CONNECT proxy is a TCP proxy: once the client's CONNECT request is answered, what's left of the connection is
// an opaque stream of bytes between the client and the destination that the request asked for. A port is a CONNECT
// proxy port when its listener is a TCP listener, so the HTTP server never gets to see its connections, and its proxy
// is in CONNECT mode. A TCP listener that is a TLS one (protocol tls) makes the client talk to the proxy over TLS: the
// handshake is done by the TCP server before the connection gets here, and the tunnel carries whatever the client
// speaks to the destination (a TLS handshake with the destination, for the most part) on top of it.

var (
	// connectReadTimeout bounds the wait for the CONNECT request of a client.
	connectReadTimeout = 10 * time.Second
	// connectDialTimeout bounds the attempt to connect to the destination of a tunnel.
	connectDialTimeout = 10 * time.Second
	// connectIdleTimeout is how long a tunnel may stay silent, in both directions, before the proxy gives up on it.
	// Unlike the connections of a TCP proxy, the ones of a tunnel are the long lived ones of the clients of the proxy.
	connectIdleTimeout = 5 * time.Minute
	// connectMaxRequestBytes bounds what the proxy reads for the CONNECT request, which is the request line and headers.
	connectMaxRequestBytes int64 = 64 * 1024
)

// EnableConnect turns the CONNECT mode of a port on or off. Turning it off doesn't create a proxy for the port.
func EnableConnect(port int, enable bool) {
	if enable {
		GetPortProxy(port).enableConnect(true)
		return
	}
	proxyLock.RLock()
	p := portProxy[port]
	proxyLock.RUnlock()
	if p != nil {
		p.enableConnect(false)
	}
}

// EnableConnectProxy marks the port as a TCP port, and turns its CONNECT mode on. A port that has a TCP listener keeps
// it, which is how a CONNECT proxy port speaks TLS to its clients: the listener is one of protocol tls. A port that has
// a listener of another kind gets a TCP one instead, which closes the one that it had.
func EnableConnectProxy(port int) error {
	if err := listeners.AddListener(port, true, false, ""); err != nil {
		return err
	}
	EnableConnect(port, true)
	return nil
}

// SetConnectDelay sets the delay that the CONNECT proxy of the port applies to the connection of a client, and to every
// transfer of its tunnels, the ones that are already open included. No delay (nil) takes the delay away, which doesn't
// create a proxy for the port.
func SetConnectDelay(port int, delay *types.Delay) {
	if delay != nil {
		GetPortProxy(port).setConnectDelay(delay)
		return
	}
	proxyLock.RLock()
	p := portProxy[port]
	proxyLock.RUnlock()
	if p != nil {
		p.setConnectDelay(nil)
	}
}

// setConnectDelay sets the delay that the CONNECT proxy applies to connections and transfers, or clears it if nil.
func (p *TCPProxy) setConnectDelay(delay *types.Delay) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.HTTP.ConnectDelay = delay
}

// nextConnectDelay is the delay for the next event (a connection, or a transfer) of the CONNECT proxy, which uses up a
// count of the delay if it has one. It is 0 if there is no delay, or the count of it is used up.
func (p *TCPProxy) nextConnectDelay() time.Duration {
	if p == nil {
		return 0
	}
	p.lock.RLock()
	delay := p.HTTP.ConnectDelay
	p.lock.RUnlock()
	return delay.Claim()
}

// WillProxyConnect tells whether the connections to the given port are the ones of the clients of a CONNECT proxy. It
// doesn't create a proxy for the port.
func WillProxyConnect(port int) bool {
	proxyLock.RLock()
	p := portProxy[port]
	proxyLock.RUnlock()
	return p != nil && p.isConnectEnabled()
}

// enableConnect turns the CONNECT mode of the proxy on or off.
func (p *TCPProxy) enableConnect(enable bool) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.HTTP.Connect = enable
}

// isConnectEnabled tells whether the proxy is in CONNECT mode.
func (p *TCPProxy) isConnectEnabled() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.HTTP.Connect
}

// getTracker gives the tracker of the proxy, which is replaced when the trackers are cleared.
func (p *TCPProxy) getTracker() *TCPProxyTracker {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.Tracker
}

// ProxyConnectConnection serves a connection that the server accepted on a port in CONNECT mode, and returns when the
// tunnel that the client asked for is done. The connection is closed on return. For a TLS port the connection is the
// one that the TLS handshake already happened on.
func ProxyConnectConnection(port int, downConn net.Conn) {
	defer downConn.Close()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TCP Proxy[%d]: Recovered from panic while tunneling a connection: %v\n", port, r)
		}
	}()
	startTime := time.Now()
	proxy := GetPortProxy(port)
	tracker := proxy.getTracker()
	downAddr := downConn.RemoteAddr().String()
	req, downReader, firstByte, err := readConnectRequest(downConn)
	if firstByte.IsZero() {
		firstByte = startTime
	}
	if err != nil {
		tracker.IncrementRejectCount("")
		log.Printf("TCP Proxy[%d]: Failed to read the CONNECT request of client [%s]: %s\n", port, downAddr, err.Error())
		if !errors.Is(err, io.EOF) && !isTimeout(err) && !errors.Is(err, syscall.ECONNRESET) {
			writeConnectResponse(downConn, port, http.StatusBadRequest)
		}
		return
	}
	if req.Method != http.MethodConnect {
		tracker.IncrementRejectCount("")
		log.Printf("TCP Proxy[%d]: Rejecting [%s] request of client [%s], only CONNECT is served\n", port, req.Method, downAddr)
		writeConnectResponse(downConn, port, http.StatusMethodNotAllowed, "Allow: CONNECT")
		return
	}
	destination, err := connectDestination(req)
	if err != nil {
		tracker.IncrementRejectCount("")
		log.Printf("TCP Proxy[%d]: Rejecting CONNECT request of client [%s]: %s\n", port, downAddr, err.Error())
		writeConnectResponse(downConn, port, http.StatusBadRequest)
		return
	}
	// A tunnel is tracked the way a proxied connection is, by the destination that it goes to, and the client address.
	st := tracker.GetOrAddTargetSessionTracker(port, destination, downAddr)
	tracker.IncrementMatchCounts(port, destination, downAddr, "")
	st.begin(downAddr, startTime)
	downT := st.downTracker()
	upT := st.addUpTracker(destination, destination, time.Now())
	// The connection is delayed before the proxy connects to the destination, as it would be if it took the proxy that
	// long to get to it. Requests that are rejected are not.
	connectDelay := proxy.nextConnectDelay()
	if connectDelay > 0 {
		downT.recordDelay()
		log.Printf("TCP Proxy[%d]: Delaying the connection of client [%s] to [%s] by [%s]\n", port, downAddr, destination, connectDelay)
		time.Sleep(connectDelay)
	}
	// what the dial does is timed, to tell the name lookup from the connecting in the time that the answer took
	dial := &util.DialTrace{}
	dialCtx := httptrace.WithClientTrace(context.Background(), dial.ClientTrace())
	upConn, err := (&net.Dialer{Timeout: connectDialTimeout}).DialContext(dialCtx, "tcp", destination)
	if err != nil {
		log.Printf("TCP Proxy[%d]: Failed to connect to [%s] for client [%s]: %s\n", port, destination, downAddr, err.Error())
		now := time.Now()
		upT.markConnectFailed()
		upT.markClosed(now)
		downT.markClosed(now)
		status := http.StatusBadGateway
		if isTimeout(err) {
			status = http.StatusGatewayTimeout
		}
		writeConnectResponse(downConn, port, status, connectTimings(firstByte, connectDelay, dial)...)
		return
	}
	defer upConn.Close()
	upT.init(port, destination, upConn.RemoteAddr().String(), time.Now())
	defer func() {
		now := time.Now()
		downT.markClosed(now)
		upT.markClosed(now)
	}()
	if !writeConnectResponse(downConn, port, http.StatusOK, connectTimings(firstByte, connectDelay, dial)...) {
		log.Printf("TCP Proxy[%d]: Failed to answer CONNECT request of client [%s] for [%s]\n", port, downAddr, destination)
		return
	}
	log.Printf("TCP Proxy[%d]: Tunneling client [%s] to [%s]\n", port, downAddr, destination)
	tn := &connectTunnel{port: port, destination: destination, proxy: proxy, down: downConn, downReader: downReader, up: upConn, downT: downT, upT: upT}
	tn.run()
	log.Printf("TCP Proxy[%d]: Finished tunneling client [%s] to [%s], took [%s] from the first byte to the last byte transferred, delayed by [%s]\n",
		port, downAddr, destination, tn.transferTook(firstByte), connectDelay+tn.delayedFor())
}

// connectTimings gives the headers that tell the time that the proxy took to answer a CONNECT request, from its first byte,
// and the delay that it added to it, if it did. The answer is the head of the response to the request, as the head of a
// tunneled response is, which is what the time is about: nothing is known of the time that the tunnel takes in the end.
// What it took to resolve the name of the destination and to connect to it, which is most of what the proxy has to do to
// answer, is told apart from the rest, for the part of it that the dial got to.
func connectTimings(firstByte time.Time, delay time.Duration, dial *util.DialTrace) []string {
	headers := []string{fmt.Sprintf("%s: %s", constants.HeaderGotoConnectProxyTook, time.Since(firstByte))}
	if delay > 0 {
		headers = append(headers, fmt.Sprintf("%s: %s", constants.HeaderGotoConnectProxyDelay, delay))
	}
	if dial != nil {
		if d, ok := dial.DNS(); ok {
			headers = append(headers, fmt.Sprintf("%s: %s", constants.HeaderGotoConnectProxyDNSTook, d))
		}
		if d, ok := dial.Dial(); ok {
			headers = append(headers, fmt.Sprintf("%s: %s", constants.HeaderGotoConnectProxyDialTook, d))
		}
	}
	return headers
}

// readConnectRequest reads the request that a client opens a tunnel with, which is its request line and headers. The
// client can send more right behind it (e.g. the TLS client hello for the destination), and that is already read by the
// time the request is: the reader that is returned is where the rest of the connection has to be read from. It also tells
// when the first byte of the request came, which is zero if none did.
func readConnectRequest(conn net.Conn) (*http.Request, *bufio.Reader, time.Time, error) {
	first := &util.FirstByteReader{Reader: conn}
	limited := &io.LimitedReader{R: first, N: connectMaxRequestBytes}
	reader := bufio.NewReader(limited)
	_ = conn.SetReadDeadline(time.Now().Add(connectReadTimeout))
	req, err := http.ReadRequest(reader)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, nil, first.At, err
	}
	// The limit is for the request, and not for the tunnel.
	limited.N = 1<<63 - 1
	return req, reader, first.At, nil
}

// connectDestination reads the destination out of the target of a CONNECT request, which is an authority (host:port).
func connectDestination(req *http.Request) (string, error) {
	authority := req.URL.Host
	if authority == "" || (req.URL.Path != "" && req.URL.Path != "/") || req.URL.RawQuery != "" || req.URL.User != nil {
		return "", fmt.Errorf("invalid target [%s], expected host:port", req.RequestURI)
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil || host == "" {
		return "", fmt.Errorf("invalid target [%s], expected host:port", req.RequestURI)
	}
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return "", fmt.Errorf("invalid port in target [%s]", req.RequestURI)
	}
	return net.JoinHostPort(host, port), nil
}

// writeConnectResponse answers a client, with the identity of the proxy of the port in the Via-Goto header, as the proxy
// does for the responses that it passes on. An answer that is not a success ends the exchange, and says so. It tells
// whether the answer was written.
func writeConnectResponse(conn net.Conn, port, status int, headers ...string) bool {
	text := http.StatusText(status)
	if status == http.StatusOK {
		text = "Connection Established"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "HTTP/1.1 %d %s\r\n", status, text)
	for _, h := range headers {
		sb.WriteString(h)
		sb.WriteString("\r\n")
	}
	fmt.Fprintf(&sb, "%s: %s\r\n", constants.HeaderViaGoto, connectViaGoto(port))
	if status != http.StatusOK {
		// No content may be sent in the answer to a success of a CONNECT request, so this is for the other ones only.
		sb.WriteString("Content-Length: 0\r\nConnection: close\r\n")
	}
	sb.WriteString("\r\n")
	_ = conn.SetWriteDeadline(time.Now().Add(connectReadTimeout))
	_, err := io.WriteString(conn, sb.String())
	_ = conn.SetWriteDeadline(time.Time{})
	return err == nil
}

// connectTunnel relays the bytes between a client and the destination that it asked for. Each of the two directions
// ends on its own: when the sender is done sending, the receiver is told so by a half close, and is left to finish
// what it has to say. The tunnel is over when both directions are, or when either of them fails, which ends both.
type connectTunnel struct {
	port        int
	destination string
	// proxy is the proxy of the port, which has the delay for the transfers of the tunnel (see delayTransfer).
	proxy      *TCPProxy
	down       net.Conn
	downReader io.Reader
	up         net.Conn
	downT      *ConnTracker
	upT        *ConnTracker
	// via is what the proxy adds to the Via-Goto header of the HTTP responses that the tunnel carries, identity is the
	// headers that tell about the proxy, which go along with it, and requests are the HTTP requests that it carried. See
	// proxyconnecthttp.go.
	via      string
	identity []headerLine
	requests methodQueue
	// delayed is the sum of the delays that the proxy added to the transfers of the tunnel, in nanoseconds.
	delayed atomic.Int64
	// lastByteAt is when the last byte that was passed on was written, in nanoseconds since the epoch.
	lastByteAt atomic.Int64
	// firstByteAt and firstByteDelayed are about the first byte that the client sent after the last request that was
	// passed on (see requestDone), which is the one that firstByteTaken is set by. They are only used by the direction of
	// the client.
	firstByteAt      time.Time
	firstByteDelayed time.Duration
	firstByteTaken   bool
	// requestsLost is set when the proxy stops following the requests, which makes it stop following the responses.
	requestsLost atomic.Bool
	lastActive   atomic.Int64
	aborted      atomic.Bool
	abortOnce    sync.Once
	// ctx is done when the tunnel is aborted, which ends the delays that are being waited for.
	ctx    context.Context
	cancel context.CancelFunc
}

// touch records the current time as the last time that the tunnel moved data.
func (tn *connectTunnel) touch() {
	tn.lastActive.Store(time.Now().UnixNano())
}

// delayedFor is the sum of the delays that the proxy added to the transfers of the tunnel so far.
func (tn *connectTunnel) delayedFor() time.Duration {
	return time.Duration(tn.delayed.Load())
}

// noteFirstByte is told by the direction of the client about what it got from the client, before the proxy holds it back
// for a delay, so that the time that the proxy takes for a request covers that delay. It keeps the first one that comes
// after the last request that was told the time of.
func (tn *connectTunnel) noteFirstByte(at time.Time) {
	if tn.firstByteAt.IsZero() || tn.firstByteTaken {
		tn.firstByteAt = at
		tn.firstByteDelayed = tn.delayedFor()
		tn.firstByteTaken = false
	}
}

// firstByte tells when the first byte of the request that the direction of the client got the head of reached the proxy,
// and what the tunnel was delayed by until then. A head that came with an earlier one (pipelined) has the time of it.
func (tn *connectTunnel) firstByte() (time.Time, time.Duration) {
	if tn.firstByteAt.IsZero() {
		return time.Now(), tn.delayedFor()
	}
	return tn.firstByteAt, tn.firstByteDelayed
}

// requestDone is told by the direction of the client that the request that it was given the time of is passed on, body
// included, so that the next byte of the client is the first one of another request. What the client sends before that
// (the body) is not.
func (tn *connectTunnel) requestDone() {
	tn.firstByteTaken = true
}

// transferTook is the time from the given first byte of the tunnel to the last byte that the proxy passed on. It is 0 if
// nothing was passed on.
func (tn *connectTunnel) transferTook(first time.Time) time.Duration {
	last := tn.lastByteAt.Load()
	if last == 0 || first.IsZero() {
		return 0
	}
	return max(0, time.Unix(0, last).Sub(first))
}

// idleFor tells how long the tunnel has been without moving data.
func (tn *connectTunnel) idleFor() time.Duration {
	return time.Since(time.Unix(0, tn.lastActive.Load()))
}

// abort closes the connections, which is what gets the directions out of their blocking reads and writes.
func (tn *connectTunnel) abort() {
	tn.abortOnce.Do(func() {
		tn.aborted.Store(true)
		if tn.cancel != nil {
			tn.cancel()
		}
		tn.down.Close()
		tn.up.Close()
	})
}

// run relays the client to the destination and back, each direction in its own goroutine, until both are done, and then aborts the tunnel.
func (tn *connectTunnel) run() {
	tn.ctx, tn.cancel = context.WithCancel(context.Background())
	tn.touch()
	tn.via = connectViaGoto(tn.port)
	tn.identity = proxyIdentity(tn.port, tn.down.RemoteAddr().String())
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		r := &tunnelReader{tn: tn, src: tn.downReader, conn: tn.down, t: tn.downT, side: "downstream"}
		w := &tunnelWriter{tn: tn, dst: tn.up, t: tn.upT, side: "upstream"}
		tn.endRelay(tn.forwardRequests(r, w), tn.up)
	}()
	go func() {
		defer wg.Done()
		r := &tunnelReader{tn: tn, src: tn.up, conn: tn.up, t: tn.upT, side: "upstream"}
		w := &tunnelWriter{tn: tn, dst: tn.down, t: tn.downT, side: "downstream"}
		tn.endRelay(tn.forwardResponses(r, w), tn.down)
	}()
	wg.Wait()
	tn.abort()
}

// endRelay ends a direction of the tunnel, which got to the end of what its source sends (that is the case if there's no
// error, or if it is the end of the stream), or failed.
func (tn *connectTunnel) endRelay(err error, dst net.Conn) {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// The source is done sending. The destination hears about it, and gets to answer.
		if !halfClose(dst) {
			tn.abort()
		}
		return
	}
	tn.abort()
}

// tunnelReader reads what the source of a direction sends, and tracks it. An error that it reads is the end of the
// direction, and is what every read that follows gives.
type tunnelReader struct {
	tn   *connectTunnel
	src  io.Reader
	conn net.Conn
	t    *ConnTracker
	side string
	err  error
}

// Read reads from the source, and tracks the bytes read, holding them back for the transfer delay of the proxy.
func (r *tunnelReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.tn.read(r.src, r.conn, p)
	if n > 0 {
		now := time.Now()
		r.tn.touch()
		r.t.recordRead(n, now)
		if r.side == "downstream" {
			r.tn.noteFirstByte(now)
		}
		if debugLogs() {
			log.Printf("TCP Proxy[%d]: [CONNECT] [READ] [%d] bytes from %s\n", r.tn.port, n, r.side)
		}
		// what was read is held back for the delay of a transfer, which is the same for what is read in one go, however
		// the proxy goes on to pass it on
		if !r.tn.delayTransfer(r.t, r.side) {
			r.err = net.ErrClosed
			return 0, r.err
		}
	}
	if err != nil {
		r.err = err
		r.tn.noteReadError(err, r.t, r.side)
	}
	return n, err
}

// tunnelWriter writes to the destination of a direction, and tracks it. A write that fails ends the tunnel.
type tunnelWriter struct {
	tn   *connectTunnel
	dst  net.Conn
	t    *ConnTracker
	side string
}

// Write writes to the destination, tracks the bytes written, and aborts the tunnel if the write fails.
func (w *tunnelWriter) Write(p []byte) (int, error) {
	_ = w.dst.SetWriteDeadline(time.Now().Add(connectIdleTimeout))
	n, err := w.dst.Write(p)
	if err != nil {
		if !w.tn.aborted.Load() {
			w.t.markWriteError()
			log.Printf("TCP Proxy[%d]: [CONNECT] [WRITE] Error writing to %s connection: %s\n", w.tn.port, w.side, err.Error())
		}
		w.tn.abort()
		return n, err
	}
	now := time.Now()
	w.t.recordWrite(n, now)
	w.tn.lastByteAt.Store(now.UnixNano())
	w.tn.touch()
	return n, nil
}

// delayTransfer holds back a transfer for the delay of the proxy, if there is one for it. The tunnel is not idle while it
// waits. It tells false if the tunnel was aborted in the meantime.
func (tn *connectTunnel) delayTransfer(t *ConnTracker, side string) bool {
	d := tn.proxy.nextConnectDelay()
	if d <= 0 {
		return true
	}
	t.recordDelay()
	if debugLogs() {
		log.Printf("TCP Proxy[%d]: [CONNECT] Delaying transfer from %s by [%s]\n", tn.port, side, d)
	}
	for d > 0 {
		step := d
		if half := connectIdleTimeout / 2; half > 0 && step > half {
			step = half
		}
		if !sleepCtx(tn.ctx, step) {
			return false
		}
		tn.delayed.Add(int64(step))
		tn.touch()
		d -= step
	}
	return true
}

// read reads from the source, and keeps waiting through the idle periods of the source as long as the tunnel is not
// idle altogether, i.e. the other direction is still at it.
func (tn *connectTunnel) read(src io.Reader, srcConn net.Conn, buff []byte) (n int, err error) {
	for {
		_ = srcConn.SetReadDeadline(time.Now().Add(connectIdleTimeout))
		n, err = src.Read(buff)
		if n == 0 && err == nil {
			continue
		}
		if n == 0 && err != nil && isTimeout(err) && !tn.aborted.Load() && tn.idleFor() < connectIdleTimeout {
			continue
		}
		return
	}
}

// noteReadError classifies the error of a read (closed, reset, timeout or other) and marks it on the tracker, unless the tunnel is aborted.
func (tn *connectTunnel) noteReadError(err error, t *ConnTracker, side string) {
	if tn.aborted.Load() {
		return
	}
	switch {
	case errors.Is(err, io.EOF):
		t.markRemoteClosed()
		log.Printf("TCP Proxy[%d]: [CONNECT] [READ] Connection closed by %s\n", tn.port, side)
	case errors.Is(err, syscall.ECONNRESET):
		t.markRemoteClosed()
		log.Printf("TCP Proxy[%d]: [CONNECT] [READ] Connection reset by %s\n", tn.port, side)
	case isTimeout(err):
		t.markReadError()
		log.Printf("TCP Proxy[%d]: [CONNECT] [READ] Read from %s timed out\n", tn.port, side)
	default:
		t.markReadError()
		log.Printf("TCP Proxy[%d]: [CONNECT] [READ] Error reading from %s: %s\n", tn.port, side, err.Error())
	}
}

// halfClose tells the other side of the connection that nothing more is coming, which a plain connection does with a
// FIN, and a TLS one with a close notify. It tells whether the connection is able to.
func halfClose(conn net.Conn) bool {
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite() == nil
	}
	return false
}
