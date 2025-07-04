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

package ctl

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/k8s"
	"goto/pkg/k8s/store"
	"log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var ()

func ApplyYaml(yamlName, namespace, kind, name, to string, delete bool) (rlist []string, errs []error) {
	resourcesToApply, err := store.Kstore.GetTargetResources(yamlName, namespace, kind, name)
	if err != nil {
		return nil, []error{err}
	}
	for ns, kindResources := range resourcesToApply {
		for kind, resources := range kindResources {
			for name, kr := range resources {
				if kr == nil {
					log.Printf("No resource exists for yamlName [%s], namespace [%s]. kind [%s] name [%s]\n", yamlName, ns, kind, name)
					continue
				}
				if to != namespace {
					krCopy := *kr
					r := *kr.Resource
					krCopy.Resource = &r
					krCopy.Namespace = to
					kr = &krCopy
					if kr.GVK.Kind == k8s.KindNamespace {
						kr.Name = to
						kr.Resource.SetName(to)
					} else {
						kr.Resource.SetNamespace(to)
					}
				}
				if err := applyResource(to, name, kr, delete); err != nil {
					errs = append(errs, err)
					log.Printf("Failed to process resource [%s/%s] with error: %s\n", ns, name, err.Error())
				} else {
					rlist = append(rlist, fmt.Sprintf("%s/%s/%s", ns, kind, name))
					log.Printf("Processed resource [%s/%s] for yamlName [%s], namespace [%s]. kind [%s] name [%s]\n", ns, name, yamlName, ns, kind, name)
				}
			}
		}
	}
	return
}

func applyResource(namespace, name string, kr *k8s.K8sResource, delete bool) error {
	ri, _, err := k8s.GetResourceInterface(kr.GVK, namespace)
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
