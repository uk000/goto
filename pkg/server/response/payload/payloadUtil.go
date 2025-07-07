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

package payload

import (
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/util"
	"io"
	"regexp"
	"strings"
)

func newResponsePayload(payload []byte, binary bool, contentType, uri, header, query, value string,
	bodyRegexes []string, paths []string, transforms []*util.Transform) (*ResponsePayload, error) {
	if contentType == "" {
		contentType = ContentTypeJSON
	}
	_, uriRegExp, responseRouter, err := util.BuildURIMatcher(uri, handleURI)
	if err != nil {
		return nil, fmt.Errorf("failed to add URI match %s with error: %s\n", uri, err.Error())
	}
	headerValueMatch := ""
	headerCaptureKey := ""
	queryValueMatch := ""
	queryCaptureKey := ""
	if util.IsFiller(value) {
		if header != "" {
			headerCaptureKey = value
		} else if query != "" {
			queryCaptureKey = value
		}
	} else if header != "" {
		headerValueMatch = value
	} else if query != "" {
		queryValueMatch = value
	}

	jsonPaths := util.NewJSONPath().Parse(paths)

	var bodyMatchRegexp *regexp.Regexp
	if len(bodyRegexes) > 0 {
		bodyMatchRegexp = regexp.MustCompile("(?i)" + strings.Join(bodyRegexes, ".*") + ".*")
	}

	var fillers []string
	if !binary {
		fillers = util.GetFillersUnmarked(string(payload))
	}
	for _, t := range transforms {
		for _, m := range t.Mappings {
			m.Init()
		}
	}

	return &ResponsePayload{
		Payload:          payload,
		ContentType:      contentType,
		isBinary:         util.IsBinaryContentType(contentType),
		URIMatch:         uri,
		HeaderMatch:      header,
		HeaderValueMatch: headerValueMatch,
		QueryMatch:       query,
		QueryValueMatch:  queryValueMatch,
		BodyMatch:        bodyRegexes,
		BodyPaths:        jsonPaths.TextPaths,
		uriRegexp:        uriRegExp,
		queryMatchRegexp: regexp.MustCompile("(?i)" + query),
		bodyMatchRegexp:  bodyMatchRegexp,
		bodyJsonPaths:    jsonPaths.Paths,
		URICaptureKeys:   util.GetFillersUnmarked(uri),
		HeaderCaptureKey: headerCaptureKey,
		QueryCaptureKey:  queryCaptureKey,
		Transforms:       transforms,
		fillers:          fillers,
		router:           responseRouter,
	}, nil
}

func getPayloadForBodyMatch(bodyReader io.ReadCloser, bodyMatchResponses map[string]*ResponsePayload) (newBodyReader io.ReadCloser, matchedResponsePayload *ResponsePayload, captures map[string]string, matched bool) {
	if len(bodyMatchResponses) == 0 {
		return nil, nil, nil, false
	}
	body := util.Read(bodyReader)
	lowerBody := strings.ToLower(body)
	for _, rp := range bodyMatchResponses {
		if rp.bodyMatchRegexp != nil && rp.bodyMatchRegexp.MatchString(lowerBody) {
			matchedResponsePayload = rp
			break
		} else if len(rp.bodyJsonPaths) > 0 {
			allMatched := true
			captures = map[string]string{}
			var data map[string]interface{}
			if err := util.ReadJson(body, &data); err == nil {
				for key, jp := range rp.bodyJsonPaths {
					if matches, err := jp.FindResults(data); err == nil && len(matches) > 0 && len(matches[0]) > 0 {
						if key != "" {
							captures[key] = fmt.Sprintf("%v", matches[0][0].Interface())
						}
					} else {
						allMatched = false
						break
					}
				}
			} else {
				allMatched = false
				break
			}
			if allMatched {
				matchedResponsePayload = rp
				break
			}
		}
	}
	newBodyReader = io.NopCloser(strings.NewReader(body))
	if matchedResponsePayload != nil {
		return newBodyReader, matchedResponsePayload, captures, true
	}
	return nil, nil, nil, false
}

func getPayloadForKV(kvMap map[string][]string, payloadMap map[string]map[string]*ResponsePayload) (*ResponsePayload, bool) {
	if len(kvMap) == 0 || len(payloadMap) == 0 {
		return nil, false
	}
	for k, kv := range kvMap {
		k = strings.ToLower(k)
		if payloadMap[k] != nil {
			for _, v := range kv {
				v = strings.ToLower(v)
				if p, found := payloadMap[k][v]; found {
					return p, found
				}
			}
			if p, found := payloadMap[k][""]; found {
				return p, found
			}
		}
	}
	return nil, false
}

func fixPayload(payload []byte, size int) []byte {
	if len(payload) == 0 && size > 0 {
		payload = util.GenerateRandomPayload(size)
	} else if len(payload) > size {
		payload = payload[:size]
	}
	return payload
}
