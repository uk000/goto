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
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"strconv"
	"strings"
	"time"
)

type CtlArgs struct {
	ContextFile string
	Context     string
	ConfigFile  string
	Name        string
	Remote      string
}

type ClientArgs struct {
	ClientMode   string
	Protocol     string
	URLs         string
	Headers      string
	RequestCount string
	Parallel     string
	Method       string
	Payload      string
	AutoPayload  string
	Delay        string
	Retries      string
	RetryDelay   string
	RetryOn      string
	Persist      string
	Verbose      string
}

type ServerArgs struct {
	Port                string
	GRPCPort            string
	MCPPort             string
	Ports               string
	Label               string
	StartupDelay        string
	ShutdownDelay       string
	Registry            string
	Locker              string
	Events              string
	PublishEvents       string
	Certs               string
	ServerLogs          string
	AdminLogs           string
	MetricsLogs         string
	ProbeLogs           string
	ClientLogs          string
	InvocationLogs      string
	RegistryLogs        string
	LockerLogs          string
	EventsLogs          string
	ReminderLogs        string
	PeerHealthLogs      string
	ProxyDebugLogs      string
	LogRequestHeaders   string
	LogRequestBody      string
	LogRPCRequestBody   string
	LogRequestMiniBody  string
	LogResponseHeaders  string
	LogResponseBody     string
	LogResponseMiniBody string
	StartupScript       string
	KubeConfig          string
	Debug               string
}

var (
	c = CtlArgs{
		ContextFile: "contextFile",
		Context:     "context",
		ConfigFile:  "file",
		Name:        "name",
		Remote:      "remote",
	}
	cs = struct{ CtlArgs }{CtlArgs{
		ContextFile: "ctxf",
		Context:     "ctx",
		ConfigFile:  "f",
		Name:        "n",
		Remote:      "r",
	}}
	ctlh = struct{ CtlArgs }{CtlArgs{
		ContextFile: "Context file path (default .goto_ctx)",
		ConfigFile:  "Config file path",
		Context:     "Context name (default 'default')",
		Name:        "Context name (default 'default')",
		Remote:      "Remote Goto URL (default empty, leads to localhost:8080)",
	}}
	ca = ClientArgs{
		ClientMode:   "client",
		Protocol:     "protocol",
		URLs:         "urls",
		Headers:      "headers",
		RequestCount: "reqCount",
		Parallel:     "parallel",
		Method:       "method",
		Payload:      "payload",
		AutoPayload:  "autodata",
		Delay:        "delay",
		Retries:      "retries",
		RetryDelay:   "retrydelay",
		RetryOn:      "retryon",
		Persist:      "persist",
		Verbose:      "verbose",
	}
	csa = struct{ ClientArgs }{ClientArgs{
		ClientMode:   "cli",
		Protocol:     "pr",
		URLs:         "u",
		Headers:      "h",
		RequestCount: "req",
		Parallel:     "par",
		Method:       "X",
		Payload:      "data",
		AutoPayload:  "a",
		Delay:        "d",
		Retries:      "r",
		RetryDelay:   "rd",
		RetryOn:      "ro",
		Persist:      "persist",
		Verbose:      "v",
	}}
	ch = struct{ ClientArgs }{ClientArgs{
		ClientMode:   "Client mode",
		Protocol:     "Client traffic protocol: [http]/[grpc]/[tcp]",
		URLs:         "List of server URLs or IP+Ports to send traffic to",
		Headers:      "List of request headers to set",
		RequestCount: "Num of requests per target",
		Parallel:     "Num of parallel calls",
		Method:       "Request Method: [GET]/[POST]/[PUT]...",
		Payload:      "Request payload",
		AutoPayload:  "Auto-generate Request payload of given size",
		Delay:        "Delay (duration) between requests",
		Retries:      "Num of retries for a failed request",
		RetryDelay:   "Delay (duration) between retries",
		RetryOn:      "Status Codes to be retried",
		Persist:      "Persist results across invocations",
		Verbose:      "Print and Persist detailed results showing all calls",
	}}
	sa = ServerArgs{
		Port:                "port",
		GRPCPort:            "grpcPort",
		MCPPort:             "mcpPort",
		Ports:               "ports",
		Label:               "label",
		StartupDelay:        "startupDelay",
		ShutdownDelay:       "shutdownDelay",
		Registry:            "registry",
		Locker:              "locker",
		Events:              "events",
		PublishEvents:       "publishEvents",
		Certs:               "certs",
		ServerLogs:          "serverLogs",
		AdminLogs:           "adminLogs",
		MetricsLogs:         "metricsLogs",
		ProbeLogs:           "probeLogs",
		ClientLogs:          "clientLogs",
		InvocationLogs:      "invocationLogs",
		RegistryLogs:        "registryLogs",
		LockerLogs:          "lockerLogs",
		EventsLogs:          "eventsLogs",
		ReminderLogs:        "reminderLogs",
		PeerHealthLogs:      "peerHealthLogs",
		ProxyDebugLogs:      "proxyDebugLogs",
		LogRequestHeaders:   "logRequestHeaders",
		LogRequestBody:      "logRequestBody",
		LogRPCRequestBody:   "logRPCRequestBody",
		LogRequestMiniBody:  "logRequestMiniBody",
		LogResponseHeaders:  "logResponseHeaders",
		LogResponseBody:     "logResponseBody",
		LogResponseMiniBody: "logResponseMiniBody",
		StartupScript:       "startupScript",
		KubeConfig:          "kubeConfig",
		Debug:               "debug",
	}
	sh = struct{ ServerArgs }{ServerArgs{
		Port:                "Primary HTTP Server Listen Port",
		GRPCPort:            "Default GRPC Server Listen Port",
		MCPPort:             "Default MCP Server Listen Port",
		Ports:               "Comma-separated list of <port/protocol>. First port acts as primary HTTP port",
		Label:               "Default Server Label",
		StartupDelay:        "Delay Server Startup (seconds)",
		ShutdownDelay:       "Delay Server Shutdown (seconds)",
		Registry:            "Registry URL for Peer Registration",
		Locker:              "Store Results in Registry Locker",
		Events:              "Generate and store events on local instance",
		PublishEvents:       "Publish events to registry (if events are enabled)",
		Certs:               "Directory Path for TLS Certificates",
		ServerLogs:          "Enable/Disable All Server Logs",
		AdminLogs:           "Enable/Disable Admin Logs",
		MetricsLogs:         "Enable/Disable Metrics Logs",
		ProbeLogs:           "Enable/Disable Probe Logs",
		ClientLogs:          "Enable/Disable Client Logs",
		InvocationLogs:      "Enable/Disable Client's Target Invocation Logs",
		RegistryLogs:        "Enable/Disable All Registry Logs",
		LockerLogs:          "Enable/Disable Registry Locker Logs",
		EventsLogs:          "Enable/Disable Registry Peer Events Logs",
		ReminderLogs:        "Enable/Disable Registry Reminder Logs",
		PeerHealthLogs:      "Enable/Disable Registry-to-Peer Health Check Logs",
		ProxyDebugLogs:      "Enable/Disable Proxy Debug Logs",
		LogRequestHeaders:   "Enable/Disable logging of request headers",
		LogRequestBody:      "Enable/Disable logging of request body",
		LogRPCRequestBody:   "Enable/Disable logging of RPC request body",
		LogRequestMiniBody:  "Enable/Disable logging of request mini body",
		LogResponseHeaders:  "Enable/Disable logging of response headers",
		LogResponseBody:     "Enable/Disable logging of response body",
		LogResponseMiniBody: "Enable/Disable logging of response mini body",
		StartupScript:       "Script to execute at startup",
		KubeConfig:          "Path to Kubernetes config file",
		Debug:               "Debug logs",
	}}
)

var (
	urls          string
	headers       string
	retryOn       string
	portsList     string
	startupScript types.ListArg
)

func setupCtlArgs() {
	stringFlag(&global.CtlConfig.ContextFile, c.ContextFile, cs.ContextFile, ctlh.ConfigFile, "")
	stringFlag(&global.CtlConfig.Context, c.Context, cs.Context, ctlh.Context, "default")
	stringFlagSet(ctl.ApplyFlagSet, &global.CtlConfig.ConfigFile, c.ConfigFile, cs.ConfigFile, ctlh.ConfigFile, "")
	stringFlagSet(ctl.CtxFlagSet, &global.CtlConfig.Name, c.Name, cs.Name, ctlh.Name, "default")
	stringFlagSet(ctl.CtxFlagSet, &global.CtlConfig.RemoteURL, c.Remote, cs.Remote, ctlh.Remote, "http://localhost:8080")
}

func setupClientArgs() {
	boolFlag(&global.CmdConfig.CmdClientMode, ca.ClientMode, "", ch.ClientMode, false)
	stringFlag(&global.CmdClientConfig.Protocol, ca.Protocol, csa.Protocol, ch.Protocol, "")
	stringFlag(&urls, ca.URLs, csa.URLs, ch.URLs, "")
	stringFlag(&headers, ca.Headers, csa.Headers, ch.Headers, "")
	intFlag(&global.CmdClientConfig.RequestCount, ca.RequestCount, csa.RequestCount, ch.RequestCount, 1)
	intFlag(&global.CmdClientConfig.Parallel, ca.Parallel, csa.Parallel, ch.Parallel, 1)
	stringFlag(&global.CmdClientConfig.Method, ca.Method, csa.Method, ch.Method, "")
	stringFlag(&global.CmdClientConfig.Payload, ca.Payload, csa.Payload, ch.Payload, "")
	stringFlag(&global.CmdClientConfig.AutoPayload, ca.AutoPayload, csa.AutoPayload, ch.AutoPayload, "")
	stringFlag(&global.CmdClientConfig.Delay, ca.Delay, csa.Delay, ch.Delay, "")
	intFlag(&global.CmdClientConfig.Retries, ca.Retries, csa.Retries, ch.Retries, 1)
	stringFlag(&global.CmdClientConfig.RetryDelay, ca.RetryDelay, csa.RetryDelay, ch.RetryDelay, "")
	stringFlag(&retryOn, ca.RetryOn, csa.RetryOn, ch.RetryOn, "")
	boolFlag(&global.CmdClientConfig.Persist, ca.Persist, "", ch.Persist, false)
	boolFlag(&global.CmdClientConfig.Verbose, ca.Verbose, csa.Verbose, ch.Verbose, false)
}

func processClientArgs() {
	global.CmdClientConfig.URLs = strings.Split(urls, ",")
	hlist := strings.Split(headers, ",")
	for _, h := range hlist {
		global.CmdClientConfig.Headers = append(global.CmdClientConfig.Headers, strings.Split(h, ":"))
	}
	retryOnList := strings.Split(retryOn, ",")
	for _, r := range retryOnList {
		if code, err := strconv.Atoi(r); err == nil {
			global.CmdClientConfig.RetryOn = append(global.CmdClientConfig.RetryOn, code)
		}
	}
}

func setupServerArgs() {
	intFlag(&global.Self.ServerPort, sa.Port, "p", sh.Port, 8080)
	intFlag(&global.Self.GRPCPort, sa.GRPCPort, "gp", sh.GRPCPort, 1234)
	intFlag(&global.Self.MCPPort, sa.MCPPort, "mcp", sh.MCPPort, 3000)
	stringFlag(&portsList, sa.Ports, "", sh.Ports, "")
	stringFlag(&global.Self.Name, sa.Label, "l", sh.Label, "")
	stringFlag(&global.Self.RegistryURL, sa.Registry, "", sh.Registry, "")
	boolFlag(&global.Flags.UseLocker, sa.Locker, "", sh.Locker, false)
	boolFlag(&global.Flags.EnableEvents, sa.Events, "", sh.Events, true)
	boolFlag(&global.Flags.PublishEvents, sa.PublishEvents, "", sh.PublishEvents, false)
	boolFlag(&global.Flags.EnableServerLogs, sa.ServerLogs, "", sh.ServerLogs, true)
	boolFlag(&global.Flags.EnableAdminLogs, sa.AdminLogs, "", sh.AdminLogs, true)
	boolFlag(&global.Flags.EnableMetricsLogs, sa.MetricsLogs, "", sh.MetricsLogs, true)
	boolFlag(&global.Flags.EnableProbeLogs, sa.ProbeLogs, "", sh.ProbeLogs, false)
	boolFlag(&global.Flags.EnableClientLogs, sa.ClientLogs, "", sh.ClientLogs, true)
	boolFlag(&global.Flags.EnableInvocationLogs, sa.InvocationLogs, "", sh.InvocationLogs, true)
	boolFlag(&global.Flags.EnableRegistryLogs, sa.RegistryLogs, "", sh.RegistryLogs, true)
	boolFlag(&global.Flags.EnableRegistryLockerLogs, sa.LockerLogs, "", sh.LockerLogs, false)
	boolFlag(&global.Flags.EnableRegistryEventsLogs, sa.EventsLogs, "", sh.EventsLogs, false)
	boolFlag(&global.Flags.EnableRegistryReminderLogs, sa.ReminderLogs, "", sh.ReminderLogs, false)
	boolFlag(&global.Flags.EnablePeerHealthLogs, sa.PeerHealthLogs, "", sh.PeerHealthLogs, true)
	boolFlag(&global.Flags.EnableProxyDebugLogs, sa.ProxyDebugLogs, "", sh.ProxyDebugLogs, false)
	boolFlag(&global.Flags.LogRequestHeaders, sa.LogRequestHeaders, "", sh.LogRequestHeaders, true)
	boolFlag(&global.Flags.LogRequestBody, sa.LogRequestBody, "", sh.LogRequestBody, false)
	boolFlag(&global.Flags.LogRPCRequestBody, sa.LogRPCRequestBody, "", sh.LogRPCRequestBody, true)
	boolFlag(&global.Flags.LogRequestMiniBody, sa.LogRequestMiniBody, "", sh.LogRequestMiniBody, false)
	boolFlag(&global.Flags.LogResponseHeaders, sa.LogResponseHeaders, "", sh.LogResponseHeaders, false)
	boolFlag(&global.Flags.LogResponseBody, sa.LogResponseBody, "", sh.LogResponseBody, false)
	boolFlag(&global.Flags.LogResponseMiniBody, sa.LogResponseMiniBody, "", sh.LogResponseMiniBody, false)
	boolFlag(&global.Debug, sa.Debug, "", sh.Debug, false)
	stringFlag(&global.ServerConfig.CertPath, sa.Certs, "", sh.Certs, "/etc/certs")
	stringFlag(&global.ServerConfig.KubeConfig, sa.KubeConfig, "", sh.KubeConfig, "~/.kube/config")
	flag.DurationVar(&global.ServerConfig.StartupDelay, sa.StartupDelay, 1*time.Second, sh.StartupDelay)
	flag.DurationVar(&global.ServerConfig.ShutdownDelay, sa.ShutdownDelay, 1*time.Second, sh.ShutdownDelay)
	flag.Var(&startupScript, sa.StartupScript, sh.StartupScript)
}

func processServerArgs() {
	setupListenerConfigs()
	setupRegistryConfigs()
	setupLogs()

	if global.ServerConfig.CertPath != "" {
		log.Printf("Will read certs from [%s]\n", global.ServerConfig.CertPath)
	}
	if global.Debug {
		log.Println("Debug logging enabled")
	}
	log.Printf("Server startupDelay [%s] shutdownDelay [%s]\n", global.ServerConfig.StartupDelay, global.ServerConfig.ShutdownDelay)
	if startupScript != nil {
		global.ServerConfig.StartupScript = startupScript
	}
}

func setupListenerConfigs() {
	ports := strings.Split(portsList, ",")
	if len(ports) == 0 {
		ports = []string{strconv.Itoa(global.Self.ServerPort)}
	} else {
		global.Self.ServerPort, _ = strconv.Atoi(ports[0])
	}
	if global.Self.Name == "" {
		global.Self.GivenName = false
		global.Self.Name = util.BuildListenerLabel(global.Self.ServerPort)
	} else {
		global.Self.GivenName = true
	}
	listeners.DefaultLabel = global.Self.Name
	listeners.Init()
	listeners.AddInitialListeners(ports)
	log.Printf("Server [%s] will listen on port [%d]\n", global.Self.Name, global.Self.ServerPort)

}

func setupRegistryConfigs() {
	if global.Flags.EnableEvents {
		log.Println("Will generate and store events locally")
	}
	if global.Self.RegistryURL != "" {
		log.Printf("Registry [%s]\n", global.Self.RegistryURL)
		if global.Flags.UseLocker {
			log.Printf("Will Store Results in Locker at Registry [%s]\n", global.Self.RegistryURL)
		}
		if global.Flags.EnableEvents && global.Flags.PublishEvents {
			log.Println("Will publish events to registry")
		}
	}
	if global.Flags.EnableRegistryLockerLogs {
		log.Println("Will Print Registry Locker Logs")
	} else {
		log.Println("Will Not Print Registry Locker Logs")
	}
	if global.Flags.EnableRegistryEventsLogs {
		log.Println("Will Print Registry Peer Events Logs")
	} else {
		log.Println("Will Not Print Registry Peer Events Logs")
	}
	if global.Flags.EnableRegistryReminderLogs {
		log.Println("Will Print Registry Reminder Logs")
	} else {
		log.Println("Will Not Print Registry Reminder Logs")
	}
}

func setupLogs() {
	if global.Flags.EnableProbeLogs {
		log.Println("Will Print Probe Logs")
	} else {
		log.Println("Will Not Print Probe Logs")
	}
	if global.Flags.LogRequestHeaders {
		log.Println("Will Log Request Headers")
	} else {
		log.Println("Will Not Log Request Headers")
	}
	if global.Flags.LogRequestMiniBody {
		log.Println("Will Log Request Mini Body")
	} else if global.Flags.LogRequestBody {
		log.Println("Will Log Request Body")
	} else {
		log.Println("Will Not Log Request Body")
	}
	if global.Flags.LogResponseHeaders {
		log.Println("Will Log Response Headers")
	} else {
		log.Println("Will Not Log Response Headers")
	}
	if global.Flags.LogResponseMiniBody {
		log.Println("Will Log Response Mini Body")
	} else if global.Flags.LogResponseBody {
		log.Println("Will Log Response Body")
	} else {
		log.Println("Will Not Log Response Body")
	}
}

func stringFlag(p *string, name, shortName, usage, value string) {
	flag.StringVar(p, name, value, usage)
	if shortName != "" {
		flag.StringVar(p, shortName, value, usage)
	}
}

func intFlag(p *int, name, shortName, usage string, value int) {
	flag.IntVar(p, name, value, usage)
	if shortName != "" {
		flag.IntVar(p, shortName, value, usage)
	}
}

func boolFlag(p *bool, name, shortName, usage string, value bool) {
	flag.BoolVar(p, name, value, usage)
	if shortName != "" {
		flag.BoolVar(p, shortName, value, usage)
	}
}

func stringFlagSet(fl *flag.FlagSet, p *string, name, shortName, usage, value string) {
	fl.StringVar(p, name, value, usage)
	if shortName != "" {
		fl.StringVar(p, shortName, value, usage)
	}
}
