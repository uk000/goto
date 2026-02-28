/**
 * Copyright 2025 uk
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

package ctl

type GRPC struct {
	Config []*GRPCConfig `yaml:"config,omitempty"`
	Serve  []string      `yaml:"serve,omitempty"`
}

type GRPCConfig struct {
	Protos   []ProtoConfig       `yaml:"protos,omitempty"`
	Services []GRPCServiceConfig `yaml:"services,omitempty"`
	Serve    []string            `yaml:"serve,omitempty"`
}

type ProtoConfig struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type GRPCServiceConfig struct {
	Service string             `yaml:"service"`
	Port    int                `yaml:"port"`
	Methods []GRPCMethodConfig `yaml:"methods"`
}

type GRPCMethodConfig struct {
	Method    string               `yaml:"method"`
	Responses []GRPCResponseConfig `yaml:"responses"`
}

type GRPCResponseConfig struct {
	Match       *RequestMatch  `yaml:"match,omitempty"`
	Stream      *StreamConfig  `yaml:"stream,omitempty"`
	Payload     map[string]any `yaml:"payload,omitempty"`
	ContentType string         `yaml:"contentType,omitempty"`
}

type RequestMatch struct {
	Headers []HeaderMatch `yaml:"headers"`
}

type HeaderMatch struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value,omitempty"`
}

type StreamConfig struct {
	Count int    `yaml:"count"`
	Delay string `yaml:"delay,omitempty"`
}

type Registry struct {
	Locker string `yaml:"locker"`
}

type Traffic struct {
	Config *TrafficConfig `yaml:"config,omitempty"`
	Invoke []string       `yaml:"invoke,omitempty"`
}

type TrafficConfig struct {
	Tracking TrafficTrackConfig `yaml:"tracking,omitempty"`
	Targets  []TrafficTarget    `yaml:"targets"`
}

type TrafficTrackConfig struct {
	Headers []string  `yaml:"headers"`
	Time    TrackTime `yaml:"time"`
}

type TrackTime struct {
	Buckets []string `yaml:"buckets"`
}

type TrafficTarget struct {
	Name          string      `yaml:"name"`
	Method        string      `yaml:"method"`
	Protocol      string      `yaml:"protocol"`
	URL           string      `yaml:"url"`
	Replicas      int         `yaml:"replicas"`
	RequestCount  int         `yaml:"requestCount"`
	Expectation   Expectation `yaml:"expectation"`
	AutoInvoke    bool        `yaml:"autoInvoke"`
	StreamPayload []string    `yaml:"streamPayload,omitempty"`
	StreamDelay   string      `yaml:"streamDelay,omitempty"`
}

type Expectation struct {
	StatusCode    int               `yaml:"statusCode"`
	Payload       string            `yaml:"payload,omitempty"`
	PayloadLength int               `yaml:"payloadLength,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"`
}

type Jobs struct {
	Config []*JobConfig `yaml:"config,omitempty"`
	Run    []string     `yaml:"run,omitempty"`
}

type JobConfig struct {
	ID            string  `yaml:"id"`
	Task          JobTask `yaml:"task"`
	Auto          bool    `yaml:"auto"`
	Count         int     `yaml:"count"`
	KeepFirst     bool    `yaml:"keepFirst"`
	MaxResults    int     `yaml:"maxResults"`
	Delay         string  `yaml:"delay,omitempty"`
	InitialDelay  string  `yaml:"initialDelay,omitempty"`
	OutputTrigger string  `yaml:"outputTrigger,omitempty"`
	FinishTrigger string  `yaml:"finishTrigger,omitempty"`
}

type JobTask struct {
	Cmd             string            `yaml:"cmd"`
	Args            []string          `yaml:"args"`
	OutputMarkers   map[string]string `yaml:"outputMarkers"`
	OutputSeparator string            `yaml:"outputSeparator,omitempty"`
}
