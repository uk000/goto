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

package grpcclient

import (
	"crypto/tls"
	"fmt"
	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type GRPCTargetsManager struct {
	targets map[string]*GRPCClient
	options GRPCOptions
	lock    sync.Mutex
}

var (
	Manager = newGRPCTargetsManager()
)

// newGRPCTargetsManager creates a targets manager with the default client options and dial options.
func newGRPCTargetsManager() *GRPCTargetsManager {
	g := &GRPCTargetsManager{}
	g.targets = map[string]*GRPCClient{}
	g.options = GRPCOptions{
		IsTLS:          false,
		VerifyTLS:      false,
		TLSVersion:     tls.VersionTLS13,
		ConnectTimeout: 2 * time.Second,
		IdleTimeout:    5 * time.Minute,
		RequestTimeout: 1 * time.Minute,
		KeepOpen:       0,
	}
	g.configureDialOptions()
	return g
}

// AddTarget creates a client for a registered service and stores it as a named target.
func (g *GRPCTargetsManager) AddTarget(name, url, authority, serverName string, options *GRPCOptions) error {
	service := gotogrpc.ServiceRegistry.GetService(name)
	if service == nil {
		return fmt.Errorf("Service not found for target %s", name)
	}
	if c, err := NewGRPCClient(fmt.Sprintf("Client(%s)", name), global.Self.ServerPort, service, url, authority, serverName, options); err == nil {
		g.lock.Lock()
		defer g.lock.Unlock()
		g.targets[name] = c
		return nil
	} else {
		return err
	}
}

// RemoveTarget closes and removes the named target.
func (g *GRPCTargetsManager) RemoveTarget(name string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if t := g.targets[name]; t != nil {
		t.Close()
		delete(g.targets, name)
	}
}

// ClearTargets closes and removes all targets.
func (g *GRPCTargetsManager) ClearTargets() {
	g.lock.Lock()
	defer g.lock.Unlock()
	for _, t := range g.targets {
		t.Close()
	}
	g.targets = map[string]*GRPCClient{}
}

// configureDialOptions sets the default keepalive and user agent dial options.
func (g *GRPCTargetsManager) configureDialOptions() {
	g.options.DialOptions = []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    g.options.IdleTimeout / 2,
			Timeout: g.options.IdleTimeout / 2,
		}),
		grpc.WithUserAgent("goto/"),
	}
}

// SetTargetTLS updates the TLS settings of the named target and reconfigures its TLS.
func (g *GRPCTargetsManager) SetTargetTLS(name string, isTLS, verifyTLS bool) {
	g.lock.Lock()
	defer g.lock.Unlock()
	t := g.targets[name]
	if t == nil {
		log.Printf("GRPCClient.SetTargetTLS: Target [%s] not found", name)
		return
	}
	t.Options.IsTLS = isTLS
	t.Options.VerifyTLS = verifyTLS
	t.configureTLS()
}

// ConnectTarget connects the named target.
func (g *GRPCTargetsManager) ConnectTarget(name string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if t := g.targets[name]; t != nil {
		t.Connect()
	} else {
		log.Printf("GRPCClient.ConnectTarget: Target [%s] not found", name)
	}
}

// ConnectTargetWithHeaders connects the named target using the given headers as call metadata.
func (g *GRPCTargetsManager) ConnectTargetWithHeaders(name string, headers map[string]string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if client := g.targets[name]; client != nil {
		client.ConnectWithHeadersOrMD(headers, nil)
	} else {
		log.Printf("GRPCClient.ConnectTargetWithHeaders: Target [%s] not found", name)
	}
}
