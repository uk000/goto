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

package types

import (
	"time"
)

type SelfInfo struct {
	ServerPort  int
	GRPCPort    int
	JSONRPCPort int
	GivenName   bool
	Name        string
	Address     string
	PodName     string
	Namespace   string
	NodeName    string
	Cluster     string
	PodIP       string
	HostIP      string
	HostLabel   string
	RegistryURL string
}

type Flags struct {
	UseLocker                    bool
	EnableEvents                 bool
	PublishEvents                bool
	EnableServerLogs             bool
	EnableAdminLogs              bool
	EnableClientLogs             bool
	EnableInvocationLogs         bool
	EnableInvocationResponseLogs bool
	EnableRegistryLogs           bool
	EnableRegistryLockerLogs     bool
	EnableRegistryEventsLogs     bool
	EnableRegistryReminderLogs   bool
	EnablePeerHealthLogs         bool
	EnableProbeLogs              bool
	EnableMetricsLogs            bool
	EnableProxyDebugLogs         bool
	EnableGRPCDebugLogs          bool
	LogRequestHeaders            bool
	LogRequestMiniBody           bool
	LogRequestBody               bool
	LogRPCRequestBody            bool
	LogResponseHeaders           bool
	LogResponseMiniBody          bool
	LogResponseBody              bool
}

type CmdConfig struct {
	CmdCtlMode    bool
	CmdClientMode bool
}

type CmdCtlConfig struct {
	Context     string
	ContextFile string
	ConfigFile  string
	Name        string
	RemoteURL   string
}

type CmdClientConfig struct {
	Protocol     string
	URLs         []string
	Method       string
	Headers      [][]string
	Payload      string
	AutoPayload  string
	RequestCount int
	Parallel     int
	Delay        string
	Retries      int
	RetryDelay   string
	RetryOn      []int
	Verbose      bool
	Persist      bool
}

type ServerConfig struct {
	CertPath      string
	WorkDir       string
	KubeConfig    string
	StartupDelay  time.Duration
	ShutdownDelay time.Duration
	StartupScript []string
	ConfigPaths   []string
	Stopping      bool
	MaxMTUSize    int
}

type Context struct {
	Name          string `yaml:"name"`
	RemoteGotoURL string `yaml:"remoteGotoURL"`
}
