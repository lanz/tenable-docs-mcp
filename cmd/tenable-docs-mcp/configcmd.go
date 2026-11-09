// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/server"
)

// configClients are the clients config prints settings for, in order.
var configClients = []string{"claude-code", "claude-desktop", "vscode", "cursor"}

// executable returns this binary's path; tests replace it.
var executable = func() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// configCmd prints ready-to-paste settings for MCP clients. It never edits a client's
// files.
func configCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	client := fs.String("client", "", "only this client: "+strings.Join(configClients, ", ")+" (default: all of them)")
	dir := fs.String("index", "", "an index folder for the server to use, instead of the default")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || *client != "" && !slices.Contains(configClients, *client) {
		fmt.Fprintf(stderr, "Usage: %s config [-client %s] [-index DIR]\n", server.Name, strings.Join(configClients, "|"))
		return 2
	}
	bin, err := executable()
	if err != nil {
		fmt.Fprintln(stderr, "config: can't find this program's path:", err)
		return 1
	}
	env := map[string]string{}
	if *dir != "" {
		abs, err := filepath.Abs(*dir)
		if err != nil {
			fmt.Fprintln(stderr, "config:", err)
			return 2
		}
		env["TDM_INDEX_DIR"] = abs
	}
	clients := configClients
	if *client != "" {
		clients = []string{*client}
	}
	for i, c := range clients {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprint(stdout, clientConfig(c, bin, env))
	}
	if isTemporary(bin) {
		fmt.Fprintf(stderr, "\nNote: %s is a temporary build (go run). Install the binary, for example with go install, and run config from it.\n", bin)
	}
	return 0
}

// clientConfig is one client's settings, with a heading saying where they go.
func clientConfig(client, bin string, env map[string]string) string {
	var b strings.Builder
	switch client {
	case "claude-code":
		b.WriteString("# Claude Code: run this command\n")
		// The name goes before -e, which takes any number of values; -- ends them.
		b.WriteString("claude mcp add --scope user tenable-docs")
		for _, k := range sortedKeys(env) {
			b.WriteString(" -e " + shellQuote(k+"="+filepath.ToSlash(env[k])))
		}
		b.WriteString(" -- " + shellQuote(filepath.ToSlash(bin)) + "\n")
	case "claude-desktop":
		fmt.Fprintf(&b, "# Claude Desktop: add to %s (Settings > Developer > Edit Config)\n", desktopConfigPath())
		b.WriteString(jsonConfig("mcpServers", map[string]any{"command": bin}, env))
	case "vscode":
		b.WriteString("# VS Code: add to .vscode/mcp.json in a workspace, or to your user mcp.json\n")
		b.WriteString(jsonConfig("servers", map[string]any{"type": "stdio", "command": bin}, env))
	case "cursor":
		b.WriteString("# Cursor: add to ~/.cursor/mcp.json\n")
		b.WriteString(jsonConfig("mcpServers", map[string]any{"command": bin}, env))
	}
	return b.String()
}

// jsonConfig is a client's JSON settings for the server, with paths escaped as JSON
// needs (a Windows path's backslashes doubled).
func jsonConfig(key string, entry map[string]any, env map[string]string) string {
	if len(env) > 0 {
		entry["env"] = env
	}
	data, _ := json.MarshalIndent(map[string]any{key: map[string]any{"tenable-docs": entry}}, "", "  ")
	return string(data) + "\n"
}

// desktopConfigPath is where Claude Desktop keeps its settings on this platform.
func desktopConfigPath() string {
	switch runtime.GOOS {
	case "windows":
		return `%APPDATA%\Claude\claude_desktop_config.json`
	case "darwin":
		return "~/Library/Application Support/Claude/claude_desktop_config.json"
	}
	return "~/.config/Claude/claude_desktop_config.json"
}

// shellQuote quotes a word for a POSIX shell, and for PowerShell and cmd when it has
// no quotes of its own, only when it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\!&|;<>()*?[]#~%") {
		return s
	}
	if !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// isTemporary reports whether a binary is go run's temporary build.
func isTemporary(bin string) bool {
	return strings.Contains(filepath.ToSlash(bin), "/go-build")
}
