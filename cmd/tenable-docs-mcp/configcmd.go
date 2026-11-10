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
var configClients = []string{"claude-code", "claude-desktop", "vscode", "cursor", "devin-desktop", "gemini", "codex"}

// developerPortal is the Tenable Developer Portal's MCP server, for API questions,
// which config sets up beside this one.
const (
	developerPortal     = "https://developer.tenable.com/mcp"
	developerPortalName = "tenable-developer"
)

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
	client := fs.String("client", "", "only this client: "+strings.Join(configClients, ", ")+" (default: all of them)")
	dir := fs.String("index", "", "an index folder for the server to use, instead of the default")
	setUsage(fs, stderr, "config [-client NAME] [-index DIR]", "print the settings that add this server to your MCP clients, ready to paste, with this program's path filled in. It only prints, and never edits a client's files")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() > 0 || *client != "" && !slices.Contains(configClients, *client) {
		fs.Usage()
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

// clientConfig is one client's settings for this server and the Developer Portal's,
// with a heading saying where they go.
func clientConfig(client, bin string, env map[string]string) string {
	var b strings.Builder
	local := func() map[string]any { return map[string]any{"command": bin} }
	switch client {
	case "claude-code":
		b.WriteString("# Claude Code: run these commands\n")
		// The name goes before -e, which takes any number of values; -- ends them.
		b.WriteString("claude mcp add --scope user tenable-docs")
		for _, k := range sortedKeys(env) {
			b.WriteString(" -e " + shellQuote(k+"="+filepath.ToSlash(env[k])))
		}
		b.WriteString(" -- " + shellQuote(filepath.ToSlash(bin)) + "\n")
		fmt.Fprintf(&b, "claude mcp add --scope user --transport http %s %s\n", developerPortalName, developerPortal)
	case "claude-desktop":
		fmt.Fprintf(&b, "# Claude Desktop: add to %s (Settings > Developer > Edit Config).\n", desktopConfigPath())
		fmt.Fprintf(&b, "# For API questions, add %s under Settings > Connectors > Add custom connector.\n", developerPortal)
		b.WriteString(jsonConfig("mcpServers", map[string]map[string]any{"tenable-docs": local()}, env))
	case "vscode":
		b.WriteString("# VS Code: add to .vscode/mcp.json in a workspace, or to your user mcp.json\n")
		b.WriteString(jsonConfig("servers", map[string]map[string]any{
			"tenable-docs":      {"type": "stdio", "command": bin},
			developerPortalName: {"type": "http", "url": developerPortal},
		}, env))
	case "cursor":
		b.WriteString("# Cursor: add to ~/.cursor/mcp.json\n")
		b.WriteString(jsonConfig("mcpServers", map[string]map[string]any{
			"tenable-docs": local(), developerPortalName: {"url": developerPortal},
		}, env))
	case "devin-desktop":
		fmt.Fprintf(&b, "# Devin Desktop (formerly Windsurf): add to %s\n", devinConfigPath())
		b.WriteString(jsonConfig("mcpServers", map[string]map[string]any{
			"tenable-docs": local(), developerPortalName: {"url": developerPortal},
		}, env))
	case "gemini":
		b.WriteString("# Gemini CLI: add to ~/.gemini/settings.json\n")
		b.WriteString(jsonConfig("mcpServers", map[string]map[string]any{
			"tenable-docs": local(), developerPortalName: {"httpUrl": developerPortal},
		}, env))
	case "codex":
		b.WriteString("# Codex: add to ~/.codex/config.toml\n")
		fmt.Fprintf(&b, "[mcp_servers.tenable-docs]\ncommand = %s\n", tomlString(bin))
		if len(env) > 0 {
			b.WriteString("\n[mcp_servers.tenable-docs.env]\n")
			for _, k := range sortedKeys(env) {
				fmt.Fprintf(&b, "%s = %s\n", k, tomlString(env[k]))
			}
		}
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\nurl = %s\n", developerPortalName, tomlString(developerPortal))
	}
	return b.String()
}

// jsonConfig is a client's JSON settings for the servers, with the local server's env
// and paths escaped as JSON needs (a Windows path's backslashes doubled).
func jsonConfig(key string, servers map[string]map[string]any, env map[string]string) string {
	if len(env) > 0 {
		servers["tenable-docs"]["env"] = env
	}
	data, _ := json.MarshalIndent(map[string]any{key: servers}, "", "  ")
	return string(data) + "\n"
}

// tomlString quotes a TOML string: a literal string, which needs no escapes (so a
// Windows path keeps its backslashes as they are), unless it holds a quote or a
// newline, which a basic string escapes as JSON does.
func tomlString(s string) string {
	if !strings.ContainsAny(s, "'\n") {
		return "'" + s + "'"
	}
	q, _ := json.Marshal(s)
	return string(q)
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

// devinConfigPath is where Devin Desktop keeps the user's MCP servers on this platform.
func devinConfigPath() string {
	if runtime.GOOS == "windows" {
		return `%APPDATA%\devin\mcp_config.json`
	}
	return "~/.config/devin/mcp_config.json"
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
