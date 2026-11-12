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

package global

import (
	"fmt"
	"goto/pkg/types"
	"net"
	"os"
	"strconv"
)

// init sets the default ports and fills Self with the host, pod, namespace, cluster and node identity derived from the environment.
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
	Self.HostLabel = BuildHostLabel()
	Self.Address = Self.PodIP + ":" + strconv.Itoa(Self.ServerPort)
}

var (
	DevTag  = "0.9.20"
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
	ServerConfig    = types.ServerConfig{
		WorkDir: os.TempDir(),
	}

	Debug bool = false
)

// GetMaxMTUSize returns the largest MTU among the network interfaces.
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

// BuildHostLabel builds the label identifying this instance from its pod, namespace, IP and, when known, node, host IP and cluster.
func BuildHostLabel() string {
	hostLabel := ""
	if Self.NodeName != "" || Self.Cluster != "" || Self.HostIP != "" {
		hostLabel = fmt.Sprintf("%s.%s[%s](%s[%s]@%s)", Self.PodName, Self.Namespace, Self.PodIP, Self.NodeName, Self.HostIP, Self.Cluster)
	} else {
		hostLabel = fmt.Sprintf("%s.%s[%s]", Self.PodName, Self.Namespace, Self.PodIP)
	}
	return hostLabel
}

// SetNodeName sets the node name from NODE_NAME, defaulting to LocalNode.
func SetNodeName() {
	Self.NodeName, _ = os.LookupEnv("NODE_NAME")
	if Self.NodeName == "" {
		Self.NodeName = "LocalNode"
	}
}

// SetPodName sets the pod name from POD_NAME, falling back to the hostname and then "local".
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

// SetCluster sets the cluster name from CLUSTER, defaulting to LocalCluster.
func SetCluster() {
	Self.Cluster, _ = os.LookupEnv("CLUSTER")
	if Self.Cluster == "" {
		Self.Cluster = "LocalCluster"
	}
}

// SetNamespace sets the namespace from NAMESPACE or the service account namespace file, defaulting to "local".
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

// SetPodIP sets the pod IP from POD_IP, else from the local address of an outbound UDP route, else "localhost".
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

// SetHostIP sets the host IP from HOST_IP, defaulting to 0.0.0.0.
func SetHostIP() {
	if ip, present := os.LookupEnv("HOST_IP"); present {
		Self.HostIP = ip
	} else {
		Self.HostIP = "0.0.0.0"
	}
}
