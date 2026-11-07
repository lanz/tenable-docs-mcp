// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/tools"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// verifyLinks checks the docs links in files (or standard input, as -), the way
// verify_citations does. It exits 1 when a link is broken.
func verifyLinks(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" verify-links", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "the page's published URL, to resolve relative links (a draft topic's own URL)")
	live := fs.Bool("live", false, "also check the live docs sites, for anchors that aren't headings and pages the index doesn't have")
	indexDir := fs.String("index", "", "the documentation index's folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	asJSON := fs.Bool("json", false, "write the results as JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s verify-links [-base URL] [-live] [-json] [-index DIR] FILE... (- for standard input)\n", server.Name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	if *base != "" && fs.NArg() > 1 {
		fmt.Fprintln(stderr, "verify-links: -base applies to one file; check files with different URLs separately")
		return 2
	}
	ix := openIndex(*indexDir, stderr)
	if ix == nil {
		return 2
	}
	defer ix.Close()
	env := tools.NewEnv(tools.Info{Name: server.Name, Version: version.Get()}, ix)
	if *live {
		env.Live = site.NewHTTP(site.HTTPConfig{UserAgent: server.UserAgent()})
	}
	broken := false
	results := map[string]any{}
	for _, name := range fs.Args() {
		var data []byte
		var err error
		if name == "-" {
			data, err = io.ReadAll(stdin)
		} else {
			data, err = os.ReadFile(name)
		}
		if err != nil {
			fmt.Fprintln(stderr, "verify-links:", err)
			return 2
		}
		out, err := tools.CheckLinks(ctx, env, string(data), *base, *live)
		if err != nil {
			fmt.Fprintln(stderr, "verify-links:", err)
			return 2
		}
		if name != "-" {
			tools.NoteUnpublished(&out, name)
		}
		broken = broken || out.Problems > 0
		if *asJSON {
			results[name] = out
			continue
		}
		if fs.NArg() > 1 {
			fmt.Fprintf(stdout, "%s:\n", name)
		}
		fmt.Fprintln(stdout, tools.VerifySummary(out))
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		var v any = results
		if fs.NArg() == 1 {
			v = results[fs.Arg(0)]
		}
		if err := enc.Encode(v); err != nil {
			fmt.Fprintln(stderr, "verify-links:", err)
			return 2
		}
	}
	if broken {
		return 1
	}
	return 0
}

// gapsCmd summarizes or clears the questions report_gap recorded.
func gapsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "export" && args[0] != "clear" && args[0] != "path") {
		fmt.Fprintf(stderr, "Usage:\n  %s gaps export [-since YYYY-MM-DD] [-json] [-file FILE]\n  %s gaps clear -yes [-file FILE]\n  %s gaps path\n", server.Name, server.Name, server.Name)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet(server.Name+" gaps "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "the gaps file (default: tenable-docs-mcp/gaps.jsonl in the user's settings folder)")
	since := fs.String("since", "", "only questions recorded on or after this date (YYYY-MM-DD)")
	asJSON := fs.Bool("json", false, "write the summary as JSON instead of Markdown")
	yes := fs.Bool("yes", false, "confirm clearing the recorded questions")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	path := *file
	if path == "" {
		p, err := gaps.DefaultPath()
		if err != nil {
			fmt.Fprintln(stderr, "gaps:", err)
			return 2
		}
		path = p
	}
	switch sub {
	case "path":
		fmt.Fprintln(stdout, path)
		return 0
	case "clear":
		if !*yes {
			fmt.Fprintf(stderr, "gaps clear deletes every recorded question in %s; add -yes to confirm\n", path)
			return 2
		}
		if err := gaps.Clear(path); err != nil {
			fmt.Fprintln(stderr, "gaps:", err)
			return 1
		}
		fmt.Fprintln(stdout, "Cleared", path)
		return 0
	}
	var from time.Time
	if *since != "" {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			fmt.Fprintln(stderr, "gaps: -since takes a date as YYYY-MM-DD")
			return 2
		}
		from = t
	}
	recs, err := gaps.Read(path)
	if err != nil {
		fmt.Fprintln(stderr, "gaps:", err)
		return 1
	}
	s := gaps.Summarize(recs, from)
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(s); err != nil {
			fmt.Fprintln(stderr, "gaps:", err)
			return 1
		}
		return 0
	}
	fmt.Fprint(stdout, s.Markdown())
	return 0
}
