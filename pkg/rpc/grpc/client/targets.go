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

package grpcclient

import (
	"crypto/tls"
	"fmt"
	gotogrpc "goto/pkg/rpc/grpc"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type GRPCTargetsManager struct {
	targets map[string]*GRPCClient
	options GRPCOptions
}

var (
	Manager = newGRPCTargetsManager()
)

func newGRPCTargetsManager() *GRPCTargetsManager {
	g := &GRPCTargetsManager{}
	g.targets = map[string]*GRPCClient{}
	g.options = GRPCOptions{
		IsTLS:          false,
		VerifyTLS:      false,
		TLSVersion:     tls.VersionTLS13,
		ConnectTimeout: 30 * time.Second,
		IdleTimeout:    5 * time.Minute,
		RequestTimeout: 1 * time.Minute,
		KeepOpen:       0,
	}
	g.configureDialOptions()
	return g
}

func (g *GRPCTargetsManager) AddTarget(name, url, authority, serverName string, options *GRPCOptions) error {
	service := gotogrpc.ServiceRegistry.GetService(name)
	if service == nil {
		return fmt.Errorf("Service not found for target %s", name)
	}
	if c, err := NewGRPCClient(service, url, authority, serverName, options); err == nil {
		g.targets[name] = c
		return nil
	} else {
		return err
	}
}

func (g *GRPCTargetsManager) RemoveTarget(name string) {
	if t := g.targets[name]; t != nil {
		t.Close()
		delete(g.targets, name)
	}
}

func (g *GRPCTargetsManager) ClearTargets() {
	for _, t := range g.targets {
		t.Close()
	}
	g.targets = map[string]*GRPCClient{}
}

func (g *GRPCTargetsManager) configureDialOptions() {
	g.options.DialOptions = []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    g.options.IdleTimeout / 2,
			Timeout: g.options.IdleTimeout / 2,
		}),
		grpc.WithUserAgent("goto/"),
	}
}

func (g *GRPCTargetsManager) SetTargetTLS(name string, isTLS, verifyTLS bool) {
	t := g.targets[name]
	if t == nil {
		log.Printf("GRPCClient.SetTargetTLS: Target [%s] not found", name)
		return
	}
	t.Options.IsTLS = isTLS
	t.Options.VerifyTLS = verifyTLS
	t.configureTLS()
}

func (g *GRPCTargetsManager) ConnectTarget(name string) {
	if t := g.targets[name]; t != nil {
		t.Connect()
	} else {
		log.Printf("GRPCClient.ConnectTarget: Target [%s] not found", name)
	}
}

func (g *GRPCTargetsManager) ConnectTargetWithHeaders(name string, headers map[string]string) {
	if client := g.targets[name]; client != nil {
		client.ConnectWithHeadersOrMD(headers, nil)
	} else {
		log.Printf("GRPCClient.ConnectTargetWithHeaders: Target [%s] not found", name)
	}
}
