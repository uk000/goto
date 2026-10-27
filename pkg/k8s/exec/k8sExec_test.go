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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/k8stest"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
)

func pod(namespace, name, app string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]interface{}{
			"name": name, "namespace": namespace,
			"labels": map[string]interface{}{"app": app},
		},
	}
}

// podExec runs PodExec, and fails the test when it panics or has not returned by the deadline.
func podExec(t *testing.T, namespace, label, container, command string) (out string, err error) {
	t.Helper()
	type result struct {
		out string
		err error
		p   any
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() { r.p = recover(); done <- r }()
		r.out, r.err = PodExec(namespace, label, container, command)
	}()
	select {
	case r := <-done:
		if r.p != nil {
			t.Fatalf("PodExec panicked: %v", r.p)
		}
		return r.out, r.err
	case <-time.After(30 * time.Second):
		t.Fatal("PodExec did not return in time")
	}
	return "", nil
}

func TestPodExecWithoutAClientFails(t *testing.T) {
	old := k8sClient.Client
	t.Cleanup(func() { k8sClient.Client = old })
	k8sClient.Client = nil
	if out, err := podExec(t, "ns", "app=x", "c", "ls"); err == nil || out != "" || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("out = %q, err = %v, want an error that there is no client", out, err)
	}
	// A client that has no typed client is not one that can find pods.
	k8sClient.Client = &k8sClient.K8sClient{}
	if out, err := podExec(t, "ns", "app=x", "c", "ls"); err == nil || out != "" || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("out = %q, err = %v, want an error that the typed client is missing", out, err)
	}
}

func TestPodExecWithNoPodForTheLabelFails(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "other-pod", "other"))
	out, err := podExec(t, "exec-ns", "app=wanted", "c", "ls")
	if err == nil || out != "" {
		t.Fatalf("out = %q, err = %v, want an error", out, err)
	}
	if want := "no pods in namespace [exec-ns] for label [app=wanted]"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	// Nothing was run: the pods were only listed.
	for _, r := range api.Requests() {
		if r.Method != "GET" {
			t.Errorf("request %s %s, want only the listing of pods", r.Method, r.Path)
		}
	}
}

func TestPodExecListsThePodsOfTheNamespaceByLabel(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "wanted-pod", "wanted"))
	podExec(t, "exec-ns", "app=wanted", "c", "ls")
	reqs := api.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the API server")
	}
	first := reqs[0]
	if first.Method != "GET" || first.Path != "/api/v1/namespaces/exec-ns/pods" {
		t.Errorf("first request = %s %s, want the listing of the pods of the namespace", first.Method, first.Path)
	}
	if q, _ := url.ParseQuery(first.RawQuery); q.Get("labelSelector") != "app=wanted" {
		t.Errorf("query = %q, want the label selector", first.RawQuery)
	}
}

func TestPodExecWhenListingPodsFailsReturnsTheError(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "a-pod", "wanted"))
	api.FailResources(500)
	out, err := podExec(t, "exec-ns", "app=wanted", "c", "ls")
	if err == nil || out != "" {
		t.Fatalf("out = %q, err = %v, want the error of the API server", out, err)
	}
	for _, r := range api.Requests() {
		if strings.HasSuffix(r.Path, "/exec") {
			t.Errorf("a command was run after listing pods failed: %s %s", r.Method, r.Path)
		}
	}
}

func TestPodExecRunsTheCommandInTheFirstPodAndContainer(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "b-pod", "wanted"))
	api.Put(pod("exec-ns", "a-pod", "wanted"))
	api.Put(pod("exec-ns", "z-pod", "other"))
	// The API server of the test does not speak the protocol of exec, so the command does not get to run, and what the
	// request was is what is looked at.
	out, err := podExec(t, "exec-ns", "app=wanted", "the-container", "echo hello; ls /")
	if err == nil || out != "" {
		t.Fatalf("out = %q, err = %v, want an error from a server that does not do exec", out, err)
	}
	var exec *url.URL
	method := ""
	for _, r := range api.Requests() {
		if strings.HasSuffix(r.Path, "/exec") {
			exec = &url.URL{Path: r.Path, RawQuery: r.RawQuery}
			method = r.Method
		}
	}
	if exec == nil {
		t.Fatal("no request to run the command reached the API server")
	}
	if method != "POST" || exec.Path != "/api/v1/namespaces/exec-ns/pods/a-pod/exec" {
		t.Errorf("request = %s %s, want a POST to exec in the first pod by name", method, exec.Path)
	}
	q := exec.Query()
	if got := q["command"]; len(got) != 3 || got[0] != "sh" || got[1] != "-c" || got[2] != "echo hello; ls /" {
		t.Errorf("command = %q, want it to be run by a shell", got)
	}
	if q.Get("container") != "the-container" || q.Get("stdout") != "true" || q.Get("stderr") != "true" || q.Get("stdin") == "true" || q.Get("tty") == "true" {
		t.Errorf("query = %v", q)
	}
}

// serveExec answers a request to run a command the way the kubelet does with the version 4 of the protocol of exec: it
// has what the command wrote to stdout and stderr sent on their streams, and then the status of the command on the
// error stream. It reports a failure to do that on the channel.
func serveExec(w http.ResponseWriter, req *http.Request, stdout, stderr string, status metav1.Status, failed chan<- error) {
	if _, err := httpstream.Handshake(req, w, []string{"v4.channel.k8s.io"}); err != nil {
		failed <- err
		return
	}
	type received struct {
		stream    httpstream.Stream
		replySent <-chan struct{}
	}
	streams := make(chan received, 8)
	conn := spdy.NewResponseUpgrader().UpgradeResponse(w, req, func(stream httpstream.Stream, replySent <-chan struct{}) error {
		streams <- received{stream, replySent}
		return nil
	})
	if conn == nil {
		failed <- http.ErrNotSupported
		return
	}
	defer conn.Close()
	byType := map[string]received{}
	for len(byType) < 3 {
		select {
		case r := <-streams:
			byType[r.stream.Headers().Get(corev1.StreamType)] = r
		case <-time.After(10 * time.Second):
			failed <- http.ErrHandlerTimeout
			return
		}
	}
	for _, r := range byType {
		<-r.replySent
	}
	statusJSON, _ := json.Marshal(status)
	for name, data := range map[string]string{corev1.StreamTypeStdout: stdout, corev1.StreamTypeStderr: stderr} {
		if data != "" {
			byType[name].stream.Write([]byte(data))
		}
	}
	byType[corev1.StreamTypeError].stream.Write(statusJSON)
	for _, r := range byType {
		r.stream.Close()
	}
}

// installExecServer makes the client of k8s talk to a server that has the pods of the API server of the test, and that
// runs commands as serveExec does.
func installExecServer(t *testing.T, api *k8stest.API, stdout, stderr string, status metav1.Status) {
	t.Helper()
	failed := make(chan error, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/exec") {
			serveExec(w, r, stdout, stderr, status, failed)
			return
		}
		api.Server.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	c, err := k8sClient.NewK8sClientForConfig(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	old := k8sClient.Client
	k8sClient.Client = c
	t.Cleanup(func() {
		k8sClient.Client = old
		select {
		case err := <-failed:
			t.Errorf("the server of exec failed: %v", err)
		default:
		}
	})
}

func TestPodExecReturnsWhatTheCommandWroteToStdout(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "run-pod", "run"))
	installExecServer(t, api, "hello from the pod\n", "ignored", metav1.Status{Status: metav1.StatusSuccess})
	out, err := podExec(t, "exec-ns", "app=run", "c", "echo hello")
	if err != nil || out != "hello from the pod\n" {
		t.Errorf("out = %q, err = %v, want the output of the command", out, err)
	}
}

func TestPodExecReturnsStderrWhenThereIsNoStdout(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "run-pod", "run"))
	installExecServer(t, api, "", "only an error message\n", metav1.Status{Status: metav1.StatusSuccess})
	out, err := podExec(t, "exec-ns", "app=run", "c", "ls /nothing")
	if err != nil || out != "only an error message\n" {
		t.Errorf("out = %q, err = %v, want what the command wrote to stderr", out, err)
	}
}

func TestPodExecWithNoOutputIsEmpty(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "run-pod", "run"))
	installExecServer(t, api, "", "", metav1.Status{Status: metav1.StatusSuccess})
	out, err := podExec(t, "exec-ns", "app=run", "c", "true")
	if err != nil || out != "" {
		t.Errorf("out = %q, err = %v, want no output and no error", out, err)
	}
}

func TestPodExecReturnsTheFailureOfTheCommand(t *testing.T) {
	api := k8stest.Install(t)
	api.Put(pod("exec-ns", "run-pod", "run"))
	installExecServer(t, api, "partial", "", metav1.Status{
		Status: metav1.StatusFailure, Reason: "InternalError", Message: "the command could not be run",
	})
	out, err := podExec(t, "exec-ns", "app=run", "c", "false")
	if err == nil || !strings.Contains(err.Error(), "the command could not be run") {
		t.Errorf("out = %q, err = %v, want the failure that the server reported", out, err)
	}
	if out != "" {
		t.Errorf("out = %q, want no output with a failure", out)
	}
}
