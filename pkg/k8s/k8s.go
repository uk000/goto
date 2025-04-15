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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	// "goto/pkg/util"
	"io"
	"log"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/tools/remotecommand"
)

type K8sResource struct {
	Resource  *unstructured.Unstructured `json:"resource"`
	GVK       *schema.GroupVersionKind   `json:"gvk"`
	Namespace string                     `json:"namespace"`
	Name      string                     `json:"name"`
}

type KindResources map[string]map[string]*K8sResource
type NamespaceKindMap map[string]KindResources
type UnitNamespaceMap map[string]NamespaceKindMap

type K8sResourceStore struct {
	K8sResources UnitNamespaceMap
	lock         *sync.Mutex
}

var (
	kstore = &K8sResourceStore{
		K8sResources: UnitNamespaceMap{},
		lock:         &sync.Mutex{},
	}
	decSer = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
)

func (k *K8sResourceStore) clear(unit, namespace string) {
	k.lock.Lock()
	defer k.lock.Unlock()
	if unit != "" {
		if namespace != "" {
			if k.K8sResources[unit] != nil {
				k.K8sResources[unit][namespace] = nil
				log.Printf("Cleared resources for unit [%s] and namespace [%s]\n", unit, namespace)
			}
		} else {
			k.K8sResources[unit] = nil
			log.Printf("Cleared resources for unit [%s]\n", unit)
		}
	} else {
		k.K8sResources = UnitNamespaceMap{}
		log.Printf("Cleared all resources\n")
	}
}

func (k *K8sResourceStore) get(unit, namespace, kind string) interface{} {
	kind, _, _ = reinterpretResource(kind, "", "")
	k.lock.Lock()
	defer k.lock.Unlock()
	var unitResources NamespaceKindMap
	if unit != "" {
		unitResources = k.K8sResources[unit]
	}
	if unitResources == nil {
		return nil
	}
	if len(namespace) > 0 {
		nsResources := unitResources[namespace]
		if nsResources == nil {
			return nil
		}
		if len(kind) > 0 {
			return nsResources[kind]
		}
		return nsResources
	}
	return unitResources
}

func (k *K8sResourceStore) storeYaml(unit string, body io.ReadCloser) (NamespaceKindMap, []error) {
	list, errs := readResourceList(body)
	if len(errs) > 0 {
		return nil, errs
	}
	log.Printf("Read %d resources\n", len(list))
	k.lock.Lock()
	defer k.lock.Unlock()

	if k.K8sResources[unit] == nil {
		k.K8sResources[unit] = NamespaceKindMap{}
	}
	storeResource := func(kr *K8sResource) {
		ns := kr.Namespace
		if kr.GVK.Kind == K8sApiResourcesMap["namespaces"].Kind {
			ns = kr.Name
		}
		kindResources := k.K8sResources[unit][ns]
		if kindResources == nil {
			kindResources = KindResources{}
			k.K8sResources[unit][ns] = kindResources
		}
		resources := kindResources[kr.GVK.Kind]
		if resources == nil {
			resources = map[string]*K8sResource{}
			kindResources[kr.GVK.Kind] = resources
		}
		resources[kr.Name] = kr
	}
	for _, item := range list {
		gvk := item.GroupVersionKind()
		storeResource(&K8sResource{
			Resource:  item,
			GVK:       &gvk,
			Namespace: item.GetNamespace(),
			Name:      item.GetName(),
		})
	}
	return k.K8sResources[unit], nil
}

func (k *K8sResourceStore) getTargetResources(unit, namespace, kind, name string) (NamespaceKindMap, error) {
	if unit == "" {
		log.Printf("No unit specified\n")
		return nil, fmt.Errorf("no unit specified")
	}
	var targetResources NamespaceKindMap
	k.lock.Lock()
	defer k.lock.Unlock()
	targetResources = k.K8sResources[unit]
	if targetResources == nil {
		log.Printf("No resources found for unit [%s]\n", unit)
		return nil, fmt.Errorf("no resources found for unit [%s]", unit)
	}
	kind, _, name = reinterpretResource(kind, namespace, name)
	if namespace != "" {
		if targetResources[namespace] == nil {
			log.Printf("No resources found for unit [%s] and namespace [%s]\n", unit, namespace)
			return nil, fmt.Errorf("no resources found for unit [%s] and namespace [%s]", unit, namespace)
		}
		temp := NamespaceKindMap{}
		temp[namespace] = targetResources[namespace]
		targetResources = temp
		if kind != "" {
			if targetResources[namespace][kind] == nil {
				log.Printf("No resources found for unit [%s], namespace [%s]. kind [%s]\n", unit, namespace, kind)
				return nil, fmt.Errorf("no resources found for unit [%s], namespace [%s]. kind [%s]", unit, namespace, kind)
			}
			temp = NamespaceKindMap{}
			temp[namespace] = KindResources{}
			temp[namespace][kind] = targetResources[namespace][kind]
			targetResources = temp
			if name != "" {
				if targetResources[namespace][kind][name] == nil {
					log.Printf("No resources found for unit [%s], namespace [%s]. kind [%s] name [%s]\n", unit, namespace, kind, name)
					return nil, fmt.Errorf("no resources found for unit [%s], namespace [%s]. kind [%s] name [%s]", unit, namespace, kind, name)
				}
				temp = NamespaceKindMap{}
				temp[namespace] = KindResources{}
				temp[namespace][kind] = map[string]*K8sResource{}
				temp[namespace][kind][name] = targetResources[namespace][kind][name]
				targetResources = temp
			}
		}
	}
	return targetResources, nil
}

func (k *K8sResourceStore) applyYaml(unit, namespace, kind, name string, delete bool) (rlist []string, errs []error) {
	resourcesToApply, err := k.getTargetResources(unit, namespace, kind, name)
	if err != nil {
		return nil, []error{err}
	}
	for ns, kindResources := range resourcesToApply {
		for kind, resources := range kindResources {
			for name, kr := range resources {
				if kr == nil {
					log.Printf("No resource exists for unit [%s], namespace [%s]. kind [%s] name [%s]\n", unit, ns, kind, name)
					continue
				}
				if err := applyResource(ns, name, kr, delete); err != nil {
					errs = append(errs, err)
					log.Printf("Failed to process resource [%s/%s] with error: %s\n", ns, name, err.Error())
				} else {
					rlist = append(rlist, fmt.Sprintf("%s/%s/%s", ns, kind, name))
					log.Printf("Processed resource [%s/%s] for unit [%s], namespace [%s]. kind [%s] name [%s]\n", ns, name, unit, ns, kind, name)
				}
			}
		}
	}
	return
}

func applyResource(namespace, name string, kr *K8sResource, delete bool) error {
	ri, _, err := getResourceInterface(kr.GVK, namespace)
	if err != nil {
		log.Printf("Failed to get resource interface with error: %s\n", err.Error())
		return err
	}
	if delete {
		if err := ri.Delete(context.TODO(), name, metav1.DeleteOptions{}); err != nil {
			log.Printf("Failed to delete resource [%s/%s] with error: %s\n", namespace, name, err.Error())
			return err
		}
	} else {
		data, err := json.Marshal(kr.Resource)
		if err != nil {
			log.Printf("Failed to marshal object with error: %s\n", err.Error())
			return err
		}
		if _, err := ri.Patch(context.TODO(), name, types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: "goto"}); err != nil {
			log.Printf("Failed to apply resource [%s/%s] with error: %s\n", namespace, name, err.Error())
			return err
		}
	}
	return nil
}

// func encodeResourceID(group, version, resource, namespace, name string) string {
// 	return strings.ToLower(fmt.Sprintf("%s/%s/%s/%s/%s", group, version, resource, namespace, name))
// }

func decodeResourceID(id string) (group, version, resource, namespace, name string) {
	pieces := strings.Split(id, "/")
	group = pieces[0]
	version = pieces[1]
	resource = pieces[2]
	if len(pieces) > 3 {
		namespace = pieces[3]
	}
	if len(pieces) > 4 {
		name = pieces[4]
	}
	if resource == "ns" || resource == "namespaces" {
		resource = "namespace"
	} else if resource == "pods" {
		resource = "pod"
	} else if resource == "svc" || resource == "services" {
		resource = "service"
	}
	return
}

func reinterpretResource(resource, namespace, name string) (string, string, string) {
	if kr := K8sApiResourcesMap[resource]; kr != nil {
		resource = kr.Kind
		if kr.Kind == KindNamespace {
			name = namespace
			namespace = ""
		}
	}
	return resource, namespace, name
}

func PodExec(namespace, label, container string, command string) (string, error) {
	var podName string
	if pods, err := k8sClient.typedClient.Pods(namespace).List(context.Background(),
		metav1.ListOptions{LabelSelector: label}); err == nil {
		if len(pods.Items) == 0 {
			log.Printf("K8s: No pods in namespace [%s] for label [%s]\n", namespace, label)
			return "", err
		} else {
			podName = pods.Items[0].Name
		}
	} else {
		log.Printf("K8s: Failed to load pods list with error: %s\n", err.Error())
		return "", err
	}
	req := k8sClient.typedClient.RESTClient().Post().Namespace(namespace).
		Resource("pods").Name(podName).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   []string{"sh", "-c", command},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(k8sClient.config, "POST", req.URL())
	if err != nil {
		log.Printf("K8s: Failed to execute pod command with error: %s\n", err.Error())
		return "", err
	}
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &outBuf,
		Stderr: &errBuf,
		Tty:    false,
	})
	if err != nil {
		log.Printf("K8s: Failed to read pod output with error: %s\n", err.Error())
		return "", err
	}
	output := outBuf.String()
	if output == "" {
		output = errBuf.String()
	}
	return output, nil
}

// func readResource(body io.ReadCloser) (*unstructured.Unstructured, *schema.GroupVersionKind, error) {
// 	obj := &unstructured.Unstructured{}
// 	_, gvk, err := decSer.Decode(util.ReadBytes(body), nil, obj)
// 	return obj, gvk, err
// }

func readResourceList(body io.ReadCloser) ([]*unstructured.Unstructured, []error) {
	docs, err := readDocuments(body)
	if err != nil {
		log.Printf("Failed to read documents with error: %s\n", err.Error())
		return nil, []error{err}
	}
	if len(docs) == 0 {
		log.Printf("No documents found in body\n")
		return nil, []error{fmt.Errorf("no documents found in body")}
	}
	list := []*unstructured.Unstructured{}
	errs := []error{}
	for _, doc := range docs {
		obj := &unstructured.Unstructured{}
		if _, _, err := decSer.Decode(doc, nil, obj); err == nil {
			list = append(list, obj)
		} else {
			log.Printf("Failed to decode document with error: %s\n", err.Error())
			errs = append(errs, err)
		}
	}
	return list, errs
}

func readDocuments(body io.ReadCloser) ([][]byte, error) {
	docs := [][]byte{}
	reader := k8syaml.NewYAMLReader(bufio.NewReader(body))
	for {
		doc, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}
