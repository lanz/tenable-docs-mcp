// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// fits reports the lines of out wider than width, color codes aside.
func tooWide(out string) []string {
	var wide []string
	for _, l := range strings.Split(ansi.ReplaceAllString(out, ""), "\n") {
		if utf8.RuneCountInString(l) > width {
			wide = append(wide, l)
		}
	}
	return wide
}

// The help fits a default terminal, lists every command, and is plain text in a pipe.
func TestHelpLayout(t *testing.T) {
	_, out, _ := runCmd("help")
	if wide := tooWide(out); len(wide) > 0 {
		t.Errorf("lines wider than %d:\n%s", width, strings.Join(wide, "\n"))
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(out, wordmark[0]) {
		t.Error("color or the wordmark in a pipe")
	}
	for _, sec := range helpSections() {
		for _, r := range sec.rows {
			if !strings.Contains(out, "\n  "+r.left+" ") {
				t.Errorf("help lacks %q", r.left)
			}
		}
	}
	// Every command is in the help.
	for _, name := range commandNames() {
		if !strings.Contains(out, "\n  "+name) {
			t.Errorf("help lacks %s", name)
		}
	}
}

// Every command's -h, and help COMMAND, print its usage and exit with 0; each fits.
func TestCommandHelp(t *testing.T) {
	cmds := [][]string{
		{"serve"}, {"config"}, {"doctor"}, {"verify-links"}, {"gaps"}, {"gaps", "export"}, {"gaps", "submit"},
		{"gaps", "clear"}, {"gaps", "path"}, {"index"}, {"index", "pull"}, {"index", "crawl"}, {"index", "build"},
		{"index", "import"}, {"index", "verify"}, {"index", "llms"},
	}
	for _, c := range toolCommands {
		cmds = append(cmds, []string{c.Name})
	}
	dir := filepath.Join(t.TempDir(), "index")
	for _, c := range cmds {
		code, out, errOut := runCmd(append(append([]string{}, c...), "-h", "-index", dir)...)
		help := out + errOut
		if code != 0 || !strings.Contains(help, "USAGE\n  tenable-docs-mcp "+strings.Join(c, " ")) {
			t.Errorf("%v -h: exit %d\n%s", c, code, help)
		}
		if wide := tooWide(help); len(wide) > 0 {
			t.Errorf("%v -h: lines wider than %d:\n%s", c, width, strings.Join(wide, "\n"))
		}
		if hc, hout, herr := runCmd(append([]string{"help"}, c...)...); hc != 0 || hout+herr == "" {
			t.Errorf("help %v: exit %d", c, hc)
		}
	}
	// A flag's value is named, not typed.
	_, _, errOut := runCmd("doctor", "-h")
	if !strings.Contains(errOut, "-index DIR") || strings.Contains(errOut, " string") {
		t.Errorf("doctor -h:\n%s", errOut)
	}
}

// A mistyped command gets a suggestion, and a usage error still exits with 2.
func TestUnknownCommandSuggests(t *testing.T) {
	for args, want := range map[string]string{
		"serach":     `Did you mean "search"?`,
		"index pul":  `Did you mean "pull"?`,
		"gaps xport": `Did you mean "export"?`,
	} {
		code, _, errOut := runCmd(strings.Fields(args)...)
		if code != 2 || !strings.Contains(errOut, want) {
			t.Errorf("%s: exit %d %q", args, code, errOut)
		}
	}
	if _, _, errOut := runCmd("zzzzzz"); strings.Contains(errOut, "Did you mean") {
		t.Errorf("a far-off name got a suggestion: %q", errOut)
	}
}

// The launch screen names the program, the index, and what to run, and fits.
func TestLaunchScreen(t *testing.T) {
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	var b bytes.Buffer
	launchScreen(&b, ix, "", false)
	out := b.String()
	for _, want := range []string{wordmark[0], tagline, "downloading now", "It's running now, waiting for one", "tenable-docs-mcp config"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	// The index's line holds its folder, whose length isn't up to us.
	var rest []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ansi.ReplaceAllString(l, ""), "Index ") {
			rest = append(rest, l)
		}
	}
	if wide := tooWide(strings.Join(rest, "\n")); len(wide) > 0 {
		t.Errorf("lines wider than %d:\n%s", width, strings.Join(wide, "\n"))
	}
	b.Reset()
	launchScreen(&b, ix, "127.0.0.1:8080", false)
	if !strings.Contains(b.String(), "http://127.0.0.1:8080/mcp") {
		t.Errorf("HTTP:\n%s", b.String())
	}
}

// An MCP client's stdin is a pipe, so it never sees the launch screen; neither does a
// log file.
func TestNoLaunchScreenForClients(t *testing.T) {
	old := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = old })
	stdinIsTerminal = func() bool { return false }
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close() // the client hangs up at once
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })
	t.Setenv("TDM_INDEX_URL", "off")
	t.Setenv("TDM_CRAWL", "off")
	_, out, errOut := runCmd("serve", "-index", filepath.Join(t.TempDir(), "index"))
	if strings.Contains(out+errOut, wordmark[0]) || strings.Contains(out+errOut, "waiting for one") || out != "" {
		t.Errorf("stdout %q, stderr %q", out, errOut)
	}
}

// Color only on a terminal, and never with NO_COLOR or TERM=dumb.
func TestColor(t *testing.T) {
	var b bytes.Buffer
	if styleFor(&b).color {
		t.Error("color into a buffer")
	}
	old := isTerminal
	t.Cleanup(func() { isTerminal = old })
	isTerminal = func(*os.File) bool { return true }
	for _, env := range [][2]string{{"NO_COLOR", "1"}, {"TERM", "dumb"}} {
		t.Run(env[0], func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if styleFor(os.Stdout).color {
				t.Errorf("color with %s=%s", env[0], env[1])
			}
		})
	}
	if s := (style{color: true}); s.bold("x") != "\x1b[1mx\x1b[0m" || (style{}).bold("x") != "x" {
		t.Error("bold")
	}
}

func TestPlaceholderAndWrap(t *testing.T) {
	for _, c := range [][3]string{{"index", "string", "DIR"}, {"limit", "int", "N"}, {"json", "", ""}, {"query", "string", "TEXT"}} {
		if got := placeholder(c[0], c[1]); got != c[2] {
			t.Errorf("placeholder(%s, %s) = %q", c[0], c[1], got)
		}
	}
	if got := wrapText("one two three four", 9); strings.Join(got, "|") != "one two|three|four" {
		t.Errorf("wrapText = %q", got)
	}
	if closest("serach", commandNames()) != "search" || closest("xyzzy", commandNames()) != "" {
		t.Error("closest")
	}
}
