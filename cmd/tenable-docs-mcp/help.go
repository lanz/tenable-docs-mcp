// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/server"
)

// repoURL is where the documentation is.
const repoURL = "https://github.com/lanz/tenable-docs-mcp"

// helpSection is a group of commands in the help.
type helpSection struct {
	title string
	rows  []helpRow
}

// toolRows lists the commands that run the server's tools, from the table they run
// from, so the help can't fall out of step with them.
func toolRows() []helpRow {
	var rows []helpRow
	for _, c := range toolCommands {
		about, _, _ := strings.Cut(c.About, ", as ")
		if short, ok := toolShort[c.Name]; ok {
			about = short
		}
		rows = append(rows, helpRow{strings.TrimSpace(c.Name + " " + c.ArgName), upperFirst(about)})
	}
	return rows
}

// toolShort shortens the descriptions too long for one line of the help; each command's
// -h has its full description.
var toolShort = map[string]string{
	"outline": "list a page's headings, or with -guide, a guide's pages",
	"compare": "compare a page or a setting between two versions",
	"changes": "list the pages changed since a date, such as 30d",
}

var (
	gapsRows = []helpRow{
		{"gaps export", "Summarize the questions the docs didn't answer"},
		{"gaps submit", "Send recorded questions to Tenable, asking about each"},
		{"gaps clear -yes", "Delete the recorded questions"},
		{"gaps path", "Print where the questions are recorded"},
	}
	indexRows = []helpRow{
		{"index pull", "Download the published index, or update it"},
		{"index crawl", "Build or update the index from the live sites"},
		{"index build", "Build the index or a snapshot from local site copies"},
		{"index import SNAPSHOT", "Build the index from a snapshot"},
		{"index verify SNAPSHOT", "Check a snapshot against its manifest"},
		{"index llms -out DIR", "Write each site's llms.txt from the index"},
	}
)

func helpSections() []helpSection {
	return []helpSection{
		{"Run the server", []helpRow{
			{"serve", "Run the MCP server over stdio, or over HTTP with -http"},
		}},
		{"Ask the docs", toolRows()},
		{"Set up and check", []helpRow{
			{"config", "Print the settings for your MCP client, ready to paste"},
			{"doctor", "Check the index, the network, and this program's path"},
			{"verify-links FILE...", "Check the docs links in answers or draft topics"},
			{"version", "Print the version"},
			{"help [COMMAND]", "Show this help, or a command's"},
		}},
		{"Recorded questions", gapsRows},
		{"The index", indexRows},
	}
}

// commandNames lists every command, for suggesting one when a name is mistyped.
func commandNames() []string {
	names := []string{"serve", "config", "doctor", "verify-links", "version", "help", "gaps", "index"}
	for _, c := range toolCommands {
		names = append(names, c.Name)
	}
	return names
}

// writeHelp writes the full help.
func writeHelp(w io.Writer) {
	s := styleFor(w)
	f, isFile := w.(*os.File)
	header(w, s, isFile && isTerminal(f))
	fmt.Fprintf(w, "\n%s\n", s.heading("Usage"))
	fmt.Fprintf(w, "  %s\n\n", s.bold(server.Name+" COMMAND [flags]"))
	writeParagraph(w, "With no command, it runs the MCP server over stdio, as MCP clients start it.")
	// The command sections line up with each other.
	col := 0
	for _, sec := range helpSections() {
		col = max(col, leftWidth(sec.rows))
	}
	for _, sec := range helpSections() {
		fmt.Fprintf(w, "\n%s\n", s.heading(sec.title))
		writeRowsAt(w, sec.rows, s.accent, col)
	}
	fmt.Fprintf(w, "\n%s\n", s.heading("Flags for asking the docs"))
	writeRows(w, s, []helpRow{
		{"-index DIR", "The index folder (default: $TDM_INDEX_DIR, or your cache folder)"},
		{"-json", "Print the tool's structured output as JSON"},
		{"-h", "List the command's other flags"},
	}, s.accent)
	fmt.Fprintf(w, "\n%s\n", s.heading("Environment"))
	writeRows(w, s, []helpRow{
		{"TDM_INDEX_DIR", "The index folder"},
		{"TDM_INDEX_URL", "Where to download the index from, or off to turn downloads off"},
		{"TDM_SITES", "Only these sites, such as en,jp (default: all eight)"},
		{"TDM_CRAWL", "Set to off to never read the live sites"},
		{"TDM_SYNONYMS", "A file of your own search synonyms"},
		{"HTTPS_PROXY", "The proxy to reach docs.tenable.com through"},
		{"NO_COLOR", "Set to turn off color"},
	}, s.accent)
	fmt.Fprintf(w, "\n%s\n", s.heading("Exit status"))
	writeParagraph(w, "0 when a command succeeds; 1 when a tool finds nothing, a check fails, or "+
		"something goes wrong; 2 for a mistake in the command line.")
	fmt.Fprintf(w, "\nRun %s for a command's flags.\nDocumentation: %s\n", s.bold(server.Name+" COMMAND -h"), repoURL)
}

// writeGroupHelp writes the help for a group of commands, such as index or gaps.
func writeGroupHelp(w io.Writer, group, title string, rows []helpRow) {
	s := styleFor(w)
	fmt.Fprintf(w, "%s\n  %s\n\n", s.heading("Usage"), s.bold(server.Name+" "+group+" COMMAND [flags]"))
	writeParagraph(w, title+".")
	short := make([]helpRow, len(rows))
	for i, r := range rows {
		short[i] = helpRow{strings.TrimPrefix(r.left, group+" "), r.about}
	}
	fmt.Fprintf(w, "\n%s\n", s.heading("Commands"))
	writeRows(w, s, short, s.accent)
	fmt.Fprintf(w, "\nRun %s for a command's flags.\n", s.bold(server.Name+" "+group+" COMMAND -h"))
}

// unknownCommand says a command doesn't exist, and suggests the nearest.
func unknownCommand(w io.Writer, prefix, name string, names []string) {
	fmt.Fprintf(w, "%s: unknown command %q\n", prefix, name)
	if c := closest(name, names); c != "" {
		fmt.Fprintf(w, "Did you mean %q?\n", c)
	}
	fmt.Fprintf(w, "Run '%s help' for the list of commands.\n", server.Name)
}

// stdinIsTerminal reports whether someone started the server by hand, rather than an
// MCP client, which talks to it through a pipe. Tests replace it.
var stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }

// launchScreen tells someone who started the server by hand what it is and what to run
// instead, on stderr. stdout carries only the protocol, so nothing goes there.
func launchScreen(w io.Writer, ix *index.Index, httpAddr string) {
	s := styleFor(w)
	header(w, s, true)
	fmt.Fprintln(w)
	switch {
	case ix == nil:
		fmt.Fprintf(w, "%s  %s\n", s.bold("Index"), "can't be opened; see the message above")
	case len(ix.Keys()) == 0:
		fmt.Fprintf(w, "%s  %s %s\n", s.bold("Index"), ix.Dir, s.dim("(downloading now; this takes a minute or two)"))
	default:
		fmt.Fprintf(w, "%s  %s %s\n", s.bold("Index"), ix.Dir, s.dim(fmt.Sprintf("(%d shards)", len(ix.Keys()))))
	}
	fmt.Fprintln(w)
	if httpAddr != "" {
		writeText(w, "Serving MCP at "+s.bold("http://"+httpAddr+"/mcp")+" for MCP clients on this computer. Press Ctrl+C to stop.")
	} else {
		writeText(w, "This is an MCP server: your MCP client starts it, and talks to it over stdin and "+
			"stdout. It's running now, waiting for one. Press Ctrl+C to stop.")
	}
	fmt.Fprintln(w)
	writeRows(w, s, []helpRow{
		{server.Name + " config", "Settings for your MCP client"},
		{server.Name + " doctor", "Check that everything works"},
		{server.Name + " help", "Every command"},
	}, s.accent)
	fmt.Fprintln(w)
}
