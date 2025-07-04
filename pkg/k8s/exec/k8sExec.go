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

package exec

import (
	"bytes"
	"context"
	"goto/pkg/k8s"
	"log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/tools/remotecommand"
)

func PodExec(namespace, label, container string, command string) (string, error) {
	var podName string
	if pods, err := k8s.Client.TypedClient.Pods(namespace).List(context.Background(),
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
	req := k8s.Client.TypedClient.RESTClient().Post().Namespace(namespace).
		Resource("pods").Name(podName).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   []string{"sh", "-c", command},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(k8s.Client.Config, "POST", req.URL())
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
