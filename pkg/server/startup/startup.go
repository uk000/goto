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
	grpcproxy "goto/pkg/proxy/grpc"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/scripts"
	"goto/pkg/util"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sgtdi/fswatcher"
)

var (
	configWatchers   []fswatcher.Watcher
	tlsConfigs       = map[string]*ctl.TLSConfigs{}
	listenerConfigs  = map[string]ctl.Listeners{}
	a2aConfigs       = map[string]*ctl.A2A{}
	mcpConfigs       = map[string]*ctl.MCP{}
	httpProxyConfigs = map[string]map[int]*httpproxy.Proxy{}
	tcpProxyConfigs  = map[string]map[int]*tcpproxy.TCPProxy{}
	grpcProxyConfigs = map[string]map[int]*grpcproxy.GRPCProxy{}
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
	for filename := range listenerConfigs {
		log.Printf("Removing Listener configs for %s.\n", filename)
		clearListeners(listenerConfigs[filename])
		delete(listenerConfigs, filename)
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
	for filename, m := range httpProxyConfigs {
		for port := range m {
			log.Printf("Removing HTTP Proxy configs for file [%s] port [%d] .\n", filename, port)
			removeHTTPProxy(port)
		}
		delete(httpProxyConfigs, filename)
	}
	for filename, m := range tcpProxyConfigs {
		for port := range m {
			log.Printf("Removing TCP Proxy configs for file [%s] port [%d] .\n", filename, port)
			removeTCPProxy(port)
		}
		delete(tcpProxyConfigs, filename)
	}
	for filename, m := range grpcProxyConfigs {
		for port := range m {
			log.Printf("Removing GRPC Proxy configs for file [%s] port [%d] .\n", filename, port)
			removeGRPCProxy(port)
		}
		delete(grpcProxyConfigs, filename)
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
		delete(tlsConfigs, filename)
	}
	if listenerConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing Listener configs.\n", filename)
		clearListeners(listenerConfigs[filename])
		delete(listenerConfigs, filename)
	}
	if a2aConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing A2A configs.\n", filename)
		clearA2A(a2aConfigs[filename])
		delete(a2aConfigs, filename)
	}
	if mcpConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing MCP configs.\n", filename)
		clearMCP(mcpConfigs[filename])
		delete(mcpConfigs, filename)
	}
	if httpProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing HTTP Proxy configs.\n", filename)
		for port := range httpProxyConfigs[filename] {
			log.Printf("Removing HTTP Proxy configs for file [%s] port [%d] .\n", filename, port)
			removeHTTPProxy(port)
		}
		delete(httpProxyConfigs, filename)
	}
	if tcpProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing TCP Proxy configs.\n", filename)
		for port := range tcpProxyConfigs[filename] {
			log.Printf("Removing TCP Proxy configs for file [%s] port [%d] .\n", filename, port)
			removeTCPProxy(port)
		}
		delete(tcpProxyConfigs, filename)
	}
	if grpcProxyConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing GRPC Proxy configs.\n", filename)
		for port := range grpcProxyConfigs[filename] {
			log.Printf("Removing GRPC Proxy configs for file [%s] port [%d] .\n", filename, port)
			removeGRPCProxy(port)
		}
		delete(grpcProxyConfigs, filename)
	}
	if httpConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing HTTP configs.\n", filename)
		clearHTTP(httpConfigs[filename])
		delete(httpConfigs, filename)
	}
	if grpcConfigs[filename] != nil {
		log.Printf("File removed: %s. Removing gRPC configs.\n", filename)
		clearGRPC(grpcConfigs[filename])
		delete(grpcConfigs, filename)
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
		configs := []*ctl.GotoConfig{}
		for _, file := range files {
			filePath := filepath.Join(configPath, file.Name())
			if len(changeLog) == 0 || changeLog.Contains(filePath) {
				if c := getFileConfig(filePath); c != nil {
					configs = append(configs, c)
				}
				delete(changeLog, filePath)
			} else {
				log.Printf("Skipping unchanged file [%s]", filePath)
			}
		}
		sort.Slice(configs, func(i, j int) bool {
			if configs[i] == nil {
				return false
			}
			if configs[j] == nil {
				return true
			}
			return configs[i].Order < configs[j].Order
		})
		for _, config := range configs {
			log.Printf("============== Loading Config: %s ===================", config.Filepath)
			loadConfig(config)
			log.Printf("============== Finished Loading Config: %s ===================", config.Filepath)
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
func getFileConfig(filePath string) *ctl.GotoConfig {
	if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		log.Printf("Reading YAML config from file: %s\n", filePath)
		return ctl.ReadConfigFromFile(filePath)
	} else if strings.HasSuffix(filePath, ".sh") {
		log.Printf("Reading YAML config from script: %s\n", filePath)
		return readConfigFromScript(filePath)
	} else {
		log.Printf("Ignoring non-config file: %s\n", filePath)
	}
	return nil
}

func readConfigFromScript(filePath string) *ctl.GotoConfig {
	script := &scripts.Script{
		Name:     "GotoConfig",
		FilePath: filePath,
	}
	var buff bytes.Buffer
	w := bufio.NewWriter(&buff)
	script.RunWithStdIn(w)
	w.Flush()
	return ctl.ParseConfig(filePath, buff.Bytes())
}

func loadConfig(config *ctl.GotoConfig) {
	parts := strings.Split(config.Filepath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	if config == nil {
		log.Println("No config loaded")
		return
	}
	loadTLSConfig(filename, config)
	loadListenersConfig(filename, config)
	loadMCPConfig(filename, config)
	loadA2AConfig(filename, config)
	loadHTTPConfig(filename, config)
	loadGRPCConfig(filename, config)
	loadProxyConfig(filename, config)
}

func loadTLSConfig(filename string, config *ctl.GotoConfig) {
	if config.TLS != nil {
		if tlsConfigs[filename] != nil {
			clearTLS(tlsConfigs[filename])
		}
		lock.Lock()
		tlsConfigs[filename] = config.TLS
		lock.Unlock()
		processTLS(config.TLS)
	}
}

func loadListenersConfig(filename string, config *ctl.GotoConfig) {
	if config.Listeners != nil {
		old := listenerConfigs[filename]
		if old != nil {
			clearRemovedListeners(old, config.Listeners)
		}
		lock.Lock()
		listenerConfigs[filename] = config.Listeners
		lock.Unlock()
		processListeners(config.Listeners)
	}
}

func loadMCPConfig(filename string, config *ctl.GotoConfig) {
	if config.MCP != nil {
		if mcpConfigs[filename] != nil {
			clearMCP(mcpConfigs[filename])
		}
		lock.Lock()
		mcpConfigs[filename] = config.MCP
		lock.Unlock()
		loadMCP(config.MCP)
	}
}

func loadA2AConfig(filename string, config *ctl.GotoConfig) {
	if config.A2A != nil {
		if a2aConfigs[filename] != nil {
			clearA2A(a2aConfigs[filename])
		}
		lock.Lock()
		a2aConfigs[filename] = config.A2A
		lock.Unlock()
		loadA2A(config.A2A)
	}
}

func loadHTTPConfig(filename string, config *ctl.GotoConfig) {
	if config.HTTP != nil {
		if httpConfigs[filename] != nil {
			clearHTTP(httpConfigs[filename])
		}
		lock.Lock()
		httpConfigs[filename] = config.HTTP
		lock.Unlock()
		loadHTTP(config.HTTP)
	}
}

func loadGRPCConfig(filename string, config *ctl.GotoConfig) {
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

func loadProxyConfig(filename string, config *ctl.GotoConfig) {
	if config.Proxies != nil {
		for _, proxy := range config.Proxies {
			if proxy.HTTP != nil {
				remove := false
				lock.Lock()
				if httpProxyConfigs[filename] == nil {
					httpProxyConfigs[filename] = map[int]*httpproxy.Proxy{}
				}
				if httpProxyConfigs[filename][proxy.HTTP.Port] != nil {
					remove = true
				}
				httpProxyConfigs[filename][proxy.HTTP.Port] = proxy.HTTP
				lock.Unlock()
				if remove {
					removeHTTPProxy(proxy.HTTP.Port)
				}
				loadHTTPProxy(proxy.HTTP)
			}
			if proxy.TCP != nil {
				remove := false
				lock.Lock()
				if tcpProxyConfigs[filename] == nil {
					tcpProxyConfigs[filename] = map[int]*tcpproxy.TCPProxy{}
				}
				if tcpProxyConfigs[filename][proxy.TCP.Port] != nil {
					remove = true
				}
				tcpProxyConfigs[filename][proxy.TCP.Port] = proxy.TCP
				lock.Unlock()
				if remove {
					removeTCPProxy(proxy.TCP.Port)
				}
				loadTCPProxy(proxy.TCP)
			}
			if proxy.GRPC != nil {
				remove := false
				lock.Lock()
				if grpcProxyConfigs[filename] == nil {
					grpcProxyConfigs[filename] = map[int]*grpcproxy.GRPCProxy{}
				}
				if grpcProxyConfigs[filename][proxy.GRPC.Port] != nil {
					remove = true
				}
				grpcProxyConfigs[filename][proxy.GRPC.Port] = proxy.GRPC
				lock.Unlock()
				if remove {
					removeGRPCProxy(proxy.GRPC.Port)
				}
				loadGRPCProxy(proxy.GRPC)
			}
		}
	}
}

func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}
