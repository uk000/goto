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

package util

import (
	"fmt"
	"net"
	"os"
)

// ListenUDS opens a unix domain socket listener at the given path, replacing a stale socket file if present.
func ListenUDS(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("empty UDS path")
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("path [%s] exists and is not a socket", path)
		}
		os.Remove(path)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0660); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// IsUnixAddr checks if the given address is a unix domain socket address.
func IsUnixAddr(addr net.Addr) bool {
	_, ok := addr.(*net.UnixAddr)
	return ok
}
