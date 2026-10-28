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
	"bytes"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A tunnel of a CONNECT proxy is opaque to the proxy, except when what it carries is plain HTTP/1.x: the proxy then follows
// the messages of both directions, one after the other, to add its identity to the Via-Goto header of the responses, the
// way the HTTP proxy does, along with the other headers that the HTTP proxy tells about itself and the destination (the
// status of the destination, the time that the proxy took, the delay that it added). Everything else about the messages
// stays what it was: they are passed on byte for byte, and the proxy only adds header lines to the head of a response,
// which is also how a Via-Goto header that the destination sent (e.g. a goto server) is kept, since the values of the
// header are the lines that it has.
//
// The proxy has to follow the requests as well as the responses, because the framing of a response depends on the request
// that it answers to (the response to a HEAD request has no body, nor has the success of a CONNECT). Whenever the proxy
// can't make sense of what a direction carries, it passes on what it read and everything that follows as it is.

const (
	// connectBufferSize is the size of the buffers that the directions of a tunnel read through.
	connectBufferSize = 32 * 1024
	// connectMaxStartLine bounds the line of a request that is looked at to tell whether it's HTTP.
	connectMaxStartLine = 8 * 1024
)

var (
	// connectMaxHeadBytes bounds the head (start line and headers) of an HTTP message that the proxy looks into.
	connectMaxHeadBytes = 64 * 1024
	// connectMaxPipelined bounds the requests of a tunnel that wait for their responses to be known of.
	connectMaxPipelined = 4096

	errMalformedHTTP = errors.New("malformed HTTP message")
	errHeadTooLarge  = errors.New("HTTP message head too large")
)

// httpHead is the head of an HTTP/1.x message, as it was read, and what the proxy needs to know of it.
type httpHead struct {
	raw     []byte
	method  string
	status  int
	chunked bool
	// length is the length of the body that the head declares. A request that doesn't declare any has no body (0), and a
	// response that doesn't has one that lasts until the connection closes (-1).
	length int64
}

// exchange is what the proxy knows of a request that a tunnel carried, for the response to it.
type exchange struct {
	method string
	// start is when the first byte of the request got to the proxy, which is zero if the proxy doesn't know.
	start time.Time
	// delayed is what the proxy had delayed the tunnel by when the request got to it (see connectTunnel.delayed).
	delayed time.Duration
}

// methodQueue holds the requests that a tunnel carried, to be matched with the responses that come back in the same
// order. A request is queued before it is passed on, and a destination can't answer a request that it has not got, so a
// response always finds its request, unless the proxy couldn't make sense of the requests.
type methodQueue struct {
	lock      sync.Mutex
	exchanges []exchange
}

func (q *methodQueue) push(ex exchange) {
	q.lock.Lock()
	defer q.lock.Unlock()
	if len(q.exchanges) < connectMaxPipelined {
		q.exchanges = append(q.exchanges, ex)
	}
}

// pop gives the request that the next response answers to, which is an empty one (no method) if it's not known.
func (q *methodQueue) pop() exchange {
	q.lock.Lock()
	defer q.lock.Unlock()
	if len(q.exchanges) == 0 {
		return exchange{}
	}
	ex := q.exchanges[0]
	q.exchanges = q.exchanges[1:]
	return ex
}

// connectViaGoto is the identity that the proxy of a port adds to the Via-Goto header of the responses that it passes on.
func connectViaGoto(port int) string {
	label := ""
	if global.Funcs.GetListenerLabelForPort != nil {
		label = util.GetViaGotoValue(port)
	}
	return cleanHeaderValue(label + "(Proxy)(HTTP)")
}

var (
	// The identity of the proxy is told in headers of its own, as the destination is likely to be a goto that has the
	// Goto-Host and Goto-Port headers to tell, which must not be mixed up with the ones of the proxy: the lines of the
	// destination are kept as they are.
	headerProxyHost          = constants.HeaderGotoConnectProxyHost
	headerProxyPort          = constants.HeaderGotoConnectProxyPort
	headerProxyRemoteAddress = constants.HeaderGotoConnectProxyRemoteAddress

	headerLineBreaks = strings.NewReplacer("\r", "", "\n", "")
)

// headerLine is a header that the proxy adds to the head of a response.
type headerLine struct {
	name  string
	value string
}

// cleanHeaderValue makes a value one that fits in the line of a header.
func cleanHeaderValue(value string) string {
	return headerLineBreaks.Replace(value)
}

// proxyIdentity gives the headers that tell about the proxy of a port, and the client that the tunnel is for, which are the
// same for every response of the tunnel.
func proxyIdentity(port int, clientAddr string) []headerLine {
	identity := []headerLine{{constants.HeaderViaGoto, connectViaGoto(port)}}
	if global.Self.HostLabel != "" {
		identity = append(identity, headerLine{headerProxyHost, global.Self.HostLabel})
	}
	return append(identity,
		headerLine{headerProxyPort, strconv.Itoa(port)},
		headerLine{headerProxyRemoteAddress, clientAddr})
}

// upstreamStatusHeader gives the value of the header that tells the status that the destination answered with, which has
// the form that the one of the HTTP proxy has: the statuses by target and endpoint. The destination of a tunnel is both.
func upstreamStatusHeader(destination string, status int) string {
	return util.ToJSONText(map[string]map[string]int{destination: {destination: status}})
}

// responseHeaders gives the headers that the proxy adds to the head of a response, which are its identity, the status
// that the destination answered with, and, if the request that it answers is known, the time that the proxy took from
// the first byte of the request to the head of the response, and the delays that it added in the meantime.
func (tn *connectTunnel) responseHeaders(status int, ex exchange) []headerLine {
	headers := append(make([]headerLine, 0, len(tn.identity)+3), tn.identity...)
	headers = append(headers, headerLine{constants.HeaderGotoConnectProxyUpstreamStatus, upstreamStatusHeader(tn.destination, status)})
	if !ex.start.IsZero() {
		headers = append(headers, headerLine{constants.HeaderGotoConnectProxyTook, time.Since(ex.start).String()})
		if delay := tn.delayedFor() - ex.delayed; delay > 0 {
			headers = append(headers, headerLine{constants.HeaderGotoConnectProxyDelay, delay.String()})
		}
	}
	return headers
}

// forwardRequests passes on what the client sends to the destination, and keeps the methods of the requests, if it's HTTP.
func (tn *connectTunnel) forwardRequests(r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, connectBufferSize)
	// Once the requests are not followed, the methods of the ones to come are not known, and the responses can't be
	// told the end of (the one to a HEAD request has a length, and no body)
	lost := func() { tn.requestsLost.Store(true) }
	for {
		head, err := nextHTTPHead(br, w, false, lost)
		if head == nil {
			return err
		}
		start, delayed := tn.firstByte()
		tn.requests.push(exchange{method: head.method, start: start, delayed: delayed})
		if _, err := w.Write(head.raw); err != nil {
			return err
		}
		if err := copyBody(w, br, head, lost); err != nil {
			return err
		}
		tn.requestDone()
	}
}

// forwardResponses passes on what the destination sends to the client. The heads of the responses get the headers of the
// proxy (see responseHeaders), if it's HTTP.
func (tn *connectTunnel) forwardResponses(r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, connectBufferSize)
	first := true
	for {
		head, err := nextHTTPHead(br, w, true, nil)
		if head == nil {
			return err
		}
		if first {
			first = false
			log.Printf("TCP Proxy[%d]: Tunnel to [%s] carries HTTP, adding Via-Goto [%s] and the proxy headers to the responses\n", tn.port, tn.destination, tn.via)
		}
		// An informational response is followed by the response that is the answer to the request, and it is the one that
		// the proxy stamps. A protocol switch is the answer to its request.
		informational := head.status >= 100 && head.status < 200 && head.status != http.StatusSwitchingProtocols
		out := head.raw
		method := ""
		if !informational {
			ex := tn.requests.pop()
			method = ex.method
			out = withHeaders(head.raw, tn.responseHeaders(head.status, ex))
		}
		if _, err := w.Write(out); err != nil {
			return err
		}
		switch {
		case informational:
		case tn.requestsLost.Load():
			// The proxy can't tell which request this is the response to, so it can't tell the end of it either
			return copyRest(w, br)
		case head.status == http.StatusSwitchingProtocols || (method == http.MethodConnect && head.status/100 == 2):
			// What follows is not HTTP anymore
			return copyRest(w, br)
		case method == http.MethodHead || head.status == http.StatusNoContent || head.status == http.StatusNotModified:
			// These have no body, whatever their headers say
		case head.chunked || head.length >= 0:
			if err := copyBody(w, br, head, nil); err != nil {
				return err
			}
		default:
			// The body lasts until the destination closes the connection
			return copyRest(w, br)
		}
	}
}

// nextHTTPHead reads the head of the next message of a direction, and passes it on after the proxy has had its way with it
// if it's a head to give. If there's none to give, because the direction is not HTTP, it is over, or it fails, the head is
// nil, and the error is what the caller has to return, after what is left of the direction was passed on as it was. The
// lost function, if there is one, is called when the direction is not followed anymore, before anything of it is passed
// on: not when it is over.
func nextHTTPHead(br *bufio.Reader, w io.Writer, response bool, lost func()) (*httpHead, error) {
	if ok, perr := peekHTTPStart(br, response); !ok {
		if perr == nil || br.Buffered() > 0 {
			notifyLost(lost)
		}
		return nil, copyRest(w, br)
	}
	raw, err := readHTTPHead(br)
	if err != nil {
		unfollowable := errors.Is(err, errMalformedHTTP) || errors.Is(err, errHeadTooLarge)
		if unfollowable {
			notifyLost(lost)
		}
		if len(raw) > 0 {
			if _, werr := w.Write(raw); werr != nil {
				return nil, werr
			}
		}
		if unfollowable {
			return nil, copyRest(w, br)
		}
		return nil, err
	}
	head, err := parseHTTPHead(raw, response)
	if err != nil {
		notifyLost(lost)
		if _, werr := w.Write(raw); werr != nil {
			return nil, werr
		}
		return nil, copyRest(w, br)
	}
	return head, nil
}

func notifyLost(lost func()) {
	if lost != nil {
		lost()
	}
}

// copyRest passes on all that is left of a direction. It gives nil when the direction ended.
func copyRest(w io.Writer, br *bufio.Reader) error {
	_, err := io.Copy(w, br)
	return err
}

// copyBody passes on the body of a message that has the given head.
func copyBody(w io.Writer, br *bufio.Reader, head *httpHead, lost func()) error {
	var err error
	switch {
	case head.chunked:
		err = copyChunked(w, br)
	case head.length > 0:
		_, err = io.CopyN(w, br, head.length)
	}
	if errors.Is(err, errMalformedHTTP) {
		notifyLost(lost)
		return copyRest(w, br)
	}
	return err
}

// copyChunked passes on a body that is sent in chunks, as it is sent: the sizes of the chunks, their extensions, and the
// trailers.
func copyChunked(w io.Writer, br *bufio.Reader) error {
	for {
		line, err := copyLine(w, br)
		if err != nil {
			return err
		}
		size, ok := parseChunkSize(line)
		if !ok {
			return errMalformedHTTP
		}
		if size == 0 {
			// The trailers end with a blank line
			for {
				line, err := copyLine(w, br)
				if err != nil {
					return err
				}
				if isBlankLine(line) {
					return nil
				}
			}
		}
		if _, err := io.CopyN(w, br, size); err != nil {
			return err
		}
		// The line break that ends the data of a chunk
		if line, err = copyLine(w, br); err != nil {
			return err
		} else if !isBlankLine(line) {
			return errMalformedHTTP
		}
	}
}

// copyLine passes on a line, and gives it. What was read of a line that has no end is passed on as well.
func copyLine(w io.Writer, br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadSlice('\n')
	if len(line) > 0 {
		if _, werr := w.Write(line); werr != nil {
			return nil, werr
		}
	}
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, errMalformedHTTP
	}
	return line, err
}

func parseChunkSize(line []byte) (int64, bool) {
	size := strings.TrimSpace(strings.SplitN(string(line), ";", 2)[0])
	n, err := strconv.ParseUint(size, 16, 62)
	return int64(n), err == nil
}

func isBlankLine(line []byte) bool {
	return len(line) == 1 && line[0] == '\n' || len(line) == 2 && line[0] == '\r' && line[1] == '\n'
}

// readHTTPHead reads a head, which ends with a blank line. What was read is given even if it is not a whole head.
func readHTTPHead(br *bufio.Reader) ([]byte, error) {
	var raw []byte
	// A line that is longer than the buffer is read in pieces, and it's only whole when its end is
	lineStart := 0
	for {
		line, err := br.ReadSlice('\n')
		raw = append(raw, line...)
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(raw) > connectMaxHeadBytes {
				return raw, errHeadTooLarge
			}
			continue
		}
		if err != nil {
			return raw, err
		}
		if len(raw) > connectMaxHeadBytes {
			return raw, errHeadTooLarge
		}
		if isBlankLine(raw[lineStart:]) {
			if lineStart == 0 {
				// There's no start line
				return raw, errMalformedHTTP
			}
			return raw, nil
		}
		lineStart = len(raw)
	}
}

// parseHTTPHead reads what the proxy needs to know of a head: the method of a request, the status of a response, and the
// length of the body that goes with it. A head that is not one of HTTP/1.x, or says things that don't add up (e.g. two
// different lengths), is an error, as the proxy can't tell where the message ends.
func parseHTTPHead(raw []byte, response bool) (*httpHead, error) {
	eol := bytes.IndexByte(raw, '\n')
	if eol < 0 {
		return nil, errMalformedHTTP
	}
	startLine := strings.TrimRight(string(raw[:eol]), "\r")
	head := &httpHead{raw: raw}
	if response {
		parts := strings.SplitN(startLine, " ", 3)
		if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/1.") || len(parts[1]) != 3 {
			return nil, errMalformedHTTP
		}
		status, err := strconv.Atoi(parts[1])
		if err != nil || status < 100 {
			return nil, errMalformedHTTP
		}
		head.status = status
		head.length = -1
	} else {
		parts := strings.Split(startLine, " ")
		if len(parts) != 3 || !isHTTPMethod(parts[0]) || !strings.HasPrefix(parts[2], "HTTP/1.") {
			return nil, errMalformedHTTP
		}
		head.method = parts[0]
	}
	header, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw[eol+1:]))).ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errMalformedHTTP, err.Error())
	}
	if te := header["Transfer-Encoding"]; len(te) > 0 {
		codings := strings.Split(strings.Join(te, ","), ",")
		if strings.EqualFold(strings.TrimSpace(codings[len(codings)-1]), "chunked") {
			head.chunked = true
		} else if !response {
			// A request has to end with chunked to have a body that the proxy can find the end of
			return nil, errMalformedHTTP
		}
		return head, nil
	}
	if values := header["Content-Length"]; len(values) > 0 {
		length := int64(-1)
		for _, value := range values {
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				n, err := strconv.ParseInt(part, 10, 64)
				if err != nil || n < 0 || part[0] == '+' || (length >= 0 && n != length) {
					return nil, errMalformedHTTP
				}
				length = n
			}
		}
		head.length = length
	}
	return head, nil
}

func isHTTPMethod(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isMethodChar(s[i]) {
			return false
		}
	}
	return true
}

func isMethodChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c == '-' || c == '_'
}

// classifyHTTPStart tells whether the beginning of a direction is that of HTTP/1.x messages: a request starts with a
// request line (a method, a target and HTTP/1.x), and a response starts with HTTP/1. It also tells whether it could tell,
// with the bytes that are there, so that the caller knows to wait for more.
//
// A request is not taken for one by the method that it starts with. Text protocols have commands that look like methods
// (HELO example.com), and the proxy holds a head back till it's whole, which a client that waits for an answer to its
// command would never get through.
func classifyHTTPStart(b []byte, response bool) (decided, ok bool) {
	if response {
		const prefix = "HTTP/1."
		n := min(len(b), len(prefix))
		if !bytes.Equal(b[:n], []byte(prefix[:n])) {
			return true, false
		}
		return len(b) >= len(prefix), len(b) >= len(prefix)
	}
	for i, c := range b {
		if c == ' ' {
			if i == 0 {
				return true, false
			}
			break
		}
		if !isMethodChar(c) || i >= 31 {
			return true, false
		}
	}
	eol := bytes.IndexByte(b, '\n')
	if eol < 0 {
		// The request line is not all there. A line that is too long to be one is not.
		return len(b) >= connectMaxStartLine, false
	}
	parts := strings.Split(strings.TrimRight(string(b[:eol]), "\r"), " ")
	return true, len(parts) == 3 && isHTTPMethod(parts[0]) && strings.HasPrefix(parts[2], "HTTP/1.")
}

// peekHTTPStart waits for as many bytes as it takes to tell whether a direction carries HTTP/1.x messages. A direction
// that is not is told by its first byte, in most cases, so the wait is as long as the one for any data. Nothing is
// consumed, and nothing is waited for when the bytes that are there tell.
func peekHTTPStart(br *bufio.Reader, response bool) (bool, error) {
	need := 1
	for {
		b, err := br.Peek(max(need, br.Buffered()))
		if decided, ok := classifyHTTPStart(b, response); decided {
			return ok, nil
		}
		if err != nil {
			return false, err
		}
		need = len(b) + 1
	}
}

// withViaGoto adds the Via-Goto header to a head, as a line of its own after the ones that the head has, which keeps the
// ones that are there already (a Via-Goto header has as many values as the lines that carry it).
func withViaGoto(raw []byte, via string) []byte {
	return withHeaders(raw, []headerLine{{constants.HeaderViaGoto, via}})
}

// withHeaders adds headers to a head, each as a line of its own after the ones that the head has, which keeps the ones
// that are there already, whatever their names (a header has as many values as the lines that carry it).
func withHeaders(raw []byte, headers []headerLine) []byte {
	// The head ends with a blank line, which is where the headers go
	end := bytes.LastIndexByte(raw[:len(raw)-1], '\n') + 1
	// The line break of a header is the one of the blank line
	eol := string(raw[end:])
	size := len(raw)
	for _, h := range headers {
		size += len(h.name) + len(h.value) + len(eol) + 2
	}
	out := make([]byte, 0, size)
	out = append(out, raw[:end]...)
	for _, h := range headers {
		out = append(out, h.name...)
		out = append(out, ": "...)
		out = append(out, cleanHeaderValue(h.value)...)
		out = append(out, eol...)
	}
	out = append(out, raw[end:]...)
	return out
}
