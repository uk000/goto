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

import (
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
)

type GRPC struct {
	Protos   []*ProtoConfig       `yaml:"protos,omitempty"`
	Services []*GRPCServiceConfig `yaml:"services,omitempty"`
}

type ProtoConfig struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	Content  string `yaml:"content"`
	Register bool   `yaml:"register"`
}

type GRPCServiceConfig struct {
	Service string              `yaml:"service"`
	Port    int                 `yaml:"port"`
	Serve   bool                `yaml:"serve"`
	Methods []*GRPCMethodConfig `yaml:"methods"`
}

type GRPCMethodConfig struct {
	Method   string              `yaml:"method"`
	Response *GRPCResponseConfig `yaml:"response"`
}

type GRPCResponseConfig struct {
	Payload    *payload.ResponsePayload `yaml:"payload"`
	Transforms []*util.Transform        `yaml:"transforms"`
}
