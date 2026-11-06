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

package apiserver

import (
	"encoding/json"
	"fmt"
)

// getSampleItem returns a single sample resource object for the given resource type.
// Returns nil if no sample is defined for that resource.
func getSampleItem(resource, namespace, name string) json.RawMessage {
	if name == "" {
		name = "sample-" + resource[:minLen(len(resource), 8)]
	}
	if namespace == "" {
		namespace = "default"
	}
	builder, ok := sampleBuilders[resource]
	if !ok {
		return nil
	}
	return builder(namespace, name)
}

// getSampleItems returns a slice of sample items to pre-populate a list.
func getSampleItems(resource, namespace string) []json.RawMessage {
	builder, ok := sampleBuilders[resource]
	if !ok {
		return nil
	}
	name1 := "sample-" + resource[:minLen(len(resource), 6)] + "-1"
	name2 := "sample-" + resource[:minLen(len(resource), 6)] + "-2"
	return []json.RawMessage{
		builder(namespace, name1),
		builder(namespace, name2),
	}
}

// minLen returns the smaller of two ints.
func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// sampleBuilders maps resource plural name → a function that builds a sample JSON object.
var sampleBuilders = map[string]func(namespace, name string) json.RawMessage{
	"pods":                     samplePod,
	"services":                 sampleService,
	"endpoints":                sampleEndpoints,
	"configmaps":               sampleConfigMap,
	"secrets":                  sampleSecret,
	"namespaces":               sampleNamespace,
	"nodes":                    sampleNode,
	"persistentvolumes":        samplePersistentVolume,
	"persistentvolumeclaims":   samplePVC,
	"serviceaccounts":          sampleServiceAccount,
	"deployments":              sampleDeployment,
	"replicasets":              sampleReplicaSet,
	"daemonsets":               sampleDaemonSet,
	"statefulsets":             sampleStatefulSet,
	"jobs":                     sampleJob,
	"cronjobs":                 sampleCronJob,
	"ingresses":                sampleIngress,
	"networkpolicies":          sampleNetworkPolicy,
	"storageclasses":           sampleStorageClass,
	"horizontalpodautoscalers": sampleHPA,
	"poddisruptionbudgets":     samplePDB,
	"roles":                    sampleRole,
	"rolebindings":             sampleRoleBinding,
	"clusterroles":             sampleClusterRole,
	"clusterrolebindings":      sampleClusterRoleBinding,
	"volumeattachments":        sampleVolumeAttachment,
	"virtualservices":          sampleVirtualService,
	"destinationrules":         sampleDestinationRule,
	"gateways":                 sampleGateway,
}

// ─── Sample builders ──────────────────────────────────────────────────────────

// samplePod builds a sample running Pod with one nginx container.
func samplePod(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": meta(name, namespace, map[string]string{
			"app": name,
		}),
		"spec": map[string]interface{}{
			"containers": []map[string]interface{}{
				{
					"name":  "app",
					"image": "nginx:1.25",
					"ports": []map[string]interface{}{
						{"containerPort": 80, "protocol": "TCP"},
					},
					"resources": map[string]interface{}{
						"requests": map[string]string{"cpu": "100m", "memory": "128Mi"},
						"limits":   map[string]string{"cpu": "500m", "memory": "256Mi"},
					},
				},
			},
			"restartPolicy": "Always",
			"dnsPolicy":     "ClusterFirst",
		},
		"status": map[string]interface{}{
			"phase":  "Running",
			"podIP":  "10.244.0.10",
			"hostIP": "192.168.1.1",
			"conditions": []map[string]interface{}{
				{"type": "Ready", "status": "True"},
				{"type": "ContainersReady", "status": "True"},
			},
			"containerStatuses": []map[string]interface{}{
				{
					"name":         "app",
					"image":        "nginx:1.25",
					"ready":        true,
					"restartCount": 0,
					"state":        map[string]interface{}{"running": map[string]string{"startedAt": "2024-01-01T00:00:00Z"}},
				},
			},
		},
	})
}

// sampleService builds a sample ClusterIP Service that selects the pods by the app label.
func sampleService(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   meta(name, namespace, map[string]string{"app": name}),
		"spec": map[string]interface{}{
			"selector":  map[string]string{"app": name},
			"type":      "ClusterIP",
			"clusterIP": "10.96.0.10",
			"ports": []map[string]interface{}{
				{"port": 80, "targetPort": 80, "protocol": "TCP", "name": "http"},
			},
		},
		"status": map[string]interface{}{
			"loadBalancer": map[string]interface{}{},
		},
	})
}

// sampleEndpoints builds a sample Endpoints object with one address and port.
func sampleEndpoints(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Endpoints",
		"metadata":   meta(name, namespace, nil),
		"subsets": []map[string]interface{}{
			{
				"addresses": []map[string]interface{}{
					{"ip": "10.244.0.10"},
				},
				"ports": []map[string]interface{}{
					{"port": 80, "protocol": "TCP"},
				},
			},
		},
	})
}

// sampleConfigMap builds a sample ConfigMap with a couple of config files as data.
func sampleConfigMap(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   meta(name, namespace, nil),
		"data": map[string]string{
			"config.yaml": "key: value\nenv: production\n",
			"app.conf":    "[server]\nport=8080\n",
		},
	})
}

// sampleSecret builds a sample Opaque Secret with base64-encoded username and password.
func sampleSecret(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   meta(name, namespace, nil),
		"type":       "Opaque",
		"data": map[string]string{
			"username": "YWRtaW4=",     // base64("admin")
			"password": "cGFzc3dvcmQ=", // base64("password")
		},
	})
}

// sampleNamespace builds a sample cluster-scoped Namespace in the Active phase.
func sampleNamespace(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   meta(name, "", map[string]string{"kubernetes.io/metadata.name": name}),
		"spec": map[string]interface{}{
			"finalizers": []string{"kubernetes"},
		},
		"status": map[string]interface{}{
			"phase": "Active",
		},
	})
}

// sampleNode builds a sample cluster-scoped Ready Node with capacity, addresses and node info.
func sampleNode(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": meta(name, "", map[string]string{
			"kubernetes.io/hostname":           name,
			"kubernetes.io/os":                 "linux",
			"kubernetes.io/arch":               "amd64",
			"node.kubernetes.io/instance-type": "n1-standard-4",
		}),
		"spec": map[string]interface{}{
			"podCIDR":  "10.244.0.0/24",
			"podCIDRs": []string{"10.244.0.0/24"},
		},
		"status": map[string]interface{}{
			"conditions": []map[string]interface{}{
				{"type": "Ready", "status": "True", "reason": "KubeletReady", "message": "kubelet is posting ready status"},
			},
			"addresses": []map[string]interface{}{
				{"type": "InternalIP", "address": "192.168.1.1"},
				{"type": "Hostname", "address": name},
			},
			"capacity": map[string]string{
				"cpu":               "4",
				"memory":            "16Gi",
				"pods":              "110",
				"ephemeral-storage": "100Gi",
			},
			"allocatable": map[string]string{
				"cpu":               "3800m",
				"memory":            "15Gi",
				"pods":              "110",
				"ephemeral-storage": "95Gi",
			},
			"nodeInfo": map[string]string{
				"machineID":               "mock-machine-id",
				"systemUUID":              "mock-system-uuid",
				"bootID":                  "mock-boot-id",
				"kernelVersion":           "5.15.0-76-generic",
				"osImage":                 "Ubuntu 22.04.2 LTS",
				"containerRuntimeVersion": "containerd://1.7.2",
				"kubeletVersion":          "v1.30.0",
				"kubeProxyVersion":        "v1.30.0",
				"operatingSystem":         "linux",
				"architecture":            "amd64",
			},
		},
	})
}

// samplePersistentVolume builds a sample cluster-scoped 10Gi hostPath PersistentVolume.
func samplePersistentVolume(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata":   meta(name, "", nil),
		"spec": map[string]interface{}{
			"capacity":                      map[string]string{"storage": "10Gi"},
			"accessModes":                   []string{"ReadWriteOnce"},
			"persistentVolumeReclaimPolicy": "Delete",
			"storageClassName":              "standard",
			"hostPath":                      map[string]string{"path": "/data/" + name},
		},
		"status": map[string]interface{}{
			"phase": "Available",
		},
	})
}

// samplePVC builds a sample Bound 5Gi PersistentVolumeClaim.
func samplePVC(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"accessModes":      []string{"ReadWriteOnce"},
			"storageClassName": "standard",
			"resources": map[string]interface{}{
				"requests": map[string]string{"storage": "5Gi"},
			},
		},
		"status": map[string]interface{}{
			"phase":       "Bound",
			"accessModes": []string{"ReadWriteOnce"},
			"capacity":    map[string]string{"storage": "5Gi"},
		},
	})
}

// sampleServiceAccount builds a sample ServiceAccount with a token secret reference.
func sampleServiceAccount(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata":   meta(name, namespace, nil),
		"secrets":    []map[string]string{{"name": name + "-token"}},
	})
}

// sampleDeployment builds a sample two-replica nginx Deployment with a rolling update strategy.
func sampleDeployment(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   meta(name, namespace, map[string]string{"app": name}),
		"spec": map[string]interface{}{
			"replicas": 2,
			"selector": map[string]interface{}{
				"matchLabels": map[string]string{"app": name},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{"app": name},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{
							"name":  "app",
							"image": "nginx:1.25",
							"ports": []map[string]interface{}{
								{"containerPort": 80},
							},
						},
					},
				},
			},
			"strategy": map[string]interface{}{
				"type": "RollingUpdate",
				"rollingUpdate": map[string]interface{}{
					"maxSurge":       1,
					"maxUnavailable": 0,
				},
			},
		},
		"status": map[string]interface{}{
			"replicas":           2,
			"readyReplicas":      2,
			"availableReplicas":  2,
			"updatedReplicas":    2,
			"observedGeneration": 1,
		},
	})
}

// sampleReplicaSet builds a sample two-replica nginx ReplicaSet.
func sampleReplicaSet(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "ReplicaSet",
		"metadata":   meta(name, namespace, map[string]string{"app": name}),
		"spec": map[string]interface{}{
			"replicas": 2,
			"selector": map[string]interface{}{
				"matchLabels": map[string]string{"app": name},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{"app": name},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{"name": "app", "image": "nginx:1.25"},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"replicas":      2,
			"readyReplicas": 2,
		},
	})
}

// sampleDaemonSet builds a sample nginx DaemonSet that tolerates all taints.
func sampleDaemonSet(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "DaemonSet",
		"metadata":   meta(name, namespace, map[string]string{"app": name}),
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": map[string]string{"app": name},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{"app": name},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{"name": "app", "image": "nginx:1.25"},
					},
					"tolerations": []map[string]interface{}{
						{"operator": "Exists"},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"desiredNumberScheduled": 3,
			"numberReady":            3,
			"numberAvailable":        3,
		},
	})
}

// sampleStatefulSet builds a sample three-replica nginx StatefulSet.
func sampleStatefulSet(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
		"metadata":   meta(name, namespace, map[string]string{"app": name}),
		"spec": map[string]interface{}{
			"replicas":    3,
			"serviceName": name,
			"selector": map[string]interface{}{
				"matchLabels": map[string]string{"app": name},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{"app": name},
				},
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{"name": "app", "image": "nginx:1.25"},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"replicas":      3,
			"readyReplicas": 3,
		},
	})
}

// sampleJob builds a sample completed busybox Job.
func sampleJob(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"completions":  1,
			"parallelism":  1,
			"backoffLimit": 3,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{"name": "job", "image": "busybox:1.36", "command": []string{"sh", "-c", "echo hello"}},
					},
					"restartPolicy": "Never",
				},
			},
		},
		"status": map[string]interface{}{
			"succeeded":      1,
			"completionTime": "2024-01-01T00:05:00Z",
			"startTime":      "2024-01-01T00:00:00Z",
		},
	})
}

// sampleCronJob builds a sample hourly busybox CronJob.
func sampleCronJob(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "batch/v1",
		"kind":       "CronJob",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"schedule":          "0 * * * *",
			"concurrencyPolicy": "Allow",
			"jobTemplate": map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []map[string]interface{}{
								{"name": "cron", "image": "busybox:1.36", "command": []string{"sh", "-c", "echo scheduled"}},
							},
							"restartPolicy": "OnFailure",
						},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"lastScheduleTime": "2024-01-01T00:00:00Z",
		},
	})
}

// sampleIngress builds a sample nginx Ingress that routes name.example.com to the service of the same name.
func sampleIngress(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   meta(name, namespace, map[string]string{"kubernetes.io/ingress.class": "nginx"}),
		"spec": map[string]interface{}{
			"ingressClassName": "nginx",
			"rules": []map[string]interface{}{
				{
					"host": name + ".example.com",
					"http": map[string]interface{}{
						"paths": []map[string]interface{}{
							{
								"path":     "/",
								"pathType": "Prefix",
								"backend": map[string]interface{}{
									"service": map[string]interface{}{
										"name": name,
										"port": map[string]interface{}{"number": 80},
									},
								},
							},
						},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"loadBalancer": map[string]interface{}{
				"ingress": []map[string]string{{"ip": "10.0.0.1"}},
			},
		},
	})
}

// sampleNetworkPolicy builds a sample NetworkPolicy that allows ingress to the app from frontend pods.
func sampleNetworkPolicy(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"podSelector": map[string]interface{}{
				"matchLabels": map[string]string{"app": name},
			},
			"policyTypes": []string{"Ingress", "Egress"},
			"ingress": []map[string]interface{}{
				{
					"from": []map[string]interface{}{
						{"podSelector": map[string]interface{}{"matchLabels": map[string]string{"role": "frontend"}}},
					},
					"ports": []map[string]interface{}{
						{"protocol": "TCP", "port": 80},
					},
				},
			},
		},
	})
}

// sampleStorageClass builds a sample cluster-scoped StorageClass with no provisioner.
func sampleStorageClass(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion":        "storage.k8s.io/v1",
		"kind":              "StorageClass",
		"metadata":          meta(name, "", nil),
		"provisioner":       "kubernetes.io/no-provisioner",
		"reclaimPolicy":     "Delete",
		"volumeBindingMode": "WaitForFirstConsumer",
	})
}

// sampleHPA builds a sample HorizontalPodAutoscaler that scales a Deployment on 80% CPU.
func sampleHPA(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"scaleTargetRef": map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       name,
			},
			"minReplicas": 1,
			"maxReplicas": 10,
			"metrics": []map[string]interface{}{
				{
					"type": "Resource",
					"resource": map[string]interface{}{
						"name": "cpu",
						"target": map[string]interface{}{
							"type":               "Utilization",
							"averageUtilization": 80,
						},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"currentReplicas": 2,
			"desiredReplicas": 2,
		},
	})
}

// samplePDB builds a sample PodDisruptionBudget with minAvailable of 1.
func samplePDB(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "policy/v1",
		"kind":       "PodDisruptionBudget",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"minAvailable": 1,
			"selector": map[string]interface{}{
				"matchLabels": map[string]string{"app": name},
			},
		},
		"status": map[string]interface{}{
			"disruptionsAllowed": 1,
			"currentHealthy":     2,
			"desiredHealthy":     1,
			"expectedPods":       2,
		},
	})
}

// sampleRole builds a sample namespaced Role with read access to pods and services.
func sampleRole(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "Role",
		"metadata":   meta(name, namespace, nil),
		"rules": []map[string]interface{}{
			{
				"apiGroups": []string{""},
				"resources": []string{"pods", "services"},
				"verbs":     []string{"get", "list", "watch"},
			},
		},
	})
}

// sampleRoleBinding builds a sample RoleBinding that binds the default service account to the Role of the same name.
func sampleRoleBinding(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "RoleBinding",
		"metadata":   meta(name, namespace, nil),
		"subjects": []map[string]interface{}{
			{"kind": "ServiceAccount", "name": "default", "namespace": namespace},
		},
		"roleRef": map[string]interface{}{
			"kind":     "Role",
			"name":     name,
			"apiGroup": "rbac.authorization.k8s.io",
		},
	})
}

// sampleClusterRole builds a sample ClusterRole with read access to core resources and read-write on deployments.
func sampleClusterRole(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata":   meta(name, "", nil),
		"rules": []map[string]interface{}{
			{
				"apiGroups": []string{""},
				"resources": []string{"pods", "services", "nodes"},
				"verbs":     []string{"get", "list", "watch"},
			},
			{
				"apiGroups": []string{"apps"},
				"resources": []string{"deployments"},
				"verbs":     readWriteVerbs,
			},
		},
	})
}

// sampleClusterRoleBinding builds a sample ClusterRoleBinding of the kube-system default service account to the ClusterRole of the same name.
func sampleClusterRoleBinding(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleBinding",
		"metadata":   meta(name, "", nil),
		"subjects": []map[string]interface{}{
			{"kind": "ServiceAccount", "name": "default", "namespace": "kube-system"},
		},
		"roleRef": map[string]interface{}{
			"kind":     "ClusterRole",
			"name":     name,
			"apiGroup": "rbac.authorization.k8s.io",
		},
	})
}

// sampleVolumeAttachment builds a sample attached VolumeAttachment.
func sampleVolumeAttachment(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "storage.k8s.io/v1",
		"kind":       "VolumeAttachment",
		"metadata":   meta(name, "", nil),
		"spec": map[string]interface{}{
			"attacher": "pd.csi.storage.gke.io",
			"nodeName": "sample-node-1",
			"source":   map[string]string{"persistentVolumeName": "sample-pv"},
		},
		"status": map[string]interface{}{
			"attached": true,
		},
	})
}

// sampleVirtualService builds a sample Istio VirtualService that routes all traffic for name.example.com to the service of the same name.
func sampleVirtualService(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "VirtualService",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"hosts": []string{name + ".example.com"},
			"http": []map[string]interface{}{
				{
					"match": []map[string]interface{}{
						{"uri": map[string]string{"prefix": "/"}},
					},
					"route": []map[string]interface{}{
						{
							"destination": map[string]interface{}{
								"host": name,
								"port": map[string]int{"number": 80},
							},
							"weight": 100,
						},
					},
				},
			},
		},
	})
}

// sampleDestinationRule builds a sample Istio DestinationRule with connection pool limits and v1/v2 subsets.
func sampleDestinationRule(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "DestinationRule",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"host": name,
			"trafficPolicy": map[string]interface{}{
				"connectionPool": map[string]interface{}{
					"tcp":  map[string]interface{}{"maxConnections": 100},
					"http": map[string]interface{}{"http2MaxRequests": 1000},
				},
				"loadBalancer": map[string]interface{}{
					"simple": "ROUND_ROBIN",
				},
			},
			"subsets": []map[string]interface{}{
				{
					"name":   "v1",
					"labels": map[string]string{"version": "v1"},
				},
				{
					"name":   "v2",
					"labels": map[string]string{"version": "v2"},
				},
			},
		},
	})
}

// sampleGateway builds a sample Istio-class Gateway API Gateway with an HTTP listener.
func sampleGateway(namespace, name string) json.RawMessage {
	return marshal(map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   meta(name, namespace, nil),
		"spec": map[string]interface{}{
			"gatewayClassName": "istio",
			"listeners": []map[string]interface{}{
				{
					"name":     "http",
					"port":     80,
					"protocol": "HTTP",
					"allowedRoutes": map[string]interface{}{
						"namespaces": map[string]interface{}{"from": "All"},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"conditions": []map[string]interface{}{
				{"type": "Accepted", "status": "True"},
			},
		},
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// meta builds a standard K8s metadata object.
func meta(name, namespace string, labels map[string]string) map[string]interface{} {
	m := map[string]interface{}{
		"name":              name,
		"uid":               "00000000-0000-0000-0000-" + padHex(name),
		"resourceVersion":   "1",
		"creationTimestamp": "2024-01-01T00:00:00Z",
		"generation":        1,
	}
	if namespace != "" {
		m["namespace"] = namespace
	}
	if len(labels) > 0 {
		m["labels"] = labels
	}
	return m
}

// padHex turns a string into a 12-char hex-looking suffix (for deterministic UIDs in samples).
func padHex(s string) string {
	h := make([]byte, 12)
	for i, c := range []byte(s) {
		h[i%12] ^= c
	}
	return fmt.Sprintf("%x", h)
}

// marshal encodes v to JSON, panicking on error (only called with static test data).
func marshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("k8s mock: marshal sample: " + err.Error())
	}
	return b
}
