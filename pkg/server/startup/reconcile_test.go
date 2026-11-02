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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/server/listeners"

	"github.com/sgtdi/fswatcher"
)

// A volume of a config map is a directory of links ("a.yaml" -> "..data/a.yaml") through a link "..data" that points at
// the directory of the current files, "..<version>". An update writes the directory of the new files, replaces "..data"
// with a link to it in one rename, and removes the directory of the old files. Updating a volume is the same thing that
// the kubelet does, and the events of the watcher are the ones that it makes.
type volume struct {
	t       *testing.T
	dir     string
	version int
	current string
	files   map[string]string
}

func newVolume(t *testing.T, files map[string]string) *volume {
	t.Helper()
	v := &volume{t: t, dir: t.TempDir()}
	v.update(files)
	return v
}

// update makes the volume have the files, and gives the directory of the new files and the one of the old ones.
func (v *volume) update(files map[string]string) (newDir, oldDir string) {
	v.t.Helper()
	v.version++
	newDir = filepath.Join(v.dir, fmt.Sprintf("..2026_10_30_21_36_%02d", v.version))
	must := func(err error) {
		v.t.Helper()
		if err != nil {
			v.t.Fatal(err)
		}
	}
	must(os.Mkdir(newDir, 0o755))
	for name, content := range files {
		must(os.WriteFile(filepath.Join(newDir, name), []byte(content), 0o600))
		link := filepath.Join(v.dir, name)
		if _, err := os.Lstat(link); err != nil {
			must(os.Symlink(filepath.Join("..data", name), link))
		}
	}
	tmp := filepath.Join(v.dir, "..data_tmp")
	must(os.Symlink(filepath.Base(newDir), tmp))
	must(os.Rename(tmp, filepath.Join(v.dir, "..data")))
	for name := range v.files {
		if _, kept := files[name]; !kept {
			must(os.Remove(filepath.Join(v.dir, name)))
		}
	}
	oldDir = v.current
	if oldDir != "" {
		must(os.RemoveAll(oldDir))
	}
	v.current, v.files = newDir, files
	return newDir, oldDir
}

// events sends the events that an update makes, in the order that the kubelet makes the changes.
func (v *volume) events(fw *fakeWatcher, newDir, oldDir string) {
	fw.send(newDir, fswatcher.EventCreate)
	fw.send(filepath.Join(v.dir, "..data_tmp"), fswatcher.EventCreate)
	fw.send(filepath.Join(v.dir, "..data"), fswatcher.EventRename)
	if oldDir != "" {
		fw.send(oldDir, fswatcher.EventRemove)
	}
}

func hasListenerConfig(name string) bool {
	lock.Lock()
	defer lock.Unlock()
	return listenerConfigs[name] != nil
}

func TestWatchedUpdateOfAVolumeReloadsOnceAndLeavesUnchangedListenersAlone(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	port, otherPort := newPort(), newPort()
	removeListenersAfter(t, port)
	v := newVolume(t, map[string]string{"ls.yaml": openTCPListenerYAML(port)})
	global.ServerConfig.ConfigPaths = []string{v.dir}
	reconcileConfigs()
	live := mustListener(t, port)
	if !isAccepting(port) {
		t.Fatalf("port %d is not open before the update", port)
	}
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)
	logged := captureLog(t)

	// the update changes another file, and leaves the listener of this one as it is
	newDir, oldDir := v.update(map[string]string{
		"ls.yaml":    openTCPListenerYAML(port),
		"other.yaml": httpYAML(otherPort, "/other", "o"),
	})
	v.events(fw, newDir, oldDir)
	waitFor(t, "the new file to be loaded", func() bool { return hasHTTPConfig("other.yaml") })
	time.Sleep(3 * configDebounce)

	if got := mustListener(t, port); got != live {
		t.Errorf("the listener that the update did not change was replaced: %p, want %p", got, live)
	}
	if !isAccepting(port) {
		t.Errorf("port %d stopped accepting connections", port)
	}
	if n := strings.Count(logged.String(), "Loading configs from path"); n != 1 {
		t.Errorf("the path was loaded %d times for one update, want once", n)
	}
	for _, unwanted := range []string{"Removing Listener", "Removing TLS configs", "Removing Listener configs", "Closing "} {
		if strings.Contains(logged.String(), unwanted) {
			t.Errorf("the update of a volume was logged as %q:\n%s", unwanted, logged.String())
		}
	}
	if !hasListenerConfig("ls.yaml") {
		t.Error("the config of the file that the update kept was lost")
	}
}

func TestWatchedUpdateOfAVolumeAppliesTheChangesOfTheFiles(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	port, added := newPort(), newPort()
	removeListenersAfter(t, port, added)
	v := newVolume(t, map[string]string{"ls.yaml": openTCPListenerYAML(port)})
	global.ServerConfig.ConfigPaths = []string{v.dir}
	reconcileConfigs()
	live := mustListener(t, port)
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	newDir, oldDir := v.update(map[string]string{"ls.yaml": openTCPListenerYAML(port, added)})
	v.events(fw, newDir, oldDir)
	waitFor(t, "the added listener to open", func() bool { return isAccepting(added) })
	if mustListener(t, port) != live {
		t.Error("the listener that the update did not change was replaced")
	}
}

func TestWatchedUpdateOfAVolumeRemovesTheConfigsOfTheFilesThatAreGone(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	port, otherPort := newPort(), newPort()
	removeListenersAfter(t, port)
	v := newVolume(t, map[string]string{
		"ls.yaml":    openTCPListenerYAML(port),
		"other.yaml": httpYAML(otherPort, "/other", "o"),
	})
	global.ServerConfig.ConfigPaths = []string{v.dir}
	reconcileConfigs()
	mustListener(t, port)
	if !hasHTTPConfig("other.yaml") {
		t.Fatal("the file was not loaded before the update")
	}
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	// the key of a file is taken out of the config map: its link is gone, and nothing is said of it but the update
	newDir, oldDir := v.update(map[string]string{"other.yaml": httpYAML(otherPort, "/other", "o")})
	v.events(fw, newDir, oldDir)
	waitFor(t, "the config of the file that is gone to be removed", func() bool { return !hasListenerConfig("ls.yaml") })
	if listeners.GetListenerForPort(port) != nil {
		t.Errorf("the listener of the file that is gone is still there")
	}
	if !portIsFree(port) {
		t.Errorf("port %d is still taken after the file of its listener was gone", port)
	}
	if !hasHTTPConfig("other.yaml") {
		t.Error("the config of the file that is still there was removed")
	}
}

func TestWatchedUpdateOfAVolumeWhoseDirectoryIsGoneRemovesAllTheConfigs(t *testing.T) {
	resetState(t)
	shortDebounce(t)
	port := newPort()
	removeListenersAfter(t, port)
	v := newVolume(t, map[string]string{"ls.yaml": openTCPListenerYAML(port), "other.yaml": httpYAML(newPort(), "/other", "o")})
	global.ServerConfig.ConfigPaths = []string{v.dir}
	reconcileConfigs()
	mustListener(t, port)
	fw := newFakeWatcher()
	watchStartupConfig(fw)
	t.Cleanup(fw.Close)

	if err := os.RemoveAll(v.dir); err != nil {
		t.Fatal(err)
	}
	fw.send(filepath.Join(v.dir, "..data"), fswatcher.EventRemove)
	waitFor(t, "all the configs to be removed", func() bool { return !hasListenerConfig("ls.yaml") && !hasHTTPConfig("other.yaml") })
	if !portIsFree(port) {
		t.Errorf("port %d is still taken after the directory of its file was gone", port)
	}
}

func TestReconcileKeepsTheConfigsWhenAPathCannotBeRead(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "configs")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "a.yaml", httpYAML(newPort(), "/a", "a"))
	global.ServerConfig.ConfigPaths = []string{path}
	reconcileConfigs()
	if !hasHTTPConfig("a.yaml") {
		t.Fatal("the file was not loaded")
	}

	// a path that is a file can't be read as a directory, which tells nothing of its files being gone
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	logged := captureLog(t)
	reconcileConfigs()
	if !hasHTTPConfig("a.yaml") {
		t.Errorf("the config was removed because its path could not be read: %s", logged.String())
	}
	if !strings.Contains(logged.String(), "Keeping the configs that are loaded") {
		t.Errorf("a path that could not be read was not reported: %s", logged.String())
	}
}

func TestReconcileRemovesTheConfigsOfEveryKindOfAFileThatIsGone(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	port := newPort()
	removeListenersAfter(t, port)
	global.ServerConfig.ConfigPaths = []string{dir}
	writeFile(t, dir, "ls.yaml", openTCPListenerYAML(port)+"tls:\n  certs:\n  - name: c\n    domain: c.test\n    autoCert: true\n")
	writeFile(t, dir, "kept.yaml", httpYAML(newPort(), "/kept", "k"))
	reconcileConfigs()
	mustListener(t, port)
	lock.Lock()
	hadTLS := tlsConfigs["ls.yaml"] != nil
	lock.Unlock()
	if !hadTLS {
		t.Fatal("the TLS config of the file was not loaded")
	}

	if err := os.Remove(filepath.Join(dir, "ls.yaml")); err != nil {
		t.Fatal(err)
	}
	reconcileConfigs()
	lock.Lock()
	hasTLS := tlsConfigs["ls.yaml"] != nil
	lock.Unlock()
	if hasTLS || hasListenerConfig("ls.yaml") || listeners.GetListenerForPort(port) != nil {
		t.Errorf("configs of the file that is gone remain: tls %t, listeners %t, listener %t", hasTLS, hasListenerConfig("ls.yaml"), listeners.GetListenerForPort(port) != nil)
	}
	if !hasHTTPConfig("kept.yaml") {
		t.Error("the config of the file that is still there was removed")
	}
}

func TestLoadConfigsFromPathsRunsOneLoadAtATime(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	port := newPort()
	writeFile(t, dir, "a.yaml", httpYAML(port, "/a", "a"))
	global.ServerConfig.ConfigPaths = []string{dir}
	// the loads of the start, of the changes, and of an update of a volume can be asked for at the same time
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if i%2 == 0 {
				reconcileConfigs()
			} else {
				loadConfigsFromPaths(ChangeLog{filepath.Join(dir, "a.yaml"): 0})
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if !hasHTTPConfig("a.yaml") {
		t.Error("the file was not loaded")
	}
}
