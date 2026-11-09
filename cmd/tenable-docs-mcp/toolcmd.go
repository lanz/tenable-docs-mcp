// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/server"
)

// toolCommand runs one of the server's tools from the command line. The server runs in
// this process, and the command calls its tool through an MCP client, so the output is
// what the tool gives an assistant: its text, or with -json its structured output.
type toolCommand struct {
	// Name is the command, and Tool the tool it runs.
	Name, Tool string
	// Arg is the tool argument the command's words fill in, such as a search's query,
	// and ArgName how help shows them.
	Arg, ArgName string
	// Results is the field of the tool's output that lists what it found; an empty
	// list exits with 1.
	Results string
	// About says what the command does, for help.
	About string
}

var toolCommands = []toolCommand{
	{"search", "search_docs", "query", "QUESTION", "results", "search the docs, as search_docs does"},
	{"read", "read_doc", "url", "URL", "", "read a section or page as Markdown, as read_doc does"},
	{"outline", "outline", "url", "URL", "items", "list a page's headings (or, with -guide, a guide's pages), as outline does"},
	{"browse", "browse_guide", "guide", "GUIDE", "entries", "show a guide's table of contents, as browse_guide does"},
	{"related", "related_pages", "url", "URL", "", "show the pages around a page, as related_pages does"},
	{"products", "list_products", "", "", "products", "list the products and languages in the index, as list_products does"},
	{"setting", "find_setting", "query", "SETTING", "settings", "explain a setting, as find_setting does"},
	{"role", "required_role", "task", "TASK", "answers", "find the role a task needs, as required_role does"},
	{"requirements", "requirements", "query", "QUESTION", "requirements", "show system requirements, as requirements does"},
	{"release-notes", "release_notes", "query", "WORDS", "entries", "show release-note entries, as release_notes does"},
	{"term", "define_term", "term", "TERM", "definitions", "define a term or product name, as define_term does"},
	{"status", "index_status", "", "", "", "show the server's version and what the index holds, as index_status does"},
}

func findToolCommand(name string) (toolCommand, bool) {
	for _, c := range toolCommands {
		if c.Name == name {
			return c, true
		}
	}
	return toolCommand{}, false
}

// toolSession runs the server in this process on the index in dir, and returns a client
// connected to it.
func toolSession(ctx context.Context, ix *index.Index) (*mcp.ClientSession, func(), error) {
	srv := server.New(server.Options{Index: ix, Synonyms: loadSynonyms(io.Discard)})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		return nil, nil, err
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: server.Name + "-cli", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		ss.Close()
		return nil, nil, err
	}
	return cs, func() { cs.Close(); ss.Close() }, nil
}

// schemaProp is a tool argument, as its input schema describes it.
type schemaProp struct {
	Type        any    `json:"type"`
	Description string `json:"description"`
}

// propType returns a schema type's name: "string", "integer", "boolean", or "array".
// Optional fields can be typed ["null", "array"].
func propType(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

func flagName(prop string) string { return strings.ReplaceAll(prop, "_", "-") }

// runTool runs a tool command: flags for every argument of the tool's input schema,
// plus -index and -json; the command's other words fill in its main argument.
func runTool(ctx context.Context, c toolCommand, args []string, stdout, stderr io.Writer) int {
	// The flags come from the tool's input schema, which needs a session; a session
	// needs an index, which -index chooses. So -index is found first.
	dir := ""
	for i, a := range args {
		switch {
		case a == "-index" || a == "--index":
			if i+1 < len(args) {
				dir = args[i+1]
			}
		case strings.HasPrefix(a, "-index=") || strings.HasPrefix(a, "--index="):
			_, dir, _ = strings.Cut(a, "=")
		}
	}
	d, err := indexDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, c.Name+":", err)
		return 1
	}
	ix, err := index.Open(d)
	if err != nil {
		fmt.Fprintln(stderr, c.Name+":", err)
		return 1
	}
	defer ix.Close()
	cs, closeSession, err := toolSession(ctx, ix)
	if err != nil {
		fmt.Fprintln(stderr, c.Name+":", err)
		return 1
	}
	defer closeSession()

	tools, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		fmt.Fprintln(stderr, c.Name+":", err)
		return 1
	}
	var tool *mcp.Tool
	for _, t := range tools.Tools {
		if t.Name == c.Tool {
			tool = t
		}
	}
	if tool == nil {
		fmt.Fprintf(stderr, "%s: the server has no %s tool\n", c.Name, c.Tool)
		return 1
	}
	var schema struct {
		Properties map[string]schemaProp `json:"properties"`
		Required   []string              `json:"required"`
	}
	raw, _ := json.Marshal(tool.InputSchema)
	_ = json.Unmarshal(raw, &schema)

	fs := flag.NewFlagSet(server.Name+" "+c.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.String("index", "", "the index folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	asJSON := fs.Bool("json", false, "print the tool's structured output as JSON")
	strs, ints, bools := map[string]*string{}, map[string]*int{}, map[string]*bool{}
	var props []string
	for name := range schema.Properties {
		props = append(props, name)
	}
	sort.Strings(props)
	for _, name := range props {
		if name == c.Arg {
			continue
		}
		p := schema.Properties[name]
		switch propType(p.Type) {
		case "string":
			strs[name] = fs.String(flagName(name), "", p.Description)
		case "integer":
			ints[name] = fs.Int(flagName(name), 0, p.Description)
		case "boolean":
			bools[name] = fs.Bool(flagName(name), false, p.Description)
		}
	}
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s %s [flags] %s\n\n%s.\n\nFlags:\n", server.Name, c.Name, c.ArgName, upperFirst(c.About))
		fs.PrintDefaults()
	}
	words, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil {
		return 2
	}

	in := map[string]any{}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for name, v := range strs {
		if set[flagName(name)] {
			in[name] = *v
		}
	}
	for name, v := range ints {
		if set[flagName(name)] {
			in[name] = *v
		}
	}
	for name, v := range bools {
		if set[flagName(name)] {
			in[name] = *v
		}
	}
	switch {
	case c.Arg != "" && len(words) > 0:
		in[c.Arg] = strings.Join(words, " ")
	case len(words) > 0:
		fmt.Fprintf(stderr, "%s %s takes no words, only flags: %q\n", server.Name, c.Name, strings.Join(words, " "))
		return 2
	}
	for _, r := range schema.Required {
		if _, ok := in[r]; !ok {
			if r == c.Arg {
				fmt.Fprintf(stderr, "%s %s: give the %s\n\n", server.Name, c.Name, strings.ToLower(c.ArgName))
			} else {
				fmt.Fprintf(stderr, "%s %s: -%s is required\n\n", server.Name, c.Name, flagName(r))
			}
			fs.Usage()
			return 2
		}
	}
	if c.Tool != "index_status" && len(ix.Keys()) == 0 {
		fmt.Fprintf(stderr, "%s: the documentation index in %s isn't built yet; run `%s index pull` to download it\n", c.Name, d, server.Name)
		return 1
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.Tool, Arguments: in})
	if err != nil {
		fmt.Fprintln(stderr, c.Name+":", err)
		return 1
	}
	text := resultText(res)
	if res.IsError {
		fmt.Fprintln(stderr, c.Name+": "+text)
		return 1
	}
	if *asJSON {
		out, err := json.MarshalIndent(res.StructuredContent, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, c.Name+":", err)
			return 1
		}
		fmt.Fprintln(stdout, string(out))
	} else {
		fmt.Fprintln(stdout, strings.TrimRight(text, "\n"))
	}
	return toolExitCode(c, res)
}

// resultText joins a tool result's text.
func resultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolExitCode is 1 when the tool found nothing, or, for status, when the index isn't
// ready; otherwise 0.
func toolExitCode(c toolCommand, res *mcp.CallToolResult) int {
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]json.RawMessage
	_ = json.Unmarshal(raw, &out)
	if c.Tool == "index_status" {
		var ix struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(out["index"], &ix)
		if ix.State != "ready" {
			return 1
		}
		return 0
	}
	if c.Results == "" {
		return 0
	}
	var list []json.RawMessage
	_ = json.Unmarshal(out[c.Results], &list)
	if len(list) > 0 {
		return 0
	}
	// define_term also answers with what a product name means.
	if c.Tool == "define_term" && len(out["product"]) > 0 && string(out["product"]) != "null" {
		return 0
	}
	return 1
}

// parseInterspersed parses flags wherever they are among the command's words, as in
// `search install nessus -product Nessus`. A lone -- ends the flags.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return words, nil
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(words, rest...), nil
		}
		words = append(words, rest[0])
		args = rest[1:]
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
