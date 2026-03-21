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
	URIPrefix string                 `yaml:"uriPrefix" json:"uriPrefix"`
	Headers   map[string]string      `yaml:"headers" json:"headers"`
	Vars      map[string]*util.Match `yaml:"vars" json:"vars"`
	uriRegexp *regexp.Regexp
	router    *mux.Router
}

type Keys struct {
	Add    map[string]string `yaml:"add" json:"add"`
	Remove []string          `yaml:"remove" json:"remove"`
}

type TrafficConfig struct {
	Delay      *types.Delay `yaml:"delay" json:"delay"`
	Retries    int          `yaml:"retries" json:"retries"`
	RetryDelay *types.Delay `yaml:"retryDelay" json:"retryDelay"`
	RetryOn    []int        `yaml:"retryOn" json:"retryOn"`
	Payload    bool         `yaml:"payload" json:"payload"`
	Clean      bool         `yaml:"clean" json:"clean"`
}

type TrafficTransform struct {
	URIMap         map[string]string     `yaml:"uriMap" json:"uriMap"`
	Headers        *Keys                 `yaml:"headers" json:"headers"`
	Queries        *Keys                 `yaml:"queries" json:"queries"`
	StripURI       string                `yaml:"stripURI" json:"stripURI"`
	RequestId      *invocation.RequestId `yaml:"requestId" json:"requestId"`
	stripURIRegexp *regexp.Regexp
}

type TargetEndpoint struct {
	URL          string `yaml:"url" json:"url"`
	Method       string `yaml:"method" json:"method"`
	Protocol     string `yaml:"protocol" json:"protocol"`
	Authority    string `yaml:"authority" json:"authority"`
	IsTLS        bool   `yaml:"tls" json:"tls"`
	RequestCount int    `yaml:"requestCount" json:"requestCount"`
	Concurrent   int    `yaml:"concurrent" json:"concurrent"`
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
	MatchAny      []*TargetMatch    `yaml:"matchAny" json:"matchAny"`
	Endpoints     []string          `yaml:"endpoints" json:"endpoints"`
	Transform     *TrafficTransform `yaml:"transform" json:"transform"`
	TrafficConfig *TrafficConfig    `yaml:"trafficConfig" json:"trafficConfig"`
	name          string
	epSpecs       map[string]*EndpointInvocation
	callCount     int
	lock          sync.RWMutex
}

type Target struct {
	Port          int                        `yaml:"-" json:"port"`
	Name          string                     `yaml:"name" json:"name"`
	Enabled       bool                       `yaml:"enabled" json:"enabled"`
	Endpoints     map[string]*TargetEndpoint `yaml:"endpoints" json:"endpoints"`
	Triggers      map[string]*TargetTrigger  `yaml:"triggers" json:"triggers"`
	Transform     *TrafficTransform          `yaml:"transform" json:"transform"`
	TrafficConfig *TrafficConfig             `yaml:"trafficConfig" json:"trafficConfig"`
	callCount     int
	lock          sync.RWMutex
}

type MatchedTarget struct {
	target         *Target
	trigger        *TargetTrigger
	endpoints      map[string]*EndpointInvocation
	transform      *TrafficTransform
	trafficConfig  *TrafficConfig
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
