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

package store

import (
	"bufio"
	"fmt"
	"goto/pkg/k8s"
	"strings"

	// "goto/pkg/util"
	"io"
	"log"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlser "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/yaml"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

type ResourcesByName map[string]*k8s.K8sResource
type KindResourcesMap map[string]ResourcesByName
type NamespaceKindMap map[string]KindResourcesMap
type YamlNamespaceMap map[string]NamespaceKindMap

type K8sResourceStore struct {
	YamlResources YamlNamespaceMap
	lock          *sync.Mutex
}

var (
	Kstore = &K8sResourceStore{
		YamlResources: YamlNamespaceMap{},
		lock:          &sync.Mutex{},
	}
	decSer = yamlser.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
)

func (k *K8sResourceStore) Clear(yamlName, namespace string) {
	k.lock.Lock()
	defer k.lock.Unlock()
	if yamlName != "" {
		if namespace != "" {
			if k.YamlResources[yamlName] != nil {
				k.YamlResources[yamlName][namespace] = nil
				log.Printf("Cleared resources for unit [%s] and namespace [%s]\n", yamlName, namespace)
			}
		} else {
			k.YamlResources[yamlName] = nil
			log.Printf("Cleared resources for unit [%s]\n", yamlName)
		}
	} else {
		k.YamlResources = YamlNamespaceMap{}
		log.Printf("Cleared all resources\n")
	}
}

func (k *K8sResourceStore) Get(yamlName, namespace, kind string) interface{} {
	kind, _, _ = reinterpretResource(kind, "", "")
	k.lock.Lock()
	defer k.lock.Unlock()
	var unitResources NamespaceKindMap
	if yamlName != "" {
		unitResources = k.YamlResources[yamlName]
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

func (k *K8sResourceStore) StoreYaml(yamlName string, body io.ReadCloser) (NamespaceKindMap, []error) {
	list, errs := readResourceList(body)
	if len(errs) > 0 {
		return nil, errs
	}
	log.Printf("Read %d resources\n", len(list))
	k.lock.Lock()
	defer k.lock.Unlock()

	if k.YamlResources[yamlName] == nil {
		k.YamlResources[yamlName] = NamespaceKindMap{}
	}
	storeResource := func(kr *k8s.K8sResource) {
		ns := kr.Namespace
		if kr.GVK.Kind == k8s.K8sApiResourcesMap["namespaces"].Kind {
			ns = kr.Name
		}
		kindResources := k.YamlResources[yamlName][ns]
		if kindResources == nil {
			kindResources = KindResourcesMap{}
			k.YamlResources[yamlName][ns] = kindResources
		}
		resources := kindResources[kr.GVK.Kind]
		if resources == nil {
			resources = ResourcesByName{}
			kindResources[kr.GVK.Kind] = resources
		}
		resources[kr.Name] = kr
	}
	for _, item := range list {
		gvk := item.GroupVersionKind()
		storeResource(&k8s.K8sResource{
			Resource:  item,
			GVK:       &gvk,
			Namespace: item.GetNamespace(),
			Name:      item.GetName(),
		})
	}
	return k.YamlResources[yamlName], nil
}

func (k *K8sResourceStore) GetTargetResources(yamlName, namespace, kind, name string) (NamespaceKindMap, error) {
	if yamlName == "" {
		log.Printf("No unit specified\n")
		return nil, fmt.Errorf("no unit specified")
	}
	var targetResources NamespaceKindMap
	k.lock.Lock()
	defer k.lock.Unlock()
	targetResources = k.YamlResources[yamlName]
	if targetResources == nil {
		log.Printf("No resources found for unit [%s]\n", yamlName)
		return nil, fmt.Errorf("no resources found for unit [%s]", yamlName)
	}
	kind, _, name = reinterpretResource(kind, namespace, name)
	if namespace != "" {
		if targetResources[namespace] == nil {
			log.Printf("No resources found for unit [%s] and namespace [%s]\n", yamlName, namespace)
			return nil, fmt.Errorf("no resources found for unit [%s] and namespace [%s]", yamlName, namespace)
		}
		temp := NamespaceKindMap{}
		temp[namespace] = targetResources[namespace]
		targetResources = temp
		if kind != "" {
			if targetResources[namespace][kind] == nil {
				log.Printf("No resources found for unit [%s], namespace [%s]. kind [%s]\n", yamlName, namespace, kind)
				return nil, fmt.Errorf("no resources found for unit [%s], namespace [%s]. kind [%s]", yamlName, namespace, kind)
			}
			temp = NamespaceKindMap{}
			temp[namespace] = KindResourcesMap{}
			temp[namespace][kind] = targetResources[namespace][kind]
			targetResources = temp
			if name != "" {
				if targetResources[namespace][kind][name] == nil {
					log.Printf("No resources found for unit [%s], namespace [%s]. kind [%s] name [%s]\n", yamlName, namespace, kind, name)
					return nil, fmt.Errorf("no resources found for unit [%s], namespace [%s]. kind [%s] name [%s]", yamlName, namespace, kind, name)
				}
				temp = NamespaceKindMap{}
				temp[namespace] = KindResourcesMap{}
				temp[namespace][kind] = ResourcesByName{}
				temp[namespace][kind][name] = targetResources[namespace][kind][name]
				targetResources = temp
			}
		}
	}
	return targetResources, nil
}

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
	if kr := k8s.K8sApiResourcesMap[resource]; kr != nil {
		resource = kr.Kind
		if kr.Kind == k8s.KindNamespace {
			name = namespace
			namespace = ""
		}
	}
	return resource, namespace, name
}

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
	reader := yaml.NewYAMLReader(bufio.NewReader(body))
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
