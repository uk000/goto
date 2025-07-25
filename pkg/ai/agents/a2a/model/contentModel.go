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

package a2amodel

type Message struct {
	Kind             string   `json:"kind"`
	MessageId        string   `json:"messageId"`
	TaskId           *string  `json:"taskId,omitempty"`
	ContextId        *string  `json:"contextId,omitempty"`
	Role             string   `json:"role"` //user or agent
	Parts            []*Part  `json:"parts"`
	Metadata         AnyMap   `json:"metadata,omitempty"`
	Extensions       []string `json:"extensions,omitempty"`
	ReferenceTaskIds []string `json:"referenceTaskIds,omitempty"`
}

type Part struct {
	Kind     string       `json:"kind"` //text, file, data
	Text     *string      `json:"text,omitempty"`
	File     *FileContent `json:"file,omitempty"`
	Data     AnyMap       `json:"data,omitempty"`
	Metadata AnyMap       `json:"metadata,omitempty"`
}

type FileContentBase struct {
	Name     *string `json:"name,omitempty"`
	MimeType *string `json:"mimeType,omitempty"`
}

type FileContentBytes struct {
	FileContentBase
	Bytes string `json:"bytes"`
}

type FileContentURI struct {
	FileContentBase
	URI string `json:"uri"`
}

type FileContent interface {
	IsFileContent()
}

func (FileContentBytes) IsFileContent() {}
func (FileContentURI) IsFileContent()   {}

type Artifact struct {
	ArtifactId  string   `json:"artifactId"`
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Parts       []*Part  `json:"parts"`
	Metadata    AnyMap   `json:"metadata,omitempty"`
	Extensions  []string `json:"extensions,omitempty"`
}
