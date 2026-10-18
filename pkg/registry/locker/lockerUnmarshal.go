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

package locker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// The marshalers in locker.go flatten the lockers (sub-keys appear next to "data", peer locker entries appear next to
// "instanceLockers", etc.), so the default decoder cannot read a dump back into the locker types. The unmarshalers
// below are the inverse of those marshalers, and are used to load a registry dump or to clone a registry.

func isNullOrEmptyJSON(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null")) || bytes.Equal(t, []byte(`""`))
}

// UnmarshalJSON reads a top-level locker entry. Sub-keys are read recursively with their respective levels.
func (ld *LockerData) UnmarshalJSON(b []byte) error {
	return ld.unmarshal(b, 1)
}

func (ld *LockerData) unmarshal(b []byte, level int) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	ld.level = level
	ld.Data = nil
	ld.SubKeys = Locker{}
	for key, raw := range fields {
		if key == "data" {
			var data any
			if err := json.Unmarshal(raw, &data); err != nil {
				return fmt.Errorf("key [data]: %w", err)
			}
			if m, ok := data.(map[string]any); ok {
				// Data is stored as text. An object here comes from a parsed view of the data, so turn it back to text.
				if text, err := json.Marshal(m); err == nil {
					data = string(text)
				}
			}
			ld.Data = data
			continue
		}
		if level == 1 && (key == "firstReported" || key == "lastReported") {
			// Timestamps are reported only for the top-level entries. They are told apart from a sub-key of
			// the same name by their value being a timestamp.
			var t time.Time
			if err := json.Unmarshal(raw, &t); err == nil {
				if key == "firstReported" {
					ld.FirstReported = t
				} else {
					ld.LastReported = t
				}
				continue
			}
		}
		if isNullOrEmptyJSON(raw) {
			ld.SubKeys[key] = nil
			continue
		}
		sub := &LockerData{}
		if err := sub.unmarshal(raw, level+1); err != nil {
			return fmt.Errorf("key [%s]: %w", key, err)
		}
		ld.SubKeys[key] = sub
	}
	return nil
}

// UnmarshalJSON reads a data locker, which is marshaled as just its locker map.
func (dl *DataLocker) UnmarshalJSON(b []byte) error {
	locker := Locker{}
	if err := json.Unmarshal(b, &locker); err != nil {
		return err
	}
	dl.lock.Lock()
	defer dl.lock.Unlock()
	dl.Locker = locker
	dl.Active = true
	return nil
}

// UnmarshalJSON reads a peer locker. This is defined explicitly, so that the UnmarshalJSON of the embedded
// DataLocker isn't promoted and used to read the whole peer locker.
func (pl *PeerLocker) UnmarshalJSON(b []byte) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	locker := Locker{}
	instanceLockers := DataLockers{}
	for key, raw := range fields {
		if key == "instanceLockers" {
			if err := json.Unmarshal(raw, &instanceLockers); err != nil {
				return fmt.Errorf("key [instanceLockers]: %w", err)
			}
			continue
		}
		if isNullOrEmptyJSON(raw) {
			locker[key] = nil
			continue
		}
		ld := &LockerData{}
		if err := ld.unmarshal(raw, 1); err != nil {
			return fmt.Errorf("key [%s]: %w", key, err)
		}
		locker[key] = ld
	}
	for label, il := range instanceLockers {
		if il == nil {
			instanceLockers[label] = newDataLocker()
		}
	}
	pl.lock.Lock()
	defer pl.lock.Unlock()
	pl.InstanceLockers = instanceLockers
	pl.DataLocker.lock.Lock()
	defer pl.DataLocker.lock.Unlock()
	pl.DataLocker.Locker = locker
	pl.DataLocker.Active = true
	return nil
}

// UnmarshalJSON reads a locker with its peer lockers and sub-lockers. The parent link and the path label of the
// locker and its sub-lockers are not part of the json, and get restored by LabeledLockers.ReplaceLockers.
func (cl *CombiLocker) UnmarshalJSON(b []byte) error {
	raw := struct {
		Label       string                 `json:"label"`
		DataLocker  Locker                 `json:"dataLocker"`
		PeerLockers map[string]*PeerLocker `json:"peerLockers"`
		SubLockers  CombiLockers           `json:"subLockers"`
		Current     bool                   `json:"current"`
		FirstOpened time.Time              `json:"firstOpened"`
		LastOpened  time.Time              `json:"lastOpened"`
	}{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.DataLocker == nil {
		raw.DataLocker = Locker{}
	}
	if raw.PeerLockers == nil {
		raw.PeerLockers = map[string]*PeerLocker{}
	}
	for peer, pl := range raw.PeerLockers {
		if pl == nil {
			raw.PeerLockers[peer] = newPeerLocker()
		}
	}
	if raw.SubLockers == nil {
		raw.SubLockers = CombiLockers{}
	}
	cl.lock.Lock()
	defer cl.lock.Unlock()
	cl.Label = raw.Label
	cl.PathLabel = raw.Label
	cl.PeerLockers = raw.PeerLockers
	cl.DataLocker = newDataLocker()
	cl.DataLocker.Locker = raw.DataLocker
	cl.SubLockers = raw.SubLockers
	cl.Current = raw.Current
	cl.FirstOpened = raw.FirstOpened
	cl.LastOpened = raw.LastOpened
	return nil
}
