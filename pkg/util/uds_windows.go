//go:build windows

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
	"syscall"
)

// wsaeConnRefused is the Windows Sockets error of a refused connection.
const wsaeConnRefused = syscall.Errno(10061)

// listenUnixRestricted listens on the socket path. The umask is not a thing on Windows.
func listenUnixRestricted(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

// isConnRefused tells if the error of connecting is the refusal of the connection.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeConnRefused)
}
