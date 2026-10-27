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

package cmd

import (
	"reflect"
	"testing"

	"goto/pkg/global"
)

// The headers of the client config are a nil map when the config is new, and the client mode that has headers in its
// arguments used to write to it, which panicked: "assignment to entry in nil map".
func TestRegressionClientHeadersInArgsDoNotPanicWhenTheConfigHasNone(t *testing.T) {
	isolate(t)
	setupClientArgs()
	parse(t, "-client", "-urls", "http://target", "-headers", "x-one:1,x-two:2")
	if global.CmdClientConfig.Headers != nil {
		t.Fatalf("headers = %v, want a config that has none yet", global.CmdClientConfig.Headers)
	}
	within(t, "processClientArgs", processClientArgs)
	if want := map[string]string{"x-one": "1", "x-two": "2"}; !reflect.DeepEqual(global.CmdClientConfig.Headers, want) {
		t.Errorf("headers = %v, want %v", global.CmdClientConfig.Headers, want)
	}
}
