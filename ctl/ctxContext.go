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

package ctl

import (
	"flag"
	"goto/pkg/global"
	"goto/pkg/types"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	DefaultCtlContextPath = func() string {
		dir, err := os.UserHomeDir()
		if err != nil {
			dir = "~"
		}
		return dir + "/.goto_context/"
	}()
	DefaultCtxFile     = DefaultCtlContextPath + "gotoContext.yaml"
	DefaultContextName = "default"
	ctxFile            string
	contexts           Contexts
	currentContext     *types.Context
	CtxFlagSet         = flag.NewFlagSet("ctx", flag.ExitOnError)
)

type Contexts map[string]*types.Context

func ctlCtx(args []string) {
	CtxFlagSet.Parse(args)
	updateContext()
}

func loadOrCreateContextFile() {
	ctxFile = global.CtlConfig.ContextFile
	if ctxFile == "" {
		ctxFile = DefaultCtxFile
	}
	data, err := os.ReadFile(ctxFile)
	if err != nil {
		if os.IsNotExist(err) {
			pieces := strings.Split(ctxFile, "/")
			ctlContextPath := ""
			if len(pieces) > 1 {
				ctlContextPath = strings.Join(pieces[:len(pieces)-1], "/")
			} else {
				ctlContextPath = DefaultCtlContextPath
			}
			os.MkdirAll(ctlContextPath, 0755)
			addContext(DefaultContextName, global.CtlConfig.RemoteURL)
		} else {
			log.Printf("Failed to read context file [%s] with error: %s\n", ctxFile, err.Error())
		}
	} else {
		if err := yaml.Unmarshal(data, &contexts); err != nil {
			log.Printf("Failed to unmarshal context file [%s] with error: %s\n", ctxFile, err.Error())
		}
	}
}

func addContext(name string, remoteURL string) {
	if name == "" {
		name = DefaultContextName
	}
	if remoteURL == "" {
		remoteURL = global.CtlConfig.RemoteURL
	}
	if contexts == nil {
		contexts = make(Contexts)
	}
	if _, exists := contexts[name]; exists {
		return
	}
	contexts[name] = &types.Context{
		Name:          name,
		RemoteGotoURL: remoteURL,
	}
	saveContexts()
}

func saveContexts() {
	if ctxFile == "" {
		ctxFile = DefaultCtxFile
	}
	out, err := yaml.Marshal(&contexts)
	if err != nil {
		log.Printf("Failed to marshal contexts: %v\n", err)
		return
	}
	if err := os.WriteFile(ctxFile, out, 0644); err != nil {
		log.Printf("Failed to write contexts to file [%s]: %v\n", ctxFile, err)
	} else {
		log.Printf("Contexts saved successfully to [%s].\n", ctxFile)
	}
}

func updateContext() {
	name := global.CtlConfig.Name
	remoteURL := global.CtlConfig.RemoteURL
	currentContext = contexts[name]
	if currentContext == nil {
		addContext(name, remoteURL)
	} else {
		if remoteURL != "" {
			currentContext.RemoteGotoURL = remoteURL
			saveContexts()
		} else {
			log.Printf("No Remote URL given for Context [%s].\n", name)
		}
	}
}

func loadContext() {
	currentContext = contexts[global.CtlConfig.Context]
	if currentContext == nil {
		log.Printf("Context [%s] not found. Using default context.\n", global.CtlConfig.Context)
		currentContext = contexts[DefaultContextName]
	}
}
