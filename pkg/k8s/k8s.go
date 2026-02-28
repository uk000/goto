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

	// "goto/pkg/util"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

var ()

// func encodeResourceID(group, version, resource, namespace, name string) string {
// 	return strings.ToLower(fmt.Sprintf("%s/%s/%s/%s/%s", group, version, resource, namespace, name))
// }

// func readResource(body io.ReadCloser) (*unstructured.Unstructured, *schema.GroupVersionKind, error) {
// 	obj := &unstructured.Unstructured{}
// 	_, gvk, err := decSer.Decode(util.ReadBytes(body), nil, obj)
// 	return obj, gvk, err
// }
