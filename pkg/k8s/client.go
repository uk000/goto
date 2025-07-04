/**
 * Copyright 2025 uk
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

package k8s

import (
	"goto/pkg/global"
	"log"
	"path/filepath"

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
	mapper      *restmapper.DeferredDiscoveryRESTMapper
}

var (
	Client = createK8sClient()
)

func createK8sClient() *K8sClient {
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
	k8sClient.Config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
	if k8sClient.clientset, err = kubernetes.NewForConfig(k8sClient.Config); err != nil {
		log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
		return nil
	}

	if k8sClient.Client, err = dynamic.NewForConfig(k8sClient.Config); err != nil {
		log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
		return nil
	}
	if k8sClient.TypedClient, err = typedclient.NewForConfig(k8sClient.Config); err != nil {
		log.Printf("K8s: Failed to load kube client with error: %s\n", err.Error())
		return nil
	}
	k8sClient.dClient, err = discovery.NewDiscoveryClientForConfig(k8sClient.Config)
	if err != nil {
		log.Printf("K8s: Failed to load discovery client with error: %s\n", err.Error())
		return nil
	}
	k8sClient.mapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(k8sClient.dClient))
	return k8sClient
}
