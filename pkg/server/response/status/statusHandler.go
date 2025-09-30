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

package status

import (
	"fmt"
	"goto/pkg/types"
	"sync"
)

type StatusConfig struct {
	statuses []int
	times    int
}

type StatusManager struct {
	Statuses map[int]*StatusConfig
	lock     sync.RWMutex
}

func NewStatusManager() *StatusManager {
	return &StatusManager{Statuses: map[int]*StatusConfig{}}
}

func (s *StatusManager) SetStatus(port int, statusCodes []int, times int) *StatusConfig {
	s.lock.Lock()
	defer s.lock.Unlock()
	status := s.Statuses[port]
	if status == nil {
		status = &StatusConfig{}
		s.Statuses[port] = status
	}
	status.SetStatus(statusCodes, times)
	return status
}

func (s *StatusManager) GetStatus(port int) (int, int) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	status := s.Statuses[port]
	if status != nil {
		return status.GetStatus()
	}
	return 0, 0
}

func (s *StatusConfig) SetStatus(statusCodes []int, times int) {
	if len(statusCodes) > 0 && statusCodes[0] > 0 {
		s.statuses = statusCodes
		s.times = -1
		if times >= 1 {
			s.times = times
		}
	} else {
		s.statuses = []int{}
		s.times = 0
	}
}

func (s *StatusConfig) GetStatus() (int, int) {
	if s.times >= 1 || s.times == -1 {
		if s.times >= 1 {
			s.times--
		}
		if len(s.statuses) == 1 {
			return s.statuses[0], s.times
		} else if len(s.statuses) > 1 {
			return types.RandomFrom(s.statuses), s.times
		}
	}
	return 0, 0
}

func (s *StatusConfig) Log(scope string, port int) string {
	if s.times > 0 {
		return fmt.Sprintf("%s Port [%d] will respond with forced statuses %+v for next [%d] requests", scope, port, s.statuses, s.times)
	} else if s.times == -1 {
		return fmt.Sprintf("%s Port [%d] will respond with forced statuses %+v forever", scope, port, s.statuses)
	} else {
		return fmt.Sprintf("%s Port [%d] will respond normally", scope, port)
	}
}
