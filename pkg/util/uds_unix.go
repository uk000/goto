//go:build !windows

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
	"net"
	"sync"
	"syscall"
)

// umaskLock serializes the changes to the umask of the process, which is not specific to a goroutine.
var umaskLock sync.Mutex

// listenUnixRestricted listens on the socket path with a umask that has the socket file created open to its owner only,
// as it would be created with wider permissions and be open to others until they are set. The umask is as narrow as it
// can be (group and others), as it applies to the files that other goroutines create in the meantime too.
func listenUnixRestricted(path string) (net.Listener, error) {
	umaskLock.Lock()
	defer umaskLock.Unlock()
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	return net.Listen("unix", path)
}

// isConnRefused tells if the error of connecting is the refusal of the connection.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
