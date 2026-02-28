package ctl

import (
	"fmt"
	"goto/pkg/util"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Scripts struct {
	Config []*ScriptConfig `yaml:"config,omitempty"`
	Run    []*ScriptRun    `yaml:"run,omitempty"`
}

type ScriptConfig struct {
	Name     string `yaml:"name"`
	FilePath string `yaml:"filePath,omitempty"`
	Content  string `yaml:"content,omitempty"`
}

type ScriptRun struct {
	Name string   `yaml:"name"`
	Args []string `yaml:"args,omitempty"`
}

func processScripts(config *GotoConfig) {
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
			if len(strings.TrimSpace(script.Name)) == 0 {
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

func runScript(script *ScriptRun) {
	args := url.Values{}
	for _, arg := range script.Args {
		args.Add("args", arg)
	}
	url := fmt.Sprintf("%s/scripts/%s/run?%s", currentContext.RemoteGotoURL, script.Name, args.Encode())
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
