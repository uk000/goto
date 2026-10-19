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

package invocation

import (
	"goto/pkg/transport"
	"sync"
	"testing"
)

func newLongRunningSpec(t *testing.T, name string) *InvocationSpec {
	t.Helper()
	spec := &InvocationSpec{Name: name, URL: "http://127.0.0.1:1/", LongRunning: true}
	if err := ValidateSpec(spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestCloneKeepsTheSpecAndLinksToTheParent(t *testing.T) {
	spec := newLongRunningSpec(t, "clone-keeps")
	spec.Headers["x-a"] = "1"
	clone := spec.Clone()
	if clone == spec || clone.parent != spec {
		t.Fatalf("clone = %p with parent %p, want a new spec whose parent is %p", clone, clone.parent, spec)
	}
	if clone.Name != "clone-keeps" || clone.URL != "http://127.0.0.1:1/" || !clone.LongRunning || clone.Headers["x-a"] != "1" || !clone.http {
		t.Errorf("clone = %+v, want the values of the spec", clone)
	}
	if clone.lock == nil || clone.lock != spec.lock {
		t.Errorf("lock of the clone = %p, want the lock of the parent %p, as it guards the transport that they share", clone.lock, spec.lock)
	}
}

func TestCloneOfASpecThatWasNotValidated(t *testing.T) {
	spec := &InvocationSpec{Name: "not-validated", URL: "http://127.0.0.1:1/"}
	clone := spec.Clone()
	if clone.parent != spec || clone.Name != "not-validated" || clone.lock != nil {
		t.Errorf("clone = %+v, want a copy with the spec as parent and no lock", clone)
	}
}

// What clones are made of can be what another clone is setting up, as the clones of a long running spec share the transport of
// their parent, which the first of them creates. Making a clone reads the parent, so it has to be in step with that.
func TestCloneWhileTransportsAreCreatedForOtherClones(t *testing.T) {
	const workers, rounds = 8, 40
	parent := newLongRunningSpec(t, "clone-race")
	transports := make([][]transport.ClientTransport, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				clone := parent.Clone()
				transports[w] = append(transports[w], clone.createTransport(&InvocationTracker{Target: clone}))
			}
		}(w)
	}
	close(start)
	wg.Wait()
	shared := parent.Transport
	if shared == nil {
		t.Fatal("parent has no transport after clones created theirs")
	}
	for w := range transports {
		for i, ct := range transports[w] {
			if ct != shared {
				t.Fatalf("transport %d of worker %d = %p, want the one that is shared through the parent, %p", i, w, ct, shared)
			}
		}
	}
	for _, ct := range transports[0] {
		ct.Close()
	}
}

func TestLongRunningClonesShareTheTransportOfTheirParent(t *testing.T) {
	parent := newLongRunningSpec(t, "clone-share")
	first := parent.Clone()
	ct1 := first.createTransport(&InvocationTracker{Target: first})
	if ct1 == nil || parent.Transport != ct1 || first.Transport != ct1 {
		t.Fatalf("transport of the first clone = %v, parent's = %v, clone's = %v, want one that is stored with both", ct1, parent.Transport, first.Transport)
	}
	second := parent.Clone()
	if second.Transport != ct1 {
		t.Errorf("transport of a clone made afterwards = %v, want the one of the parent, %v", second.Transport, ct1)
	}
	if ct2 := second.createTransport(&InvocationTracker{Target: second}); ct2 != ct1 {
		t.Errorf("transport created for the second clone = %v, want the shared one, %v", ct2, ct1)
	}
	ct1.Close()
}
