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

package k8sApi

// These tests run the APIs against a real cluster, which is the one of the kube context that is named in the environment:
//
//	GOTO_K8S_TEST_CONTEXT=rancher-desktop go test -count=1 -run Integration -v ./pkg/k8s/api/
//
// They are skipped without it. The context is used as it is named and the current context of the kube config is never
// read or changed, so that a cluster that the tests are not meant for is not reached by a different current context.
// They refuse a cluster that is not on the loopback interface (a local cluster, like the one of Rancher Desktop), and
// work in a namespace that they make (goto-it-xxxxxxxx) and delete when they are done. Nothing else is changed.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/ctl"
	"goto/pkg/k8s/store"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	integrationContextEnv = "GOTO_K8S_TEST_CONTEXT"
	integrationNsPrefix   = "goto-it-"
)

// integrationCluster is a client for the cluster of the context of the environment, which is the active one until the test
// is done. It is made with the timeout that the clients of the config API have, which the watches have to outlast.
func integrationCluster(t *testing.T) *k8sClient.K8sClient {
	t.Helper()
	name := os.Getenv(integrationContextEnv)
	if name == "" {
		t.Skipf("%s is not set: set it to the name of a local kube context, like rancher-desktop, to run this against a real cluster", integrationContextEnv)
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{CurrentContext: name}).ClientConfig()
	if err != nil {
		t.Fatalf("kube context [%s] cannot be loaded: %v", name, err)
	}
	u, err := url.Parse(cfg.Host)
	if err != nil {
		t.Fatalf("server [%s] of the context [%s] is not a url: %v", cfg.Host, name, err)
	}
	if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing to run against [%s] of the context [%s]: it is not on the loopback interface, so it is not a local cluster", cfg.Host, name)
	}
	cfg.Timeout = 5 * time.Second
	c, err := k8sClient.NewK8sClientForRestConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The cluster has to be reachable, and is not used otherwise.
	version, err := c.TypedClient.RESTClient().Get().AbsPath("/version").DoRaw(context.Background())
	if err != nil {
		t.Skipf("the cluster of the context [%s] is not reachable: %v", name, err)
	}
	t.Logf("using the cluster of the context [%s] at [%s]: %s", name, cfg.Host, strings.ReplaceAll(strings.TrimSpace(string(version)), "\n", " "))

	oldClients := map[string]*k8sClient.K8sClient{}
	for n, existing := range k8sClient.Clients {
		oldClients[n] = existing
	}
	oldCurrent := k8sClient.ReplaceCurrentClient(c)
	k8sClient.Clients[name] = c
	store.Cache.Clear()
	t.Cleanup(func() {
		store.Cache.Clear()
		k8sClient.ReplaceCurrentClient(oldCurrent)
		for n := range k8sClient.Clients {
			if oldClients[n] == nil {
				delete(k8sClient.Clients, n)
			}
		}
		for n, existing := range oldClients {
			k8sClient.Clients[n] = existing
		}
	})
	return c
}

// integrationNamespace makes a namespace for the test, which is deleted when the test is done.
func integrationNamespace(t *testing.T, c *k8sClient.K8sClient) string {
	t.Helper()
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	ns := integrationNsPrefix + hex.EncodeToString(suffix)
	_, err := c.TypedClient.Namespaces().Create(context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("cannot make the namespace [%s]: %v", ns, err)
	}
	t.Cleanup(func() {
		// Only a namespace that this test made is ever deleted.
		if !strings.HasPrefix(ns, integrationNsPrefix) {
			t.Errorf("not deleting the namespace [%s]: it is not one of the tests", ns)
			return
		}
		if err := c.TypedClient.Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}); err != nil {
			t.Errorf("cannot delete the namespace [%s], which has to be done by hand: %v", ns, err)
		}
	})
	return ns
}

func configMapYaml(namespace, name, value string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  k: %s
`, name, namespace, value)
}

// applyConfigMap stores the config map as the YAML of a unit and applies it with the YAML API, as goto is used.
func applyConfigMap(t *testing.T, unit, namespace, name, value string) {
	t.Helper()
	if _, errs := store.Kstore.StoreYaml(unit, io.NopCloser(strings.NewReader(configMapYaml(namespace, name, value)))); len(errs) > 0 {
		t.Fatalf("storing the YAML failed: %v", errs)
	}
	if _, errs := ctl.ApplyYaml(unit, namespace, "ConfigMap", name, namespace, false); len(errs) > 0 {
		t.Fatalf("applying the YAML failed: %v", errs)
	}
}

func deleteConfigMap(t *testing.T, unit, namespace, name string) {
	t.Helper()
	if _, errs := ctl.ApplyYaml(unit, namespace, "ConfigMap", name, namespace, true); len(errs) > 0 {
		t.Fatalf("deleting failed: %v", errs)
	}
}

// waitEvent reads the stream until the event of the type and name, and checks that all that it reads is of the namespace.
// A cluster has config maps that it makes in each namespace by itself (Istio makes istio-ca-root-cert and istio-ca-crl),
// which are in the watch as well as the ones of the test, and are passed over.
func waitEvent(t *testing.T, s *stream, namespace, eventType, name string) event {
	t.Helper()
	for {
		e := s.next(t)
		if got, _ := e.Object["metadata"].(map[string]interface{})["namespace"].(string); got != namespace {
			t.Fatalf("event = %s of [%s/%s], want only what is in the namespace [%s]", e.Type, got, e.name(), namespace)
		}
		if e.Type == eventType && e.name() == name {
			return e
		}
	}
}

func dataOf(e event) string {
	data, _ := e.Object["data"].(map[string]interface{})
	value, _ := data["k"].(string)
	return value
}

func TestIntegrationReadResources(t *testing.T) {
	integrationCluster(t)
	rr := call(t, "GET", "/k8s/resources/namespaces", "Accept", "application/json")
	wantStatus(t, rr, 200)
	list := decode(t, rr)
	items, _ := list["items"].([]interface{})
	found := false
	for _, item := range items {
		if metadataName(item.(map[string]interface{})) == "kube-system" {
			found = true
		}
	}
	if list["kind"] != "NamespaceList" || !found {
		t.Errorf("namespaces = kind %v with %d items, want a list with kube-system", list["kind"], len(items))
	}

	rr = call(t, "GET", "/k8s/resources/pods/kube-system/all", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if pods, _ := decode(t, rr)["items"].([]interface{}); len(pods) == 0 {
		t.Error("kube-system has no pods in the list")
	}

	rr = call(t, "GET", "/k8s/resources/namespaces/kube-system", "Accept", "application/json")
	wantStatus(t, rr, 200)
	if got := metadataName(decode(t, rr)); got != "kube-system" {
		t.Errorf("namespace = %q", got)
	}
	wantStatus(t, call(t, "GET", "/k8s/resources/namespaces/"+integrationNsPrefix+"missing"), 404)
}

func TestIntegrationApplyReadAndWatchThroughTheAPIs(t *testing.T) {
	c := integrationCluster(t)
	ns := integrationNamespace(t, c)
	const name, unit = "it-cm", "it-unit"
	srv := watchServer(t)

	applyConfigMap(t, unit, ns, name, "v1")
	rr := call(t, "GET", "/k8s/resources/configmaps/"+ns+"/"+name, "Accept", "application/json")
	wantStatus(t, rr, 200)
	if data, _ := decode(t, rr)["data"].(map[string]interface{}); data["k"] != "v1" {
		t.Fatalf("read after the apply = %v", data)
	}

	s := openStream(t, srv, "/k8s/watch/configmaps/"+ns+"/all")
	if e := waitEvent(t, s, ns, "ADDED", name); dataOf(e) != "v1" {
		t.Errorf("first event = %v, want the config map that exists, as added", e.Object)
	}

	// A change that goes through goto is seen by the watch, and by the next read, which is not served from the cache.
	applyConfigMap(t, unit, ns, name, "v2")
	if e := waitEvent(t, s, ns, "MODIFIED", name); dataOf(e) != "v2" {
		t.Errorf("event = %v, want the change to v2", e.Object)
	}
	rr = call(t, "GET", "/k8s/resources/configmaps/"+ns+"/"+name, "Accept", "application/json")
	if data, _ := decode(t, rr)["data"].(map[string]interface{}); data["k"] != "v2" {
		t.Errorf("read after the second apply = %v, want v2 and not what was cached", data)
	}

	// So is a change that is made by somebody else, which the read does not see until the cache lets go of the resource.
	cm, err := c.TypedClient.ConfigMaps(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cm.Data["k"] = "v3"
	if _, err := c.TypedClient.ConfigMaps(ns).Update(context.Background(), cm, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := waitEvent(t, s, ns, "MODIFIED", name); dataOf(e) != "v3" {
		t.Errorf("event = %v, want the change to v3 that was made outside goto", e.Object)
	}

	deleteConfigMap(t, unit, ns, name)
	waitEvent(t, s, ns, "DELETED", name)
	wantStatus(t, call(t, "GET", "/k8s/resources/configmaps/"+ns+"/"+name), 404)
}

func TestIntegrationWatchOfANameAndOfSelectors(t *testing.T) {
	c := integrationCluster(t)
	ns := integrationNamespace(t, c)
	srv := watchServer(t)
	applyConfigMap(t, "it-unit", ns, "mine", "a")
	applyConfigMap(t, "it-unit", ns, "other", "a")

	s := openStream(t, srv, "/k8s/watch/configmaps/"+ns+"/mine")
	s.wantNext(t, "ADDED", "mine")
	applyConfigMap(t, "it-unit", ns, "other", "b")
	s.wantNone(t)
	applyConfigMap(t, "it-unit", ns, "mine", "b")
	s.wantNext(t, "MODIFIED", "mine")

	// A watch of a kind that is in no namespace, by the name that the namespace has.
	ns2 := openStream(t, srv, "/k8s/watch/namespaces/"+ns)
	ns2.wantNext(t, "ADDED", ns)

	// A watch with a timeout ends with it, and the server closes it.
	timed := openStream(t, srv, "/k8s/watch/configmaps/"+ns+"/all?timeout=2")
	start := time.Now()
	timed.wantEnd(t, 10*time.Second)
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("the watch ended after %v, want it to last the 2 seconds", elapsed)
	}
}

// The clients that the config API makes have a timeout of 5 seconds, which cut the watches that they were used for.
func TestIntegrationWatchOutlastsTheTimeoutOfTheClient(t *testing.T) {
	c := integrationCluster(t)
	if c.Config.Timeout != 5*time.Second {
		t.Fatalf("timeout of the client = %v, want the 5 seconds that the test is about", c.Config.Timeout)
	}
	ns := integrationNamespace(t, c)
	applyConfigMap(t, "it-unit", ns, "slow", "a")
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/"+ns+"/slow")
	s.wantNext(t, "ADDED", "slow")
	time.Sleep(7 * time.Second)
	applyConfigMap(t, "it-unit", ns, "slow", "b")
	if e := s.wantNext(t, "MODIFIED", "slow"); dataOf(e) != "b" {
		t.Errorf("event = %v, want the change after the timeout of the client", e.Object)
	}
}

func TestIntegrationSwitchContext(t *testing.T) {
	c := integrationCluster(t)
	name := os.Getenv(integrationContextEnv)
	k8sClient.ReplaceCurrentClient(nil)
	k8sClient.Clients["it-ctx"] = c
	t.Cleanup(func() { delete(k8sClient.Clients, "it-ctx") })
	wantStatus(t, call(t, "POST", "/k8s/context/it-ctx"), 200)
	if k8sClient.Active() != c {
		t.Fatalf("active client = %+v, want the one of the context [%s]", k8sClient.Active(), name)
	}
	wantStatus(t, call(t, "GET", "/k8s/resources/namespaces/kube-system"), 200)
}

// The watch of a resource by callbacks, which informers report to, on a real cluster.
func TestIntegrationWatchResourceByCallbacks(t *testing.T) {
	c := integrationCluster(t)
	ns := integrationNamespace(t, c)
	const name, unit = "cb-cm", "it-unit"

	events := make(chan string, 50)
	w := &store.K8sResourceWatchCallback{
		Name:     "integration",
		OnAdd:    func(namespace, n string, obj interface{}) { events <- "add " + namespace + "/" + n },
		OnUpdate: func(namespace, n string, obj interface{}) { events <- "update " + namespace + "/" + n },
		OnDelete: func(namespace, n string, obj interface{}) { events <- "delete " + namespace + "/" + n },
	}
	store.WatcheResource("", "v1", "configmaps", ns, name, w)
	t.Cleanup(func() { store.UnwatchResource("", "v1", "configmaps", ns, name, w) })

	want := func(expected string) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for {
			select {
			case got := <-events:
				if got == expected {
					return
				}
			case <-deadline:
				t.Fatalf("did not get [%s]", expected)
			}
		}
	}
	applyConfigMap(t, unit, ns, name, "a")
	want("add " + ns + "/" + name)
	applyConfigMap(t, unit, ns, name, "b")
	want("update " + ns + "/" + name)
	deleteConfigMap(t, unit, ns, name)
	want("delete " + ns + "/" + name)
}
