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
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"goto/pkg/types"
)

// saveSelf puts the identity of the server back when the test is done.
func saveSelf(t *testing.T) {
	t.Helper()
	self := Self
	t.Cleanup(func() { Self = self })
}

// unsetEnv removes a variable for the test, and gives it back when the test is done.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestPackageDefaults(t *testing.T) {
	if DevTag == "" {
		t.Error("the development version is empty")
	}
	if Self.ServerPort != 8080 || Self.GRPCPort != 1234 {
		t.Errorf("ports = %d, %d, want the defaults 8080 and 1234", Self.ServerPort, Self.GRPCPort)
	}
	if want := Self.PodIP + ":" + strconv.Itoa(Self.ServerPort); Self.Address != want {
		t.Errorf("address = %q, want %q", Self.Address, want)
	}
	if Self.HostLabel == "" || Self.PodName == "" || Self.Cluster == "" || Self.NodeName == "" || Self.HostIP == "" || Self.PodIP == "" {
		t.Errorf("identity has empty parts: %+v", Self)
	}
	if ServerConfig.WorkDir != os.TempDir() {
		t.Errorf("work dir = %q, want the temp dir %q", ServerConfig.WorkDir, os.TempDir())
	}
	if ServerConfig.MaxMTUSize != GetMaxMTUSize() {
		t.Errorf("max MTU = %d, want %d", ServerConfig.MaxMTUSize, GetMaxMTUSize())
	}
	if Debug {
		t.Error("debug is on by default")
	}
}

func TestDefaultFlags(t *testing.T) {
	on := map[string]bool{
		"EnableServerLogs": Flags.EnableServerLogs, "EnableAdminLogs": Flags.EnableAdminLogs,
		"EnableClientLogs": Flags.EnableClientLogs, "EnableInvocationLogs": Flags.EnableInvocationLogs,
		"EnableRegistryLogs": Flags.EnableRegistryLogs, "EnablePeerHealthLogs": Flags.EnablePeerHealthLogs,
		"EnableMetricsLogs": Flags.EnableMetricsLogs, "LogRequestHeaders": Flags.LogRequestHeaders,
		"EnableProxyDebugLogs": Flags.EnableProxyDebugLogs, "EnableGRPCDebugLogs": Flags.EnableGRPCDebugLogs,
	}
	for name, v := range on {
		if !v {
			t.Errorf("%s is off, want it on by default", name)
		}
	}
	off := map[string]bool{
		"UseLocker":       Flags.UseLocker,
		"EnableProbeLogs": Flags.EnableProbeLogs, "LogRequestBody": Flags.LogRequestBody,
		"LogResponseHeaders": Flags.LogResponseHeaders, "LogResponseBody": Flags.LogResponseBody,
		"EnableRegistryLockerLogs":   Flags.EnableRegistryLockerLogs,
		"EnableRegistryReminderLogs": Flags.EnableRegistryReminderLogs, "VerboseMCP": Flags.VerboseMCP,
	}
	for name, v := range off {
		if v {
			t.Errorf("%s is on, want it off by default", name)
		}
	}
}

func TestStructsStartEmpty(t *testing.T) {
	if CmdConfig != (types.CmdConfig{}) || CtlConfig != (types.CmdCtlConfig{}) {
		t.Errorf("command configs = %+v %+v, want empty", CmdConfig, CtlConfig)
	}
	if CmdClientConfig.URLs != nil || CmdClientConfig.Headers != nil || CmdClientConfig.Protocol != "" {
		t.Errorf("client config = %+v, want empty", CmdClientConfig)
	}
	if Funcs.GetPeers != nil || Funcs.IsReadinessProbe != nil {
		t.Error("the hooks of the server are given by the packages that use them, not by global")
	}
	if GRPCServer != nil || GRPCManager != nil {
		t.Error("gRPC server and manager are set without a server")
	}
}

func TestGetMaxMTUSize(t *testing.T) {
	ifs, _ := net.Interfaces()
	want := 0
	for _, i := range ifs {
		if i.MTU > want {
			want = i.MTU
		}
	}
	if got := GetMaxMTUSize(); got != want {
		t.Errorf("max MTU = %d, want the biggest of the interfaces, %d", got, want)
	}
}

func TestSetNodeName(t *testing.T) {
	saveSelf(t)
	t.Setenv("NODE_NAME", "node-1")
	SetNodeName()
	if Self.NodeName != "node-1" {
		t.Errorf("node = %q, want node-1", Self.NodeName)
	}
	t.Setenv("NODE_NAME", "")
	SetNodeName()
	if Self.NodeName != "LocalNode" {
		t.Errorf("node with an empty value = %q, want LocalNode", Self.NodeName)
	}
	unsetEnv(t, "NODE_NAME")
	SetNodeName()
	if Self.NodeName != "LocalNode" {
		t.Errorf("node without a value = %q, want LocalNode", Self.NodeName)
	}
}

func TestSetCluster(t *testing.T) {
	saveSelf(t)
	t.Setenv("CLUSTER", "prod-east")
	SetCluster()
	if Self.Cluster != "prod-east" {
		t.Errorf("cluster = %q, want prod-east", Self.Cluster)
	}
	unsetEnv(t, "CLUSTER")
	SetCluster()
	if Self.Cluster != "LocalCluster" {
		t.Errorf("cluster without a value = %q, want LocalCluster", Self.Cluster)
	}
}

func TestSetHostIP(t *testing.T) {
	saveSelf(t)
	t.Setenv("HOST_IP", "10.1.2.3")
	SetHostIP()
	if Self.HostIP != "10.1.2.3" {
		t.Errorf("host IP = %q, want 10.1.2.3", Self.HostIP)
	}
	unsetEnv(t, "HOST_IP")
	SetHostIP()
	if Self.HostIP != "0.0.0.0" {
		t.Errorf("host IP without a value = %q, want 0.0.0.0", Self.HostIP)
	}
}

func TestSetPodName(t *testing.T) {
	saveSelf(t)
	t.Setenv("POD_NAME", "pod-1")
	SetPodName()
	if Self.PodName != "pod-1" {
		t.Errorf("pod = %q, want pod-1", Self.PodName)
	}
	// A variable that is there but empty is taken as it is, and the name falls back to local.
	t.Setenv("POD_NAME", "")
	SetPodName()
	if Self.PodName != "local" {
		t.Errorf("pod with an empty value = %q, want local", Self.PodName)
	}
	unsetEnv(t, "POD_NAME")
	SetPodName()
	host, _ := os.Hostname()
	if host == "" {
		host = "local"
	}
	if Self.PodName != host {
		t.Errorf("pod without a value = %q, want the host name %q", Self.PodName, host)
	}
}

func TestSetNamespace(t *testing.T) {
	saveSelf(t)
	t.Setenv("NAMESPACE", "team-a")
	SetNamespace()
	if Self.Namespace != "team-a" {
		t.Errorf("namespace = %q, want team-a", Self.Namespace)
	}
	t.Setenv("NAMESPACE", "")
	SetNamespace()
	if Self.Namespace != "" {
		t.Errorf("namespace with an empty value = %q, want it as it is", Self.Namespace)
	}
	unsetEnv(t, "NAMESPACE")
	SetNamespace()
	want := "local"
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		want = string(data)
	}
	if Self.Namespace != want {
		t.Errorf("namespace without a value = %q, want %q", Self.Namespace, want)
	}
}

func TestSetPodIP(t *testing.T) {
	saveSelf(t)
	t.Setenv("POD_IP", "10.9.8.7")
	SetPodIP()
	if Self.PodIP != "10.9.8.7" {
		t.Errorf("pod IP = %q, want 10.9.8.7", Self.PodIP)
	}
	// Without the variable the address that the host would use to go out is taken (no packet is sent for it), and when
	// there is no route, localhost.
	unsetEnv(t, "POD_IP")
	SetPodIP()
	if Self.PodIP != "localhost" && net.ParseIP(Self.PodIP) == nil {
		t.Errorf("pod IP without a value = %q, want an IP or localhost", Self.PodIP)
	}
}

func TestBuildHostLabel(t *testing.T) {
	saveSelf(t)
	tests := []struct {
		name string
		self types.SelfInfo
		want string
	}{
		{"with node, host and cluster",
			types.SelfInfo{PodName: "pod", Namespace: "ns", PodIP: "1.2.3.4", NodeName: "node", HostIP: "5.6.7.8", Cluster: "cl"},
			"pod.ns[1.2.3.4](node[5.6.7.8]@cl)"},
		{"with the node only", types.SelfInfo{PodName: "pod", Namespace: "ns", PodIP: "1.2.3.4", NodeName: "node"},
			"pod.ns[1.2.3.4](node[]@)"},
		{"with the cluster only", types.SelfInfo{PodName: "pod", Namespace: "ns", PodIP: "1.2.3.4", Cluster: "cl"},
			"pod.ns[1.2.3.4]([]@cl)"},
		{"with the host IP only", types.SelfInfo{PodName: "pod", Namespace: "ns", PodIP: "1.2.3.4", HostIP: "5.6.7.8"},
			"pod.ns[1.2.3.4]([5.6.7.8]@)"},
		{"without node, host and cluster", types.SelfInfo{PodName: "pod", Namespace: "ns", PodIP: "1.2.3.4"}, "pod.ns[1.2.3.4]"},
		{"empty", types.SelfInfo{}, ".[]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Self = tt.self
			if got := BuildHostLabel(); got != tt.want {
				t.Errorf("label = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHostLabelOfDefaultsHasEveryPart(t *testing.T) {
	saveSelf(t)
	t.Setenv("POD_NAME", "p")
	t.Setenv("NAMESPACE", "n")
	t.Setenv("POD_IP", "1.1.1.1")
	t.Setenv("NODE_NAME", "nd")
	t.Setenv("HOST_IP", "2.2.2.2")
	t.Setenv("CLUSTER", "c")
	SetPodName()
	SetNamespace()
	SetPodIP()
	SetNodeName()
	SetHostIP()
	SetCluster()
	if got := BuildHostLabel(); got != "p.n[1.1.1.1](nd[2.2.2.2]@c)" {
		t.Errorf("label = %q", got)
	}
	if strings.ContainsAny(BuildHostLabel(), " \n") {
		t.Error("the label has white space")
	}
}
