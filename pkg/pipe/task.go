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

package pipe

import (
	"goto/pkg/client/target"
	"goto/pkg/job"
	"goto/pkg/scripts"
)

type TaskType string

const (
	TaskJob         TaskType = "Job"
	TaskScript      TaskType = "Script"
	TaskHTTPRequest TaskType = "HTTPRequest"
	TaskTraffic     TaskType = "Traffic"
	TaskK8sApply    TaskType = "K8sApply"
	TaskK8sDelete   TaskType = "K8sDelete"
)

type Task interface {
	Init(pipe *Pipe)
	GetName() string
	GetSpec() string
	GetInput() interface{}
	SetInput(interface{})
	GetStatus() string
	run()
}

type AbstractTask struct {
	Type   TaskType
	Name   string
	Spec   string
	Input  interface{}
	Result interface{}
	Status string
	pipe   *Pipe
}

type JobTask struct {
	AbstractTask
	Job *job.Job
}

type ScriptTask struct {
	AbstractTask
	Script *scripts.Script
}

type HTTPRequestTask struct {
	AbstractTask
	request  map[string]interface{}
	response map[string]interface{}
}

type TrafficTask struct {
	AbstractTask
	Target *target.Target
}
