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
		if code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("%s: code %d, output %q", arg, code, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCmd("serach")
	if code != 2 || !strings.Contains(errOut, `unknown command "serach"`) || !strings.Contains(errOut, "Usage:") {
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
