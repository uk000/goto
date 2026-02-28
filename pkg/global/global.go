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

package global

import (
	"fmt"
	"goto/pkg/types"
	"net"
	"os"
	"strconv"
)

func init() {
	Self.ServerPort = 8080
	Self.GRPCPort = 1234
	ServerConfig.MaxMTUSize = GetMaxMTUSize()
	SetHostIP()
	SetPodIP()
	SetNamespace()
	SetCluster()
	SetPodName()
	SetNodeName()
	Self.HostLabel = BuildHostLabel(Self.ServerPort)
	Self.Address = Self.PodIP + ":" + strconv.Itoa(Self.ServerPort)
}

var (
	DevTag  = "0.9.5"
	Version string
	Commit  string
	Funcs   = types.Funcs{}
	Self    = types.SelfInfo{}
	Flags   = types.Flags{
		EnableServerLogs:     true,
		EnableAdminLogs:      true,
		EnableClientLogs:     true,
		EnableInvocationLogs: true,
		EnableRegistryLogs:   true,
		EnablePeerHealthLogs: true,
		EnableMetricsLogs:    true,
		LogRequestHeaders:    true,
		EnableProxyDebugLogs: true,
		EnableGRPCDebugLogs:  true,
	}
	CmdConfig       = types.CmdConfig{}
	CtlConfig       = types.CmdCtlConfig{}
	CmdClientConfig = types.CmdClientConfig{}
	ServerConfig    = types.ServerConfig{}

	Debug bool = false
)

func GetMaxMTUSize() int {
	var m int = 0
	if ifs, _ := net.Interfaces(); ifs != nil {
		for _, i := range ifs {
			if i.MTU > m {
				m = i.MTU
			}
		}
	}
	return m
}

func BuildHostLabel(port int) string {
	hostLabel := ""
	if Self.NodeName != "" || Self.Cluster != "" || Self.HostIP != "" {
		hostLabel = fmt.Sprintf("%s.%s[%s:%d](%s[%s]@%s)", Self.PodName, Self.Namespace, Self.PodIP, port, Self.NodeName, Self.HostIP, Self.Cluster)
	} else {
		hostLabel = fmt.Sprintf("%s.%s[%s:%d]", Self.PodName, Self.Namespace, Self.PodIP, port)
	}
	return hostLabel
}

func SetNodeName() {
	Self.NodeName, _ = os.LookupEnv("NODE_NAME")
	if Self.NodeName == "" {
		Self.NodeName = "LocalNode"
	}
}

func SetPodName() {
	pod, present := os.LookupEnv("POD_NAME")
	if !present {
		pod, _ = os.Hostname()
	}
	if pod == "" {
		pod = "local"
	}
	Self.PodName = pod
}

func SetCluster() {
	Self.Cluster, _ = os.LookupEnv("CLUSTER")
	if Self.Cluster == "" {
		Self.Cluster = "LocalCluster"
	}
}

func SetNamespace() {
	ns, present := os.LookupEnv("NAMESPACE")
	if !present {
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			ns = string(data)
			present = true
		}
	}
	if !present {
		ns = "local"
	}
	Self.Namespace = ns
}

func SetPodIP() {
	if ip, present := os.LookupEnv("POD_IP"); present {
		Self.PodIP = ip
	} else {
		conn, err := net.Dial("udp", "8.8.8.8:80")
		if err == nil {
			defer conn.Close()
			Self.PodIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
		} else {
			Self.PodIP = "localhost"
		}
	}
}

func SetHostIP() {
	if ip, present := os.LookupEnv("HOST_IP"); present {
		Self.HostIP = ip
	} else {
		Self.HostIP = "0.0.0.0"
	}
}
