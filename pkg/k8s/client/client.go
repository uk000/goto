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
	"encoding/base64"
	"fmt"
	"goto/pkg/global"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedclient "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type K8sClient struct {
	Config      *rest.Config
	Client      dynamic.Interface
	clientset   *kubernetes.Clientset
	dClient     *discovery.DiscoveryClient
	TypedClient *typedclient.CoreV1Client
	Mapper      *restmapper.DeferredDiscoveryRESTMapper
}

var (
	CurrentClient *K8sClient
	Client        = createDefaultK8sClient()
	Clients       = map[string]*K8sClient{}
	// lock guards Clients and CurrentClient, which the admin APIs change while requests read them.
	lock sync.RWMutex
)

// SetCurrentK8sClient switches the current client to the one of the given name. The current client stays as it is when
// there is no client of that name.
func SetCurrentK8sClient(name string) bool {
	lock.Lock()
	defer lock.Unlock()
	k8sClient := Clients[name]
	if k8sClient == nil {
		return false
	}
	CurrentClient = k8sClient
	return true
}

// GetClient returns the client that was configured with the given name, or nil.
func GetClient(name string) *K8sClient {
	lock.RLock()
	defer lock.RUnlock()
	return Clients[name]
}

// Active returns the client that the APIs talk to: the one that was switched to, or the default one when there was no
// switch. It is nil when there is neither. A caller that makes more than one request for what it does should call this
// once and use the client that it got, so that a switch in between does not mix two clusters.
func Active() *K8sClient {
	lock.RLock()
	defer lock.RUnlock()
	if CurrentClient != nil {
		return CurrentClient
	}
	return Client
}

// ReplaceCurrentClient makes the client given the current one (nil for none) and returns the one that was current.
func ReplaceCurrentClient(c *K8sClient) *K8sClient {
	lock.Lock()
	defer lock.Unlock()
	old := CurrentClient
	CurrentClient = c
	return old
}

// ID tells the cluster that the client is for, which is what the data that is kept for a cluster is kept by. It is empty
// for a client that is not configured.
func (c *K8sClient) ID() string {
	if c == nil || c.Config == nil {
		return ""
	}
	return c.Config.Host
}

// StreamConfig returns a copy of the config of the client for requests that last, like watches and streams of exec. The
// timeout of the config is for requests that are answered at once, and cuts those that last.
func (c *K8sClient) StreamConfig() *rest.Config {
	cfg := rest.CopyConfig(c.Config)
	cfg.Timeout = 0
	return cfg
}

// StreamDynamic returns a dynamic client for requests that last (see StreamConfig).
func (c *K8sClient) StreamDynamic() (dynamic.Interface, error) {
	return dynamic.NewForConfig(c.StreamConfig())
}

// NewK8sClientForRestConfig makes a client for the config, which is not changed. It does not contact the server.
func NewK8sClientForRestConfig(config *rest.Config) (*K8sClient, error) {
	k8sClient := &K8sClient{Config: rest.CopyConfig(config)}
	if err := configureClient(k8sClient); err != nil {
		return nil, err
	}
	return k8sClient, nil
}

// NewK8sClientForConfig makes a client for the API server at the url, which verifies the server with the CA certificate
// given as base 64 text. It does not contact the server.
func NewK8sClientForConfig(url, caData string) (*K8sClient, error) {
	caBytes, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(caData)))
	if err != nil {
		return nil, fmt.Errorf("CA data is not base 64: %w", err)
	}
	k8sClient := &K8sClient{
		Config: &rest.Config{
			Host: url,
			TLSClientConfig: rest.TLSClientConfig{
				CAData: caBytes,
			},
			Timeout: 5 * time.Second,
		},
	}
	if err := configureClient(k8sClient); err != nil {
		return nil, err
	}
	return k8sClient, nil
}

func CreateK8sClientForConfig(name, url, caData string) error {
	k8sClient, err := NewK8sClientForConfig(url, caData)
	if err != nil {
		log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
		return err
	}
	lock.Lock()
	defer lock.Unlock()
	Clients[name] = k8sClient
	return nil
}

func createDefaultK8sClient() *K8sClient {
	k8sClient := &K8sClient{}
	var err error
	if global.ServerConfig.KubeConfig != "" {
		if k8sClient.Config, err = clientcmd.BuildConfigFromFlags("", global.ServerConfig.KubeConfig); err != nil {
			log.Printf("K8s: Failed to load kube config [%s] with error: %s\n", global.ServerConfig.KubeConfig, err.Error())
			return nil
		}
	} else {
		if k8sClient.Config, err = rest.InClusterConfig(); err != nil {
			kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
			if k8sClient.Config, err = clientcmd.BuildConfigFromFlags("", kubeconfig); err != nil {
				log.Printf("K8s: Failed to load kube config [%s] with error: %s\n", kubeconfig, err.Error())
				return nil
			}
		}
	}
	if err = configureClient(k8sClient); err != nil {
		log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
		return nil
	}
	lock.Lock()
	Clients["default"] = k8sClient
	lock.Unlock()
	return k8sClient
}

func configureClient(k8sClient *K8sClient) (err error) {
	k8sClient.Config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
	if k8sClient.clientset, err = kubernetes.NewForConfig(k8sClient.Config); err != nil {
		return
	}

	if k8sClient.Client, err = dynamic.NewForConfig(k8sClient.Config); err != nil {
		return
	}
	if k8sClient.TypedClient, err = typedclient.NewForConfig(k8sClient.Config); err != nil {
		return
	}
	k8sClient.dClient, err = discovery.NewDiscoveryClientForConfig(k8sClient.Config)
	if err != nil {
		return
	}
	k8sClient.Mapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(k8sClient.dClient))
	return
}
