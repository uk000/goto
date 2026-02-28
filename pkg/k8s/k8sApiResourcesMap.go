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
	k8sClient "goto/pkg/k8s/client"
	"log"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type K8sApiResource struct {
	ShortName  string   `json:"shortName"`
	Version    string   `json:"version"`
	Kind       string   `json:"kind"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs"`
	Group      string   `json:"group"`
	OtherName  string   `json:"otherName"`
	GVK        string   `json:"gvk"`
}

const (
	KindNamespace = "Namespace"
)

type K8sResource struct {
	Resource  *unstructured.Unstructured `json:"resource"`
	GVK       *schema.GroupVersionKind   `json:"gvk"`
	Namespace string                     `json:"namespace"`
	Name      string                     `json:"name"`
}

func GetGVRMapping(gvk *schema.GroupVersionKind) (*meta.RESTMapping, error) {
	gvr, err := k8sClient.Client.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		log.Printf("Failed to covert GVK to GVR with error: %s\n", err.Error())
		return nil, err
	}
	return gvr, nil
}

func GetResourceInterface(gvk *schema.GroupVersionKind, namespace string) (dynamic.ResourceInterface, *meta.RESTMapping, error) {
	gvr, err := GetGVRMapping(gvk)
	if err != nil {
		return nil, nil, err
	}
	if gvr.Scope.Name() == meta.RESTScopeNameNamespace {
		return k8sClient.Client.Client.Resource(gvr.Resource).Namespace(namespace), gvr, nil
	} else {
		return k8sClient.Client.Client.Resource(gvr.Resource), gvr, nil
	}
}

var (
	K8sApiResourcesMap = func() map[string]*K8sApiResource {
		m := map[string]*K8sApiResource{
			"authorizationpolicies": &K8sApiResource{
				ShortName:  "ap",
				Version:    "security.istio.io/v1",
				Kind:       "AuthorizationPolicy",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "security.istio.io/v1/AuthorizationPolicy",
			},
			"configmaps": &K8sApiResource{
				ShortName:  "cm",
				Version:    "v1",
				Kind:       "ConfigMap",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/ConfigMap",
			},
			"cronjobs": &K8sApiResource{
				ShortName:  "cj",
				Version:    "batch/v1",
				Kind:       "CronJob",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "cron",
				GVK:        "batch/v1/CronJob",
			},
			"daemonsets": &K8sApiResource{
				ShortName:  "ds",
				Version:    "apps/v1",
				Kind:       "DaemonSet",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "daemon",
				GVK:        "apps/v1/DaemonSet",
			},
			"destinationrules": &K8sApiResource{
				ShortName:  "dr",
				Version:    "networking.istio.io/v1",
				Kind:       "DestinationRule",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "dest",
				GVK:        "networking.istio.io/v1/DestinationRule",
			},
			"deployments": &K8sApiResource{
				ShortName:  "deploy",
				Version:    "apps/v1",
				Kind:       "Deployment",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "dep",
				GVK:        "apps/v1/Deployment",
			},
			"endpoints": &K8sApiResource{
				ShortName:  "ep",
				Version:    "v1",
				Kind:       "Endpoints",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Endpoints",
			},
			"envoyfilters": &K8sApiResource{
				ShortName:  "",
				Version:    "networking.istio.io/v1alpha3",
				Kind:       "EnvoyFilter",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "networking.istio.io/v1alpha3/EnvoyFilter",
			},
			"events": &K8sApiResource{
				ShortName:  "ev",
				Version:    "v1",
				Kind:       "Event",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Event",
			},
			"gateways": &K8sApiResource{
				ShortName:  "gw",
				Version:    "gateway.networking.k8s.io/v1",
				Kind:       "Gateway",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "gateway.networking.k8s.io/v1/Gateway",
			},
			"grpcroutes": &K8sApiResource{
				ShortName:  "",
				Version:    "gateway.networking.k8s.io/v1",
				Kind:       "GRPCRoute",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "gateway.networking.k8s.io/v1/GRPCRoute",
			},
			"horizontalpodautoscalers": &K8sApiResource{
				ShortName:  "hpa",
				Version:    "autoscaling/v2",
				Kind:       "HorizontalPodAutoscaler",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "autoscaling/v2/HorizontalPodAutoscaler",
			},
			"httproutes": &K8sApiResource{
				ShortName:  "",
				Version:    "gateway.networking.k8s.io/v1",
				Kind:       "HTTPRoute",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "gateway.networking.k8s.io/v1/HTTPRoute",
			},
			"ingresses": &K8sApiResource{
				ShortName:  "ing",
				Version:    "networking.k8s.io/v1",
				Kind:       "Ingress",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "networking.k8s.io/v1/Ingress",
			},
			"jobs": &K8sApiResource{
				ShortName:  "",
				Version:    "batch/v1",
				Kind:       "Job",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "batch/v1/Job",
			},
			"namespaces": &K8sApiResource{
				ShortName:  "ns",
				Version:    "v1",
				Kind:       "Namespace",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Namespace",
			},
			"networkpolicies": &K8sApiResource{
				ShortName:  "netpol",
				Version:    "networking.k8s.io/v1",
				Kind:       "NetworkPolicy",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "networking.k8s.io/v1/NetworkPolicy",
			},
			"nodes": &K8sApiResource{
				ShortName:  "no",
				Version:    "v1",
				Kind:       "Node",
				Namespaced: false,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Node",
			},
			"peerauthentications": &K8sApiResource{
				ShortName:  "pa",
				Version:    "security.istio.io/v1",
				Kind:       "PeerAuthentication",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "security.istio.io/v1/PeerAuthentication",
			},
			"persistentvolumes": &K8sApiResource{
				ShortName:  "pv",
				Version:    "v1",
				Kind:       "PersistentVolume",
				Namespaced: false,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/PersistentVolume",
			},
			"pods": &K8sApiResource{
				ShortName:  "po",
				Version:    "v1",
				Kind:       "Pod",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Pod",
			},
			"poddisruptionbudgets": &K8sApiResource{
				ShortName:  "pdb",
				Version:    "policy/v1",
				Kind:       "PodDisruptionBudget",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "policy/v1/PodDisruptionBudget",
			},
			"replicasets": &K8sApiResource{
				ShortName:  "rs",
				Version:    "apps/v1",
				Kind:       "ReplicaSet",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "replica",
				GVK:        "apps/v1/ReplicaSet",
			},
			"requestauthentications": &K8sApiResource{
				ShortName:  "ra",
				Version:    "security.istio.io/v1",
				Kind:       "RequestAuthentication",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "security.istio.io/v1/RequestAuthentication",
			},
			"roles": &K8sApiResource{
				ShortName:  "",
				Version:    "rbac.authorization.k8s.io/v1",
				Kind:       "Role",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "rbac.authorization.k8s.io/v1/Role",
			},
			"rolebindings": &K8sApiResource{
				ShortName:  "rb",
				Version:    "rbac.authorization.k8s.io/v1",
				Kind:       "RoleBinding",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "rbac.authorization.k8s.io/v1/RoleBinding",
			},
			"secrets": &K8sApiResource{
				ShortName:  "",
				Version:    "v1",
				Kind:       "Secret",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Secret",
			},
			"serviceaccounts": &K8sApiResource{
				ShortName:  "sa",
				Version:    "v1",
				Kind:       "ServiceAccount",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/ServiceAccount",
			},
			"serviceentries": &K8sApiResource{
				ShortName:  "se",
				Version:    "networking.istio.io/v1",
				Kind:       "ServiceEntry",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "gateway.networking.k8s.io/v1/Gateway",
			},
			"services": &K8sApiResource{
				ShortName:  "svc",
				Version:    "v1",
				Kind:       "Service",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "v1/Service",
			},
			"sidecars": &K8sApiResource{
				ShortName:  "sc",
				Version:    "networking.istio.io/v1",
				Kind:       "Sidecar",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "networking.istio.io/v1/Sidecar",
			},
			"statefulsets": &K8sApiResource{
				ShortName:  "sts",
				Version:    "apps/v1",
				Kind:       "StatefulSet",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "stateful",
				GVK:        "apps/v1/StatefulSet",
			},
			"storageclasses": &K8sApiResource{
				ShortName:  "sc",
				Version:    "storage.k8s.io/v1",
				Kind:       "StorageClass",
				Namespaced: false,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "storage.k8s.io/v1/StorageClass",
			},
			"telemetries": &K8sApiResource{
				ShortName:  "telemetry",
				Version:    "telemetry.istio.io/v1",
				Kind:       "Telemetry",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "telemetry.istio.io/v1/Telemetry",
			},
			"virtualservices": &K8sApiResource{
				ShortName:  "vs",
				Version:    "networking.istio.io/v1",
				Kind:       "VirtualService",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "virtual",
				GVK:        "networking.istio.io/v1/VirtualService",
			},
			"volumeattachments": &K8sApiResource{
				ShortName:  "",
				Version:    "storage.k8s.io/v1",
				Kind:       "VolumeAttachment",
				Namespaced: false,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "storage.k8s.io/v1/VolumeAttachment",
			},
			"wasmplugins": &K8sApiResource{
				ShortName:  "",
				Version:    "extensions.istio.io/v1alpha1",
				Kind:       "WasmPlugin",
				Namespaced: true,
				Verbs:      []string{"delete", "deletecollection", "get", "list", "patch", "create", "update", "watch"},
				OtherName:  "wasm",
				GVK:        "extensions.istio.io/v1alpha1/WasmPlugin",
			},
			"workloadentries": &K8sApiResource{
				ShortName:  "we",
				Version:    "networking.istio.io/v1",
				Kind:       "WorkloadEntry",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "networking.istio.io/v1/WorkloadEntry",
			},
			"workloadgroups": &K8sApiResource{
				ShortName:  "wg",
				Version:    "networking.istio.io/v1",
				Kind:       "WorkloadGroup",
				Namespaced: true,
				Verbs:      []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"},
				OtherName:  "",
				GVK:        "networking.istio.io/v1/WorkloadGroup",
			},
		}
		for _, resource := range m {
			if resource.ShortName != "" {
				m[strings.ToLower(resource.ShortName)] = resource
			}
			if resource.OtherName != "" {
				m[strings.ToLower(resource.OtherName)] = resource
			}
			if resource.Kind != "" {
				m[strings.ToLower(resource.Kind)] = resource
			}
		}
		return m
	}()
)
