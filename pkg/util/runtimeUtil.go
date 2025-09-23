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

package util

import (
	"fmt"
	"goto/pkg/global"
	"io/ioutil"
	"net"
	"os"
	"runtime"
	"strings"
)

func GetPodName() string {
	if global.Self.PodName == "" {
		pod, present := os.LookupEnv("POD_NAME")
		if !present {
			pod, _ = os.Hostname()
		}
		global.Self.PodName = pod
	}
	return global.Self.PodName
}

func GetNodeName() string {
	if global.Self.NodeName == "" {
		global.Self.NodeName, _ = os.LookupEnv("NODE_NAME")
	}
	return global.Self.NodeName
}

func GetCluster() string {
	if global.Self.Cluster == "" {
		global.Self.Cluster, _ = os.LookupEnv("CLUSTER")
	}
	if global.Self.Cluster == "" {
		return "local"
	}
	return global.Self.Cluster
}

func GetNamespace() string {
	if global.Self.Namespace == "" {
		ns, present := os.LookupEnv("NAMESPACE")
		if !present {
			if data, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
				ns = string(data)
				present = true
			}
		}
		if !present {
			ns = "local"
		}
		global.Self.Namespace = ns
	}
	return global.Self.Namespace
}

func GetPodIP() string {
	if ip, present := os.LookupEnv("POD_IP"); present {
		global.Self.PodIP = ip
	} else {
		conn, err := net.Dial("udp", "8.8.8.8:80")
		if err == nil {
			defer conn.Close()
			global.Self.PodIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
		} else {
			global.Self.PodIP = "localhost"
		}
	}
	return global.Self.PodIP
}

func GetHostIP() string {
	if global.Self.HostIP == "" {
		if ip, present := os.LookupEnv("HOST_IP"); present {
			global.Self.HostIP = ip
		} else {
			global.Self.HostIP = "0.0.0.0"
		}
	}
	return global.Self.HostIP
}

func BuildHostLabel(port int) string {
	hostLabel := ""
	node := GetNodeName()
	cluster := GetCluster()
	host := GetHostIP()
	if node != "" || cluster != "" || host != "" {
		hostLabel = fmt.Sprintf("%s.%s[%s:%d](%s[%s]@%s)", GetPodName(), GetNamespace(), GetPodIP(), port, node, host, cluster)
	} else {
		hostLabel = fmt.Sprintf("%s.%s[%s:%d]", GetPodName(), GetNamespace(), GetPodIP(), port)
	}
	return hostLabel
}

func BuildListenerLabel(port int) string {
	return fmt.Sprintf("[%s:%d].[%s@%s]", GetPodIP(), port, GetNamespace(), GetCluster())
}

func GetHostLabel() string {
	if global.Self.HostLabel == "" {
		global.Self.HostLabel = BuildHostLabel(global.Self.ServerPort)
	}
	return global.Self.HostLabel
}

func PrintCallers(level int, callee string) {
	pc := make([]uintptr, 16)
	n := runtime.Callers(1, pc)
	frames := runtime.CallersFrames(pc[:n])
	var callers []string
	i := 0
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "util") &&
			strings.Contains(frame.Function, "goto") {
			callers = append(callers, frame.Function)
			i++
		}
		if !more || i >= level {
			break
		}
	}
	fmt.Println("-----------------------------------------------")
	fmt.Printf("Callers of [%s]: %+v\n", callee, callers)
	fmt.Println("-----------------------------------------------")
}
