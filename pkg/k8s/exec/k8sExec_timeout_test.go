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

package exec

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
)

// A cluster that does not answer is not waited for for ever.
func TestPodExecEndsWithItsTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c, err := k8sClient.NewK8sClientForConfig(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	// The timeout of the client is for what is answered at once, and is longer than the one that is looked at here.
	c.Config.Timeout = time.Minute
	c, err = k8sClient.NewK8sClientForRestConfig(c.Config)
	if err != nil {
		t.Fatal(err)
	}
	old, oldTimeout := k8sClient.Client, ExecTimeout
	k8sClient.Client = c
	ExecTimeout = 300 * time.Millisecond
	t.Cleanup(func() { k8sClient.Client, ExecTimeout = old, oldTimeout })

	start := time.Now()
	out, err := podExec(t, "ns", "app=x", "c", "ls")
	if err == nil || out != "" {
		t.Fatalf("out = %q, err = %v, want an error", out, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("PodExec took %v, want it to end with its timeout", elapsed)
	}
}
