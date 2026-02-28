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

import (
	"flag"
	"goto/pkg/global"
	"os"

	"gopkg.in/yaml.v3"
)

type GotoConfig struct {
	Name     string    `yaml:"name"`
	Scripts  *Scripts  `yaml:"scripts,omitempty"`
	GRPC     *GRPC     `yaml:"grpc,omitempty"`
	Registry *Registry `yaml:"registry,omitempty"`
	Traffic  *Traffic  `yaml:"traffic,omitempty"`
	Jobs     *Jobs     `yaml:"jobs,omitempty"`
	MCP      *MCP      `yaml:"mcp,omitempty"`
	A2A      *A2A      `yaml:"a2a,omitempty"`
}

var (
	ApplyFlagSet = flag.NewFlagSet("apply", flag.ExitOnError)
)

func Ctl(args []string) {
	loadOrCreateContextFile()
	switch args[0] {
	case "ctx":
		ctlCtx(args[1:])
	case "apply":
		ctlApply(args[1:])
	}
}

func ctlApply(args []string) {
	ApplyFlagSet.Parse(args)
	loadContext()
	config := LoadConfig(global.CtlConfig.ConfigFile)
	processScripts(config)
	processMCP(config)
	processA2A(config)
}

func LoadConfig(path string) *GotoConfig {
	config := &GotoConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, config); err != nil {
			panic(err)
		}
	} else {
		panic(err)
	}
	return config
}
