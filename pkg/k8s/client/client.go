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

// NewK8sClientForConfig makes a client for the API server at the url, which verifies the server with the CA certificate
// given as base 64 text. It does not contact the server.
func NewK8sClientForConfig(url, caData string) (*K8sClient, error) {
	caBytes, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(caData)))
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
				log.Printf("K8s: Failed to load kube config [%s] with error: %s\n", global.ServerConfig.KubeConfig, err.Error())
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
