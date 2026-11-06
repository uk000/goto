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

package invocation

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/metrics"
	gotogrpc "goto/pkg/rpc/grpc"
	grpc "goto/pkg/rpc/grpc/client"
	gototls "goto/pkg/tls"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type InvocationSpec struct {
	Name                 string            `json:"name"`
	Protocol             string            `json:"protocol"`
	Method               string            `json:"method"`
	Host                 string            `json:"host"`
	Service              string            `json:"service"`
	URL                  string            `json:"url"`
	BURLS                []string          `json:"burls"`
	Headers              map[string]string `json:"headers"`
	LowerHeaders         bool              `json:"lowerHeaders"`
	Body                 string            `json:"body"`
	AutoPayload          string            `json:"autoPayload"`
	Replicas             int               `json:"replicas"`
	RequestCount         int               `json:"requestCount"`
	WarmupCount          int               `json:"warmupCount"`
	InitialDelay         string            `json:"initialDelay"`
	Delay                string            `json:"delay"`
	Retries              int               `json:"retries"`
	RetryDelay           string            `json:"retryDelay"`
	RetriableStatusCodes []int             `json:"retriableStatusCodes"`
	KeepOpen             string            `json:"keepOpen"`
	SendID               bool              `json:"sendID"`
	RequestId            *types.RequestId  `json:"requestId"`
	ConnTimeout          string            `json:"connTimeout"`
	ConnIdleTimeout      string            `json:"connIdleTimeout"`
	RequestTimeout       string            `json:"requestTimeout"`
	AutoInvoke           bool              `json:"autoInvoke"`
	Fallback             bool              `json:"fallback"`
	AB                   bool              `json:"ab"`
	Random               bool              `json:"random"`
	StreamPayload        []string          `json:"streamPayload"`
	StreamDelay          string            `json:"streamDelay"`
	Binary               bool              `json:"binary"`
	CollectResponse      bool              `json:"collectResponse"`
	TrackPayload         bool              `json:"trackPayload"`
	Assertions           Assertions        `json:"assertions"`
	AutoUpgrade          bool              `json:"autoUpgrade"`
	VerifyTLS            bool              `json:"verifyTLS"`
	TLS                  bool              `json:"tls"`
	NoSNI                bool              `json:"noSNI"`
	TLSVersion           uint16            `json:"tlsVersion"`
	ClientCert           string            `json:"clientCert"`
	ALPN                 *gototls.ALPN     `json:"alpn"`
	BodyReader           io.Reader         `json:"-"`
	ResponseWriter       io.Writer         `json:"-"`
	LongRunning          bool              `json:"-"`
	Transport            transport.ClientTransport
	httpVersionMajor     int
	httpVersionMinor     int
	tcp                  bool
	grpc                 bool
	http                 bool
	h2                   bool
	authority            string
	connTimeoutD         time.Duration
	connIdleTimeoutD     time.Duration
	requestTimeoutD      time.Duration
	initialDelayD        time.Duration
	delayD               time.Duration
	streamDelayD         time.Duration
	retryDelayD          time.Duration
	keepOpenD            time.Duration
	autoPayloadSize      int
	parent               *InvocationSpec
	lock                 *sync.RWMutex
}

type RequestHeaders [][]string

type Assert struct {
	StatusCode    int               `json:"statusCode"`
	PayloadSize   int               `json:"payloadSize"`
	Payload       string            `json:"payload"`
	Headers       map[string]string `json:"headers"`
	Retries       int               `json:"retries"`
	FailedURL     string            `json:"failedURL"`
	SuccessURL    string            `json:"successURL"`
	headersRegexp map[string]*regexp.Regexp
	payload       []byte
}

type Assertions []*Assert

type InvocationTracker struct {
	ID         uint32              `json:"id"`
	ClientPort int                 `json:"clientPort"`
	Target     *InvocationSpec     `json:"target"`
	Status     *InvocationStatus   `json:"status"`
	Payloads   [][]byte            `json:"-"`
	Channels   *InvocationChannels `json:"-"`
	CustomID   string              `json:"customID"`
	OnHeaders  func(http.Header, int, *gototls.PeerCertInfo)
	client     *InvocationClient
}

type InvocationChannels struct {
	StopChannel   chan bool
	DoneChannel   chan bool
	ResultChannel chan *InvocationResult
	Sinks         []ResultSink
	Lock          sync.RWMutex
}

type TargetInvocations struct {
	trackers map[uint32]*InvocationTracker
	lock     sync.RWMutex
}

type InvocationClient struct {
	tracker         *InvocationTracker
	transportClient transport.ClientTransport
	lock            sync.RWMutex
}

type ResultSink func(*InvocationResult)
type ResultSinkFactory func(*InvocationTracker) ResultSink

const (
	maxIdleClientDuration    = 60 * time.Second
	clientConnReportDuration = 5 * time.Second
)

var (
	invocationCounter uint32
	chanStopCleanup   = make(chan bool, 2)
	activeInvocations = map[uint32]*InvocationTracker{}
	activeTargets     = map[string]*TargetInvocations{}
	targetClients     = map[string]transport.ClientTransport{}
	invocationsLock   sync.RWMutex
	_                 = global.OnShutdown(Shutdown)
)

// init starts the background goroutine that monitors target HTTP clients.
func init() {
	go monitorHttpClients()
}

// Shutdown signals the HTTP client monitor goroutine to stop.
func Shutdown() {
	chanStopCleanup <- true
}

// ValidateSpec validates an InvocationSpec and fills in its defaults and derived settings (timeouts, protocol, authority, assertions, lock).
func ValidateSpec(spec *InvocationSpec) error {
	var err error
	if spec.Headers == nil {
		// Specs given without headers must still be safe for callers that add headers to them (e.g. TargetClient.AddTarget).
		spec.Headers = map[string]string{}
	}
	if err = spec.validateTrafficConfig(); err != nil {
		return err
	}
	if err = spec.validateConnectionAndRequestConfigs(); err != nil {
		return err
	}
	if err = spec.validatePayload(); err != nil {
		return err
	}
	spec.processProtocol()
	spec.processAuthority()
	if spec.Assertions != nil {
		spec.prepareAssertions()
	}
	spec.lock = &sync.RWMutex{}
	return nil
}

// processProtocol derives the tcp/grpc/http/h2 flags, HTTP version and TLS setting from the spec's protocol and URL, defaulting to HTTP/1.1.
func (is *InvocationSpec) processProtocol() {
	if is.Protocol != "" {
		lowerProto := strings.ToLower(is.Protocol)
		if strings.EqualFold(lowerProto, "tcp") {
			is.tcp = true
			is.httpVersionMajor = 0
			is.httpVersionMinor = 0
		} else if strings.EqualFold(lowerProto, "grpc") || strings.EqualFold(lowerProto, "grpcs") {
			is.grpc = true
			is.TLS = strings.EqualFold(lowerProto, "grpcs")
			is.httpVersionMajor = 2
			is.httpVersionMinor = 0
		} else if major, minor, ok := http.ParseHTTPVersion(is.Protocol); ok {
			if major == 1 && (minor == 0 || minor == 1) {
				is.httpVersionMajor = major
				is.httpVersionMinor = minor
			} else if major == 2 {
				is.httpVersionMajor = major
				is.httpVersionMinor = 0
			}
		} else if strings.EqualFold(is.Protocol, "HTTP/2") || strings.EqualFold(is.Protocol, "HTTP/2.0") ||
			strings.EqualFold(is.Protocol, "H2") || strings.EqualFold(is.Protocol, "H2C") {
			is.httpVersionMajor = 2
			is.httpVersionMinor = 0
		} else if strings.HasPrefix(strings.ToLower(is.URL), "http") {
			is.http = true
			is.httpVersionMajor = 1
			is.httpVersionMinor = 1
		}
	}
	if !is.tcp && is.httpVersionMajor == 0 {
		is.httpVersionMajor = 1
		is.httpVersionMinor = 1
		is.Protocol = fmt.Sprintf("HTTP/%d.%d", is.httpVersionMajor, is.httpVersionMinor)
	} else if is.httpVersionMajor == 2 {
		is.h2 = true
	}
	if !is.tcp && !is.grpc {
		is.http = true
	}
	if strings.HasPrefix(strings.ToLower(is.URL), "https") {
		is.TLS = true
	}
	if is.http && !strings.HasPrefix(is.URL, "http") {
		is.URL = "http://" + is.URL
	}
}

// processAuthority resolves the request authority from the spec's host, then its Host header, then the URL's host.
func (is *InvocationSpec) processAuthority() {
	is.authority = is.Host
	if is.authority == "" {
		for h, v := range is.Headers {
			if strings.EqualFold(h, "host") {
				is.authority = v
			}
		}
	}
	if is.authority == "" {
		if u, e := url.Parse(is.URL); e == nil {
			is.authority = u.Host
		}
	}
}

// validatePayload validates the auto payload size and stream delay and stores their parsed values.
func (is *InvocationSpec) validatePayload() error {
	if is.AutoPayload != "" {
		is.autoPayloadSize = util.ParseSize(is.AutoPayload)
		if is.autoPayloadSize <= 0 {
			return fmt.Errorf("invalid AutoPayload, must be a valid size like 100, 10K, etc")
		}
	}
	if is.StreamDelay != "" {
		var err error
		if is.streamDelayD, err = time.ParseDuration(is.StreamDelay); err != nil {
			return fmt.Errorf("invalid delay")
		}
	}
	return nil
}

// validateConnectionAndRequestConfigs parses the connection, idle and request timeouts (applying defaults), defaults the TLS version, and checks that the client cert exists.
func (is *InvocationSpec) validateConnectionAndRequestConfigs() error {
	var err error
	if is.ConnTimeout != "" {
		if is.connTimeoutD, err = time.ParseDuration(is.ConnTimeout); err != nil {
			return fmt.Errorf("invalid ConnectionTimeout")
		}
	} else {
		is.connTimeoutD = 5 * time.Second
		is.ConnTimeout = "5s"
	}
	if is.ConnIdleTimeout != "" {
		if is.connIdleTimeoutD, err = time.ParseDuration(is.ConnIdleTimeout); err != nil {
			return fmt.Errorf("invalid ConnectionIdleTimeout")
		}
	} else {
		is.connIdleTimeoutD = 5 * time.Minute
		is.ConnIdleTimeout = "5m"
	}
	if is.RequestTimeout != "" {
		if is.requestTimeoutD, err = time.ParseDuration(is.RequestTimeout); err != nil {
			return fmt.Errorf("invalid RequestIdleTimeout")
		}
	} else {
		is.requestTimeoutD = 5 * time.Minute
		is.RequestTimeout = "5m"
	}
	if is.TLSVersion == 0 {
		is.TLSVersion = tls.VersionTLS13
	}
	if is.ClientCert != "" {
		if _, err := gototls.GetCerts(is.ClientCert); err != nil {
			return err
		}
	}
	return nil
}

// validateTrafficConfig validates the required fields, B-URLs, replicas and request count, and parses the delays, applying defaults.
func (is *InvocationSpec) validateTrafficConfig() error {
	var err error
	if is.Name == "" {
		return fmt.Errorf("name is required")
	}
	if is.Method == "" {
		is.Method = "GET"
	}
	if is.URL == "" {
		return fmt.Errorf("url is required")
	}
	if (is.AB || is.Fallback || is.Random) && len(is.BURLS) == 0 {
		return fmt.Errorf("at least one B-URL is required for Fallback, ABMode or RandomMode")
	}
	if is.Replicas < 0 {
		return fmt.Errorf("invalid replicas")
	} else if is.Replicas == 0 {
		is.Replicas = 1
	}
	if is.RequestCount < 0 {
		return fmt.Errorf("invalid requestCount")
	} else if is.RequestCount == 0 {
		is.RequestCount = 1
	}
	if is.RequestCount < is.Replicas {
		return fmt.Errorf("RequestCount cannot be less than replicas.")
	}
	if is.InitialDelay != "" {
		if is.initialDelayD, err = time.ParseDuration(is.InitialDelay); err != nil {
			return fmt.Errorf("invalid initial delay")
		}
	}
	if is.Delay != "" {
		if is.delayD, err = time.ParseDuration(is.Delay); err != nil {
			return fmt.Errorf("invalid delay")
		}
	}
	if is.RetryDelay != "" {
		if is.retryDelayD, err = time.ParseDuration(is.RetryDelay); err != nil {
			return fmt.Errorf("invalid retryDelay")
		}
	} else {
		is.retryDelayD = 1 * time.Second
		is.RetryDelay = "1s"
	}
	if is.KeepOpen != "" {
		if is.keepOpenD, err = time.ParseDuration(is.KeepOpen); err != nil {
			return fmt.Errorf("invalid keepOpen")
		}
	}
	return nil
}

// prepareAssertions precomputes assertion payloads and header regexps, turning on response payload tracking when a payload or size is asserted.
func (is *InvocationSpec) prepareAssertions() {
	for _, a := range is.Assertions {
		if len(a.Payload) > 0 {
			if is.Binary {
				if b, err := base64.RawStdEncoding.DecodeString(a.Payload); err == nil {
					a.payload = b
				} else {
					a.payload = []byte(a.Payload)
				}
			} else {
				a.payload = []byte(a.Payload)
			}
			a.PayloadSize = len(a.payload)
		}
		if a.PayloadSize > 0 {
			is.CollectResponse = true
			is.TrackPayload = true
		}
		if len(a.Headers) > 0 {
			a.headersRegexp = map[string]*regexp.Regexp{}
			for h, hv := range a.Headers {
				if h != "" {
					h := strings.ToLower(h)
					a.headersRegexp[h] = nil
					if hv != "" {
						a.headersRegexp[h] = regexp.MustCompile("(?i)" + hv)
					}
				}
			}
		}
	}
}

// Clone returns a copy of the spec that has the spec as its parent, and shares the lock of its parent. The lock guards the
// transport that the clones of a long running spec share through their parent, which one of them stores in the parent when
// it creates it, so the parent is copied while holding that lock.
func (is *InvocationSpec) Clone() *InvocationSpec {
	if is.lock != nil {
		is.lock.RLock()
		defer is.lock.RUnlock()
	}
	is2 := *is
	is2.parent = is
	return &is2
}

// createTransport returns the client transport for the spec, sharing the parent's transport for long-running specs and creating an HTTP or gRPC one otherwise.
func (is *InvocationSpec) createTransport(tracker *InvocationTracker) transport.ClientTransport {
	is.lock.Lock()
	defer is.lock.Unlock()
	var ct transport.ClientTransport
	if is.LongRunning && is.parent != nil {
		ct = is.parent.Transport
	}
	if ct == nil {
		if is.http {
			ct = getHttpClientForTarget(tracker)
		} else if is.grpc {
			ct = getGrpcClientForTarget(tracker)
		}
	}
	if ct != nil && is.LongRunning {
		is.Transport = ct
		if is.parent != nil {
			is.parent.Transport = ct
		}
	}
	return ct
}

// GetActiveInvocations returns the status of all currently active invocations, keyed by target name and invocation ID.
func GetActiveInvocations() map[string]map[uint32]*InvocationStatus {
	results := map[string]map[uint32]*InvocationStatus{}
	currentActiveTargets := map[string]*TargetInvocations{}
	invocationsLock.RLock()
	for target, targetInvocations := range activeTargets {
		currentActiveTargets[target] = targetInvocations
	}
	invocationsLock.RUnlock()
	for target, targetInvocations := range currentActiveTargets {
		results[target] = map[uint32]*InvocationStatus{}
		targetInvocations.lock.RLock()
		for _, tracker := range targetInvocations.trackers {
			results[target][tracker.ID] = tracker.Status
		}
		targetInvocations.lock.RUnlock()
	}
	return results
}

// IsAnyTargetActive reports whether any of the given targets has an active invocation.
func IsAnyTargetActive(targets []string) bool {
	invocationsLock.RLock()
	defer invocationsLock.RUnlock()
	for _, target := range targets {
		if activeTargets[target] != nil {
			return true
		}
	}
	return false
}

// StopTarget sends a stop request to every active invocation of the target that isn't already done, stopping or stopped.
func StopTarget(target string) {
	invocationsLock.RLock()
	targetInvocations := activeTargets[target]
	invocationsLock.RUnlock()
	if targetInvocations != nil {
		targetInvocations.lock.RLock()
		for _, tracker := range targetInvocations.trackers {
			done := false
			tracker.Channels.Lock.RLock()
			doneChannel := tracker.Channels.DoneChannel
			stopChannel := tracker.Channels.StopChannel
			stopRequested := tracker.Status.StopRequested
			stopped := tracker.Status.Stopped
			tracker.Channels.Lock.RUnlock()
			select {
			case done = <-doneChannel:
			default:
			}
			if !done {
				if !stopRequested && !stopped {
					stopChannel <- true
				}
			}
		}
		targetInvocations.lock.RUnlock()
	}
}

// processStopRequest polls the tracker's stop channel until a stop request arrives, then flags the invocation status as stop requested.
func processStopRequest(tracker *InvocationTracker) {
	for !tracker.Status.StopRequested && !tracker.Status.Stopped {
		tracker.Channels.Lock.RLock()
		stopChannel := tracker.Channels.StopChannel
		tracker.Channels.Lock.RUnlock()
		if stopChannel != nil {
			stopRequested := false
			select {
			case stopRequested = <-stopChannel:
			default:
			}
			if stopRequested {
				tracker.Status.StopRequested = true
				stopped := tracker.Status.Stopped
				if stopped {
					if global.Flags.EnableInvocationLogs {
						log.Printf("[%s]: Invocation[%d]: Received stop request for target [%s] that is already stopped\n", global.Self.Name, tracker.ID, tracker.Target.Name)
					}
				} else {
					remaining := (tracker.Target.RequestCount * tracker.Target.Replicas) - tracker.Status.CompletedRequests
					if global.Flags.EnableInvocationLogs {
						log.Printf("[%s]: Invocation[%d]: Received stop request for target [%s] with remaining requests [%d]\n", global.Self.Name, tracker.ID, tracker.Target.Name, remaining)
					}
				}
			} else {
				time.Sleep(2 * time.Second)
			}
		} else {
			break
		}
	}
}

// ResetActiveInvocations resets the invocation counter and forgets all active invocations and targets.
func ResetActiveInvocations() {
	invocationsLock.Lock()
	invocationCounter = 0
	activeInvocations = map[uint32]*InvocationTracker{}
	activeTargets = map[string]*TargetInvocations{}
	invocationsLock.Unlock()
}

// RegisterInvocation creates a tracker with the next invocation ID for the given target spec and result sinks.
func RegisterInvocation(clientPort int, target *InvocationSpec, sinks ...ResultSinkFactory) (*InvocationTracker, error) {
	return newTracker(atomic.AddUint32(&invocationCounter, 1), clientPort, target, sinks...)
}

// newTracker builds an InvocationTracker with its channels, transport client, result sinks and the request payloads (stream, auto, body or reader).
func newTracker(id uint32, clientPort int, target *InvocationSpec, sinks ...ResultSinkFactory) (*InvocationTracker, error) {
	tracker := &InvocationTracker{
		ID:         id,
		ClientPort: clientPort,
		Target:     target,
		Channels: &InvocationChannels{
			StopChannel:   make(chan bool, 20),
			DoneChannel:   make(chan bool, 2),
			ResultChannel: make(chan *InvocationResult, 200),
		},
	}
	if err := tracker.createClient(target); err != nil {
		return nil, err
	}
	tracker.Status = &InvocationStatus{tracker: tracker, lastStatusCode: -1}
	for _, sinkFactory := range sinks {
		if sink := sinkFactory(tracker); sink != nil {
			tracker.Channels.Sinks = append(tracker.Channels.Sinks, sink)
		}
	}
	if len(target.StreamPayload) > 0 {
		for _, p := range target.StreamPayload {
			if target.Binary {
				if b, err := base64.RawStdEncoding.DecodeString(p); err == nil {
					tracker.Payloads = append(tracker.Payloads, b)
				} else {
					tracker.Payloads = append(tracker.Payloads, []byte(p))
				}
			} else {
				tracker.Payloads = append(tracker.Payloads, []byte(p))
			}
		}
		target.Body = ""
	} else if target.autoPayloadSize > 0 {
		tracker.Payloads = [][]byte{types.GenerateRandomPayload(target.autoPayloadSize)}
		target.Body = ""
	} else if target.Body != "" {
		tracker.Payloads = [][]byte{[]byte(target.Body)}
	} else if target.BodyReader != nil {
		tracker.Payloads = [][]byte{util.ReadBytes(target.BodyReader)}
		target.BodyReader = nil
	} else {
		tracker.Payloads = [][]byte{nil}
	}
	return tracker, nil
}

// createClient creates the tracker's client and its transport, failing if no transport can be created for the target.
func (tracker *InvocationTracker) createClient(target *InvocationSpec) error {
	tracker.client = &InvocationClient{tracker: tracker}
	tracker.client.transportClient = target.createTransport(tracker)
	if tracker.client.transportClient == nil {
		return fmt.Errorf("failed to create client for target [%s]", target.Name)
	}
	return nil
}

// getHttpClientForTarget returns the cached HTTP client of the target or creates one, caching it unless the target is long running.
func getHttpClientForTarget(tracker *InvocationTracker) transport.ClientTransport {
	target := tracker.Target
	invocationsLock.RLock()
	client := targetClients[target.Name]
	invocationsLock.RUnlock()
	if client == nil || client.HTTP() == nil {
		client = transport.CreateHTTPClient(0, target.Name, target.h2, target.AutoUpgrade, target.TLS, target.NoSNI,
			target.authority, target.requestTimeoutD, target.connTimeoutD,
			target.connIdleTimeoutD, metrics.ConnTracker)
		if !target.LongRunning {
			invocationsLock.Lock()
			targetClients[target.Name] = client
			invocationsLock.Unlock()
		}
	}
	return client
}

// getGrpcClientForTarget returns the cached gRPC client of the target or creates one from the registered service, or nil if that fails.
func getGrpcClientForTarget(tracker *InvocationTracker) transport.ClientTransport {
	target := tracker.Target
	invocationsLock.RLock()
	client := targetClients[target.Name]
	invocationsLock.RUnlock()
	if client == nil {
		service := gotogrpc.ServiceRegistry.GetService(target.Service)
		if service == nil {
			log.Printf("Service %s not found for target %s", target.Service, target.Name)
			return nil
		}
		if grpcClient, err := grpc.NewGRPCClient(fmt.Sprintf("Client(%s)", target.Name), tracker.ClientPort, service, target.URL, target.authority, target.authority,
			&grpc.GRPCOptions{
				IsTLS:          target.TLS,
				VerifyTLS:      target.VerifyTLS,
				ConnectTimeout: target.connTimeoutD,
				IdleTimeout:    target.connIdleTimeoutD,
				RequestTimeout: target.requestTimeoutD,
				KeepOpen:       target.keepOpenD,
			}); err == nil {
			client = grpcClient
			if !target.LongRunning {
				invocationsLock.Lock()
				targetClients[target.Name] = client
				invocationsLock.Unlock()
			}
		} else {
			log.Println(err.Error())
		}
	}
	return client
}

// activate registers the tracker in the active invocations and active targets.
func (tracker *InvocationTracker) activate() {
	invocationsLock.Lock()
	activeInvocations[tracker.ID] = tracker
	targetInvocations := activeTargets[tracker.Target.Name]
	if targetInvocations == nil {
		targetInvocations = &TargetInvocations{trackers: map[uint32]*InvocationTracker{}}
		activeTargets[tracker.Target.Name] = targetInvocations
	}
	invocationsLock.Unlock()
	targetInvocations.lock.Lock()
	targetInvocations.trackers[tracker.ID] = tracker
	targetInvocations.lock.Unlock()
}

// deactivate completes the invocation: signals done, closes the channels, unregisters the tracker, and closes the client unless the target is long running.
func (tracker *InvocationTracker) deactivate() {
	if tracker.Channels != nil {
		tracker.Channels.Done()
		tracker.Channels.ReadStopRequest()
	}
	tracker.reportRepeatedResponse()
	tracker.CloseChannels()
	tracker.Status.Closed = true
	invocationsLock.Lock()
	delete(activeInvocations, tracker.ID)
	targetInvocations := activeTargets[tracker.Target.Name]
	invocationsLock.Unlock()
	if targetInvocations != nil {
		targetInvocations.lock.Lock()
		delete(targetInvocations.trackers, tracker.ID)
		size := len(targetInvocations.trackers)
		targetInvocations.lock.Unlock()
		if size == 0 {
			invocationsLock.Lock()
			delete(activeTargets, tracker.Target.Name)
			invocationsLock.Unlock()
		}
	}
	if !tracker.Target.LongRunning {
		tracker.client.close()
		tracker.client = nil
	}
	tracker.logFinishedInvocation((tracker.Target.RequestCount * tracker.Target.Replicas) - tracker.Status.CompletedRequests)
}

// RemoveHttpClientForTarget closes and forgets the cached client of the target.
func RemoveHttpClientForTarget(target string) {
	invocationsLock.Lock()
	if client := targetClients[target]; client != nil {
		client.Close()
		delete(targetClients, target)
	}
	invocationsLock.Unlock()
}

// monitorHttpClients periodically reports active connection counts per target and evicts clients of inactive targets after repeated idle checks, until shutdown.
func monitorHttpClients() {
	watchListForRemoval := map[string]int{}
	for {
		select {
		case <-chanStopCleanup:
			return
		case <-time.Tick(clientConnReportDuration):
			invocationsLock.RLock()
			for target, client := range targetClients {
				if client != nil && client.Transport() != nil {
					metrics.UpdateActiveTargetConnCount(target, client.Transport().GetOpenConnectionCount())
				} else {
					metrics.UpdateActiveTargetConnCount(target, 1)
				}
			}
			invocationsLock.RUnlock()
		case <-time.Tick(maxIdleClientDuration):
			invocationsLock.Lock()
			for target, client := range targetClients {
				if activeTargets[target] == nil && (client.Transport() == nil || client.Transport().GetOpenConnectionCount() > 0) {
					if watchListForRemoval[target] < 3 {
						watchListForRemoval[target]++
					} else {
						client.Close()
						delete(targetClients, target)
						delete(watchListForRemoval, target)
					}
				} else if watchListForRemoval[target] > 0 {
					delete(watchListForRemoval, target)
				}
			}
			invocationsLock.Unlock()
		}
	}
}

// CloseChannels closes the tracker's channels, if it has any.
func (t *InvocationTracker) CloseChannels() {
	if t.Channels != nil {
		t.Channels.Close()
	}
}

// AddSink adds a result sink to the tracker's channels, if it has any.
func (t *InvocationTracker) AddSink(sink ResultSink) {
	if t.Channels != nil {
		t.Channels.AddSink(sink)
	}
}

// Stop sends a stop request on the tracker's channels, if it has any.
func (t *InvocationTracker) Stop() {
	if t.Channels != nil {
		t.Channels.Stop()
	}
}

// AddSink appends a result sink to the channels under their lock.
func (c *InvocationChannels) AddSink(sink ResultSink) {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	c.Sinks = append(c.Sinks, sink)
}

// Done signals completion on the done channel, if it is still open.
func (c *InvocationChannels) Done() {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	if c.DoneChannel != nil {
		c.DoneChannel <- true
	}
}

// ReadStopRequest drains a pending stop request from the stop channel without blocking.
func (c *InvocationChannels) ReadStopRequest() {
	c.Lock.RLock()
	defer c.Lock.RUnlock()
	if c.StopChannel != nil {
		select {
		case <-c.StopChannel:
		default:
		}
	}
}

// Stop sends a stop request on the stop channel, if it is still open.
func (c *InvocationChannels) Stop() {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	if c.StopChannel != nil {
		c.StopChannel <- true
	}
}

// Close closes the stop, done and result channels and clears them so they are only closed once.
func (c *InvocationChannels) Close() {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	if c.StopChannel != nil {
		close(c.StopChannel)
		c.StopChannel = nil
	}
	if c.DoneChannel != nil {
		close(c.DoneChannel)
		c.DoneChannel = nil
	}
	if c.ResultChannel != nil {
		close(c.ResultChannel)
		c.ResultChannel = nil
	}
}
