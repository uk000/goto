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
	"strings"
	"sync"
	"sync/atomic"
)

// ResourceStore is a thread-safe in-memory store for Kubernetes resource objects.
// Items are keyed by (group, version, resource, namespace) for collections, and
// additionally by name within each collection.
type ResourceStore struct {
	mu    sync.RWMutex
	items map[string]map[string]json.RawMessage // collectionKey → name → raw JSON
	// touched has the collections that a client has written to. A collection that was not is served with samples, and one
	// that was is served with what it has, which may be nothing.
	touched map[string]struct{}
	rv      atomic.Int64 // monotonic resource-version counter
}

// NewResourceStore creates an empty ResourceStore.
func NewResourceStore() *ResourceStore {
	return &ResourceStore{
		items:   make(map[string]map[string]json.RawMessage),
		touched: make(map[string]struct{}),
	}
}

// collectionKey returns a unique string key for a resource collection:
// "<group>/<version>/<resource>/<namespace>" (namespace is "" for cluster-scoped).
func collectionKey(group, version, resource, namespace string) string {
	return strings.Join([]string{group, version, resource, namespace}, "/")
}

// nextRV returns the next monotonically increasing resource version as a string.
func (s *ResourceStore) nextRV() string {
	return fmt.Sprintf("%d", s.rv.Add(1))
}

// List returns all items stored in the given (group, version, resource, namespace) collection.
func (s *ResourceStore) List(group, version, resource, namespace string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	col := s.items[collectionKey(group, version, resource, namespace)]
	out := make([]json.RawMessage, 0, len(col))
	for _, v := range col {
		out = append(out, v)
	}
	return out
}

// ListAll returns all items across every namespace for the given (group, version, resource).
// Used for cross-namespace list requests (e.g. GET /api/v1/pods).
func (s *ResourceStore) ListAll(group, version, resource string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := strings.Join([]string{group, version, resource}, "/") + "/"
	var out []json.RawMessage
	for key, col := range s.items {
		if strings.HasPrefix(key, prefix) || key == strings.Join([]string{group, version, resource, ""}, "/") {
			for _, v := range col {
				out = append(out, v)
			}
		}
	}
	return out
}

// Get returns the raw JSON for a single resource item. ok is false if not found.
func (s *ResourceStore) Get(group, version, resource, namespace, name string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	col, ok := s.items[collectionKey(group, version, resource, namespace)]
	if !ok {
		return nil, false
	}
	item, ok := col[name]
	return item, ok
}

// Set stores or replaces a resource item that a client wrote, stamping a fresh resourceVersion. The collection is not
// served with samples any more. Returns the stored JSON (with updated resourceVersion).
func (s *ResourceStore) Set(group, version, resource, namespace, name string, data json.RawMessage) json.RawMessage {
	return s.put(group, version, resource, namespace, name, data, true)
}

// Seed stores a sample as the item, which leaves the collection to be served with samples.
func (s *ResourceStore) Seed(group, version, resource, namespace, name string, data json.RawMessage) json.RawMessage {
	return s.put(group, version, resource, namespace, name, data, false)
}

// put stamps a fresh resourceVersion on the item and stores it, marking the collection as touched by a client if touch is set.
func (s *ResourceStore) put(group, version, resource, namespace, name string, data json.RawMessage, touch bool) json.RawMessage {
	rv := s.nextRV()
	data = patchMetaField(data, "resourceVersion", rv)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := collectionKey(group, version, resource, namespace)
	if s.items[key] == nil {
		s.items[key] = make(map[string]json.RawMessage)
	}
	s.items[key][name] = data
	if touch {
		s.touched[key] = struct{}{}
	}
	return data
}

// Touched tells whether a client has written to the collection, or to any collection of the resource when the namespace
// is empty.
func (s *ResourceStore) Touched(group, version, resource, namespace string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if namespace != "" {
		_, ok := s.touched[collectionKey(group, version, resource, namespace)]
		return ok
	}
	prefix := strings.Join([]string{group, version, resource}, "/") + "/"
	for key := range s.touched {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// Delete removes a single resource item. Returns true if it existed.
func (s *ResourceStore) Delete(group, version, resource, namespace, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := collectionKey(group, version, resource, namespace)
	if _, ok := s.items[key][name]; !ok {
		return false
	}
	delete(s.items[key], name)
	s.touched[key] = struct{}{}
	return true
}

// DeleteCollection removes all items in the given collection and returns the count.
func (s *ResourceStore) DeleteCollection(group, version, resource, namespace string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := collectionKey(group, version, resource, namespace)
	n := len(s.items[key])
	delete(s.items, key)
	s.touched[key] = struct{}{}
	return n
}

// ─── JSON helpers ─────────────────────────────────────────────────────────────

// patchMetaField sets a single field inside the "metadata" object of a K8s JSON blob.
// If the JSON is unparseable or has no metadata object, the input is returned unmodified.
func patchMetaField(data json.RawMessage, field, value string) json.RawMessage {
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	if meta == nil {
		meta = make(map[string]interface{})
	}
	meta[field] = value
	obj["metadata"] = meta
	updated, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return updated
}

// patchMetaFields sets multiple metadata fields at once.
func patchMetaFields(data json.RawMessage, fields map[string]interface{}) json.RawMessage {
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	if meta == nil {
		meta = make(map[string]interface{})
	}
	for k, v := range fields {
		meta[k] = v
	}
	obj["metadata"] = meta
	updated, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return updated
}

// extractMetaField reads a string field from the "metadata" object of a K8s JSON blob.
func extractMetaField(data json.RawMessage, field string) string {
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	if meta == nil {
		return ""
	}
	v, _ := meta[field].(string)
	return v
}
