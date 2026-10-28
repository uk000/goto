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

package constants

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// stringConstants reads the string constants that the package declares in a file, by name, so that the checks cover
// every constant that is there, and the ones that will be added.
func stringConstants(t *testing.T, file string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Errorf("%s is not a string literal", name.Name)
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				consts[name.Name] = v
			}
		}
	}
	return consts
}

func TestHeaderConstantsAreNotEmptyAndNotRepeated(t *testing.T) {
	consts := stringConstants(t, "headers.go")
	if len(consts) < 80 {
		t.Fatalf("found %d constants in headers.go, want all of them (more than 80)", len(consts))
	}
	byValue := map[string]string{}
	names := make([]string, 0, len(consts))
	for name := range consts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := consts[name]
		if v == "" {
			t.Errorf("%s is empty", name)
		}
		if other, ok := byValue[v]; ok {
			t.Errorf("%s and %s have the same value %q", name, other, v)
		}
		byValue[v] = name
	}
}

// The names of the headers are tokens of HTTP, as they are sent as they are. The content types are not headers, and the
// authority is a pseudo header of HTTP/2.
func TestHeaderNamesAreValidHeaderNames(t *testing.T) {
	token := regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	for name, v := range stringConstants(t, "headers.go") {
		switch {
		case strings.HasPrefix(name, "ContentType"):
			if !strings.Contains(v, "/") {
				t.Errorf("%s = %q, want a media type", name, v)
			}
		case name == "HeaderAuthority":
			if v != ":authority" {
				t.Errorf("%s = %q, want the pseudo header of HTTP/2", name, v)
			}
		default:
			if !token.MatchString(v) {
				t.Errorf("%s = %q is not a valid header name", name, v)
			}
		}
	}
}

// HTTP headers are not case sensitive, so two constants that differ only by case are the same header. The one pair that
// is meant to be the same is the content type, which has a form that is made for lookups in the maps of the headers of
// HTTP/2 and gRPC, where names are lower case.
func TestHeaderNamesAreNotRepeatedByCase(t *testing.T) {
	byLower := map[string]string{}
	names := []string{}
	consts := stringConstants(t, "headers.go")
	for name := range consts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, "ContentType") {
			continue
		}
		lower := strings.ToLower(consts[name])
		if other, ok := byLower[lower]; ok && !(name == "HeaderContentTypeLower" && other == "HeaderContentType") &&
			!(other == "HeaderContentTypeLower" && name == "HeaderContentType") {
			t.Errorf("%s (%q) and %s are the same header", name, consts[name], other)
		}
		byLower[lower] = name
	}
	if byLower["content-type"] == "" {
		t.Error("no content type header found")
	}
}

func TestHeaderConstantValues(t *testing.T) {
	consts := stringConstants(t, "headers.go")
	want := map[string]string{
		"HeaderViaGoto":          "Via-Goto",
		"HeaderGotoHost":         "Goto-Host",
		"HeaderGotoPort":         "Goto-Port",
		"HeaderGotoProtocol":     "Goto-Protocol",
		"HeaderGotoRequestID":    "Goto-Request-Id",
		"XRequestID":             "x-request-id",
		"HeaderContentType":      "Content-Type",
		"HeaderContentTypeLower": "content-type",
		"HeaderContentLength":    "Content-Length",
		"HeaderTransferEncoding": "Transfer-Encoding",
		"HeaderAuthority":        ":authority",
		"ContentTypeJSON":        "application/json",
		"ContentTypeYAML":        "application/yaml",
		"ContentTypeMarkdown":    "text/markdown; charset=utf-8",
		"HeaderGotoRPCMethod":    "Goto-RPC-Method",
	}
	for name, v := range want {
		if consts[name] != v {
			t.Errorf("%s = %q, want %q", name, consts[name], v)
		}
	}
	// The ones that are used by the code of this package are the ones that the tests above read.
	if HeaderViaGoto != consts["HeaderViaGoto"] || ContentTypeJSON != consts["ContentTypeJSON"] || XRequestID != consts["XRequestID"] {
		t.Error("the constants of the package are not the ones of the source")
	}
}

// The headers that a proxy adds are named for it: Goto-Proxy- for the proxies of requests, Goto-Connect-Proxy- for the
// CONNECT proxy, and Goto-Client- for the client, and none of them is shared between those.
func TestProxyHeadersAreNamedForTheirProxy(t *testing.T) {
	groups := []struct{ constPrefix, valuePrefix string }{
		{"HeaderGotoProxy", "Goto-Proxy-"},
		{"HeaderGotoConnectProxy", "Goto-Connect-Proxy-"},
		{"HeaderGotoClient", "Goto-Client-"},
	}
	owner := map[string]string{}
	for name, v := range stringConstants(t, "headers.go") {
		for _, g := range groups {
			if !strings.HasPrefix(name, g.constPrefix) {
				continue
			}
			if !strings.HasPrefix(v, g.valuePrefix) {
				t.Errorf("%s = %q, want it to start with %s", name, v, g.valuePrefix)
			}
			if other, ok := owner[strings.ToLower(v)]; ok {
				t.Errorf("%s and %s have the same value %q", name, other, v)
			}
			owner[strings.ToLower(v)] = name
		}
	}
	// the headers of the two proxies that tell the same thing are told apart by their names
	for proxy, connect := range map[string]string{
		HeaderGotoProxyHost:           HeaderGotoConnectProxyHost,
		HeaderGotoProxyPort:           HeaderGotoConnectProxyPort,
		HeaderGotoProxyUpstreamStatus: HeaderGotoConnectProxyUpstreamStatus,
		HeaderGotoProxyDelay:          HeaderGotoConnectProxyDelay,
		HeaderGotoProxyTook:           HeaderGotoConnectProxyTook,
	} {
		if strings.EqualFold(proxy, connect) {
			t.Errorf("the proxy and the CONNECT proxy both use %q", proxy)
		}
	}
}

func TestLockerKeys(t *testing.T) {
	consts := stringConstants(t, "constants.go")
	want := map[string]string{
		"LockerClientKey": "client", "LockerClientResultsKey": "results", "LockerInvocationsKey": "invocations",
		"LockerEventsKey": "events", "LockerDefaultLabel": "default", "LockerCurrent": "current",
		"LockerPeers": "peerLockers", "LockerAll": "all",
	}
	if len(consts) != len(want) {
		t.Errorf("constants.go has %d constants, the test knows %d: %v", len(consts), len(want), consts)
	}
	seen := map[string]string{}
	for name, v := range want {
		if consts[name] != v {
			t.Errorf("%s = %q, want %q", name, consts[name], v)
		}
		if other, ok := seen[v]; ok {
			t.Errorf("%s and %s have the same value %q", name, other, v)
		}
		seen[v] = name
	}
	if LockerClientKey != "client" || LockerAll != "all" {
		t.Error("the constants of the package are not the ones of the source")
	}
}
