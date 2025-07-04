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
	"flag"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
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
	DefaultCtxFile     = DefaultCtlContextPath + "context.yaml"
	DefaultContextName = "default"
	ctxFile            string
	contexts           Contexts
	currentContext     *types.Context
	config             *GotoConfig
	CtxFlagSet         = flag.NewFlagSet("ctx", flag.ExitOnError)
	ApplyFlagSet       = flag.NewFlagSet("apply", flag.ExitOnError)
)

type Contexts map[string]*types.Context

func Ctl(args []string) {
	loadOrCreateContextFile()
	switch args[0] {
	case "ctx":
		ctlCtx(args[1:])
	case "apply":
		ctlApply(args[1:])
	}
}

func ctlCtx(args []string) {
	CtxFlagSet.Parse(args)
	updateContext()
}

func ctlApply(args []string) {
	ApplyFlagSet.Parse(args)
	loadContext()
	loadConfig()
	processScripts()
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
			panic(err)
		}
	} else {
		if err := yaml.Unmarshal(data, &contexts); err != nil {
			panic(err)
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

func loadConfig() {
	config = &GotoConfig{}
	if data, err := os.ReadFile(global.CtlConfig.ConfigFile); err == nil {
		if err := yaml.Unmarshal(data, config); err != nil {
			panic(err)
		}
	} else {
		panic(err)
	}
	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		log.Fatalf("error marshalling YAML: %v", err)
	}
	fmt.Println(string(yamlBytes))
}

func processScripts() {
	if config.Scripts == nil || (len(config.Scripts.Config) == 0 && len(config.Scripts.Run) == 0) {
		log.Println("No scripts to configure or run")
		return
	}
	if config.Scripts.Config != nil {
		for _, script := range config.Scripts.Config {
			if script == nil || script.Name == "" || (script.Content == "" && script.FilePath == "") {
				log.Println("Script name and one of [content, path] must be given. Skipping empty or invalid script.")
				continue
			}
			sendScript(script)
		}
	}
	if config.Scripts.Run != nil {
		for _, script := range config.Scripts.Run {
			if len(strings.TrimSpace(script)) == 0 {
				log.Println("Empty script name in run list. Skipping.")
				continue
			}
			runScript(script)
		}
	}
}

func sendScript(script *ScriptConfig) {
	var url string
	var content []byte
	if script.FilePath != "" {
		url = fmt.Sprintf("%s/scripts/store/%s", currentContext.RemoteGotoURL, script.Name)
		file, err := os.ReadFile(script.FilePath)
		if err != nil {
			log.Printf("Failed to read script file [%s]: %v\n", script.FilePath, err)
			return
		}
		content = file
	} else {
		url = fmt.Sprintf("%s/scripts/add/%s", currentContext.RemoteGotoURL, script.Name)
		content = []byte(script.Content)
	}
	log.Printf("Sending script [%s] to [%s]\n", script.Name, url)
	resp, err := http.Post(url, "text/plain", strings.NewReader(string(content)))
	if err != nil {
		log.Printf("Failed to send script [%s]: %v\n", script.Name, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Server returned non-OK status: %s\n", resp.Status)
	} else {
		log.Printf("Script [%s] sent successfully. Response: [%s]\n", script.Name, util.Read(resp.Body))
	}
}

func runScript(script string) {
	url := fmt.Sprintf("%s/scripts/%s/run", currentContext.RemoteGotoURL, script)
	log.Printf("Sending run request for script [%s] to [%s]\n", script, url)
	resp, err := http.Post(url, "text/plain", nil)
	if err != nil {
		log.Printf("Failed to trigger execution for script [%s]: %v\n", script, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Server returned non-OK status: %s\n", resp.Status)
	} else {
		log.Printf("Script [%s] executed successfully. Response: [%s]\n", script, util.Read(resp.Body))
	}
}
