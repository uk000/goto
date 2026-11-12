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
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	gototls "goto/pkg/tls"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	portProxy = map[int]*TCPProxy{}
	proxyLock sync.RWMutex

	// idleTimeout is how long a proxied connection may stay silent, in both directions, before the proxy gives up on it.
	idleTimeout = 5 * time.Second
	// dialTimeout bounds every attempt to connect to an upstream endpoint.
	dialTimeout = 5 * time.Second
	// sniReadTimeout bounds the wait for the TLS client hello on ports that route by SNI.
	sniReadTimeout = 5 * time.Second

	// errMalformedHello is reported for client hellos that the SNI parser can't make sense of at all.
	errMalformedHello = gototls.ErrMalformedClientHello
)

type TCPProxy struct {
	Port      int                     `yaml:"port" json:"port"`
	Enabled   bool                    `yaml:"enabled" json:"enabled"`
	Upstreams map[string]*TCPUpstream `yaml:"upstreams" json:"upstreams"`
	// HTTP is what the port does for the HTTP clients that connect to it: it can be an HTTP CONNECT proxy.
	HTTP    TCPProxyHTTP     `yaml:"http" json:"http"`
	Tracker *TCPProxyTracker `yaml:"tracker" json:"tracker"`
	lock    sync.RWMutex
}

// TCPProxyHTTP is the HTTP side of a TCP proxy port, which is the config of its CONNECT proxy mode:
//
//	http:
//	  connect: true
//	  connectDelay:
//	    min: 1s
//	    max: 2s
//	    count: 5
type TCPProxyHTTP struct {
	// Connect makes the port an HTTP CONNECT proxy: the connections of the port are the ones of clients that ask for a
	// tunnel to a destination, instead of the ones that get proxied to the upstreams. See proxyconnect.go.
	Connect bool `yaml:"connect" json:"connect"`
	// ConnectDelay is the delay that a CONNECT proxy port applies to the connection of a client, and to every transfer of
	// the tunnels that it has. Its count, if it has one, is for the connections and the transfers of the port altogether.
	ConnectDelay *types.Delay `yaml:"connectDelay,omitempty" json:"connectDelay,omitempty"`
}

type TCPEndpoint struct {
	Name    string `yaml:"name" json:"name"`
	Address string `yaml:"address" json:"address"`
}

// TCPUpstream is a set of endpoints that the connections of a proxy port get routed to. The first endpoint, by name,
// is the primary one: its responses go back to the client. Whatever the client sends goes to all the endpoints, and
// the responses of the other endpoints are discarded.
type TCPUpstream struct {
	Name      string                  `yaml:"name" json:"name"`
	Endpoints map[string]*TCPEndpoint `yaml:"endpoints" json:"endpoints"`
	Match     *UpstreamMatch          `yaml:"match" json:"match"`
	Delay     *types.Delay            `yaml:"delay" json:"delay"`
	// Retries is the number of times a failed connection to an endpoint is retried, after the first attempt.
	Retries    int          `yaml:"retries" json:"retries"`
	RetryDelay *types.Delay `yaml:"retryDelay" json:"retryDelay"`
	// DropPct makes the proxy skip every 100/DropPct-th write, in either direction.
	DropPct            int `yaml:"dropPct" json:"dropPct"`
	proxyPort          int
	writeSinceLastDrop int
	lock               sync.RWMutex
}

type UpstreamMatch struct {
	// SNI is a comma separated list of server name patterns (regular expressions) to match client's SNI against.
	SNI       string `yaml:"sni" json:"sni"`
	sniRegexp *regexp.Regexp
}

// newTCPProxy creates an enabled proxy for the port, with no upstreams and a fresh tracker.
func newTCPProxy(port int) *TCPProxy {
	p := &TCPProxy{
		Port:      port,
		Enabled:   true,
		Upstreams: map[string]*TCPUpstream{},
	}
	p.initTracker()
	return p
}

// WillProxyTCP tells whether connections to the given port are to be proxied. It doesn't create a proxy for the port.
func WillProxyTCP(port int) bool {
	proxyLock.RLock()
	p := portProxy[port]
	proxyLock.RUnlock()
	return p != nil && p.isEnabled() && p.hasAnyTargets()
}

// GetPortProxy returns the TCP proxy of the port, creating and registering it if it doesn't exist.
func GetPortProxy(port int) *TCPProxy {
	proxyLock.RLock()
	proxy := portProxy[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxyLock.Lock()
		if proxy = portProxy[port]; proxy == nil {
			proxy = newTCPProxy(port)
			portProxy[port] = proxy
		}
		proxyLock.Unlock()
	}
	return proxy
}

// snapshotProxies returns a copy of the map of the proxies of all the ports.
func snapshotProxies() map[int]*TCPProxy {
	proxyLock.RLock()
	defer proxyLock.RUnlock()
	proxies := make(map[int]*TCPProxy, len(portProxy))
	for port, p := range portProxy {
		proxies[port] = p
	}
	return proxies
}

// ClearAllProxies drops the proxies of all the ports, releasing their CONNECT config.
func ClearAllProxies() {
	proxyLock.Lock()
	old := portProxy
	portProxy = map[int]*TCPProxy{}
	proxyLock.Unlock()
	for _, p := range old {
		p.releaseConnect()
	}
}

// ClearPortProxy replaces the proxy of the port with a new empty one, releasing the CONNECT config of the old one.
func ClearPortProxy(port int) {
	proxyLock.Lock()
	old := portProxy[port]
	portProxy[port] = newTCPProxy(port)
	proxyLock.Unlock()
	old.releaseConnect()
}

// releaseConnect takes the CONNECT config away from a proxy that was replaced. The tunnels that are open keep the proxy
// that they were made through, and would go on delaying their transfers with the delay that it had.
func (p *TCPProxy) releaseConnect() {
	if p == nil {
		return
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	p.HTTP = TCPProxyHTTP{}
}

// parseUpstreams reads upstreams as JSON from the reader and validates them, turning a panic of the parsers into an error.
func parseUpstreams(r io.Reader) (upstreams map[string]*TCPUpstream, err error) {
	// The payload parsers of the shared types can panic on partial values (e.g. a delay with only a min)
	defer func() {
		if rec := recover(); rec != nil {
			upstreams, err = nil, fmt.Errorf("invalid upstreams payload: %v", rec)
		}
	}()
	upstreams = map[string]*TCPUpstream{}
	if err = util.ReadJsonPayloadFromBody(r, &upstreams); err != nil {
		return nil, err
	}
	if err = ValidateUpstreams(upstreams); err != nil {
		return nil, err
	}
	return upstreams, nil
}

// ValidateUpstreams checks the upstreams (names, endpoints, addresses, delays, retries, drop percentage and SNI match) and fills in missing names.
func ValidateUpstreams(upstreams map[string]*TCPUpstream) error {
	names := make([]string, 0, len(upstreams))
	for name := range upstreams {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		upstream := upstreams[name]
		if upstream == nil {
			return fmt.Errorf("upstream [%s] is empty", name)
		}
		if upstream.Name == "" {
			upstream.Name = name
		}
		if upstream.Name == "" {
			return errors.New("upstream name missing")
		}
		if len(upstream.Endpoints) == 0 {
			return errors.New("upstream endpoints missing")
		}
		if upstream.Retries < 0 {
			return fmt.Errorf("upstream [%s] has negative retries [%d]", name, upstream.Retries)
		}
		if upstream.DropPct < 0 || upstream.DropPct > 100 {
			return fmt.Errorf("upstream [%s] has invalid dropPct [%d], expected 0-100", name, upstream.DropPct)
		}
		for _, d := range []*types.Delay{upstream.Delay, upstream.RetryDelay} {
			if d != nil && (d.Min == nil || d.Max == nil) {
				return fmt.Errorf("upstream [%s] has a delay without min or max", name)
			}
		}
		if upstream.Match != nil {
			if _, err := compileSNIMatch(upstream.Match.SNI); err != nil {
				return fmt.Errorf("upstream [%s]: %s", name, err.Error())
			}
		}
		for epName, ep := range upstream.Endpoints {
			if ep == nil || ep.Address == "" {
				return fmt.Errorf("target endpoint [%s] missing address/port", epName)
			}
			if err := validateAddress(ep.Address); err != nil {
				return fmt.Errorf("target endpoint [%s]: %s", epName, err.Error())
			}
			if ep.Name == "" {
				ep.Name = epName
			}
		}
	}
	return nil
}

// validateAddress checks that the address is a host and a valid port.
func validateAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid address [%s], expected host:port", address)
	}
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid port in address [%s]", address)
	}
	return nil
}

// compileSNIMatch compiles a comma separated list of SNI patterns into one regular expression, or nil if the list is empty.
func compileSNIMatch(sni string) (*regexp.Regexp, error) {
	names := []string{}
	for _, name := range strings.Split(sni, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	re, err := regexp.Compile("(" + strings.Join(names, "|") + ")")
	if err != nil {
		return nil, fmt.Errorf("invalid SNI match [%s]: %s", sni, err.Error())
	}
	return re, nil
}

// ProxyTCPConnection proxies a connection that the server accepted on the given port, and returns when the connection
// is done. The connection is closed on return.
func ProxyTCPConnection(port int, downConn net.Conn) {
	defer downConn.Close()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TCP Proxy[%d]: Recovered from panic while proxying a connection: %v\n", port, r)
		}
	}()
	startTime := time.Now()
	p := GetPortProxy(port)
	if p.hasSNITargets() {
		p.proxyTCPWithSNI(downConn, startTime)
	} else {
		p.proxyTCPOpaque(downConn, startTime)
	}
}

// initTracker replaces the tracker of the proxy with a fresh one.
func (p *TCPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Tracker = NewTCPTracker(p.Port)
}

// isEnabled tells whether the proxy is enabled.
func (p *TCPProxy) isEnabled() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.Enabled
}

// hasAnyTargets tells whether the proxy has any upstreams.
func (p *TCPProxy) hasAnyTargets() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return len(p.Upstreams) > 0
}

// hasSNITargets tells whether any upstream of the proxy matches on SNI.
func (p *TCPProxy) hasSNITargets() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	for _, target := range p.Upstreams {
		if target != nil && target.Match != nil && target.Match.sniRegexp != nil {
			return true
		}
	}
	return false
}

// MarshalJSON serializes the proxy, using copies of the upstreams map and of the connect delay, which are changed concurrently.
func (p *TCPProxy) MarshalJSON() ([]byte, error) {
	p.lock.RLock()
	type proxyJSON struct {
		Port      int                     `json:"port"`
		Enabled   bool                    `json:"enabled"`
		Upstreams map[string]*TCPUpstream `json:"upstreams"`
		HTTP      TCPProxyHTTP            `json:"http"`
		Tracker   *TCPProxyTracker        `json:"tracker"`
	}
	// the delay is a copy of its own, as the tunnels of the port count it down while the proxy is marshaled
	httpSide := TCPProxyHTTP{Connect: p.HTTP.Connect, ConnectDelay: p.HTTP.ConnectDelay.Clone()}
	v := proxyJSON{Port: p.Port, Enabled: p.Enabled, Upstreams: copyUpstreams(p.Upstreams), HTTP: httpSide, Tracker: p.Tracker}
	p.lock.RUnlock()
	return json.Marshal(v)
}

// copyUpstreams returns a shallow copy of the upstreams map.
func copyUpstreams(upstreams map[string]*TCPUpstream) map[string]*TCPUpstream {
	c := make(map[string]*TCPUpstream, len(upstreams))
	for name, up := range upstreams {
		c[name] = up
	}
	return c
}

// getUpstreams returns a copy of the map of the upstreams of the proxy.
func (p *TCPProxy) getUpstreams() map[string]*TCPUpstream {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return copyUpstreams(p.Upstreams)
}

// AddUpstreams sets the upstreams of the proxy, replacing the ones it had. Upstreams whose SNI match can't be compiled
// are not added, because they'd otherwise take all the traffic of the port.
func (p *TCPProxy) AddUpstreams(upstreams map[string]*TCPUpstream) {
	added := make(map[string]*TCPUpstream, len(upstreams))
	regexps := map[string]*regexp.Regexp{}
	for name, upstream := range upstreams {
		if upstream == nil {
			continue
		}
		if upstream.Match != nil {
			re, err := compileSNIMatch(upstream.Match.SNI)
			if err != nil {
				log.Printf("TCP Proxy[%d]: Ignoring upstream [%s]: %s\n", p.Port, name, err.Error())
				continue
			}
			regexps[name] = re
		}
		added[name] = upstream
	}
	p.lock.Lock()
	for name, upstream := range added {
		if upstream.Name == "" {
			upstream.Name = name
		}
		if upstream.Match != nil {
			upstream.Match.sniRegexp = regexps[name]
		}
		upstream.proxyPort = p.Port
	}
	p.Upstreams = added
	p.lock.Unlock()
}

// addNewUpstream adds an upstream that has the one endpoint at the address, keyed and named by the address.
func (p *TCPProxy) addNewUpstream(name, address string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.Upstreams == nil {
		p.Upstreams = map[string]*TCPUpstream{}
	}
	p.Upstreams[address] = &TCPUpstream{
		Name: address,
		Endpoints: map[string]*TCPEndpoint{
			name: {Name: name, Address: address},
		},
		proxyPort: p.Port,
	}
}

// getMatchingTCPUpstream picks the upstream for a client that asks for the given SNI, which is empty if the client
// didn't ask for any. Upstreams are considered in the order of their names, so the choice doesn't vary. A client that
// asks for an SNI goes to the first upstream whose match accepts it. A client that doesn't ask for any goes to the
// first upstream that has no SNI match to satisfy, which is any of them on ports that don't route by SNI.
func (p *TCPProxy) getMatchingTCPUpstream(sni string) *TCPUpstream {
	p.lock.RLock()
	defer p.lock.RUnlock()
	names := make([]string, 0, len(p.Upstreams))
	for name := range p.Upstreams {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		up := p.Upstreams[name]
		if up == nil {
			continue
		}
		hasMatch := up.Match != nil && up.Match.sniRegexp != nil
		if sni == "" {
			if !hasMatch {
				return up
			}
		} else if hasMatch && up.Match.sniRegexp.MatchString(sni) {
			return up
		}
	}
	return nil
}

// proxyTCPOpaque proxies a connection to the first upstream that has no SNI match, without reading anything from it.
func (p *TCPProxy) proxyTCPOpaque(downConn net.Conn, startTime time.Time) {
	downAddr := downConn.RemoteAddr().String()
	up := p.getMatchingTCPUpstream("")
	if up == nil {
		p.Tracker.IncrementRejectCount("")
		log.Printf("TCP Proxy[%d]: No matching target found for downstream client [%s]\n", p.Port, downAddr)
		return
	}
	st := p.Tracker.GetOrAddTargetSessionTracker(p.Port, up.Name, downAddr)
	p.Tracker.IncrementMatchCounts(p.Port, up.Name, downAddr, "")
	st.begin(downAddr, startTime)
	st.proxyPipe(downConn, up, nil)
}

// proxyTCPWithSNI reads the SNI of the client hello, routes the connection to the upstream that matches it, and forwards the hello to it.
func (p *TCPProxy) proxyTCPWithSNI(downConn net.Conn, startTime time.Time) {
	downAddr := downConn.RemoteAddr().String()
	sni, hello, err := readClientSNI(downConn)
	if err != nil {
		log.Printf("TCP Proxy[%d]: Error while reading downstream SNI from Connection [%s]: %s\n", p.Port, downAddr, err.Error())
		if errors.Is(err, errMalformedHello) {
			p.Tracker.IncrementRejectCount("")
			return
		}
	} else {
		log.Printf("TCP Proxy[%d]: Read downstream SNI = [%s]\n", p.Port, sni)
	}
	up := p.getMatchingTCPUpstream(sni)
	if up == nil {
		p.Tracker.IncrementRejectCount(sni)
		log.Printf("TCP Proxy[%d]: No matching target found for SNI [%s] from downstream [%s]\n", p.Port, sni, downAddr)
		return
	}
	st := p.Tracker.GetOrAddTargetSessionTracker(p.Port, up.Name, downAddr)
	p.Tracker.IncrementMatchCounts(p.Port, up.Name, downAddr, sni)
	st.begin(downAddr, startTime)
	st.setSNI(sni)
	st.downTracker().recordPreRead(len(hello), time.Now())
	st.proxyPipe(downConn, up, hello)
}

// readClientSNI reads the TLS client hello off a connection, without answering it. It returns the SNI that the client
// asked for, and the bytes that were read, which have to be forwarded to whoever the connection gets routed to.
func readClientSNI(conn net.Conn) (sni string, hello []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			sni, hello, err = "", nil, fmt.Errorf("%w: %v", errMalformedHello, r)
		}
	}()
	_ = conn.SetReadDeadline(time.Now().Add(sniReadTimeout))
	defer conn.SetReadDeadline(time.Time{})
	sni, buff, err := gototls.ReadTLSSNIFromConn(conn)
	return sni, buff.Bytes(), err
}

// sortedEndpoints returns the endpoints of the upstream in the order of their names, so the first one is the primary.
func (t *TCPUpstream) sortedEndpoints() []*TCPEndpoint {
	t.lock.RLock()
	defer t.lock.RUnlock()
	names := make([]string, 0, len(t.Endpoints))
	for name := range t.Endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	endpoints := make([]*TCPEndpoint, 0, len(names))
	for _, name := range names {
		if ep := t.Endpoints[name]; ep != nil {
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints
}

// MarshalJSON serializes the upstream, using copies of its delays, which count down concurrently.
func (t *TCPUpstream) MarshalJSON() ([]byte, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	type upstreamJSON struct {
		Name       string                  `json:"name"`
		Endpoints  map[string]*TCPEndpoint `json:"endpoints"`
		Match      *UpstreamMatch          `json:"match"`
		Delay      *types.Delay            `json:"delay"`
		Retries    int                     `json:"retries"`
		RetryDelay *types.Delay            `json:"retryDelay"`
		DropPct    int                     `json:"dropPct"`
	}
	v := upstreamJSON{Name: t.Name, Endpoints: t.Endpoints, Match: t.Match, Retries: t.Retries, DropPct: t.DropPct}
	v.Delay = t.Delay.Clone()
	v.RetryDelay = t.RetryDelay.Clone()
	return json.Marshal(v)
}

// nextDelay computes the next delay that the given delay config asks for, and uses up a count of it.
func nextDelay(d *types.Delay) time.Duration {
	return d.Claim()
}

// nextDelay takes the next write delay of the upstream, and uses up a count of it.
func (t *TCPUpstream) nextDelay() time.Duration {
	t.lock.Lock()
	defer t.lock.Unlock()
	return nextDelay(t.Delay)
}

// nextRetryDelay takes the next delay to wait before retrying a connection, and uses up a count of it.
func (t *TCPUpstream) nextRetryDelay() time.Duration {
	t.lock.Lock()
	defer t.lock.Unlock()
	return nextDelay(t.RetryDelay)
}

// shouldDrop tells whether the current write is one to skip, which is every 100/DropPct-th write.
func (t *TCPUpstream) shouldDrop() bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.DropPct <= 0 {
		return false
	}
	t.writeSinceLastDrop++
	if t.writeSinceLastDrop >= (100 / t.DropPct) {
		t.writeSinceLastDrop = 0
		return true
	}
	return false
}

// debugLogs tells whether the proxy debug logs are enabled.
func debugLogs() bool {
	return global.Flags.EnableProxyDebugLogs
}
