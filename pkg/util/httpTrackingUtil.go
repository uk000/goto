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
	"strconv"
	"strings"
)

func ParseTrackingHeaders(headers string) ([]string, map[string][]string) {
	trackingHeaders := []string{}
	crossTrackingHeaders := map[string][]string{}
	pieces := strings.Split(headers, ",")
	for _, piece := range pieces {
		crossHeaders := strings.Split(piece, "|")
		for i, h := range crossHeaders {
			crossHeaders[i] = strings.ToLower(h)
		}
		if len(crossHeaders) > 1 {
			crossTrackingHeaders[crossHeaders[0]] = append(crossTrackingHeaders[crossHeaders[0]], crossHeaders[1:]...)
		}
		for _, h := range crossHeaders {
			exists := false
			for _, eh := range trackingHeaders {
				if strings.EqualFold(h, eh) {
					exists = true
				}
			}
			if !exists {
				trackingHeaders = append(trackingHeaders, strings.ToLower(h))
			}
		}
	}
	return trackingHeaders, crossTrackingHeaders
}

func ParseTimeBuckets(b string) ([][]int, bool) {
	pieces := strings.Split(b, ",")
	buckets := [][]int{}
	var e error
	hasError := false
	for _, piece := range pieces {
		bucket := strings.Split(piece, "-")
		low := 0
		high := 0
		if len(bucket) == 2 {
			if low, e = strconv.Atoi(bucket[0]); e == nil {
				high, e = strconv.Atoi(bucket[1])
			}
		}
		if e != nil || low < 0 || high < 0 || (low == 0 && high == 0) || (high != 0 && high < low) {
			hasError = true
			break
		} else {
			buckets = append(buckets, []int{low, high})
		}
	}
	return buckets, !hasError
}

func BuildCrossHeadersMap(crossTrackingHeaders map[string][]string) map[string]string {
	crossHeadersMap := map[string]string{}
	for header, subheaders := range crossTrackingHeaders {
		for _, subheader := range subheaders {
			crossHeadersMap[subheader] = header
		}
	}
	return crossHeadersMap
}

func UpdateTrackingCountsByURIAndID(id string, uri string,
	countsByIDs map[string]int,
	countsByURIs map[string]int,
	countsByURIIDs map[string]map[string]int,
) {
	if countsByIDs != nil {
		countsByIDs[id]++
	}
	if countsByURIs != nil {
		countsByURIs[uri]++
	}
	if countsByURIIDs != nil {
		if countsByURIIDs[uri] == nil {
			countsByURIIDs[uri] = map[string]int{}
		}
		countsByURIIDs[uri][id]++
	}
}

func UpdateTrackingCountsByURIKeyValuesID(id string, uri string,
	trackingKeys []string,
	actualKeyValues map[string][]string,
	countsByKeys map[string]int,
	countsByKeyValues map[string]map[string]int,
	countsByURIKeys map[string]map[string]int,
	countsByKeyIDs map[string]map[string]int,
) {
	if trackingKeys == nil {
		return
	}
	for _, key := range trackingKeys {
		if values := actualKeyValues[key]; len(values) > 0 {
			if countsByKeys != nil {
				countsByKeys[key]++
			}
			if countsByKeyValues != nil {
				if countsByKeyValues[key] == nil {
					countsByKeyValues[key] = map[string]int{}
				}
				countsByKeyValues[key][values[0]]++
			}
			if countsByURIKeys != nil {
				if countsByURIKeys[uri] == nil {
					countsByURIKeys[uri] = map[string]int{}
				}
				countsByURIKeys[uri][key]++
			}
			if countsByURIKeys != nil {
				if countsByURIKeys[uri] == nil {
					countsByURIKeys[uri] = map[string]int{}
				}
				countsByURIKeys[uri][key]++
			}
			if countsByKeyIDs != nil {
				if countsByKeyIDs[key] == nil {
					countsByKeyIDs[key] = map[string]int{}
				}
				countsByKeyIDs[key][id]++
			}
		}
	}
}
