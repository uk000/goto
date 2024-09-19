/**
 * Copyright 2024 uk
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
  if global.PodName == "" {
    pod, present := os.LookupEnv("POD_NAME")
    if !present {
      pod, _ = os.Hostname()
    }
    global.PodName = pod
  }
  return global.PodName
}

func GetNodeName() string {
  if global.NodeName == "" {
    global.NodeName, _ = os.LookupEnv("NODE_NAME")
  }
  return global.NodeName
}

func GetCluster() string {
  if global.Cluster == "" {
    global.Cluster, _ = os.LookupEnv("CLUSTER")
  }
  return global.Cluster
}

func GetNamespace() string {
  if global.Namespace == "" {
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
    global.Namespace = ns
  }
  return global.Namespace
}

func GetHostIP() string {
  if global.HostIP == "" {
    if ip, present := os.LookupEnv("POD_IP"); present {
      global.HostIP = ip
    } else {
      conn, err := net.Dial("udp", "8.8.8.8:80")
      if err == nil {
        defer conn.Close()
        global.HostIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
      } else {
        global.HostIP = "localhost"
      }
    }
  }
  return global.HostIP
}

func BuildHostLabel(port int) string {
  hostLabel := ""
  node := GetNodeName()
  cluster := GetCluster()
  if node != "" || cluster != "" {
    hostLabel = fmt.Sprintf("%s.%s@%s:%d(%s@%s)", GetPodName(), GetNamespace(), GetHostIP(), port, node, cluster)
  } else {
    hostLabel = fmt.Sprintf("%s.%s@%s:%d", GetPodName(), GetNamespace(), GetHostIP(), port)
  }
  return hostLabel
}

func BuildListenerLabel(port int) string {
  return fmt.Sprintf("Goto-%s:%d", GetHostIP(), port)
}

func GetHostLabel() string {
  if global.HostLabel == "" {
    global.HostLabel = BuildHostLabel(global.ServerPort)
  }
  return global.HostLabel
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
