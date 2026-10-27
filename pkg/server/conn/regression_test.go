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

package conn

import (
	"strings"
	"testing"

	"goto/pkg/constants"
)

// A request that has no connection (the ones that are made of gRPC calls are) is told its local address by the listener
// that it is served by. The default listener, which serves the ports that have no listener, has none, and was asked its
// address.
func TestMiddlewareWithoutConnectionOrListenerDoesNotPanic(t *testing.T) {
	port := newPort()
	useDefaultListener(t, port, false, false, nil)
	tr := newRequest(t, port, nil, nil)
	var s *served
	panicked := func() (recovered any) {
		defer func() { recovered = recover() }()
		s = serve(tr, true)
		return nil
	}()
	if panicked != nil {
		t.Fatalf("the middleware panicked: %v", panicked)
	}
	if s.nextCalls != 1 {
		t.Errorf("next was called %d times, want once", s.nextCalls)
	}
	if got := s.rr.Header().Get(constants.HeaderGotoProtocol); got != "HTTP/1.1" {
		t.Errorf("protocol header = %q, want the Goto headers to be sent", got)
	}
	if got := logOf(tr); !strings.Contains(got, "LocalAddr: [],") {
		t.Errorf("log = %q, want an empty local address when none is known", got)
	}
}
