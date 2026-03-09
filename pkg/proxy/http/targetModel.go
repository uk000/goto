package httpproxy

import (
	"goto/pkg/invocation"
	"goto/pkg/types"
	"goto/pkg/util"
	"regexp"
	"sync"

	"github.com/gorilla/mux"
)

type ITarget interface {
	GetName() string
	Enable()
	Disable()
	IsRunning() bool
	Stop()
	Close()
}

type TargetEndpointResponse struct {
	target   string
	endpoint string
	response *invocation.InvocationResultResponse
}

type TargetMatch struct {
	URIPrefix string                 `json:"uriPrefix"`
	Headers   map[string]string      `json:"headers"`
	Vars      map[string]*util.Match `json:"vars"`
	uriRegexp *regexp.Regexp
	router    *mux.Router
}

type Keys struct {
	Add    map[string]string `json:"add"`
	Remove []string          `json:"remove"`
}

type TrafficConfig struct {
	Delay      *types.Delay `json:"delay"`
	Retries    int          `json:"retries"`
	RetryDelay *types.Delay `json:"retryDelay"`
	RetryOn    []int        `json:"retryOn"`
}

type TrafficTransform struct {
	URIMap         map[string]string     `json:"uriMap"`
	Headers        *Keys                 `json:"headers"`
	Queries        *Keys                 `json:"queries"`
	StripURI       string                `json:"stripURI"`
	RequestId      *invocation.RequestId `json:"requestId"`
	stripURIRegexp *regexp.Regexp
}

type TargetEndpoint struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	Protocol     string `json:"protocol"`
	Authority    string `json:"authority"`
	IsTLS        bool   `json:"tls"`
	RequestCount int    `json:"requestCount"`
	Concurrent   int    `json:"concurrent"`
	name         string
	callCount    int
	target       *Target
	lock         sync.RWMutex
}

type EndpointInvocation struct {
	ep     *TargetEndpoint
	is     *invocation.InvocationSpec
	target *Target
}

type TargetTrigger struct {
	MatchAny      []*TargetMatch    `json:"matchAny"`
	Endpoints     []string          `json:"endpoints"`
	Transform     *TrafficTransform `json:"transform"`
	TrafficConfig *TrafficConfig    `json:"trafficConfig"`
	name          string
	epSpecs       map[string]*EndpointInvocation
	callCount     int
	lock          sync.RWMutex
}

type Target struct {
	Port      int                        `json:"port"`
	Enabled   bool                       `json:"enabled"`
	Name      string                     `json:"name"`
	Endpoints map[string]*TargetEndpoint `json:"endpoints"`
	Triggers  map[string]*TargetTrigger  `json:"triggers"`
	Transform *TrafficTransform          `json:"transform"`
	callCount int
	lock      sync.RWMutex
}

type MatchedTarget struct {
	target         *Target
	trigger        *TargetTrigger
	endpoints      map[string]*EndpointInvocation
	transform      *TrafficTransform
	matchedURI     string
	matchedHeaders map[string]string
	captureKeys    map[string]string
}

type TargetTracker struct {
	callCount          int
	writeSinceLastDrop int
	isRunning          bool
	stopChan           chan bool
}
