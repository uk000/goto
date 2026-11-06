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
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ─── Route dispatchers ────────────────────────────────────────────────────────

// handleCollection dispatches collection-level operations (list / create / deleteCollection).
// It logs the full request (method, URI, headers, body) via util.PrintRequest before dispatch.
func handleCollection(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	group, version, namespace, resource := extractVars(r)
	msg := fmt.Sprintf("[K8sMockServer] %s collection %s/%s/%s/%s", r.Method, group, version, resource, namespace)
	util.AddLogMessage(msg, r)
	switch r.Method {
	case http.MethodGet:
		doList(w, r, group, version, namespace, resource)
	case http.MethodPost:
		doCreate(w, r, group, version, namespace, resource)
	case http.MethodDelete:
		doDeleteCollection(w, r, group, version, namespace, resource)
	}
}

// handleItem dispatches item-level operations (get / update / patch / delete).
// It logs the full request (method, URI, headers, body) via util.PrintRequest before dispatch.
func handleItem(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	group, version, namespace, resource := extractVars(r)
	name := util.GetStringParamValue(r, "name")
	msg := fmt.Sprintf("[K8sMockServer] %s item %s/%s/%s/%s/%s", r.Method, group, version, resource, namespace, name)
	util.AddLogMessage(msg, r)
	switch r.Method {
	case http.MethodGet:
		doGet(w, r, group, version, namespace, resource, name)
	case http.MethodPut:
		doUpdate(w, r, group, version, namespace, resource, name)
	case http.MethodPatch:
		doPatch(w, r, group, version, namespace, resource, name)
	case http.MethodDelete:
		doDelete(w, r, group, version, namespace, resource, name)
	}
}

// extractVars reads route path parameters populated by gorilla/mux.
// For core-v1 routes the {group} and {version} variables are absent from the path
// template, so we default group="" and version="v1".
func extractVars(r *http.Request) (group, version, namespace, resource string) {
	group = util.GetStringParamValue(r, "group")
	version = util.GetStringParamValue(r, "version")
	if version == "" {
		version = "v1"
	}
	namespace = util.GetStringParamValue(r, "namespace")
	resource = util.GetStringParamValue(r, "resource")
	return
}

// ─── CRUD operations ──────────────────────────────────────────────────────────

// doList returns all items in the collection from the store.
// If the store is empty, sample items are pre-populated on first access.
// Cluster-scoped requests (no namespace in path) return items from all namespaces.
func doList(w http.ResponseWriter, r *http.Request, group, version, namespace, resource string) {
	var items []json.RawMessage
	if namespace != "" {
		items = globalStore.List(group, version, resource, namespace)
	} else {
		items = globalStore.ListAll(group, version, resource)
	}

	// Pre-populate store with samples on first list of a collection that nobody has written to.
	if len(items) == 0 && !globalStore.Touched(group, version, resource, namespace) {
		ns := namespace
		if ns == "" {
			ns = "default"
		}
		if samples := getSampleItems(resource, ns); len(samples) > 0 {
			for _, s := range samples {
				name := extractMetaField(s, "name")
				sampleNS := extractMetaField(s, "namespace")
				if sampleNS == "" {
					sampleNS = ns
				}
				globalStore.Seed(group, version, resource, sampleNS, name, s)
			}
			if namespace != "" {
				items = globalStore.List(group, version, resource, namespace)
			} else {
				items = globalStore.ListAll(group, version, resource)
			}
		}
	}

	if items == nil {
		items = []json.RawMessage{}
	}

	kind := resourceKind(resource)
	gv := apiVersion(group, version)

	resp := map[string]interface{}{
		"apiVersion": gv,
		"kind":       kind + "List",
		"metadata": map[string]interface{}{
			"resourceVersion": globalStore.nextRV(),
			"selfLink":        r.RequestURI,
		},
		"items": items,
	}

	msg := fmt.Sprintf("[K8sMockServer] Listed %d %s items (ns=%q)", len(items), resource, namespace)
	util.AddLogMessage(msg, r)
	writeJSON(w, http.StatusOK, resp)
}

// doGet returns a single resource item from the store.
// Falls back to a sample if the item is absent; returns 404 if no sample exists.
func doGet(w http.ResponseWriter, r *http.Request, group, version, namespace, resource, name string) {
	item, ok := globalStore.Get(group, version, resource, namespace, name)
	if !ok {
		// A collection that a client has written to is what it has: what was deleted from it is not there.
		var sample json.RawMessage
		if !globalStore.Touched(group, version, resource, namespace) {
			sample = getSampleItem(resource, namespace, name)
		}
		if sample == nil {
			util.AddLogMessage(fmt.Sprintf("[K8sMockServer] NotFound %s/%s %q", resource, namespace, name), r)
			writeStatus(w, http.StatusNotFound, "NotFound",
				fmt.Sprintf("%s %q not found", resource, name))
			return
		}
		item = globalStore.Seed(group, version, resource, namespace, name, sample)
	}
	util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Got %s/%s/%s", resource, namespace, name), r)
	writeRaw(w, http.StatusOK, item)
}

// doCreate stores a new resource and returns 201 Created.
// Metadata fields (uid, resourceVersion, creationTimestamp) are injected if absent.
func doCreate(w http.ResponseWriter, r *http.Request, group, version, namespace, resource string) {
	body, err := readBody(r)
	if err != nil {
		util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Create read error: %v", err), r)
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	if len(body) == 0 {
		body = []byte(`{}`)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	uid := uuid.New().String()
	name := extractMetaField(body, "name")
	if name == "" {
		name = resource + "-" + uid[:8]
	}
	ns := extractMetaField(body, "namespace")
	if ns == "" && namespace != "" {
		ns = namespace
	}

	body = patchMetaFields(body, map[string]interface{}{
		"name":              name,
		"namespace":         ns,
		"uid":               uid,
		"creationTimestamp": now,
	})
	body = ensureTypeFields(body, group, version, resource)

	stored := globalStore.Set(group, version, resource, ns, name, body)
	msg := fmt.Sprintf("[K8sMockServer] Created %s/%s/%s", resource, ns, name)
	util.AddLogMessage(msg, r)
	writeRaw(w, http.StatusCreated, stored)
}

// doUpdate replaces an existing resource (PUT semantics).
func doUpdate(w http.ResponseWriter, r *http.Request, group, version, namespace, resource, name string) {
	body, err := readBody(r)
	if err != nil || len(body) == 0 {
		msg := "request body is empty or unreadable"
		if err != nil {
			msg = err.Error()
		}
		util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Update error: %s", msg), r)
		writeStatus(w, http.StatusBadRequest, "BadRequest", msg)
		return
	}
	body = ensureTypeFields(body, group, version, resource)
	stored := globalStore.Set(group, version, resource, namespace, name, body)
	msg := fmt.Sprintf("[K8sMockServer] Updated %s/%s/%s", resource, namespace, name)
	util.AddLogMessage(msg, r)
	writeRaw(w, http.StatusOK, stored)
}

// doPatch applies a partial update to an existing resource (PATCH semantics).
// A JSON merge-patch is applied; strategic-merge and json-patch are accepted and logged
// but treated as merge-patch for simplicity.
func doPatch(w http.ResponseWriter, r *http.Request, group, version, namespace, resource, name string) {
	body, err := readBody(r)
	if err != nil {
		util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Patch read error: %v", err), r)
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}

	contentType := r.Header.Get("Content-Type")
	log.Printf("[K8sMockServer] PATCH %s/%s/%s content-type=%s", resource, namespace, name, contentType)

	current, ok := globalStore.Get(group, version, resource, namespace, name)
	if !ok {
		current = getSampleItem(resource, namespace, name)
		if current == nil {
			util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Patch NotFound %s/%s/%s", resource, namespace, name), r)
			writeStatus(w, http.StatusNotFound, "NotFound",
				fmt.Sprintf("%s %q not found", resource, name))
			return
		}
	}

	merged := jsonMerge(current, body)
	stored := globalStore.Set(group, version, resource, namespace, name, merged)
	util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Patched %s/%s/%s", resource, namespace, name), r)
	writeRaw(w, http.StatusOK, stored)
}

// doDelete removes a single resource and returns a Status object.
func doDelete(w http.ResponseWriter, r *http.Request, group, version, namespace, resource, name string) {
	if !globalStore.Delete(group, version, resource, namespace, name) {
		util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Delete NotFound %s/%s/%s", resource, namespace, name), r)
		writeStatus(w, http.StatusNotFound, "NotFound",
			fmt.Sprintf("%s %q not found", resource, name))
		return
	}
	util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Deleted %s/%s/%s", resource, namespace, name), r)
	writeStatus(w, http.StatusOK, "Success", "")
}

// doDeleteCollection removes all items in a collection and returns a Status object.
func doDeleteCollection(w http.ResponseWriter, r *http.Request, group, version, namespace, resource string) {
	n := globalStore.DeleteCollection(group, version, resource, namespace)
	util.AddLogMessage(fmt.Sprintf("[K8sMockServer] DeleteCollection %s/%s removed %d items", resource, namespace, n), r)
	writeStatus(w, http.StatusOK, "Success", "")
}

// ─── Discovery handlers ───────────────────────────────────────────────────────

// handleAPIVersions serves the /api discovery document listing the core API versions.
func handleAPIVersions(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	util.AddLogMessage("[K8sMockServer] Discovery: /api", r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"kind":       "APIVersions",
		"apiVersion": "v1",
		"versions":   []string{"v1"},
		"serverAddressByClientCIDRs": []map[string]string{
			{"clientCIDR": "0.0.0.0/0", "serverAddress": r.Host},
		},
	})
}

// handleCoreV1Resources serves the /api/v1 discovery document listing the core v1 resources.
func handleCoreV1Resources(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	util.AddLogMessage("[K8sMockServer] Discovery: /api/v1", r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"kind":         "APIResourceList",
		"apiVersion":   "v1",
		"groupVersion": "v1",
		"resources":    coreV1APIResources,
	})
}

// handleAPIGroupList serves the /apis discovery document listing the supported API groups.
func handleAPIGroupList(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	util.AddLogMessage("[K8sMockServer] Discovery: /apis", r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"kind":       "APIGroupList",
		"apiVersion": "v1",
		"groups":     apiGroups,
	})
}

// handleAPIGroupVersions serves the discovery document of one API group, or 404 if the group is unknown.
func handleAPIGroupVersions(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	groupName := util.GetStringParamValue(r, "group")
	util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Discovery: /apis/%s", groupName), r)
	for _, g := range apiGroups {
		if g.Name == groupName {
			writeJSON(w, http.StatusOK, g)
			return
		}
	}
	writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("API group %q not found", groupName))
}

// handleAPIGroupResources serves the resource list of a group/version, which is empty for groups that are not known (e.g. CRDs).
func handleAPIGroupResources(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("K8sMockServer", r)
	groupName := util.GetStringParamValue(r, "group")
	version := util.GetStringParamValue(r, "version")
	gv := groupName + "/" + version
	util.AddLogMessage(fmt.Sprintf("[K8sMockServer] Discovery: /apis/%s", gv), r)
	resources, ok := groupAPIResources[gv]
	if !ok {
		resources = []apiResource{} // return empty list for unknown groups (e.g. CRDs)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"kind":         "APIResourceList",
		"apiVersion":   "v1",
		"groupVersion": gv,
		"resources":    resources,
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// readBody reads and returns the full request body.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

// writeJSON encodes v as indented JSON with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("[K8sMockServer] writeJSON encode error: %v", err)
	}
}

// writeRaw writes a raw JSON blob with the given HTTP status.
func writeRaw(w http.ResponseWriter, status int, data json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		log.Printf("[K8sMockServer] writeRaw error: %v", err)
	}
}

// writeStatus writes a Kubernetes Status object. The status of one is Success or Failure, which is what clients tell an
// error by; what is given for an error (NotFound, BadRequest) is the reason of it.
func writeStatus(w http.ResponseWriter, code int, reason, message string) {
	status := metav1.StatusSuccess
	if code >= http.StatusBadRequest {
		status = metav1.StatusFailure
	} else {
		reason = ""
	}
	writeJSON(w, code, map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Status",
		"status":     status,
		"message":    message,
		"reason":     reason,
		"code":       code,
	})
}

// apiVersion constructs the apiVersion string ("apps/v1", "v1", etc.)
func apiVersion(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

// resourceKind converts a plural resource name to its Kind ("pods" → "Pod").
// Falls back to capitalising the first letter and stripping a trailing 's'.
func resourceKind(resource string) string {
	if info, ok := resourceInfoByPlural[resource]; ok {
		return info.Kind
	}
	if len(resource) == 0 {
		return resource
	}
	k := string(resource[0]-32) + resource[1:]
	if len(k) > 1 && k[len(k)-1] == 's' {
		k = k[:len(k)-1]
	}
	return k
}

// ensureTypeFields injects apiVersion and kind into a JSON object if absent.
func ensureTypeFields(data json.RawMessage, group, version, resource string) json.RawMessage {
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}
	if obj["apiVersion"] == nil || obj["apiVersion"] == "" {
		obj["apiVersion"] = apiVersion(group, version)
	}
	if obj["kind"] == nil || obj["kind"] == "" {
		obj["kind"] = resourceKind(resource)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

// jsonMerge performs a recursive JSON map merge of patch onto base.
// Array fields are replaced (not merged). A null value in the patch deletes the key.
func jsonMerge(base, patch json.RawMessage) json.RawMessage {
	var baseObj, patchObj map[string]interface{}
	if err := json.Unmarshal(base, &baseObj); err != nil {
		return patch
	}
	if err := json.Unmarshal(patch, &patchObj); err != nil {
		return base
	}
	merged := mergeMap(baseObj, patchObj)
	out, err := json.Marshal(merged)
	if err != nil {
		return base
	}
	return out
}

// mergeMap recursively merges patch into a copy of base per JSON merge-patch rules, where a null value deletes the key.
func mergeMap(base, patch map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(result, k) // JSON merge-patch: null means delete
			continue
		}
		if patchMap, ok := v.(map[string]interface{}); ok {
			if baseMap, ok := result[k].(map[string]interface{}); ok {
				result[k] = mergeMap(baseMap, patchMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}
