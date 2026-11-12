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
	"os"
	"syscall"
	"testing"
)

func TestListenUDSLeavesUmaskOfProcessAlone(t *testing.T) {
	const umask = 022
	previous := syscall.Umask(umask)
	defer syscall.Umask(previous)
	ln, err := ListenUDS(udsTestPath(t, "m.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := syscall.Umask(umask); got != umask {
		t.Errorf("umask after ListenUDS = %o, want %o", got, umask)
	}
}

func TestListenUDSCreatesSocketClosedToOthers(t *testing.T) {
	// the file is created closed to the group and others, not made so by a change that comes after
	path := udsTestPath(t, "p.sock")
	previous := syscall.Umask(0)
	defer syscall.Umask(previous)
	ln, err := listenUnixRestricted(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("permissions of a new socket = %o, want none for the group and others without waiting for a chmod", perm)
	}
}
