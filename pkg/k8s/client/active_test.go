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

package client

import (
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestActiveIsTheCurrentClientOrElseTheDefault(t *testing.T) {
	isolate(t)
	def, err := NewK8sClientForConfig("127.0.0.1:1", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewK8sClientForConfig("127.0.0.1:2", "")
	if err != nil {
		t.Fatal(err)
	}
	lock.Lock()
	Client, CurrentClient = def, nil
	lock.Unlock()
	if Active() != def {
		t.Errorf("active = %s, want the default client when there was no switch", desc(Active()))
	}
	if old := ReplaceCurrentClient(other); old != nil {
		t.Errorf("replaced client = %s, want none", desc(old))
	}
	if Active() != other {
		t.Errorf("active = %s, want the current client", desc(Active()))
	}
	if old := ReplaceCurrentClient(nil); old != other || Active() != def {
		t.Errorf("after putting the current client away the active is %s and the replaced is %s, want the default and the other", desc(Active()), desc(old))
	}
	lock.Lock()
	Client = nil
	lock.Unlock()
	if Active() != nil {
		t.Errorf("active = %s, want none when there is no client", desc(Active()))
	}
}

func TestSwitchingTheContextMakesItTheActiveClient(t *testing.T) {
	isolate(t)
	one, _ := NewK8sClientForConfig("127.0.0.1:1", "")
	lock.Lock()
	Clients = map[string]*K8sClient{"one": one}
	CurrentClient = nil
	lock.Unlock()
	if !SetCurrentK8sClient("one") || Active() != one {
		t.Errorf("active = %s after switching, want the client that was switched to", desc(Active()))
	}
}

func TestIDIsTheServerOfTheClient(t *testing.T) {
	c, err := NewK8sClientForConfig("https://10.0.0.1:6443", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID() != "https://10.0.0.1:6443" {
		t.Errorf("id = %q", c.ID())
	}
	if (*K8sClient)(nil).ID() != "" || (&K8sClient{}).ID() != "" {
		t.Error("a client that is not configured has an id")
	}
}

func TestStreamConfigHasNoTimeAndLeavesTheConfigAlone(t *testing.T) {
	c, err := NewK8sClientForConfig("127.0.0.1:1", "")
	if err != nil {
		t.Fatal(err)
	}
	stream := c.StreamConfig()
	if stream.Timeout != 0 || stream.Host != c.Config.Host {
		t.Errorf("stream config = host %q timeout %v, want the host of the client and no timeout", stream.Host, stream.Timeout)
	}
	if c.Config.Timeout != 5*time.Second {
		t.Errorf("timeout of the client = %v, want 5s: the copy is the one that is changed", c.Config.Timeout)
	}
	if d, err := c.StreamDynamic(); err != nil || d == nil {
		t.Errorf("stream dynamic client = %v, %v", d, err)
	}
}

func TestNewK8sClientForRestConfigCopiesTheConfig(t *testing.T) {
	cfg := &rest.Config{Host: "127.0.0.1:1", Timeout: time.Second}
	c, err := NewK8sClientForRestConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Config == cfg || c.Config.Host != "127.0.0.1:1" || c.Config.Timeout != time.Second {
		t.Errorf("config = %+v, want a copy of the one that was given", c.Config)
	}
	if c.Client == nil || c.TypedClient == nil || c.Mapper == nil {
		t.Errorf("client is not complete: %s", desc(c))
	}
}

func TestCAThatIsNotBase64IsRefused(t *testing.T) {
	c, err := NewK8sClientForConfig("127.0.0.1:1", "not*base64!")
	if err == nil || c != nil || !strings.Contains(err.Error(), "base 64") {
		t.Errorf("client = %v, err = %v, want an error about the CA data", c, err)
	}
}
