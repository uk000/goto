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
	"goto/pkg/types"
	"net"
)

func init() {
	Self.ServerPort = 8080
	Self.GRPCPort = 1234
	ServerConfig.MaxMTUSize = GetMaxMTUSize()
}

var (
	DevTag  = "0.9.4-beta8.3"
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
