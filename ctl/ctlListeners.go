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

package ctl

import (
	"goto/pkg/server/listeners"
	"log"
)

type Listeners map[int]*listeners.Listener

func processListeners(ll Listeners) {
	if len(ll) == 0 {
		log.Println("No Listeners to configure")
		return
	}
	for port, l := range ll {
		l.Port = port
		_, msg := listeners.AddOrUpdateListener(l)
		log.Println(msg)
	}
}
