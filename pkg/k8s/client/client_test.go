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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goto/pkg/global"
)

// isolate gives back what the tests change in the package when the test is done, and keeps the home directory and
// the environment of the cluster out of reach of the test.
func isolate(t *testing.T) {
	t.Helper()
	lock.Lock()
	clients := map[string]*K8sClient{}
	for k, v := range Clients {
		clients[k] = v
	}
	current, def, kubeConfig := CurrentClient, Client, global.ServerConfig.KubeConfig
	lock.Unlock()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	t.Setenv("KUBECONFIG", "")
	t.Cleanup(func() {
		lock.Lock()
		defer lock.Unlock()
		Clients, CurrentClient, Client = clients, current, def
		global.ServerConfig.KubeConfig = kubeConfig
	})
}

// caPEM makes a CA certificate, as the base 64 text that the API of k8s takes.
func caData(t *testing.T) (string, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "k8s-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return base64.StdEncoding.EncodeToString(pemBytes), pemBytes
}

func writeKubeConfig(t *testing.T, path, server string) {
	t.Helper()
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: %s
contexts:
- name: x
  context:
    cluster: c
    user: u
current-context: x
users:
- name: u
  user:
    token: test-token
`, server)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewK8sClientForConfig(t *testing.T) {
	b64, pemBytes := caData(t)
	c, err := NewK8sClientForConfig("https://127.0.0.1:6443", b64)
	if err != nil {
		t.Fatal(err)
	}
	if c.Config.Host != "https://127.0.0.1:6443" || string(c.Config.TLSClientConfig.CAData) != string(pemBytes) {
		t.Errorf("config = %q with %d bytes of CA, want the url and the CA that was given", c.Config.Host, len(c.Config.TLSClientConfig.CAData))
	}
	if c.Config.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.Config.Timeout)
	}
	if c.Config.NegotiatedSerializer == nil {
		t.Error("the config has no serializer")
	}
	if c.Client == nil || c.TypedClient == nil || c.Mapper == nil || c.clientset == nil || c.dClient == nil {
		t.Errorf("client is not complete: %s", desc(c))
	}
}

func TestNewK8sClientForConfigWithoutCA(t *testing.T) {
	c, err := NewK8sClientForConfig("http://127.0.0.1:8001", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Config.Host != "http://127.0.0.1:8001" || len(c.Config.TLSClientConfig.CAData) != 0 {
		t.Errorf("config = %q with %d bytes of CA", c.Config.Host, len(c.Config.TLSClientConfig.CAData))
	}
}

func TestNewK8sClientForConfigWithBadCAFails(t *testing.T) {
	if c, err := NewK8sClientForConfig("https://127.0.0.1:6443", base64.StdEncoding.EncodeToString([]byte("not a certificate"))); err == nil {
		t.Errorf("a client was made with a CA that is not a certificate: %+v", c.Config)
	}
}

func TestCreateK8sClientForConfigRegistersTheClient(t *testing.T) {
	isolate(t)
	if GetClient("created") != nil {
		t.Fatal("the client is there before it is created")
	}
	if err := CreateK8sClientForConfig("created", "http://127.0.0.1:8002", ""); err != nil {
		t.Fatal(err)
	}
	c := GetClient("created")
	if c == nil || c.Config.Host != "http://127.0.0.1:8002" {
		t.Errorf("client = %s, want the one that was created", desc(c))
	}
	// Creating with a name again replaces the client.
	if err := CreateK8sClientForConfig("created", "http://127.0.0.1:8003", ""); err != nil {
		t.Fatal(err)
	}
	if again := GetClient("created"); again == c || again.Config.Host != "http://127.0.0.1:8003" {
		t.Errorf("client = %s, want the new one", desc(again))
	}
}

func TestCreateK8sClientForConfigWithBadCADoesNotRegister(t *testing.T) {
	isolate(t)
	bad := base64.StdEncoding.EncodeToString([]byte("not a certificate"))
	if err := CreateK8sClientForConfig("bad", "https://127.0.0.1:6443", bad); err == nil {
		t.Error("no error for a CA that is not a certificate")
	}
	if GetClient("bad") != nil {
		t.Error("a client that failed to be made is registered")
	}
}

func TestSetCurrentK8sClient(t *testing.T) {
	isolate(t)
	CreateK8sClientForConfig("one", "http://127.0.0.1:8004", "")
	CreateK8sClientForConfig("two", "http://127.0.0.1:8005", "")
	if !SetCurrentK8sClient("one") || CurrentClient != GetClient("one") {
		t.Fatalf("current = %s, want the first client", desc(CurrentClient))
	}
	if !SetCurrentK8sClient("two") || CurrentClient != GetClient("two") {
		t.Fatalf("current = %s, want the second client", desc(CurrentClient))
	}
	if SetCurrentK8sClient("") || SetCurrentK8sClient("missing") {
		t.Error("a client that does not exist was made current")
	}
}

func TestCreateDefaultK8sClientFromGivenKubeConfig(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	writeKubeConfig(t, path, "https://10.1.2.3:6443")
	global.ServerConfig.KubeConfig = path
	c := createDefaultK8sClient()
	if c == nil || c.Config.Host != "https://10.1.2.3:6443" || c.Config.BearerToken != "test-token" {
		t.Fatalf("client = %s, want the one of the kube config", desc(c))
	}
	if GetClient("default") != c {
		t.Error("the default client is not registered as default")
	}
	if c.Client == nil || c.Mapper == nil || c.TypedClient == nil {
		t.Errorf("client is not complete: %s", desc(c))
	}
}

func TestCreateDefaultK8sClientWithMissingKubeConfig(t *testing.T) {
	isolate(t)
	global.ServerConfig.KubeConfig = filepath.Join(t.TempDir(), "does-not-exist")
	if c := createDefaultK8sClient(); c != nil {
		t.Errorf("client = %s, want none for a kube config that does not exist", desc(c))
	}
	// A kube config is not looked for in the home directory when one is given.
	writeKubeConfig(t, filepath.Join(os.Getenv("HOME"), ".kube", "config"), "https://10.9.9.9:6443")
	if c := createDefaultK8sClient(); c != nil {
		t.Errorf("client = %s, want none: the given kube config has to be used", desc(c))
	}
}

func TestCreateDefaultK8sClientFallsBackToHomeKubeConfig(t *testing.T) {
	isolate(t)
	global.ServerConfig.KubeConfig = ""
	if c := createDefaultK8sClient(); c != nil {
		t.Fatalf("client = %s, want none when there is neither a cluster nor a kube config", desc(c))
	}
	writeKubeConfig(t, filepath.Join(os.Getenv("HOME"), ".kube", "config"), "https://10.4.5.6:6443")
	c := createDefaultK8sClient()
	if c == nil || c.Config.Host != "https://10.4.5.6:6443" {
		t.Fatalf("client = %s, want the one of the kube config in the home directory", desc(c))
	}
}

func TestCreateDefaultK8sClientWithBrokenKubeConfig(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte("this: is: not: a kube config"), 0o600); err != nil {
		t.Fatal(err)
	}
	global.ServerConfig.KubeConfig = path
	if c := createDefaultK8sClient(); c != nil {
		t.Errorf("client = %s, want none for a kube config that cannot be read", desc(c))
	}
}

// desc describes a client by its host. A client must not be printed whole: its mapper discovers the API server when it
// is printed.
func desc(c *K8sClient) string {
	if c == nil {
		return "<nil>"
	}
	return c.Config.Host
}
