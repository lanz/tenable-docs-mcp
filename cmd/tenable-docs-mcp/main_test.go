// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runCmd(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := runCmd("version")
	if code != 0 || !strings.HasPrefix(out, "tenable-docs-mcp ") {
		t.Errorf("version: code %d, output %q", code, out)
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, out, _ := runCmd(arg)
		if code != 0 || !strings.Contains(out, "USAGE") || !strings.Contains(out, "ASK THE DOCS") {
			t.Errorf("%s: code %d, output %q", arg, code, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCmd("serach")
	if code != 2 || !strings.Contains(errOut, `unknown command "serach"`) || !strings.Contains(errOut, `Did you mean "search"?`) {
		t.Errorf("unknown command: code %d, stderr %q", code, errOut)
	}
}

func TestServeRefusesNonLoopbackHTTP(t *testing.T) {
	code, _, errOut := runCmd("serve", "-http", "0.0.0.0:8080")
	if code != 2 || !strings.Contains(errOut, "only listens on this machine") {
		t.Errorf("non-loopback address: code %d, stderr %q", code, errOut)
	}
}

func TestServeRejectsExtraArguments(t *testing.T) {
	code, _, errOut := runCmd("serve", "extra")
	if code != 2 || !strings.Contains(errOut, `unexpected argument "extra"`) {
		t.Errorf("extra argument: code %d, stderr %q", code, errOut)
	}
}

func TestServeHTTPStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	// Not the user's own index.
	t.Setenv("TDM_INDEX_DIR", t.TempDir())
	go func() { done <- run(ctx, []string{"serve", "-http", "127.0.0.1:0"}, &out, &errOut) }()
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("serve -http after cancel: code %d, stderr %q", code, errOut.String())
	}
}

// -hosted needs an address and its host names, and the hosted flags need -hosted.
func TestServeHostedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "-hosted"},
		{"serve", "-hosted", "-http", ":8080"},
		{"serve", "-hosted", "-allow-host", "docs.tenable.com"},
		{"serve", "-hosted", "-http", ":8080", "-allow-host", " , "},
		{"serve", "-allow-host", "docs.tenable.com"},
		{"serve", "-client-ip-header", "CF-Connecting-IP", "-http", "127.0.0.1:8080"},
		{"serve", "-rate", "60"},
		{"serve", "-max-in-flight", "10"},
		{"serve", "-hosted", "-http", ":8080", "-allow-host", "docs.tenable.com:443"},
		{"serve", "-hosted", "-http", ":8080", "-allow-host", "docs.tenable.com", "-rate", "0"},
		{"serve", "-hosted", "-http", ":8080", "-allow-host", "docs.tenable.com", "-max-in-flight", "-1"},
	} {
		if code, _, errOut := runCmd(args...); code != 2 || !strings.Contains(errOut, "tenable-docs-mcp serve: -") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
}
