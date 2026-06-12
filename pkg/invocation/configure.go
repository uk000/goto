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
	ALPN                 []string          `json:"alpn"`
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

func init() {
	go monitorHttpClients()
}

func Shutdown() {
	chanStopCleanup <- true
}

func ValidateSpec(spec *InvocationSpec) error {
	var err error
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
	} else {
		is.streamDelayD = 10 * time.Millisecond
		is.StreamDelay = "10ms"
	}
	return nil
}

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
		is.requestTimeoutD = 30 * time.Second
		is.RequestTimeout = "30s"
	}
	if is.TLSVersion == 0 {
		is.TLSVersion = tls.VersionTLS13
	}
	if is.ClientCert != "" {
		if c, _ := gototls.GetCert(is.ClientCert); c == nil {
			return fmt.Errorf("ClientCert Not Uploaded")
		}
	}
	return nil
}

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
	} else {
		is.delayD = 10 * time.Millisecond
		is.Delay = "10ms"
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

func (is *InvocationSpec) Clone() *InvocationSpec {
	is2 := *is
	is2.parent = is
	return &is2
}

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

func ResetActiveInvocations() {
	invocationsLock.Lock()
	invocationCounter = 0
	activeInvocations = map[uint32]*InvocationTracker{}
	activeTargets = map[string]*TargetInvocations{}
	invocationsLock.Unlock()
}

func RegisterInvocation(clientPort int, target *InvocationSpec, sinks ...ResultSinkFactory) (*InvocationTracker, error) {
	return newTracker(atomic.AddUint32(&invocationCounter, 1), clientPort, target, sinks...)
}

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

func (tracker *InvocationTracker) createClient(target *InvocationSpec) error {
	tracker.client = &InvocationClient{tracker: tracker}
	tracker.client.transportClient = target.createTransport(tracker)
	if tracker.client.transportClient == nil {
		return fmt.Errorf("failed to create client for target [%s]", target.Name)
	}
	return nil
}

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

func RemoveHttpClientForTarget(target string) {
	invocationsLock.Lock()
	if client := targetClients[target]; client != nil {
		client.Close()
		delete(targetClients, target)
	}
	invocationsLock.Unlock()
}

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

func (t *InvocationTracker) CloseChannels() {
	if t.Channels != nil {
		t.Channels.Close()
	}
}

func (t *InvocationTracker) AddSink(sink ResultSink) {
	if t.Channels != nil {
		t.Channels.AddSink(sink)
	}
}

func (t *InvocationTracker) Stop() {
	if t.Channels != nil {
		t.Channels.Stop()
	}
}

func (t *InvocationTracker) IsStopped() {
	if t.Channels != nil {
		t.Channels.Stop()
	}
}

func (c *InvocationChannels) AddSink(sink ResultSink) {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	c.Sinks = append(c.Sinks, sink)
}

func (c *InvocationChannels) Done() {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	if c.DoneChannel != nil {
		c.DoneChannel <- true
	}
}

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

func (c *InvocationChannels) Stop() {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	if c.StopChannel != nil {
		c.StopChannel <- true
	}
}

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
