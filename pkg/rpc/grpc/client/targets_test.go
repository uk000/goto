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
	"testing"
	"time"

	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"

	"google.golang.org/grpc/connectivity"
)

// resetManager gives a test a manager without targets, and leaves one behind.
func resetManager(t *testing.T) {
	t.Helper()
	Manager.ClearTargets()
	t.Cleanup(Manager.ClearTargets)
}

// registerSampleService makes the sample service known to the registry, which is what the targets are created from.
func registerSampleService(t *testing.T, e *testEnv) *gotogrpc.GRPCService {
	t.Helper()
	newClient(t, e, nil)
	s := gotogrpc.ServiceRegistry.GetService("SampleService")
	if s == nil {
		t.Fatalf("SampleService is not in the registry after reflection")
	}
	return s
}

func TestManagerDefaults(t *testing.T) {
	m := newGRPCTargetsManager()
	if len(m.targets) != 0 {
		t.Errorf("new manager has targets")
	}
	if m.options.IsTLS || m.options.VerifyTLS || m.options.TLSVersion != tls.VersionTLS13 {
		t.Errorf("TLS defaults: %+v", m.options)
	}
	if m.options.ConnectTimeout != 2*time.Second || m.options.IdleTimeout != 5*time.Minute || m.options.RequestTimeout != time.Minute || m.options.KeepOpen != 0 {
		t.Errorf("timeout defaults: %+v", m.options)
	}
	if len(m.options.DialOptions) != 2 {
		t.Errorf("dial options = %d, want keepalive and user agent", len(m.options.DialOptions))
	}
}

func TestManagerAddAndRemoveTarget(t *testing.T) {
	e := newEnv(t)
	resetManager(t)
	service := registerSampleService(t, e)

	if err := Manager.AddTarget("NoSuchService", e.addr, "", "", &GRPCOptions{}); err == nil {
		t.Errorf("a target for a service that is not in the registry must be refused")
	}
	if len(Manager.targets) != 0 {
		t.Fatalf("a refused target must not be kept")
	}

	if err := Manager.AddTarget("SampleService", e.addr, "auth.example", "", &GRPCOptions{}); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	c := Manager.targets["SampleService"]
	if c == nil {
		t.Fatalf("target not added")
	}
	if c.Label != "Client(SampleService)" || c.Service != service || c.URL != e.addr || c.Authority != "auth.example" || c.ClientPort != global.Self.ServerPort {
		t.Errorf("target = label %q service %v url %q authority %q port %d", c.Label, c.Service, c.URL, c.Authority, c.ClientPort)
	}

	Manager.ConnectTarget("SampleService")
	if c.GRPC() == nil {
		t.Fatalf("ConnectTarget must connect the client")
	}
	conn := c.GRPC()
	Manager.ConnectTargetWithHeaders("SampleService", map[string]string{"x-h": "v"})
	if c.GRPC() != conn {
		t.Errorf("connecting a connected target keeps the connection")
	}
	// targets that are not there are reported, not a failure
	Manager.ConnectTarget("missing")
	Manager.ConnectTargetWithHeaders("missing", nil)
	Manager.SetTargetTLS("missing", true, true)
	Manager.RemoveTarget("missing")

	Manager.RemoveTarget("SampleService")
	if _, found := Manager.targets["SampleService"]; found {
		t.Errorf("target not removed")
	}
	if st := conn.GetState(); st != connectivity.Shutdown {
		t.Errorf("removing a target must close its connection, state = %s", st)
	}
}

func TestManagerSetTargetTLS(t *testing.T) {
	e := newEnv(t)
	resetManager(t)
	registerSampleService(t, e)
	if err := Manager.AddTarget("SampleService", e.addr, "", "sni.example", &GRPCOptions{TLSVersion: tls.VersionTLS12}); err != nil {
		t.Fatal(err)
	}
	c := Manager.targets["SampleService"]
	if c.Options.IsTLS || c.tlsConfig != nil {
		t.Fatalf("target starts as plain")
	}
	Manager.SetTargetTLS("SampleService", true, false)
	if !c.Options.IsTLS || c.Options.VerifyTLS || c.tlsConfig == nil || !c.tlsConfig.InsecureSkipVerify || c.tlsConfig.ServerName != "sni.example" {
		t.Errorf("TLS not enabled: %+v %+v", c.Options, c.tlsConfig)
	}
	Manager.SetTargetTLS("SampleService", true, true)
	if !c.Options.VerifyTLS || c.tlsConfig.InsecureSkipVerify {
		t.Errorf("verification not enabled: %+v", c.Options)
	}
	Manager.SetTargetTLS("SampleService", false, false)
	if c.Options.IsTLS || c.tlsConfig != nil || c.tlsCredentials != nil {
		t.Errorf("TLS not disabled: %+v", c.Options)
	}
}

func TestManagerClearTargets(t *testing.T) {
	e := newEnv(t)
	resetManager(t)
	registerSampleService(t, e)
	Manager.AddTarget("SampleService", e.addr, "", "", &GRPCOptions{})
	c := Manager.targets["SampleService"]
	Manager.ConnectTarget("SampleService")
	conn := c.GRPC()
	Manager.ClearTargets()
	if len(Manager.targets) != 0 {
		t.Errorf("targets not cleared")
	}
	if conn.GetState() != connectivity.Shutdown {
		t.Errorf("clearing the targets must close their connections")
	}
}
