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
	// lock guards the maps of the configs, and loaded.
	lock = sync.Mutex{}
	// configLock makes loading or removing the configs of a file one step at a time: the changes of the files come from
	// the goroutines of the watchers, and the debounced loads, which would otherwise read the maps while another changes
	// them. It is taken before lock.
	configLock sync.Mutex
	// loadLock makes loading the config paths one load at a time: the loads come from the start, and from the debounced
	// reactions to the changes of the files, which would otherwise read and apply the files of the paths at the same time.
	loadLock sync.Mutex
	// watchersLock guards configWatchers.
	watchersLock sync.Mutex
	// configDebounce is how long the changes of the config files are waited on before they are loaded or removed: an
	// editor or a copy makes a burst of events for one change.
	configDebounce = 5 * time.Second
)

type ChangeLog map[string]any

// Start loads the startup configs from the configured config paths, once, on the first call.
func Start() {
	lock.Lock()
	first := !loaded
	loaded = true
	lock.Unlock()
	if first {
		loadStartupConfigs()
	}
	//runStartupScript()
}

// Stop closes the file watchers that were set on the config paths.
func Stop() {
	watchersLock.Lock()
	watchers := configWatchers
	configWatchers = nil
	watchersLock.Unlock()
	for _, cw := range watchers {
		// A watcher is started by a goroutine of its own, so it may not be running yet, and it has to be closed all the same
		// or it starts after this. Closing a watcher that is closed already, or never started, does nothing.
		if cw != nil {
			cw.Close()
		}
	}
}

// loadStartupConfigs loads the configs from the config paths and sets a file watcher on each path to reload on changes.
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
				watchersLock.Lock()
				configWatchers = append(configWatchers, configWatcher)
				watchersLock.Unlock()
			}
		}
	}
}

// isAtomicUpdateEntry tells if the path is one of the entries that a volume of a config map is updated with. An update
// puts the new files in a directory named "..<timestamp>", points the link "..data" at it, and removes the directory of the
// old files; the files themselves are links through "..data" that stay where they are. So these entries say that files
// may have changed, and the removal of one of them says nothing about any file being removed.
func isAtomicUpdateEntry(path string) bool {
	return strings.HasPrefix(filepath.Base(path), "..")
}

// watchStartupConfig reacts to the events of a config path watcher: removals clean up the configs, other changes are loaded after a delay.
func watchStartupConfig(configWatcher fswatcher.Watcher) {
	interval := configDebounce
	debounceReconcile := util.Debounce(interval)
	changeChan := make(chan string, 10)
	go loadChanges(changeChan, interval)
	go configWatcher.Watch(context.Background())
	go func() {
		// the loader of the changes has nothing more to wait for once the watcher is closed
		defer close(changeChan)
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
			if isAtomicUpdateEntry(event.Path) {
				// The entries of one update (the new directory, the swap of "..data", the removal of the old directory)
				// all come to the same reconciliation, which runs once. It must not be one step for the removals and
				// another for the rest: they would run at the same time, and could wipe the configs that the other loads.
				debounceReconcile(reconcileConfigs)
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

// reconcileConfigs makes the configs that are loaded the ones of the files that are there: every file is loaded again,
// and the configs of the files that are not there anymore are removed. A listener that a file keeps unchanged is not
// touched by it.
func reconcileConfigs() {
	loadConfigsFromPaths(nil)
	removeConfigsOfMissingFiles()
}

// removeConfigsOfMissingFiles removes the configs of the files that are not in any of the config paths. A path that
// can't be read for any reason but not being there says nothing of its files, so nothing is removed then.
func removeConfigsOfMissingFiles() {
	present := map[string]bool{}
	for _, configPath := range global.ServerConfig.ConfigPaths {
		files, err := os.ReadDir(configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("Failed to read config path [%s] with error: %s. Keeping the configs that are loaded.\n", configPath, err.Error())
			return
		}
		for _, file := range files {
			present[file.Name()] = true
		}
	}
	for _, filename := range loadedConfigFiles() {
		if !present[filename] {
			removeConfigs(filename)
		}
	}
}

// loadedConfigFiles gives the names of the files that have a config of any kind loaded.
func loadedConfigFiles() []string {
	lock.Lock()
	defer lock.Unlock()
	names := map[string]bool{}
	for name := range tlsConfigs {
		names[name] = true
	}
	for name := range listenerConfigs {
		names[name] = true
	}
	for name := range a2aConfigs {
		names[name] = true
	}
	for name := range mcpConfigs {
		names[name] = true
	}
	for name := range httpProxyConfigs {
		names[name] = true
	}
	for name := range tcpProxyConfigs {
		names[name] = true
	}
	for name := range grpcProxyConfigs {
		names[name] = true
	}
	for name := range httpConfigs {
		names[name] = true
	}
	for name := range grpcConfigs {
		names[name] = true
	}
	files := make([]string, 0, len(names))
	for name := range names {
		files = append(files, name)
	}
	sort.Strings(files)
	return files
}

// removeAllConfigs clears every loaded config of every kind, and forgets them.
func removeAllConfigs() {
	configLock.Lock()
	defer configLock.Unlock()
	lock.Lock()
	defer lock.Unlock()
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

// removeConfigs clears and forgets all the configs of every kind that were loaded from the given file.
func removeConfigs(filePath string) {
	parts := strings.Split(filePath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	configLock.Lock()
	defer configLock.Unlock()
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

// Contains tells if the log has the given path, or a path that contains it or is contained by it.
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

// loadConfigsFromPaths reads the files of the config paths (only the changed ones if a change log is given) and loads them in the order of their configs.
func loadConfigsFromPaths(changeLog ChangeLog) {
	loadLock.Lock()
	defer loadLock.Unlock()
	for _, configPath := range global.ServerConfig.ConfigPaths {
		log.Printf("Loading configs from path [%s]", configPath)
		files, err := os.ReadDir(configPath)
		if err != nil {
			log.Printf("Failed to read config path [%s] with error: %s\n", configPath, err.Error())
			continue
		}
		configs := []*ctl.GotoConfig{}
		// the files are told from the changes that there were when the path was started: the changes are taken off the
		// log as they are loaded, and the log being empty then doesn't make the files that follow changed
		onlyChanged := len(changeLog) > 0
		for _, file := range files {
			filePath := filepath.Join(configPath, file.Name())
			if !onlyChanged || changeLog.Contains(filePath) {
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

// loadChanges collects the changed paths from the channel and loads them once the changes stop coming, after the debounce interval.
func loadChanges(changeChan chan string, interval time.Duration) {
	debounce := util.Debounce(interval)
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

// getFileConfig reads the config of a YAML file, or of the output of a shell script, and ignores any other file.
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

// readConfigFromScript runs the script and parses its output as a config.
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

// loadConfig loads each section of a config (TLS, listeners, MCP, A2A, HTTP, gRPC, proxies), one section failing not stopping the others.
func loadConfig(config *ctl.GotoConfig) {
	if config == nil {
		log.Println("No config loaded")
		return
	}
	parts := strings.Split(config.Filepath, string(os.PathSeparator))
	filename := parts[len(parts)-1]
	configLock.Lock()
	defer configLock.Unlock()
	guarded("TLS", filename, func() { loadTLSConfig(filename, config) })
	guarded("Listeners", filename, func() { loadListenersConfig(filename, config) })
	guarded("MCP", filename, func() { loadMCPConfig(filename, config) })
	guarded("A2A", filename, func() { loadA2AConfig(filename, config) })
	guarded("HTTP", filename, func() { loadHTTPConfig(filename, config) })
	guarded("gRPC", filename, func() { loadGRPCConfig(filename, config) })
	guarded("Proxy", filename, func() { loadProxyConfig(filename, config) })
}

// guarded loads a section of a config. The sections are read from files that people write and change while the server
// runs, and the code that reads them trusts the type of what it finds (a name that is a number, a schema that is a
// list): a section that can't be read must not take the server down, nor keep the other sections from loading.
func guarded(section, filename string, load func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[*** ERROR ***] Failed to load %s configs of file [%s]: %v\n", section, filename, r)
		}
	}()
	load()
}

// loadTLSConfig loads the TLS section of a config, replacing only the certs that changed since the file was last loaded.
func loadTLSConfig(filename string, config *ctl.GotoConfig) {
	if config.TLS != nil {
		old := tlsConfigs[filename]
		// The certs that did not change are not cleared and loaded again: that restarts their listeners, which serve
		// without their certs in between. The CAs are, as nothing serves from them.
		toClear, toLoad := diffCerts(old, config.TLS)
		if old != nil {
			clearCerts(toClear)
			clearCAs(old.CACerts)
		}
		lock.Lock()
		tlsConfigs[filename] = config.TLS
		lock.Unlock()
		processTLSCerts(config.TLS, toLoad)
	}
}

// loadListenersConfig loads the listeners section of a config, removing the listeners that the file no longer has.
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

// loadMCPConfig loads the MCP section of a config, replacing the servers that the file loaded before.
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

// loadA2AConfig loads the A2A section of a config, replacing the agents that the file loaded before.
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

// loadHTTPConfig loads the HTTP section of a config, replacing the response payloads that the file loaded before.
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

// loadGRPCConfig loads the gRPC section of a config, replacing the protos and services that the file loaded before.
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

// loadProxyConfig loads the HTTP, TCP and gRPC proxies of a config, replacing the proxy that a file had loaded before on the same port.
func loadProxyConfig(filename string, config *ctl.GotoConfig) {
	if config.Proxies != nil {
		for _, proxy := range config.Proxies {
			if proxy == nil {
				continue
			}
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

// runStartupScript runs the startup script commands of the server config, if any.
func runStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}
