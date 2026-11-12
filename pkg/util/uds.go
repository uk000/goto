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

package util

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// udsPerm is the permissions of the socket files of the listeners.
const udsPerm os.FileMode = 0660

// udsProbeTimeout is how long to wait for the connection that checks whether an existing socket file is in use.
const udsProbeTimeout = time.Second

// udsPorts maps the socket path of each unix domain socket listener to the port that identifies the listener, since the
// connections accepted on a socket have no port in their local address.
var udsPorts sync.Map

// ListenUDS opens a unix domain socket listener at the given path, replacing a stale socket file if present. A socket
// file that something still accepts connections on is not replaced, as that would take the path from a running process.
func ListenUDS(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("empty UDS path")
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("path [%s] exists and is not a socket", path)
		}
		if err := checkSocketIsStale(path); err != nil {
			return nil, err
		}
		os.Remove(path)
	}
	listener, err := listenUnixRestricted(path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, udsPerm); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// checkSocketIsStale fails if the socket file at the path is, or may be, in use. It's stale only if connecting to it is
// refused, which is what a socket file that nothing listens on does (the process that had it is gone).
func checkSocketIsStale(path string) error {
	c, err := net.DialTimeout("unix", path, udsProbeTimeout)
	if err == nil {
		c.Close()
		return fmt.Errorf("path [%s] is a socket that is in use", path)
	}
	if isConnRefused(err) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("path [%s] is a socket that can't be told to be stale: %s", path, err.Error())
}

// IsUnixAddr tells if the address is that of a unix domain socket.
func IsUnixAddr(addr net.Addr) bool {
	_, ok := addr.(*net.UnixAddr)
	return ok
}

// RegisterUDSPort makes the port the one of the connections accepted on the socket path.
func RegisterUDSPort(path string, port int) {
	udsPorts.Store(path, port)
}

// UnregisterUDSPort forgets the port of the socket path, unless the path has been registered for another port since.
func UnregisterUDSPort(path string, port int) {
	udsPorts.CompareAndDelete(path, port)
}

// GetUDSPort gives the port of the listener on the unix domain socket that the address is of, or 0 if it's not such an address.
func GetUDSPort(addr net.Addr) int {
	if addr != nil && IsUnixAddr(addr) {
		if port, ok := udsPorts.Load(addr.String()); ok {
			return port.(int)
		}
	}
	return 0
}
