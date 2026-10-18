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

package status

import (
	"errors"
	"fmt"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
)

type StatusHeaderMatch struct {
	Header      string `json:"header"`
	HeaderValue string `json:"value"`
	Present     *bool  `json:"present"`
}

type StatusURIMatch struct {
	Prefix string `json:"prefix"`
	Exact  string `json:"exact"`
	Regex  string `json:"regex"`
	Not    bool   `json:"not"`
	regexp *regexp.Regexp
}

type StatusMatch struct {
	URIMatch      *StatusURIMatch      `json:"uri"`
	HeaderMatches []*StatusHeaderMatch `json:"headers"`
	MethodMatch   []string             `json:"methods"`
}

type StatusConfig struct {
	Port            int          `json:"port"`
	Statuses        []int        `json:"statuses"`
	Times           int          `json:"times"`
	LastStatusIndex int          `json:"lastStatusIndex"`
	LastStatus      int          `json:"lastStatus"`
	StatusCount     int          `json:"statusCount"`
	Match           *StatusMatch `json:"match"`
}

type StatusManager struct {
	PortStatus map[int][]*StatusConfig
	lock       sync.RWMutex
}

func NewStatusManager() *StatusManager {
	return &StatusManager{PortStatus: map[int][]*StatusConfig{}}
}

func newStatusConfig(uriExact, uriPrefix, header, headerValue string, methods []string, statusCodes []int, times int, present bool) *StatusConfig {
	sc := &StatusConfig{
		Statuses: statusCodes,
		Times:    times,
		Match: &StatusMatch{
			URIMatch: &StatusURIMatch{
				Exact:  uriExact,
				Prefix: uriPrefix,
			},
			HeaderMatches: []*StatusHeaderMatch{},
			MethodMatch:   methods,
		},
	}
	header = strings.TrimSpace(header)
	if header != "" {
		sc.Match.HeaderMatches = append(sc.Match.HeaderMatches, &StatusHeaderMatch{
			Header:      header,
			HeaderValue: headerValue,
			Present:     &present,
		})
	}
	return sc
}

func (s *StatusManager) Clear(port int, uriExact, uriPrefix string) {
	if uriExact != "" {
		s.findOrRemoveStatusConfig(port, uriExact, uriPrefix, "", "", false, true)
	} else {
		s.lock.Lock()
		defer s.lock.Unlock()
		s.PortStatus[port] = []*StatusConfig{}
	}
}

func (s *StatusManager) ParseStatusConfig(body io.Reader) (sc *StatusConfig, err error) {
	sc = &StatusConfig{}
	if err = util.ReadJsonPayloadFromBody(body, sc); err != nil {
		return nil, err
	}
	return sc, nil
}

func (s *StatusManager) ParseAndApplyStatusConfig(port int, body io.Reader) (sc *StatusConfig, err error) {
	sc, err = s.ParseStatusConfig(body)
	if err == nil {
		err = s.ApplyStatusConfig(port, sc)
	}
	if err != nil {
		return nil, err
	}
	return sc, nil
}

func (s *StatusManager) ApplyStatusConfig(port int, sc *StatusConfig) (err error) {
	if len(sc.Statuses) == 0 {
		return errors.New("no status")
	}
	for i, s := range sc.Statuses {
		if s < 0 {
			return errors.New("invalid status")
		} else if s == 0 {
			sc.Statuses[i] = 200
		}
	}
	if sc.Times < -1 {
		return errors.New("invalid status times")
	}
	if sc.Times == 0 {
		sc.Times = -1
	}
	if sc.Match == nil {
		sc.Match = &StatusMatch{}
	}
	if sc.Match.URIMatch == nil {
		sc.Match.URIMatch = &StatusURIMatch{}
	}
	if sc.Match.URIMatch.Regex != "" {
		if sc.Match.URIMatch.regexp, err = regexp.Compile(sc.Match.URIMatch.Regex); err != nil {
			return err
		}
	}
	if sc.Match.HeaderMatches == nil {
		sc.Match.HeaderMatches = []*StatusHeaderMatch{}
	}
	t := true
	for _, m := range sc.Match.HeaderMatches {
		if m.Present == nil {
			m.Present = &t
		}
	}
	if sc.Port <= 0 {
		sc.Port = port
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.PortStatus[sc.Port] == nil {
		s.PortStatus[sc.Port] = []*StatusConfig{}
	}
	// The manager keeps a copy. The caller keeps reading the config that it gave, while requests use the one of the
	// manager and count it down.
	s.PortStatus[sc.Port] = append(s.PortStatus[sc.Port], sc.snapshot())
	return nil
}

// snapshot returns a copy of the config, which is safe to read, report and return while requests use the original.
// It must be called with the lock held, or on a config that is not shared yet.
func (s *StatusConfig) snapshot() *StatusConfig {
	cp := *s
	cp.Statuses = slices.Clone(s.Statuses)
	// Match is not changed after a config is created.
	return &cp
}

// Snapshot returns a copy of the configured statuses of all ports, for reporting them. They must not be read
// directly while requests are using them.
func (s *StatusManager) Snapshot() map[int][]*StatusConfig {
	s.lock.RLock()
	defer s.lock.RUnlock()
	snapshot := make(map[int][]*StatusConfig, len(s.PortStatus))
	for port, configs := range s.PortStatus {
		copies := make([]*StatusConfig, len(configs))
		for i, sc := range configs {
			copies[i] = sc.snapshot()
		}
		snapshot[port] = copies
	}
	return snapshot
}

func (s *StatusManager) SetStatus(port int, statusCodes []int, times int, present bool) *StatusConfig {
	return s.SetStatusFor(port, "", "", "", "", nil, statusCodes, times, present)
}

func (s *StatusManager) findOrRemoveStatusConfig(port int, uriExact, uriPrefix, header, headerValue string, present bool, remove bool) *StatusConfig {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.findOrRemoveStatusConfigLocked(port, uriExact, uriPrefix, header, headerValue, present, remove)
}

// findOrRemoveStatusConfigLocked must be called with the lock held, since it removes configs.
func (s *StatusManager) findOrRemoveStatusConfigLocked(port int, uriExact, uriPrefix, header, headerValue string, present bool, remove bool) *StatusConfig {
	statuses := s.PortStatus[port]
	for i, sc := range statuses {
		if strings.EqualFold(sc.Match.URIMatch.Exact, uriExact) && strings.EqualFold(sc.Match.URIMatch.Prefix, uriPrefix) {
			matched := true
			for _, m := range sc.Match.HeaderMatches {
				if !m.matchOne(header, headerValue, present) {
					matched = false
					break
				}
			}
			if matched {
				if remove {
					s.PortStatus[port] = statuses[0:i]
					s.PortStatus[port] = append(s.PortStatus[port], statuses[i+1:]...)
				}
				return sc
			}
		}
	}
	return nil
}

func (s *StatusManager) SetStatusFor(port int, uriExact, uriPrefix, header, headerValue string, methods []string, statusCodes []int, times int, present bool) *StatusConfig {
	uriExact = strings.ToLower(uriExact)
	uriPrefix = strings.ToLower(uriPrefix)
	header = strings.ToLower(header)
	// Requests use the configs while they are being changed, so finding, changing and adding them is done under the
	// lock, as using a status is.
	s.lock.Lock()
	defer s.lock.Unlock()
	sc := s.findOrRemoveStatusConfigLocked(port, uriExact, uriPrefix, header, headerValue, present, false)
	if sc != nil {
		sc.SetStatus(statusCodes, times)
	} else {
		sc = newStatusConfig(uriExact, uriPrefix, header, headerValue, methods, statusCodes, times, present)
		s.PortStatus[port] = append(s.PortStatus[port], sc)
	}
	// A copy, as the caller reports it while requests use the config.
	return sc.snapshot()
}

func (s *StatusManager) GetStatus(port int) (status int, times int) {
	return s.GetStatusFor(port, "", nil, "")
}

// GetStatusFor finds the status that the request gets, and counts the use of it. The first status that matches the
// request and is not used up is the one. A status that is used up is passed over, so that the statuses that were set
// after it apply to the requests that it matches.
func (s *StatusManager) GetStatusFor(port int, uri string, headers map[string][]string, method string) (status int, times int) {
	// Not a read lock, as using a status counts it down.
	s.lock.Lock()
	defer s.lock.Unlock()
	statuses := s.PortStatus[port]
	uri = strings.ToLower(uri)
	lowerHeaders := util.ToLowerHeadersValues(headers)
	for _, sc := range statuses {
		if !sc.isUsedUp() && sc.Match.match(uri, lowerHeaders) {
			return sc.GetStatus()
		}
	}
	return 0, 0
}

// isUsedUp tells if the status was given a number of times, and all of them were used. A status that was set to 0
// is not used up, it never had a status to give. It is left as it was before: it still matches the requests, and
// the statuses that are set after it do not apply to them.
func (s *StatusConfig) isUsedUp() bool {
	return s.Times == 0 && len(s.Statuses) > 0
}

func (sm *StatusMatch) match(uri string, headers map[string]string) bool {
	if sm.URIMatch.Prefix != "" {
		hasPrefix := strings.HasPrefix(uri, sm.URIMatch.Prefix)
		if hasPrefix == sm.URIMatch.Not {
			return false
		}
	}
	if sm.URIMatch.Exact != "" {
		isEqual := strings.EqualFold(uri, sm.URIMatch.Exact)
		if isEqual == sm.URIMatch.Not {
			return false
		}
	}
	if len(sm.HeaderMatches) == 0 {
		return true
	}
	matched := true
	for _, m := range sm.HeaderMatches {
		if !m.match(headers) {
			matched = false
			break
		}
	}
	return matched
}

func (m *StatusHeaderMatch) match(headers map[string]string) bool {
	hv, present := headers[m.Header]
	if !*m.Present {
		return !present
	}
	if !present {
		return false
	}
	if m.HeaderValue == "" {
		return true
	}
	if strings.EqualFold(hv, m.HeaderValue) {
		return true
	}
	return false
}

func (m *StatusHeaderMatch) matchOne(h, hv string, present bool) bool {
	if *m.Present != present {
		return false
	}
	if !strings.EqualFold(h, m.Header) {
		return false
	}
	if !strings.EqualFold(hv, m.HeaderValue) {
		return false
	}
	return true
}

// SetStatus changes the config. It must be called with the lock of the StatusManager held (not a read lock), or on a
// config that is not shared.
func (s *StatusConfig) SetStatus(statusCodes []int, times int) bool {
	if len(statusCodes) > 0 && statusCodes[0] > 0 {
		s.Statuses = statusCodes
		s.Times = -1
		if times >= 1 {
			s.Times = times
		}
	} else {
		s.Statuses = []int{}
		s.Times = 0
	}
	return true
}

// GetStatus gives the status of the config, and counts its use down. Since it changes the config, it must be called
// with the lock of the StatusManager held (not a read lock), or on a config that is not shared.
func (s *StatusConfig) GetStatus() (status int, times int) {
	if s.Times >= 1 || s.Times == -1 {
		if s.Times >= 1 {
			s.Times--
		}
		if len(s.Statuses) == 1 {
			return s.Statuses[0], s.Times
		} else if len(s.Statuses) > 1 {
			return types.RandomFrom(s.Statuses), s.Times
		}
	}
	return 0, 0
}

func (s *StatusConfig) Log(scope string, port int) string {
	statusFor := ""
	notURI := ""
	if s.Match.URIMatch.Not {
		notURI = "Not "
	}
	if s.Match.URIMatch.Prefix != "" {
		statusFor = fmt.Sprintf(" URI:[%sPrefix: %s]", notURI, s.Match.URIMatch.Prefix)
	} else if s.Match.URIMatch.Exact != "" {
		statusFor = fmt.Sprintf(" URI:[%sExact: %s]", notURI, s.Match.URIMatch.Exact)
	}
	if len(s.Match.HeaderMatches) > 0 {
		statusFor = fmt.Sprintf("%s Headers[", statusFor)
		for i, m := range s.Match.HeaderMatches {
			if i > 0 {
				statusFor = fmt.Sprintf("%s AND ", statusFor)
			}
			if m.Header != "" {
				if !*m.Present {
					statusFor = fmt.Sprintf("%s{No Header: %s}", statusFor, m.Header)
				} else {
					statusFor = fmt.Sprintf("%s{Header: %s}", statusFor, m.Header)
				}
			}
		}
		statusFor = fmt.Sprintf("%s]", statusFor)
	}
	msg := ""
	if len(s.Statuses) == 0 || s.Statuses[0] == 0 {
		msg = fmt.Sprintf("%s Port [%d] will respond normally with no forced status", scope, port)
	} else if s.Times > 0 {
		msg = fmt.Sprintf("%s Port [%d] will respond with forced statuses %+v for next [%d] requests", scope, port, s.Statuses, s.Times)
	} else if s.Times == -1 {
		msg = fmt.Sprintf("%s Port [%d] will respond with forced statuses %+v forever", scope, port, s.Statuses)
	} else {
		msg = fmt.Sprintf("%s Port [%d] will respond with no forced status as times count is 0", scope, port)
	}
	if statusFor != "" {
		msg = fmt.Sprintf("%s for %s", msg, statusFor)
	}
	return msg
}
