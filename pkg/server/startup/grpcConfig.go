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

package startup

import (
	"goto/ctl"
	"goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/protos"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/server/response/payload"
	"log"
)

func clearGRPC(g *ctl.GRPC) {
	if g != nil {
		for _, p := range g.Protos {
			log.Printf("Removing proto definitions: %s", p.Name)
			protos.ProtosRegistry.RemoveProto(p.Name)
		}
		for _, s := range g.Services {
			log.Printf("Removing gRPC service definition: %s", s.Service)
			grpc.ServiceRegistry.RemoveService(s.Service)
			grpc.ServiceRegistry.RemoveActiveService(s.Service)
		}
	}
}

func loadGRPC(g *ctl.GRPC) {
	if g == nil {
		return
	}
	for _, p := range g.Protos {
		log.Printf("Loading proto definitions: %s", p.Name)
		protos.ProtosRegistry.AddProto(p.Name, p.Path, []byte(p.Content), false)
	}
	services := map[int][]*grpc.GRPCService{}
	serviceNames := []string{}
	for _, s := range g.Services {
		gs := grpc.ServiceRegistry.GetService(s.Service)
		if gs == nil {
			log.Printf("No gRPC Service configured by the name [%s]\n", s.Service)
			continue
		}
		processGRRPCService(s.Port, gs, s.Methods)
		if s.Serve {
			services[s.Port] = append(services[s.Port], gs)
		}
		serviceNames = append(serviceNames, s.Service)
	}
	grpcserver.GRPCManager.ServeMulti(services)
	log.Println("============================================================")
	log.Printf("gRPC Services loaded successfully: %+v", serviceNames)
	log.Println("============================================================")
}

func processGRRPCService(port int, gs *grpc.GRPCService, methods []*ctl.GRPCMethodConfig) {
	for _, m := range methods {
		sm := gs.Methods[m.Method]
		if sm == nil {
			log.Printf("No gRPC Service Method configured by the name [%s]\n", m.Method)
			continue
		}
		if m.Response != nil {
			for _, rp := range m.Response.Payloads {
				if rp.RequestMatches == nil {
					rp.RequestMatches = append(rp.RequestMatches, payload.NewRequestMatch(sm.URI))
				} else {
					for _, m := range rp.RequestMatches {
						m.URIPrefix = sm.URI
					}
				}
				if err := payload.PayloadManager.SetURIResponsePayloadWithMatches(port, rp, true); err != nil {
					log.Printf("Error processing HTTP response: %s\n", err.Error())
				}
			}
		}
	}
}
