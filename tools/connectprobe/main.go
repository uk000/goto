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

// connectprobe times the phases of a call that goes through an HTTP CONNECT proxy (a goto port in CONNECT mode, or any
// other), with the DNS resolver and the dialer of Go, which are what the goto client uses. curl has its own resolver and
// its own way of falling back between IPv4 and IPv6, so what it measures can differ from what the goto client gets. Run
// it where the client runs. It uses nothing but the standard library, so that it is small enough to copy into a pod.
// See README.md.
package main

import (
	"context"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
