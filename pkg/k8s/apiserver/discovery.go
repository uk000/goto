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

// This file contains discovery data: the resource registry, API group list,
// and per-group resource lists used by /api, /api/v1, /apis, /apis/{g}/{v}.
// These responses enable client-go's REST mapper to resolve resource types.

// ─── Types ────────────────────────────────────────────────────────────────────

type apiResource struct {
	Name         string   `json:"name"`
	SingularName string   `json:"singularName"`
	Namespaced   bool     `json:"namespaced"`
	Kind         string   `json:"kind"`
	Verbs        []string `json:"verbs"`
	ShortNames   []string `json:"shortNames,omitempty"`
}

type groupVersion struct {
	GroupVersion string `json:"groupVersion"`
	Version      string `json:"version"`
}

type apiGroup struct {
	Name             string         `json:"name"`
	Versions         []groupVersion `json:"versions"`
	PreferredVersion groupVersion   `json:"preferredVersion"`
}

// resourceInfoEntry is used for kind-lookup by resource plural name.
type resourceInfoEntry struct {
	Kind  string
	Group string
}

// ─── Standard verbs ───────────────────────────────────────────────────────────

var readWriteVerbs = []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"}
var readOnlyVerbs = []string{"get", "list", "watch"}
var noDeleteCollectionVerbs = []string{"create", "delete", "get", "list", "patch", "update", "watch"}

// ─── Resource info by plural name ────────────────────────────────────────────
// Used by resourceKind() in handlers.go.

var resourceInfoByPlural = map[string]resourceInfoEntry{
	// Core v1
	"bindings":               {Kind: "Binding"},
	"componentstatuses":      {Kind: "ComponentStatus"},
	"configmaps":             {Kind: "ConfigMap"},
	"endpoints":              {Kind: "Endpoints"},
	"events":                 {Kind: "Event"},
	"limitranges":            {Kind: "LimitRange"},
	"namespaces":             {Kind: "Namespace"},
	"nodes":                  {Kind: "Node"},
	"persistentvolumeclaims": {Kind: "PersistentVolumeClaim"},
	"persistentvolumes":      {Kind: "PersistentVolume"},
	"pods":                   {Kind: "Pod"},
	"podtemplates":           {Kind: "PodTemplate"},
	"replicationcontrollers": {Kind: "ReplicationController"},
	"resourcequotas":         {Kind: "ResourceQuota"},
	"secrets":                {Kind: "Secret"},
	"serviceaccounts":        {Kind: "ServiceAccount"},
	"services":               {Kind: "Service"},
	// apps/v1
	"controllerrevisions": {Kind: "ControllerRevision", Group: "apps"},
	"daemonsets":          {Kind: "DaemonSet", Group: "apps"},
	"deployments":         {Kind: "Deployment", Group: "apps"},
	"replicasets":         {Kind: "ReplicaSet", Group: "apps"},
	"statefulsets":        {Kind: "StatefulSet", Group: "apps"},
	// autoscaling/v2
	"horizontalpodautoscalers": {Kind: "HorizontalPodAutoscaler", Group: "autoscaling"},
	// batch/v1
	"cronjobs": {Kind: "CronJob", Group: "batch"},
	"jobs":     {Kind: "Job", Group: "batch"},
	// networking.k8s.io/v1
	"ingressclasses":  {Kind: "IngressClass", Group: "networking.k8s.io"},
	"ingresses":       {Kind: "Ingress", Group: "networking.k8s.io"},
	"networkpolicies": {Kind: "NetworkPolicy", Group: "networking.k8s.io"},
	// policy/v1
	"poddisruptionbudgets": {Kind: "PodDisruptionBudget", Group: "policy"},
	// rbac.authorization.k8s.io/v1
	"clusterrolebindings": {Kind: "ClusterRoleBinding", Group: "rbac.authorization.k8s.io"},
	"clusterroles":        {Kind: "ClusterRole", Group: "rbac.authorization.k8s.io"},
	"rolebindings":        {Kind: "RoleBinding", Group: "rbac.authorization.k8s.io"},
	"roles":               {Kind: "Role", Group: "rbac.authorization.k8s.io"},
	// storage.k8s.io/v1
	"csidrivers":           {Kind: "CSIDriver", Group: "storage.k8s.io"},
	"csinodes":             {Kind: "CSINode", Group: "storage.k8s.io"},
	"csistoragecapacities": {Kind: "CSIStorageCapacity", Group: "storage.k8s.io"},
	"storageclasses":       {Kind: "StorageClass", Group: "storage.k8s.io"},
	"volumeattachments":    {Kind: "VolumeAttachment", Group: "storage.k8s.io"},
	// Istio – networking.istio.io/v1
	"destinationrules": {Kind: "DestinationRule", Group: "networking.istio.io"},
	"envoyfilters":     {Kind: "EnvoyFilter", Group: "networking.istio.io"},
	"serviceentries":   {Kind: "ServiceEntry", Group: "networking.istio.io"},
	"sidecars":         {Kind: "Sidecar", Group: "networking.istio.io"},
	"virtualservices":  {Kind: "VirtualService", Group: "networking.istio.io"},
	"workloadentries":  {Kind: "WorkloadEntry", Group: "networking.istio.io"},
	"workloadgroups":   {Kind: "WorkloadGroup", Group: "networking.istio.io"},
	// Istio – security.istio.io/v1
	"authorizationpolicies":  {Kind: "AuthorizationPolicy", Group: "security.istio.io"},
	"peerauthentications":    {Kind: "PeerAuthentication", Group: "security.istio.io"},
	"requestauthentications": {Kind: "RequestAuthentication", Group: "security.istio.io"},
	// Istio – telemetry.istio.io/v1
	"telemetries": {Kind: "Telemetry", Group: "telemetry.istio.io"},
	// Istio – extensions.istio.io/v1alpha1
	"wasmplugins": {Kind: "WasmPlugin", Group: "extensions.istio.io"},
	// Gateway API – gateway.networking.k8s.io/v1
	"gateways":   {Kind: "Gateway", Group: "gateway.networking.k8s.io"},
	"grpcroutes": {Kind: "GRPCRoute", Group: "gateway.networking.k8s.io"},
	"httproutes": {Kind: "HTTPRoute", Group: "gateway.networking.k8s.io"},
}

// ─── Core v1 resource list (/api/v1) ─────────────────────────────────────────

var coreV1APIResources = []apiResource{
	{Name: "bindings", SingularName: "binding", Namespaced: true, Kind: "Binding", Verbs: []string{"create"}},
	{Name: "componentstatuses", SingularName: "componentstatus", Namespaced: false, Kind: "ComponentStatus", Verbs: readOnlyVerbs, ShortNames: []string{"cs"}},
	{Name: "configmaps", SingularName: "configmap", Namespaced: true, Kind: "ConfigMap", Verbs: readWriteVerbs, ShortNames: []string{"cm"}},
	{Name: "endpoints", SingularName: "endpoints", Namespaced: true, Kind: "Endpoints", Verbs: readWriteVerbs, ShortNames: []string{"ep"}},
	{Name: "events", SingularName: "event", Namespaced: true, Kind: "Event", Verbs: readWriteVerbs, ShortNames: []string{"ev"}},
	{Name: "limitranges", SingularName: "limitrange", Namespaced: true, Kind: "LimitRange", Verbs: readWriteVerbs, ShortNames: []string{"limits"}},
	{Name: "namespaces", SingularName: "namespace", Namespaced: false, Kind: "Namespace", Verbs: noDeleteCollectionVerbs, ShortNames: []string{"ns"}},
	{Name: "nodes", SingularName: "node", Namespaced: false, Kind: "Node", Verbs: readWriteVerbs, ShortNames: []string{"no"}},
	{Name: "persistentvolumeclaims", SingularName: "persistentvolumeclaim", Namespaced: true, Kind: "PersistentVolumeClaim", Verbs: readWriteVerbs, ShortNames: []string{"pvc"}},
	{Name: "persistentvolumes", SingularName: "persistentvolume", Namespaced: false, Kind: "PersistentVolume", Verbs: readWriteVerbs, ShortNames: []string{"pv"}},
	{Name: "pods", SingularName: "pod", Namespaced: true, Kind: "Pod", Verbs: readWriteVerbs, ShortNames: []string{"po"}},
	{Name: "podtemplates", SingularName: "podtemplate", Namespaced: true, Kind: "PodTemplate", Verbs: readWriteVerbs},
	{Name: "replicationcontrollers", SingularName: "replicationcontroller", Namespaced: true, Kind: "ReplicationController", Verbs: readWriteVerbs, ShortNames: []string{"rc"}},
	{Name: "resourcequotas", SingularName: "resourcequota", Namespaced: true, Kind: "ResourceQuota", Verbs: readWriteVerbs, ShortNames: []string{"quota"}},
	{Name: "secrets", SingularName: "secret", Namespaced: true, Kind: "Secret", Verbs: readWriteVerbs},
	{Name: "serviceaccounts", SingularName: "serviceaccount", Namespaced: true, Kind: "ServiceAccount", Verbs: readWriteVerbs, ShortNames: []string{"sa"}},
	{Name: "services", SingularName: "service", Namespaced: true, Kind: "Service", Verbs: noDeleteCollectionVerbs, ShortNames: []string{"svc"}},
}

// ─── API groups (/apis) ───────────────────────────────────────────────────────

var apiGroups = []apiGroup{
	{
		Name:             "apps",
		Versions:         []groupVersion{{GroupVersion: "apps/v1", Version: "v1"}},
		PreferredVersion: groupVersion{GroupVersion: "apps/v1", Version: "v1"},
	},
	{
		Name:             "autoscaling",
		Versions:         []groupVersion{{GroupVersion: "autoscaling/v2", Version: "v2"}, {GroupVersion: "autoscaling/v1", Version: "v1"}},
		PreferredVersion: groupVersion{GroupVersion: "autoscaling/v2", Version: "v2"},
	},
	{
		Name:             "batch",
		Versions:         []groupVersion{{GroupVersion: "batch/v1", Version: "v1"}},
		PreferredVersion: groupVersion{GroupVersion: "batch/v1", Version: "v1"},
	},
	{
		Name:             "networking.k8s.io",
		Versions:         []groupVersion{{GroupVersion: "networking.k8s.io/v1", Version: "v1"}},
		PreferredVersion: groupVersion{GroupVersion: "networking.k8s.io/v1", Version: "v1"},
	},
	{
		Name:             "policy",
		Versions:         []groupVersion{{GroupVersion: "policy/v1", Version: "v1"}},
		PreferredVersion: groupVersion{GroupVersion: "policy/v1", Version: "v1"},
	},
	{
		Name: "rbac.authorization.k8s.io",
		Versions: []groupVersion{
			{GroupVersion: "rbac.authorization.k8s.io/v1", Version: "v1"},
		},
		PreferredVersion: groupVersion{GroupVersion: "rbac.authorization.k8s.io/v1", Version: "v1"},
	},
	{
		Name: "storage.k8s.io",
		Versions: []groupVersion{
			{GroupVersion: "storage.k8s.io/v1", Version: "v1"},
		},
		PreferredVersion: groupVersion{GroupVersion: "storage.k8s.io/v1", Version: "v1"},
	},
	{
		Name: "networking.istio.io",
		Versions: []groupVersion{
			{GroupVersion: "networking.istio.io/v1", Version: "v1"},
			{GroupVersion: "networking.istio.io/v1alpha3", Version: "v1alpha3"},
		},
		PreferredVersion: groupVersion{GroupVersion: "networking.istio.io/v1", Version: "v1"},
	},
	{
		Name: "security.istio.io",
		Versions: []groupVersion{
			{GroupVersion: "security.istio.io/v1", Version: "v1"},
		},
		PreferredVersion: groupVersion{GroupVersion: "security.istio.io/v1", Version: "v1"},
	},
	{
		Name: "telemetry.istio.io",
		Versions: []groupVersion{
			{GroupVersion: "telemetry.istio.io/v1", Version: "v1"},
		},
		PreferredVersion: groupVersion{GroupVersion: "telemetry.istio.io/v1", Version: "v1"},
	},
	{
		Name: "extensions.istio.io",
		Versions: []groupVersion{
			{GroupVersion: "extensions.istio.io/v1alpha1", Version: "v1alpha1"},
		},
		PreferredVersion: groupVersion{GroupVersion: "extensions.istio.io/v1alpha1", Version: "v1alpha1"},
	},
	{
		Name: "gateway.networking.k8s.io",
		Versions: []groupVersion{
			{GroupVersion: "gateway.networking.k8s.io/v1", Version: "v1"},
		},
		PreferredVersion: groupVersion{GroupVersion: "gateway.networking.k8s.io/v1", Version: "v1"},
	},
}

// ─── Per-group resource lists (/apis/{group}/{version}) ───────────────────────

// groupAPIResources maps "group/version" → slice of apiResource.
var groupAPIResources = map[string][]apiResource{
	"apps/v1": {
		{Name: "controllerrevisions", SingularName: "controllerrevision", Namespaced: true, Kind: "ControllerRevision", Verbs: readWriteVerbs},
		{Name: "daemonsets", SingularName: "daemonset", Namespaced: true, Kind: "DaemonSet", Verbs: readWriteVerbs, ShortNames: []string{"ds"}},
		{Name: "deployments", SingularName: "deployment", Namespaced: true, Kind: "Deployment", Verbs: readWriteVerbs, ShortNames: []string{"deploy"}},
		{Name: "replicasets", SingularName: "replicaset", Namespaced: true, Kind: "ReplicaSet", Verbs: readWriteVerbs, ShortNames: []string{"rs"}},
		{Name: "statefulsets", SingularName: "statefulset", Namespaced: true, Kind: "StatefulSet", Verbs: readWriteVerbs, ShortNames: []string{"sts"}},
	},
	"autoscaling/v2": {
		{Name: "horizontalpodautoscalers", SingularName: "horizontalpodautoscaler", Namespaced: true, Kind: "HorizontalPodAutoscaler", Verbs: readWriteVerbs, ShortNames: []string{"hpa"}},
	},
	"autoscaling/v1": {
		{Name: "horizontalpodautoscalers", SingularName: "horizontalpodautoscaler", Namespaced: true, Kind: "HorizontalPodAutoscaler", Verbs: readWriteVerbs, ShortNames: []string{"hpa"}},
	},
	"batch/v1": {
		{Name: "cronjobs", SingularName: "cronjob", Namespaced: true, Kind: "CronJob", Verbs: readWriteVerbs, ShortNames: []string{"cj"}},
		{Name: "jobs", SingularName: "job", Namespaced: true, Kind: "Job", Verbs: readWriteVerbs},
	},
	"networking.k8s.io/v1": {
		{Name: "ingressclasses", SingularName: "ingressclass", Namespaced: false, Kind: "IngressClass", Verbs: readWriteVerbs},
		{Name: "ingresses", SingularName: "ingress", Namespaced: true, Kind: "Ingress", Verbs: readWriteVerbs, ShortNames: []string{"ing"}},
		{Name: "networkpolicies", SingularName: "networkpolicy", Namespaced: true, Kind: "NetworkPolicy", Verbs: readWriteVerbs, ShortNames: []string{"netpol"}},
	},
	"policy/v1": {
		{Name: "poddisruptionbudgets", SingularName: "poddisruptionbudget", Namespaced: true, Kind: "PodDisruptionBudget", Verbs: readWriteVerbs, ShortNames: []string{"pdb"}},
	},
	"rbac.authorization.k8s.io/v1": {
		{Name: "clusterrolebindings", SingularName: "clusterrolebinding", Namespaced: false, Kind: "ClusterRoleBinding", Verbs: readWriteVerbs},
		{Name: "clusterroles", SingularName: "clusterrole", Namespaced: false, Kind: "ClusterRole", Verbs: readWriteVerbs},
		{Name: "rolebindings", SingularName: "rolebinding", Namespaced: true, Kind: "RoleBinding", Verbs: readWriteVerbs, ShortNames: []string{"rb"}},
		{Name: "roles", SingularName: "role", Namespaced: true, Kind: "Role", Verbs: readWriteVerbs},
	},
	"storage.k8s.io/v1": {
		{Name: "csidrivers", SingularName: "csidriver", Namespaced: false, Kind: "CSIDriver", Verbs: readWriteVerbs},
		{Name: "csinodes", SingularName: "csinode", Namespaced: false, Kind: "CSINode", Verbs: readWriteVerbs},
		{Name: "csistoragecapacities", SingularName: "csistoragecapacity", Namespaced: true, Kind: "CSIStorageCapacity", Verbs: readWriteVerbs},
		{Name: "storageclasses", SingularName: "storageclass", Namespaced: false, Kind: "StorageClass", Verbs: readWriteVerbs, ShortNames: []string{"sc"}},
		{Name: "volumeattachments", SingularName: "volumeattachment", Namespaced: false, Kind: "VolumeAttachment", Verbs: readWriteVerbs},
	},
	"networking.istio.io/v1": {
		{Name: "destinationrules", SingularName: "destinationrule", Namespaced: true, Kind: "DestinationRule", Verbs: readWriteVerbs, ShortNames: []string{"dr"}},
		{Name: "serviceentries", SingularName: "serviceentry", Namespaced: true, Kind: "ServiceEntry", Verbs: readWriteVerbs, ShortNames: []string{"se"}},
		{Name: "sidecars", SingularName: "sidecar", Namespaced: true, Kind: "Sidecar", Verbs: readWriteVerbs},
		{Name: "virtualservices", SingularName: "virtualservice", Namespaced: true, Kind: "VirtualService", Verbs: readWriteVerbs, ShortNames: []string{"vs"}},
		{Name: "workloadentries", SingularName: "workloadentry", Namespaced: true, Kind: "WorkloadEntry", Verbs: readWriteVerbs, ShortNames: []string{"we"}},
		{Name: "workloadgroups", SingularName: "workloadgroup", Namespaced: true, Kind: "WorkloadGroup", Verbs: readWriteVerbs, ShortNames: []string{"wg"}},
	},
	"networking.istio.io/v1alpha3": {
		{Name: "envoyfilters", SingularName: "envoyfilter", Namespaced: true, Kind: "EnvoyFilter", Verbs: readWriteVerbs},
	},
	"security.istio.io/v1": {
		{Name: "authorizationpolicies", SingularName: "authorizationpolicy", Namespaced: true, Kind: "AuthorizationPolicy", Verbs: readWriteVerbs, ShortNames: []string{"ap"}},
		{Name: "peerauthentications", SingularName: "peerauthentication", Namespaced: true, Kind: "PeerAuthentication", Verbs: readWriteVerbs, ShortNames: []string{"pa"}},
		{Name: "requestauthentications", SingularName: "requestauthentication", Namespaced: true, Kind: "RequestAuthentication", Verbs: readWriteVerbs, ShortNames: []string{"ra"}},
	},
	"telemetry.istio.io/v1": {
		{Name: "telemetries", SingularName: "telemetry", Namespaced: true, Kind: "Telemetry", Verbs: readWriteVerbs},
	},
	"extensions.istio.io/v1alpha1": {
		{Name: "wasmplugins", SingularName: "wasmplugin", Namespaced: true, Kind: "WasmPlugin", Verbs: readWriteVerbs},
	},
	"gateway.networking.k8s.io/v1": {
		{Name: "gateways", SingularName: "gateway", Namespaced: true, Kind: "Gateway", Verbs: readWriteVerbs, ShortNames: []string{"gtw"}},
		{Name: "grpcroutes", SingularName: "grpcroute", Namespaced: true, Kind: "GRPCRoute", Verbs: readWriteVerbs},
		{Name: "httproutes", SingularName: "httproute", Namespaced: true, Kind: "HTTPRoute", Verbs: readWriteVerbs},
	},
}
