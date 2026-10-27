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

package cmd

import (
	"bytes"
	"flag"
	"io"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"goto/ctl"
	"goto/pkg/global"
	"goto/pkg/types"
)

// quietFlagSet is a flag set that returns a mistake in the arguments, and does not exit or print.
func quietFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// freshFlags gives the code the flag sets that the process has when it starts, which have none of the flags yet.
func freshFlags() {
	flag.CommandLine = quietFlagSet("test")
	ctl.ApplyFlagSet, ctl.CtxFlagSet = quietFlagSet("apply"), quietFlagSet("ctx")
	urls, headers, retryOn, portsList = "", "", "", ""
	startupScript, configPaths = nil, nil
	global.CmdConfig = types.CmdConfig{}
	global.CmdClientConfig = types.CmdClientConfig{}
}

// isolate gives the test fresh flags, and puts back the flags, arguments and settings of the process when the test is
// done. It returns what is logged.
func isolate(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldCommandLine, oldArgs := flag.CommandLine, os.Args
	oldApply, oldCtx := ctl.ApplyFlagSet, ctl.CtxFlagSet
	oldURLs, oldHeaders, oldRetryOn, oldPorts := urls, headers, retryOn, portsList
	oldStartup, oldConfigPaths := startupScript, configPaths
	oldSelf, oldFlags, oldServer := global.Self, global.Flags, global.ServerConfig
	oldCtl, oldCmd, oldClient := global.CtlConfig, global.CmdConfig, global.CmdClientConfig
	oldDebug, oldVersion, oldCommit := global.Debug, global.Version, global.Commit
	oldLogFlags, oldLogWriter := log.Flags(), log.Writer()

	freshFlags()
	logged := &bytes.Buffer{}
	log.SetOutput(logged)
	t.Cleanup(func() {
		flag.CommandLine, os.Args = oldCommandLine, oldArgs
		ctl.ApplyFlagSet, ctl.CtxFlagSet = oldApply, oldCtx
		urls, headers, retryOn, portsList = oldURLs, oldHeaders, oldRetryOn, oldPorts
		startupScript, configPaths = oldStartup, oldConfigPaths
		global.Self, global.Flags, global.ServerConfig = oldSelf, oldFlags, oldServer
		global.CtlConfig, global.CmdConfig, global.CmdClientConfig = oldCtl, oldCmd, oldClient
		global.Debug, global.Version, global.Commit = oldDebug, oldVersion, oldCommit
		log.SetFlags(oldLogFlags)
		log.SetOutput(oldLogWriter)
	})
	return logged
}

func setupAll() {
	setupCtlArgs()
	setupClientArgs()
	setupServerArgs()
}

func parse(t *testing.T, args ...string) {
	t.Helper()
	if err := flag.CommandLine.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
}

// within runs fn, and fails the test when it panics or has not returned by the deadline.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s panicked: %v", what, p)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not return in time", what)
	}
}

func hasAll(t *testing.T, logged *bytes.Buffer, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log has no %q:\n%s", want, logged)
		}
	}
}

func hasNone(t *testing.T, logged *bytes.Buffer, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(logged.String(), u) {
			t.Errorf("log has %q:\n%s", u, logged)
		}
	}
}

func TestEveryFlagIsRegisteredUnderItsNames(t *testing.T) {
	isolate(t)
	setupAll()
	// The flags of the command line, and the short name of each (if it has one).
	want := map[string]string{
		"contextFile": "ctxf", "context": "ctx",
		"client": "", "protocol": "pr", "urls": "u", "headers": "h", "reqCount": "req", "parallel": "par", "method": "X",
		"payload": "data", "autodata": "a", "delay": "d", "retries": "r", "retrydelay": "rd", "retryon": "ro", "persist": "", "verbose": "v",
		"port": "p", "grpcPort": "gp", "rpcPort": "rpc", "ports": "", "cn": "", "label": "l", "registry": "", "locker": "", "events": "",
		"publishEvents": "", "serverLogs": "", "adminLogs": "", "metricsLogs": "", "probeLogs": "", "clientLogs": "", "invocationLogs": "",
		"registryLogs": "", "lockerLogs": "", "eventsLogs": "", "reminderLogs": "", "peerHealthLogs": "", "proxyDebugLogs": "",
		"logRequestHeaders": "", "logRequestBody": "", "logRPCRequestBody": "", "logRequestMiniBody": "", "logResponseHeaders": "",
		"logResponseBody": "", "logResponseMiniBody": "", "debug": "", "certs": "", "kubeConfig": "", "startupDelay": "", "shutdownDelay": "",
		"startupScript": "", "config": "",
	}
	expected := map[string]bool{}
	for name, short := range want {
		expected[name] = true
		f := flag.CommandLine.Lookup(name)
		if f == nil {
			t.Errorf("flag -%s is not registered", name)
			continue
		}
		if f.Usage == "" {
			t.Errorf("flag -%s has no usage", name)
		}
		if short == "" {
			continue
		}
		expected[short] = true
		sf := flag.CommandLine.Lookup(short)
		if sf == nil {
			t.Errorf("flag -%s has no short name -%s", name, short)
		} else if sf.DefValue != f.DefValue || sf.Usage != f.Usage {
			t.Errorf("flag -%s has default %q and usage %q, but -%s has %q and %q", short, sf.DefValue, sf.Usage, name, f.DefValue, f.Usage)
		}
	}
	registered := 0
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		registered++
		if !expected[f.Name] {
			t.Errorf("flag -%s is registered, and is not one that is expected", f.Name)
		}
	})
	if registered != len(expected) {
		t.Errorf("flags = %d, want %d", registered, len(expected))
	}
	for name, names := range map[string][]string{"apply": {"file", "f"}, "ctx": {"name", "n", "remote", "r"}} {
		fs := ctl.ApplyFlagSet
		if name == "ctx" {
			fs = ctl.CtxFlagSet
		}
		count := 0
		fs.VisitAll(func(*flag.Flag) { count++ })
		if count != len(names) {
			t.Errorf("flags of %s = %d, want %d", name, count, len(names))
		}
		for _, n := range names {
			if f := fs.Lookup(n); f == nil || f.Usage == "" {
				t.Errorf("flag -%s of %s is not registered, or has no usage", n, name)
			}
		}
	}
}

func TestSetupCtlArgs(t *testing.T) {
	isolate(t)
	setupCtlArgs()
	if got := global.CtlConfig; got != (types.CmdCtlConfig{Context: "default", Name: "default", RemoteURL: "http://localhost:8080"}) {
		t.Errorf("defaults = %+v", got)
	}
	parse(t, "-contextFile", "/tmp/c.yaml", "-context", "other")
	if global.CtlConfig.ContextFile != "/tmp/c.yaml" || global.CtlConfig.Context != "other" {
		t.Errorf("config = %+v", global.CtlConfig)
	}
	parse(t, "-ctxf", "/tmp/d.yaml", "-ctx", "another")
	if global.CtlConfig.ContextFile != "/tmp/d.yaml" || global.CtlConfig.Context != "another" {
		t.Errorf("config = %+v", global.CtlConfig)
	}
	for _, args := range [][]string{{"-f", "a.yaml"}, {"-file", "b.yaml"}} {
		if err := ctl.ApplyFlagSet.Parse(args); err != nil || global.CtlConfig.ConfigFile != args[1] {
			t.Errorf("apply %v: %v, %q", args, err, global.CtlConfig.ConfigFile)
		}
	}
	if err := ctl.CtxFlagSet.Parse([]string{"-n", "mine", "-r", "http://mine:1"}); err != nil || global.CtlConfig.Name != "mine" || global.CtlConfig.RemoteURL != "http://mine:1" {
		t.Errorf("ctx -n -r: %v, %+v", err, global.CtlConfig)
	}
	if err := ctl.CtxFlagSet.Parse([]string{"-name", "yours", "-remote", "http://yours:2"}); err != nil || global.CtlConfig.Name != "yours" || global.CtlConfig.RemoteURL != "http://yours:2" {
		t.Errorf("ctx -name -remote: %v, %+v", err, global.CtlConfig)
	}
}

func TestSetupClientArgsDefaultsAndLongNames(t *testing.T) {
	isolate(t)
	setupClientArgs()
	c := global.CmdClientConfig
	if c.RequestCount != 1 || c.Parallel != 1 || c.Retries != 1 || c.Protocol != "" || c.Method != "" || c.Persist || c.Verbose || global.CmdConfig.CmdClientMode {
		t.Errorf("defaults = %+v, client mode %v", c, global.CmdConfig.CmdClientMode)
	}
	parse(t, "-client", "-protocol", "grpc", "-urls", "a,b", "-headers", "h:v", "-reqCount", "7", "-parallel", "3", "-method", "POST",
		"-payload", "body", "-autodata", "100", "-delay", "2s", "-retries", "4", "-retrydelay", "1s", "-retryon", "500,503", "-persist", "-verbose")
	c = global.CmdClientConfig
	if !global.CmdConfig.CmdClientMode || c.Protocol != "grpc" || c.RequestCount != 7 || c.Parallel != 3 || c.Method != "POST" ||
		c.Payload != "body" || c.AutoPayload != "100" || c.Delay != "2s" || c.Retries != 4 || c.RetryDelay != "1s" || !c.Persist || !c.Verbose {
		t.Errorf("config = %+v, client mode %v", c, global.CmdConfig.CmdClientMode)
	}
	if urls != "a,b" || headers != "h:v" || retryOn != "500,503" {
		t.Errorf("urls = %q, headers = %q, retryOn = %q", urls, headers, retryOn)
	}
}

func TestSetupClientArgsShortNamesSetTheSameThings(t *testing.T) {
	isolate(t)
	setupClientArgs()
	parse(t, "-pr", "tcp", "-u", "x", "-h", "a:b", "-req", "2", "-par", "5", "-X", "PUT", "-data", "p", "-a", "9", "-d", "1s",
		"-r", "3", "-rd", "3s", "-ro", "404", "-v")
	c := global.CmdClientConfig
	if c.Protocol != "tcp" || c.RequestCount != 2 || c.Parallel != 5 || c.Method != "PUT" || c.Payload != "p" || c.AutoPayload != "9" ||
		c.Delay != "1s" || c.Retries != 3 || c.RetryDelay != "3s" || !c.Verbose || urls != "x" || headers != "a:b" || retryOn != "404" {
		t.Errorf("config = %+v, urls = %q, headers = %q, retryOn = %q", c, urls, headers, retryOn)
	}
}

func TestSetupServerArgsDefaults(t *testing.T) {
	isolate(t)
	setupServerArgs()
	if global.Self.ServerPort != 8080 || global.Self.GRPCPort != 1234 || global.Self.JSONRPCPort != 3000 || global.Self.Name != "" || global.Self.RegistryURL != "" {
		t.Errorf("self = %+v", global.Self)
	}
	sc := global.ServerConfig
	if sc.CommonName != "goto.goto" || sc.CertPath != "/etc/certs" || sc.KubeConfig != "~/.kube/config" || sc.StartupDelay != time.Second || sc.ShutdownDelay != time.Second {
		t.Errorf("server config = %+v", sc)
	}
	f := global.Flags
	for name, v := range map[string]bool{
		"EnableServerLogs": f.EnableServerLogs, "EnableAdminLogs": f.EnableAdminLogs, "EnableMetricsLogs": f.EnableMetricsLogs,
		"EnableClientLogs": f.EnableClientLogs, "EnableInvocationLogs": f.EnableInvocationLogs, "EnableRegistryLogs": f.EnableRegistryLogs,
		"EnablePeerHealthLogs": f.EnablePeerHealthLogs, "LogRequestHeaders": f.LogRequestHeaders, "LogRPCRequestBody": f.LogRPCRequestBody,
	} {
		if !v {
			t.Errorf("%s is off by default", name)
		}
	}
	for name, v := range map[string]bool{
		"UseLocker": f.UseLocker, "EnableEvents": f.EnableEvents, "PublishEvents": f.PublishEvents, "EnableProbeLogs": f.EnableProbeLogs,
		"EnableRegistryLockerLogs": f.EnableRegistryLockerLogs, "EnableRegistryEventsLogs": f.EnableRegistryEventsLogs,
		"EnableRegistryReminderLogs": f.EnableRegistryReminderLogs, "EnableProxyDebugLogs": f.EnableProxyDebugLogs,
		"LogRequestBody": f.LogRequestBody, "LogRequestMiniBody": f.LogRequestMiniBody, "LogResponseHeaders": f.LogResponseHeaders,
		"LogResponseBody": f.LogResponseBody, "LogResponseMiniBody": f.LogResponseMiniBody, "Debug": global.Debug,
	} {
		if v {
			t.Errorf("%s is on by default", name)
		}
	}
	if portsList != "" || startupScript != nil || configPaths != nil {
		t.Errorf("ports = %q, startup scripts = %v, config paths = %v", portsList, startupScript, configPaths)
	}
}

func TestSetupServerArgsParsing(t *testing.T) {
	isolate(t)
	setupServerArgs()
	parse(t, "-port", "9090", "-grpcPort", "9091", "-rpcPort", "9092", "-ports", "9090,9093/tcp", "-cn", "my.cn", "-label", "my-label",
		"-registry", "http://registry:1", "-locker", "-events", "-publishEvents", "-probeLogs", "-serverLogs=false", "-adminLogs=false",
		"-lockerLogs", "-logRequestBody", "-logResponseMiniBody", "-debug", "-certs", "/my/certs", "-kubeConfig", "/my/kube",
		"-startupDelay", "5s", "-shutdownDelay", "2s", "-startupScript", "echo one", "-startupScript", "echo two", "-config", "/c1", "-config", "/c2")
	if global.Self.ServerPort != 9090 || global.Self.GRPCPort != 9091 || global.Self.JSONRPCPort != 9092 || global.Self.Name != "my-label" || global.Self.RegistryURL != "http://registry:1" {
		t.Errorf("self = %+v", global.Self)
	}
	sc := global.ServerConfig
	if sc.CommonName != "my.cn" || sc.CertPath != "/my/certs" || sc.KubeConfig != "/my/kube" || sc.StartupDelay != 5*time.Second || sc.ShutdownDelay != 2*time.Second {
		t.Errorf("server config = %+v", sc)
	}
	f := global.Flags
	if !f.UseLocker || !f.EnableEvents || !f.PublishEvents || !f.EnableProbeLogs || f.EnableServerLogs || f.EnableAdminLogs || !f.EnableRegistryLockerLogs ||
		!f.LogRequestBody || !f.LogResponseMiniBody || !global.Debug {
		t.Errorf("flags = %+v, debug = %v", f, global.Debug)
	}
	if portsList != "9090,9093/tcp" {
		t.Errorf("ports = %q", portsList)
	}
	if !reflect.DeepEqual(startupScript, types.ListArg{"echo one", "echo two"}) || !reflect.DeepEqual(configPaths, types.ListArg{"/c1", "/c2"}) {
		t.Errorf("startup scripts = %v, config paths = %v, want the lists that were given", startupScript, configPaths)
	}
}

func TestSetupServerArgsShortNamesSetTheSameThings(t *testing.T) {
	isolate(t)
	setupServerArgs()
	parse(t, "-p", "7000", "-gp", "7001", "-rpc", "7002", "-l", "short-label")
	if global.Self.ServerPort != 7000 || global.Self.GRPCPort != 7001 || global.Self.JSONRPCPort != 7002 || global.Self.Name != "short-label" {
		t.Errorf("self = %+v", global.Self)
	}
}

func TestSetupServerArgsRejectsAValueThatIsNotValid(t *testing.T) {
	for _, args := range [][]string{{"-port", "eighty"}, {"-startupDelay", "soon"}, {"-debug=maybe"}, {"-nosuchflag"}} {
		isolate(t)
		setupServerArgs()
		if err := flag.CommandLine.Parse(args); err == nil {
			t.Errorf("parse %v: no error", args)
		}
	}
}

func TestProcessClientArgs(t *testing.T) {
	isolate(t)
	urls = "http://a,http://b"
	headers = "x-one:1,x-two:2,malformed"
	retryOn = "500,503,notanumber"
	within(t, "processClientArgs", processClientArgs)
	c := global.CmdClientConfig
	if !reflect.DeepEqual(c.URLs, []string{"http://a", "http://b"}) {
		t.Errorf("URLs = %v", c.URLs)
	}
	if want := map[string]string{"x-one": "1", "x-two": "2"}; !reflect.DeepEqual(c.Headers, want) {
		t.Errorf("headers = %v, want %v", c.Headers, want)
	}
	if !reflect.DeepEqual(c.RetryOn, []int{500, 503}) {
		t.Errorf("retry on = %v", c.RetryOn)
	}
}

func TestProcessClientArgsWithoutHeadersOrRetries(t *testing.T) {
	isolate(t)
	urls = "http://only"
	within(t, "processClientArgs", processClientArgs)
	c := global.CmdClientConfig
	if !reflect.DeepEqual(c.URLs, []string{"http://only"}) || len(c.Headers) != 0 || len(c.RetryOn) != 0 {
		t.Errorf("config = %+v", c)
	}
}

func TestProcessClientArgsKeepsTheHeadersThatTheConfigHas(t *testing.T) {
	isolate(t)
	global.CmdClientConfig.Headers = map[string]string{"kept": "yes", "x-one": "old"}
	headers = "x-one:new"
	within(t, "processClientArgs", processClientArgs)
	if want := map[string]string{"kept": "yes", "x-one": "new"}; !reflect.DeepEqual(global.CmdClientConfig.Headers, want) {
		t.Errorf("headers = %v, want %v", global.CmdClientConfig.Headers, want)
	}
}

func TestProcessServerArgs(t *testing.T) {
	logged := isolate(t)
	setupServerArgs()
	parse(t, "-startupScript", "echo hi", "-config", "/etc/goto", "-debug", "-certs", "/the/certs")
	global.ServerConfig.StartupScript, global.ServerConfig.ConfigPaths = nil, nil
	processServerArgs()
	if !reflect.DeepEqual(global.ServerConfig.StartupScript, []string{"echo hi"}) || !reflect.DeepEqual(global.ServerConfig.ConfigPaths, []string{"/etc/goto"}) {
		t.Errorf("startup scripts = %v, config paths = %v", global.ServerConfig.StartupScript, global.ServerConfig.ConfigPaths)
	}
	hasAll(t, logged, "Will read certs from [/the/certs]", "Debug logging enabled", "Server startupDelay [1s] shutdownDelay [1s]")
}

func TestProcessServerArgsWithoutListsLeavesTheConfigAsItIs(t *testing.T) {
	logged := isolate(t)
	global.ServerConfig.StartupScript, global.ServerConfig.ConfigPaths = []string{"keep"}, []string{"keep-too"}
	global.ServerConfig.CertPath = ""
	global.Debug = false
	processServerArgs()
	if !reflect.DeepEqual(global.ServerConfig.StartupScript, []string{"keep"}) || !reflect.DeepEqual(global.ServerConfig.ConfigPaths, []string{"keep-too"}) {
		t.Errorf("startup scripts = %v, config paths = %v", global.ServerConfig.StartupScript, global.ServerConfig.ConfigPaths)
	}
	hasNone(t, logged, "Will read certs", "Debug logging enabled")
}

func TestSetupRegistryConfigsReportsWhatItWillDo(t *testing.T) {
	logged := isolate(t)
	global.Flags.EnableEvents = true
	global.Flags.PublishEvents = true
	global.Flags.UseLocker = true
	global.Flags.EnableRegistryLockerLogs = true
	global.Self.RegistryURL = "http://registry:9"
	setupRegistryConfigs()
	hasAll(t, logged, "Will generate and store events locally", "Registry [http://registry:9]", "Will Store Results in Locker at Registry [http://registry:9]",
		"Will publish events to registry", "Will Print Registry Locker Logs", "Will Not Print Registry Peer Events Logs", "Will Not Print Registry Reminder Logs")
}

func TestSetupRegistryConfigsWithoutARegistry(t *testing.T) {
	logged := isolate(t)
	global.Flags.EnableEvents, global.Flags.PublishEvents, global.Flags.UseLocker = false, true, true
	global.Flags.EnableRegistryLockerLogs, global.Flags.EnableRegistryEventsLogs, global.Flags.EnableRegistryReminderLogs = false, true, true
	global.Self.RegistryURL = ""
	setupRegistryConfigs()
	hasNone(t, logged, "Will generate", "Registry [", "Locker at", "publish events", "Will Print Registry Locker")
	hasAll(t, logged, "Will Not Print Registry Locker Logs", "Will Print Registry Peer Events Logs", "Will Print Registry Reminder Logs")
}

func TestSetupRegistryConfigsPublishesEventsOnlyWhenTheyAreOn(t *testing.T) {
	logged := isolate(t)
	global.Flags.EnableEvents, global.Flags.PublishEvents = false, true
	global.Self.RegistryURL = "http://registry:9"
	setupRegistryConfigs()
	hasAll(t, logged, "Registry [http://registry:9]")
	hasNone(t, logged, "Will publish events to registry")
}

func TestSetupLogsReportsWhatItWillLog(t *testing.T) {
	logged := isolate(t)
	global.Flags = types.Flags{EnableProbeLogs: true, LogRequestHeaders: true, LogRequestMiniBody: true, LogRequestBody: true, LogResponseHeaders: true, LogResponseBody: true}
	setupLogs()
	hasAll(t, logged, "Will Print Probe Logs", "Will Log Request Headers", "Will Log Request Mini Body", "Will Log Response Headers", "Will Log Response Body")
	// What is logged of the request is the mini body when both are on.
	hasNone(t, logged, "Will Log Request Body", "Will Not")
}

func TestSetupLogsWhenNothingIsOn(t *testing.T) {
	logged := isolate(t)
	global.Flags = types.Flags{}
	setupLogs()
	hasAll(t, logged, "Will Not Print Probe Logs", "Will Not Log Request Headers", "Will Not Log Request Body", "Will Not Log Response Headers", "Will Not Log Response Body")
}

func TestSetupLogsLogsTheBodyWhenOnlyTheBodyIsOn(t *testing.T) {
	logged := isolate(t)
	global.Flags = types.Flags{LogRequestBody: true, LogResponseMiniBody: true, LogResponseBody: true}
	setupLogs()
	hasAll(t, logged, "Will Log Request Body", "Will Log Response Mini Body")
	hasNone(t, logged, "Will Log Request Mini Body", "Will Log Response Body")
}

func TestFlagHelpersRegisterTheShortNameOnlyWhenThereIsOne(t *testing.T) {
	isolate(t)
	var s string
	var n int
	var b bool
	stringFlag(&s, "str", "s", "usage", "dflt")
	intFlag(&n, "num", "n", "usage", 5)
	boolFlag(&b, "bool", "b", "usage", true)
	stringFlag(new(string), "nolong", "", "usage", "")
	if s != "dflt" || n != 5 || !b {
		t.Errorf("defaults = %q, %d, %v", s, n, b)
	}
	parse(t, "-s", "from-short", "-n", "9", "-b=false")
	if s != "from-short" || n != 9 || b {
		t.Errorf("values = %q, %d, %v, want the short names to set the same variables", s, n, b)
	}
	parse(t, "-str", "from-long", "-num", "2", "-bool")
	if s != "from-long" || n != 2 || !b {
		t.Errorf("values = %q, %d, %v", s, n, b)
	}
	count := 0
	flag.CommandLine.VisitAll(func(*flag.Flag) { count++ })
	if count != 7 {
		t.Errorf("flags = %d, want 7 (3 with a short name, and 1 without)", count)
	}
}

func TestStringFlagSetRegistersOnTheGivenSetOnly(t *testing.T) {
	isolate(t)
	fs := quietFlagSet("other")
	var f string
	stringFlagSet(fs, &f, "file", "f", "usage", "def")
	stringFlagSet(fs, new(string), "only", "", "usage", "")
	if f != "def" {
		t.Errorf("default = %q", f)
	}
	if err := fs.Parse([]string{"-f", "x"}); err != nil || f != "x" {
		t.Errorf("parse = %v, %q", err, f)
	}
	if err := fs.Parse([]string{"-file", "y"}); err != nil || f != "y" {
		t.Errorf("parse = %v, %q", err, f)
	}
	if fs.Lookup("only") == nil || fs.Lookup("o") != nil {
		t.Error("the flag without a short name has one, or is missing")
	}
	count := 0
	flag.CommandLine.VisitAll(func(*flag.Flag) { count++ })
	if count != 0 {
		t.Errorf("the helper registered %d flags on the command line", count)
	}
}
