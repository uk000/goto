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

package cmd

import (
	"flag"
	"goto/ctl"
	"goto/pkg/client"
	"goto/pkg/global"
	"goto/pkg/server"
	"log"
)

func Execute() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("Version: %s, Commit: %s\n", global.Version, global.Commit)

	setupCtlArgs()
	setupClientArgs()
	setupServerArgs()
	flag.Parse()
	args := flag.Args()
	if len(args) > 0 {
		global.CmdConfig.CmdCtlMode = true
		ctl.Ctl(args)
	} else if global.CmdConfig.CmdClientMode {
		processClientArgs()
		client.Run()
	} else {
		processServerArgs()
		server.Run()
	}
}
