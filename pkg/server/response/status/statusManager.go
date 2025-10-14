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
	"errors"
	"fmt"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"regexp"
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
}

type StatusConfig struct {
	Port     int          `json:"port"`
	Statuses []int        `json:"statuses"`
	Times    int          `json:"times"`
	Match    *StatusMatch `json:"match"`
}

type StatusManager struct {
	Statuses map[int][]*StatusConfig
	lock     sync.RWMutex
}

func NewStatusManager() *StatusManager {
	return &StatusManager{Statuses: map[int][]*StatusConfig{}}
}

func newStatusConfig(uriPrefix, header, headerValue string, statusCodes []int, times int, present bool) *StatusConfig {
	return &StatusConfig{
		Statuses: statusCodes,
		Times:    times,
		Match: &StatusMatch{
			URIMatch: &StatusURIMatch{
				Prefix: uriPrefix,
			},
			HeaderMatches: []*StatusHeaderMatch{
				{
					Header:      header,
					HeaderValue: headerValue,
					Present:     &present,
				},
			},
		},
	}
}

func (s *StatusManager) Clear(port int) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.Statuses[port] = []*StatusConfig{}
}

func (s *StatusManager) ParseStatusConfig(port int, body io.Reader) (sc *StatusConfig, err error) {
	sc = &StatusConfig{}
	if err = util.ReadJsonPayloadFromBody(body, sc); err != nil {
		return nil, err
	}
	if len(sc.Statuses) == 0 {
		return nil, errors.New("no status")
	}
	for i, s := range sc.Statuses {
		if s < 0 {
			return nil, errors.New("invalid status")
		} else if s == 0 {
			sc.Statuses[i] = 200
		}
	}
	if sc.Times < -1 {
		return nil, errors.New("invalid status times")
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
			return nil, err
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
	if s.Statuses[sc.Port] == nil {
		s.Statuses[sc.Port] = []*StatusConfig{}
	}
	s.Statuses[sc.Port] = append(s.Statuses[sc.Port], sc)
	return sc, nil
}

func (s *StatusManager) SetStatus(port int, statusCodes []int, times int, present bool) *StatusConfig {
	return s.SetStatusFor(port, "", "", "", statusCodes, times, present)
}

func (s *StatusManager) findStatusConfig(port int, uriPrefix, header, headerValue string, present bool) *StatusConfig {
	s.lock.RLock()
	defer s.lock.RUnlock()
	statuses := s.Statuses[port]
	for _, sc := range statuses {
		if !strings.EqualFold(sc.Match.URIMatch.Prefix, uriPrefix) {
			continue
		}
		matched := true
		for _, m := range sc.Match.HeaderMatches {
			if !m.matchOne(header, headerValue, present) {
				matched = false
				break
			}
		}
		if matched {
			return sc
		}
	}
	return nil
}

func (s *StatusManager) SetStatusFor(port int, uriPrefix, header, headerValue string, statusCodes []int, times int, present bool) *StatusConfig {
	s.lock.Lock()
	statuses := s.Statuses[port]
	if statuses == nil {
		statuses = []*StatusConfig{}
		s.Statuses[port] = statuses
	}
	s.lock.Unlock()
	uriPrefix = strings.ToLower(uriPrefix)
	header = strings.ToLower(header)
	headerValue = strings.ToLower(headerValue)
	sc := s.findStatusConfig(port, uriPrefix, header, headerValue, present)
	if sc != nil {
		sc.SetStatus(statusCodes, times)
	} else {
		sc = newStatusConfig(uriPrefix, header, headerValue, statusCodes, times, present)
		s.Statuses[port] = append(s.Statuses[port], sc)
	}
	return sc
}

func (s *StatusManager) GetStatus(port int) (int, int) {
	return s.GetStatusFor(port, "", nil)
}

func (s *StatusManager) GetStatusFor(port int, uri string, headers map[string][]string) (int, int) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	statuses := s.Statuses[port]
	uri = strings.ToLower(uri)
	lowerHeaders := util.ToLowerHeadersValues(headers)
	for _, sc := range statuses {
		if sc.Match.match(uri, lowerHeaders) {
			return sc.GetStatus()
		}
	}
	return 0, 0
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

func (s *StatusConfig) GetStatus() (int, int) {
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
