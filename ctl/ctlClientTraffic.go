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

package ctl

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
