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

package startup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"goto/ctl"
	"goto/pkg/global"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/scripts"
	"goto/pkg/util"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgtdi/fswatcher"
)

var (
	configWatchers   []fswatcher.Watcher
	tlsConfigs       = map[string]ctl.PortTLS{}
	a2aConfigs       = map[string]*ctl.A2A{}
	mcpConfigs       = map[string]*ctl.MCP{}
	httpProxyConfigs = map[string]*httpproxy.Proxy{}
	tcpProxyConfigs  = map[string]*tcpproxy.TCPProxy{}
	httpConfigs      = map[string]*ctl.HTTP{}
	grpcConfigs      = map[string]*ctl.GRPC{}
	loaded           = false
	lock             = sync.Mutex{}
)

type ChangeLog map[string]any

func Start() {
	if !loaded {
		loaded = true
		loadStartupConfigs()
	}
	//runStartupScript()
}

func Stop() {
	for _, cw := range configWatchers {
		if cw.IsRunning() {
			cw.Close()
		}
	}
}

func loadStartupConfigs() {
	if len(global.ServerConfig.ConfigPaths) > 0 {
		loadConfigsFromPaths(nil)
		for _, configPath := range global.ServerConfig.ConfigPaths {
			configWatcher, err := fswatcher.New(fswatcher.WithPath(configPath, fswatcher.WithDepth(fswatcher.WatchTopLevel)))
			if err != nil {
				log.Printf("Failed to set watch for config [%s] with error: %s\n", configPath, err.Error())
				log.Printf("Will load config without watching\n")
			} else {
				watchStartupConfig(configWatcher)
			}
			configWatchers = append(configWatchers, configWatcher)
		}
	}
}

func watchStartupConfig(configWatcher fswatcher.Watcher) {
	debounceRemove := util.Debounce(5 * time.Second)
	debounceLoad := util.Debounce(5 * time.Second)
	changeChan := make(chan string, 10)
	go loadChanges(changeChan)
	go configWatcher.Watch(context.Background())
	go func() {
		for event := range configWatcher.Events() {
			removed := false
			for _, t := range event.Types {
				switch t {
				case fswatcher.EventRename:
					_, err := os.Stat(event.Path)
					removed = errors.Is(err, os.ErrNotExist)
				case fswatcher.EventRemove:
					removed = true
				}
			}
			all := false
			if strings.Contains(event.Path, "..") {
				all = true
			}
			if all {
				if removed {
					debounceRemove(func() {
						removeAllConfigs()
					})
				} else {
					debounceLoad(func() {
						loadConfigsFromPaths(nil)
					})
				}
			} else {
				if removed {
					log.Printf("Files removed in [%s]. Will cleanup configs.", event.Path)
					removeConfigs(event.Path)
				} else {
					log.Printf("Files changed in [%s]. Will reload with delay.", event.Path)
					changeChan <- event.Path
				}
			}
		}
	}()
}

func removeAllConfigs() {
	for filename := range tlsConfigs {
		log.Printf("Removing TLS configs for %s.\n", filename)
		clearTLS(tlsConfigs[filename])
		delete(tlsConfigs, filename)
	}
	for filename := range a2aConfigs {
		log.Printf("Removing A2A configs for %s.\n", filename)
		clearA2A(a2aConfigs[filename])
		delete(a2aConfigs, filename)
	}
	for filename := range mcpConfigs {
		log.Printf("Removing MCP configs for %s.\n", filename)
		clearMCP(mcpConfigs[filename])
		delete(mcpConfigs, filename)
	}
	for filename := range httpProxyConfigs {
		log.Printf("Removing HTTP Proxy configs for %s.\n", filename)
		removeHTTPProxy(httpProxyConfigs[filename])
		delete(httpProxyConfigs, filename)
	}
	for filename := range tcpProxyConfigs {
		log.Printf("Removing TCP Proxy configs for %s.\n", filename)
		removeTCPProxy(tcpProxyConfigs[filename])
		delete(tcpProxyConfigs, filename)
	}
	for filename := range httpConfigs {
		log.Printf("Removing HTTP configs for %s.\n", filename)
		clearHTTP(httpConfigs[filename])
		delete(httpConfigs, filename)
	}
	for filename := range grpcConfigs {
		log.Printf("Removing gRPC configs for %s.\n", filename)
		clearGRPC(grpcConfigs[filename])
		delete(grpcConfigs, filename)
	}
}

func removeConfigs(filePath string) {
	parts := strings.Split(filePath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	lock.Lock()
	defer lock.Unlock()
	log.Printf("File [%s] removed.", filename)
	if tlsConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing TLS configs.\n", filename)
		clearTLS(tlsConfigs[filename])
	}
	if a2aConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing A2A configs.\n", filename)
		clearA2A(a2aConfigs[filename])
	}
	if mcpConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing MCP configs.\n", filename)
		clearMCP(mcpConfigs[filename])
	}
	if httpProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing HTTP Proxy configs.\n", filename)
		removeHTTPProxy(httpProxyConfigs[filename])
	}
	if tcpProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing TCP Proxy configs.\n", filename)
		removeTCPProxy(tcpProxyConfigs[filename])
	}
	if httpConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing HTTP configs.\n", filename)
		clearHTTP(httpConfigs[filename])
	}
	if grpcConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing gRPC configs.\n", filename)
		clearGRPC(grpcConfigs[filename])
	}
}

func (c ChangeLog) Contains(v string) bool {
	if _, ok := c[v]; ok {
		return true
	}
	for k := range c {
		if strings.Contains(k, v) || strings.Contains(v, k) {
			return true
		}
	}
	return false
}

func loadConfigsFromPaths(changeLog ChangeLog) {
	for _, configPath := range global.ServerConfig.ConfigPaths {
		log.Printf("Loading configs from path [%s]", configPath)
		files, err := os.ReadDir(configPath)
		if err != nil {
			log.Printf("Failed to read config path [%s] with error: %s\n", configPath, err.Error())
			continue
		}
		for _, file := range files {
			filePath := filepath.Join(configPath, file.Name())
			if len(changeLog) == 0 || changeLog.Contains(filePath) {
				loadConfigFromFile(filePath)
			} else {
				log.Printf("Skipping unchanged file [%s]", filePath)
			}
		}
	}
}

func loadChanges(changeChan chan string) {
	debounce := util.Debounce(5 * time.Second)
	lock := sync.Mutex{}
	changeLog := ChangeLog{}
	for path := range changeChan {
		lock.Lock()
		changeLog[path] = 0
		lock.Unlock()
		debounce(func() {
			lock.Lock()
			loadConfigsFromPaths(changeLog)
			lock.Unlock()
		})
	}
}

func loadConfigFromFile(filePath string) {
	if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		log.Printf("Loading config from file: %s\n", filePath)
		loadConfig(filePath, ctl.LoadConfig(filePath))
	} else if strings.HasSuffix(filePath, ".sh") {
		log.Printf("Loading config from script: %s\n", filePath)
		loadConfigFromScript(filePath)
	} else {
		log.Printf("Ignoring non-config file: %s\n", filePath)
	}
}

func loadConfigFromScript(filePath string) {
	script := &scripts.Script{
		Name:     "GotoConfig",
		FilePath: filePath,
	}
	var buff bytes.Buffer
	w := bufio.NewWriter(&buff)
	script.RunWithStdIn(w)
	w.Flush()
	loadConfig(filePath, ctl.ParseConfig(buff.Bytes()))
}

func loadConfig(filePath string, config *ctl.GotoConfig) {
	parts := strings.Split(filePath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	if config == nil {
		log.Println("No config loaded")
		return
	}
	if config.TLS != nil {
		if tlsConfigs[filename] != nil {
			clearTLS(tlsConfigs[filename])
		}
		lock.Lock()
		tlsConfigs[filename] = config.TLS
		lock.Unlock()
		processTLS(config.TLS)

	}
	if config.MCP != nil {
		if mcpConfigs[filename] != nil {
			clearMCP(mcpConfigs[filename])
		}
		lock.Lock()
		mcpConfigs[filename] = config.MCP
		lock.Unlock()
		loadMCP(config.MCP)
	}
	if config.A2A != nil {
		if a2aConfigs[filename] != nil {
			clearA2A(a2aConfigs[filename])
		}
		lock.Lock()
		a2aConfigs[filename] = config.A2A
		lock.Unlock()
		loadA2A(config.A2A)
	}
	if config.Proxies != nil {
		for _, proxy := range config.Proxies {
			if proxy.HTTP != nil {
				if httpProxyConfigs[filename] != nil {
					removeHTTPProxy(httpProxyConfigs[filename])
				}
				lock.Lock()
				httpProxyConfigs[filename] = proxy.HTTP
				lock.Unlock()
				loadHTTPProxy(proxy.HTTP)
			}
			if proxy.TCP != nil {
				if tcpProxyConfigs[filename] != nil {
					removeTCPProxy(tcpProxyConfigs[filename])
				}
				lock.Lock()
				tcpProxyConfigs[filename] = proxy.TCP
				lock.Unlock()
				loadTCPProxy(proxy.TCP)
			}
		}
	}
	if config.HTTP != nil {
		if httpConfigs[filename] != nil {
			clearHTTP(httpConfigs[filename])
		}
		lock.Lock()
		httpConfigs[filename] = config.HTTP
		lock.Unlock()
		loadHTTP(config.HTTP)
	}
	if config.GRPC != nil {
		if grpcConfigs[filename] != nil {
			clearGRPC(grpcConfigs[filename])
		}
		lock.Lock()
		grpcConfigs[filename] = config.GRPC
		lock.Unlock()
		loadGRPC(config.GRPC)
	}
}

func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}
